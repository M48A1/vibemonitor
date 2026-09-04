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

type WSHub struct {
	mu      sync.RWMutex
	conns   map[*websocket.Conn]struct{}
	store   *store.Store
	trigger chan struct{}
}

func NewWSHub(s *store.Store) *WSHub {
	hub := &WSHub{
		conns:   make(map[*websocket.Conn]struct{}),
		store:   s,
		trigger: make(chan struct{}, 10),
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

	for {
		select {
		case <-hubWakeup(h.trigger, ticker.C):
			h.broadcastNodes()
		}
	}
}

func hubWakeup(c1 <-chan struct{}, c2 <-chan time.Time) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		select {
		case <-c1:
			out <- struct{}{}
		case <-c2:
			out <- struct{}{}
		}
	}()
	return out
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

	h.mu.Lock()
	h.conns[conn] = struct{}{}
	h.mu.Unlock()

	defer func() {
		h.mu.Lock()
		delete(h.conns, conn)
		h.mu.Unlock()
	}()

	// Send initial data immediately
	_ = h.sendNodesTo(conn)

	ctx := r.Context()
	for {
		typ, msg, err := conn.Read(ctx)
		if err != nil {
			break
		}
		if typ == websocket.MessageText {
			if string(msg) == "get" {
				_ = h.sendNodesTo(conn)
			}
		}
	}
}

func (h *WSHub) sendNodesTo(conn *websocket.Conn) error {
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
	return conn.Write(ctx, websocket.MessageText, payload)
}

func (h *WSHub) broadcastNodes() {
	h.mu.RLock()
	conns := make([]*websocket.Conn, 0, len(h.conns))
	for c := range h.conns {
		conns = append(conns, c)
	}
	h.mu.RUnlock()

	if len(conns) == 0 {
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

	for _, conn := range conns {
		go func(c *websocket.Conn) {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = c.Write(ctx, websocket.MessageText, payload)
		}(conn)
	}
}
