package trips_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

func TestClassify_Car(t *testing.T) {
	now := time.Now().Unix()
	// Points ~830m apart per 60s step ≈ 50 km/h computed speed, well under train threshold.
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Acc: 10},
		{Tst: now + 60, Lat: 51.5075, Lon: -0.1, Acc: 10},
		{Tst: now + 120, Lat: 51.515, Lon: -0.1, Acc: 10},
		{Tst: now + 180, Lat: 51.5225, Lon: -0.1, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{MaxTrainSpeedKmh: 150}
	trip, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	assert.Equal(t, trips.ModeCar, trip.Mode)
	assert.Greater(t, trip.DistanceKm, 0.0)
	assert.Greater(t, trip.MaxSpeedKmh, 0.0)
}

func TestClassify_Train_MaxSpeed(t *testing.T) {
	now := time.Now().Unix()
	// ~47 km apart in 60s ≈ 2800 km/h computed — well above any train threshold.
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Acc: 10},
		{Tst: now + 60, Lat: 51.8, Lon: -0.5, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{MaxTrainSpeedKmh: 150}
	trip, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	assert.Equal(t, trips.ModeTrain, trip.Mode)
}

func TestClassify_ExclusionZone_Start(t *testing.T) {
	now := time.Now().Unix()
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: 51.5, Lon: -0.1, Vel: 0},
		{Tst: now + 60, Lat: 51.6, Lon: -0.2, Vel: 40},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		ExclusionZones: []config.ExclusionZone{
			{Name: "station", Lat: 51.5, Lon: -0.1, RadiusM: 500},
		},
	}
	_, keep := trips.Classify(raw, cfg)
	assert.False(t, keep)
}

func TestClassify_ExclusionZone_End(t *testing.T) {
	now := time.Now().Unix()
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: 51.3, Lon: 0.1, Vel: 40},
		{Tst: now + 60, Lat: 51.5, Lon: -0.1, Vel: 0},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		ExclusionZones: []config.ExclusionZone{
			{Name: "station", Lat: 51.5, Lon: -0.1, RadiusM: 500},
		},
	}
	_, keep := trips.Classify(raw, cfg)
	assert.False(t, keep)
}
