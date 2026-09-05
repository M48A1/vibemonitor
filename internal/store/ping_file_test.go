package store

import (
	"bytes"
	"encoding/json"
	"os"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestPingFileMigrationAndRestore(t *testing.T) {
	s, n, path := pingStore(t)
	s.mu.Lock()
	s.nodes[n.UUID].PingHistory = map[string][]PingSample{"target": {{Host: "192.0.2.1:80", Method: "tcp", Timestamp: time.Now().Unix(), Latency: 25}}}
	s.nodes[n.UUID].LastReport = &protocol.Report{PingResults: []protocol.PingResult{{Name: "target", Host: "192.0.2.1:80", Method: "tcp", Latency: 25}}}
	legacy, err := json.Marshal(DataFile{Config: s.config, Nodes: s.nodes})
	s.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, legacy, 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path + ".ping.json"); err != nil {
		t.Fatal(err)
	}
	reopened, err := New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	main, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(main, []byte("ping_history")) || bytes.Contains(main, []byte(`"latency": 25`)) {
		t.Fatal("ping data remains in main file")
	}
	reopened, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	got := reopened.GetNode(n.UUID)
	if len(got.PingHistory["target"]) != 1 || len(got.LastReport.PingResults) != 1 {
		t.Fatal("migration lost ping data")
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
	// Restoring a different main snapshot must not silently reuse this history.
	if err := os.WriteFile(path, append(main, '\n'), 0600); err != nil {
		t.Fatal(err)
	}
	reopened, err = New(path, "")
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if len(reopened.GetNode(n.UUID).PingHistory) != 0 {
		t.Fatal("mismatched sidecar loaded")
	}
}
