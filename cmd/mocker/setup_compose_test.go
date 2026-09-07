package main

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"testing"
)

// repoFile resolves a path relative to the repository root through
// [runtime.Caller] rather than through the working directory. That is not
// pedantry: [runSetup] calls os.Chdir on the whole PROCESS (setup.go), and
// setup_test.go exercises it, so a relative path read from a parallel test
// in this package resolves against whichever directory that test happens to
// be sitting in — measured here as a test that passed and failed on
// alternate runs of the same unchanged tree.
func repoFile(t *testing.T, parts ...string) string {
	t.Helper()
	_, self, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller could not name this test file")
	}
	root := filepath.Join(filepath.Dir(self), "..", "..") // cmd/mocker -> repo root
	return filepath.Join(append([]string{root}, parts...)...)
}

// scriptDefault pulls the default out of one `export NAME="${NAME:-value}"`
// line of scripts/compose-tls.sh. It is a regex over the script's text and
// not a shell execution on purpose: bash is exactly what `mocker setup`
// exists to avoid depending on (docs/agent/ops.md), so the test that proves
// the two agree must not need it either.
func scriptDefault(t *testing.T, script []byte, name string) string {
	t.Helper()
	re := regexp.MustCompile(`export ` + regexp.QuoteMeta(name) + `="\$\{` + regexp.QuoteMeta(name) + `:-([^}]*)\}"`)
	m := re.FindSubmatch(script)
	if m == nil {
		t.Fatalf(`scripts/compose-tls.sh has no export %s="${%s:-...}" line`, name, name)
	}
	return string(m[1])
}

// TestComposeDefaultsMatchTheScript pins the duplication docs/agent/ops.md
// admits to: scripts/compose-tls.sh and setup_compose.go each carry their
// own copy of the overlay's two defaults, deliberately — the Go half exists
// because bash is not a given on macOS or Windows — and until now nothing
// caught them drifting apart. A subnet that disagrees is not a cosmetic
// difference: MOCKER_TRUST_PROXY is derived from it (Caddy's .254), so a
// stack started through the script and one started through `mocker setup`
// would trust different addresses.
func TestComposeDefaultsMatchTheScript(t *testing.T) {
	t.Parallel()

	script, err := os.ReadFile(repoFile(t, "scripts", "compose-tls.sh"))
	if err != nil {
		t.Fatalf("read scripts/compose-tls.sh: %v", err)
	}

	if got := scriptDefault(t, script, "MOCKER_TLS_SUBNET"); got != defaultTLSSubnet {
		t.Errorf("MOCKER_TLS_SUBNET default: script says %q, setup_compose.go says %q", got, defaultTLSSubnet)
	}
	if got, want := scriptDefault(t, script, "MOCKER_TLS_PORT"), strconv.Itoa(defaultTLSPort); got != want {
		t.Errorf("MOCKER_TLS_PORT default: script says %q, setup_compose.go says %q", got, want)
	}
}
