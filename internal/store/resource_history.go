package store

import (
	"database/sql"
	"errors"
	"math"
	"time"
)

const resourceHistoryRetentionSec = 90 * 86400

type ResourceSample struct {
	Timestamp int64   `json:"t"`
	Usage     float64 `json:"v"`
}

type ResourceStats struct {
	Current float64 `json:"current"`
	Avg     float64 `json:"avg"`
	Min     float64 `json:"min"`
	Max     float64 `json:"max"`
	Count   int64   `json:"count"`
}

type ResourceHistoryResponse struct {
	UUID        string           `json:"uuid"`
	Metric      string           `json:"metric"`
	Range       string           `json:"range"`
	StartTime   int64            `json:"start_time"`
	EndTime     int64            `json:"end_time"`
	StepSeconds int64            `json:"step_seconds"`
	Stats       ResourceStats    `json:"stats"`
	Samples     []ResourceSample `json:"samples"`
}

func validResourcePercent(value float64) bool {
	return !math.IsNaN(value) && !math.IsInf(value, 0) && value >= 0 && value <= 100
}

func totalNetworkRate(up, down int64) (int64, bool) {
	if up < 0 || down < 0 || up > math.MaxInt64-down {
		return 0, false
	}
	return up + down, true
}

func (s *sqliteDB) latestResourceSampleTime(uuid string) (int64, error) {
	var timestamp sql.NullInt64
	err := s.db.QueryRow("SELECT MAX(timestamp) FROM resource_history WHERE node_uuid = ?", uuid).Scan(&timestamp)
	return timestamp.Int64, err
}

func (s *sqliteDB) recordResourceSample(uuid string, timestamp int64, cpu, ram sql.NullFloat64, network sql.NullInt64) error {
	_, err := s.db.Exec(`INSERT INTO resource_history(node_uuid,timestamp,cpu_usage,ram_usage,network_rate)
		VALUES(?,?,?,?,?) ON CONFLICT(node_uuid,timestamp) DO UPDATE SET
		cpu_usage=excluded.cpu_usage,ram_usage=excluded.ram_usage,network_rate=excluded.network_rate`, uuid, timestamp, cpu, ram, network)
	return err
}

func (s *sqliteDB) pruneOldResourceHistory(before int64) (int64, error) {
	result, err := s.db.Exec("DELETE FROM resource_history WHERE timestamp < ?", before)
	if err != nil {
		return 0, err
	}
	return result.RowsAffected()
}

func resourceDuration(name string) (int64, bool) {
	switch name {
	case "1h":
		return 3600, true
	case "24h":
		return 86400, true
	case "7d":
		return 7 * 86400, true
	case "31d":
		return 31 * 86400, true
	default:
		return 0, false
	}
}

func (s *Store) GetResourceHistory(uuid, metric, timeRange string) (*ResourceHistoryResponse, error) {
	if metric != "cpu" && metric != "memory" && metric != "network" {
		return nil, errors.New("invalid resource metric")
	}
	duration, ok := resourceDuration(timeRange)
	if !ok {
		return nil, errors.New("invalid resource range")
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.nodes[uuid] == nil {
		return nil, errors.New("node not found")
	}
	now := time.Now().Unix()
	start := now - duration
	// Keep at most about 720 buckets in long ranges, aligned to whole minutes.
	step := ((duration+719)/720 + 59) / 60 * 60
	column := "cpu_usage"
	switch metric {
	case "memory":
		column = "ram_usage"
	case "network":
		column = "network_rate"
	}
	response := &ResourceHistoryResponse{
		UUID: uuid, Metric: metric, Range: timeRange, StartTime: start,
		EndTime: now, StepSeconds: step, Samples: []ResourceSample{},
	}
	where := " FROM resource_history WHERE node_uuid = ? AND timestamp >= ? AND timestamp <= ? AND " + column + " IS NOT NULL"
	var avg, minValue, maxValue sql.NullFloat64
	err := s.sdb.db.QueryRow("SELECT COUNT(*), AVG("+column+"), MIN("+column+"), MAX("+column+")"+where,
		uuid, start, now).Scan(&response.Stats.Count, &avg, &minValue, &maxValue)
	if err != nil {
		return nil, err
	}
	if response.Stats.Count > 0 {
		response.Stats.Avg = avg.Float64
		response.Stats.Min = minValue.Float64
		response.Stats.Max = maxValue.Float64
		if err := s.sdb.db.QueryRow("SELECT "+column+where+" ORDER BY timestamp DESC LIMIT 1", uuid, start, now).Scan(&response.Stats.Current); err != nil {
			return nil, err
		}
	}
	rows, err := s.sdb.db.Query("SELECT MIN(timestamp), AVG("+column+")"+where+" GROUP BY timestamp / ? ORDER BY MIN(timestamp)", uuid, start, now, step)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var point ResourceSample
		if err := rows.Scan(&point.Timestamp, &point.Usage); err != nil {
			return nil, err
		}
		response.Samples = append(response.Samples, point)
	}
	return response, rows.Err()
}
