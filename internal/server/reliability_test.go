package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSessionRevocationAndFailedSettings(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
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
		w := call("POST", "/api/admin/login", `{"password":"original"}`, "", false)
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
	if err := os.Mkdir(path+".tmp", 0700); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/api/admin/settings", `{"site_title":"lost","new_password":"changed"}`, second, false)
	if w.Code != 500 || s.store.GetConfig().SiteTitle == "lost" || !s.store.VerifyAdminPassword("original") {
		t.Fatal("failed settings were accepted")
	}
	if err := os.Remove(path + ".tmp"); err != nil {
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
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.json"), AdminPassword: "pass"})
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
		r = httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"password":"wrong"}`))
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
