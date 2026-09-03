// custom_test.go is a BLACK-BOX test file (package mockplane_test, exactly
// like routes_test.go and livestate_test.go): everything it exercises is
// reached through [mockplane.Plane.ServeHTTP], the same entry point a real
// client uses, with a fake [mockplane.CustomSource] standing in for
// *customep.Repo — the identical no-database pattern fakeSpecSource
// (routes_test.go) already established for [mockplane.SpecSource].
package mockplane_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// fakeCustomSource is an in-memory [mockplane.CustomSource]: rows keyed by
// workspace id, with a call counter mirroring fakeSpecSource's own so a
// test can prove the route cache actually rebuilt (or didn't) instead of
// inferring it from behavior alone.
type fakeCustomSource struct {
	mu    sync.Mutex
	rows  map[int64][]*customep.Row
	calls atomic.Int32
}

func (f *fakeCustomSource) ForWorkspace(_ context.Context, workspaceID int64) ([]*customep.Row, error) {
	f.calls.Add(1)
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.rows[workspaceID], nil
}

func (f *fakeCustomSource) setRows(workspaceID int64, rows []*customep.Row) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.rows[workspaceID] = rows
}

// customRow is a minimal, valid *customep.Row fixture: OverrideOn true (so
// buildRuntime actually puts it in the table — see customep.Row's own doc
// comment on what false would mean instead), ActiveStatus already defaulted
// to 200 exactly like customep.Repo.Create would leave an unset one, and an
// empty Responses map a test fills in as needed.
func customRow(id int64, method, path string, sourceOrder int64) *customep.Row {
	return &customep.Row{
		ID:            id,
		Method:        method,
		Path:          path,
		CanonicalPath: router.CanonicalPath(path),
		SourceOrder:   sourceOrder,
		OverrideOn:    true,
		ActiveStatus:  200,
		Responses:     map[string]overrides.Variant{},
	}
}

// TestServeCustom_AnswersOnPathSpecDoesNotHave is the direct proof of this
// stage's first task: a custom endpoint serves a request the spec's own
// route table has nothing for, without disturbing what the spec DOES serve.
func TestServeCustom_AnswersOnPathSpecDoesNotHave(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/users", "listUsers", 1)},
	}}
	row := customRow(1, "GET", "/widgets", 1)
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"custom":true}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); body["custom"] != true {
		t.Errorf("body = %v, want the custom endpoint's own pinned body", body)
	}

	// The spec's own route, on a different path, must still answer —
	// merging custom endpoints into the table must not shadow it.
	req2 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/users", nil)
	rec2 := httptest.NewRecorder()
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("spec route status = %d, want 200; body=%s", rec2.Code, rec2.Body)
	}
}

// TestServeCustom_OverridesSpecOperationAtEqualCanonicalPath is DESIGN §8
// rule 3, proven end to end: a custom endpoint canonically equal to a spec
// operation wins the tie, and removing the row (with the matching Revision
// bump workspaces.Repo.Update's own writes always carry) gives the spec
// operation back, unshadowed.
func TestServeCustom_OverridesSpecOperationAtEqualCanonicalPath(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/items/{id}", "getItem", 1)},
	}}
	row := customRow(1, "GET", "/items/{id}", 1)
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"source":"custom"}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(spec, ws)
	p.SetCustomEndpoints(custom)

	get := func() map[string]any {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items/42", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		return decodeJSON(t, rec.Body.Bytes())
	}

	if body := get(); body["source"] != "custom" {
		t.Fatalf("body = %v, want the custom endpoint to win at equal specificity (DESIGN §8 rule 3)", body)
	}

	// Same pattern TestRouteCache_...RevisionBump (routes_test.go) uses to
	// force a rebuild: fakeSource hands back the SAME *workspaces.Workspace
	// pointer every lookup, so mutating Revision here is exactly what a real
	// customep.Repo.Delete's own bumpRevisionTx looks like to this cache.
	custom.setRows(1, nil)
	ws.Revision++

	body := get()
	if body["source"] == "custom" {
		t.Fatalf("body = %v, want the SPEC operation now that the custom row is gone", body)
	}
	if body["op"] != "getItem" {
		t.Errorf("body = %v, want buildFakeDoc's own {\"op\":\"getItem\"} shape from the un-shadowed spec operation", body)
	}
}

// TestServeCustom_SpecLessWorkspaceServesCustomAndElse404s is this stage's
// second task, proven directly: a workspace with NO spec attached (and a
// Plane built with a nil SpecSource, the harder of the two "no spec" shapes
// — see runtime.go's buildRuntime) still serves its own custom endpoint, and
// every other path still 404s exactly as it always has.
func TestServeCustom_SpecLessWorkspaceServesCustomAndElse404s(t *testing.T) {
	row := customRow(1, "GET", "/ping", 1)
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"pong":true}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()} // SpecID nil
	src := &fakeSource{bySlug: map[string]*workspaces.Workspace{"alex": ws}}
	p := mockplane.New(testConfig(), src, nil, testLogger()) // nil SpecSource too
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); body["pong"] != true {
		t.Errorf("body = %v, want the custom endpoint's own pinned body", body)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/nope", nil)
	rec2 := httptest.NewRecorder()
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 for an unmatched path on a spec-less workspace; body=%s", rec2.Code, rec2.Body)
	}
}

// TestServeCustom_RouteOff_Answers404LikeUnmatchedRoute proves route_off on
// a custom endpoint is indistinguishable from the SAME path never having
// been declared at all — the identical DESIGN §8 guarantee
// TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute (respond_test.go)
// already pins for op_overrides, checked here against custom.go's own
// route_off branch instead of respond.go's.
func TestServeCustom_RouteOff_Answers404LikeUnmatchedRoute(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}

	row := customRow(1, "GET", "/widgets", 1)
	row.RouteOff = true
	pOff := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	pOff.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()
	pOff.ServeHTTP(rec, req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route_off answers the same 404 an unmatched route gets); body=%s", rec.Code, rec.Body)
	}

	// A second Plane with no custom endpoint at all — the genuinely
	// unmatched baseline route_off must be byte-identical to, for the SAME
	// workspace slug and the SAME request path (the 404 message embeds
	// both, so this is the only fair comparison).
	pMissing := newPlaneWithSpec(&fakeSpecSource{routes: map[int64][]router.Route{1: {}}}, specWorkspace("alex", 1, 1))

	want := httptest.NewRecorder()
	reqWant := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	pMissing.ServeHTTP(want, reqWant)

	if rec.Body.String() != want.Body.String() {
		t.Errorf("route_off body = %s, want the exact unmatched-route 404 body %s", rec.Body, want.Body)
	}
	if rec.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
		t.Errorf("route_off Content-Type = %q, want %q", rec.Header().Get("Content-Type"), want.Header().Get("Content-Type"))
	}
}

// TestServeCustom_WhenSelectsStatus proves a custom endpoint's own when[]
// (P1c2) beats its ActiveStatus fallback, and that with no matching
// condition ActiveStatus is what actually answers — there is no third,
// "document's own choice" step for a custom endpoint to fall further back
// to, unlike a spec operation's chooseVariant.
func TestServeCustom_WhenSelectsStatus(t *testing.T) {
	row := customRow(1, "POST", "/widgets", 1)
	row.ActiveStatus = http.StatusCreated
	row.Responses["201"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"created":true}`)}
	row.Responses["409"] = overrides.Variant{
		Mode: "pinned",
		Body: json.RawMessage(`{"conflict":true}`),
		When: []overrides.Condition{{In: "header", Name: "X-Force-Conflict", Op: "exists"}},
	}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(custom)

	// No trigger header: no when[] matches (a variant with an empty When
	// never matches — overrides.MatchAll's own contract), so ActiveStatus
	// answers.
	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (ActiveStatus, no when[] matched); body=%s", rec.Code, rec.Body)
	}

	// Trigger header present: the "409" variant's own when[] beats
	// ActiveStatus (DESIGN §4/§12's own priority).
	req2 := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/widgets", nil)
	req2.Header.Set("X-Force-Conflict", "1")
	rec2 := httptest.NewRecorder()
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (when[] must beat ActiveStatus); body=%s", rec2.Code, rec2.Body)
	}
	if body := decodeJSON(t, rec2.Body.Bytes()); body["conflict"] != true {
		t.Errorf("body = %v, want the 409 variant's own pinned body", body)
	}
}

// TestServeCustom_PinnedBodyOverMaxResponseIsRefused is the direct regression
// for the trap this stage's own task calls out by name: the pinned-body
// re-check against p.cfg.MaxResponse (respond.go's inline gate for a spec
// route's pinned body) must apply here too, or a stored custom-endpoint body
// bypasses the live ceiling every generated body is held to.
func TestServeCustom_PinnedBodyOverMaxResponseIsRefused(t *testing.T) {
	row := customRow(1, "GET", "/big", 1)
	row.Responses["200"] = overrides.Variant{
		Mode: "pinned",
		Body: json.RawMessage(`"` + strings.Repeat("x", 100) + `"`),
	}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	cfg := testConfig()
	cfg.MaxResponse = 10 // far under the pinned body's own size
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()}
	src := &fakeSource{bySlug: map[string]*workspaces.Workspace{"alex": ws}}
	p := mockplane.New(cfg, src, nil, testLogger())
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/big", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the resolved status still answers; only the oversized body is refused); body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: a pinned body over MOCKER_MAX_RESPONSE must never reach the wire", rec.Body.String())
	}
}

// TestServeNoRoute_NotFoundBodyReplacesBodyOnly is this stage's third task:
// ws.Settings.NotFoundBody replaces ONLY the 404 body — same status, same
// CORS headers (Step 2 of serveResolved runs long before serveNoRoute).
func TestServeNoRoute_NotFoundBodyReplacesBodyOnly(t *testing.T) {
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()}
	ws.Settings.NotFoundBody = json.RawMessage(`{"custom404":true}`)
	p := newPlane(ws) // nil SpecSource, no custom endpoints: the plain "no route" 404 path

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/anything", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if body["custom404"] != true {
		t.Errorf("body = %v, want the workspace's own NotFoundBody verbatim", body)
	}
	if _, has := body["error"]; has {
		t.Errorf("body = %v, must not still carry the default error shape once NotFoundBody is set", body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want CORS unaffected by NotFoundBody", got)
	}
}

// TestServeRoute_NoCustomSourceWired_BehavesAsBeforeP1c2 is HARD RULE 6 for
// this stage: a Plane whose SetCustomEndpoints is never called must serve
// exactly what it did before custom.go existed — the whole rest of this
// package's pre-existing, unmodified test suite already proves this in
// aggregate; this is the one direct assertion naming it.
func TestServeRoute_NoCustomSourceWired_BehavesAsBeforeP1c2(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 1)},
	}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1)) // SetCustomEndpoints never called

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/not-a-route", nil)
	rec2 := httptest.NewRecorder()
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec2.Code, rec2.Body)
	}
}

// TestServeCustom_SessionDelayBeatsRowDelay is this stage's own acceptance
// observation 4, proven directly: custom.go inlines its OWN delay chain
// (this file's own header comment), so a fix to respond.go's
// effectiveDelayMs alone would leave a custom endpoint deaf to the session
// layer entirely, with every OTHER test in this package still green. A
// live-state delay on the SAME route as a smaller row DelayMs is what
// actually gets awaited — DESIGN §4's Session beats the row.
func TestServeCustom_SessionDelayBeatsRowDelay(t *testing.T) {
	rowDelay := 10 // must lose to the session's own value below
	row := customRow(1, "GET", "/legacy/ping", 1)
	row.DelayMs = &rowDelay
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"ok":true}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(&fakeSpecSource{routes: map[int64][]router.Route{1: {}}}, ws)
	p.SetCustomEndpoints(custom)

	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/legacy/ping"},
		Action: livestate.ActionDelay, Ms: 60,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p.SetLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/legacy/ping", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.ServeHTTP(rec, req)
	if elapsed := time.Since(start); elapsed < 60*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the session's own 60ms delay (the row's own 10ms must not win)", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// TestServeCustom_PauseParksThenReleasedServesNormally is
// TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus's custom-route
// twin: custom.go must obey the session layer's pause exactly like a spec
// operation does — the same "obeys the session layer exactly as spec
// operations do" claim this file's own header comment set out to prove.
func TestServeCustom_PauseParksThenReleasedServesNormally(t *testing.T) {
	row := customRow(1, "GET", "/legacy/ping", 1)
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"ok":true}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(&fakeSpecSource{routes: map[int64][]router.Route{1: {}}}, ws)
	p.SetCustomEndpoints(custom)

	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/legacy/ping"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p.SetLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/legacy/ping", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		p.ServeHTTP(rec, req)
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("ServeHTTP returned before the pause was ever released")
	case <-time.After(50 * time.Millisecond):
		// still parked — correct
	}
	if rec.Body.Len() != 0 {
		t.Fatalf("wrote a body while parked: %q", rec.Body.String())
	}

	store.Clear(ws.ID)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ServeHTTP never returned after Clear")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}
