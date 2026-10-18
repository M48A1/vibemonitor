package store

import (
	"errors"
	"math/bits"
	"time"
)

const trafficDailyRetentionDays = 180

var trafficLocation = time.FixedZone("UTC+8", 8*3600)

// Daily totals travel with the node snapshot and counter baseline, so a retry,
// restart, backup or restore cannot apply a delta twice.
func (n *Node) recordDailyTraffic(delta int64, previous, current time.Time) {
	location := trafficLocation
	if delta < 0 || previous.IsZero() || !current.After(previous) {
		return
	}
	if n.DailyTraffic == nil {
		n.DailyTraffic = make(map[string]int64)
	}
	local := current.In(location)
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, location).AddDate(0, 0, -trafficDailyRetentionDays)
	cutoffKey := cutoff.Format("2006-01-02")
	for date := range n.DailyTraffic {
		if date < cutoffKey {
			delete(n.DailyTraffic, date)
		}
	}
	start := previous.In(location)
	if start.Before(cutoff) {
		start = cutoff
	}
	total := uint64(current.Sub(previous))
	portion := func(at time.Time) int64 {
		hi, lo := bits.Mul64(uint64(delta), uint64(at.Sub(previous)))
		q, _ := bits.Div64(hi, lo, total)
		return int64(q)
	}
	// Split cumulative counters by elapsed time; differences of cumulative
	// allocations preserve every byte even across midnight and short months.
	for start.Before(current) {
		day := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, location)
		end := day.AddDate(0, 0, 1)
		if end.After(current) {
			end = current.In(location)
		}
		n.DailyTraffic[day.Format("2006-01-02")] += portion(end) - portion(start)
		start = end
	}
}

type DailyTrafficPoint struct {
	Date          string `json:"date"`
	Bytes         int64  `json:"bytes"`
	Status        string `json:"status"`
	Complete      bool   `json:"complete"`
	OnlineSeconds int64  `json:"online_seconds"`
}
type TrafficHistoryResponse struct {
	UUID      string              `json:"uuid"`
	Range     string              `json:"range"`
	StartDate string              `json:"start_date"`
	EndDate   string              `json:"end_date"` // Inclusive display date; billing ranges are half-open.
	Timezone  string              `json:"timezone"`
	ResetDay  int                 `json:"reset_day"`
	Total     int64               `json:"total"`
	Days      []DailyTrafficPoint `json:"days"`
}

func trafficHistoryRange(resetDay int, name string, now time.Time) (time.Time, time.Time, error) {
	now = now.In(trafficLocation)
	start, end := GetBillingCycleRange(resetDay, now)
	switch name {
	case "current":
	case "previous":
		end = start
		start, _ = GetBillingCycleRange(resetDay, start.Add(-time.Nanosecond))
	case "three":
		for i := 0; i < 2; i++ {
			start, _ = GetBillingCycleRange(resetDay, start.Add(-time.Nanosecond))
		}
	default:
		return time.Time{}, time.Time{}, errors.New("invalid traffic range")
	}
	return start, end, nil
}

func (s *Store) GetTrafficHistory(uuid, name string) (*TrafficHistoryResponse, error) {
	return s.trafficHistoryAt(uuid, name, time.Now())
}
func (s *Store) trafficHistoryAt(uuid, name string, now time.Time) (*TrafficHistoryResponse, error) {
	now = now.In(trafficLocation)
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[uuid]
	if n == nil {
		return nil, ErrNodeNotFound
	}
	start, end, err := trafficHistoryRange(n.ResetDay, name, now)
	if err != nil {
		return nil, err
	}
	response := &TrafficHistoryResponse{UUID: uuid, Range: name, StartDate: start.Format("2006-01-02"), EndDate: end.AddDate(0, 0, -1).Format("2006-01-02"), Timezone: now.Format("MST -07:00"), ResetDay: n.ResetDay, Days: []DailyTrafficPoint{}}
	for day := start; day.Before(end); day = day.AddDate(0, 0, 1) {
		point := DailyTrafficPoint{Date: day.Format("2006-01-02"), Complete: !day.AddDate(0, 0, 1).After(now)}
		point.Status, point.OnlineSeconds = n.dailyOnlineStatus(day, now)
		if value, ok := n.DailyTraffic[point.Date]; ok && !day.After(now) {
			point.Bytes = value
			response.Total += value
		}
		response.Days = append(response.Days, point)
	}
	return response, nil
}

// Track the union of heartbeat liveness intervals, using the same timeout as
// the dashboard. Long reporting gaps do not become "online" just because the
// cumulative network counters can later recover traffic over that interval.
func (n *Node) recordDailyOnline(now time.Time) {
	if n.DailyOnline == nil {
		n.DailyOnline = make(map[string]int64)
	}
	if n.OnlineHistorySince.IsZero() {
		n.OnlineHistorySince = now
	}
	start, end := now, now.Add(n.offlineThreshold())
	if n.DailyOnlineUntil.After(start) {
		start = n.DailyOnlineUntil
	}
	if !end.After(start) {
		return
	}
	for start.Before(end) {
		local := start.In(trafficLocation)
		day := time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, trafficLocation)
		boundary := day.AddDate(0, 0, 1)
		if boundary.After(end) {
			boundary = end
		}
		n.DailyOnline[day.Format("2006-01-02")] += int64(boundary.Sub(start))
		start = boundary
	}
	n.DailyOnlineUntil = end
	cutoff := now.In(trafficLocation).AddDate(0, 0, -trafficDailyRetentionDays).Format("2006-01-02")
	for date := range n.DailyOnline {
		if date < cutoff {
			delete(n.DailyOnline, date)
		}
	}
}

func (n *Node) dailyOnlineStatus(day, now time.Time) (string, int64) {
	if day.After(now) {
		return "future", 0
	}
	end := day.AddDate(0, 0, 1)
	if end.After(now) {
		end = now
	}
	online := time.Duration(n.DailyOnline[day.Format("2006-01-02")])
	// Stored heartbeat grace may extend beyond now; do not count future uptime.
	if n.DailyOnlineUntil.After(now) {
		tailStart, tailEnd := now, n.DailyOnlineUntil
		if tailStart.Before(day) {
			tailStart = day
		}
		if tailEnd.After(day.AddDate(0, 0, 1)) {
			tailEnd = day.AddDate(0, 0, 1)
		}
		if tailEnd.After(tailStart) {
			online -= tailEnd.Sub(tailStart)
		}
	}
	online = max(0, min(online, end.Sub(day)))
	if n.OnlineHistorySince.IsZero() || n.OnlineHistorySince.After(day) {
		return "unknown", int64(online / time.Second)
	}
	if online == 0 {
		return "offline", 0
	}
	if online >= end.Sub(day) {
		return "online", int64(online / time.Second)
	}
	return "partial", int64(online / time.Second)
}
