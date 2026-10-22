package store

import (
	"path/filepath"
	"testing"
	"time"
	"vibemonitor/pkg/protocol"
)

func TestTrafficHistoryCycleRanges(t *testing.T) {
	loc := time.FixedZone("billing", 8*3600)
	for _, tc := range []struct {
		now, span, start, end string
		reset                 int
	}{
		{"2026-10-18", "current", "2026-10-15", "2026-11-15", 15},
		{"2026-10-18", "previous", "2026-09-15", "2026-10-15", 15},
		{"2026-10-18", "three", "2026-08-15", "2026-11-15", 15},
		{"2026-03-01", "current", "2026-02-28", "2026-03-31", 31},
		{"2026-03-01", "previous", "2026-01-31", "2026-02-28", 31},
		{"2024-03-01", "three", "2023-12-31", "2024-03-31", 31},
		{"2026-10-15", "current", "2026-10-15", "2026-11-15", 15},
	} {
		now, _ := time.ParseInLocation("2006-01-02", tc.now, loc)
		start, end, err := trafficHistoryRange(tc.reset, tc.span, now)
		if err != nil || start.Format("2006-01-02") != tc.start || end.Format("2006-01-02") != tc.end {
			t.Fatalf("%+v got %v %v %v", tc, start, end, err)
		}
	}
}

func TestDailyTrafficSplitsAndRetention(t *testing.T) {
	loc := time.FixedZone("billing", 8*3600)
	midnight := time.Date(2026, 10, 15, 0, 0, 0, 0, loc)
	n := &Node{DailyTraffic: map[string]int64{"2025-01-01": 99}}
	n.recordDailyTraffic(101, midnight.Add(-time.Second), midnight.Add(time.Second))
	if n.DailyTraffic["2026-10-14"] != 50 || n.DailyTraffic["2026-10-15"] != 51 || len(n.DailyTraffic) != 2 {
		t.Fatalf("split: %v", n.DailyTraffic)
	}
	n.recordDailyTraffic(0, midnight.AddDate(0, 0, 1), midnight.AddDate(0, 0, 1).Add(time.Minute))
	if value, ok := n.DailyTraffic["2026-10-16"]; !ok || value != 0 {
		t.Fatal("measured zero day missing")
	}
	n.recordDailyTraffic(80, midnight, midnight)
	if n.DailyTraffic["2026-10-15"] != 51 {
		t.Fatal("invalid interval counted")
	}
	// UTC midnight is 08:00 in the fixed billing timezone, not a daily boundary.
	d := &Node{}
	start := time.Date(2026, 10, 14, 23, 59, 59, 0, time.UTC)
	d.recordDailyTraffic(100, start, start.Add(2*time.Second))
	if d.DailyTraffic["2026-10-15"] != 100 || len(d.DailyTraffic) != 1 {
		t.Fatalf("not UTC+8: %v", d.DailyTraffic)
	}

}

func TestDailyTrafficIngestionPersistenceAndRestore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "source.db")
	s, err := New(path, "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "daily", ResetDay: 15, InitialUsedGB: 1})
	if err != nil {
		t.Fatal(err)
	}
	loc := trafficLocation
	at := time.Date(2026, 10, 14, 23, 59, 59, 0, loc)
	report := protocol.Report{UpdatedAt: at, Uptime: 100}
	report.Network.BootID = "boot-a"
	report.Network.TotalUp = 1000
	send := func(r protocol.Report) {
		t.Helper()
		if _, err := s.ingestReportAt(n.Token, r, r.UpdatedAt); err != nil {
			t.Fatal(err)
		}
	}
	send(report)
	report.UpdatedAt = at.Add(2 * time.Second)
	report.Uptime += 2
	report.Network.TotalUp += 100
	send(report)
	send(report) // Duplicate must not charge twice.
	report.UpdatedAt = report.UpdatedAt.Add(time.Second)
	report.Uptime++
	report.Network.TotalUp = 1
	send(report) // Counter reset must not produce a negative or inflated day.
	response, err := s.trafficHistoryAt(n.UUID, "three", at.Add(3*time.Second))
	if err != nil || response.Total != 100 {
		t.Fatalf("history=%+v err=%v", response, err)
	}
	if s.GetNodes()[0].DailyTraffic != nil {
		t.Fatal("daily history leaked into dashboard payload")
	}
	previous, err := s.trafficHistoryAt(n.UUID, "previous", at.Add(3*time.Second))
	if err != nil || previous.Total != 50 || previous.EndDate != "2026-10-14" {
		t.Fatalf("previous=%+v err=%v", previous, err)
	}
	current, err := s.trafficHistoryAt(n.UUID, "current", at.Add(3*time.Second))
	if err != nil || current.Total != 50 || current.Days[1].Bytes != 0 {
		t.Fatalf("current=%+v err=%v", current, err)
	}
	s.mu.RLock()
	snapshot := cloneNodeForSave(s.nodes[n.UUID])
	s.mu.RUnlock()
	snapshot.DailyTraffic["2026-10-15"] = 999
	current, _ = s.trafficHistoryAt(n.UUID, "current", at.Add(3*time.Second))
	if current.Total != 50 {
		t.Fatal("snapshot aliases live daily map")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	destination := filepath.Join(t.TempDir(), "restored.db")
	if err := RestoreData(path, destination); err != nil {
		t.Fatal(err)
	}
	restored, err := New(destination, "")
	if err != nil {
		t.Fatal(err)
	}
	defer restored.Close()
	got, err := restored.trafficHistoryAt(n.UUID, "three", at.Add(3*time.Second))
	if err != nil || got.Total != 100 {
		t.Fatalf("restored=%+v err=%v", got, err)
	}
	if err := restored.DeleteNode(n.UUID); err != nil {
		t.Fatal(err)
	}
	if _, err := restored.GetTrafficHistory(n.UUID, "current"); err != ErrNodeNotFound {
		t.Fatalf("deleted history: %v", err)
	}
}

func TestDailyOnlineStatusUTC8(t *testing.T) {
	day := time.Date(2026, 10, 15, 0, 0, 0, 0, trafficLocation)
	n := &Node{OnlineHistorySince: day.AddDate(0, 0, -1)}
	// The intervals overlap: count their union, not both full grace periods.
	n.recordDailyOnline(day.Add(-5 * time.Second))
	n.recordDailyOnline(day)
	if got := n.DailyOnline["2026-10-15"]; got != int64(10*time.Second) {
		t.Fatalf("overlap counted twice: %d", got)
	}
	status, secs := n.dailyOnlineStatus(day, day.Add(2*time.Second))
	if status != "online" || secs != 2 {
		t.Fatalf("future grace counted: %s %d", status, secs)
	}
	status, secs = n.dailyOnlineStatus(day, day.AddDate(0, 0, 1))
	if status != "partial" || secs != 10 {
		t.Fatalf("partial: %s %d", status, secs)
	}
	status, _ = n.dailyOnlineStatus(day.AddDate(0, 0, 1), day.AddDate(0, 0, 2))
	if status != "offline" {
		t.Fatalf("offline day: %s", status)
	}
	n.DailyOnline["2026-10-15"] = int64(24 * time.Hour)
	status, secs = n.dailyOnlineStatus(day, day.AddDate(0, 0, 1))
	if status != "online" || secs != 86400 {
		t.Fatalf("full day: %s %d", status, secs)
	}
	status, _ = n.dailyOnlineStatus(day.AddDate(0, 0, 2), day.AddDate(0, 0, 1))
	if status != "future" {
		t.Fatalf("future: %s", status)
	}
	unknown := &Node{}
	status, _ = unknown.dailyOnlineStatus(day, day.AddDate(0, 0, 1))
	if status != "unknown" {
		t.Fatalf("unknown history: %s", status)
	}
	// Billing midnight is 16:00 UTC regardless of the host timezone.
	utc := time.Date(2026, 10, 14, 16, 0, 0, 0, time.UTC)
	start, _, err := trafficHistoryRange(15, "current", utc)
	if err != nil || !start.Equal(utc) {
		t.Fatalf("UTC+8 reset: %v %v", start, err)
	}
}

func TestDailyTrafficSumsUploadAndDownload(t *testing.T) {
	for _, detailed := range []bool{false, true} {
		t.Run(map[bool]string{false: "aggregate", true: "per-interface"}[detailed], func(t *testing.T) {
			s, err := New(filepath.Join(t.TempDir(), "data.db"), "test-password")
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			n, err := s.CreateNodeWithOptions(NodeOptions{Name: "bidirectional", ResetDay: 15})
			if err != nil {
				t.Fatal(err)
			}
			base := time.Date(2026, 10, 15, 10, 0, 0, 0, trafficLocation)
			for _, sample := range []struct {
				hours    int
				up, down int64
			}{{0, 1000, 2000}, {1, 1100, 2200}, {14, 1200, 2400}, {15, 1230, 2470}, {15, 1230, 2470}} {
				at := base.Add(time.Duration(sample.hours) * time.Hour)
				r := protocol.Report{UpdatedAt: at, Uptime: int64(100 + sample.hours*3600)}
				r.Network.BootID = "same-boot"
				r.Network.TotalUp, r.Network.TotalDown = sample.up, sample.down
				if detailed {
					r.Network.Interfaces = map[string]protocol.InterfaceCounters{"eth0": {Up: sample.up, Down: sample.down}}
				}
				if _, err := s.ingestReportAt(n.Token, r, at); err != nil {
					t.Fatal(err)
				}
			}
			got, err := s.trafficHistoryAt(n.UUID, "current", base.Add(15*time.Hour))
			if err != nil {
				t.Fatal(err)
			}
			// First day: upload 200 + download 400; second day: upload 30 + download 70.
			if got.Days[0].Bytes != 600 || got.Days[1].Bytes != 100 || got.Total != 700 {
				t.Fatalf("daily upload+download sum: first=%d second=%d total=%d", got.Days[0].Bytes, got.Days[1].Bytes, got.Total)
			}
			s.mu.RLock()
			cycleUsed := s.nodes[n.UUID].CurrentCycleUsed
			s.mu.RUnlock()
			if cycleUsed != got.Total {
				t.Fatalf("cycle=%d, sum of days=%d", cycleUsed, got.Total)
			}
		})
	}
}
