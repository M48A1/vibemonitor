package web

import (
	"embed"
	"io/fs"
	"net/http"
)

//go:embed dist/*
var distFS embed.FS

// Handler returns an http.Handler that serves the embedded frontend files
func Handler() http.Handler {
	sub, err := fs.Sub(distFS, "dist")
	if err != nil {
		panic(err)
	}
	fileServer := http.FileServer(http.FS(sub))

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Single-Page Application (SPA) fallback:
		// If requesting root or a non-file path, serve index.html
		path := r.URL.Path
		if path == "" || path == "/" {
			fileServer.ServeHTTP(w, r)
			return
		}

		// Try opening the file
		f, err := sub.Open(path[1:])
		if err != nil {
			// File not found, serve index.html for SPA
			r.URL.Path = "/"
			fileServer.ServeHTTP(w, r)
			return
		}
		_ = f.Close()
		fileServer.ServeHTTP(w, r)
	})
}
