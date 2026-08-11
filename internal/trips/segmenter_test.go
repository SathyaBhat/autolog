package trips_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

func makePoint(tst int64, lat, lon, vel float64) owntracks.Point {
	return owntracks.Point{Tst: tst, Lat: lat, Lon: lon, Vel: vel, Acc: 10, Tag: "drive"}
}

const testGap = 10 * time.Minute

func TestSegment_SingleTrip(t *testing.T) {
	now := time.Now().Unix()
	points := []owntracks.Point{
		makePoint(now, 51.5, -0.1, 30),
		makePoint(now+60, 51.51, -0.11, 35),
		makePoint(now+120, 51.52, -0.12, 28),
	}
	segs := trips.Segment(points, testGap)
	require.Len(t, segs, 1)
	assert.Len(t, segs[0].Points, 3)
}

func TestSegment_TwoTrips_GapOverThreshold(t *testing.T) {
	now := time.Now().Unix()
	points := []owntracks.Point{
		makePoint(now, 51.5, -0.1, 30),
		makePoint(now+60, 51.51, -0.11, 35),
		// gap of 20 min — exceeds 10 min threshold
		makePoint(now+1260, 51.6, -0.2, 20),
		makePoint(now+1320, 51.61, -0.21, 25),
	}
	segs := trips.Segment(points, testGap)
	assert.Len(t, segs, 2)
}

func TestSegment_StitchedByLargerGap(t *testing.T) {
	now := time.Now().Unix()
	points := []owntracks.Point{
		makePoint(now, 51.5, -0.1, 30),
		makePoint(now+60, 51.51, -0.11, 35),
		// gap of 20 min — within 90 min default, so still one trip
		makePoint(now+1260, 51.6, -0.2, 20),
		makePoint(now+1320, 51.61, -0.21, 25),
	}
	segs := trips.Segment(points, 90*time.Minute)
	assert.Len(t, segs, 1)
}

func TestSegment_Empty(t *testing.T) {
	segs := trips.Segment(nil, testGap)
	assert.Empty(t, segs)
}

func TestSegment_SinglePoint(t *testing.T) {
	segs := trips.Segment([]owntracks.Point{makePoint(1000, 51.5, -0.1, 0)}, testGap)
	assert.Empty(t, segs)
}

func TestSegmentWithStays_SplitsOnStay(t *testing.T) {
	// Build a synthetic route: drive → park 6 min → drive again.
	base := int64(1_700_000_000)
	// Leg A: 5 moving points spaced 60 s apart, each ~800 m apart (~48 km/h)
	legA := []owntracks.Point{
		{Tst: base, Lat: -33.8600, Lon: 151.2000, Acc: 10},
		{Tst: base + 60, Lat: -33.8528, Lon: 151.2000, Acc: 10},
		{Tst: base + 120, Lat: -33.8456, Lon: 151.2000, Acc: 10},
		{Tst: base + 180, Lat: -33.8384, Lon: 151.2000, Acc: 10},
		{Tst: base + 240, Lat: -33.8312, Lon: 151.2000, Acc: 10},
	}
	// Stay: 7 points clustered within 20 m over 6 min
	stayBase := base + 300
	stay := []owntracks.Point{
		{Tst: stayBase, Lat: -33.8312, Lon: 151.2001, Acc: 10},
		{Tst: stayBase + 60, Lat: -33.8312, Lon: 151.2002, Acc: 10},
		{Tst: stayBase + 120, Lat: -33.8313, Lon: 151.2001, Acc: 10},
		{Tst: stayBase + 180, Lat: -33.8312, Lon: 151.2000, Acc: 10},
		{Tst: stayBase + 240, Lat: -33.8311, Lon: 151.2001, Acc: 10},
		{Tst: stayBase + 300, Lat: -33.8312, Lon: 151.2002, Acc: 10},
		{Tst: stayBase + 360, Lat: -33.8313, Lon: 151.2000, Acc: 10},
	}
	// Leg B: 5 moving points
	legBBase := stayBase + 420
	legB := []owntracks.Point{
		{Tst: legBBase, Lat: -33.8312, Lon: 151.2000, Acc: 10, Tag: "drive"},
		{Tst: legBBase + 60, Lat: -33.8240, Lon: 151.2000, Acc: 10, Tag: "drive"},
		{Tst: legBBase + 120, Lat: -33.8168, Lon: 151.2000, Acc: 10, Tag: "drive"},
		{Tst: legBBase + 180, Lat: -33.8096, Lon: 151.2000, Acc: 10, Tag: "drive"},
		{Tst: legBBase + 240, Lat: -33.8024, Lon: 151.2000, Acc: 10, Tag: "drive"},
	}
	all := append(append(legA, stay...), legB...)
	cfg := trips.ClassifierConfig{
		Flags:       trips.AlgorithmFlags{StaySegment: true},
		StayRadiusM: 100,
		StayMinDur:  5 * time.Minute,
		StayMaxGap:  5 * time.Minute,
	}
	segs := trips.SegmentWithStays(all, cfg)
	// Expect 2 trips: legA and legB (stay is the boundary)
	assert.GreaterOrEqual(t, len(segs), 2)
}
