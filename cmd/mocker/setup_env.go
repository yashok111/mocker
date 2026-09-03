package main

import (
	"bufio"
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"regexp"
	"strings"
)

// The pure half of `mocker setup`: how .env is rendered from .env.example,
// how a value is read back out of it, and the two secrets it mints. Kept
// free of I/O so setup_test.go can hold every rule without docker.

// envValue is scripts/compose-tls.sh's env_value: the LAST assignment of
// key wins, the value is everything after the first '=', and no quoting is
// honoured — none is in .env.example, and docker's env_file parser honours
// none either.
func envValue(env []byte, key string) string {
	value := ""
	sc := bufio.NewScanner(bytes.NewReader(env))
	for sc.Scan() {
		line := sc.Text()
		if rest, ok := strings.CutPrefix(line, key+"="); ok {
			value = rest
		}
	}
	return value
}

// setEnvValue rewrites the LAST `key=` line of env to value, or appends
// one when the key is absent — in place, so a value stays under its own
// comment in the file, exactly as scripts/init-env.sh's awk did for the
// hash.
func setEnvValue(env []byte, key, value string) []byte {
	lines := strings.Split(string(env), "\n")
	last := -1
	for i, line := range lines {
		if strings.HasPrefix(line, key+"=") {
			last = i
		}
	}
	if last < 0 {
		trimmed := strings.TrimRight(string(env), "\n")
		return []byte(trimmed + "\n" + key + "=" + value + "\n")
	}
	lines[last] = key + "=" + value
	return []byte(strings.Join(lines, "\n"))
}

// renderEnv is .env.example with the wizard's answers written in:
// MOCKER_SHARED_PASSWORD_HASH (the argon2id verifier), MOCKER_ROUTING=path
// (one host, no wildcard DNS — the single-machine default the wizard
// exists for), the admin host, and MOCKER_MCP_KEY when the operator asked
// for an agent door. MOCKER_DEV stays whatever the example says: the HTTPS
// overlay forces it to 0 in the container's environment regardless.
func renderEnv(example []byte, hash, adminHost, mcpKey string) []byte {
	out := setEnvValue(example, "MOCKER_SHARED_PASSWORD_HASH", hash)
	out = setEnvValue(out, "MOCKER_ROUTING", "path")
	out = setEnvValue(out, "MOCKER_ADMIN_HOST", adminHost)
	if mcpKey != "" {
		out = setEnvValue(out, "MOCKER_MCP_KEY", mcpKey)
	}
	return out
}

// generatePassword is scripts/init-env.sh's recipe: 18 random bytes as
// base64 with the three URL-unfriendly characters dropped, so the value
// pastes anywhere without quoting.
func generatePassword() (string, error) {
	var raw [18]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("random password: %w", err)
	}
	s := base64.StdEncoding.EncodeToString(raw[:])
	return strings.NewReplacer("/", "", "+", "", "=", "").Replace(s), nil
}

// generateMCPKey mints a 48-hex-character bearer key — above the 32-byte
// floor internal/config enforces on MOCKER_MCP_KEY, and hex so it survives
// every shell and every MCP client config file unquoted.
func generateMCPKey() (string, error) {
	var raw [24]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("random MCP key: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

// bareHost is the shape a host name must have to land in deploy/Caddyfile
// as {$VAR} at parse time (scripts/compose-tls.sh's own check): whitespace
// or a brace there would become Caddy configuration, not a host name. A
// port is refused too — internal/config refuses it at startup.
var bareHost = regexp.MustCompile(`^[A-Za-z0-9.-]+$`)
