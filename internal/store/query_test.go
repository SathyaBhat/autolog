package store

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/trips"
)

func TestListTripsFiltersAndOrders(t *testing.T) {
	ctx := context.Background()
	s, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	for _, trip := range []trips.Trip{
		{
			Date: "2026-08-10", StartTime: time.Unix(1000, 0), EndTime: time.Unix(2000, 0),
			StartLocation: "Home", EndLocation: "Office", DistanceKm: 10, Mode: trips.ModeCar,
		},
		{
			Date: "2026-08-11", StartTime: time.Unix(3000, 0), EndTime: time.Unix(4000, 0),
			StartLocation: "Home", EndLocation: "Airport", DistanceKm: 20, Mode: trips.ModeTrain,
		},
	} {
		require.NoError(t, s.SaveTrip(ctx, trip))
	}

	got, err := s.ListTrips(ctx, "2026-08-11", "", "airport", "train", 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "Airport", got[0].EndLocation)
	require.Equal(t, trips.ModeTrain, got[0].Mode)
}

func TestGetTripSummaryMatchesDisplayedStartMinute(t *testing.T) {
	ctx := context.Background()
	s, err := New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })

	start := time.Date(2026, 8, 22, 0, 0, 45, 0, time.UTC)
	require.NoError(t, s.SaveTrip(ctx, trips.Trip{
		Date: "2026-08-22", StartTime: start, EndTime: start.Add(time.Hour),
		StartLocation: "Home", EndLocation: "Home", DistanceKm: 76.9, Mode: trips.ModeCar,
	}))

	got, err := s.GetTripSummary(ctx, "2026-08-22", time.Date(2026, 8, 22, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)
	require.Equal(t, start, got.StartTime)
}
