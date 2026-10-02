package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// Beijing midnight is a fixed UTC+8 boundary, independent of the host timezone
// and available zoneinfo files. Hour keys themselves are Unix/UTC hours.
var historyRollupLocation = time.FixedZone("Asia/Shanghai", 8*3600)

func historyDayStart(now time.Time) int64 {
	t := now.In(historyRollupLocation)
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, historyRollupLocation).Unix()
}

func (s *sqliteDB) initHistoryRollupSchema() error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
CREATE TABLE IF NOT EXISTS history_rollups (
 node_uuid TEXT NOT NULL, kind TEXT NOT NULL, target_name TEXT NOT NULL DEFAULT '',
 host TEXT NOT NULL DEFAULT '', method TEXT NOT NULL DEFAULT '', hour INTEGER NOT NULL,
 sample_count INTEGER NOT NULL, valid_count INTEGER NOT NULL, value_sum REAL NOT NULL,
 value_min REAL NOT NULL, value_max REAL NOT NULL, first_at INTEGER NOT NULL,
 last_at INTEGER NOT NULL, last_value REAL NOT NULL, peak_at INTEGER NOT NULL,
 timeout_at INTEGER NOT NULL DEFAULT 0, gaps_json TEXT NOT NULL DEFAULT '[]',
 PRIMARY KEY(node_uuid,kind,target_name,host,method,hour)
);
CREATE INDEX IF NOT EXISTS idx_history_rollups_time ON history_rollups(hour);
CREATE TABLE IF NOT EXISTS history_rollup_state (
 id INTEGER PRIMARY KEY CHECK(id=1), completed_before INTEGER NOT NULL
);
CREATE TABLE IF NOT EXISTS history_rollup_dirty (
 node_uuid TEXT NOT NULL, kind TEXT NOT NULL, hour INTEGER NOT NULL,
 PRIMARY KEY(node_uuid,kind,hour)
);
CREATE INDEX IF NOT EXISTS idx_history_rollup_dirty_time ON history_rollup_dirty(hour,node_uuid,kind);
INSERT OR IGNORE INTO history_rollup_state(id,completed_before) VALUES(1,0);
`)
	if err != nil {
		return err
	}
	// Invalidation belongs to the same SQLite transaction as ingestion. This
	// also covers retries, maintenance and direct SQL helpers. Dirty hours use
	// raw data until their replacement summary commits.
	for _, table := range []struct{ name, kind string }{{"ping_history", "ping"}, {"resource_history", "resource"}} {
		for _, event := range []string{"INSERT", "UPDATE", "DELETE"} {
			when := ""
			ref := "NEW"
			if event == "DELETE" {
				ref = "OLD"
			}
			// Explicit UPSERT is necessary here: INSERT OR IGNORE inside a
			// trigger can inherit an outer raw UPSERT's conflict policy and
			// abort on a dirty hour that has already been queued.
			body := fmt.Sprintf(`INSERT INTO history_rollup_dirty(node_uuid,kind,hour)
 SELECT %s.node_uuid,'%s',(%s.timestamp/3600)*3600
 WHERE %s.timestamp < COALESCE((SELECT completed_before FROM history_rollup_state WHERE id=1),0)
 ON CONFLICT(node_uuid,kind,hour) DO NOTHING;`, ref, table.kind, ref, ref)
			if event == "UPDATE" {
				// Retries that reproduce the same sample leave its summary
				// valid. IS NOT handles nullable resource fields as well as zero.
				columns := []string{"node_uuid", "timestamp", "target_name", "host", "method", "latency"}
				if table.kind == "resource" {
					columns = []string{"node_uuid", "timestamp", "cpu_usage", "ram_usage", "network_rate"}
				}
				for i, column := range columns {
					if i == 0 {
						when = " WHEN "
					} else {
						when += " OR "
					}
					when += "OLD." + column + " IS NOT NEW." + column
				}
				body += fmt.Sprintf(`INSERT INTO history_rollup_dirty(node_uuid,kind,hour)
 SELECT OLD.node_uuid,'%s',(OLD.timestamp/3600)*3600
 WHERE OLD.timestamp < COALESCE((SELECT completed_before FROM history_rollup_state WHERE id=1),0)
 ON CONFLICT(node_uuid,kind,hour) DO NOTHING;`, table.kind)
			}
			name := fmt.Sprintf("%s_rollup_%s", table.name, event)
			if _, err = tx.Exec("DROP TRIGGER IF EXISTS " + name); err != nil {
				return err
			}
			query := fmt.Sprintf("CREATE TRIGGER %s AFTER %s ON %s%s BEGIN %s END", name, event, table.name, when, body)
			if _, err = tx.Exec(query); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

// Process one complete day or one invalidated hour per transaction. The caller
// releases persistMu between batches, so initial 90-day backfill does not hold
// up real-time ingestion or all persistence for the entire migration.
func (s *sqliteDB) rollupHistoryBatch(ctx context.Context, now time.Time) (more bool, err error) {
	today := historyDayStart(now)
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var completed int64
	err = tx.QueryRowContext(ctx, "SELECT completed_before FROM history_rollup_state WHERE id=1").Scan(&completed)
	if err != nil && err != sql.ErrNoRows {
		return false, err
	}
	oldestDay := historyDayStart(now.Add(-time.Duration(pingHistoryRetentionSec) * time.Second))
	if completed == 0 {
		var earliest sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT MIN(t) FROM (
 SELECT MIN(timestamp) AS t FROM ping_history WHERE timestamp>=?
 UNION ALL SELECT MIN(timestamp) FROM resource_history WHERE timestamp>=?)`,
			now.Unix()-pingHistoryRetentionSec, now.Unix()-resourceHistoryRetentionSec).Scan(&earliest); err != nil {
			return false, err
		}
		completed = today
		if earliest.Valid {
			completed = min(today, historyDayStart(time.Unix(earliest.Int64, 0)))
		}
	}
	completed = max(completed, oldestDay)
	if completed < today {
		end := min(completed+86400, today)
		if err = rebuildHistoryRollups(ctx, tx, completed, end, "", ""); err != nil {
			return false, err
		}
		if _, err = tx.ExecContext(ctx, "DELETE FROM history_rollup_dirty WHERE hour>=? AND hour<?", completed, end); err != nil {
			return false, err
		}
		completed = end
		more = completed < today
	} else {
		var node, kind string
		var hour int64
		err = tx.QueryRowContext(ctx, "SELECT node_uuid,kind,hour FROM history_rollup_dirty WHERE hour<? ORDER BY hour LIMIT 1", today).Scan(&node, &kind, &hour)
		if err != nil && err != sql.ErrNoRows {
			return false, err
		}
		if err == nil {
			if err = rebuildHistoryRollups(ctx, tx, hour, hour+3600, node, kind); err != nil {
				return false, err
			}
			if _, err = tx.ExecContext(ctx, "DELETE FROM history_rollup_dirty WHERE node_uuid=? AND kind=? AND hour=?", node, kind, hour); err != nil {
				return false, err
			}
			more = true // One further tick determines whether any repairs remain.
		}
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO history_rollup_state(id,completed_before) VALUES(1,?)
 ON CONFLICT(id) DO UPDATE SET completed_before=excluded.completed_before
 WHERE completed_before<>excluded.completed_before`, completed); err != nil {
		return false, err
	}
	return more, tx.Commit()
}

func rebuildHistoryRollups(ctx context.Context, tx *sql.Tx, start, end int64, node, kind string) error {
	filter, args := "hour>=? AND hour<?", []any{start, end}
	if node != "" {
		filter += " AND node_uuid=?"
		args = append(args, node)
	}
	if kind == "ping" {
		filter += " AND kind='ping'"
	} else if kind == "resource" {
		filter += " AND kind IN ('cpu','memory','network')"
	}
	if _, err := tx.ExecContext(ctx, "DELETE FROM history_rollups WHERE "+filter, args...); err != nil {
		return err
	}
	if kind != "ping" {
		for _, metric := range []struct{ name, column string }{{"cpu", "cpu_usage"}, {"memory", "ram_usage"}, {"network", "network_rate"}} {
			where, values := "timestamp>=? AND timestamp<? AND "+metric.column+" IS NOT NULL", []any{start, end}
			index := " INDEXED BY idx_resource_history_time"
			if node != "" {
				where += " AND node_uuid=?"
				values = append(values, node)
				index = "" // The (node_uuid,timestamp) primary key bounds a repair.
			}
			// Joining the last sample preserves the latest valid value, rather
			// than mistaking an hourly mean for the current measurement.
			query := fmt.Sprintf(`INSERT INTO history_rollups(node_uuid,kind,target_name,host,method,hour,
 sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json)
 SELECT a.node_uuid,'%s','','','',a.hour,a.n,a.n,a.total,a.lo,a.hi,a.first_at,a.last_at,r.%s,
 (SELECT MIN(p.timestamp) FROM resource_history p WHERE p.node_uuid=a.node_uuid
 AND p.timestamp>=a.hour AND p.timestamp<a.hour+3600 AND p.%s=a.hi),0,'[]'
 FROM (SELECT node_uuid,(timestamp/3600)*3600 AS hour,COUNT(*) AS n,SUM(CAST(%s AS REAL)) AS total,
 MIN(%s) AS lo,MAX(%s) AS hi,MIN(timestamp) AS first_at,MAX(timestamp) AS last_at
 FROM resource_history%s WHERE %s GROUP BY node_uuid,timestamp/3600) a
 JOIN resource_history r ON r.node_uuid=a.node_uuid AND r.timestamp=a.last_at`, metric.name, metric.column, metric.column, metric.column, metric.column, metric.column, index, where)
			if _, err := tx.ExecContext(ctx, query, values...); err != nil {
				return err
			}
		}
	}
	if kind == "resource" {
		return nil
	}
	where, values := "timestamp>=? AND timestamp<?", []any{start, end}
	index := " INDEXED BY idx_ping_cleanup"
	if node != "" {
		where += " AND node_uuid=?"
		values = append(values, node)
		index = "" // Allow the planner to use a node/series index for a repair.
	}
	rows, err := tx.QueryContext(ctx, `SELECT node_uuid,target_name,host,method,timestamp,latency
 FROM ping_history`+index+` WHERE `+where+` ORDER BY node_uuid,target_name,host,method,timestamp`, values...)
	if err != nil {
		return err
	}
	defer rows.Close()
	// Finish reading before writing summaries on the same connection. At most
	// 24 rows per target/day plus exact outage spans are kept, never raw history.
	var groups []pingHourlyRollup
	for rows.Next() {
		var uuid, target, host, method string
		var timestamp int64
		var latency int
		if err = rows.Scan(&uuid, &target, &host, &method, &timestamp, &latency); err != nil {
			return err
		}
		hour := timestamp / 3600 * 3600
		if len(groups) == 0 || !groups[len(groups)-1].matches(uuid, target, host, method, hour) {
			groups = append(groups, pingHourlyRollup{node: uuid, target: target, host: host, method: method, hour: hour, min: -1, max: -1})
		}
		groups[len(groups)-1].add(timestamp, latency)
	}
	if err = rows.Err(); err != nil {
		return err
	}
	if err = rows.Close(); err != nil {
		return err
	}
	stmt, err := tx.PrepareContext(ctx, `INSERT INTO history_rollups(node_uuid,kind,target_name,host,method,hour,
 sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json)
 VALUES(?,'ping',?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)`)
	if err != nil {
		return err
	}
	defer stmt.Close()
	for _, g := range groups {
		gaps := "[]"
		if len(g.gaps) > 0 {
			encoded, err := json.Marshal(g.gaps)
			if err != nil {
				return err
			}
			gaps = string(encoded)
		}
		if _, err = stmt.ExecContext(ctx, g.node, g.target, g.host, g.method, g.hour, g.count, g.valid, g.sum, g.min, g.max, g.first, g.last, g.lastValue, g.peak, g.timeout, gaps); err != nil {
			return err
		}
	}
	return nil
}

type pingHourlyRollup struct {
	node, target, host, method        string
	hour, first, last, peak, timeout  int64
	count, valid, min, max, lastValue int
	sum                               float64
	gaps                              []OfflineInterval
}

func (g *pingHourlyRollup) matches(node, target, host, method string, hour int64) bool {
	return g.node == node && g.target == target && g.host == host && g.method == method && g.hour == hour
}

func (g *pingHourlyRollup) add(timestamp int64, latency int) {
	if g.count == 0 {
		g.first = timestamp
	} else if timestamp-g.last > 3*PingSampleIntervalSec {
		g.gaps = append(g.gaps, OfflineInterval{g.last + PingSampleIntervalSec, timestamp})
	}
	g.count++
	g.last, g.lastValue = timestamp, latency
	if latency < 0 {
		if g.timeout == 0 {
			g.timeout = timestamp
		}
		return
	}
	g.valid++
	g.sum += float64(latency)
	if g.min < 0 || latency < g.min {
		g.min = latency
	}
	if g.max < 0 || latency > g.max {
		g.max, g.peak = latency, timestamp
	}
}

func (s *sqliteDB) pruneHistoryRollups(before int64) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, table := range []string{"history_rollups", "history_rollup_dirty"} {
		if _, err = tx.Exec("DELETE FROM "+table+" WHERE hour<=?", before-3600); err != nil {
			return err
		}
	}
	return tx.Commit()
}
