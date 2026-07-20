package config

import (
	"testing"
	"time"
)

func TestLoadDefaults(t *testing.T) {
	t.Setenv("GOLDAPI_KEY", "k")
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

func TestLoadRequiresKey(t *testing.T) {
	t.Setenv("GOLDAPI_KEY", "")
	if _, err := Load(); err == nil {
		t.Fatal("expected error when GOLDAPI_KEY is missing")
	}
}

func TestLoadAppliesOverrides(t *testing.T) {
	t.Setenv("GOLDAPI_KEY", "k")
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
			t.Setenv("GOLDAPI_KEY", "k")
			t.Setenv("POLL_INTERVAL", bad)
			if _, err := Load(); err == nil {
				t.Fatalf("expected error for POLL_INTERVAL=%q", bad)
			}
		})
	}
}
