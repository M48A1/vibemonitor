package monitor

import "testing"

func TestCPUStatsExcludeGuestAccounting(t *testing.T) {
	var tracker CPUTracker
	total, idle, err := parseCPUStats([]byte("cpu  100 0 0 100 0 0 0 0 100 0\ncpu0 100 0 0 100 0 0 0 0 100 0\n"))
	if err != nil || total != 200 || idle != 100 {
		t.Fatalf("first counters = %d/%d, %v", total, idle, err)
	}
	tracker.CalculateUsage(total, idle)
	total, idle, err = parseCPUStats([]byte("cpu  150 0 0 150 0 0 0 0 150 0\n"))
	if err != nil {
		t.Fatal(err)
	}
	if usage := tracker.CalculateUsage(total, idle); usage != 50 {
		t.Fatalf("guest time counted twice: usage = %v, want 50", usage)
	}
}

func TestCPUStatsFormats(t *testing.T) {
	for _, test := range []struct {
		data        string
		total, idle uint64
	}{
		{"intr 1\ncpu\t10 20 30 40 5 6 7 8 999 999\n", 126, 45},
		{"cpu 10 20 30 40", 100, 40},
	} {
		total, idle, err := parseCPUStats([]byte(test.data))
		if err != nil || total != test.total || idle != test.idle {
			t.Fatalf("parse %q = %d/%d, %v", test.data, total, idle, err)
		}
	}
	for _, data := range []string{"cpu0 1 2 3 4", "cpu 1 2 3", "cpu 1 bad 3 4", "cpu 18446744073709551615 1 0 0"} {
		if _, _, err := parseCPUStats([]byte(data)); err == nil {
			t.Fatalf("accepted invalid stats %q", data)
		}
	}
}
