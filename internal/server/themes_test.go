package server

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vibemonitor/internal/store"
)

func TestThemeRoutes(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	s.adminTokens.Store("test", time.Now().Add(time.Hour))
	h := s.Handler()
	call := func(method, url string, body io.Reader, auth bool, kind string) *httptest.ResponseRecorder {
		r := httptest.NewRequest(method, url, body)
		if auth {
			r.Header.Set("Authorization", "Bearer test")
		}
		if kind != "" {
			r.Header.Set("Content-Type", kind)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	for _, endpoint := range []struct{ method, url string }{{"GET", "/api/admin/themes"}, {"POST", "/api/admin/themes"}, {"POST", "/api/admin/themes/select"}, {"DELETE", "/api/admin/themes/hex"}, {"GET", "/api/admin/themes/example"}} {
		if w := call(endpoint.method, endpoint.url, nil, false, ""); w.Code != http.StatusUnauthorized {
			t.Fatalf("unprotected %s: %d", endpoint.url, w.Code)
		}
	}
	sample := call("GET", "/api/admin/themes/example", nil, true, "")
	if sample.Code != 200 {
		t.Fatal(sample.Body.String())
	}
	z, err := zip.NewReader(bytes.NewReader(sample.Body.Bytes()), int64(sample.Body.Len()))
	if err != nil || len(z.File) != 2 {
		t.Fatal("invalid example ZIP")
	}
	var b bytes.Buffer
	mp := multipart.NewWriter(&b)
	f, _ := mp.CreateFormFile("file", "theme.zip")
	f.Write(sample.Body.Bytes())
	mp.Close()
	w := call("POST", "/api/admin/themes", &b, true, mp.FormDataContentType())
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	var theme store.Theme
	if err = json.Unmarshal(w.Body.Bytes(), &theme); err != nil {
		t.Fatal(err)
	}
	w = call("POST", "/api/admin/themes/select", strings.NewReader(`{"id":"`+theme.ID+`"}`), true, "application/json")
	if w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	w = call("GET", "/api/public", nil, false, "")
	var public map[string]any
	json.Unmarshal(w.Body.Bytes(), &public)
	if public["site_theme"] != theme.ID {
		t.Fatal("selection not public")
	}
	payload, _ := s.wsHub.nodesPayload()
	if !bytes.Contains(payload, []byte(theme.ID)) {
		t.Fatal("selection missing from websocket")
	}
	w = call("GET", "/api/themes/"+theme.ID+"/style.css", nil, false, "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), "--primary") || w.Header().Get("Content-Type") != "text/css; charset=utf-8" {
		t.Fatalf("bad CSS response: %d", w.Code)
	}
	r := httptest.NewRequest("GET", "/api/themes/"+theme.ID+"/style.css", nil)
	r.Header.Set("If-None-Match", w.Header().Get("ETag"))
	cached := httptest.NewRecorder()
	h.ServeHTTP(cached, r)
	if cached.Code != 304 {
		t.Fatal("CSS not revalidated")
	}
	if w = call("DELETE", "/api/admin/themes/hex", nil, true, ""); w.Code != 400 {
		t.Fatal("built-in deleted")
	}
	if w = call("DELETE", "/api/admin/themes/"+theme.ID, nil, true, ""); w.Code != 200 {
		t.Fatal(w.Body.String())
	}
	if s.store.GetConfig().SiteTheme != store.DefaultTheme {
		t.Fatal("deletion did not reset selection")
	}
	if w = call("GET", "/api/themes/"+theme.ID+"/style.css", nil, false, ""); w.Code != 404 {
		t.Fatal("deleted CSS accessible")
	}
}

func TestBuiltinThemeRoutes(t *testing.T) {
	for _, theme := range []struct{ id, marker string }{{store.RakugakiTheme, "--paper: #faf9f5"}, {store.Win2000Theme, "--background: #3a6ea5"}, {store.DesignTheme, "--background: #eeece8"}} {
		t.Run(theme.id, func(t *testing.T) {
			s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			s.adminTokens.Store("test", time.Now().Add(time.Hour))
			h := s.Handler()
			r := httptest.NewRequest("POST", "/api/admin/themes/select", strings.NewReader(`{"id":"`+theme.id+`"}`))
			r.Header.Set("Authorization", "Bearer test")
			w := httptest.NewRecorder()
			h.ServeHTTP(w, r)
			if w.Code != 200 || s.store.GetConfig().SiteTheme != theme.id {
				t.Fatalf("builtin selection failed: %s", w.Body.String())
			}
			w = httptest.NewRecorder()
			h.ServeHTTP(w, httptest.NewRequest("GET", "/"+theme.id+".css", nil))
			if w.Code != 200 || !strings.HasPrefix(w.Header().Get("Content-Type"), "text/css") || !strings.Contains(w.Body.String(), theme.marker) {
				t.Fatal("builtin stylesheet missing")
			}
		})
	}
}
