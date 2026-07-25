package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

// Stay represents a period where the device remained within a small radius.
type Stay struct {
	CentLat      float64
	CentLon      float64
	ArrivalTst   int64
	DepartureTst int64
}

// DetectStays runs a sliding-window stay-point algorithm over sorted pts.
// radiusM is the cluster radius in metres; minDuration is the minimum stay
// length; maxGap is the maximum allowed time gap between included points
// before the current cluster is closed.
func DetectStays(pts []owntracks.Point, radiusM float64, minDuration, maxGap time.Duration) []Stay {
	if len(pts) < 2 {
		return nil
	}
	maxGapSec := int64(maxGap.Seconds())
	minDurSec := int64(minDuration.Seconds())

	var stays []Stay
	i := 0
	for i < len(pts) {
		centLat := pts[i].Lat
		centLon := pts[i].Lon
		arrivalTst := pts[i].Tst
		lastIncluded := i
		n := 1 // count of points in cluster

		for j := i + 1; j < len(pts); j++ {
			gap := pts[j].Tst - pts[lastIncluded].Tst
			if gap > maxGapSec {
				break
			}
			distM := HaversineKm(centLat, centLon, pts[j].Lat, pts[j].Lon) * 1000
			if distM <= radiusM {
				// Update centroid incrementally.
				n++
				centLat += (pts[j].Lat - centLat) / float64(n)
				centLon += (pts[j].Lon - centLon) / float64(n)
				lastIncluded = j
			}
		}

		duration := pts[lastIncluded].Tst - arrivalTst
		if duration >= minDurSec {
			stays = append(stays, Stay{
				CentLat:      centLat,
				CentLon:      centLon,
				ArrivalTst:   arrivalTst,
				DepartureTst: pts[lastIncluded].Tst,
			})
			i = lastIncluded + 1
		} else {
			i++
		}
	}

	return mergeAdjacentStays(stays, radiusM, maxGapSec)
}

func mergeAdjacentStays(stays []Stay, radiusM float64, maxGapSec int64) []Stay {
	if len(stays) < 2 {
		return stays
	}
	out := []Stay{stays[0]}
	for _, s := range stays[1:] {
		prev := &out[len(out)-1]
		gap := s.ArrivalTst - prev.DepartureTst
		distM := HaversineKm(prev.CentLat, prev.CentLon, s.CentLat, s.CentLon) * 1000
		if gap <= maxGapSec && distM <= radiusM {
			prev.DepartureTst = s.DepartureTst
			// recompute centroid as simple mean of the two centroids
			prev.CentLat = (prev.CentLat + s.CentLat) / 2
			prev.CentLon = (prev.CentLon + s.CentLon) / 2
		} else {
			out = append(out, s)
		}
	}
	return out
}
