package owntracks_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/owntracks"
)

func TestClient_Fetch(t *testing.T) {
	pts := []owntracks.Point{
		{
			Tst:              1700000000,
			Lat:              51.5,
			Lon:              -0.1,
			Vel:              30,
			Acc:              10,
			Cog:              275,
			Tag:              "drive",
			MotionActivities: []string{"automotive"},
		},
		{Tst: 1700000060, Lat: 51.51, Lon: -0.11, Vel: 32, Acc: 8},
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/api/0/locations", r.URL.Path)
		assert.Equal(t, "testuser", r.URL.Query().Get("user"))
		assert.Equal(t, "testdevice", r.URL.Query().Get("device"))
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`, r.URL.Query().Get("from"))
		assert.Regexp(t, `^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}$`, r.URL.Query().Get("to"))
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": pts})
	}))
	defer srv.Close()

	client := owntracks.New(srv.URL, "testuser", "testdevice")
	from := time.Unix(1700000000, 0)
	to := time.Unix(1700003600, 0)
	got, err := client.Fetch(context.Background(), from, to)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, int64(1700000000), got[0].Tst)
	assert.Equal(t, 51.5, got[0].Lat)
	assert.Equal(t, 275.0, got[0].Cog)
	assert.Equal(t, "drive", got[0].Tag)
	assert.Equal(t, []string{"automotive"}, got[0].MotionActivities)
}

func TestClient_Fetch_EmptyResponse(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]interface{}{"data": []owntracks.Point{}})
	}))
	defer srv.Close()

	client := owntracks.New(srv.URL, "u", "d")
	got, err := client.Fetch(context.Background(), time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	assert.Empty(t, got)
}
