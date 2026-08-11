package notify

import (
	"fmt"
	"strings"
	"time"

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
		stopLabel := label(s.Location, s.Lat, s.Lon)
		duration := time.Duration(s.DepartureTst-s.ArrivalTst) * time.Second
		if duration >= time.Minute {
			stopLabel = fmt.Sprintf("%s (%s)", stopLabel, formatDuration(duration))
		}
		parts = append(parts, stopLabel)
	}
	parts = append(parts, label(t.EndLocation, t.EndLat, t.EndLon))
	return strings.Join(parts, " → ")
}

func formatDuration(d time.Duration) string {
	hours := int(d / time.Hour)
	minutes := int(d/time.Minute) % 60
	if hours > 0 {
		if minutes == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}
