package store

import (
	"os"
	"testing"
	"vibemonitor/pkg/protocol"
)

func TestIndependentNodeTargetsAndBilling(t *testing.T) {
	s, first, path := pingStore(t)
	profile := &NodeProfile{Targets: []protocol.PingTarget{{Name: "电信", Host: "example.com:443"}}, DueDate: "2027-01-31", PaymentCycle: "year", Price: 45, Currency: "EUR"}
	second, err := s.CreateNodeWithOptions(NodeOptions{Name: "second", Profile: profile})
	if err != nil {
		t.Fatal(err)
	}
	if s.NodeTargets(first.UUID)[0].Host == s.NodeTargets(second.UUID)[0].Host {
		t.Fatal("targets not isolated")
	}
	report := protocol.Report{PingResults: []protocol.PingResult{{Name: "电信", Host: "example.com:443", Method: "tcp", Latency: -1}, {Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 10}}}
	if _, err := s.IngestReport(second.Token, report, ""); err != nil {
		t.Fatal(err)
	}
	h, err := s.GetPingHistory(second.UUID, "电信", "1h")
	if err != nil || len(h.Samples) != 1 || h.Stats.PacketLoss != 100 {
		t.Fatal("incorrect node history", h, err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateNodeWithOptions(second.UUID, NodeOptions{Profile: &NodeProfile{Targets: []protocol.PingTarget{}}}); err == nil {
		t.Fatal("expected write failure")
	}
	if s.GetNode(second.UUID).Profile.DueDate != "2027-01-31" {
		t.Fatal("failed write changed billing")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.GetNode(second.UUID).Profile.Price != 45 {
		t.Fatal("billing not persisted")
	}
	if err := reopened.UpdateNodeWithOptions(second.UUID, NodeOptions{Profile: &NodeProfile{Targets: []protocol.PingTarget{}}}); err != nil {
		t.Fatal(err)
	}
	h, _ = reopened.GetPingHistory(second.UUID, "电信", "1h")
	if len(h.Samples) != 0 {
		t.Fatal("removed target retained")
	}
}
func TestInvalidNodeProfiles(t *testing.T) {
	for _, p := range []*NodeProfile{{DueDate: "2026-02-30"}, {PaymentCycle: "week"}, {Price: -1}, {Targets: []protocol.PingTarget{{Name: "bad", Host: "1.1.1.1"}}}} {
		if validateProfile(p) == nil {
			t.Fatal("invalid profile accepted", p)
		}
	}
}

func TestLegacyTargetsBecomeIndependent(t *testing.T) {
	s, node, path := pingStore(t)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.GetNode(node.UUID).Profile == nil {
		t.Fatal("legacy targets not migrated")
	}
	if err := reopened.UpdateSettings("", "", []protocol.PingTarget{{Name: "changed", Host: "example.com:443"}}, ""); err != nil {
		t.Fatal(err)
	}
	targets := reopened.NodeTargets(node.UUID)
	if len(targets) != 1 || targets[0].Name != "target" {
		t.Fatal("global changes affected migrated node")
	}
}
