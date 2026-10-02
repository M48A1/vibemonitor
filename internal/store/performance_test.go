package store

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
	"vibemonitor/pkg/protocol"
)

func perfStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func perfReport(at time.Time) protocol.Report {
	r := protocol.Report{UpdatedAt: at}
	r.CPU.Usage = 20
	r.RAM.Total = 1024
	r.RAM.Used = 512
	return r
}
func TestDashboardPayloadAndStableOrder(t *testing.T) {
	s := perfStore(t)
	for i := 0; i < 10; i++ {
		n, err := s.CreateNode(fmt.Sprint(i), "", "")
		if err != nil {
			t.Fatal(err)
		}
		s.mu.Lock()
		n = s.nodes[n.UUID]
		for j := 0; j < 60; j++ {
			n.History = append(n.History, HistoryPoint{Timestamp: 1700000000 + int64(j), CPUUsage: 23.4, RAMUsage: 45.6, NetUp: 12345, NetDown: 98765})
		}
		s.mu.Unlock()
	}
	full, err := json.Marshal(s.GetNodes())
	if err != nil {
		t.Fatal(err)
	}
	small, _ := json.Marshal(s.GetDashboardNodes())
	t.Logf("10 nodes, 60 history points each: full=%d B compact=%d B (%.1f%% smaller)", len(full), len(small), 100*(1-float64(len(small))/float64(len(full))))
	if len(small) >= len(full)/2 {
		t.Fatal("dashboard includes excess history")
	}
	first := s.GetNodes()
	for i := 0; i < 20; i++ {
		next := s.GetNodes()
		for j := range first {
			if first[j].UUID != next[j].UUID {
				t.Fatal("unstable order")
			}
		}
	}
	if len(first[0].History) != 60 || len(s.GetDashboardNodes()[0].History) != 0 {
		t.Fatal("legacy history contract changed")
	}
	s.mu.Lock()
	s.nodes[first[0].UUID].History[0].CPUUsage = 99
	s.mu.Unlock()
	if first[0].History[0].CPUUsage == 99 {
		t.Fatal("public history aliases mutable state")
	}
}
func TestSlowSnapshotDoesNotBlockIngestAndKeepsNewReport(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err = s.ingestReportAt(n.Token, perfReport(now), now); err != nil {
		t.Fatal(err)
	}
	conn, err := s.sdb.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	before := s.sdb.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() { done <- s.Save() }()
	deadline := time.Now().Add(2 * time.Second)
	for s.sdb.db.Stats().WaitCount == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.sdb.db.Stats().WaitCount == before {
		conn.Close()
		t.Fatal("save did not reach disk wait")
	}
	ingested := make(chan error, 1)
	next := now.Add(time.Second)
	go func() { _, e := s.ingestReportAt(n.Token, perfReport(next), next); ingested <- e }()
	select {
	case err = <-ingested:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		conn.Close()
		t.Fatal("disk wait blocked ingestion")
	}
	conn.Close()
	if err = <-done; err != nil {
		t.Fatal(err)
	}
	if err = s.Save(); err != nil {
		t.Fatal(err)
	}
	saved, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if !saved[n.UUID].LastReport.UpdatedAt.Equal(next) {
		t.Fatal("snapshot cleared dirty state of newer report")
	}
}
func TestHistoryBatchRetryAndDeletedNode(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	_, err = s.sdb.db.Exec("CREATE TRIGGER fail_resource BEFORE INSERT ON resource_history BEGIN SELECT RAISE(ABORT, 'test failure'); END")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.ingestReportAt(n.Token, perfReport(now), now); err != nil {
		t.Fatal(err)
	}
	s.mu.RLock()
	pending := len(s.pendingSamples)
	s.mu.RUnlock()
	if pending != 1 {
		t.Fatal("failed sample was not retained")
	}
	s.sdb.db.Exec("DROP TRIGGER fail_resource")
	if err = s.flushHistory(); err != nil {
		t.Fatal(err)
	}
	response, err := s.GetResourceHistory(n.UUID, "cpu", "1h")
	if err != nil || response.Stats.Count != 1 {
		t.Fatalf("retry lost sample: %+v %v", response, err)
	}
	s.mu.Lock()
	s.pendingSamples = append(s.pendingSamples, historySample{UUID: n.UUID, Timestamp: now.Unix() + 60})
	s.mu.Unlock()
	if err = s.DeleteNode(n.UUID); err != nil {
		t.Fatal(err)
	}
	if err = s.flushHistory(); err != nil {
		t.Fatal(err)
	}
	var count int
	s.sdb.db.QueryRow("SELECT count(*) FROM resource_history WHERE node_uuid=?", n.UUID).Scan(&count)
	if count != 0 {
		t.Fatal("pending history resurrected deleted node")
	}
}
func TestConcurrentReportsSnapshotsAndQueries(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-time.Minute)
	var wg sync.WaitGroup
	wg.Add(3)
	go func() {
		defer wg.Done()
		for i := 0; i < 80; i++ {
			at := now.Add(time.Duration(i) * time.Second)
			if _, e := s.ingestReportAt(n.Token, perfReport(at), at); e != nil {
				t.Error(e)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 12; i++ {
			if e := s.Save(); e != nil {
				t.Error(e)
			}
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 25; i++ {
			s.GetNodes()
			s.GetDashboardNodes()
			if _, e := s.GetResourceHistory(n.UUID, "cpu", "1h"); e != nil {
				t.Error(e)
			}
		}
	}()
	wg.Wait()
}

func TestHistoryTransactionRollbackAndTargetChange(t *testing.T) {
	s := perfStore(t)
	profile := &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}}}
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "node", Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	r := perfReport(now)
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 12}}
	if _, err = s.sdb.db.Exec("CREATE TRIGGER fail_ping BEFORE INSERT ON ping_history BEGIN SELECT RAISE(ABORT,'test batch failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err = s.ingestReportAt(n.Token, r, now); err != nil {
		t.Fatal(err)
	}
	var count int
	if err = s.sdb.db.QueryRow("SELECT count(*) FROM resource_history").Scan(&count); err != nil || count != 0 {
		t.Fatal("resource half of failed batch committed")
	}
	if _, err = s.sdb.db.Exec("DROP TRIGGER fail_ping"); err != nil {
		t.Fatal(err)
	}
	// Removing a target while its batch is pending must also discard that retry.
	if err = s.UpdateNodeWithOptions(n.UUID, NodeOptions{Profile: &NodeProfile{}, ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1}); err != nil {
		t.Fatal(err)
	}
	if err = s.sdb.db.QueryRow("SELECT count(*) FROM ping_history").Scan(&count); err != nil || count != 0 {
		t.Fatal("removed target resurrected")
	}
	if err = s.sdb.db.QueryRow("SELECT count(*) FROM resource_history").Scan(&count); err != nil || count != 1 {
		t.Fatal("valid pending resource lost")
	}
}

func TestWaitingHistoryReaderDoesNotHoldStateLock(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	s.ingestReportAt(n.Token, perfReport(now), now)
	c1, err := s.sdb.readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c1.Close()
	c2, err := s.sdb.readDB.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer c2.Close()
	before := s.sdb.readDB.Stats().WaitCount
	done := make(chan error, 1)
	go func() { _, e := s.GetResourceHistory(n.UUID, "cpu", "1h"); done <- e }()
	deadline := time.Now().Add(2 * time.Second)
	for s.sdb.readDB.Stats().WaitCount == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.sdb.readDB.Stats().WaitCount == before {
		t.Fatal("reader did not wait")
	}
	ingested := make(chan error, 1)
	go func() {
		at := now.Add(time.Second)
		_, e := s.ingestReportAt(n.Token, perfReport(at), at)
		ingested <- e
	}()
	select {
	case e := <-ingested:
		if e != nil {
			t.Fatal(e)
		}
	case <-time.After(time.Second):
		t.Fatal("history query blocked ingestion")
	}
	c1.Close()
	c2.Close()
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}
