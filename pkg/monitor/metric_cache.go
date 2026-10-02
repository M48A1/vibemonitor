package monitor

import (
	"sync"
	"time"
)

const slowMetricInterval = 5 * time.Second

// metricCache keeps expensive, slowly changing values out of the one-second
// CPU/network/rate path. Failures retain the last good value and retry after one
// second; an initial failure never starts the normal five-second cache window.
type metricCache[T any] struct {
	mu          sync.Mutex
	value       T
	lastSuccess time.Time
	lastAttempt time.Time
}

func (c *metricCache[T]) getAt(now time.Time, read func() (T, error)) T {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.lastSuccess.IsZero() && now.Sub(c.lastSuccess) < slowMetricInterval && !now.Before(c.lastSuccess) {
		return c.value
	}
	if !c.lastAttempt.IsZero() && now.Sub(c.lastAttempt) < time.Second && !now.Before(c.lastAttempt) {
		return c.value
	}
	c.lastAttempt = now
	if value, err := read(); err == nil {
		c.value = value
		c.lastSuccess = now
	}
	return c.value
}
