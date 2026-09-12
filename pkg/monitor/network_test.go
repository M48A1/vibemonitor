package monitor

import (
	"fmt"
	"strings"
	"testing"
)

func TestTrafficInterfaceSelection(t *testing.T) {
	var input strings.Builder
	for _, name := range []string{"eth0", "ens3", "lo", "docker0", "veth123", "br-abc", "wg0", "tun0", "tailscale0", "tap0", "virbr0"} {
		fmt.Fprintf(&input, "%s: 100 0 0 0 0 0 0 0 200 0 0 0 0 0 0 0\n", name)
	}
	down, up, source, err := parseNetworkCounters([]byte(input.String()), defaultTrafficInterface)
	if err != nil || down != 200 || up != 400 || source != "interfaces-v1:ens3,eth0" {
		t.Fatalf("auto: %d %d %q %v", down, up, source, err)
	}
	down, up, source, err = parseNetworkCounters([]byte(input.String()), func(name string) bool { return name == "wg0" })
	if err != nil || down != 100 || up != 200 || source != "interfaces-v1:wg0" {
		t.Fatalf("explicit: %d %d %s %v", down, up, source, err)
	}
	if _, _, _, err := parseNetworkCounters([]byte(input.String()), func(string) bool { return false }); err == nil {
		t.Fatal("missing interface must not produce zero counters")
	}
	if _, _, _, err := parseNetworkCounters([]byte("eth0: invalid\n"), defaultTrafficInterface); err == nil {
		t.Fatal("malformed counters accepted")
	}
}
