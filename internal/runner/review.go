package runner

import (
	"context"
	"fmt"
	"sort"
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

const (
	reviewBefore = 30 * time.Minute
	reviewAfter  = 4 * time.Hour
)

// ReviewTrip re-fetches and reclassifies the trip nearest target, which must
// be expressed in the device's local timezone. With reprocess=true, the
// existing stored trip is replaced without sending a notification.
func (r *Runner) ReviewTrip(ctx context.Context, target time.Time, reprocess bool) (trips.Trip, error) {
	from := target.Add(-reviewBefore).UTC()
	to := target.Add(reviewAfter).UTC()
	points, err := r.ot.Fetch(ctx, from, to)
	if err != nil {
		return trips.Trip{}, err
	}
	sort.Slice(points, func(i, j int) bool {
		return points[i].Tst < points[j].Tst
	})

	classCfg := r.classifierConfig()
	rawTrips := r.segmentPoints(points, classCfg)
	sydneyTZ := target.Location()
	if sydneyTZ == nil {
		sydneyTZ = time.UTC
	}

	var match *trips.Trip
	var nearest time.Duration
	for _, raw := range rawTrips {
		trip, _, keep := trips.Classify(raw, classCfg)
		if !keep {
			continue
		}
		start := trip.StartTime.In(sydneyTZ)
		if start.Format("2006-01-02") != target.Format("2006-01-02") {
			continue
		}
		delta := start.Sub(target)
		if delta < 0 {
			delta = -delta
		}
		if delta <= 2*time.Minute && (match == nil || delta < nearest) {
			copy := trip
			match = &copy
			nearest = delta
		}
	}
	if match == nil {
		return trips.Trip{}, fmt.Errorf("no classified trip near %s", target.Format("2006-01-02 15:04 MST"))
	}

	r.annotateTrip(ctx, match)
	if reprocess {
		exists, err := r.store.TripExists(ctx, match.Date, match.StartTime)
		if err != nil {
			return trips.Trip{}, err
		}
		if exists {
			if err := r.store.ReplaceTrip(ctx, *match); err != nil {
				return trips.Trip{}, err
			}
		} else {
			if err := r.store.SaveTrip(ctx, *match); err != nil {
				return trips.Trip{}, err
			}
		}
	}
	return *match, nil
}

func (r *Runner) classifierConfig() trips.ClassifierConfig {
	return trips.ClassifierConfig{
		MaxTrainSpeedKmh: r.cfg.Filters.MaxTrainSpeedKmh,
		MaxAccM:          r.cfg.Filters.MaxAccM,
		StopGap:          r.cfg.Filters.StopGap,
		ExclusionZones:   r.cfg.Filters.ExclusionZones,
		Flags: trips.AlgorithmFlags{
			AnomalyFilter:  r.cfg.Filters.AlgorithmFlags.AnomalyFilter,
			StaySegment:    r.cfg.Filters.AlgorithmFlags.StaySegment,
			SegmentVote:    r.cfg.Filters.AlgorithmFlags.SegmentVote,
			AccelTrainGate: r.cfg.Filters.AlgorithmFlags.AccelTrainGate,
		},
		AnomalyMaxKmh: r.cfg.Filters.AnomalyMaxKmh,
		StayRadiusM:   r.cfg.Filters.StayRadiusM,
		StayMinDur:    r.cfg.Filters.StayMinDur,
		StayMaxGap:    r.cfg.Filters.StayMaxGap,
	}
}

func (r *Runner) segmentPoints(points []owntracks.Point, cfg trips.ClassifierConfig) []trips.RawTrip {
	if cfg.Flags.StaySegment {
		return trips.SegmentWithStays(points, cfg)
	}
	if cfg.Flags.AnomalyFilter {
		points = trips.FilterAnomalousPoints(points, cfg.AnomalyMaxKmh)
	}
	return trips.Segment(points, r.cfg.Filters.MaxTripGap)
}

func (r *Runner) annotateTrip(ctx context.Context, trip *trips.Trip) {
	homeZones := r.cfg.Filters.HomeZones
	applyHomeLabel := func(lat, lon float64, geocoded string) string {
		if label := trips.HomeLabel(lat, lon, homeZones); label != "" {
			return label
		}
		return geocoded
	}

	if r.geo != nil {
		if loc, err := r.geo.Reverse(ctx, trip.StartLat, trip.StartLon); err == nil {
			trip.StartLocation = applyHomeLabel(trip.StartLat, trip.StartLon, loc.Label)
		}
		if loc, err := r.geo.Reverse(ctx, trip.EndLat, trip.EndLon); err == nil {
			trip.EndLocation = applyHomeLabel(trip.EndLat, trip.EndLon, loc.Label)
		}
		for i := range trip.StopPoints {
			if loc, err := r.geo.Reverse(ctx, trip.StopPoints[i].Lat, trip.StopPoints[i].Lon); err == nil {
				trip.StopPoints[i].Location = applyHomeLabel(trip.StopPoints[i].Lat, trip.StopPoints[i].Lon, loc.Label)
			}
		}
		return
	}

	trip.StartLocation = applyHomeLabel(trip.StartLat, trip.StartLon, trip.StartLocation)
	trip.EndLocation = applyHomeLabel(trip.EndLat, trip.EndLon, trip.EndLocation)
	for i := range trip.StopPoints {
		trip.StopPoints[i].Location = applyHomeLabel(trip.StopPoints[i].Lat, trip.StopPoints[i].Lon, trip.StopPoints[i].Location)
	}
}
