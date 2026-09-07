// Tests for internal/resources.
//
// This whole package's test files (repo_test.go's former single file, now
// split by concern) are `package resources`, not `package resources_test`:
// several tests reach for [confirmEntityHook] (D13 clause 39) and
// [fenceConfirmTx] directly, and a fabricated-byte-slice test exercises
// [Repo.checkBatchCaps] without needing a spec large enough to trigger the
// per-entity cap for real. Every test that DOES need to prove a
// wire-observable property goes through the exported
// Confirm/Decline/List/Get/Create/Delete surface, same as any real caller
// would use it.
//
// helpers_test.go holds the fixture the rest of repo_*_test.go draws on:
// fixtureDoc (three resource families in one spec), testSpecConfig,
// import*/insert*/newTestRepo, and the smaller fence/nested-family fixture
// builders (mustReadCore, confirmDeepChain, mustResourceID, …). Split out of
// the former 2118-line repo_test.go — mechanical moves only, no assertion
// changed — into repo_create_test.go (Create/Confirm's own storage
// properties: id overwrite, concurrent seq allocation, the row/byte caps),
// repo_decision_test.go (the decision row, decline/confirm's guards, the
// config fence, atomicity), repo_read_test.go (ForWorkspace and the
// vanished-resource distinction), repo_nested_test.go (P3g: one level and
// deep nesting) and repo_base_scope_test.go (P3h: the declared base-value
// set).
package resources

import (
	"database/sql"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testkit"
	"github.com/yashok111/mocker/internal/testspec"
)

// fixtureDoc is a small, hand-built OpenAPI document declaring three
// resource families in one spec, so a single import exercises D13 clause
// 7's "determinism across families" without a second Import call:
//
//   - /widgets: id_field "id" (the reconcileIDField fast path), integer id,
//     a bare-array 200, and a POST whose request body is the identical
//     $ref as the item schema — the one shape [computeWriteForm] answers
//     "bare" for (R12), so this fixture also proves write_form without a
//     second document.
//   - /users: id_field "userId" (clause 6 — reconcileIDField's exact-name
//     match, never "id"), no POST at all, so write_form stays nil without
//     computeWriteForm running (locateFamilyOperations returns a nil
//     postRoute).
//   - /gadgets: a WRAPPED 200 ({"items": [...], "total": N}), integer id —
//     clause 48's own "run it once for a wrapped 200 and once for a bare
//     array" needs one family of each shape, and /widgets alone (bare) is
//     not enough to hold that half of the clause.
const fixtureDoc = `{
  "openapi": "3.0.3",
  "info": {"title": "resources fixture", "version": "1.0.0"},
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}}
        }}}}
      },
      "post": {
        "operationId": "createWidget",
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Widget"}}}},
        "responses": {"201": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    },
    "/widgets/{id}": {
      "get": {
        "operationId": "getWidget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    },
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
    },
    "/gadgets": {
      "get": {
        "operationId": "listGadgets",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "object", "properties": {
            "items": {"type": "array", "items": {"$ref": "#/components/schemas/Gadget"}},
            "total": {"type": "integer"}
          }}
        }}}}
      }
    },
    "/gadgets/{id}": {
      "get": {
        "operationId": "getGadget",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Gadget"}
        }}}}
      }
    },
    "/notes": {
      "get": {
        "operationId": "listNotes",
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Note"}}
        }}}}
      }
    },
    "/notes/{id}": {
      "get": {
        "operationId": "getNote",
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "d", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Note"}
        }}}}
      }
    }
  },
  "components": {
    "schemas": {
      "Widget": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}},
      "User": {"type": "object", "properties": {"userId": {"type": "integer"}, "name": {"type": "string"}}},
      "Gadget": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}},
      "Note": {"type": "object", "properties": {
        "id": {"type": "integer"},
        "tags": {"type": "array", "items": {"type": "string"}}
      }}
    }
  }
}`

const (
	familyWidgets = "/widgets"
	familyUsers   = "/users"
	familyGadgets = "/gadgets"
	// familyNotes is reset_test.go's own family (D13-of-P3b clause 3): its
	// "tags" array carries no minItems/maxItems, so its LENGTH tracks
	// settings.listSize at generation time rather than being fixed at
	// confirm — the one property [Repo.ResetData]'s reseed mode needs a
	// family for that widgets/users/gadgets, all scalar-only, cannot give.
	familyNotes = "/notes"
)

func testSpecConfig(t *testing.T) *config.Config {
	t.Helper()
	return &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		DataDir:        t.TempDir(),
		MaxBody:        10 << 20,
		MaxResponse:    4 << 20,
		RuntimeCache:   32,
		Dev:            true,
	}
}

// importFixtureSpec imports fixtureDoc, deriving its two resource_suggestions
// rows in the same transaction as its operations, exactly as a real upload
// would (internal/specs.Repo.Import).
func importFixtureSpec(t *testing.T, db *store.DB) int64 {
	t.Helper()
	return importSpecDoc(t, db, []byte(fixtureDoc))
}

// importSpecDoc is importFixtureSpec over a document the caller names. It
// exists for the one clause that needs a family this package's own inline
// fixture does not declare, and it reaches for internal/testspec rather than
// growing that fixture: D13 clause 8 puts the derivation fixture in the
// SHARED package precisely so a second copy does not appear beside it, and
// this package's inline document is already the copy that rule is about.
func importSpecDoc(t *testing.T, db *store.DB, doc []byte) int64 {
	t.Helper()
	sr := specs.NewRepo(db, testSpecConfig(t))
	res, err := sr.Import(t.Context(), specs.ImportInput{Name: "fixture", Source: "upload", Document: doc})
	if err != nil {
		t.Fatalf("import fixture spec: %v", err)
	}
	return res.Spec.ID
}

// insertWorkspace writes a workspaces row directly (this package's own
// pattern for reading/writing that table, matching internal/checkpoints,
// internal/scenarios and internal/specs' own test helpers), returning its
// id and slug.
func insertWorkspace(t *testing.T, db *store.DB, slug string, specID *int64, settings domain.Settings) int64 {
	t.Helper()
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	now := time.Now().Unix()
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (slug, name, spec_id, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, ?, ?, ?)`,
		slug, slug, specID, string(settingsJSON), now, now)
	if err != nil {
		t.Fatalf("insert workspace %q: %v", slug, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("workspace id: %v", err)
	}
	return id
}

// insertResourceRow writes a resources row directly, bypassing Confirm
// entirely — every Create/Delete/List/Get test below needs only a resource
// to exist, never a spec or a population run, so exercising the full
// Confirm pipeline for those would be paying an unrelated cost.
func insertResourceRow(t *testing.T, db *store.DB, workspaceID int64, family, idField, idType string) int64 {
	t.Helper()
	wrapper, err := jsonx.Marshal(specs.Wrapper{IDType: idType})
	if err != nil {
		t.Fatalf("marshal wrapper: %v", err)
	}
	res, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resources
			(workspace_id, route_family, name, id_field, id_strategy, parent_id, scope_params,
			 entity_schema, wrapper, filter_map, write_form, seq, seed_count)
		VALUES (?, ?, ?, ?, 'seq', NULL, '[]', 'x', ?, '{}', NULL, 0, 0)`,
		workspaceID, family, family, idField, string(wrapper))
	if err != nil {
		t.Fatalf("insert resource row: %v", err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		t.Fatalf("resource id: %v", err)
	}
	return id
}

func resourceRowCount(t *testing.T, db *store.DB, workspaceID int64, family string) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, family).Scan(&n); err != nil {
		t.Fatalf("count resources: %v", err)
	}
	return n
}

func entityCount(t *testing.T, db *store.DB, resourceID int64) int {
	t.Helper()
	var n int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities WHERE resource_id = ?", resourceID).Scan(&n); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	return n
}

func decisionState(t *testing.T, db *store.DB, workspaceID int64, family string) (string, bool) {
	t.Helper()
	var state string
	err := db.R.QueryRowContext(t.Context(), "SELECT state FROM resource_decisions WHERE workspace_id = ? AND route_family = ?", workspaceID, family).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", false
	}
	if err != nil {
		t.Fatalf("read decision: %v", err)
	}
	return state, true
}

func workspaceRevision(t *testing.T, db *store.DB, id int64) int64 {
	t.Helper()
	var rev int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT revision FROM workspaces WHERE id = ?", id).Scan(&rev); err != nil {
		t.Fatalf("read revision: %v", err)
	}
	return rev
}

func newTestRepo(t *testing.T, db *store.DB, maxResponseBytes, trafficMaxBody int64) *Repo {
	t.Helper()
	sr := specs.NewRepo(db, testSpecConfig(t))
	return NewRepo(db, sr, maxResponseBytes, trafficMaxBody, defaultMaxEntityRows)
}

// jsonIDType names the JSON type a decode into `any` gives a value's kind —
// "number" or "string" are the only two clause 48 itself ever compares.
func jsonIDType(v any) string {
	switch v.(type) {
	case float64:
		return "number"
	case string:
		return "string"
	default:
		return fmt.Sprintf("%T", v)
	}
}

func mustReadCore(t *testing.T, db *store.DB, wsID int64) workspaceCore {
	t.Helper()
	repo := &Repo{db: db}
	core, err := repo.readWorkspaceCore(t.Context(), wsID)
	if err != nil {
		t.Fatalf("readWorkspaceCore: %v", err)
	}
	return core
}

func mustSetSettings(t *testing.T, db *store.DB, wsID int64, s domain.Settings) {
	t.Helper()
	b, err := s.MarshalJSONStable()
	if err != nil {
		t.Fatalf("marshal settings: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET settings = ? WHERE id = ?", string(b), wsID); err != nil {
		t.Fatalf("update settings: %v", err)
	}
}

func runFenceTx(t *testing.T, db *store.DB, wsID int64, before workspaceCore, seed int64, listSize int) error {
	t.Helper()
	return db.Write(t.Context(), func(tx *sql.Tx) error {
		// "" / nil: every caller of this helper builds its workspace with
		// the zero-value domain.Settings (no basePath parameter), so the
		// implicit declared set never changes across the window these
		// tests exercise — D6.5's own basePath/basePathValues comparison
		// stays trivially satisfied and does not interfere with what these
		// tests are actually probing (Seed/ListSize and the incarnation
		// fence).
		return fenceConfirmTx(t.Context(), tx, wsID, before, seed, listSize, "", nil)
	})
}

// confirmDeepChain imports [testspec.DeepNestingDoc] and confirms
// FamilyDeepOrgs, FamilyDeepTeams and FamilyDeepUsers — depths 0, 1 and 2 —
// at listSize L, in that order (D5.1: each nested family confirms only
// once its own immediate parent already is). FamilyDeepBadges (depth 3,
// the ceiling) is deliberately NOT confirmed here — the handful of tests
// that need it confirm it themselves, at whatever L their own property
// needs (P7's own cap test wants a much larger L than the others).
func confirmDeepChain(t *testing.T, listSize int) (repo *Repo, org, teams, users *Resource, wsID int64) {
	t.Helper()
	dir := t.TempDir()
	db := testkit.NewDBAt(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID = insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: listSize})
	repo = newTestRepo(t, db, 4<<20, 64<<10)

	var err error
	org, err = repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs)
	if err != nil {
		t.Fatalf("confirm %q: %v", testspec.FamilyDeepOrgs, err)
	}
	teams, err = repo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams)
	if err != nil {
		t.Fatalf("confirm %q: %v", testspec.FamilyDeepTeams, err)
	}
	users, err = repo.Confirm(t.Context(), wsID, testspec.FamilyDeepUsers)
	if err != nil {
		t.Fatalf("confirm %q: %v", testspec.FamilyDeepUsers, err)
	}
	return repo, org, teams, users, wsID
}

// mustResourceID reads back a confirmed resource's id by family — used
// where a test only has the workspace id and the family name in hand
// (P7's own leave-the-rest-alone check).
func mustResourceID(t *testing.T, db *store.DB, workspaceID int64, family string) int64 {
	t.Helper()
	var id int64
	if err := db.R.QueryRowContext(t.Context(), "SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, family).Scan(&id); err != nil {
		t.Fatalf("read resource id for %q: %v", family, err)
	}
	return id
}

// baseFixtureDoc is fixtureDoc's own /widgets family alone, reused here so
// every base-scope test below shares one small document rather than each
// re-declaring its own.
const declaredBaseA = "7"

const declaredBaseB = "8"
