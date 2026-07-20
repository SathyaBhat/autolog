package notify_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/notify"
	"github.com/sathyabhat/autolog/internal/trips"
)

func TestTelegram_Send(t *testing.T) {
	var gotBody map[string]string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.True(t, strings.HasSuffix(r.URL.Path, "/sendMessage"))
		require.NoError(t, json.NewDecoder(r.Body).Decode(&gotBody))
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	tg := notify.NewTelegramWithBaseURL(srv.URL, "testtoken", "12345")
	trip := trips.Trip{
		Date:       "2026-07-20",
		StartTime:  time.Date(2026, 7, 20, 8, 0, 0, 0, time.UTC),
		EndTime:    time.Date(2026, 7, 20, 9, 30, 0, 0, time.UTC),
		StartLat:   51.5074, StartLon: -0.1278,
		EndLat:     48.8566, EndLon: 2.3522,
		DistanceKm: 341.2,
		Mode:       trips.ModeCar,
	}
	err := tg.Send(context.Background(), trip)
	require.NoError(t, err)
	assert.Equal(t, "12345", gotBody["chat_id"])
	assert.Contains(t, gotBody["text"], "341.2 km")
	assert.Contains(t, gotBody["text"], "2026-07-20")
}
