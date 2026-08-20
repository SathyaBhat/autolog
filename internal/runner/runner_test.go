package runner_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/zap"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/runner"
	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

type stubOwnTracks struct {
	points []owntracks.Point
}

const (
	testHomeLat = 51.5000
	testHomeLon = -0.1000
)

func (s *stubOwnTracks) Fetch(_ context.Context, _, _ time.Time) ([]owntracks.Point, error) {
	return s.points, nil
}

type stubNotifier struct {
	sent []trips.Trip
}

func (s *stubNotifier) SendAll(_ context.Context, ts []trips.Trip) error {
	s.sent = append(s.sent, ts...)
	return nil
}

func TestRunner_ProcessOnce_StoresTrip(t *testing.T) {
	now := time.Now().Unix()
	pts := []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Vel: 50, Tag: "drive"},
		{Tst: now + 60, Lat: 51.52, Lon: -0.13, Vel: 55, Tag: "drive"},
		{Tst: now + 120, Lat: 51.54, Lon: -0.15, Vel: 48, Tag: "drive"},
	}

	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	tg := &stubNotifier{}
	ot := &stubOwnTracks{points: pts}

	cfg := &config.Config{
		Filters: config.FiltersConfig{MaxTrainSpeedKmh: 150},
	}

	r := runner.NewWithDeps(cfg, ot, st, tg, nil, zap.NewNop())
	err = r.ProcessOnce(context.Background(), time.Unix(now-3600, 0), time.Unix(now+3600, 0))
	require.NoError(t, err)

	exists, err := st.TripExists(context.Background(), time.Unix(now, 0).UTC().Format("2006-01-02"), time.Unix(now, 0).UTC())
	require.NoError(t, err)
	assert.True(t, exists)

	stored, err := st.GetTripPoints(context.Background(), time.Unix(now, 0).UTC().Format("2006-01-02"), time.Unix(now, 0).UTC())
	require.NoError(t, err)
	require.Len(t, stored, 3)
	assert.Equal(t, "drive", stored[0].Tag)
}

func TestRunner_ProcessOnce_NoNotificationUnder100km(t *testing.T) {
	now := time.Now().Unix()
	pts := []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Vel: 50, Tag: "drive"},
		{Tst: now + 60, Lat: 51.51, Lon: -0.11, Vel: 50, Tag: "drive"},
	}

	st, _ := store.New(":memory:")
	defer st.Close()
	tg := &stubNotifier{}

	cfg := &config.Config{Filters: config.FiltersConfig{MaxTrainSpeedKmh: 150}}
	r := runner.NewWithDeps(cfg, &stubOwnTracks{points: pts}, st, tg, nil, zap.NewNop())
	_ = r.ProcessOnce(context.Background(), time.Unix(now-3600, 0), time.Unix(now+3600, 0))

	assert.Empty(t, tg.sent)
}

func TestRunner_ExplicitTrip_StoresTripBelowNormalMinimumAndNotifies(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	pts := []owntracks.Point{
		{Tst: now.Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
		{Tst: now.Add(60 * time.Second).Unix(), Lat: 51.5000, Lon: -0.0750, Acc: 10},
		{Tst: now.Add(120 * time.Second).Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
	}

	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()
	tg := &stubNotifier{}
	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			MaxTrainSpeedKmh: 150,
			HomeZones:        []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}},
		}},
		&stubOwnTracks{points: pts},
		st,
		tg,
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	trip, completed, err := r.StopExplicitTrip(context.Background(), now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, now, trip.StartTime)
	assert.Equal(t, trips.ModeCar, trip.Mode)
	assert.Less(t, trip.DistanceKm, 5.0)
	require.Len(t, tg.sent, 1)
	assert.Equal(t, trip.StartTime, tg.sent[0].StartTime)

	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.True(t, active.IsZero())
}

func TestRunner_ExplicitTrip_StopClearsActiveTripWhenPointsAreMissing(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			HomeZones: []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}},
		}},
		&stubOwnTracks{points: []owntracks.Point{
			{Tst: now.Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
		}},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)
	require.NoError(t, r.StartExplicitTrip(context.Background(), now))

	_, _, err = r.StopExplicitTrip(context.Background(), now.Add(time.Minute))
	require.Error(t, err)

	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.False(t, active.IsZero())
}

func TestRunner_ExplicitTrip_IntermediateStopsContinueUntilHome(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	home := config.ExclusionZone{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}
	points := []owntracks.Point{
		{Tst: now.Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
		{Tst: now.Add(time.Minute).Unix(), Lat: 51.4800, Lon: -0.1200, Acc: 10},
		{Tst: now.Add(100 * time.Second).Unix(), Lat: 51.4900, Lon: -0.1150, Acc: 10},
		{Tst: now.Add(2 * time.Minute).Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
	}
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			MaxTrainSpeedKmh: 150,
			HomeZones:        []config.ExclusionZone{home},
		}},
		&stubOwnTracks{points: points},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	_, completed, err := r.StopExplicitTrip(context.Background(), now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, completed)
	require.NoError(t, r.StartExplicitTrip(context.Background(), now.Add(90*time.Second)))

	trip, completed, err := r.StopExplicitTrip(context.Background(), now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, completed)
	assert.Equal(t, now, trip.StartTime)
	require.Len(t, trip.StopPoints, 1)
	assert.Equal(t, now.Add(time.Minute).Unix(), trip.StopPoints[0].ArrivalTst)
	assert.Equal(t, now.Add(90*time.Second).Unix(), trip.StopPoints[0].DepartureTst)

	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.True(t, active.IsZero())
}

func TestRunner_ExplicitTrip_SumsLegDistancesWithoutStopBridge(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	home := config.ExclusionZone{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}
	away := owntracks.Point{Tst: now.Add(time.Minute).Unix(), Lat: 51.4800, Lon: -0.1200, Acc: 10}
	returnStart := owntracks.Point{Tst: now.Add(100 * time.Second).Unix(), Lat: 51.4900, Lon: -0.1150, Acc: 10}
	homeEnd := owntracks.Point{Tst: now.Add(2 * time.Minute).Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10}
	points := []owntracks.Point{
		{Tst: now.Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
		away,
		returnStart,
		homeEnd,
	}

	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			MaxTrainSpeedKmh: 150,
			HomeZones:        []config.ExclusionZone{home},
		}},
		&stubOwnTracks{points: points},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	_, completed, err := r.StopExplicitTrip(context.Background(), now.Add(time.Minute))
	require.NoError(t, err)
	assert.False(t, completed)
	require.NoError(t, r.StartExplicitTrip(context.Background(), now.Add(90*time.Second)))

	trip, completed, err := r.StopExplicitTrip(context.Background(), now.Add(2*time.Minute))
	require.NoError(t, err)
	assert.True(t, completed)

	expected := trips.HaversineKm(testHomeLat, testHomeLon, away.Lat, away.Lon) +
		trips.HaversineKm(returnStart.Lat, returnStart.Lon, homeEnd.Lat, homeEnd.Lon)
	assert.InDelta(t, expected, trip.DistanceKm, 0.001)
	assert.Less(t, trip.DistanceKm,
		expected+trips.HaversineKm(away.Lat, away.Lon, returnStart.Lat, returnStart.Lon))
}

func TestRunner_ExplicitTrip_PersistsStartWithoutImmediateGpsFix(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			HomeZones: []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 100}},
		}},
		&stubOwnTracks{},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.Equal(t, now, active)
}

func TestRunner_ExplicitTrip_AcceptsDelayedHomeFixAfterStart(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			HomeZones: []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}},
		}},
		&stubOwnTracks{points: []owntracks.Point{
			{Tst: now.Add(10 * time.Minute).Unix(), Lat: testHomeLat, Lon: testHomeLon, Acc: 10},
		}},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.Equal(t, now, active)
}

func TestRunner_ExplicitTrip_AcceptsStartBeforeDelayedDepartureFix(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			HomeZones: []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}},
		}},
		&stubOwnTracks{points: []owntracks.Point{
			// The first fix can arrive after the explicit start event.
			{Tst: now.Add(5 * time.Minute).Unix(), Lat: 51.5063, Lon: testHomeLon, Acc: 10},
		}},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.Equal(t, now, active)
}

func TestRunner_ExplicitTrip_AcceptsStartRegardlessOfImmediateFixLocation(t *testing.T) {
	now := time.Now().UTC().Truncate(time.Second)
	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	r := runner.NewWithDeps(
		&config.Config{Filters: config.FiltersConfig{
			HomeZones: []config.ExclusionZone{{Name: "Home", Lat: testHomeLat, Lon: testHomeLon, RadiusM: 500}},
		}},
		&stubOwnTracks{points: []owntracks.Point{
			{Tst: now.Add(5 * time.Minute).Unix(), Lat: 51.5200, Lon: testHomeLon, Acc: 10},
		}},
		st,
		&stubNotifier{},
		nil,
		zap.NewNop(),
	)

	require.NoError(t, r.StartExplicitTrip(context.Background(), now))
	active, err := st.GetActiveManualTripStart(context.Background())
	require.NoError(t, err)
	assert.Equal(t, now, active)
}

func TestRunner_ProcessOnce_TaggedStopReachesNotificationAndStore(t *testing.T) {
	now := time.Now().Unix()
	pts := []owntracks.Point{
		{Tst: now, Lat: 51.5000, Lon: -0.1000, Vel: 40, Tag: "drive"},
		{Tst: now + 300, Lat: 51.5300, Lon: -0.1200, Vel: 3, Tag: "drive"},
		{Tst: now + 900, Lat: 51.5301, Lon: -0.1201, Vel: 0},
		{Tst: now + 1200, Lat: 51.5320, Lon: -0.1100, Vel: 40, Tag: "drive"},
		{Tst: now + 1500, Lat: 51.5005, Lon: -0.0995, Vel: 50, Tag: "drive"},
	}

	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()

	tg := &stubNotifier{}
	cfg := &config.Config{Filters: config.FiltersConfig{MaxTrainSpeedKmh: 150}}
	r := runner.NewWithDeps(cfg, &stubOwnTracks{points: pts}, st, tg, nil, zap.NewNop())

	require.NoError(t, r.ProcessOnce(context.Background(), time.Unix(now-3600, 0), time.Unix(now+3600, 0)))

	require.Len(t, tg.sent, 1)
	require.Len(t, tg.sent[0].StopPoints, 1)
	assert.Equal(t, trips.StopConfidenceHigh, tg.sent[0].StopPoints[0].Confidence)
	assert.Equal(t, "drive tag cleared", tg.sent[0].StopPoints[0].Evidence)

	stops, err := st.GetTripStops(context.Background(), time.Unix(now, 0).UTC().Format("2006-01-02"), time.Unix(now, 0).UTC())
	require.NoError(t, err)
	require.Len(t, stops, 1)
	assert.Equal(t, "drive tag cleared", stops[0].Evidence)
}

func TestRunner_ReviewTrip_InspectAndReprocess(t *testing.T) {
	loc, err := time.LoadLocation("Australia/Sydney")
	require.NoError(t, err)
	target := time.Date(2026, 8, 10, 17, 58, 0, 0, loc)
	start := target.Unix()
	points := []owntracks.Point{
		{Tst: start, Lat: 51.5000, Lon: -0.1000, Vel: 40, Tag: "drive"},
		{Tst: start + 300, Lat: 51.5300, Lon: -0.1200, Vel: 3, Tag: "drive"},
		{Tst: start + 900, Lat: 51.5301, Lon: -0.1201, Vel: 0},
		{Tst: start + 1200, Lat: 51.5320, Lon: -0.1100, Vel: 40, Tag: "drive"},
		{Tst: start + 1500, Lat: 51.5005, Lon: -0.0995, Vel: 50, Tag: "drive"},
	}

	st, err := store.New(":memory:")
	require.NoError(t, err)
	defer st.Close()
	cfg := &config.Config{Filters: config.FiltersConfig{MaxTrainSpeedKmh: 150}}
	r := runner.NewWithDeps(cfg, &stubOwnTracks{points: points}, st, &stubNotifier{}, nil, zap.NewNop())

	inspected, err := r.ReviewTrip(context.Background(), target, false)
	require.NoError(t, err)
	require.Len(t, inspected.StopPoints, 1)
	assert.Empty(t, func() []trips.StopPoint {
		stops, _ := st.GetTripStops(context.Background(), inspected.Date, inspected.StartTime)
		return stops
	}())

	old := inspected
	old.Points = nil
	old.StopPoints = nil
	require.NoError(t, st.SaveTrip(context.Background(), old))

	reprocessed, err := r.ReviewTrip(context.Background(), target, true)
	require.NoError(t, err)
	require.Len(t, reprocessed.StopPoints, 1)

	storedPoints, err := st.GetTripPoints(context.Background(), reprocessed.Date, reprocessed.StartTime)
	require.NoError(t, err)
	require.Len(t, storedPoints, len(points))
	assert.Equal(t, "drive", storedPoints[0].Tag)
	storedStops, err := st.GetTripStops(context.Background(), reprocessed.Date, reprocessed.StartTime)
	require.NoError(t, err)
	require.Len(t, storedStops, 1)
}
