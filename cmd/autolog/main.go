package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/joho/godotenv"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/notify"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/runner"
	"github.com/sathyabhat/autolog/internal/store"
)

func main() {
	cfgFile := flag.String("config", "", "path to config.yaml (default: ./config.yaml)")
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
		zap.Duration("scheduler_interval", cfg.Scheduler.Interval),
		zap.String("store_path", cfg.Store.Path),
	)

	st, err := store.New(cfg.Store.Path)
	if err != nil {
		log.Fatal("failed to open store", zap.Error(err))
	}
	defer st.Close()

	ot := owntracks.New(cfg.OwnTracks.URL, cfg.OwnTracks.User, cfg.OwnTracks.Device)

	var notifier runner.Notifier
	if os.Getenv("NOTIFY_STDOUT") == "true" {
		notifier = &notify.Stdout{}
		log.Info("notifications: stdout")
	} else {
		notifier = notify.NewTelegram(cfg.Telegram.BotToken, cfg.Telegram.ChatID)
		log.Info("notifications: telegram")
	}
	r := runner.New(cfg, ot, st, notifier, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	if err := r.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		log.Fatal("runner exited with error", zap.Error(err))
	}
	log.Info("autolog stopped cleanly")
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
