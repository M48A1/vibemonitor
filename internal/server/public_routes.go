package server

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"net/http"
	"strings"
	"time"

	"vibemonitor/internal/version"
)

func (s *Server) registerPublicRoutes(mux *http.ServeMux) {
	// 1. Health & Meta
	mux.HandleFunc("GET /ping", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("pong"))
	})

	mux.HandleFunc("GET /api/version", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{
			"version": version.Version,
			"hash":    version.Commit,
		})
	})

	mux.HandleFunc("GET /api/public", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.GetConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"site_title": cfg.SiteTitle,
			"site_theme": cfg.SiteTheme,
			"site_icon":  cfg.SiteIcon,
		})
	})

	mux.HandleFunc("GET /api/visitor-ip", http.NotFound)

	mux.HandleFunc("GET /api/site-icon", func(w http.ResponseWriter, r *http.Request) {
		data, contentType, err := s.store.SiteIconData()
		if errors.Is(err, sql.ErrNoRows) {
			http.NotFound(w, r)
			return
		}
		if err != nil {
			http.Error(w, "could not load icon", http.StatusInternalServerError)
			return
		}
		h := sha256.Sum256(data)
		etag := `"` + hex.EncodeToString(h[:16]) + `"`
		w.Header().Set("Content-Type", contentType)
		w.Header().Set("ETag", etag)
		w.Header().Set("Cache-Control", "no-cache")
		if match := r.Header.Get("If-None-Match"); match != "" {
			if strings.Contains(match, etag) || match == "*" {
				w.WriteHeader(http.StatusNotModified)
				return
			}
		}
		http.ServeContent(w, r, "site-icon", time.Time{}, bytes.NewReader(data))
	})

}
