//go:build linux && (amd64 || arm64)

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
	ServerURL  string
	Token      string
	Interval   time.Duration
	Name       string
	Interfaces []string
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

const maxPingConcurrency = 8

func New(opts Options) *Client {
	if opts.Interval <= 0 {
		opts.Interval = 3 * time.Second
	}
	serverURL := strings.TrimRight(opts.ServerURL, "/")

	return &Client{
		serverURL: serverURL,
		token:     opts.Token,
		interval:  opts.Interval,
		collector: monitor.NewCollector(opts.Interfaces...),
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
			Transport: &http.Transport{
				Proxy: http.ProxyFromEnvironment,
				DialContext: (&net.Dialer{
					Timeout:   5 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          10,
				MaxIdleConnsPerHost:   5,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   5 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		},
		pingTrigger: make(chan struct{}, 1),
	}
}

func (c *Client) postRPC(method string, params any) (*protocol.Response, error) {
	return c.postRPCContext(context.Background(), method, params)
}

func (c *Client) postRPCContext(ctx context.Context, method string, params any) (*protocol.Response, error) {
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
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		return nil, err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

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
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	log.Printf("[Agent] Starting VibeMonitor agent probe...")
	log.Printf("[Agent] Target server: %s", c.serverURL)

	// Retry pending info after every successful metrics report; refresh periodically
	// even if the master was replaced without a visible connection failure.
	var lastBasicInfo time.Time
	reportBasicInfo := func() {
		info, err := c.collector.GetBasicInfo()
		if err == nil {
			info.ReportIntervalSeconds = c.interval.Seconds()
			var resp *protocol.Response
			resp, err = c.postRPCContext(ctx, protocol.MethodAgentBasicInfo, protocol.BasicInfoParams{Info: info})
			if err == nil {
				lastBasicInfo = time.Now()
				if resp != nil {
					c.handleRPCResult(resp.Result)
				}
				log.Printf("[Agent] Reported BasicInfo successfully: %s (%s)", info.OS, info.CPUName)
			}
		}
		if err != nil {
			log.Printf("[Agent] BasicInfo pending, will retry after reconnect: %v", err)
		}
	}
	reportBasicInfo()

	// 2. Start Ping monitoring worker
	go c.runPingWorker(ctx)

	// 3. Metrics Reporting loop
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()
	basicTicker := time.NewTicker(15 * time.Minute)
	defer basicTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			log.Printf("[Agent] Stopped by context.")
			return nil
		case <-basicTicker.C:
			reportBasicInfo()
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

			resp, err := c.postRPCContext(ctx, protocol.MethodAgentReport, protocol.ReportParams{Report: report})
			if err != nil {
				lastBasicInfo = time.Time{}
				log.Printf("[Agent] Report failed: %v", err)
			} else {
				if resp != nil {
					c.handleRPCResult(resp.Result)
				}
				if lastBasicInfo.IsZero() || time.Since(lastBasicInfo) >= 15*time.Minute {
					reportBasicInfo()
				}
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
	sem := make(chan struct{}, maxPingConcurrency)
	for i, t := range targets {
		wg.Add(1)
		sem <- struct{}{}
		go func(idx int, target protocol.PingTarget) {
			defer wg.Done()
			defer func() { <-sem }()
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
