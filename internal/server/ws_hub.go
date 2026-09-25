package server

import (
	"bytes"
	"context"
	"encoding/json"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/coder/websocket"
	"vibemonitor/internal/store"
)

type wsClient struct {
	conn   *websocket.Conn
	sendCh chan []byte
}

type WSHub struct {
	mu          sync.RWMutex
	clients     map[*wsClient]struct{}
	store       *store.Store
	trigger     chan struct{}
	lastPayload []byte
	payloadMu   sync.Mutex
}

const maxWSClients = 1000

func NewWSHub(s *store.Store) *WSHub {
	hub := &WSHub{
		clients: make(map[*wsClient]struct{}),
		store:   s,
		trigger: make(chan struct{}, 1),
	}
	s.SetOnUpdate(func() {
		select {
		case hub.trigger <- struct{}{}:
		default:
		}
	})
	go hub.run()
	return hub
}

func (h *WSHub) run() {
	// Periodic check every 3s to detect offline state transitions and keep alive
	ticker := time.NewTicker(3 * time.Second)
	defer ticker.Stop()

	minInterval := 1 * time.Second
	lastBroadcast := time.Now()
	var pendingTrigger bool

	for {
		select {
		case <-h.trigger:
			if time.Since(lastBroadcast) >= minInterval {
				lastBroadcast = time.Now()
				pendingTrigger = false
				h.broadcastNodes()
			} else {
				pendingTrigger = true
			}
		case <-ticker.C:
			if pendingTrigger || time.Since(lastBroadcast) >= minInterval {
				lastBroadcast = time.Now()
				pendingTrigger = false
				h.broadcastNodes()
			}
		}
	}
}

func (h *WSHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	tooMany := len(h.clients) >= maxWSClients
	h.mu.RUnlock()
	if tooMany {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: false,
	})
	if err != nil {
		log.Printf("[WS] Accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	ctx, cancel := context.WithCancel(r.Context())
	defer cancel()

	client := &wsClient{
		conn:   conn,
		sendCh: make(chan []byte, 4),
	}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
	}()

	// Dedicated single writer goroutine for this client
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg, ok := <-client.sendCh:
				if !ok {
					return
				}
				writeCtx, writeCancel := context.WithTimeout(ctx, 3*time.Second)
				err := conn.Write(writeCtx, websocket.MessageText, msg)
				writeCancel()
				if err != nil {
					cancel()
					return
				}
			}
		}
	}()

	// Send initial data immediately
	_ = h.sendNodesTo(client)

	for {
		typ, msg, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText {
			if string(msg) == "get" {
				_ = h.sendNodesTo(client)
			}
		}
	}
}

func (h *WSHub) sendNodesTo(client *wsClient) error {
	nodes := h.store.GetNodes()
	payload, err := json.Marshal(map[string]any{
		"nodes":  nodes,
		"status": "success",
	})
	if err != nil {
		return err
	}
	select {
	case client.sendCh <- payload:
	default:
		select {
		case <-client.sendCh:
		default:
		}
		select {
		case client.sendCh <- payload:
		default:
		}
	}
	return nil
}

// broadcastNodes sends the current node+appearance state to all connected clients.
// If the serialised payload is identical to the previous broadcast it is skipped
// (deduplication), which prevents redundant writes on high-frequency ticks.
func (h *WSHub) broadcastNodes() {
	h.doBroadcastNodes(false)
}

// forceBroadcastNodes sends the current state to all clients unconditionally,
// bypassing payload deduplication.  Use this after a settings change to ensure
// clients receive the update even if the node list itself did not change.
func (h *WSHub) forceBroadcastNodes() {
	h.doBroadcastNodes(true)
}

func (h *WSHub) doBroadcastNodes(force bool) {
	h.mu.RLock()
	clients := make([]*wsClient, 0, len(h.clients))
	for c := range h.clients {
		clients = append(clients, c)
	}
	h.mu.RUnlock()

	if len(clients) == 0 {
		return
	}

	nodes := h.store.GetNodes()
	payload, err := json.Marshal(map[string]any{
		"nodes":  nodes,
		"status": "success",
	})
	if err != nil {
		return
	}

	h.payloadMu.Lock()
	if !force && bytes.Equal(h.lastPayload, payload) {
		h.payloadMu.Unlock()
		return
	}
	h.lastPayload = payload
	h.payloadMu.Unlock()

	for _, client := range clients {
		select {
		case client.sendCh <- payload:
		default:
			select {
			case <-client.sendCh:
			default:
			}
			select {
			case client.sendCh <- payload:
			default:
			}
		}
	}
}
