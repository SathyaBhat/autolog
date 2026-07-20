package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

const tripGap = 5 * time.Minute

// Segment splits a sorted slice of GPS points into RawTrips.
// A new trip begins whenever the gap between consecutive points exceeds 5 min.
// Trips with fewer than 2 points are discarded.
func Segment(points []owntracks.Point) []RawTrip {
	if len(points) < 2 {
		return nil
	}

	var result []RawTrip
	current := []owntracks.Point{points[0]}

	for i := 1; i < len(points); i++ {
		prev := points[i-1]
		curr := points[i]
		gap := time.Duration(curr.Tst-prev.Tst) * time.Second
		if gap > tripGap {
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
