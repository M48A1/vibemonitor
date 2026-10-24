package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

var ErrRankingPeriod = errors.New("invalid ranking period")

type RankingEntry struct {
	UUID    string  `json:"uuid"`
	Name    string  `json:"name"`
	Value   float64 `json:"value"`
	Samples int64   `json:"samples"`
}

type RankingGroup struct {
	Metric  string         `json:"metric"`
	Entries []RankingEntry `json:"entries"`
}

type RankingsResponse struct {
	Period    string         `json:"period"`
	StartTime int64          `json:"start_time"`
	EndTime   int64          `json:"end_time"`
	Timezone  string         `json:"timezone"`
	Groups    []RankingGroup `json:"groups"`
}

var rankingMetrics = []struct{ name, column string }{
	{"connections", "connections"}, {"load", "load1"}, {"memory", "ram_usage"}, {"connection_rate", "connection_rate"},
}

func rankingStart(period string, now time.Time) (time.Time, error) {
	now = now.In(trafficLocation)
	day := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, trafficLocation)
	switch period {
	case "day":
		return day, nil
	case "week":
		return day.AddDate(0, 0, -(int(day.Weekday())+6)%7), nil
	case "month":
		return time.Date(now.Year(), now.Month(), 1, 0, 0, 0, 0, trafficLocation), nil
	default:
		return time.Time{}, ErrRankingPeriod
	}
}

func (s *Store) GetRankingsContext(ctx context.Context, period string) (*RankingsResponse, error) {
	return s.rankingsAt(ctx, period, time.Now())
}

func (s *Store) rankingsAt(ctx context.Context, period string, now time.Time) (*RankingsResponse, error) {
	start, err := rankingStart(period, now)
	if err != nil {
		return nil, err
	}
	response := &RankingsResponse{Period: period, StartTime: start.Unix(), EndTime: now.Unix(), Timezone: "UTC+8", Groups: []RankingGroup{}}
	names := make(map[string]string)
	traffic := []RankingEntry{}
	firstDay, lastDay := start.Format("2006-01-02"), now.In(trafficLocation).Format("2006-01-02")
	s.mu.RLock()
	for id, n := range s.nodes {
		names[id] = n.Name
		entry := RankingEntry{UUID: id, Name: n.Name}
		for day, value := range n.DailyTraffic {
			if day >= firstDay && day <= lastDay && value >= 0 {
				entry.Value += float64(value)
				entry.Samples++
			}
		}
		if entry.Samples > 0 {
			traffic = append(traffic, entry)
		}
	}
	s.mu.RUnlock()
	// One snapshot keeps the watermark, dirty hours and raw samples consistent.
	tx, err := s.sdb.reader().BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var watermark int64
	if err = tx.QueryRowContext(ctx, "SELECT completed_before FROM history_rollup_state WHERE id=1").Scan(&watermark); err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	first, last := resourceRollupHours(start.Unix(), now.Unix(), watermark)
	rawFields := "node_uuid,connections,load1,ram_usage,connection_rate"
	raw := "SELECT " + rawFields + " FROM resource_history WHERE timestamp>=? AND timestamp<=?"
	args := []any{start.Unix(), now.Unix()}
	if first < last {
		// Bound raw reads to uncovered edges and dirty hours; month views must
		// not rescan every archived minute once hourly summaries are available.
		raw = "SELECT " + rawFields + " FROM resource_history WHERE timestamp>=? AND timestamp<? UNION ALL " +
			"SELECT " + rawFields + " FROM resource_history WHERE timestamp>=? AND timestamp<=? UNION ALL " +
			`SELECT r.node_uuid,r.connections,r.load1,r.ram_usage,r.connection_rate
 FROM history_rollup_dirty d CROSS JOIN resource_history r
 WHERE d.kind='resource' AND d.hour>=? AND d.hour<?
 AND r.node_uuid=d.node_uuid AND r.timestamp>=d.hour AND r.timestamp<d.hour+3600`
		args = []any{start.Unix(), first, last, now.Unix(), first, last}
	}
	columns := []string{"node_uuid"}
	for _, metric := range rankingMetrics {
		columns = append(columns, "SUM("+metric.column+")", "COUNT("+metric.column+")")
	}
	type aggregate struct {
		sum   float64
		count int64
	}
	values := make(map[string]map[string]aggregate)
	add := func(id, metric string, sum float64, count int64) {
		if _, exists := names[id]; !exists || count == 0 {
			return
		}
		if values[metric] == nil {
			values[metric] = make(map[string]aggregate)
		}
		old := values[metric][id]
		values[metric][id] = aggregate{old.sum + sum, old.count + count}
	}
	rows, err := tx.QueryContext(ctx, "SELECT "+strings.Join(columns, ",")+" FROM ("+raw+") GROUP BY node_uuid", args...)
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var id string
		sums, counts := make([]sql.NullFloat64, len(rankingMetrics)), make([]int64, len(rankingMetrics))
		dest := []any{&id}
		for i := range rankingMetrics {
			dest = append(dest, &sums[i], &counts[i])
		}
		if err = rows.Scan(dest...); err != nil {
			rows.Close()
			return nil, err
		}
		for i, metric := range rankingMetrics {
			add(id, metric.name, sums[i].Float64, counts[i])
		}
	}
	if err = rows.Err(); err != nil {
		rows.Close()
		return nil, err
	}
	if err = rows.Close(); err != nil {
		return nil, err
	}
	if first < last {
		rows, err = tx.QueryContext(ctx, `SELECT node_uuid,kind,SUM(value_sum),SUM(valid_count)
 FROM history_rollups h WHERE hour>=? AND hour<? AND kind IN ('connections','load','memory','connection_rate')
 AND target_name='' AND host='' AND method='' AND valid_count>0 AND NOT EXISTS
 (SELECT 1 FROM history_rollup_dirty d WHERE d.node_uuid=h.node_uuid AND d.kind='resource' AND d.hour=h.hour)
 GROUP BY node_uuid,kind`, first, last)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id, metric string
			var sum float64
			var count int64
			if err = rows.Scan(&id, &metric, &sum, &count); err != nil {
				rows.Close()
				return nil, err
			}
			add(id, metric, sum, count)
		}
		if err = rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		if err = rows.Close(); err != nil {
			return nil, err
		}
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	for _, metric := range rankingMetrics {
		group := RankingGroup{Metric: metric.name, Entries: []RankingEntry{}}
		for id, value := range values[metric.name] {
			group.Entries = append(group.Entries, RankingEntry{UUID: id, Name: names[id], Value: value.sum / float64(value.count), Samples: value.count})
		}
		group.Entries = topThree(group.Entries)
		response.Groups = append(response.Groups, group)
	}
	response.Groups = append(response.Groups, RankingGroup{Metric: "traffic", Entries: topThree(traffic)})
	return response, nil
}

func topThree(entries []RankingEntry) []RankingEntry {
	sort.Slice(entries, func(i, j int) bool {
		if entries[i].Value == entries[j].Value {
			return entries[i].UUID < entries[j].UUID
		}
		return entries[i].Value > entries[j].Value
	})
	if len(entries) > 3 {
		return entries[:3]
	}
	return entries
}
