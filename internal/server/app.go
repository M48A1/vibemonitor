package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"sync"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/internal/version"
	"vibemonitor/internal/web"
	"vibemonitor/pkg/protocol"
)

type Server struct {
	addr        string
	store       *store.Store
	wsHub       *WSHub
	rpc         *RPCHandler
	adminTokens sync.Map // token -> time.Time
	authMu      sync.RWMutex
	loginLimit  loginLimiter
}

type Options struct {
	ListenAddr    string
	DataFile      string
	AdminUsername string
	AdminPassword string
}

func New(opts Options) (*Server, error) {
	if opts.ListenAddr == "" {
		opts.ListenAddr = "0.0.0.0:1314"
	}
	if opts.DataFile == "" {
		opts.DataFile = "vibemonitor-data.json"
	}

	st, err := store.New(opts.DataFile, opts.AdminPassword, opts.AdminUsername)
	if err != nil {
		return nil, err
	}

	s := &Server{
		addr:  opts.ListenAddr,
		store: st,
		wsHub: NewWSHub(st),
		rpc:   NewRPCHandler(st),
	}
	return s, nil
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(data)
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

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()

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
			"site_title":   cfg.SiteTitle,
			"announcement": cfg.Announcement,
		})
	})

	// 2. Public Nodes Info & Ping History
	mux.HandleFunc("GET /api/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.store.GetNodes())
	})

	mux.HandleFunc("GET /api/nodes/ping-history", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.URL.Query().Get("uuid")
		target := r.URL.Query().Get("target")
		timeRange := r.URL.Query().Get("range")
		if timeRange == "" {
			timeRange = "1h"
		}
		if uuid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing uuid parameter"})
			return
		}
		resp, err := s.store.GetPingHistory(uuid, target, timeRange)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": err.Error()})
			return
		}
		resp.Host = ""
		for i := range resp.Samples {
			resp.Samples[i].Host = ""
		}
		writeJSON(w, http.StatusOK, resp)
	})

	// 3. WebSocket Clients endpoint
	mux.HandleFunc("GET /api/clients", s.wsHub.HandleWS)

	// 4. Agent Ingestion & Registration
	mux.HandleFunc("POST /api/clients/v2/rpc", s.rpc.HandleV2RPC)
	mux.HandleFunc("POST /api/clients/register", s.rpc.HandleRegister)

	// 5. Admin Authentication & Management
	mux.HandleFunc("POST /api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.loginLimit.allow(r) {
			w.Header().Set("Retry-After", "300")
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "too many login attempts; retry in 5 minutes"})
			return
		}
		s.authMu.RLock()
		defer s.authMu.RUnlock()
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		if !s.store.VerifyAdmin(req.Username, req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid username or password"})
			return
		}

		// Generate random admin session token
		h := sha256.New()
		h.Write([]byte(store.GenerateToken(32)))
		token := hex.EncodeToString(h.Sum(nil))
		s.adminTokens.Store(token, time.Now().Add(7*24*time.Hour))

		http.SetCookie(w, &http.Cookie{
			Name:     "admin_token",
			Value:    token,
			Path:     "/",
			HttpOnly: true,
			Secure:   requestHTTPS(r),
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
		clearAdminCookie(w, r)
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// Node Management
	mux.HandleFunc("GET /api/admin/nodes/{uuid}/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		node := s.store.GetNode(r.PathValue("uuid"))
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": node.Token})
	})

	mux.HandleFunc("POST /api/admin/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			Profile        *store.NodeProfile `json:"profile"`
			Name           string             `json:"name"`
			Group          string             `json:"group"`
			Region         string             `json:"region"`
			TrafficLimitGB float64            `json:"traffic_limit_gb"`
			ResetDay       int                `json:"reset_day"`
			InitialUsedGB  float64            `json:"initial_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		node, err := s.store.CreateNodeWithOptions(store.NodeOptions{
			Profile:        req.Profile,
			Name:           req.Name,
			Group:          req.Group,
			Region:         req.Region,
			TrafficLimitGB: req.TrafficLimitGB,
			ResetDay:       req.ResetDay,
			InitialUsedGB:  req.InitialUsedGB,
		})
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "node": node})
	})

	mux.HandleFunc("PUT /api/admin/nodes/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		uuid := r.PathValue("uuid")
		var req struct {
			Profile        *store.NodeProfile `json:"profile"`
			Name           string             `json:"name"`
			Group          string             `json:"group"`
			Region         string             `json:"region"`
			Weight         int                `json:"weight"`
			TrafficLimitGB float64            `json:"traffic_limit_gb"`
			ResetDay       int                `json:"reset_day"`
			InitialUsedGB  float64            `json:"initial_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		if err := s.store.UpdateNodeWithOptions(uuid, store.NodeOptions{
			Profile:        req.Profile,
			Name:           req.Name,
			Group:          req.Group,
			Region:         req.Region,
			Weight:         req.Weight,
			TrafficLimitGB: req.TrafficLimitGB,
			ResetDay:       req.ResetDay,
			InitialUsedGB:  req.InitialUsedGB,
		}); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	mux.HandleFunc("DELETE /api/admin/nodes/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		uuid := r.PathValue("uuid")
		if err := s.store.DeleteNode(uuid); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// Settings
	mux.HandleFunc("POST /api/admin/settings", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			SiteTitle    string                 `json:"site_title"`
			Announcement string                 `json:"announcement"`
			NewPassword  string                 `json:"new_password"`
			PingTargets  *[]protocol.PingTarget `json:"ping_targets"`
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
		if err := s.store.UpdateSettings(req.SiteTitle, req.Announcement, pts, req.NewPassword); err != nil {
			log.Printf("[Store] Settings save failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "could not save settings"})
			return
		}
		if req.NewPassword != "" {
			s.adminTokens.Clear()
			clearAdminCookie(w, r)
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// 6. Dynamic Installer
	mux.HandleFunc("GET /install.sh", HandleInstallScript(s.store))

	// 7. Embedded Web UI (Catch-all for SPA)
	mux.Handle("/", web.Handler())
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.Body = http.MaxBytesReader(w, r.Body, maxRequestBytes)
		mux.ServeHTTP(w, r)
	})
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}

	handler := s.Handler()
	httpServer := &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[Server] VibeMonitor listening on http://%s", s.addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-ctx.Done():
		log.Printf("[Server] Shutting down gracefully...")
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		if err := s.store.Close(); err != nil {
			log.Printf("[Store] Final save failed: %v", err)
			return err
		}
		log.Printf("[Server] Data flushed and server stopped.")
		return nil
	case err := <-errCh:
		if err := s.store.Close(); err != nil {
			log.Printf("[Store] Final save failed: %v", err)
			return err
		}
		return err
	}
}
