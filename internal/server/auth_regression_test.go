package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"
)

func TestAgentRPCDoesNotRecordConnectionOrReportedIP(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := s.store.CreateNode("test", "", "JP")
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, "/api/clients/v2/rpc", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"agent.basicInfo","params":{"info":{"os":"Linux","ipv4":"203.0.113.9","ipv6":"2001:db8::9"}}}`))
	r.RemoteAddr = "198.51.100.7:1234"
	r.Header.Set("X-Forwarded-For", "192.0.2.8")
	r.Header.Set("Authorization", "Bearer "+node.Token)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusOK {
		t.Fatalf("agent report failed: %d %s", w.Code, w.Body.String())
	}
	stored, err := json.Marshal(s.store.GetNode(node.UUID))
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{"client_ip", "ipv4", "ipv6", "198.51.100.7", "192.0.2.8", "203.0.113.9", "2001:db8::9"} {
		if strings.Contains(string(stored), value) {
			t.Fatalf("stored node still contains IP data %q", value)
		}
	}
}

func TestVisitorIPEndpointRemoved(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	r := httptest.NewRequest(http.MethodGet, "/api/visitor-ip", nil)
	w := httptest.NewRecorder()
	s.Handler().ServeHTTP(w, r)
	if w.Code != http.StatusNotFound {
		t.Fatalf("visitor IP endpoint status = %d, want 404", w.Code)
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
