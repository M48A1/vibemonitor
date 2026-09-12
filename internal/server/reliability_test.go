package server

import (
	"compress/gzip"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionRevocationAndFailedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(Options{DataFile: path, AdminPassword: "original"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	h := s.Handler()
	call := func(method, path, body, token string, cookie bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, path, strings.NewReader(body))
		if cookie {
			r.AddCookie(&http.Cookie{Name: "admin_token", Value: token})
		} else if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	login := func() string {
		w := call("POST", "/api/admin/login", `{"username":"admin","password":"original"}`, "", false)
		var v struct{ Token string }
		json.Unmarshal(w.Body.Bytes(), &v)
		if v.Token == "" {
			t.Fatal(w.Body.String())
		}
		return v.Token
	}
	first := login()
	w := call("POST", "/api/admin/logout", "", first, true)
	if w.Code != 200 || len(w.Result().Cookies()) == 0 || w.Result().Cookies()[0].MaxAge != -1 {
		t.Fatal("cookie logout failed")
	}
	if strings.Contains(call("GET", "/api/admin/status", "", first, false).Body.String(), `"is_admin":true`) {
		t.Fatal("logged-out token remains valid")
	}
	second, third := login(), login()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TRIGGER block_config BEFORE INSERT ON config BEGIN SELECT RAISE(ABORT,'write failure'); END`); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/api/admin/settings", `{"site_title":"lost","new_password":"changed"}`, second, false)
	if w.Code != 500 || s.store.GetConfig().SiteTitle == "lost" || !s.store.VerifyAdminPassword("original") {
		t.Fatal("failed settings were accepted")
	}
	if _, err := db.Exec("DROP TRIGGER block_config"); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/api/admin/settings", `{"new_password":"changed"}`, second, false)
	if w.Code != 200 || !s.store.VerifyAdminPassword("changed") {
		t.Fatal("password change failed")
	}
	for _, token := range []string{second, third} {
		if strings.Contains(call("GET", "/api/admin/status", "", token, false).Body.String(), `"is_admin":true`) {
			t.Fatal("old session survived password change")
		}
	}
}

type unreadableBody struct{ t *testing.T }

func (b unreadableBody) Read([]byte) (int, error) {
	b.t.Fatal("invalid token request body was read")
	return 0, nil
}
func (unreadableBody) Close() error { return nil }

func TestRequestLimitsAndEarlyAuthentication(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	h := s.Handler()
	r := httptest.NewRequest("POST", "/api/clients/v2/rpc", nil)
	r.Body = unreadableBody{t}
	r.Header.Set("Authorization", "Bearer invalid")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 401 {
		t.Fatal(w.Code)
	}
	n, err := s.store.CreateNode("test", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r = httptest.NewRequest("POST", "/api/clients/v2/rpc", strings.NewReader(strings.Repeat("x", maxRequestBytes+1)))
	r.Header.Set("Authorization", "Bearer "+n.Token)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != 413 {
		t.Fatalf("oversized request status: %d", w.Code)
	}
	for i := 0; i < 11; i++ {
		r = httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"admin","password":"wrong"}`))
		r.Header.Set("X-Forwarded-For", string(rune('a'+i)))
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		want := 401
		if i == 10 {
			want = 429
		}
		if w.Code != want {
			t.Fatalf("login attempt %d got %d", i, w.Code)
		}
	}
}

func TestGzipCompression(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(Options{DataFile: path, AdminPassword: "password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	h := s.Handler()

	// 1. Request with Accept-Encoding: gzip to /api/public
	r := httptest.NewRequest("GET", "/api/public", nil)
	r.Header.Set("Accept-Encoding", "gzip")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)

	if w.Code != 200 {
		t.Fatalf("expected 200, got %d", w.Code)
	}
	if w.Header().Get("Content-Encoding") != "gzip" {
		t.Fatalf("expected Content-Encoding: gzip, got %q", w.Header().Get("Content-Encoding"))
	}

	gzReader, err := gzip.NewReader(w.Body)
	if err != nil {
		t.Fatalf("failed to create gzip reader: %v", err)
	}
	defer gzReader.Close()
	decompressed, err := io.ReadAll(gzReader)
	if err != nil {
		t.Fatalf("failed to decompress gzip body: %v", err)
	}
	if !strings.Contains(string(decompressed), "VibeMonitor") {
		t.Fatalf("decompressed body missing VibeMonitor: %s", string(decompressed))
	}

	// 2. Request without Accept-Encoding: gzip
	rNoGzip := httptest.NewRequest("GET", "/api/public", nil)
	wNoGzip := httptest.NewRecorder()
	h.ServeHTTP(wNoGzip, rNoGzip)
	if wNoGzip.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("unexpected Content-Encoding: gzip when client did not accept it")
	}

	// 3. /ping endpoint should not be compressed
	rPing := httptest.NewRequest("GET", "/ping", nil)
	rPing.Header.Set("Accept-Encoding", "gzip")
	wPing := httptest.NewRecorder()
	h.ServeHTTP(wPing, rPing)
	if wPing.Header().Get("Content-Encoding") == "gzip" {
		t.Fatal("/ping should not be gzip compressed")
	}
	if wPing.Body.String() != "pong" {
		t.Fatalf("expected pong, got %s", wPing.Body.String())
	}
}
