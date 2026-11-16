package store

import (
	"errors"
	"fmt"
	"maps"
	"math"
	"strings"
	"sync"
	"time"
	_ "time/tzdata"

	"vibemonitor/pkg/protocol"
)

var ErrInvalidCycleUsage = errors.New("invalid current cycle usage")
var ErrInvalidNodeOptions = errors.New("invalid node options")

const defaultBillingTimezone = "UTC+8"

var billingLocations sync.Map

func billingTimezoneName(name string) string {
	if name = strings.TrimSpace(name); name == "" {
		return defaultBillingTimezone
	}
	return name
}

func optionBillingTimezone(value *string) string {
	if value == nil {
		return defaultBillingTimezone
	}
	return billingTimezoneName(*value)
}

func loadBillingLocation(name string) (*time.Location, error) {
	if name == defaultBillingTimezone {
		return trafficLocation, nil
	}
	if name == "Local" {
		return nil, fmt.Errorf("Local depends on the server settings")
	}
	if location, ok := billingLocations.Load(name); ok {
		return location.(*time.Location), nil
	}
	location, err := time.LoadLocation(name)
	if err == nil {
		billingLocations.Store(name, location)
	}
	return location, err
}

func (n *Node) billingLocation() *time.Location {
	location, err := loadBillingLocation(billingTimezoneName(n.BillingTimezone))
	if err != nil {
		return trafficLocation
	}
	return location
}

// Internal updates use exactly -1 to preserve an existing value. HTTP handlers
// reject negative inputs before translating omitted fields to this sentinel.
func validateTrafficOptions(opts NodeOptions, updating bool) error {
	if !(updating && opts.ResetDay == -1) && (opts.ResetDay < 0 || opts.ResetDay > 31) {
		return fmt.Errorf("%w: reset day must be between 1 and 31 (0 defaults to 1)", ErrInvalidNodeOptions)
	}
	if opts.BillingTimezone != nil {
		if _, err := loadBillingLocation(optionBillingTimezone(opts.BillingTimezone)); err != nil {
			return fmt.Errorf("%w: billing timezone must be UTC+8 or an IANA name such as Asia/Shanghai", ErrInvalidNodeOptions)
		}
	}
	for _, field := range []struct {
		name  string
		value float64
	}{{"traffic limit", opts.TrafficLimitGB}, {"initial usage", opts.InitialUsedGB}} {
		if updating && field.value == -1 {
			continue
		}
		if _, err := trafficGBToBytes(field.value); err != nil {
			return fmt.Errorf("%w: %s must be nonnegative and less than 8589934592 GiB", ErrInvalidNodeOptions, field.name)
		}
	}
	return nil
}

// Recalculate the cycle from measured traffic. Manual offsets apply to
// the original range, and are cleared when its start changes.
func (n *Node) changeBillingCycle(resetDay int, timezone string, now time.Time) {
	oldLocation := n.billingLocation()
	n.ResetDay = resetDay
	n.BillingTimezone = timezone
	location := n.billingLocation()
	// Keep existing daily totals under their original date labels. Only
	// subsequent reports use the new zone for daily aggregation.
	if oldLocation == trafficLocation && location != trafficLocation && n.RankingDailyTraffic == nil {
		n.RankingDailyTraffic = maps.Clone(n.DailyTraffic)
	}
	start, _ := n.billingCycleRange(now)
	if n.CycleStart.Equal(start) {
		return
	}
	n.InitialUsed = 0
	n.TrafficManualAt = nil
	n.CycleStart = start
	n.CurrentCycleUsed = sumDailyTraffic(n.DailyTraffic, start.Format("2006-01-02"), now.In(location).Format("2006-01-02"))
}

func sumDailyTraffic(daily map[string]int64, first, last string) int64 {
	var total int64
	for day, measured := range daily {
		if day >= first && day <= last {
			total += measured
		}
	}
	return total
}

func trafficGBToBytes(gb float64) (int64, error) {
	const gib = float64(1024 * 1024 * 1024)
	if math.IsNaN(gb) || math.IsInf(gb, 0) || gb < 0 || gb >= float64(math.MaxInt64)/gib {
		return 0, ErrInvalidCycleUsage
	}
	return int64(gb * gib), nil
}

func networkCounterDelta(current, previous int64) int64 {
	if current >= previous {
		return current - previous
	}
	// A reset loses one sampling interval; charging the new raw value can
	// recount traffic from an interface that was recreated with existing bytes.
	return 0
}

func trafficSampleTime(report *protocol.Report, received time.Time) time.Time {
	if report == nil || report.UpdatedAt.IsZero() {
		return received
	}
	// Use the probe's sample time when it is close to receipt time. Otherwise
	// prefer the server clock so a skewed probe cannot move traffic between cycles.
	lag := received.Sub(report.UpdatedAt)
	if lag < -2*time.Minute || lag > 2*time.Minute {
		return received
	}
	return report.UpdatedAt
}

func currentCycleDelta(delta int64, cycleStart, previousAt, currentAt time.Time) int64 {
	if delta <= 0 {
		return 0
	}
	if previousAt.IsZero() || currentAt.IsZero() || !currentAt.After(previousAt) {
		return delta
	}
	if !currentAt.After(cycleStart) {
		return 0
	}
	if !previousAt.Before(cycleStart) {
		return delta
	}
	// Counters are sampled, so traffic inside the interval cannot be split
	// exactly. Estimate the portion after the billing boundary by elapsed time.
	part := float64(currentAt.Sub(cycleStart)) / float64(currentAt.Sub(previousAt))
	return int64(math.Round(float64(delta) * part))
}
