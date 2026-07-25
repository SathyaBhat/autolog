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

func (s *Stdout) SendAll(_ context.Context, ts []trips.Trip) error {
	fmt.Println("🚙 New trips logged:")
	for _, t := range ts {
		startSyd := t.StartTime.In(sydneyTZ)
		fmt.Printf("  • %s: %s, %.1f km\n", startSyd.Format("02 Jan 2006 15:04"), formatRoute(t), t.DistanceKm)
	}
	return nil
}
