package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

const defaultTripGap = 90 * time.Minute

// SegmentWithStays detects stays in pts, then returns one RawTrip per
// inter-stay gap. Points are pre-filtered for anomalous speed if
// flags.AnomalyFilter is set. Falls back to Segment() if no stays are found.
func SegmentWithStays(points []owntracks.Point, cfg ClassifierConfig) []RawTrip {
	pts := points
	if cfg.Flags.AnomalyFilter {
		maxKmh := cfg.AnomalyMaxKmh
		if maxKmh <= 0 {
			maxKmh = 500
		}
		pts = FilterAnomalousPoints(pts, maxKmh)
	}

	radiusM := cfg.StayRadiusM
	if radiusM <= 0 {
		radiusM = 50
	}
	minDur := cfg.StayMinDur
	if minDur <= 0 {
		minDur = 5 * time.Minute
	}
	maxGap := cfg.StayMaxGap
	if maxGap <= 0 {
		maxGap = 5 * time.Minute
	}

	stays := DetectStays(pts, radiusM, minDur, maxGap)
	if len(stays) == 0 {
		// No stays detected — fall back to gap-based segmentation.
		return Segment(pts, defaultTripGap)
	}

	var result []RawTrip

	// Trip before the first stay.
	if len(pts) > 0 && pts[0].Tst < stays[0].ArrivalTst {
		var seg []owntracks.Point
		for _, p := range pts {
			if p.Tst < stays[0].ArrivalTst {
				seg = append(seg, p)
			}
		}
		if len(seg) >= 2 {
			result = append(result, RawTrip{Points: seg})
		}
	}

	// Trips between consecutive stays.
	for i := 0; i < len(stays)-1; i++ {
		from := stays[i].DepartureTst
		to := stays[i+1].ArrivalTst
		var seg []owntracks.Point
		for _, p := range pts {
			if p.Tst > from && p.Tst < to {
				seg = append(seg, p)
			}
		}
		if len(seg) >= 2 {
			result = append(result, RawTrip{Points: seg})
		}
	}

	// Trip after the last stay.
	last := stays[len(stays)-1]
	var seg []owntracks.Point
	for _, p := range pts {
		if p.Tst > last.DepartureTst {
			seg = append(seg, p)
		}
	}
	if len(seg) >= 2 {
		result = append(result, RawTrip{Points: seg})
	}

	return result
}

// Segment splits a sorted slice of GPS points into RawTrips using the given
// gap threshold. A new trip begins whenever the gap between consecutive points
// exceeds the threshold. Trips with fewer than 2 points are discarded.
func Segment(points []owntracks.Point, maxGap time.Duration) []RawTrip {
	if len(points) < 2 {
		return nil
	}
	if maxGap <= 0 {
		maxGap = defaultTripGap
	}

	var result []RawTrip
	current := []owntracks.Point{points[0]}

	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]
		gap := time.Duration(curr.Tst-prev.Tst) * time.Second
		if gap > maxGap {
			if len(current) >= 2 {
				result = append(result, RawTrip{Points: current})
			}
			current = []owntracks.Point{curr}
		} else {
			current = append(current, curr)
		}
	}
	if len(current) >= 2 {
		result = append(result, RawTrip{Points: current})
	}
	return result
}
