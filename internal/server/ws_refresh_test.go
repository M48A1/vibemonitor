package server

import (
	"path/filepath"
	"testing"
	"time"

	"vibemonitor/internal/store"
)

func TestThrottledUpdateDoesNotWaitForPeriodicCheck(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "data.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	h := NewWSHub(s)
	defer h.Close()
	client := &wsClient{sendCh: make(chan []byte, 4)}
	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()
	// An update during the initial throttle window used to wait three seconds.
	h.trigger <- struct{}{}
	select {
	case <-client.sendCh:
	case <-time.After(2 * time.Second):
		t.Fatal("throttled update waited for the periodic offline check")
	}
}
