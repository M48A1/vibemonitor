package server

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"log"
	"net/http"
	"strings"
	"sync"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/internal/web"
	"vibemonitor/pkg/protocol"
)

type Server struct {
	addr        string
	store       *store.Store
	wsHub       *WSHub
	rpc         *RPCHandler
	adminTokens sync.Map // token -> time.Time
}

type Options struct {
	ListenAddr    string
	DataFile      string
	AdminPassword string
}

func New(opts Options) (*Server, error) {
	if opts.ListenAddr == "" {
		opts.ListenAddr = "0.0.0.0:25774"
	}
	if opts.DataFile == "" {
		opts.DataFile = "vibemonitor-data.json"
	}

	st, err := store.New(opts.DataFile, opts.AdminPassword)
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
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token == "" {
		if cookie, err := r.Cookie("admin_token"); err == nil {
			token = cookie.Value
		}
	}
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
			"version": "1.0.0-lite",
			"hash":    "vibemonitor",
		})
	})

	mux.HandleFunc("GET /api/public", func(w http.ResponseWriter, r *http.Request) {
		cfg := s.store.GetConfig()
		writeJSON(w, http.StatusOK, map[string]any{
			"site_title":   cfg.SiteTitle,
			"announcement": cfg.Announcement,
			"ping_targets": cfg.PingTargets,
		})
	})

	// 2. Public Nodes Info
	mux.HandleFunc("GET /api/nodes", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, s.store.GetNodes())
	})

	// 3. WebSocket Clients endpoint
	mux.HandleFunc("GET /api/clients", s.wsHub.HandleWS)

	// 4. Agent Ingestion & Registration
	mux.HandleFunc("POST /api/clients/v2/rpc", s.rpc.HandleV2RPC)
	mux.HandleFunc("POST /api/clients/register", s.rpc.HandleRegister)

	// 5. Admin Authentication & Management
	mux.HandleFunc("POST /api/admin/login", func(w http.ResponseWriter, r *http.Request) {
		var req struct {
			Password string `json:"password"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}

		if !s.store.VerifyAdminPassword(req.Password) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "invalid password"})
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
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != "" {
			s.adminTokens.Delete(token)
		}
		http.SetCookie(w, &http.Cookie{
			Name:   "admin_token",
			Value:  "",
			Path:   "/",
			MaxAge: -1,
		})
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// Node Management
	mux.HandleFunc("POST /api/admin/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			Name           string  `json:"name"`
			Group          string  `json:"group"`
			Region         string  `json:"region"`
			TrafficLimitGB float64 `json:"traffic_limit_gb"`
			ResetDay       int     `json:"reset_day"`
			InitialUsedGB  float64 `json:"initial_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}

		node, err := s.store.CreateNodeWithOptions(store.NodeOptions{
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
			Name           string  `json:"name"`
			Group          string  `json:"group"`
			Region         string  `json:"region"`
			Weight         int     `json:"weight"`
			TrafficLimitGB float64 `json:"traffic_limit_gb"`
			ResetDay       int     `json:"reset_day"`
			InitialUsedGB  float64 `json:"initial_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}

		if err := s.store.UpdateNodeWithOptions(uuid, store.NodeOptions{
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
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "invalid json"})
			return
		}

		var pts []protocol.PingTarget
		if req.PingTargets != nil {
			pts = *req.PingTargets
		}
		if req.SiteTitle != "" || req.Announcement != "" || req.PingTargets != nil {
			_ = s.store.UpdateConfig(req.SiteTitle, req.Announcement, "", pts)
		}
		if req.NewPassword != "" {
			if err := s.store.SetAdminPassword(req.NewPassword); err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
				return
			}
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	// 6. Dynamic Installer
	mux.HandleFunc("GET /install.sh", HandleInstallScript(s.store))

	// 7. Embedded Web UI (Catch-all for SPA)
	mux.Handle("/", web.Handler())
	return mux
}

func (s *Server) Run() error {
	handler := s.Handler()
	log.Printf("[Server] VibeMonitor listening on http://%s", s.addr)
	return http.ListenAndServe(s.addr, handler)
}

