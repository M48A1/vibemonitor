package server

import (
	"encoding/json"
	"errors"
	"net/http"

	"vibemonitor/internal/store"
)

func (s *Server) registerNodeRoutes(mux *http.ServeMux) {
	// Node Management
	mux.HandleFunc("GET /api/admin/nodes/{uuid}/profile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		node := s.store.GetNode(r.PathValue("uuid"))
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"profile": node.Profile})
	})

	mux.HandleFunc("GET /api/admin/nodes/{uuid}/token", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		node := s.store.GetNode(r.PathValue("uuid"))
		if node == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "node not found"})
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"token": node.Token})
	})

	mux.HandleFunc("POST /api/admin/nodes", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			Profile        *store.NodeProfile `json:"profile"`
			Name           string             `json:"name"`
			Group          string             `json:"group"`
			Region         string             `json:"region"`
			TrafficLimitGB float64            `json:"traffic_limit_gb"`
			ResetDay       int                `json:"reset_day"`
			InitialUsedGB  float64            `json:"initial_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		node, err := s.store.CreateNodeWithOptionsContext(r.Context(), store.NodeOptions{
			Profile:        req.Profile,
			Name:           req.Name,
			Group:          req.Group,
			Region:         req.Region,
			TrafficLimitGB: req.TrafficLimitGB,
			ResetDay:       req.ResetDay,
			InitialUsedGB:  req.InitialUsedGB,
		})
		if err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrInvalidNodeOptions) || errors.Is(err, store.ErrInvalidNodeOrder) || errors.Is(err, store.ErrInvalidTrafficWarning) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success", "node": node})
	})

	mux.HandleFunc("PUT /api/admin/nodes/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		uuid := r.PathValue("uuid")
		var req struct {
			Profile        *store.NodeProfile `json:"profile"`
			Name           string             `json:"name"`
			Group          string             `json:"group"`
			Region         string             `json:"region"`
			TrafficLimitGB *float64           `json:"traffic_limit_gb"`
			ResetDay       *int               `json:"reset_day"`
			InitialUsedGB  *float64           `json:"initial_used_gb"`
			CycleUsedGB    *float64           `json:"cycle_used_gb"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}

		trafficLimitGB, initialUsedGB, resetDay := -1.0, -1.0, -1
		if (req.TrafficLimitGB != nil && *req.TrafficLimitGB < 0) ||
			(req.InitialUsedGB != nil && *req.InitialUsedGB < 0) ||
			(req.ResetDay != nil && *req.ResetDay < 0) {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "traffic values and reset day must be nonnegative"})
			return
		}
		if req.TrafficLimitGB != nil {
			trafficLimitGB = *req.TrafficLimitGB
		}
		if req.InitialUsedGB != nil {
			initialUsedGB = *req.InitialUsedGB
		}
		if req.ResetDay != nil {
			resetDay = *req.ResetDay
		}
		if err := s.store.UpdateNodeWithOptionsContext(r.Context(), uuid, store.NodeOptions{
			Profile:        req.Profile,
			Name:           req.Name,
			Group:          req.Group,
			Region:         req.Region,
			TrafficLimitGB: trafficLimitGB,
			ResetDay:       resetDay,
			InitialUsedGB:  initialUsedGB,
			CycleUsedGB:    req.CycleUsedGB,
		}); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrInvalidCycleUsage) || errors.Is(err, store.ErrInvalidNodeOptions) || errors.Is(err, store.ErrInvalidNodeOrder) || errors.Is(err, store.ErrInvalidTrafficWarning) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	mux.HandleFunc("DELETE /api/admin/nodes/{uuid}", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		uuid := r.PathValue("uuid")
		if err := s.store.DeleteNodeContext(r.Context(), uuid); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

	mux.HandleFunc("PUT /api/admin/nodes/reorder", func(w http.ResponseWriter, r *http.Request) {
		if !s.checkAdmin(r) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		var req struct {
			Order []struct {
				UUID   string `json:"uuid"`
				Weight int    `json:"weight"`
			} `json:"order"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]any{"error": "invalid or oversized json"})
			return
		}
		orderMap := make(map[string]int, len(req.Order))
		for _, entry := range req.Order {
			orderMap[entry.UUID] = entry.Weight
		}
		if err := s.store.ReorderNodesContext(r.Context(), orderMap); err != nil {
			status := http.StatusInternalServerError
			if errors.Is(err, store.ErrInvalidNodeOrder) {
				status = http.StatusBadRequest
			}
			writeJSON(w, status, map[string]any{"error": err.Error()})
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"status": "success"})
	})

}
