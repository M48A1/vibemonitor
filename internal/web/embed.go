package web

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
	"time"
)

//go:embed dist/*
var distFS embed.FS

var (
	fileETags = make(map[string]string)
)

// validThemes is the authoritative list of allowed site themes for this package.
// Keep in sync with the store package (store/settings.go UpdateSettingsWithAppearance).
var validThemes = map[string]bool{"default": true, "hex": true}

// normaliseAppearance sanitises theme/mode values before they are embedded in HTML.
// Unknown values fall back to the safe defaults rather than being injected verbatim.
func normaliseAppearance(theme, mode string) (string, string) {
	if !validThemes[theme] {
		theme = "default"
	}
	if mode != "light" {
		mode = "dark"
	}
	return theme, mode
}

// htmlPlaceholder is the exact attribute string embedded in index.html that is
// replaced at request time.  The init() function below guards against template drift.
const htmlPlaceholder = `data-site-theme="default" data-theme="dark"`

func init() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: cannot open dist FS: " + err.Error())
	}
	// Verify that the appearance placeholder is present before the server starts,
	// so a mismatched template fails loudly rather than silently serving wrong themes.
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("web: cannot read index.html: " + err.Error())
	}
	if !bytes.Contains(index, []byte(htmlPlaceholder)) {
		panic("web: index.html does not contain the expected appearance placeholder; update htmlPlaceholder or the HTML template")
	}
	// Pre-compute ETags for all static assets.
	_ = fs.WalkDir(sub, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		f, err := sub.Open(p)
		if err != nil {
			return nil
		}
		defer f.Close()
		h := sha256.New()
		if _, err := io.Copy(h, f); err == nil {
			fileETags[p] = `"` + hex.EncodeToString(h.Sum(nil)[:16]) + `"`
		}
		return nil
	})
}

// Handler returns an http.Handler that serves the embedded frontend files with ETag support
func Handler() http.Handler {
	return HandlerWithAppearance(nil)
}

// HandlerWithAppearance renders the site-wide selection before the first paint.
func HandlerWithAppearance(appearance func() (string, string)) http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))
	index, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic(err)
	}

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqPath := strings.TrimPrefix(r.URL.Path, "/")
		if reqPath == "" {
			reqPath = "index.html"
		}

		// Check if the requested file exists
		targetFile := reqPath
		f, err := sub.Open(targetFile)
		if err != nil {
			// SPA fallback: serve index.html
			targetFile = "index.html"
			f, err = sub.Open(targetFile)
			if err != nil {
				http.NotFound(w, r)
				return
			}
		}
		_ = f.Close()

		if targetFile == "index.html" && appearance != nil {
			theme, mode := normaliseAppearance(appearance())
			replacement := []byte(`data-site-theme="` + theme + `" data-theme="` + mode + `"`)
			page := bytes.Replace(index, []byte(htmlPlaceholder), replacement, 1)
			// The init() check above guarantees the placeholder is present in the
			// embedded file; a length difference confirms the substitution ran.
			if len(page) == len(index) && theme+mode != "defaultdark" {
				// Placeholder mismatch at runtime — serve the unchanged file rather
				// than hiding the problem; the init panic catches this at startup.
				page = index
			}
			hash := sha256.Sum256(page)
			w.Header().Set("ETag", `"`+hex.EncodeToString(hash[:16])+`"`)
			w.Header().Set("Cache-Control", "no-cache")
			http.ServeContent(w, r, "index.html", time.Time{}, bytes.NewReader(page))
			return
		}

		etag := fileETags[targetFile]
		if etag != "" {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", "no-cache")
			if match := r.Header.Get("If-None-Match"); match != "" {
				if strings.Contains(match, etag) || match == "*" {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		if targetFile == "index.html" && r.URL.Path != "/" && r.URL.Path != "/index.html" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
