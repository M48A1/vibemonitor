//go:build linux && amd64

package agent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"time"

	"vibemonitor/pkg/monitor"
	"vibemonitor/pkg/protocol"
)

type Options struct {
	ServerURL string
	Token     string
	Interval  time.Duration
	Name      string
}

type Client struct {
	serverURL  string
	token      string
	interval   time.Duration
	collector  monitor.Collector
	httpClient *http.Client

	mu          sync.RWMutex
	pingTargets []protocol.PingTarget
	pingResults []protocol.PingResult
	pingTrigger chan struct{}
}

func New(opts Options) *Client {
	if opts.Interval <= 0 {
		opts.Interval = 3 * time.Second
	}
	serverURL := strings.TrimRight(opts.ServerURL, "/")

	return &Client{
		serverURL: serverURL,
		token:     opts.Token,
		interval:  opts.Interval,
		collector: monitor.NewCollector(),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		pingTrigger: make(chan struct{}, 1),
	}
}

func (c *Client) postRPC(method string, params any) (*protocol.Response, error) {
	paramsRaw, err := json.Marshal(params)
	if err != nil {
		return nil, err
	}

	req := protocol.Request{
		JSONRPC: protocol.JSONRPCVersion,
		Method:  method,
		Params:  paramsRaw,
		ID:      fmt.Sprintf("%s-%d", method, time.Now().UnixNano()),
	}

	body, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	endpoint := c.serverURL + "/api/clients/v2/rpc"
	httpReq, err := http.NewRequest(http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("server returned status %d: %s", resp.StatusCode, string(b))
	}

	var rpcResp protocol.Response
	if err := json.NewDecoder(resp.Body).Decode(&rpcResp); err != nil {
		return nil, err
	}
	if rpcResp.Error != nil {
		return nil, fmt.Errorf("RPC error (%d): %s", rpcResp.Error.Code, rpcResp.Error.Message)
	}

	return &rpcResp, nil
}

func (c *Client) Run(ctx context.Context) error {
	log.Printf("[Agent] Starting VibeMonitor agent probe...")
	log.Printf("[Agent] Target server: %s", c.serverURL)

	// 1. Initial Basic Info report
	basicInfo, err := c.collector.GetBasicInfo()
	basicInfo.ReportIntervalSeconds = c.interval.Seconds()
	if err != nil {
		log.Printf("[Agent] Warning: failed to gather basic info: %v", err)
	} else {
		for retries := 0; retries < 5; retries++ {
			resp, err := c.postRPC(protocol.MethodAgentBasicInfo, protocol.BasicInfoParams{Info: basicInfo})
			if err == nil {
				log.Printf("[Agent] Reported BasicInfo successfully: %s (%s)", basicInfo.OS, basicInfo.CPUName)
				if resp != nil {
					c.handleRPCResult(resp.Result)
				}
				break
			}
			log.Printf("[Agent] Failed to upload basic info (attempt %d/5): %v. Retrying in 2s...", retries+1, err)
			time.Sleep(2 * time.Second)
		}
	}

	// 2. Start Ping monitoring worker
	go c.runPingWorker(ctx)

	// 3. Metrics Reporting loop
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	// Capture interrupt signals
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Agent] Stopped by context.")
			return nil
		case <-sigCh:
			log.Printf("[Agent] Exiting cleanly on interrupt signal.")
			return nil
		case <-ticker.C:
			report, err := c.collector.GetReport()
			if err != nil {
				log.Printf("[Agent] Error collecting metrics: %v", err)
				continue
			}

			report.ReportIntervalSeconds = c.interval.Seconds()

			// Attach latest ping results
			c.mu.RLock()
			if len(c.pingResults) > 0 {
				report.PingResults = c.pingResults
			}
			c.mu.RUnlock()

			resp, err := c.postRPC(protocol.MethodAgentReport, protocol.ReportParams{Report: report})
			if err != nil {
				log.Printf("[Agent] Report failed: %v", err)
			} else if resp != nil {
				c.handleRPCResult(resp.Result)
			}
		}
	}
}

func (c *Client) handleRPCResult(result any) {
	if result == nil {
		return
	}
	m, ok := result.(map[string]any)
	if !ok {
		return
	}
	rawTargets, exists := m["ping_targets"]
	if !exists || rawTargets == nil {
		return
	}

	b, err := json.Marshal(rawTargets)
	if err != nil {
		return
	}
	var newTargets []protocol.PingTarget
	if err := json.Unmarshal(b, &newTargets); err != nil {
		return
	}

	c.mu.Lock()
	changed := !reflect.DeepEqual(c.pingTargets, newTargets)
	if changed {
		c.pingTargets = newTargets
	}
	c.mu.Unlock()

	if changed {
		select {
		case c.pingTrigger <- struct{}{}:
		default:
		}
	}
}

func (c *Client) runPingWorker(ctx context.Context) {
	ticker := time.NewTicker(25 * time.Second)
	defer ticker.Stop()

	// Initial probe after 2s
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Second):
		c.measurePings()
	}

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.pingTrigger:
			c.measurePings()
		case <-ticker.C:
			c.measurePings()
		}
	}
}

func (c *Client) measurePings() {
	c.mu.RLock()
	targets := make([]protocol.PingTarget, len(c.pingTargets))
	copy(targets, c.pingTargets)
	c.mu.RUnlock()

	if len(targets) == 0 {
		c.mu.Lock()
		c.pingResults = nil
		c.mu.Unlock()
		return
	}

	var wg sync.WaitGroup
	results := make([]protocol.PingResult, len(targets))
	for i, t := range targets {
		wg.Add(1)
		go func(idx int, target protocol.PingTarget) {
			defer wg.Done()
			latency, method := pingHost(target.Host, 2*time.Second)
			results[idx] = protocol.PingResult{
				Name:    target.Name,
				Host:    target.Host,
				Latency: latency,
				Method:  method,
			}
		}(i, t)
	}
	wg.Wait()

	c.mu.Lock()
	c.pingResults = results
	c.mu.Unlock()
}

func pingHost(host string, timeout time.Duration) (int, string) {
	host = strings.TrimSpace(host)
	if host == "" {
		return -1, "tcp"
	}

	// 1. If host has port (e.g. "1.2.3.4:80" or "example.com:443"), do TCP connect
	if strings.Contains(host, ":") {
		start := time.Now()
		conn, err := net.DialTimeout("tcp4", host, timeout)
		if err == nil {
			_ = conn.Close()
			return int(time.Since(start).Milliseconds()), "tcp"
		}
		return -1, "tcp"
	}

	// 2. Pure IP or hostname without port: Try system ICMP ping first
	if ms := execSystemPing(host, timeout); ms >= 0 {
		return ms, "icmp"
	}

	// Fallback to TCP port 80, then 443
	for _, port := range []string{"80", "443"} {
		start := time.Now()
		conn, err := net.DialTimeout("tcp4", net.JoinHostPort(host, port), timeout)
		if err == nil {
			_ = conn.Close()
			return int(time.Since(start).Milliseconds()), "tcp"
		}
	}

	return -1, "tcp"
}

func execSystemPing(host string, timeout time.Duration) int {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, "ping", "-4", "-c", "1", "-W", "2", host)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return -1
	}
	s := string(out)
	if idx := strings.Index(s, "time="); idx != -1 {
		after := s[idx+5:]
		var ms float64
		if _, err := fmt.Sscanf(after, "%f", &ms); err == nil {
			return int(ms)
		}
	}
	return -1
}
