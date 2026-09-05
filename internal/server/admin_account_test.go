package server

import (
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
)

func TestSingleAdminAccount(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.json")
	for _, username := range []string{"owner", "replacement"} {
		s, err := New(Options{DataFile: path, AdminUsername: username, AdminPassword: "secret"})
		if err != nil {
			t.Fatal(err)
		}
		for _, tc := range []struct {
			body string
			code int
		}{
			{`{"username":"owner","password":"secret"}`, 200},
			{`{"username":"admin","password":"secret"}`, 401},
			{`{"username":"replacement","password":"secret"}`, 401},
			{`{"password":"secret"}`, 401},
			{`{"username":"owner","password":"wrong"}`, 401},
		} {
			r := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(tc.body))
			w := httptest.NewRecorder()
			s.Handler().ServeHTTP(w, r)
			if w.Code != tc.code {
				t.Fatalf("login status %d, want %d", w.Code, tc.code)
			}
		}
		if err := s.store.Close(); err != nil {
			t.Fatal(err)
		}
	}
}
