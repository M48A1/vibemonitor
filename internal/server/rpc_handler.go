package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

type RPCHandler struct {
	store *store.Store
}

func NewRPCHandler(s *store.Store) *RPCHandler {
	return &RPCHandler{store: s}
}

func extractToken(r *http.Request) string {
	// 1. Query parameter
	if t := r.URL.Query().Get("token"); t != "" {
		return strings.TrimSpace(t)
	}
	// 2. Authorization header
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	// 3. X-Token header
	if x := r.Header.Get("X-Token"); x != "" {
		return strings.TrimSpace(x)
	}
	return ""
}

func getClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		parts := strings.Split(xff, ",")
		return strings.TrimSpace(parts[0])
	}
	if xrip := r.Header.Get("X-Real-IP"); xrip != "" {
		return strings.TrimSpace(xrip)
	}
	addr := r.RemoteAddr
	if idx := strings.LastIndex(addr, ":"); idx != -1 {
		return addr[:idx]
	}
	return addr
}

func (h *RPCHandler) HandleV2RPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	token := extractToken(r)
	if token == "" || h.store.FindNodeByToken(token) == nil {
		writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(nil, -32000, "invalid token", nil))
		return
	}

	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxRequestBytes))
	if err != nil {
		writeJSON(w, jsonErrorStatus(err), protocol.ErrorResponse(nil, -32700, "failed to read body", nil))
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.ErrorResponse(nil, -32700, "parse error", nil))
		return
	}

	clientIP := getClientIP(r)

	switch req.Method {
	case protocol.MethodAgentBasicInfo:
		var params protocol.BasicInfoParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusOK, protocol.ErrorResponse(req.ID, -32602, "invalid basic info params", err.Error()))
			return
		}
		node, err := h.store.IngestBasicInfo(token, params.Info, clientIP)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(req.ID, -32000, err.Error(), nil))
			return
		}
		targets := h.store.NodeTargets(node.UUID)
		log.Printf("[RPC] BasicInfo reported from node %s (%s)", node.Name, clientIP)
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{
			"status":       "success",
			"ping_targets": targets,
		}))

	case protocol.MethodAgentReport:
		var params protocol.ReportParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusOK, protocol.ErrorResponse(req.ID, -32602, "invalid report params", err.Error()))
			return
		}
		node, err := h.store.IngestReport(token, params.Report, clientIP)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(req.ID, -32000, err.Error(), nil))
			return
		}
		targets := h.store.NodeTargets(node.UUID)
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{
			"status":       "success",
			"events":       []any{},
			"ping_targets": targets,
		}))

	case protocol.MethodAgentPull:
		// Used by VibeMonitor agents to poll tasks/events. We have no arbitrary commands, so return empty events.
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{
			"status": "success",
			"events": []any{},
		}))

	case protocol.MethodAgentPingResult:
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{"status": "success"}))

	default:
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{"status": "ignored"}))
	}
}

func (h *RPCHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	auth := r.Header.Get("Authorization")
	adKey := strings.TrimPrefix(auth, "Bearer ")
	config := h.store.GetConfig()

	if config.AutoDiscoveryKey == "" || adKey != config.AutoDiscoveryKey {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"status": "error",
			"error":  "invalid auto discovery key",
		})
		return
	}

	name := r.URL.Query().Get("name")
	if name == "" {
		name = "auto-node"
	}

	node, err := h.store.CreateNode(name, "Auto", "DEFAULT")
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "error",
			"error":  err.Error(),
		})
		return
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status": "success",
		"data": map[string]any{
			"uuid":  node.UUID,
			"token": node.Token,
		},
	})
}
