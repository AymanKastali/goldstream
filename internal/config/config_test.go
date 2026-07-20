package config

import (
	"log/slog"
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("PORT", "")
	t.Setenv("POLL_INTERVAL", "")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "8080" {
		t.Errorf("Port = %q want 8080", cfg.Port)
	}
	if cfg.PollInterval != 60*time.Second {
		t.Errorf("PollInterval = %v want 60s", cfg.PollInterval)
	}
	if cfg.FetchTimeout != 8*time.Second {
		t.Errorf("FetchTimeout = %v want 8s", cfg.FetchTimeout)
	}
	if cfg.HTTPTimeout != 10*time.Second {
		t.Errorf("HTTPTimeout = %v want 10s", cfg.HTTPTimeout)
	}
	if cfg.ShutdownTimeout != 5*time.Second {
		t.Errorf("ShutdownTimeout = %v want 5s", cfg.ShutdownTimeout)
	}
}

func TestLoadAppliesOverrides(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("POLL_INTERVAL", "30s")

	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Port != "9090" {
		t.Errorf("Port = %q want 9090", cfg.Port)
	}
	if cfg.PollInterval != 30*time.Second {
		t.Errorf("PollInterval = %v want 30s", cfg.PollInterval)
	}
}

func TestLoadRejectsBadPollInterval(t *testing.T) {
	for _, bad := range []string{"abc", "0s", "-5s"} {
		t.Run(bad, func(t *testing.T) {
			t.Setenv("POLL_INTERVAL", bad)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for POLL_INTERVAL=%q", bad)
			}
		})
	}
}

func TestLoadRejectsBadLogLevel(t *testing.T) {
	t.Setenv("LOG_LEVEL", "bogus")
	if _, err := Load(); err == nil {
		t.Fatal("expected error for invalid LOG_LEVEL")
	}
}

func TestLoadDefaultsForNewFields(t *testing.T) {
	t.Setenv("RECONNECT_DELAY", "")
	t.Setenv("LOG_LEVEL", "")
	cfg, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if cfg.ReconnectDelay != 3*time.Second {
		t.Errorf("ReconnectDelay = %v want 3s", cfg.ReconnectDelay)
	}
	if cfg.LogLevel != slog.LevelDebug {
		t.Errorf("LogLevel = %v want debug", cfg.LogLevel)
	}
}

func TestLoadMapsLogLevel(t *testing.T) {
	cases := map[string]slog.Level{
		"debug": slog.LevelDebug,
		"info":  slog.LevelInfo,
		"warn":  slog.LevelWarn,
		"error": slog.LevelError,
		"WARN":  slog.LevelWarn, // case-insensitive
	}
	for raw, want := range cases {
		t.Run(raw, func(t *testing.T) {
			t.Setenv("LOG_LEVEL", raw)
			cfg, err := Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.LogLevel != want {
				t.Errorf("LOG_LEVEL=%q → %v want %v", raw, cfg.LogLevel, want)
			}
		})
	}
}
