package trips_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

func makeAccPoint(tst int64, lat, lon float64) owntracks.Point {
	return owntracks.Point{Tst: tst, Lat: lat, Lon: lon, Acc: 10}
}

func TestFilterAnomalousPoints_NoOutlier(t *testing.T) {
	// ~50 km/h normal driving points — none removed
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(60, -33.8610, 151.2150),
		makeAccPoint(120, -33.8530, 151.2200),
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	assert.Len(t, got, 3)
}

func TestFilterAnomalousPoints_MiddleOutlier(t *testing.T) {
	// middle point teleports thousands of km then back — should be removed
	// Use 4 points: normal-slow-outlier-slow-normal, so middle outlier (p2)
	// has both segments fast but first and last can be normal
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),      // p0: normal, slow to p1
		makeAccPoint(60, -33.8610, 151.2150),     // p1: normal, fast to p2 (outlier at 10,10)
		makeAccPoint(90, 10.0, 10.0),             // p2: OUTLIER, both segments fast
		makeAccPoint(120, -33.8605, 151.2145),    // p3: normal, slow from p2
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	// p0: first, p0->p1 slow, not filtered
	// p1: middle, p0->p1 slow, p1->p2 fast (~15000 km in 30s), not filtered (needs both fast)
	// p2: middle, p1->p2 fast, p2->p3 fast (~15000 km in 30s), FILTERED (both fast)
	// p3: last, p2->p3 fast, FILTERED
	// Expected result: p0 and p1 only
	assert.Len(t, got, 2)
	assert.Equal(t, int64(0), got[0].Tst)
	assert.Equal(t, int64(60), got[1].Tst)
}

func TestFilterAnomalousPoints_Disabled(t *testing.T) {
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(30, 10.0, 10.0),
		makeAccPoint(60, -33.8610, 151.2150),
	}
	got := trips.FilterAnomalousPoints(pts, 0) // disabled
	assert.Len(t, got, 3)
}

func TestFilterAnomalousPoints_ZeroDelta(t *testing.T) {
	// duplicate timestamp — points with zero/negative time delta removed
	// p0 has first timestamp, p1 has same timestamp (zero delta), p2 has later timestamp
	// With new logic: p0 (first) has dt=0 to p1, so filtered; p1 (middle) has dt=0 from p0, so filtered
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(0, -33.8700, 151.2100), // same tst as p0
		makeAccPoint(60, -33.8610, 151.2150),
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	// p0: first with dt=0 to next, filtered
	// p1: middle with dt=0 from prev, filtered
	// p2: last, not filtered
	assert.Len(t, got, 1)
	assert.Equal(t, int64(60), got[0].Tst)
}

func TestFilterAnomalousPoints_FirstPointOutlier(t *testing.T) {
	// First point is a GPS teleport — should be removed
	pts := []owntracks.Point{
		makeAccPoint(0, 10.0, 10.0),           // outlier: ~15,000 km from next point in 30s
		makeAccPoint(30, -33.8688, 151.2093),
		makeAccPoint(90, -33.8610, 151.2150),
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(30), got[0].Tst)
}

func TestFilterAnomalousPoints_LastPointOutlier(t *testing.T) {
	// Last point is a GPS teleport — should be removed
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(60, -33.8610, 151.2150),
		makeAccPoint(90, 10.0, 10.0),           // outlier: ~15,000 km from previous in 30s
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(60), got[len(got)-1].Tst)
}
