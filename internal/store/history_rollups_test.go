package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func rollupTestDB(t *testing.T) *sqliteDB {
	t.Helper()
	db, err := openSQLite(filepath.Join(t.TempDir(), "history.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

func TestHistoryRollupMidnightAndHourlyStatistics(t *testing.T) {
	db := rollupTestDB(t)
	now := time.Date(2026, 10, 2, 0, 0, 0, 0, historyRollupLocation)
	day := historyDayStart(now) - 86400
	for i, point := range []struct {
		offset  int64
		latency int
	}{{60, 0}, {120, 20}, {180, -1}, {600, 90}, {3700, 100}} {
		if err := db.recordPingSample("n", "t", "example.test", "tcp", day+point.offset, point.latency); err != nil {
			t.Fatalf("sample %d: %v", i, err)
		}
	}
	for _, point := range []struct {
		offset   int64
		cpu, ram sql.NullFloat64
		network  sql.NullInt64
	}{
		{60, sql.NullFloat64{Float64: 10, Valid: true}, sql.NullFloat64{}, sql.NullInt64{Int64: math.MaxInt64, Valid: true}},
		{120, sql.NullFloat64{Float64: 20, Valid: true}, sql.NullFloat64{Float64: 40, Valid: true}, sql.NullInt64{Int64: math.MaxInt64, Valid: true}},
		{180, sql.NullFloat64{}, sql.NullFloat64{Float64: 60, Valid: true}, sql.NullInt64{}},
	} {
		if err := db.recordResourceSample("n", day+point.offset, point.cpu, point.ram, point.network); err != nil {
			t.Fatal(err)
		}
	}
	more, err := db.rollupHistoryBatch(context.Background(), now.Add(-time.Second))
	if err != nil || more {
		t.Fatalf("before midnight: more=%v err=%v", more, err)
	}
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollups").Scan(&count); err != nil || count != 0 {
		t.Fatalf("unfinished day was summarized: count=%d err=%v", count, err)
	}
	if more, err = db.rollupHistoryBatch(context.Background(), now); err != nil || more {
		t.Fatalf("midnight: more=%v err=%v", more, err)
	}
	var samples, valid int
	var sum, minimum, maximum, lastValue float64
	var first, last, peak, timeout int64
	var encoded string
	if err := db.db.QueryRow(`SELECT sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json
 FROM history_rollups WHERE node_uuid='n' AND kind='ping' AND hour=?`, day).Scan(&samples, &valid, &sum, &minimum, &maximum, &first, &last, &lastValue, &peak, &timeout, &encoded); err != nil {
		t.Fatal(err)
	}
	if samples != 4 || valid != 3 || sum != 110 || minimum != 0 || maximum != 90 || lastValue != 90 || first != day+60 || last != day+600 || peak != day+600 || timeout != day+180 {
		t.Fatalf("invalid ping summary: samples=%d valid=%d sum=%v min=%v max=%v first=%d last=%d lastValue=%v peak=%d timeout=%d", samples, valid, sum, minimum, maximum, first, last, lastValue, peak, timeout)
	}
	var gaps []OfflineInterval
	if err := json.Unmarshal([]byte(encoded), &gaps); err != nil || len(gaps) != 1 || gaps[0] != (OfflineInterval{day + 240, day + 600}) {
		t.Fatalf("gaps=%s err=%v", encoded, err)
	}
	for _, metric := range []struct {
		name              string
		sum, min, max     float64
		first, last, peak int64
	}{
		{"cpu", 30, 10, 20, day + 60, day + 120, day + 120},
		{"memory", 100, 40, 60, day + 120, day + 180, day + 180},
		{"network", float64(math.MaxInt64) * 2, float64(math.MaxInt64), float64(math.MaxInt64), day + 60, day + 120, day + 60},
	} {
		if err := db.db.QueryRow(`SELECT sample_count,value_sum,value_min,value_max,first_at,last_at,peak_at
 FROM history_rollups WHERE node_uuid='n' AND kind=?`, metric.name).Scan(&samples, &sum, &minimum, &maximum, &first, &last, &peak); err != nil {
			t.Fatal(err)
		}
		if samples != 2 || sum != metric.sum || minimum != metric.min || maximum != metric.max || first != metric.first || last != metric.last || peak != metric.peak {
			t.Fatalf("%s summary: n=%d sum=%v min=%v max=%v first=%d last=%d peak=%d", metric.name, samples, sum, minimum, maximum, first, last, peak)
		}
	}
	// A completed day is stable across repeated runs, with no duplicate rows.
	if _, err = db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollups").Scan(&count); err != nil || count != 5 {
		t.Fatalf("repeated job count=%d err=%v", count, err)
	}
}

func TestHistoryRollupLateWriteInvalidationAndRepair(t *testing.T) {
	db := rollupTestDB(t)
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, historyRollupLocation)
	day := historyDayStart(now) - 86400
	if err := db.recordPingSample("n", "t", "h", "tcp", day+60, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if err := db.recordPingSample("n", "t", "h", "tcp", day+120, -1); err != nil {
		t.Fatal(err)
	}
	var dirty int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty WHERE node_uuid='n' AND kind='ping' AND hour=?", day).Scan(&dirty); err != nil || dirty != 1 {
		t.Fatalf("late insert invalidation=%d err=%v", dirty, err)
	}
	if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	var count, valid int
	if err := db.db.QueryRow("SELECT sample_count,valid_count FROM history_rollups WHERE kind='ping'").Scan(&count, &valid); err != nil || count != 2 || valid != 1 {
		t.Fatalf("repaired counts=(%d,%d), err=%v", count, valid, err)
	}
	// An outer raw UPSERT must not turn the trigger's duplicate invalidation
	// into an ABORT, including NEW/OLD identifying the very same dirty hour.
	for _, latency := range []int{30, 40, 40} {
		if err := db.recordPingSample("n", "t", "h", "tcp", day+60, latency); err != nil {
			t.Fatalf("repeated historical upsert failed: %v", err)
		}
	}
	// A moved row invalidates both its old and new buckets, atomically.
	if _, err := db.db.Exec("UPDATE ping_history SET timestamp=? WHERE timestamp=?", day+3660, day+60); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty").Scan(&dirty); err != nil || dirty != 2 {
		t.Fatalf("moved row invalidation=%d err=%v", dirty, err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.db.Exec("DELETE FROM ping_history"); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollups").Scan(&count); err != nil || count != 0 {
		t.Fatalf("deleted raw left summaries=%d err=%v", count, err)
	}
}

func TestHistoryRollupIdenticalRetriesKeepSummariesValid(t *testing.T) {
	db := rollupTestDB(t)
	now := time.Date(2026, 10, 2, 0, 5, 0, 0, historyRollupLocation)
	at := historyDayStart(now) - 86400 + 60
	cpu := sql.NullFloat64{Float64: 10, Valid: true}
	network := sql.NullInt64{Int64: 0, Valid: true}
	if err := db.recordPingSample("n", "route", "h", "tcp", at, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.recordResourceSample("n", at, cpu, sql.NullFloat64{}, network); err != nil {
		t.Fatal(err)
	}
	if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 3; i++ {
		if err := db.recordPingSample("n", "route", "h", "tcp", at, 10); err != nil {
			t.Fatal(err)
		}
		if err := db.recordResourceSample("n", at, cpu, sql.NullFloat64{}, network); err != nil {
			t.Fatal(err)
		}
	}
	var dirty int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty").Scan(&dirty); err != nil || dirty != 0 {
		t.Fatalf("identical retries unnecessarily invalidated %d hours: %v", dirty, err)
	}
	// A host change and NULL -> zero are real corrections, even if all other
	// values match. Both kinds must still invalidate atomically.
	if err := db.recordPingSample("n", "route", "other", "tcp", at, 10); err != nil {
		t.Fatal(err)
	}
	if err := db.recordResourceSample("n", at, cpu, sql.NullFloat64{Float64: 0, Valid: true}, network); err != nil {
		t.Fatal(err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty").Scan(&dirty); err != nil || dirty != 2 {
		t.Fatalf("real corrections invalidated %d kinds instead of 2: %v", dirty, err)
	}
}

func TestHistoryRollupFailedBatchAndRestartCheckpoint(t *testing.T) {
	path := filepath.Join(t.TempDir(), "history.db")
	db, err := openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = db.Close() }()
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, historyRollupLocation)
	firstDay := historyDayStart(now) - 3*86400
	if err := db.recordPingSample("n", "t", "h", "tcp", firstDay+60, 10); err != nil {
		t.Fatal(err)
	}
	if _, err := db.db.Exec("CREATE TRIGGER fail_rollup BEFORE INSERT ON history_rollups BEGIN SELECT RAISE(ABORT,'test failure'); END"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.rollupHistoryBatch(context.Background(), now); err == nil {
		t.Fatal("expected transaction failure")
	}
	var watermark int64
	if err := db.db.QueryRow("SELECT completed_before FROM history_rollup_state").Scan(&watermark); err != nil || watermark != 0 {
		t.Fatalf("failed batch advanced watermark=%d err=%v", watermark, err)
	}
	if _, err := db.db.Exec("DROP TRIGGER fail_rollup"); err != nil {
		t.Fatal(err)
	}
	if more, err := db.rollupHistoryBatch(context.Background(), now); err != nil || !more {
		t.Fatalf("first backfill: more=%v err=%v", more, err)
	}
	if err := db.db.QueryRow("SELECT completed_before FROM history_rollup_state").Scan(&watermark); err != nil || watermark != firstDay+86400 {
		t.Fatalf("checkpoint=%d err=%v", watermark, err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = openSQLite(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	if err := db.db.QueryRow("SELECT completed_before FROM history_rollup_state").Scan(&watermark); err != nil || watermark != historyDayStart(now) {
		t.Fatalf("resumed watermark=%d err=%v", watermark, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := db.rollupHistoryBatch(ctx, now); err == nil {
		t.Fatal("cancelled batch must not run")
	}
}

func TestHistoryRollupRetentionKeepsPartialHour(t *testing.T) {
	db := rollupTestDB(t)
	now := time.Date(2026, 10, 2, 9, 0, 0, 0, historyRollupLocation)
	day := historyDayStart(now) - 86400
	for _, offset := range []int64{60, 3660} {
		if err := db.recordPingSample("n", "t", "h", "tcp", day+offset, 10); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if _, err := db.pruneOldPingHistory(day + 3600 + 120); err != nil {
		t.Fatal(err)
	}
	if err := db.pruneHistoryRollups(day + 3600 + 120); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollups").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial bucket missing count=%d err=%v", count, err)
	}
	if err := db.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty").Scan(&count); err != nil || count != 1 {
		t.Fatalf("partial bucket invalidation missing count=%d err=%v", count, err)
	}
}

func TestHistoryDayStartUsesBeijingTimezone(t *testing.T) {
	// 16:00 UTC is exactly the following day's Beijing midnight.
	before := time.Date(2026, 10, 1, 15, 59, 59, 0, time.UTC)
	after := before.Add(time.Second)
	if historyDayStart(after)-historyDayStart(before) != 86400 {
		t.Fatal("wrong Beijing midnight boundary")
	}
	if historyDayStart(after) != after.Unix() {
		t.Fatal("midnight was shifted by host timezone")
	}
}

func TestPingMidnightWriterMatchesRawHistory(t *testing.T) {
	db := rollupTestDB(t)
	now := time.Date(2026, 10, 2, 0, 5, 0, 0, historyRollupLocation)
	day := historyDayStart(now) - 2*86400
	points := []struct {
		offset  int64
		latency int
	}{{60, 10}, {120, -1}, {480, 20}, {3660, -1}, {3720, -1}, {3780, -1}, {86460, 0}, {2*86400 + 120, 30}}
	for _, point := range points {
		if err := db.recordPingSample("n", "route", "h", "tcp", day+point.offset, point.latency); err != nil {
			t.Fatal(err)
		}
	}
	observation := PingObservation{Host: "h", Method: "tcp", StartedAt: day + 60, LastSampleAt: day + points[len(points)-1].offset}
	read := func() *PingHistoryResponse {
		r := &PingHistoryResponse{UUID: "n", Target: "route", Host: "h", Method: "tcp", Range: "all", StartTime: observation.StartedAt, EndTime: now.Unix()}
		if err := db.queryPingHistory(context.Background(), r, observation); err != nil {
			t.Fatal(err)
		}
		return r
	}
	raw := read()
	for i := 0; i < 2; i++ {
		if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
			t.Fatal(err)
		}
	}
	rolled := read()
	if rolled.Stats != raw.Stats || !reflect.DeepEqual(rolled.OfflineIntervals, raw.OfflineIntervals) {
		t.Fatalf("midnight altered exact statistics: raw=%+v gaps=%+v rolled=%+v gaps=%+v", raw.Stats, raw.OfflineIntervals, rolled.Stats, rolled.OfflineIntervals)
	}
	if len(rolled.Samples) >= len(raw.Samples) || len(rolled.Samples) > maxChartSamples {
		t.Fatalf("long history did not use hourly summaries: raw=%d rolled=%d", len(raw.Samples), len(rolled.Samples))
	}
	// A correction must use raw data while dirty and remain identical after
	// the real repair batch commits, including an all-timeout hour becoming valid.
	if err := db.recordPingSample("n", "route", "h", "tcp", day+3720, 42); err != nil {
		t.Fatal(err)
	}
	dirty := read()
	if _, err := db.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	repaired := read()
	if repaired.Stats != dirty.Stats || !reflect.DeepEqual(repaired.OfflineIntervals, dirty.OfflineIntervals) {
		t.Fatalf("repair changed corrected history: dirty=%+v repaired=%+v", dirty.Stats, repaired.Stats)
	}
}
