package store

import (
	"context"
	"encoding/json"
	"time"
)

type ArchivedDailyReport struct {
	Date  string
	Nodes []NodeDailyReport
}

// ArchivedDailyReportsContext lists existing summaries in date order. Early
// versions also stored a UTC+8 copy, so select one archived row for each node.
func (s *Store) ArchivedDailyReportsContext(ctx context.Context, first, last time.Time) ([]ArchivedDailyReport, error) {
	rows, err := s.sdb.reader().QueryContext(ctx, `WITH ranked AS (
		SELECT r.date,r.node_uuid,r.summary_json,r.timezone,ROW_NUMBER() OVER (
			PARTITION BY r.date,r.node_uuid ORDER BY CASE
				WHEN n.uuid IS NOT NULL AND r.timezone=COALESCE(NULLIF(json_extract(n.data_json,'$.billing_timezone'),''),'UTC+8') THEN 0
				WHEN r.timezone<>'UTC+8' THEN 1 ELSE 2 END,r.timezone
		) AS row_number
		FROM node_daily_reports AS r LEFT JOIN nodes AS n ON n.uuid=r.node_uuid
		WHERE r.date BETWEEN ? AND ?
	) SELECT date,node_uuid,timezone,summary_json FROM ranked WHERE row_number=1
	ORDER BY date,json_extract(summary_json,'$.name'),node_uuid`, first.Format(time.DateOnly), last.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ArchivedDailyReport
	for rows.Next() {
		var n NodeDailyReport
		var uuid, date, zone, data string
		if err := rows.Scan(&date, &uuid, &zone, &data); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &n); err != nil {
			return nil, err
		}
		n.Date, n.UUID, n.Timezone = date, uuid, zone
		if len(result) == 0 || result[len(result)-1].Date != date {
			result = append(result, ArchivedDailyReport{Date: date})
		}
		group := &result[len(result)-1]
		group.Nodes = append(group.Nodes, n)
	}
	return result, rows.Err()
}

func (s *Store) ArchivedDailyBandwidthContext(ctx context.Context, date time.Time, reports []NodeDailyReport) (ReportBandwidth, error) {
	rates := make([]bandwidthWindow, 0, len(reports))
	for _, node := range reports {
		location, err := loadBillingLocation(node.Timezone)
		if err != nil {
			return ReportBandwidth{}, err
		}
		start := localDayStart(date, location)
		end := localDayStart(date.AddDate(0, 0, 1), location)
		rates = append(rates, bandwidthWindow{node.UUID, start.Unix(), end.Unix()})
	}
	return s.sdb.dailyReportBandwidth(ctx, rates)
}
