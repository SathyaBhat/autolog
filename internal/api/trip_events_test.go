package api_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sathyabhat/autolog/internal/api"
	"github.com/sathyabhat/autolog/internal/trips"
)

type fakeRunner struct {
	start time.Time
	stop  time.Time
}

func (f *fakeRunner) StartExplicitTrip(_ context.Context, t time.Time) error {
	f.start = t
	return nil
}

func (f *fakeRunner) StopExplicitTrip(_ context.Context, t time.Time) (trips.Trip, error) {
	f.stop = t
	return trips.Trip{Date: "2026-08-13", DistanceKm: 1.2}, nil
}

func TestTripEvents_StartAndStop(t *testing.T) {
	fake := &fakeRunner{}
	handler := api.NewTripEvents(fake, "secret", zap.NewNop()).Handler()

	start := httptest.NewRequest(http.MethodPost, "/api/trips/start",
		strings.NewReader(`{"timestamp":"2026-08-13T00:50:00Z"}`))
	start.Header.Set("Authorization", "Bearer secret")
	startResp := httptest.NewRecorder()
	handler.ServeHTTP(startResp, start)
	require.Equal(t, http.StatusOK, startResp.Code)
	require.Equal(t, time.Date(2026, 8, 13, 0, 50, 0, 0, time.UTC), fake.start)

	stop := httptest.NewRequest(http.MethodPost, "/api/trips/stop",
		strings.NewReader(`{"timestamp":"2026-08-13T01:20:00Z"}`))
	stop.Header.Set("Authorization", "Bearer secret")
	stopResp := httptest.NewRecorder()
	handler.ServeHTTP(stopResp, stop)
	require.Equal(t, http.StatusOK, stopResp.Code)
	require.Equal(t, time.Date(2026, 8, 13, 1, 20, 0, 0, time.UTC), fake.stop)
}

func TestTripEvents_RequiresToken(t *testing.T) {
	handler := api.NewTripEvents(&fakeRunner{}, "secret", zap.NewNop()).Handler()
	req := httptest.NewRequest(http.MethodPost, "/api/trips/start", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)
	require.Equal(t, http.StatusUnauthorized, resp.Code)
}
