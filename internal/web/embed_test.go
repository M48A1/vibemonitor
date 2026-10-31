package web

import (
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"path"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// serve runs a single GET through the static handler and records the response.
func serve(h http.Handler, target string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodGet, target, nil)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	return rec
}

func TestHandlerServesIndexForRoot(t *testing.T) {
	rec := serve(Handler(), "/", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if body := strings.ToLower(rec.Body.String()); !strings.Contains(body, "<!doctype html") {
		t.Fatalf("body does not look like index.html: %.80q", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
		t.Errorf("Content-Type = %q, want text/html", ct)
	}
	if rec.Header().Get("ETag") == "" {
		t.Error("missing ETag for index.html")
	}
	if cc := rec.Header().Get("Cache-Control"); cc != "no-cache" {
		t.Errorf("Cache-Control = %q, want no-cache", cc)
	}
}

func TestHandlerNotModifiedOnMatchingETag(t *testing.T) {
	h := Handler()
	etag := serve(h, "/", nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag to revalidate with")
	}

	rec := serve(h, "/", map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotModified)
	}
	if body := rec.Body.String(); body != "" {
		t.Errorf("304 response carried a body: %.80q", body)
	}

	// A stale validator must still be answered with the full document.
	stale := serve(h, "/", map[string]string{"If-None-Match": `"deadbeef"`})
	if stale.Code != http.StatusOK {
		t.Errorf("stale ETag status = %d, want %d", stale.Code, http.StatusOK)
	}
	if !strings.Contains(stale.Body.String(), "<!DOCTYPE html>") && !strings.Contains(stale.Body.String(), "<!doctype html>") {
		t.Error("stale ETag did not return index.html")
	}
}

func TestHandlerServesAssetsWithETag(t *testing.T) {
	rec := serve(Handler(), "/style.css", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/css") {
		t.Errorf("Content-Type = %q, want text/css", ct)
	}
	if got, want := rec.Header().Get("ETag"), fileETags["style.css"]; got == "" || got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}
}

func TestHandlerFallsBackToIndexForUnknownPath(t *testing.T) {
	rec := serve(Handler(), "/nodes/42", nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if !strings.Contains(rec.Body.String(), "<!DOCTYPE html>") && !strings.Contains(rec.Body.String(), "<!doctype html>") {
		t.Error("SPA fallback did not serve index.html")
	}
	if got, want := rec.Header().Get("ETag"), fileETags["index.html"]; got != want {
		t.Errorf("ETag = %q, want the index.html ETag %q", got, want)
	}
}

func TestEveryDistFileHasETag(t *testing.T) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	err = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		if fileETags[p] == "" {
			t.Errorf("%s has no pre-computed ETag", p)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("WalkDir: %v", err)
	}
}

// TestIndexReferencesShippedAssets catches a rename that would 404 for browsers.
func TestIndexReferencesShippedAssets(t *testing.T) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}

	localRef := regexp.MustCompile(`(?:src|href)="(/[^"/][^"]*)"`)
	matches := localRef.FindAllStringSubmatch(string(index), -1)
	if len(matches) == 0 {
		t.Fatal("index.html references no local assets")
	}
	for _, m := range matches {
		ref := m[1]
		if strings.HasPrefix(ref, "/api/") {
			continue // served by the API, not the embedded FS
		}
		// The management pages and the installer are served by explicit routes.
		if ref == "/admin" || strings.HasPrefix(ref, "/admin/") || ref == "/install.sh" {
			continue
		}
		if _, err := fs.Stat(sub, strings.TrimPrefix(ref, "/")); err != nil {
			t.Errorf("index.html references %s which is not embedded: %v", ref, err)
		}
	}
}

// TestDocumentsPinAssetURLsToBuildVersion ensures every shipped asset is
// referenced by one of the entry points and that no reference skips the build
// version a browser needs to cache it forever.
func TestDocumentsPinAssetURLsToBuildVersion(t *testing.T) {
	if buildVersion == "" {
		t.Fatal("buildVersion is empty")
	}
	var all strings.Builder
	all.WriteString(serve(Handler(), "/", nil).Body.String())
	for _, name := range adminDocuments {
		all.WriteString(serve(DocumentHandler(name), "/"+name, nil).Body.String())
	}
	body := all.String()

	for p := range fileETags {
		if strings.Contains(p, "/") || p == "index.html" || strings.HasSuffix(p, ".html") {
			continue
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".css", ".js":
		default:
			continue
		}
		if strings.Contains(body, `="/`+p+`"`) {
			t.Errorf("%s is referenced without the ?v= build version", p)
			continue
		}
		if !strings.Contains(body, `="/`+p+`?v=`+buildVersion+`"`) {
			t.Errorf("%s is not referenced with ?v=%s", p, buildVersion)
		}
	}
}

// TestDashboardShellShipsNoManagementDialogs keeps management UI on its own
// pages: a dialog that lives in the public shell cannot be deep linked, so the
// /admin pages must own it instead.
func TestDashboardShellShipsNoManagementDialogs(t *testing.T) {
	body := serve(Handler(), "/", nil).Body.String()
	for _, id := range []string{
		"settingsModal", "externalSettingsModal", "themeManagementModal",
		"addNodeModal", "registerNodeModal", "sortNodesModal", "selectNodeModal",
		"editNodeModal", "nodeGuideModal",
	} {
		if strings.Contains(body, `id="`+id+`"`) {
			t.Errorf("dashboard shell still ships the %s dialog", id)
		}
	}
	for _, src := range []string{`/settings.js`, `/nodes.js`} {
		if strings.Contains(body, src) {
			t.Errorf("dashboard shell still loads the removed %s", src)
		}
	}
	// The dashboard may only render the public cards, so its scripts must not
	// carry the management handlers either.
	app := serve(Handler(), "/app.js", nil).Body.String()
	for _, fn := range []string{"openEditModal", "showGuide", "deleteNode", "copyGuideCommand"} {
		if strings.Contains(app, fn) {
			t.Errorf("dashboard app.js still references %s", fn)
		}
	}
}

// TestAdminDocumentsShipNoLoginForm keeps one login dialog in the product: the
// management pages are only served to a signed-in admin, so a copy of the form
// inside them could never be reached.
func TestAdminDocumentsShipNoLoginForm(t *testing.T) {
	for _, name := range adminDocuments {
		body := serve(DocumentHandler(name), "/"+name, nil).Body.String()
		for _, marker := range []string{"adminGate", "adminLoginForm", "adminLoginUsername", "adminLoginPassword"} {
			if strings.Contains(body, marker) {
				t.Errorf("%s still ships %s", name, marker)
			}
		}
		if !strings.Contains(body, `id="adminLoading"`) {
			t.Errorf("%s lost its loading notice", name)
		}
	}
	// Losing the session mid-visit sends the page back to the dashboard, which
	// owns the dialog, instead of showing a second, unreachable form.
	core := serve(Handler(), "/admin-core.js", nil).Body.String()
	if strings.Contains(core, "wireLogin") || strings.Contains(core, "adminLoginForm") {
		t.Error("admin-core.js still wires a page-local login form")
	}
	if !strings.Contains(core, "location.replace('/')") {
		t.Error("admin-core.js does not bounce an expired session to the dashboard")
	}
}

func TestVersionedAssetIsCachedImmutably(t *testing.T) {
	target := "/themes.css?v=" + buildVersion
	rec := serve(Handler(), target, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Cache-Control"), assetCacheControl; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if got, want := rec.Header().Get("ETag"), fileETags["themes.css"]; got == "" || got != want {
		t.Errorf("ETag = %q, want %q", got, want)
	}

	// The version query must not change which bytes are served.
	plain := serve(Handler(), "/themes.css", nil)
	if rec.Body.String() != plain.Body.String() {
		t.Error("versioned asset differs from the plain asset body")
	}
}

func TestVersionedAssetStillHonoursConditionalRequest(t *testing.T) {
	path := "/style.css?v=" + buildVersion
	etag := serve(Handler(), path, nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag to revalidate with")
	}

	rec := serve(Handler(), path, map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotModified)
	}
	if got, want := rec.Header().Get("Cache-Control"), assetCacheControl; got != want {
		t.Errorf("304 Cache-Control = %q, want %q", got, want)
	}
}

func TestUnversionedAssetRevalidates(t *testing.T) {
	for _, target := range []string{"/style.css", "/themes.css?v=stale", "/index.html"} {
		rec := serve(Handler(), target, nil)
		if got, want := rec.Header().Get("Cache-Control"), documentCacheControl; got != want {
			t.Errorf("%s Cache-Control = %q, want %q", target, got, want)
		}
	}
}

// TestSPAFallbackIsNotCachedImmutably keeps deep links on the revalidating path.
func TestSPAFallbackIsNotCachedImmutably(t *testing.T) {
	rec := serve(Handler(), "/nodes/42?v="+buildVersion, nil)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
	}
	if got, want := rec.Header().Get("Cache-Control"), documentCacheControl; got != want {
		t.Errorf("Cache-Control = %q, want %q", got, want)
	}
	if !strings.Contains(rec.Body.String(), "?v="+buildVersion) {
		t.Error("SPA fallback served a shell without versioned asset URLs")
	}
}

// TestIndexETagCoversBuildVersion keeps a shell revalidation from outliving the
// assets it points at.
func TestIndexETagCoversBuildVersion(t *testing.T) {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		t.Fatalf("fs.Sub: %v", err)
	}
	raw, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		t.Fatalf("read index.html: %v", err)
	}
	sum := sha256.Sum256(raw)
	rawETag := `"` + hex.EncodeToString(sum[:16]) + `"`

	if fileETags["index.html"] == rawETag {
		t.Error("shell ETag is the raw file hash, so asset-only changes would 304")
	}
	if got, want := serve(Handler(), "/", nil).Header().Get("ETag"), fileETags["index.html"]; got != want {
		t.Errorf("served ETag = %q, want %q", got, want)
	}
	if etagOf("a") == etagOf("b") {
		t.Error("etagOf is not sensitive to its input")
	}
}

// TestIndexHasContentLength keeps small non-gzip clients from waiting on a chunked body.
func TestIndexHasContentLength(t *testing.T) {
	rec := serve(Handler(), "/", nil)

	if got, want := rec.Header().Get("Content-Length"), strconv.Itoa(len(indexHTML)); got != want {
		t.Errorf("Content-Length = %q, want %q", got, want)
	}
	if got, want := rec.Body.Len(), len(indexHTML); got != want {
		t.Errorf("body length = %d, want %d", got, want)
	}
}
