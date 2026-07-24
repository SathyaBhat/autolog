package notify

import (
	"context"
	"fmt"

	"github.com/sathyabhat/autolog/internal/geocode"
	"github.com/sathyabhat/autolog/internal/trips"
)

// Stdout prints trip notifications to standard output instead of Telegram.
type Stdout struct {
	geo *geocode.Client
}

// NewStdout creates a Stdout notifier. geo may be nil to skip reverse geocoding.
func NewStdout(geo *geocode.Client) *Stdout {
	return &Stdout{geo: geo}
}

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
