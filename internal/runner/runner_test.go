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

func (s *stubOwnTracks) Fetch(_ context.Context, _, _ time.Time) ([]owntracks.Point, error) {
	return s.points, nil
}

type stubNotifier struct {
	sent []trips.Trip
}

func (s *stubNotifier) Send(_ context.Context, t trips.Trip) error {
	s.sent = append(s.sent, t)
	return nil
}

func TestRunner_ProcessOnce_StoresTrip(t *testing.T) {
	now := time.Now().Unix()
	pts := []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Vel: 50},
		{Tst: now + 60, Lat: 51.52, Lon: -0.13, Vel: 55},
		{Tst: now + 120, Lat: 51.54, Lon: -0.15, Vel: 48},
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
}

func TestRunner_ProcessOnce_NoNotificationUnder100km(t *testing.T) {
	now := time.Now().Unix()
	pts := []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Vel: 50},
		{Tst: now + 60, Lat: 51.51, Lon: -0.11, Vel: 50},
	}

	st, _ := store.New(":memory:")
	defer st.Close()
	tg := &stubNotifier{}

	cfg := &config.Config{Filters: config.FiltersConfig{MaxTrainSpeedKmh: 150}}
	r := runner.NewWithDeps(cfg, &stubOwnTracks{points: pts}, st, tg, nil, zap.NewNop())
	_ = r.ProcessOnce(context.Background(), time.Unix(now-3600, 0), time.Unix(now+3600, 0))

	assert.Empty(t, tg.sent)
}
