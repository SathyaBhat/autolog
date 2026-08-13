package runner

import (
	"context"
	"fmt"
	"sort"
	"sync"
	"time"

	"go.uber.org/zap"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

const notifyThresholdKm = 5.0
const firstRunLookback = 24 * time.Hour

// locationFetcher is the subset of owntracks.Client used by Runner.
type locationFetcher interface {
	Fetch(ctx context.Context, from, to time.Time) ([]owntracks.Point, error)
}

// Notifier sends trip notifications. *notify.Telegram and *notify.Stdout both satisfy this.
type Notifier interface {
	SendAll(ctx context.Context, ts []trips.Trip) error
}

// geocoder resolves coordinates to a human-readable label.
type geocoder interface {
	Reverse(ctx context.Context, lat, lon float64) (geocode.Location, error)
}

// Runner orchestrates the fetch → segment → classify → store → notify pipeline.
type Runner struct {
	cfg      *config.Config
	ot       locationFetcher
	store    *store.Store
	tg       Notifier
	geo      geocoder
	log      *zap.Logger
	manualMu sync.Mutex
}

// New creates a Runner with concrete dependencies.
func New(cfg *config.Config, ot *owntracks.Client, st *store.Store, tg Notifier, geo *geocode.Client, log *zap.Logger) *Runner {
	return NewWithDeps(cfg, ot, st, tg, geo, log)
}

// NewWithDeps creates a Runner with interface dependencies (for testing).
func NewWithDeps(cfg *config.Config, ot locationFetcher, st *store.Store, tg Notifier, geo geocoder, log *zap.Logger) *Runner {
	return &Runner{cfg: cfg, ot: ot, store: st, tg: tg, geo: geo, log: log}
}

// StartExplicitTrip records a phone-triggered drive start. The start survives
// service restarts until a matching stop event completes the trip.
func (r *Runner) StartExplicitTrip(ctx context.Context, start time.Time) error {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()

	active, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		return err
	}
	if !active.IsZero() {
		return fmt.Errorf("an explicit trip is already active since %s", active.UTC().Format(time.RFC3339))
	}
	return r.store.SetActiveManualTripStart(ctx, start)
}

// StopExplicitTrip fetches exactly the OwnTracks window between the phone
// events, classifies it as an explicit car trip, stores it, and notifies.
func (r *Runner) StopExplicitTrip(ctx context.Context, end time.Time) (trips.Trip, error) {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()

	start, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		return trips.Trip{}, err
	}
	if start.IsZero() {
		return trips.Trip{}, fmt.Errorf("no explicit trip is active")
	}
	if !end.After(start) {
		return trips.Trip{}, fmt.Errorf("trip stop must be after trip start")
	}
	// A stop is terminal even when OwnTracks has no usable points. This keeps
	// a failed test or interrupted drive from blocking the next start event.
	if err := r.store.ClearActiveManualTripStart(ctx); err != nil {
		return trips.Trip{}, err
	}

	trip, ok, err := r.fetchExplicitTrip(ctx, start, end)
	if err != nil {
		return trips.Trip{}, err
	}
	if !ok {
		return trips.Trip{}, fmt.Errorf("fewer than two accurate OwnTracks points found between start and stop")
	}
	r.annotateTrip(ctx, &trip)

	if err := r.saveExplicitTrip(ctx, trip, true); err != nil {
		return trips.Trip{}, err
	}
	return trip, nil
}

func (r *Runner) fetchExplicitTrip(ctx context.Context, start, end time.Time) (trips.Trip, bool, error) {
	points, err := r.ot.Fetch(ctx, start.UTC(), end.UTC())
	if err != nil {
		return trips.Trip{}, false, err
	}
	filtered := make([]owntracks.Point, 0, len(points))
	for _, p := range points {
		if p.Tst >= start.Unix() && p.Tst <= end.Unix() {
			filtered = append(filtered, p)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Tst < filtered[j].Tst })
	if len(filtered) < 2 {
		return trips.Trip{}, false, nil
	}

	cfg := r.classifierConfig()
	cfg.ExplicitDrive = true
	trip, reason, keep := trips.Classify(trips.RawTrip{Points: filtered}, cfg)
	if !keep {
		return trips.Trip{}, false, fmt.Errorf("explicit trip discarded: %s", reason)
	}
	return trip, true, nil
}

func (r *Runner) saveExplicitTrip(ctx context.Context, trip trips.Trip, notify bool) error {
	exists, err := r.store.TripExists(ctx, trip.Date, trip.StartTime)
	if err != nil {
		return err
	}
	if exists {
		if err := r.store.ReplaceTrip(ctx, trip); err != nil {
			return err
		}
	} else if err := r.store.SaveTrip(ctx, trip); err != nil {
		return err
	}
	if notify && r.tg != nil {
		if err := r.tg.SendAll(ctx, []trips.Trip{trip}); err != nil {
			r.log.Error("explicit trip notification failed", zap.Error(err))
		}
	}
	return nil
}

// Run blocks, processing on each ticker tick, until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	batchTicker := time.NewTicker(r.cfg.Scheduler.Interval)
	defer batchTicker.Stop()
	manualInterval := r.cfg.Scheduler.ManualTripInterval
	if manualInterval <= 0 {
		manualInterval = time.Minute
	}
	manualTicker := time.NewTicker(manualInterval)
	defer manualTicker.Stop()

	r.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("runner shutting down")
			return ctx.Err()
		case <-batchTicker.C:
			r.tick(ctx)
		case <-manualTicker.C:
			r.refreshExplicitTrip(ctx)
		}
	}
}

func (r *Runner) tick(ctx context.Context) {
	to := time.Now().UTC()

	last, err := r.store.GetLastProcessedTime(ctx)
	if err != nil {
		r.log.Error("failed to get last processed time", zap.Error(err))
		return
	}

	var from time.Time
	if last.IsZero() {
		from = to.Add(-firstRunLookback)
		r.log.Info("first run: processing last 24h", zap.Time("from", from))
	} else {
		from = last
	}

	active, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		r.log.Error("failed to get active explicit trip", zap.Error(err))
		return
	}
	if !active.IsZero() {
		if !from.Before(active) {
			r.log.Debug("batch processing paused during explicit trip",
				zap.Time("start", active))
			return
		}
		if active.Before(to) {
			to = active
		}
	}

	if err := r.ProcessOnce(ctx, from, to); err != nil {
		r.log.Error("process window failed", zap.Error(err))
		return
	}

	if err := r.store.SetLastProcessedTime(ctx, to); err != nil {
		r.log.Error("failed to update last processed time", zap.Error(err))
	}
}

// refreshExplicitTrip updates the stored partial trip while a phone-triggered
// trip is active. It deliberately does not notify; stop is the commit point.
func (r *Runner) refreshExplicitTrip(ctx context.Context) {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()

	start, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		r.log.Error("failed to get active explicit trip", zap.Error(err))
		return
	}
	if start.IsZero() {
		return
	}

	trip, ok, err := r.fetchExplicitTrip(ctx, start, time.Now().UTC())
	if err != nil {
		r.log.Warn("explicit trip refresh failed", zap.Error(err))
		return
	}
	if !ok {
		return
	}
	if err := r.saveExplicitTrip(ctx, trip, false); err != nil {
		r.log.Error("explicit trip refresh save failed", zap.Error(err))
		return
	}
	r.log.Debug("explicit trip refreshed",
		zap.Time("start", trip.StartTime),
		zap.Float64("distance_km", trip.DistanceKm),
		zap.Int("points", len(trip.Points)))
}

// Backfill processes the full range [from, to] in monthly chunks to avoid
// loading all GPS history into memory at once. It does not update the
// last_processed_time state — the caller is responsible for that.
func (r *Runner) Backfill(ctx context.Context, from, to time.Time) error {
	cursor := from
	for cursor.Before(to) {
		chunkEnd := cursor.AddDate(0, 1, 0)
		if chunkEnd.After(to) {
			chunkEnd = to
		}
		r.log.Info("backfill chunk", zap.Time("from", cursor), zap.Time("to", chunkEnd))
		if err := r.ProcessOnce(ctx, cursor, chunkEnd); err != nil {
			return err
		}
		cursor = chunkEnd
	}
	return nil
}

// ProcessOnce fetches points in [from, to], segments, classifies, stores, and
// notifies. Exported so tests can call it directly without the ticker.
func (r *Runner) ProcessOnce(ctx context.Context, from, to time.Time) error {
	points, err := r.ot.Fetch(ctx, from, to)
	if err != nil {
		return err
	}
	if len(points) == 0 {
		r.log.Info("no location points in window", zap.Time("from", from), zap.Time("to", to))
		return nil
	}

	classCfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh:      r.cfg.Filters.MaxTrainSpeedKmh,
		MinDistanceKm:         r.cfg.Filters.MinDistanceKm,
		ExplicitMinDistanceKm: r.cfg.Filters.ExplicitMinDistanceKm,
		MaxAccM:               r.cfg.Filters.MaxAccM,
		StopGap:               r.cfg.Filters.StopGap,
		ExclusionZones:        r.cfg.Filters.ExclusionZones,
		Flags: trips.AlgorithmFlags{
			AnomalyFilter:  r.cfg.Filters.AlgorithmFlags.AnomalyFilter,
			StaySegment:    r.cfg.Filters.AlgorithmFlags.StaySegment,
			SegmentVote:    r.cfg.Filters.AlgorithmFlags.SegmentVote,
			AccelTrainGate: r.cfg.Filters.AlgorithmFlags.AccelTrainGate,
		},
		AnomalyMaxKmh:    r.cfg.Filters.AnomalyMaxKmh,
		StayRadiusM:      r.cfg.Filters.StayRadiusM,
		StayMinDur:       r.cfg.Filters.StayMinDur,
		StayMaxGap:       r.cfg.Filters.StayMaxGap,
		TransitGap:       r.cfg.Filters.TransitGap,
		TransitMinDistKm: r.cfg.Filters.TransitMinDistKm,
	}

	var rawTrips []trips.RawTrip
	if r.cfg.Filters.AlgorithmFlags.StaySegment {
		rawTrips = trips.SegmentWithStays(points, classCfg)
	} else {
		pts := points
		if r.cfg.Filters.AlgorithmFlags.AnomalyFilter {
			pts = trips.FilterAnomalousPoints(pts, classCfg.AnomalyMaxKmh)
		}
		rawTrips = trips.Segment(pts, r.cfg.Filters.MaxTripGap)
	}
	r.log.Info("segmented trips", zap.Int("points", len(points)), zap.Int("trips", len(rawTrips)))

	sydneyTZ, _ := time.LoadLocation("Australia/Sydney")
	if sydneyTZ == nil {
		sydneyTZ = time.UTC
	}

	var toNotify []trips.Trip
	for _, raw := range rawTrips {
		trip, reason, keep := trips.Classify(raw, classCfg)
		if !keep {
			first := raw.Points[0]
			last := raw.Points[len(raw.Points)-1]
			startSyd := time.Unix(first.Tst, 0).In(sydneyTZ)
			r.log.Info("trip discarded",
				zap.String("reason", reason),
				zap.String("start_time_syd", startSyd.Format("02 Jan 2006 15:04")),
				zap.Float64("start_lat", first.Lat),
				zap.Float64("start_lon", first.Lon),
				zap.Float64("end_lat", last.Lat),
				zap.Float64("end_lon", last.Lon),
			)
			continue
		}

		homeZones := r.cfg.Filters.HomeZones
		applyHomeLabel := func(lat, lon float64, geocoded string) string {
			if label := trips.HomeLabel(lat, lon, homeZones); label != "" {
				return label
			}
			return geocoded
		}

		if r.geo != nil {
			if startLoc, err := r.geo.Reverse(ctx, trip.StartLat, trip.StartLon); err == nil {
				trip.StartLocation = applyHomeLabel(trip.StartLat, trip.StartLon, startLoc.Label)
			} else {
				r.log.Warn("geocode start failed", zap.Error(err))
			}
			if endLoc, err := r.geo.Reverse(ctx, trip.EndLat, trip.EndLon); err == nil {
				trip.EndLocation = applyHomeLabel(trip.EndLat, trip.EndLon, endLoc.Label)
			} else {
				r.log.Warn("geocode end failed", zap.Error(err))
			}
			for i := range trip.StopPoints {
				if loc, err := r.geo.Reverse(ctx, trip.StopPoints[i].Lat, trip.StopPoints[i].Lon); err == nil {
					trip.StopPoints[i].Location = applyHomeLabel(trip.StopPoints[i].Lat, trip.StopPoints[i].Lon, loc.Label)
				} else {
					r.log.Warn("geocode stop failed", zap.Error(err))
				}
			}
		} else {
			trip.StartLocation = applyHomeLabel(trip.StartLat, trip.StartLon, trip.StartLocation)
			trip.EndLocation = applyHomeLabel(trip.EndLat, trip.EndLon, trip.EndLocation)
			for i := range trip.StopPoints {
				trip.StopPoints[i].Location = applyHomeLabel(trip.StopPoints[i].Lat, trip.StopPoints[i].Lon, trip.StopPoints[i].Location)
			}
		}

		exists, err := r.store.TripExists(ctx, trip.Date, trip.StartTime)
		if err != nil {
			return err
		}
		if exists {
			if err := r.store.SaveTripStopsIfMissing(ctx, trip.Date, trip.StartTime, trip.StopPoints); err != nil {
				return err
			}
			if len(trip.StopPoints) > 0 {
				r.log.Info("trip stops backfilled",
					zap.String("date", trip.Date),
					zap.Time("start", trip.StartTime),
					zap.Int("stops", len(trip.StopPoints)),
				)
			}
			r.log.Debug("trip already stored, skipping",
				zap.String("date", trip.Date),
				zap.Time("start", trip.StartTime))
			continue
		}

		if err := r.store.SaveTrip(ctx, trip); err != nil {
			return err
		}

		r.log.Info("trip stored",
			zap.String("date", trip.Date),
			zap.Float64("distance_km", trip.DistanceKm),
			zap.Float64("max_speed_kmh", trip.MaxSpeedKmh),
			zap.String("mode", string(trip.Mode)),
			zap.Int("stops", len(trip.StopPoints)),
		)
		for i, stop := range trip.StopPoints {
			r.log.Info("stop detected",
				zap.Int("index", i),
				zap.Float64("lat", stop.Lat),
				zap.Float64("lon", stop.Lon),
				zap.Int64("arrival_tst", stop.ArrivalTst),
				zap.Int64("departure_tst", stop.DepartureTst),
				zap.Int64("duration_s", stop.DepartureTst-stop.ArrivalTst),
				zap.String("confidence", string(stop.Confidence)),
				zap.String("evidence", stop.Evidence),
				zap.String("location", stop.Location),
			)
		}

		if trip.DistanceKm >= notifyThresholdKm && trip.Mode == trips.ModeCar {
			toNotify = append(toNotify, trip)
		}
	}

	if len(toNotify) > 0 {
		if err := r.tg.SendAll(ctx, toNotify); err != nil {
			r.log.Error("telegram notification failed", zap.Error(err))
		}
	}
	return nil
}
