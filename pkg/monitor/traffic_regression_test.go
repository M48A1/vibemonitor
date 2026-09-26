package monitor

import (
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestDetailedCountersKeepInterfacesSeparate(t *testing.T) {
	input := []byte("eth0: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\neth1: 300 0 0 0 0 0 0 0 400 0 0 0 0 0 0 0\n")
	down, up, source, counters, err := parseNetworkCountersDetailed(input, defaultTrafficInterface)
	if err != nil || down != 400 || up != 600 || source != "interfaces-v1:eth0,eth1" {
		t.Fatalf("aggregate: %d %d %q %v", down, up, source, err)
	}
	if counters["eth0"] != (protocol.InterfaceCounters{Up: 200, Down: 100}) || counters["eth1"] != (protocol.InterfaceCounters{Up: 400, Down: 300}) {
		t.Fatalf("interface counters: %+v", counters)
	}
}

func TestInterfaceSpeedSurvivesOneCounterReset(t *testing.T) {
	var tracker NetTracker
	tracker.CalculateInterfaceSpeed(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 1000}, "eth1": {Up: 5000},
	})
	time.Sleep(10 * time.Millisecond)
	up, _ := tracker.CalculateInterfaceSpeed(map[string]protocol.InterfaceCounters{
		"eth0": {Up: 2000}, "eth1": {Up: 0},
	})
	if up <= 0 {
		t.Fatalf("healthy interface traffic was lost after another reset: %d", up)
	}
}
