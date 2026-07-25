package trips

import "github.com/sathyabhat/autolog/internal/owntracks"

// FilterAnomalousPoints removes GPS fixes whose implied speed to or from
// both neighbours exceeds maxSpeedKmh (e.g. cell-tower jumps).
// Pass maxSpeedKmh <= 0 to disable.
func FilterAnomalousPoints(pts []owntracks.Point, maxSpeedKmh float64) []owntracks.Point {
	if maxSpeedKmh <= 0 || len(pts) < 2 {
		return pts
	}
	out := make([]owntracks.Point, 0, len(pts))
	for i, p := range pts {
		if isAnomalous(pts, i, maxSpeedKmh) {
			continue
		}
		out = append(out, p)
	}
	return out
}

func isAnomalous(pts []owntracks.Point, i int, maxKmh float64) bool {
	p := pts[i]

	// Points with zero or negative time delta to the previous point are anomalous.
	// (This filters duplicates and timestamp reversals.)
	if i > 0 {
		prev := pts[i-1]
		dt := float64(p.Tst - prev.Tst)
		if dt <= 0 {
			return true
		}
	}

	// First point: check only the forward segment.
	if i == 0 {
		if len(pts) > 1 {
			next := pts[1]
			dt := float64(next.Tst - p.Tst)
			if dt <= 0 {
				return true
			}
			d := HaversineKm(p.Lat, p.Lon, next.Lat, next.Lon)
			return d/dt*3600 > maxKmh
		}
		return false
	}

	// Last point: backward segment already checked via dt <= 0 guard above.
	// Speed was already validated in the i > 0 block above (dt check passed),
	// so now check the speed magnitude.
	if i == len(pts)-1 {
		prev := pts[i-1]
		dt := float64(p.Tst - prev.Tst) // already known > 0 from the check above
		d := HaversineKm(prev.Lat, prev.Lon, p.Lat, p.Lon)
		return d/dt*3600 > maxKmh
	}

	// Middle point: both prev and next segments must be fast
	prev := pts[i-1]
	next := pts[i+1]

	dt1 := float64(p.Tst - prev.Tst)
	d1 := HaversineKm(prev.Lat, prev.Lon, p.Lat, p.Lon)
	prevFast := d1/dt1*3600 > maxKmh

	dt2 := float64(next.Tst - p.Tst)
	if dt2 <= 0 {
		return true
	}
	d2 := HaversineKm(p.Lat, p.Lon, next.Lat, next.Lon)
	nextFast := d2/dt2*3600 > maxKmh

	return prevFast && nextFast
}
