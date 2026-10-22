package server

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"log"
	"net/http"
	"strings"

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

		iconURL, err := s.store.SaveSiteIcon(data, contentType)
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
		if _, err := s.store.SaveSiteIcon(nil, ""); err != nil {
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
		if err := s.store.UpdateSettingsWithIcon(req.SiteTitle, pts, req.NewPassword, req.SiteIcon); err != nil {
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

}
