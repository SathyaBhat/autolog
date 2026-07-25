package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/sathyabhat/autolog/internal/owntracks"
	"github.com/sathyabhat/autolog/internal/trips"
)

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

func main() {
	var (
		urlFlag    = flag.String("url", "", "OwnTracks Recorder base URL (env: OWNTRACKS_URL)")
		userFlag   = flag.String("user", "", "OwnTracks user (env: OWNTRACKS_USER)")
		deviceFlag = flag.String("device", "", "OwnTracks device (env: OWNTRACKS_DEVICE)")
		daysFlag   = flag.Int("days", 30, "Number of past days to fetch")
	)
	flag.Parse()

	// Resolve values: flag > env var
	otURL := resolveString(*urlFlag, "OWNTRACKS_URL")
	otUser := resolveString(*userFlag, "OWNTRACKS_USER")
	otDevice := resolveString(*deviceFlag, "OWNTRACKS_DEVICE")

	missing := []string{}
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
		fmt.Fprintf(os.Stderr, "\nUsage: replay --url URL --user USER --device DEVICE [--days N]\n")
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

	// Sort by Tst ascending.
	sort.Slice(points, func(i, j int) bool {
		return points[i].Tst < points[j].Tst
	})

	// Load local timezone; fall back to UTC on error.
	localTZ, err := time.LoadLocation("Australia/Sydney")
	if err != nil {
		localTZ = time.UTC
	}

	w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
	fmt.Fprintln(w, "variant\tdate\tstart\tend\tdist_km\tmax_spd\tmode\t#stops")

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
			ExclusionZones:   nil,
		}

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
			fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%.1f\t%.0f\t%s\t%d\n",
				v.name,
				trip.Date,
				trip.StartTime.In(localTZ).Format("15:04"),
				trip.EndTime.In(localTZ).Format("15:04"),
				trip.DistanceKm,
				trip.MaxSpeedKmh,
				string(trip.Mode),
				len(trip.StopPoints),
			)
		}
	}

	w.Flush()
}

// resolveString returns flagVal if non-empty, otherwise the env var value.
func resolveString(flagVal, envKey string) string {
	if flagVal != "" {
		return flagVal
	}
	return os.Getenv(envKey)
}
