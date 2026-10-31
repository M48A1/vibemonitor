package server

import (
	"net/http"

	"vibemonitor/internal/web"
)

// registerAdminPageRoutes serves the management area as real pages. Each page is
// a separate document with its own script, so the dashboard stays a public view
// and only the section in use loads its code.
//
// A visitor without an admin session never receives a management document: they
// are sent back to the dashboard, where the only login dialog lives. That keeps
// the management area from advertising its forms, ids and routes to anonymous
// callers; the write APIs stay protected on their own.
func (s *Server) registerAdminPageRoutes(mux *http.ServeMux) {
	redirect := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			sendToDashboard(w, r)
			return
		}
		http.Redirect(w, r, "/admin/nodes", http.StatusFound)
	})
	mux.HandleFunc("GET /admin", redirect)
	mux.HandleFunc("GET /admin/{$}", redirect)
	mux.Handle("GET /admin/nodes", s.adminPage("admin-nodes.html"))
	mux.Handle("GET /admin/settings", s.adminPage("admin-settings.html"))
	mux.Handle("GET /admin/theme", s.adminPage("admin-theme.html"))
	mux.Handle("GET /admin/telegram", s.adminPage("admin-telegram.html"))
	// An unknown management URL must not fall through to the dashboard shell,
	// which would look like a broken admin page.
	mux.HandleFunc("GET /admin/", http.NotFound)
}

// adminPage serves one management document to a signed-in admin only. The check
// runs before the embedded document handler so its ETag and conditional request
// handling can never hand the page to a session that already signed out.
func (s *Server) adminPage(name string) http.Handler {
	page := web.DocumentHandler(name)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			sendToDashboard(w, r)
			return
		}
		page.ServeHTTP(w, r)
	})
}

// sendToDashboard bounces an anonymous management request. The answer depends on
// the session cookie, so no cache may keep the redirect.
func sendToDashboard(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	http.Redirect(w, r, "/", http.StatusFound)
}
