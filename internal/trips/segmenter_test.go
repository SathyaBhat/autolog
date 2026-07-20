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
	return owntracks.Point{Tst: tst, Lat: lat, Lon: lon, Vel: vel, Acc: 10}
}

func TestSegment_SingleTrip(t *testing.T) {
	now := time.Now().Unix()
	points := []owntracks.Point{
		makePoint(now, 51.5, -0.1, 30),
		makePoint(now+60, 51.51, -0.11, 35),
		makePoint(now+120, 51.52, -0.12, 28),
	}
	segs := trips.Segment(points)
	require.Len(t, segs, 1)
	assert.Len(t, segs[0].Points, 3)
}

func TestSegment_TwoTrips_GapOver5Min(t *testing.T) {
	now := time.Now().Unix()
	points := []owntracks.Point{
		makePoint(now, 51.5, -0.1, 30),
		makePoint(now+60, 51.51, -0.11, 35),
		// gap of 10 min
		makePoint(now+660, 51.6, -0.2, 20),
		makePoint(now+720, 51.61, -0.21, 25),
	}
	segs := trips.Segment(points)
	assert.Len(t, segs, 2)
}

func TestSegment_Empty(t *testing.T) {
	segs := trips.Segment(nil)
	assert.Empty(t, segs)
}

func TestSegment_SinglePoint(t *testing.T) {
	segs := trips.Segment([]owntracks.Point{makePoint(1000, 51.5, -0.1, 0)})
	assert.Empty(t, segs)
}
