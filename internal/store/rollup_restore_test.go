package store

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func rollupRestoreFixture(t *testing.T, path, label string) string {
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

func seedRestoreRollups(t *testing.T, db *sqliteDB, uuid string, hour int64) {
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

func TestRestoreDiscardsDerivedHistoryAndWatermark(t *testing.T) {
	for _, legacy := range []bool{false, true} {
		name := "with_rollups"
		if legacy {
			name = "without_rollups"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			sourcePath := filepath.Join(dir, "source.db")
			destPath := filepath.Join(dir, "dest.db")
			backupPath := filepath.Join(dir, "backup.db")
			sourceID := rollupRestoreFixture(t, sourcePath, "source")
			destID := rollupRestoreFixture(t, destPath, "destination")
			hour := time.Now().Unix() / 3600 * 3600
			for _, item := range []struct {
				path, id string
				hour     int64
			}{{sourcePath, sourceID, hour}, {destPath, destID, hour + 86400}} {
				db, err := openSQLite(item.path)
				if err != nil {
					t.Fatal(err)
				}
				seedRestoreRollups(t, db, item.id, item.hour)
				if err = db.Close(); err != nil {
					t.Fatal(err)
				}
			}
			if err := ExportData(sourcePath, backupPath); err != nil {
				t.Fatal(err)
			}
			// A full export naturally carries derived tables. Restoring must
			// nevertheless rebuild them from the restored raw observations.
			backup, err := openSQLite(backupPath)
			if err != nil {
				t.Fatal(err)
			}
			assertDBRowCount(t, backup, "history_rollups", 2)
			if legacy {
				rows, err := backup.db.Query("SELECT name FROM sqlite_schema WHERE type='trigger' AND sql LIKE '%history_rollup%'")
				if err != nil {
					t.Fatal(err)
				}
				var triggers []string
				for rows.Next() {
					var trigger string
					if err = rows.Scan(&trigger); err != nil {
						t.Fatal(err)
					}
					triggers = append(triggers, trigger)
				}
				if err = rows.Err(); err != nil {
					t.Fatal(err)
				}
				rows.Close()
				for _, trigger := range triggers {
					if _, err = backup.db.Exec(`DROP TRIGGER "` + strings.ReplaceAll(trigger, `"`, `""`) + `"`); err != nil {
						t.Fatal(err)
					}
				}
				for _, table := range []string{"history_rollups", "history_rollup_state", "history_rollup_dirty"} {
					if _, err = backup.db.Exec("DROP TABLE " + table); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err = backup.Close(); err != nil {
				t.Fatal(err)
			}
			if err = RestoreData(backupPath, destPath); err != nil {
				t.Fatal(err)
			}
			// Inspect without starting the worker, so it cannot mask stale
			// restored rows by rebuilding them before this assertion.
			restored, err := openBackup(destPath)
			if err != nil {
				t.Fatal(err)
			}
			defer restored.Close()
			for _, table := range []string{"history_rollups", "history_rollup_state", "history_rollup_dirty"} {
				assertDBRowCount(t, restored, table, 0)
			}
			for _, table := range []string{"nodes", "ping_history", "resource_history", "ping_observations"} {
				assertDBRowCount(t, restored, table, 1)
			}
			var uuid string
			if err = restored.db.QueryRow("SELECT uuid FROM nodes").Scan(&uuid); err != nil || uuid != sourceID {
				t.Fatalf("restored node = %q, want %q; %v", uuid, sourceID, err)
			}
		})
	}
}

func TestSQLiteDeleteNodeClearsDerivedHistory(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	uuid := rollupRestoreFixture(t, path, "deleted")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	seedRestoreRollups(t, db, uuid, time.Now().Unix()/3600*3600)
	if err = db.deleteNode(uuid); err != nil {
		t.Fatal(err)
	}
	for _, table := range []string{"nodes", "ping_history", "resource_history", "ping_observations", "history_rollups", "history_rollup_dirty"} {
		assertDBRowCount(t, db, table, 0)
	}
}

func TestSQLitePruneTargetsClearsStaleHostsAndRollups(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	uuid := rollupRestoreFixture(t, path, "targets")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	hour := time.Now().Unix() / 3600 * 3600
	seedRestoreRollups(t, db, uuid, hour)
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
	uuid := rollupRestoreFixture(t, path, "store operations")
	s, err := New(path, "password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	func() {
		s.persistMu.Lock()
		defer s.persistMu.Unlock()
		seedRestoreRollups(t, s.sdb, uuid, time.Now().Unix()/3600*3600)
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

func TestRestoreRollupCleanupFailureRollsBackRawReplacement(t *testing.T) {
	dir := t.TempDir()
	sourcePath := filepath.Join(dir, "source.db")
	destPath := filepath.Join(dir, "dest.db")
	rollupRestoreFixture(t, sourcePath, "source")
	destID := rollupRestoreFixture(t, destPath, "destination")
	db, err := openSQLite(destPath)
	if err != nil {
		t.Fatal(err)
	}
	seedRestoreRollups(t, db, destID, time.Now().Unix()/3600*3600)
	if _, err = db.db.Exec(`CREATE TRIGGER reject_rollup_cleanup BEFORE DELETE ON history_rollups
		BEGIN SELECT RAISE(ABORT,'test cleanup failure'); END`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	if err = RestoreData(sourcePath, destPath); err == nil {
		t.Fatal("restore succeeded despite rollup cleanup failure")
	}
	unchanged, err := openBackup(destPath)
	if err != nil {
		t.Fatal(err)
	}
	defer unchanged.Close()
	var uuid string
	if err = unchanged.db.QueryRow("SELECT uuid FROM nodes").Scan(&uuid); err != nil || uuid != destID {
		t.Fatalf("failed restore replaced destination node: %q, %v", uuid, err)
	}
	for _, table := range []string{"ping_history", "resource_history", "ping_observations", "history_rollup_state"} {
		assertDBRowCount(t, unchanged, table, 1)
	}
	assertDBRowCount(t, unchanged, "history_rollups", 2)
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
