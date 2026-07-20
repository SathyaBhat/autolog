package config_test

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/sathyabhat/autolog/internal/config"
)

func TestLoad_EnvVarsOnly(t *testing.T) {
	t.Setenv("OWNTRACKS_URL", "http://localhost:8083")
	t.Setenv("OWNTRACKS_USER", "bob")
	t.Setenv("OWNTRACKS_DEVICE", "phone")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_CHAT_ID", "456")

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, "http://localhost:8083", cfg.OwnTracks.URL)
	assert.Equal(t, "bob", cfg.OwnTracks.User)
	assert.Equal(t, "phone", cfg.OwnTracks.Device)
	assert.Equal(t, "123:abc", cfg.Telegram.BotToken)
	assert.Equal(t, "456", cfg.Telegram.ChatID)
}

func TestLoad_Defaults(t *testing.T) {
	t.Setenv("OWNTRACKS_URL", "http://localhost:8083")
	t.Setenv("OWNTRACKS_USER", "bob")
	t.Setenv("OWNTRACKS_DEVICE", "phone")
	t.Setenv("TELEGRAM_BOT_TOKEN", "123:abc")
	t.Setenv("TELEGRAM_CHAT_ID", "456")

	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Equal(t, 6*60*60, int(cfg.Scheduler.Interval.Seconds()))
	assert.Equal(t, 150.0, cfg.Filters.MaxTrainSpeedKmh)
}

func TestLoad_MissingRequired(t *testing.T) {
	for _, k := range []string{"OWNTRACKS_URL", "OWNTRACKS_USER", "OWNTRACKS_DEVICE", "TELEGRAM_BOT_TOKEN", "TELEGRAM_CHAT_ID"} {
		os.Unsetenv(k)
	}
	_, err := config.Load("")
	assert.Error(t, err) // owntracks fields are still required
}

func TestLoad_TelegramOptional(t *testing.T) {
	t.Setenv("OWNTRACKS_URL", "http://localhost:8083")
	t.Setenv("OWNTRACKS_USER", "bob")
	t.Setenv("OWNTRACKS_DEVICE", "phone")
	// no TELEGRAM_* set
	cfg, err := config.Load("")
	require.NoError(t, err)
	assert.Empty(t, cfg.Telegram.BotToken)
}
