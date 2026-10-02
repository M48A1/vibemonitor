package store

import (
	"context"
	"database/sql"
	"errors"
)

// resourceRollupHours returns only fully covered UTC hours. A watermark can be
// at local midnight in a zone with a fractional-hour offset, so round it down
// as well as the query end; its remaining partial hour stays on the raw path.
func resourceRollupHours(start, end, watermark int64) (int64, int64) {
	first := (start + 3599) / 3600 * 3600
	last := min(end/3600*3600, watermark/3600*3600)
	return first, last
}

// readResourceRollupHistory combines complete clean hours with live raw data.
// A single read transaction prevents the midnight job's watermark and rows from
// changing between selection and aggregation. Column is selected by the caller
// from the fixed cpu_usage/ram_usage/network_rate allowlist.
func (s *sqliteDB) readResourceRollupHistory(ctx context.Context, response *ResourceHistoryResponse, column string) (*ResourceHistoryResponse, error) {
	tx, err := s.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var watermark int64
	err = tx.QueryRowContext(ctx, "SELECT completed_before FROM history_rollup_state WHERE id=1").Scan(&watermark)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return nil, err
	}
	first, last := resourceRollupHours(response.StartTime, response.EndTime, watermark)
	raw := "SELECT 1 AS sample_count, CAST(" + column + " AS REAL) AS value_sum, " + column + " AS value_min, " + column + " AS value_max, timestamp AS first_at, timestamp AS last_at, " + column + " AS last_value FROM resource_history WHERE node_uuid=? AND " + column + " IS NOT NULL AND "
	measurements := raw + "timestamp>=? AND timestamp<=?"
	args := []any{response.UUID, response.StartTime, response.EndTime}
	materialization := "NOT MATERIALIZED"
	if first < last {
		materialization = "MATERIALIZED"
		measurements = `SELECT valid_count AS sample_count,value_sum,value_min,value_max,first_at,last_at,last_value
			FROM history_rollups h WHERE node_uuid=? AND kind=? AND target_name='' AND host='' AND method=''
			AND hour>=? AND hour<? AND valid_count>0 AND NOT EXISTS
			(SELECT 1 FROM history_rollup_dirty d WHERE d.node_uuid=h.node_uuid AND d.kind='resource' AND d.hour=h.hour)
			UNION ALL ` + raw + `timestamp>=? AND timestamp<?
			UNION ALL ` + raw + `timestamp>=? AND timestamp<=?
			UNION ALL SELECT 1,CAST(r.` + column + ` AS REAL),r.` + column + `,r.` + column + `,r.timestamp,r.timestamp,r.` + column + `
			FROM history_rollup_dirty d CROSS JOIN resource_history r
			WHERE d.node_uuid=? AND d.kind='resource' AND d.hour>=? AND d.hour<?
			AND r.node_uuid=d.node_uuid AND r.timestamp>=d.hour AND r.timestamp<d.hour+3600 AND r.` + column + ` IS NOT NULL`
		args = []any{
			response.UUID, response.Metric, first, last,
			response.UUID, response.StartTime, first,
			response.UUID, last, response.EndTime,
			response.UUID, first, last,
		}
	}
	// Keep the real raw extrema for Stats while the existing graph remains an
	// average. The latest value comes from the same materialized measurement set.
	query := `WITH measurements AS ` + materialization + ` (` + measurements + `)
		SELECT MIN(first_at),SUM(sample_count),SUM(value_sum),MIN(value_min),MAX(value_max),
		(SELECT last_value FROM measurements ORDER BY last_at DESC LIMIT 1)
		FROM measurements GROUP BY first_at / ? ORDER BY MIN(first_at)`
	args = append(args, response.StepSeconds)
	rows, err := tx.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	response.Stats = ResourceStats{}
	response.Samples = []ResourceSample{}
	var totalSum float64
	for rows.Next() {
		var point ResourceSample
		var count int64
		var sum, minimum, maximum, current float64
		if err := rows.Scan(&point.Timestamp, &count, &sum, &minimum, &maximum, &current); err != nil {
			return nil, err
		}
		if response.Stats.Count == 0 {
			response.Stats.Min, response.Stats.Max = minimum, maximum
		}
		response.Stats.Min = min(response.Stats.Min, minimum)
		response.Stats.Max = max(response.Stats.Max, maximum)
		response.Stats.Count += count
		response.Stats.Current = current
		totalSum += sum
		point.Usage = sum / float64(count)
		response.Samples = append(response.Samples, point)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if response.Stats.Count > 0 {
		response.Stats.Avg = totalSum / float64(response.Stats.Count)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := tx.Commit(); err != nil {
		return nil, err
	}
	return response, nil
}
