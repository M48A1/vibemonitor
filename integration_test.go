package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"vibemonitor/internal/server"
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
	dataFile := "test-vibemonitor-data.json"
	_ = os.Remove(dataFile)
	defer os.Remove(dataFile)

	adminPass := "adminSecret123"

	srv, err := server.New(server.Options{
		ListenAddr:    "127.0.0.1:25774",
		DataFile:      dataFile,
		AdminPassword: adminPass,
	})
	if err != nil {
		t.Fatalf("Failed to create server: %v", err)
	}

	client := &http.Client{
		Transport: &memoryTransport{handler: srv.Handler()},
	}

	baseURL := "http://127.0.0.1:25774"

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
	badLogin, _ := json.Marshal(map[string]string{"password": "wrong"})
	resp, _ = client.Post(baseURL+"/api/admin/login", "application/json", bytes.NewReader(badLogin))
	if resp.StatusCode != http.StatusUnauthorized {
		t.Fatalf("Expected 401 for wrong password, got %d", resp.StatusCode)
	}
	resp.Body.Close()

	// 4. Test Admin Login with correct password
	goodLogin, _ := json.Marshal(map[string]string{"password": adminPass})
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
		CPUName:  "Apple M1 Max",
		CPUCores: 10,
		Arch:     "arm64",
		OS:       "Darwin",
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

	// Verify /api/public returns ping_targets
	pResp, _ := client.Get(baseURL + "/api/public")
	var pubData map[string]any
	_ = json.NewDecoder(pResp.Body).Decode(&pubData)
	pResp.Body.Close()
	pts, ok := pubData["ping_targets"].([]any)
	if !ok || len(pts) != 1 {
		t.Fatalf("Expected 1 ping target in /api/public, got %v", pubData["ping_targets"])
	}
	t.Log("[PASS] Ping targets configured and verified in /api/public")

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
