// runtime_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [runtime], [Plane.runtimeFor] and [Plane.buildRuntime]
// are all unexported, and the whole point of this file is to prove exactly
// what those private pieces do — that a runtime's *gen.Generator was really
// built with Options filled from the WORKSPACE's settings and the PLANE's
// config (not some default), that its variants and table trace back to
// SpecSource verbatim, and that each of buildRuntime's three database calls
// (plus a bad document) fails the way a caller (routes.go's serveRoute) can
// act on. routes_test.go, in the sibling _test package, already covers the
// cache's revision-bump and singleflight behavior end-to-end through
// ServeHTTP; this file does not repeat that, it goes one level deeper.
package mockplane

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/url"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// runtimeTestLogger mirrors plane_test.go's testLogger: this file is a
// separate (white-box, package mockplane) compilation unit from that
// black-box _test package, so it needs its own copy rather than sharing one.
func runtimeTestLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// widgetsDoc is a small, valid OAS 3.0 document with exactly one operation:
// GET /widgets, answering 200 with a top-level JSON array of objects. The
// array shape is deliberate — [gen]'s list contract (DESIGN §9) recognizes a
// bare top-level array as a genuine list route, which is what lets these
// tests observe Options.ListSize and Options.MaxBytes from the OUTSIDE (the
// generated array's length) without reaching into gen's own unexported
// machinery.
const widgetsDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "Widgets", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "operationId": "listWidgets",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": {
                      "id": { "type": "integer" },
                      "name": { "type": "string", "minLength": 30, "maxLength": 30 }
                    }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

// widgetsVariant is the [gen.ResponseVariant] for widgetsDoc's one operation.
// OpRowID must match the router.Route this test's fake SpecSource hands back
// for the same operation — router.Route.OpRowID is the key the runtime's
// variants map is built on.
const widgetsOpRowID = int64(1)

func widgetsVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID:    widgetsOpRowID,
		Selector:   "200",
		HTTPStatus: 200,
		IsDefault:  false,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1widgets/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1widgets/get",
	}
}

func widgetsRoute() router.Route {
	return router.Route{
		OpRowID:        widgetsOpRowID,
		OperationLabel: "listWidgets",
		Method:         "GET",
		Path:           "/widgets",
		CanonicalPath:  "/widgets",
		SourceOrder:    1,
	}
}

// listRequest is the gen.Request a plain GET /widgets (no query string, not
// a detail route) resolves to.
func listRequest() gen.Request {
	return gen.Request{Method: "GET", CanonicalPath: "/widgets", PathParams: map[string]string{}, Query: url.Values{}, Status: 200}
}

// fakeRuntimeSource is this file's own [SpecSource]: every call can be aimed
// at a specific error, and Routes/Variants/Normalized are otherwise plain
// getters over pre-set fields — a smaller, more direct fixture than
// routes_test.go's fakeSpecSource (which this white-box file, being a
// different package, could not reuse anyway).
type fakeRuntimeSource struct {
	normalized    []byte
	normalizedErr error
	routes        []router.Route
	routesErr     error
	variants      map[int64][]gen.ResponseVariant
	variantsErr   error

	calls int // counts Normalized calls only — enough to prove the cache path
}

func (f *fakeRuntimeSource) Normalized(_ context.Context, _ int64) ([]byte, error) {
	f.calls++
	if f.normalizedErr != nil {
		return nil, f.normalizedErr
	}
	return f.normalized, nil
}

func (f *fakeRuntimeSource) Routes(_ context.Context, _ int64) ([]router.Route, error) {
	if f.routesErr != nil {
		return nil, f.routesErr
	}
	return f.routes, nil
}

func (f *fakeRuntimeSource) Variants(_ context.Context, _ int64) (map[int64][]gen.ResponseVariant, error) {
	if f.variantsErr != nil {
		return nil, f.variantsErr
	}
	return f.variants, nil
}

// widgetsSource returns a fakeRuntimeSource wired to widgetsDoc, with no
// injected errors — the happy-path fixture every test below starts from and
// mutates a single field of.
func widgetsSource() *fakeRuntimeSource {
	return &fakeRuntimeSource{
		normalized: []byte(widgetsDoc),
		routes:     []router.Route{widgetsRoute()},
		variants:   map[int64][]gen.ResponseVariant{widgetsOpRowID: {widgetsVariant()}},
	}
}

// runtimeTestConfig builds a config.Config satisfying the same "hand-built
// test config" trap list plane_test.go's own testConfig documents, letting
// each test pick its own MaxResponse/RuntimeCache — the two fields this
// file's tests actually vary.
func runtimeTestConfig(maxResponse int64, runtimeCache int) *config.Config {
	return &config.Config{
		BaseDomain:     "mock.local",
		AdminHost:      "mocker.local",
		Routing:        config.RoutingHost,
		ReservedPrefix: "/__mocker",
		AuthMode:       config.AuthShared,
		MaxBody:        10 << 20,
		MaxResponse:    maxResponse,
		RuntimeCache:   runtimeCache,
		Dev:            true,
	}
}

func widgetsWorkspace(id int64, settings domain.Settings) *workspaces.Workspace {
	specID := int64(1)
	return &workspaces.Workspace{ID: id, Slug: "alex", SpecID: &specID, Revision: 1, Settings: settings}
}

// TestBuildRuntime_PopulatesEveryField proves each of runtime's four fields
// traces back to exactly the source [Plane.buildRuntime]'s own doc comment
// promises: the table matches the routes SpecSource served, the variants map
// is SpecSource's own map (same key, same variant), and settings is the
// workspace's settings snapshot, unmodified.
func TestBuildRuntime_PopulatesEveryField(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.ListSize = 7
	ws := widgetsWorkspace(1, settings)
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	if rt.table == nil {
		t.Fatal("table is nil")
	}
	m, ok := rt.table.Match("GET", []string{"widgets"})
	if !ok {
		t.Fatal("table has no match for GET /widgets, want the one route SpecSource served")
	}
	if m.Route.OpRowID != widgetsOpRowID {
		t.Errorf("matched OpRowID = %d, want %d", m.Route.OpRowID, widgetsOpRowID)
	}

	if rt.gen == nil {
		t.Fatal("gen is nil")
	}

	vs, ok := rt.variants[widgetsOpRowID]
	if !ok || len(vs) != 1 {
		t.Fatalf("variants[%d] = %v, want exactly the one variant SpecSource served", widgetsOpRowID, vs)
	}
	if vs[0] != widgetsVariant() {
		t.Errorf("variants[%d][0] = %+v, want %+v", widgetsOpRowID, vs[0], widgetsVariant())
	}

	if rt.settings.ListSize != 7 {
		t.Errorf("settings.ListSize = %d, want 7 (the workspace's own settings snapshot)", rt.settings.ListSize)
	}
}

// TestBuildRuntime_ListSizeThreadsFromSettings is the direct, external proof
// that gen.Options.ListSize comes from ws.Settings.ListSize (buildRuntime's
// doc comment, step 4): the SAME document and SpecSource, two workspaces
// differing ONLY in Settings.ListSize, must generate a /widgets array of
// exactly that length — there being no query string and no minItems/maxItems
// on the schema, nothing else could be deciding the count.
func TestBuildRuntime_ListSizeThreadsFromSettings(t *testing.T) {
	for _, listSize := range []int{3, 8} {
		settings := domain.DefaultSettings()
		settings.ListSize = listSize
		ws := widgetsWorkspace(1, settings)
		// A generous MaxResponse so the byte budget is never what's limiting
		// the array — this test is about ListSize alone.
		p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())

		rt, err := p.runtimeFor(t.Context(), ws)
		if err != nil {
			t.Fatalf("listSize=%d: runtimeFor: %v", listSize, err)
		}

		body, err := rt.gen.Body(widgetsVariant(), listRequest())
		if err != nil {
			t.Fatalf("listSize=%d: Body: %v", listSize, err)
		}
		var items []any
		if err := json.Unmarshal(body, &items); err != nil {
			t.Fatalf("listSize=%d: decode body %s: %v", listSize, body, err)
		}
		if len(items) != listSize {
			t.Errorf("listSize=%d: generated array length = %d, want %d (ws.Settings.ListSize never reached gen.Options)",
				listSize, len(items), listSize)
		}
	}
}

// TestBuildRuntime_MaxBytesThreadsFromConfig is the same kind of external,
// A/B proof for gen.Options.MaxBytes <- cfg.MaxResponse (buildRuntime's doc
// comment, step 4): the SAME document, SpecSource and workspace settings
// (ListSize large enough that the byte budget, not the list size, is what
// binds), two Planes differing ONLY in cfg.MaxResponse, must generate
// arrays of different lengths — a small budget must visibly truncate below
// what a large one allows.
func TestBuildRuntime_MaxBytesThreadsFromConfig(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.ListSize = 50 // want plenty of room for the budget to be the binding constraint
	ws := widgetsWorkspace(1, settings)

	lenFor := func(maxResponse int64) int {
		t.Helper()
		p := New(runtimeTestConfig(maxResponse, 32), nil, widgetsSource(), runtimeTestLogger())
		rt, err := p.runtimeFor(t.Context(), ws)
		if err != nil {
			t.Fatalf("maxResponse=%d: runtimeFor: %v", maxResponse, err)
		}
		body, err := rt.gen.Body(widgetsVariant(), listRequest())
		if err != nil {
			t.Fatalf("maxResponse=%d: Body: %v", maxResponse, err)
		}
		var items []any
		if err := json.Unmarshal(body, &items); err != nil {
			t.Fatalf("maxResponse=%d: decode body %s: %v", maxResponse, body, err)
		}
		return len(items)
	}

	small := lenFor(120) // a couple of ~55-byte items, not fifty
	large := lenFor(4 << 20)

	if large != 50 {
		t.Fatalf("large budget: generated array length = %d, want 50 (the unconstrained ListSize)", large)
	}
	if small >= large {
		t.Errorf("small budget (120 bytes): generated array length = %d, want fewer than the unconstrained %d — cfg.MaxResponse never reached gen.Options.MaxBytes",
			small, large)
	}
}

// TestBuildRuntime_SeedThreadsFromSettings proves gen.Options.Seed comes
// from ws.Settings.Seed: two workspaces identical in every other field, but
// with a different Seed, must generate DIFFERENT bytes for the exact same
// request — DESIGN §9's whole determinism story starts at this one field,
// and if it never reached gen.Options every workspace would render
// identically regardless of its own configured seed.
func TestBuildRuntime_SeedThreadsFromSettings(t *testing.T) {
	bodyFor := func(seed int64) []byte {
		t.Helper()
		settings := domain.DefaultSettings()
		settings.Seed = seed
		ws := widgetsWorkspace(1, settings)
		p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
		rt, err := p.runtimeFor(t.Context(), ws)
		if err != nil {
			t.Fatalf("seed=%d: runtimeFor: %v", seed, err)
		}
		body, err := rt.gen.Body(widgetsVariant(), listRequest())
		if err != nil {
			t.Fatalf("seed=%d: Body: %v", seed, err)
		}
		return body
	}

	b1 := bodyFor(1)
	b2 := bodyFor(2)
	if string(b1) == string(b2) {
		t.Errorf("bodies for Settings.Seed 1 and 2 are byte-identical, want different (ws.Settings.Seed never reached gen.Options.Seed): %s", b1)
	}
}

// TestRuntimeFor_ErrorFromNormalized proves a SpecSource.Normalized failure
// surfaces as a runtimeFor error, not a panic or a silently empty runtime —
// the error routes.go's serveRoute logs and turns into a 500.
func TestRuntimeFor_ErrorFromNormalized(t *testing.T) {
	src := widgetsSource()
	injected := errors.New("db down")
	src.normalizedErr = injected
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())

	_, err := p.runtimeFor(t.Context(), widgetsWorkspace(1, domain.DefaultSettings()))
	if !errors.Is(err, injected) {
		t.Errorf("runtimeFor error = %v, want it to wrap %v", err, injected)
	}
}

// TestRuntimeFor_ErrorFromMalformedNormalizedDocument proves a stored
// document that openapi.Load itself refuses (never valid in practice, since
// Import already validated it — but buildRuntime must not trust that
// invariant blindly) still surfaces as an error rather than a panic.
func TestRuntimeFor_ErrorFromMalformedNormalizedDocument(t *testing.T) {
	src := widgetsSource()
	src.normalized = []byte(`{"not":"a document"}`) // valid JSON, no openapi/swagger field
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())

	if _, err := p.runtimeFor(t.Context(), widgetsWorkspace(1, domain.DefaultSettings())); err == nil {
		t.Error("runtimeFor error = nil, want an error for a document Load itself refuses")
	}
}

// TestRuntimeFor_ErrorFromVariants mirrors TestRuntimeFor_ErrorFromNormalized
// for the Variants call.
func TestRuntimeFor_ErrorFromVariants(t *testing.T) {
	src := widgetsSource()
	injected := errors.New("db down")
	src.variantsErr = injected
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())

	_, err := p.runtimeFor(t.Context(), widgetsWorkspace(1, domain.DefaultSettings()))
	if !errors.Is(err, injected) {
		t.Errorf("runtimeFor error = %v, want it to wrap %v", err, injected)
	}
}

// TestRuntimeFor_ErrorFromRoutes mirrors TestRuntimeFor_ErrorFromNormalized
// for the Routes call — the same one P1a's routeTable already made.
func TestRuntimeFor_ErrorFromRoutes(t *testing.T) {
	src := widgetsSource()
	injected := errors.New("db down")
	src.routesErr = injected
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())

	_, err := p.runtimeFor(t.Context(), widgetsWorkspace(1, domain.DefaultSettings()))
	if !errors.Is(err, injected) {
		t.Errorf("runtimeFor error = %v, want it to wrap %v", err, injected)
	}
}

// TestRuntimeFor_CachesAndRebuildsOnRevisionBump is runtime_test.go's own,
// direct (not through ServeHTTP) proof that runtimeFor goes through the same
// cache routes_test.go exercises end-to-end: calling it twice for the same
// (workspace, revision) must hit the cache — same *runtime pointer back,
// Normalized called once — and bumping Revision must force a rebuild.
func TestRuntimeFor_CachesAndRebuildsOnRevisionBump(t *testing.T) {
	src := widgetsSource()
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())
	ws := widgetsWorkspace(1, domain.DefaultSettings())

	first, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("first runtimeFor: %v", err)
	}
	second, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("second runtimeFor: %v", err)
	}
	if first != second {
		t.Error("runtimeFor returned a different *runtime for an unchanged (workspace, revision), want the cached one")
	}
	if src.calls != 1 {
		t.Errorf("Normalized called %d times across two requests at the same revision, want 1 (cache hit)", src.calls)
	}

	ws.Revision++
	third, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("post-bump runtimeFor: %v", err)
	}
	if third == second {
		t.Error("runtimeFor returned the SAME *runtime after a revision bump, want a rebuild")
	}
	if src.calls != 2 {
		t.Errorf("Normalized called %d times after a revision bump, want 2 (bump must force a rebuild)", src.calls)
	}
}

// --- overrides: buildRuntime's new (P1c) load, and the lookup helpers it
// feeds (overrides.go) --------------------------------------------------

// fakeOverrideSource is this file's own [OverrideSource]: a pre-set map
// returned verbatim, with a call counter and an injectable error — the same
// shape as fakeRuntimeSource's own methods, for the same reason (a smaller,
// more direct fixture than a real overrides.Repo needs no database).
//
// delay, if non-zero, is slept before returning — mirroring routes_test.go's
// fakeSpecSource.delay: without it, a build over this tiny in-memory fixture
// finishes fast enough that concurrent goroutines can miss piling onto the
// SAME singleflight call (routeCache.getOrBuild's own "check cache, then
// join singleflight" is check-then-act — a goroutine that checks between two
// back-to-back builds becomes a second, harmless-but-extra leader instead of
// a follower). Widening the window is what routes_test.go's own
// TestRouteCache_ColdBuildIsSingleFlighted relies on for the same reason.
type fakeOverrideSource struct {
	rows  map[string]*overrides.Row
	err   error
	delay time.Duration

	calls int
}

func (f *fakeOverrideSource) ForWorkspace(_ context.Context, _ int64) (map[string]*overrides.Row, error) {
	f.calls++
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	if f.err != nil {
		return nil, f.err
	}
	return f.rows, nil
}

// widgetsOverrideRow is a valid op_overrides row bound to widgetsRoute's
// operation: one recipe at "id", on the 200 response.
func widgetsOverrideRow() *overrides.Row {
	return &overrides.Row{
		WorkspaceID: 1,
		Method:      "GET",
		Path:        "/widgets",
		OverrideOn:  true,
		Responses: map[string]overrides.Variant{
			"200": {
				Mode: "generated",
				Recipes: map[string]recipes.Recipe{
					"id": {Kind: recipes.KindConst, Data: json.RawMessage(`42`)},
				},
			},
		},
	}
}

// profileDoc is a second, small OAS document — a plain OBJECT response, not
// a list — used only by TestBuildRuntime_IdentityAndAuthThreadIntoGenerator
// so that test can bind a recipe directly at the response root ("name")
// without also having to reproduce gen's own list-item data-path grammar
// (arrName/recipePrefix, an internal/gen concern this package does not own)
// just to prove one fact: that ws.Settings.Identity reaches gen.Options.
const profileDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "Profile", "version": "1.0.0" },
  "paths": {
    "/profile": {
      "get": {
        "operationId": "getProfile",
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": {
                    "name": { "type": "string" }
                  }
                }
              }
            }
          }
        }
      }
    }
  }
}`

const profileOpRowID = int64(1)

func profileVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID:    profileOpRowID,
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1profile/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1profile/get",
	}
}

func profileRoute() router.Route {
	return router.Route{
		OpRowID:        profileOpRowID,
		OperationLabel: "getProfile",
		Method:         "GET",
		Path:           "/profile",
		CanonicalPath:  "/profile",
		SourceOrder:    1,
	}
}

func profileSource() *fakeRuntimeSource {
	return &fakeRuntimeSource{
		normalized: []byte(profileDoc),
		routes:     []router.Route{profileRoute()},
		variants:   map[int64][]gen.ResponseVariant{profileOpRowID: {profileVariant()}},
	}
}

// TestBuildRuntime_LoadsOverridesAndTheLookupFindsTheRow proves the load
// side of step 7 (buildRuntime's doc comment): a row ForWorkspace served for
// GET /widgets is reachable through lookupOverride keyed off the MATCHED
// route, and is the exact same *overrides.Row — no copy, no re-decode.
func TestBuildRuntime_LoadsOverridesAndTheLookupFindsTheRow(t *testing.T) {
	ws := widgetsWorkspace(1, domain.DefaultSettings())
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	row := widgetsOverrideRow()
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey("GET", "/widgets"): row,
	}})

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	route := widgetsRoute()
	got, ok := rt.lookupOverride(&route)
	if !ok {
		t.Fatal("lookupOverride found no row for GET /widgets, want the one ForWorkspace served")
	}
	if got != row {
		t.Errorf("lookupOverride returned a different *overrides.Row than ForWorkspace served: got %+v, want %+v", got, row)
	}
}

// TestLookupOverride_KeysOnPathNotCanonicalPath is the direct, unit-level
// proof of overrides.go's central claim: two routes that share a
// CanonicalPath ("/widgets/{}") but differ in their literal, ORIGINAL Path
// ("/widgets/{id}" vs "/widgets/{name}") must NOT collide. A row stored
// under one must be invisible to a lookup for the other — the exact
// "a re-imported spec whose parameter got renamed must not silently
// reattach a stale row to the new operation" requirement.
func TestLookupOverride_KeysOnPathNotCanonicalPath(t *testing.T) {
	row := &overrides.Row{Method: "GET", Path: "/widgets/{id}", OverrideOn: true, Responses: map[string]overrides.Variant{}}
	rt := &runtime{overrides: map[string]*overrides.Row{
		overrides.OpKey("GET", "/widgets/{id}"): row,
	}}

	renamed := router.Route{Method: "GET", Path: "/widgets/{name}", CanonicalPath: "/widgets/{}"}
	if _, ok := rt.lookupOverride(&renamed); ok {
		t.Error("lookupOverride matched a route whose CanonicalPath collides but whose literal Path differs — a stale row must go inert, not silently reattach")
	}

	same := router.Route{Method: "GET", Path: "/widgets/{id}", CanonicalPath: "/widgets/{}"}
	got, ok := rt.lookupOverride(&same)
	if !ok || got != row {
		t.Fatalf("lookupOverride(matching Path) = (%+v, %v), want the stored row", got, ok)
	}
}

// TestBuildRuntime_OverrideForUnknownOperationIsInert is the build-level
// proof: a row bound to a path no route in the workspace answers (as a
// re-import that dropped an operation would leave behind) must not fail
// buildRuntime and must not drop any OTHER row out of the map.
func TestBuildRuntime_OverrideForUnknownOperationIsInert(t *testing.T) {
	ws := widgetsWorkspace(1, domain.DefaultSettings())
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	staleRow := &overrides.Row{Method: "GET", Path: "/gone", OverrideOn: true, Responses: map[string]overrides.Variant{}}
	validRow := widgetsOverrideRow()
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey("GET", "/gone"):    staleRow,
		overrides.OpKey("GET", "/widgets"): validRow,
	}})

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v, want a stale override row (no matching route) to build cleanly", err)
	}

	route := widgetsRoute()
	if got, ok := rt.lookupOverride(&route); !ok || got != validRow {
		t.Error("the valid row went missing alongside the stale one — one unreachable row must never drop the whole map")
	}
}

// TestBuildRuntime_NilOverrideSourceLeavesRuntimeUnaffected is HARD RULE 5
// of the task ("the plane must keep working with a nil OverrideSource")
// checked directly against the lookup helpers a nil-Plane's runtime hands
// back, not just "buildRuntime doesn't panic".
func TestBuildRuntime_NilOverrideSourceLeavesRuntimeUnaffected(t *testing.T) {
	ws := widgetsWorkspace(1, domain.DefaultSettings())
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger()) // SetOverrides never called

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}
	route := widgetsRoute()
	if _, ok := rt.lookupOverride(&route); ok {
		t.Error("lookupOverride found a row with no OverrideSource ever configured, want ok=false")
	}
	if _, ok := rt.lookupRecipes(&route, "200"); ok {
		t.Error("lookupRecipes found a set with no OverrideSource ever configured, want ok=false")
	}
}

// TestRuntimeFor_ErrorFromOverrides mirrors the existing
// TestRuntimeFor_ErrorFromNormalized/Variants/Routes trio for the new
// ForWorkspace call: a database error there must surface as a runtimeFor
// error (which serveRoute logs and turns into a 500), never a panic or a
// silently empty override set.
func TestRuntimeFor_ErrorFromOverrides(t *testing.T) {
	src := widgetsSource()
	p := New(runtimeTestConfig(4<<20, 32), nil, src, runtimeTestLogger())
	injected := errors.New("db down")
	p.SetOverrides(&fakeOverrideSource{err: injected})

	_, err := p.runtimeFor(t.Context(), widgetsWorkspace(1, domain.DefaultSettings()))
	if !errors.Is(err, injected) {
		t.Errorf("runtimeFor error = %v, want it to wrap %v", err, injected)
	}
}

// TestBuildRuntime_CompilesRecipesPerStatus proves step 2b's compile-at-build
// contract: the recipe bound at "200" in widgetsOverrideRow comes back
// through lookupRecipes keyed by (route, "200"), and is absent for any
// status the row never bound.
func TestBuildRuntime_CompilesRecipesPerStatus(t *testing.T) {
	ws := widgetsWorkspace(1, domain.DefaultSettings())
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey("GET", "/widgets"): widgetsOverrideRow(),
	}})

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	route := widgetsRoute()
	set, ok := rt.lookupRecipes(&route, "200")
	if !ok {
		t.Fatal("lookupRecipes found nothing for GET /widgets status 200, want the compiled recipe")
	}
	if set.Len() != 1 {
		t.Errorf("compiled set has %d recipes, want 1", set.Len())
	}
	if _, ok := set.Lookup("id"); !ok {
		t.Error(`compiled set has no recipe at "id", want the one bound in widgetsOverrideRow`)
	}

	if _, ok := rt.lookupRecipes(&route, "404"); ok {
		t.Error("lookupRecipes found a set for status 404, which widgetsOverrideRow never bound")
	}
}

// TestBuildRuntime_InvalidStoredRecipeIsLoggedAndSkipped proves buildRecipeSets'
// failure mode: a Recipes map that fails recipes.Compile (simulating a row
// that reached the column some other way than Put/PutMany's own validation)
// must not fail buildRuntime, must not take the row's OTHER data down with
// it, and must simply be absent from lookupRecipes.
func TestBuildRuntime_InvalidStoredRecipeIsLoggedAndSkipped(t *testing.T) {
	ws := widgetsWorkspace(1, domain.DefaultSettings())
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	badRow := &overrides.Row{
		Method:     "GET",
		Path:       "/widgets",
		OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {
				Mode: "generated",
				Recipes: map[string]recipes.Recipe{
					"id": {Kind: recipes.Kind("not-a-real-kind")},
				},
			},
		},
	}
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey("GET", "/widgets"): badRow,
	}})

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v, want a malformed stored recipe to be logged, not fail the whole build", err)
	}

	route := widgetsRoute()
	if _, ok := rt.lookupRecipes(&route, "200"); ok {
		t.Error("lookupRecipes found a set compiled from an invalid recipe, want it declined")
	}
	// The row itself must still be reachable — only its recipes were bad.
	if _, ok := rt.lookupOverride(&route); !ok {
		t.Error("an invalid recipe took the whole override row down with it, want only the recipe set to be skipped")
	}
}

// TestBuildRuntime_IdentityAndAuthThreadIntoGenerator proves buildRuntime's
// doc comment step 4 addition: ws.Settings.Identity reaches gen.Options.Identity.
// Nothing else in profileDoc could produce "Ada" for a field the document
// itself declares no example, const or default for — an identity recipe is
// the only value source that even knows the name exists, and it only knows
// it through gen.Options.
func TestBuildRuntime_IdentityAndAuthThreadIntoGenerator(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.Identity = domain.Identity{ID: "u-1", Name: "Ada"}
	ws := widgetsWorkspace(1, settings)
	p := New(runtimeTestConfig(4<<20, 32), nil, profileSource(), runtimeTestLogger())

	row := &overrides.Row{
		Method:     "GET",
		Path:       "/profile",
		OverrideOn: true,
		Responses: map[string]overrides.Variant{
			"200": {
				Mode: "generated",
				Recipes: map[string]recipes.Recipe{
					"name": {Kind: recipes.KindIdentity, Field: "name"},
				},
			},
		},
	}
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey("GET", "/profile"): row,
	}})

	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}
	route := profileRoute()
	set, ok := rt.lookupRecipes(&route, "200")
	if !ok {
		t.Fatal("lookupRecipes found nothing, want the compiled identity recipe")
	}

	req := gen.Request{
		Method: "GET", CanonicalPath: "/profile", PathParams: map[string]string{},
		Query: url.Values{}, Status: 200, Recipes: set,
	}
	body, err := rt.gen.Body(profileVariant(), req)
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	if got["name"] != "Ada" {
		t.Errorf("name = %v, want %q (ws.Settings.Identity never reached gen.Options.Identity)", got["name"], "Ada")
	}
}

// TestRuntimeFor_ConcurrentBuildLoadsOverridesOnce is the -race regression
// for the overrides load riding along the SAME singleflight the route/
// variant/document load already goes through: many goroutines racing a cold
// (workspace, revision) must trigger exactly one ForWorkspace call, and
// every one of them must still see the row once the build completes.
func TestRuntimeFor_ConcurrentBuildLoadsOverridesOnce(t *testing.T) {
	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	overrideSrc := &fakeOverrideSource{
		rows: map[string]*overrides.Row{
			overrides.OpKey("GET", "/widgets"): widgetsOverrideRow(),
		},
		delay: 20 * time.Millisecond,
	}
	p.SetOverrides(overrideSrc)
	ws := widgetsWorkspace(1, domain.DefaultSettings())

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			rt, err := p.runtimeFor(t.Context(), ws)
			if err != nil {
				t.Errorf("runtimeFor: %v", err)
				return
			}
			route := widgetsRoute()
			if _, ok := rt.lookupOverride(&route); !ok {
				t.Error("lookupOverride found nothing under concurrent resolution")
			}
		}()
	}
	wg.Wait()

	if overrideSrc.calls != 1 {
		t.Errorf("ForWorkspace called %d times by %d concurrent resolutions of the same (workspace, revision), want 1 (single-flighted)",
			overrideSrc.calls, goroutines)
	}
}
