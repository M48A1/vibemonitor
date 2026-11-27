package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"math"
	"strings"
	"time"
)

const reportSettleDelay = 5 * time.Minute

type ReportLineQuality struct {
	Samples    int     `json:"samples"`
	Successful int     `json:"successful"`
	Pairs      int     `json:"jitter_pairs"`
	LatencyMS  float64 `json:"latency_ms"`
	JitterMS   float64 `json:"jitter_ms"`
	Loss       float64 `json:"loss_percent"`
}

type ReportBandwidth struct {
	BytesPerSecond float64 `json:"bytes_per_second"`
	Minutes        int     `json:"peak_minutes"`
	Samples        int     `json:"sampled_minutes"`
}

type NodeDailyReport struct {
	UUID            string            `json:"node_uuid"`
	Name            string            `json:"name"`
	Date            string            `json:"date"`
	Timezone        string            `json:"timezone"`
	DaySeconds      int64             `json:"day_seconds"`
	OnlineSeconds   float64           `json:"online_seconds"`
	CoverageSeconds float64           `json:"coverage_seconds"`
	Drops           int               `json:"drops"`
	OfflineSeconds  float64           `json:"offline_seconds"`
	TrafficObserved bool              `json:"traffic_observed"`
	Upload          int64             `json:"upload_bytes"`
	Download        int64             `json:"download_bytes"`
	CyclePercent    *float64          `json:"cycle_percent,omitempty"`
	Bandwidth       ReportBandwidth   `json:"bandwidth"`
	Prime           ReportLineQuality `json:"prime"`
	Other           ReportLineQuality `json:"other"`
}

type nodeReportWindow struct {
	summary NodeDailyReport
	start   time.Time
	end     time.Time
	targets []reportProbeTarget
}

// Snapshot only the counters for each completed window while reports are
// arriving. The historical sample queries run outside the state lock.
func (s *Store) reportWindows(now, date time.Time) ([]nodeReportWindow, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var windows []nodeReportWindow
	for _, n := range s.nodes {
		location := n.billingLocation()
		start := localDayStart(date, location)
		end := localDayStart(date.AddDate(0, 0, 1), location)
		if !start.Before(end) || !n.CreatedAt.Before(end) {
			continue
		}
		if now.Before(end.Add(reportSettleDelay)) {
			return nil, false
		}
		r := NodeDailyReport{
			UUID: n.UUID, Name: n.Name, Date: start.Format(reportDateLayout),
			Timezone: location.String(), DaySeconds: int64(end.Sub(start) / time.Second),
		}
		if h := n.DailyReportHistory; h != nil && h.LocalZone == r.Timezone {
			day := h.Days[reportDayKey(r.Timezone, r.Date)]
			r.TrafficObserved, r.Upload, r.Download = day.TrafficObserved, day.Upload, day.Download
			if day.QuotaObserved {
				percent := 100 * float64(day.CycleUsed) / float64(day.CycleLimit)
				r.CyclePercent = &percent
			}
		}
		targets := s.targetsLocked(n)
		probes := make([]reportProbeTarget, 0, len(targets))
		for _, target := range targets {
			probes = append(probes, reportProbeTarget{target.Name, target.Host, n.PingObserved[target.Name]})
		}
		windows = append(windows, nodeReportWindow{r, start, end, probes})
	}
	return windows, true
}

var (
	ErrReportDayOpen = errors.New("还有节点未结束该统计日，或数据仍在入库")
	ErrReportNoNodes = errors.New("该统计日没有可生成日报的节点")
	ErrReportExpired = errors.New("该统计日已超出原始采样保留期限")
)

// GenerateDailyReportContext adds the missing summaries for a complete civil
// date, reusing any that already exist. It reports whether the date was saved.
func (s *Store) GenerateDailyReportContext(ctx context.Context, date, now time.Time) (bool, error) {
	date = civilDate(date)
	_, found, err := s.sdb.loadDailyReportBandwidth(ctx, date)
	if found || err != nil {
		return false, err
	}
	windows, ready := s.reportWindows(now, date)
	if !ready {
		return false, ErrReportDayOpen
	}
	if len(windows) == 0 {
		return false, ErrReportNoNodes
	}
	for _, window := range windows {
		if window.start.Unix() < now.Unix()-resourceHistoryRetentionSec {
			return false, ErrReportExpired
		}
	}
	if err := s.SaveContext(ctx); err != nil {
		return false, err
	}
	rates := make([]bandwidthWindow, 0, len(windows))
	for _, w := range windows {
		if err := s.collectDailyNodeReport(ctx, w, now); err != nil {
			return false, err
		}
		rates = append(rates, bandwidthWindow{w.summary.UUID, w.start.Unix(), w.end.Unix()})
	}
	return s.sdb.archiveDailyReportBandwidth(ctx, date, rates)
}

func (s *Store) collectDailyNodeReport(ctx context.Context, w nodeReportWindow, now time.Time) error {
	r := w.summary
	var present int
	err := s.sdb.reader().QueryRowContext(ctx, `SELECT 1 FROM node_daily_reports
		WHERE timezone=? AND date=? AND node_uuid=?`, r.Timezone, r.Date, r.UUID).Scan(&present)
	if err == nil {
		return nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err := s.sdb.fillReportOutages(ctx, &w, now); err != nil {
		return err
	}
	r = w.summary
	r.Prime, r.Other, err = s.sdb.reportQuality(ctx, w)
	if err != nil {
		return err
	}
	r.Bandwidth, err = s.sdb.reportBandwidth(ctx, r.UUID, w.start, w.end)
	if err != nil {
		return err
	}
	data, err := json.Marshal(r)
	if err != nil {
		return err
	}
	_, err = s.sdb.db.ExecContext(ctx, `INSERT INTO node_daily_reports(timezone,date,node_uuid,summary_json)
		VALUES(?,?,?,?) ON CONFLICT DO NOTHING`, r.Timezone, r.Date, r.UUID, string(data))
	return err
}

type reportQualityAccumulator struct {
	ReportLineQuality
	delaySum, jitterSum float64
}

func (a *reportQualityAccumulator) result() ReportLineQuality {
	if a.Successful > 0 {
		a.LatencyMS = a.delaySum / float64(a.Successful)
	}
	if a.Pairs > 0 {
		a.JitterMS = a.jitterSum / float64(a.Pairs)
	}
	if a.Samples > 0 {
		a.Loss = 100 * float64(a.Samples-a.Successful) / float64(a.Samples)
	}
	return a.ReportLineQuality
}

func (s *sqliteDB) reportQuality(ctx context.Context, window nodeReportWindow) (ReportLineQuality, ReportLineQuality, error) {
	if len(window.targets) == 0 {
		return ReportLineQuality{}, ReportLineQuality{}, nil
	}
	// Use the existing (node, target, time) index to bound each series to this day.
	args := []any{window.summary.UUID, window.start.Unix(), window.end.Unix()}
	for _, target := range window.targets {
		args = append(args, target.name)
	}
	placeholders := strings.Repeat(",?", len(window.targets)-1)
	rows, err := s.reader().QueryContext(ctx, `SELECT target_name,host,method,timestamp,latency
		FROM ping_history WHERE node_uuid=? AND timestamp>=? AND timestamp<? AND target_name IN (?`+placeholders+`)
		ORDER BY target_name,timestamp`, args...)
	if err != nil {
		return ReportLineQuality{}, ReportLineQuality{}, err
	}
	var parts [2]reportQualityAccumulator
	defer rows.Close()
	var lastTarget, lastHost, lastMethod string
	var lastTimestamp int64
	lastLatency, lastPart := -1, -1
	for rows.Next() {
		var target, host, method string
		var at int64
		var latency int
		if err := rows.Scan(&target, &host, &method, &at, &latency); err != nil {
			return ReportLineQuality{}, ReportLineQuality{}, err
		}
		hour := time.Unix(at, 0).In(trafficLocation).Hour()
		part := 1
		if hour >= 20 && hour < 23 {
			part = 0
		}
		a := &parts[part]
		a.Samples++
		if latency >= 0 {
			a.Successful++
			a.delaySum += float64(latency)
			// A timeout, a sampling gap or a change of endpoint starts a new sequence.
			if lastLatency >= 0 && lastPart == part && lastTarget == target && lastHost == host &&
				lastMethod == method && at-lastTimestamp <= 2*PingSampleIntervalSec {
				a.Pairs++
				a.jitterSum += math.Abs(float64(latency) - float64(lastLatency))
			}
		}
		lastTarget, lastHost, lastMethod, lastTimestamp, lastLatency, lastPart = target, host, method, at, latency, part
	}
	return parts[0].result(), parts[1].result(), rows.Err()
}

type reportRateSample struct {
	minute int64
	rate   float64
}

func (s *sqliteDB) reportBandwidth(ctx context.Context, uuid string, start, end time.Time) (ReportBandwidth, error) {
	rows, err := s.reader().QueryContext(ctx, `SELECT timestamp/60,MAX(network_rate)
		FROM resource_history WHERE node_uuid=? AND timestamp>=? AND timestamp<? AND network_rate IS NOT NULL
		GROUP BY timestamp/60 ORDER BY timestamp/60`, uuid, start.Unix(), end.Unix())
	return readReportPeak(rows, err)
}

func readReportPeak(rows *sql.Rows, err error) (ReportBandwidth, error) {
	if err != nil {
		return ReportBandwidth{}, err
	}
	defer rows.Close()
	var samples []reportRateSample
	for rows.Next() {
		var sample reportRateSample
		if err := rows.Scan(&sample.minute, &sample.rate); err != nil {
			return ReportBandwidth{}, err
		}
		samples = append(samples, sample)
	}
	return reportPeak(samples), rows.Err()
}

// Duration is measured around the peak in consecutive sampled minutes at
// 95% or more of the peak. An unsampled minute breaks the run.
func reportPeak(samples []reportRateSample) ReportBandwidth {
	report := ReportBandwidth{Samples: len(samples)}
	for _, sample := range samples {
		report.BytesPerSecond = max(report.BytesPerSecond, sample.rate)
	}
	if report.BytesPerSecond == 0 {
		return report
	}
	start := 0
	containsPeak := false
	for i, sample := range samples {
		if sample.rate < 0.95*report.BytesPerSecond {
			start, containsPeak = i+1, false
			continue
		}
		if i > 0 && sample.minute != samples[i-1].minute+1 {
			start, containsPeak = i, false
		}
		containsPeak = containsPeak || sample.rate == report.BytesPerSecond
		if containsPeak {
			report.Minutes = max(report.Minutes, i-start+1)
		}
	}
	return report
}

type bandwidthWindow struct {
	UUID string `json:"uuid"`
	From int64  `json:"from"`
	To   int64  `json:"to"`
}

func (s *sqliteDB) dailyReportBandwidth(ctx context.Context, rates []bandwidthWindow) (ReportBandwidth, error) {
	if len(rates) == 0 {
		return ReportBandwidth{}, nil
	}
	raw, err := json.Marshal(rates)
	if err != nil {
		return ReportBandwidth{}, err
	}
	rows, err := s.reader().QueryContext(ctx, `SELECT minute,TOTAL(rate) FROM (
		SELECT h.timestamp/60 AS minute,MAX(h.network_rate) AS rate FROM json_each(?) AS w
		JOIN resource_history AS h ON h.node_uuid=json_extract(w.value,'$.uuid')
			AND h.timestamp>=json_extract(w.value,'$.from') AND h.timestamp<json_extract(w.value,'$.to')
		WHERE h.network_rate IS NOT NULL GROUP BY minute,h.node_uuid
		) GROUP BY minute ORDER BY minute`, string(raw))
	return readReportPeak(rows, err)
}

func (s *Store) LoadDailyTelegramContext(ctx context.Context, date string) ([]string, int, error) {
	var data string
	var sent int
	err := s.sdb.reader().QueryRowContext(ctx, `SELECT messages_json,sent_parts FROM telegram_daily_reports WHERE date=?`, date).Scan(&data, &sent)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, 0, nil
	}
	if err != nil {
		return nil, 0, err
	}
	var parts []string
	err = json.Unmarshal([]byte(data), &parts)
	return parts, sent, err
}

func (s *Store) QueueDailyTelegramContext(ctx context.Context, date string, parts []string) error {
	data, err := json.Marshal(parts)
	if err != nil {
		return err
	}
	_, err = s.sdb.db.ExecContext(ctx, `INSERT INTO telegram_daily_reports(date,messages_json) VALUES(?,?) ON CONFLICT DO NOTHING`, date, string(data))
	return err
}

func (s *Store) MarkDailyTelegramPartContext(ctx context.Context, date string, sent int) error {
	_, err := s.sdb.db.ExecContext(ctx, `UPDATE telegram_daily_reports SET sent_parts=? WHERE date=? AND sent_parts<?`, sent, date, sent)
	return err
}
