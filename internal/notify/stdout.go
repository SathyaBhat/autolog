package notify

import (
	"context"
	"fmt"

	"github.com/sathyabhat/autolog/internal/trips"
)

// Stdout prints trip notifications to standard output instead of Telegram.
type Stdout struct{}

func (s *Stdout) Send(_ context.Context, t trips.Trip) error {
	fmt.Printf("🚗 %s: %.4f,%.4f → %.4f,%.4f, %.1f km\n",
		t.Date,
		t.StartLat, t.StartLon,
		t.EndLat, t.EndLon,
		t.DistanceKm,
	)
	return nil
}
