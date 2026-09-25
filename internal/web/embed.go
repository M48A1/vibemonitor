package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

var (
	fileETags = make(map[string]string)
)

func init() {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic("web: cannot open dist FS: " + err.Error())
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
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

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

		if targetFile == "index.html" && r.URL.Path != "/" {
			r = r.Clone(r.Context())
			r.URL.Path = "/"
		}
		fileServer.ServeHTTP(w, r)
	})
}
