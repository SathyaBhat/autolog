package trips

import (
	"fmt"
	"math"
	"time"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/owntracks"
)

// ClassifierConfig controls the train-detection and exclusion-zone heuristics.
type ClassifierConfig struct {
	MaxTrainSpeedKmh float64
	MinDistanceKm    float64
	MaxAccM          float64
	StopGap          time.Duration
	ExclusionZones   []config.ExclusionZone
	Flags            AlgorithmFlags
	AnomalyMaxKmh    float64 // used when Flags.AnomalyFilter is true; 0 → 500 km/h default
	StayRadiusM      float64 // used when Flags.StaySegment is true; 0 → 50 m default
	StayMinDur       time.Duration
	StayMaxGap       time.Duration
	TransitGap       time.Duration // min gap between consecutive points to flag as transit
	TransitMinDistKm float64       // min distance across that gap to confirm transit
}

// Classify measures and classifies a RawTrip.
// Returns (trip, reason, true) if the trip should be stored, (Trip{}, reason, false) if it
// should be discarded (exclusion zone hit, too short, or all points inaccurate).
// Train trips are stored but marked ModeTrain so they appear in the log.
func Classify(raw RawTrip, cfg ClassifierConfig) (Trip, string, bool) {
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
		return Trip{}, "insufficient accurate points", false
	}

	first := pts[0]
	last := pts[len(pts)-1]

	if InExclusionZone(first.Lat, first.Lon, cfg.ExclusionZones) ||
		InExclusionZone(last.Lat, last.Lon, cfg.ExclusionZones) {
		return Trip{}, "exclusion zone", false
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
		return Trip{}, fmt.Sprintf("too short (%.2f km)", distKm), false
	}

	if isTransit(pts, cfg.TransitGap, cfg.TransitMinDistKm) {
		return Trip{}, "transit", false
	}

	var mode TransportMode
	if cfg.Flags.SegmentVote {
		mode = classifyBySegmentVote(pts, cfg.MaxTrainSpeedKmh)
	} else {
		mode = classifyMode(pts, maxSpeed, cfg.MaxTrainSpeedKmh)
	}

	if mode == ModeTrain && cfg.Flags.AccelTrainGate {
		if !confirmTrain(pts, maxSpeed) {
			mode = ModeCar
		}
	}

	stopGap := cfg.StopGap
	if stopGap <= 0 {
		stopGap = 10 * time.Minute
	}
	stayRadius := cfg.StayRadiusM
	if stayRadius <= 0 {
		stayRadius = 50
	}
	stayMaxGap := cfg.StayMaxGap
	if stayMaxGap <= 0 {
		stayMaxGap = 5 * time.Minute
	}
	var stops []StopPoint
	for _, s := range DetectStays(pts, stayRadius, stopGap, stayMaxGap) {
		stops = append(stops, StopPoint{
			Lat:          s.CentLat,
			Lon:          s.CentLon,
			ArrivalTst:   s.ArrivalTst,
			DepartureTst: s.DepartureTst,
		})
	}

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
		StopPoints:  stops,
		Points:      pts,
	}, "", true
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

// isTransit returns true if any consecutive pair of points has a time gap >=
// transitGap and a distance >= transitMinDistKm. Zero/negative thresholds
// disable the check.
func isTransit(pts []owntracks.Point, transitGap time.Duration, transitMinDistKm float64) bool {
	if transitGap <= 0 || transitMinDistKm <= 0 {
		return false
	}
	gapSec := int64(transitGap.Seconds())
	for i := 1; i < len(pts); i++ {
		dt := pts[i].Tst - pts[i-1].Tst
		if dt >= gapSec {
			d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
			if d >= transitMinDistKm {
				return true
			}
		}
	}
	return false
}

// classifyBySegmentVote splits pts at >50% relative speed changes, classifies
// each segment by average speed, then returns the mode with the most total
// seconds. Speed ladder: ≤7 → walking, ≤20 → cycling, ≤trainThreshold → car, >trainThreshold → train.
func classifyBySegmentVote(pts []owntracks.Point, trainThreshold float64) TransportMode {
	type seg struct {
		mode   TransportMode
		durSec int64
	}
	var segments []seg
	segStart := 0
	prevSpeed := -1.0

	flush := func(end int) {
		if end <= segStart {
			return
		}
		var dist float64
		for k := segStart + 1; k <= end && k < len(pts); k++ {
			dist += HaversineKm(pts[k-1].Lat, pts[k-1].Lon, pts[k].Lat, pts[k].Lon)
		}
		dur := pts[min64(int64(end), int64(len(pts)-1))].Tst - pts[segStart].Tst
		if dur <= 0 {
			return
		}
		avg := dist / float64(dur) * 3600
		var m TransportMode
		switch {
		case avg <= 7:
			m = ModeWalking
		case avg <= 20:
			m = ModeCycling
		case avg <= trainThreshold:
			m = ModeCar
		default:
			m = ModeTrain
		}
		segments = append(segments, seg{mode: m, durSec: dur})
	}

	for i := 1; i < len(pts); i++ {
		dt := float64(pts[i].Tst - pts[i-1].Tst)
		if dt <= 0 {
			continue
		}
		d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
		speed := d / dt * 3600
		if prevSpeed > 0 && speed > 0 {
			change := (speed - prevSpeed) / prevSpeed
			if change > 0.5 || change < -0.5 {
				flush(i - 1)
				segStart = i - 1
			}
		}
		prevSpeed = speed
	}
	flush(len(pts) - 1)

	tally := map[TransportMode]int64{}
	for _, s := range segments {
		tally[s.mode] += s.durSec
	}

	var best TransportMode = ModeCar
	var bestDur int64
	for m, d := range tally {
		if d > bestDur {
			bestDur = d
			best = m
		}
	}
	return best
}

// confirmTrain returns true if the speed profile is consistent with rail:
// average acceleration magnitude < 0.2 m/s² AND max/avg speed ratio < 1.3.
func confirmTrain(pts []owntracks.Point, maxSpeed float64) bool {
	if len(pts) < 3 {
		return true // not enough data to refute
	}
	// Compute per-segment speeds in m/s.
	speeds := make([]float64, 0, len(pts)-1)
	for i := 1; i < len(pts); i++ {
		dt := float64(pts[i].Tst - pts[i-1].Tst)
		if dt <= 0 {
			continue
		}
		d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon) * 1000
		speeds = append(speeds, d/dt)
	}
	if len(speeds) < 2 {
		return true
	}
	var totalAccel float64
	for i := 1; i < len(speeds); i++ {
		dt := float64(pts[i+1].Tst - pts[i].Tst)
		if dt <= 0 {
			continue
		}
		totalAccel += math.Abs(speeds[i]-speeds[i-1]) / dt
	}
	avgAccel := totalAccel / float64(len(speeds)-1)
	if avgAccel >= 0.2 {
		return false
	}
	var sumSpeed float64
	for _, s := range speeds {
		sumSpeed += s
	}
	avgSpeed := sumSpeed / float64(len(speeds))
	if avgSpeed <= 0 {
		return true
	}
	maxSpeedMs := maxSpeed / 3.6
	if maxSpeedMs/avgSpeed >= 1.3 {
		return false
	}
	return true
}
