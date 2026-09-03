// Tests for internal/resources.
//
// This file is `package resources`, not `package resources_test`: several
// tests reach for [confirmEntityHook] (D13 clause 39) and [fenceConfirmTx]
// directly, and a fabricated-byte-slice test exercises [Repo.checkBatchCaps]
// without needing a spec large enough to trigger the per-entity cap for
// real. Every test that DOES need to prove a wire-observable property goes
// through the exported Confirm/Decline/List/Get/Create/Delete surface, same
// as any real caller would use it.
package resources

import (
	"database/sql"
	"errors"
	"fmt"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
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

func newTestDB(t *testing.T, path string) *store.DB {
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

// TestNewRepo_MaxEntityRowsBoundary is D11's own boundary: config.Load's
// count() validator (internal/config/config.go) accepts MOCKER_MAX_ENTITIES
// values n >= 0, so an explicit zero is a legitimate operator choice
// ("confirm nothing, ever") and must reach the cap AS zero, not be silently
// coerced into the pre-P3h constant the way a genuinely never-set field
// (a hand-built *config.Config struct literal, or a caller that reached
// this constructor before config.MaxEntities existed) still is. Before this
// fix NewRepo's `maxEntityRows <= 0` fallback could not tell the two apart;
// the fallback now triggers only on a NEGATIVE value, which config.Load's
// validator never lets an operator produce.
func TestNewRepo_MaxEntityRowsBoundary(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	sr := specs.NewRepo(db, testSpecConfig(t))

	if got := NewRepo(db, sr, 4<<20, 64<<10, 0).maxEntityRows; got != 0 {
		t.Fatalf("maxEntityRows for an explicit MOCKER_MAX_ENTITIES=0 = %d, want 0 (an operator's own zero must not be overridden)", got)
	}
	if got := NewRepo(db, sr, 4<<20, 64<<10, -1).maxEntityRows; got != defaultMaxEntityRows {
		t.Fatalf("maxEntityRows for a negative value (never produced by config.Load) = %d, want the fallback %d", got, defaultMaxEntityRows)
	}
	if got := NewRepo(db, sr, 4<<20, 64<<10, defaultMaxEntityRows).maxEntityRows; got != defaultMaxEntityRows {
		t.Fatalf("maxEntityRows for an explicit positive value = %d, want %d unchanged", got, defaultMaxEntityRows)
	}
}

// --- D13 clauses 1, 2: it stores, and survives a restart ------------------

func TestConfirm_StoresEntities(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 5})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	revBefore := workspaceRevision(t, db, wsID)
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count = %d, want 5 (seed_count)", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revBefore+1 {
		t.Fatalf("revision after Confirm = %d, want %d (D4: the decision transitions bump revision)", got, revBefore+1)
	}

	// A POST makes it +1, and — D13 clause 23 — does NOT bump revision:
	// an entity write changes nothing the runtime cache keys on.
	revAfterConfirm := workspaceRevision(t, db, wsID)
	if _, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "extra"}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 6 {
		t.Fatalf("entity count after Create = %d, want 6", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revAfterConfirm {
		t.Fatalf("revision after Create = %d, want unchanged %d", got, revAfterConfirm)
	}

	// A DELETE makes it -1, and likewise does not bump revision.
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	deleted, err := repo.Delete(t.Context(), res.ID, "", "", entities[0].EntityKey)
	if err != nil || !deleted {
		t.Fatalf("Delete = %v, %v, want true, nil", deleted, err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count after Delete = %d, want 5", got)
	}
	if got := workspaceRevision(t, db, wsID); got != revAfterConfirm {
		t.Fatalf("revision after Delete = %d, want unchanged %d", got, revAfterConfirm)
	}

	// And the OTHER decision transition. Clause 23 says revision moves on
	// BOTH, and until this block only the Confirm half was held: the
	// runtime-invalidation test in internal/mockplane forces its cache to
	// miss by incrementing ws.Revision in test code rather than re-reading
	// the column, so it passes identically whether or not Decline bumps
	// anything, and no other test read revision after a Decline at all.
	// Delete Repo.Decline's own bumpRevisionTx call and this is the
	// assertion that goes red.
	revBeforeDecline := workspaceRevision(t, db, wsID)
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline with the correct slug: %v", err)
	}
	if got := workspaceRevision(t, db, wsID); got != revBeforeDecline+1 {
		t.Fatalf("revision after Decline = %d, want %d (D4: BOTH decision transitions bump revision)", got, revBeforeDecline+1)
	}
}

func TestConfirm_SurvivesRestart(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	dbPath := dir + "/mocker.db"

	var (
		wsID       int64
		resourceID int64
		created    Entity
	)
	func() {
		db, err := store.Open(t.Context(), dbPath)
		if err != nil {
			t.Fatalf("open db: %v", err)
		}
		if err := db.Migrate(t.Context(), nil); err != nil {
			t.Fatalf("migrate: %v", err)
		}
		specID := importFixtureSpec(t, db)
		wsID = insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 7, ListSize: 3})
		repo := newTestRepo(t, db, 4<<20, 64<<10)
		res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
		if err != nil {
			t.Fatalf("Confirm: %v", err)
		}
		resourceID = res.ID
		created, err = repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "posted"})
		if err != nil {
			t.Fatalf("Create: %v", err)
		}
		if err := db.Close(); err != nil {
			t.Fatalf("close db: %v", err)
		}
	}()

	// A second store.Open over the SAME file returns the same ids and
	// bodies, including the one Create wrote.
	db2, err := store.Open(t.Context(), dbPath)
	if err != nil {
		t.Fatalf("reopen db: %v", err)
	}
	t.Cleanup(func() {
		if err := db2.Close(); err != nil {
			t.Errorf("close db2: %v", err)
		}
	})
	repo2 := newTestRepo(t, db2, 4<<20, 64<<10)
	entities, err := repo2.List(t.Context(), resourceID, "", "")
	if err != nil {
		t.Fatalf("List after restart: %v", err)
	}
	if len(entities) != 4 { // 3 populated + 1 created
		t.Fatalf("entity count after restart = %d, want 4", len(entities))
	}
	got, ok, err := repo2.Get(t.Context(), resourceID, "", "", created.EntityKey)
	if err != nil || !ok {
		t.Fatalf("Get after restart = %v, %v, want ok", ok, err)
	}
	if string(got.Data) != string(created.Data) {
		t.Fatalf("body after restart = %s, want %s", got.Data, created.Data)
	}
}

// --- D13 clause 4: a client-sent id is overwritten -------------------------

func TestCreate_OverwritesClientSuppliedID(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	e, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"id": 999, "name": "spoofed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if e.EntityKey == "999" {
		t.Fatalf("EntityKey = %q, the client-sent id must be overwritten by the allocated seq", e.EntityKey)
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", "999"); err != nil || ok {
		t.Fatalf("Get(999) = ok=%v err=%v, want not found — the client's id must never be honoured", ok, err)
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", e.EntityKey); err != nil || !ok {
		t.Fatalf("Get(%s) = ok=%v err=%v, want found under the ALLOCATED key", e.EntityKey, ok, err)
	}
}

// --- D13 clause 27: the id overwrite holds for a non-"id" field -----------

func TestCreate_OverwritesNonIDField(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyUsers, "userId", "")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	e, err := repo.Create(t.Context(), resourceID, "", "", "userId", "", map[string]any{"userId": "zzz", "name": "spoofed"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	var data map[string]any
	if err := jsonx.Unmarshal(e.Data, &data); err != nil {
		t.Fatalf("decode stored data: %v", err)
	}
	if data["userId"] == "zzz" {
		t.Fatalf("data[userId] = %v, want the allocated seq overwriting the client value", data["userId"])
	}
	if _, ok, err := repo.Get(t.Context(), resourceID, "", "", "zzz"); err != nil || ok {
		t.Fatalf("Get(zzz) = ok=%v err=%v, want not found", ok, err)
	}
}

// --- D13 clause 15: seq is allocated in the transaction — N concurrent
// POSTs under -race give N distinct ids and N rows. ------------------------

func TestCreate_ConcurrentSeqAllocation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	const n = 20
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		keys = make(map[string]bool, n)
		errs []error
	)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			e, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": fmt.Sprintf("w%d", i)})
			mu.Lock()
			defer mu.Unlock()
			if err != nil {
				errs = append(errs, err)
				return
			}
			keys[e.EntityKey] = true
		}(i)
	}
	wg.Wait()

	if len(errs) != 0 {
		t.Fatalf("Create errors: %v", errs)
	}
	if len(keys) != n {
		t.Fatalf("distinct entity keys = %d, want %d (collision on seq allocation)", len(keys), n)
	}
	if got := entityCount(t, db, resourceID); got != n {
		t.Fatalf("entity row count = %d, want %d", got, n)
	}
}

// --- D13 clauses 17, 18, 28, 32: the caps, on both paths, under
// concurrency, and at exactly 1000. -----------------------------------------

func TestCreate_RowCapExactly1000(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	repo := newTestRepo(t, db, 64<<20, 64<<10) // generous byte cap: this test is about the ROW cap only

	for i := 0; i < defaultMaxEntityRows; i++ {
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"}); err != nil {
			t.Fatalf("Create %d: %v", i, err)
		}
	}
	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count = %d, want %d (row 1000 must be accepted)", got, defaultMaxEntityRows)
	}

	// Row 1001 refuses with ErrEntityLimit and the count is unchanged
	// (clause 18: a refusal never leaves a row).
	_, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"})
	if !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Create row 1001 error = %v, want ErrEntityLimit", err)
	}
	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count after refused row 1001 = %d, want unchanged %d", got, defaultMaxEntityRows)
	}
}

func TestCreate_ByteTotalCap(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")
	// A tiny maxResponseBytes makes the byte-total cap (MaxResponse/2) the
	// one that bites, well before the row cap could.
	repo := newTestRepo(t, db, 400, 64<<10)

	var lastErr error
	accepted := 0
	for i := 0; i < 50; i++ {
		_, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "widget-name-padding"})
		if err != nil {
			lastErr = err
			break
		}
		accepted++
	}
	if !errors.Is(lastErr, ErrEntityLimit) {
		t.Fatalf("final Create error = %v, want ErrEntityLimit (byte total cap)", lastErr)
	}
	if got := entityCount(t, db, resourceID); got != accepted {
		t.Fatalf("entity count = %d, want %d (a refusal must leave no row)", got, accepted)
	}
}

func TestCreate_CapsHoldUnderConcurrency(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")

	// Pre-fill to one below the row cap, then fire N goroutines at the
	// boundary: the row count read INSIDE Create's own transaction (not the
	// reader pool) is what must keep the resource exactly at the cap.
	repo := newTestRepo(t, db, 64<<20, 64<<10)
	for i := 0; i < defaultMaxEntityRows-5; i++ {
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"}); err != nil {
			t.Fatalf("prefill Create %d: %v", i, err)
		}
	}

	const n = 20 // more goroutines than the 5 remaining slots
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": "w"})
		}()
	}
	wg.Wait()

	if got := entityCount(t, db, resourceID); got != defaultMaxEntityRows {
		t.Fatalf("entity count after concurrent burst = %d, want exactly %d", got, defaultMaxEntityRows)
	}
}

// TestCreate_CapsHoldUnderConcurrency_ByteTotal is D13 clause 32's OTHER
// half: TestCreate_CapsHoldUnderConcurrency above only ever fires goroutines
// at the ROW-count boundary (a generous byte cap, tiny bodies). This one
// keeps the row cap generous and drives the BYTE-total cap (entityByteCap,
// MaxResponse/2) to its boundary instead — an implementation that moved
// only that check onto the reader pool (a stale snapshot under concurrent
// writers) while leaving the row-count check on the writer tx would pass
// every other test in this file and still let two concurrent POSTs push
// the stored byte total past the cap, which is exactly what clause 33 says
// must not happen (a resource over that line bricks GET X).
func TestCreate_CapsHoldUnderConcurrency_ByteTotal(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	resourceID := insertResourceRow(t, db, wsID, familyWidgets, "id", "integer")

	// trafficMaxBody stays generous (floors to 64KiB) so only the
	// byte-TOTAL cap — MaxResponse/2 — can bite in this test.
	repo := newTestRepo(t, db, 4000, 64<<10)
	totalCap := repo.entityByteCap()

	pad := strings.Repeat("x", 200)
	// Measure one entity's ACTUAL stored size (id overwrite included)
	// rather than guessing at JSON-encoding overhead, then delete it again
	// so the prefill loop below starts from zero.
	probe, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad})
	if err != nil {
		t.Fatalf("probe Create: %v", err)
	}
	oneSize := int64(len(probe.Data))
	if _, err := repo.Delete(t.Context(), resourceID, "", "", probe.EntityKey); err != nil {
		t.Fatalf("delete probe: %v", err)
	}

	// Prefill sequentially until only room for a handful more entities
	// remains, then fire far more goroutines than that at the boundary —
	// the write-tx-internal re-read of SUM(LENGTH(data)) (clause 32's own
	// text: "read inside Create's own transaction") is what must keep the
	// final total at or under the cap, since the single writer connection
	// serializes every one of these transactions regardless of how many
	// goroutines race to start one.
	room := oneSize * 3
	for {
		var total sql.NullInt64
		if err := db.R.QueryRowContext(t.Context(), "SELECT SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID).Scan(&total); err != nil {
			t.Fatalf("sum bytes: %v", err)
		}
		if totalCap-total.Int64 <= room {
			break
		}
		if _, err := repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad}); err != nil {
			t.Fatalf("prefill Create: %v", err)
		}
	}

	const n = 20 // far more goroutines than the ~3 remaining slots
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = repo.Create(t.Context(), resourceID, "", "", "id", "integer", map[string]any{"name": pad})
		}()
	}
	wg.Wait()

	var finalTotal sql.NullInt64
	if err := db.R.QueryRowContext(t.Context(), "SELECT SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID).Scan(&finalTotal); err != nil {
		t.Fatalf("sum bytes: %v", err)
	}
	if finalTotal.Int64 > totalCap {
		t.Fatalf("stored byte total after concurrent burst = %d, want <= cap %d (a check on the reader pool — a pre-writer snapshot — would let this pass over)", finalTotal.Int64, totalCap)
	}
}

func TestCheckBatchCaps(t *testing.T) {
	t.Parallel()
	repo := &Repo{maxResponseBytes: 1000, trafficMaxBody: 0, maxEntityRows: defaultMaxEntityRows} // perEntityByteCap floors to 64KiB

	t.Run("row cap", func(t *testing.T) {
		t.Parallel()
		bodies := make([][]byte, defaultMaxEntityRows+1)
		for i := range bodies {
			bodies[i] = []byte(`{}`)
		}
		if err := repo.checkBatchCaps(bodies); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("byte total cap", func(t *testing.T) {
		t.Parallel()
		big := make([]byte, 600)
		bodies := [][]byte{big, big} // 1200 > maxResponseBytes/2 (500)
		if err := repo.checkBatchCaps(bodies); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("per-entity cap", func(t *testing.T) {
		t.Parallel()
		huge := make([]byte, minEntityBodyCap+1)
		if err := repo.checkBatchCaps([][]byte{huge}); !errors.Is(err, ErrEntityLimit) {
			t.Fatalf("checkBatchCaps() = %v, want ErrEntityLimit", err)
		}
	})

	t.Run("within every cap", func(t *testing.T) {
		t.Parallel()
		if err := repo.checkBatchCaps([][]byte{[]byte(`{"a":1}`)}); err != nil {
			t.Fatalf("checkBatchCaps() = %v, want nil", err)
		}
	})
}

// --- D13 clauses 12, 13, 43: the decision row, the slug guard, and the
// decline guard under concurrency. ------------------------------------------

func TestDecisionLifecycle(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// Declining an UNCONFIRMED suggestion writes state='declined' and it
	// stays declined across a reload (a second read of the row).
	if err := repo.Decline(t.Context(), wsID, familyUsers, ""); err != nil {
		t.Fatalf("Decline (unconfirmed, no slug needed): %v", err)
	}
	if state, ok := decisionState(t, db, wsID, familyUsers); !ok || state != "declined" {
		t.Fatalf("decision state = %q, ok=%v, want declined", state, ok)
	}
	if state, ok := decisionState(t, db, wsID, familyUsers); !ok || state != "declined" {
		t.Fatalf("decision state on reload = %q, ok=%v, want still declined", state, ok)
	}

	// Confirming writes the decision row too.
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); !ok || state != "confirmed" {
		t.Fatalf("decision state = %q, ok=%v, want confirmed", state, ok)
	}

	// Declining a CONFIRMED resource without a slug changes nothing.
	if err := repo.Decline(t.Context(), wsID, familyWidgets, ""); !errors.Is(err, ErrConfirmSlugRequired) {
		t.Fatalf("Decline without slug = %v, want ErrConfirmSlugRequired", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 1 {
		t.Fatalf("resource row was removed despite the refused decline")
	}

	// With the WRONG slug, same refusal, same no-op.
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "not-alpha"); !errors.Is(err, ErrConfirmSlugMismatch) {
		t.Fatalf("Decline with wrong slug = %v, want ErrConfirmSlugMismatch", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 1 {
		t.Fatalf("resource row was removed despite the mismatched slug")
	}

	// Declining a confirmed one WITH the right slug deletes the resource
	// and its entities (ON DELETE CASCADE).
	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline with correct slug: %v", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("resource row still present after a correctly-slugged decline")
	}
	if entityCount(t, db, res.ID) != 0 {
		t.Fatalf("entities still present after their resource was declined")
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); !ok || state != "declined" {
		t.Fatalf("decision state = %q, ok=%v, want declined", state, ok)
	}
}

func TestDecline_UnknownFamily(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// No spec attached, no resources row: neither a suggestion nor a
	// resource can name this family.
	if err := repo.Decline(t.Context(), wsID, "/nonexistent", ""); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Decline unknown family = %v, want ErrUnknownFamily", err)
	}
}

func TestConfirm_UnknownFamily(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, "/nonexistent"); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Confirm unknown family = %v, want ErrUnknownFamily", err)
	}
}

// TestConfirm_And_ResetData_AfterRederiveDropsFamily is P3f's own decisions.md
// §D9 P4: [findSuggestion] owns no query of its own — it reads through
// specs.EnsureSuggestions and so inherits [internal/specs]' single "newest
// generation only" predicate (§D3.2, §D13.3) with no second, driftable
// implementation here. Once a rederive's newest generation no longer names
// familyWidgets, a Confirm of it is refused exactly like an unknown family
// (§D6.1), and an already-confirmed familyWidgets is classified `stranded`
// by the next reseed (§D6.2) — the same closed reason
// TestResetData_Reseed_OneStrandedOneRepopulated already produces by
// REBINDING the workspace to a narrower spec; this test produces the
// identical outcome by minting a narrower NEWEST GENERATION over the SAME
// spec instead, which is the one path this slice adds.
//
// The narrowing itself is direct SQL, not a second rederive: decisions.md
// §D9's fixture rule ("no production seam is added to make derivation
// configurable") applies here exactly as it does in internal/specs' own
// tests — [internal/specs.Repo.Rederive] itself is exercised by that
// package, not re-derived a second way from this one.
func TestConfirm_And_ResetData_AfterRederiveDropsFamily(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// familyWidgets is confirmed BEFORE the drop, so the reseed half below
	// has an already-confirmed family to classify; familyUsers is left
	// unconfirmed, so the confirm half below has a family to refuse —
	// D6.1 and D6.2 are two different consequences of the same drop and
	// each needs its own family to observe cleanly.
	widgets, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets before drop: %v", err)
	}

	// Narrow the spec by MINTING GENERATION 2 without the two families and
	// LEAVING generation 1 standing — the state a real
	// [internal/specs.Repo.Rederive] call leaves behind, which adds a
	// generation and never deletes the superseded one.
	//
	// Copying rather than DELETING is load-bearing for what this test is
	// FOR, and the run that shipped this slice measured the difference:
	// the first version narrowed by deleting generation 1's two rows, and
	// under P4's own mutation — a second, gen-blind SELECT inside
	// [findSuggestion], the §D13.3 divergence the property forbids — the
	// test stayed GREEN. With the rows physically gone, a read WITHOUT the
	// newest-generation predicate and a read WITH it both return nothing,
	// so the two implementations are indistinguishable and every assertion
	// below holds against either. Leaving the stale generation in place is
	// exactly what a gen-blind SELECT trips over: it resolves familyWidgets
	// from the superseded rows, the Confirm below succeeds instead of
	// refusing, and the test goes red.
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO resource_suggestions
			(spec_id, gen, route_family, name, id_field, parent_family, entity_schema, wrapper, confidence)
		SELECT spec_id, 2, route_family, name, id_field, parent_family, entity_schema, wrapper, confidence
		  FROM resource_suggestions
		 WHERE spec_id = ? AND gen = 1 AND route_family NOT IN (?, ?)`,
		specID, familyWidgets, familyUsers); err != nil {
		t.Fatalf("mint generation 2 without %s and %s: %v", familyWidgets, familyUsers, err)
	}

	// D6.1: a confirm of the dropped, never-confirmed family now fails
	// exactly like an unknown one.
	if _, err := repo.Confirm(t.Context(), wsID, familyUsers); !errors.Is(err, ErrUnknownFamily) {
		t.Fatalf("Confirm dropped family = %v, want ErrUnknownFamily", err)
	}

	// D6.2: reset-data's reseed classifies the already-confirmed family
	// `stranded` — the existing closed reason, never a fifth one.
	out, err := repo.ResetData(t.Context(), wsID, ResetModeReseed, "alpha")
	if err != nil {
		t.Fatalf("ResetData(reseed): %v", err)
	}
	if out.Changed {
		t.Errorf("Changed = true, want false — the only confirmed family was skipped")
	}
	if len(out.Skipped) != 1 || out.Skipped[0].RouteFamily != familyWidgets || out.Skipped[0].Reason != skipReasonStranded {
		t.Fatalf("Skipped = %+v, want [{%s stranded}]", out.Skipped, familyWidgets)
	}
	if got := entityCount(t, db, widgets.ID); got != 2 {
		t.Errorf("entity count = %d, want 2 (unchanged — the row was left standing)", got)
	}
}

func TestConfirm_AlreadyConfirmed(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("first Confirm: %v", err)
	}
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrAlreadyConfirmed) {
		t.Fatalf("second Confirm = %v, want ErrAlreadyConfirmed", err)
	}
}

// TestDecline_ConcurrencyGuard is D13 clause 43: one goroutine confirming
// and one declining without a slug, racing on the SAME family, must leave
// either a confirmed resource with ALL its entities or no resource at all —
// never a resource whose entities are gone. Both outcomes are legal
// (whichever transaction the single writer connection serializes first);
// only a torn state is a failure.
func TestDecline_ConcurrencyGuard(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 4})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		_, _ = repo.Confirm(t.Context(), wsID, familyWidgets)
	}()
	go func() {
		defer wg.Done()
		_ = repo.Decline(t.Context(), wsID, familyWidgets, "") // no slug: refused if a resource exists by then
	}()
	wg.Wait()

	n := resourceRowCount(t, db, wsID, familyWidgets)
	switch n {
	case 0:
		// Declined (or never confirmed): nothing to check further.
	case 1:
		var resourceID int64
		if err := db.R.QueryRowContext(t.Context(), "SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", wsID, familyWidgets).Scan(&resourceID); err != nil {
			t.Fatalf("read resource id: %v", err)
		}
		if got := entityCount(t, db, resourceID); got != 4 {
			t.Fatalf("torn state: resource exists with %d entities, want the full seed_count 4", got)
		}
	default:
		t.Fatalf("resource row count = %d, want 0 or 1", n)
	}
}

// --- D13 clause 17's CONFIRM half: the caps hold on generated population,
// not only on Create. --------------------------------------------------

// TestConfirm_CapsHoldOnGeneratedPopulation is the gap the acceptance
// report named: every OTHER Confirm test in this file uses a generous
// 4<<20-byte budget, so none of them ever drives generation over a cap.
// This one dials maxResponseBytes down to a few hundred bytes and clamps
// ListSize to its own ceiling (200 — clampSeedCount's own max, comfortably
// below defaultMaxEntityRows' 1000, which is why the ROW cap is not
// Confirm-reachable at all through seed_count alone and this test targets
// the byte-total cap instead) so [Repo.checkBatchCaps] — called from
// [Repo.prepareConfirm] at repo.go's own "if err := r.checkBatchCaps(bodies)"
// line, BEFORE the write transaction ever opens — is what refuses, not a
// hand-called checkBatchCaps in isolation the way TestCheckBatchCaps
// exercises it.
func TestConfirm_CapsHoldOnGeneratedPopulation(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 200})
	// entityByteCap = maxResponseBytes/2 = 200 bytes total — 200 generated
	// widget bodies comfortably exceed that, well before the (unreachable
	// here) row cap of 1000 could ever matter.
	repo := newTestRepo(t, db, 400, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Confirm over the byte-total cap = %v, want ErrEntityLimit", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a refused confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a refused confirm: %d rows", total)
	}
	// checkBatchCaps refuses BEFORE the write transaction (which is what
	// writes the decision row) even opens — a refused confirm must leave
	// no decision row behind either, unlike a refusal reached INSIDE the
	// transaction (D13 clause 18 territory).
	if state, ok := decisionState(t, db, wsID, familyWidgets); ok {
		t.Fatalf("decision state = %q, want no decision row written for a confirm refused before its transaction opened", state)
	}

	// A retry with room to spare succeeds cleanly, proving the refusal
	// above really was the byte cap and not a permanent defect in the
	// fixture or the workspace.
	repo2 := newTestRepo(t, db, 4<<20, 64<<10)
	if _, err := repo2.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("retry Confirm with a generous cap: %v", err)
	}
}

// --- D13 clause 30: the config fence, through Confirm itself -------------

// TestConfirm_StaleConfigViaHook closes the gap TestFenceConfirmTx_*
// above leaves: those two call fenceConfirmTx directly through the
// runFenceTx helper, which cannot tell a Confirm that still calls it at its
// real call site (repo.go's write transaction, right after fenceConfirmTx's
// own line) from one that silently stopped. [confirmPreWriteHook] fires in
// the exact window D4/R36 describes — after prepareConfirm's generation
// half returns, before the write transaction opens — so mutating settings
// from inside it races Confirm's OWN fence, not a bespoke stand-in for it.
func TestConfirm_StaleConfigViaHook(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	confirmPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{Seed: 99, ListSize: 3})
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("Confirm racing a settings change = %v, want ErrStaleConfig", err)
	}
	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a stale-config confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a stale-config confirm: %d rows", total)
	}

	// A retry with the hook cleared, against the now-settled settings,
	// succeeds — the race really was the cause, not a permanent defect.
	confirmPreWriteHook = confirmPreWriteHookNoop
	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err != nil {
		t.Fatalf("retry Confirm after settings settled: %v", err)
	}
}

// --- D13 clause 48: one collection, one id type ----------------------------

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

// TestCreateAndConfirm_AgreeOnIDType is D13 clause 48, through the REAL
// Repo end to end — never a fake store, which is all the two existing
// citations (mockplane's TestResourceServePost_BodyIsStoreOutputVerbatim
// and this package's own TestConfirm_NonIDFieldWorks_IntegerShape) ever
// exercised: a POST-created row and a Confirm-generated row, for the SAME
// family, must decode "id" as the SAME JSON type. Run once for /widgets (a
// bare-array 200) and once for /gadgets (a wrapped 200) — clause 48's own
// "wrapper carries the types and nothing else".
func TestCreateAndConfirm_AgreeOnIDType(t *testing.T) {
	t.Parallel()
	// The third row is the STRING-id half, and it is why this table has
	// three rather than two: the first version ran a bare array and a
	// wrapped 200 and BOTH declared id as {type: integer}, so clause 48's
	// own second sentence — "in a family whose id is a string, both decode
	// as a string" — was held by nothing but gen.CoerceIDValue's pure unit
	// test, which the acceptance walk had already ruled insufficient alone.
	// An idType that coerced every id to a number would have passed both
	// integer families.
	//
	// It comes from internal/testspec rather than from this file's own
	// inline document because that is where D13 clause 8 puts the
	// derivation fixture, and /bareitems there already declares exactly the
	// shape this needs: a bare array whose item id is {type: string}.
	for _, tc := range []struct {
		family string
		doc    []byte
		want   string
	}{
		{familyWidgets, []byte(fixtureDoc), "number"},
		{familyGadgets, []byte(fixtureDoc), "number"},
		{testspec.FamilyBareItems, testspec.DerivationDoc(), "string"},
	} {
		family := tc.family
		t.Run(family, func(t *testing.T) {
			t.Parallel()
			db := newTestDB(t, t.TempDir()+"/mocker.db")
			specID := importSpecDoc(t, db, tc.doc)
			wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
			repo := newTestRepo(t, db, 4<<20, 64<<10)

			res, err := repo.Confirm(t.Context(), wsID, family)
			if err != nil {
				t.Fatalf("Confirm: %v", err)
			}
			entities, err := repo.List(t.Context(), res.ID, "", "")
			if err != nil || len(entities) == 0 {
				t.Fatalf("List = %v, %v, want at least one confirm-generated entity", entities, err)
			}
			var confirmGenerated map[string]any
			if err := jsonx.Unmarshal(entities[0].Data, &confirmGenerated); err != nil {
				t.Fatalf("decode confirm-generated entity: %v", err)
			}
			confirmIDType := jsonIDType(confirmGenerated["id"])

			created, err := repo.Create(t.Context(), res.ID, "", "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "posted"})
			if err != nil {
				t.Fatalf("Create: %v", err)
			}
			var postCreated map[string]any
			if err := jsonx.Unmarshal(created.Data, &postCreated); err != nil {
				t.Fatalf("decode POST-created entity: %v", err)
			}
			postIDType := jsonIDType(postCreated["id"])

			if confirmIDType != postIDType {
				t.Fatalf("%s: confirm-generated id decodes as %s, POST-created id decodes as %s, want the same JSON type", family, confirmIDType, postIDType)
			}
			// Agreement alone is not the clause: two sides that both got
			// it wrong agree perfectly. The type must be the one the SPEC
			// declares, and the expectation is written into the table
			// rather than read back off the resource row — a row whose
			// idType was itself derived wrong would otherwise agree with
			// the bodies it produced.
			if confirmIDType != tc.want {
				t.Fatalf("%s: id decodes as %s, want %s (declared idType %q)", family, confirmIDType, tc.want, res.Wrapper.IDType)
			}
		})
	}
}

// --- D13 clauses 14, 39: confirm is atomic, and the injected failure is
// inside the write transaction. ---------------------------------------------

func TestConfirm_AtomicOnEntityInsertFailure(t *testing.T) {
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 5})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	// Fail INSIDE the write transaction, at entity 3 of 5 — after the
	// resources row and two entities were inserted in this same
	// transaction, so a non-atomic implementation would leave a partial
	// resource behind.
	confirmEntityHook = func(i, n int) error {
		if i == 2 {
			return fmt.Errorf("injected failure at entity %d of %d", i, n)
		}
		return nil
	}
	t.Cleanup(func() { confirmEntityHook = confirmEntityHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); err == nil {
		t.Fatalf("Confirm succeeded despite the injected mid-transaction failure")
	}

	if resourceRowCount(t, db, wsID, familyWidgets) != 0 {
		t.Fatalf("a resources row survived a failed confirm")
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("entities survived a failed confirm: %d rows", total)
	}

	// A retry with the hook cleared succeeds cleanly — proving the failure
	// really was injected inside the transaction, not a permanent defect.
	confirmEntityHook = confirmEntityHookNoop
	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("retry Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 5 {
		t.Fatalf("entity count after retry = %d, want 5", got)
	}
}

// --- D13 clauses 30, 37: the config fence, and it catches an incarnation
// change while a bare revision bump does not. -------------------------------

func TestFenceConfirmTx_CatchesStaleSeedAndListSize(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{Seed: 1, ListSize: 5})
	before := mustReadCore(t, db, wsID)

	tests := []struct {
		name     string
		mutate   func()
		wantSame bool
	}{
		{"unchanged", func() {}, true},
		{"seed changed", func() {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 2, ListSize: 5})
		}, false},
		{"listSize changed", func() {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 6})
		}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mustSetSettings(t, db, wsID, domain.Settings{Seed: 1, ListSize: 5})
			tt.mutate()
			err := runFenceTx(t, db, wsID, before, 1, 5)
			same := err == nil
			if same != tt.wantSame {
				t.Fatalf("fenceConfirmTx same=%v err=%v, want same=%v", same, err, tt.wantSame)
			}
		})
	}
}

func TestFenceConfirmTx_IncarnationVsBareRevisionBump(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	wsID := insertWorkspace(t, db, "alpha", nil, domain.Settings{Seed: 1, ListSize: 5})
	before := mustReadCore(t, db, wsID)

	// A bare revision bump — a second family's confirm, or an anonymous
	// POST {prefix}/state — must NOT refuse.
	if err := db.Write(t.Context(), func(tx *sql.Tx) error {
		return bumpRevisionTx(t.Context(), tx, wsID, time.Now())
	}); err != nil {
		t.Fatalf("bump revision: %v", err)
	}
	if err := runFenceTx(t, db, wsID, before, 1, 5); err != nil {
		t.Fatalf("fenceConfirmTx after a bare revision bump = %v, want nil", err)
	}

	// A workspace deleted and re-created with the SAME id (created_at
	// differs) refuses with ErrStaleConfig.
	if _, err := db.W.ExecContext(t.Context(), "DELETE FROM workspaces WHERE id = ?", wsID); err != nil {
		t.Fatalf("delete workspace: %v", err)
	}
	if _, err := db.W.ExecContext(t.Context(), `
		INSERT INTO workspaces (id, slug, name, revision, settings, created_at, updated_at)
		VALUES (?, ?, ?, 1, '{}', ?, ?)`,
		wsID, "alpha", "alpha", time.Now().Unix()+1, time.Now().Unix()+1); err != nil {
		t.Fatalf("re-create workspace: %v", err)
	}
	if err := runFenceTx(t, db, wsID, before, 1, 5); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("fenceConfirmTx after workspace re-creation = %v, want ErrStaleConfig", err)
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

// --- ForWorkspace (mockplane.ResourceSource) -------------------------------

// TestForWorkspace_ReturnsConfirmedResources proves the method the review
// finding "resources.Repo never implements a ForWorkspace-style bulk read"
// says the package needs actually exists and answers what buildRuntime
// (internal/mockplane/runtime.go) needs: every CONFIRMED resource of ONE
// workspace, none of another's, keyed (by the caller, not this method) on
// RouteFamily.
func TestForWorkspace_ReturnsConfirmedResources(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsA := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 3})
	wsB := insertWorkspace(t, db, "beta", &specID, domain.Settings{Seed: 1, ListSize: 3})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	widgets, err := repo.Confirm(t.Context(), wsA, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm widgets for wsA: %v", err)
	}
	users, err := repo.Confirm(t.Context(), wsA, familyUsers)
	if err != nil {
		t.Fatalf("Confirm users for wsA: %v", err)
	}
	// A confirm on wsB must never show up in wsA's list.
	if _, err := repo.Confirm(t.Context(), wsB, familyWidgets); err != nil {
		t.Fatalf("Confirm widgets for wsB: %v", err)
	}

	got, err := repo.ForWorkspace(t.Context(), wsA)
	if err != nil {
		t.Fatalf("ForWorkspace: %v", err)
	}
	if len(got) != 2 {
		t.Fatalf("ForWorkspace(wsA) returned %d resources, want 2 (widgets, users)", len(got))
	}
	byFamily := make(map[string]*Resource, len(got))
	for _, r := range got {
		byFamily[r.RouteFamily] = r
	}
	w, ok := byFamily[familyWidgets]
	if !ok {
		t.Fatalf("ForWorkspace(wsA) missing %q", familyWidgets)
	}
	if w.ID != widgets.ID || w.IDField != "id" || w.SeedCount != 3 || w.WorkspaceID != wsA {
		t.Fatalf("widgets row = %+v, want ID=%d IDField=id SeedCount=3 WorkspaceID=%d", w, widgets.ID, wsA)
	}
	if w.WriteForm == nil || *w.WriteForm != "bare" {
		t.Fatalf("widgets.WriteForm = %v, want \"bare\" (the fixture's POST body matches the item schema)", w.WriteForm)
	}
	u, ok := byFamily[familyUsers]
	if !ok {
		t.Fatalf("ForWorkspace(wsA) missing %q", familyUsers)
	}
	if u.ID != users.ID || u.IDField != "userId" {
		t.Fatalf("users row = %+v, want ID=%d IDField=userId", u, users.ID)
	}
	if u.WriteForm != nil {
		t.Fatalf("users.WriteForm = %v, want nil (the fixture declares no POST on /users)", *u.WriteForm)
	}

	// A workspace with no confirmed resource at all gets an empty slice,
	// never an error.
	wsEmpty := insertWorkspace(t, db, "gamma", &specID, domain.Settings{})
	got, err = repo.ForWorkspace(t.Context(), wsEmpty)
	if err != nil || len(got) != 0 {
		t.Fatalf("ForWorkspace(wsEmpty) = %v, %v, want empty, nil", got, err)
	}
}

// --- R37: a vanished resource is distinguishable from an empty one --------

// TestListGetDelete_ErrResourceGoneWhenResourceRowMissing is D13 clause 34
// exercised directly against the real store, not a hand-written fake: the
// review finding this defends against is that List/Get/Delete queried only
// the entities table, so a resource declined out from under a parked
// request (R37) answered an ordinary empty/not-found instead of
// ErrResourceGone — silently breaking [resourceBranch]'s (internal/
// mockplane/resource.go) fall-through-to-the-generator contract for three
// of the four verbs. resourceID here never named a row at all (never
// inserted), the simplest stand-in for "the row is gone" that needs no
// Confirm-then-Decline dance to set up.
func TestListGetDelete_ErrResourceGoneWhenResourceRowMissing(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	repo := newTestRepo(t, db, 4<<20, 64<<10)
	const goneID = 999999

	if _, err := repo.List(t.Context(), goneID, "", ""); !errors.Is(err, ErrResourceGone) {
		t.Fatalf("List on a missing resource = %v, want ErrResourceGone", err)
	}
	if _, ok, err := repo.Get(t.Context(), goneID, "", "", "1"); !errors.Is(err, ErrResourceGone) || ok {
		t.Fatalf("Get on a missing resource = ok=%v err=%v, want ok=false err=ErrResourceGone", ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), goneID, "", "", "1"); !errors.Is(err, ErrResourceGone) || deleted {
		t.Fatalf("Delete on a missing resource = deleted=%v err=%v, want deleted=false err=ErrResourceGone", deleted, err)
	}
}

// TestListGetDelete_ErrResourceGoneAfterDecline is the same property through
// the real lifecycle: Confirm, populate an entity via Create, Decline (which
// deletes the resources row and cascades its entities), then prove List/Get/
// Delete on the now-gone resourceID answer ErrResourceGone — never a bare
// empty list or a bare not-found, which [resourceBranch] cannot tell apart
// from "the family is confirmed but this row/entity legitimately doesn't
// exist".
func TestListGetDelete_ErrResourceGoneAfterDecline(t *testing.T) {
	t.Parallel()
	db := newTestDB(t, t.TempDir()+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	entities, err := repo.List(t.Context(), res.ID, "", "")
	if err != nil || len(entities) == 0 {
		t.Fatalf("List before decline = %v, %v, want at least one entity", entities, err)
	}
	key := entities[0].EntityKey

	if err := repo.Decline(t.Context(), wsID, familyWidgets, "alpha"); err != nil {
		t.Fatalf("Decline: %v", err)
	}

	if _, err := repo.List(t.Context(), res.ID, "", ""); !errors.Is(err, ErrResourceGone) {
		t.Fatalf("List after decline = %v, want ErrResourceGone", err)
	}
	if _, ok, err := repo.Get(t.Context(), res.ID, "", "", key); !errors.Is(err, ErrResourceGone) || ok {
		t.Fatalf("Get after decline = ok=%v err=%v, want ok=false err=ErrResourceGone", ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, "", "", key); !errors.Is(err, ErrResourceGone) || deleted {
		t.Fatalf("Delete after decline = deleted=%v err=%v, want deleted=false err=ErrResourceGone", deleted, err)
	}
}

// TestConfirm_NestedFamily_ZeroLiveParents_PopulatesNothing is the P=0 edge
// of D5.3's "one row set per live ancestor tuple" — a nested family
// confirmed while its already-confirmed parent has ZERO live entities
// (every row deleted through the ordinary Delete route, exactly as an
// operator would) must generate zero row sets, not one implicit unscoped
// batch. [chainScopes] is what has to get this right now: an empty
// keysByParent at the parent's own level makes [extendScopes] fan out to
// zero tuples, so the whole scope list this family populates against is
// empty — never falling back to the single top-level scope [""], which
// would write seedCount rows at scope_key="", a scope no live parent key
// will ever anchor (D6.3): permanent garbage rows counted by
// [Repo.CountEntities] but unreachable through any GET.
// familyOrgs/familyOrgUsers/nestedFixtureDoc are declared in
// nested_test.go, same package.
func TestConfirm_NestedFamily_ZeroLiveParents_PopulatesNothing(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, []byte(nestedFixtureDoc))
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, familyOrgs)
	if err != nil {
		t.Fatalf("confirm parent %q: %v", familyOrgs, err)
	}
	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list org entities: %v", err)
	}
	for _, e := range orgEntities {
		if _, derr := repo.Delete(t.Context(), org.ID, "", "", e.EntityKey); derr != nil {
			t.Fatalf("delete org entity %q: %v", e.EntityKey, derr)
		}
	}
	if got := entityCount(t, repo.db, org.ID); got != 0 {
		t.Fatalf("org entity count after deleting every row = %d, want 0", got)
	}

	user, err := repo.Confirm(t.Context(), wsID, familyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child %q over a parent with zero live rows: %v", familyOrgUsers, err)
	}
	if got := entityCount(t, repo.db, user.ID); got != 0 {
		t.Fatalf("user entity count = %d, want 0 (P=0 live parents)", got)
	}
	// D5.5 point 4: Seq is the family-wide TOTAL just inserted (P×L = 0),
	// SeedCount stays the PER-SCOPE count clampSeedCount(listSize) computes
	// from settings alone (L=2) — P=0 changes how many scopes exist, not
	// what a single scope's own count is.
	if user.Seq != 0 {
		t.Fatalf("user.Seq = %d, want 0 (P=0 * L=2)", user.Seq)
	}
	if user.SeedCount != 2 {
		t.Fatalf("user.SeedCount = %d, want 2", user.SeedCount)
	}
	entities, err := repo.List(t.Context(), user.ID, "", "")
	if err != nil {
		t.Fatalf("list user entities: %v", err)
	}
	if len(entities) != 0 {
		t.Fatalf("user entities = %v, want none", entities)
	}
}

// --- P3g: deep nesting (depth 2/3), decisions.md mocker-p3g-deep-nesting --

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
	db := newTestDB(t, dir+"/mocker.db")
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

// TestConfirm_DepthTwo_ImmediateParentNotConfirmed_Refuses is P4: confirming
// a depth-2 family (users) whose IMMEDIATE parent (teams) is not confirmed
// answers 409 parent_not_confirmed and writes no resources row — even
// though the ROOT (orgs) two levels up IS confirmed. Mutation this fails
// under: deleting the CONFIRMED predicate from the parent read in
// [Repo.prepareConfirm] (D5.1's own single-hop check).
func TestConfirm_DepthTwo_ImmediateParentNotConfirmed_Refuses(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs); err != nil {
		t.Fatalf("confirm root: %v", err)
	}
	// FamilyDeepTeams (the immediate parent of users) is deliberately NOT
	// confirmed.
	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepUsers); !errors.Is(err, ErrParentNotConfirmed) {
		t.Fatalf("confirm depth-2 family with root confirmed but immediate parent not = %v, want ErrParentNotConfirmed", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepUsers); n != 0 {
		t.Errorf("resource row written on a refused confirm, want none")
	}
	// P4's own second assertion is deliberately NOT a row COUNT: with no
	// confirmed parent there are no scopes and the population is empty
	// under both a correct and a wrong implementation.
}

// TestConfirm_DepthTwo_PopulatesOneRowSetPerAncestorTuple is P5: a depth-2
// family's population is one row set per LIVE ancestor TUPLE, not per
// immediate-parent key alone (D5.3). At L=2, orgs=2, teams=2 per org (4
// total, over 2 one-value scopes), users=2 per team over 4 DISTINCT
// two-value scopes — never all users collapsing onto one scope keyed by
// the innermost value alone, which is the mutation P5 names
// (EncodeScope([]string{key}), the deleted P3e line).
func TestConfirm_DepthTwo_PopulatesOneRowSetPerAncestorTuple(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	if len(orgEntities) != 2 {
		t.Fatalf("orgs = %d, want 2", len(orgEntities))
	}

	seenScopes := map[ScopeKey]bool{}
	totalTeams, totalUsers := 0, 0
	for _, orgE := range orgEntities {
		teamEntities, terr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if terr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, terr)
		}
		if len(teamEntities) != 2 {
			t.Fatalf("teams under org %q = %d, want 2", orgE.EntityKey, len(teamEntities))
		}
		totalTeams += len(teamEntities)

		for _, teamE := range teamEntities {
			scope := EncodeScope([]string{orgE.EntityKey, teamE.EntityKey})
			if seenScopes[scope] {
				t.Fatalf("scope %q seen under two different teams — tuples are not distinct", scope)
			}
			seenScopes[scope] = true

			userEntities, uerr := repo.List(t.Context(), users.ID, "", scope)
			if uerr != nil {
				t.Fatalf("list users under scope %q: %v", scope, uerr)
			}
			if len(userEntities) != 2 {
				t.Fatalf("users under scope %q = %d, want 2 (D5.3: one row set per live ancestor tuple)", scope, len(userEntities))
			}
			totalUsers += len(userEntities)
		}
	}
	if totalTeams != 4 {
		t.Fatalf("total teams = %d, want 4 (2 orgs * 2 teams)", totalTeams)
	}
	if len(seenScopes) != 4 {
		t.Fatalf("distinct user scopes = %d, want 4", len(seenScopes))
	}
	if totalUsers != 8 {
		t.Fatalf("total users = %d, want 8 (4 team scopes * 2 users)", totalUsers)
	}
}

// TestConfirm_DepthTwo_EntityKeyUniqueAcrossEveryScope is P6: entity_key
// stays unique FAMILY-WIDE across every one of a depth-2 family's scopes —
// keys "1".."8" with no repeat across the 4 scopes — and the next POST,
// into any scope, mints the family-wide next key "9", never restarting per
// scope. Mutation this fails under: restarting the per-scope counter at 1
// (every scope would then hold "1","2", and the UNIQUE index — which
// includes scope_key — would not complain).
func TestConfirm_DepthTwo_EntityKeyUniqueAcrossEveryScope(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	seenKeys := map[string]bool{}
	var firstScope ScopeKey
	for _, orgE := range orgEntities {
		teamEntities, terr := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgE.EntityKey}))
		if terr != nil {
			t.Fatalf("list teams under org %q: %v", orgE.EntityKey, terr)
		}
		for _, teamE := range teamEntities {
			scope := EncodeScope([]string{orgE.EntityKey, teamE.EntityKey})
			if firstScope == "" {
				firstScope = scope
			}
			userEntities, uerr := repo.List(t.Context(), users.ID, "", scope)
			if uerr != nil {
				t.Fatalf("list users under scope %q: %v", scope, uerr)
			}
			for _, u := range userEntities {
				if seenKeys[u.EntityKey] {
					t.Fatalf("entity_key %q repeated across scopes — not unique family-wide", u.EntityKey)
				}
				seenKeys[u.EntityKey] = true
			}
		}
	}
	for _, want := range []string{"1", "2", "3", "4", "5", "6", "7", "8"} {
		if !seenKeys[want] {
			t.Errorf("entity_key %q missing from the family-wide key set %v", want, seenKeys)
		}
	}
	if len(seenKeys) != 8 {
		t.Fatalf("distinct entity_key count = %d, want 8", len(seenKeys))
	}

	created, err := repo.Create(t.Context(), users.ID, "", firstScope, users.IDField, users.Wrapper.IDType, map[string]any{"name": "extra"})
	if err != nil {
		t.Fatalf("create into scope %q: %v", firstScope, err)
	}
	if created.EntityKey != "9" {
		t.Fatalf("POST after a depth-2 confirm minted entityKey %q, want \"9\" (family-wide next key)", created.EntityKey)
	}
}

// TestConfirm_DepthThree_OverCap_RefusesDeepestFamily_LeavesRestAlone is
// P7: a confirm whose prepared population exceeds the row cap answers 409
// entity_limit and writes nothing — for the FAMILY THAT ACTUALLY EXCEEDS
// IT, not the whole chain. At L=6 (D2's own table), depths 0..3 hold 6,
// 36, 216 and 1296 rows — only the deepest (badges, depth 3) crosses
// defaultMaxEntityRows (1000); orgs/teams/users must stay confirmed and untouched
// (D5.4: "the refusal lands on the DEEPEST family... nothing rolls back").
func TestConfirm_DepthThree_OverCap_RefusesDeepestFamily_LeavesRestAlone(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 6})
	// Generous byte caps: only the ROW-COUNT cap (1000) is meant to fire
	// here, not either byte cap incidentally.
	repo := newTestRepo(t, db, 64<<20, 64<<10)

	for _, fam := range []string{testspec.FamilyDeepOrgs, testspec.FamilyDeepTeams, testspec.FamilyDeepUsers} {
		if _, err := repo.Confirm(t.Context(), wsID, fam); err != nil {
			t.Fatalf("confirm %q: %v", fam, err)
		}
	}

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepBadges); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("confirm badges (1296 rows over the 1000 cap) = %v, want ErrEntityLimit", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepBadges); n != 0 {
		t.Errorf("badges resources row written on a refused confirm, want none")
	}
	if got := entityCount(t, db, mustResourceID(t, db, wsID, testspec.FamilyDeepUsers)); got != 216 {
		t.Errorf("users entity count changed by an unrelated refusal deeper in the chain, got %d, want 216", got)
	}
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

// TestDecline_Root_WithConfirmedGrandchild_RefusesNamingMiddle is P11: a
// three-level chain confirmed (orgs/teams/users), declining the ROOT
// refuses with 409 child_confirmed and the message names the MIDDLE family
// (teams — orgs' own DIRECT child), never the grandchild two levels down.
// D5.2's induction proof is what makes the existing SINGLE-HOP check
// (unchanged by this slice) correct at this depth: declining leaf, then
// middle, then root then succeeds in that order.
func TestDecline_Root_WithConfirmedGrandchild_RefusesNamingMiddle(t *testing.T) {
	t.Parallel()
	repo, _, _, _, wsID := confirmDeepChain(t, 2)

	err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepOrgs, "acme")
	if !errors.Is(err, ErrChildConfirmed) {
		t.Fatalf("decline root with a depth-2 grandchild confirmed = %v, want ErrChildConfirmed", err)
	}
	if !strings.Contains(err.Error(), testspec.FamilyDeepTeams) {
		t.Errorf("ErrChildConfirmed message = %q, want it to name the MIDDLE family %q", err, testspec.FamilyDeepTeams)
	}
	if strings.Contains(err.Error(), testspec.FamilyDeepUsers) {
		t.Errorf("ErrChildConfirmed message = %q, names the GRANDCHILD too — D5.1 stays single-hop", err)
	}

	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepUsers, "acme"); err != nil {
		t.Fatalf("decline leaf: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepTeams, "acme"); err != nil {
		t.Fatalf("decline middle: %v", err)
	}
	if err := repo.Decline(t.Context(), wsID, testspec.FamilyDeepOrgs, "acme"); err != nil {
		t.Fatalf("decline root after both descendants are gone: %v", err)
	}
}

// TestConfirm_DepthTwo_ScopeParamsHoldDetailRouteNames is P15: scope_params
// holds the DETAIL route's own outer parameter NAMES, in order —
// ["organizationId", "team"] on [testspec.DeepNestingDoc]'s depth-2 family,
// whose detail route spells BOTH outer parameters differently from its
// collection route ("orgId"/"teamId"). Asserted by VALUE, deliberately not
// by length: [outerParamNames] drops the last path SEGMENT by position, so
// both the collection and the detail route give exactly two names at this
// depth — a length assertion is green under a mutation that reads the
// WRONG route's spelling (D13's own warning: an earlier draft of this
// property asserted the length and was green against the very
// implementation it names).
func TestConfirm_DepthTwo_ScopeParamsHoldDetailRouteNames(t *testing.T) {
	t.Parallel()
	_, _, _, users, _ := confirmDeepChain(t, 2)
	want := []string{"organizationId", "team"}
	if !slices.Equal(users.ScopeParams, want) {
		t.Fatalf("users.ScopeParams = %v, want %v (the DETAIL route's own spelling)", users.ScopeParams, want)
	}
}

// TestConfirm_DeepChain_CascadeColumnsStayNull is P23: both
// resources.parent_id and entities.parent_entity_id stay NULL on every row
// of a three-level confirmed chain, including a row written through the
// ordinary POST path afterward (D9 — a decision, not a deferral: see
// "Architecture" in CLAUDE.md for the argument in full). Mutation this
// fails under, one at a time: writing the parent's resources.id into
// resources.parent_id at confirm ([insertConfirmedResourceTx], which
// hard-codes NULL today), or writing the anchoring parent row's entities.id
// into entities.parent_entity_id at population (Confirm's own entity
// INSERT, which never names that column at all).
func TestConfirm_DeepChain_CascadeColumnsStayNull(t *testing.T) {
	t.Parallel()
	repo, org, teams, users, _ := confirmDeepChain(t, 2)

	orgEntities, err := repo.List(t.Context(), org.ID, "", "")
	if err != nil {
		t.Fatalf("list orgs: %v", err)
	}
	teamEntities, err := repo.List(t.Context(), teams.ID, "", EncodeScope([]string{orgEntities[0].EntityKey}))
	if err != nil {
		t.Fatalf("list teams: %v", err)
	}
	scope := EncodeScope([]string{orgEntities[0].EntityKey, teamEntities[0].EntityKey})
	if _, err := repo.Create(t.Context(), users.ID, "", scope, users.IDField, users.Wrapper.IDType, map[string]any{"name": "extra"}); err != nil {
		t.Fatalf("create extra user via the ordinary POST path: %v", err)
	}

	var resourcesWithParent, entitiesWithParent int
	if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM resources WHERE parent_id IS NOT NULL").Scan(&resourcesWithParent); err != nil {
		t.Fatalf("count resources.parent_id: %v", err)
	}
	if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities WHERE parent_entity_id IS NOT NULL").Scan(&entitiesWithParent); err != nil {
		t.Fatalf("count entities.parent_entity_id: %v", err)
	}
	if resourcesWithParent != 0 {
		t.Errorf("resources.parent_id non-null rows = %d, want 0 (D9)", resourcesWithParent)
	}
	if entitiesWithParent != 0 {
		t.Errorf("entities.parent_entity_id non-null rows = %d, want 0 (D9)", entitiesWithParent)
	}
}

// TestConfirm_NestedFamily_AncestorSwap_CaughtElementWise is P24: the
// confirm fence ([Repo.fenceParentTx]) compares the recomputed scope list
// ELEMENT-WISE, not by length. Swapping the root's SECOND row for a fresh
// one (delete, then create — never reusing the deleted key, since
// [allocateSeq] never reissues) keeps the scope COUNT at 2 but changes the
// tuple at that position; a length-only fence would miss it. The swap
// lands inside [confirmPreWriteHook]'s own window — after
// [Repo.prepareConfirm]'s generation half reads the live keys, before the
// write transaction re-reads them — so it races [Repo.fenceParentTx]
// itself, not a bespoke stand-in for it.
func TestConfirm_NestedFamily_AncestorSwap_CaughtElementWise(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importSpecDoc(t, db, testspec.DeepNestingDoc())
	wsID := insertWorkspace(t, db, "acme", &specID, domain.Settings{Seed: 1, ListSize: 2})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	org, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs)
	if err != nil {
		t.Fatalf("confirm root: %v", err)
	}

	confirmPreWriteHook = func() {
		if _, derr := repo.Delete(t.Context(), org.ID, "", "", "2"); derr != nil {
			t.Errorf("swap: delete org entity 2: %v", derr)
		}
		if _, cerr := repo.Create(t.Context(), org.ID, "", "", org.IDField, org.Wrapper.IDType, map[string]any{"name": "swapped org"}); cerr != nil {
			t.Errorf("swap: create replacement org entity: %v", cerr)
		}
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("confirm over a swapped ancestor row = %v, want ErrStaleConfig", err)
	}
	if n := resourceRowCount(t, db, wsID, testspec.FamilyDeepTeams); n != 0 {
		t.Errorf("teams resources row written despite the swap being refused")
	}
}

// --- P3h: the base scope — D18/D14's P1-P4, P9-P11 --------------------

// baseFixtureDoc is fixtureDoc's own /widgets family alone, reused here so
// every base-scope test below shares one small document rather than each
// re-declaring its own.
const declaredBaseA = "7"
const declaredBaseB = "8"

// TestList_Get_Create_Delete_DisjointAcrossBaseValues is P1/P2/P3 together:
// two requests differing only in a declared base value answer disjoint row
// sets (P1), a write into one base value is invisible in another (P2), and
// a keyed request (Get/Delete) addressed under one declared base value
// cannot reach a row that belongs to another (P3).
func TestList_Get_Create_Delete_DisjointAcrossBaseValues(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{declaredBaseA, declaredBaseB},
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}

	baseA, baseB := ScopeKey(declaredBaseA), ScopeKey(declaredBaseB)

	// P1: the two declared base values answer disjoint row sets, each of
	// the confirm's own listSize (3).
	entitiesA, err := repo.List(t.Context(), res.ID, baseA, "")
	if err != nil {
		t.Fatalf("List base A: %v", err)
	}
	entitiesB, err := repo.List(t.Context(), res.ID, baseB, "")
	if err != nil {
		t.Fatalf("List base B: %v", err)
	}
	if len(entitiesA) != 3 || len(entitiesB) != 3 {
		t.Fatalf("len(entitiesA)=%d len(entitiesB)=%d, want 3 and 3", len(entitiesA), len(entitiesB))
	}
	keysA := map[string]bool{}
	for _, e := range entitiesA {
		keysA[e.EntityKey] = true
	}
	for _, e := range entitiesB {
		if keysA[e.EntityKey] {
			t.Fatalf("entity_key %q present under BOTH base A and base B — the two collections must be disjoint", e.EntityKey)
		}
	}

	// P2: a POST into base A is invisible in base B, and B's collection is
	// byte-identical to what it was before.
	created, err := repo.Create(t.Context(), res.ID, baseA, "", res.IDField, res.Wrapper.IDType, map[string]any{"name": "into-A"})
	if err != nil {
		t.Fatalf("Create into base A: %v", err)
	}
	entitiesBAfter, err := repo.List(t.Context(), res.ID, baseB, "")
	if err != nil {
		t.Fatalf("List base B after write: %v", err)
	}
	if len(entitiesBAfter) != len(entitiesB) {
		t.Fatalf("base B collection changed after a write into base A: len=%d, want unchanged %d", len(entitiesBAfter), len(entitiesB))
	}
	for i := range entitiesB {
		if entitiesBAfter[i].EntityKey != entitiesB[i].EntityKey || string(entitiesBAfter[i].Data) != string(entitiesB[i].Data) {
			t.Fatalf("base B row %d changed after a write into base A", i)
		}
	}
	entitiesAAfter, err := repo.List(t.Context(), res.ID, baseA, "")
	if err != nil {
		t.Fatalf("List base A after write: %v", err)
	}
	found := false
	for _, e := range entitiesAAfter {
		if e.EntityKey == created.EntityKey {
			found = true
		}
	}
	if !found {
		t.Fatalf("the row created under base A does not appear in base A's own collection")
	}

	// P3: a keyed request (Get/Delete) addressed under one declared base
	// value cannot reach a row that belongs to another. entity_key is ONE
	// counter across the whole family (D6.2), so the same key never exists
	// under both base scopes at once — the defect this property observes is
	// a cross-value REACH, not a same-key collision.
	victimKey := entitiesA[0].EntityKey
	if _, ok, err := repo.Get(t.Context(), res.ID, baseB, "", victimKey); err != nil || ok {
		t.Fatalf("Get(baseB, key=%q) = ok=%v err=%v, want ok=false (the key belongs to base A)", victimKey, ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, baseB, "", victimKey); err != nil || deleted {
		t.Fatalf("Delete(baseB, key=%q) = deleted=%v err=%v, want deleted=false", victimKey, deleted, err)
	}
	// The row survives byte-identical under its OWN base value.
	if _, ok, err := repo.Get(t.Context(), res.ID, baseA, "", victimKey); err != nil || !ok {
		t.Fatalf("Get(baseA, key=%q) after the refused cross-base delete = ok=%v err=%v, want ok=true", victimKey, ok, err)
	}
	// And the identical requests through base A's OWN url succeed.
	if _, ok, err := repo.Get(t.Context(), res.ID, baseA, "", victimKey); err != nil || !ok {
		t.Fatalf("Get(baseA, key=%q) = ok=%v err=%v, want ok=true", victimKey, ok, err)
	}
	if deleted, err := repo.Delete(t.Context(), res.ID, baseA, "", victimKey); err != nil || !deleted {
		t.Fatalf("Delete(baseA, key=%q) = deleted=%v err=%v, want deleted=true", victimKey, deleted, err)
	}
}

// TestConfirm_PopulatesOneRowSetPerDeclaredBaseValue is P4: a confirm over
// THREE declared base values holds 3*listSize rows, listSize under each of
// the three base scope keys.
func TestConfirm_PopulatesOneRowSetPerDeclaredBaseValue(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	values := []string{"1", "2", "3"}
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 4, BasePath: "/orgs/{orgId}", BasePathValues: values,
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	res, err := repo.Confirm(t.Context(), wsID, familyWidgets)
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	if got := entityCount(t, db, res.ID); got != 3*4 {
		t.Fatalf("total entity count = %d, want %d (3 declared values x listSize 4)", got, 3*4)
	}
	for _, v := range values {
		n, err := repo.List(t.Context(), res.ID, ScopeKey(v), "")
		if err != nil {
			t.Fatalf("List base %q: %v", v, err)
		}
		if len(n) != 4 {
			t.Errorf("base %q holds %d rows, want listSize 4 — a declared value with no rows fails this property", v, len(n))
		}
	}
}

// TestConfirm_EmptyDeclaredSetRefuses is P9: basePath carries a parameter
// and basePathValues is empty -> 409 base_scope_undeclared, and the confirm
// writes NOTHING — no resources row, no resource_decisions row, no entity.
func TestConfirm_EmptyDeclaredSetRefuses(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: nil,
	})
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrBaseScopeUndeclared) {
		t.Fatalf("Confirm with an empty declared set = %v, want ErrBaseScopeUndeclared", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the refusal: %d", n)
	}
	if state, ok := decisionState(t, db, wsID, familyWidgets); ok {
		t.Fatalf("a resource_decisions row was written despite the refusal: state=%q", state)
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("%d entity rows were written despite the refusal", total)
	}
}

// TestConfirm_PopulationExceedingCapAcrossDeclaredSetRefuses is P10: the
// cap check sees the DECLARED-SET factor, not just listSize^depth. Six
// declared values, a top-level family, listSize clamped to its own ceiling
// of 200 (clampSeedCount) -> 6*200=1200 > 1000, refused with no partial
// write — the top-level analogue of the worked example decisions.md D6.3
// gives for a depth-1 nested family, over this package's own default cap
// of 1000.
func TestConfirm_PopulationExceedingCapAcrossDeclaredSetRefuses(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	wsID := insertWorkspace(t, db, "alpha", &specID, domain.Settings{
		Seed: 1, ListSize: 200, BasePath: "/orgs/{orgId}", BasePathValues: []string{"1", "2", "3", "4", "5", "6"},
	})
	repo := newTestRepo(t, db, 64<<20, 64<<10) // generous byte caps: only the ROW cap must bite

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrEntityLimit) {
		t.Fatalf("Confirm over 1200 prepared rows = %v, want ErrEntityLimit", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the cap refusal: %d", n)
	}
	var total int
	if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM entities").Scan(&total); err != nil {
		t.Fatalf("count entities: %v", err)
	}
	if total != 0 {
		t.Fatalf("%d entity rows were written despite the cap refusal — the declared-set factor was not seen by the cap check", total)
	}
}

// TestFenceConfirmTx_CatchesStaleBasePathValues is P11: a settings edit
// that changes basePathValues, landing INSIDE the write transaction's own
// window (through confirmPreWriteHook — never before prepareConfirm's own
// read, which the pre-transaction fence already catches and proves
// nothing about the IN-TRANSACTION one), is caught with 409 stale_config —
// and this runs WHILE A SCENARIO IS ACTIVE, because D6.5 requires
// basePath/basePathValues to join the UNCONDITIONAL half of the fence,
// never the `!scenarioID.Valid` branch guarding seed/listSize: a fixture
// with no scenario active would pass even with the wrong placement.
func TestFenceConfirmTx_CatchesStaleBasePathValues(t *testing.T) {
	dir := t.TempDir()
	db := newTestDB(t, dir+"/mocker.db")
	specID := importFixtureSpec(t, db)
	settings := domain.Settings{Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7"}}
	wsID := insertWorkspace(t, db, "alpha", &specID, settings)
	repo := newTestRepo(t, db, 4<<20, 64<<10)

	scenarioID := insertScenario(t, db, wsID, "s1", settings)
	activateScenario(t, db, wsID, scenarioID)

	confirmPreWriteHook = func() {
		mustSetSettings(t, db, wsID, domain.Settings{
			Seed: 1, ListSize: 3, BasePath: "/orgs/{orgId}", BasePathValues: []string{"7", "8"},
		})
	}
	t.Cleanup(func() { confirmPreWriteHook = confirmPreWriteHookNoop })

	if _, err := repo.Confirm(t.Context(), wsID, familyWidgets); !errors.Is(err, ErrStaleConfig) {
		t.Fatalf("Confirm with basePathValues changed inside the window = %v, want ErrStaleConfig", err)
	}
	if n := resourceRowCount(t, db, wsID, familyWidgets); n != 0 {
		t.Fatalf("a resources row was written despite the fence refusal: %d", n)
	}
}
