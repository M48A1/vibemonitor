package store

import (
	"context"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func pingRollupTestDB(t testing.TB) *sqliteDB {
	t.Helper()
	db, err := openSQLite(filepath.Join(t.TempDir(), "data.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func seedPingRollupsForQuery(t testing.TB, db *sqliteDB, uuid, target, host, method string, samples []PingSample, before int64) {
	t.Helper()
	if len(samples) == 0 {
		t.Fatal("empty rollup fixture")
	}
	first, last := samples[0].Timestamp, samples[0].Timestamp
	for _, sample := range samples {
		if err := db.recordPingSample(uuid, target, host, method, sample.Timestamp, sample.Latency); err != nil {
			t.Fatal(err)
		}
		first, last = min(first, sample.Timestamp), max(last, sample.Timestamp)
	}
	tx, err := db.db.Begin()
	if err != nil {
		t.Fatal(err)
	}
	defer tx.Rollback()
	// Use the production writer, including exact all-timeout values and gaps.
	if err := rebuildHistoryRollups(context.Background(), tx, first/3600*3600, last/3600*3600+3600, uuid, "ping"); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("INSERT INTO history_rollup_state(id,completed_before) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET completed_before=excluded.completed_before", before); err != nil {
		t.Fatal(err)
	}
	if _, err := tx.Exec("DELETE FROM history_rollup_dirty WHERE node_uuid=? AND kind='ping'", uuid); err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
}

func pingRollupFixture(t *testing.T) (*sqliteDB, *PingHistoryResponse, PingObservation, []PingSample, int64) {
	t.Helper()
	db := pingRollupTestDB(t)
	base := int64(1700000000 / 3600 * 3600)
	r := &PingHistoryResponse{UUID: "query-node", Target: "route", Host: "example.com:443", Method: "tcp", Range: "7d", StartTime: base + 3300, EndTime: base + 11700}
	observation := PingObservation{Host: r.Host, Method: r.Method, StartedAt: base, LastSampleAt: base + 11280}
	var samples []PingSample
	for _, point := range []struct {
		offset  int64
		latency int
	}{{3240, 10}, {3300, 12}, {3360, -1}, {3420, 18}, {3600, 10}, {3660, -1}, {3720, 30}, {4200, 5}, {6000, 50}, {6600, 15}, {7320, 20}, {7380, 0}, {7440, 10}, {10500, -1}, {10860, 20}, {10920, 10}, {11280, 40}} {
		sample := PingSample{Host: r.Host, Method: r.Method, Timestamp: base + point.offset, Latency: point.latency}
		samples = append(samples, sample)
		if err := db.recordPingSample(r.UUID, r.Target, r.Host, r.Method, sample.Timestamp, sample.Latency); err != nil {
			t.Fatal(err)
		}
	}
	return db, r, observation, samples, base
}

func runPingRollupQuery(t testing.TB, db *sqliteDB, template *PingHistoryResponse, observation PingObservation) *PingHistoryResponse {
	t.Helper()
	r := *template
	r.Samples = nil
	r.OfflineIntervals = nil
	if err := db.queryPingHistory(context.Background(), &r, observation); err != nil {
		t.Fatal(err)
	}
	return &r
}

func comparePingRollupStats(t *testing.T, want, got *PingHistoryResponse) {
	t.Helper()
	if want.Stats != got.Stats || !reflect.DeepEqual(want.OfflineIntervals, got.OfflineIntervals) {
		t.Fatalf("rollup changed exact history:\nwant stats=%+v gaps=%+v\ngot stats=%+v gaps=%+v", want.Stats, want.OfflineIntervals, got.Stats, got.OfflineIntervals)
	}
	if len(got.Samples) > maxChartSamples {
		t.Fatalf("rollup chart grew to %d points", len(got.Samples))
	}
}

func TestPingRollupsPreserveExactStatsAndAllGapEdges(t *testing.T) {
	db, r, observation, samples, base := pingRollupFixture(t)
	want := runPingRollupQuery(t, db, r, observation)
	seedPingRollupsForQuery(t, db, r.UUID, r.Target, r.Host, r.Method, samples, base+10800)
	// Remove the two eligible hours. The initial/final partial hours stay raw.
	if _, err := db.db.Exec("DELETE FROM ping_history WHERE timestamp>=? AND timestamp<?", base+3600, base+10800); err != nil {
		t.Fatal(err)
	}
	// Simulate a summary-only archive. Normal retention invalidates deletions.
	if _, err := db.db.Exec("DELETE FROM history_rollup_dirty"); err != nil {
		t.Fatal(err)
	}
	got := runPingRollupQuery(t, db, r, observation)
	comparePingRollupStats(t, want, got)
	if len(got.Samples) != 4 {
		t.Fatalf("partial/raw + full/rollup hours produced %d points", len(got.Samples))
	}
	timeouts := 0
	for _, p := range got.Samples {
		if p.Latency < 0 {
			timeouts++
		}
	}
	if timeouts != 3 {
		t.Fatalf("rollup hid timeout witnesses: %+v", got.Samples)
	}
}

func TestPingRollupsDirtyAndIncompleteHoursReadRaw(t *testing.T) {
	for _, scenario := range []string{"dirty", "incomplete", "missing"} {
		t.Run(scenario, func(t *testing.T) {
			db, r, observation, samples, base := pingRollupFixture(t)
			before := base + 10800
			if scenario == "incomplete" {
				before = base + 7200
			}
			seedPingRollupsForQuery(t, db, r.UUID, r.Target, r.Host, r.Method, samples, before)
			if _, err := db.db.Exec("UPDATE ping_history SET latency=99 WHERE timestamp=?", base+7380); err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "dirty":
				if _, err := db.db.Exec("INSERT OR IGNORE INTO history_rollup_dirty(node_uuid,kind,hour) VALUES(?,'ping',?)", r.UUID, base+7200); err != nil {
					t.Fatal(err)
				}
			case "missing":
				if _, err := db.db.Exec("DELETE FROM history_rollups WHERE hour=?", base+7200); err != nil {
					t.Fatal(err)
				}
			}
			short := *r
			short.Range = "24h"
			want := runPingRollupQuery(t, db, &short, observation)
			got := runPingRollupQuery(t, db, r, observation)
			comparePingRollupStats(t, want, got)
			if got.Stats.Max != 99 {
				t.Fatal("stale/incomplete rollup hid latest raw sample")
			}
		})
	}
}

func TestPingRollupsDoNotMixObservationEpochOrMeasurementSeries(t *testing.T) {
	db, r, observation, samples, base := pingRollupFixture(t)
	seedPingRollupsForQuery(t, db, r.UUID, r.Target, r.Host, r.Method, samples, base+10800)
	other := []PingSample{{Timestamp: base + 7200, Latency: 9999}}
	seedPingRollupsForQuery(t, db, r.UUID, r.Target, "old.example.com:443", "icmp", other, base+10800)
	r.StartTime = base + 4200
	observation.StartedAt = r.StartTime
	short := *r
	short.Range = "24h"
	want := runPingRollupQuery(t, db, &short, observation)
	got := runPingRollupQuery(t, db, r, observation)
	comparePingRollupStats(t, want, got)
	if got.Stats.Max == 9999 || got.Samples[0].Timestamp < r.StartTime {
		t.Fatal("old host/method/epoch entered current history")
	}
}

func TestPingRollupPreviousPreservesGapAcrossRangeBoundary(t *testing.T) {
	db := pingRollupTestDB(t)
	base := int64(1700000000 / 3600 * 3600)
	r := &PingHistoryResponse{UUID: "query-node", Target: "route", Host: "example.com:443", Method: "tcp", Range: "all", StartTime: base + 3600, EndTime: base + 7320}
	observation := PingObservation{Host: r.Host, Method: r.Method, StartedAt: base, LastSampleAt: base + 7320}
	points := []PingSample{{Timestamp: base + 3480, Latency: 10}, {Timestamp: base + 3720, Latency: 20}, {Timestamp: base + 7320, Latency: 30}}
	for _, p := range points {
		if err := db.recordPingSample(r.UUID, r.Target, r.Host, r.Method, p.Timestamp, p.Latency); err != nil {
			t.Fatal(err)
		}
	}
	want := runPingRollupQuery(t, db, r, observation)
	seedPingRollupsForQuery(t, db, r.UUID, r.Target, r.Host, r.Method, points, base+7200)
	if _, err := db.db.Exec("DELETE FROM ping_history WHERE timestamp<?", base+7200); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("DELETE FROM history_rollup_dirty"); err != nil {
		t.Fatal(err)
	}
	got := runPingRollupQuery(t, db, r, observation)
	comparePingRollupStats(t, want, got)
	if len(got.OfflineIntervals) != 2 || got.OfflineIntervals[0].Start != r.StartTime || got.OfflineIntervals[0].End != base+3720 {
		t.Fatalf("previous rollup lost clipped gap: %+v", got.OfflineIntervals)
	}
}

func TestPingRollupsLongRangesMatchRawAndLimitChartPoints(t *testing.T) {
	db := pingRollupTestDB(t)
	now := time.Date(2026, 10, 2, 12, 20, 0, 0, historyRollupLocation)
	start := now.Unix() - pingHistoryRetentionSec
	// Sparse hourly observations exercise thousands of cross-hour gaps, all-
	// timeout hours and the 90-day chart bound without a large minute fixture.
	if _, err := db.db.Exec(`WITH RECURSIVE points(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM points WHERE i<2160)
 INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency)
 SELECT 'n','route','h','tcp',?+i*3600,CASE WHEN i%5=0 THEN -1 ELSE i%100 END FROM points`, start); err != nil {
		t.Fatal(err)
	}
	observation := PingObservation{Host: "h", Method: "tcp", StartedAt: start, LastSampleAt: now.Unix()}
	var templates []*PingHistoryResponse
	for _, entry := range []struct {
		name     string
		duration int64
	}{{"7d", 7 * 86400}, {"31d", 31 * 86400}, {"all", pingHistoryRetentionSec}} {
		templates = append(templates, &PingHistoryResponse{UUID: "n", Target: "route", Host: "h", Method: "tcp", Range: entry.name, StartTime: now.Unix() - entry.duration, EndTime: now.Unix()})
	}
	raw := make([]*PingHistoryResponse, len(templates))
	for i, template := range templates {
		raw[i] = runPingRollupQuery(t, db, template, observation)
	}
	for i := 0; i < 91; i++ {
		more, err := db.rollupHistoryBatch(context.Background(), now)
		if err != nil {
			t.Fatal(err)
		}
		if !more {
			break
		}
		if i == 90 {
			t.Fatal("90-day backfill did not finish")
		}
	}
	for i, template := range templates {
		t.Run(template.Range, func(t *testing.T) {
			rolled := runPingRollupQuery(t, db, template, observation)
			comparePingRollupStats(t, raw[i], rolled)
		})
	}
}

func BenchmarkPingRollup90Days(b *testing.B) {
	db := pingRollupTestDB(b)
	now := time.Now().Unix()/3600*3600 + 1200
	start := now - 90*86400
	_, err := db.db.Exec(`WITH RECURSIVE points(timestamp,i) AS (SELECT ?,0 UNION ALL SELECT timestamp+60,i+1 FROM points WHERE timestamp+60<=?)
		INSERT INTO ping_history(node_uuid,target_name,host,method,timestamp,latency) SELECT 'bench','route','example.com:443','tcp',timestamp,CASE WHEN i%10=0 THEN -1 ELSE 12 END FROM points`, start, now)
	if err != nil {
		b.Fatal(err)
	}
	tx, err := db.db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	defer tx.Rollback()
	if err = rebuildHistoryRollups(context.Background(), tx, start/3600*3600, now/3600*3600+3600, "bench", "ping"); err != nil {
		b.Fatal(err)
	}
	if err = tx.Commit(); err != nil {
		b.Fatal(err)
	}
	observation := PingObservation{Host: "example.com:443", Method: "tcp", StartedAt: start, LastSampleAt: now}
	for _, scenario := range []string{"raw", "rolled"} {
		b.Run(scenario, func(b *testing.B) {
			before := int64(0)
			if scenario == "rolled" {
				before = historyDayStart(time.Unix(now, 0))
			}
			if _, err := db.db.Exec("INSERT INTO history_rollup_state(id,completed_before) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET completed_before=excluded.completed_before", before); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				r := &PingHistoryResponse{UUID: "bench", Target: "route", Host: observation.Host, Method: observation.Method, Range: "all", StartTime: start, EndTime: now}
				if err := db.queryPingHistory(context.Background(), r, observation); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
