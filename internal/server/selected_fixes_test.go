package server

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"vibemonitor/internal/store"
)

func TestCombinedSettingsFailureDoesNotChangePassword(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "old-pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	h := s.Handler()
	s.adminTokens.Store("session-a", time.Now().Add(time.Hour))
	s.adminTokens.Store("session-b", time.Now().Add(time.Hour))
	originalTitle := s.store.GetConfig().SiteTitle
	submit := func(icon string) *httptest.ResponseRecorder {
		data, _ := json.Marshal(map[string]any{"site_title": "New title", "new_password": "new-pass", "site_icon": icon})
		r := httptest.NewRequest("POST", "/api/admin/settings", bytes.NewReader(data))
		r.Header.Set("Authorization", "Bearer session-a")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := submit("javascript:alert(1)"); w.Code == http.StatusOK {
		t.Fatal("invalid icon accepted")
	}
	if !s.store.VerifyAdminPassword("old-pass") || s.store.VerifyAdminPassword("new-pass") || s.store.GetConfig().SiteTitle != originalTitle {
		t.Fatal("invalid settings partially committed")
	}
	if _, ok := s.adminTokens.Load("session-a"); !ok {
		t.Fatal("failed operation revoked session")
	}
	if w := submit("https://example.com/icon.png"); w.Code != http.StatusOK {
		t.Fatalf("valid save: %d %s", w.Code, w.Body.String())
	}
	if !s.store.VerifyAdminPassword("new-pass") {
		t.Fatal("password not updated")
	}
	for _, token := range []string{"session-a", "session-b"} {
		if _, ok := s.adminTokens.Load(token); ok {
			t.Fatal("old session still active")
		}
	}
}

func TestIconUploadSizeBoundariesAndBackup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "data.db")
	s, err := New(Options{DataFile: path, AdminPassword: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	s.adminTokens.Store("admin", time.Now().Add(time.Hour))
	h := s.Handler()
	var last []byte
	for _, size := range []int{(1 << 20) + 1, store.MaxIconBytes, store.MaxIconBytes + 1} {
		data := bytes.Repeat([]byte{'x'}, size)
		copy(data, []byte("\x89PNG\r\n\x1a\n"))
		var body bytes.Buffer
		mw := multipart.NewWriter(&body)
		part, err := mw.CreateFormFile("icon", "logo.png")
		if err != nil {
			t.Fatal(err)
		}
		part.Write(data)
		mw.Close()
		r := httptest.NewRequest("POST", "/api/admin/upload-icon", &body)
		r.Header.Set("Content-Type", mw.FormDataContentType())
		r.Header.Set("Authorization", "Bearer admin")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if size <= store.MaxIconBytes {
			if w.Code != http.StatusOK {
				t.Fatalf("size %d rejected: %d %s", size, w.Code, w.Body.String())
			}
			last = data
		} else if w.Code == http.StatusOK {
			t.Fatal("oversized icon accepted")
		}
	}
	// An oversized replacement must not destroy the previously stored image.
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/site-icon", nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), last) {
		t.Fatal("uploaded icon changed on failure")
	}
	if files, _ := filepath.Glob(filepath.Join(filepath.Dir(path), "site-icon.*")); len(files) != 0 {
		t.Fatal("new upload wrote external files")
	}
	backup := filepath.Join(t.TempDir(), "backup.db")
	if err := store.ExportData(path, backup); err != nil {
		t.Fatal(err)
	}
	dest := filepath.Join(t.TempDir(), "restored.db")
	if err := store.RestoreData(backup, dest); err != nil {
		t.Fatal(err)
	}
	restored, err := New(Options{DataFile: dest})
	if err != nil {
		t.Fatal(err)
	}
	defer restored.store.Close()
	w = httptest.NewRecorder()
	restored.Handler().ServeHTTP(w, httptest.NewRequest("GET", "/api/site-icon", nil))
	if w.Code != http.StatusOK || !bytes.Equal(w.Body.Bytes(), last) {
		t.Fatal("icon not portable in backup")
	}
	// Other API routes retain their original 1 MiB body limit.
	r := httptest.NewRequest("POST", "/api/admin/settings", strings.NewReader(`{"site_title":"`+strings.Repeat("x", 1<<20)+`"}`))
	r.Header.Set("Authorization", "Bearer admin")
	w = httptest.NewRecorder()
	h.ServeHTTP(w, r)
	if w.Code != http.StatusRequestEntityTooLarge {
		t.Fatalf("ordinary body limit relaxed: %d", w.Code)
	}
}
