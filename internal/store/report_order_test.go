package store

import (
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestReportOrderingAcrossClockCorrectionAndRestart(t *testing.T) {
	for _, tc := range []struct {
		name   string
		shift  time.Duration
		uptime int64
		boot   string
		accept bool
	}{
		{"clock corrected by one hour", -time.Hour, 1060, "boot-a", true},
		{"small clock correction", -time.Second, 1060, "boot-a", true},
		{"reboot with older clock", -time.Hour, 10, "boot-b", true},
		{"duplicate", 0, 1000, "boot-a", false},
		{"out of order", -time.Second, 999, "boot-a", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := New(filepath.Join(t.TempDir(), "data.db"), "test-password")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			n, err := s.CreateNode("node", "", "")
			if err != nil {
				t.Fatal(err)
			}
			now := time.Now().UTC()
			first := protocol.Report{UpdatedAt: now}
			first.Uptime = 1000
			first.Network.BootID = "boot-a"
			first.Network.TotalUp = 100
			if _, err := s.ingestReportAt(n.Token, first, now); err != nil {
				t.Fatal(err)
			}
			next := first
			next.UpdatedAt = now.Add(tc.shift)
			next.Uptime = tc.uptime
			next.Network.BootID = tc.boot
			next.Network.TotalUp = 200
			received := now.Add(time.Minute)
			if _, err := s.ingestReportAt(n.Token, next, received); err != nil {
				t.Fatal(err)
			}
			got := s.GetNode(n.UUID)
			wantSeen, wantCounter := now, int64(100)
			if tc.accept {
				wantSeen, wantCounter = received, 200
			}
			if !got.LastSeen.Equal(wantSeen) || got.LastReport.Network.TotalUp != wantCounter {
				t.Fatalf("lastSeen=%v counter=%d; want %v, %d", got.LastSeen, got.LastReport.Network.TotalUp, wantSeen, wantCounter)
			}
			if got.CurrentCycleUsed != 0 {
				t.Fatalf("ambiguous interval charged: %d", got.CurrentCycleUsed)
			}
			if tc.accept {
				next.UpdatedAt = next.UpdatedAt.Add(time.Second)
				next.Uptime++
				next.Network.TotalUp += 25
				if _, err := s.ingestReportAt(n.Token, next, received.Add(time.Second)); err != nil {
					t.Fatal(err)
				}
				if used := s.GetNode(n.UUID).CurrentCycleUsed; used != 25 {
					t.Fatalf("traffic did not resume: %d", used)
				}
			}
		})
	}
}
