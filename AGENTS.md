# Repository Guidelines

## Project Structure

This is a Go service that records explicitly started and stopped trips using OwnTracks location history, classifies the completed journey, stores results in SQLite, and sends notifications. Live trips are controlled by the phone event API; the daemon does not automatically create trips. Application entry points live in `cmd/`:

- `cmd/autolog` runs the event/API daemon and backfill/review commands.
- `cmd/autolog-mcp` exposes read-only trip queries over MCP HTTP.
- `cmd/replay` replays stored trips for algorithm inspection.

Core packages are under `internal/`: `runner` coordinates processing, `trips` segments and classifies GPS data, `store` manages SQLite, `geocode` handles reverse geocoding, and `notify` formats notifications. Tests sit beside their implementation files. Configuration examples are in `config.yaml.example`, `.env.example`, and `docker-compose.yml`.

## Build, Test, and Development Commands

```bash
go test ./...                 # Run the full test suite
go test ./internal/trips/...  # Test one package
go build ./...                # Build all packages
go run ./cmd/autolog          # Run the event/API daemon locally
NOTIFY_STDOUT=true go run ./cmd/autolog  # Run without Telegram delivery
docker compose up -d          # Run the containerized service
```

For historical data, use `go run ./cmd/autolog -backfill -from YYYY-MM-DD`. Review a stored trip with `-inspect-date` and `-inspect-start`; add `-reprocess` only when database updates are intended. Use `go run ./cmd/replay --db autolog.db --days 30` to compare classifier variants.

The live lifecycle is:

1. `POST /api/trips/start` opens a journey.
2. Away-from-home stop/start events record intermediate stops.
3. `POST /api/trips/stop` at home fetches OwnTracks points, classifies and stores the completed car trip, and sends its notification.

Historical backfill, inspection, and replay still use heuristic segmentation. They are separate from live trip completion.

## MCP Interface

The read-only MCP interface is available at `/mcp` when `HTTP_ADDR` is configured on `cmd/autolog`. It uses `TRIP_EVENT_TOKEN` as the bearer token. The standalone `cmd/autolog-mcp` binary is also available for serving a database directly:

```bash
go build -o bin/autolog-mcp ./cmd/autolog-mcp
MCP_TOKEN=... ./bin/autolog-mcp -db /path/to/autolog.db -addr :8081
```

The MCP tools are:

- `list_trips` lists trips with filters for local date, location, and transport mode. Results include `stop_count` and named `intermediate_stops`.
- `trip_details` returns one trip's full stop records and optional GPS points. Use the local trip date and displayed start time in `HH:MM`.
- `trip_days` groups trips by local day and summarizes destinations and endpoint routes.

MCP dates and times are returned in `Australia/Sydney`. The MCP server reads the same SQLite database as the event daemon.

## Coding Style and Naming

Use standard Go formatting (`gofmt`) and idiomatic Go names: mixedCaps for exported identifiers, short lower-case names for locals, and descriptive package names. Keep business logic in the appropriate `internal/` package and add focused helpers rather than duplicating classification or time handling. Do not reintroduce a background trip detector or scheduler without an explicit product decision.

## Testing Guidelines

Tests use Go’s standard testing package with `testify` assertions. Name tests `Test<Subject>_<Scenario>` and keep fixtures deterministic with explicit UTC timestamps. Run `go test ./...` before submitting changes; add regression coverage for classifier, storage, API, or notification behavior you modify.

## Commits and Pull Requests

Recent commits use concise imperative or present-tense subjects, often with `fix:` and `feat:` prefixes (for example, `fix: aggregate explicit trip legs`). Keep commits focused. Pull requests should explain behavior changes, list validation commands, call out configuration or migration impacts, and include request/response examples when changing the HTTP or MCP interfaces.

## Security and Time Handling

Do not commit `.env`, tokens, credentials, or production databases. The server’s system time is UTC. Convert times in responses to queries to `Australia/Sydney`, including daylight-saving adjustments. Treat `AGENTS.md` as the authoritative repository guidance; keep other agent-specific files pointed at it rather than duplicating these rules.
