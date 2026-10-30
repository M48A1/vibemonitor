package web

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io"
	"io/fs"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"
)

//go:embed dist/*
var distFS embed.FS

// assetCacheControl applies to asset URLs that carry the current build version.
// Their content can never change under the same URL, so browsers may keep them.
const assetCacheControl = "public, max-age=31536000, immutable"

// documentCacheControl forces revalidation of the HTML shell, which references
// the versioned asset URLs and therefore has to stay fresh.
const documentCacheControl = "no-cache"

var (
	fileETags = make(map[string]string)
	// buildVersion fingerprints the whole bundle so asset URLs can be cached hard.
	buildVersion string
	// indexHTML is index.html with every local asset URL pinned to buildVersion.
	indexHTML []byte
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

	// Fold every sub-resource ETag into one version shared by the whole bundle.
	assets := make([]string, 0, len(fileETags))
	for p := range fileETags {
		if p != "index.html" {
			assets = append(assets, p)
		}
	}
	sort.Strings(assets)
	fingerprint := sha256.New()
	for _, p := range assets {
		_, _ = io.WriteString(fingerprint, p+"="+fileETags[p]+"\n")
	}
	buildVersion = hex.EncodeToString(fingerprint.Sum(nil)[:4])

	// Pin the asset URLs in the shell so browsers can cache them forever.
	raw, err := fs.ReadFile(sub, "index.html")
	if err != nil {
		panic("web: cannot read index.html: " + err.Error())
	}
	doc := string(raw)
	for _, p := range assets {
		if strings.Contains(p, "/") {
			continue
		}
		switch strings.ToLower(path.Ext(p)) {
		case ".css", ".js":
		default:
			continue
		}
		for _, attr := range []string{"href", "src"} {
			doc = strings.ReplaceAll(doc, attr+`="/`+p+`"`, attr+`="/`+p+`?v=`+buildVersion+`"`)
		}
	}
	indexHTML = []byte(doc)
	// The served shell is a rewrite of index.html, so its validator has to change
	// whenever the pinned asset version does.
	fileETags["index.html"] = etagOf(fileETags["index.html"] + buildVersion)
}

// etagOf derives a strong validator from a fingerprint string.
func etagOf(seed string) string {
	sum := sha256.Sum256([]byte(seed))
	return `"` + hex.EncodeToString(sum[:16]) + `"`
}

// cacheControlFor returns the caching policy for a served file. Only the shell
// revalidates; sub-resources are cacheable once their URL carries the version.
func cacheControlFor(file, version string) string {
	if file == "index.html" || buildVersion == "" || version != buildVersion {
		return documentCacheControl
	}
	return assetCacheControl
}

// Handler returns an http.Handler that serves the embedded frontend files with
// ETag revalidation, immutable versioned assets and SPA fallback.
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

		// Check if the requested file exists, otherwise fall back to the shell.
		targetFile := reqPath
		if _, err := fs.Stat(sub, targetFile); err != nil {
			targetFile = "index.html"
			if _, err := fs.Stat(sub, targetFile); err != nil {
				http.NotFound(w, r)
				return
			}
		}

		cacheControl := cacheControlFor(targetFile, r.URL.Query().Get("v"))
		if etag := fileETags[targetFile]; etag != "" {
			w.Header().Set("ETag", etag)
			w.Header().Set("Cache-Control", cacheControl)
			if match := r.Header.Get("If-None-Match"); match != "" {
				if strings.Contains(match, etag) || match == "*" {
					w.WriteHeader(http.StatusNotModified)
					return
				}
			}
		}

		// The shell is served from the pre-versioned copy so its asset URLs always
		// match buildVersion; every other file goes through the file server.
		if targetFile == "index.html" {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.Header().Set("Content-Length", strconv.Itoa(len(indexHTML)))
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(indexHTML)
			return
		}

		w.Header().Set("Cache-Control", cacheControl)
		fileServer.ServeHTTP(w, r)
	})
}
