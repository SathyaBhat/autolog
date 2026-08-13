package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/sathyabhat/autolog/internal/trips"
)

type tripEventRunner interface {
	StartExplicitTrip(context.Context, time.Time) error
	StopExplicitTrip(context.Context, time.Time) (trips.Trip, error)
}

type TripEvents struct {
	runner tripEventRunner
	token  string
}

func NewTripEvents(r tripEventRunner, token string) *TripEvents {
	return &TripEvents{runner: r, token: token}
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
	t, err := parseEventTime(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.runner.StartExplicitTrip(r.Context(), t); err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"status": "started", "timestamp": t.UTC()})
}

func (s *TripEvents) handleStop(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	t, err := parseEventTime(r)
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	trip, err := s.runner.StopExplicitTrip(r.Context(), t)
	if err != nil {
		http.Error(w, err.Error(), http.StatusConflict)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":      "stored",
		"date":        trip.Date,
		"start_time":  trip.StartTime,
		"end_time":    trip.EndTime,
		"distance_km": trip.DistanceKm,
	})
}

func parseEventTime(r *http.Request) (time.Time, error) {
	var req eventRequest
	if r.Body != nil {
		err := json.NewDecoder(io.LimitReader(r.Body, 4096)).Decode(&req)
		if err != nil && err != io.EOF {
			return time.Time{}, err
		}
	}
	if req.Timestamp == "" {
		return time.Now().UTC(), nil
	}
	return time.Parse(time.RFC3339Nano, req.Timestamp)
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
