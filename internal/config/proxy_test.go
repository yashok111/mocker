package config_test

import (
	"testing"

	"github.com/yashok111/mocker/internal/config"
)

func TestProxyPolicyConfig(t *testing.T) {
	setBaseEnv(t)
	t.Setenv("MOCKER_PROXY_ALLOWLIST", "https://api.example.com,http://127.0.0.1:9000")
	cfg, err := config.Load()
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.ProxyAllowlist) != 2 {
		t.Fatal("missing proxy origins")
	}
	t.Setenv("MOCKER_PROXY_ALLOWLIST", "https://api.example.com/path")
	if _, err := config.Load(); err == nil {
		t.Fatal("allowed path in origin")
	}
	t.Setenv("MOCKER_PROXY_ALLOWLIST", "")
	t.Setenv("MOCKER_PROXY_CA_FILE", "/does-not-exist/mocker-proxy-ca.pem")
	if _, err := config.Load(); err == nil {
		t.Fatal("ignored missing CA")
	}
}
