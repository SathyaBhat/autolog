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

// StartExplicitTrip starts a home-to-home journey. Repeated starts while a
// journey is active are treated as continuation events.
func (r *Runner) StartExplicitTrip(ctx context.Context, start time.Time) error {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()

	active, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		return err
	}
	if !active.IsZero() {
		stops, err := r.store.GetActiveManualStops(ctx)
		if err != nil {
			return err
		}
		if len(stops) > 0 {
			last := &stops[len(stops)-1]
			if last.DepartureTst == 0 && start.Unix() > last.ArrivalTst {
				last.DepartureTst = start.Unix()
				if err := r.store.SetActiveManualStops(ctx, stops); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if len(r.cfg.Filters.HomeZones) == 0 {
		return fmt.Errorf("cannot start explicit trip: no home zones configured")
	}
	return r.store.SetActiveManualTripStart(ctx, start)
}

// StopExplicitTrip treats stops away from home as intermediate stops. The
// journey is committed only when the endpoint is back inside a home zone.
func (r *Runner) StopExplicitTrip(ctx context.Context, end time.Time) (trips.Trip, bool, error) {
	r.manualMu.Lock()
	defer r.manualMu.Unlock()

	start, err := r.store.GetActiveManualTripStart(ctx)
	if err != nil {
		return trips.Trip{}, false, err
	}
	if start.IsZero() {
		return trips.Trip{}, false, fmt.Errorf("no explicit trip is active")
	}
	if !end.After(start) {
		return trips.Trip{}, false, nil
	}

	points, err := r.fetchJourneyPoints(ctx, start, end)
	if err != nil {
		return trips.Trip{}, false, err
	}
	endpoint, ok := nearestPoint(points, end)
	if !ok {
		return trips.Trip{}, false, nil
	}
	if !trips.InHomeZone(endpoint.Lat, endpoint.Lon, r.cfg.Filters.HomeZones) {
		stops, err := r.store.GetActiveManualStops(ctx)
		if err != nil {
			return trips.Trip{}, false, err
		}
		if len(stops) == 0 || stops[len(stops)-1].ArrivalTst != end.Unix() {
			stops = append(stops, trips.StopPoint{
				Lat:        endpoint.Lat,
				Lon:        endpoint.Lon,
				ArrivalTst: end.Unix(),
			})
			if err := r.store.SetActiveManualStops(ctx, stops); err != nil {
				return trips.Trip{}, false, err
			}
		}
		return trips.Trip{}, false, nil
	}
	stops, err := r.store.GetActiveManualStops(ctx)
	if err != nil {
		return trips.Trip{}, false, err
	}
	trip, ok, err := r.classifyExplicitJourney(points, start.Unix(), end.Unix(), stops)
	if err != nil {
		return trips.Trip{}, false, err
	}
	if !ok {
		return trips.Trip{}, false, fmt.Errorf("fewer than two accurate OwnTracks points found between journey start and return home")
	}
	trip.StartTime = start.UTC()
	trip.Date = start.UTC().Format("2006-01-02")
	trip.StopPoints = append(trip.StopPoints, stops...)
	sort.SliceStable(trip.StopPoints, func(i, j int) bool {
		return trip.StopPoints[i].ArrivalTst < trip.StopPoints[j].ArrivalTst
	})
	r.annotateTrip(ctx, &trip)

	if err := r.saveExplicitTrip(ctx, trip, true); err != nil {
		return trips.Trip{}, false, err
	}
	if err := r.store.ClearActiveManualTripStart(ctx); err != nil {
		return trips.Trip{}, false, err
	}
	if err := r.store.ClearActiveManualStops(ctx); err != nil {
		return trips.Trip{}, false, err
	}
	return trip, true, nil
}

func (r *Runner) fetchJourneyPoints(ctx context.Context, start, end time.Time) ([]owntracks.Point, error) {
	from, to := start, end
	if to.Before(from) {
		from, to = to, from
	}
	points, err := r.ot.Fetch(ctx, from.UTC(), to.Add(5*time.Minute).UTC())
	if err != nil {
		return nil, err
	}
	filtered := make([]owntracks.Point, 0, len(points))
	for _, p := range points {
		if p.Tst >= start.Unix() && (end.Before(start) || p.Tst <= end.Unix()) {
			filtered = append(filtered, p)
		}
	}
	sort.Slice(filtered, func(i, j int) bool { return filtered[i].Tst < filtered[j].Tst })
	return filtered, nil
}

func nearestPoint(points []owntracks.Point, target time.Time) (owntracks.Point, bool) {
	if len(points) == 0 {
		return owntracks.Point{}, false
	}
	best := points[0]
	bestDelta := absDuration(time.Unix(best.Tst, 0).Sub(target))
	for _, p := range points[1:] {
		if delta := absDuration(time.Unix(p.Tst, 0).Sub(target)); delta < bestDelta {
			best, bestDelta = p, delta
		}
	}
	return best, true
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}

func (r *Runner) classifyExplicitJourney(points []owntracks.Point, startTst, endTst int64, stops []trips.StopPoint) (trips.Trip, bool, error) {
	cfg := r.classifierConfig()
	cfg.ExplicitDrive = true
	// Each leg is measured independently, so a short leg should not be
	// rejected just because the complete journey normally has a minimum.
	cfg.MinDistanceKm = 0.001
	cfg.ExplicitMinDistanceKm = 0.001

	var legs []trips.Trip
	legStart := startTst
	for _, stop := range stops {
		if stop.DepartureTst <= stop.ArrivalTst {
			continue
		}
		leg, ok := classifyExplicitLeg(points, legStart, stop.ArrivalTst, cfg)
		if !ok {
			return trips.Trip{}, false, nil
		}
		legs = append(legs, leg)
		legStart = stop.DepartureTst
	}
	leg, ok := classifyExplicitLeg(points, legStart, endTst, cfg)
	if !ok {
		return trips.Trip{}, false, nil
	}
	legs = append(legs, leg)

	first := legs[0]
	last := legs[len(legs)-1]
	combined := trips.Trip{
		StartLat:    first.StartLat,
		StartLon:    first.StartLon,
		EndLat:      last.EndLat,
		EndLon:      last.EndLon,
		EndTime:     last.EndTime,
		DistanceKm:  0,
		MaxSpeedKmh: 0,
		Mode:        trips.ModeCar,
	}
	for _, leg := range legs {
		combined.DistanceKm += leg.DistanceKm
		if leg.MaxSpeedKmh > combined.MaxSpeedKmh {
			combined.MaxSpeedKmh = leg.MaxSpeedKmh
		}
		combined.Points = append(combined.Points, leg.Points...)
	}
	return combined, true, nil
}

func classifyExplicitLeg(points []owntracks.Point, startTst, endTst int64, cfg trips.ClassifierConfig) (trips.Trip, bool) {
	legPoints := make([]owntracks.Point, 0)
	for _, point := range points {
		if point.Tst >= startTst && point.Tst <= endTst {
			legPoints = append(legPoints, point)
		}
	}
	if len(legPoints) < 2 {
		return trips.Trip{}, false
	}
	leg, _, keep := trips.Classify(trips.RawTrip{Points: legPoints}, cfg)
	return leg, keep
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

// Run blocks until shutdown. Trip completion is event-driven through the
// explicit start/stop API; no background job creates or completes trips.
func (r *Runner) Run(ctx context.Context) error {
	<-ctx.Done()
	r.log.Info("runner shutting down")
	return ctx.Err()
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
