package geocode_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/geocode"
)

type fakeGeoStore struct {
	data map[string]string
}

func newFakeGeoStore() *fakeGeoStore {
	return &fakeGeoStore{data: make(map[string]string)}
}

func storeKey(lat, lon float64) string {
	return fmt.Sprintf("%.4f,%.4f", lat, lon)
}

func (f *fakeGeoStore) GetGeocode(_ context.Context, lat, lon float64) (string, bool, error) {
	v, ok := f.data[storeKey(lat, lon)]
	return v, ok, nil
}

func (f *fakeGeoStore) SaveGeocode(_ context.Context, lat, lon float64, label string) error {
	f.data[storeKey(lat, lon)] = label
	return nil
}

func TestClient_WithStore_CachesInDB(t *testing.T) {
	var apiCalls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiCalls.Add(1)
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintln(w, `{"address":{"road":"Test Road","suburb":"Test Suburb"}}`)
	}))
	defer srv.Close()

	gs := newFakeGeoStore()
	c := geocode.NewWithBaseURL(srv.URL).WithStore(gs)

	loc1, err := c.Reverse(context.Background(), 51.5074, -0.1278)
	require.NoError(t, err)
	assert.Equal(t, "Test Road, Test Suburb", loc1.Label)
	assert.Equal(t, int32(1), apiCalls.Load())

	// Second call for same coords: should hit DB store, not Nominatim
	loc2, err := c.Reverse(context.Background(), 51.5074, -0.1278)
	require.NoError(t, err)
	assert.Equal(t, "Test Road, Test Suburb", loc2.Label)
	assert.Equal(t, int32(1), apiCalls.Load(), "Nominatim should not be called again")
}
