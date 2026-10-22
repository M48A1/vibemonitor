package server

import (
	"errors"
	"log"
	"net/http"

	"vibemonitor/internal/store"
)

func (s *Server) registerHistoryRoutes(mux *http.ServeMux) {
	// 2. Public Nodes Info & Ping History
	mux.HandleFunc("GET /api/nodes", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("view") == "dashboard" {
			writeJSON(w, http.StatusOK, s.store.GetDashboardNodes())
			return
		}
		writeJSON(w, http.StatusOK, s.store.GetNodes())
	})

	mux.HandleFunc("GET /api/nodes/ping-history", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.URL.Query().Get("uuid")
		target := r.URL.Query().Get("target")
		timeRange := r.URL.Query().Get("range")
		if timeRange == "" {
			timeRange = "1h"
		}
		if uuid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing uuid parameter"})
			return
		}
		resp, err := s.store.GetPingHistoryContext(r.Context(), uuid, target, timeRange)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			if errors.Is(err, store.ErrNodeNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
				return
			}
			log.Printf("[Store] Ping history query failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load ping history"})
			return
		}
		resp.Host = ""
		for i := range resp.Samples {
			resp.Samples[i].Host = ""
		}
		writeJSON(w, http.StatusOK, resp)
	})
	mux.HandleFunc("GET /api/nodes/resource-history", func(w http.ResponseWriter, r *http.Request) {
		uuid := r.URL.Query().Get("uuid")
		metric := r.URL.Query().Get("metric")
		timeRange := r.URL.Query().Get("range")
		if metric != "cpu" && metric != "memory" && metric != "disk" && metric != "network" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid resource metric"})
			return
		}
		switch timeRange {
		case "1h", "24h", "7d", "31d":
		default:
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid resource range"})
			return
		}
		if uuid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "missing uuid parameter"})
			return
		}
		resp, err := s.store.GetResourceHistoryContext(r.Context(), uuid, metric, timeRange)
		if err != nil {
			if r.Context().Err() != nil {
				return
			}
			if errors.Is(err, store.ErrNodeNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
				return
			}
			log.Printf("[Store] Resource history query failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load resource history"})
			return
		}
		writeJSON(w, http.StatusOK, resp)
	})

	mux.HandleFunc("GET /api/nodes/traffic-history", func(w http.ResponseWriter, r *http.Request) {
		uuid, span := r.URL.Query().Get("uuid"), r.URL.Query().Get("range")
		if uuid == "" || (span != "current" && span != "previous" && span != "three") {
			writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid traffic history parameters"})
			return
		}
		response, err := s.store.GetTrafficHistory(uuid, span)
		if err != nil {
			if errors.Is(err, store.ErrNodeNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{"error": "node not found"})
			} else {
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "could not load traffic history"})
			}
			return
		}
		writeJSON(w, http.StatusOK, response)
	})

}
