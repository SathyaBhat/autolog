package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"strconv"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

// zoneFlag implements flag.Value for repeated zone flags.
// --exclude accepts lat,lon,radius_m
// --home accepts lat,lon,radius_m,name
type zoneFlag struct {
	zones   []config.ExclusionZone
	withName bool // true = require a 4th name field
}

func (z *zoneFlag) String() string { return fmt.Sprintf("%v", z.zones) }
func (z *zoneFlag) Set(s string) error {
	if z.withName {
		parts := strings.SplitN(s, ",", 4)
		if len(parts) != 4 {
			return fmt.Errorf("--home requires lat,lon,radius_m,name (e.g. -37.812,144.962,500,Home)")
		}
		lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil { return fmt.Errorf("invalid lat: %v", err) }
		lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil { return fmt.Errorf("invalid lon: %v", err) }
		radius, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil { return fmt.Errorf("invalid radius_m: %v", err) }
		z.zones = append(z.zones, config.ExclusionZone{Name: strings.TrimSpace(parts[3]), Lat: lat, Lon: lon, RadiusM: radius})
	} else {
		parts := strings.SplitN(s, ",", 3)
		if len(parts) != 3 {
			return fmt.Errorf("--exclude requires lat,lon,radius_m (e.g. -37.812,144.962,500)")
		}
		lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil { return fmt.Errorf("invalid lat: %v", err) }
		lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil { return fmt.Errorf("invalid lon: %v", err) }
		radius, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil { return fmt.Errorf("invalid radius_m: %v", err) }
		z.zones = append(z.zones, config.ExclusionZone{Lat: lat, Lon: lon, RadiusM: radius})
	}
	return nil
}

type variant struct {
	name  string
	flags trips.AlgorithmFlags
}

var variants = []variant{
	{"baseline", trips.AlgorithmFlags{}},
	{"anomaly_filter", trips.AlgorithmFlags{AnomalyFilter: true}},
	{"stay_segment", trips.AlgorithmFlags{AnomalyFilter: true, StaySegment: true}},
	{"segment_vote", trips.AlgorithmFlags{SegmentVote: true}},
	{"accel_train_gate", trips.AlgorithmFlags{AccelTrainGate: true}},
	{"all", trips.AlgorithmFlags{AnomalyFilter: true, StaySegment: true, SegmentVote: true, AccelTrainGate: true}},
}

type tripResult struct {
	trip trips.Trip
}

func main() {
	var (
		urlFlag     = flag.String("url", "", "OwnTracks Recorder base URL (env: OWNTRACKS_URL)")
		userFlag    = flag.String("user", "", "OwnTracks user (env: OWNTRACKS_USER)")
		deviceFlag  = flag.String("device", "", "OwnTracks device (env: OWNTRACKS_DEVICE)")
		daysFlag    = flag.Int("days", 30, "Number of past days to fetch")
		flatFlag    = flag.Bool("flat", false, "Print flat variant-per-row output instead of pivot table")
		geocodeFlag = flag.Bool("geocode", false, "Reverse-geocode trip start/end via Nominatim (slow: 1 req/sec)")
		excludes    = zoneFlag{}
		homes       = zoneFlag{withName: true}
	)
	flag.Var(&excludes, "exclude", "Exclusion zone as lat,lon,radius_m (repeatable; env: AUTOLOG_EXCLUSION_ZONES)")
	flag.Var(&homes, "home", "Home zone as lat,lon,radius_m,name (repeatable; env: AUTOLOG_HOME_ZONES)")
	flag.Parse()

	// Env vars (same semicolon-separated format as config) append to flag-provided zones.
	for _, z := range config.ParseZonesEnv("AUTOLOG_EXCLUSION_ZONES") {
		excludes.zones = append(excludes.zones, z)
	}
	for _, z := range config.ParseZonesEnv("AUTOLOG_HOME_ZONES") {
		homes.zones = append(homes.zones, z)
	}

	otURL := resolveString(*urlFlag, "OWNTRACKS_URL")
	otUser := resolveString(*userFlag, "OWNTRACKS_USER")
	otDevice := resolveString(*deviceFlag, "OWNTRACKS_DEVICE")

	var missing []string
	if otURL == "" {
		missing = append(missing, "--url / OWNTRACKS_URL")
	}
	if otUser == "" {
		missing = append(missing, "--user / OWNTRACKS_USER")
	}
	if otDevice == "" {
		missing = append(missing, "--device / OWNTRACKS_DEVICE")
	}
	if len(missing) > 0 {
		for _, m := range missing {
			fmt.Fprintf(os.Stderr, "missing required value: %s\n", m)
		}
		fmt.Fprintf(os.Stderr, "\nUsage: replay --url URL --user USER --device DEVICE [--days N] [--flat]\n")
		os.Exit(1)
	}

	days := *daysFlag
	if days <= 0 {
		days = 30
	}

	now := time.Now().UTC()
	from := now.AddDate(0, 0, -days)

	client := owntracks.New(otURL, otUser, otDevice)
	ctx := context.Background()

	fmt.Fprintf(os.Stderr, "Fetching points from %s to %s (%d days)...\n",
		from.Format("2006-01-02"), now.Format("2006-01-02"), days)

	points, err := client.Fetch(ctx, from, now)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error fetching points: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "Fetched %d points\n", len(points))

	sort.Slice(points, func(i, j int) bool {
		return points[i].Tst < points[j].Tst
	})

	localTZ, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		localTZ = time.UTC
	}

	if len(excludes.zones) > 0 {
		fmt.Fprintf(os.Stderr, "Exclusion zones (%d):\n", len(excludes.zones))
		for _, z := range excludes.zones {
			fmt.Fprintf(os.Stderr, "  %.4f,%.4f  r=%.0fm\n", z.Lat, z.Lon, z.RadiusM)
		}
		fmt.Fprintln(os.Stderr)
	}
	if len(homes.zones) > 0 {
		fmt.Fprintf(os.Stderr, "Home zones (%d):\n", len(homes.zones))
		for _, z := range homes.zones {
			fmt.Fprintf(os.Stderr, "  %s  %.4f,%.4f  r=%.0fm\n", z.Name, z.Lat, z.Lon, z.RadiusM)
		}
		fmt.Fprintln(os.Stderr)
	}

	// Run all variants, collect results keyed by variant name.
	results := make(map[string][]tripResult, len(variants))
	for _, v := range variants {
		cfg := classCfg(v.flags, excludes.zones)
		var rawTrips []trips.RawTrip
		if v.flags.StaySegment {
			rawTrips = trips.SegmentWithStays(points, cfg)
		} else {
			pts := points
			if v.flags.AnomalyFilter {
				pts = trips.FilterAnomalousPoints(pts, 500)
			}
			rawTrips = trips.Segment(pts, 90*time.Minute)
		}
		for _, raw := range rawTrips {
			trip, _, keep := trips.Classify(raw, cfg)
			if !keep {
				continue
			}
			results[v.name] = append(results[v.name], tripResult{trip})
		}
		fmt.Fprintf(os.Stderr, "  %-18s %d trips\n", v.name+":", len(results[v.name]))
	}
	fmt.Fprintln(os.Stderr)

	// Reverse-geocode baseline trips (start, end, and each stop).
	var geo *geocode.Client
	if *geocodeFlag {
		geo = geocode.New()
		totalCalls := 0
		for _, r := range results["baseline"] {
			totalCalls += 2 + len(r.trip.StopPoints)
		}
		fmt.Fprintf(os.Stderr, "Geocoding baseline trips (%d calls, ~%ds)...\n", totalCalls, totalCalls)
		applyHome := func(lat, lon float64, geocoded string) string {
			if label := trips.HomeLabel(lat, lon, homes.zones); label != "" {
				return label
			}
			return geocoded
		}
		for i := range results["baseline"] {
			t := &results["baseline"][i].trip
			if loc, err := geo.Reverse(ctx, t.StartLat, t.StartLon); err == nil {
				t.StartLocation = applyHome(t.StartLat, t.StartLon, loc.Label)
			}
			if loc, err := geo.Reverse(ctx, t.EndLat, t.EndLon); err == nil {
				t.EndLocation = applyHome(t.EndLat, t.EndLon, loc.Label)
			}
			for j := range t.StopPoints {
				if loc, err := geo.Reverse(ctx, t.StopPoints[j].Lat, t.StopPoints[j].Lon); err == nil {
					t.StopPoints[j].Location = applyHome(t.StopPoints[j].Lat, t.StopPoints[j].Lon, loc.Label)
				}
			}
		}
		fmt.Fprintln(os.Stderr)
	}

	if *flatFlag {
		printFlat(results, localTZ, geo != nil)
	} else {
		printPivot(results, localTZ, geo != nil)
	}
}

// classCfg builds a ClassifierConfig for a given flag set and exclusion zones.
func classCfg(flags trips.AlgorithmFlags, zones []config.ExclusionZone) trips.ClassifierConfig {
	return trips.ClassifierConfig{
		MaxTrainSpeedKmh: 150,
		MaxAccM:          100,
		StopGap:          10 * time.Minute,
		Flags:            flags,
		AnomalyMaxKmh:    500,
		StayRadiusM:      50,
		StayMinDur:       5 * time.Minute,
		StayMaxGap:       5 * time.Minute,
		ExclusionZones:   zones,
	}
}

// printFlat prints one row per (variant, trip) — original format.
func printFlat(results map[string][]tripResult, tz *time.Location, withGeo bool) {
	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	if withGeo {
		fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\t#stops\tfrom\tto")
	} else {
		fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\t#stops")
	}
	for _, v := range variants {
		for _, r := range results[v.name] {
			t := r.trip
			if withGeo {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t%s\t%s\n",
					v.name, t.Date,
					t.StartTime.In(tz).Format("15:04"),
					t.EndTime.In(tz).Format("15:04"),
					t.DistanceKm, t.MaxSpeedKmh,
					string(t.Mode), len(t.StopPoints),
					t.StartLocation, t.EndLocation,
				)
			} else {
				fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\n",
					v.name, t.Date,
					t.StartTime.In(tz).Format("15:04"),
					t.EndTime.In(tz).Format("15:04"),
					t.DistanceKm, t.MaxSpeedKmh,
					string(t.Mode), len(t.StopPoints),
				)
			}
		}
	}
	w.Flush()
}

// printPivot groups trips by baseline anchor and prints all variants side-by-side.
// Each baseline trip is one section; variants that produced a matching trip (by
// overlapping time window) are shown inline. Variants with no overlap are listed
// as extra trips after the baseline section.
func printPivot(results map[string][]tripResult, tz *time.Location, withGeo bool) {
	baseline := results["baseline"]

	// Track which non-baseline trips have been matched to a baseline trip.
	matched := make(map[string]map[int]bool)
	for _, v := range variants[1:] {
		matched[v.name] = make(map[int]bool)
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)

	// Header
	if withGeo {
		fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\t#stops\tfrom\tto\tnote")
	} else {
		fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\t#stops\tnote")
	}

	for _, br := range baseline {
		bt := br.trip
		bStart := bt.StartTime.Unix()
		bEnd := bt.EndTime.Unix()

		// Print baseline row.
		if withGeo {
			fmt.Fprintf(w, "baseline\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t%s\t%s\t\n",
				bt.Date,
				bt.StartTime.In(tz).Format("15:04"),
				bt.EndTime.In(tz).Format("15:04"),
				bt.DistanceKm, bt.MaxSpeedKmh,
				string(bt.Mode), len(bt.StopPoints),
				bt.StartLocation, bt.EndLocation,
			)
		} else {
			fmt.Fprintf(w, "baseline\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t\n",
				bt.Date,
				bt.StartTime.In(tz).Format("15:04"),
				bt.EndTime.In(tz).Format("15:04"),
				bt.DistanceKm, bt.MaxSpeedKmh,
				string(bt.Mode), len(bt.StopPoints),
			)
		}

		// Print intermediate stops for this baseline trip.
		for _, sp := range bt.StopPoints {
			arr := time.Unix(sp.ArrivalTst, 0).In(tz).Format("15:04")
			dep := time.Unix(sp.DepartureTst, 0).In(tz).Format("15:04")
			durMin := (sp.DepartureTst - sp.ArrivalTst) / 60
			loc := sp.Location
			if loc == "" {
				loc = fmt.Sprintf("%.4f,%.4f", sp.Lat, sp.Lon)
			}
			if withGeo {
				fmt.Fprintf(w, "  stop\t\t%s\t%s\t\t\t\t\t%s\t\t%dm pause\n", arr, dep, loc, durMin)
			} else {
				fmt.Fprintf(w, "  stop\t\t%s\t%s\t\t\t\t\t%dm pause @ %s\n", arr, dep, durMin, loc)
			}
		}

		// For each other variant, find trips that overlap with this baseline trip.
		for _, v := range variants[1:] {
			var overlapping []tripResult
			var overlapIdx []int
			for i, r := range results[v.name] {
				s := r.trip.StartTime.Unix()
				e := r.trip.EndTime.Unix()
				// Overlap: intervals [bStart,bEnd] and [s,e] share any time.
				if s <= bEnd && e >= bStart {
					overlapping = append(overlapping, r)
					overlapIdx = append(overlapIdx, i)
				}
			}

			if len(overlapping) == 0 {
				// Variant produced nothing for this time window.
				if withGeo {
					fmt.Fprintf(w, "  %s\t\t\t\t\t\t\t\t\t\t(no trip)\n", v.name)
				} else {
					fmt.Fprintf(w, "  %s\t\t\t\t\t\t\t\t(no trip)\n", v.name)
				}
				continue
			}

			for idx, r := range overlapping {
				t := r.trip
				note := ""
				if string(t.Mode) != string(bt.Mode) {
					note = fmt.Sprintf("mode: %s→%s", bt.Mode, t.Mode)
				}
				if len(overlapping) > 1 && idx == 0 {
					note = fmt.Sprintf("split into %d trips", len(overlapping))
				} else if len(overlapping) > 1 && idx > 0 {
					note = "  └─ part"
				}
				if withGeo {
					fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t\t\t%s\n",
						v.name, t.Date,
						t.StartTime.In(tz).Format("15:04"),
						t.EndTime.In(tz).Format("15:04"),
						t.DistanceKm, t.MaxSpeedKmh,
						string(t.Mode), len(t.StopPoints),
						note,
					)
				} else {
					fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t%s\n",
						v.name, t.Date,
						t.StartTime.In(tz).Format("15:04"),
						t.EndTime.In(tz).Format("15:04"),
						t.DistanceKm, t.MaxSpeedKmh,
						string(t.Mode), len(t.StopPoints),
						note,
					)
				}
				matched[v.name][overlapIdx[idx]] = true
			}
		}
		if withGeo {
			fmt.Fprintln(w, "\t\t\t\t\t\t\t\t\t\t") // blank separator
		} else {
			fmt.Fprintln(w, "\t\t\t\t\t\t\t\t") // blank separator
		}
	}

	// Print any non-baseline trips not matched to a baseline trip.
	anyExtra := false
	for _, v := range variants[1:] {
		for i, r := range results[v.name] {
			if matched[v.name][i] {
				continue
			}
			if !anyExtra {
				if withGeo {
					fmt.Fprintln(w, "--- extra trips (no baseline match) ---\t\t\t\t\t\t\t\t\t\t")
				} else {
					fmt.Fprintln(w, "--- extra trips (no baseline match) ---\t\t\t\t\t\t\t\t")
				}
				anyExtra = true
			}
			t := r.trip
			if withGeo {
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t\t\t(no baseline overlap)\n",
					v.name, t.Date,
					t.StartTime.In(tz).Format("15:04"),
					t.EndTime.In(tz).Format("15:04"),
					t.DistanceKm, t.MaxSpeedKmh,
					string(t.Mode), len(t.StopPoints),
				)
			} else {
				fmt.Fprintf(w, "  %s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\t(no baseline overlap)\n",
					v.name, t.Date,
					t.StartTime.In(tz).Format("15:04"),
					t.EndTime.In(tz).Format("15:04"),
					t.DistanceKm, t.MaxSpeedKmh,
					string(t.Mode), len(t.StopPoints),
				)
			}
		}
	}

	// Print summary counts.
	if withGeo {
		fmt.Fprintln(w, "\t\t\t\t\t\t\t\t\t\t")
		fmt.Fprintln(w, "--- trip counts per variant ---\t\t\t\t\t\t\t\t\t\t")
	} else {
		fmt.Fprintln(w, "\t\t\t\t\t\t\t\t")
		fmt.Fprintln(w, "--- trip counts per variant ---\t\t\t\t\t\t\t\t")
	}
	for _, v := range variants {
		fmt.Fprintf(w, "  %s\t%d trips\t\t\t\t\t\t\t\n", v.name, len(results[v.name]))
	}

	w.Flush()

	// Print a mode-change summary to stderr.
	fmt.Fprintln(os.Stderr, "Mode changes vs baseline:")
	for _, v := range variants[1:] {
		changes := 0
		for _, r := range results[v.name] {
			bt := findBaselineMatch(baseline, r.trip)
			if bt != nil && string(bt.Mode) != string(r.trip.Mode) {
				changes++
			}
		}
		if changes > 0 {
			fmt.Fprintf(os.Stderr, "  %-18s %d mode change(s)\n", v.name+":", changes)
		}
	}
}

// findBaselineMatch returns the baseline trip that best overlaps with t, or nil.
func findBaselineMatch(baseline []tripResult, t trips.Trip) *trips.Trip {
	s := t.StartTime.Unix()
	e := t.EndTime.Unix()
	for _, br := range baseline {
		bs := br.trip.StartTime.Unix()
		be := br.trip.EndTime.Unix()
		if s <= be && e >= bs {
			trip := br.trip
			return &trip
		}
	}
	return nil
}

// resolveString returns flagVal if non-empty, otherwise the env var value.
func resolveString(flagVal, envKey string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envKey)
}
