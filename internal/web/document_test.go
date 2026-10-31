package web

import (
	"net/http"
	"strings"
	"testing"
)

// TestDocumentHandlerServesAdminPages covers the contract every management page
// relies on: an un-cached HTML document with versioned asset URLs.
func TestDocumentHandlerServesAdminPages(t *testing.T) {
	if len(adminDocuments) == 0 {
		t.Fatal("no admin documents are registered")
	}
	for _, name := range adminDocuments {
		rec := serve(DocumentHandler(name), "/"+name, nil)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s status = %d, want %d", name, rec.Code, http.StatusOK)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/html") {
			t.Errorf("%s Content-Type = %q, want text/html", name, ct)
		}
		if got := rec.Header().Get("Cache-Control"); got != documentCacheControl {
			t.Errorf("%s Cache-Control = %q, want %q", name, got, documentCacheControl)
		}
		if rec.Header().Get("ETag") == "" {
			t.Errorf("%s is missing an ETag", name)
		}
		body := rec.Body.String()
		if !strings.Contains(body, "<!DOCTYPE html>") {
			t.Errorf("%s does not look like an HTML document", name)
		}
		if !strings.Contains(body, `content="noindex"`) {
			t.Errorf("%s must ask search engines not to index it", name)
		}
		if !strings.Contains(body, `/admin-core.js?v=`+buildVersion) {
			t.Errorf("%s does not load the shared management script at the build version", name)
		}
		if strings.Contains(body, `/admin-core.js"`) {
			t.Errorf("%s references the shared script without a version", name)
		}
	}
}

// TestDocumentHandlerNotModified keeps a page reload from resending the body.
func TestDocumentHandlerNotModified(t *testing.T) {
	h := DocumentHandler("admin-nodes.html")
	etag := serve(h, "/admin/nodes", nil).Header().Get("ETag")
	if etag == "" {
		t.Fatal("no ETag to revalidate with")
	}

	rec := serve(h, "/admin/nodes", map[string]string{"If-None-Match": etag})
	if rec.Code != http.StatusNotModified {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusNotModified)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("304 response carried a body: %.80q", rec.Body.String())
	}

	stale := serve(h, "/admin/nodes", map[string]string{"If-None-Match": `"deadbeef"`})
	if stale.Code != http.StatusOK {
		t.Errorf("stale ETag status = %d, want %d", stale.Code, http.StatusOK)
	}
}

// TestDocumentHandlerRejectsUnknownDocument guards the panic path at build time.
func TestDocumentHandlerRejectsUnknownDocument(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("DocumentHandler accepted an unknown document")
		}
	}()
	DocumentHandler("missing.html")
}
