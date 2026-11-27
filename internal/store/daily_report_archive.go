package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

// ArchivedDailyReportComplete prevents a partly collected date from being
// presented as a fleet report while nodes in later timezones are still active.
func (s *Store) ArchivedDailyReportComplete(date, now time.Time, reports []NodeDailyReport) bool {
	windows, ready := s.reportWindows(now, date)
	if !ready {
		return false
	}
	stored := make(map[string]bool, len(reports))
	for _, report := range reports {
		stored[report.UUID] = true
	}
	for _, window := range windows {
		if !stored[window.summary.UUID] {
			return false
		}
	}
	return true
}

func (s *Store) ArchivedDailyBandwidthContext(ctx context.Context, date, now time.Time, reports []NodeDailyReport) (ReportBandwidth, error) {
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
	return s.sdb.loadOrArchiveDailyReportBandwidth(ctx, date, now, rates)
}

func (s *sqliteDB) loadDailyReportBandwidth(ctx context.Context, date time.Time) (ReportBandwidth, bool, error) {
	var peak ReportBandwidth
	err := s.reader().QueryRowContext(ctx, `SELECT peak_bytes_per_second,peak_minutes,sampled_minutes
		FROM daily_report_bandwidth WHERE date=?`, date.Format(reportDateLayout)).Scan(
		&peak.BytesPerSecond, &peak.Minutes, &peak.Samples)
	if errors.Is(err, sql.ErrNoRows) {
		return ReportBandwidth{}, false, nil
	}
	return peak, err == nil, err
}

func (s *sqliteDB) loadOrArchiveDailyReportBandwidth(ctx context.Context, date, now time.Time, windows []bandwidthWindow) (ReportBandwidth, error) {
	peak, found, err := s.loadDailyReportBandwidth(ctx, date)
	if found || err != nil || len(windows) == 0 {
		return peak, err
	}
	for _, window := range windows {
		if window.From < now.Unix()-resourceHistoryRetentionSec {
			// An old database may predate the fleet archive. A partly retained
			// sampling window cannot reconstruct that day's aggregate peak.
			return ReportBandwidth{}, nil
		}
	}
	peak, err = s.dailyReportBandwidth(ctx, windows)
	if err != nil {
		return ReportBandwidth{}, err
	}
	_, err = s.db.ExecContext(ctx, `INSERT INTO daily_report_bandwidth(date,peak_bytes_per_second,peak_minutes,sampled_minutes)
		VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, date.Format(reportDateLayout), peak.BytesPerSecond, peak.Minutes, peak.Samples)
	if err != nil {
		return ReportBandwidth{}, err
	}
	// Scheduled and on-demand reports can both find an unarchived date.
	// Read back the first committed result so both use the same snapshot.
	peak, _, err = s.loadDailyReportBandwidth(ctx, date)
	return peak, err
}
