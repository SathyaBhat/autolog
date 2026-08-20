package notify

import (
	"fmt"
	"strings"

	"github.com/sathyabhat/autolog/internal/trips"
)

// formatRoute builds a "Start → Stop1 → Stop2 → End" string for a trip.
func formatRoute(t trips.Trip) string {
	label := func(loc string, lat, lon float64) string {
		if loc != "" {
			return loc
		}
		return fmt.Sprintf("%.4f,%.4f", lat, lon)
	}

	parts := []string{label(t.StartLocation, t.StartLat, t.StartLon)}
	for _, s := range t.StopPoints {
		parts = append(parts, label(s.Location, s.Lat, s.Lon))
	}
	parts = append(parts, label(t.EndLocation, t.EndLat, t.EndLon))
	return strings.Join(parts, " → ")
}
