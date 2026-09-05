package server

import (
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
	conn    *websocket.Conn
	writeMu sync.Mutex
}

func (c *wsClient) writeMessage(ctx context.Context, msgType websocket.MessageType, p []byte) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	return c.conn.Write(ctx, msgType, p)
}

type WSHub struct {
	mu      sync.RWMutex
	clients map[*wsClient]struct{}
	store   *store.Store
	trigger chan struct{}
}

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
	ticker := time.NewTicker(2 * time.Second)
	defer ticker.Stop()

	lastBroadcast := time.Now()
	minInterval := 1 * time.Second

	for {
		select {
		case <-h.trigger:
			// Throttled broadcast on update
			if time.Since(lastBroadcast) >= minInterval {
				lastBroadcast = time.Now()
				h.broadcastNodes()
			}
		case <-ticker.C:
			lastBroadcast = time.Now()
			h.broadcastNodes()
		}
	}
}

func (h *WSHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
	})
	if err != nil {
		log.Printf("[WS] Accept error: %v", err)
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	client := &wsClient{conn: conn}

	h.mu.Lock()
	h.clients[client] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.clients, client)
		h.mu.Unlock()
	}()

	// Send initial data immediately
	_ = h.sendNodesTo(client)

	ctx := r.Context()
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
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	return client.writeMessage(ctx, websocket.MessageText, payload)
}

func (h *WSHub) broadcastNodes() {
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

	for _, client := range clients {
		go func(c *wsClient) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = c.writeMessage(ctx, websocket.MessageText, payload)
		}(client)
	}
}
