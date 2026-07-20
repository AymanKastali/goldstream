// Package config loads runtime settings from the environment (12-factor).
package config

import (
	"errors"
	"fmt"
	"os"
	"time"
)

// Defaults applied when the corresponding env var is unset. The timeouts are
// fixed operational settings (not env-overridable) — they live here so all
// configuration sits in one place rather than leaking into main.
const (
	defaultPort            = "8080"
	defaultPollInterval    = 60 * time.Second
	defaultFetchTimeout    = 8 * time.Second
	defaultHTTPTimeout     = 10 * time.Second
	defaultShutdownTimeout = 5 * time.Second
)

// Config holds everything the service needs at boot.
type Config struct {
	GoldAPIKey      string
	Port            string
	PollInterval    time.Duration
	FetchTimeout    time.Duration // bounds a single upstream price fetch
	HTTPTimeout     time.Duration // whole-request timeout for the goldapi client
	ShutdownTimeout time.Duration // grace period for in-flight requests on shutdown
}

// Load reads config from the environment and fails fast on missing or invalid
// input, so the service never starts half-configured.
func Load() (Config, error) {
	key := os.Getenv("GOLDAPI_KEY")
	if key == "" {
		return Config{}, errors.New("GOLDAPI_KEY is required")
	}

	cfg := Config{
		GoldAPIKey:      key,
		Port:            defaultPort,
		PollInterval:    defaultPollInterval,
		FetchTimeout:    defaultFetchTimeout,
		HTTPTimeout:     defaultHTTPTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
	}
	if port := os.Getenv("PORT"); port != "" {
		cfg.Port = port
	}
	if raw := os.Getenv("POLL_INTERVAL"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("invalid POLL_INTERVAL %q: must be a positive duration", raw)
		}
		cfg.PollInterval = d
	}
	return cfg, nil
}
