package server

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"
)

func TestServerStopsWebSocketHub(t *testing.T) {
	s, err := New(Options{ListenAddr: "invalid-listen-address", DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "pass"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Run(context.Background()); err == nil {
		t.Fatal("invalid listen address was accepted")
	}
	select {
	case <-s.wsHub.done:
	default:
		t.Fatal("WebSocket hub still running after server exit")
	}
	w := httptest.NewRecorder()
	s.wsHub.HandleWS(w, httptest.NewRequest(http.MethodGet, "/api/clients", nil))
	if w.Code != http.StatusServiceUnavailable {
		t.Fatalf("closed hub accepted a new connection: %d", w.Code)
	}
}
