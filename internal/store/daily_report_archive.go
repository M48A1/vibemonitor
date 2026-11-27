package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"time"
)

type ArchivedDailyReport struct {
	Date      string
	Nodes     []NodeDailyReport
	Bandwidth ReportBandwidth
	finalized bool
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
	) SELECT r.date,r.node_uuid,r.timezone,r.summary_json,b.date IS NOT NULL,
		COALESCE(b.peak_bytes_per_second,0),COALESCE(b.peak_minutes,0),COALESCE(b.sampled_minutes,0)
	FROM ranked AS r LEFT JOIN daily_report_bandwidth AS b ON b.date=r.date WHERE r.row_number=1
	ORDER BY r.date,json_extract(r.summary_json,'$.name'),r.node_uuid`, first.Format(time.DateOnly), last.Format(time.DateOnly))
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []ArchivedDailyReport
	for rows.Next() {
		var n NodeDailyReport
		var uuid, date, zone, data string
		var peak ReportBandwidth
		var finalized bool
		if err := rows.Scan(&date, &uuid, &zone, &data, &finalized, &peak.BytesPerSecond, &peak.Minutes, &peak.Samples); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(data), &n); err != nil {
			return nil, err
		}
		n.Date, n.UUID, n.Timezone = date, uuid, zone
		if len(result) == 0 || result[len(result)-1].Date != date {
			result = append(result, ArchivedDailyReport{Date: date, Bandwidth: peak, finalized: finalized})
		}
		group := &result[len(result)-1]
		group.Nodes = append(group.Nodes, n)
	}
	return result, rows.Err()
}

// LatestArchivedDailyReportContext returns the newest complete saved report,
// even if no archive was written on any of the last few days.
func (s *Store) LatestArchivedDailyReportContext(ctx context.Context, through, now time.Time) (ArchivedDailyReport, error) {
	before := civilDate(through).AddDate(0, 0, 1).Format(reportDateLayout)
	for {
		var key sql.NullString
		err := s.sdb.reader().QueryRowContext(ctx, `SELECT MAX(date) FROM node_daily_reports WHERE date<?`, before).Scan(&key)
		if err != nil || !key.Valid {
			return ArchivedDailyReport{}, err
		}
		date, err := time.Parse(reportDateLayout, key.String)
		if err != nil {
			return ArchivedDailyReport{}, err
		}
		reports, err := s.ArchivedDailyReportsContext(ctx, date, date)
		if err != nil {
			return ArchivedDailyReport{}, err
		}
		if len(reports) > 0 && s.ArchivedDailyReportComplete(date, now, reports[0]) {
			return reports[0], nil
		}
		before = key.String
	}
}

// ArchivedDailyReportComplete prevents a partly collected date from being
// presented as a fleet report while nodes in later timezones are still active.
func (s *Store) ArchivedDailyReportComplete(date, now time.Time, report ArchivedDailyReport) bool {
	// The fleet archive is written only once every node has settled. Older
	// dates without this marker still use the original completeness check.
	if report.finalized {
		return true
	}
	windows, ready := s.reportWindows(now, date)
	if !ready {
		return false
	}
	stored := make(map[string]bool, len(report.Nodes))
	for _, node := range report.Nodes {
		stored[node.UUID] = true
	}
	for _, window := range windows {
		if !stored[window.summary.UUID] {
			return false
		}
	}
	return true
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

func (s *sqliteDB) archiveDailyReportBandwidth(ctx context.Context, date time.Time, windows []bandwidthWindow) (bool, error) {
	peak, err := s.dailyReportBandwidth(ctx, windows)
	if err != nil {
		return false, err
	}
	result, err := s.db.ExecContext(ctx, `INSERT INTO daily_report_bandwidth(date,peak_bytes_per_second,peak_minutes,sampled_minutes)
		VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, date.Format(reportDateLayout), peak.BytesPerSecond, peak.Minutes, peak.Samples)
	if err != nil {
		return false, err
	}
	inserted, err := result.RowsAffected()
	return inserted > 0, err
}
