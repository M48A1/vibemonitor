package server

import (
	"encoding/json"
	"net/http"
	"time"

	"vibemonitor/internal/store"
)

func (s *Server) pruneExpiredTokens() {
	now := time.Now()
	s.adminTokens.Range(func(key, val any) bool {
		if expiry, ok := val.(time.Time); ok && now.After(expiry) {
			s.adminTokens.Delete(key)
		}
		return true
	})
}

func (s *Server) checkAdmin(r *http.Request) bool {
	s.authMu.RLock()
	defer s.authMu.RUnlock()
	token := adminToken(r)
	if token == "" {
		return false
	}
	val, ok := s.adminTokens.Load(token)
	if !ok {
		return false
	}
	expiry := val.(time.Time)
	if time.Now().After(expiry) {
		s.adminTokens.Delete(token)
		return false
	}
	return true
}

func (s *Server) registerAuthRoutes(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.loginLimit.allow(r, s.trustedProxies...) {
			w.Header().Set("Retry-After", "300")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts; retry in 5 minutes"})
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		// Keep bcrypt outside authMu, then ensure the credentials did not change
		// before issuing a session. Password changes clear all existing sessions.
		credentials := s.store.GetConfig()
		if !s.store.VerifyAdmin(req.Username, req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid username or password"})
			return
		}
		s.authMu.RLock()
		defer s.authMu.RUnlock()
		current := s.store.GetConfig()
		if credentials.AdminUsername != current.AdminUsername || credentials.AdminPassword != current.AdminPassword {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "credentials changed; retry login"})
			return
		}

		// Generate random admin session token
		token := store.GenerateToken(32)
		s.adminTokens.Store(token, time.Now().Add(7*24*time.Hour))

		http.SetCookie(w, &http.Cookie{
			Name:     "admin_token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   requestHTTPS(r, s.trustedProxies...),
			SameSite: http.SameSiteStrictMode,
			MaxAge:   7 * 86400,
		})

		writeJSON(w, http.StatusOK, map[string]any{
			"status": "success",
			"token":  token,
		})
	})

	mux.HandleFunc("GET /api/admin/status", func(w http.ResponseWriter, r *http.Request) {
		isAdmin := s.checkAdmin(r)
		writeJSON(w, http.StatusOK, map[string]any{
			"is_admin": isAdmin,
		})
	})

	mux.HandleFunc("POST /api/admin/logout", func(w http.ResponseWriter, r *http.Request) {
		s.adminTokens.Delete(adminToken(r))
		clearAdminCookie(w, r, s.trustedProxies...)
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

}
