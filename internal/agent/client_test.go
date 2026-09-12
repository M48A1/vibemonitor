//go:build linux && amd64

package agent

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"vibemonitor/pkg/protocol"
)

func TestBasicInfoRetriesPastFiveFailuresAndReconnect(t *testing.T) {
	var attempts, reports atomic.Int32
	refreshed := make(chan struct{}, 2)
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req protocol.Request
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			return
		}
		if req.Method == protocol.MethodAgentBasicInfo {
			attempt := attempts.Add(1)
			if attempt <= 6 {
				http.Error(w, "offline", 503)
				return
			}
			refreshed <- struct{}{}
		} else if req.Method == protocol.MethodAgentReport && attempts.Load() >= 7 && reports.Add(1) == 1 {
			http.Error(w, "disconnected", 503)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"success"}}`))
	}))
	defer s.Close()
	c := New(Options{ServerURL: s.URL, Token: "test", Interval: time.Millisecond})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	for i := 0; i < 2; i++ {
		select {
		case <-refreshed:
		case <-time.After(5 * time.Second):
			t.Fatal("basic info was not retried after failures/reconnect")
		}
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("agent did not stop")
	}
}

func TestCancelInitialBasicInfoRequest(t *testing.T) {
	started := make(chan struct{})
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
	}))
	defer s.Close()
	c := New(Options{ServerURL: s.URL, Token: "test"})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- c.Run(ctx) }()
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("request not started")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("request ignored cancellation")
	}
}

func TestTokenOnlyInHeader(t *testing.T) {
	s := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.RawQuery != "" {
			t.Error("token or parameters leaked into URL")
		}
		if r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("missing authorization")
		}
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"jsonrpc":"2.0","id":1,"result":{"status":"success"}}`))
	}))
	defer s.Close()
	c := New(Options{ServerURL: s.URL, Token: "test-secret"})
	if _, err := c.postRPC("agent.pull", struct{}{}); err != nil {
		t.Fatal(err)
	}
}

func TestTCPMeasurementLabelsProtocol(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	latency, method := pingHost(listener.Addr().String(), time.Second)
	if latency < 0 || method != "tcp" {
		t.Fatalf("measurement: %d %s", latency, method)
	}
}

func TestMeasurePingsConcurrency(t *testing.T) {
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()

	c := New(Options{ServerURL: "http://127.0.0.1", Token: "test"})

	// Create 12 targets, exceeding maxPingConcurrency (8) to exercise semaphore
	targets := make([]protocol.PingTarget, 12)
	for i := 0; i < 12; i++ {
		targets[i] = protocol.PingTarget{
			Name: "target",
			Host: listener.Addr().String(),
		}
	}
	c.pingTargets = targets

	c.measurePings()

	c.mu.RLock()
	defer c.mu.RUnlock()
	if len(c.pingResults) != 12 {
		t.Fatalf("expected 12 ping results, got %d", len(c.pingResults))
	}
	for i, res := range c.pingResults {
		if res.Latency < 0 || res.Method != "tcp" {
			t.Errorf("result %d unexpected: %+v", i, res)
		}
	}
}
