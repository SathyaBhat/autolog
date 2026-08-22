package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sathyabhat/autolog/internal/api"
	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/mcpserver"
	"github.com/sathyabhat/autolog/internal/notify"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/runner"
	"github.com/sathyabhat/autolog/internal/store"
	"github.com/sathyabhat/autolog/internal/trips"
)

func main() {
	cfgFile := flag.String("config", "", "path to config.yaml (default: ./config.yaml)")
	backfill := flag.Bool("backfill", false, "run a historical backfill and exit")
	backfillFrom := flag.String("from", "", "start date for backfill, YYYY-MM-DD (required with -backfill)")
	inspectDate := flag.String("inspect-date", "", "inspect a trip on local date YYYY-MM-DD")
	inspectStart := flag.String("inspect-start", "", "trip start time HH:MM, used with -inspect-date")
	inspectPoints := flag.Bool("inspect-points", false, "print every point during trip inspection")
	reprocess := flag.Bool("reprocess", false, "replace the inspected trip in SQLite without notifying")
	flag.Parse()

	// Load .env if present; silently ignored when not found (e.g. in Docker where
	// env vars are injected directly).
	_ = godotenv.Load()

	tmpLog, _ := zap.NewDevelopment()

	cfg, err := config.Load(*cfgFile)
	if err != nil {
		tmpLog.Fatal("failed to load config", zap.Error(err))
	}

	log := buildLogger(cfg.Log.Level)
	defer log.Sync() //nolint:errcheck

	log.Info("autolog starting",
		zap.String("owntracks_url", cfg.OwnTracks.URL),
		zap.String("owntracks_device", cfg.OwnTracks.Device),
		zap.String("store_path", cfg.Store.Path),
	)

	st, err := store.New(cfg.Store.Path)
	if err != nil {
		log.Fatal("failed to open store", zap.Error(err))
	}
	defer st.Close()

	ot := owntracks.New(cfg.OwnTracks.URL, cfg.OwnTracks.User, cfg.OwnTracks.Device)

	geo := geocode.New().WithStore(st)
	log.Info("geocoding: nominatim")

	var notifier runner.Notifier
	if os.Getenv("NOTIFY_STDOUT") == "true" {
		notifier = notify.NewStdout(geo)
		log.Info("notifications: stdout")
	} else {
		if cfg.Telegram.BotToken == "" || cfg.Telegram.ChatID == "" {
			log.Fatal("telegram.bot_token and telegram.chat_id are required when NOTIFY_STDOUT is not set")
		}
		notifier = notify.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID, cfg.Telegram.MessageThreadID, geo)
		log.Info("notifications: telegram")
	}
	r := runner.New(cfg, ot, st, notifier, geo, log)

	var eventServer *http.Server
	if cfg.HTTP.Addr != "" {
		if cfg.HTTP.TripEventToken == "" {
			log.Fatal("TRIP_EVENT_TOKEN is required when HTTP_ADDR is set")
		}
		mcpHandler, err := mcpserver.HTTPHandler(st, "Australia/Sydney", cfg.HTTP.TripEventToken)
		if err != nil {
			log.Fatal("failed to configure MCP HTTP handler", zap.Error(err))
		}
		httpMux := http.NewServeMux()
		httpMux.Handle("/mcp", mcpHandler)
		httpMux.Handle("/mcp/", mcpHandler)
		httpMux.Handle("/api/trips/", api.NewTripEvents(r, cfg.HTTP.TripEventToken, log).Handler())
		eventServer = &http.Server{
			Addr:              cfg.HTTP.Addr,
			Handler:           httpMux,
			ReadHeaderTimeout: 5 * time.Second,
		}
		go func() {
			log.Info("trip event server listening", zap.String("addr", cfg.HTTP.Addr))
			if err := eventServer.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Error("trip event server failed", zap.Error(err))
			}
		}()
		defer func() {
			shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = eventServer.Shutdown(shutdownCtx)
		}()
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if *inspectDate != "" || *inspectStart != "" || *reprocess {
		if *inspectDate == "" || *inspectStart == "" {
			log.Fatal("-inspect-date and -inspect-start are required for trip inspection/reprocessing")
		}
		target, err := parseReviewTarget(*inspectDate, *inspectStart)
		if err != nil {
			log.Fatal("invalid inspection target", zap.Error(err))
		}
		trip, err := r.ReviewTrip(ctx, target, *reprocess)
		if err != nil {
			log.Fatal("trip review failed", zap.Error(err))
		}
		printTripReview(trip, target, *reprocess, *inspectPoints)
		return
	}

	if !*backfill && os.Getenv("BACKFILL") == "true" {
		*backfill = true
	}
	if *backfillFrom == "" {
		*backfillFrom = os.Getenv("BACKFILL_FROM")
	}

	if *backfill {
		if *backfillFrom == "" {
			log.Fatal("-from / BACKFILL_FROM is required with -backfill / BACKFILL=true (e.g. 2025-01-01)")
		}
		from, err := time.Parse("2006-01-02", *backfillFrom)
		if err != nil {
			log.Fatal("invalid -from date, expected YYYY-MM-DD", zap.Error(err))
		}
		to := time.Now().UTC()
		log.Info("starting backfill", zap.Time("from", from), zap.Time("to", to))
		if err := r.Backfill(ctx, from, to); err != nil && !errors.Is(err, context.Canceled) {
			log.Fatal("backfill failed", zap.Error(err))
		}
		log.Info("backfill complete")
		return
	}

	<-ctx.Done()
	log.Info("autolog stopped cleanly")
}

func parseReviewTarget(date, start string) (time.Time, error) {
	loc, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		return time.Time{}, err
	}
	return time.ParseInLocation("2006-01-02 15:04", date+" "+start, loc)
}

func printTripReview(trip trips.Trip, target time.Time, reprocessed, showPoints bool) {
	tagged, untagged := 0, 0
	for _, point := range trip.Points {
		if point.Tag == "drive" {
			tagged++
		} else {
			untagged++
		}
	}

	action := "inspection only"
	if reprocessed {
		action = "reprocessed into SQLite"
	}
	fmt.Printf("Trip %s (%s)\n", target.Format("2006-01-02 15:04 MST"), action)
	fmt.Printf("  start: %s\n", trip.StartTime.In(target.Location()).Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  end:   %s\n", trip.EndTime.In(target.Location()).Format("2006-01-02 15:04:05 MST"))
	fmt.Printf("  route: %s -> %s\n", trip.StartLocation, trip.EndLocation)
	fmt.Printf("  mode: %s, distance: %.2f km, max speed: %.1f km/h\n", trip.Mode, trip.DistanceKm, trip.MaxSpeedKmh)
	fmt.Printf("  points: %d (%d tagged drive, %d untagged)\n", len(trip.Points), tagged, untagged)
	if showPoints {
		for i, point := range trip.Points {
			fmt.Printf("    point %d: %s %.6f,%.6f tag=%q vel=%.1f acc=%.1f cog=%.1f activities=%v\n",
				i+1,
				time.Unix(point.Tst, 0).In(target.Location()).Format("15:04:05"),
				point.Lat,
				point.Lon,
				point.Tag,
				point.Vel,
				point.Acc,
				point.Cog,
				point.MotionActivities,
			)
		}
	}
	fmt.Printf("  stops: %d\n", len(trip.StopPoints))
	for i, stop := range trip.StopPoints {
		arrival := time.Unix(stop.ArrivalTst, 0).In(target.Location())
		departure := time.Unix(stop.DepartureTst, 0).In(target.Location())
		fmt.Printf("    %d. %s -> %s (%.0f min, %s, %s)\n",
			i+1,
			arrival.Format("15:04"),
			departure.Format("15:04"),
			float64(stop.DepartureTst-stop.ArrivalTst)/60,
			stop.Confidence,
			stop.Evidence,
		)
	}
}

func buildLogger(level string) *zap.Logger {
	var zapLevel zapcore.Level
	if err := zapLevel.UnmarshalText([]byte(level)); err != nil {
		zapLevel = zapcore.InfoLevel
	}

	encCfg := zap.NewProductionEncoderConfig()
	encCfg.EncodeTime = zapcore.ISO8601TimeEncoder

	logCfg := zap.Config{
		Level:            zap.NewAtomicLevelAt(zapLevel),
		Development:      false,
		Encoding:         "json",
		EncoderConfig:    encCfg,
		OutputPaths:      []string{"stdout"},
		ErrorOutputPaths: []string{"stderr"},
	}

	log, err := logCfg.Build()
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to build logger: %v\n", err)
		log, _ = zap.NewProduction()
	}
	return log
}
