package config

import (
	"fmt"
	"os"
	"strconv"
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

// AlgorithmFlags mirrors trips.AlgorithmFlags for YAML/env configuration.
type AlgorithmFlags struct {
	AnomalyFilter  bool `mapstructure:"anomaly_filter"`
	StaySegment    bool `mapstructure:"stay_segment"`
	SegmentVote    bool `mapstructure:"segment_vote"`
	AccelTrainGate bool `mapstructure:"accel_train_gate"`
}

type FiltersConfig struct {
	MaxTrainSpeedKmh float64         `mapstructure:"max_train_speed_kmh"`
	MinDistanceKm    float64         `mapstructure:"min_distance_km"`
	MaxAccM          float64         `mapstructure:"max_acc_m"`
	MaxTripGap       time.Duration   `mapstructure:"max_trip_gap"`
	StopGap          time.Duration   `mapstructure:"stop_gap"`
	ExclusionZones   []ExclusionZone `mapstructure:"exclusion_zones"`
	HomeZones        []ExclusionZone `mapstructure:"home_zones"`
	AlgorithmFlags   AlgorithmFlags  `mapstructure:"algorithm_flags"`
	StayRadiusM      float64         `mapstructure:"stay_radius_m"`
	StayMinDur       time.Duration   `mapstructure:"stay_min_dur"`
	StayMaxGap       time.Duration   `mapstructure:"stay_max_gap"`
	AnomalyMaxKmh    float64         `mapstructure:"anomaly_max_kmh"`
	TransitGap       time.Duration   `mapstructure:"transit_gap"`
	TransitMinDistKm float64         `mapstructure:"transit_min_dist_km"`
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
	v.SetDefault("filters.max_trip_gap", 90*time.Minute)
	v.SetDefault("filters.stop_gap", 10*time.Minute)
	v.SetDefault("filters.transit_gap", 5*time.Minute)
	v.SetDefault("filters.transit_min_dist_km", 5.0)
	v.SetDefault("store.path", "autolog.db")
	v.SetDefault("log.level", "info")
	v.SetDefault("filters.stay_radius_m", 50.0)
	v.SetDefault("filters.stay_min_dur", 5*time.Minute)
	v.SetDefault("filters.stay_max_gap", 5*time.Minute)
	v.SetDefault("filters.anomaly_max_kmh", 500.0)

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
	_ = v.BindEnv("filters.transit_gap", "FILTERS_TRANSIT_GAP")
	_ = v.BindEnv("filters.transit_min_dist_km", "FILTERS_TRANSIT_MIN_DIST_KM")
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

	// Env vars append to (not replace) YAML-configured zones.
	cfg.Filters.ExclusionZones = append(cfg.Filters.ExclusionZones, ParseZonesEnv("AUTOLOG_EXCLUSION_ZONES")...)
	cfg.Filters.HomeZones = append(cfg.Filters.HomeZones, ParseZonesEnv("AUTOLOG_HOME_ZONES")...)

	if err := cfg.validate(); err != nil {
		return nil, err
	}

	return &cfg, nil
}

// ParseZonesEnv parses a semicolon-separated list of "lat,lon,radius_m,name" entries
// from an env var. Entries without a name field are also accepted (name defaults to "").
// Malformed entries are silently skipped.
func ParseZonesEnv(envKey string) []ExclusionZone {
	raw := os.Getenv(envKey)
	if raw == "" {
		return nil
	}
	var zones []ExclusionZone
	for _, entry := range strings.Split(raw, ";") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		parts := strings.SplitN(entry, ",", 4)
		if len(parts) < 3 {
			continue
		}
		lat, err := strconv.ParseFloat(strings.TrimSpace(parts[0]), 64)
		if err != nil {
			continue
		}
		lon, err := strconv.ParseFloat(strings.TrimSpace(parts[1]), 64)
		if err != nil {
			continue
		}
		radius, err := strconv.ParseFloat(strings.TrimSpace(parts[2]), 64)
		if err != nil {
			continue
		}
		name := ""
		if len(parts) == 4 {
			name = strings.TrimSpace(parts[3])
		}
		zones = append(zones, ExclusionZone{Name: name, Lat: lat, Lon: lon, RadiusM: radius})
	}
	return zones
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
