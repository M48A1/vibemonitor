package server

import (
	"sync"
	"time"
)

// Pacing for the ingestion RPCs.
//
// A node token is a bearer credential: anything holding one writes to the store
// at whatever rate it likes. A looping agent, or a token copied off a node,
// would fill the disk with samples and spend the CPU on history writes, so each
// token gets a floor between two calls of the same method. The floors sit far
// below a healthy agent's own cadence; only a loop hits them.
const (
	// maxPacedTokens bounds memory: one entry per reporting node, plus what
	// deleted tokens leave behind until the TTL expires.
	maxPacedTokens = 4096
	// pacedEntryTTL is how long an idle token keeps its slot.
	pacedEntryTTL = 10 * time.Minute

	// minReportSpacing is the floor between two reports from one token: twenty
	// writes a second, far more than any monitoring interval needs, and bound
	// enough that a loop cannot fill the disk.
	minReportSpacing = 50 * time.Millisecond
	// maxReportSpacing caps what a declared interval may ask for. A node that
	// reports hourly may reconnect twice in a minute, and that is not abuse.
	maxReportSpacing = 30 * time.Second
	// basicInfoSpacing is generous: the agent sends this every fifteen minutes,
	// and once after a failure, never in a loop of its own.
	basicInfoSpacing = time.Second
)

// rpcLimiter keeps the time of the last accepted call per token and method. The
// zero value is ready to use.
type rpcLimiter struct {
	mu   sync.Mutex
	last map[string]time.Time
}

// allow records a call from token for method and reports whether it arrived at
// least spacing after the previous one.
func (l *rpcLimiter) allow(method, token string, now time.Time, spacing time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.last == nil {
		l.last = make(map[string]time.Time)
	}
	key := method + "\x00" + token
	previous, seen := l.last[key]
	if seen && now.Sub(previous) < spacing {
		// The older timestamp stays: a burst of rejected calls must not push the
		// next accepted one further out than the spacing itself.
		return false
	}
	if !seen && len(l.last) >= maxPacedTokens {
		l.pruneExpiredLocked(now)
		if len(l.last) >= maxPacedTokens {
			l.dropOldestLocked()
		}
	}
	l.last[key] = now
	return true
}

func (l *rpcLimiter) pruneExpired() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.pruneExpiredLocked(time.Now())
}

func (l *rpcLimiter) pruneExpiredLocked(now time.Time) {
	for key, last := range l.last {
		if now.Sub(last) >= pacedEntryTTL {
			delete(l.last, key)
		}
	}
}

// dropOldestLocked makes room for one more token once the map is full and
// nothing has expired, so a flood of distinct tokens cannot lock everyone out.
func (l *rpcLimiter) dropOldestLocked() {
	var oldestKey string
	var oldest time.Time
	for key, last := range l.last {
		if oldestKey == "" || last.Before(oldest) {
			oldestKey, oldest = key, last
		}
	}
	delete(l.last, oldestKey)
}

// reportSpacing turns the interval a node declares into the shortest gap the
// server accepts between two of its reports. Half of it absorbs ticker and
// network jitter, so a healthy agent never reaches the floor, while an interval
// of zero or a fraction of a second cannot buy a faster write rate.
func reportSpacing(intervalSeconds float64) time.Duration {
	spacing := minReportSpacing
	// The upper bound only keeps the conversion inside time.Duration; anything
	// that large is capped below anyway.
	if intervalSeconds > 0 && intervalSeconds < 1e9 {
		if half := time.Duration(intervalSeconds * float64(time.Second) / 2); half > spacing {
			spacing = half
		}
	}
	if spacing > maxReportSpacing {
		spacing = maxReportSpacing
	}
	return spacing
}
