package server

import (
	"bytes"
	"compress/gzip"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
	"time"
)

func TestAdminTokenRequiresBearerScheme(t *testing.T) {
	for auth, want := range map[string]string{
		"Bearer secret": "secret",
		"Basic secret":  "",
		"secret":        "",
	} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.Header.Set("Authorization", auth)
		r.AddCookie(&http.Cookie{Name: "admin_token", Value: "cookie-secret"})
		if got := adminToken(r); got != want {
			t.Errorf("Authorization %q: got %q, want %q", auth, got, want)
		}
	}
}

func TestRequestHTTPSInvalidRemoteAddress(t *testing.T) {
	for _, remote := range []string{"invalid", "hostname:1234", ""} {
		r := httptest.NewRequest(http.MethodGet, "/", nil)
		r.RemoteAddr = remote
		r.Header.Set("X-Forwarded-Proto", "https")
		if requestHTTPS(r) {
			t.Errorf("trusted invalid remote address %q", remote)
		}
	}
}

func TestLoginLimiterAdmitsNewAddressAtCapacity(t *testing.T) {
	now := time.Now()
	l := loginLimiter{clients: make(map[string]loginWindow)}
	for i := 0; i < maxLoginClients; i++ {
		expiry := now.Add(5 * time.Minute)
		if i == 0 {
			expiry = now.Add(time.Minute)
		}
		l.clients["client-"+strconv.Itoa(i)] = loginWindow{count: 1, expires: expiry}
	}
	r := httptest.NewRequest(http.MethodPost, "/api/admin/login", nil)
	r.RemoteAddr = "198.51.100.42:1234"
	if !l.allow(r) {
		t.Fatal("new address denied when client map was full")
	}
	if len(l.clients) != maxLoginClients || l.clients["198.51.100.42"].count != 1 {
		t.Fatal("limiter did not keep a bounded, active window for the new address")
	}
	if _, ok := l.clients["client-0"]; ok {
		t.Fatal("oldest window was not evicted")
	}
}

func TestGzipMiddlewarePreservesEncodedResponse(t *testing.T) {
	var compressed bytes.Buffer
	gz := gzip.NewWriter(&compressed)
	if _, err := gz.Write([]byte("already encoded")); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	h := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Encoding", "gzip")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(compressed.Bytes())
	}))
	r := httptest.NewRequest(http.MethodGet, "/asset", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusOK || w.Header().Get("Content-Encoding") != "gzip" || !bytes.Equal(w.Body.Bytes(), compressed.Bytes()) {
		t.Fatal("middleware changed an already encoded response")
	}
}
