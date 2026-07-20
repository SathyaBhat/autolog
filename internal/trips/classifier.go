package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/owntracks"
)

// ClassifierConfig controls the train-detection and exclusion-zone heuristics.
type ClassifierConfig struct {
	MaxTrainSpeedKmh float64
	ExclusionZones   []config.ExclusionZone
}

// Classify measures and classifies a RawTrip.
// Returns (trip, true) if the trip should be stored, (Trip{}, false) if it
// should be discarded (exclusion zone hit).
// Train trips are stored but marked ModeTrain so they appear in the log.
func Classify(raw RawTrip, cfg ClassifierConfig) (Trip, bool) {
	pts := raw.Points
	first := pts[0]
	last := pts[len(pts)-1]

	if InExclusionZone(first.Lat, first.Lon, cfg.ExclusionZones) ||
		InExclusionZone(last.Lat, last.Lon, cfg.ExclusionZones) {
		return Trip{}, false
	}

	var distKm float64
	var maxSpeed float64
	for i := 1; i < len(pts); i++ {
		distKm += HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
	}
	for _, p := range pts {
		if p.Vel > maxSpeed {
			maxSpeed = p.Vel
		}
	}

	mode := classifyMode(pts, maxSpeed, cfg.MaxTrainSpeedKmh)

	startTime := time.Unix(first.Tst, 0).UTC()
	endTime := time.Unix(last.Tst, 0).UTC()

	return Trip{
		Date:        startTime.Format("2006-01-02"),
		StartTime:   startTime,
		EndTime:     endTime,
		StartLat:    first.Lat,
		StartLon:    first.Lon,
		EndLat:      last.Lat,
		EndLon:      last.Lon,
		DistanceKm:  distKm,
		MaxSpeedKmh: maxSpeed,
		Mode:        mode,
	}, true
}

// classifyMode returns the transport mode based on speed profile.
// Train heuristic: max speed exceeds threshold OR sustained avg >130 km/h
// over any 10-minute window.
func classifyMode(pts []owntracks.Point, maxSpeed, threshold float64) TransportMode {
	if maxSpeed >= threshold {
		return ModeTrain
	}
	const windowSec = int64(600)
	for i := 0; i < len(pts); i++ {
		windowStart := pts[i].Tst
		var sumSpeed float64
		count := 0
		for j := i; j < len(pts) && pts[j].Tst-windowStart <= windowSec; j++ {
			sumSpeed += pts[j].Vel
			count++
		}
		if count >= 3 && sumSpeed/float64(count) > 130 {
			return ModeTrain
		}
	}
	return ModeCar
}
