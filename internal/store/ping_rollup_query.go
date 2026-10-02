package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
)

type pingHourRollup struct {
	hour, first, last, peak, timeout int64
	count, valid                     int
	sum, minimum, maximum, current   float64
	gaps                             []OfflineInterval
}

func longPingRange(name string) bool {
	switch name {
	case "7d", "7*24h", "7x24h", "168h", "31d", "31*24h", "31x24h", "744h", "all":
		return true
	default:
		return false
	}
}

// Only fully completed, clean hours are eligible. A missing/dirty hour is read
// from raw data, including the range's two partial-hour boundaries and today.
func readPingHourRollups(ctx context.Context, tx *sql.Tx, r *PingHistoryResponse) ([]pingHourRollup, int64, error) {
	if !longPingRange(r.Range) {
		return nil, 0, nil
	}
	var tables int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM sqlite_schema WHERE type='table' AND name IN ('history_rollups','history_rollup_state','history_rollup_dirty')`).Scan(&tables); err != nil {
		return nil, 0, err
	}
	if tables != 3 {
		return nil, 0, nil
	} // Backward-compatible with pre-rollup data.
	var before int64
	err := tx.QueryRowContext(ctx, "SELECT completed_before FROM history_rollup_state WHERE id=1").Scan(&before)
	if err == sql.ErrNoRows {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	start := (r.StartTime + 3599) / 3600 * 3600
	end := min(r.EndTime/3600*3600, before/3600*3600)
	if start >= end {
		return nil, before, nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT hour,sample_count,valid_count,value_sum,value_min,value_max,first_at,last_at,last_value,peak_at,timeout_at,gaps_json
		FROM history_rollups h WHERE node_uuid=? AND kind='ping' AND target_name=? AND host=? AND method=? AND hour>=? AND hour<? AND sample_count>0
		AND NOT EXISTS (SELECT 1 FROM history_rollup_dirty d WHERE d.node_uuid=h.node_uuid AND d.kind='ping' AND d.hour=h.hour) ORDER BY hour`,
		r.UUID, r.Target, r.Host, r.Method, start, end)
	if err != nil {
		return nil, 0, err
	}
	defer rows.Close()
	var result []pingHourRollup
	for rows.Next() {
		if result == nil {
			// The hour bound is known. Avoid repeatedly copying hundreds of
			// summary structs as a 90-day result slice grows.
			result = make([]pingHourRollup, 0, int(min((end-start)/3600, pingHistoryRetentionSec/3600)))
		}
		var v pingHourRollup
		var minimum, maximum sql.NullFloat64
		var gaps string
		if err = rows.Scan(&v.hour, &v.count, &v.valid, &v.sum, &minimum, &maximum, &v.first, &v.last, &v.current, &v.peak, &v.timeout, &gaps); err != nil {
			return nil, 0, err
		}
		v.minimum, v.maximum = minimum.Float64, maximum.Float64
		if gaps != "" && gaps != "[]" && gaps != "null" {
			if err = json.Unmarshal([]byte(gaps), &v.gaps); err != nil {
				return nil, 0, fmt.Errorf("invalid ping rollup gaps: %w", err)
			}
		}
		result = append(result, v)
	}
	return result, before, rows.Err()
}

func previousPingRollup(ctx context.Context, tx *sql.Tx, r *PingHistoryResponse, observation PingObservation, before int64) (sql.NullInt64, error) {
	var previous sql.NullInt64
	if before <= 0 {
		return previous, nil
	}
	err := tx.QueryRowContext(ctx, `SELECT MAX(last_at) FROM history_rollups h WHERE node_uuid=? AND kind='ping' AND target_name=? AND host=? AND method=?
		AND last_at<? AND last_at>=? AND hour<? AND NOT EXISTS (SELECT 1 FROM history_rollup_dirty d WHERE d.node_uuid=h.node_uuid AND d.kind='ping' AND d.hour=h.hour)`,
		r.UUID, r.Target, r.Host, r.Method, r.StartTime, observation.StartedAt, before/3600*3600).Scan(&previous)
	return previous, err
}

type pingHistoryBuilder struct {
	r            *PingHistoryResponse
	previous     sql.NullInt64
	first, last  int64
	valid, lost  int
	sum          float64
	step, origin int64
	raw, buckets []PingSample
	bucket       pingChartBucket
	rolled       bool
}

func newPingHistoryBuilder(r *PingHistoryResponse, previous sql.NullInt64, rolled bool) *pingHistoryBuilder {
	r.Stats = PingStats{Current: -1, Min: -1, Max: -1}
	step := max(int64(1), (r.EndTime-r.StartTime+1+maxChartSamples-1)/maxChartSamples)
	origin := r.StartTime
	if rolled {
		// An hour cannot be split back into minute samples. Keep complete hours
		// in a single display bucket and preserve their peak/timeout witnesses.
		origin = r.StartTime / 3600 * 3600
		step = max(int64(3600), ((r.EndTime-origin+1+maxChartSamples-1)/maxChartSamples+3599)/3600*3600)
	}
	return &pingHistoryBuilder{r: r, previous: previous, step: step, origin: origin, rolled: rolled,
		raw: make([]PingSample, 0, 24), buckets: make([]PingSample, 0, 24)}
}

func (b *pingHistoryBuilder) observe(first, last int64) {
	const threshold = int64(PingSampleIntervalSec * 3)
	if b.first == 0 {
		b.first = first
		leadingStart := b.r.StartTime
		leadingGap := first-leadingStart > threshold
		if b.previous.Valid {
			leadingStart = max(leadingStart, b.previous.Int64+PingSampleIntervalSec)
			leadingGap = first-b.previous.Int64 > threshold
		}
		if leadingGap && first > leadingStart {
			b.r.OfflineIntervals = append(b.r.OfflineIntervals, OfflineInterval{leadingStart, first})
		}
	} else if first-b.last > threshold {
		b.r.OfflineIntervals = append(b.r.OfflineIntervals, OfflineInterval{b.last + PingSampleIntervalSec, first})
	}
	b.last = last
}

func (b *pingHistoryBuilder) chartBucket(at int64) {
	index := (at - b.origin) / b.step
	if b.bucket.count > 0 && b.bucket.index != index {
		b.buckets = append(b.buckets, b.bucket.sample())
		b.bucket = pingChartBucket{}
	}
	b.bucket.index = index
}

func (b *pingHistoryBuilder) addRaw(at int64, latency int) {
	b.observe(at, at)
	b.r.Stats.Current = latency
	b.r.Stats.TotalCount++
	if latency < 0 {
		b.lost++
	} else {
		b.valid++
		b.sum += float64(latency)
		if b.r.Stats.Min < 0 || latency < b.r.Stats.Min {
			b.r.Stats.Min = latency
		}
		b.r.Stats.Max = max(b.r.Stats.Max, latency)
	}
	sample := PingSample{Host: b.r.Host, Method: b.r.Method, Timestamp: at, Latency: latency}
	if !b.rolled && b.r.Stats.TotalCount <= maxChartSamples {
		b.raw = append(b.raw, sample)
	}
	b.chartBucket(at)
	b.bucket.add(b.bucket.index, sample)
}

func (b *pingHistoryBuilder) addRollup(v pingHourRollup) {
	b.observe(v.first, v.last)
	b.r.OfflineIntervals = append(b.r.OfflineIntervals, v.gaps...)
	b.r.Stats.Current = int(v.current)
	b.r.Stats.TotalCount += v.count
	b.valid += v.valid
	b.lost += v.count - v.valid
	b.sum += v.sum
	if v.valid > 0 {
		if b.r.Stats.Min < 0 || int(v.minimum) < b.r.Stats.Min {
			b.r.Stats.Min = int(v.minimum)
		}
		b.r.Stats.Max = max(b.r.Stats.Max, int(v.maximum))
	}
	b.chartBucket(v.hour)
	b.bucket.count += v.count
	b.bucket.valid += v.valid
	b.bucket.sum += v.sum
	if v.valid > 0 && (b.bucket.valid == v.valid || int(v.maximum) > b.bucket.maximum.Latency) {
		b.bucket.maximum = PingSample{Host: b.r.Host, Method: b.r.Method, Timestamp: v.peak, Latency: int(v.maximum)}
	}
	if v.timeout > 0 && !b.bucket.hasLoss {
		b.bucket.hasLoss = true
		b.bucket.loss = PingSample{Host: b.r.Host, Method: b.r.Method, Timestamp: v.timeout, Latency: -1}
	}
}

func (b *pingHistoryBuilder) readRaw(ctx context.Context, tx *sql.Tx, start, end int64) error {
	if start > end {
		return nil
	}
	rows, err := tx.QueryContext(ctx, `SELECT timestamp,latency FROM ping_history WHERE node_uuid=? AND target_name=? AND host=? AND method=? AND timestamp>=? AND timestamp<=? ORDER BY timestamp`,
		b.r.UUID, b.r.Target, b.r.Host, b.r.Method, start, end)
	if err != nil {
		return err
	}
	defer rows.Close()
	var at int64
	var latency int
	for rows.Next() {
		if err = rows.Scan(&at, &latency); err != nil {
			return err
		}
		b.addRaw(at, latency)
	}
	return rows.Err()
}

func (b *pingHistoryBuilder) finish(observation PingObservation) {
	if b.bucket.count > 0 {
		b.buckets = append(b.buckets, b.bucket.sample())
	}
	if !b.rolled && b.r.Stats.TotalCount <= maxChartSamples {
		b.r.Samples = b.raw
	} else {
		b.r.Samples = b.buckets
	}
	if b.valid > 0 {
		b.r.Stats.Avg = math.Round(b.sum/float64(b.valid)*10) / 10
	}
	last := b.last
	if last == 0 {
		last = observation.LastSampleAt
		if b.previous.Valid {
			last = b.previous.Int64
		}
	}
	if last > 0 && b.r.EndTime-last > PingSampleIntervalSec*3 {
		b.r.OfflineIntervals = append(b.r.OfflineIntervals, OfflineInterval{max(b.r.StartTime, last+PingSampleIntervalSec), b.r.EndTime})
	}
	for _, interval := range b.r.OfflineIntervals {
		missing := int((interval.End - interval.Start + PingSampleIntervalSec - 1) / PingSampleIntervalSec)
		b.r.Stats.TotalCount += missing
		b.lost += missing
	}
	if b.r.Stats.TotalCount > 0 {
		b.r.Stats.PacketLoss = math.Round(float64(b.lost)/float64(b.r.Stats.TotalCount)*1000) / 10
	}
}
