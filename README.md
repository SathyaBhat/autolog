# autolog

Records manually started and stopped car trips using location history from [OwnTracks Recorder](https://owntracks.org/booklet/guide/recorder/), then sends notifications via Telegram (or stdout). Trips are stored in a local SQLite database with reverse-geocoded locations.

## How it works

Autolog does not automatically detect live trips. The phone or shortcut controls the lifecycle:

1. A trip start event records the explicit start time immediately.
2. Stop and start events away from home are treated as intermediate journey events.
3. The journey remains active until a stop event arrives while the device is back in a home zone.
4. A stop event at home completes the journey, which is classified, stored, and notified.

Historical replay remains available through the backfill, inspection, and replay commands. Those commands segment location history heuristically; they are not used for live trip completion.

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

http:
  addr: ":8080"
  trip_event_token: "${TRIP_EVENT_TOKEN}"

filters:
  max_train_speed_kmh: 150   # speeds above this classify the trip as train
  max_acc_m: 100             # GPS points with accuracy worse than this are dropped
  # A manual stop inside one of these zones completes the journey.
  exclusion_zones:
    - name: "home"
      lat: 0.0000
      lon: 0.0000
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

To heuristically process historical location data from a specific date:

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
POST /autolog/api/trips/stop
Authorization: Bearer <TRIP_EVENT_TOKEN>
Content-Type: application/json
{"timestamp":"2026-08-13T00:50:00Z"}
```

The start call records the explicit start time without waiting for a GPS fix. Repeated starts and stops away from home are continuation events; each away-from-home stop is stored as an explicit stop and the response is `{"status":"ongoing"}`. The next start closes that stop's departure time. A stop at home fetches the complete OwnTracks window, stores the final result as a car trip with the explicit stops, and returns `{"status":"completed"}`. Authenticated event requests always return HTTP 200; processing failures are written to the application log. Trips are not created by the background daemon without these events.

### Query trips through MCP

Autolog includes a read-only MCP server for asking an AI client about trip history.
When `HTTP_ADDR` is configured, it is available at `/mcp` on the same HTTP server
as the phone event API. It reads the same SQLite database and exposes `list_trips`,
`trip_details`, and `trip_days` tools. The existing `TRIP_EVENT_TOKEN` is required
as `Authorization: Bearer ...`.

Build it once:

```bash
go build -o bin/autolog-mcp ./cmd/autolog-mcp
MCP_TOKEN=the-generated-value ./bin/autolog-mcp -db /path/to/autolog.db -addr :8081
```

Register it with an MCP client using a configuration like:

```json
{
  "mcpServers": {
    "autolog": {
      "url": "https://your-autolog-host/autolog/mcp",
      "headers": {
        "Authorization": "Bearer ${TRIP_EVENT_TOKEN}"
      }
    }
  }
}
```

The `trip_days` tool is intended for questions such as "where did we take trips
between Monday and Friday?"; `list_trips` handles more detailed filtering by date,
location, and transport mode.

## Development

```bash
go test ./...
```
