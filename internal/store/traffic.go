package store

import (
	"errors"
	"fmt"
	"math"
	"time"

	"vibemonitor/pkg/protocol"
)

var ErrInvalidCycleUsage = errors.New("invalid current cycle usage")
var ErrInvalidNodeOptions = errors.New("invalid node options")

// Internal updates use exactly -1 to preserve an existing value. HTTP handlers
// reject negative inputs before translating omitted fields to this sentinel.
func validateTrafficOptions(opts NodeOptions, updating bool) error {
	if !(updating && opts.ResetDay == -1) && (opts.ResetDay < 0 || opts.ResetDay > 31) {
		return fmt.Errorf("%w: reset day must be between 1 and 31 (0 defaults to 1)", ErrInvalidNodeOptions)
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

// A different cycle start cannot retain usage or manual offsets belonging to
// the previous range. Daily history contains measured traffic only.
func (n *Node) changeResetDay(resetDay int, now time.Time) {
	start, _ := GetBillingCycleRange(resetDay, now)
	n.ResetDay = resetDay
	if n.CycleStart.Equal(start) {
		return
	}
	n.CycleStart = start
	n.CurrentCycleUsed = 0
	n.InitialUsed = 0
	n.TrafficManualAt = nil
	first, last := start.Format("2006-01-02"), now.In(trafficLocation).Format("2006-01-02")
	for day, measured := range n.DailyTraffic {
		if day >= first && day <= last {
			n.CurrentCycleUsed += measured
		}
	}
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
