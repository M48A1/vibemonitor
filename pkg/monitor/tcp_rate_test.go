package monitor

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestParseTCPConnectionCounters(t *testing.T) {
	for _, tc := range []struct {
		name    string
		input   string
		active  uint64
		passive uint64
		wantErr bool
	}{
		{
			name:    "counter columns can move",
			input:   "Ip: InReceives\nIp: 12\nTcp: PassiveOpens Other ActiveOpens\nTcp: 42 7 100\n",
			active:  100,
			passive: 42,
		},
		{
			name:    "missing counter",
			input:   "Tcp: ActiveOpens Other\nTcp: 100 7\n",
			wantErr: true,
		},
		{
			name:    "mismatched rows",
			input:   "Tcp: ActiveOpens PassiveOpens\nTcp: 100\n",
			wantErr: true,
		},
		{
			name:    "invalid counter",
			input:   "Tcp: ActiveOpens PassiveOpens\nTcp: -1 42\n",
			wantErr: true,
		},
		{
			name:    "missing values",
			input:   "Tcp: ActiveOpens PassiveOpens\n",
			wantErr: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			active, passive, err := parseTCPConnectionCounters([]byte(tc.input))
			if (err != nil) != tc.wantErr {
				t.Fatalf("parse error = %v, want error %v", err, tc.wantErr)
			}
			if !tc.wantErr && (active != tc.active || passive != tc.passive) {
				t.Fatalf("counters = %d/%d, want %d/%d", active, passive, tc.active, tc.passive)
			}
		})
	}
}

func TestTCPConnectionRateTracker(t *testing.T) {
	var tracker tcpConnectionRateTracker
	start := time.Now()
	if got := tracker.CalculateRateAt(100, 50, start); got != nil {
		t.Fatalf("first sample = %v, want no data", *got)
	}
	if got := tracker.CalculateRateAt(120, 55, start.Add(2500*time.Millisecond)); got == nil || *got != 10 {
		t.Fatalf("rate after 2.5s = %v, want 10 attempts/s", got)
	}
	if got := tracker.CalculateRateAt(120, 55, start.Add(3500*time.Millisecond)); got == nil || *got != 0 {
		t.Fatalf("unchanged counters = %v, want measured zero", got)
	}
	if got := tracker.CalculateRateAt(1000, 1000, start.Add(3500*time.Millisecond)); got != nil {
		t.Fatalf("duplicate timestamp = %v, want no data", *got)
	}
	if got := tracker.CalculateRateAt(122, 57, start.Add(4500*time.Millisecond)); got == nil || *got != 4 {
		t.Fatalf("rate after duplicate = %v, want 4 attempts/s", got)
	}
	if got := tracker.CalculateRateAt(1, 0, start.Add(5500*time.Millisecond)); got != nil {
		t.Fatalf("counter reset = %v, want no data", *got)
	}
	if got := tracker.CalculateRateAt(2, 3, start.Add(6500*time.Millisecond)); got == nil || *got != 4 {
		t.Fatalf("rate after counter reset = %v, want 4 attempts/s", got)
	}
	tracker.Reset()
	if got := tracker.CalculateRateAt(3, 4, start.Add(7500*time.Millisecond)); got != nil {
		t.Fatalf("first sample after read failure = %v, want no data", *got)
	}
}

func TestTCPConnectionRateJSONDistinguishesMissingAndZero(t *testing.T) {
	connections := protocol.ConnectionsReport{TCP: 1, UDP: 2}
	missing, err := json.Marshal(connections)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(missing), "tcp_new_per_sec") {
		t.Fatalf("missing rate must be omitted: %s", missing)
	}
	zero := 0.0
	connections.TCPNewPerSecond = &zero
	measured, err := json.Marshal(connections)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(measured), `"tcp_new_per_sec":0`) {
		t.Fatalf("measured zero rate must be present: %s", measured)
	}
}
