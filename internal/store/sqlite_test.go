package store

import (
	"path/filepath"
	"testing"

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

	err = s.UpdateSettings("", "", []protocol.PingTarget{
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
