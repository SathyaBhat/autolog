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
	Confidence   StopConfidence
	Evidence     string
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
		tagStart := i
		if i+1 < len(pts) {
			tagStart = i + 1
		}
		if stay, next, ok := taggedStopAt(pts, tagStart, radiusM, minDurSec); ok {
			stays = append(stays, stay)
			i = next
			continue
		}

		centLat := pts[i].Lat
		centLon := pts[i].Lon
		arrivalTst := pts[i].Tst
		lastIncluded := i
		n := 1 // count of points in cluster
		longGapStay := false

		for j := i + 1; j < len(pts); j++ {
			gap := pts[j].Tst - pts[lastIncluded].Tst
			if gap > maxGapSec {
				// Significant-mode reporting can leave only one point on
				// either side of a long stop. Treat that pair as a stay when
				// the device was moving before and after it; a lone silent
				// endpoint is not enough evidence.
				if gap >= minDurSec &&
					HaversineKm(centLat, centLon, pts[j].Lat, pts[j].Lon)*1000 <= radiusM &&
					i > 0 && j+1 < len(pts) &&
					movingBetween(pts[i-1], pts[i]) &&
					movingBetween(pts[j], pts[j+1]) {
					confidence, evidence := longGapEvidence(pts[i], pts[j])
					stays = append(stays, Stay{
						CentLat:      (centLat + pts[j].Lat) / 2,
						CentLon:      (centLon + pts[j].Lon) / 2,
						ArrivalTst:   pts[i].Tst,
						DepartureTst: pts[j].Tst,
						Confidence:   confidence,
						Evidence:     evidence,
					})
					i = j + 1
					lastIncluded = j
					longGapStay = true
					break
				}
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

		if longGapStay {
			continue
		}

		duration := pts[lastIncluded].Tst - arrivalTst
		if duration >= minDurSec {
			stays = append(stays, Stay{
				CentLat:      centLat,
				CentLon:      centLon,
				ArrivalTst:   arrivalTst,
				DepartureTst: pts[lastIncluded].Tst,
				Confidence:   StopConfidenceHigh,
				Evidence:     "repeated co-located points",
			})
			i = lastIncluded + 1
		} else {
			i++
		}
	}

	return mergeAdjacentStays(stays, radiusM, maxGapSec)
}

func taggedStopAt(pts []owntracks.Point, i int, radiusM float64, minDurSec int64) (Stay, int, bool) {
	if i <= 0 || i >= len(pts) || pts[i-1].Tag != "drive" || pts[i].Tag == "drive" {
		return Stay{}, i + 1, false
	}

	j := i
	var latSum, lonSum float64
	var count int
	for ; j < len(pts) && pts[j].Tag != "drive"; j++ {
		latSum += pts[j].Lat
		lonSum += pts[j].Lon
		count++
	}
	if j == len(pts) || count == 0 {
		return Stay{}, i + 1, false
	}

	arrival := pts[i-1]
	departure := pts[j]
	if departure.Tst-arrival.Tst < minDurSec {
		return Stay{}, i + 1, false
	}

	centLat := latSum / float64(count)
	centLon := lonSum / float64(count)
	if HaversineKm(centLat, centLon, arrival.Lat, arrival.Lon)*1000 > radiusM {
		return Stay{}, i + 1, false
	}
	for k := i; k < j; k++ {
		if HaversineKm(centLat, centLon, pts[k].Lat, pts[k].Lon)*1000 > radiusM {
			return Stay{}, i + 1, false
		}
	}

	return Stay{
		CentLat:      centLat,
		CentLon:      centLon,
		ArrivalTst:   arrival.Tst,
		DepartureTst: departure.Tst,
		Confidence:   StopConfidenceHigh,
		Evidence:     "drive tag cleared",
	}, j, true
}

func movingBetween(a, b owntracks.Point) bool {
	dt := b.Tst - a.Tst
	if dt <= 0 {
		return false
	}
	return HaversineKm(a.Lat, a.Lon, b.Lat, b.Lon)/float64(dt)*3600 > 1
}

func longGapEvidence(arrival, departure owntracks.Point) (StopConfidence, string) {
	if lowReportedVelocity(arrival) && lowReportedVelocity(departure) {
		return StopConfidenceHigh, "co-located endpoints, low reported velocity, movement before and after"
	}
	return StopConfidenceMedium, "co-located endpoints with movement before and after"
}

func lowReportedVelocity(p owntracks.Point) bool {
	return p.Vel >= 0 && p.Vel <= 5
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
			if prev.Confidence != StopConfidenceHigh && s.Confidence == StopConfidenceHigh {
				prev.Confidence = StopConfidenceHigh
			}
			if prev.Evidence == "" {
				prev.Evidence = s.Evidence
			} else if s.Evidence != "" && prev.Evidence != s.Evidence {
				prev.Evidence += "; " + s.Evidence
			}
		} else {
			out = append(out, s)
		}
	}
	return out
}
