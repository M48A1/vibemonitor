package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func deletionStore(t *testing.T) *Store {
	t.Helper()
	s, err := New(filepath.Join(t.TempDir(), "test.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	close(s.stopFlush)
	<-s.flushDone
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, id := range []string{"deleted", "survivor"} {
		s.nodes[id] = &Node{UUID: id, Name: id, Token: id + "-token"}
		s.tokenIndex[id+"-token"] = id
	}
	if err := s.saveLocked(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"deleted", "survivor"} {
		for _, query := range []string{
			`INSERT INTO ping_history(node_uuid,target_name,timestamp,latency) VALUES(?,'test',1,10)`,
			`INSERT INTO resource_history(node_uuid,timestamp,cpu_usage) VALUES(?,1,10)`,
			`INSERT INTO ping_observations VALUES(?,'test','host','tcp',1,1)`,
			`INSERT INTO telegram_alert_state VALUES(?,'epoch','{}')`,
		} {
			if _, err := s.sdb.db.Exec(query, id); err != nil {
				t.Fatal(err)
			}
		}
	}
	s.pendingSamples = []historySample{{UUID: "deleted", Timestamp: 2}, {UUID: "survivor", Timestamp: 2}}
	s.dirty = true
	return s
}

func TestDeleteNodeDoesNotBlockDashboardOrReports(t *testing.T) {
	s := deletionStore(t)
	// Occupy SQLite's sole writer connection to deterministically model slow I/O.
	conn, err := s.sdb.db.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	before := s.sdb.db.Stats().WaitCount
	done := make(chan error, 1)
	go func() { done <- s.DeleteNode("deleted") }()
	deadline := time.Now().Add(3 * time.Second)
	for s.sdb.db.Stats().WaitCount == before {
		if time.Now().After(deadline) {
			t.Fatal("deletion did not reach SQLite")
		}
		time.Sleep(time.Millisecond)
	}
	responsive := make(chan error, 1)
	go func() {
		s.GetDashboardNodes()
		_, err := s.IngestBasicInfo("survivor-token", protocol.BasicInfo{})
		if err == nil {
			_, err = s.IngestBasicInfo("deleted-token", protocol.BasicInfo{})
		}
		responsive <- err
	}()
	select {
	case err := <-responsive:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("slow deletion blocked dashboard/report ingestion")
	}
	conn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("deletion did not finish")
	}
	if len(s.GetDashboardNodes()) != 1 {
		t.Fatal("wrong remaining node count")
	}
	if _, err := s.IngestBasicInfo("deleted-token", protocol.BasicInfo{}); err == nil {
		t.Fatal("deleted token still accepted")
	}
	if len(s.pendingSamples) != 1 || s.pendingSamples[0].UUID != "survivor" {
		t.Fatal("pending samples lost or deleted samples retained")
	}
	for _, table := range []string{"nodes", "ping_history", "resource_history", "ping_observations", "history_rollups", "history_rollup_dirty", "telegram_alert_state"} {
		column := "node_uuid"
		if table == "nodes" {
			column = "uuid"
		}
		var count int
		if err := s.sdb.db.QueryRow("SELECT count(*) FROM " + table + " WHERE " + column + "='deleted'").Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count != 0 {
			t.Fatalf("deleted data remains in %s", table)
		}
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := s.sdb.db.QueryRow("SELECT count(*) FROM resource_history WHERE node_uuid='survivor'").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("survivor history was lost: %d rows", count)
	}
	loaded, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if len(loaded) != 1 || loaded["survivor"] == nil {
		t.Fatal("deleted node resurrected after save")
	}
}

func TestDeleteNodeFailureKeepsLiveAndPersistedState(t *testing.T) {
	s := deletionStore(t)
	if _, err := s.sdb.db.Exec(`CREATE TRIGGER fail_delete BEFORE DELETE ON ping_history BEGIN SELECT RAISE(ABORT,'injected failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode("deleted"); err == nil {
		t.Fatal("expected deletion failure")
	}
	if len(s.GetDashboardNodes()) != 2 || len(s.pendingSamples) != 2 {
		t.Fatal("failure changed live state")
	}
	if _, err := s.IngestBasicInfo("deleted-token", protocol.BasicInfo{}); err != nil {
		t.Fatal("failure revoked token:", err)
	}
	loaded, err := s.sdb.loadNodes()
	if err != nil {
		t.Fatal(err)
	}
	if loaded["deleted"] == nil || s.sdb.nodeCache["deleted"] == "" {
		t.Fatal("failure changed persisted state/cache")
	}
	if _, err := s.sdb.db.Exec("DROP TRIGGER fail_delete"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteNode("deleted"); err != nil {
		t.Fatal("retry failed:", err)
	}
	if err := s.DeleteNode("survivor"); err != nil {
		t.Fatal("last-node deletion failed:", err)
	}
	if len(s.GetDashboardNodes()) != 0 {
		t.Fatal("last node remains")
	}
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	loaded, err = s.sdb.loadNodes()
	if err != nil || len(loaded) != 0 {
		t.Fatal("last-node deletion not persisted", err)
	}
}
