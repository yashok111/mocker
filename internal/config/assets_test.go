package config

import (
	"strings"
	"testing"
)

// A6 (A3): the two asset variables and their three relations.
func TestLoad_assets(t *testing.T) {
	t.Setenv("MOCKER_ADMIN_HOST", "mocker.local")
	t.Setenv("MOCKER_BASE_DOMAIN", "mock.local")
	t.Setenv("MOCKER_SHARED_PASSWORD_HASH", "x")

	cfg, err := Load()
	if err != nil {
		t.Fatalf("defaults: %v", err)
	}
	if cfg.MaxAsset != 8<<20 || cfg.MaxAssetsTotal != 64<<20 {
		t.Fatalf("defaults: MaxAsset=%d MaxAssetsTotal=%d", cfg.MaxAsset, cfg.MaxAssetsTotal)
	}

	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"per-file cap above the body limit", map[string]string{"MOCKER_MAX_ASSET": "20mb"}, "must not exceed MOCKER_MAX_BODY"},
		{"quota below the per-file cap", map[string]string{"MOCKER_MAX_ASSET": "4mb", "MOCKER_MAX_ASSETS_TOTAL": "2mb"}, "must not be below MOCKER_MAX_ASSET"},
		{"per-file cap below the floor", map[string]string{"MOCKER_MAX_ASSET": "512", "MOCKER_MAX_ASSETS_TOTAL": "1mb"}, "at least 1kb"},
		{"unparseable", map[string]string{"MOCKER_MAX_ASSET": "big"}, "MOCKER_MAX_ASSET"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			_, err := Load()
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want one containing %q", err, tc.want)
			}
		})
	}

	t.Setenv("MOCKER_MAX_ASSET", "2mb")
	t.Setenv("MOCKER_MAX_ASSETS_TOTAL", "2mb")
	if cfg, err := Load(); err != nil || cfg.MaxAsset != 2<<20 || cfg.MaxAssetsTotal != 2<<20 {
		t.Fatalf("equal caps must load: %+v %v", cfg, err)
	}
}
