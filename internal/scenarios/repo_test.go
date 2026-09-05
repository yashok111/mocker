package scenarios_test

import (
	"bytes"
	"database/sql"
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/store"
)

// newTestDB opens a fresh, migrated SQLite file under t.TempDir(), mirroring
// internal/overrides/repo_test.go's harness.
func newTestDB(t *testing.T) *store.DB {
	t.Helper()
	db, err := store.Open(t.Context(), t.TempDir()+"/mocker.db")
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

// insertSpec writes a minimal specs row directly (the scenarios package
// owns no spec logic and must not import internal/specs — see repo.go's
// own doc comment on readSpecRef).
func insertSpec(t *testing.T, db *store.DB, name, hash string) int64 {
	t.Helper()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO specs (name, format, source, hash, raw, normalized, created_at)
		VALUES (?, 'oas31', 'upload', ?, '{}', '{}', unixepoch())`,
		name, hash,
	)
	if err != nil {
		t.Fatalf("insert spec %q: %v", name, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("spec id: %v", err)
	}
	return id
}

// insertWorkspace writes a workspaces row directly, exactly as
// internal/overrides/repo_test.go's own insertWorkspace does, but takes a
// full domain.Settings and an optional spec_id since CreateFromCurrentState
// reads both.
func insertWorkspace(t *testing.T, db *store.DB, slug string, specID *int64, settings domain.Settings) int64 {
	t.Helper()
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, unixepoch(), unixepoch())`,
		slug, slug, specID, string(settingsJSON),
	)
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

func workspaceRevision(t *testing.T, db *store.DB, id int64) int64 {
	t.Helper()
	var rev int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT revision FROM workspaces WHERE id = ?", id).Scan(&rev); err != nil {
		t.Fatalf("read revision for workspace %d: %v", id, err)
	}
	return rev
}

func workspaceScenarioID(t *testing.T, db *store.DB, id int64) *int64 {
	t.Helper()
	var v sql.NullInt64
	if err := db.R.QueryRowContext(t.Context(), "SELECT scenario_id FROM workspaces WHERE id = ?", id).Scan(&v); err != nil {
		t.Fatalf("read scenario_id for workspace %d: %v", id, err)
	}
	if !v.Valid {
		return nil
	}
	return &v.Int64
}

func newRepos(t *testing.T, db *store.DB) (*scenarios.Repo, *overrides.Repo) {
	t.Helper()
	or := overrides.NewRepo(db)
	return scenarios.NewRepo(db, or), or
}

// scenarioRowCount reads the total row count of the scenarios table across
// EVERY workspace — used by the cross-workspace clone test to prove a
// refused CloneFrom wrote no row anywhere, not merely that the target
// workspace's own List stayed empty.
func scenarioRowCount(t *testing.T, db *store.DB) int64 {
	t.Helper()
	var n int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM scenarios").Scan(&n); err != nil {
		t.Fatalf("count scenarios: %v", err)
	}
	return n
}

// TestList_returnsSummariesNeverSnapshots covers §C: List's shape is
// {id, name, createdAt, isActive} and IsActive is computed against the
// workspace's OWN scenario_id, not stored on the scenario row.
func TestList_returnsSummariesNeverSnapshots(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s1, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if _, err := repo.SetActive(t.Context(), ws, &s1.ID); err != nil {
		t.Fatalf("activate s1: %v", err)
	}
	// A10 forbids a second create while one is active — deactivate first
	// so this test can build a SECOND scenario to prove List shows both.
	if _, err := repo.SetActive(t.Context(), ws, nil); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	s2, err := repo.CreateFromCurrentState(t.Context(), ws, "two")
	if err != nil {
		t.Fatalf("create s2: %v", err)
	}
	if _, err := repo.SetActive(t.Context(), ws, &s2.ID); err != nil {
		t.Fatalf("activate s2: %v", err)
	}

	got, err := repo.List(t.Context(), ws)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("List returned %d entries, want 2", len(got))
	}
	if got[0].Name != "one" || got[0].IsActive {
		t.Errorf("entry 0 = %+v, want name=one isActive=false", got[0])
	}
	if got[1].Name != "two" || !got[1].IsActive {
		t.Errorf("entry 1 = %+v, want name=two isActive=true", got[1])
	}
}

// TestCreateFromCurrentState_refusedWhileActive is A10.
func TestCreateFromCurrentState_refusedWhileActive(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s1, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create s1: %v", err)
	}
	if _, err := repo.SetActive(t.Context(), ws, &s1.ID); err != nil {
		t.Fatalf("activate s1: %v", err)
	}

	before, err := repo.List(t.Context(), ws)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	_, err = repo.CreateFromCurrentState(t.Context(), ws, "two")
	if !errors.Is(err, scenarios.ErrScenarioActive) {
		t.Fatalf("CreateFromCurrentState while active: got %v, want ErrScenarioActive", err)
	}

	after, err := repo.List(t.Context(), ws)
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("observable scenario list changed after a refused create: before=%d after=%d", len(before), len(after))
	}
}

// TestSetActive_activatingAlreadyActiveIsANoOp is A7.
func TestSetActive_activatingAlreadyActiveIsANoOp(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	rev1, err := repo.SetActive(t.Context(), ws, &s.ID)
	if err != nil {
		t.Fatalf("activate: %v", err)
	}

	rev2, err := repo.SetActive(t.Context(), ws, &s.ID)
	if err != nil {
		t.Fatalf("re-activate: %v", err)
	}
	if rev2 != rev1 {
		t.Errorf("re-activating the already-active scenario changed revision: %d -> %d", rev1, rev2)
	}
	if got := workspaceRevision(t, db, ws); got != rev1 {
		t.Errorf("stored revision = %d, want unchanged %d", got, rev1)
	}
}

// TestSetActive_deactivatingAlreadyInactiveIsANoOp extends A7 to the nil
// (deactivate) direction — the same idempotence argument applies to
// {"scenario": ""} being sent twice.
func TestSetActive_deactivatingAlreadyInactiveIsANoOp(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	rev1 := workspaceRevision(t, db, ws)
	rev2, err := repo.SetActive(t.Context(), ws, nil)
	if err != nil {
		t.Fatalf("deactivate an already-inactive workspace: %v", err)
	}
	if rev2 != rev1 {
		t.Errorf("deactivating an already-inactive workspace changed revision: %d -> %d", rev1, rev2)
	}
}

// TestDelete_activeScenarioBumpsRevision is A9.
func TestDelete_activeScenarioBumpsRevision(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if _, err := repo.SetActive(t.Context(), ws, &s.ID); err != nil {
		t.Fatalf("activate: %v", err)
	}
	revBefore := workspaceRevision(t, db, ws)

	if err := repo.Delete(t.Context(), ws, s.ID); err != nil {
		t.Fatalf("delete active scenario: %v", err)
	}

	revAfter := workspaceRevision(t, db, ws)
	if revAfter != revBefore+1 {
		t.Errorf("revision after deleting the active scenario = %d, want %d (before %d + 1)", revAfter, revBefore+1, revBefore)
	}
	if got := workspaceScenarioID(t, db, ws); got != nil {
		t.Errorf("workspaces.scenario_id after deleting the active scenario = %v, want nil (ON DELETE SET NULL)", *got)
	}

	// A second DELETE of the same, now-gone id is a 404, not a second bump.
	if err := repo.Delete(t.Context(), ws, s.ID); !errors.Is(err, scenarios.ErrNotFound) {
		t.Fatalf("second delete: got %v, want ErrNotFound", err)
	}
	if got := workspaceRevision(t, db, ws); got != revAfter {
		t.Errorf("a 404'd delete still bumped revision: %d -> %d", revAfter, got)
	}
}

// TestDelete_inactiveScenarioDoesNotBumpRevision is Delete's other half:
// removing a scenario that ISN'T active must not cost a rebuild either.
func TestDelete_inactiveScenarioDoesNotBumpRevision(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	revBefore := workspaceRevision(t, db, ws)

	if err := repo.Delete(t.Context(), ws, s.ID); err != nil {
		t.Fatalf("delete inactive scenario: %v", err)
	}
	if got := workspaceRevision(t, db, ws); got != revBefore {
		t.Errorf("revision after deleting an INACTIVE scenario = %d, want unchanged %d", got, revBefore)
	}
}

// TestOwnership_crossWorkspaceScenarioIdIs404 is A8, exercised across
// Get, SetActive and Delete: a scenario id that genuinely exists, but in a
// DIFFERENT workspace, must answer exactly like one that does not exist at
// all — never activate, never delete, never leak the other workspace's
// row.
func TestOwnership_crossWorkspaceScenarioIdIs404(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	wsA := insertWorkspace(t, db, "a", nil, domain.DefaultSettings())
	wsB := insertWorkspace(t, db, "b", nil, domain.DefaultSettings())

	sB, err := repo.CreateFromCurrentState(t.Context(), wsB, "b-scenario")
	if err != nil {
		t.Fatalf("create in workspace B: %v", err)
	}

	if _, err := repo.Get(t.Context(), wsA, sB.ID); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("Get(A, B's scenario): got %v, want ErrNotFound", err)
	}
	if _, err := repo.SetActive(t.Context(), wsA, &sB.ID); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("SetActive(A, B's scenario): got %v, want ErrNotFound", err)
	}
	if err := repo.Delete(t.Context(), wsA, sB.ID); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("Delete(A, B's scenario): got %v, want ErrNotFound", err)
	}

	// B's own workspace must be entirely unaffected by A's failed attempts.
	if got := workspaceScenarioID(t, db, wsB); got != nil {
		t.Errorf("workspace B's scenario_id = %v after A's failed SetActive, want nil (A never touched B)", *got)
	}
	if _, err := repo.Get(t.Context(), wsB, sB.ID); err != nil {
		t.Errorf("Get(B, B's own scenario) after A's failed attempts: %v", err)
	}
}

// TestCreateFromCurrentState_snapshotsSettingsAndOverrides is the basic
// round trip: settings and every current op_overrides row land in the
// snapshot, and OverrideOn=false rows survive (A2) alongside OverrideOn=true
// ones.
func TestCreateFromCurrentState_snapshotsSettingsAndOverrides(t *testing.T) {
	db := newTestDB(t)
	repo, overridesRepo := newRepos(t, db)
	settings := domain.DefaultSettings()
	settings.ListSize = 7
	specID := insertSpec(t, db, "platform", "hash-1")
	ws := insertWorkspace(t, db, "ws", &specID, settings)

	if _, _, err := overridesRepo.Put(t.Context(), ws, "GET /widgets", func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("put override: %v", err)
	}
	if _, _, err := overridesRepo.Put(t.Context(), ws, "GET /quizzes", func(row *overrides.Row) error {
		row.OverrideOn = false
		return nil
	}); err != nil {
		t.Fatalf("put masking override: %v", err)
	}

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "snap")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if s.Bundle.Workspace.Settings.ListSize != 7 {
		t.Errorf("snapshot listSize = %d, want 7", s.Bundle.Workspace.Settings.ListSize)
	}
	if s.Bundle.Spec.Hash != "hash-1" || s.Bundle.Spec.Name != "platform" {
		t.Errorf("snapshot spec ref = %+v, want hash-1/platform", s.Bundle.Spec)
	}
	if len(s.Bundle.Overrides) != 2 {
		t.Fatalf("snapshot has %d override entries, want 2", len(s.Bundle.Overrides))
	}
	byPath := map[string]bool{} // path -> overrideOn
	for _, e := range s.Bundle.Overrides {
		byPath[e.Path] = e.OverrideOn
	}
	if on, ok := byPath["/widgets"]; !ok || !on {
		t.Errorf("/widgets entry = present:%v on:%v, want present:true on:true", ok, on)
	}
	if on, ok := byPath["/quizzes"]; !ok || on {
		t.Errorf("/quizzes entry = present:%v on:%v, want present:true on:false (A2)", ok, on)
	}

	// Get(id) must read back the identical content CreateFromCurrentState
	// itself returned.
	got, err := repo.Get(t.Context(), ws, s.ID)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Bundle.Overrides) != 2 {
		t.Fatalf("Get: %d override entries, want 2", len(got.Bundle.Overrides))
	}
}

// TestByName_resolvesForTheMockPlanesDirective is §B seam 3's by-name
// lookup: mockplane.ScenarioSource needs to translate a
// `/__mocker/state {"scenario":"<name>"}` NAME into an id before it can
// call SetActive, and an unknown name must answer exactly like an unknown
// id does.
func TestByName_resolvesForTheMockPlanesDirective(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "named-one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	got, err := repo.ByName(t.Context(), ws, "named-one")
	if err != nil {
		t.Fatalf("ByName: %v", err)
	}
	if got.ID != s.ID {
		t.Errorf("ByName id = %d, want %d", got.ID, s.ID)
	}

	if _, err := repo.ByName(t.Context(), ws, "does-not-exist"); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("ByName(unknown): got %v, want ErrNotFound", err)
	}
}

// TestCreateFromCurrentState_duplicateNameIsRejected pins
// UNIQUE (workspace_id, name).
func TestCreateFromCurrentState_duplicateNameIsRejected(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	if _, err := repo.CreateFromCurrentState(t.Context(), ws, "dup"); err != nil {
		t.Fatalf("first create: %v", err)
	}
	if _, err := repo.CreateFromCurrentState(t.Context(), ws, "dup"); !errors.Is(err, scenarios.ErrDuplicateName) {
		t.Fatalf("second create with the same name: got %v, want ErrDuplicateName", err)
	}
}

// TestCreateFromCurrentState_emptyNameRejected covers ErrInvalidName
// before any SQL runs.
func TestCreateFromCurrentState_emptyNameRejected(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	if _, err := repo.CreateFromCurrentState(t.Context(), ws, "   "); !errors.Is(err, scenarios.ErrInvalidName) {
		t.Fatalf("create with a blank name: got %v, want ErrInvalidName", err)
	}
}

// TestA15_snapshotSurvivesTheSpecItWasTakenAgainstChanging is A15's
// assigned cover at the repo layer: a scenario's recorded spec identity
// (Spec.Hash) is provenance ONLY. This imports a SECOND spec (the setup
// A15's own text says it needs), points the workspace at it AFTER the
// scenario was saved against the first, and asserts Get still returns the
// snapshot unchanged and unrefused — nothing in this package ever compares
// a stored snapshot's Spec.Hash against "the current spec".
func TestA15_snapshotSurvivesTheSpecItWasTakenAgainstChanging(t *testing.T) {
	db := newTestDB(t)
	repo, overridesRepo := newRepos(t, db)
	specA := insertSpec(t, db, "spec-a", "hash-a")
	specB := insertSpec(t, db, "spec-b", "hash-b")
	ws := insertWorkspace(t, db, "ws", &specA, domain.DefaultSettings())

	// A row for an operation that only makes sense under spec A.
	if _, _, err := overridesRepo.Put(t.Context(), ws, "GET /only-in-spec-a", func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("put override: %v", err)
	}

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "snap")
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	if s.Bundle.Spec.Hash != "hash-a" {
		t.Fatalf("snapshot spec hash = %q, want hash-a", s.Bundle.Spec.Hash)
	}

	// Re-point the workspace at spec B directly — this package has no
	// "attach spec" operation of its own (that is internal/workspaces'
	// job), so the fixture writes the column by hand exactly as
	// internal/overrides' own fixtures write FK targets directly.
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET spec_id = ? WHERE id = ?", specB, ws); err != nil {
		t.Fatalf("repoint workspace at spec B: %v", err)
	}

	// The scenario's row for GET /only-in-spec-a is now for a route "the
	// current spec" (B) does not have at all. Get must still succeed and
	// return it — inert, not an error (A15).
	got, err := repo.Get(t.Context(), ws, s.ID)
	if err != nil {
		t.Fatalf("Get after the workspace's spec changed: %v", err)
	}
	if got.Bundle.Spec.Hash != "hash-a" {
		t.Errorf("Get returned Spec.Hash = %q, want the SNAPSHOT's own hash-a (provenance, A15) not the workspace's current spec", got.Bundle.Spec.Hash)
	}
	found := false
	for _, e := range got.Bundle.Overrides {
		if e.Path == "/only-in-spec-a" {
			found = true
		}
	}
	if !found {
		t.Errorf("the /only-in-spec-a override row is missing from the snapshot after the spec changed — A15 says it must stay, inert")
	}
}

// TestScanScenario_rejectsNonEmptyEndpoints is scanScenario's own test for
// the guard C2 of P2c's gate moved here. bundle.Validate no longer refuses
// a non-empty Endpoints array at the format level — P2c reuses this same
// v3 format for a checkpoint's config_snap, which DOES legitimately carry
// one — so this package is now the ONLY place left that still refuses one
// for a SCENARIO specifically (§0: runtime.custom is keyed by a
// custom_endpoints DB row id, and a row inside a BLOB has no id to be keyed
// by).
//
// There is deliberately no way to drive this through
// CreateFromCurrentState, this package's own write path: [bundle.New]'s
// signature is unchanged and always hard-codes an empty Endpoints slice
// (C2), so nothing this package ever writes can produce a non-empty one —
// which is exactly why there is no matching write-side guard to test
// alongside this one. The fixture below writes the scenarios row directly,
// standing in for "a row that got into storage some other way" (a
// hand-run UPDATE, or a future bug) — precisely the case scanScenario's own
// decode step already exists to catch, one guard below this one.
func TestScanScenario_rejectsNonEmptyEndpoints(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	// A non-empty Endpoints array is a legitimate SHAPE at the bundle
	// level (C2/C3) — this is what a checkpoint's config_snap looks like,
	// never what a scenarios-table row may hold.
	b := bundle.New("ws", domain.DefaultSettings(), bundle.SpecRef{}, nil)
	b.Endpoints = []bundle.EndpointEntry{{
		Method:    "GET",
		Path:      "/custom",
		Responses: map[string]overrides.Variant{"200": {Mode: "generated"}},
	}}
	snapshot, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode fixture snapshot: %v", err)
	}

	res, err := db.W.ExecContext(t.Context(),
		`INSERT INTO scenarios (workspace_id, name, snapshot, created_at) VALUES (?, ?, ?, unixepoch())`,
		ws, "bad-snap", snapshot,
	)
	if err != nil {
		t.Fatalf("insert scenario row directly: %v", err)
	}
	scenarioID, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("scenario id: %v", err)
	}

	if _, err := repo.Get(t.Context(), ws, scenarioID); !errors.Is(err, bundle.ErrInvalid) {
		t.Errorf("Get: got %v, want an error wrapping bundle.ErrInvalid", err)
	}
	if _, err := repo.ByName(t.Context(), ws, "bad-snap"); !errors.Is(err, bundle.ErrInvalid) {
		t.Errorf("ByName: got %v, want an error wrapping bundle.ErrInvalid", err)
	}
	// Review finding 6: activation used to check only that the row existed,
	// so a snapshot no read path could decode became the ACTIVE scenario and
	// the mock plane silently served the workspace layer under it.
	if _, err := repo.SetActive(t.Context(), ws, &scenarioID); !errors.Is(err, bundle.ErrInvalid) {
		t.Errorf("SetActive: got %v, want an error wrapping bundle.ErrInvalid — an unreadable scenario must not activate", err)
	}
}

// TestCloneFrom_succeedsWhileAnotherScenarioIsActive is SIG-CLONE's central
// claim: unlike CreateFromCurrentState (A10), CloneFrom never reads the
// workspace's own layer, so an active scenario elsewhere in the workspace
// (here: the SOURCE itself, made active) must not refuse it. It also pins
// the two properties an implementation that merely copied
// CreateFromCurrentState's shape would get wrong: the stored snapshot bytes
// are IDENTICAL to the source's (a raw SQL copy, not a decode-then-encode
// round trip that could reorder or reformat anything), and the returned
// *Scenario carries a POPULATED Bundle — the one thing a re-read through the
// wrong connection (r.db.R instead of the open tx) cannot produce, per
// SIG-CLONE's own comment.
func TestCloneFrom_succeedsWhileAnotherScenarioIsActive(t *testing.T) {
	db := newTestDB(t)
	repo, overridesRepo := newRepos(t, db)
	settings := domain.DefaultSettings()
	settings.ListSize = 9
	ws := insertWorkspace(t, db, "ws", nil, settings)
	if _, _, err := overridesRepo.Put(t.Context(), ws, "GET /widgets", func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("put override: %v", err)
	}

	source, err := repo.CreateFromCurrentState(t.Context(), ws, "source")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := repo.SetActive(t.Context(), ws, &source.ID); err != nil {
		t.Fatalf("activate source: %v", err)
	}

	clone, err := repo.CloneFrom(t.Context(), ws, source.ID, "clone")
	if err != nil {
		t.Fatalf("CloneFrom while a scenario is active: %v", err)
	}

	if clone.Bundle.Workspace.Settings.ListSize != 9 {
		t.Errorf("clone.Bundle.Workspace.Settings.ListSize = %d, want 9 (a zero Bundle is what a re-read through the wrong connection produces)",
			clone.Bundle.Workspace.Settings.ListSize)
	}

	var sourceRaw, cloneRaw []byte
	if err := db.R.QueryRowContext(t.Context(), "SELECT snapshot FROM scenarios WHERE id = ?", source.ID).Scan(&sourceRaw); err != nil {
		t.Fatalf("read source snapshot: %v", err)
	}
	if err := db.R.QueryRowContext(t.Context(), "SELECT snapshot FROM scenarios WHERE id = ?", clone.ID).Scan(&cloneRaw); err != nil {
		t.Fatalf("read clone snapshot: %v", err)
	}
	if !bytes.Equal(sourceRaw, cloneRaw) {
		t.Errorf("clone's stored snapshot is not byte-identical to source's: %d bytes vs %d bytes", len(cloneRaw), len(sourceRaw))
	}
}

// TestCloneFrom_afterWorkspaceOverridesChanged_matchesSource creates the
// divergence BEFORE the clone, never after: the workspace's LIVE overrides
// change after the source scenario was already saved, and CloneFrom must
// still copy the SOURCE's stored bytes — not a fresh snapshot of the
// workspace's now-different current state, which CloneFrom never reads at
// all (SIG-CLONE).
func TestCloneFrom_afterWorkspaceOverridesChanged_matchesSource(t *testing.T) {
	db := newTestDB(t)
	repo, overridesRepo := newRepos(t, db)
	settings := domain.DefaultSettings()
	settings.ListSize = 3
	ws := insertWorkspace(t, db, "ws", nil, settings)

	source, err := repo.CreateFromCurrentState(t.Context(), ws, "source")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if len(source.Bundle.Overrides) != 0 {
		t.Fatalf("source has %d override entries before the divergence, want 0", len(source.Bundle.Overrides))
	}

	// The divergence, created BEFORE the clone: an override added to the
	// LIVE workspace after the source scenario was already saved.
	if _, _, err := overridesRepo.Put(t.Context(), ws, "GET /widgets", func(row *overrides.Row) error {
		row.Responses = map[string]overrides.Variant{"200": {Mode: "generated"}}
		return nil
	}); err != nil {
		t.Fatalf("put override after snapshotting: %v", err)
	}

	clone, err := repo.CloneFrom(t.Context(), ws, source.ID, "clone")
	if err != nil {
		t.Fatalf("CloneFrom: %v", err)
	}
	if len(clone.Bundle.Overrides) != 0 {
		t.Errorf("clone has %d override entries, want 0 (must match SOURCE, not the workspace's now-changed current state)",
			len(clone.Bundle.Overrides))
	}
	if clone.Bundle.Workspace.Settings.ListSize != 3 {
		t.Errorf("clone.Bundle.Workspace.Settings.ListSize = %d, want 3 (the source's own value)", clone.Bundle.Workspace.Settings.ListSize)
	}
}

// TestCloneFrom_crossWorkspaceSourceIsNotFound is A8's ownership scoping
// carried into CloneFrom's INSERT...SELECT: sourceID belonging to a
// DIFFERENT workspace must answer exactly like a sourceID that does not
// exist at all, and must write NO row at all — the SELECT's own WHERE
// clause makes the two indistinguishable by construction.
func TestCloneFrom_crossWorkspaceSourceIsNotFound(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	wsA := insertWorkspace(t, db, "a", nil, domain.DefaultSettings())
	wsB := insertWorkspace(t, db, "b", nil, domain.DefaultSettings())

	sB, err := repo.CreateFromCurrentState(t.Context(), wsB, "b-scenario")
	if err != nil {
		t.Fatalf("create in workspace B: %v", err)
	}

	before := scenarioRowCount(t, db)
	if _, err := repo.CloneFrom(t.Context(), wsA, sB.ID, "clone"); !errors.Is(err, scenarios.ErrNotFound) {
		t.Fatalf("CloneFrom(A, B's scenario): got %v, want ErrNotFound", err)
	}
	if after := scenarioRowCount(t, db); after != before {
		t.Errorf("CloneFrom across workspaces wrote a row: %d scenarios before, %d after", before, after)
	}
}

// TestCloneFrom_duplicateNameIsErrDuplicateName pins
// UNIQUE (workspace_id, name) on the clone's target name, exactly as
// CreateFromCurrentState's own duplicate-name test does for create.
func TestCloneFrom_duplicateNameIsErrDuplicateName(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	source, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}
	if _, err := repo.CreateFromCurrentState(t.Context(), ws, "two"); err != nil {
		t.Fatalf("create second scenario: %v", err)
	}

	if _, err := repo.CloneFrom(t.Context(), ws, source.ID, "two"); !errors.Is(err, scenarios.ErrDuplicateName) {
		t.Fatalf("CloneFrom with a taken name: got %v, want ErrDuplicateName", err)
	}
}

// TestCloneFrom_blankNameIsErrInvalidName covers ErrInvalidName on the SAME
// path the create side already uses (SIG-CLONE), checked BEFORE either the
// ErrNotFound or ErrDuplicateName branch ever runs.
func TestCloneFrom_blankNameIsErrInvalidName(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	source, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create source: %v", err)
	}

	if _, err := repo.CloneFrom(t.Context(), ws, source.ID, "   "); !errors.Is(err, scenarios.ErrInvalidName) {
		t.Fatalf("CloneFrom with a blank name: got %v, want ErrInvalidName", err)
	}
}

// TestRename_changesNameScopedToWorkspaceLeavesRevisionAndReturnsBundle
// covers Rename's whole contract in one pass: the name actually changes,
// scoping to the workspace refuses a scenario id belonging to ANOTHER one
// exactly like every other lookup in this package (A8), workspaces.revision
// is left untouched (§4: the runtime cache never keys on a scenario's
// name), and the returned *Scenario carries a populated Bundle — asserted
// on a field inside it, since `.Name` alone is a top-level column that
// would come back populated whether or not the re-read went through the
// open transaction, and so would not catch a re-read through the wrong one.
func TestRename_changesNameScopedToWorkspaceLeavesRevisionAndReturnsBundle(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	settings := domain.DefaultSettings()
	settings.ListSize = 5
	wsA := insertWorkspace(t, db, "a", nil, settings)
	wsB := insertWorkspace(t, db, "b", nil, domain.DefaultSettings())

	sA, err := repo.CreateFromCurrentState(t.Context(), wsA, "old-name")
	if err != nil {
		t.Fatalf("create in workspace A: %v", err)
	}
	sB, err := repo.CreateFromCurrentState(t.Context(), wsB, "b-scenario")
	if err != nil {
		t.Fatalf("create in workspace B: %v", err)
	}

	revBefore := workspaceRevision(t, db, wsA)

	renamed, err := repo.Rename(t.Context(), wsA, sA.ID, "new-name")
	if err != nil {
		t.Fatalf("rename: %v", err)
	}
	if renamed.Name != "new-name" {
		t.Errorf("renamed.Name = %q, want new-name", renamed.Name)
	}
	if renamed.Bundle.Workspace.Settings.ListSize != 5 {
		t.Errorf("renamed.Bundle.Workspace.Settings.ListSize = %d, want 5 (a zero Bundle is what a re-read through the wrong connection produces)",
			renamed.Bundle.Workspace.Settings.ListSize)
	}

	if revAfter := workspaceRevision(t, db, wsA); revAfter != revBefore {
		t.Errorf("workspace A revision after rename = %d, want unchanged %d (§4: a rename bumps no revision)", revAfter, revBefore)
	}

	got, err := repo.Get(t.Context(), wsA, sA.ID)
	if err != nil {
		t.Fatalf("Get after rename: %v", err)
	}
	if got.Name != "new-name" {
		t.Errorf("Get after rename returned Name = %q, want new-name", got.Name)
	}

	// A8: B's scenario id, renamed through A's workspace, is out of scope —
	// exactly like Get/SetActive/Delete's own cross-workspace refusal.
	if _, err := repo.Rename(t.Context(), wsA, sB.ID, "stolen"); !errors.Is(err, scenarios.ErrNotFound) {
		t.Errorf("Rename(A, B's scenario): got %v, want ErrNotFound", err)
	}
	gotB, gerr := repo.Get(t.Context(), wsB, sB.ID)
	if gerr != nil {
		t.Fatalf("Get workspace B's scenario after A's failed cross-workspace rename: %v", gerr)
	}
	if gotB.Name != "b-scenario" {
		t.Errorf("workspace B's scenario name after A's failed cross-workspace rename = %q, want unchanged b-scenario", gotB.Name)
	}
}

// TestRename_duplicateNameIsErrDuplicateName pins UNIQUE (workspace_id,
// name) on the rename target, the same way CreateFromCurrentState's own
// test does for create.
func TestRename_duplicateNameIsErrDuplicateName(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	a, err := repo.CreateFromCurrentState(t.Context(), ws, "a")
	if err != nil {
		t.Fatalf("create a: %v", err)
	}
	if _, err := repo.CreateFromCurrentState(t.Context(), ws, "b"); err != nil {
		t.Fatalf("create b: %v", err)
	}

	if _, err := repo.Rename(t.Context(), ws, a.ID, "b"); !errors.Is(err, scenarios.ErrDuplicateName) {
		t.Fatalf("Rename to a taken name: got %v, want ErrDuplicateName", err)
	}
}

// TestRename_blankNameIsErrInvalidName is Rename's half of the blank-name
// contract SIG-CLONE and SIG-RENAME share — Rename validates through the
// SAME ErrInvalidName path before ever issuing its UPDATE.
func TestRename_blankNameIsErrInvalidName(t *testing.T) {
	db := newTestDB(t)
	repo, _ := newRepos(t, db)
	ws := insertWorkspace(t, db, "ws", nil, domain.DefaultSettings())

	s, err := repo.CreateFromCurrentState(t.Context(), ws, "one")
	if err != nil {
		t.Fatalf("create: %v", err)
	}

	if _, err := repo.Rename(t.Context(), ws, s.ID, "   "); !errors.Is(err, scenarios.ErrInvalidName) {
		t.Fatalf("Rename with a blank name: got %v, want ErrInvalidName", err)
	}
}
