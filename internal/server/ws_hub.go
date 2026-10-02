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
	compact bool
	conn    *websocket.Conn
	sendCh  chan []byte
}

type WSHub struct {
	mu                 sync.RWMutex
	clients            map[*wsClient]struct{}
	closed             bool
	store              *store.Store
	trigger            chan struct{}
	stop               chan struct{}
	done               chan struct{}
	closeOnce          sync.Once
	lastPayload        []byte
	lastCompactPayload []byte
	payloadMu          sync.Mutex
}

const maxWSClients = 1000

func NewWSHub(s *store.Store) *WSHub {
	hub := &WSHub{
		clients: make(map[*wsClient]struct{}),
		store:   s,
		trigger: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
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
	defer close(h.done)

	minInterval := 1 * time.Second
	lastBroadcast := time.Now()
	// Send coalesced updates as soon as the rate limit expires, independently
	// of the periodic offline-state check.
	timer := time.NewTimer(time.Hour)
	timer.Stop()
	defer timer.Stop()
	var pending <-chan time.Time
	flush := func() {
		if !timer.Stop() {
			select {
			case <-timer.C:
			default:
			}
		}
		pending = nil
		lastBroadcast = time.Now()
		h.broadcastNodes()
	}

	for {
		select {
		case <-h.stop:
			return
		case <-h.trigger:
			if time.Since(lastBroadcast) >= minInterval {
				flush()
			} else if pending == nil {
				timer.Reset(minInterval - time.Since(lastBroadcast))
				pending = timer.C
			}
		case <-pending:
			flush()
		case <-ticker.C:
			if time.Since(lastBroadcast) >= minInterval {
				flush()
			}
		}
	}
}

func (h *WSHub) Close() {
	h.closeOnce.Do(func() {
		h.mu.Lock()
		h.closed = true
		clients := make([]*wsClient, 0, len(h.clients))
		for client := range h.clients {
			clients = append(clients, client)
		}
		h.mu.Unlock()
		close(h.stop)
		for _, client := range clients {
			if client.conn != nil {
				_ = client.conn.CloseNow()
			}
		}
		h.store.SetOnUpdate(nil)
	})
	<-h.done
}

func (h *WSHub) HandleWS(w http.ResponseWriter, r *http.Request) {
	h.mu.RLock()
	tooMany := len(h.clients) >= maxWSClients
	closed := h.closed
	h.mu.RUnlock()
	if closed {
		http.Error(w, "server shutting down", http.StatusServiceUnavailable)
		return
	}
	if tooMany {
		http.Error(w, "too many connections", http.StatusServiceUnavailable)
		return
	}
	conn, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Require the browser Origin to match the request Host, including behind a reverse proxy.
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
		compact: r.URL.Query().Get("view") == "dashboard",
		conn:    conn,
		sendCh:  make(chan []byte, 4),
	}

	h.mu.Lock()
	if h.closed {
		h.mu.Unlock()
		_ = conn.CloseNow()
		return
	}
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
	payload, err := h.nodesPayload(client.compact)
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

func (h *WSHub) nodesPayload(compact ...bool) ([]byte, error) {
	var nodes []*store.Node
	if len(compact) > 0 && compact[0] {
		nodes = h.store.GetDashboardNodes()
	} else {
		nodes = h.store.GetNodes()
	}
	cfg := h.store.GetConfig()
	return json.Marshal(map[string]any{
		"nodes":      nodes,
		"site_title": cfg.SiteTitle,
		"site_theme": cfg.SiteTheme,
		"site_icon":  cfg.SiteIcon,
		"status":     "success",
	})
}

// broadcastNodes sends the current node and public appearance state to clients.
// If the serialised payload is identical to the previous broadcast it is skipped
// (deduplication), which prevents redundant writes on high-frequency ticks.
func (h *WSHub) broadcastNodes() {
	h.doBroadcastNodes(false)
}

// forceBroadcastNodes immediately sends a fresh payload after a settings change.
func (h *WSHub) forceBroadcastNodes() {
	h.doBroadcastNodes(true)
}

func (h *WSHub) doBroadcastNodes(force bool) {
	h.mu.RLock()
	groups := [2][]*wsClient{}
	for c := range h.clients {
		index := 0
		if c.compact {
			index = 1
		}
		groups[index] = append(groups[index], c)
	}
	h.mu.RUnlock()
	// Serialize only formats with subscribers. All clients in a group share bytes.
	for index, clients := range groups {
		if len(clients) == 0 {
			continue
		}
		payload, err := h.nodesPayload(index == 1)
		if err != nil {
			continue
		}
		h.payloadMu.Lock()
		last := &h.lastPayload
		if index == 1 {
			last = &h.lastCompactPayload
		}
		duplicate := !force && bytes.Equal(*last, payload)
		if !duplicate {
			*last = payload
		}
		h.payloadMu.Unlock()
		if duplicate {
			continue
		}
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
}
