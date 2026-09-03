// scenario_test.go is a WHITE-BOX test file (package mockplane, like
// runtime_test.go beside it, whose fixtures it reuses): [composeScenarioLayer]
// and [Plane.buildRuntime] are both unexported, and this file exists to prove
// the two facts the live smoke suite structurally cannot.
//
// The composition is a PURE function precisely so it can be tested here, one
// call at a time, without a spec document or a cache key — every case below
// names the wrong implementation it fails, because a case that cannot is
// decoration.
package mockplane

import (
	"context"
	"encoding/json"
	"errors"
	"maps"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// --- fixtures ---------------------------------------------------------------

// fakeScenarioSource is this file's [ScenarioSource]: pre-set lookups, an
// injectable Get error (the "a stored snapshot went bad" path), and a record
// of every SetActive argument. No database, exactly like fakeOverrideSource
// and fakeRuntimeSource in runtime_test.go.
type fakeScenarioSource struct {
	byID   map[int64]*scenarios.Scenario
	byName map[string]*scenarios.Scenario
	getErr error

	revision  int64
	setActive []*int64
}

func (f *fakeScenarioSource) Get(_ context.Context, _, scenarioID int64) (*scenarios.Scenario, error) {
	if f.getErr != nil {
		return nil, f.getErr
	}
	sc, ok := f.byID[scenarioID]
	if !ok {
		return nil, scenarios.ErrNotFound
	}
	return sc, nil
}

func (f *fakeScenarioSource) ByName(_ context.Context, _ int64, name string) (*scenarios.Scenario, error) {
	sc, ok := f.byName[name]
	if !ok {
		return nil, scenarios.ErrNotFound
	}
	return sc, nil
}

func (f *fakeScenarioSource) SetActive(_ context.Context, _ int64, scenarioID *int64) (int64, error) {
	f.setActive = append(f.setActive, scenarioID)
	f.revision++
	return f.revision, nil
}

// scenarioWorkspace is widgetsWorkspace with an ACTIVE scenario — the one
// thing that makes buildRuntime consult a ScenarioSource at all.
func scenarioWorkspace(id int64, settings domain.Settings, scenarioID int64) *workspaces.Workspace {
	ws := widgetsWorkspace(id, settings)
	ws.ScenarioID = &scenarioID
	return ws
}

// snapshotOf builds the [bundle.Bundle] a saved scenario decodes to, through
// [bundle.New] rather than a struct literal: New is what the create path
// actually calls, so a change to how it fills the envelope shows up here
// instead of being papered over by a hand-built value.
func snapshotOf(settings domain.Settings, entries ...bundle.OverrideEntry) bundle.Bundle {
	return bundle.New("snapshot", settings, bundle.SpecRef{Name: "widgets", Hash: "deadbeef"}, entries)
}

// entry is one bundle override entry with an ActiveStatus marker: which
// LAYER a composed row came from is otherwise invisible, and a status is the
// smallest field that carries the answer.
func entry(method, path string, overrideOn bool, activeStatus int) bundle.OverrideEntry {
	return bundle.OverrideEntry{
		Method:       method,
		Path:         path,
		OverrideOn:   overrideOn,
		ActiveStatus: &activeStatus,
		Responses:    map[string]overrides.Variant{},
	}
}

// wsRow is the workspace-layer counterpart of entry: the same marker in the
// shape [OverrideSource.ForWorkspace] hands buildRuntime.
func wsRow(method, path string, overrideOn bool, activeStatus int) *overrides.Row {
	return &overrides.Row{
		Method:       method,
		Path:         path,
		OverrideOn:   overrideOn,
		ActiveStatus: &activeStatus,
		Responses:    map[string]overrides.Variant{},
	}
}

// --- the composition, per key -----------------------------------------------

// TestComposeScenarioLayer_MembershipDecidesTheLayer is A1/A2 in test form:
// the composed map is built BY KEY, and a row's OverrideOn flag decides its
// EFFECT, never whether it is in the map.
//
// The fourth case is the load-bearing one and the one a gate round actually
// caught: a scenario row with OverrideOn=false must STAY in the composed map,
// masking the workspace's own active edit, because respond.go computes
// `overrideActive := hasRow && row.OverrideOn` and gates every branch on that
// — so a present-but-off row means "under this scenario, answer as the SPEC
// says", a third state neither layer can express alone. An implementation
// that filtered OverrideOn=false entries out on the way in passes the first
// three cases and makes DESIGN §12's «пустой список» / «всё падает»
// scenarios unexpressible over a workspace that has edits of its own.
func TestComposeScenarioLayer_MembershipDecidesTheLayer(t *testing.T) {
	const (
		fromWorkspace = 418
		fromScenario  = 503
	)
	widgets := overrides.OpKey("GET", "/widgets")
	things := overrides.OpKey("GET", "/things")

	tests := []struct {
		name         string
		wsRows       map[string]*overrides.Row
		snapshot     []bundle.OverrideEntry
		key          string
		wantPresent  bool
		wantStatus   int  // ActiveStatus of the composed row: which layer won
		wantOverride bool // composed row's OverrideOn
	}{
		{
			name:         "a key only the workspace has falls through",
			wsRows:       map[string]*overrides.Row{widgets: wsRow("GET", "/widgets", true, fromWorkspace)},
			snapshot:     []bundle.OverrideEntry{entry("GET", "/things", true, fromScenario)},
			key:          widgets,
			wantPresent:  true,
			wantStatus:   fromWorkspace,
			wantOverride: true,
		},
		{
			name:         "a key only the scenario has appears",
			wsRows:       map[string]*overrides.Row{widgets: wsRow("GET", "/widgets", true, fromWorkspace)},
			snapshot:     []bundle.OverrideEntry{entry("GET", "/things", true, fromScenario)},
			key:          things,
			wantPresent:  true,
			wantStatus:   fromScenario,
			wantOverride: true,
		},
		{
			name:         "a key both layers have takes the scenario's",
			wsRows:       map[string]*overrides.Row{widgets: wsRow("GET", "/widgets", true, fromWorkspace)},
			snapshot:     []bundle.OverrideEntry{entry("GET", "/widgets", true, fromScenario)},
			key:          widgets,
			wantPresent:  true,
			wantStatus:   fromScenario,
			wantOverride: true,
		},
		{
			name:         "a scenario row with OverrideOn=false masks the workspace's active edit",
			wsRows:       map[string]*overrides.Row{widgets: wsRow("GET", "/widgets", true, fromWorkspace)},
			snapshot:     []bundle.OverrideEntry{entry("GET", "/widgets", false, fromScenario)},
			key:          widgets,
			wantPresent:  true,
			wantStatus:   fromScenario,
			wantOverride: false,
		},
		{
			name:        "a key neither layer has is absent",
			wsRows:      map[string]*overrides.Row{widgets: wsRow("GET", "/widgets", true, fromWorkspace)},
			snapshot:    []bundle.OverrideEntry{entry("GET", "/widgets", true, fromScenario)},
			key:         things,
			wantPresent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, composed := composeScenarioLayer(
				domain.DefaultSettings(), tt.wsRows, snapshotOf(domain.DefaultSettings(), tt.snapshot...))

			row, ok := composed[tt.key]
			if ok != tt.wantPresent {
				t.Fatalf("composed[%q] present = %v, want %v (membership decides the layer)", tt.key, ok, tt.wantPresent)
			}
			if !tt.wantPresent {
				return
			}
			if row.ActiveStatus == nil {
				t.Errorf("composed[%q].ActiveStatus is nil, want %d — the marker that says which layer the row came from is gone",
					tt.key, tt.wantStatus)
			} else if *row.ActiveStatus != tt.wantStatus {
				t.Errorf("composed[%q].ActiveStatus = %d, want %d — the row came from the wrong layer",
					tt.key, *row.ActiveStatus, tt.wantStatus)
			}
			if row.OverrideOn != tt.wantOverride {
				t.Errorf("composed[%q].OverrideOn = %v, want %v — the FLAG decides the effect, and respond.go reads it as hasRow && row.OverrideOn",
					tt.key, row.OverrideOn, tt.wantOverride)
			}
		})
	}
}

// TestComposeScenarioLayer_DoesNotMutateItsInputs is the purity half of A5,
// and it is not a formality: buildRuntime hands this function the very map
// [OverrideSource.ForWorkspace] returned — a source is free to cache and
// reuse it — and the runtime built from the result is shared unlocked across
// concurrent requests. A composition that wrote into either input would be a
// data race and a poisoned upstream cache at once, and neither shows up in a
// single-goroutine happy path.
func TestComposeScenarioLayer_DoesNotMutateItsInputs(t *testing.T) {
	key := overrides.OpKey("GET", "/widgets")
	row := wsRow("GET", "/widgets", true, 418)
	wsRows := map[string]*overrides.Row{key: row}
	before := maps.Clone(wsRows)

	wsSettings := domain.DefaultSettings()
	wsSettings.ListSize = 3
	snapSettings := domain.DefaultSettings()
	snapSettings.ListSize = 8
	snap := snapshotOf(snapSettings, entry("GET", "/widgets", false, 503))

	composed, composedRows := composeScenarioLayer(wsSettings, wsRows, snap)

	if len(wsRows) != len(before) || wsRows[key] != row {
		t.Errorf("the workspace's own map was modified: %v, want it untouched", wsRows)
	}
	if row.OverrideOn != true || *row.ActiveStatus != 418 {
		t.Errorf("the workspace's own *overrides.Row was modified in place: %+v", row)
	}
	if wsSettings.ListSize != 3 {
		t.Errorf("the workspace's settings were modified: ListSize = %d, want 3", wsSettings.ListSize)
	}
	if snap.Workspace.Settings.ListSize != 8 || snap.Overrides[0].OverrideOn {
		t.Errorf("the snapshot was modified: %+v", snap)
	}
	if composed.ListSize != 8 {
		t.Errorf("composed ListSize = %d, want the scenario's 8", composed.ListSize)
	}
	if composedRows[key] == row {
		t.Error("the composed map reused the workspace's *overrides.Row for a key the scenario also carries, want the scenario's own row")
	}
}

// --- the composition, settings ----------------------------------------------

// TestComposeScenarioLayer_Settings is A3/A3a: a setting is
// scenario-effective if and only if it is read from INSIDE the runtime, plus
// one deliberate exception.
//
// The three "from the workspace" rows are the ones no live observation can
// prove (§G): cors and notFoundBody are read outside any runtime, so a
// composed value there is unreachable by construction, and basePath is read
// by the route table from ws.Settings directly. An implementation that wrote
// all three from the scenario passes every curl in the smoke suite while
// making runtime.settings' own doc comment false for three fields — and the
// next reader to take the obvious value out of rt.settings gets the wrong
// one, silently. This table is the only thing standing between that and
// nobody noticing.
func TestComposeScenarioLayer_Settings(t *testing.T) {
	envelopeWS, envelopeScenario := "wsData", "scenarioData"

	wsSettings := domain.DefaultSettings()
	wsSettings.Seed = 11
	wsSettings.BasePath = "/ws-api"
	wsSettings.ListSize = 3
	wsSettings.NullRate = 0.1
	wsSettings.Envelope = &envelopeWS
	wsSettings.DelayMs = 10
	wsSettings.Identity = domain.Identity{ID: 1, Name: "Workspace User"}
	wsSettings.Auth = domain.AuthSettings{JWTTTLSec: 60, Alg: "HS256", SigningKey: "ws-key"}
	wsSettings.CORS = domain.CORSSettings{Mode: domain.CORSReflect, Credentials: true}
	wsSettings.NotFoundBody = json.RawMessage(`{"from":"workspace"}`)

	snapSettings := domain.DefaultSettings()
	snapSettings.Seed = 99
	snapSettings.BasePath = "/scenario-api"
	snapSettings.ListSize = 8
	snapSettings.NullRate = 0.9
	snapSettings.Envelope = &envelopeScenario
	snapSettings.DelayMs = 90
	snapSettings.Identity = domain.Identity{ID: 2, Name: "Scenario User"}
	snapSettings.Auth = domain.AuthSettings{JWTTTLSec: 600, Alg: "HS512", SigningKey: "scenario-key"}
	snapSettings.CORS = domain.CORSSettings{Mode: domain.CORSOff, Credentials: false}
	snapSettings.NotFoundBody = json.RawMessage(`{"from":"scenario"}`)

	got, _ := composeScenarioLayer(wsSettings, nil, snapshotOf(snapSettings))

	tests := []struct {
		field string
		got   any
		want  any
		why   string
	}{
		{"seed", got.Seed, int64(99), "read inside the build (gen.Options.Seed)"},
		{"listSize", got.ListSize, 8, "read inside the build (gen.Options.ListSize)"},
		{"nullRate", got.NullRate, 0.9, "read inside the build (gen.Options.NullRate)"},
		{"identity.name", got.Identity.Name, "Scenario User", "read inside the build (gen.Options.Identity)"},
		{"auth.signingKey", got.Auth.SigningKey, "scenario-key", "read inside the build (gen.Options.Auth)"},
		{"auth.alg", got.Auth.Alg, "HS512", "read inside the build (gen.Options.Auth)"},
		{"delayMs", got.DelayMs, 90, "read from rt.settings by respond.go and custom.go"},
		{"envelope", *got.Envelope, envelopeScenario, "read from rt.settings by respond.go and custom.go"},

		{"basePath", got.BasePath, "/ws-api", "A3's deliberate exception: a scenario that moved the URL would be a fork with worse ergonomics (§12:530-531)"},
	}
	// cors and notFoundBody are asserted below rather than in this table:
	// one is a struct and the other a byte slice, and neither compares
	// correctly through an `any` equality that would silently pass on a nil
	// vs empty slice.

	for _, tt := range tests {
		if tt.got != tt.want {
			t.Errorf("%s = %v, want %v (%s)", tt.field, tt.got, tt.want, tt.why)
		}
	}

	if got.CORS != wsSettings.CORS {
		t.Errorf("cors = %+v, want the WORKSPACE's %+v — plane.go reads CORS per request and the preflight path never builds a runtime, so a scenario value here could not take effect at all",
			got.CORS, wsSettings.CORS)
	}
	if string(got.NotFoundBody) != `{"from":"workspace"}` {
		t.Errorf("notFoundBody = %s, want the WORKSPACE's — routes.go's serveNoRoute reads it from ws.Settings directly, so a scenario value here is unreachable",
			got.NotFoundBody)
	}
}

// --- through the real build --------------------------------------------------

// TestBuildRuntime_RecipeSetsCompileFromTheComposedMap is A4, and it is the
// only cover for the half of it a live curl cannot see unless the divergence
// is expressed as a recipe.
//
// Both layers bind a `const` recipe at the SAME data path of the SAME
// operation. If buildRecipeSets is still handed the map ForWorkspace
// returned — the pre-P2b line — the composed row is served while the
// WORKSPACE's recipe set is what compiled, and the generated body carries
// "from-workspace". That implementation compiles, keeps every pre-existing
// test green, and is wrong only in the bytes.
func TestBuildRuntime_RecipeSetsCompileFromTheComposedMap(t *testing.T) {
	const scenarioID = int64(5)
	key := overrides.OpKey("GET", "/profile")

	constRecipe := func(value string) map[string]overrides.Variant {
		return map[string]overrides.Variant{
			"200": {
				Mode: "generated",
				Recipes: map[string]recipes.Recipe{
					"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"` + value + `"`)},
				},
			},
		}
	}

	wsOwn := &overrides.Row{Method: "GET", Path: "/profile", OverrideOn: true, Responses: constRecipe("from-workspace")}
	snapEntry := bundle.OverrideEntry{
		Method: "GET", Path: "/profile", OverrideOn: true, Responses: constRecipe("from-scenario"),
	}

	p := New(runtimeTestConfig(4<<20, 32), nil, profileSource(), runtimeTestLogger())
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{key: wsOwn}})
	p.SetScenarios(&fakeScenarioSource{byID: map[int64]*scenarios.Scenario{
		scenarioID: {ID: scenarioID, Name: "s", Bundle: snapshotOf(domain.DefaultSettings(), snapEntry)},
	}})

	rt, err := p.runtimeFor(t.Context(), scenarioWorkspace(1, domain.DefaultSettings(), scenarioID))
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	route := profileRoute()
	set, ok := rt.lookupRecipes(&route, "200")
	if !ok {
		t.Fatal("lookupRecipes found nothing, want the compiled recipe of the COMPOSED row")
	}
	body, err := rt.gen.Body(profileVariant(), gen.Request{
		Method: "GET", CanonicalPath: "/profile", PathParams: map[string]string{},
		Query: url.Values{}, Status: 200, Recipes: set,
	})
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	if got["name"] != "from-scenario" {
		t.Errorf("name = %v, want %q — buildRecipeSets compiled the WORKSPACE's map while the composed row is what serves (A4)",
			got["name"], "from-scenario")
	}
}

// TestBuildRuntime_ScenarioSettingsReachTheGeneratorAndBasePathDoesNot proves
// A3a at the level that actually matters: the composed settings are what
// gen.Options was filled from (the array length is the scenario's listSize,
// and nothing else in widgetsDoc decides it — no query string, no
// minItems/maxItems), while rt.settings.BasePath is still the WORKSPACE's.
//
// Both halves in one test on purpose: an implementation that threads
// ws.Settings into gen.Options fails the first, and one that lets the
// scenario supply basePath fails the second, and the two mistakes are the
// opposite ends of the same decision.
func TestBuildRuntime_ScenarioSettingsReachTheGeneratorAndBasePathDoesNot(t *testing.T) {
	const scenarioID = int64(9)

	wsSettings := domain.DefaultSettings()
	wsSettings.ListSize = 3
	wsSettings.BasePath = "/ws-api"

	snapSettings := domain.DefaultSettings()
	snapSettings.ListSize = 8
	snapSettings.BasePath = "/scenario-api"

	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	p.SetScenarios(&fakeScenarioSource{byID: map[int64]*scenarios.Scenario{
		scenarioID: {ID: scenarioID, Name: "s", Bundle: snapshotOf(snapSettings)},
	}})

	rt, err := p.runtimeFor(t.Context(), scenarioWorkspace(1, wsSettings, scenarioID))
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}

	body, err := rt.gen.Body(widgetsVariant(), listRequest())
	if err != nil {
		t.Fatalf("Body: %v", err)
	}
	var items []any
	if err := json.Unmarshal(body, &items); err != nil {
		t.Fatalf("decode body %s: %v", body, err)
	}
	if len(items) != 8 {
		t.Errorf("generated array length = %d, want the SCENARIO's listSize 8 — the composed settings never reached gen.Options", len(items))
	}

	if rt.settings.BasePath != "/ws-api" {
		t.Errorf("rt.settings.BasePath = %q, want the WORKSPACE's %q — a scenario that relocated every route would be a fork, which §12 rejects scenarios in favour of",
			rt.settings.BasePath, "/ws-api")
	}
	if _, ok := rt.table.Match("GET", []string{"ws-api", "widgets"}); !ok {
		t.Error("the route table was not built at the workspace's basePath — a scenario must never move a workspace's URLs")
	}
}

// TestBuildRuntime_UnloadableScenarioServesTheWorkspaceLayer pins the failure
// posture: a snapshot that cannot be loaded or decoded is logged and skipped,
// never propagated as a build error. The alternative — failing the build —
// turns one bad row in `scenarios` into a 500 on EVERY request of that
// workspace, on a plane whose entire job is to keep answering; it is the same
// call buildRecipeSets already makes for an invalid stored recipe.
func TestBuildRuntime_UnloadableScenarioServesTheWorkspaceLayer(t *testing.T) {
	const scenarioID = int64(3)
	key := overrides.OpKey("GET", "/widgets")
	own := wsRow("GET", "/widgets", true, 418)

	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{key: own}})
	p.SetScenarios(&fakeScenarioSource{getErr: errors.New("decode snapshot: unexpected end of JSON input")})

	rt, err := p.runtimeFor(t.Context(), scenarioWorkspace(1, domain.DefaultSettings(), scenarioID))
	if err != nil {
		t.Fatalf("runtimeFor: %v, want an unloadable scenario to be logged and skipped, not to fail the build", err)
	}

	route := widgetsRoute()
	got, ok := rt.lookupOverride(&route)
	if !ok || got != own {
		t.Errorf("lookupOverride = (%+v, %v), want the WORKSPACE's own row — an unloadable scenario must leave that layer serving", got, ok)
	}
}

// TestBuildRuntime_NoScenarioSourceIgnoresAnActiveScenario is the "no source
// wired, no change" contract every other source on Plane already follows: a
// workspace whose scenario_id is set, on a Plane that never had
// [Plane.SetScenarios] called, serves its own layer. It is what makes every
// pre-P2b test in this package keep passing unmodified — and it is exactly
// why cmd/mocker forgetting the setter would ship a green suite over a dead
// feature rather than a crash.
func TestBuildRuntime_NoScenarioSourceIgnoresAnActiveScenario(t *testing.T) {
	key := overrides.OpKey("GET", "/widgets")
	own := wsRow("GET", "/widgets", true, 418)

	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{key: own}})

	rt, err := p.runtimeFor(t.Context(), scenarioWorkspace(1, domain.DefaultSettings(), 42))
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}
	route := widgetsRoute()
	if got, ok := rt.lookupOverride(&route); !ok || got != own {
		t.Errorf("lookupOverride = (%+v, %v), want the workspace's own row with no ScenarioSource wired", got, ok)
	}
}

// TestResourceBranch_ActiveScenarioDoesNotChangeDeclaredBaseScopeSet is P22
// (P3h D4.5): activating a scenario whose captured settings name a
// DIFFERENT declared base-scope set must not change what serving accepts —
// a value the WORKSPACE declares and the scenario does not is served, and
// a value only the SCENARIO declares is refused. Mutation: drop the
// BasePathValues line from composeScenarioLayer. Runs through
// [Plane.runtimeFor] (the real buildRuntime/composeScenarioLayer path, not
// a hand-built fixture) so the property is observed exactly where D4.5
// puts the restore.
func TestResourceBranch_ActiveScenarioDoesNotChangeDeclaredBaseScopeSet(t *testing.T) {
	const scenarioID = int64(9)

	wsSettings := domain.DefaultSettings()
	wsSettings.BasePath = "/orgs/{orgId}"
	wsSettings.BasePathValues = []string{"7"}

	snapSettings := domain.DefaultSettings()
	snapSettings.BasePath = "/orgs/{orgId}"
	snapSettings.BasePathValues = []string{"99"} // the scenario's OWN declared set — must never win

	p := New(runtimeTestConfig(4<<20, 32), nil, widgetsSource(), runtimeTestLogger())
	p.SetScenarios(&fakeScenarioSource{byID: map[int64]*scenarios.Scenario{
		scenarioID: {ID: scenarioID, Name: "s", Bundle: snapshotOf(snapSettings)},
	}})
	p.SetResources(stubResourceSource{})
	p.SetEntities(&fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return []resources.Entity{{Data: jsonx.RawMessage(`{"id":1}`)}}, nil
	}})

	ws := scenarioWorkspace(1, wsSettings, scenarioID)
	rt, err := p.runtimeFor(t.Context(), ws)
	if err != nil {
		t.Fatalf("runtimeFor: %v", err)
	}
	rt.resources = map[string]*resources.Resource{
		"/widgets": {ID: 1, WorkspaceID: 1, RouteFamily: "/widgets", IDField: "id", Wrapper: specs.Wrapper{}},
	}

	get := func(t *testing.T, path string) int {
		t.Helper()
		m := mustMatch(t, rt, http.MethodGet, path)
		values, ok := router.BaseValues(rt.settings.BasePath, NormalizeSegments(path))
		if !ok {
			t.Fatalf("router.BaseValues(%q, %q) = _, false", rt.settings.BasePath, path)
		}
		base := resources.EncodeScope(values)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), ws, rt, m, base)
		return rec.Code
	}

	if got := get(t, "/orgs/7/widgets"); got != http.StatusOK {
		t.Errorf("GET under the WORKSPACE's own declared value (7) = %d, want 200 — the scenario must not shrink the workspace's declared set", got)
	}
	if got := get(t, "/orgs/99/widgets"); got != http.StatusNotFound {
		t.Errorf("GET under the SCENARIO's own declared value (99) = %d, want 404 — activating a scenario must not widen the declared set beyond the workspace's own", got)
	}
}
