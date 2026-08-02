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
	trip, _, keep := trips.Classify(raw, cfg)
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
	trip, _, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	assert.Equal(t, trips.ModeTrain, trip.Mode)
}

func TestClassify_Transit_Discarded(t *testing.T) {
	now := time.Now().Unix()
	// Simulate a metro/bus trip: one segment with a 6-minute gap covering 6 km.
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: -33.730588, Lon: 150.944042, Acc: 10},
		{Tst: now + 360, Lat: -33.777193, Lon: 151.117753, Acc: 72}, // 360s gap, ~17 km
		{Tst: now + 720, Lat: -33.865145, Lon: 151.202092, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		TransitGap:       5 * time.Minute,
		TransitMinDistKm: 5.0,
	}
	_, reason, keep := trips.Classify(raw, cfg)
	assert.False(t, keep)
	assert.Equal(t, "transit", reason)
}

func TestClassify_Transit_CarNotAffected(t *testing.T) {
	now := time.Now().Unix()
	// Normal car trip: frequent points, no single gap exceeding threshold.
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: -33.730, Lon: 150.918, Acc: 10},
		{Tst: now + 60, Lat: -33.735, Lon: 150.925, Acc: 10},
		{Tst: now + 120, Lat: -33.740, Lon: 150.932, Acc: 10},
		{Tst: now + 180, Lat: -33.745, Lon: 150.939, Acc: 10},
		{Tst: now + 240, Lat: -33.750, Lon: 150.946, Acc: 10},
		{Tst: now + 300, Lat: -33.755, Lon: 150.953, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		TransitGap:       5 * time.Minute,
		TransitMinDistKm: 5.0,
	}
	trip, _, keep := trips.Classify(raw, cfg)
	assert.True(t, keep)
	assert.Equal(t, trips.ModeCar, trip.Mode)
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
	_, _, keep := trips.Classify(raw, cfg)
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
	_, _, keep := trips.Classify(raw, cfg)
	assert.False(t, keep)
}

func TestClassify_SegmentVote_CarDominates(t *testing.T) {
	// Mixed trip: short fast section (train speed) + long slow car section.
	// SegmentVote should pick car because it has more total time.
	now := time.Now().Unix()
	raw := trips.RawTrip{Points: []owntracks.Point{
		// 60-s car-speed leg: ~830 m / 60 s ≈ 50 km/h
		{Tst: now, Lat: -33.8600, Lon: 151.2000, Acc: 10},
		{Tst: now + 60, Lat: -33.8528, Lon: 151.2000, Acc: 10},
		{Tst: now + 120, Lat: -33.8456, Lon: 151.2000, Acc: 10},
		{Tst: now + 180, Lat: -33.8384, Lon: 151.2000, Acc: 10},
		{Tst: now + 240, Lat: -33.8312, Lon: 151.2000, Acc: 10},
		{Tst: now + 300, Lat: -33.8240, Lon: 151.2000, Acc: 10},
		// 30-s high-speed blip: ~5 km in 30 s ≈ 600 km/h (but only 30 s)
		{Tst: now + 330, Lat: -33.7800, Lon: 151.2000, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		Flags:            trips.AlgorithmFlags{SegmentVote: true},
	}
	trip, _, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	assert.Equal(t, trips.ModeCar, trip.Mode)
}

func TestClassify_AccelTrainGate_DowngradesJerkyHighSpeed(t *testing.T) {
	// High max speed but with large speed variance (jerky motorway)
	// AccelTrainGate should downgrade from train to car.
	now := time.Now().Unix()
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: -33.8600, Lon: 151.2000, Acc: 10},
		{Tst: now + 30, Lat: -33.8350, Lon: 151.2000, Acc: 10}, // ~100 km/h
		{Tst: now + 35, Lat: -33.8310, Lon: 151.2000, Acc: 10}, // sudden burst: ~290 km/h
		{Tst: now + 90, Lat: -33.8050, Lon: 151.2000, Acc: 10}, // back to normal
		{Tst: now + 150, Lat: -33.7800, Lon: 151.2000, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		Flags:            trips.AlgorithmFlags{AccelTrainGate: true},
	}
	trip, _, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	// Without the gate this might be classified train due to high max speed;
	// with the gate it should be downgraded to car.
	assert.Equal(t, trips.ModeCar, trip.Mode)
}

func TestClassify_StopPoints_DwellClusterDetected(t *testing.T) {
	// Reproduces a real trip: drive away, dwell 45 min at one spot pinging
	// every 3 min (OwnTracks Move-mode timer cadence), drive to a second
	// spot, dwell 9 min, drive back. Neither dwell has any single reporting
	// gap >= stop_gap, so the old gap-only check would miss both. A bare
	// 89-min reporting gap with no spatial cluster (monitoring mode switch)
	// must NOT be reported as a stop.
	now := time.Now().Unix()
	var pts []owntracks.Point
	add := func(offsetSec int64, lat, lon float64) {
		pts = append(pts, owntracks.Point{Tst: now + offsetSec, Lat: lat, Lon: lon, Acc: 10})
	}

	// Drive away from start.
	add(0, -33.7337, 150.9184)
	add(60, -33.7280, 150.9250)
	add(120, -33.7245, 150.9285)

	// Dwell #1: ~45 min stationary, pinging every 3 min.
	dwellStart := int64(180)
	for i := int64(0); i <= 15; i++ {
		add(dwellStart+i*180, -33.7239, 150.9303)
	}
	dwellEnd := dwellStart + 15*180 // 2880

	// Drive to a second spot.
	add(dwellEnd+120, -33.7200, 150.9260)
	add(dwellEnd+240, -33.7183, 150.9209)

	// Dwell #2: ~9 min stationary, pinging every 3 min.
	dwell2Start := dwellEnd + 240 + 60
	for i := int64(0); i <= 3; i++ {
		add(dwell2Start+i*180, -33.7183, 150.9209)
	}
	dwell2End := dwell2Start + 3*180

	// Drive back toward start.
	add(dwell2End+120, -33.7250, 150.9200)
	add(dwell2End+240, -33.7300, 150.9180)
	lastMoving := dwell2End + 300
	add(lastMoving, -33.7311, 150.9177)

	// Monitoring mode switched to "Significant" on arrival: 89-min silence,
	// no movement, single point after the gap. Must not appear as a stop.
	add(lastMoving+89*60, -33.7311, 150.9177)

	raw := trips.RawTrip{Points: pts}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		MinDistanceKm:    1,
		StopGap:          10 * time.Minute,
	}
	trip, _, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	require.Len(t, trip.StopPoints, 2, "expected both dwell stops to be detected")

	assert.InDelta(t, -33.7239, trip.StopPoints[0].Lat, 0.01)
	assert.InDelta(t, 150.9303, trip.StopPoints[0].Lon, 0.01)
	assert.Equal(t, dwellEnd-dwellStart, trip.StopPoints[0].DepartureTst-trip.StopPoints[0].ArrivalTst)

	assert.InDelta(t, -33.7183, trip.StopPoints[1].Lat, 0.01)
	assert.InDelta(t, 150.9209, trip.StopPoints[1].Lon, 0.01)
}

func TestClassify_AccelTrainGate_KeepsSmoothTrain(t *testing.T) {
	// Smooth high-speed profile: consistent ~200 km/h, low acceleration variance.
	// AccelTrainGate should keep as train.
	now := time.Now().Unix()
	// Each step: ~1.67 km in 30 s = ~200 km/h, consistent spacing
	raw := trips.RawTrip{Points: []owntracks.Point{
		{Tst: now, Lat: -33.8600, Lon: 151.2000, Acc: 10},
		{Tst: now + 30, Lat: -33.8450, Lon: 151.2000, Acc: 10},
		{Tst: now + 60, Lat: -33.8300, Lon: 151.2000, Acc: 10},
		{Tst: now + 90, Lat: -33.8150, Lon: 151.2000, Acc: 10},
		{Tst: now + 120, Lat: -33.8000, Lon: 151.2000, Acc: 10},
		{Tst: now + 150, Lat: -33.7850, Lon: 151.2000, Acc: 10},
	}}
	cfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		Flags:            trips.AlgorithmFlags{AccelTrainGate: true},
	}
	trip, _, keep := trips.Classify(raw, cfg)
	require.True(t, keep)
	assert.Equal(t, trips.ModeTrain, trip.Mode)
}
