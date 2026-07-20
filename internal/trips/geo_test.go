package trips_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/sathyabhat/autolog/internal/config"
	"github.com/sathyabhat/autolog/internal/trips"
)

func TestHaversineKm(t *testing.T) {
	// London to Paris ≈ 341 km
	km := trips.HaversineKm(51.5074, -0.1278, 48.8566, 2.3522)
	assert.InDelta(t, 341.0, km, 5.0)
}

func TestHaversineKm_SamePoint(t *testing.T) {
	km := trips.HaversineKm(51.5, -0.1, 51.5, -0.1)
	assert.Equal(t, 0.0, km)
}

func TestInExclusionZone_Inside(t *testing.T) {
	zones := []config.ExclusionZone{
		{Name: "home", Lat: 51.5, Lon: -0.1, RadiusM: 500},
	}
	assert.True(t, trips.InExclusionZone(51.5, -0.1, zones))
}

func TestInExclusionZone_Outside(t *testing.T) {
	zones := []config.ExclusionZone{
		{Name: "home", Lat: 51.5, Lon: -0.1, RadiusM: 100},
	}
	// ~1 km away
	assert.False(t, trips.InExclusionZone(51.509, -0.1, zones))
}

func TestInExclusionZone_Empty(t *testing.T) {
	assert.False(t, trips.InExclusionZone(51.5, -0.1, nil))
}
