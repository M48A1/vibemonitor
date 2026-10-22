package server

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestRequestClientIPTrustBoundary(t *testing.T) {
	proxies, err := parseTrustedProxies("10.0.0.0/24,2001:db8:1::/64")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name, peer, forwarded, want string
	}{
		{"direct spoof", "198.51.100.1:1234", "203.0.113.1", "198.51.100.1"},
		{"no header", "10.0.0.1:1234", "", "10.0.0.1"},
		{"installer loopback proxy", "127.0.0.1:1234", "203.0.113.1", "203.0.113.1"},
		{"trusted chain", "10.0.0.1:1234", "203.0.113.1, 10.0.0.2", "203.0.113.1"},
		{"stop at untrusted hop", "10.0.0.1:1234", "203.0.113.1, 198.51.100.2, 10.0.0.2", "198.51.100.2"},
		{"invalid trusted suffix", "10.0.0.1:1234", "203.0.113.1, garbage", "10.0.0.1"},
		{"untrusted prefix ignored", "10.0.0.1:1234", "garbage, 203.0.113.1", "203.0.113.1"},
		{"mapped direct address", "[::ffff:198.51.100.1]:1234", "203.0.113.1", "198.51.100.1"},
		{"ipv6 chain", "[2001:db8:1::1]:1234", "2001:db8::10, 2001:db8:1::2", "2001:db8::10"},
		{"header zones rejected", "10.0.0.1:1234", "fe80::1%eth0", "10.0.0.1"},
		{"oversize chain", "10.0.0.1:1234", strings.Repeat("203.0.113.1,", 400), "10.0.0.1"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
			r.RemoteAddr = tc.peer
			r.Header.Set("X-Forwarded-For", tc.forwarded)
			if got := requestClientIP(r, proxies); got != tc.want {
				t.Fatalf("client IP = %q, want %q", got, tc.want)
			}
		})
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
	r.RemoteAddr = "127.0.0.1:1234"
	r.Header.Add("X-Forwarded-For", "203.0.113.1")
	r.Header.Add("X-Forwarded-For", "10.0.0.1")
	if got := requestClientIP(r, proxies); got != "203.0.113.1" {
		t.Fatalf("multiple forwarding headers: %q", got)
	}
}

func TestProxyLoginFailuresDoNotLockOtherClients(t *testing.T) {
	for _, peer := range []string{"127.0.0.1:50000", "10.0.0.1:50000"} {
		t.Run(peer, func(t *testing.T) {
			s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password", TrustedProxy: "10.0.0.1"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			handler := s.Handler()
			for i := 1; i <= 11; i++ {
				body := `{"username":"none","password":"invalid"}`
				want := http.StatusUnauthorized
				if i == 11 {
					body = `{"username":"admin","password":"test-password"}`
					want = http.StatusOK
				}
				r := httptest.NewRequest(http.MethodPost, "/api/admin/login", strings.NewReader(body))
				r.RemoteAddr = peer
				r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
				w := httptest.NewRecorder()
				handler.ServeHTTP(w, r)
				if w.Code != want {
					t.Fatalf("client %d: status %d, want %d; %s", i, w.Code, want, w.Body.String())
				}
			}
		})
	}
}

func TestLoginLimiterIgnoresUntrustedForwardedFor(t *testing.T) {
	var limiter loginLimiter
	for i := 1; i <= 11; i++ {
		r := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
		r.RemoteAddr = fmt.Sprintf("198.51.100.1:%d", 1234+i)
		r.Header.Set("X-Forwarded-For", fmt.Sprintf("203.0.113.%d", i))
		if allowed := limiter.allow(r); allowed != (i <= 10) {
			t.Fatalf("request %d allowed = %v", i, allowed)
		}
	}
	if len(limiter.clients) != 1 {
		t.Fatalf("forged headers created %d client buckets", len(limiter.clients))
	}
}
