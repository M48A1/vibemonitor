package server

import (
	"encoding/json"
	"io"
	"log"
	"net/http"
	"strings"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

type RPCHandler struct {
	store         *store.Store
	identityEpoch string
	limit         rpcLimiter
}

func NewRPCHandler(s *store.Store) *RPCHandler {
	return &RPCHandler{store: s, identityEpoch: store.GenerateToken(16)}
}

// pruneLimits forgets the pace of tokens that stopped reporting, so a long-lived
// server does not accumulate an entry per node ever created.
func (h *RPCHandler) pruneLimits() {
	h.limit.pruneExpired()
}

func extractToken(r *http.Request) string {
	if auth := r.Header.Get("Authorization"); strings.HasPrefix(auth, "Bearer ") {
		return strings.TrimSpace(strings.TrimPrefix(auth, "Bearer "))
	}
	if x := r.Header.Get("X-Token"); x != "" {
		return strings.TrimSpace(x)
	}
	return ""
}

func (h *RPCHandler) HandleV2RPC(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	if r.URL.Query().Has("token") {
		writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(nil, -32000, "token in URL is not supported", nil))
		return
	}

	token := extractToken(r)
	if token == "" || h.store.FindNodeByToken(token) == nil {
		writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(nil, -32000, "invalid token", nil))
		return
	}

	// Server.Handler applies the request size limit before routing here.
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeJSON(w, jsonErrorStatus(err), protocol.ErrorResponse(nil, -32700, "failed to read body", nil))
		return
	}

	var req protocol.Request
	if err := json.Unmarshal(body, &req); err != nil {
		writeJSON(w, http.StatusBadRequest, protocol.ErrorResponse(nil, -32700, "parse error", nil))
		return
	}

	switch req.Method {
	case protocol.MethodAgentBasicInfo:
		var params protocol.BasicInfoParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusOK, protocol.ErrorResponse(req.ID, -32602, "invalid basic info params", err.Error()))
			return
		}
		if !h.limit.allow(protocol.MethodAgentBasicInfo, token, time.Now(), basicInfoSpacing) {
			writeJSON(w, http.StatusTooManyRequests, protocol.ErrorResponse(req.ID, -32000, "basic info arrives too often; retry later", nil))
			return
		}
		node, err := h.store.IngestBasicInfo(token, params.Info)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(req.ID, -32000, err.Error(), nil))
			return
		}
		targets := h.store.NodeTargets(node.UUID)
		log.Printf("[RPC] BasicInfo reported from node %s", node.Name)
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{
			"status":                 "success",
			"ping_targets":           targets,
			"network_identity_epoch": h.identityEpoch,
		}))

	case protocol.MethodAgentReport:
		var params protocol.ReportParams
		if err := json.Unmarshal(req.Params, &params); err != nil {
			writeJSON(w, http.StatusOK, protocol.ErrorResponse(req.ID, -32602, "invalid report params", err.Error()))
			return
		}
		if !h.limit.allow(protocol.MethodAgentReport, token, time.Now(), reportSpacing(params.Report.ReportIntervalSeconds)) {
			writeJSON(w, http.StatusTooManyRequests, protocol.ErrorResponse(req.ID, -32000, "reports arrive faster than the interval this node declares; this one was not stored", nil))
			return
		}
		node, err := h.store.IngestReport(token, params.Report)
		if err != nil {
			writeJSON(w, http.StatusUnauthorized, protocol.ErrorResponse(req.ID, -32000, err.Error(), nil))
			return
		}
		targets := h.store.NodeTargets(node.UUID)
		writeJSON(w, http.StatusOK, protocol.SuccessResponse(req.ID, map[string]any{
			"status":                 "success",
			"events":                 []any{},
			"ping_targets":           targets,
			"network_identity_epoch": h.identityEpoch,
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
