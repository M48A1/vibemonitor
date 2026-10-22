package monitor

import "testing"

func TestMemAvailableZeroIsExhaustedMemory(t *testing.T) {
	base := "MemTotal: 100 kB\nMemFree: 10 kB\nBuffers: 20 kB\nCached: 30 kB\nSwapTotal: 50 kB\nSwapFree: 20 kB\n"
	for _, test := range []struct {
		name, available string
		used            int64
	}{
		{"exhausted", "MemAvailable: 0 kB\n", 100 * 1024},
		{"available", "MemAvailable: 25 kB\n", 75 * 1024},
		{"older kernel", "", 40 * 1024},
	} {
		t.Run(test.name, func(t *testing.T) {
			total, used, swapTotal, swapUsed, err := parseMemInfo([]byte(base + test.available))
			if err != nil || total != 100*1024 || used != test.used || swapTotal != 50*1024 || swapUsed != 30*1024 {
				t.Fatalf("memory=%d/%d swap=%d/%d, err=%v", used, total, swapUsed, swapTotal, err)
			}
		})
	}
}
