// reset_test.go is D3's acceptance clauses 1 through 20 (including 19a),
// minus the four D10 assigns to internal/admin (6, 7, 8, 19a's status half)
// — this package's own share of decisions.md (mocker-p3b-resources).
// `package resources`, like repo_test.go, for the same two reasons: several
// tests here reach for [resetPreWriteHook] and [fenceResetTx] directly, and
// the exported [Repo.ResetData] surface is what every wire-observable
// property goes through, same as any real caller would use it.
package resources

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
)

// fixtureDocUsersOnly is fixtureDoc's /users fragment alone — the SAME
// schema, so a resources row confirmed under fixtureDoc still fits this
// spec's own derived suggestion byte for byte. It exists to STRAND
// /widgets (and /notes, /gadgets) without touching /users: rebinding a
// workspace to this spec's id makes [Repo.findSuggestion] return nil for
// every family this document does not declare (D3 R8's "stranded"), while
// /users stays confirmable exactly as before.
const fixtureDocUsersOnly = `{
  "openapi": "3.0.3",
  "info": {"title": "resources fixture (users only)", "version": "1.0.0"},
  "paths": {
    "/users": {
      "get": {
        "operationId": "listUsers",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/User"}}
        }}}}
      }
    },
    "/users/{userId}": {
      "get": {
        "operationId": "getUser",
        "parameters": [{"name": "userId", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/User"}
        }}}}
      }
    }
  },
  "components": {
    "schemas": {
      "User": {"type": "object", "properties": {"userId": {"type": "integer"}, "name": {"type": "string"}}}
    }
  }
}`

// brokenDoc declares a real route family, /broken, whose 200 responses
// carry NO "content" at all — a shape [internal/specs]' own derivation
// refuses to suggest (D3 clause 19's own text: "derivation already screens
// out everything gen.Body would fail on"). It exists purely so a test can
// hand-insert a resource_suggestions row for it (bypassing derivation,
// exactly as EnsureSuggestions' own lazy-backfill rule allows — a spec
// that already has ANY resource_suggestions row, including this hand-built
// one, is never re-derived) and observe [Repo.ResetData] fail to generate
// a population for a family it otherwise believes is live.
const brokenDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "broken", "version": "1.0.0"},
  "paths": {
    "/broken": {
      "get": {"operationId": "listBroken", "responses": {"200": {"description": "d"}}}
    },
    "/broken/{id}": {
      "get": {
        "operationId": "getBroken",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d"}}
      }
    }
  }
}`

// insertSuggestionRow hand-writes one resource_suggestions row, bypassing
// [internal/specs]' own deriveSuggestions entirely — clause 19's fixture
// technique: a family derivation refuses (no JSON content, no derivable id
// type) still needs a suggestion row for [Repo.findSuggestion] to find, or
// [Repo.prepareReset] would report it `stranded` rather than
// `population_failed` (D3 R8's own discrimination rule).
func insertSuggestionRow(t *testing.T, db *store.DB, specID int64, family string) {
	t.Helper()
	wrapper, err := jsonx.Marshal(specs.Wrapper{IDType: "integer"})
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resource_suggestions (spec_id, gen, route_family, name, id_field, entity_schema, wrapper, confidence)
		VALUES (?, 1, ?, ?, 'id', '/dummy', ?, 1.0)`,
		specID, family, family, string(wrapper)); err != nil {
		t.Fatalf("insert resource suggestion row for %q: %v", family, err)
	}
}

// insertEntityRow hand-writes one entities row directly — used only by the
// population_failed fixture, which needs pre-existing rows to prove
// [Repo.ResetData] leaves a population_failed family's rows standing
// without ever routing them through [Repo.Confirm] (that path is
// unreachable for /broken: derivation itself refuses the family).
func insertEntityRow(t *testing.T, db *store.DB, resourceID int64, entityKey string) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO entities (resource_id, scope_key, entity_key, data, created_at, updated_at)
		VALUES (?, '', ?, '{"id":1}', 0, 0)`, resourceID, entityKey); err != nil {
		t.Fatalf("insert entity row: %v", err)
	}
}

// rebindSpec points a workspace at a different spec — direct SQL, mirroring
// insertWorkspace's own pattern for that table, standing in for a real
// PATCH /api/workspaces/{id} the admin plane would otherwise perform.
func rebindSpec(t *testing.T, db *store.DB, workspaceID, specID int64) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET spec_id = ? WHERE id = ?", specID, workspaceID); err != nil {
		t.Fatalf("rebind workspace %d to spec %d: %v", workspaceID, specID, err)
	}
}

// insertScenario writes one scenarios row carrying settings as its
// snapshot's ONLY meaningful field for this file's purposes — the encoded
// document [Repo.effectiveSettings] decodes back via [bundle.Decode] when
// a workspace's scenario_id points at it.
func insertScenario(t *testing.T, db *store.DB, workspaceID int64, name string, settings domain.Settings) int64 {
	t.Helper()
	b := bundle.New("ws", settings, bundle.SpecRef{Name: "spec", Hash: "h"}, nil)
	blob, err := bundle.Encode(b)
	if err != nil {
		t.Fatalf("encode scenario snapshot: %v", err)
	}
	res, err := db.W.ExecContext(t.Context(),
		"INSERT INTO scenarios (workspace_id, name, snapshot, created_at) VALUES (?, ?, ?, 0)",
		workspaceID, name, blob)
	if err != nil {
		t.Fatalf("insert scenario: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("scenario id: %v", err)
	}
	return id
}

func activateScenario(t *testing.T, db *store.DB, workspaceID, scenarioID int64) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET scenario_id = ? WHERE id = ?", scenarioID, workspaceID); err != nil {
		t.Fatalf("activate scenario %d for workspace %d: %v", scenarioID, workspaceID, err)
	}
}

// --- clause 1: clear deletes entities, keeps resources ---------------------

func TestResetData_Clear_DeletesEntitiesKeepsResourcesRows(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 5})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	widgets, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets: %v", err)
	}
	users, err := repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm users: %v", err)
	}
	if entityCount(t, db, widgets.ID) != 5 || entityCount(t, db, users.ID) != 5 {
		t.Fatalf("test precondition broken: want 5 entities per family before clear")
	}

	out, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil {
		t.Fatalf("ResetData(clear): %v", err)
	}
	if !out.Changed {
		t.Errorf("Changed = false, want true")
	}
	if out.Deleted != 10 {
		t.Errorf("Deleted = %d, want 10 (5 widgets + 5 users)", out.Deleted)
	}
	if len(out.Skipped) != 0 {
		t.Errorf("Skipped = %v, want none — clear has no skip cases", out.Skipped)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 1 || resourceRowCount(t, db, wsID, familyUsers) != 1 {
		t.Fatalf("clear deleted a resources row — families must survive, only entities go")
	}
	if entityCount(t, db, widgets.ID) != 0 || entityCount(t, db, users.ID) != 0 {
		t.Fatalf("entities survived clear")
	}
}

// --- clause 2: two reseeds with no config change are byte-identical -------

func TestResetData_Reseed_Idempotent(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 42, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); err != nil {
		t.Fatalf("first reseed: %v", err)
	}
	first, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List after first reseed: %v", err)
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); err != nil {
		t.Fatalf("second reseed: %v", err)
	}
	second, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List after second reseed: %v", err)
	}

	if len(first) != len(second) {
		t.Fatalf("row count changed between reseeds: %d vs %d", len(first), len(second))
	}
	firstByKey := make(map[string]string, len(first))
	for _, e := range first {
		firstByKey[e.EntityKey] = string(e.Data)
	}
	for _, e := range second {
		want, ok := firstByKey[e.EntityKey]
		if !ok {
			t.Fatalf("entity_key %q present after second reseed but not first", e.EntityKey)
		}
		if string(e.Data) != want {
			t.Errorf("data for entity_key %q changed between two reseeds with no config change: %s vs %s", e.EntityKey, want, e.Data)
		}
	}
}

// --- clause 3: a listSize edit changes DATA but not ROW COUNT -------------

func TestResetData_Reseed_ListSizeEditChangesDataNotRowCount(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyNotes)
	if err != nil {
		t.Fatalf("Confirm notes: %v", err)
	}
	before, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List before reseed: %v", err)
	}
	if len(before) != 2 {
		t.Fatalf("entity count before reseed = %d, want 2 (seed_count)", len(before))
	}

	// listSize moves; res.SeedCount (stored on the resources ROW at
	// confirm time) does not — this is exactly what reseed must key its
	// row count on, per D3 R7.
	mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 30})

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if !out.Changed {
		t.Fatalf("Changed = false, want true")
	}
	after, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List after reseed: %v", err)
	}
	if len(after) != 2 {
		t.Fatalf("entity count after reseed = %d, want 2 (resources.seed_count, unaffected by the listSize edit)", len(after))
	}

	// The DATA changed: the "tags" array's length tracks the new
	// listSize=30, not the confirm-time listSize=2 (D3 R7's own example —
	// a schema with no minItems/maxItems).
	changed := false
	for i := range before {
		if string(before[i].Data) != string(after[i].Data) {
			changed = true
		}
	}
	if !changed {
		t.Errorf("no entity's data changed after a listSize edit + reseed — want at least one (the tags array length tracks the CURRENT listSize)")
	}
}

// --- clause 4: reseed resets seq to seed_count, so the next POST is N+1 --

func TestResetData_Reseed_ResetsSeqToSeedCountPlusOne(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// Move seq off its just-confirmed value first, so this test cannot
	// pass merely because seq was ALREADY at seed_count.
	if _, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "x"}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}

	created, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "y"})
	if err != nil {
		t.Fatalf("Create after reseed: %v", err)
	}
	if created.EntityKey != "4" {
		t.Errorf("entity_key after reseed at seed_count=3 = %q, want \"4\"", created.EntityKey)
	}
}

// --- clause 5: clear does NOT rewind seq -----------------------------------

func TestResetData_Clear_DoesNotRewindSeq(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "x"}); err != nil {
		t.Fatalf("Create: %v", err) // seq now 4
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha"); err != nil {
		t.Fatalf("ResetData(clear): %v", err)
	}

	created, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "y"})
	if err != nil {
		t.Fatalf("Create after clear: %v", err)
	}
	if created.EntityKey != "5" {
		t.Errorf("entity_key after clear = %q, want \"5\" (the counter is not rewound)", created.EntityKey)
	}
}

// --- clause 9: no revision bump, either mode -------------------------------

func TestResetData_NeitherModeBumpsRevision(t *testing.T) {
	t.Parallel()
	for _, mode := range []ResetMode{ResetModeReseed, ResetModeClear} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t, t.TempDir()+"/mocker.db")
			specID := importFixtureSpec(t, db)
			wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
			repo := newTestRepo(t, db, 4<<20, 64<<10)

			if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			before := workspaceRevision(t, db, wsID)
			if _, err := repo.ResetData(t.Context(), wsID, mode, "alpha"); err != nil {
				t.Fatalf("ResetData(%s): %v", mode, err)
			}
			if got := workspaceRevision(t, db, wsID); got != before {
				t.Errorf("revision after ResetData(%s) = %d, want unchanged %d", mode, got, before)
			}
		})
	}
}

// --- clauses 10, 13: fenceResetTx through ResetData itself, via the hook --

// TestResetData_Reseed_StaleConfigOnSeedEdit_NoScenario is clause 10's
// first half, exercised through [Repo.ResetData]'s own real call to
// [fenceResetTx] via [resetPreWriteHook] — never fenceResetTx run in
// isolation, which cannot tell a ResetData that still calls it apart from
// one that silently stopped (the same reasoning TestConfirm_StaleConfigViaHook
// applies to Confirm/fenceConfirmTx).
func TestResetData_Reseed_StaleConfigOnSeedEdit_NoScenario(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	resetPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{Seed: 99, ListSize: 3})
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("ResetData(reseed) racing a seed edit = %v, want ErrStaleConfig", err)
	}
	if entityCount(t, db, res.ID) != 3 {
		t.Fatalf("entities changed after a refused reseed")
	}
}

// TestResetData_Clear_SucceedsDespiteSeedEdit is clause 10's other half:
// the identical race, but mode=clear, which does not fence seed/listSize at
// all and must succeed.
func TestResetData_Clear_SucceedsDespiteSeedEdit(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	resetPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{Seed: 99, ListSize: 3})
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	out, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil {
		t.Fatalf("ResetData(clear) racing a seed edit = %v, want nil", err)
	}
	if !out.Changed || out.Deleted != 3 {
		t.Errorf("ResetData(clear) outcome = %+v, want Changed=true Deleted=3", out)
	}
}

// TestResetData_BareRevisionBumpRefusesNeitherMode is clause 13.
func TestResetData_BareRevisionBumpRefusesNeitherMode(t *testing.T) {
	for _, mode := range []ResetMode{ResetModeReseed, ResetModeClear} {
		t.Run(string(mode), func(t *testing.T) {
			db := newTestDB(t, t.TempDir()+"/mocker.db")
			specID := importFixtureSpec(t, db)
			wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
			repo := newTestRepo(t, db, 4<<20, 64<<10)
			if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
				t.Fatalf("Confirm: %v", err)
			}

			resetPreWriteHook = func() {
				if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET revision = revision + 1 WHERE id = ?", wsID); err != nil {
					t.Fatalf("bump revision: %v", err)
				}
			}
			t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

			if _, err := repo.ResetData(t.Context(), wsID, mode, "alpha"); err != nil {
				t.Fatalf("ResetData(%s) after a bare revision bump = %v, want nil", mode, err)
			}
		})
	}
}

// --- clause 11: an active scenario supplies the seed, workspace edits don't

func TestResetData_Reseed_ActiveScenarioSuppliesSeed(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	scenarioSettings := domain.Settings{Seed: 777, ListSize: 3}
	scID := insertScenario(t, db, wsID, "s1", scenarioSettings)
	activateScenario(t, db, wsID, scID)

	// The workspace's OWN settings change after activation — the fence
	// must not care, because a scenario is active.
	resetPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{Seed: 555, ListSize: 3})
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed) with an active scenario and a workspace settings edit = %v, want nil", err)
	}
	if !out.Changed {
		t.Fatalf("Changed = false, want true")
	}

	// The generated rows must match what the SCENARIO's own seed (777)
	// produces, not the workspace's edited seed (555) nor its original one
	// (1) — computed through the identical generation path this package's
	// own reseed uses.
	generator, _, err := repo.buildGenerator(t.Context(), specID, scenarioSettings)
	if err != nil {
		t.Fatalf("buildGenerator: %v", err)
	}
	routes, err := repo.specs.Routes(t.Context(), specID)
	if err != nil {
		t.Fatalf("Routes: %v", err)
	}
	variants, err := repo.specs.Variants(t.Context(), specID)
	if err != nil {
		t.Fatalf("Variants: %v", err)
	}
	detailRoute, idParam, _, err := locateFamilyOperations(routes, familyWidgets)
	if err != nil {
		t.Fatalf("locateFamilyOperations: %v", err)
	}
	detailVariant, ok := defaultVariant200(variants[detailRoute.OpRowID])
	if !ok {
		t.Fatalf("no default 200 variant for widgets")
	}
	wantBodies, err := populateEntities(generator, detailVariant, familyWidgets, idParam, 3, 1, res.IDField, res.Wrapper.IDType)
	if err != nil {
		t.Fatalf("populateEntities: %v", err)
	}

	got, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(got) != len(wantBodies) {
		t.Fatalf("row count = %d, want %d", len(got), len(wantBodies))
	}
	for i, e := range got {
		if string(e.Data) != string(wantBodies[i]) {
			t.Errorf("entity %d data = %s, want %s (the scenario's own seed=777)", i, e.Data, wantBodies[i])
		}
	}
}

// --- clause 12: the roster fence -------------------------------------------

func TestResetData_Reseed_RosterFence(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets: %v", err)
	}
	before, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List before: %v", err)
	}

	// A second family confirmed AFTER prepareReset's own roster read, but
	// before the write transaction — via the hook, exactly the window
	// D3 R10 names.
	resetPreWriteHook = func() {
		if _, err := repo.Confirm(t.Context(), wsID, familyUsers); err != nil {
			t.Fatalf("concurrent Confirm users: %v", err)
		}
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("ResetData(reseed) racing a concurrent confirm = %v, want ErrStaleConfig", err)
	}

	after, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List after: %v", err)
	}
	if len(after) != len(before) {
		t.Fatalf("widgets entity count changed after a refused reseed: %d vs %d", len(after), len(before))
	}
	for i := range before {
		if string(before[i].Data) != string(after[i].Data) {
			t.Errorf("widgets entity %d data changed after a refused reseed", i)
		}
	}
}

// --- clause 14: the no-op count is taken LIVE, inside the transaction -----

func TestResetData_Clear_NoOpThenConcurrentInsertGetsDeletedAnyway(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 1})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// First clear: a real delete.
	out, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil || !out.Changed || out.Deleted != 1 {
		t.Fatalf("first clear = %+v, %v, want Changed=true Deleted=1, nil", out, err)
	}

	// Second clear over now-empty entities: a genuine no-op.
	out2, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil {
		t.Fatalf("second clear: %v", err)
	}
	if out2.Changed || out2.Deleted != 0 {
		t.Errorf("second clear (no-op) = %+v, want Changed=false Deleted=0", out2)
	}

	// Third clear: an entity inserted DURING the pre-write window, after
	// the no-op count would have been decided by a PRE-transaction read,
	// must still be deleted by THIS call.
	resetPreWriteHook = func() {
		insertEntityRow(t, db, res.ID, "999")
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	out3, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil {
		t.Fatalf("third clear: %v", err)
	}
	if !out3.Changed || out3.Deleted != 1 {
		t.Errorf("third clear = %+v, want Changed=true Deleted=1 (the concurrently-inserted row)", out3)
	}
	if entityCount(t, db, res.ID) != 0 {
		t.Errorf("entities remain after the third clear")
	}
}

// --- clause 15: reseed from zero repopulates ------------------------------

func TestResetData_Reseed_FromZeroRepopulates(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 4})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if _, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha"); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if entityCount(t, db, res.ID) != 0 {
		t.Fatalf("test precondition broken: want 0 entities after clear")
	}

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("reseed from zero: %v", err)
	}
	if !out.Changed {
		t.Errorf("Changed = false, want true")
	}
	if got := entityCount(t, db, res.ID); got != 4 {
		t.Errorf("entity count after reseed from zero = %d, want 4 (seed_count)", got)
	}
}

// --- clauses 16, 17, 20: per-family stranded skip, and clear ignoring it --

// strandOneOfTwoFamilies confirms widgets (which will go stranded) and
// users (which stays valid) under fixtureDoc, then rebinds the workspace
// to fixtureDocUsersOnly — a spec that still declares /users, identically,
// but not /widgets at all.
func strandOneOfTwoFamilies(t *testing.T, db *store.DB, repo *Repo) (wsID int64, widgets, users *Resource) {
	t.Helper()
	specA := importFixtureSpec(t, db)
	wsID = insertWorkspace(t, db, "alpha", &specA, domain.Settings{Seed: 1, ListSize: 2})

	var err error
	widgets, err = repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets: %v", err)
	}
	users, err = repo.Confirm(t.Context(), wsID, familyUsers)
	if err != nil {
		t.Fatalf("Confirm users: %v", err)
	}

	specB := importSpecDoc(t, db, []byte(fixtureDocUsersOnly))
	rebindSpec(t, db, wsID, specB)
	return wsID, widgets, users
}

// TestResetData_Reseed_AllFamiliesStranded is clause 16: the only confirmed
// family is stranded, so the call deletes and inserts nothing.
func TestResetData_Reseed_AllFamiliesStranded(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specA := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specA, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	specB := importSpecDoc(t, db, []byte(fixtureDocUsersOnly))
	rebindSpec(t, db, wsID, specB)

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — every family was skipped")
	}
	if out.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", out.Deleted)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyWidgets || out.Skipped[0].Reason != skipReasonStranded {
		t.Fatalf("Skipped = %+v, want [{%s stranded}]", out.Skipped, familyWidgets)
	}
	if got := entityCount(t, db, res.ID); got != 2 {
		t.Errorf("entity count = %d, want 2 (unchanged — the row was left standing)", got)
	}
}

// TestResetData_Reseed_OneStrandedOneRepopulated is clause 17.
func TestResetData_Reseed_OneStrandedOneRepopulated(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	wsID, widgets, users := strandOneOfTwoFamilies(t, db, repo)

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if !out.Changed {
		t.Errorf("Changed = false, want true — users was repopulated")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyWidgets || out.Skipped[0].Reason != skipReasonStranded {
		t.Fatalf("Skipped = %+v, want [{%s stranded}]", out.Skipped, familyWidgets)
	}
	if got := entityCount(t, db, widgets.ID); got != 2 {
		t.Errorf("widgets entity count = %d, want 2 (left standing)", got)
	}
	if got := entityCount(t, db, users.ID); got != 2 {
		t.Errorf("users entity count = %d, want 2 (repopulated)", got)
	}
}

// TestResetData_Clear_IgnoresStrandingDeletesEverything is clause 20: clear
// over the SAME stranded/valid pair deletes both — stranding is a reseed
// concept the DELETE-side of clear never consults.
func TestResetData_Clear_IgnoresStrandingDeletesEverything(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	wsID, widgets, users := strandOneOfTwoFamilies(t, db, repo)

	out, err := repo.ResetData(t.Context(), wsID, ResetModeClear, "alpha")
	if err != nil {
		t.Fatalf("ResetData(clear): %v", err)
	}
	if !out.Changed || out.Deleted != 4 {
		t.Errorf("ResetData(clear) = %+v, want Changed=true Deleted=4", out)
	}
	if entityCount(t, db, widgets.ID) != 0 || entityCount(t, db, users.ID) != 0 {
		t.Errorf("clear left rows behind for the stranded family")
	}
}

// --- clause 18: over_caps -------------------------------------------------

func TestResetData_Reseed_OverCapsSkip(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 50})
	generous := newTestRepo(t, db, 64<<20, 64<<10)

	res, err := generous.Confirm(t.Context(), wsID, familyGadgets)
	if err != nil {
		t.Fatalf("Confirm (generous cap): %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 50 {
		t.Fatalf("test precondition broken: want 50 entities, got %d", got)
	}

	// A second Repo over the SAME db, with a byte-total cap far too small
	// for 50 regenerated gadget bodies — checkBatchCaps fails during
	// prepareReset, reported as over_caps rather than refusing the call.
	tight := newTestRepo(t, db, 500, 64<<10)
	out, err := tight.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed) over the tight cap: %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — the only family was skipped")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyGadgets || out.Skipped[0].Reason != skipReasonOverCaps {
		t.Fatalf("Skipped = %+v, want [{%s over_caps}]", out.Skipped, familyGadgets)
	}
	if got := entityCount(t, db, res.ID); got != 50 {
		t.Errorf("entity count = %d, want 50 (left standing)", got)
	}
}

// --- clause 19: population_failed ------------------------------------------

func TestResetData_Reseed_PopulationFailedSkip(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(brokenDoc))
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// Bypass Confirm entirely (derivation refuses /broken — no JSON
	// content on its 200): a hand-inserted resources row, a hand-inserted
	// resource_suggestions row so findSuggestion does not report
	// `stranded`, and two hand-inserted entity rows standing in for a
	// population an earlier, differently-shaped spec actually produced.
	const family = "/broken"
	insertSuggestionRow(t, db, specID, family)
	resourceID := insertResourceRow(t, db, wsID, family, "id", "integer")
	// insertResourceRow's own default is seed_count=0 (its other callers
	// never populate through it) — this fixture needs a non-zero one, or
	// populateEntities' own 1..seedCount loop runs zero times and reports
	// a trivial, error-free EMPTY population rather than failing at all.
	if _, err := db.W.ExecContext(t.Context(), "UPDATE resources SET seed_count = 2 WHERE id = ?", resourceID); err != nil {
		t.Fatalf("set seed_count: %v", err)
	}
	insertEntityRow(t, db, resourceID, "1")
	insertEntityRow(t, db, resourceID, "2")

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false")
	}
	if out.Deleted != 0 {
		t.Errorf("Deleted = %d, want 0", out.Deleted)
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != family || out.Skipped[0].Reason != skipReasonPopulationFailed {
		t.Fatalf("Skipped = %+v, want [{%s population_failed}]", out.Skipped, family)
	}
	if got := entityCount(t, db, resourceID); got != 2 {
		t.Errorf("entity count = %d, want 2 (left standing)", got)
	}
}

// --- clause 19a (the internal/resources half): empty roster, no spec read -

func TestResetData_EmptyRoster_NoSpecBound(t *testing.T) {
	t.Parallel()
	for _, mode := range []ResetMode{ResetModeReseed, ResetModeClear} {
		t.Run(string(mode), func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t, t.TempDir()+"/mocker.db")
			wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
			repo := newTestRepo(t, db, 4<<20, 64<<10)

			// A nil spec_id would panic on dereference the moment any
			// spec-reading code ran — this call succeeding at all is
			// already evidence the spec was never read.
			out, err := repo.ResetData(t.Context(), wsID, mode, "alpha")
			if err != nil {
				t.Fatalf("ResetData(%s) over an empty roster with no spec bound: %v", mode, err)
			}
			if out.Changed || out.Deleted != 0 || len(out.Skipped) != 0 {
				t.Errorf("ResetData(%s) = %+v, want the zero ResetOutcome", mode, out)
			}
		})
	}
}

// --- clause 7's third property: WHICH slug comparison caught the rename ---

// TestResetData_RenameBetweenTheReadAndTheWrite_CaughtInsideTheTransaction
// is the half of clause 7 that no other test observes.
//
// ResetData compares confirmSlug TWICE (D3 R9): once before the
// transaction, against the slug [Repo.readWorkspaceCore] just read, and
// once INSIDE it against a fresh SELECT. Only the second is authoritative
// — the window between those two reads is exactly where another request
// renames the workspace, and the pre-check cannot see it.
//
// Both comparisons call [compareConfirmSlug] and both therefore answer
// ErrConfirmSlugMismatch, so an ERROR CODE cannot say which of them fired.
// A fixture that renames BEFORE the call is refused by the pre-check and
// leaves the in-transaction read unexecuted: delete it and every test in
// this package and in internal/admin stays green. That is what the
// acceptance step of the P3b run measured, by deleting it.
//
// So the rename has to land in the window itself, and
// [resetPreWriteHook] is the one seam placed there — after the
// pre-transaction read, before db.Write. The pre-check sees "alpha" and
// passes; the transaction's own SELECT sees "renamed" and refuses.
//
// Verified by MUTATION: removing the in-transaction comparison from
// resetTx (reset.go step 3) makes this test, and only this test, red.
func TestResetData_RenameBetweenTheReadAndTheWrite_CaughtInsideTheTransaction(t *testing.T) {
	for _, mode := range []ResetMode{ResetModeReseed, ResetModeClear} {
		t.Run(string(mode), func(t *testing.T) {
			db := newTestDB(t, t.TempDir()+"/mocker.db")
			specID := importFixtureSpec(t, db)
			wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
			repo := newTestRepo(t, db, 4<<20, 64<<10)
			res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			before := entityCount(t, db, res.ID)
			if before == 0 {
				t.Fatalf("fixture confirms nothing: entity count is 0")
			}

			// The rename lands in the window: ResetData has already read
			// "alpha" and compared it, and has not yet opened db.Write.
			resetPreWriteHook = func() {
				mustRenameWorkspace(t, db, wsID, "renamed")
			}
			t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

			// "alpha" was the caller's slug and it was CORRECT when they
			// sent it, so the pre-check passes and only the transaction's
			// own re-read can refuse.
			_, err = repo.ResetData(t.Context(), wsID, mode, "alpha")
			if !errors.Is(err, ErrConfirmSlugMismatch) {
				t.Fatalf("ResetData(%s) racing a rename = %v, want ErrConfirmSlugMismatch", mode, err)
			}
			if got := entityCount(t, db, res.ID); got != before {
				t.Fatalf("a refused ResetData(%s) changed the entity count: %d, want %d", mode, got, before)
			}
		})
	}
}

func mustRenameWorkspace(t *testing.T, db *store.DB, wsID int64, slug string) {
	t.Helper()
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET slug = ? WHERE id = ?", slug, wsID); err != nil {
		t.Fatalf("rename workspace: %v", err)
	}
}

// --- P3g: deep reseed groups (depth 2/3), decisions.md mocker-p3g-deep-nesting

// TestResetData_Reseed_DeepSubtreeGroup_LeafOverCaps_SkipsWholeGroup is
// P12: a reseed GROUP is a whole SUBTREE, repopulated or skipped
// atomically — not "root plus direct children" (P3e's own shape,
// [familyGroups]' P3g rewrite). Root and middle populate small (2 and 4
// rows); the leaf's own SeedCount is frozen large at ITS OWN confirm, so a
// tight total-byte cap at RESET time fails only the leaf. Correct code
// skips all THREE (group_skipped for root and middle, over_caps for the
// leaf, its own reason) and changes no row anywhere in the subtree.
// Mutation this fails under: grouping root with its direct children only,
// which would repopulate root and middle while leaving the leaf standing
// under stale scopes.
func TestResetData_Reseed_DeepSubtreeGroup_LeafOverCaps_SkipsWholeGroup(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	generous := newTestRepo(t, db, 64<<20, 64<<10)

	if _, err := generous.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs); err != nil {
		t.Fatalf("confirm root: %v", err)
	}
	if _, err := generous.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams); err != nil {
		t.Fatalf("confirm middle: %v", err)
	}
	// The leaf's own SeedCount is frozen at ITS OWN confirm time (reset.go's
	// own comment) — raise ListSize just before confirming it so its
	// population (4 team-scopes * 50 = 200 rows) is large next to the
	// root's (2) and middle's (4).
	mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 50})
	if _, err := generous.Confirm(t.Context(), wsID, testspec.FamilyDeepUsers); err != nil {
		t.Fatalf("confirm leaf: %v", err)
	}
	leafID := mustResourceID(t, db, wsID, testspec.FamilyDeepUsers)
	if got := entityCount(t, db, leafID); got != 200 {
		t.Fatalf("test precondition broken: want 200 leaf entities (4 team-scopes * 50), got %d", got)
	}

	// A total-byte cap that fits root+middle's own reseed but not the
	// leaf's — the identical technique P3e's own
	// TestResetData_Reseed_NestedGroupAtomicity_ChildOverCaps uses at one
	// level.
	tight := newTestRepo(t, db, 4000, 64<<10)
	out, err := tight.ResetData(t.Context(), wsID, ResetModeReseed, "acme")
	if err != nil {
		t.Fatalf("ResetData(reseed) over the tight cap: %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — the whole subtree was skipped, nothing deleted or inserted")
	}

	want := map[string]string{
		testspec.FamilyDeepOrgs:  skipReasonGroupSkipped,
		testspec.FamilyDeepTeams: skipReasonGroupSkipped,
		testspec.FamilyDeepUsers: skipReasonOverCaps,
	}
	if len(out.Skipped) != len(want) {
		t.Fatalf("Skipped = %+v, want %d entries", out.Skipped, len(want))
	}
	for _, sk := range out.Skipped {
		wantReason, ok := want[sk.RouteFamily]
		if !ok {
			t.Errorf("Skipped names unexpected family %q", sk.RouteFamily)
			continue
		}
		if sk.Reason != wantReason {
			t.Errorf("Skipped[%q].Reason = %q, want %q (D8.2 rule 2 — a whole subtree skips or repopulates together)", sk.RouteFamily, sk.Reason, wantReason)
		}
	}

	if got := entityCount(t, db, mustResourceID(t, db, wsID, testspec.FamilyDeepOrgs)); got != 2 {
		t.Errorf("root rows changed by a skipped group, got %d, want 2 (untouched)", got)
	}
	if got := entityCount(t, db, mustResourceID(t, db, wsID, testspec.FamilyDeepTeams)); got != 4 {
		t.Errorf("middle rows changed by a skipped group, got %d, want 4 (untouched)", got)
	}
	if got := entityCount(t, db, leafID); got != 200 {
		t.Errorf("leaf rows changed by a skipped group, got %d, want 200 (untouched)", got)
	}
}

// TestResetData_Reseed_DeepSubtree_ReScopesBothHopsToPreparedKeys is P13: a
// reseed re-scopes each level to the level above's NEWLY minted keys, at
// EVERY hop of a three-level chain — not just the first. A row POSTed into
// the ROOT (before the reseed) and a row POSTed into a MIDDLE scope (also
// before the reseed) make both levels' LIVE keys differ from what the
// reseed will freshly mint, so an implementation that threads the root's
// prepared keys correctly into the middle but then falls back to the
// middle's LIVE rows for the leaf — "correct at depth 1, wrong at depth
// 2" — is exactly what this test would catch and a single-hop fixture
// cannot.
func TestResetData_Reseed_DeepSubtree_ReScopesBothHopsToPreparedKeys(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, wsID := confirmDeepChain(t, 2)

	// Extra LIVE row at the ROOT: org keys are "1","2" after confirm, so
	// the POST mints "3" — a key the reseed's own fresh "1","2" will not
	// carry.
	extraOrg, err := repo.Create(t.Context(), org.ID, "", "", org.IDField, org.Wrapper.IDType, map[string]any{"name": "extra org"})
	if err != nil {
		t.Fatalf("post extra org: %v", err)
	}
	if extraOrg.EntityKey != "3" {
		t.Fatalf("test precondition: extra org key = %q, want \"3\"", extraOrg.EntityKey)
	}

	// Extra LIVE row inside a MIDDLE scope (org "1"'s own teams, which hold
	// keys "1","2" after confirm): the POST mints "5" (the family-wide next
	// key, after the 4 confirmed teams) — a key the reseed's own fresh
	// per-scope pair will not carry either.
	extraTeam, err := repo.Create(t.Context(), teams.ID, "", EncodeScope([]string{"1"}), teams.IDField, teams.Wrapper.IDType, map[string]any{"name": "extra team"})
	if err != nil {
		t.Fatalf("post extra team: %v", err)
	}
	if extraTeam.EntityKey != "5" {
		t.Fatalf("test precondition: extra team key = %q, want \"5\" (family-wide next after the 4 confirmed)", extraTeam.EntityKey)
	}

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "acme"); err != nil {
		t.Fatalf("ResetData reseed: %v", err)
	}

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs after reseed: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("orgs after reseed = %d, want 2 (a reseed always renumbers \"1\"..\"N\")", len(orgEntities))
	}
	for i, want := range []string{"1", "2"} {
		if orgEntities[i].EntityKey != want {
			t.Fatalf("org entity %d key = %q, want %q", i, orgEntities[i].EntityKey, want)
		}
	}

	// FIRST HOP: middle re-scopes to the root's NEW keys, never to "3" —
	// the deleted extra org's now-gone key.
	if rows, lerr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{"3"})); lerr != nil || len(rows) != 0 {
		t.Fatalf(`teams under scope "3" (the deleted extra org's key) = %d, %v, want 0, nil`, len(rows), lerr)
	}

	totalTeams := 0
	for _, orgE := range orgEntities {
		rows, lerr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if lerr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, lerr)
		}
		if len(rows) != 2 {
			t.Fatalf("teams under org %q = %d, want 2", orgE.EntityKey, len(rows))
		}
		totalTeams += len(rows)
	}
	if totalTeams != 4 {
		t.Fatalf("total teams after reseed = %d, want 4", totalTeams)
	}

	// SECOND HOP: leaf re-scopes to the MIDDLE's NEW keys, never to
	// "1/5" — the deleted extra team's now-gone key. This is the hop P13
	// exists for: consuming the middle's own PREPARED keys, never its live
	// rows, at the SECOND level of the chain as well as the first.
	if rows, lerr := repo.List(t.Context(), users.ID, "", EncodeScope([]string{"1", "5"})); lerr != nil || len(rows) != 0 {
		t.Fatalf(`users under scope "1/5" (the deleted extra team's key) = %d, %v, want 0, nil`, len(rows), lerr)
	}

	totalUsers := 0
	for _, orgE := range orgEntities {
		teamEntities, lerr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if lerr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, lerr)
		}
		for _, teamE := range teamEntities {
			scope := EncodeScope([]string{orgE.EntityKey, teamE.EntityKey})
			rows, uerr := repo.List(t.Context(), users.ID, "", scope)
			if uerr != nil {
				t.Fatalf("list users under scope %q: %v", scope, uerr)
			}
			if len(rows) != 2 {
				t.Fatalf("users under scope %q = %d, want 2 (D8.3: the second hop must consume the middle's own PREPARED keys)", scope, len(rows))
			}
			totalUsers += len(rows)
		}
	}
	if totalUsers != 8 {
		t.Fatalf("total users after reseed = %d, want 8", totalUsers)
	}
}

// --- P3h: reset-data over the declared base-scope set — D14's P20/P20b ---

// TestResetData_Reseed_RepopulatesEveryDeclaredBaseScope is P20: a
// workspace with TWO declared base values, reseeded, holds its own
// disjoint row set under each — the shape an implementation that never
// learned about the declared set fails (it repopulates ONE base scope and
// still reports changed:true).
func TestResetData_Reseed_RepopulatesEveryDeclaredBaseScope(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7", "8"},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if !out.Changed {
		t.Fatalf("Changed = false, want true")
	}

	base7, base8 := ScopeKey("7"), ScopeKey("8")
	rows7, err := repo.List(t.Context(), res.ID, base7, "")
	if err != nil {
		t.Fatalf("List base 7: %v", err)
	}
	rows8, err := repo.List(t.Context(), res.ID, base8, "")
	if err != nil {
		t.Fatalf("List base 8: %v", err)
	}
	if len(rows7) != 3 || len(rows8) != 3 {
		t.Fatalf("rows7=%d rows8=%d, want 3 and 3 (listSize under EACH declared base value)", len(rows7), len(rows8))
	}
	keys7 := map[string]bool{}
	for _, e := range rows7 {
		keys7[e.EntityKey] = true
	}
	for _, e := range rows8 {
		if keys7[e.EntityKey] {
			t.Fatalf("entity_key %q present under both base 7 and base 8 after reseed — the two scopes must be disjoint", e.EntityKey)
		}
	}
	if got := entityCount(t, db, res.ID); got != 6 {
		t.Fatalf("total entity count after reseed = %d, want 6 (2 declared values x listSize 3)", got)
	}
}

// TestResetData_Reseed_UsesWorkspaceDeclaredSetNotActiveScenarios is P20b:
// a reseed reads the WORKSPACE's own basePathValues, never an active
// scenario's — the wrong implementation is not hypothetical, per D14's own
// text: prepareReset parses the workspace's settings and builds effSettings
// from them through effectiveSettings one line apart, and reaching for the
// SAME variable one field further (effSettings.BasePathValues, which is the
// SCENARIO's own captured value while one is active) is the natural
// mistake. This re-runs the reseed with a scenario ACTIVE whose captured
// settings name a DIFFERENT declared set, and asserts the rows land in the
// WORKSPACE's own base scopes, not the scenario's.
func TestResetData_Reseed_UsesWorkspaceDeclaredSetNotActiveScenarios(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsSettings := domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7", "8"},
	}
	wsID := insertWorkspace(t, db, "alpha", &specID, wsSettings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	// The scenario's OWN settings name a completely different declared set
	// (base "99" only) — effectiveSettings would hand this back verbatim
	// for Seed/ListSize while a scenario is active, and D4.5/D18.3 forbid
	// letting it also decide the base scopes a reseed writes into.
	scenarioSettings := domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"99"},
	}
	scID := insertScenario(t, db, wsID, "s1", scenarioSettings)
	activateScenario(t, db, wsID, scID)

	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed) with a scenario active = %v, want nil", err)
	}
	if !out.Changed {
		t.Fatalf("Changed = false, want true")
	}

	rows7, err := repo.List(t.Context(), res.ID, ScopeKey("7"), "")
	if err != nil {
		t.Fatalf("List base 7: %v", err)
	}
	rows8, err := repo.List(t.Context(), res.ID, ScopeKey("8"), "")
	if err != nil {
		t.Fatalf("List base 8: %v", err)
	}
	if len(rows7) != 3 || len(rows8) != 3 {
		t.Fatalf("rows7=%d rows8=%d, want 3 and 3 — the reseed must use the WORKSPACE's declared set (7, 8)", len(rows7), len(rows8))
	}
	rows99, err := repo.List(t.Context(), res.ID, ScopeKey("99"), "")
	if err != nil {
		t.Fatalf("List base 99: %v", err)
	}
	if len(rows99) != 0 {
		t.Fatalf("rows under base 99 (the SCENARIO's own declared value) = %d, want 0", len(rows99))
	}
}

// TestResetData_Reseed_NestedGroup_MultipleBaseValues is D6.1's own
// per-base ancestor-tuple logic (prepareGroupPopulation's keysForBase(base)
// at reset.go and tuplesByFamily[parentFamily][base]) exercised at a
// NESTED family for the first time: P20/P20b above confirm only a
// depth-0 family, where this branch is never reached (reset.go's own
// `if parentFamily == ""` guard skips it entirely), and
// TestResetData_Reseed_NestedGroup and its siblings confirm a nested
// family only under the implicit single base "" — the two properties were
// never exercised together before this test, even though
// TestConfirm_NestedFamily_AncestorWalkWithinBaseValue already covers the
// identical shape on the CONFIRM side (D6.1 names it "the single most
// likely wrong implementation of the whole slice"). Two declared base
// values, a depth-1 nested family (orgs -> teams, [testspec.DeepNestingDoc]),
// reseeded: each base's team rows must be scoped ONLY to that same base's
// own freshly-reseeded org keys, never to the other base's.
//
// Mutation this is built to catch, either of the two reset.go names in its
// own comments: swapping `succeeded[parentFamily].keysForBase(base)` for
// the family-wide `.keys` (unrestricted by base), or
// `tuplesByFamily[parentFamily][base]` for
// `tuplesByFamily[parentFamily][bases[0]]` (always the FIRST declared
// base's own ancestor tuples) — either reads a DIFFERENT base's own key
// set while building this base's population. Under the first mutation,
// base 8's own population fans out over base 7's chunked org keys (index 0
// of the unrestricted, family-wide chunked list, which — with bases
// processed in declared order — always holds base 7's own org keys
// first), producing team rows scoped to a base-7 org key while nominally
// under base 8: exactly the leak asserted against below.
func TestResetData_Reseed_NestedGroup_MultipleBaseValues(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{
		Seed: 1, ListSize: 2, BasePath: "/tenants/{tenantId}", BasePathValues: []string{"7", "8"},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs); err != nil {
		t.Fatalf("confirm root: %v", err)
	}
	teams, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams)
	if err != nil {
		t.Fatalf("confirm child: %v", err)
	}
	orgID := mustResourceID(t, db, wsID, testspec.FamilyDeepOrgs)

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "acme"); err != nil {
		t.Fatalf("ResetData reseed: %v", err)
	}

	base7, base8 := ScopeKey("7"), ScopeKey("8")
	orgsUnder7, err := repo.List(t.Context(), orgID, base7, "")
	if err != nil {
		t.Fatalf("List orgs under base 7: %v", err)
	}
	orgsUnder8, err := repo.List(t.Context(), orgID, base8, "")
	if err != nil {
		t.Fatalf("List orgs under base 8: %v", err)
	}
	if len(orgsUnder7) != 2 || len(orgsUnder8) != 2 {
		t.Fatalf("orgs under base7=%d base8=%d, want 2 and 2 after reseed", len(orgsUnder7), len(orgsUnder8))
	}

	// Each base's teams must be scoped to exactly that same base's own
	// freshly-reseeded org keys — 2 org tuples * listSize 2 = 4 team rows
	// under each base.
	for _, tc := range []struct {
		base ScopeKey
		orgs []Entity
	}{{base7, orgsUnder7}, {base8, orgsUnder8}} {
		total := 0
		for _, o := range tc.orgs {
			scoped, err := repo.List(t.Context(), teams.ID, tc.base, EncodeScope([]string{o.EntityKey}))
			if err != nil {
				t.Fatalf("List teams under base %q org %q: %v", tc.base, o.EntityKey, err)
			}
			if len(scoped) != 2 {
				t.Fatalf("teams under base %q org %q = %d, want 2", tc.base, o.EntityKey, len(scoped))
			}
			total += len(scoped)
		}
		if total != 4 {
			t.Fatalf("teams under base %q total = %d, want 4", tc.base, total)
		}
	}

	// The cross-base symptom of both named mutations: teams scoped under
	// ONE base but addressed by the OTHER base's own org key must not
	// exist — the property a single-base-value fixture cannot fail
	// against at all.
	for _, o := range orgsUnder8 {
		leaked, err := repo.List(t.Context(), teams.ID, base7, EncodeScope([]string{o.EntityKey}))
		if err != nil {
			t.Fatalf("List teams under base7/org %q (a base8 org key): %v", o.EntityKey, err)
		}
		if len(leaked) != 0 {
			t.Fatalf("teams under base7/org %q (a base8 org key) = %d, want 0 — the ancestor walk crossed base scopes", o.EntityKey, len(leaked))
		}
	}
	for _, o := range orgsUnder7 {
		leaked, err := repo.List(t.Context(), teams.ID, base8, EncodeScope([]string{o.EntityKey}))
		if err != nil {
			t.Fatalf("List teams under base8/org %q (a base7 org key): %v", o.EntityKey, err)
		}
		if len(leaked) != 0 {
			t.Fatalf("teams under base8/org %q (a base7 org key) = %d, want 0 — the ancestor walk crossed base scopes", o.EntityKey, len(leaked))
		}
	}

	if got := entityCount(t, db, teams.ID); got != 8 {
		t.Fatalf("team entity count after reseed = %d, want 8 (2 bases * 2 orgs * listSize 2)", got)
	}
}

// TestResetData_Reseed_StaleBasePathValuesWithScenarioActive is P20b's
// reseed-side counterpart to [TestFenceConfirmTx_CatchesStaleBasePathValues],
// and it exists because the confirm-side one proves nothing about this verb:
// the two fences are different functions over different call paths, and
// deleting fenceResetTx's own basePath/basePathValues comparison left every
// test in this package green when the run's acceptance step checked it.
//
// The edit lands INSIDE the write transaction's window, through
// [resetPreWriteHook] — never before [Repo.prepareReset]'s own read, which the
// pre-transaction path already catches and which proves nothing about the
// IN-TRANSACTION check. A SCENARIO IS ACTIVE throughout, because D6.5 puts
// basePath/basePathValues in the UNCONDITIONAL half of the fence rather than
// the `!scenarioID.Valid` branch that guards seed/listSize: a fixture with no
// scenario active passes even when the comparison sits in the wrong branch,
// so it cannot tell the two placements apart.
//
// What rides on the refusal is not tidiness. prepareReset builds the whole
// population — bodies and base scope keys alike — against the declared set it
// read BEFORE the transaction. Without this fence the reseed deletes the
// family's rows and inserts that stale population, tagged with base scope keys
// the workspace no longer declares, and answers 200 changed:true while every
// row it just wrote is unreachable behind D7.3's membership check.
func TestResetData_Reseed_StaleBasePathValuesWithScenarioActive(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	settings := domain.Settings{Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7", "8"}}
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	before := entityCount(t, db, res.ID)
	if before == 0 {
		t.Fatalf("fixture confirmed no entity rows, so a refused reseed proves nothing")
	}

	scenarioID := insertScenario(t, db, wsID, "s1", settings)
	activateScenario(t, db, wsID, scenarioID)

	resetPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{
			Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"9"},
		})
	}
	t.Cleanup(func() { resetPreWriteHook = resetPreWriteHookNoop })

	if _, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha"); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("ResetData(reseed) racing a basePathValues edit = %v, want ErrStaleConfig", err)
	}
	if got := entityCount(t, db, res.ID); got != before {
		t.Fatalf("entity rows changed after a refused reseed: %d, want %d", got, before)
	}
}
