# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Commands

```bash
# Run all tests
go test ./...

# Run tests for a single package
go test ./internal/trips/...

# Run a single test
go test ./internal/trips/... -run TestClassify_Car -v

# Run the daemon locally (stdout mode, no Telegram required)
NOTIFY_STDOUT=true go run ./cmd/autolog

# Backfill historical data
go run ./cmd/autolog -backfill -from 2026-01-01

# Inspect/reprocess one trip without duplicate notification
go run ./cmd/autolog -inspect-date 2026-08-10 -inspect-start 17:58
go run ./cmd/autolog -inspect-date 2026-08-10 -inspect-start 17:58 -reprocess

# Replay stored trips through algorithm variants (once cmd/replay exists)
go run ./cmd/replay --db autolog.db --days 30

# Build
go build ./...
```

## Architecture

autolog is a single-binary Go daemon. On each scheduler tick it runs a linear pipeline: **fetch → filter → segment → classify → geocode → store → notify**.

### Pipeline flow (`internal/runner/runner.go`)

`ProcessOnce()` is the core: it calls `owntracks.Fetch()`, passes the point slice to `trips.Segment()` (or `trips.SegmentWithStays()` when the flag is set), then calls `trips.Classify()` for each raw trip. Trips that pass are reverse-geocoded, deduplicated against the DB via `TripExists`, saved, and queued for Telegram notification. The runner also exports `Backfill()` which chunks a date range into monthly `ProcessOnce` calls.

### Trip detection (`internal/trips/`)

- **`segmenter.go`** — splits a sorted point slice into `RawTrip`s. Default: cuts on time gaps > `max_trip_gap` (default 90 min). Alternative path `SegmentWithStays()` detects stay clusters first and uses them as boundaries instead.
- **`staypoint.go`** — sliding-window stay-point algorithm: points within `stay_radius_m` of a running centroid for ≥ `stay_min_dur` form a `Stay`. Adjacent stays at the same location are merged.
- **`classifier.go`** — takes a `RawTrip`, drops inaccurate points (`max_acc_m`), checks exclusion zones, computes Haversine distance and speed. Train detection: max speed ≥ threshold OR sustained avg >130 km/h in any 10-min window. Optional flags: `SegmentVote` (split at >50% relative speed change, classify each sub-segment, vote by time) and `AccelTrainGate` (require avg acceleration <0.2 m/s² and max/avg speed <1.3 to confirm train).
- **`filter.go`** — `FilterAnomalousPoints()` drops GPS fixes where implied speed on both adjacent segments exceeds `anomaly_max_kmh`.
- **`geo.go`** — Haversine formula and exclusion-zone check.
- **`types.go`** — `TransportMode` constants, `RawTrip`, `Trip`, `StopPoint`, `Stay`, `AlgorithmFlags`, `ClassifierConfig`.

### Algorithm flags (`AlgorithmFlags`)

All flags default to `false` (baseline behaviour). Set in config under `filters.algorithm_flags`:

| Flag | Effect |
|---|---|
| `anomaly_filter` | Drop implausible GPS jumps before any computation |
| `stay_segment` | Use stay-point boundaries instead of raw time gap |
| `segment_vote` | Classify trip mode by dominant segment, not max speed |
| `accel_train_gate` | Require smooth speed profile to confirm train mode |

### Storage (`internal/store/`)

SQLite via `modernc.org/sqlite` (CGO-free). Schema: `trips`, `trip_points` (linked by `trip_id`), `state` (key-value, stores `last_processed_time`), `geocode_cache`. Schema migrations are applied as `ALTER TABLE` statements at startup — errors are swallowed because SQLite returns an error when a column already exists.

### Geocoding (`internal/geocode/`)

Nominatim reverse-geocode with a SQLite cache (rounded to 4 decimal places). `geocode.Client.WithStore()` wraps it with the cache layer.

### Notification (`internal/notify/`)

Two implementations: `Telegram` (sends batched messages) and `Stdout` (for development). Both implement the `runner.Notifier` interface (`SendAll`). `format.go` owns the Telegram message template.

### Configuration

Viper reads `config.yaml` (or the path from `-config`), then environment variables (`.` replaced with `_`). `.env` is auto-loaded if present. Required fields: `owntracks.url`, `owntracks.user`, `owntracks.device`. Telegram credentials are required unless `NOTIFY_STDOUT=true`.
