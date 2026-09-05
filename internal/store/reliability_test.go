package store

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestTrafficZeroBaselineAndRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	n, err := s.CreateNodeWithOptions(NodeOptions{Name: "test", ResetDay: 1})
	if err != nil {
		t.Fatal(err)
	}
	for _, total := range []int64{0, 100, 0, 25} {
		_, err = s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: total, TotalDown: total}}, "")
		if err != nil {
			t.Fatal(err)
		}
	}
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 250 {
		t.Fatalf("usage: got %d want 250", got)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.IngestReport(n.Token, protocol.Report{Network: protocol.NetworkReport{TotalUp: 30, TotalDown: 30}}, "")
	if got := s.GetNode(n.UUID).CurrentCycleUsed; got != 260 {
		t.Fatalf("usage after restart: %d", got)
	}
}

func TestAdaptiveOfflineThreshold(t *testing.T) {
	now := time.Now()
	n := Node{LastSeen: now.Add(-30 * time.Second), BasicInfo: &protocol.BasicInfo{ReportIntervalSeconds: 30}}
	n.calculateDynamicFields(now)
	if !n.Online {
		t.Fatal("30s interval falsely offline")
	}
	n.LastSeen = now.Add(-91 * time.Second)
	n.calculateDynamicFields(now)
	if n.Online {
		t.Fatal("stale probe still online")
	}
	n.BasicInfo = nil
	n.LastSeen = now.Add(-11 * time.Second)
	n.calculateDynamicFields(now)
	if n.Online {
		t.Fatal("legacy default changed")
	}
}

func TestFailedNodeChangesRollback(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.CreateNode("original", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	if _, err := s.CreateNode("lost", "", ""); err == nil {
		t.Fatal("create unexpectedly succeeded")
	}
	if err := s.UpdateNode(n.UUID, "lost", "", "", 0); err == nil {
		t.Fatal("update unexpectedly succeeded")
	}
	if err := s.DeleteNode(n.UUID); err == nil {
		t.Fatal("delete unexpectedly succeeded")
	}
	if len(s.GetNodes()) != 1 || s.GetNode(n.UUID).Name != "original" || s.FindNodeByToken(n.Token) == nil {
		t.Fatal("failed change mutated memory")
	}
}

func TestBackupValidation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	s, err := New(path, "pass")
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateData(data); err != nil {
		t.Fatal(err)
	}
	for _, invalid := range []string{`{}`, `{"config":{"admin_password":"x"},"nodes":{"x":null}}`, `not json`} {
		if ValidateData([]byte(invalid)) == nil {
			t.Fatal("invalid backup accepted")
		}
	}
}
