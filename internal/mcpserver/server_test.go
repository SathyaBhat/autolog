package mcpserver

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

func TestListTripsIncludesIntermediateStopNames(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	start := time.Date(2026, 8, 22, 10, 50, 45, 0, time.FixedZone("Australia/Sydney", 10*60*60))
	require.NoError(t, st.SaveTrip(ctx, trips.Trip{
		Date: "2026-08-22", StartTime: start, EndTime: start.Add(5 * time.Hour),
		StartLocation: "Home", EndLocation: "Home", DistanceKm: 76.9, Mode: trips.ModeCar,
		StopPoints: []trips.StopPoint{
			{Location: "Production Avenue, Sydney", ArrivalTst: start.Unix() + 60, DepartureTst: start.Unix() + 120},
			{Location: "Mulgoa Road, Sydney", ArrivalTst: start.Unix() + 180, DepartureTst: start.Unix() + 240},
		},
	}))

	loc, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	service := &Server{store: st, location: loc}
	_, out, err := service.listTrips(ctx, nil, listTripsInput{FromDate: "2026-08-22", ToDate: "2026-08-22"})
	require.NoError(t, err)
	require.Len(t, out.Trips, 1)
	require.Equal(t, []string{"Production Avenue, Sydney", "Mulgoa Road, Sydney"}, out.Trips[0].IntermediateStops)
}

func TestTripDetailsAcceptsDisplayedStartMinuteWithSeconds(t *testing.T) {
	ctx := context.Background()
	st, err := store.New(":memory:")
	require.NoError(t, err)
	t.Cleanup(func() { _ = st.Close() })

	loc, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	start := time.Date(2026, 8, 22, 10, 50, 45, 0, loc)
	require.NoError(t, st.SaveTrip(ctx, trips.Trip{
		Date: "2026-08-22", StartTime: start, EndTime: start.Add(5 * time.Hour),
		StartLocation: "Home", EndLocation: "Home", DistanceKm: 76.9, Mode: trips.ModeCar,
		StopPoints: []trips.StopPoint{
			{Location: "Production Avenue, Sydney", ArrivalTst: start.Unix() + 60, DepartureTst: start.Unix() + 120},
		},
	}))

	service := &Server{store: st, location: loc}
	_, out, err := service.tripDetails(ctx, nil, tripDetailsInput{
		Date:      "2026-08-22",
		StartTime: "10:50",
	})
	require.NoError(t, err)
	require.Len(t, out.Stops, 1)
	require.Equal(t, "Production Avenue, Sydney", out.Stops[0].Location)
}
