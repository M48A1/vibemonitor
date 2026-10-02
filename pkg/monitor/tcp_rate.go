package monitor

import (
	"bufio"
	"bytes"
	"fmt"
	"strconv"
	"strings"
	"sync"
	"time"
)

// parseTCPConnectionCounters reads the cumulative TCP connection attempt
// counters from the paired header/value lines in /proc/net/snmp.
func parseTCPConnectionCounters(data []byte) (active, passive uint64, err error) {
	var headers []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) == 0 || fields[0] != "Tcp:" {
			continue
		}
		if headers == nil {
			if len(fields) < 2 {
				return 0, 0, fmt.Errorf("empty TCP SNMP header")
			}
			headers = fields[1:]
			continue
		}
		if len(fields) != len(headers)+1 {
			return 0, 0, fmt.Errorf("TCP SNMP header/value count mismatch")
		}
		var foundActive, foundPassive bool
		for i, name := range headers {
			switch name {
			case "ActiveOpens":
				active, err = strconv.ParseUint(fields[i+1], 10, 64)
				foundActive = true
			case "PassiveOpens":
				passive, err = strconv.ParseUint(fields[i+1], 10, 64)
				foundPassive = true
			default:
				continue
			}
			if err != nil {
				return 0, 0, fmt.Errorf("invalid %s counter: %w", name, err)
			}
		}
		if !foundActive || !foundPassive {
			return 0, 0, fmt.Errorf("TCP SNMP connection counters missing")
		}
		return active, passive, nil
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, err
	}
	return 0, 0, fmt.Errorf("TCP SNMP header/value lines missing")
}

// tcpConnectionRateTracker converts cumulative TCP opens into new connection
// attempts per second. A pointer distinguishes a measured zero from no data.
type tcpConnectionRateTracker struct {
	mu          sync.Mutex
	lastActive  uint64
	lastPassive uint64
	lastTime    time.Time
	initialized bool
}

func (t *tcpConnectionRateTracker) CalculateRateAt(active, passive uint64, now time.Time) *float64 {
	t.mu.Lock()
	defer t.mu.Unlock()

	if now.IsZero() || (t.initialized && !now.After(t.lastTime)) {
		return nil
	}
	if !t.initialized || active < t.lastActive || passive < t.lastPassive {
		t.lastActive, t.lastPassive, t.lastTime, t.initialized = active, passive, now, true
		return nil
	}

	rate := (float64(active-t.lastActive) + float64(passive-t.lastPassive)) / now.Sub(t.lastTime).Seconds()
	t.lastActive, t.lastPassive, t.lastTime = active, passive, now
	return &rate
}

func (t *tcpConnectionRateTracker) Reset() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.initialized = false
	t.lastTime = time.Time{}
}
