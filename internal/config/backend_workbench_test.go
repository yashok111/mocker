package config_test

import (
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

// TestLoad_BackendWorkbenchFrozenByDefault pins the 2026-10-12 freeze: an
// environment that says nothing about MOCKER_BACKEND_WORKBENCH gets a process
// with the workbench hidden, and only an explicit truthy value thaws it.
func TestLoad_BackendWorkbenchFrozenByDefault(t *testing.T) {
	for _, tc := range []struct {
		raw    string
		frozen bool
	}{
		{"", true},
		{"0", true},
		{"off", true},
		{"1", false},
		{"true", false},
	} {
		t.Run(tc.raw, func(t *testing.T) {
			setBaseEnv(t)
			t.Setenv("MOCKER_BACKEND_WORKBENCH", tc.raw)
			cfg, err := config.Load()
			if err != nil {
				t.Fatal(err)
			}
			if cfg.BackendWorkbenchFrozen != tc.frozen {
				t.Fatalf("MOCKER_BACKEND_WORKBENCH=%q: frozen = %v, want %v", tc.raw, cfg.BackendWorkbenchFrozen, tc.frozen)
			}
		})
	}
}
