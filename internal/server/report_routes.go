package server

import (
	"context"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"time"

	"vibemonitor/internal/store"
)

func (s *Server) runScheduledReportGeneration(ctx context.Context, now time.Time) error {
	schedule, err := s.store.ReportScheduleContext(ctx)
	if err != nil || !schedule.Enabled {
		return err
	}
	date, err := schedule.ScheduledReportDate(now)
	if err != nil {
		return err
	}
	_, err = s.store.GenerateDailyReportContext(ctx, date, now)
	if errors.Is(err, store.ErrReportDayOpen) {
		// If the schedule's timezone is more than a day ahead of a node,
		// the previous eligible date may have settled only just now.
		_, err = s.store.GenerateDailyReportContext(ctx, date.AddDate(0, 0, -1), now)
	}
	if errors.Is(err, store.ErrReportDayOpen) || errors.Is(err, store.ErrReportNoNodes) {
		return nil
	}
	return err
}

func (s *Server) registerReportRoutes(mux *http.ServeMux) {
	adminOnly := func(handler http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Cache-Control", "no-store")
			if !s.checkAdmin(r) {
				writeJSON(w, http.StatusUnauthorized, map[string]string{"error": "unauthorized"})
				return
			}
			handler(w, r)
		}
	}
	mux.HandleFunc("GET /api/admin/reports/schedule", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		schedule, err := s.store.ReportScheduleContext(r.Context())
		if err != nil {
			log.Printf("[Daily report] Could not read schedule: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法读取自动生成设置"})
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	}))
	mux.HandleFunc("POST /api/admin/reports/schedule", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		var schedule store.ReportSchedule
		if err := json.NewDecoder(r.Body).Decode(&schedule); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]string{"error": "自动生成设置格式无效"})
			return
		}
		if err := s.store.UpdateReportScheduleContext(r.Context(), schedule); err != nil {
			if errors.Is(err, store.ErrInvalidReportSchedule) {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
			} else {
				log.Printf("[Daily report] Could not save schedule: %v", err)
				writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法保存自动生成设置"})
			}
			return
		}
		writeJSON(w, http.StatusOK, schedule)
	}))
	mux.HandleFunc("POST /api/admin/reports/generate", adminOnly(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Date string `json:"date"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			writeJSON(w, jsonErrorStatus(err), map[string]string{"error": "统计日期格式无效"})
			return
		}
		var date time.Time
		if request.Date != "" {
			var err error
			date, err = time.Parse(time.DateOnly, request.Date)
			if err != nil {
				writeJSON(w, http.StatusBadRequest, map[string]string{"error": "统计日期格式无效"})
				return
			}
		}
		date, created, err := s.generateReportManually(r.Context(), date, time.Now())
		switch {
		case err == nil:
			writeJSON(w, http.StatusOK, map[string]any{"date": date.Format(time.DateOnly), "created": created})
		case errors.Is(err, store.ErrReportDayOpen), errors.Is(err, store.ErrReportNoNodes), errors.Is(err, store.ErrReportExpired):
			writeJSON(w, http.StatusConflict, map[string]string{"error": err.Error()})
		default:
			log.Printf("[Daily report] Manual generation failed: %v", err)
			writeJSON(w, http.StatusInternalServerError, map[string]string{"error": "无法生成日报"})
		}
	}))
}

func (s *Server) generateReportManually(ctx context.Context, date, now time.Time) (time.Time, bool, error) {
	if !date.IsZero() {
		created, err := s.store.GenerateDailyReportContext(ctx, date, now)
		return date, created, err
	}
	today := now.In(dailyReportBeijing)
	for daysBack := 0; daysBack <= 2; daysBack++ {
		date = today.AddDate(0, 0, -daysBack)
		created, err := s.store.GenerateDailyReportContext(ctx, date, now)
		if !errors.Is(err, store.ErrReportDayOpen) {
			return date, created, err
		}
	}
	return time.Time{}, false, store.ErrReportDayOpen
}
