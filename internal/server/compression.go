package server

import (
	"compress/gzip"
	"io"
	"net/http"
	"strings"
	"sync"
)

var gzipPool = sync.Pool{
	New: func() any {
		w, _ := gzip.NewWriterLevel(io.Discard, gzip.DefaultCompression)
		return w
	},
}

type gzipResponseWriter struct {
	http.ResponseWriter
	gz          *gzip.Writer
	wroteHeader bool
	compressing bool
}

func (w *gzipResponseWriter) WriteHeader(code int) {
	if code >= 100 && code < 200 {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	if w.wroteHeader {
		return
	}
	w.wroteHeader = true
	if code == http.StatusNoContent || code == http.StatusNotModified || w.Header().Get("Content-Encoding") != "" {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	contentType := strings.ToLower(w.Header().Get("Content-Type"))
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "audio/") ||
		strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "font/") ||
		strings.HasPrefix(contentType, "application/zip") || strings.HasPrefix(contentType, "application/gzip") ||
		strings.HasPrefix(contentType, "application/pdf") {
		w.ResponseWriter.WriteHeader(code)
		return
	}
	w.compressing = true
	w.gz.Reset(w.ResponseWriter)
	w.ResponseWriter.Header().Set("Content-Encoding", "gzip")
	w.ResponseWriter.Header().Del("Content-Length")
	w.ResponseWriter.Header().Add("Vary", "Accept-Encoding")
	w.ResponseWriter.WriteHeader(code)
}

func (w *gzipResponseWriter) Write(b []byte) (int, error) {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressing {
		return w.gz.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *gzipResponseWriter) Flush() {
	if !w.wroteHeader {
		w.WriteHeader(http.StatusOK)
	}
	if w.compressing {
		_ = w.gz.Flush()
	}
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Skip gzip for WebSocket, non-gzip clients, tiny health checks, or HEAD requests
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") ||
			strings.EqualFold(r.Header.Get("Upgrade"), "websocket") ||
			r.Method == http.MethodHead ||
			r.URL.Path == "/ping" {
			next.ServeHTTP(w, r)
			return
		}

		gz := gzipPool.Get().(*gzip.Writer)
		defer gzipPool.Put(gz)

		gw := &gzipResponseWriter{ResponseWriter: w, gz: gz}
		defer func() {
			if gw.compressing {
				_ = gz.Close()
			}
		}()
		next.ServeHTTP(gw, r)
	})
}
