package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestSettingsCannotSwitchAwayFromHex(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "old-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.store.Close()
	s.adminTokens.Store("admin", time.Now().Add(time.Hour))
	h := s.Handler()
	submit := func(body string, admin bool) *httptest.ResponseRecorder {
		r := httptest.NewRequest("POST", "/api/admin/settings", strings.NewReader(body))
		if admin {
			r.Header.Set("Authorization", "Bearer admin")
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := submit(`{"site_theme":"hex","color_mode":"light"}`, false); w.Code != http.StatusUnauthorized {
		t.Fatalf("visitor can change theme: %d", w.Code)
	}
	if w := submit(`{"site_theme":"hex","color_mode":"dark"}`, true); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if w := submit(`{"site_title":"Renamed"}`, true); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/public", nil))
	var public map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &public); err != nil {
		t.Fatal(err)
	}
	if public["site_theme"] != nil || public["color_mode"] != nil {
		t.Fatalf("removed appearance settings are still public: %v", public)
	}
	// Every browser starts with the same selection, including SPA paths.
	for _, path := range []string{"/", "/index.html", "/dashboard"} {
		w = httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusOK || !strings.Contains(w.Body.String(), `data-site-theme="hex" data-theme="light"`) {
			t.Fatalf("wrong first paint at %s: %d", path, w.Code)
		}
	}
	client := &wsClient{sendCh: make(chan []byte, 1)}
	if err := s.wsHub.sendNodesTo(client); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(<-client.sendCh), `"appearance"`) {
		t.Fatal("initial websocket payload still contains appearance settings")
	}
	s.wsHub.mu.Lock()
	s.wsHub.clients[client] = struct{}{}
	s.wsHub.mu.Unlock()
	if w := submit(`{"site_theme":"default","color_mode":"dark"}`, true); w.Code != http.StatusOK {
		t.Fatal(w.Body.String())
	}
	if cfg := s.store.GetConfig(); cfg.SiteTheme != "hex" || cfg.ColorMode != "light" {
		t.Fatal("legacy client changed the fixed appearance")
	}
	s.wsHub.forceBroadcastNodes()
	if strings.Contains(string(<-client.sendCh), `"appearance"`) {
		t.Fatal("broadcast still contains appearance settings")
	}
	s.wsHub.mu.Lock()
	delete(s.wsHub.clients, client)
	s.wsHub.mu.Unlock()
}
