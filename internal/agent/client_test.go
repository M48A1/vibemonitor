//go:build linux && amd64

package agent

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

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
