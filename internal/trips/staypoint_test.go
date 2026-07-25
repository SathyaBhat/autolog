package trips_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

func pts(base int64, coords [][2]float64, intervalSec int64) []owntracks.Point {
	out := make([]owntracks.Point, len(coords))
	for i, c := range coords {
		out[i] = owntracks.Point{Tst: base + int64(i)*intervalSec, Lat: c[0], Lon: c[1], Acc: 10}
	}
	return out
}

func TestDetectStays_OneStay(t *testing.T) {
	// 10 points clustered near same location over 10 minutes.
	base := int64(1_700_000_000)
	coords := [][2]float64{
		{-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
		{-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
		{-33.8687, 151.2094}, {-33.8690, 151.2092}, {-33.8688, 151.2093},
		{-33.8689, 151.2094},
	}
	p := pts(base, coords, 60) // one point per minute → 9 min span
	stays := trips.DetectStays(p, 100, 5*time.Minute, 5*time.Minute)
	require.Len(t, stays, 1)
	assert.InDelta(t, -33.8688, stays[0].CentLat, 0.001)
	assert.Equal(t, base, stays[0].ArrivalTst)
}

func TestDetectStays_TwoStays_GapSplits(t *testing.T) {
	base := int64(1_700_000_000)
	// Stay A: 6 min at location A
	coordsA := [][2]float64{
		{-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
		{-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
	}
	pA := pts(base, coordsA, 60)
	// 20-min gap (exceeds 5-min maxGap)
	// Stay B: 6 min at location B (far away)
	coordsB := [][2]float64{
		{-33.9000, 151.2500}, {-33.9001, 151.2501}, {-33.8999, 151.2499},
		{-33.9002, 151.2500}, {-33.9000, 151.2502}, {-33.9001, 151.2498},
	}
	offsetB := base + int64(len(coordsA)-1)*60 + 20*60 + 60
	pB := pts(offsetB, coordsB, 60)

	all := append(pA, pB...)
	stays := trips.DetectStays(all, 100, 5*time.Minute, 5*time.Minute)
	assert.Len(t, stays, 2)
}

func TestDetectStays_TooShort_Discarded(t *testing.T) {
	base := int64(1_700_000_000)
	// Only 3 points at 60-s intervals = 2 min < minDuration 5 min
	coords := [][2]float64{
		{-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
	}
	p := pts(base, coords, 60)
	stays := trips.DetectStays(p, 100, 5*time.Minute, 5*time.Minute)
	assert.Empty(t, stays)
}

func TestDetectStays_MergesAdjacentNearbyStays(t *testing.T) {
	base := int64(1_700_000_000)
	// Two stays at virtually the same spot, gap = 3 min (< maxGap 5 min)
	coordsA := [][2]float64{
		{-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
		{-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
	}
	pA := pts(base, coordsA, 60)
	coordsB := [][2]float64{
		{-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
		{-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
	}
	offsetB := base + int64(len(coordsA)-1)*60 + 3*60 + 60
	pB := pts(offsetB, coordsB, 60)
	all := append(pA, pB...)
	stays := trips.DetectStays(all, 100, 5*time.Minute, 5*time.Minute)
	assert.Len(t, stays, 1)
}
