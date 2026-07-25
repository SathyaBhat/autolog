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
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(30, 10.0, 10.0),     // outlier: ~15,000 km in 30 s
		makeAccPoint(60, -33.8610, 151.2150),
	}
	got := trips.FilterAnomalousPoints(pts, 500)
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
	// duplicate timestamp — second point removed
	pts := []owntracks.Point{
		makeAccPoint(0, -33.8688, 151.2093),
		makeAccPoint(0, -33.8700, 151.2100), // same tst
		makeAccPoint(60, -33.8610, 151.2150),
	}
	got := trips.FilterAnomalousPoints(pts, 500)
	assert.Len(t, got, 2)
}
