package server

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"

	"vibemonitor/internal/store"
)

func (s *Server) registerTelegramRoutes(mux *http.ServeMux) {
	// 5. Admin Authentication & Management
	mux.HandleFunc("GET /api/admin/telegram", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		cfg := s.store.GetConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"enabled": cfg.TelegramEnabled, "chat_id": cfg.TelegramChatID,
			"token_configured":      cfg.TelegramBotToken != "",
			"offline_delay_seconds": cfg.TelegramOfflineDelaySeconds,
			"reminder_days":         cfg.TelegramReminderDays,
			"reminder_hour":         cfg.TelegramReminderHour,
			"reminder_timezone":     cfg.TelegramReminderTimezone,
			"templates":             cfg.TelegramTemplates.Effective(),
			"default_templates":     store.DefaultTelegramTemplates(),
		})
	})
	mux.HandleFunc("POST /api/admin/telegram", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			BotToken            string                   `json:"bot_token"`
			ChatID              string                   `json:"chat_id"`
			Enabled             bool                     `json:"enabled"`
			Clear               bool                     `json:"clear"`
			OfflineDelaySeconds *int                     `json:"offline_delay_seconds"`
			ReminderDays        *int                     `json:"reminder_days"`
			ReminderHour        *int                     `json:"reminder_hour"`
			ReminderTimezone    *string                  `json:"reminder_timezone"`
			Templates           *store.TelegramTemplates `json:"templates"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}
		if err := s.store.UpdateTelegramOptions(store.TelegramOptions{
			BotToken: req.BotToken, ChatID: req.ChatID, Enabled: req.Enabled, Clear: req.Clear,
			OfflineDelaySeconds: req.OfflineDelaySeconds, ReminderDays: req.ReminderDays,
			ReminderHour: req.ReminderHour, ReminderTimezone: req.ReminderTimezone,
			Templates: req.Templates,
		}); err != nil {
			if errors.Is(err, store.ErrInvalidSettings) {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
			} else {
				log.Printf("[Store] Telegram settings save failed: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save Telegram settings"})
			}
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})
	mux.HandleFunc("POST /api/admin/telegram/test", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		cfg := s.store.GetConfig()
		client := telegramHTTPClient()
		if err := sendTelegramMessage(r.Context(), client, cfg, "✅ VibeMonitor Telegram 告警测试成功"); err != nil {
			writeJSON(w, http.StatusBadGateway, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

}
