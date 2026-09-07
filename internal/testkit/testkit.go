// Package testkit is the one place this tree's most-copied test bootstrap
// lives: NewDB/NewDBAt (byte-identical across
// internal/assets/repo_test.go, internal/checkpoints/repo_test.go,
// internal/customep/repo_test.go, internal/overrides/repo_test.go,
// internal/scenarios/repo_test.go, internal/workspaces/repo_test.go and
// internal/store/editversion_test.go, plus path-parametrised twins in
// internal/resources/repo_test.go and internal/specs/repo_test.go — nine
// copies before this package existed).
//
// The other most-copied bootstrap — the "wired admin.Server over a
// migrated DB" internal/admin's own newTestServerCfg and internal/mcp's own
// newResourcesTestServer each built independently — is NOT here: it lives
// in the adminkit subpackage, because it needs internal/admin, and
// internal/admin imports back almost every package in this tree
// (checkpoints, resources, customep, …). A single testkit package holding
// both helpers would make importing this one for NewDB ALSO drag in
// internal/admin — and admin, in turn, imports several of the very
// packages whose repo_test.go wants NewDB, e.g. internal/checkpoints and
// internal/resources — closing a real import cycle the moment their
// (internal, package-name-shared) test files imported this package at
// all, even without calling the admin helper. Proven empirically, not
// assumed: that was the exact failure before the split.
//
// It is test-support only, the same shape as internal/testauth and
// internal/testspec: nothing in cmd/ or the serving path imports it, and
// boundary_test.go fails the build if a production (non-_test.go) file
// ever does — the same guard internal/jsonx puts on encoding/json.
package testkit

import (
	"testing"

	"github.com/yashok111/mocker/internal/store"
)

// NewDB opens a fresh, migrated SQLite file under t.TempDir() and closes it
// on cleanup — the harness every repository test in this tree uses when it
// has no reason to hold on to the file's path itself.
func NewDB(t testing.TB) *store.DB {
	t.Helper()
	return NewDBAt(t, t.TempDir()+"/mocker.db")
}

// NewDBAt is NewDB with an explicit path, for the tests (internal/resources,
// internal/specs) that need the path value again later — e.g. to reopen the
// same file by hand and prove state survives a close/reopen — rather than
// only ever deriving it once, inline, inside t.TempDir()+"/mocker.db".
func NewDBAt(t testing.TB, path string) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close db: %v", err)
		}
	})
	if err := db.Migrate(t.Context(), nil); err != nil {
		t.Fatalf("migrate: %v", err)
	}
	return db
}
