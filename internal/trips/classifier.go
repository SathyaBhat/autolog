package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/owntracks"
)

// ClassifierConfig controls the train-detection and exclusion-zone heuristics.
type ClassifierConfig struct {
	MaxTrainSpeedKmh float64
	MinDistanceKm    float64
	MaxAccM          float64
	ExclusionZones   []config.ExclusionZone
}

// Classify measures and classifies a RawTrip.
// Returns (trip, true) if the trip should be stored, (Trip{}, false) if it
// should be discarded (exclusion zone hit, too short, or all points inaccurate).
// Train trips are stored but marked ModeTrain so they appear in the log.
func Classify(raw RawTrip, cfg ClassifierConfig) (Trip, bool) {
	maxAcc := cfg.MaxAccM
	if maxAcc <= 0 {
		maxAcc = 100.0
	}

	// Drop points with accuracy radius worse than threshold (cell-tower fixes).
	var pts []owntracks.Point
	for _, p := range raw.Points {
		if p.Acc <= maxAcc {
			pts = append(pts, p)
		}
	}
	if len(pts) < 2 {
		return Trip{}, false
	}

	first := pts[0]
	last := pts[len(pts)-1]

	if InExclusionZone(first.Lat, first.Lon, cfg.ExclusionZones) ||
		InExclusionZone(last.Lat, last.Lon, cfg.ExclusionZones) {
		return Trip{}, false
	}

	// Compute distance and speed from coordinates, not reported vel.
	// vel=null deserialises as 0 and cannot be trusted for classification.
	var distKm, maxSpeed float64
	for i := 1; i < len(pts); i++ {
		d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
		distKm += d
		dt := float64(pts[i].Tst - pts[i-1].Tst) // seconds
		if dt > 0 {
			speedKmh := d / dt * 3600
			if speedKmh > maxSpeed {
				maxSpeed = speedKmh
			}
		}
	}

	minDist := cfg.MinDistanceKm
	if minDist <= 0 {
		minDist = 2.0
	}
	if distKm < minDist {
		return Trip{}, false
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
		Points:      pts,
	}, true
}

// classifyMode returns the transport mode based on speed profile.
// Train heuristic: max speed exceeds threshold OR sustained avg >130 km/h
// over any 10-minute window. Speeds are computed from coordinates.
func classifyMode(pts []owntracks.Point, maxSpeed, threshold float64) TransportMode {
	if maxSpeed >= threshold {
		return ModeTrain
	}
	// Sustained-speed window: compute avg coordinate-derived speed over 10-min windows.
	const windowSec = int64(600)
	for i := 0; i < len(pts)-1; i++ {
		windowStart := pts[i].Tst
		var totalDist float64
		for j := i + 1; j < len(pts) && pts[j].Tst-windowStart <= windowSec; j++ {
			totalDist += HaversineKm(pts[j-1].Lat, pts[j-1].Lon, pts[j].Lat, pts[j].Lon)
		}
		elapsed := float64(min64(pts[len(pts)-1].Tst, windowStart+windowSec)-windowStart)
		if elapsed > 0 && totalDist/elapsed*3600 > 130 {
			return ModeTrain
		}
	}
	return ModeCar
}

func min64(a, b int64) int64 {
	if a < b {
		return a
	}
	return b
}
