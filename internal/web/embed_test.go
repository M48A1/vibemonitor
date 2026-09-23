package web

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestAppearanceInvalidatesHTMLCache(t *testing.T) {
	theme, mode := "default", "dark"
	h := HandlerWithAppearance(func() (string, string) { return theme, mode })
	get := func(etag string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/", nil)
		r.Header.Set("If-None-Match", etag)
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	first := get("")
	etag := first.Header().Get("ETag")
	if get(etag).Code != http.StatusNotModified {
		t.Fatal("unchanged HTML is not cacheable")
	}
	theme, mode = "hex", "light"
	next := get(etag)
	if next.Code != http.StatusOK || next.Header().Get("ETag") == etag || !strings.Contains(next.Body.String(), `data-site-theme="hex" data-theme="light"`) {
		t.Fatal("theme change served stale HTML")
	}
	theme, mode = `"><script>`, "unknown"
	if !strings.Contains(get("").Body.String(), `data-site-theme="default" data-theme="dark"`) {
		t.Fatal("invalid appearance not normalized")
	}
	for _, path := range []string{"/themes.js", "/themes.css"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK || strings.Contains(w.Body.String(), "<!DOCTYPE html>") {
			t.Fatalf("missing embedded asset %s", path)
		}
	}
}
