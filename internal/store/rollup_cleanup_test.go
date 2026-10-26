package store

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func rollupCleanupFixture(t *testing.T, path, label string) string {
	t.Helper()
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: label, Profile: &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "example.com:443"}}}})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	r := perfReport(time.Now())
	r.PingResults = []protocol.PingResult{{Name: "route", Host: "example.com:443", Method: "tcp", Latency: 10}}
	if _, err = s.IngestReport(n.Token, r); err != nil {
		s.Close()
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	return n.UUID
}

func seedCleanupRollups(t *testing.T, db *sqliteDB, uuid string, hour int64) {
	t.Helper()
	for _, kind := range []string{"ping", "cpu"} {
		target, host, method := "", "", ""
		if kind == "ping" {
			target, host, method = "route", "example.com:443", "tcp"
		}
		_, err := db.db.Exec(`INSERT INTO history_rollups
			(node_uuid,kind,target_name,host,method,hour,sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json)
			VALUES(?,?,?,?,?,?,3,2,22,10,12,?,?,11,?,0,'[]')`, uuid, kind, target, host, method, hour, hour+10, hour+120, hour+90)
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec(`INSERT INTO history_rollup_state(id,completed_before) VALUES(1,?)
		ON CONFLICT(id) DO UPDATE SET completed_before=excluded.completed_before`, hour+3600); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("INSERT OR REPLACE INTO history_rollup_dirty(node_uuid,kind,hour) VALUES(?,'resource',?)", uuid, hour); err != nil {
		t.Fatal(err)
	}
}

func assertDBRowCount(t *testing.T, db *sqliteDB, table string, want int) {
	t.Helper()
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM " + table).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("%s rows = %d, want %d", table, count, want)
	}
}

func TestSQLiteDeleteNodeClearsDerivedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	uuid := rollupCleanupFixture(t, path, "deleted")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedCleanupRollups(t, db, uuid, time.Now().Unix()/3600*3600)
	if err = db.deleteNode(uuid); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"nodes", "ping_history", "resource_history", "ping_observations", "history_rollups", "history_rollup_dirty"} {
		assertDBRowCount(t, db, table, 0)
	}
}

func TestSQLitePruneTargetsClearsStaleHostsAndRollups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	uuid := rollupCleanupFixture(t, path, "targets")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hour := time.Now().Unix() / 3600 * 3600
	seedCleanupRollups(t, db, uuid, hour)
	if _, err = db.db.Exec(`INSERT INTO history_rollups
		(node_uuid,kind,target_name,host,method,hour,sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json)
		VALUES(?,'ping','route','old.example.com:443','tcp',?,1,1,10,10,10,?,?,10,?,0,'[]')`, uuid, hour, hour+10, hour+10, hour+10); err != nil {
		t.Fatal(err)
	}
	if err = db.pruneNodePing(uuid, []protocol.PingTarget{{Name: "route", Host: "example.com:443"}}); err != nil {
		t.Fatal(err)
	}
	assertDBRowCount(t, db, "ping_history", 1)
	assertDBRowCount(t, db, "ping_observations", 1)
	assertDBRowCount(t, db, "history_rollups", 2)
	if err = db.pruneNodePing(uuid, nil); err != nil {
		t.Fatal(err)
	}
	assertDBRowCount(t, db, "ping_history", 0)
	assertDBRowCount(t, db, "ping_observations", 0)
	assertDBRowCount(t, db, "history_rollups", 1) // CPU remains independent of targets.
	var pingDirty int
	if err = db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty WHERE kind='ping'").Scan(&pingDirty); err != nil || pingDirty != 0 {
		t.Fatalf("deleted target left %d dirty jobs: %v", pingDirty, err)
	}
}

func TestStoreTargetAndNodeDeletionClearRollupsImmediately(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	uuid := rollupCleanupFixture(t, path, "store operations")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	func() {
		s.persistMu.Lock()
		defer s.persistMu.Unlock()
		seedCleanupRollups(t, s.sdb, uuid, time.Now().Unix()/3600*3600)
	}()
	if err = s.UpdateNodeWithOptions(uuid, NodeOptions{
		Profile:        &NodeProfile{Targets: []protocol.PingTarget{{Name: "route", Host: "replacement.example.com:443"}}},
		ResetDay:       -1,
		TrafficLimitGB: -1,
		InitialUsedGB:  -1,
	}); err != nil {
		t.Fatal(err)
	}
	assertDBRowCount(t, s.sdb, "ping_history", 0)
	assertDBRowCount(t, s.sdb, "history_rollups", 1) // Resource summary remains.
	if err = s.DeleteNode(uuid); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"nodes", "ping_history", "resource_history", "ping_observations", "history_rollups", "history_rollup_dirty"} {
		assertDBRowCount(t, s.sdb, table, 0)
	}
}

func TestStoreShutdownCancelsRollupWaitingForSQLite(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	conn, err := db.db.Conn(context.Background())
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	defer conn.Close()
	// Pin the single writer before the startup job begins. Stop must cancel
	// BeginTx's pool wait; otherwise shutdown could wait the full batch timeout.
	s := &Store{sdb: db, stopFlush: make(chan struct{}), flushDone: make(chan struct{})}
	before := db.db.Stats().WaitCount
	go s.periodicFlusher()
	deadline := time.Now().Add(time.Second)
	for db.db.Stats().WaitCount == before && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if db.db.Stats().WaitCount == before {
		conn.Close()
		s.Close()
		t.Fatal("startup rollup did not begin waiting for SQLite")
	}
	done := make(chan error, 1)
	go func() { done <- s.Close() }()
	select {
	case err = <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		conn.Close() // Always release a stuck worker before failing the test.
		<-done
		t.Fatal("shutdown did not cancel the startup rollup's SQLite wait")
	}
}
