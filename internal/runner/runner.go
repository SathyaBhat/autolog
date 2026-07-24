package runner

import (
	"context"
	"time"

	"go.uber.org/zap"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

const notifyThresholdKm = 100.0
const firstRunLookback = 24 * time.Hour

// locationFetcher is the subset of owntracks.Client used by Runner.
type locationFetcher interface {
	Fetch(ctx context.Context, from, to time.Time) ([]owntracks.Point, error)
}

// Notifier sends trip notifications. *notify.Telegram and *notify.Stdout both satisfy this.
type Notifier interface {
	Send(ctx context.Context, t trips.Trip) error
}

// geocoder resolves coordinates to a human-readable label.
type geocoder interface {
	Reverse(ctx context.Context, lat, lon float64) (geocode.Location, error)
}

// Runner orchestrates the fetch → segment → classify → store → notify pipeline.
type Runner struct {
	cfg   *config.Config
	ot    locationFetcher
	store *store.Store
	tg    Notifier
	geo   geocoder
	log   *zap.Logger
}

// New creates a Runner with concrete dependencies.
func New(cfg *config.Config, ot *owntracks.Client, st *store.Store, tg Notifier, geo *geocode.Client, log *zap.Logger) *Runner {
	return NewWithDeps(cfg, ot, st, tg, geo, log)
}

// NewWithDeps creates a Runner with interface dependencies (for testing).
func NewWithDeps(cfg *config.Config, ot locationFetcher, st *store.Store, tg Notifier, geo geocoder, log *zap.Logger) *Runner {
	return &Runner{cfg: cfg, ot: ot, store: st, tg: tg, geo: geo, log: log}
}

// Run blocks, processing on each ticker tick, until ctx is cancelled.
func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.cfg.Scheduler.Interval)
	defer ticker.Stop()

	r.tick(ctx)

	for {
		select {
		case <-ctx.Done():
			r.log.Info("runner shutting down")
			return ctx.Err()
		case <-ticker.C:
			r.tick(ctx)
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

	if err := r.ProcessOnce(ctx, from, to); err != nil {
		r.log.Error("process window failed", zap.Error(err))
		return
	}

	if err := r.store.SetLastProcessedTime(ctx, to); err != nil {
		r.log.Error("failed to update last processed time", zap.Error(err))
	}
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

	rawTrips := trips.Segment(points)
	r.log.Info("segmented trips", zap.Int("points", len(points)), zap.Int("trips", len(rawTrips)))

	classCfg := trips.ClassifierConfig{
		MaxTrainSpeedKmh: r.cfg.Filters.MaxTrainSpeedKmh,
		MinDistanceKm:    r.cfg.Filters.MinDistanceKm,
		MaxAccM:          r.cfg.Filters.MaxAccM,
		ExclusionZones:   r.cfg.Filters.ExclusionZones,
	}

	for _, raw := range rawTrips {
		trip, keep := trips.Classify(raw, classCfg)
		if !keep {
			r.log.Debug("trip discarded by exclusion zone",
				zap.Float64("start_lat", raw.Points[0].Lat),
				zap.Float64("start_lon", raw.Points[0].Lon))
			continue
		}

		if r.geo != nil {
			if startLoc, err := r.geo.Reverse(ctx, trip.StartLat, trip.StartLon); err == nil {
				trip.StartLocation = startLoc.Label
			} else {
				r.log.Warn("geocode start failed", zap.Error(err))
			}
			if endLoc, err := r.geo.Reverse(ctx, trip.EndLat, trip.EndLon); err == nil {
				trip.EndLocation = endLoc.Label
			} else {
				r.log.Warn("geocode end failed", zap.Error(err))
			}
		}

		exists, err := r.store.TripExists(ctx, trip.Date, trip.StartTime)
		if err != nil {
			return err
		}
		if exists {
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
		)

		if trip.DistanceKm >= notifyThresholdKm && trip.Mode == trips.ModeCar {
			if err := r.tg.Send(ctx, trip); err != nil {
				r.log.Error("telegram notification failed", zap.Error(err))
			}
		}
	}
	return nil
}
