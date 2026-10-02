// Package recordproxy owns installation-local proxy controls and replay data.
package recordproxy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/probe"
)

type Config struct {
	Version         int64             `json:"version"`
	Mode            string            `json:"mode"`
	Upstream        string            `json:"upstream"`
	TimeoutSeconds  int               `json:"timeoutSeconds"`
	ForwardAuth     bool              `json:"forwardAuth"`
	ForwardCookies  bool              `json:"forwardCookies"`
	Overwrite       string            `json:"overwrite"`
	CaptureEntities bool              `json:"captureEntities"`
	Operations      map[string]string `json:"operations"`
}

func DefaultConfig() Config {
	return Config{Mode: "off", TimeoutSeconds: 15, Overwrite: "last", Operations: map[string]string{}}
}
func (c Config) Validate(allowlist []string) error {
	switch c.Mode {
	case "off", "passthrough", "record", "replay":
	default:
		return errors.New("mode must be off, passthrough, record or replay")
	}
	if c.TimeoutSeconds < 1 || c.TimeoutSeconds > 120 {
		return errors.New("timeoutSeconds must be between 1 and 120")
	}
	if c.Version < 0 {
		return errors.New("version must be nonnegative")
	}
	if c.Overwrite != "first" && c.Overwrite != "last" {
		return errors.New("overwrite must be first or last")
	}
	if len(c.Operations) > 1000 {
		return errors.New("at most 1000 operation policies")
	}
	network, err := c.validateOperationPolicies()
	if err != nil {
		return err
	}
	if len(c.Upstream) > 2048 {
		return errors.New("upstream URL is too long")
	}
	if c.Upstream != "" {
		if _, err := probe.ValidateProxyURL(c.Upstream); err != nil {
			return err
		}
	}
	if network {
		_, err := probe.ValidateProxyTarget(c.Upstream, allowlist)
		return err
	}
	// Replay is deliberately available after the network allowlist is removed.
	if c.Mode == "replay" && c.Upstream == "" {
		return errors.New("replay requires the original upstream URL")
	}
	return nil
}
func (c Config) EffectiveMode(operation string) string {
	switch c.Operations[operation] {
	case "mock":
		return "off"
	case "proxy":
		if c.Mode == "off" {
			return "passthrough"
		}
	}
	return c.Mode
}

func (c Config) validateOperationPolicies() (bool, error) {
	network := c.Mode == "record" || c.Mode == "passthrough"
	for key, policy := range c.Operations {
		parts := strings.SplitN(key, " ", 2)
		if len(key) > 2048 || len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
			return false, fmt.Errorf("invalid operation key %q", key)
		}
		switch parts[0] {
		case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
		default:
			return false, errors.New("invalid operation method")
		}
		switch policy {
		case "default", "mock", "proxy":
		default:
			return false, errors.New("operation policy must be default, mock or proxy")
		}
		if c.Mode == "off" && policy == "proxy" {
			network = true
		}
	}
	return network, nil
}
