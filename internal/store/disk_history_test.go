package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestDiskHistoryIngestionAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	s, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("disk", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Add(-10 * time.Minute)
	for i, pair := range [][2]int64{{25, 100}, {75, 100}, {0, 0}, {-1, 100}, {101, 100}, {0, 100}} {
		at := now.Add(time.Duration(i) * time.Minute)
		r := protocol.Report{UpdatedAt: at}
		r.Disk.Used, r.Disk.Total = pair[0], pair[1]
		if _, err := s.ingestReportAt(n.Token, r, at); err != nil {
			t.Fatal(err)
		}
	}
	assertDisk := func(st *Store) {
		t.Helper()
		for _, span := range []string{"1h", "24h", "7d", "31d"} {
			history, err := st.GetResourceHistory(n.UUID, "disk", span)
			if err != nil {
				t.Fatal(err)
			}
			stats := history.Stats
			if stats.Count != 3 || stats.Current != 0 || stats.Min != 0 || stats.Max != 75 || stats.Avg != 100.0/3 {
				t.Fatalf("%s: unexpected disk stats: %+v", span, stats)
			}
		}
	}
	assertDisk(s)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := RestoreData(path, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := New(destination, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	assertDisk(restored)
}

func TestDiskHistoryMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE resource_history(node_uuid TEXT NOT NULL,timestamp INTEGER NOT NULL,cpu_usage REAL,ram_usage REAL,network_rate INTEGER,PRIMARY KEY(node_uuid,timestamp)); INSERT INTO resource_history VALUES('old',1,12,34,56)`)
	db.Close()
	if err != nil {
		t.Fatal(err)
	}
	upgraded, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer upgraded.Close()
	var disk sql.NullFloat64
	var cpu float64
	if err := upgraded.db.QueryRow("SELECT cpu_usage,disk_usage FROM resource_history WHERE node_uuid='old'").Scan(&cpu, &disk); err != nil {
		t.Fatal(err)
	}
	if cpu != 12 || disk.Valid {
		t.Fatalf("migration altered existing data: cpu=%v disk=%v", cpu, disk)
	}
}

func TestDiskHistoryRollupAndInvalidation(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "rollup.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	start := historyDayStart(time.Now()) - 86400
	for i, value := range []float64{25, 75} {
		if _, err := db.db.Exec("INSERT INTO resource_history(node_uuid,timestamp,disk_usage) VALUES('disk',?,?)", start+int64(i)*60, value); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.rollupHistoryBatch(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	check := func(want float64) {
		t.Helper()
		response, err := db.readResourceRollupHistory(context.Background(), &ResourceHistoryResponse{UUID: "disk", Metric: "disk", StartTime: start, EndTime: start + 86400, StepSeconds: 3600}, "disk_usage")
		if err != nil {
			t.Fatal(err)
		}
		if response.Stats.Count != 2 || response.Stats.Avg != want {
			t.Fatalf("unexpected rollup: %+v", response.Stats)
		}
	}
	check(50)
	if _, err := db.db.Exec("UPDATE resource_history SET disk_usage=95 WHERE node_uuid='disk' AND timestamp=?", start+60); err != nil {
		t.Fatal(err)
	}
	check(60) // Dirty summaries must fall back to the updated raw samples.
	if _, err := db.rollupHistoryBatch(context.Background(), time.Now()); err != nil {
		t.Fatal(err)
	}
	check(60)
}
