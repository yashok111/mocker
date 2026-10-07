package config_test

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

func TestLoadTestTargets(t *testing.T) {
	setBaseEnv(t)
	valid := `{"id":"orders","version":1,"origin":"http://127.0.0.1:9191","allowedIPs":["127.0.0.1"],"credentialRef":"ORDERS_REFERENCE_TOKEN","isolationId":"11111111-1111-4111-8111-111111111111"}`
	for _, tc := range []struct {
		name, raw string
		bad       bool
	}{
		{"disabled", `[]`, false}, {"valid", `[` + valid + `]`, false},
		{"duplicate", `[` + valid + `,` + valid + `]`, true}, {"same isolation", `[` + valid + `,` + strings.Replace(valid, `"orders"`, `"orders-alias"`, 1) + `]`, true},
		{"null", `null`, true},
		{"unknown", `[{"unknown":true}]`, true},
		{"path", `[` + strings.Replace(valid, "9191", "9191/path", 1) + `]`, true},
		{"public IP", `[` + strings.ReplaceAll(valid, "127.0.0.1", "8.8.8.8") + `]`, true},
		{"missing allowlist", `[` + strings.Replace(valid, `["127.0.0.1"]`, `[]`, 1) + `]`, true},
		{"zero version", `[` + strings.Replace(valid, `"version":1`, `"version":0`, 1) + `]`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("MOCKER_TEST_TARGETS", tc.raw)
			c, err := config.Load()
			if (err != nil) != tc.bad {
				t.Fatalf("error=%v", err)
			}
			if !tc.bad && c.TestTargets == nil {
				t.Fatal("nil targets")
			}
		})
	}
}
