# Geocode Cache and Trip Locations Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Persist reverse-geocode results to SQLite and store start/end location labels on every trip row.

**Architecture:** A new `geocode_cache` table stores rounded lat/lon → label, making `geocode.Client` check SQLite before hitting Nominatim. The `trips` table gains `start_location` and `end_location` TEXT columns (nullable, added via `ALTER TABLE` migration). The runner resolves locations after classify and sets them on `trips.Trip` before saving. Notifiers drop their own geocode calls and use the pre-resolved labels instead.

**Tech Stack:** Go, SQLite (`modernc.org/sqlite`), Nominatim REST API, `go.uber.org/zap`, `github.com/stretchr/testify`

## Global Constraints

- Go module path: `github.com/sathyabhat/autolog`
- SQLite driver: `modernc.org/sqlite` (pure-Go, no CGo)
- No new dependencies — use only packages already in `go.mod`
- All tests use `:memory:` SQLite; no file I/O in tests
- `geocode.Client.Reverse` signature must not change (callers outside notifiers may exist)
- Run `go test ./...` after every task; all packages must pass

---

## File Map

| File | Change |
|---|---|
| `internal/store/store.go` | Add `geocode_cache` table, migration for `trips` columns, `GetGeocode`/`SaveGeocode`/`GetTripByID` methods |
| `internal/store/store_test.go` | Tests for geocode cache and location columns |
| `internal/geocode/nominatim.go` | Add `Geocoder` interface; `Client` gains optional persistent cache via `StoreCache`; in-memory cache stays |
| `internal/trips/types.go` | Add `StartLocation`, `EndLocation string` fields to `Trip` |
| `internal/runner/runner.go` | Geocode start/end after classify, set on trip before save |
| `internal/notify/telegram.go` | Use `trip.StartLocation`/`trip.EndLocation` instead of calling `geo.Reverse` |
| `internal/notify/stdout.go` | Same as telegram.go |

---

### Task 1: Add `geocode_cache` table and geocode persistence methods to Store

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Produces:
  - `func (s *Store) GetGeocode(ctx context.Context, lat, lon float64) (string, bool, error)`
  - `func (s *Store) SaveGeocode(ctx context.Context, lat, lon float64, label string) error`
  - Cache key uses `math.Round(v*1e4)/1e4` (4 decimal places, matching `geocode.round`)

- [ ] **Step 1: Write failing tests**

Add to `internal/store/store_test.go`:

```go
func TestGeocodeCache_MissAndHit(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Miss
	label, ok, err := s.GetGeocode(ctx, 51.5074, -0.1278)
	require.NoError(t, err)
	assert.False(t, ok)
	assert.Empty(t, label)

	// Save
	require.NoError(t, s.SaveGeocode(ctx, 51.5074, -0.1278, "Westminster, London"))

	// Hit
	label, ok, err = s.GetGeocode(ctx, 51.5074, -0.1278)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Westminster, London", label)
}

func TestGeocodeCache_RoundingCollision(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// Two coords that round to the same 4dp key should share the cache entry
	require.NoError(t, s.SaveGeocode(ctx, 51.50741, -0.12781, "Near Westminster"))

	label, ok, err := s.GetGeocode(ctx, 51.50749, -0.12789)
	require.NoError(t, err)
	assert.True(t, ok)
	assert.Equal(t, "Near Westminster", label)
}
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/store/... -run TestGeocodeCache -v
```

Expected: compile error — `GetGeocode` and `SaveGeocode` undefined.

- [ ] **Step 3: Add `geocode_cache` table to schema and implement methods**

In `internal/store/store.go`, add to the `schema` const (append before the final backtick):

```go
CREATE TABLE IF NOT EXISTS geocode_cache (
    lat   REAL NOT NULL,
    lon   REAL NOT NULL,
    label TEXT NOT NULL,
    PRIMARY KEY (lat, lon)
);
```

Add import `"math"` to the import block, then add these methods:

```go
func geoRound(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

// GetGeocode looks up a cached reverse-geocode label for lat/lon.
// Returns (label, true, nil) on hit, ("", false, nil) on miss.
func (s *Store) GetGeocode(ctx context.Context, lat, lon float64) (string, bool, error) {
	var label string
	err := s.db.QueryRowContext(ctx,
		`SELECT label FROM geocode_cache WHERE lat = ? AND lon = ?`,
		geoRound(lat), geoRound(lon),
	).Scan(&label)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return label, true, nil
}

// SaveGeocode upserts a reverse-geocode label for lat/lon.
func (s *Store) SaveGeocode(ctx context.Context, lat, lon float64, label string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT OR REPLACE INTO geocode_cache (lat, lon, label) VALUES (?, ?, ?)`,
		geoRound(lat), geoRound(lon), label,
	)
	return err
}
```

- [ ] **Step 4: Run tests**

```bash
go test ./internal/store/... -v
```

Expected: all pass including `TestGeocodeCache_MissAndHit` and `TestGeocodeCache_RoundingCollision`.

- [ ] **Step 5: Commit**

```bash
git add internal/store/store.go internal/store/store_test.go
git commit -m "feat: add geocode_cache table and GetGeocode/SaveGeocode to store"
```

---

### Task 2: Migrate `trips` table — add `start_location` / `end_location` columns

**Files:**
- Modify: `internal/store/store.go`
- Modify: `internal/store/store_test.go`

**Interfaces:**
- Consumes: existing `Store.SaveTrip`, `Store.TripExists`
- Produces: `SaveTrip` now persists `trip.StartLocation` and `trip.EndLocation`; existing rows have NULL for those columns (nullable TEXT)

**Note:** The `trips` table already exists in production databases. We must use `ALTER TABLE … ADD COLUMN` for the migration rather than changing `CREATE TABLE IF NOT EXISTS`, which is a no-op on existing tables.

- [ ] **Step 1: Write failing test**

Add to `internal/store/store_test.go`:

```go
func TestSaveTrip_LocationColumns(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 24, 8, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date:          "2026-07-24",
		StartTime:     start,
		EndTime:       start.Add(time.Hour),
		StartLat:      51.5, StartLon: -0.1,
		EndLat:        51.8, EndLon: -0.3,
		DistanceKm:    45, MaxSpeedKmh: 90, Mode: trips.ModeCar,
		StartLocation: "Oxford Street, Westminster",
		EndLocation:   "High Street, Camden",
	}
	require.NoError(t, s.SaveTrip(ctx, trip))

	// Read back via raw SQL to verify columns are persisted
	var sl, el string
	err := s.DB().QueryRowContext(ctx,
		`SELECT start_location, end_location FROM trips WHERE date = ? AND start_time = ?`,
		"2026-07-24", start.Unix(),
	).Scan(&sl, &el)
	require.NoError(t, err)
	assert.Equal(t, "Oxford Street, Westminster", sl)
	assert.Equal(t, "High Street, Camden", el)
}

func TestSaveTrip_LocationColumns_EmptyOK(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	start := time.Date(2026, 7, 24, 9, 0, 0, 0, time.UTC)
	trip := trips.Trip{
		Date: "2026-07-24", StartTime: start, EndTime: start.Add(time.Hour),
		StartLat: 51.5, StartLon: -0.1, EndLat: 51.8, EndLon: -0.3,
		DistanceKm: 30, MaxSpeedKmh: 80, Mode: trips.ModeCar,
		// StartLocation and EndLocation intentionally empty
	}
	require.NoError(t, s.SaveTrip(ctx, trip))
}
```

The test calls `s.DB()` — expose it temporarily for test access.

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./internal/store/... -run TestSaveTrip_Location -v
```

Expected: compile error — `trips.Trip` has no `StartLocation`/`EndLocation` field and `s.DB()` doesn't exist.

- [ ] **Step 3: Add fields to `trips.Trip`**

In `internal/trips/types.go`, add two fields to `Trip`:

```go
type Trip struct {
	Date          string
	StartTime     time.Time
	EndTime       time.Time
	StartLat      float64
	StartLon      float64
	EndLat        float64
	EndLon        float64
	DistanceKm    float64
	MaxSpeedKmh   float64
	Mode          TransportMode
	StartLocation string
	EndLocation   string
	Points        []owntracks.Point
}
```

- [ ] **Step 4: Add migration and update `SaveTrip` in store**

In `internal/store/store.go`:

1. Add a migration block to `New()` — after `db.Exec(schema)` succeeds, run:

```go
migrations := []string{
    `ALTER TABLE trips ADD COLUMN start_location TEXT NOT NULL DEFAULT ''`,
    `ALTER TABLE trips ADD COLUMN end_location TEXT NOT NULL DEFAULT ''`,
}
for _, m := range migrations {
    if _, err := db.Exec(m); err != nil {
        // SQLite returns an error if the column already exists; ignore it.
        // We treat any error here as "already applied".
        _ = err
    }
}
```

2. Update the `INSERT OR IGNORE` in `SaveTrip` to include the new columns:

```go
res, err := tx.ExecContext(ctx, `
    INSERT OR IGNORE INTO trips
      (date, start_time, end_time, start_lat, start_lon, end_lat, end_lon,
       distance_km, max_speed_kmh, mode, start_location, end_location)
    VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
    t.Date,
    t.StartTime.Unix(),
    t.EndTime.Unix(),
    t.StartLat, t.StartLon,
    t.EndLat, t.EndLon,
    t.DistanceKm,
    t.MaxSpeedKmh,
    string(t.Mode),
    t.StartLocation,
    t.EndLocation,
)
```

3. Add `DB()` accessor for test use (unexported receiver, exported method — only used in tests):

```go
// DB returns the underlying *sql.DB. Used in tests only.
func (s *Store) DB() *sql.DB { return s.db }
```

- [ ] **Step 5: Run all store tests**

```bash
go test ./internal/store/... -v
```

Expected: all pass.

- [ ] **Step 6: Run full suite**

```bash
go test ./...
```

Expected: all packages pass.

- [ ] **Step 7: Commit**

```bash
git add internal/trips/types.go internal/store/store.go internal/store/store_test.go
git commit -m "feat: add start_location/end_location columns to trips table and Trip type"
```

---

### Task 3: Wire persistent geocode cache into `geocode.Client`

**Files:**
- Modify: `internal/geocode/nominatim.go`

**Interfaces:**
- Consumes:
  - `store.Store.GetGeocode(ctx, lat, lon float64) (string, bool, error)`
  - `store.Store.SaveGeocode(ctx, lat, lon float64, label string) error`
- Produces:
  - `type GeoStore interface { GetGeocode(ctx context.Context, lat, lon float64) (string, bool, error); SaveGeocode(ctx context.Context, lat, lon float64, label string) error }`
  - `func (c *Client) WithStore(gs GeoStore) *Client` — attaches persistent cache; returns receiver for chaining
  - `Client.Reverse` behaviour: check in-memory → check DB → call Nominatim → write to DB and in-memory

**Note:** We do NOT import `internal/store` from `internal/geocode` (that would create a cycle). Instead, `geocode` defines a `GeoStore` interface that `*store.Store` satisfies structurally.

- [ ] **Step 1: Write failing test**

Create `internal/geocode/nominatim_test.go`:

```go
package geocode_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/geocode"
)

// fakeGeoStore is an in-test implementation of geocode.GeoStore.
type fakeGeoStore struct {
	data map[string]string
}

func newFakeGeoStore() *fakeGeoStore {
	return &fakeGeoStore{data: make(map[string]string)}
}

func key(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

func (f *fakeGeoStore) GetGeocode(_ context.Context, lat, lon float64) (string, bool, error) {
	v, ok := f.data[key(lat, lon)]
	return v, ok, nil
}

func (f *fakeGeoStore) SaveGeocode(_ context.Context, lat, lon float64, label string) error {
	f.data[key(lat, lon)] = label
	return nil
}

func TestClient_WithStore_CachesInDB(t *testing.T) {
	var apiCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"address":{"road":"Test Road","suburb":"Test Suburb"}}`)
	}))
	defer srv.Close()

	gs := newFakeGeoStore()
	c := geocode.NewWithBaseURL(srv.URL).WithStore(gs)

	loc1, err := c.Reverse(context.Background(), 51.5074, -0.1278)
	require.NoError(t, err)
	assert.Equal(t, "Test Road, Test Suburb", loc1.Label)
	assert.Equal(t, int32(1), apiCalls.Load())

	// Second call for same coords: should hit DB store, not Nominatim
	loc2, err := c.Reverse(context.Background(), 51.5074, -0.1278)
	require.NoError(t, err)
	assert.Equal(t, "Test Road, Test Suburb", loc2.Label)
	assert.Equal(t, int32(1), apiCalls.Load(), "Nominatim should not be called again")
}
```

Note: `fakeGeoStore` key function needs `"fmt"` imported — add it.

- [ ] **Step 2: Run to confirm failure**

```bash
go test ./internal/geocode/... -v
```

Expected: compile error — `geocode.GeoStore`, `geocode.NewWithBaseURL`, `(*Client).WithStore` undefined.

- [ ] **Step 3: Update `nominatim.go`**

Replace the full contents of `internal/geocode/nominatim.go`:

```go
package geocode

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sync"
	"time"
)

const nominatimBaseURL = "https://nominatim.openstreetmap.org"

// Location holds a human-readable label for a coordinate.
type Location struct {
	Label string
}

// GeoStore is the persistent-cache interface geocode.Client writes through.
// *store.Store satisfies this interface.
type GeoStore interface {
	GetGeocode(ctx context.Context, lat, lon float64) (string, bool, error)
	SaveGeocode(ctx context.Context, lat, lon float64, label string) error
}

type nominatimResponse struct {
	Address struct {
		Road          string `json:"road"`
		Neighbourhood string `json:"neighbourhood"`
		Suburb        string `json:"suburb"`
		Quarter       string `json:"quarter"`
		District      string `json:"district"`
		City          string `json:"city"`
		Town          string `json:"town"`
		Village       string `json:"village"`
	} `json:"address"`
}

type cacheKey struct {
	lat, lon float64
}

// Client calls the Nominatim reverse geocoding API with local caching.
type Client struct {
	baseURL string
	http    *http.Client
	mu      sync.Mutex
	cache   map[cacheKey]Location
	gs      GeoStore // optional persistent cache
}

// New returns a Nominatim client pointed at the public Nominatim instance.
func New() *Client {
	return NewWithBaseURL(nominatimBaseURL)
}

// NewWithBaseURL returns a client using a custom base URL (used in tests).
func NewWithBaseURL(baseURL string) *Client {
	return &Client{
		baseURL: baseURL,
		http:    &http.Client{Timeout: 10 * time.Second},
		cache:   make(map[cacheKey]Location),
	}
}

// WithStore attaches a persistent geocode cache. Returns the receiver for chaining.
func (c *Client) WithStore(gs GeoStore) *Client {
	c.gs = gs
	return c
}

// Reverse returns a location label for the given coordinates.
// Lookup order: in-memory cache → persistent store → Nominatim API.
// Results are written back to both caches on a Nominatim hit.
// A 1-second sleep is applied on Nominatim calls to respect the 1 req/sec policy.
func (c *Client) Reverse(ctx context.Context, lat, lon float64) (Location, error) {
	key := cacheKey{round(lat), round(lon)}

	c.mu.Lock()
	if loc, ok := c.cache[key]; ok {
		c.mu.Unlock()
		return loc, nil
	}
	c.mu.Unlock()

	// Check persistent store.
	if c.gs != nil {
		if label, ok, err := c.gs.GetGeocode(ctx, lat, lon); err == nil && ok {
			loc := Location{Label: label}
			c.mu.Lock()
			c.cache[key] = loc
			c.mu.Unlock()
			return loc, nil
		}
	}

	time.Sleep(time.Second)

	url := fmt.Sprintf("%s/reverse?format=jsonv2&lat=%f&lon=%f", c.baseURL, lat, lon)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return Location{}, fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("User-Agent", "autolog/1.0 (https://github.com/sathyabhat/autolog)")
	req.Header.Set("Accept-Language", "en")

	resp, err := c.http.Do(req)
	if err != nil {
		return Location{}, fmt.Errorf("nominatim request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Location{}, fmt.Errorf("nominatim returned status %d", resp.StatusCode)
	}

	var result nominatimResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return Location{}, fmt.Errorf("decode nominatim response: %w", err)
	}

	loc := Location{Label: label(result)}

	c.mu.Lock()
	c.cache[key] = loc
	c.mu.Unlock()

	if c.gs != nil {
		_ = c.gs.SaveGeocode(ctx, lat, lon, loc.Label)
	}

	return loc, nil
}

func round(v float64) float64 {
	return math.Round(v*1e4) / 1e4
}

func label(r nominatimResponse) string {
	a := r.Address
	area := ""
	for _, s := range []string{a.Neighbourhood, a.Suburb, a.Quarter, a.District, a.City, a.Town, a.Village} {
		if s != "" {
			area = s
			break
		}
	}
	if a.Road != "" && area != "" {
		return a.Road + ", " + area
	}
	if a.Road != "" {
		return a.Road
	}
	if area != "" {
		return area
	}
	return "unknown"
}
```

- [ ] **Step 4: Run geocode tests**

```bash
go test ./internal/geocode/... -v
```

Expected: `TestClient_WithStore_CachesInDB` passes.

- [ ] **Step 5: Run full suite**

```bash
go test ./...
```

Expected: all pass. (If `internal/notify` tests call `geocode.New()`, they still work — `New()` is unchanged.)

- [ ] **Step 6: Commit**

```bash
git add internal/geocode/nominatim.go internal/geocode/nominatim_test.go
git commit -m "feat: add GeoStore interface and persistent cache to geocode.Client"
```

---

### Task 4: Wire persistent store into `geocode.Client` at startup

**Files:**
- Modify: `cmd/autolog/main.go`

**Interfaces:**
- Consumes:
  - `geocode.NewWithBaseURL` / `geocode.New()` (unchanged)
  - `(*geocode.Client).WithStore(gs geocode.GeoStore) *geocode.Client`
  - `*store.Store` satisfies `geocode.GeoStore`

- [ ] **Step 1: Update `main.go`**

Change the geocode construction line (currently `geo := geocode.New()`) to attach the store:

```go
geo := geocode.New().WithStore(st)
```

The store `st` is already constructed above this line — no other changes needed.

- [ ] **Step 2: Build to verify no compile errors**

```bash
go build ./cmd/autolog/...
```

Expected: exits 0 with no output.

- [ ] **Step 3: Run full suite**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 4: Commit**

```bash
git add cmd/autolog/main.go
git commit -m "feat: attach persistent geocode cache (store) to geocode client at startup"
```

---

### Task 5: Resolve start/end locations in runner and store on Trip

**Files:**
- Modify: `internal/runner/runner.go`
- Modify: `internal/runner/runner_test.go` (if it exists — check and update)

**Interfaces:**
- Consumes:
  - `trips.Trip.StartLocation string`, `trips.Trip.EndLocation string` (Task 2)
  - `geocode.Client.Reverse(ctx, lat, lon) (geocode.Location, error)` (unchanged)
- Produces: after `Classify`, before `SaveTrip`, runner calls `Reverse` for start and end coords and sets them on the trip. Geocode errors are logged and do not abort the trip save.

- [ ] **Step 1: Check runner tests**

```bash
go test ./internal/runner/... -v
```

Note which tests exist and what mock interfaces they use — you'll need to update them if they assert on `trips.Trip` fields.

- [ ] **Step 2: Add a `geocoder` interface and field to Runner**

In `internal/runner/runner.go`, add after the `locationFetcher` interface:

```go
// geocoder resolves coordinates to a human-readable label.
type geocoder interface {
	Reverse(ctx context.Context, lat, lon float64) (geocode.Location, error)
}
```

Add `geo geocoder` to the `Runner` struct:

```go
type Runner struct {
	cfg   *config.Config
	ot    locationFetcher
	store *store.Store
	tg    Notifier
	geo   geocoder
	log   *zap.Logger
}
```

Update `New` and `NewWithDeps` to accept and store the geocoder:

```go
func New(cfg *config.Config, ot *owntracks.Client, st *store.Store, tg Notifier, geo *geocode.Client, log *zap.Logger) *Runner {
	return NewWithDeps(cfg, ot, st, tg, geo, log)
}

func NewWithDeps(cfg *config.Config, ot locationFetcher, st *store.Store, tg Notifier, geo geocoder, log *zap.Logger) *Runner {
	return &Runner{cfg: cfg, ot: ot, store: st, tg: tg, geo: geo, log: log}
}
```

Add import for `geocode` package:
```go
"github.com/sathyabhat/autolog/internal/geocode"
```

- [ ] **Step 3: Resolve locations in `ProcessOnce`**

In `ProcessOnce`, after `trip, keep := trips.Classify(raw, classCfg)` and the `!keep` check, add:

```go
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
```

- [ ] **Step 4: Update `main.go` call to `runner.New`**

In `cmd/autolog/main.go`, the `runner.New` call must now pass `geo`:

```go
r := runner.New(cfg, ot, st, notifier, geo, log)
```

- [ ] **Step 5: Fix any runner tests that break**

Read `internal/runner/runner_test.go`. If the test constructs a `Runner` via `NewWithDeps`, add a `nil` geocoder argument (geocode is optional):

```go
r := runner.NewWithDeps(cfg, mockOT, st, mockNotifier, nil, log)
```

- [ ] **Step 6: Run full suite**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 7: Commit**

```bash
git add internal/runner/runner.go internal/runner/runner_test.go cmd/autolog/main.go
git commit -m "feat: resolve and store start/end locations on trips in runner"
```

---

### Task 6: Simplify notifiers — use pre-resolved locations from Trip

**Files:**
- Modify: `internal/notify/telegram.go`
- Modify: `internal/notify/stdout.go`
- Modify: `internal/notify/telegram_test.go`

**Interfaces:**
- Consumes: `trips.Trip.StartLocation string`, `trips.Trip.EndLocation string` (Task 2)
- After this task: notifiers no longer call `geo.Reverse`; they use pre-resolved labels from the trip. If a label is empty, fall back to coordinates (same behaviour as before for safety).

- [ ] **Step 1: Update `telegram.go`**

Replace the `Send` method body. The `geo` field and constructor remain — removing them would break callers that pass `geo`; we just stop using it in `Send`:

```go
func (tg *Telegram) Send(ctx context.Context, t trips.Trip) error {
	var text string
	if t.StartLocation != "" && t.EndLocation != "" {
		text = fmt.Sprintf("🚗 %s: %s → %s, %.1f km", t.Date, t.StartLocation, t.EndLocation, t.DistanceKm)
	} else {
		text = fmt.Sprintf(
			"🚗 %s: %.4f,%.4f → %.4f,%.4f, %.1f km",
			t.Date,
			t.StartLat, t.StartLon,
			t.EndLat, t.EndLon,
			t.DistanceKm,
		)
	}
	// ... rest of HTTP send unchanged
```

- [ ] **Step 2: Update `stdout.go`**

Replace the `Send` method:

```go
func (s *Stdout) Send(ctx context.Context, t trips.Trip) error {
	if t.StartLocation != "" && t.EndLocation != "" {
		fmt.Printf("🚗 %s: %s → %s, %.1f km\n", t.Date, t.StartLocation, t.EndLocation, t.DistanceKm)
		return nil
	}
	fmt.Printf("🚗 %s: %.4f,%.4f → %.4f,%.4f, %.1f km\n",
		t.Date,
		t.StartLat, t.StartLon,
		t.EndLat, t.EndLon,
		t.DistanceKm,
	)
	return nil
}
```

- [ ] **Step 3: Read and update `telegram_test.go`**

Read `internal/notify/telegram_test.go`. For any test that expects the geocoded label format, set `StartLocation`/`EndLocation` on the `trips.Trip` directly instead of relying on a mock geocoder. Remove any mock geocoder setup from those tests.

- [ ] **Step 4: Run notify tests**

```bash
go test ./internal/notify/... -v
```

Expected: all pass.

- [ ] **Step 5: Run full suite**

```bash
go test ./...
```

Expected: all pass.

- [ ] **Step 6: Commit**

```bash
git add internal/notify/telegram.go internal/notify/stdout.go internal/notify/telegram_test.go
git commit -m "refactor: notifiers use pre-resolved trip locations instead of calling geocode"
```

---

## Self-Review

**Spec coverage:**
- ✅ Geocode cache persisted to SQLite — Task 1
- ✅ `trips` table gains `start_location`/`end_location` — Task 2
- ✅ `geocode.Client` checks persistent cache before Nominatim — Task 3
- ✅ Wired at startup — Task 4
- ✅ Runner resolves and stores locations before saving — Task 5
- ✅ Notifiers use pre-resolved labels — Task 6

**Placeholder scan:** No TBDs, TODOs, or vague steps found.

**Type consistency:**
- `GeoStore` interface defined in Task 3, consumed in Task 1 (store methods match the interface signature exactly)
- `Trip.StartLocation`/`Trip.EndLocation` added in Task 2, consumed in Tasks 5 and 6
- `runner.New` signature change (adds `geo`) applied in both Task 5 (runner.go) and Task 5 (main.go) in same commit
- `geocode.NewWithBaseURL` introduced in Task 3, used in test; `geocode.New()` unchanged
