package config

import (
	"strings"
	"testing"
)

// The 2026-09-03 audit's config findings: values Load accepted that could
// never work, each now refused on the ground with the variable named.
func TestLoad_refusesValuesThatCannotWork(t *testing.T) {
	t.Setenv("MOCKER_ADMIN_HOST", "mocker.local")
	t.Setenv("MOCKER_BASE_DOMAIN", "mock.local")
	t.Setenv("MOCKER_SHARED_PASSWORD_HASH", "x")
	for _, tc := range []struct {
		name string
		env  map[string]string
		want string
	}{
		{"reserved prefix that trims to empty", map[string]string{"MOCKER_RESERVED_PREFIX": "//"}, "MOCKER_RESERVED_PREFIX"},
		{"reserved prefix with an empty segment", map[string]string{"MOCKER_RESERVED_PREFIX": "/a//b"}, "MOCKER_RESERVED_PREFIX"},
		{"admin host with a port", map[string]string{"MOCKER_ADMIN_HOST": "mocker.local:8080"}, "MOCKER_ADMIN_HOST must be a bare host"},
		{"base domain with a scheme", map[string]string{"MOCKER_BASE_DOMAIN": "http://mock.local"}, "MOCKER_BASE_DOMAIN must be a bare host"},
		{"zero response cap", map[string]string{"MOCKER_MAX_RESPONSE": "0"}, "MOCKER_MAX_RESPONSE must be at least 1kb"},
		{"tiny traffic body cap", map[string]string{"MOCKER_TRAFFIC_MAX_BODY": "100"}, "MOCKER_TRAFFIC_MAX_BODY must be at least 1kb"},
		{"size that overflows", map[string]string{"MOCKER_MAX_BODY": "9000000000000000000kb"}, "MOCKER_MAX_BODY"},
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
	t.Setenv("MOCKER_RESERVED_PREFIX", "/__m/")
	cfg, err := Load()
	if err != nil || cfg.ReservedPrefix != "/__m" {
		t.Fatalf("trailing slash: cfg.ReservedPrefix = %q, err = %v; want /__m", cfg.ReservedPrefix, err)
	}
}

func TestParseSize_overflowIsAnError(t *testing.T) {
	t.Parallel()
	if n, err := ParseSize("9000000000000000000kb"); err == nil {
		t.Fatalf("ParseSize overflow = %d, want an error", n)
	}
	if n, err := ParseSize("8589934591gb"); err != nil || n <= 0 {
		t.Fatalf("ParseSize(largest gb that fits) = %d, %v", n, err)
	}
}
