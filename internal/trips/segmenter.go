package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

const defaultTripGap = 90 * time.Minute

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
