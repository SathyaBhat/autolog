package owntracks

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client fetches location history from an OwnTracks Recorder instance.
type Client struct {
	baseURL string
	user    string
	device  string
	http    *http.Client
}

// New creates a new Client. baseURL should be e.g. "https://host/otrecorder".
func New(baseURL, user, device string) *Client {
	return &Client{
		baseURL: baseURL,
		user:    user,
		device:  device,
		http:    &http.Client{Timeout: 30 * time.Second},
	}
}

// Fetch returns all location points recorded between from and to (inclusive).
func (c *Client) Fetch(ctx context.Context, from, to time.Time) ([]Point, error) {
	u, err := url.Parse(c.baseURL + "/api/0/locations")
	if err != nil {
		return nil, fmt.Errorf("parse base url: %w", err)
	}
	q := u.Query()
	q.Set("user", c.user)
	q.Set("device", c.device)
	q.Set("from", strconv.FormatInt(from.Unix(), 10))
	q.Set("to", strconv.FormatInt(to.Unix(), 10))
	u.RawQuery = q.Encode()

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch locations: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("unexpected status %d from OwnTracks Recorder", resp.StatusCode)
	}

	var envelope response
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return envelope.Data, nil
}
