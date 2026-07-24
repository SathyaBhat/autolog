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

filters:
  max_train_speed_kmh: 150   # speeds above this classify the trip as train
  min_distance_km: 5.0       # trips shorter than this are discarded
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

### Backfill historical data

To process historical location data from a specific date:

```bash
go run ./cmd/autolog -backfill -from 2026-01-01
# or via env:
BACKFILL=true BACKFILL_FROM=2026-01-01 go run ./cmd/autolog
```

## Development

```bash
go test ./...
```
