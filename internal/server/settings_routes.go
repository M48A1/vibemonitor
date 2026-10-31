package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

func (s *Server) registerSettingsRoutes(mux *http.ServeMux) {
	// Settings & Icon Upload
	mux.HandleFunc("POST /api/admin/upload-icon", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if err := r.ParseMultipartForm(store.MaxIconBytes + (64 << 10)); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "文件过大或格式无效（最大 2MB）"})
			return
		}
		defer r.MultipartForm.RemoveAll()
		file, header, err := r.FormFile("icon")
		if err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "缺少图片文件"})
			return
		}
		defer file.Close()

		data, err := io.ReadAll(io.LimitReader(file, store.MaxIconBytes+1))
		if len(data) > store.MaxIconBytes {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "图片最大 2MB"})
			return
		}
		if err != nil || len(data) == 0 {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "读取图片失败"})
			return
		}

		contentType := http.DetectContentType(data)
		nameLower := ""
		if header != nil {
			nameLower = strings.ToLower(header.Filename)
		}
		switch {
		case strings.HasPrefix(contentType, "image/png") || strings.HasSuffix(nameLower, ".png"):
			contentType = "image/png"
		case strings.HasPrefix(contentType, "image/jpeg") || strings.HasSuffix(nameLower, ".jpg") || strings.HasSuffix(nameLower, ".jpeg"):
			contentType = "image/jpeg"
		case strings.HasPrefix(contentType, "image/gif") || strings.HasSuffix(nameLower, ".gif"):
			contentType = "image/gif"
		case strings.HasPrefix(contentType, "image/webp") || strings.HasSuffix(nameLower, ".webp"):
			contentType = "image/webp"
		case strings.HasSuffix(nameLower, ".ico") || contentType == "image/x-icon" || contentType == "image/vnd.microsoft.icon":
			contentType = "image/x-icon"
		case strings.Contains(contentType, "xml") || strings.HasSuffix(nameLower, ".svg") || bytes.Contains(data[:min(len(data), 512)], []byte("<svg")):
			contentType = "image/svg+xml"
		default:
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "只支持 PNG、JPG、SVG、WebP、ICO、GIF 图片"})
			return
		}

		iconURL, err := s.store.SaveSiteIconContext(r.Context(), data, contentType)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.wsHub.forceBroadcastNodes()

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"url":    iconURL,
		})
	})

	mux.HandleFunc("POST /api/admin/delete-icon", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if _, err := s.store.SaveSiteIconContext(r.Context(), nil, ""); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		s.wsHub.forceBroadcastNodes()
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	mux.HandleFunc("POST /api/admin/settings", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			SiteTitle   string                 `json:"site_title"`
			SiteIcon    *string                `json:"site_icon"`
			NewPassword string                 `json:"new_password"`
			PingTargets *[]protocol.PingTarget `json:"ping_targets"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		var pts []protocol.PingTarget
		if req.PingTargets != nil {
			pts = *req.PingTargets
		}
		s.authMu.Lock()
		defer s.authMu.Unlock()
		if _, ok := s.adminTokens.Load(adminToken(r)); !ok {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "session expired"})
			return
		}
		if err := s.store.UpdateSettingsWithIconContext(r.Context(), req.SiteTitle, pts, req.NewPassword, req.SiteIcon); err != nil {
			if errors.Is(err, store.ErrInvalidSettings) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
			log.Printf("[Store] Settings save failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save settings"})
			return
		}
		if req.NewPassword != "" {
			s.adminTokens.Clear()
			clearAdminCookie(w, r, s.trustedProxies...)
		}
		s.wsHub.forceBroadcastNodes()
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// Registration window. Self-registration hands out a node token, so the key
	// it accepts is only valid for as long as an administrator opened it; the
	// panel calls these three to open, watch and close the window.
	mux.HandleFunc("GET /api/admin/register", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		writeJSON(w, http.StatusOK, registerWindowPayload(s.store.GetConfig(), time.Now()))
	})
	mux.HandleFunc("POST /api/admin/register", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			Minutes int `json:"minutes"`
		}
		// An empty body opens the default window; anything malformed is a caller
		// bug and must not look like a request for sixty minutes.
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil && !errors.Is(err, io.EOF) {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid registration request"})
			return
		}
		if _, _, err := s.store.OpenRegistrationContext(r.Context(), req.Minutes); err != nil {
			log.Printf("[Store] Opening the registration window failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not open registration"})
			return
		}
		writeJSON(w, http.StatusOK, registerWindowPayload(s.store.GetConfig(), time.Now()))
	})
	mux.HandleFunc("DELETE /api/admin/register", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		if err := s.store.CloseRegistrationContext(r.Context()); err != nil {
			log.Printf("[Store] Closing the registration window failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not close registration"})
			return
		}
		writeJSON(w, http.StatusOK, registerWindowPayload(s.store.GetConfig(), time.Now()))
	})
}

// registerWindowPayload describes the window without handing out a closed key.
// seconds_left is computed here rather than in the browser: a panel whose clock
// is off would otherwise show a command that no longer works.
func registerWindowPayload(cfg store.Config, now time.Time) map[string]any {
	left := cfg.RegisterUntil - now.Unix()
	if left < 0 {
		left = 0
	}
	open := cfg.AutoDiscoveryKey != "" && left > 0
	payload := map[string]any{
		"open":            open,
		"seconds_left":    left,
		"max_minutes":     store.MaxRegistrationMinutes,
		"default_minutes": store.DefaultRegistrationMinutes,
	}
	if open {
		payload["register_key"] = cfg.AutoDiscoveryKey
		payload["expires_at"] = cfg.RegisterUntil
	}
	return payload
}
