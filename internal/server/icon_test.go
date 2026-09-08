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
)

func TestSiteIconUploadAndServe(t *testing.T) {
	dataPath := filepath.Join(t.TempDir(), "data.json")
	s, err := New(Options{DataFile: dataPath, AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.store.Close() })

	h := s.Handler()

	// 1. Initial GET /api/site-icon should be 404
	req := httptest.NewRequest("GET", "/api/site-icon", nil)
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 for missing icon, got %d", w.Code)
	}

	// 2. Unauthorized upload attempt
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("icon", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	// PNG magic header bytes + small sample
	pngBytes := []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n', 0x00, 0x00, 0x00, 0x0d, 'I', 'H', 'D', 'R'}
	_, _ = part.Write(pngBytes)
	_ = writer.Close()

	uploadReq := httptest.NewRequest("POST", "/api/admin/upload-icon", &body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	w = httptest.NewRecorder()
	h.ServeHTTP(w, uploadReq)
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("expected 401 for unauthorized upload, got %d", w.Code)
	}

	// 3. Login as admin
	loginReq := httptest.NewRequest("POST", "/api/admin/login", strings.NewReader(`{"username":"admin","password":"test-password"}`))
	w = httptest.NewRecorder()
	h.ServeHTTP(w, loginReq)
	if w.Code != http.StatusOK {
		t.Fatalf("login failed: %d", w.Code)
	}
	var loginResp struct{ Token string }
	if err := json.NewDecoder(w.Body).Decode(&loginResp); err != nil || loginResp.Token == "" {
		t.Fatalf("missing token: %v", err)
	}
	adminToken := loginResp.Token

	// 4. Authorized upload valid PNG
	body.Reset()
	writer = multipart.NewWriter(&body)
	part, err = writer.CreateFormFile("icon", "logo.png")
	if err != nil {
		t.Fatal(err)
	}
	_, _ = part.Write(pngBytes)
	_ = writer.Close()

	uploadReq = httptest.NewRequest("POST", "/api/admin/upload-icon", &body)
	uploadReq.Header.Set("Content-Type", writer.FormDataContentType())
	uploadReq.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, uploadReq)
	if w.Code != http.StatusOK {
		t.Fatalf("upload failed with code %d: %s", w.Code, w.Body.String())
	}
	var uploadResp struct {
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.NewDecoder(w.Body).Decode(&uploadResp); err != nil || uploadResp.Status != "success" || uploadResp.URL == "" {
		t.Fatalf("invalid upload response: %v", err)
	}

	// 5. GET /api/site-icon should return 200 with image/png
	req = httptest.NewRequest("GET", "/api/site-icon", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 for site-icon, got %d", w.Code)
	}
	if w.Header().Get("Content-Type") != "image/png" {
		t.Fatalf("expected image/png content type, got %s", w.Header().Get("Content-Type"))
	}
	if !bytes.Equal(w.Body.Bytes(), pngBytes) {
		t.Fatalf("served bytes mismatch")
	}

	// 6. GET /api/public should contain site_icon
	req = httptest.NewRequest("GET", "/api/public", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("public endpoint failed: %d", w.Code)
	}
	var pubResp struct {
		SiteTitle string `json:"site_title"`
		SiteIcon  string `json:"site_icon"`
	}
	if err := json.NewDecoder(w.Body).Decode(&pubResp); err != nil || pubResp.SiteIcon == "" {
		t.Fatalf("expected site_icon in public response, got: %+v", pubResp)
	}

	// 7. Test settings update (title only)
	settingsReq := httptest.NewRequest("POST", "/api/admin/settings", strings.NewReader(`{"site_title":"New Title"}`))
	settingsReq.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, settingsReq)
	if w.Code != http.StatusOK {
		t.Fatalf("settings save failed: %d (%s)", w.Code, w.Body.String())
	}

	// 8. Delete icon
	delReq := httptest.NewRequest("POST", "/api/admin/delete-icon", nil)
	delReq.Header.Set("Authorization", "Bearer "+adminToken)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, delReq)
	if w.Code != http.StatusOK {
		t.Fatalf("delete icon failed: %d", w.Code)
	}

	// 9. GET /api/site-icon should now be 404
	req = httptest.NewRequest("GET", "/api/site-icon", nil)
	w = httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Fatalf("expected 404 after icon deletion, got %d", w.Code)
	}
}
