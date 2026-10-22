package monitor

import (
	"testing"
	"time"
	"vibemonitor/pkg/protocol"
)

func TestReleaseInterfaceSpeedAccuracy(t *testing.T) {
	var tracker NetTracker
	start := time.Now()
	tracker.CalculateInterfaceSpeedAt(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 1000, Down: 5000},
	}, start)
	up, down := tracker.CalculateInterfaceSpeedAt(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 2500, Down: 9500},
	}, start.Add(1500*time.Millisecond))
	if up != 1000 || down != 3000 {
		t.Fatalf("rates = %d/%d, want 1000/3000 B/s", up, down)
	}
	// Duplicate/out-of-order snapshots must not replace the valid baseline.
	tracker.CalculateInterfaceSpeedAt(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 999999, Down: 999999},
	}, start)
	up, down = tracker.CalculateInterfaceSpeedAt(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 3000, Down: 100},      // receive counter reset
		"eth1": {Up: 999999, Down: 999999}, // newly discovered interface
	}, start.Add(2*time.Second))
	if up != 1000 || down != 0 {
		t.Fatalf("reset/new interface rates = %d/%d, want 1000/0", up, down)
	}
}
