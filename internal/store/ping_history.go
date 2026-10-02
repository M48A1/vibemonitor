package store

import (
	"context"
	"database/sql"
	"errors"
	"math"
	"sort"
	"time"
)

// PingObservation separates a target's current measurement series from older
// hosts or methods. Its start survives restarts and SQLite history retention.
const pingObservationUpsert = `INSERT INTO ping_observations(node_uuid,target_name,host,method,started_at,last_sample_at) VALUES(?,?,?,?,?,?)
 ON CONFLICT(node_uuid,target_name) DO UPDATE SET host=excluded.host,method=excluded.method,started_at=excluded.started_at,last_sample_at=excluded.last_sample_at
 WHERE excluded.last_sample_at>=ping_observations.last_sample_at AND
 (excluded.host<>ping_observations.host OR excluded.method<>ping_observations.method OR
  excluded.started_at<>ping_observations.started_at OR excluded.last_sample_at<>ping_observations.last_sample_at)`

type PingObservation struct {
	Host         string `json:"host"`
	Method       string `json:"method"`
	StartedAt    int64  `json:"started_at"`
	LastSampleAt int64  `json:"last_sample_at,omitempty"`
}

type pingLossPoint struct {
	timestamp int64
	lost      int // Cumulative observed timeouts, not a rounded percentage.
}

// The overview keeps 24 chart points but counts a complete rolling day. Prefix
// counts and a binary search avoid rescanning 1,440 points on every WS update.
type pingLossWindow struct {
	Host, Method string
	points       []pingLossPoint
	baseLost     int
}

func (w *pingLossWindow) append(sample PingSample) {
	lost := w.baseLost
	if len(w.points) > 0 {
		last := w.points[len(w.points)-1]
		if sample.Timestamp <= last.timestamp {
			return
		}
		lost = last.lost
	}
	if sample.Latency < 0 {
		lost++
	}
	w.points = append(w.points, pingLossPoint{sample.Timestamp, lost})
	cutoff := sample.Timestamp - 86400
	i := sort.Search(len(w.points), func(i int) bool { return w.points[i].timestamp >= cutoff })
	if i > 0 {
		w.baseLost = w.points[i-1].lost
		w.points = w.points[i:]
	}
}

func (w *pingLossWindow) counts(cutoff int64) (lost, total int) {
	i := sort.Search(len(w.points), func(i int) bool { return w.points[i].timestamp >= cutoff })
	if i == len(w.points) {
		return 0, 0
	}
	base := w.baseLost
	if i > 0 {
		base = w.points[i-1].lost
	}
	return w.points[len(w.points)-1].lost - base, len(w.points) - i
}

func (s *Store) recordPingObservationLocked(n *Node, name, host, method string, at int64) {
	if n.PingObserved == nil {
		n.PingObserved = make(map[string]PingObservation)
	}
	if n.pingWindows == nil {
		n.pingWindows = make(map[string]*pingLossWindow)
	}
	observation := n.PingObserved[name]
	if observation.Host != host || observation.StartedAt <= 0 || (observation.Method != "" && observation.Method != method) {
		observation = PingObservation{Host: host, Method: method, StartedAt: at}
		delete(n.PingHistory, name)
		delete(n.pingWindows, name)
	} else if observation.Method == "" {
		observation.Method = method
		observation.StartedAt = at // Until the first measurement, availability is unknown.
	}
	n.PingObserved[name] = observation
	if n.pingWindows[name] == nil {
		n.pingWindows[name] = &pingLossWindow{Host: host, Method: method}
	}
	// Adopting a legacy probe's first method does not mix it with another series.
	n.pingWindows[name].Method = method
}

func (s *Store) GetPingHistory(uuid, targetName, timeRange string) (*PingHistoryResponse, error) {
	return s.GetPingHistoryContext(context.Background(), uuid, targetName, timeRange)
}

// GetPingHistoryContext cancels SQLite work when the HTTP request goes away.
func (s *Store) GetPingHistoryContext(ctx context.Context, uuid, targetName, timeRange string) (*PingHistoryResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.RLock()
	n := s.nodes[uuid]
	if n == nil {
		s.mu.RUnlock()
		return nil, errors.New("node not found")
	}
	var host string
	for i, target := range s.targetsLocked(n) {
		if targetName == "" && i == 0 {
			targetName = target.Name
		}
		if target.Name == targetName {
			host = target.Host
			break
		}
	}
	observation := n.PingObserved[targetName]
	// Only the selected target's tiny preview needs a copy, not every history.
	recent := append([]PingSample(nil), n.PingHistory[targetName]...)
	s.mu.RUnlock()
	now := time.Now().Unix()
	duration := int64(86400)
	switch timeRange {
	case "1h":
		duration = 3600
	case "7d", "7*24h", "7x24h", "168h":
		duration = 7 * 86400
	case "31d", "31*24h", "31x24h", "744h":
		duration = 31 * 86400
	case "all":
		duration = pingHistoryRetentionSec
	default:
		timeRange = "24h"
	}
	start := now - duration
	if observation.Host == host && observation.StartedAt > start {
		start = min(observation.StartedAt, now)
	}
	response := &PingHistoryResponse{UUID: uuid, Target: targetName, Host: host, Method: observation.Method,
		Range: timeRange, StartTime: start, EndTime: now, Stats: PingStats{Current: -1, Min: -1, Max: -1}, Samples: []PingSample{}}
	if host == "" {
		return response, ctx.Err()
	}
	key := pingHistoryCacheKey{uuid: uuid, target: targetName, host: host, timeRange: timeRange, observation: observation}
	if cached := s.cachedPingHistory(key); cached != nil {
		return cached, ctx.Err()
	}
	if s.sdb != nil {
		if err := s.sdb.queryPingHistory(ctx, response, observation); err != nil {
			return nil, err
		}
		if response.Stats.TotalCount > 0 {
			s.cachePingHistory(key, response)
			return response, nil
		}
	}
	// A disk failure may leave the latest sample queued for retry. Use its small
	// in-memory preview; never hide SQLite or cancellation errors as empty data.
	for _, sample := range recent {
		if sample.Host == host && sample.Method == observation.Method && sample.Timestamp >= start && sample.Timestamp <= now {
			response.Samples = append(response.Samples, sample)
		}
	}
	if len(response.Samples) > 0 {
		fillPingStats(response, response.Samples)
	}
	s.cachePingHistory(key, response)
	return response, ctx.Err()
}

type pingHistoryCacheKey struct {
	uuid, target, host, timeRange string
	observation                   PingObservation
}
type pingHistoryCacheEntry struct {
	expires  time.Time
	response *PingHistoryResponse
}

func clonePingHistory(r *PingHistoryResponse) *PingHistoryResponse {
	c := *r
	c.Samples = append([]PingSample{}, r.Samples...)
	c.OfflineIntervals = append([]OfflineInterval(nil), r.OfflineIntervals...)
	return &c
}
func (s *Store) cachedPingHistory(key pingHistoryCacheKey) *PingHistoryResponse {
	s.pingHistoryCacheMu.Lock()
	defer s.pingHistoryCacheMu.Unlock()
	entry, ok := s.pingHistoryCache[key]
	if !ok {
		return nil
	}
	if !time.Now().Before(entry.expires) {
		delete(s.pingHistoryCache, key)
		return nil
	}
	return clonePingHistory(entry.response)
}
func (s *Store) cachePingHistory(key pingHistoryCacheKey, response *PingHistoryResponse) {
	// At most 32 entries and 720 outage spans each, capped near 2 MiB. The key's
	// epoch/latest sample invalidate changes immediately; unrelated viewers share
	// one second of history work. Callers may redact hosts without mutating cache.
	if len(response.OfflineIntervals) > maxChartSamples {
		return
	}
	s.pingHistoryCacheMu.Lock()
	defer s.pingHistoryCacheMu.Unlock()
	if s.pingHistoryCache == nil {
		s.pingHistoryCache = make(map[pingHistoryCacheKey]pingHistoryCacheEntry)
	}
	if len(s.pingHistoryCache) >= 32 {
		now := time.Now()
		for k, entry := range s.pingHistoryCache {
			if !now.Before(entry.expires) {
				delete(s.pingHistoryCache, k)
			}
		}
		if len(s.pingHistoryCache) >= 32 {
			for k := range s.pingHistoryCache {
				delete(s.pingHistoryCache, k)
				break
			}
		}
	}
	s.pingHistoryCache[key] = pingHistoryCacheEntry{time.Now().Add(time.Second), clonePingHistory(response)}
}

func fillPingStats(response *PingHistoryResponse, samples []PingSample) {
	stats := PingStats{Current: -1, Min: -1, Max: -1, TotalCount: len(samples)}
	lost, valid := 0, 0
	var sum int64
	for _, sample := range samples {
		if sample.Latency < 0 {
			lost++
			continue
		}
		if stats.Min < 0 || sample.Latency < stats.Min {
			stats.Min = sample.Latency
		}
		stats.Max = max(stats.Max, sample.Latency)
		valid++
		sum += int64(sample.Latency)
	}
	if len(samples) > 0 {
		stats.Current = samples[len(samples)-1].Latency
		stats.PacketLoss = math.Round(float64(lost)/float64(len(samples))*1000) / 10
	}
	if valid > 0 {
		stats.Avg = math.Round(float64(sum)/float64(valid)*10) / 10
	}
	response.Stats = stats
}

func (s *sqliteDB) queryPingHistory(ctx context.Context, r *PingHistoryResponse, observation PingObservation) error {
	tx, err := s.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if r.Method == "" {
		err = tx.QueryRowContext(ctx, `SELECT method FROM ping_history WHERE node_uuid=? AND target_name=? AND host=?
			AND timestamp<=? ORDER BY timestamp DESC LIMIT 1`, r.UUID, r.Target, r.Host, r.EndTime).Scan(&r.Method)
		if errors.Is(err, sql.ErrNoRows) {
			return nil // No observations: unknown, even when the node is offline.
		}
		if err != nil {
			return err
		}
	}
	if observation.StartedAt <= 0 || observation.Host != r.Host {
		var first sql.NullInt64
		if err = tx.QueryRowContext(ctx, `SELECT MIN(timestamp) FROM ping_history WHERE node_uuid=? AND target_name=? AND host=? AND method=?`, r.UUID, r.Target, r.Host, r.Method).Scan(&first); err != nil {
			if errors.Is(err, sql.ErrNoRows) {
				return nil
			}
			return err
		}
		if !first.Valid {
			return nil
		}
		r.StartTime = max(r.StartTime, first.Int64)
	}
	var previous sql.NullInt64
	if err = tx.QueryRowContext(ctx, `SELECT MAX(timestamp) FROM ping_history WHERE node_uuid=? AND target_name=? AND host=? AND method=? AND timestamp<? AND timestamp>=?`,
		r.UUID, r.Target, r.Host, r.Method, r.StartTime, observation.StartedAt).Scan(&previous); err != nil {
		return err
	}
	rollups, before, err := readPingHourRollups(ctx, tx, r)
	if err != nil {
		return err
	}
	rolledPrevious, err := previousPingRollup(ctx, tx, r, observation, before)
	if err != nil {
		return err
	}
	if rolledPrevious.Valid && (!previous.Valid || rolledPrevious.Int64 > previous.Int64) {
		previous = rolledPrevious
	}
	builder := newPingHistoryBuilder(r, previous, len(rollups) > 0)
	cursor := r.StartTime
	for _, rollup := range rollups {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = builder.readRaw(ctx, tx, cursor, rollup.hour-1); err != nil {
			return err
		}
		builder.addRollup(rollup)
		cursor = rollup.hour + 3600
	}
	if err = builder.readRaw(ctx, tx, cursor, r.EndTime); err != nil {
		return err
	}
	builder.finish(observation)
	return ctx.Err()
}

type pingChartBucket struct {
	index        int64
	count, valid int
	sum          float64
	maximum      PingSample
	loss         PingSample
	hasLoss      bool
}

func (b *pingChartBucket) add(index int64, sample PingSample) {
	b.index = index
	b.count++
	if sample.Latency < 0 {
		if !b.hasLoss {
			b.loss = sample
			b.hasLoss = true
		}
		return
	}
	b.valid++
	b.sum += float64(sample.Latency)
	if b.valid == 1 || sample.Latency > b.maximum.Latency {
		b.maximum = sample
	}
}
func (b *pingChartBucket) sample() PingSample {
	if b.hasLoss {
		return b.loss
	}
	sample := b.maximum
	average := int(math.Round(b.sum / float64(b.valid)))
	if sample.Latency <= average+15 {
		sample.Latency = average
	}
	return sample
}
