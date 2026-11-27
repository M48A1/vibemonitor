package store

import (
	"context"
	"sort"
	"time"
)

type reportProbeTarget struct {
	name        string
	host        string
	observation PingObservation
}

type reportOutageEdge struct {
	at       int64
	coverage int
	offline  int
}

func (s *sqliteDB) fillReportOutages(ctx context.Context, w *nodeReportWindow, now time.Time) error {
	dayStart, dayEnd := w.start.Unix(), w.end.Unix()
	// Read around both day boundaries so the history's grace period can be
	// resolved before clipping durations and attributing an outage's start.
	start := dayStart - 4*PingSampleIntervalSec
	end := min(now.Unix(), dayEnd+4*PingSampleIntervalSec)
	var edges []reportOutageEdge
	for _, target := range w.targets {
		observation := target.observation
		if observation.Host != target.host || observation.Method == "" {
			observation = PingObservation{}
		}
		history := &PingHistoryResponse{
			UUID: w.summary.UUID, Target: target.name, Host: target.host,
			Method: observation.Method, Range: "24h", StartTime: start, EndTime: end,
		}
		if observation.Host == target.host {
			history.StartTime = max(start, observation.StartedAt)
		}
		if history.StartTime >= end {
			continue
		}
		if err := s.queryPingHistory(ctx, history, observation); err != nil {
			return err
		}
		if history.firstObservedAt == 0 || history.firstObservedAt >= end {
			continue
		}
		coverageStart := max(start, history.firstObservedAt)
		edges = append(edges, reportOutageEdge{at: coverageStart, coverage: 1}, reportOutageEdge{at: end, coverage: -1})
		for _, gap := range history.OfflineIntervals {
			from, to := max(coverageStart, gap.Start), min(end, gap.End)
			if from < to {
				edges = append(edges, reportOutageEdge{at: from, offline: 1}, reportOutageEdge{at: to, offline: -1})
			}
		}
	}
	summarizeReportOutages(&w.summary, edges, dayStart, dayEnd)
	return nil
}

// A node is offline when all targets that have observations at that time
// show a gap. A timeout sample still proves that the probe was reporting.
func summarizeReportOutages(report *NodeDailyReport, edges []reportOutageEdge, start, end int64) {
	sort.Slice(edges, func(i, j int) bool { return edges[i].at < edges[j].at })
	covered, offline := 0, 0
	wasDown := false
	for i := 0; i < len(edges); {
		at := edges[i].at
		if at >= end {
			break
		}
		for i < len(edges) && edges[i].at == at {
			covered += edges[i].coverage
			offline += edges[i].offline
			i++
		}
		if i == len(edges) {
			break
		}
		isDown := covered > 0 && covered == offline
		if isDown && !wasDown && at >= start {
			report.Drops++
		}
		wasDown = isDown
		if covered > 0 {
			seconds := float64(max(0, min(end, edges[i].at)-max(start, at)))
			report.CoverageSeconds += seconds
			if isDown {
				report.OfflineSeconds += seconds
			} else {
				report.OnlineSeconds += seconds
			}
		}
	}
}
