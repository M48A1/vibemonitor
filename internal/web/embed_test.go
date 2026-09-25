package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestHexPagesAndAssetsUseCacheValidation(t *testing.T) {
	h := Handler()
	for _, path := range []string{"/", "/index.html", "/dashboard", "/themes.js", "/themes.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: status %d", path, w.Code)
		}
		body := w.Body.String()
		if path == "/" || path == "/index.html" || path == "/dashboard" {
			if !strings.Contains(body, `data-site-theme="hex" data-theme="light"`) {
				t.Fatalf("%s: wrong initial appearance", path)
			}
			for _, removed := range []string{"settingColorMode", "name=\"siteTheme\"", "nodeSearch", "groupButtons", "newNodeGroup", "editNodeGroup"} {
				if strings.Contains(body, removed) {
					t.Fatalf("removed control %s remains", removed)
				}
			}
		} else if strings.Contains(body, "<!DOCTYPE html>") {
			t.Fatalf("missing asset %s", path)
		}
		etag := w.Header().Get("ETag")
		if etag == "" {
			t.Fatalf("missing ETag for %s", path)
		}
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("If-None-Match", etag)
		cached := httptest.NewRecorder()
		h.ServeHTTP(cached, req)
		if cached.Code != http.StatusNotModified {
			t.Fatalf("%s: cache validation failed", path)
		}
	}
}
