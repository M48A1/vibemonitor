package server

import (
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestGetClientIPOnlyUsesValidForwardedIPFromLoopback(t *testing.T) {
	tests := []struct {
		name, remote, xff, realIP, want string
	}{
		{"trusted proxy", "127.0.0.1:1234", "198.51.100.7, 203.0.113.9", "", "198.51.100.7"},
		{"invalid XFF falls back", "127.0.0.1:1234", "fake-client", "203.0.113.9", "203.0.113.9"},
		{"invalid headers use peer", "127.0.0.1:1234", "fake-client", "also-fake", "127.0.0.1"},
		{"untrusted peer", "192.0.2.10:1234", "198.51.100.7", "203.0.113.9", "192.0.2.10"},
		{"IPv6 proxy", "[::1]:1234", "2001:db8::12", "", "2001:db8::12"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", nil)
			r.RemoteAddr = tt.remote
			r.Header.Set("X-Forwarded-For", tt.xff)
			r.Header.Set("X-Real-IP", tt.realIP)
			if got := getClientIP(r); got != tt.want {
				t.Fatalf("client IP = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRPCRejectsQueryToken(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.store.CreateNode("legacy", "", "")
	if err != nil {
		t.Fatal(err)
	}
	request := func(queryToken, headerName, headerToken string) int {
		t.Helper()
		path := "/api/clients/v2/rpc"
		if queryToken != "" {
			path += "?token=" + url.QueryEscape(queryToken)
		}
		r := httptest.NewRequest(http.MethodPost, path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"agent.pull"}`))
		if headerToken != "" {
			if headerName == "Authorization" {
				r.Header.Set(headerName, "Bearer "+headerToken)
			} else {
				r.Header.Set(headerName, headerToken)
			}
		}
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, r)
		return w.Code
	}
	if got := request("", "Authorization", n.Token); got != http.StatusOK {
		t.Fatalf("Authorization header stopped working: %d", got)
	}
	if got := request("wrong", "Authorization", n.Token); got != http.StatusUnauthorized {
		t.Fatalf("URL token was accepted alongside a valid header: %d", got)
	}
	if got := request(n.Token, "Authorization", "wrong"); got != http.StatusUnauthorized {
		t.Fatalf("invalid header unexpectedly fell back to URL token: %d", got)
	}
	if got := request(n.Token, "", ""); got != http.StatusUnauthorized {
		t.Fatalf("URL token authenticated without a header: %d", got)
	}
	if got := request("", "X-Token", n.Token); got != http.StatusOK {
		t.Fatalf("X-Token header stopped working: %d", got)
	}
}
