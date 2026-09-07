// Package adminkit is internal/testkit's admin-server bootstrap
// (NewAdminServer), split into its own package rather than living inside
// internal/testkit itself for one reason: internal/admin imports most of
// this tree's other packages (checkpoints, resources, customep, overrides,
// scenarios, specs, assets, mcp, …), so a package that imports
// internal/admin cannot be imported back by any of THOSE packages'
// internal (package-name-shared, not "_test"-suffixed) test files without
// closing a real import cycle — proven empirically: moving NewAdminServer
// into the plain internal/testkit package made
// internal/checkpoints/repo_test.go (package checkpoints) and
// internal/resources/repo_test.go (package resources) fail to build with
// "import cycle not allowed in test", even though neither file calls
// NewAdminServer at all — merely importing the PACKAGE it lives in was
// enough, because Go resolves an import at package granularity. Splitting
// the DB-only helpers (internal/testkit, imports only internal/store, a
// leaf) from this admin-importing one keeps every repo_test.go free to
// import the former without ever touching internal/admin transitively.
package adminkit

import (
	"io"
	"log/slog"
	"testing"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/workspaces"
)

// AdminServer bundles a fully wired *admin.Server with the database and
// config it was built from, so a caller that needs to reach past the HTTP
// surface — closing the database to force /readyz to fail, inspecting cfg,
// dispatching through Server.CallAsMCP directly — can, without this package
// having to guess which of those any given caller wants back.
type AdminServer struct {
	Server *admin.Server
	DB     *store.DB
	Cfg    *config.Config
}

// NewAdminServer builds a fully wired admin.Server over a fresh, migrated
// SQLite database at cfg.DBPath(): open+migrate (via testkit.NewDBAt),
// auth.NewSharedPassword, auth.NewManager, workspaces.NewRepo, a discard
// logger, admin.New — the sequence internal/admin's own newTestServerCfg
// and internal/mcp's own newResourcesTestServer each built independently,
// identically, before this package existed.
func NewAdminServer(t testing.TB, cfg *config.Config) *AdminServer {
	t.Helper()
	db := testkit.NewDBAt(t, cfg.DBPath())

	provider := auth.NewSharedPassword(cfg)
	sessions := auth.NewManager(db, cfg, provider)
	ws := workspaces.NewRepo(db)
	log := slog.New(slog.NewTextHandler(io.Discard, nil))

	return &AdminServer{Server: admin.New(cfg, sessions, ws, db, log), DB: db, Cfg: cfg}
}
