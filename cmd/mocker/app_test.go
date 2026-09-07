package main

import (
	"io"
	"log/slog"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/testauth"
)

// appTestConfig is the smallest configuration [run]'s phases will assemble
// against: the fields config.Load would have filled in, spelled out here
// because this fixture bypasses Load entirely (the same shape
// internal/admin's own testConfig uses, and for the same reason).
func appTestConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		Addr:                ":0",
		LogLevel:            "info",
		BaseDomain:          "mock.local",
		AdminHost:           "mocker.local",
		Routing:             config.RoutingHost,
		ReservedPrefix:      "/__mocker",
		AuthMode:            config.AuthShared,
		SharedPasswordHash:  testauth.Hash, // pre-minted: argon2 is ~110 ms per fixture under -race
		DataDir:             t.TempDir(),
		MaxBody:             10 << 20,
		MaxResponse:         4 << 20,
		TrafficMaxBody:      64 << 10,
		MaxEntities:         1000,
		RuntimeCache:        32,
		CheckpointRetention: 20,
		StreamMaxConns:      4,
	}
}

// wireApp runs every phase [run] runs before it opens a listener, and
// nothing after: this test is about the wiring, not about serving.
func wireApp(t *testing.T) *app {
	t.Helper()
	a := &app{cfg: appTestConfig(t), log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := a.openStore(t.Context()); err != nil {
		t.Fatalf("openStore: %v", err)
	}
	t.Cleanup(func() { _ = a.db.Close() })
	if err := a.buildPlanes(t.Context()); err != nil {
		t.Fatalf("buildPlanes: %v", err)
	}
	a.wireMockPlane()
	t.Cleanup(a.liveState.Close)
	return a
}

// TestAppWiringIsComplete is THE test this file exists for, and the one
// that would have caught both of the incidents CLAUDE.md records: a fully
// green `go test` shipped with a feature dead in production, because every
// package's own tests wire the source they exercise and only this file can
// forget the line. It drives the real phases against a real database — no
// fake plane, no fake admin server — and asks both planes whether anything
// they need is still nil.
//
// It deliberately does NOT enumerate the sources itself. The lists live in
// [mockplane.Plane.Ready] and [admin.Server.Ready], next to the fields they
// check, so a new source added there fails HERE without anybody remembering
// to update a second list.
func TestAppWiringIsComplete(t *testing.T) {
	t.Parallel()

	a := wireApp(t)
	a.wireStreaming()
	t.Cleanup(a.streamRegistry.Close)
	t.Cleanup(a.mockStreams.Close)
	a.wireMCP()

	if err := a.checkWiring(); err != nil {
		t.Fatalf("a fully wired app reports a gap: %v", err)
	}
}

// TestCheckWiringRefusesAMissingPhase is the other half: a checkWiring that
// always returned nil would pass the test above by itself. Skipping
// wireStreaming is the cheapest realistic version of the mistake — one
// phase's worth of setters missing from an otherwise complete assembly —
// and both planes must name their own missing setter, since each holds a
// different registry from that one phase.
func TestCheckWiringRefusesAMissingPhase(t *testing.T) {
	t.Parallel()

	a := wireApp(t)
	a.wireMCP() // MOCKER_MCP_KEY is unset here, so this is a no-op by design

	err := a.checkWiring()
	if err == nil {
		t.Fatal("checkWiring accepted an app whose streaming phase never ran")
	}
	for _, want := range []string{"mock plane", "SetStreams", "admin plane", "SetStream"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("checkWiring error %q does not mention %q", err, want)
		}
	}
}

// TestCheckWiringIgnoresMCP pins the one source a deployment may legitimately
// leave out: with MOCKER_MCP_KEY unset, wireMCP mounts nothing and the
// process must still start.
func TestCheckWiringIgnoresMCP(t *testing.T) {
	t.Parallel()

	a := wireApp(t)
	a.wireStreaming()
	t.Cleanup(a.streamRegistry.Close)
	t.Cleanup(a.mockStreams.Close)
	// wireMCP is never called at all — a stronger version of "the key is
	// unset" than calling it and watching the branch not fire.

	if err := a.checkWiring(); err != nil {
		t.Fatalf("checkWiring refused an app with no MCP endpoint: %v", err)
	}
}
