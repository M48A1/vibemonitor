package store

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func pingStore(t *testing.T) (*Store, *Node, string) {
	t.Helper()
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	n, err := s.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", "", []protocol.PingTarget{{Name: "target", Host: "192.0.2.1:80"}}, ""); err != nil {
		t.Fatal(err)
	}
	return s, n, path
}

func TestPingReportsAreBoundedAndConfigured(t *testing.T) {
	s, n, _ := pingStore(t)
	oversized := protocol.Report{PingResults: make([]protocol.PingResult, MaxPingTargets+1)}
	if _, err := s.IngestReport(n.Token, oversized, ""); err == nil {
		t.Fatal("oversized results accepted")
	}
	if s.GetNode(n.UUID).LastReport != nil {
		t.Fatal("rejected request mutated node")
	}
	for batch := 0; batch < 3; batch++ {
		r := protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 20}}}
		for i := 1; i < MaxPingTargets; i++ {
			r.PingResults = append(r.PingResults, protocol.PingResult{Name: fmt.Sprintf("unknown-%d-%d", batch, i), Host: "192.0.2.1:80", Method: "tcp"})
		}
		if _, err := s.IngestReport(n.Token, r, ""); err != nil {
			t.Fatal(err)
		}
	}
	n = s.GetNode(n.UUID)
	if len(n.PingHistory) != 1 || len(n.LastReport.PingResults) != 1 {
		t.Fatal("unconfigured results retained")
	}
}

func TestTargetChangesPruneHistoryAndRollback(t *testing.T) {
	s, n, path := pingStore(t)
	report := protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 20}}}
	if _, err := s.IngestReport(n.Token, report, ""); err != nil {
		t.Fatal(err)
	}
	duplicate := []protocol.PingTarget{{Name: "target", Host: "192.0.2.1:80"}, {Name: "target", Host: "192.0.2.2:443"}}
	if err := s.UpdateSettings("", "", duplicate, ""); err == nil {
		t.Fatal("duplicate names accepted")
	}
	if err := s.UpdateConfig("", "", "", duplicate); err == nil {
		t.Fatal("alternate config API accepted duplicates")
	}
	changed := []protocol.PingTarget{{Name: "target", Host: "192.0.2.2:443"}}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", "", changed, ""); err == nil {
		t.Fatal("write unexpectedly succeeded")
	}
	h, _ := s.GetPingHistory(n.UUID, "target", "1h")
	if h.Host != "192.0.2.1:80" || len(h.Samples) != 1 {
		t.Fatal("failed config write damaged history")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateSettings("", "", changed, ""); err != nil {
		t.Fatal(err)
	}
	// A delayed old probe report must not reintroduce old-host observations.
	s.IngestReport(n.Token, report, "")
	h, _ = s.GetPingHistory(n.UUID, "target", "1h")
	if h.Host != "192.0.2.2:443" || len(h.Samples) != 0 {
		t.Fatal("old samples were relabeled")
	}
	if len(s.GetNode(n.UUID).LastReport.PingResults) != 0 {
		t.Fatal("stale dashboard result survived")
	}
}

func TestPingMethodsNeverMixAndMigrationIsBounded(t *testing.T) {
	s, n, path := pingStore(t)
	now := time.Now().Unix()
	s.mu.Lock()
	s.nodes[n.UUID].PingHistory = map[string][]PingSample{
		"target": {{Timestamp: now - 120, Latency: 10, Host: "192.0.2.1:80", Method: "icmp"}, {Timestamp: now - 60, Latency: 100, Host: "192.0.2.1:80", Method: "tcp"}},
	}
	s.mu.Unlock()
	h, _ := s.GetPingHistory(n.UUID, "target", "1h")
	if h.Method != "tcp" || h.Stats.Avg != 100 || len(h.Samples) != 1 {
		t.Fatal("TCP and ICMP mixed", h)
	}
	s.mu.Lock()
	for i := 0; i < 100; i++ {
		s.nodes[n.UUID].PingHistory[fmt.Sprint(i)] = []PingSample{{Timestamp: now, Latency: 1}}
	}
	s.nodes[n.UUID].PingHistory["target"] = append(s.nodes[n.UUID].PingHistory["target"], PingSample{Timestamp: now, Latency: 999})
	s.mu.Unlock()
	if err := s.Save(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	got := reopened.GetNode(n.UUID)
	if len(got.PingHistory) != 1 || len(got.PingHistory["target"]) != 2 {
		t.Fatal("legacy or unconfigured series survived migration")
	}
}
