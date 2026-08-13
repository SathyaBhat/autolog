# autolog

Automatically logs car trips from [OwnTracks Recorder](https://owntracks.org/booklet/guide/recorder/) and sends notifications via Telegram (or stdout). Trips are stored in a local SQLite database with reverse-geocoded start/end locations.

## How it works

On each scheduler tick (default: every 6 hours), autolog:

1. Fetches location points from OwnTracks Recorder for the period since the last run
2. Segments points into trips (a new trip begins after a >5 min gap)
3. Classifies each trip by mode (car or train) based on speed profile
4. Discards trips below the minimum distance or within configured exclusion zones
5. Reverse-geocodes the start and end coordinates via [Nominatim](https://nominatim.openstreetmap.org/) (cached in SQLite)
6. Stores new trips and notifies via Telegram or stdout

## Requirements

- An [OwnTracks Recorder](https://github.com/owntracks/recorder) instance with location history
- A Telegram bot (optional — stdout mode available for testing)

## Configuration

Configuration is read from `config.yaml`, environment variables, or a `.env` file. Environment variables take precedence.

Copy `config.yaml.example` to `config.yaml` and edit:

```yaml
owntracks:
  url: "https://your-otrecorder-host/otrecorder"
  user: "yourname"
  device: "yourphone"

telegram:
  bot_token: "123456:ABC-DEF..."
  chat_id: "123456789"

scheduler:
  interval: 6h
  manual_trip_interval: 1m

http:
  addr: ":8080"
  trip_event_token: "${TRIP_EVENT_TOKEN}"

filters:
  max_train_speed_kmh: 150   # speeds above this classify the trip as train
  min_distance_km: 5.0       # trips shorter than this are discarded
  explicit_min_distance_km: 3.0 # phone-triggered trips shorter than this are discarded
  max_acc_m: 100             # GPS points with accuracy worse than this are dropped
  exclusion_zones:
    - name: "home"
      lat: -33.7317
      lon: 150.9134
      radius_m: 300

store:
  path: "/data/autolog.db"

log:
  level: info
```

### Environment variables

| Variable | Description |
|---|---|
| `OWNTRACKS_URL` | OwnTracks Recorder base URL |
| `OWNTRACKS_USER` | OwnTracks username |
| `OWNTRACKS_DEVICE` | OwnTracks device name |
| `TELEGRAM_BOT_TOKEN` | Telegram bot token |
| `TELEGRAM_CHAT_ID` | Telegram chat ID to send messages to |
| `SCHEDULER_INTERVAL` | How often to check for new trips (default: `6h`) |
| `MANUAL_TRIP_INTERVAL` | How often to refresh an active phone-triggered trip (default: `1m`) |
| `FILTERS_EXPLICIT_MIN_DISTANCE_KM` | Minimum distance for phone-triggered trips (default: `3.0`) |
| `HTTP_ADDR` | Address for the phone trip-event API; empty disables it |
| `TRIP_EVENT_TOKEN` | Bearer token required by the phone trip-event API |
| `STORE_PATH` | Path to the SQLite database (default: `autolog.db`) |
| `NOTIFY_STDOUT` | Set to `true` to print notifications to stdout instead of Telegram |
| `LOG_LEVEL` | Log level: `debug`, `info`, `warn`, `error` (default: `info`) |

## Running

### Local

```bash
cp config.yaml.example config.yaml
# edit config.yaml

go run ./cmd/autolog
```

Test without Telegram:

```bash
NOTIFY_STDOUT=true go run ./cmd/autolog
```

### Docker / Dokploy

```bash
docker compose up -d
```

The compose file expects a `dokploy-network` external network and an `.env` file for configuration. The database is stored in a named volume `autolog-data`.

The included Traefik labels expose the API at:

```text
https://wraeclast.bishop-bass.ts.net/autolog/api/trips/start
https://wraeclast.bishop-bass.ts.net/autolog/api/trips/stop
```

Traefik strips `/autolog` before forwarding the request to the container.

### Generate the event token

Generate a high-entropy token locally and add it to `.env`:

```sh
openssl rand -hex 32
```

Store the resulting value as:

```dotenv
TRIP_EVENT_TOKEN=the-generated-value
```

Keep `.env` out of git and restrict its permissions:

```sh
chmod 600 .env
```

Do not put the token in Traefik labels or the Shortcut URL. The Shortcut should send it in the `Authorization: Bearer ...` header.

### Backfill historical data

To process historical location data from a specific date:

```bash
go run ./cmd/autolog -backfill -from 2026-01-01
# or via env:
BACKFILL=true BACKFILL_FROM=2026-01-01 go run ./cmd/autolog
```

### Inspect or reprocess one trip

Review a past trip using the current OwnTracks data and classifier without changing the database:

```bash
go run ./cmd/autolog -inspect-date 2026-08-10 -inspect-start 17:58
```

Add `-inspect-points` to print every replayed GPS point and its OwnTracks metadata.

Replace the matching stored trip's points, classification, and stops without sending a duplicate notification:

```bash
go run ./cmd/autolog -inspect-date 2026-08-10 -inspect-start 17:58 -reprocess
```

The time is interpreted in `Australia/Sydney`. The review prints the selected trip's mode, distance, tag counts, and each detected stop with duration, confidence, and evidence.

### Phone-triggered trips

With `HTTP_ADDR` and `TRIP_EVENT_TOKEN` configured, iOS Shortcuts can call:

```text
POST /autolog/api/trips/start
POST /api/trips/stop
Authorization: Bearer <TRIP_EVENT_TOKEN>
Content-Type: application/json
{"timestamp":"2026-08-13T00:50:00Z"}
```

The start call persists the active trip. While it is active, autolog refreshes the partial trip at `MANUAL_TRIP_INTERVAL` without notifying. The stop call fetches the complete OwnTracks window, stores the final result as a car trip, and bypasses the normal transit, exclusion-zone, and minimum-distance discard filters.

## Development

```bash
go test ./...
```
