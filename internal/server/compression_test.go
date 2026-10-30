package server

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"vibemonitor/internal/web"
)

// serving wraps a handler that writes the given content type and body.
func serving(contentType, body string) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", contentType)
		_, _ = io.WriteString(w, body)
	})
}

func do(h http.Handler, method, path, acceptEncoding string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if acceptEncoding != "" {
		req.Header.Set("Accept-Encoding", acceptEncoding)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestGzipMiddlewareCompressesText(t *testing.T) {
	payload := strings.Repeat("vibemonitor ", 64)
	rec := do(gzipMiddleware(serving("text/css; charset=utf-8", payload)), http.MethodGet, "/style.css", "gzip, deflate")

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if vary := rec.Header().Values("Vary"); !strings.Contains(strings.Join(vary, ","), "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", vary)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("compressed response kept Content-Length")
	}

	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if string(decoded) != payload {
		t.Errorf("decoded body = %.40q, want %.40q", decoded, payload)
	}
}

func TestGzipMiddlewareSkipsClientsWithoutGzip(t *testing.T) {
	const payload = "plain text body"
	rec := do(gzipMiddleware(serving("text/plain", payload)), http.MethodGet, "/style.css", "")

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none", got)
	}
	if rec.Body.String() != payload {
		t.Errorf("body = %q, want %q", rec.Body.String(), payload)
	}
}

func TestGzipMiddlewareSkipsPrecompressedTypes(t *testing.T) {
	for _, contentType := range []string{"application/zip", "image/png", "application/pdf", "font/woff2"} {
		rec := do(gzipMiddleware(serving(contentType, "already-compressed")), http.MethodGet, "/theme.zip", "gzip")
		if got := rec.Header().Get("Content-Encoding"); got != "" {
			t.Errorf("%s: Content-Encoding = %q, want none", contentType, got)
		}
	}
}

func TestGzipMiddlewareSkipsNotModified(t *testing.T) {
	h := gzipMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("ETag", `"abc"`)
		w.WriteHeader(http.StatusNotModified)
	}))
	rec := do(h, http.MethodGet, "/style.css", "gzip")

	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotModified)
	}
	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("Content-Encoding = %q, want none on 304", got)
	}
}

func TestGzipMiddlewareSkipsPingAndHead(t *testing.T) {
	handler := gzipMiddleware(serving("text/plain", "ok"))

	if rec := do(handler, http.MethodGet, "/ping", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("/ping was compressed")
	}
	if rec := do(handler, http.MethodHead, "/style.css", "gzip"); rec.Header().Get("Content-Encoding") != "" {
		t.Error("HEAD request was compressed")
	}
}

func TestGzipMiddlewareSkipsWebSocketUpgrade(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/ws", nil)
	req.Header.Set("Accept-Encoding", "gzip")
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	rec := httptest.NewRecorder()

	gzipMiddleware(serving("text/plain", "ok")).ServeHTTP(rec, req)

	if got := rec.Header().Get("Content-Encoding"); got != "" {
		t.Errorf("WebSocket upgrade was compressed: Content-Encoding = %q", got)
	}
}

// TestGzipMiddlewareCompressesShell checks the real static handler end to end:
// the shell sets a Content-Length, which must be dropped once gzip kicks in.
func TestGzipMiddlewareCompressesShell(t *testing.T) {
	rec := do(gzipMiddleware(web.Handler()), http.MethodGet, "/", "gzip")

	if got := rec.Header().Get("Content-Encoding"); got != "gzip" {
		t.Fatalf("Content-Encoding = %q, want gzip", got)
	}
	if rec.Header().Get("Content-Length") != "" {
		t.Error("compressed shell kept Content-Length")
	}
	if vary := strings.Join(rec.Header().Values("Vary"), ","); !strings.Contains(vary, "Accept-Encoding") {
		t.Errorf("Vary = %q, want it to include Accept-Encoding", vary)
	}

	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	if err != nil {
		t.Fatalf("gzip.NewReader: %v", err)
	}
	defer zr.Close()
	decoded, err := io.ReadAll(zr)
	if err != nil {
		t.Fatalf("read gzip body: %v", err)
	}
	if !strings.Contains(string(decoded), "<!DOCTYPE html>") {
		t.Errorf("decoded shell does not look like index.html: %.60q", decoded)
	}
	if !strings.Contains(string(decoded), "?v=") {
		t.Error("decoded shell has no versioned asset URLs")
	}
}
