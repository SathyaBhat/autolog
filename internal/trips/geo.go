package trips

import (
	"math"

	"github.com/sathyabhat/autolog/internal/config"
)

const earthRadiusKm = 6371.0

// HaversineKm returns the great-circle distance in kilometres between two
// WGS-84 coordinates.
func HaversineKm(lat1, lon1, lat2, lon2 float64) float64 {
	dLat := toRad(lat2 - lat1)
	dLon := toRad(lon2 - lon1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(toRad(lat1))*math.Cos(toRad(lat2))*
			math.Sin(dLon/2)*math.Sin(dLon/2)
	return earthRadiusKm * 2 * math.Atan2(math.Sqrt(a), math.Sqrt(1-a))
}

// InExclusionZone returns true if (lat, lon) falls within the radius of any
// configured exclusion zone.
func InExclusionZone(lat, lon float64, zones []config.ExclusionZone) bool {
	for _, z := range zones {
		distM := HaversineKm(lat, lon, z.Lat, z.Lon) * 1000
		if distM <= z.RadiusM {
			return true
		}
	}
	return false
}

// HomeLabel returns the zone Name if (lat, lon) falls within any home zone,
// or "" if it does not match any zone.
func HomeLabel(lat, lon float64, zones []config.ExclusionZone) string {
	for _, z := range zones {
		distM := HaversineKm(lat, lon, z.Lat, z.Lon) * 1000
		if distM <= z.RadiusM {
			return z.Name
		}
	}
	return ""
}

func toRad(deg float64) float64 {
	return deg * math.Pi / 180
}
