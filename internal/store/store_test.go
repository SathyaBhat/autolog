package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

func newTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { s.Close() })
	return s
}

func TestSaveTrip_And_TripExists(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date:        "2026-07-20",
		StartTime:   start,
		EndTime:     start.Add(30 * time.Minute),
		StartLat:    51.5,
		StartLon:    -0.1,
		EndLat:      51.8,
		EndLon:      -0.3,
		DistanceKm:  45.2,
		MaxSpeedKmh: 95.0,
		Mode:        trips.ModeCar,
	}

	require.NoError(t, s.SaveTrip(ctx, trip))

	exists, err := s.TripExists(ctx, "2026-07-20", start)
	require.NoError(t, err)
	assert.True(t, exists)

	exists, err = s.TripExists(ctx, "2026-07-20", start.Add(time.Hour))
	require.NoError(t, err)
	assert.False(t, exists)
}

func TestSaveTrip_Idempotent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 20, 9, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date: "2026-07-20", StartTime: start, EndTime: start.Add(time.Hour),
		StartLat: 51.5, StartLon: -0.1, EndLat: 51.8, EndLon: -0.3,
		DistanceKm: 50, MaxSpeedKmh: 80, Mode: trips.ModeCar,
	}
	require.NoError(t, s.SaveTrip(ctx, trip))
	require.NoError(t, s.SaveTrip(ctx, trip))
}

func TestLastProcessedTime_RoundTrip(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	ts, err := s.GetLastProcessedTime(ctx)
	require.NoError(t, err)
	assert.True(t, ts.IsZero())

	now := time.Now().UTC().Truncate(time.Second)
	require.NoError(t, s.SetLastProcessedTime(ctx, now))

	got, err := s.GetLastProcessedTime(ctx)
	require.NoError(t, err)
	assert.Equal(t, now, got)
}
