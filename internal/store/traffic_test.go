package store

import (
	"math"
	"path/filepath"
	"sort"
	"strings"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func cycleUsageOptions(value *float64) NodeOptions {
	return NodeOptions{ResetDay: -1, TrafficLimitGB: -1, InitialUsedGB: -1, CycleUsedGB: value}
}

func TestStoreCloseWaitsForFlusher(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-s.flushDone:
	default:
		t.Fatal("store closed before periodic flusher exited")
	}
}

func TestIngestReportUsesOneReceiveTime(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("clock", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", []protocol.PingTarget{{Name: "target", Host: "192.0.2.1:80"}}, ""); err != nil {
		t.Fatal(err)
	}
	receivedAt := time.Now().Add(-2 * time.Minute).Truncate(time.Second)
	report := protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 12}}}
	if _, err := s.ingestReportAt(n.Token, report, "", receivedAt); err != nil {
		t.Fatal(err)
	}
	got := s.GetNode(n.UUID)
	if !got.LastSeen.Equal(receivedAt.UTC()) || len(got.History) != 1 || got.History[0].Timestamp != receivedAt.Unix() {
		t.Fatalf("report timestamps differ from receipt time: last seen %s, history %+v", got.LastSeen, got.History)
	}
	samples := got.PingHistory["target"]
	if len(samples) != 1 || samples[0].Timestamp != receivedAt.Unix() {
		t.Fatalf("ping timestamp differs from receipt time: %+v", samples)
	}
}

func TestManualCycleUsageReplacesTotalAndContinuesAfterCorrection(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNode("manual", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range []int64{100, 200} {
		if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: total}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	targetGB := 1.25
	if err := s.UpdateNodeWithOptions(n.UUID, cycleUsageOptions(&targetGB)); err != nil {
		t.Fatal(err)
	}
	targetBytes := int64(targetGB * 1024 * 1024 * 1024)
	if got := s.GetNode(n.UUID); got.CycleTotalUsed != targetBytes || got.CurrentCycleUsed != 0 || got.TrafficManualAt == nil {
		t.Fatalf("manual correction: %+v", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.UpdateNode(n.UUID, "renamed", "", "", 0); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(n.UUID); got.CycleTotalUsed != targetBytes || got.TrafficManualAt == nil {
		t.Fatalf("ordinary edit changed manual usage: %+v", got)
	}
	previousAt := time.Now().UTC().Add(-20 * time.Second)
	currentAt := previousAt.Add(20 * time.Second)
	manualAt := previousAt.Add(10 * time.Second)
	s.mu.Lock()
	s.nodes[n.UUID].LastReport.UpdatedAt = previousAt
	s.nodes[n.UUID].LastSeen = previousAt
	s.nodes[n.UUID].TrafficManualAt = &manualAt
	s.mu.Unlock()
	if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: 300}, UpdatedAt: currentAt}, ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(n.UUID); got.CycleTotalUsed != targetBytes+50 || got.TrafficManualAt != nil {
		t.Fatalf("post-correction traffic: used %d, correction time %v", got.CycleTotalUsed, got.TrafficManualAt)
	}
}

func TestManualCycleUsageValidatesInput(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("validation", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []float64{-1, math.NaN(), math.Inf(1), 1e20} {
		if err := s.UpdateNodeWithOptions(n.UUID, cycleUsageOptions(&value)); err == nil {
			t.Fatalf("invalid usage %v accepted", value)
		}
	}
	if got := s.GetNode(n.UUID).CycleTotalUsed; got != 0 {
		t.Fatalf("invalid input changed usage: %d", got)
	}
	zero := 0.0
	if err := s.UpdateNodeWithOptions(n.UUID, cycleUsageOptions(&zero)); err != nil {
		t.Fatalf("zero correction rejected: %v", err)
	}
}

func TestTrafficUsesSampleTimestampForRollover(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "dated", ResetDay: 1})
	if err != nil {
		t.Fatal(err)
	}
	boundary := time.Date(2026, 10, 1, 0, 0, 0, 0, time.Local)
	oldStart := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	s.mu.Lock()
	s.nodes[n.UUID].CycleStart = oldStart
	s.mu.Unlock()
	send := func(sampleAt, receivedAt time.Time, total int64) {
		t.Helper()
		if _, err := s.ingestReportAt(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: total}, UpdatedAt: sampleAt.UTC()}, "", receivedAt); err != nil {
			t.Fatal(err)
		}
	}
	send(boundary.Add(-2*time.Minute), boundary.Add(-2*time.Minute), 100)
	send(boundary.Add(-30*time.Second), boundary.Add(30*time.Second), 150)
	s.mu.RLock()
	if got := s.nodes[n.UUID]; got.CurrentCycleUsed != 50 || !got.CycleStart.Equal(oldStart) {
		t.Fatalf("pre-boundary sample was placed in new cycle: used %d, start %s", got.CurrentCycleUsed, got.CycleStart)
	}
	s.mu.RUnlock()
	// A later counter with an older sample time must not change the baseline.
	send(boundary.Add(-90*time.Second), boundary, 130)
	s.mu.RLock()
	if got := s.nodes[n.UUID]; got.LastTotalUp != 150 || got.CurrentCycleUsed != 50 {
		t.Fatalf("out-of-order sample changed usage: total %d, used %d", got.LastTotalUp, got.CurrentCycleUsed)
	}
	s.mu.RUnlock()
	send(boundary.Add(30*time.Second), boundary.Add(40*time.Second), 250)
	s.mu.RLock()
	if got := s.nodes[n.UUID]; got.CurrentCycleUsed != 50 || !got.CycleStart.Equal(boundary) {
		t.Fatalf("post-boundary usage: used %d, start %s", got.CurrentCycleUsed, got.CycleStart)
	}
	s.mu.RUnlock()
}

func TestTrafficDefaultsToMonthlyCycle(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "monthly", TrafficLimitGB: 1})
	if err != nil {
		t.Fatal(err)
	}
	if n.ResetDay != 1 {
		t.Fatalf("blank reset day became %d, want 1", n.ResetDay)
	}
	for _, total := range []int64{100, 150} {
		if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: total, TotalDown: total}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 100 {
		t.Fatalf("usage with blank reset day: got %d, want 100", got)
	}
}

func TestTrafficMigrationStartsAtLastCounter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNode("legacy", "", "")
	if err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	s.nodes[n.UUID].ResetDay = 0
	s.nodes[n.UUID].CycleStart = time.Time{}
	s.nodes[n.UUID].InitialUsed = 25
	s.nodes[n.UUID].TrafficBaselineSet = false
	s.nodes[n.UUID].LastReport = &protocol.Report{Network: protocol.NetworkReport{TotalUp: 100, TotalDown: 200}}
	s.dirty = true
	s.mu.Unlock()
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: 110, TotalDown: 220}}, ""); err != nil {
		t.Fatal(err)
	}
	got := s.GetNode(n.UUID)
	if got.ResetDay != 1 || got.CycleTotalUsed != 55 {
		t.Fatalf("migrated node: reset day %d, usage %d; want 1 and 55", got.ResetDay, got.CycleTotalUsed)
	}
}

func TestTrafficAccountsPerInterfaceAfterReset(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("multi", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, sample := range []struct {
		first, second, want int64
	}{
		{1000, 5000, 0},
		{2000, 0, 1000},
		{2500, 100, 1600},
	} {
		report := protocol.Report{Network: protocol.NetworkReport{
			Source:  "interfaces-v1:eth0,eth1",
			TotalUp: sample.first + sample.second,
			Interfaces: map[string]protocol.InterfaceCounters{
				"eth0": {Up: sample.first},
				"eth1": {Up: sample.second},
			},
		}}
		if _, err := s.IngestReport(n.Token, report, ""); err != nil {
			t.Fatal(err)
		}
		if got := s.GetNode(n.UUID).CurrentCycleUsed; got != sample.want {
			t.Fatalf("after %d + %d: got %d, want %d", sample.first, sample.second, got, sample.want)
		}
	}
}

func TestTrafficRebasesAfterBootChangeOrCounterReset(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("reboot", "", "")
	if err != nil {
		t.Fatal(err)
	}
	send := func(boot string, bytes int64) {
		t.Helper()
		report := protocol.Report{Network: protocol.NetworkReport{
			BootID: boot, Source: "interfaces-v1:eth0", TotalUp: bytes,
			Interfaces: map[string]protocol.InterfaceCounters{"eth0": {Up: bytes}},
		}}
		if _, err := s.IngestReport(n.Token, report, ""); err != nil {
			t.Fatal(err)
		}
	}
	send("boot-a", 100)
	send("boot-a", 150)
	send("boot-b", 10000) // A different machine or reboot must not charge its lifetime bytes.
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 50 {
		t.Fatalf("boot change charged old counters: %d", got)
	}
	send("boot-b", 10020)
	send("boot-b", 80) // A recreated interface can start with a nonzero counter.
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 70 {
		t.Fatalf("counter reset charged new raw value: %d", got)
	}
	send("boot-b", 100)
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 90 {
		t.Fatalf("counter failed to resume after reset: %d", got)
	}
}

func TestTrafficKeepsCommonInterfacesWhenSelectionChanges(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("changing", "", "")
	if err != nil {
		t.Fatal(err)
	}
	send := func(counters map[string]protocol.InterfaceCounters, total, want int64) {
		t.Helper()
		var names []string
		for name := range counters {
			names = append(names, name)
		}
		sort.Strings(names)
		report := protocol.Report{Network: protocol.NetworkReport{
			BootID: "one-boot", Source: "interfaces-v1:" + strings.Join(names, ","),
			TotalUp: total, Interfaces: counters,
		}}
		if _, err := s.IngestReport(n.Token, report, ""); err != nil {
			t.Fatal(err)
		}
		if got := s.GetNode(n.UUID).CurrentCycleUsed; got != want {
			t.Fatalf("after %v: used %d, want %d", names, got, want)
		}
	}
	send(map[string]protocol.InterfaceCounters{"eth0": {Up: 100}}, 100, 0)
	send(map[string]protocol.InterfaceCounters{"eth0": {Up: 130}, "eth1": {Up: 5000}}, 5130, 30)
	send(map[string]protocol.InterfaceCounters{"eth0": {Up: 150}, "eth1": {Up: 5010}}, 5160, 60)
	send(map[string]protocol.InterfaceCounters{"eth0": {Up: 180}}, 180, 90)
}

func TestTrafficUpgradeToInterfaceCountersSurvivesRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNode("upgrade", "", "")
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range []int64{100, 150} {
		if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: total}}, ""); err != nil {
			t.Fatal(err)
		}
	}
	report := func(total int64) protocol.Report {
		return protocol.Report{Network: protocol.NetworkReport{
			TotalUp: total,
			Interfaces: map[string]protocol.InterfaceCounters{
				"eth0": {Up: total},
			},
		}}
	}
	if _, err := s.IngestReport(n.Token, report(160), ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 50 {
		t.Fatalf("switch to detailed counters charged transition interval: %d", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := s.IngestReport(n.Token, report(170), ""); err != nil {
		t.Fatal(err)
	}
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 60 {
		t.Fatalf("detailed baseline after restart: got %d, want 60", got)
	}
}

func TestTrafficCycleBoundaryUsesOnlyCurrentIntervalShare(t *testing.T) {
	start := time.Date(2026, 9, 1, 0, 0, 0, 0, time.UTC)
	previous := start.Add(-time.Hour)
	current := start.Add(time.Hour)
	for _, tc := range []struct {
		name      string
		prev, cur time.Time
		want      int64
	}{
		{"crosses boundary", previous, current, 50},
		{"entirely before", previous, start, 0},
		{"entirely after", start, current, 100},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := currentCycleDelta(100, start, tc.prev, tc.cur); got != tc.want {
				t.Fatalf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTrafficRolloverSplitsSampleInterval(t *testing.T) {
	s, err := New(filepath.Join(t.TempDir(), "data.db"), "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	now := time.Now()
	boundary := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	previousAt := boundary.Add(-24 * time.Hour)
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "boundary", ResetDay: now.Day()})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: 100}}, ""); err != nil {
		t.Fatal(err)
	}
	s.mu.Lock()
	stored := s.nodes[n.UUID]
	stored.CycleStart = previousAt
	stored.CurrentCycleUsed = 777
	stored.InitialUsed = 42
	stored.LastSeen = previousAt
	stored.LastReport.UpdatedAt = previousAt
	s.mu.Unlock()
	currentAt := time.Now().UTC()
	if _, err := s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: 200}, UpdatedAt: currentAt}, ""); err != nil {
		t.Fatal(err)
	}
	got := s.GetNode(n.UUID)
	want := currentCycleDelta(100, boundary, previousAt, currentAt)
	if got.CurrentCycleUsed != want || got.InitialUsed != 0 || !got.CycleStart.Equal(boundary) {
		t.Fatalf("rollover usage %d (want %d), initial %d, start %s", got.CurrentCycleUsed, want, got.InitialUsed, got.CycleStart)
	}
	if got.CurrentCycleUsed >= 100 {
		t.Fatal("entire pre-boundary interval was charged to the new cycle")
	}
}
