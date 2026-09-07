// ready_test.go is a BLACK-BOX test file (package admin_test, like
// admin_test.go whose testConfig it reuses): [admin.Server.Ready] is a
// startup-time question cmd/mocker asks from outside this package, and the
// test that pins it asks it the same way.
package admin_test

import (
	"io"
	"log/slog"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/workspaces"
)

// The Server's sources, stubbed by EMBEDDING each interface rather than
// implementing it: Ready asks only whether a field is nil, and this file
// serves no request, so a nil embedded interface — which panics loudly the
// moment a method IS called — is the honest shape for a test about wiring
// rather than about behaviour.
type (
	readyLiveState struct{ admin.LiveStateSource }
	readyTraffic   struct{ admin.TrafficControl }
	readyPreviewer struct{ admin.Previewer }
)

// newReadyServer builds a Server the way cmd/mocker does — a real database
// underneath, since [admin.New] hands it to nine repositories — and wires
// nothing else. It deliberately does not go through newTestServerCfg:
// that helper returns the built http.Handler, and this file needs the
// *admin.Server itself.
func newReadyServer(t *testing.T) *admin.Server {
	t.Helper()
	cfg := testConfig(t)
	db, err := store.Open(t.Context(), cfg.DBPath())
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	return admin.New(cfg, auth.NewManager(db, cfg, auth.NewSharedPassword(cfg)), workspaces.NewRepo(db), db, log)
}

// TestServerReadyReportsEverySetterUntilItRuns is the bar this file exists
// for: a bare New reports every required source, and each name disappears
// exactly when its own setter runs. Both halves matter — a Ready that
// always reported nothing would pass a "fully wired reports nothing" test
// all by itself.
func TestServerReadyReportsEverySetterUntilItRuns(t *testing.T) {
	t.Parallel()

	steps := []struct {
		name string
		wire func(*admin.Server)
	}{
		{"SetLiveState", func(s *admin.Server) { s.SetLiveState(readyLiveState{}) }},
		{"SetTraffic", func(s *admin.Server) { s.SetTraffic(readyTraffic{}) }},
		{"SetStream", func(s *admin.Server) {
			s.SetStream(stream.NewRegistry(), stream.NewWorkspaceRegistry(1), admin.StreamOptions{})
		}},
		{"SetPreviewer", func(s *admin.Server) { s.SetPreviewer(readyPreviewer{}) }},
	}

	srv := newReadyServer(t)

	var want []string
	for _, s := range steps {
		want = append(want, s.name)
	}
	if got := srv.Ready(); !slices.Equal(got, want) {
		t.Fatalf("bare New: Ready() = %v, want %v", got, want)
	}

	for _, s := range steps {
		if !slices.Contains(srv.Ready(), s.name) {
			t.Fatalf("%s: not reported before its setter ran (Ready() = %v)", s.name, srv.Ready())
		}
		s.wire(srv)
		if slices.Contains(srv.Ready(), s.name) {
			t.Fatalf("%s: still reported after its setter ran (Ready() = %v)", s.name, srv.Ready())
		}
	}

	if got := srv.Ready(); len(got) != 0 {
		t.Fatalf("every setter ran: Ready() = %v, want nothing missing", got)
	}
}

// TestServerReadyIgnoresMCP pins the one OPTIONAL source: MOCKER_MCP_KEY
// unset means /mcp is not a route at all, so a Server that never got
// SetMCP is a deployment's deliberate choice and must not keep the process
// from starting.
func TestServerReadyIgnoresMCP(t *testing.T) {
	t.Parallel()

	srv := newReadyServer(t)
	srv.SetLiveState(readyLiveState{})
	srv.SetTraffic(readyTraffic{})
	srv.SetStream(stream.NewRegistry(), stream.NewWorkspaceRegistry(1), admin.StreamOptions{})
	srv.SetPreviewer(readyPreviewer{})

	if got := srv.Ready(); len(got) != 0 {
		t.Fatalf("SetMCP never called: Ready() = %v, want nothing missing", got)
	}
}

// TestServerReadyRequiresBothStreamRegistries is the half of SetStream's
// contract that is easy to get wrong: its own doc comment allows a nil mock
// registry for "a test harness with no mock plane", and a production
// Server given one drops the `mock` object out of GET /api/stream/stats
// while refusing nothing at all — the quietest possible form of the failure
// Ready exists to catch.
func TestServerReadyRequiresBothStreamRegistries(t *testing.T) {
	t.Parallel()

	srv := newReadyServer(t)
	srv.SetStream(stream.NewRegistry(), nil, admin.StreamOptions{})

	if got := srv.Ready(); !slices.Contains(got, "SetStream") {
		t.Fatalf("mock registry nil: Ready() = %v, want it to name SetStream", got)
	}
}
