// Package config loads runtime settings from the environment (12-factor).
package config

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
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
	defaultReconnectDelay  = 3 * time.Second
	defaultLogLevel        = slog.LevelDebug // default debug so the full flow prints out of the box
)

// Config holds everything the service needs at boot.
type Config struct {
	Port            string
	PollInterval    time.Duration
	FetchTimeout    time.Duration // bounds a single upstream price fetch
	HTTPTimeout     time.Duration // whole-request timeout for the goldapi client
	ShutdownTimeout time.Duration // grace period for in-flight requests on shutdown
	ReconnectDelay  time.Duration // how long the browser waits before reconnecting (SSE "retry:")
	LogLevel        slog.Level    // minimum log level; debug traces every step of the flow
}

// Load reads config from the environment and fails fast on invalid input, so
// the service never starts half-configured. The upstream (gold-api.com) is
// keyless, so no secret is required.
func Load() (Config, error) {
	cfg := Config{
		Port:            defaultPort,
		PollInterval:    defaultPollInterval,
		FetchTimeout:    defaultFetchTimeout,
		HTTPTimeout:     defaultHTTPTimeout,
		ShutdownTimeout: defaultShutdownTimeout,
		ReconnectDelay:  defaultReconnectDelay,
		LogLevel:        defaultLogLevel,
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
	if raw := os.Getenv("RECONNECT_DELAY"); raw != "" {
		d, err := time.ParseDuration(raw)
		if err != nil || d <= 0 {
			return Config{}, fmt.Errorf("invalid RECONNECT_DELAY %q: must be a positive duration", raw)
		}
		cfg.ReconnectDelay = d
	}
	if raw := os.Getenv("LOG_LEVEL"); raw != "" {
		switch strings.ToLower(raw) {
		case "debug":
			cfg.LogLevel = slog.LevelDebug
		case "info":
			cfg.LogLevel = slog.LevelInfo
		case "warn":
			cfg.LogLevel = slog.LevelWarn
		case "error":
			cfg.LogLevel = slog.LevelError
		default:
			return Config{}, fmt.Errorf("invalid LOG_LEVEL %q: want debug|info|warn|error", raw)
		}
	}
	return cfg, nil
}
