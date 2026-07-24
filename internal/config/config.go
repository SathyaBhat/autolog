package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	OwnTracks OwnTracksConfig `mapstructure:"owntracks"`
	Telegram  TelegramConfig  `mapstructure:"telegram"`
	Scheduler SchedulerConfig `mapstructure:"scheduler"`
	Filters   FiltersConfig   `mapstructure:"filters"`
	Store     StoreConfig     `mapstructure:"store"`
	Log       LogConfig       `mapstructure:"log"`
}

type OwnTracksConfig struct {
	URL    string `mapstructure:"url"`
	User   string `mapstructure:"user"`
	Device string `mapstructure:"device"`
}

type TelegramConfig struct {
	BotToken        string `mapstructure:"bot_token"`
	ChatID          string `mapstructure:"chat_id"`
	MessageThreadID string `mapstructure:"message_thread_id"`
}

type SchedulerConfig struct {
	Interval time.Duration `mapstructure:"interval"`
}

type FiltersConfig struct {
	MaxTrainSpeedKmh float64         `mapstructure:"max_train_speed_kmh"`
	MinDistanceKm    float64         `mapstructure:"min_distance_km"`
	MaxAccM          float64         `mapstructure:"max_acc_m"`
	ExclusionZones   []ExclusionZone `mapstructure:"exclusion_zones"`
}

type ExclusionZone struct {
	Name    string  `mapstructure:"name"`
	Lat     float64 `mapstructure:"lat"`
	Lon     float64 `mapstructure:"lon"`
	RadiusM float64 `mapstructure:"radius_m"`
}

type StoreConfig struct {
	Path string `mapstructure:"path"`
}

type LogConfig struct {
	Level string `mapstructure:"level"`
}

func Load(cfgFile string) (*Config, error) {
	v := viper.New()

	v.SetDefault("scheduler.interval", 6*time.Hour)
	v.SetDefault("filters.max_train_speed_kmh", 150.0)
	v.SetDefault("filters.min_distance_km", 5.0)
	v.SetDefault("filters.max_acc_m", 100.0)
	v.SetDefault("store.path", "autolog.db")
	v.SetDefault("log.level", "info")

	if cfgFile != "" {
		v.SetConfigFile(cfgFile)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(".")
		v.AddConfigPath("$HOME/.autolog")
		v.AddConfigPath("/etc/autolog")
	}

	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()
	_ = v.BindEnv("owntracks.url", "OWNTRACKS_URL")
	_ = v.BindEnv("owntracks.user", "OWNTRACKS_USER")
	_ = v.BindEnv("owntracks.device", "OWNTRACKS_DEVICE")
	_ = v.BindEnv("telegram.bot_token", "TELEGRAM_BOT_TOKEN")
	_ = v.BindEnv("telegram.chat_id", "TELEGRAM_CHAT_ID")
	_ = v.BindEnv("telegram.message_thread_id", "TELEGRAM_MESSAGE_THREAD_ID")
	_ = v.BindEnv("scheduler.interval", "SCHEDULER_INTERVAL")
	_ = v.BindEnv("filters.max_train_speed_kmh", "FILTERS_MAX_TRAIN_SPEED_KMH")
	_ = v.BindEnv("store.path", "STORE_PATH")
	_ = v.BindEnv("log.level", "LOG_LEVEL")

	if err := v.ReadInConfig(); err != nil {
		if _, ok := err.(viper.ConfigFileNotFoundError); !ok {
			return nil, fmt.Errorf("reading config file: %w", err)
		}
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshalling config: %w", err)
	}

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

func (c *Config) validate() error {
	if c.OwnTracks.URL == "" {
		return fmt.Errorf("owntracks.url (or OWNTRACKS_URL) is required")
	}
	if c.OwnTracks.User == "" {
		return fmt.Errorf("owntracks.user (or OWNTRACKS_USER) is required")
	}
	if c.OwnTracks.Device == "" {
		return fmt.Errorf("owntracks.device (or OWNTRACKS_DEVICE) is required")
	}
	return nil
}
