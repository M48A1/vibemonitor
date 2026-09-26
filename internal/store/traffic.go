package store

import (
	"errors"
	"math"
	"time"

	"vibemonitor/pkg/protocol"
)

var ErrInvalidCycleUsage = errors.New("invalid current cycle usage")

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
