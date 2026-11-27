package store

import (
	"strings"
	"time"
)

const reportDateLayout = "2006-01-02"
const reportCounterRetentionDays = 7

// These counters are saved with the node and its traffic baseline. The separate
// daily report table keeps completed summaries beyond the live counter window.
type dailyReportHistory struct {
	Since          time.Time
	OnlineUntil    time.Time
	LocalSince     time.Time
	LocalZone      string
	Days           map[string]reportDayCounters
	lastPrunedDate string
}

type reportDayCounters struct {
	Upload, Download int64
	TrafficObserved  bool
	Drops            int
	Offline          time.Duration
	CycleUsed        int64
	CycleLimit       int64
	QuotaObserved    bool
}

func reportDayKey(zone, date string) string {
	return zone + "|" + date
}

func (n *Node) reportLocations() []*time.Location {
	local := n.billingLocation()
	if local == trafficLocation {
		return []*time.Location{trafficLocation}
	}
	return []*time.Location{trafficLocation, local}
}

func (n *Node) recordReportLiveness(now time.Time) {
	h := n.DailyReportHistory
	if h == nil {
		h = &dailyReportHistory{Since: now, Days: make(map[string]reportDayCounters)}
		n.DailyReportHistory = h
	}
	localZone := n.billingLocation().String()
	if h.LocalZone != localZone {
		h.LocalZone, h.LocalSince = localZone, now
	}
	if !h.OnlineUntil.IsZero() && h.OnlineUntil.Before(now) {
		for _, location := range n.reportLocations() {
			start := maxReportTime(h.OnlineUntil, h.coverageStart(location))
			h.addOutage(location, start, now, start.Equal(h.OnlineUntil))
		}
	}
	h.OnlineUntil = maxReportTime(h.OnlineUntil, now.Add(n.offlineThreshold()))

	// Use a UTC date only for the retention cutoff, with a one-day buffer
	// for all the configured offsets.
	today := now.UTC().Format(reportDateLayout)
	if h.lastPrunedDate == today {
		return
	}
	h.lastPrunedDate = today
	cutoff := now.UTC().AddDate(0, 0, -reportCounterRetentionDays-1).Format(reportDateLayout)
	for key := range h.Days {
		_, date, _ := strings.Cut(key, "|")
		if date < cutoff {
			delete(h.Days, key)
		}
	}
}

func (h *dailyReportHistory) coverageStart(location *time.Location) time.Time {
	if location == trafficLocation {
		return h.Since
	}
	return h.LocalSince
}

func (h *dailyReportHistory) addOutage(location *time.Location, start, end time.Time, countStart bool) {
	cutoff := localDayStart(civilDate(end.In(location)).AddDate(0, 0, -reportCounterRetentionDays), location)
	splitByLocalDay(end, maxReportTime(start, cutoff), location, func(date string, from, to time.Time) {
		key := reportDayKey(location.String(), date)
		day := h.Days[key]
		if countStart && from.Equal(start) {
			day.Drops++
		}
		day.Offline += to.Sub(from)
		h.Days[key] = day
	})
}

func (n *Node) recordReportTraffic(up, down int64, previous, current time.Time) {
	if up < 0 || down < 0 || previous.IsZero() || !current.After(previous) {
		return
	}
	h := n.DailyReportHistory
	for _, location := range n.reportLocations() {
		cutoff := localDayStart(civilDate(current.In(location)).AddDate(0, 0, -reportCounterRetentionDays), location)
		start := maxReportTime(maxReportTime(previous, cutoff), h.coverageStart(location))
		splitByLocalDay(current, start, location, func(date string, from, to time.Time) {
			key := reportDayKey(location.String(), date)
			day := h.Days[key]
			day.Upload += trafficPortion(up, previous, current, to) - trafficPortion(up, previous, current, from)
			day.Download += trafficPortion(down, previous, current, to) - trafficPortion(down, previous, current, from)
			day.TrafficObserved = true
			h.Days[key] = day
		})
	}
}

func (n *Node) recordReportQuota(at time.Time) {
	h := n.DailyReportHistory
	for _, location := range n.reportLocations() {
		key := reportDayKey(location.String(), at.In(location).Format(reportDateLayout))
		day := h.Days[key]
		day.CycleUsed = n.InitialUsed + n.CurrentCycleUsed
		day.CycleLimit = n.TrafficLimit
		day.QuotaObserved = n.TrafficLimit > 0
		h.Days[key] = day
	}
}

func maxReportTime(a, b time.Time) time.Time {
	if a.Before(b) {
		return b
	}
	return a
}
