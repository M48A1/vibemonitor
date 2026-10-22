package server

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/coder/websocket"
	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

func TestWSConcurrentRegistrationLimit(t *testing.T) {
	h := &WSHub{clients: make(map[*wsClient]struct{})}
	var accepted atomic.Int32
	var wg sync.WaitGroup
	start := make(chan struct{})
	for i := 0; i < maxWSClients+200; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-start
			if h.registerClient(&wsClient{}) {
				accepted.Add(1)
			}
		}()
	}
	close(start)
	wg.Wait()
	if int(accepted.Load()) != maxWSClients || len(h.clients) != maxWSClients {
		t.Fatalf("accepted=%d registered=%d, limit=%d", accepted.Load(), len(h.clients), maxWSClients)
	}
	h.mu.Lock()
	h.clients = make(map[*wsClient]struct{})
	h.closed = true
	h.mu.Unlock()
	if h.registerClient(&wsClient{}) {
		t.Fatal("registered a client after shutdown")
	}
}

func TestWSSnapshotRequestRate(t *testing.T) {
	client := &wsClient{}
	now := time.Now()
	if !client.allowSnapshotRequest(now) {
		t.Fatal("initial request rejected")
	}
	for i := 1; i < 1000; i++ {
		if client.allowSnapshotRequest(now.Add(time.Duration(i) * time.Millisecond)) {
			t.Fatal("snapshot request flood bypassed one-second interval")
		}
	}
	if !client.allowSnapshotRequest(now.Add(time.Second)) {
		t.Fatal("request at the next interval rejected")
	}
}

func TestWSGetFloodDoesNotProduceExtraSnapshots(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "data.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	// No broadcast worker: only an explicit request could produce another frame.
	h := &WSHub{store: s, clients: make(map[*wsClient]struct{})}
	ts := httptest.NewServer(http.HandlerFunc(h.HandleWS))
	defer ts.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(ts.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.CloseNow()
	if _, _, err = conn.Read(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 50; i++ {
		if err = conn.Write(ctx, websocket.MessageText, []byte("get")); err != nil {
			t.Fatal(err)
		}
	}
	readCtx, stop := context.WithTimeout(ctx, 150*time.Millisecond)
	defer stop()
	if _, _, err = conn.Read(readCtx); err == nil {
		t.Fatal("get flood produced another snapshot inside the initial one-second interval")
	}
	if readCtx.Err() != context.DeadlineExceeded {
		t.Fatalf("unexpected websocket read error: %v", err)
	}
}

func TestWSSnapshotCacheFormatsAndForcedRefresh(t *testing.T) {
	s, err := store.New(filepath.Join(t.TempDir(), "data.db"), "test-password")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	node, err := s.CreateNode("first", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.IngestReport(node.Token, protocol.Report{UpdatedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	legacy := &wsClient{sendCh: make(chan []byte, 4)}
	compact := &wsClient{compact: true, sendCh: make(chan []byte, 4)}
	h := &WSHub{store: s, clients: map[*wsClient]struct{}{legacy: {}, compact: {}}}
	if err = h.sendNodesTo(legacy); err != nil {
		t.Fatal(err)
	}
	if err = h.sendNodesTo(compact); err != nil {
		t.Fatal(err)
	}
	full, small := <-legacy.sendCh, <-compact.sendCh
	if !bytes.Contains(full, []byte(`"history"`)) || bytes.Contains(small, []byte(`"history"`)) {
		t.Fatal("cached subscriber formats were mixed")
	}
	for i := 0; i < 10; i++ {
		if err = h.sendNodesTo(compact); err != nil {
			t.Fatal(err)
		}
		payload := <-compact.sendCh
		if &payload[0] != &small[0] {
			t.Fatal("resynchronisations did not share a recent snapshot")
		}
	}
	if err = s.UpdateConfig("updated title", "", nil); err != nil {
		t.Fatal(err)
	}
	h.forceBroadcastNodes()
	for _, client := range []*wsClient{legacy, compact} {
		if payload := <-client.sendCh; !bytes.Contains(payload, []byte("updated title")) {
			t.Fatal("forced appearance refresh returned cached settings")
		}
	}
	if _, err = s.CreateNode("new node", "", ""); err != nil {
		t.Fatal(err)
	}
	time.Sleep(minWSUpdateInterval + 20*time.Millisecond)
	if err = h.sendNodesTo(compact); err != nil {
		t.Fatal(err)
	}
	if payload := <-compact.sendCh; !bytes.Contains(payload, []byte("new node")) {
		t.Fatal("new node remained stale beyond one second")
	}
}
