package main

import (
	"errors"
	"fmt"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// The compose half of `mocker setup`: scripts/compose-tls.sh rewritten as
// a function, because the wizard runs on macOS and Windows where bash is
// not a given. Every rule the script enforces is enforced here, in the
// same words, and setup_test.go pins the arithmetic.

// composeFiles is the overlay pair the HTTPS stack is always started from.
var composeFiles = []string{"docker-compose.yml", "docker-compose.tls.yml"}

// defaultTLSSubnet, defaultTLSPort: the overlay's two knobs, with the
// script's defaults.
const (
	defaultTLSSubnet = "172.30.10.0/24"
	defaultTLSPort   = 8443
)

// slash24 is the ONE subnet shape the overlay takes: an IPv4 /24 ending in
// .0, whose .1 is the gateway and whose .254 is Caddy's static address.
var slash24 = regexp.MustCompile(`^([0-9]{1,3}\.[0-9]{1,3}\.[0-9]{1,3})\.0/24$`)

// composeEnv is the interpolation environment docker-compose.tls.yml
// needs, derived exactly as compose-tls.sh derives it: the two host names
// out of .env (always the FILE, never the caller's shell, so Caddy's site
// blocks cannot disagree with the names mocker started with), the gateway
// and Caddy's address from the subnet, and COMPOSE_ENV_FILES pointed at the
// null device so compose's second, interpolating read of .env does not
// print one bogus warning per `$` in the hash.
func composeEnv(env []byte, subnet string, port int) ([]string, error) {
	baseDomain := envValue(env, "MOCKER_BASE_DOMAIN")
	adminHost := envValue(env, "MOCKER_ADMIN_HOST")
	if baseDomain == "" || adminHost == "" {
		return nil, errors.New(".env must set MOCKER_BASE_DOMAIN and MOCKER_ADMIN_HOST")
	}
	for name, value := range map[string]string{"MOCKER_BASE_DOMAIN": baseDomain, "MOCKER_ADMIN_HOST": adminHost} {
		if !bareHost.MatchString(value) {
			return nil, fmt.Errorf("%s=%q is not a bare host name", name, value)
		}
	}
	m := slash24.FindStringSubmatch(subnet)
	if m == nil {
		return nil, fmt.Errorf("subnet %q must be an IPv4 /24 ending in .0", subnet)
	}
	if port < 1 || port > 65535 {
		return nil, fmt.Errorf("port %d is outside 1..65535", port)
	}
	return []string{
		"COMPOSE_ENV_FILES=" + os.DevNull,
		"MOCKER_BASE_DOMAIN=" + baseDomain,
		"MOCKER_ADMIN_HOST=" + adminHost,
		"MOCKER_TLS_SUBNET=" + subnet,
		"MOCKER_TLS_GATEWAY=" + m[1] + ".1",
		"MOCKER_TLS_CADDY_IP=" + m[1] + ".254",
		"MOCKER_TLS_PORT=" + strconv.Itoa(port),
	}, nil
}

// composeArgs prefixes a compose verb with the overlay pair.
func composeArgs(verb ...string) []string {
	args := []string{"compose"}
	for _, f := range composeFiles {
		args = append(args, "-f", f)
	}
	return append(args, verb...)
}

// minComposeVersion is what the stack needs: `ports: !reset` (2.24) and
// `env_file: format: raw` (2.30) — the overlay's own comment names both.
const minComposeVersion = "2.30"

// composeVersionOK parses `docker compose version --short` ("2.31.0",
// "v2.31.0") and compares major.minor against minComposeVersion.
func composeVersionOK(short string) (bool, error) {
	s := strings.TrimPrefix(strings.TrimSpace(short), "v")
	got, err := majorMinor(s)
	if err != nil {
		return false, err
	}
	want, _ := majorMinor(minComposeVersion)
	return got[0] > want[0] || (got[0] == want[0] && got[1] >= want[1]), nil
}

func majorMinor(s string) ([2]int, error) {
	parts := strings.SplitN(s, ".", 3)
	if len(parts) < 2 {
		return [2]int{}, fmt.Errorf("version %q: want major.minor", s)
	}
	var out [2]int
	for i := range 2 {
		n, err := strconv.Atoi(parts[i])
		if err != nil {
			return [2]int{}, fmt.Errorf("version %q: %w", s, err)
		}
		out[i] = n
	}
	return out, nil
}
