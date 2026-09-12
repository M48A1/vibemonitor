package monitor

import (
	"bufio"
	"bytes"
	"fmt"
	"sort"
	"strconv"
	"strings"
)

func defaultTrafficInterface(name string) bool {
	if name == "lo" {
		return false
	}
	for _, prefix := range []string{"docker", "veth", "br-", "virbr", "tun", "tap", "wg", "tailscale", "zt", "ip6tnl", "sit", "gre", "gretap", "ip6gre", "erspan", "vxlan", "dummy"} {
		if strings.HasPrefix(name, prefix) {
			return false
		}
	}
	return true
}

func parseNetworkCounters(data []byte, include func(string) bool) (down, up int64, source string, err error) {
	var names []string
	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		parts := strings.SplitN(scanner.Text(), ":", 2)
		if len(parts) != 2 {
			continue
		}
		name := strings.TrimSpace(parts[0])
		if !include(name) {
			continue
		}
		fields := strings.Fields(parts[1])
		if len(fields) < 16 {
			return 0, 0, "", fmt.Errorf("invalid counters for %s", name)
		}
		rx, e1 := strconv.ParseInt(fields[0], 10, 64)
		tx, e2 := strconv.ParseInt(fields[8], 10, 64)
		if e1 != nil || e2 != nil || rx < 0 || tx < 0 {
			return 0, 0, "", fmt.Errorf("invalid counters for %s", name)
		}
		down += rx
		up += tx
		names = append(names, name)
	}
	if err := scanner.Err(); err != nil {
		return 0, 0, "", err
	}
	if len(names) == 0 {
		return 0, 0, "", fmt.Errorf("no matching traffic interfaces")
	}
	sort.Strings(names)
	return down, up, "interfaces-v1:" + strings.Join(names, ","), nil
}
