# Trip Algorithm Improvements Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Apply four incremental trip-detection improvements (speed anomaly filter, stay-point segmentation, segment-then-vote mode classification, acceleration-based train confirmation) so that each can be evaluated against real trips from the past month.

**Architecture:** Each improvement is self-contained and toggled by a new config flag, so the existing baseline behaviour is preserved unless explicitly enabled. The `trips` package owns all detection logic; `config` gains new flags; `runner` passes the updated config structs. A standalone CLI tool (`cmd/replay`) reads stored GPS points from the existing SQLite DB and re-runs the pipeline with each combination of flags, printing a side-by-side trip table without writing anything to the DB.

**Tech Stack:** Go 1.26, `modernc.org/sqlite` (CGO-free), `github.com/spf13/viper`, `go.uber.org/zap`, `github.com/stretchr/testify`

## Global Constraints

- Module path: `github.com/sathyabhat/autolog`
- CGO_ENABLED=0 — no cgo imports, use `modernc.org/sqlite` only
- Logger: `go.uber.org/zap`, JSON, ISO8601
- Config: `github.com/spf13/viper`, YAML + env vars, no prefix
- All new public functions must have unit tests using `github.com/stretchr/testify`
- Do not change existing SQLite schema — replay tool reads only, never writes
- TDD: write failing test → run it → implement → run again → commit

---

## File Map

| File | Action | Responsibility |
|------|--------|---------------|
| `internal/trips/filter.go` | **Create** | Speed-anomaly point filter |
| `internal/trips/filter_test.go` | **Create** | Tests for speed-anomaly filter |
| `internal/trips/staypoint.go` | **Create** | Stay-point detection + stay-based segmenter |
| `internal/trips/staypoint_test.go` | **Create** | Tests for stay-point detection |
| `internal/trips/segmenter.go` | **Modify** | Accept optional stay-based mode via config flag |
| `internal/trips/classifier.go` | **Modify** | Add segment-then-vote + accel-based train gate |
| `internal/trips/classifier_test.go` | **Modify** | New test cases for segment voting + train accel gate |
| `internal/trips/types.go` | **Modify** | Add `AlgorithmFlags` struct + new fields on `ClassifierConfig` |
| `internal/config/config.go` | **Modify** | Add `AlgorithmFlags` to `FiltersConfig`; wire viper defaults |
| `cmd/replay/main.go` | **Create** | Standalone replay CLI |

---

## Task 1: Speed-Anomaly Point Filter

**Goal:** Drop GPS points whose implied speed to or from their neighbours exceeds a configurable threshold (e.g. 500 km/h), before any distance or speed computation. This prevents wild outlier fixes from corrupting max-speed and distance totals.

**Files:**
- Create: `internal/trips/filter.go`
- Create: `internal/trips/filter_test.go`

**Interfaces:**
- Produces: `func FilterAnomalousPoints(pts []owntracks.Point, maxSpeedKmh float64) []owntracks.Point`
  - Returns a new slice with anomalous points removed. A point is anomalous when the speed implied by *both* the segment before it and the segment after it exceeds `maxSpeedKmh`. For the first and last points, only the single adjacent segment is checked.
  - Points with zero or negative time delta to their neighbour are also dropped.
  - If `maxSpeedKmh <= 0`, the function returns `pts` unchanged (disabled).

- [ ] **Step 1: Write the failing test**

```go
// internal/trips/filter_test.go
package trips_test

import (
    "testing"
    "github.com/stretchr/testify/assert"
    "github.com/sathyabhat/autolog/internal/owntracks"
    "github.com/sathyabhat/autolog/internal/trips"
)

func makeAccPoint(tst int64, lat, lon float64) owntracks.Point {
    return owntracks.Point{Tst: tst, Lat: lat, Lon: lon, Acc: 10}
}

func TestFilterAnomalousPoints_NoOutlier(t *testing.T) {
    // ~50 km/h normal driving points — none removed
    pts := []owntracks.Point{
        makeAccPoint(0,   -33.8688, 151.2093),
        makeAccPoint(60,  -33.8610, 151.2150),
        makeAccPoint(120, -33.8530, 151.2200),
    }
    got := trips.FilterAnomalousPoints(pts, 500)
    assert.Len(t, got, 3)
}

func TestFilterAnomalousPoints_MiddleOutlier(t *testing.T) {
    // middle point teleports thousands of km then back — should be removed
    pts := []owntracks.Point{
        makeAccPoint(0,   -33.8688, 151.2093),
        makeAccPoint(30,  10.0,     10.0),     // outlier: ~15,000 km in 30 s
        makeAccPoint(60,  -33.8610, 151.2150),
    }
    got := trips.FilterAnomalousPoints(pts, 500)
    assert.Len(t, got, 2)
    assert.Equal(t, int64(0), got[0].Tst)
    assert.Equal(t, int64(60), got[1].Tst)
}

func TestFilterAnomalousPoints_Disabled(t *testing.T) {
    pts := []owntracks.Point{
        makeAccPoint(0,   -33.8688, 151.2093),
        makeAccPoint(30,  10.0,     10.0),
        makeAccPoint(60,  -33.8610, 151.2150),
    }
    got := trips.FilterAnomalousPoints(pts, 0) // disabled
    assert.Len(t, got, 3)
}

func TestFilterAnomalousPoints_ZeroDelta(t *testing.T) {
    // duplicate timestamp — second point removed
    pts := []owntracks.Point{
        makeAccPoint(0,  -33.8688, 151.2093),
        makeAccPoint(0,  -33.8700, 151.2100), // same tst
        makeAccPoint(60, -33.8610, 151.2150),
    }
    got := trips.FilterAnomalousPoints(pts, 500)
    assert.Len(t, got, 2)
}
```

- [ ] **Step 2: Run test to verify it fails**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run TestFilterAnomalous -v
```
Expected: compile error — `FilterAnomalousPoints` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/trips/filter.go
package trips

import "github.com/sathyabhat/autolog/internal/owntracks"

// FilterAnomalousPoints removes GPS fixes whose implied speed to or from
// both neighbours exceeds maxSpeedKmh (e.g. cell-tower jumps).
// Pass maxSpeedKmh <= 0 to disable.
func FilterAnomalousPoints(pts []owntracks.Point, maxSpeedKmh float64) []owntracks.Point {
    if maxSpeedKmh <= 0 || len(pts) < 2 {
        return pts
    }
    out := make([]owntracks.Point, 0, len(pts))
    for i, p := range pts {
        if isAnomalous(pts, i, maxSpeedKmh) {
            continue
        }
        out = append(out, p)
    }
    return out
}

func isAnomalous(pts []owntracks.Point, i int, maxKmh float64) bool {
    p := pts[i]
    if i > 0 {
        prev := pts[i-1]
        dt := float64(p.Tst - prev.Tst)
        if dt <= 0 {
            return true
        }
        d := HaversineKm(prev.Lat, prev.Lon, p.Lat, p.Lon)
        if d/dt*3600 > maxKmh {
            // Only anomalous if ALSO the next segment is fast (middle point)
            // or there is no next segment (last point).
            if i == len(pts)-1 {
                return true
            }
            next := pts[i+1]
            dt2 := float64(next.Tst - p.Tst)
            if dt2 <= 0 {
                return true
            }
            d2 := HaversineKm(p.Lat, p.Lon, next.Lat, next.Lon)
            if d2/dt2*3600 > maxKmh {
                return true
            }
        }
    } else {
        // First point: check only the forward segment.
        if len(pts) > 1 {
            next := pts[1]
            dt := float64(next.Tst - p.Tst)
            if dt <= 0 {
                return true
            }
            d := HaversineKm(p.Lat, p.Lon, next.Lat, next.Lon)
            if d/dt*3600 > maxKmh {
                return true
            }
        }
    }
    return false
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run TestFilterAnomalous -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Run full test suite to check for regressions**

```bash
cd /home/sathyabhat/code/autolog
go test ./... -v 2>&1 | tail -20
```
Expected: all existing tests still PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/sathyabhat/code/autolog
git add internal/trips/filter.go internal/trips/filter_test.go
git commit -m "feat: add speed-anomaly GPS point filter"
```

---

## Task 2: Stay-Point Detection

**Goal:** Implement the sliding-window stay-point algorithm (inspired by reitti/dawarich). A "stay" is a cluster of GPS points that remain within `radiusM` metres of a running centroid for at least `minDuration`. Stays become trip boundaries in Task 3.

**Files:**
- Create: `internal/trips/staypoint.go`
- Create: `internal/trips/staypoint_test.go`

**Interfaces:**
- Produces: `type Stay struct { CentLat, CentLon float64; ArrivalTst, DepartureTst int64 }`
- Produces: `func DetectStays(pts []owntracks.Point, radiusM float64, minDuration, maxGap time.Duration) []Stay`
  - `radiusM`: cluster radius in metres (e.g. 50.0)
  - `minDuration`: minimum time at a stay to qualify (e.g. 5 min)
  - `maxGap`: maximum gap between consecutive included points before closing the stay (e.g. 5 min)
  - Returns stays sorted by ArrivalTst ascending.
  - Adjacent stays whose centroids are within `radiusM` and whose gap ≤ `maxGap` are merged into one.

- [ ] **Step 1: Write failing tests**

```go
// internal/trips/staypoint_test.go
package trips_test

import (
    "testing"
    "time"

    "github.com/stretchr/testify/assert"
    "github.com/stretchr/testify/require"
    "github.com/sathyabhat/autolog/internal/owntracks"
    "github.com/sathyabhat/autolog/internal/trips"
)

func pts(base int64, coords [][2]float64, intervalSec int64) []owntracks.Point {
    out := make([]owntracks.Point, len(coords))
    for i, c := range coords {
        out[i] = owntracks.Point{Tst: base + int64(i)*intervalSec, Lat: c[0], Lon: c[1], Acc: 10}
    }
    return out
}

func TestDetectStays_OneStay(t *testing.T) {
    // 10 points clustered near same location over 10 minutes.
    base := int64(1_700_000_000)
    coords := [][2]float64{
        {-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
        {-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
        {-33.8687, 151.2094}, {-33.8690, 151.2092}, {-33.8688, 151.2093},
        {-33.8689, 151.2094},
    }
    p := pts(base, coords, 60) // one point per minute → 9 min span
    stays := trips.DetectStays(p, 100, 5*time.Minute, 5*time.Minute)
    require.Len(t, stays, 1)
    assert.InDelta(t, -33.8688, stays[0].CentLat, 0.001)
    assert.Equal(t, base, stays[0].ArrivalTst)
}

func TestDetectStays_TwoStays_GapSplits(t *testing.T) {
    base := int64(1_700_000_000)
    // Stay A: 6 min at location A
    coordsA := [][2]float64{
        {-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
        {-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
    }
    pA := pts(base, coordsA, 60)
    // 20-min gap (exceeds 5-min maxGap)
    // Stay B: 6 min at location B (far away)
    coordsB := [][2]float64{
        {-33.9000, 151.2500}, {-33.9001, 151.2501}, {-33.8999, 151.2499},
        {-33.9002, 151.2500}, {-33.9000, 151.2502}, {-33.9001, 151.2498},
    }
    offsetB := base + int64(len(coordsA)-1)*60 + 20*60 + 60
    pB := pts(offsetB, coordsB, 60)

    all := append(pA, pB...)
    stays := trips.DetectStays(all, 100, 5*time.Minute, 5*time.Minute)
    assert.Len(t, stays, 2)
}

func TestDetectStays_TooShort_Discarded(t *testing.T) {
    base := int64(1_700_000_000)
    // Only 3 points at 60-s intervals = 2 min < minDuration 5 min
    coords := [][2]float64{
        {-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
    }
    p := pts(base, coords, 60)
    stays := trips.DetectStays(p, 100, 5*time.Minute, 5*time.Minute)
    assert.Empty(t, stays)
}

func TestDetectStays_MergesAdjacentNearbyStays(t *testing.T) {
    base := int64(1_700_000_000)
    // Two stays at virtually the same spot, gap = 3 min (< maxGap 5 min)
    coordsA := [][2]float64{
        {-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
        {-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
    }
    pA := pts(base, coordsA, 60)
    coordsB := [][2]float64{
        {-33.8688, 151.2093}, {-33.8689, 151.2094}, {-33.8687, 151.2092},
        {-33.8690, 151.2093}, {-33.8688, 151.2095}, {-33.8689, 151.2091},
    }
    offsetB := base + int64(len(coordsA)-1)*60 + 3*60 + 60
    pB := pts(offsetB, coordsB, 60)
    all := append(pA, pB...)
    stays := trips.DetectStays(all, 100, 5*time.Minute, 5*time.Minute)
    assert.Len(t, stays, 1)
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run TestDetectStays -v
```
Expected: compile error — `Stay`, `DetectStays` undefined.

- [ ] **Step 3: Write the implementation**

```go
// internal/trips/staypoint.go
package trips

import (
    "time"
    "github.com/sathyabhat/autolog/internal/owntracks"
)

// Stay represents a period where the device remained within a small radius.
type Stay struct {
    CentLat      float64
    CentLon      float64
    ArrivalTst   int64
    DepartureTst int64
}

// DetectStays runs a sliding-window stay-point algorithm over sorted pts.
// radiusM is the cluster radius in metres; minDuration is the minimum stay
// length; maxGap is the maximum allowed time gap between included points
// before the current cluster is closed.
func DetectStays(pts []owntracks.Point, radiusM float64, minDuration, maxGap time.Duration) []Stay {
    if len(pts) < 2 {
        return nil
    }
    maxGapSec := int64(maxGap.Seconds())
    minDurSec := int64(minDuration.Seconds())

    var stays []Stay
    i := 0
    for i < len(pts) {
        centLat := pts[i].Lat
        centLon := pts[i].Lon
        arrivalTst := pts[i].Tst
        lastIncluded := i
        n := 1 // count of points in cluster

        for j := i + 1; j < len(pts); j++ {
            gap := pts[j].Tst - pts[lastIncluded].Tst
            if gap > maxGapSec {
                break
            }
            distM := HaversineKm(centLat, centLon, pts[j].Lat, pts[j].Lon) * 1000
            if distM <= radiusM {
                // Update centroid incrementally.
                n++
                centLat += (pts[j].Lat - centLat) / float64(n)
                centLon += (pts[j].Lon - centLon) / float64(n)
                lastIncluded = j
            }
        }

        duration := pts[lastIncluded].Tst - arrivalTst
        if duration >= minDurSec {
            stays = append(stays, Stay{
                CentLat:      centLat,
                CentLon:      centLon,
                ArrivalTst:   arrivalTst,
                DepartureTst: pts[lastIncluded].Tst,
            })
            i = lastIncluded + 1
        } else {
            i++
        }
    }

    return mergeAdjacentStays(stays, radiusM, maxGapSec)
}

func mergeAdjacentStays(stays []Stay, radiusM float64, maxGapSec int64) []Stay {
    if len(stays) < 2 {
        return stays
    }
    out := []Stay{stays[0]}
    for _, s := range stays[1:] {
        prev := &out[len(out)-1]
        gap := s.ArrivalTst - prev.DepartureTst
        distM := HaversineKm(prev.CentLat, prev.CentLon, s.CentLat, s.CentLon) * 1000
        if gap <= maxGapSec && distM <= radiusM {
            prev.DepartureTst = s.DepartureTst
            // recompute centroid as simple mean of the two centroids
            prev.CentLat = (prev.CentLat + s.CentLat) / 2
            prev.CentLon = (prev.CentLon + s.CentLon) / 2
        } else {
            out = append(out, s)
        }
    }
    return out
}
```

- [ ] **Step 4: Run tests to verify they pass**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run TestDetectStays -v
```
Expected: all 4 tests PASS.

- [ ] **Step 5: Run full test suite**

```bash
cd /home/sathyabhat/code/autolog
go test ./... 2>&1 | tail -10
```
Expected: all tests PASS.

- [ ] **Step 6: Commit**

```bash
cd /home/sathyabhat/code/autolog
git add internal/trips/staypoint.go internal/trips/staypoint_test.go
git commit -m "feat: add sliding-window stay-point detection"
```

---

## Task 3: Stay-Based Segmentation + Algorithm Flags

**Goal:** Wire the stay-point detector as an alternative segmentation path. Add `AlgorithmFlags` to config so each improvement can be toggled independently. The existing gap-based `Segment()` remains the default (flags all off).

**Files:**
- Modify: `internal/trips/types.go` — add `AlgorithmFlags`
- Modify: `internal/trips/segmenter.go` — add `SegmentWithStays()`
- Modify: `internal/trips/segmenter_test.go` — new tests for stay-based segmentation
- Modify: `internal/config/config.go` — add `AlgorithmFlags` to `FiltersConfig`; wire viper defaults

**Interfaces:**
- Consumes: `Stay` and `DetectStays()` from Task 2
- Consumes: `FilterAnomalousPoints()` from Task 1
- Produces (on `ClassifierConfig`): `AlgorithmFlags` struct embedded (added in this task, used by Task 4)

```go
// AlgorithmFlags selects which detection improvements are active.
type AlgorithmFlags struct {
    AnomalyFilter   bool // Task 1: drop implausible GPS jumps before classification
    StaySegment     bool // Task 3: use stay-point boundaries instead of raw time gap
    SegmentVote     bool // Task 4: split trip by speed changes, vote on dominant mode
    AccelTrainGate  bool // Task 4: require low acceleration to confirm train mode
}
```

- [ ] **Step 1: Add `AlgorithmFlags` to types.go**

Open `internal/trips/types.go`. After the existing imports/constants, add:

```go
// AlgorithmFlags selects which detection improvements are active.
// All flags default to false (baseline behaviour preserved).
type AlgorithmFlags struct {
    AnomalyFilter  bool
    StaySegment    bool
    SegmentVote    bool
    AccelTrainGate bool
}
```

Also add to `ClassifierConfig` in `internal/trips/classifier.go`:

```go
// In ClassifierConfig, add:
Flags           AlgorithmFlags
AnomalyMaxKmh  float64  // used when Flags.AnomalyFilter is true; 0 → 500 km/h default
StayRadiusM    float64  // used when Flags.StaySegment is true; 0 → 50 m default
StayMinDur     time.Duration
StayMaxGap     time.Duration
```

- [ ] **Step 2: Add `SegmentWithStays()` to segmenter.go**

Append to `internal/trips/segmenter.go`:

```go
// SegmentWithStays detects stays in pts, then returns one RawTrip per
// inter-stay gap. Points are pre-filtered for anomalous speed if
// flags.AnomalyFilter is set. Falls back to Segment() if no stays are found.
func SegmentWithStays(points []owntracks.Point, cfg ClassifierConfig) []RawTrip {
    pts := points
    if cfg.Flags.AnomalyFilter {
        maxKmh := cfg.AnomalyMaxKmh
        if maxKmh <= 0 {
            maxKmh = 500
        }
        pts = FilterAnomalousPoints(pts, maxKmh)
    }

    radiusM := cfg.StayRadiusM
    if radiusM <= 0 {
        radiusM = 50
    }
    minDur := cfg.StayMinDur
    if minDur <= 0 {
        minDur = 5 * time.Minute
    }
    maxGap := cfg.StayMaxGap
    if maxGap <= 0 {
        maxGap = 5 * time.Minute
    }

    stays := DetectStays(pts, radiusM, minDur, maxGap)
    if len(stays) == 0 {
        // No stays detected — fall back to gap-based segmentation.
        return Segment(pts, defaultTripGap)
    }

    var result []RawTrip

    // Trip before the first stay.
    if len(pts) > 0 && pts[0].Tst < stays[0].ArrivalTst {
        var seg []owntracks.Point
        for _, p := range pts {
            if p.Tst < stays[0].ArrivalTst {
                seg = append(seg, p)
            }
        }
        if len(seg) >= 2 {
            result = append(result, RawTrip{Points: seg})
        }
    }

    // Trips between consecutive stays.
    for i := 0; i < len(stays)-1; i++ {
        from := stays[i].DepartureTst
        to := stays[i+1].ArrivalTst
        var seg []owntracks.Point
        for _, p := range pts {
            if p.Tst > from && p.Tst < to {
                seg = append(seg, p)
            }
        }
        if len(seg) >= 2 {
            result = append(result, RawTrip{Points: seg})
        }
    }

    // Trip after the last stay.
    last := stays[len(stays)-1]
    var seg []owntracks.Point
    for _, p := range pts {
        if p.Tst > last.DepartureTst {
            seg = append(seg, p)
        }
    }
    if len(seg) >= 2 {
        result = append(result, RawTrip{Points: seg})
    }

    return result
}
```

- [ ] **Step 3: Add tests for `SegmentWithStays`**

Append to `internal/trips/segmenter_test.go`:

```go
func TestSegmentWithStays_SplitsOnStay(t *testing.T) {
    // Build a synthetic route: drive → park 6 min → drive again.
    base := int64(1_700_000_000)
    // Leg A: 5 moving points spaced 60 s apart, each ~800 m apart (~48 km/h)
    legA := []owntracks.Point{
        {Tst: base,       Lat: -33.8600, Lon: 151.2000, Acc: 10},
        {Tst: base + 60,  Lat: -33.8528, Lon: 151.2000, Acc: 10},
        {Tst: base + 120, Lat: -33.8456, Lon: 151.2000, Acc: 10},
        {Tst: base + 180, Lat: -33.8384, Lon: 151.2000, Acc: 10},
        {Tst: base + 240, Lat: -33.8312, Lon: 151.2000, Acc: 10},
    }
    // Stay: 7 points clustered within 20 m over 6 min
    stayBase := base + 300
    stay := []owntracks.Point{
        {Tst: stayBase,       Lat: -33.8312, Lon: 151.2001, Acc: 10},
        {Tst: stayBase + 60,  Lat: -33.8312, Lon: 151.2002, Acc: 10},
        {Tst: stayBase + 120, Lat: -33.8313, Lon: 151.2001, Acc: 10},
        {Tst: stayBase + 180, Lat: -33.8312, Lon: 151.2000, Acc: 10},
        {Tst: stayBase + 240, Lat: -33.8311, Lon: 151.2001, Acc: 10},
        {Tst: stayBase + 300, Lat: -33.8312, Lon: 151.2002, Acc: 10},
        {Tst: stayBase + 360, Lat: -33.8313, Lon: 151.2000, Acc: 10},
    }
    // Leg B: 5 moving points
    legBBase := stayBase + 420
    legB := []owntracks.Point{
        {Tst: legBBase,       Lat: -33.8312, Lon: 151.2000, Acc: 10},
        {Tst: legBBase + 60,  Lat: -33.8240, Lon: 151.2000, Acc: 10},
        {Tst: legBBase + 120, Lat: -33.8168, Lon: 151.2000, Acc: 10},
        {Tst: legBBase + 180, Lat: -33.8096, Lon: 151.2000, Acc: 10},
        {Tst: legBBase + 240, Lat: -33.8024, Lon: 151.2000, Acc: 10},
    }
    all := append(append(legA, stay...), legB...)
    cfg := trips.ClassifierConfig{
        Flags:      trips.AlgorithmFlags{StaySegment: true},
        StayRadiusM: 100,
        StayMinDur:  5 * time.Minute,
        StayMaxGap:  5 * time.Minute,
    }
    segs := trips.SegmentWithStays(all, cfg)
    // Expect 2 trips: legA and legB (stay is the boundary)
    assert.GreaterOrEqual(t, len(segs), 2)
}
```

- [ ] **Step 4: Add `AlgorithmFlags` to config**

In `internal/config/config.go`, add to `FiltersConfig`:

```go
// In FiltersConfig struct, add:
AlgorithmFlags AlgorithmFlags `mapstructure:"algorithm_flags"`
StayRadiusM    float64        `mapstructure:"stay_radius_m"`
StayMinDur     time.Duration  `mapstructure:"stay_min_dur"`
StayMaxGap     time.Duration  `mapstructure:"stay_max_gap"`
AnomalyMaxKmh  float64        `mapstructure:"anomaly_max_kmh"`
```

And add to `AlgorithmFlags` struct in config (mirror of trips.AlgorithmFlags):

```go
type AlgorithmFlags struct {
    AnomalyFilter  bool `mapstructure:"anomaly_filter"`
    StaySegment    bool `mapstructure:"stay_segment"`
    SegmentVote    bool `mapstructure:"segment_vote"`
    AccelTrainGate bool `mapstructure:"accel_train_gate"`
}
```

Add viper defaults in `Load()`:
```go
v.SetDefault("filters.stay_radius_m", 50.0)
v.SetDefault("filters.stay_min_dur", 5*time.Minute)
v.SetDefault("filters.stay_max_gap", 5*time.Minute)
v.SetDefault("filters.anomaly_max_kmh", 500.0)
```

- [ ] **Step 5: Thread flags through runner**

In `internal/runner/runner.go`, inside `ProcessOnce()`, update `classCfg` construction:

```go
classCfg := trips.ClassifierConfig{
    MaxTrainSpeedKmh: r.cfg.Filters.MaxTrainSpeedKmh,
    MinDistanceKm:    r.cfg.Filters.MinDistanceKm,
    MaxAccM:          r.cfg.Filters.MaxAccM,
    StopGap:          r.cfg.Filters.StopGap,
    ExclusionZones:   r.cfg.Filters.ExclusionZones,
    Flags: trips.AlgorithmFlags{
        AnomalyFilter:  r.cfg.Filters.AlgorithmFlags.AnomalyFilter,
        StaySegment:    r.cfg.Filters.AlgorithmFlags.StaySegment,
        SegmentVote:    r.cfg.Filters.AlgorithmFlags.SegmentVote,
        AccelTrainGate: r.cfg.Filters.AlgorithmFlags.AccelTrainGate,
    },
    AnomalyMaxKmh: r.cfg.Filters.AnomalyMaxKmh,
    StayRadiusM:   r.cfg.Filters.StayRadiusM,
    StayMinDur:    r.cfg.Filters.StayMinDur,
    StayMaxGap:    r.cfg.Filters.StayMaxGap,
}
```

And replace the `trips.Segment(...)` call:
```go
var rawTrips []trips.RawTrip
if r.cfg.Filters.AlgorithmFlags.StaySegment {
    rawTrips = trips.SegmentWithStays(points, classCfg)
} else {
    rawTrips = trips.Segment(points, r.cfg.Filters.MaxTripGap)
}
```

- [ ] **Step 6: Run full test suite**

```bash
cd /home/sathyabhat/code/autolog
go test ./... 2>&1 | tail -20
```
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/sathyabhat/code/autolog
git add internal/trips/types.go internal/trips/segmenter.go internal/trips/segmenter_test.go \
    internal/config/config.go internal/runner/runner.go
git commit -m "feat: stay-based segmentation and algorithm flags"
```

---

## Task 4: Segment-Vote Mode Classification + Acceleration Train Gate

**Goal:** Add two new mode-classification heuristics controlled by `AlgorithmFlags.SegmentVote` and `AlgorithmFlags.AccelTrainGate`. These augment (not replace) the existing `classifyMode()` path.

**SegmentVote:** Split the trip's point sequence at locations where the relative speed change between consecutive segments exceeds 50%. Classify each sub-segment by its average speed using the ladder: ≤7 km/h → walking, ≤20 km/h → cycling, ≤120 km/h → car, >120 km/h → transit/train. Pick the mode with the most total time.

**AccelTrainGate:** Before confirming `ModeTrain`, verify that the average acceleration magnitude is <0.2 m/s² AND `maxSpeed/avgSpeed < 1.3`. If these don't hold, downgrade to `ModeCar`.

**Files:**
- Modify: `internal/trips/classifier.go`
- Modify: `internal/trips/classifier_test.go`

**Interfaces:**
- Consumes: `AlgorithmFlags`, `ClassifierConfig` (with `Flags` field from Task 3)
- The existing `Classify()` signature is unchanged; internal logic branches on `cfg.Flags`

- [ ] **Step 1: Write failing tests**

Append to `internal/trips/classifier_test.go`:

```go
func TestClassify_SegmentVote_CarDominates(t *testing.T) {
    // Mixed trip: short fast section (train speed) + long slow car section.
    // SegmentVote should pick car because it has more total time.
    now := time.Now().Unix()
    raw := trips.RawTrip{Points: []owntracks.Point{
        // 60-s car-speed leg: ~830 m / 60 s ≈ 50 km/h
        {Tst: now,       Lat: -33.8600, Lon: 151.2000, Acc: 10},
        {Tst: now + 60,  Lat: -33.8528, Lon: 151.2000, Acc: 10},
        {Tst: now + 120, Lat: -33.8456, Lon: 151.2000, Acc: 10},
        {Tst: now + 180, Lat: -33.8384, Lon: 151.2000, Acc: 10},
        {Tst: now + 240, Lat: -33.8312, Lon: 151.2000, Acc: 10},
        {Tst: now + 300, Lat: -33.8240, Lon: 151.2000, Acc: 10},
        // 30-s high-speed blip: ~5 km in 30 s ≈ 600 km/h (but only 30 s)
        {Tst: now + 330, Lat: -33.7800, Lon: 151.2000, Acc: 10},
    }}
    cfg := trips.ClassifierConfig{
        MaxTrainSpeedKmh: 150,
        Flags:            trips.AlgorithmFlags{SegmentVote: true},
    }
    trip, _, keep := trips.Classify(raw, cfg)
    require.True(t, keep)
    assert.Equal(t, trips.ModeCar, trip.Mode)
}

func TestClassify_AccelTrainGate_DowngradesJerkyHighSpeed(t *testing.T) {
    // High max speed but with large speed variance (jerky motorway)
    // AccelTrainGate should downgrade from train to car.
    now := time.Now().Unix()
    raw := trips.RawTrip{Points: []owntracks.Point{
        {Tst: now,       Lat: -33.8600, Lon: 151.2000, Acc: 10},
        {Tst: now + 30,  Lat: -33.8350, Lon: 151.2000, Acc: 10}, // ~100 km/h
        {Tst: now + 35,  Lat: -33.8310, Lon: 151.2000, Acc: 10}, // sudden burst: ~290 km/h
        {Tst: now + 90,  Lat: -33.8050, Lon: 151.2000, Acc: 10}, // back to normal
        {Tst: now + 150, Lat: -33.7800, Lon: 151.2000, Acc: 10},
    }}
    cfg := trips.ClassifierConfig{
        MaxTrainSpeedKmh: 150,
        Flags:            trips.AlgorithmFlags{AccelTrainGate: true},
    }
    trip, _, keep := trips.Classify(raw, cfg)
    require.True(t, keep)
    // Without the gate this might be classified train due to high max speed;
    // with the gate it should be downgraded to car.
    assert.Equal(t, trips.ModeCar, trip.Mode)
}

func TestClassify_AccelTrainGate_KeepsSmoothTrain(t *testing.T) {
    // Smooth high-speed profile: consistent ~200 km/h, low acceleration variance.
    // AccelTrainGate should keep as train.
    now := time.Now().Unix()
    // Each step: ~1.67 km in 30 s = ~200 km/h, consistent spacing
    raw := trips.RawTrip{Points: []owntracks.Point{
        {Tst: now,        Lat: -33.8600, Lon: 151.2000, Acc: 10},
        {Tst: now + 30,   Lat: -33.8450, Lon: 151.2000, Acc: 10},
        {Tst: now + 60,   Lat: -33.8300, Lon: 151.2000, Acc: 10},
        {Tst: now + 90,   Lat: -33.8150, Lon: 151.2000, Acc: 10},
        {Tst: now + 120,  Lat: -33.8000, Lon: 151.2000, Acc: 10},
        {Tst: now + 150,  Lat: -33.7850, Lon: 151.2000, Acc: 10},
    }}
    cfg := trips.ClassifierConfig{
        MaxTrainSpeedKmh: 150,
        Flags:            trips.AlgorithmFlags{AccelTrainGate: true},
    }
    trip, _, keep := trips.Classify(raw, cfg)
    require.True(t, keep)
    assert.Equal(t, trips.ModeTrain, trip.Mode)
}
```

- [ ] **Step 2: Run to verify they fail**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run "TestClassify_SegmentVote|TestClassify_AccelTrainGate" -v
```
Expected: compile errors (Flags field not on ClassifierConfig yet) or test failures.

- [ ] **Step 3: Implement segment-vote and accel gate in classifier.go**

In `internal/trips/classifier.go`, replace the `mode := classifyMode(...)` line with:

```go
var mode TransportMode
if cfg.Flags.SegmentVote {
    mode = classifyBySegmentVote(pts, cfg.MaxTrainSpeedKmh)
} else {
    mode = classifyMode(pts, maxSpeed, cfg.MaxTrainSpeedKmh)
}

if mode == ModeTrain && cfg.Flags.AccelTrainGate {
    if !confirmTrain(pts, maxSpeed) {
        mode = ModeCar
    }
}
```

Add the two new functions at the bottom of `classifier.go`:

```go
// classifyBySegmentVote splits pts at >50% relative speed changes, classifies
// each segment by average speed, then returns the mode with the most total
// seconds. Speed ladder: ≤7 → walking, ≤20 → cycling, ≤120 → car, >120 → train.
func classifyBySegmentVote(pts []owntracks.Point, trainThreshold float64) TransportMode {
    type seg struct {
        mode    TransportMode
        durSec  int64
    }
    var segments []seg
    segStart := 0
    prevSpeed := -1.0

    flush := func(end int) {
        if end <= segStart {
            return
        }
        var dist float64
        for k := segStart + 1; k <= end && k < len(pts); k++ {
            dist += HaversineKm(pts[k-1].Lat, pts[k-1].Lon, pts[k].Lat, pts[k].Lon)
        }
        dur := pts[min64(end, len(pts)-1)].Tst - pts[segStart].Tst
        if dur <= 0 {
            return
        }
        avg := dist / float64(dur) * 3600
        var m TransportMode
        switch {
        case avg <= 7:
            m = ModeWalking
        case avg <= 20:
            m = ModeCycling
        case avg <= 120:
            m = ModeCar
        default:
            m = ModeTrain
        }
        segments = append(segments, seg{mode: m, durSec: dur})
    }

    for i := 1; i < len(pts); i++ {
        dt := float64(pts[i].Tst - pts[i-1].Tst)
        if dt <= 0 {
            continue
        }
        d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon)
        speed := d / dt * 3600
        if prevSpeed > 0 && speed > 0 {
            change := (speed - prevSpeed) / prevSpeed
            if change > 0.5 || change < -0.5 {
                flush(i - 1)
                segStart = i - 1
            }
        }
        prevSpeed = speed
    }
    flush(len(pts) - 1)

    tally := map[TransportMode]int64{}
    for _, s := range segments {
        tally[s.mode] += s.durSec
    }

    var best TransportMode = ModeCar
    var bestDur int64
    for m, d := range tally {
        if d > bestDur {
            bestDur = d
            best = m
        }
    }
    return best
}

// confirmTrain returns true if the speed profile is consistent with rail:
// average acceleration magnitude < 0.2 m/s² AND max/avg speed ratio < 1.3.
func confirmTrain(pts []owntracks.Point, maxSpeed float64) bool {
    if len(pts) < 3 {
        return true // not enough data to refute
    }
    // Compute per-segment speeds in m/s.
    speeds := make([]float64, 0, len(pts)-1)
    for i := 1; i < len(pts); i++ {
        dt := float64(pts[i].Tst - pts[i-1].Tst)
        if dt <= 0 {
            continue
        }
        d := HaversineKm(pts[i-1].Lat, pts[i-1].Lon, pts[i].Lat, pts[i].Lon) * 1000
        speeds = append(speeds, d/dt)
    }
    if len(speeds) < 2 {
        return true
    }
    var totalAccel float64
    for i := 1; i < len(speeds); i++ {
        dt := float64(pts[i+1].Tst - pts[i].Tst)
        if dt <= 0 {
            continue
        }
        totalAccel += math.Abs(speeds[i]-speeds[i-1]) / dt
    }
    avgAccel := totalAccel / float64(len(speeds)-1)
    if avgAccel >= 0.2 {
        return false
    }
    var sumSpeed float64
    for _, s := range speeds {
        sumSpeed += s
    }
    avgSpeed := sumSpeed / float64(len(speeds))
    if avgSpeed <= 0 {
        return true
    }
    maxSpeedMs := maxSpeed / 3.6
    if maxSpeedMs/avgSpeed >= 1.3 {
        return false
    }
    return true
}
```

Also add `ModeWalking` and `ModeCycling` to `types.go`:
```go
ModeWalking  TransportMode = "walking"
ModeCycling  TransportMode = "cycling"
```

- [ ] **Step 4: Add `math` import to classifier.go if not present**

Check the import block in `classifier.go`. Add `"math"` if missing.

- [ ] **Step 5: Run new tests**

```bash
cd /home/sathyabhat/code/autolog
go test ./internal/trips/... -run "TestClassify_SegmentVote|TestClassify_AccelTrainGate" -v
```
Expected: all 3 tests PASS.

- [ ] **Step 6: Run full test suite**

```bash
cd /home/sathyabhat/code/autolog
go test ./... 2>&1 | tail -20
```
Expected: all tests PASS.

- [ ] **Step 7: Commit**

```bash
cd /home/sathyabhat/code/autolog
git add internal/trips/classifier.go internal/trips/classifier_test.go internal/trips/types.go
git commit -m "feat: segment-vote mode classification and accel-based train gate"
```

---

## Task 5: Replay CLI

**Goal:** Build `cmd/replay/main.go` — a standalone CLI that reads stored GPS points from the existing SQLite DB, runs the full pipeline under each algorithm flag combination (baseline + each flag on + all on), and prints a comparison table of trips per combination. Nothing is written to the DB.

This is the evaluation tool for "how do trips differ over the past month".

**Files:**
- Create: `cmd/replay/main.go`

**Interfaces:**
- Consumes: `store.Store.GetTripPoints()`, `trips.Segment()`, `trips.SegmentWithStays()`, `trips.Classify()`, `trips.FilterAnomalousPoints()`
- Reads trips stored in the DB, re-fetches their GPS points, runs each algorithm variant
- Output: tab-separated table to stdout, one row per (trip, variant) pair

**Usage:**
```
go run ./cmd/replay --db autolog.db --days 30
```

Columns: `variant | date | start | end | dist_km | max_spd | mode | start_loc | end_loc`

Variants printed: `baseline`, `anomaly_filter`, `stay_segment`, `segment_vote`, `accel_train_gate`, `all`

- [ ] **Step 1: Write the replay CLI**

```go
// cmd/replay/main.go
package main

import (
    "context"
    "database/sql"
    "flag"
    "fmt"
    "os"
    "text/tabwriter"
    "time"

    _ "modernc.org/sqlite"

    "github.com/sathyabhat/autolog/internal/trips"
)

func main() {
    dbPath := flag.String("db", "autolog.db", "path to autolog SQLite database")
    days := flag.Int("days", 30, "how many days back to replay")
    flag.Parse()

    db, err := sql.Open("sqlite", *dbPath)
    if err != nil {
        fmt.Fprintf(os.Stderr, "open db: %v\n", err)
        os.Exit(1)
    }
    defer db.Close()

    since := time.Now().UTC().AddDate(0, 0, -*days)

    // Fetch stored trips and their GPS points.
    type storedTrip struct {
        date      string
        startTime time.Time
        startLoc  string
        endLoc    string
    }
    rows, err := db.QueryContext(context.Background(), `
        SELECT date, start_time, start_location, end_location
        FROM trips
        WHERE start_time >= ?
        ORDER BY start_time`, since.Unix())
    if err != nil {
        fmt.Fprintf(os.Stderr, "query trips: %v\n", err)
        os.Exit(1)
    }
    var stored []storedTrip
    for rows.Next() {
        var st storedTrip
        var startUnix int64
        if err := rows.Scan(&st.date, &startUnix, &st.startLoc, &st.endLoc); err != nil {
            rows.Close()
            fmt.Fprintf(os.Stderr, "scan: %v\n", err)
            os.Exit(1)
        }
        st.startTime = time.Unix(startUnix, 0).UTC()
        stored = append(stored, st)
    }
    rows.Close()
    if err := rows.Err(); err != nil {
        fmt.Fprintf(os.Stderr, "rows err: %v\n", err)
        os.Exit(1)
    }

    type variant struct {
        name  string
        flags trips.AlgorithmFlags
    }
    variants := []variant{
        {"baseline", trips.AlgorithmFlags{}},
        {"anomaly_filter", trips.AlgorithmFlags{AnomalyFilter: true}},
        {"stay_segment", trips.AlgorithmFlags{AnomalyFilter: true, StaySegment: true}},
        {"segment_vote", trips.AlgorithmFlags{SegmentVote: true}},
        {"accel_train_gate", trips.AlgorithmFlags{AccelTrainGate: true}},
        {"all", trips.AlgorithmFlags{AnomalyFilter: true, StaySegment: true, SegmentVote: true, AccelTrainGate: true}},
    }

    w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
    fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\tstart_loc\tend_loc")

    for _, st := range stored {
        // Load GPS points for this trip.
        ptRows, err := db.QueryContext(context.Background(), `
            SELECT p.tst, p.lat, p.lon, p.vel, p.acc
            FROM trip_points p
            JOIN trips t ON t.id = p.trip_id
            WHERE t.date = ? AND t.start_time = ?
            ORDER BY p.tst`, st.date, st.startTime.Unix())
        if err != nil {
            fmt.Fprintf(os.Stderr, "query points for %s %s: %v\n", st.date, st.startTime, err)
            continue
        }
        var pts []trips.OwnTracksPoint
        for ptRows.Next() {
            var p trips.OwnTracksPoint
            if err := ptRows.Scan(&p.Tst, &p.Lat, &p.Lon, &p.Vel, &p.Acc); err != nil {
                break
            }
            pts = append(pts, p)
        }
        ptRows.Close()
        if len(pts) < 2 {
            continue
        }

        for _, v := range variants {
            cfg := trips.ClassifierConfig{
                MaxTrainSpeedKmh: 150,
                MinDistanceKm:    2.0,
                MaxAccM:          100,
                StopGap:          10 * time.Minute,
                Flags:            v.flags,
                AnomalyMaxKmh:    500,
                StayRadiusM:      50,
                StayMinDur:       5 * time.Minute,
                StayMaxGap:       5 * time.Minute,
            }

            var rawTrips []trips.RawTrip
            if v.flags.StaySegment {
                rawTrips = trips.SegmentWithStays(asOwnTracksPoints(pts), cfg)
            } else {
                filtered := asOwnTracksPoints(pts)
                if v.flags.AnomalyFilter {
                    filtered = trips.FilterAnomalousPoints(filtered, 500)
                }
                rawTrips = trips.Segment(filtered, 90*time.Minute)
            }

            for _, raw := range rawTrips {
                trip, _, keep := trips.Classify(raw, cfg)
                if !keep {
                    continue
                }
                fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%s\t%s\n",
                    v.name,
                    trip.Date,
                    trip.StartTime.Format("15:04"),
                    trip.EndTime.Format("15:04"),
                    trip.DistanceKm,
                    trip.MaxSpeedKmh,
                    string(trip.Mode),
                    st.startLoc,
                    st.endLoc,
                )
            }
        }
    }
    w.Flush()
}

// OwnTracksPoint is a local alias so cmd/replay doesn't import internal/owntracks.
// trips package re-exports a compatible type via trips.OwnTracksPoint.
type replayPoint = trips.OwnTracksPoint

func asOwnTracksPoints(pts []trips.OwnTracksPoint) []trips.OwnTracksPoint {
    return pts
}
```

Note: `trips.OwnTracksPoint` needs to be added as a type alias in `internal/trips/types.go`:
```go
// OwnTracksPoint is an alias for owntracks.Point, exported so cmd/replay
// can scan DB rows without importing internal/owntracks directly.
type OwnTracksPoint = owntracks.Point
```

- [ ] **Step 2: Build to verify it compiles**

```bash
cd /home/sathyabhat/code/autolog
go build ./cmd/replay/...
```
Expected: builds without error.

- [ ] **Step 3: Run against the live DB**

```bash
cd /home/sathyabhat/code/autolog
go run ./cmd/replay --db autolog.db --days 30 | head -40
```
Expected: tab-separated output with one header row and multiple data rows. Inspect whether stay-based segmentation produces different trip counts or mode assignments from baseline.

- [ ] **Step 4: Commit**

```bash
cd /home/sathyabhat/code/autolog
git add cmd/replay/main.go internal/trips/types.go
git commit -m "feat: add replay CLI for per-variant trip comparison"
```

---

## Self-Review

### Spec coverage

| Requirement | Task |
|---|---|
| Speed-anomaly filter | Task 1 |
| Stay-point detection | Task 2 |
| Stay-based segmentation toggle | Task 3 |
| Algorithm flags wired through config + runner | Task 3 |
| Segment-vote mode classification | Task 4 |
| Acceleration-based train gate | Task 4 |
| Walking / cycling modes added | Task 4 |
| Replay CLI for evaluation | Task 5 |
| Baseline preserved when all flags off | Task 3 (runner branch), all defaults false |

### Placeholder scan

None found — all steps include actual code.

### Type consistency

- `AlgorithmFlags` defined in `internal/trips/types.go` (Task 3) and mirrored in `internal/config/config.go` (Task 3).
- `ClassifierConfig.Flags AlgorithmFlags` added in Task 3, consumed in Task 4.
- `trips.OwnTracksPoint` alias added in Task 3 step 1 (`types.go`), used in Task 5.
- `SegmentWithStays` accepts `ClassifierConfig` (Task 3), consistent with Task 5 usage.
- `Stay` struct produced in Task 2, consumed internally in `SegmentWithStays` (Task 3).
