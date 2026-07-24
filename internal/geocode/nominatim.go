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
	gs      GeoStore
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
