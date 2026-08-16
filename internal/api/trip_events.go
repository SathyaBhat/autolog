package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"github.com/sathyabhat/autolog/internal/trips"
)

type tripEventRunner interface {
	StartExplicitTrip(context.Context, time.Time) error
	StopExplicitTrip(context.Context, time.Time) (trips.Trip, bool, error)
}

type TripEvents struct {
	runner tripEventRunner
	token  string
	log    *zap.Logger
}

func NewTripEvents(r tripEventRunner, token string, logs ...*zap.Logger) *TripEvents {
	log := zap.NewNop()
	if len(logs) > 0 && logs[0] != nil {
		log = logs[0]
	}
	return &TripEvents{runner: r, token: token, log: log}
}

type eventRequest struct {
	Timestamp string `json:"timestamp"`
}

func (s *TripEvents) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/trips/start", s.handleStart)
	mux.HandleFunc("/api/trips/stop", s.handleStop)
	return s.auth(mux)
}

func (s *TripEvents) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth := strings.TrimSpace(r.Header.Get("Authorization"))
		if s.token == "" || !strings.EqualFold(strings.TrimPrefix(auth, "Bearer "), s.token) {
			s.log.Warn("trip event unauthorized",
				zap.String("path", r.URL.Path),
				zap.String("remote_addr", r.RemoteAddr))
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *TripEvents) handleStart(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.log.Info("trip start event received", zap.String("remote_addr", r.RemoteAddr))
	t, provided, payload, err := parseEventTime(r)
	if err != nil {
		s.log.Error("trip start event failed",
			zap.String("payload", payload),
			zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]string{"status": "error"})
		return
	}
	s.log.Info("trip start event submitted",
		zap.String("payload", payload),
		zap.Time("event_timestamp", t.UTC()),
		zap.String("timestamp_source", timestampSource(provided)),
		zap.Time("received_at", time.Now().UTC()))
	if err := s.runner.StartExplicitTrip(r.Context(), t); err != nil {
		s.log.Error("trip start event failed",
			zap.Time("timestamp", t),
			zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]string{"status": "error"})
		return
	}
	s.log.Info("trip start event accepted", zap.Time("timestamp", t.UTC()))
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *TripEvents) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	s.log.Info("trip stop event received", zap.String("remote_addr", r.RemoteAddr))
	t, provided, payload, err := parseEventTime(r)
	if err != nil {
		s.log.Error("trip stop event failed",
			zap.String("payload", payload),
			zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]string{"status": "error"})
		return
	}
	s.log.Info("trip stop event submitted",
		zap.String("payload", payload),
		zap.Time("event_timestamp", t.UTC()),
		zap.String("timestamp_source", timestampSource(provided)),
		zap.Time("received_at", time.Now().UTC()))
	trip, completed, err := s.runner.StopExplicitTrip(r.Context(), t)
	if err != nil {
		s.log.Error("trip stop event failed",
			zap.Time("timestamp", t),
			zap.Error(err))
		writeJSON(w, http.StatusOK, map[string]string{"status": "error"})
		return
	}
	s.log.Info("trip stop event accepted",
		zap.Time("timestamp", t.UTC()),
		zap.Bool("completed", completed),
		zap.String("date", trip.Date),
		zap.Float64("distance_km", trip.DistanceKm))
	writeJSON(w, http.StatusOK, map[string]any{
		"status": func() string {
			if completed {
				return "completed"
			}
			return "ongoing"
		}(),
	})
}

func parseEventTime(r *http.Request) (time.Time, bool, string, error) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 4096))
	if err != nil {
		return time.Time{}, false, string(body), err
	}
	payload := string(body)
	var req eventRequest
	if len(strings.TrimSpace(payload)) > 0 {
		if err := json.Unmarshal(body, &req); err != nil {
			return time.Time{}, false, payload, err
		}
	}
	if req.Timestamp == "" {
		return time.Now().UTC(), false, payload, nil
	}
	t, err := time.Parse(time.RFC3339Nano, req.Timestamp)
	return t, true, payload, err
}

func timestampSource(provided bool) string {
	if provided {
		return "shortcut"
	}
	return "server_fallback"
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
