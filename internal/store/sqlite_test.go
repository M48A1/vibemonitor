package store

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestSQLiteMigrationFromJSON(t *testing.T) {
	tempDir := t.TempDir()
	jsonPath := filepath.Join(tempDir, "data.json")

	legacyConfig := Config{
		AdminUsername: "testadmin",
		AdminPassword: "testpassword",
		SiteTitle:     "Old Title",
		PingTargets: []protocol.PingTarget{
			{Name: "Target1", Host: "1.1.1.1:80"},
		},
	}
	legacyNodes := map[string]*Node{
		"node-1": {
			UUID:   "node-1",
			Name:   "Test Node 1",
			Token:  "token-123",
			Online: true,
		},
	}

	data, err := json.Marshal(DataFile{Config: legacyConfig, Nodes: legacyNodes})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(jsonPath, data, 0600); err != nil {
		t.Fatal(err)
	}

	// 创建 sidecar ping 数据
	sidecarPath := jsonPath + ".ping.json"
	sidecarData, err := json.Marshal(pingFile{
		Nodes: map[string]pingNode{
			"node-1": {
				History: map[string][]PingSample{
					"Target1": {
						{Host: "1.1.1.1:80", Method: "tcp", Timestamp: time.Now().Unix() - 60, Latency: 42},
					},
				},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sidecarPath, sidecarData, 0600); err != nil {
		t.Fatal(err)
	}

	// 初始化 Store，触发自动迁移
	s, err := New(jsonPath, "")
	if err != nil {
		t.Fatalf("failed to open store: %v", err)
	}
	defer s.Close()

	if s.config.AdminUsername != "testadmin" || s.config.SiteTitle != "Old Title" {
		t.Errorf("unexpected config: %+v", s.config)
	}

	node := s.GetNode("node-1")
	if node == nil || node.Name != "Test Node 1" {
		t.Errorf("node not migrated properly: %+v", node)
	}

	// 验证 ping 历史
	hist, err := s.GetPingHistory("node-1", "Target1", "1h")
	if err != nil {
		t.Fatalf("failed to get ping history: %v", err)
	}
	if len(hist.Samples) == 0 || hist.Samples[0].Latency != 42 {
		t.Errorf("ping samples not migrated properly: %+v", hist)
	}

	// 验证旧文件备份
	if _, err := os.Stat(jsonPath + ".bak"); err != nil {
		t.Errorf("expected backup file %s.bak to exist", jsonPath)
	}
}

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
