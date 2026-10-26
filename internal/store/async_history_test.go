package store

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func flushTestHistory(t *testing.T, s *Store) {
	t.Helper()
	if err := s.flushHistory(); err != nil {
		t.Fatal(err)
	}
}

func TestBackgroundHistoryAndFinalSave(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.ingestReportAt(n.Token, perfReport(now), now); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(5 * time.Second)
	for {
		var count int
		if err := s.sdb.reader().QueryRow("SELECT count(*) FROM resource_history WHERE node_uuid=?", n.UUID).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count == 1 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("background worker did not persist history")
		}
		time.Sleep(10 * time.Millisecond)
	}
	// Stop the timer to make the remaining sample depend on Close's final save.
	close(s.stopFlush)
	<-s.flushDone
	next := now.Add(time.Minute)
	if _, err := s.ingestReportAt(n.Token, perfReport(next), next); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.reader().QueryRow("SELECT count(*) FROM resource_history WHERE node_uuid=?", n.UUID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("final save lost queued history: count=%d err=%v", count, err)
	}
}

func TestCanceledHistoryFlushRetainsQueuedSamples(t *testing.T) {
	s := perfStore(t)
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	if _, err := s.ingestReportAt(n.Token, perfReport(now), now); err != nil {
		t.Fatal(err)
	}
	conn, err := s.sdb.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	before := s.sdb.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() { done <- s.flushHistoryContext(ctx) }()
	deadline := time.Now().Add(time.Second)
	for s.sdb.db.Stats().WaitCount == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if s.sdb.db.Stats().WaitCount == before {
		t.Fatal("history writer did not reach SQLite wait")
	}
	// Even a new minute's sample must return while the previous batch waits.
	next := now.Add(time.Minute)
	ingested := make(chan error, 1)
	go func() {
		_, err := s.ingestReportAt(n.Token, perfReport(next), next)
		ingested <- err
	}()
	select {
	case err := <-ingested:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("history write blocked report acknowledgement")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("expected canceled write, got %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("canceled history write did not stop")
	}
	s.mu.RLock()
	pending := len(s.pendingSamples)
	s.mu.RUnlock()
	if pending != 2 {
		t.Fatalf("cancellation lost samples: %d", pending)
	}
	conn.Close()
	flushTestHistory(t, s)
	var count int
	if err := s.sdb.reader().QueryRow("SELECT count(*) FROM resource_history WHERE node_uuid=?", n.UUID).Scan(&count); err != nil || count != 2 {
		t.Fatalf("retry lost samples: count=%d err=%v", count, err)
	}
}
