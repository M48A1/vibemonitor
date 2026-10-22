package server

import (
	"bytes"
	"context"
	"encoding/json"
	"github.com/coder/websocket"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

func TestCompactAndLegacySubscribers(t *testing.T) {
	s, err := New(Options{DataFile: filepath.Join(t.TempDir(), "data.db"), AdminPassword: "test-password"})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	n, err := s.store.CreateNode("node", "", "")
	if err != nil {
		t.Fatal(err)
	}
	r := protocol.Report{UpdatedAt: time.Now()}
	r.CPU.Usage = 12
	s.store.IngestReport(n.Token, r)
	legacy := &wsClient{sendCh: make(chan []byte, 8)}
	compact := &wsClient{compact: true, sendCh: make(chan []byte, 8)}
	// Use a non-running hub to make dedup assertions independent of ticker timing.
	h := &WSHub{store: s.store, clients: map[*wsClient]struct{}{legacy: {}, compact: {}}}
	h.broadcastNodes()
	a, b := <-legacy.sendCh, <-compact.sendCh
	if !bytes.Contains(a, []byte(`"history"`)) || bytes.Contains(b, []byte(`"history"`)) {
		t.Fatal("subscriber formats not isolated")
	}
	for i := 0; i < 10; i++ {
		h.broadcastNodes()
	}
	if len(legacy.sendCh) != 0 || len(compact.sendCh) != 0 {
		t.Fatal("unchanged payload rebroadcast")
	}
	for _, v := range []struct {
		url     string
		history bool
	}{{"/api/nodes", true}, {"/api/nodes?view=dashboard", false}} {
		w := httptest.NewRecorder()
		s.Handler().ServeHTTP(w, httptest.NewRequest("GET", v.url, nil))
		var nodes []store.Node
		if err = json.Unmarshal(w.Body.Bytes(), &nodes); err != nil || len(nodes) != 1 {
			t.Fatal("invalid nodes response")
		}
		if (len(nodes[0].History) > 0) != v.history {
			t.Fatal("REST compatibility lost")
		}
	}
	ts := httptest.NewServer(s.Handler())
	defer ts.Close()
	for _, suffix := range []string{"", "?view=dashboard"} {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		conn, _, e := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http")+"/api/clients"+suffix, nil)
		if e != nil {
			cancel()
			t.Fatal(e)
		}
		_, payload, e := conn.Read(ctx)
		conn.CloseNow()
		cancel()
		if e != nil {
			t.Fatal(e)
		}
		if bytes.Contains(payload, []byte(`"history"`)) != (suffix == "") {
			t.Fatal("websocket query negotiation failed")
		}
	}

}
