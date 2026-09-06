//go:build linux && amd64

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibemonitor/internal/server"
	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

type memoryTransport struct {
	handler http.Handler
}

func (m *memoryTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	rec := httptest.NewRecorder()
	m.handler.ServeHTTP(rec, req)
	return rec.Result(), nil
}

func TestFullWorkflow(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data.json")

	adminPass := "adminSecret123"

	srv, err := server.New(server.Options{
		ListenAddr:    "127.0.0.1:1314",
		DataFile:      dataFile,
		AdminPassword: adminPass,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	client := &http.Client{
		Transport: &memoryTransport{handler: srv.Handler()},
	}

	baseURL := "http://127.0.0.1:1314"

	// 1. Test /ping
	resp, err := client.Get(baseURL + "/ping")
	if err != nil {
		t.Fatalf("Failed to ping server: %v", err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(body) != "pong" {
		t.Fatalf("Expected pong, got %s", string(body))
	}
	t.Log("[PASS] /ping returned pong")

	// 2. Test /api/public
	resp, err = client.Get(baseURL + "/api/public")
	if err != nil {
		t.Fatalf("Failed /api/public: %v", err)
	}
	var pub map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&pub)
	resp.Body.Close()
	if pub["site_title"] != "VibeMonitor" {
		t.Fatalf("Unexpected site title: %v", pub["site_title"])
	}
	t.Log("[PASS] /api/public verified")

	// 3. Test Admin Login with wrong password
	badLogin, _ := json.Marshal(map[string]string{"username": "admin", "password": "wrong"})
	resp, _ = client.Post(baseURL+"/api/admin/login", "application/json", bytes.NewReader(badLogin))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for wrong password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Test Admin Login with correct password
	goodLogin, _ := json.Marshal(map[string]string{"username": "admin", "password": adminPass})
	resp, err = client.Post(baseURL+"/api/admin/login", "application/json", bytes.NewReader(goodLogin))
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Login failed: %v, status: %d", err, resp.StatusCode)
	}
	var loginData struct {
		Token string `json:"token"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&loginData)
	resp.Body.Close()
	if loginData.Token == "" {
		t.Fatalf("Expected admin token, got empty")
	}
	t.Log("[PASS] Admin login succeeded")

	// 5. Test Add Node
	createNodePayload, _ := json.Marshal(map[string]string{
		"name":   "Test-Tokyo-Node",
		"group":  "Production",
		"region": "JP",
	})
	req, _ := http.NewRequest(http.MethodPost, baseURL+"/api/admin/nodes", bytes.NewReader(createNodePayload))
	req.Header.Set("Authorization", "Bearer "+loginData.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err = client.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Create node failed: %v", err)
	}
	var nodeResp struct {
		Node struct {
			UUID  string `json:"uuid"`
			Token string `json:"token"`
		} `json:"node"`
	}
	_ = json.NewDecoder(resp.Body).Decode(&nodeResp)
	resp.Body.Close()
	nodeUUID := nodeResp.Node.UUID
	nodeToken := nodeResp.Node.Token
	if nodeUUID == "" || nodeToken == "" {
		t.Fatalf("Failed to retrieve new node UUID/token")
	}
	t.Logf("[PASS] Created node UUID: %s, Token: %s", nodeUUID, nodeToken)

	// 6. Test VibeMonitor v2 JSON-RPC Agent Ingestion
	// Upload BasicInfo
	basicInfo := protocol.BasicInfo{
		CPUName:  "Intel Xeon",
		CPUCores: 10,
		Arch:     "amd64",
		OS:       "Linux",
		MemTotal: 32 * 1024 * 1024 * 1024,
	}
	basicReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodAgentBasicInfo,
		Params:  mustMarshal(protocol.BasicInfoParams{Info: basicInfo}),
		ID:      1,
	}
	rpcResp := postRPCWithClient(t, client, baseURL, nodeToken, basicReq)
	if rpcResp.Error != nil {
		t.Fatalf("BasicInfo RPC error: %v", rpcResp.Error)
	}
	t.Log("[PASS] agent.basicInfo reported successfully")

	// Test setting PingTargets via /api/admin/settings
	settingsPayload, _ := json.Marshal(map[string]any{
		"site_title": "VibeMonitor",
		"ping_targets": []protocol.PingTarget{
			{Name: "上海电信", Host: "180.153.28.1:80"},
		},
	})
	sReq, _ := http.NewRequest(http.MethodPost, baseURL+"/api/admin/settings", bytes.NewReader(settingsPayload))
	sReq.Header.Set("Authorization", "Bearer "+loginData.Token)
	sReq.Header.Set("Content-Type", "application/json")
	sResp, err := client.Do(sReq)
	if err != nil || sResp.StatusCode != http.StatusOK {
		t.Fatalf("Admin settings failed: %v", err)
	}
	sResp.Body.Close()

	// Public settings must not expose ping target addresses.
	pResp, _ := client.Get(baseURL + "/api/public")
	var pubData map[string]any
	_ = json.NewDecoder(pResp.Body).Decode(&pubData)
	pResp.Body.Close()
	if _, ok := pubData["ping_targets"]; ok {
		t.Fatalf("Public settings leaked ping targets: %v", pubData["ping_targets"])
	}
	t.Log("[PASS] Public settings do not expose ping target addresses")

	// Upload Report with PingResults
	report := protocol.Report{
		CPU: protocol.CPUReport{
			Usage: 25.5,
			Cores: 10,
		},
		RAM: protocol.RAMReport{
			Total: 32 * 1024 * 1024 * 1024,
			Used:  8 * 1024 * 1024 * 1024,
		},
		Disk: protocol.DiskReport{
			Total: 500 * 1024 * 1024 * 1024,
			Used:  150 * 1024 * 1024 * 1024,
		},
		Network: protocol.NetworkReport{
			Up:        1024 * 1024,
			Down:      5 * 1024 * 1024,
			TotalUp:   100 * 1024 * 1024,
			TotalDown: 500 * 1024 * 1024,
		},
		PingResults: []protocol.PingResult{
			{Name: "上海电信", Host: "180.153.28.1:80", Latency: 38},
		},
		Uptime: 3600,
	}
	reportReq := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  protocol.MethodAgentReport,
		Params:  mustMarshal(protocol.ReportParams{Report: report}),
		ID:      2,
	}
	rpcResp = postRPCWithClient(t, client, baseURL, nodeToken, reportReq)
	if rpcResp.Error != nil {
		t.Fatalf("Report RPC error: %v", rpcResp.Error)
	}
	t.Log("[PASS] agent.report with PingResults ingested successfully")

	// 7. Verify /api/nodes reflects the reported metrics
	resp, _ = client.Get(baseURL + "/api/nodes")
	var nodesList []map[string]any
	_ = json.NewDecoder(resp.Body).Decode(&nodesList)
	resp.Body.Close()
	if len(nodesList) != 1 {
		t.Fatalf("Expected 1 node, got %d", len(nodesList))
	}
	n := nodesList[0]
	if n["online"] != true {
		t.Fatalf("Node should be marked online")
	}
	lastRep, ok := n["last_report"].(map[string]any)
	if !ok {
		t.Fatalf("Expected last_report in node")
	}
	pResults, ok := lastRep["ping_results"].([]any)
	if !ok || len(pResults) != 1 {
		t.Fatalf("Expected 1 ping_result in last_report, got %v", lastRep["ping_results"])
	}
	t.Log("[PASS] Node verified online with correct metrics & ping_results in /api/nodes")

	// 7b. Verify /api/nodes/ping-history and JSON file persistence
	resp, err = client.Get(baseURL + "/api/nodes/ping-history?uuid=" + nodeUUID + "&target=%E4%B8%8A%E6%B5%B7%E7%94%B5%E4%BF%A1&range=1h")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Failed to fetch ping history: %v, status: %d", err, resp.StatusCode)
	}
	var pingHist store.PingHistoryResponse
	_ = json.NewDecoder(resp.Body).Decode(&pingHist)
	resp.Body.Close()

	if len(pingHist.Samples) != 1 {
		t.Fatalf("Expected 1 ping sample in history, got %d", len(pingHist.Samples))
	}
	if pingHist.Samples[0].Latency != 38 {
		t.Fatalf("Expected latency 38, got %d", pingHist.Samples[0].Latency)
	}
	if pingHist.Stats.Current != 38 || pingHist.Stats.Min != 38 || pingHist.Stats.Max != 38 {
		t.Fatalf("Incorrect ping stats: %+v", pingHist.Stats)
	}
	t.Log("[PASS] /api/nodes/ping-history verified successfully")

	// Verify configuration backup and SQLite persistence on disk
	diskData, err := os.ReadFile(dataFile)
	if err != nil {
		t.Fatalf("Failed to read JSON data file from disk: %v", err)
	}
	if strings.Contains(string(diskData), "ping_history") {
		t.Fatal("main file still contains ping history")
	}
	if _, err := os.Stat(strings.TrimSuffix(dataFile, ".json") + ".db"); err != nil {
		t.Fatalf("SQLite database missing: %v", err)
	}
	t.Log("[PASS] Ping history verified successfully persisted in SQLite database")

	// 8. Test /install.sh
	resp, err = client.Get(baseURL + "/install.sh?token=" + nodeToken)
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("install.sh failed: %v", err)
	}
	scriptBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(scriptBytes, []byte(nodeToken)) {
		t.Fatalf("install.sh does not contain node token")
	}
	t.Log("[PASS] /install.sh dynamic generation verified")

	// 9. Test Embedded Web UI
	resp, err = client.Get(baseURL + "/")
	if err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("Web UI root failed: %v", err)
	}
	uiBytes, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if !bytes.Contains(uiBytes, []byte("VibeMonitor")) {
		t.Fatalf("Web UI does not contain VibeMonitor title")
	}
	t.Log("[PASS] Embedded Web UI rendered successfully")
}

func mustMarshal(v any) json.RawMessage {
	b, _ := json.Marshal(v)
	return b
}

func postRPCWithClient(t *testing.T, client *http.Client, baseURL, token string, req protocol.Request) protocol.Response {
	b, _ := json.Marshal(req)
	url := fmt.Sprintf("%s/api/clients/v2/rpc?token=%s", baseURL, token)
	resp, err := client.Post(url, "application/json", bytes.NewReader(b))
	if err != nil {
		t.Fatalf("Failed to post RPC: %v", err)
	}
	defer resp.Body.Close()

	var rpcResp protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		t.Fatalf("Failed to decode RPC response: %v", err)
	}
	return rpcResp
}

func TestGracefulShutdownAndPersistence(t *testing.T) {
	dataFile := filepath.Join(t.TempDir(), "data.json")

	srv, err := server.New(server.Options{
		ListenAddr:    "127.0.0.1:19876",
		DataFile:      dataFile,
		AdminPassword: "shutdownPassword",
	})
	if err != nil {
		t.Fatalf("Failed to initialize server: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	errCh := make(chan error, 1)
	go func() {
		errCh <- srv.Run(ctx)
	}()

	time.Sleep(50 * time.Millisecond)

	// Trigger cancel (simulating SIGTERM)
	cancel()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("Server shutdown with error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("Server did not shut down gracefully within timeout")
	}

	// Verify data file exists on disk
	if _, err := os.Stat(dataFile); err != nil {
		t.Fatalf("Expected data file to exist after shutdown: %v", err)
	}
	t.Log("[PASS] Graceful shutdown and persistence verified")
}
