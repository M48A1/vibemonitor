package store

import (
	"os"
	"sync"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestStoreConcurrency(t *testing.T) {
	dataFile := "test-store-concurrency.json"
	_ = os.Remove(dataFile)
	defer os.Remove(dataFile)

	st, err := New(dataFile, "pass123")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	if !st.VerifyAdminPassword("pass123") {
		t.Fatalf("Password verification failed")
	}

	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			node, err := st.CreateNode("node", "default", "US")
			if err != nil {
				t.Errorf("CreateNode failed: %v", err)
				return
			}
			_, _ = st.IngestReport(node.Token, protocol.Report{
				CPU: protocol.CPUReport{Usage: float64(idx * 5)},
				RAM: protocol.RAMReport{Total: 1024, Used: 512},
				UpdatedAt: time.Now(),
			}, "127.0.0.1")
		}(i)
	}
	wg.Wait()

	nodes := st.GetNodes()
	if len(nodes) != 10 {
		t.Fatalf("Expected 10 nodes, got %d", len(nodes))
	}
}

func TestGetBillingCycleRange(t *testing.T) {
	// Case 1: today is Sep 4, resetDay is 15 -> cycle is Aug 15 to Sep 15
	now := time.Date(2026, 9, 4, 12, 0, 0, 0, time.UTC)
	start, end := GetBillingCycleRange(15, now)
	if start.Year() != 2026 || start.Month() != 8 || start.Day() != 15 {
		t.Fatalf("Expected start 2026-08-15, got %v", start)
	}
	if end.Year() != 2026 || end.Month() != 9 || end.Day() != 15 {
		t.Fatalf("Expected end 2026-09-15, got %v", end)
	}

	// Case 2: today is Sep 20, resetDay is 15 -> cycle is Sep 15 to Oct 15
	now2 := time.Date(2026, 9, 20, 12, 0, 0, 0, time.UTC)
	start2, end2 := GetBillingCycleRange(15, now2)
	if start2.Year() != 2026 || start2.Month() != 9 || start2.Day() != 15 {
		t.Fatalf("Expected start 2026-09-15, got %v", start2)
	}
	if end2.Year() != 2026 || end2.Month() != 10 || end2.Day() != 15 {
		t.Fatalf("Expected end 2026-10-15, got %v", end2)
	}
}

func TestTrafficAccounting(t *testing.T) {
	dataFile := "test-store-traffic.json"
	_ = os.Remove(dataFile)
	defer os.Remove(dataFile)

	st, err := New(dataFile, "pass123")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	// Create node: 1000GB limit, 15th reset day, 250GB initial used
	node, err := st.CreateNodeWithOptions(NodeOptions{
		Name:           "HK-VPS",
		TrafficLimitGB: 1000,
		ResetDay:       15,
		InitialUsedGB:  250,
	})
	if err != nil {
		t.Fatalf("CreateNodeWithOptions failed: %v", err)
	}

	// Initial check
	fetched := st.GetNode(node.UUID)
	const GB = int64(1024 * 1024 * 1024)
	if fetched.CycleTotalUsed != 250*GB {
		t.Fatalf("Expected 250GB used, got %d", fetched.CycleTotalUsed/GB)
	}
	if fetched.CycleRemaining != 750*GB {
		t.Fatalf("Expected 750GB remaining, got %d", fetched.CycleRemaining/GB)
	}

	// Ingest first report: TotalUp = 10GB, TotalDown = 15GB
	_, err = st.IngestReport(node.Token, protocol.Report{
		Network: protocol.NetworkReport{
			TotalUp:   10 * GB,
			TotalDown: 15 * GB,
		},
		UpdatedAt: time.Now(),
	}, "1.1.1.1")
	if err != nil {
		t.Fatalf("IngestReport failed: %v", err)
	}

	// First report establishes baseline, no delta yet
	fetched = st.GetNode(node.UUID)
	if fetched.CycleTotalUsed != 250*GB {
		t.Fatalf("Expected 250GB used after baseline, got %d", fetched.CycleTotalUsed/GB)
	}

	// Ingest second report: TotalUp = 12GB (+2GB), TotalDown = 18GB (+3GB) -> delta = 5GB
	_, err = st.IngestReport(node.Token, protocol.Report{
		Network: protocol.NetworkReport{
			TotalUp:   12 * GB,
			TotalDown: 18 * GB,
		},
		UpdatedAt: time.Now(),
	}, "1.1.1.1")
	if err != nil {
		t.Fatalf("IngestReport failed: %v", err)
	}

	fetched = st.GetNode(node.UUID)
	expectedUsed := (250 + 5) * GB
	if fetched.CycleTotalUsed != expectedUsed {
		t.Fatalf("Expected %d bytes used, got %d", expectedUsed, fetched.CycleTotalUsed)
	}
	expectedRemaining := (1000 - 255) * GB
	if fetched.CycleRemaining != expectedRemaining {
		t.Fatalf("Expected %d bytes remaining, got %d", expectedRemaining, fetched.CycleRemaining)
	}

	// Simulate cycle rollover (reaching next month's 15th)
	futureDate := time.Date(2026, 9, 16, 12, 0, 0, 0, time.UTC)
	st.mu.Lock()
	nodeInternal := st.nodes[node.UUID]
	nodeInternal.checkCycleRollover(futureDate)
	st.mu.Unlock()

	// InitialUsed should now be 0, CurrentCycleUsed should be 0
	st.mu.RLock()
	nodeInternal = st.nodes[node.UUID]
	if nodeInternal.InitialUsed != 0 {
		t.Fatalf("Expected InitialUsed to reset to 0, got %d", nodeInternal.InitialUsed)
	}
	if nodeInternal.CurrentCycleUsed != 0 {
		t.Fatalf("Expected CurrentCycleUsed to reset to 0, got %d", nodeInternal.CurrentCycleUsed)
	}
	st.mu.RUnlock()
}

func TestPingMonitoring(t *testing.T) {
	dataFile := "test-store-ping.json"
	_ = os.Remove(dataFile)
	defer os.Remove(dataFile)

	st, err := New(dataFile, "pass123")
	if err != nil {
		t.Fatalf("Failed to create store: %v", err)
	}

	targets := []protocol.PingTarget{
		{Name: "上海电信", Host: "180.153.28.1:80"},
		{Name: "广州移动", Host: "120.196.165.2:80"},
	}

	if err := st.UpdateConfig("", "", "", targets); err != nil {
		t.Fatalf("UpdateConfig with targets failed: %v", err)
	}

	cfg := st.GetConfig()
	if len(cfg.PingTargets) != 2 {
		t.Fatalf("Expected 2 ping targets, got %d", len(cfg.PingTargets))
	}
	if cfg.PingTargets[0].Name != "上海电信" || cfg.PingTargets[1].Name != "广州移动" {
		t.Fatalf("Unexpected targets: %+v", cfg.PingTargets)
	}

	node, err := st.CreateNode("Tokyo-01", "Prod", "JP")
	if err != nil {
		t.Fatalf("CreateNode failed: %v", err)
	}

	pingResults := []protocol.PingResult{
		{Name: "上海电信", Host: "180.153.28.1:80", Latency: 45},
		{Name: "广州移动", Host: "120.196.165.2:80", Latency: 58},
	}

	report := protocol.Report{
		CPU:         protocol.CPUReport{Usage: 12.0},
		RAM:         protocol.RAMReport{Total: 2048, Used: 1024},
		PingResults: pingResults,
		UpdatedAt:   time.Now(),
	}

	_, err = st.IngestReport(node.Token, report, "1.2.3.4")
	if err != nil {
		t.Fatalf("IngestReport failed: %v", err)
	}

	fetched := st.GetNode(node.UUID)
	if fetched.LastReport == nil || len(fetched.LastReport.PingResults) != 2 {
		t.Fatalf("Expected 2 PingResults in LastReport, got %+v", fetched.LastReport)
	}
	if fetched.LastReport.PingResults[0].Latency != 45 {
		t.Fatalf("Expected latency 45, got %d", fetched.LastReport.PingResults[0].Latency)
	}
}
