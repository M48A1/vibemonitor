package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/netip"
	"sync"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/internal/web"
)

type Server struct {
	addr           string
	store          *store.Store
	wsHub          *WSHub
	rpc            *RPCHandler
	adminTokens    sync.Map // token -> time.Time
	authMu         sync.RWMutex
	loginLimit     loginLimiter
	trustedProxies []netip.Prefix
}

type Options struct {
	ListenAddr    string
	DataFile      string
	AdminUsername string
	AdminPassword string
	TrustedProxy  string // Comma-separated IP addresses or CIDR ranges of immediate reverse proxies.
}

func New(opts Options) (*Server, error) {
	trustedProxies, err := parseTrustedProxies(opts.TrustedProxy)
	if err != nil {
		return nil, err
	}
	if opts.ListenAddr == "" {
		opts.ListenAddr = "[::]:1314"
	}
	if opts.DataFile == "" {
		opts.DataFile = "vibemonitor-data.db"
	}

	st, err := store.New(opts.DataFile, opts.AdminPassword, opts.AdminUsername)
	if err != nil {
		return nil, err
	}

	s := &Server{
		addr:           opts.ListenAddr,
		store:          st,
		wsHub:          NewWSHub(st),
		rpc:            NewRPCHandler(st, trustedProxies),
		trustedProxies: trustedProxies,
	}
	return s, nil
}

// Close stops background broadcasts before closing their backing store.
// Call it when a Server created with New is not run via Run.
func (s *Server) Close() error {
	s.wsHub.Close()
	return s.store.Close()
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Printf("[HTTP] encode JSON response failed: %v", err)
	}
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	s.registerThemeRoutes(mux)
	s.registerPublicRoutes(mux)
	s.registerHistoryRoutes(mux)
	s.registerTelegramRoutes(mux)
	s.registerAuthRoutes(mux)
	s.registerNodeRoutes(mux)
	s.registerSettingsRoutes(mux)

	// 3. WebSocket Clients endpoint
	mux.HandleFunc("GET /api/clients", s.wsHub.HandleWS)

	// 4. Agent Ingestion & Registration
	mux.HandleFunc("POST /api/clients/v2/rpc", s.rpc.HandleV2RPC)
	mux.HandleFunc("POST /api/clients/register", s.rpc.HandleRegister)

	// 6. Dynamic Installer
	mux.HandleFunc("GET /install.sh", HandleInstallScript(s.store, s.trustedProxies...))

	// 7. Embedded Web UI (Catch-all for SPA)
	mux.Handle("/", web.Handler())
	baseHandler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		limit := int64(maxRequestBytes)
		if r.Method == http.MethodPost && r.URL.Path == "/api/admin/upload-icon" {
			limit = store.MaxIconBytes + (64 << 10) // Allow multipart headers, not larger files.
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/admin/themes" {
			limit = store.MaxThemePackageBytes + (64 << 10)
		}
		r.Body = http.MaxBytesReader(w, r.Body, limit)
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data: https:; connect-src 'self' wss: ws:")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		mux.ServeHTTP(w, r)
	})
	return gzipMiddleware(baseHandler)
}

func (s *Server) Run(ctx context.Context) error {
	if ctx == nil {
		ctx = context.Background()
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	handler := s.Handler()
	httpServer := &http.Server{
		Addr:              s.addr,
		Handler:           handler,
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	// Periodic expired admin tokens cleaner
	alertsDone := make(chan struct{})
	go func() {
		defer close(alertsDone)
		s.runTelegramAlerts(runCtx)
	}()
	cleanupTicker := time.NewTicker(15 * time.Minute)
	defer cleanupTicker.Stop()
	go func() {
		for {
			select {
			case <-runCtx.Done():
				return
			case <-cleanupTicker.C:
				s.pruneExpiredTokens()
				s.loginLimit.pruneExpired()
				s.rpc.pruneLimits()
			}
		}
	}()

	errCh := make(chan error, 1)
	go func() {
		log.Printf("[Server] VibeMonitor listening on http://%s", s.addr)
		if err := httpServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
		close(errCh)
	}()

	select {
	case <-runCtx.Done():
		log.Printf("[Server] Shutting down gracefully...")
		<-alertsDone
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = httpServer.Shutdown(shutdownCtx)
		if err := s.Close(); err != nil {
			log.Printf("[Store] Final save failed: %v", err)
			return err
		}
		log.Printf("[Server] Data flushed and server stopped.")
		return nil
	case err := <-errCh:
		stop()
		<-alertsDone
		if err := s.Close(); err != nil {
			log.Printf("[Store] Final save failed: %v", err)
			return err
		}
		return err
	}
}
