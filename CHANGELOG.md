# Changelog

All notable changes to autolog are documented here.

## 2026-08-22

- Removed obsolete automatic-trip state and the no-op background runner loop.
- Removed the unused home-start grace setting and `last_processed_time` store state.
- Updated documentation to describe explicit `/api/trips/start` and `/api/trips/stop` control.
- Manual trips no longer discard short journeys or classify long GPS gaps as transit.
- MCP trip listings now include intermediate stop names.
- MCP trip details now resolve trips whose stored start timestamp includes seconds.

## 2026-08-20

- Aggregated explicit trip legs into one home-to-home journey while preserving intermediate stops.

## 2026-08-18

- Deferred explicit-trip GPS lookup until the journey is stopped.

## 2026-08-17

- Added read-only trip history over MCP HTTP.
- Added tolerance for delayed GPS fixes near home.

## 2026-08-16

- Changed live trip logging to explicit, event-driven home-to-home journeys.

## 2026-08-13 to 2026-08-14

- Added the phone-driven start/stop trip API.
- Improved trip-event outcome logging and raw payload diagnostics.
- Added active-trip cleanup when a stop completes a journey.
- Added submitted event timestamp logging.

## 2026-08-11

- Added trip inspection and optional historical reprocessing.

## 2026-07-25 to 2026-08-02

- Added dwell-cluster stop detection and stop timestamps.
- Added anomaly filtering, stay-based segmentation, segment-vote classification, and acceleration-based train gating.
- Added home-zone configuration, environment-based zones, and the trip replay CLI.
- Added transit-trip filtering based on GPS gap and displacement. This was later removed from the manual live-trip path.

## 2026-07-24

- Added batched notifications, intermediate stop detection, smarter trip stitching, and richer Telegram messages.
- Added persistent reverse-geocode caching and stored trip start/end locations.
- Added Telegram topic support and stdout notification mode.

## Initial implementation

- Added the Go module, OwnTracks Recorder client, trip segmentation and classification, SQLite storage, geocoding, Telegram notifications, Docker configuration, and the initial daemon entry point.
