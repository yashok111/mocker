// Package recordproxy owns installation-local proxy controls and replay data.
package recordproxy

import (
	"errors"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/luafn"
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
	// OperationPatches maps an operation key (the same "METHOD /path" shape
	// Operations uses) to a Lua patch body applied to that operation's
	// upstream response before it is served. It rides BESIDE Operations
	// rather than inside it so an old stored config unmarshals unchanged.
	OperationPatches map[string]string `json:"operationPatches"`
}

func DefaultConfig() Config {
	return Config{Mode: "off", TimeoutSeconds: 15, Overwrite: "last", Operations: map[string]string{}, OperationPatches: map[string]string{}}
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
	if len(c.OperationPatches) > 1000 {
		return errors.New("at most 1000 operation patches")
	}
	for key := range c.OperationPatches {
		if err := validOperationKey(key); err != nil {
			return err
		}
	}
	// Compiled at WRITE time (the same D8 rule the endpoint functions and
	// the stream hooks follow): a syntax error is a 400 carrying the
	// parser's own words, never a failed patch on the first proxied request.
	for key, source := range c.OperationPatches {
		if err := luafn.ValidatePatch(source); err != nil {
			return fmt.Errorf("operation patch for %q does not compile: %w", key, err)
		}
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
	switch lookupOperation(c.Operations, operation) {
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
		if err := validOperationKey(key); err != nil {
			return network, err
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

// validOperationKey is the one key-shape rule both maps answer to, so a
// policy and a patch for the same operation can never disagree about what
// the key looks like.
func validOperationKey(key string) error {
	parts := strings.SplitN(key, " ", 2)
	if len(key) > 2048 || len(parts) != 2 || !strings.HasPrefix(parts[1], "/") {
		return fmt.Errorf("invalid operation key %q", key)
	}
	switch parts[0] {
	case "GET", "HEAD", "POST", "PUT", "PATCH", "DELETE", "OPTIONS":
	default:
		return errors.New("invalid operation method")
	}
	return nil
}

// lookupOperation answers what keys (Operations or OperationPatches — both
// maps resolve the same way, so a policy and a patch written under the same
// key always reach the same requests) holds for operation, "" for nothing.
//
// operation is "METHOD /path": the matched spec route's template, or — when
// no spec route matched, the usual case behind a proxy — the request's own
// path below the base path. Keys are documented as "METHOD /path/{name}",
// so an exact lookup alone left a template key dead for every request no
// spec route matched (2026-11-01: a patch keyed by the upstream's template
// never ran on the concrete path it was written for). Hence, in order:
//
//  1. the exact key, always first (a literal key keeps working, and on a
//     spec route the key spelled exactly as its template stays the one);
//  2. otherwise a key matching by shape: same method, same segment count,
//     each literal segment equal, each "{name}" segment taking exactly one
//     non-empty segment. A segment that only CONTAINS a brace pair
//     ("file.{ext}") is literal. On a spec route the request's values are
//     already gone, so its "{id}" is matched like any segment: a renamed
//     parameter ("{widgetId}") matches it, a literal value never does;
//  3. among several such keys, the one with the most literal segments, then
//     the lexicographically smallest key — independent of map order.
func lookupOperation(keys map[string]string, operation string) string {
	if value, ok := keys[operation]; ok {
		return value
	}
	method, path, _ := strings.Cut(operation, " ")
	segments := operationSegments(path)
	// best stays "" until a key matches: a match scores at least 0, and no
	// valid key is "" (validOperationKey), so keys[""] is the empty answer.
	best, bestLiterals := "", noOperationMatch
	for key := range keys {
		literals := matchOperationKey(key, method, segments)
		if literals > bestLiterals || literals == bestLiterals && key < best {
			best, bestLiterals = key, literals
		}
	}
	return keys[best]
}

// noOperationMatch is [matchOperationKey]'s answer for a key that does not
// match; any match counts its literal segments, so it scores 0 or more.
const noOperationMatch = -1

// matchOperationKey answers how many literal segments key has when it
// matches method and segments by shape (see [lookupOperation]), and
// [noOperationMatch] when it does not.
func matchOperationKey(key, method string, segments []string) int {
	keyMethod, keyPath, _ := strings.Cut(key, " ")
	keySegments := operationSegments(keyPath)
	if keyMethod != method || len(keySegments) != len(segments) {
		return noOperationMatch
	}
	literals := 0
	for i, k := range keySegments {
		if isParameterSegment(k) {
			if segments[i] == "" {
				return noOperationMatch
			}
			continue
		}
		if k != segments[i] {
			return noOperationMatch
		}
		literals++
	}
	return literals
}

// isParameterSegment reports a whole-segment "{name}" with a non-empty name.
func isParameterSegment(segment string) bool {
	return len(segment) > 2 && strings.HasPrefix(segment, "{") && strings.HasSuffix(segment, "}")
}

func operationSegments(path string) []string {
	return strings.Split(strings.TrimPrefix(path, "/"), "/")
}
