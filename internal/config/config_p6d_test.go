package config_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// TestLoad_p6dStreamVariables is decisions.md mocker-p6d-websocket D5/A4:
// the three WebSocket variables' defaults, their floors, and the origin
// list's element validation — a malformed origin fails startup naming the
// variable rather than silently never matching.
func TestLoad_p6dStreamVariables(t *testing.T) {
	t.Run("defaults", func(t *testing.T) {
		setBaseEnv(t)
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}
		if cfg.StreamMaxFrame != 64<<10 || cfg.StreamSendBudget != 256<<10 || len(cfg.StreamOrigins) != 0 {
			t.Errorf("defaults = %d/%d/%v, want 65536/262144/[]", cfg.StreamMaxFrame, cfg.StreamSendBudget, cfg.StreamOrigins)
		}
	})
	for _, key := range []string{"MOCKER_STREAM_MAX_FRAME", "MOCKER_STREAM_SEND_BUDGET"} {
		t.Run(key+" below 1kb refused", func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv(key, "512")
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), key) {
				t.Fatalf("Load() error = %v, want a refusal naming %s", err, key)
			}
		})
	}
	t.Run("origins are normalised", func(t *testing.T) {
		setBaseEnv(t)
		t.Setenv("MOCKER_STREAM_ORIGINS", "HTTPS://Allowed.Example, http://other.example:8080")
		cfg, err := config.Load()
		if err != nil {
			t.Fatalf("Load() error = %v, want nil", err)
		}
		if len(cfg.StreamOrigins) != 2 || cfg.StreamOrigins[0] != "https://allowed.example" || cfg.StreamOrigins[1] != "http://other.example:8080" {
			t.Errorf("origins = %v, want lower-cased scheme://host[:port]", cfg.StreamOrigins)
		}
	})
	for _, bad := range []string{"not-a-url", "allowed.example", "https://allowed.example/path", "ftp://x.example", "https://u:p@x.example"} {
		t.Run("origin "+bad+" refused", func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("MOCKER_STREAM_ORIGINS", bad)
			if _, err := config.Load(); err == nil || !strings.Contains(err.Error(), "MOCKER_STREAM_ORIGINS") {
				t.Fatalf("Load() error = %v, want a refusal naming MOCKER_STREAM_ORIGINS", err)
			}
		})
	}
}
