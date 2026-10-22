package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

func resourceRollupFixture(t testing.TB) *sqliteDB {
	t.Helper()
	s, err := openSQLite(filepath.Join(t.TempDir(), "resource-rollups.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	// The fixture also works before the main schema migration is integrated.
	_, err = s.db.Exec(`CREATE TABLE IF NOT EXISTS history_rollups (
		node_uuid TEXT NOT NULL,kind TEXT NOT NULL,target_name TEXT NOT NULL,host TEXT NOT NULL,method TEXT NOT NULL,
		hour INTEGER NOT NULL,sample_count INTEGER NOT NULL,valid_count INTEGER NOT NULL,value_sum REAL NOT NULL,
		value_min REAL NOT NULL,value_max REAL NOT NULL,first_at INTEGER NOT NULL,last_at INTEGER NOT NULL,
		last_value REAL NOT NULL,peak_at INTEGER NOT NULL,timeout_at INTEGER NOT NULL,gaps_json TEXT NOT NULL,
		PRIMARY KEY(node_uuid,kind,target_name,host,method,hour));
		CREATE TABLE IF NOT EXISTS history_rollup_state(id INTEGER PRIMARY KEY,completed_before INTEGER NOT NULL);
		CREATE TABLE IF NOT EXISTS history_rollup_dirty(node_uuid TEXT NOT NULL,kind TEXT NOT NULL,hour INTEGER NOT NULL,
		PRIMARY KEY(node_uuid,kind,hour));`)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func createResourceTestRollups(t testing.TB, s *sqliteDB, uuid string, before int64) {
	t.Helper()
	for _, metric := range []struct{ kind, column string }{{"cpu", "cpu_usage"}, {"memory", "ram_usage"}, {"network", "network_rate"}} {
		column := metric.column
		query := `INSERT INTO history_rollups(node_uuid,kind,target_name,host,method,hour,sample_count,valid_count,
			value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json)
			SELECT node_uuid,?,'','','',(r.timestamp/3600)*3600,COUNT(*),COUNT(*),SUM(CAST(` + column + ` AS REAL)),
			MIN(` + column + `),MAX(` + column + `),MIN(timestamp),MAX(timestamp),
			(SELECT latest.` + column + ` FROM resource_history latest WHERE latest.node_uuid=r.node_uuid
			AND latest.timestamp>=(r.timestamp/3600)*3600 AND latest.timestamp<(r.timestamp/3600)*3600+3600
			AND latest.` + column + ` IS NOT NULL ORDER BY latest.timestamp DESC LIMIT 1),
			(SELECT peak.timestamp FROM resource_history peak WHERE peak.node_uuid=r.node_uuid
			AND peak.timestamp>=(r.timestamp/3600)*3600 AND peak.timestamp<(r.timestamp/3600)*3600+3600
			AND peak.` + column + ` IS NOT NULL ORDER BY peak.` + column + ` DESC,peak.timestamp LIMIT 1),0,'[]'
			FROM resource_history r WHERE node_uuid=? AND timestamp<? AND ` + column + ` IS NOT NULL GROUP BY timestamp/3600`
		if _, err := s.db.Exec(query, metric.kind, uuid, before); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.Exec("INSERT INTO history_rollup_state(id,completed_before) VALUES(1,?) ON CONFLICT(id) DO UPDATE SET completed_before=excluded.completed_before", before); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM history_rollup_dirty WHERE node_uuid=?", uuid); err != nil {
		t.Fatal(err)
	}
}

func resourceTestHistory(t testing.TB, s *sqliteDB, uuid, metric string, start, end, step int64) *ResourceHistoryResponse {
	t.Helper()
	column := map[string]string{"cpu": "cpu_usage", "memory": "ram_usage", "network": "network_rate"}[metric]
	response, err := s.readResourceRollupHistory(context.Background(), &ResourceHistoryResponse{
		UUID: uuid, Metric: metric, Range: "7d", StartTime: start, EndTime: end, StepSeconds: step,
	}, column)
	if err != nil {
		t.Fatal(err)
	}
	return response
}

func TestResourceRollupsMixCleanDirtyPartialAndLiveHours(t *testing.T) {
	s := resourceRollupFixture(t)
	end := time.Date(2026, 10, 5, 12, 7, 0, 0, time.UTC).Unix()
	start := end - 7*86400
	first := (start + 3599) / 3600 * 3600
	today := end / 86400 * 86400
	dirtyHour := first + 3600
	values := []struct {
		at    int64
		value float64
	}{
		{start - 1, 999}, {start, 2}, {first - 1, 4},
		{first + 60, 10}, {first + 120, 20},
		{dirtyHour + 60, 50}, {dirtyHour + 120, 90},
		{today - 3500, 80}, {today + 1, 60}, {end - 1, 70}, {end + 1, 999},
	}
	for _, v := range values {
		if err := s.recordResourceSample("node", v.at, sql.NullFloat64{Float64: v.value, Valid: true}, sql.NullFloat64{Float64: v.value / 2, Valid: true}, sql.NullInt64{Int64: int64(v.value * 10), Valid: true}); err != nil {
			t.Fatal(err)
		}
	}
	if err := s.recordResourceSample("node", end, sql.NullFloat64{}, sql.NullFloat64{Float64: 17, Valid: true}, sql.NullInt64{Int64: 25, Valid: true}); err != nil {
		t.Fatal(err)
	}
	createResourceTestRollups(t, s, "node", today)
	// A clean old hour must actually read the summary. A dirty hour must ignore
	// its stale summary; neither its raw values nor its summary can be counted twice.
	if _, err := s.db.Exec("UPDATE resource_history SET cpu_usage=999,ram_usage=999,network_rate=999 WHERE node_uuid='node' AND timestamp=?", first+60); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("UPDATE history_rollups SET value_sum=1,value_min=1,value_max=1,last_value=1 WHERE node_uuid='node' AND hour=?", dirtyHour); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.Exec("DELETE FROM history_rollup_dirty WHERE node_uuid='node'; INSERT INTO history_rollup_dirty(node_uuid,kind,hour) VALUES('node','resource',?)", dirtyHour); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		metric                 string
		count                  int64
		sum, min, max, current float64
	}{
		{"cpu", 9, 386, 2, 90, 70},
		{"memory", 10, 210, 1, 45, 17},
		{"network", 10, 3885, 20, 900, 25},
	} {
		t.Run(test.metric, func(t *testing.T) {
			response := resourceTestHistory(t, s, "node", test.metric, start, end, 3600)
			stats := response.Stats
			if stats.Count != test.count || math.Abs(stats.Avg-test.sum/float64(test.count)) > 1e-9 || stats.Min != test.min || stats.Max != test.max || stats.Current != test.current {
				t.Fatalf("weighted stats = %+v", stats)
			}
			if response.Samples[0].Timestamp != start || response.Samples[1].Timestamp != first+60 {
				t.Fatalf("partial-hour graph timestamps changed: %+v", response.Samples)
			}
		})
	}
}

func TestResourceRollupCoverageRoundsFractionalWatermark(t *testing.T) {
	for _, test := range []struct{ start, end, watermark, first, last int64 }{
		{3601, 18000, 15300, 7200, 14400},
		{3600, 18000, 14400, 3600, 14400},
		{3601, 18000, 0, 7200, 0},
	} {
		first, last := resourceRollupHours(test.start, test.end, test.watermark)
		if first != test.first || last != test.last {
			t.Fatalf("coverage for %+v = %d/%d", test, first, last)
		}
	}
}

func resourceLongFixture(t testing.TB) (*sqliteDB, int64, int64) {
	t.Helper()
	s := resourceRollupFixture(t)
	end := time.Date(2026, 10, 5, 12, 7, 0, 0, time.UTC).Unix()
	start := end - 31*86400
	_, err := s.db.Exec(`WITH RECURSIVE minutes(i) AS (VALUES(0) UNION ALL SELECT i+1 FROM minutes WHERE i<44640)
		INSERT INTO resource_history(node_uuid,timestamp,cpu_usage,ram_usage,network_rate)
		SELECT 'node',?+i*60,CASE WHEN i%11=0 THEN NULL ELSE i%100 END,(i%100)/2.0,1000000000000000+i FROM minutes`, start)
	if err != nil {
		t.Fatal(err)
	}
	return s, start, end
}

func TestResourceRollup31DaysMatchesRawWeightedResults(t *testing.T) {
	s, start, end := resourceLongFixture(t)
	for _, metric := range []string{"cpu", "memory", "network"} {
		t.Run(metric, func(t *testing.T) {
			// A zero watermark is the raw fallback before the first nightly job.
			if _, err := s.db.Exec("INSERT INTO history_rollup_state(id,completed_before) VALUES(1,0) ON CONFLICT(id) DO UPDATE SET completed_before=0"); err != nil {
				t.Fatal(err)
			}
			raw := resourceTestHistory(t, s, "node", metric, start, end, 7200)
			if _, err := s.db.Exec("DELETE FROM history_rollups"); err != nil {
				t.Fatal(err)
			}
			createResourceTestRollups(t, s, "node", end/86400*86400)
			rolled := resourceTestHistory(t, s, "node", metric, start, end, 7200)
			if rolled.Stats.Count != raw.Stats.Count || rolled.Stats.Current != raw.Stats.Current || rolled.Stats.Min != raw.Stats.Min || rolled.Stats.Max != raw.Stats.Max || math.Abs(rolled.Stats.Avg-raw.Stats.Avg) > math.Max(1e-9, math.Abs(raw.Stats.Avg)*1e-12) {
				t.Fatalf("raw %+v != rollups %+v", raw.Stats, rolled.Stats)
			}
			if len(raw.Samples) != len(rolled.Samples) || len(rolled.Samples) > 720 {
				t.Fatalf("graph length raw %d, rolled %d", len(raw.Samples), len(rolled.Samples))
			}
			for i, point := range rolled.Samples {
				if point.Timestamp != raw.Samples[i].Timestamp || math.Abs(point.Usage-raw.Samples[i].Usage) > math.Max(1e-9, math.Abs(point.Usage)*1e-12) {
					t.Fatalf("bucket %d: raw %+v != rollups %+v", i, raw.Samples[i], point)
				}
			}
		})
	}
}

func TestResourceRollupRangesAndCancellation(t *testing.T) {
	sdb := resourceRollupFixture(t)
	s := &Store{sdb: sdb, nodes: map[string]*Node{"node": {UUID: "node"}}}
	for _, test := range []struct {
		rangeName string
		step      int64
	}{{"1h", 60}, {"24h", 120}, {"7d", 3600}, {"31d", 7200}} {
		response, err := s.GetResourceHistory("node", "cpu", test.rangeName)
		if err != nil || response.StepSeconds != test.step || response.Stats.Count != 0 || len(response.Samples) != 0 {
			t.Fatalf("range %s: %+v, %v", test.rangeName, response, err)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := s.GetResourceHistoryContext(ctx, "node", "cpu", "31d"); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled rollup query = %v", err)
	}
}

// Exercise the real midnight writer together with the reader. The other query
// fixtures intentionally create summaries themselves to isolate read semantics.
func TestResourceMidnightWriterAndReaderRepairAgreement(t *testing.T) {
	s := resourceRollupFixture(t)
	now := time.Date(2026, 10, 5, 0, 0, 0, 0, historyRollupLocation)
	end := now.Unix()
	day := end - 86400
	valid := func(value float64) sql.NullFloat64 { return sql.NullFloat64{Float64: value, Valid: true} }
	net := func(value int64) sql.NullInt64 { return sql.NullInt64{Int64: value, Valid: true} }
	for _, sample := range []struct {
		at       int64
		cpu, ram sql.NullFloat64
		network  sql.NullInt64
	}{
		{day + 60, valid(10), sql.NullFloat64{}, net(math.MaxInt64)},
		{day + 120, valid(20), valid(40), net(math.MaxInt64)},
		{day + 180, sql.NullFloat64{}, valid(60), sql.NullInt64{}},
		{day + 3660, valid(0), valid(0), net(0)},
		{end - 1, sql.NullFloat64{}, sql.NullFloat64{}, sql.NullInt64{}},
		{end, valid(70), valid(80), net(2)},
	} {
		if err := s.recordResourceSample("node", sample.at, sample.cpu, sample.ram, sample.network); err != nil {
			t.Fatal(err)
		}
	}
	readAll := func() map[string]*ResourceHistoryResponse {
		result := make(map[string]*ResourceHistoryResponse)
		for _, metric := range []string{"cpu", "memory", "network"} {
			result[metric] = resourceTestHistory(t, s, "node", metric, end-7*86400, end, 3600)
		}
		return result
	}
	raw := readAll()
	if raw["cpu"].Stats != (ResourceStats{Current: 70, Avg: 25, Min: 0, Max: 70, Count: 4}) {
		t.Fatalf("raw boundary baseline: %+v", raw["cpu"].Stats)
	}
	if more, err := s.rollupHistoryBatch(context.Background(), now); err != nil || more {
		t.Fatalf("midnight writer: more %v, err %v", more, err)
	}
	if rolled := readAll(); !reflect.DeepEqual(raw, rolled) {
		t.Fatalf("real writer differs from raw: raw %+v, rolled %+v", raw, rolled)
	}
	// Removing a value via NULL and then adding a late peak must invalidate all
	// three resource summaries in that hour, with no invalid/duplicate samples.
	if err := s.recordResourceSample("node", day+120, sql.NullFloat64{}, valid(10), sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	if err := s.recordResourceSample("node", day+240, valid(90), sql.NullFloat64{}, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	// A persistence retry can repeat the same UPSERT while this hour is already
	// dirty. It must remain idempotent rather than aborting the entire batch.
	if err := s.recordResourceSample("node", day+240, valid(90), sql.NullFloat64{}, sql.NullInt64{}); err != nil {
		t.Fatal(err)
	}
	// OLD and NEW can resolve to the same already-dirty hour even when the
	// timestamp changes. Both invalidations must coexist without a conflict.
	if _, err := s.db.Exec("UPDATE resource_history SET timestamp=? WHERE node_uuid='node' AND timestamp=?", day+300, day+240); err != nil {
		t.Fatal(err)
	}
	var dirtyCount int
	if err := s.db.QueryRow("SELECT COUNT(*) FROM history_rollup_dirty WHERE node_uuid='node' AND kind='resource'").Scan(&dirtyCount); err != nil || dirtyCount != 1 {
		t.Fatalf("same-hour invalidation: count %d, err %v", dirtyCount, err)
	}
	beforeRepair := readAll()
	if beforeRepair["cpu"].Stats != (ResourceStats{Current: 70, Avg: 42.5, Min: 0, Max: 90, Count: 4}) {
		t.Fatalf("dirty raw fallback: %+v", beforeRepair["cpu"].Stats)
	}
	if _, err := s.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if repaired := readAll(); !reflect.DeepEqual(beforeRepair, repaired) {
		t.Fatal("dirty-hour repair changed the reader's statistics or graph")
	}
	// Emptying the entire hour must delete its summaries instead of allowing an
	// old max/current/average to survive after the dirty marker is cleared.
	if _, err := s.db.Exec("DELETE FROM resource_history WHERE node_uuid='node' AND timestamp>=? AND timestamp<?", day, day+3600); err != nil {
		t.Fatal(err)
	}
	beforeRepair = readAll()
	if beforeRepair["cpu"].Stats.Count != 2 || beforeRepair["cpu"].Stats.Max != 70 {
		t.Fatalf("deleted-hour raw fallback: %+v", beforeRepair["cpu"].Stats)
	}
	if _, err := s.rollupHistoryBatch(context.Background(), now); err != nil {
		t.Fatal(err)
	}
	if repaired := readAll(); !reflect.DeepEqual(beforeRepair, repaired) {
		t.Fatal("empty-hour repair resurrected deleted resource values")
	}
}

func BenchmarkResourceRollup31Days(b *testing.B) {
	s, start, end := resourceLongFixture(b)
	createResourceTestRollups(b, s, "node", end/86400*86400)
	for _, test := range []struct {
		name      string
		watermark int64
	}{{"raw", 0}, {"hourly", end / 86400 * 86400}} {
		b.Run(test.name, func(b *testing.B) {
			if _, err := s.db.Exec("UPDATE history_rollup_state SET completed_before=? WHERE id=1", test.watermark); err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				resourceTestHistory(b, s, "node", "cpu", start, end, 7200)
			}
		})
	}
}
