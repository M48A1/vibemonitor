package store

import (
	"errors"
	"math/bits"
	"time"
)

const trafficDailyRetentionDays = 180

var trafficLocation = time.FixedZone("UTC+8", 8*3600)

// civilDate gives calendar arithmetic a date without DST transitions.
func civilDate(at time.Time) time.Time {
	return time.Date(at.Year(), at.Month(), at.Day(), 0, 0, 0, 0, time.UTC)
}

// localDayStart returns the first instant of the date, even when a clock
// transition repeats or skips midnight. A skipped date starts at the next day.
func localDayStart(date time.Time, location *time.Location) time.Time {
	for at := date.Add(-26 * time.Hour).In(location); ; {
		_, offset := at.Zone()
		candidate := date.Add(-time.Duration(offset) * time.Second).In(location)
		_, zoneEnd := at.ZoneBounds()
		if candidate.Before(at) {
			return at
		}
		if zoneEnd.IsZero() || candidate.Before(zoneEnd) {
			return candidate
		}
		at = zoneEnd
	}
}

func nextLocalDay(day time.Time) time.Time {
	return localDayStart(civilDate(day).AddDate(0, 0, 1), day.Location())
}

// Split cumulative counters by elapsed time. Differences of cumulative
// allocations preserve every byte even across midnight and short months.
func distributeDaily(daily map[string]int64, value int64, previous, current, start time.Time, location *time.Location) {
	total := uint64(current.Sub(previous))
	portion := func(at time.Time) int64 {
		hi, lo := bits.Mul64(uint64(value), uint64(at.Sub(previous)))
		q, _ := bits.Div64(hi, lo, total)
		return int64(q)
	}
	for start = start.In(location); start.Before(current); {
		end := nextLocalDay(start)
		if end.After(current) {
			end = current.In(location)
		}
		daily[start.Format("2006-01-02")] += portion(end) - portion(start)
		start = end
	}
}

// Reports persist these totals with the associated counter baseline.
func recordDailyTraffic(daily *map[string]int64, delta int64, previous, current time.Time, location *time.Location) {
	if delta < 0 || previous.IsZero() || !current.After(previous) {
		return
	}
	if *daily == nil {
		*daily = make(map[string]int64)
	}
	date := civilDate(current.In(location)).AddDate(0, 0, -trafficDailyRetentionDays)
	cutoff := localDayStart(date, location)
	cutoffKey := cutoff.Format("2006-01-02")
	for day := range *daily {
		if day < cutoffKey {
			delete(*daily, day)
		}
	}
	start := previous
	if start.Before(cutoff) {
		start = cutoff
	}
	distributeDaily(*daily, delta, previous, current, start, location)
}

// Old history contains day totals only, so split each day's bytes across the
// overlapping new dates in proportion to elapsed time.
func redistributeDaily(daily map[string]int64, from, to *time.Location, through time.Time) map[string]int64 {
	if len(daily) == 0 {
		return nil
	}
	result := make(map[string]int64, len(daily)+1)
	for key, value := range daily {
		date, err := time.Parse("2006-01-02", key)
		if err != nil || value < 0 {
			continue
		}
		start := localDayStart(date, from)
		end := nextLocalDay(start)
		if end.After(through) && through.After(start) {
			end = through
		}
		distributeDaily(result, value, start, end, start, to)
	}
	return result
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

func trafficHistoryRange(resetDay int, name string, now time.Time, location *time.Location) (time.Time, time.Time, error) {
	start, end := billingCycleRange(resetDay, now, location)
	switch name {
	case "current":
	case "previous":
		end = start
		start, _ = billingCycleRange(resetDay, start.Add(-time.Nanosecond), location)
	case "three":
		for i := 0; i < 2; i++ {
			start, _ = billingCycleRange(resetDay, start.Add(-time.Nanosecond), location)
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
	s.mu.RLock()
	defer s.mu.RUnlock()
	n := s.nodes[uuid]
	if n == nil {
		return nil, ErrNodeNotFound
	}
	start, end, err := trafficHistoryRange(n.ResetDay, name, now, n.billingLocation())
	if err != nil {
		return nil, err
	}
	response := &TrafficHistoryResponse{UUID: uuid, Range: name, StartDate: start.Format("2006-01-02"), EndDate: end.Add(-time.Nanosecond).Format("2006-01-02"), Timezone: billingTimezoneName(n.BillingTimezone), ResetDay: n.ResetDay, Days: []DailyTrafficPoint{}}
	for day := start; day.Before(end); day = nextLocalDay(day) {
		point := DailyTrafficPoint{Date: day.Format("2006-01-02"), Complete: !nextLocalDay(day).After(now)}
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
	location := n.billingLocation()
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
		local := start.In(location)
		boundary := nextLocalDay(local)
		if boundary.After(end) {
			boundary = end
		}
		n.DailyOnline[local.Format("2006-01-02")] += int64(boundary.Sub(start))
		start = boundary
	}
	n.DailyOnlineUntil = end
	cutoff := civilDate(now.In(location)).AddDate(0, 0, -trafficDailyRetentionDays).Format("2006-01-02")
	for date := range n.DailyOnline {
		if date < cutoff {
			delete(n.DailyOnline, date)
		}
	}
}

// Move the known heartbeat tail separately so history can still subtract
// future grace time from the correct local dates.
func (n *Node) rezoneDailyOnline(from, to *time.Location, now time.Time) {
	if len(n.DailyOnline) == 0 {
		return
	}
	until := n.DailyOnlineUntil
	if until.After(now) {
		tail := make(map[string]int64)
		distributeDaily(tail, int64(until.Sub(now)), now, until, now, from)
		for day, value := range tail {
			n.DailyOnline[day] = max(0, n.DailyOnline[day]-value)
		}
	}
	n.DailyOnline = redistributeDaily(n.DailyOnline, from, to, now)
	if until.After(now) {
		distributeDaily(n.DailyOnline, int64(until.Sub(now)), now, until, now, to)
	}
}

func (n *Node) dailyOnlineStatus(day, now time.Time) (string, int64) {
	if day.After(now) {
		return "future", 0
	}
	end := nextLocalDay(day)
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
		if tailEnd.After(nextLocalDay(day)) {
			tailEnd = nextLocalDay(day)
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
