package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

// AlgorithmFlags selects which detection improvements are active.
// All flags default to false (baseline behaviour preserved).
type AlgorithmFlags struct {
	AnomalyFilter  bool
	StaySegment    bool
	SegmentVote    bool
	AccelTrainGate bool
}

// TransportMode is the detected mode of transport for a trip.
type TransportMode string

const (
	ModeCar     TransportMode = "car"
	ModeTrain   TransportMode = "train"
	ModeUnknown TransportMode = "unknown"
	ModeWalking TransportMode = "walking"
	ModeCycling TransportMode = "cycling"
)

// RawTrip is a contiguous sequence of GPS points with no gap >5 min.
type RawTrip struct {
	Points []owntracks.Point
}

// StopPoint is a coordinate where the device paused mid-trip (gap > stop_gap).
type StopPoint struct {
	Lat      float64
	Lon      float64
	Location string // filled in by runner after geocoding
}

// OwnTracksPoint is a re-export of owntracks.Point for use by cmd/replay.
type OwnTracksPoint = owntracks.Point

// Trip is a classified, measured drive (or other movement).
type Trip struct {
	Date          string
	StartTime     time.Time
	EndTime       time.Time
	StartLat      float64
	StartLon      float64
	EndLat        float64
	EndLon        float64
	DistanceKm    float64
	MaxSpeedKmh   float64
	Mode          TransportMode
	StartLocation string
	EndLocation   string
	StopPoints    []StopPoint
	Points        []owntracks.Point
}
