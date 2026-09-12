package store

import (
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestSQLitePingHistoryRecordAndQuery(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test.db")

	s, err := New(dbPath, "admin123")
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer s.Close()

	node, err := s.CreateNode("Server-1", "Default", "US")
	if err != nil {
		t.Fatalf("failed to add node: %v", err)
	}

	err = s.UpdateSettings("", []protocol.PingTarget{
		{Name: "Google", Host: "8.8.8.8:53"},
	}, "")
	if err != nil {
		t.Fatalf("failed to update settings: %v", err)
	}

	// 上报 ping
	_, err = s.IngestReport(node.Token, protocol.Report{
		PingResults: []protocol.PingResult{
			{Name: "Google", Host: "8.8.8.8:53", Method: "tcp", Latency: 20},
		},
	}, "1.2.3.4")
	if err != nil {
		t.Fatalf("failed to ingest report: %v", err)
	}

	hist, err := s.GetPingHistory(node.UUID, "Google", "1h")
	if err != nil {
		t.Fatalf("failed to get ping history: %v", err)
	}
	if len(hist.Samples) != 1 || hist.Samples[0].Latency != 20 {
		t.Errorf("unexpected samples: %+v", hist.Samples)
	}
	if hist.Stats.Current != 20 || hist.Stats.Avg != 20 {
		t.Errorf("unexpected stats: %+v", hist.Stats)
	}
}

func TestSQLitePingHistoryRanges(t *testing.T) {
	tempDir := t.TempDir()
	dbPath := filepath.Join(tempDir, "test_ranges.db")

	s, err := New(dbPath, "admin123")
	if err != nil {
		t.Fatalf("failed to initialize store: %v", err)
	}
	defer s.Close()

	node, err := s.CreateNode("Server-Range", "Default", "US")
	if err != nil {
		t.Fatalf("failed to add node: %v", err)
	}

	err = s.UpdateSettings("", []protocol.PingTarget{
		{Name: "Target-A", Host: "1.1.1.1:53"},
	}, "")
	if err != nil {
		t.Fatalf("failed to update settings: %v", err)
	}

	now := time.Now().Unix()
	// Insert ping samples at various intervals:
	// - 30 minutes ago (within 1h)
	// - 6 hours ago (within 24h, not 1h)
	// - 3 days ago (within 7d, not 24h)
	// - 15 days ago (only in 'all')
	samples := []struct {
		offset  int64
		latency int
	}{
		{offset: 30 * 60, latency: 15},
		{offset: 6 * 3600, latency: 25},
		{offset: 3 * 86400, latency: 35},
		{offset: 15 * 86400, latency: 45},
	}

	for _, smp := range samples {
		ts := now - smp.offset
		err = s.sdb.recordPingSample(node.UUID, "Target-A", "1.1.1.1:53", "tcp", ts, smp.latency)
		if err != nil {
			t.Fatalf("failed to record sample at %d: %v", ts, err)
		}
	}

	// 1h range
	h1, err := s.GetPingHistory(node.UUID, "Target-A", "1h")
	if err != nil {
		t.Fatalf("failed to get 1h history: %v", err)
	}
	if len(h1.Samples) != 1 {
		t.Errorf("expected 1 sample in 1h, got %d", len(h1.Samples))
	}
	if h1.Range != "1h" {
		t.Errorf("expected range '1h', got %s", h1.Range)
	}

	// 24h range
	h24, err := s.GetPingHistory(node.UUID, "Target-A", "24h")
	if err != nil {
		t.Fatalf("failed to get 24h history: %v", err)
	}
	if len(h24.Samples) != 2 {
		t.Errorf("expected 2 samples in 24h, got %d", len(h24.Samples))
	}
	if h24.Range != "24h" {
		t.Errorf("expected range '24h', got %s", h24.Range)
	}

	// 7d range (最近一周)
	h7d, err := s.GetPingHistory(node.UUID, "Target-A", "7d")
	if err != nil {
		t.Fatalf("failed to get 7d history: %v", err)
	}
	if len(h7d.Samples) != 3 {
		t.Errorf("expected 3 samples in 7d, got %d", len(h7d.Samples))
	}
	if h7d.Range != "7d" {
		t.Errorf("expected range '7d', got %s", h7d.Range)
	}

	// all range (所有)
	hAll, err := s.GetPingHistory(node.UUID, "Target-A", "all")
	if err != nil {
		t.Fatalf("failed to get all history: %v", err)
	}
	if len(hAll.Samples) != 4 {
		t.Errorf("expected 4 samples in all, got %d", len(hAll.Samples))
	}
	if hAll.Range != "all" {
		t.Errorf("expected range 'all', got %s", hAll.Range)
	}
	if hAll.StartTime <= 0 || hAll.EndTime <= hAll.StartTime {
		t.Errorf("invalid StartTime/EndTime in all range: %d / %d", hAll.StartTime, hAll.EndTime)
	}
}
