package store

import (
	"context"
	"database/sql"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestRankingCalendarBoundaries(t *testing.T) {
	for _, tc := range []struct{ at, period, want string }{
		{"2026-11-01T16:30:00Z", "day", "2026-11-02"},
		{"2026-11-01T16:30:00Z", "week", "2026-11-02"},
		{"2026-11-01T15:30:00Z", "week", "2026-10-26"},
		{"2026-10-31T16:30:00Z", "month", "2026-11-01"},
	} {
		now, _ := time.Parse(time.RFC3339, tc.at)
		start, err := rankingStart(tc.period, now)
		if err != nil || start.Format("2006-01-02 15:04") != tc.want+" 00:00" {
			t.Fatalf("%+v: %v %v", tc, start, err)
		}
	}
	if _, err := rankingStart("invalid", time.Now()); err != ErrRankingPeriod {
		t.Fatal(err)
	}
}

func TestRankingsRawRollupsAndDirtyHours(t *testing.T) {
	db, err := openSQLite(filepath.Join(t.TempDir(), "rankings.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	now := time.Date(2026, 10, 24, 12, 0, 0, 0, trafficLocation)
	start, _ := rankingStart("month", now)
	s := &Store{sdb: db, nodes: map[string]*Node{}}
	for _, id := range []string{"a", "b", "c", "d", "empty"} {
		s.nodes[id] = &Node{UUID: id, Name: id}
	}
	// No LastReport/Online required: historical rankings include offline nodes.
	s.nodes["a"].DailyTraffic = map[string]int64{"2026-10-01": 100, "2026-10-24": 200, "2026-09-30": 999, "2026-10-25": 999}
	s.nodes["a"].InitialUsed = 9999 // Manual adjustments must not enter calendar traffic.
	s.nodes["b"].DailyTraffic = map[string]int64{"2026-10-24": 500}
	s.nodes["c"].DailyTraffic = map[string]int64{"2026-10-24": 0}
	insert := func(id string, at int64, value float64) {
		t.Helper()
		_, err := db.db.Exec(`INSERT INTO resource_history(node_uuid,timestamp,connections,load1,ram_usage,connection_rate) VALUES(?,?,?,?,?,?)`, id, at, value, value, value, value)
		if err != nil {
			t.Fatal(err)
		}
	}
	insert("a", start.Unix()+60, 10)
	insert("a", start.Unix()+120, 30)
	insert("a", now.Unix()-60, 80) // Weighted mean = 40, not mean of hourly means (50).
	insert("b", start.Unix()+60, 50)
	insert("c", now.Unix()-60, 0)
	insert("d", start.Unix()+60, 50) // Stable tie break by UUID.
	insert("deleted", now.Unix()-60, 1000)
	insert("a", start.Unix()-60, 999)
	insert("a", now.Unix()+60, 999)
	raw, err := s.rankingsAt(context.Background(), "month", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range raw.Groups[:4] {
		if len(group.Entries) != 3 || group.Entries[0].UUID != "b" || group.Entries[1].UUID != "d" || group.Entries[2].UUID != "a" || group.Entries[2].Value != 40 || group.Entries[2].Samples != 3 {
			t.Fatalf("bad ranking: %+v", group)
		}
	}
	if got := raw.Groups[4].Entries; len(got) != 3 || got[0].Value != 500 || got[1].Value != 300 || got[2].Value != 0 {
		t.Fatalf("traffic: %+v", got)
	}
	for i := 0; i < 32; i++ {
		more, err := db.rollupHistoryBatch(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
	}
	rolled, err := s.rankingsAt(context.Background(), "month", now)
	if err != nil || !reflect.DeepEqual(raw, rolled) {
		t.Fatalf("rollup changed ranking: %v\n%+v\n%+v", err, raw, rolled)
	}
	if _, err = db.db.Exec("UPDATE resource_history SET connections=100,load1=100,ram_usage=100,connection_rate=100 WHERE node_uuid='a' AND timestamp=?", start.Unix()+60); err != nil {
		t.Fatal(err)
	}
	dirty, err := s.rankingsAt(context.Background(), "month", now)
	if err != nil {
		t.Fatal(err)
	}
	for _, group := range dirty.Groups[:4] {
		if group.Entries[0].UUID != "a" || group.Entries[0].Value != 70 {
			t.Fatalf("dirty hour not replaced: %+v", group)
		}
	}
	if _, err = db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	repaired, err := s.rankingsAt(context.Background(), "month", now)
	if err != nil || !reflect.DeepEqual(dirty, repaired) {
		t.Fatalf("repair changed ranking: %v", err)
	}
	day, err := s.rankingsAt(context.Background(), "day", now)
	if err != nil || len(day.Groups[0].Entries) != 2 || day.Groups[0].Entries[0].Value != 80 {
		t.Fatalf("day: %+v %v", day, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err = s.rankingsAt(ctx, "day", now); err == nil {
		t.Fatal("cancelled query succeeded")
	}
}

func TestRankingHistoryMigration(t *testing.T) {
	path := filepath.Join(t.TempDir(), "old.db")
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
	var connections, load, rate sql.NullFloat64
	var memory float64
	err = upgraded.db.QueryRow("SELECT connections,load1,connection_rate,ram_usage FROM resource_history").Scan(&connections, &load, &rate, &memory)
	if err != nil || connections.Valid || load.Valid || rate.Valid || memory != 34 {
		t.Fatalf("migration fabricated history: %v %v %v %v %v", connections, load, rate, memory, err)
	}
}

func TestRankingMetricIngestionAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "ingest.db")
	s, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("ranked", "", "")
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().Add(-5 * time.Minute)
	for i, rate := range []float64{5, 0, math.NaN()} {
		at := now.Add(time.Duration(i) * time.Minute)
		r := protocol.Report{UpdatedAt: at, Load: protocol.LoadReport{Load1: float64(i)}, Connections: protocol.ConnectionsReport{TCP: 10, UDP: 5, TCPNewPerSecond: &rate}}
		if i == 2 {
			r.Connections.TCPNewPerSecond = nil
			r.Load.Load1 = -1
			r.Connections.TCP = -1
		}
		if _, err = s.ingestReportAt(n.Token, r, at); err != nil {
			t.Fatal(err)
		}
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var connections, load, rate float64
	var count int
	err = reopened.sdb.db.QueryRow("SELECT AVG(connections),AVG(load1),AVG(connection_rate),COUNT(connection_rate) FROM resource_history WHERE node_uuid=?", n.UUID).Scan(&connections, &load, &rate, &count)
	if err != nil || connections != 15 || load != 0.5 || rate != 2.5 || count != 2 {
		t.Fatalf("bad persisted metrics: %v %v %v %v %v", connections, load, rate, count, err)
	}
}
