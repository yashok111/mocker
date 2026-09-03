// resource_integration_test.go is the one file in this package that stands
// up a REAL *store.DB, a real *specs.Repo and a real *resources.Repo behind
// [Plane.SetResources]/[Plane.SetEntities], rather than the hand-rolled
// fakes every other test in this package uses (fixtureRuntime's own fake
// SpecSource, resource_test.go's fakeEntityStore/stubResourceSource). Two
// properties genuinely need that: D13 clause 23's "after a WARMED runtime,
// a decline makes the very next request fall back to the generator with no
// restart" — a fake ResourceSource wired once by hand cannot distinguish
// "buildRuntime re-consults it on every cache miss" from "it was cached
// forever", since the fake IS the cache in every other test — and clause
// 24's "a spec re-bind does not corrupt a confirmed resource's stored
// rows", which is a property of the REAL (workspace_id, revision) route
// table built from a REAL SpecSource, not something a fixture *runtime
// built by hand (fixtureRuntime) can exercise at all.
package mockplane

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/testspec"
	"github.com/yashok111/mocker/internal/workspaces"
)

// resourceIntegrationConfig mirrors internal/resources/repo_test.go's own
// testSpecConfig — the config shape both *specs.Repo and *resources.Repo
// need to exist at all.
func resourceIntegrationConfig(t *testing.T) *config.Config {
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

func resourceIntegrationDB(t *testing.T) *store.DB {
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

// resourceIntegrationImport imports doc under name (distinct bytes give a
// distinct spec id — Import dedupes by raw hash, so two calls with
// byte-identical documents would collapse into one spec, defeating a
// re-bind test that needs two).
func resourceIntegrationImport(t *testing.T, sr *specs.Repo, name, doc string) int64 {
	t.Helper()
	res, err := sr.Import(t.Context(), specs.ImportInput{Name: name, Source: "upload", Document: []byte(doc)})
	if err != nil {
		t.Fatalf("import spec %q: %v", name, err)
	}
	return res.Spec.ID
}

// resourceIntegrationWorkspace inserts a workspaces row directly (this
// package's own tables live outside internal/workspaces, so every fixture
// in this tree writes the row by hand rather than standing up a full
// *workspaces.Repo) and returns its id.
func resourceIntegrationWorkspace(t *testing.T, db *store.DB, slug string, specID int64, settings domain.Settings) int64 {
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

// rebindDocA/rebindDocB are byte-distinct from each other (different
// info.title, otherwise identical) so importing both yields two SEPARATE
// spec rows (Import dedupes by raw hash) that declare the exact same
// /widgets family — the shape D13 clause 24's first half needs: "re-bound
// to a spec that STILL declares the family". Both declare a POST whose
// request body is the identical $ref as the item schema — the one shape
// [computeWriteForm] answers "bare" for (R12) — because clause 24 also
// asks "POST still writes", which needs a route to take over at all: this
// pair is self-contained (rather than reusing respond_test.go's
// widgetsFamilyDoc, which declares no POST) exactly for that reason.
const rebindDocA = `{
  "openapi": "3.0.3",
  "info": { "title": "rebind fixture A", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "responses": {"200": {"description": "ok", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}}
        }}}}
      },
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Widget"}}}},
        "responses": {"201": {"description": "ok", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    },
    "/widgets/{id}": {
      "get": {
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "ok", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    }
  },
  "components": {"schemas": {
    "Widget": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}}
  }}
}`

const rebindDocB = `{
  "openapi": "3.0.3",
  "info": { "title": "rebind fixture B", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "responses": {"200": {"description": "ok", "content": {"application/json": {
          "schema": {"type": "array", "items": {"$ref": "#/components/schemas/Widget"}}
        }}}}
      },
      "post": {
        "requestBody": {"content": {"application/json": {"schema": {"$ref": "#/components/schemas/Widget"}}}},
        "responses": {"201": {"description": "ok", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    },
    "/widgets/{id}": {
      "get": {
        "parameters": [{"name": "id", "in": "path", "required": true, "schema": {"type": "integer"}}],
        "responses": {"200": {"description": "ok", "content": {"application/json": {
          "schema": {"$ref": "#/components/schemas/Widget"}
        }}}}
      }
    }
  },
  "components": {"schemas": {
    "Widget": {"type": "object", "properties": {"id": {"type": "integer"}, "name": {"type": "string"}}}
  }}
}`

// noWidgetsDoc declares an unrelated route and nothing under /widgets at
// all — D13 clause 24's second half: "re-bound to one that does NOT declare
// it".
const noWidgetsDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "no widgets", "version": "1.0.0" },
  "paths": {
    "/other": {
      "get": { "responses": { "200": { "description": "ok" } } }
    }
  }
}`

// TestResourceBranch_DeclineInvalidatesWarmedRuntimeNoRestart is D13 clause
// 23's own harder half — not the revision-bump counter (already held by
// internal/resources/repo_test.go's TestConfirm_StoresEntities), but the
// property no counter can show: a runtime warmed BEFORE a decline does not
// go on serving that resource's storage forever. It goes through the real
// buildRuntime/ForWorkspace wiring end to end — a real *specs.Repo and a
// real *resources.Repo behind [Plane.SetResources]/[Plane.SetEntities],
// neither hand-rolled — never touching rt.resources by hand the way every
// other test in this package does. A [ResourceSource] cached once and never
// re-consulted on the NEXT runtimeFor call would pass every other test in
// this package and still fail this one.
func TestResourceBranch_DeclineInvalidatesWarmedRuntimeNoRestart(t *testing.T) {
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	specRepo := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, specRepo, "widgets", widgetsFamilyDoc)
	resRepo := resources.NewRepo(db, specRepo, cfg.MaxResponse, 64<<10, 1000)

	settings := domain.Settings{Seed: 1, ListSize: 3}
	wsID := resourceIntegrationWorkspace(t, db, "alex", specID, settings)

	p := New(cfg, nil, specRepo, runtimeTestLogger())
	p.SetResources(resRepo)
	p.SetEntities(resRepo)

	ws := &workspaces.Workspace{ID: wsID, Slug: "alex", SpecID: &specID, Revision: 1, Settings: settings}

	// Warm the runtime BEFORE confirming — the exact cache entry the
	// decline below must invalidate, not a fresh build that would
	// trivially already see it.
	rtBefore, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor before confirm: %v", err)
	}
	if len(rtBefore.resources) != 0 {
		t.Fatalf("rtBefore.resources = %v, want empty before any confirm", rtBefore.resources)
	}

	confirmed, err := resRepo.Confirm(t.Context(), wsID, "/widgets")
	if err != nil {
		t.Fatalf("Confirm: %v", err)
	}
	// A marker entity the generator could never itself reproduce (nothing
	// about its own data derivation knows this string) — the one thing
	// that lets GET /widgets's answer AFTER the decline below prove "back
	// on the generator" rather than "coincidentally the same bytes", which
	// clause 5's own determinism (same seed, same settings survive the
	// decline) would otherwise make a plain body comparison worthless for.
	if _, err := resRepo.Create(t.Context(), confirmed.ID, resources.ScopeKey(""), resources.ScopeKey(""), confirmed.IDField, confirmed.Wrapper.IDType, map[string]any{"id": 1, "name": "marker-only-in-storage"}); err != nil {
		t.Fatalf("Create marker entity: %v", err)
	}
	// D13 clause 23's counter half, mirrored here on the SAME workspace
	// this test's runtime half then goes on to exercise: Confirm bumped
	// revision by one.
	ws.Revision++

	rtConfirmed, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor after confirm: %v", err)
	}
	if _, ok := rtConfirmed.resources["/widgets"]; !ok {
		t.Fatalf("rtConfirmed.resources missing /widgets after a real Confirm through the real ForWorkspace wiring")
	}

	reqBefore := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	recBefore := httptest.NewRecorder()
	p.serveGenerated(recBefore, reqBefore, ws, rtConfirmed, mustMatch(t, rtConfirmed, http.MethodGet, "/widgets"), resources.ScopeKey(""))
	if recBefore.Code != http.StatusOK {
		t.Fatalf("GET /widgets before decline: status = %d, body=%s", recBefore.Code, recBefore.Body)
	}
	if !strings.Contains(recBefore.Body.String(), "marker-only-in-storage") {
		t.Fatalf("GET /widgets before decline = %s, want it to include the marker — the confirmed resource must actually be serving from storage for this test to mean anything", recBefore.Body)
	}

	if err := resRepo.Decline(t.Context(), wsID, "/widgets", "alex"); err != nil {
		t.Fatalf("Decline: %v", err)
	}
	ws.Revision++

	// The very next runtimeFor call, on the SAME already-warmed *Plane — no
	// reconstruction, no second SetResources/SetEntities call — must see
	// the (workspace_id, revision) cache miss the decline's own bump
	// caused and re-consult ForWorkspace, which no longer names /widgets.
	rtDeclined, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor after decline: %v", err)
	}
	if _, ok := rtDeclined.resources["/widgets"]; ok {
		t.Fatalf("rtDeclined.resources still carries /widgets — a decline must be visible on the NEXT runtimeFor call with no restart")
	}

	reqAfter := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	recAfter := httptest.NewRecorder()
	p.serveGenerated(recAfter, reqAfter, ws, rtDeclined, mustMatch(t, rtDeclined, http.MethodGet, "/widgets"), resources.ScopeKey(""))
	if recAfter.Code != http.StatusOK {
		t.Fatalf("GET /widgets after decline: status = %d, body=%s", recAfter.Code, recAfter.Body)
	}
	if strings.Contains(recAfter.Body.String(), "marker-only-in-storage") {
		t.Fatalf("GET /widgets after decline = %s, still carries the marker — the request must fall back to the generator, never keep serving the declined family's storage", recAfter.Body)
	}
}

// TestResourceBranch_RebindDoesNotCorrupt is D13 clause 24: after re-binding
// the workspace to a DIFFERENT spec that still declares /widgets, both GETs
// still serve the SAME stored rows and POST still writes; after re-binding
// to one that does NOT declare it, the routes 404 like any undeclared path
// and the rows are still there when the original spec comes back.
func TestResourceBranch_RebindDoesNotCorrupt(t *testing.T) {
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	specRepo := specs.NewRepo(db, cfg)
	specA := resourceIntegrationImport(t, specRepo, "widgets-a", rebindDocA)
	specB := resourceIntegrationImport(t, specRepo, "widgets-b", rebindDocB)
	specC := resourceIntegrationImport(t, specRepo, "no-widgets", noWidgetsDoc)
	resRepo := resources.NewRepo(db, specRepo, cfg.MaxResponse, 64<<10, 1000)

	settings := domain.Settings{Seed: 1, ListSize: 3}
	wsID := resourceIntegrationWorkspace(t, db, "alex", specA, settings)

	p := New(cfg, nil, specRepo, runtimeTestLogger())
	p.SetResources(resRepo)
	p.SetEntities(resRepo)

	ws := &workspaces.Workspace{ID: wsID, Slug: "alex", SpecID: &specA, Revision: 1, Settings: settings}

	confirmed, err := resRepo.Confirm(t.Context(), wsID, "/widgets")
	if err != nil {
		t.Fatalf("Confirm under spec A: %v", err)
	}
	entitiesBefore, err := resRepo.List(t.Context(), confirmed.ID, resources.ScopeKey(""), resources.ScopeKey(""))
	if err != nil || len(entitiesBefore) != 3 {
		t.Fatalf("List after confirm = %v, %v, want 3 entities", entitiesBefore, err)
	}

	rebind := func(specID int64) *runtime {
		t.Helper()
		if _, err := db.W.ExecContext(t.Context(), "UPDATE workspaces SET spec_id = ?, revision = revision + 1 WHERE id = ?", specID, wsID); err != nil {
			t.Fatalf("rebind workspace to spec %d: %v", specID, err)
		}
		ws.SpecID = &specID
		ws.Revision++
		rt, err := p.runtimeFor(t.Context(), ws)
		if err != nil {
			t.Fatalf("runtimeFor after rebind to spec %d: %v", specID, err)
		}
		return rt
	}

	// Re-bind to spec B, which still declares /widgets: both GETs still
	// serve the SAME stored rows, and POST still writes.
	rtB := rebind(specB)
	if _, ok := rtB.resources["/widgets"]; !ok {
		t.Fatalf("rtB.resources missing /widgets after rebinding to a spec that still declares it")
	}
	recList := httptest.NewRecorder()
	p.serveGenerated(recList, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil), ws, rtB, mustMatch(t, rtB, http.MethodGet, "/widgets"), resources.ScopeKey(""))
	if recList.Code != http.StatusOK {
		t.Fatalf("GET /widgets under spec B: status = %d, body=%s", recList.Code, recList.Body)
	}
	got := decodeAny(t, recList.Body.Bytes())
	want := decodeAny(t, []byte(mustMarshalEntities(t, entitiesBefore)))
	if fmt.Sprint(got) != fmt.Sprint(want) {
		t.Errorf("GET /widgets under spec B = %s, want the SAME rows confirmed under spec A: %s", recList.Body.String(), mustMarshalEntities(t, entitiesBefore))
	}

	postRec := httptest.NewRecorder()
	postReq := newPostRequest(t, "http://alex.mock.local/widgets", "application/json", `{"name":"posted-under-spec-b"}`)
	p.serveGenerated(postRec, postReq, ws, rtB, mustMatch(t, rtB, http.MethodPost, "/widgets"), resources.ScopeKey(""))
	if postRec.Code != http.StatusCreated {
		t.Fatalf("POST /widgets under spec B: status = %d, body=%s (write_form was computed at confirm time against spec A and does not change on a rebind)", postRec.Code, postRec.Body)
	}
	if entities, err := resRepo.List(t.Context(), confirmed.ID, resources.ScopeKey(""), resources.ScopeKey("")); err != nil || len(entities) != 4 {
		t.Fatalf("entity count after POST under spec B = %v, %v, want 4", entities, err)
	}

	// Re-bind to spec C, which does NOT declare /widgets at all: the route
	// simply is not in the table any more — a plain unrouted-path miss,
	// the same as any path the spec never named — and the rows survive
	// underneath, untouched.
	rtC := rebind(specC)
	// rtC.resources is a per-WORKSPACE read (ForWorkspace never consults the
	// current spec at all — D14's own carve-out: "the branch runs only
	// after a route resolves"), so the confirmed row still shows up here;
	// what must actually be gone is the ROUTE, checked next.
	if _, ok := rtC.resources["/widgets"]; !ok {
		t.Fatalf("rtC.resources lost /widgets after a rebind — ForWorkspace must stay a per-workspace read, unaffected by which spec is currently bound")
	}
	if _, ok := rtC.table.Match(http.MethodGet, NormalizeSegments("/widgets")); ok {
		t.Fatalf("rtC's route table still matches GET /widgets after rebinding to a spec that declares no such path")
	}
	if entities, err := resRepo.List(t.Context(), confirmed.ID, resources.ScopeKey(""), resources.ScopeKey("")); err != nil || len(entities) != 4 {
		t.Fatalf("entities under spec C = %v, %v, want the same 4 rows, untouched by a rebind that merely makes them unreachable", entities, err)
	}

	// Re-bind back to spec A: the rows are still there, unmodified by the
	// whole round trip.
	rtA2 := rebind(specA)
	recList2 := httptest.NewRecorder()
	p.serveGenerated(recList2, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil), ws, rtA2, mustMatch(t, rtA2, http.MethodGet, "/widgets"), resources.ScopeKey(""))
	if recList2.Code != http.StatusOK {
		t.Fatalf("GET /widgets back under spec A: status = %d, body=%s", recList2.Code, recList2.Body)
	}
	afterRoundTrip, ok := decodeAny(t, recList2.Body.Bytes()).([]any)
	if !ok {
		t.Fatalf("GET /widgets back under spec A did not decode as an array: %s", recList2.Body)
	}
	if len(afterRoundTrip) != 4 {
		t.Fatalf("entity count back under spec A = %d, want 4 (the POST made under spec B survives the whole round trip)", len(afterRoundTrip))
	}
}

// TestResourceBranch_NestedFamily_OuterParamSpelledDifferently_ServedWhole
// is P15 (decisions.md's own acceptance property), on
// [testspec.NestedDerivationDoc] (D4.3) — the fixture that document's own
// export comment says [internal/mockplane] imports for exactly this
// property; before this test, nothing in this package did.
// /orgs/{orgId}/users (collection) and /orgs/{organizationId}/users/{id}
// (detail) spell their shared outer path parameter DIFFERENTLY.
// [router.CanonicalPath] erases both to the SAME family "/orgs/{}/users"
// (D2), so both routes belong to the one confirmed resource — but
// [scopeOf] must read the outer VALUE positionally off route.Path (D5.6),
// never by matching res.ScopeParams (which stores only the DETAIL route's
// own spelling, "organizationId") against m.Params: a by-name read would
// miss on the COLLECTION route (whose match carries "orgId", not
// "organizationId"), yield an empty parent key, and serve that route
// 404 entity_not_found instead of the stored scope — the family served
// HALF, which is the defect this property exists to catch.
func TestResourceBranch_NestedFamily_OuterParamSpelledDifferently_ServedWhole(t *testing.T) {
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	specRepo := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, specRepo, "nested", string(testspec.NestedDerivationDoc()))
	resRepo := resources.NewRepo(db, specRepo, cfg.MaxResponse, 64<<10, 1000)

	settings := domain.Settings{Seed: 1, ListSize: 2}
	wsID := resourceIntegrationWorkspace(t, db, "acme", specID, settings)

	p := New(cfg, nil, specRepo, runtimeTestLogger())
	p.SetResources(resRepo)
	p.SetEntities(resRepo)

	ws := &workspaces.Workspace{ID: wsID, Slug: "acme", SpecID: &specID, Revision: 1, Settings: settings}

	if _, err := resRepo.Confirm(t.Context(), wsID, testspec.FamilyOrgs); err != nil {
		t.Fatalf("confirm parent %q: %v", testspec.FamilyOrgs, err)
	}
	ws.Revision++
	child, err := resRepo.Confirm(t.Context(), wsID, testspec.FamilyOrgUsers)
	if err != nil {
		t.Fatalf("confirm child %q: %v", testspec.FamilyOrgUsers, err)
	}
	ws.Revision++
	// Test precondition, not the property itself: ScopeParams stores the
	// DETAIL route's own spelling (D5.6) — if this ever reads "orgId"
	// instead, the fixture stopped exercising the asymmetry this property
	// needs.
	if len(child.ScopeParams) != 1 || child.ScopeParams[0] != "organizationId" {
		t.Fatalf("child ScopeParams = %v, want [\"organizationId\"] (D5.6: the detail route's own spelling) — test precondition broken", child.ScopeParams)
	}

	roster, err := resRepo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace: %v", err)
	}
	var parentID int64
	for _, r := range roster {
		if r.RouteFamily == testspec.FamilyOrgs {
			parentID = r.ID
		}
	}
	if parentID == 0 {
		t.Fatalf("parent resource %q not found in roster %v", testspec.FamilyOrgs, roster)
	}
	orgs, err := resRepo.List(t.Context(), parentID, resources.ScopeKey(""), resources.ScopeKey(""))
	if err != nil || len(orgs) == 0 {
		t.Fatalf("List orgs: %v, %v", orgs, err)
	}
	orgID := orgs[0].EntityKey

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	collectionPath := "/orgs/" + orgID + "/users"
	recCollection := httptest.NewRecorder()
	p.serveGenerated(recCollection, httptest.NewRequest(http.MethodGet, "http://acme.mock.local"+collectionPath, nil),
		ws, rt, mustMatch(t, rt, http.MethodGet, collectionPath), resources.ScopeKey(""))
	if recCollection.Code != http.StatusOK {
		t.Fatalf("GET %s (collection, spelled orgId): status = %d, want 200 (the STORED scope, not 404 half-service); body=%s",
			collectionPath, recCollection.Code, recCollection.Body)
	}
	collectionRows, ok := decodeAny(t, recCollection.Body.Bytes()).([]any)
	if !ok || len(collectionRows) == 0 {
		t.Fatalf("GET %s (collection) = %s, want a non-empty STORED array", collectionPath, recCollection.Body)
	}
	first, ok := collectionRows[0].(map[string]any)
	if !ok {
		t.Fatalf("GET %s row 0 = %v, want a JSON object", collectionPath, collectionRows[0])
	}

	detailPath := collectionPath + "/" + fmt.Sprint(first["id"])
	recDetail := httptest.NewRecorder()
	p.serveGenerated(recDetail, httptest.NewRequest(http.MethodGet, "http://acme.mock.local"+detailPath, nil),
		ws, rt, mustMatch(t, rt, http.MethodGet, detailPath), resources.ScopeKey(""))
	if recDetail.Code != http.StatusOK {
		t.Fatalf("GET %s (detail, spelled organizationId): status = %d, want 200 (the SAME stored scope); body=%s",
			detailPath, recDetail.Code, recDetail.Body)
	}
	detailRow := decodeAny(t, recDetail.Body.Bytes())
	if fmt.Sprint(detailRow) != fmt.Sprint(any(first)) {
		t.Errorf("GET %s = %v, want the SAME stored row the collection route (spelled differently) already served: %v", detailPath, detailRow, first)
	}
}

// TestResourceBranch_NestedFamily_DepthTwo_BothOuterParamsSpelledDifferently_ServedWhole
// is P17: the scope is read POSITIONALLY, never by parameter name — the
// property D13 records as held by NOTHING in P3e's own acceptance run (its
// own P15), re-observed one level deeper. [testspec.DeepNestingDoc]'s
// depth-2 family (users) spells BOTH of its outer path parameters
// differently between its collection route ("orgId", "teamId") and its
// detail route ("organizationId", "team") — the document's own export
// comment says this double asymmetry is what tells a positional read apart
// from a by-name one AT DEPTH: a fixture differing in only one position
// cannot fail an implementation that reads position 0 positionally and
// position 1 by name, and P3e's own single-parameter fixture
// ([testspec.NestedDerivationDoc]) is exactly that implementation's blind
// spot. Mutation: resolve each outer value by looking its name up in
// res.ScopeParams instead of reading scopeOf's own positional slice.
func TestResourceBranch_NestedFamily_DepthTwo_BothOuterParamsSpelledDifferently_ServedWhole(t *testing.T) {
	db := resourceIntegrationDB(t)
	cfg := resourceIntegrationConfig(t)
	specRepo := specs.NewRepo(db, cfg)
	specID := resourceIntegrationImport(t, specRepo, "deep", string(testspec.DeepNestingDoc()))
	resRepo := resources.NewRepo(db, specRepo, cfg.MaxResponse, 64<<10, 1000)

	settings := domain.Settings{Seed: 1, ListSize: 2}
	wsID := resourceIntegrationWorkspace(t, db, "acme", specID, settings)

	p := New(cfg, nil, specRepo, runtimeTestLogger())
	p.SetResources(resRepo)
	p.SetEntities(resRepo)

	ws := &workspaces.Workspace{ID: wsID, Slug: "acme", SpecID: &specID, Revision: 1, Settings: settings}

	if _, err := resRepo.Confirm(t.Context(), wsID, testspec.FamilyDeepOrgs); err != nil {
		t.Fatalf("confirm root %q: %v", testspec.FamilyDeepOrgs, err)
	}
	ws.Revision++
	if _, err := resRepo.Confirm(t.Context(), wsID, testspec.FamilyDeepTeams); err != nil {
		t.Fatalf("confirm middle %q: %v", testspec.FamilyDeepTeams, err)
	}
	ws.Revision++
	child, err := resRepo.Confirm(t.Context(), wsID, testspec.FamilyDeepUsers)
	if err != nil {
		t.Fatalf("confirm leaf %q: %v", testspec.FamilyDeepUsers, err)
	}
	ws.Revision++
	// Test precondition, not the property itself: ScopeParams stores the
	// DETAIL route's own spelling at EVERY position (D5.6) — if either ever
	// reads the COLLECTION route's spelling instead, the fixture stopped
	// exercising the asymmetry this property needs.
	if len(child.ScopeParams) != 2 || child.ScopeParams[0] != "organizationId" || child.ScopeParams[1] != "team" {
		t.Fatalf(`child ScopeParams = %v, want ["organizationId" "team"] (D5.6: the detail route's own spelling) — test precondition broken`, child.ScopeParams)
	}

	roster, err := resRepo.ForWorkspace(t.Context(), wsID)
	if err != nil {
		t.Fatalf("ForWorkspace: %v", err)
	}
	var orgID, teamID int64
	for _, r := range roster {
		switch r.RouteFamily {
		case testspec.FamilyDeepOrgs:
			orgID = r.ID
		case testspec.FamilyDeepTeams:
			teamID = r.ID
		}
	}
	if orgID == 0 || teamID == 0 {
		t.Fatalf("root/middle resource not found in roster %v", roster)
	}
	orgs, err := resRepo.List(t.Context(), orgID, resources.ScopeKey(""), resources.ScopeKey(""))
	if err != nil || len(orgs) == 0 {
		t.Fatalf("List orgs: %v, %v", orgs, err)
	}
	orgKey := orgs[0].EntityKey

	teams, err := resRepo.List(t.Context(), teamID, resources.ScopeKey(""), resources.EncodeScope([]string{orgKey}))
	if err != nil || len(teams) == 0 {
		t.Fatalf("List teams under org %q: %v, %v", orgKey, teams, err)
	}
	teamKey := teams[0].EntityKey

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	collectionPath := "/orgs/" + orgKey + "/teams/" + teamKey + "/users"
	recCollection := httptest.NewRecorder()
	p.serveGenerated(recCollection, httptest.NewRequest(http.MethodGet, "http://acme.mock.local"+collectionPath, nil),
		ws, rt, mustMatch(t, rt, http.MethodGet, collectionPath), resources.ScopeKey(""))
	if recCollection.Code != http.StatusOK {
		t.Fatalf("GET %s (collection, spelled orgId/teamId): status = %d, want 200 (the STORED scope, not 404 half-service); body=%s",
			collectionPath, recCollection.Code, recCollection.Body)
	}
	collectionRows, ok := decodeAny(t, recCollection.Body.Bytes()).([]any)
	if !ok || len(collectionRows) == 0 {
		t.Fatalf("GET %s (collection) = %s, want a non-empty STORED array", collectionPath, recCollection.Body)
	}
	first, ok := collectionRows[0].(map[string]any)
	if !ok {
		t.Fatalf("GET %s row 0 = %v, want a JSON object", collectionPath, collectionRows[0])
	}

	detailPath := collectionPath + "/" + fmt.Sprint(first["id"])
	recDetail := httptest.NewRecorder()
	p.serveGenerated(recDetail, httptest.NewRequest(http.MethodGet, "http://acme.mock.local"+detailPath, nil),
		ws, rt, mustMatch(t, rt, http.MethodGet, detailPath), resources.ScopeKey(""))
	if recDetail.Code != http.StatusOK {
		t.Fatalf("GET %s (detail, spelled organizationId/team): status = %d, want 200 (the SAME stored scope); body=%s",
			detailPath, recDetail.Code, recDetail.Body)
	}
	detailRow := decodeAny(t, recDetail.Body.Bytes())
	if fmt.Sprint(detailRow) != fmt.Sprint(any(first)) {
		t.Errorf("GET %s = %v, want the SAME stored row the collection route (spelled differently at BOTH positions) already served: %v", detailPath, detailRow, first)
	}
}

// mustMarshalEntities builds the bare-array JSON [resourceServeCollection]
// itself would produce for entities — this file's own comparison baseline,
// built the same way marshalCollection (resource.go) builds it for a
// bare-array wrapper (ArrayKey nil).
func mustMarshalEntities(t *testing.T, entities []resources.Entity) string {
	t.Helper()
	var b strings.Builder
	b.WriteByte('[')
	for i, e := range entities {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(e.Data)
	}
	b.WriteByte(']')
	return b.String()
}
