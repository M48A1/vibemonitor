package server

import (
	"crypto/subtle"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"net/netip"
	"strings"
	"time"

	"vibemonitor/internal/store"
	"vibemonitor/pkg/protocol"
)

type RPCHandler struct {
	store          *store.Store
	identityEpoch  string
	limit          rpcLimiter
	registerLimit  loginLimiter
	trustedProxies []netip.Prefix
}

func NewRPCHandler(s *store.Store, trustedProxies []netip.Prefix) *RPCHandler {
	return &RPCHandler{store: s, identityEpoch: store.GenerateToken(16), trustedProxies: trustedProxies}
}

// pruneLimits forgets the pace of tokens that stopped reporting, so a long-lived
// server does not accumulate an entry per node ever created.
func (h *RPCHandler) pruneLimits() {
	h.limit.pruneExpired()
	h.registerLimit.pruneExpired()
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

// maxRegisterNameLen keeps a self-registered name from filling the panel; a
// hostname is far shorter.
const maxRegisterNameLen = 64

// validRegisterName accepts the characters a hostname or a hand-typed node name
// is likely to carry on a command line. Anything else falls back to a default
// rather than refusing the install.
func validRegisterName(name string) bool {
	if name == "" || len(name) > maxRegisterNameLen {
		return false
	}
	for _, r := range name {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		case r == '-' || r == '_' || r == '.':
		default:
			return false
		}
	}
	return true
}

func (h *RPCHandler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}

	adKey := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
	// One read of key and deadline: an expiring window must not hand a key to a
	// request that arrived before it closed and a refusal to one that did not.
	// The comparison is constant time because the key is a credential.
	key, open := h.store.RegistrationOpen(time.Now())
	if !open || subtle.ConstantTimeCompare([]byte(adKey), []byte(key)) != 1 {
		writeJSON(w, http.StatusUnauthorized, map[string]any{
			"status": "error",
			"error":  "no registration window is open, or the key is invalid",
		})
		return
	}

	// A window is meant for one machine at a time: the same address registering
	// in a loop would create nodes without bound.
	if !h.registerLimit.allow(r, h.trustedProxies...) {
		w.Header().Set("Retry-After", "300")
		writeJSON(w, http.StatusTooManyRequests, map[string]any{
			"status": "error",
			"error":  "too many registrations from this address; retry in 5 minutes",
		})
		return
	}

	name := r.URL.Query().Get("name")
	if !validRegisterName(name) {
		name = "auto-node"
	}

	node, err := h.store.CreateNodeContext(r.Context(), name, "Auto", "DEFAULT")
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
