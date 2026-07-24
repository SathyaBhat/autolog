package store_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/owntracks"
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

func TestSaveTrip_StoresPoints(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 20, 10, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date:        "2026-07-20",
		StartTime:   start,
		EndTime:     start.Add(time.Hour),
		StartLat:    51.5, StartLon: -0.1,
		EndLat:      51.8, EndLon: -0.3,
		DistanceKm:  30, MaxSpeedKmh: 80, Mode: trips.ModeCar,
		Points: []owntracks.Point{
			{Tst: start.Unix(), Lat: 51.5, Lon: -0.1, Vel: 0, Acc: 5},
			{Tst: start.Unix() + 1800, Lat: 51.65, Lon: -0.2, Vel: 80, Acc: 8},
			{Tst: start.Unix() + 3600, Lat: 51.8, Lon: -0.3, Vel: 0, Acc: 5},
		},
	}

	require.NoError(t, s.SaveTrip(ctx, trip))

	pts, err := s.GetTripPoints(ctx, "2026-07-20", start)
	require.NoError(t, err)
	require.Len(t, pts, 3)
	assert.Equal(t, 51.5, pts[0].Lat)
	assert.Equal(t, 51.65, pts[1].Lat)
	assert.Equal(t, 51.8, pts[2].Lat)
}

func TestSaveTrip_Idempotent_NoDoublePoints(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 20, 11, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date:        "2026-07-20",
		StartTime:   start,
		EndTime:     start.Add(time.Hour),
		StartLat:    51.5, StartLon: -0.1,
		EndLat:      51.8, EndLon: -0.3,
		DistanceKm:  30, MaxSpeedKmh: 80, Mode: trips.ModeCar,
		Points: []owntracks.Point{
			{Tst: start.Unix(), Lat: 51.5, Lon: -0.1, Vel: 0, Acc: 5},
		},
	}

	require.NoError(t, s.SaveTrip(ctx, trip))
	require.NoError(t, s.SaveTrip(ctx, trip))

	pts, err := s.GetTripPoints(ctx, "2026-07-20", start)
	require.NoError(t, err)
	assert.Len(t, pts, 1)
}

func TestGeocodeCache_MissAndHit(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Miss
	label, ok, err := s.GetGeocode(ctx, 51.5074, -0.1278)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, label)

	// Save
	require.NoError(t, s.SaveGeocode(ctx, 51.5074, -0.1278, "Westminster, London"))

	// Hit
	label, ok, err = s.GetGeocode(ctx, 51.5074, -0.1278)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Westminster, London", label)
}

func TestGeocodeCache_RoundingCollision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Two coords that round to the same 4dp key should share the cache entry
	// 51.50741 and 51.50744 both round to 51.5074; -0.12781 and -0.12784 both round to -0.1278
	require.NoError(t, s.SaveGeocode(ctx, 51.50741, -0.12781, "Near Westminster"))

	label, ok, err := s.GetGeocode(ctx, 51.50744, -0.12784)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Near Westminster", label)
}

func TestSaveTrip_LocationColumns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date:          "2026-07-24",
		StartTime:     start,
		EndTime:       start.Add(time.Hour),
		StartLat:      51.5, StartLon: -0.1,
		EndLat:        51.8, EndLon: -0.3,
		DistanceKm:    45, MaxSpeedKmh: 90, Mode: trips.ModeCar,
		StartLocation: "Oxford Street, Westminster",
		EndLocation:   "High Street, Camden",
	}
	require.NoError(t, s.SaveTrip(ctx, trip))

	var sl, el string
	err := s.DB().QueryRowContext(ctx,
		`SELECT start_location, end_location FROM trips WHERE date = ? AND start_time = ?`,
		"2026-07-24", start.Unix(),
	).Scan(&sl, &el)
	require.NoError(t, err)
	assert.Equal(t, "Oxford Street, Westminster", sl)
	assert.Equal(t, "High Street, Camden", el)
}

func TestSaveTrip_LocationColumns_EmptyOK(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date: "2026-07-24", StartTime: start, EndTime: start.Add(time.Hour),
		StartLat: 51.5, StartLon: -0.1, EndLat: 51.8, EndLon: -0.3,
		DistanceKm: 30, MaxSpeedKmh: 80, Mode: trips.ModeCar,
	}
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
