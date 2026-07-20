package trips

import (
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

// TransportMode is the detected mode of transport for a trip.
type TransportMode string

const (
	ModeCar     TransportMode = "car"
	ModeTrain   TransportMode = "train"
	ModeUnknown TransportMode = "unknown"
)

// RawTrip is a contiguous sequence of GPS points with no gap >5 min.
type RawTrip struct {
	Points []owntracks.Point
}

// Trip is a classified, measured drive (or other movement).
type Trip struct {
	Date        string
	StartTime   time.Time
	EndTime     time.Time
	StartLat    float64
	StartLon    float64
	EndLat      float64
	EndLon      float64
	DistanceKm  float64
	MaxSpeedKmh float64
	Mode        TransportMode
}
