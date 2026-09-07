// respond_livestate_test.go: activeStatus, when-conditions and live-state
// precedence — activeStatus picks its declared variant (or answers an empty
// body, never 500, when undeclared), the full
// live-beats-when-beats-activeStatus-beats-document order, a when-match's
// pinned body and recipes applying at the final status, route-off consuming
// no live-state counter, fail_directive answering twice then normal, and a
// truncated body never matching a when-predicate. Split out of the former
// respond_test.go (package mockplane, not mockplane_test — see
// helpers_test.go's own comment for why).
package mockplane

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
)

// TestServeGenerated_ActiveStatus_PicksDeclaredVariant proves active_status
// overrides chooseVariant's own DESIGN §7 pick when the document itself
// declares a response at the pinned status.
func TestServeGenerated_ActiveStatus_PicksDeclaredVariant(t *testing.T) {
	v200 := itemVariant()
	v201 := gen.ResponseVariant{
		OpRowID: 1, Selector: "201", HTTPStatus: 201, MediaType: "application/json",
		SchemaPtr: itemVariant().SchemaPtr, OpPointer: itemVariant().OpPointer,
	}
	variants := map[int64][]gen.ResponseVariant{1: {v200, v201}}

	pinnedStatus := 201
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, ActiveStatus: &pinnedStatus,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()}, variants, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (active_status overrides chooseVariant's own 200 pick); body=%s", rec.Code, rec.Body)
	}
}

// TestServeGenerated_ActiveStatus_UndeclaredStatusAnswersEmptyBodyNot500
// proves the other half: a pinned status the document never declares a
// response for is not an error — it answers that exact status with no
// body, never a 500 and never silently falling back to chooseVariant's own
// pick.
func TestServeGenerated_ActiveStatus_UndeclaredStatusAnswersEmptyBodyNot500(t *testing.T) {
	pinnedStatus := 202
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, ActiveStatus: &pinnedStatus,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != 202 {
		t.Fatalf("status = %d, want 202 (the pinned, undeclared status), never 500; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: the document declares no response at this status", rec.Body.String())
	}
}

// TestServeGenerated_StatusPrecedence_LiveBeatsWhenBeatsActiveStatusBeatsDocument
// is the order table the digest asks for by name: given a route with
// active_status=418, a when[] on 409 that matches, and a live-state force of
// 503, the answer is 503; drop the force and it is 409; drop the matching
// when[] too (leaving only active_status) and it is 418; drop active_status
// as well and it is the document's own choice (chooseVariant's 200).
func TestServeGenerated_StatusPrecedence_LiveBeatsWhenBeatsActiveStatusBeatsDocument(t *testing.T) {
	s418 := 418
	matchingWhen := map[string]overrides.Variant{
		"409": {When: []overrides.Condition{{In: "header", Name: "X-Debug", Op: "exists"}}},
	}

	tests := []struct {
		name         string
		liveStatus   int // 0 = no live directive set at all
		responses    map[string]overrides.Variant
		activeStatus *int
		wantStatus   int
	}{
		{"a live force wins over a matching when[] and active_status", 503, matchingWhen, &s418, http.StatusServiceUnavailable},
		{"a matching when[] wins once there is no live force", 0, matchingWhen, &s418, http.StatusConflict},
		{"active_status wins once the when[] is gone", 0, map[string]overrides.Variant{}, &s418, 418},
		{"the document's own choice once active_status is gone too", 0, map[string]overrides.Variant{}, nil, http.StatusOK},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rows := map[string]*overrides.Row{
				itemOverrideKey(): {
					Method: "GET", Path: "/items", OverrideOn: true,
					ActiveStatus: tt.activeStatus,
					Responses:    tt.responses,
				},
			}
			rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
				map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
			m := mustMatch(t, rt, "GET", "/items")

			ws := respondTestWorkspace()
			store := livestate.NewStore(0, nil)
			if tt.liveStatus != 0 {
				if err := store.Set(ws.ID, livestate.Directive{
					Target: livestate.Target{Method: "GET", Path: "/items"},
					Action: livestate.ActionStatus, Status: tt.liveStatus,
				}); err != nil {
					t.Fatalf("store.Set: %v", err)
				}
			}
			p := respondTestPlaneWithLiveState(store)

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			req.Header.Set("X-Debug", "1") // satisfies matchingWhen's own condition in every subtest
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

// TestServeGenerated_WhenMatch_PinnedBodyAndRecipesApplyAtFinalStatus is the
// property that silently breaks if the status choice and the response
// assembly ever key off different statuses: once SelectWhen picks "409",
// mode "pinned"'s own body/media type — stored on that SAME "409" entry —
// must be what actually reaches the wire, not chooseVariant's original pick.
func TestServeGenerated_WhenMatch_PinnedBodyAndRecipesApplyAtFinalStatus(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"409": {
					Mode:      "pinned",
					MediaType: "application/json",
					Body:      json.RawMessage(`{"error":"debug"}`),
					When:      []overrides.Condition{{In: "header", Name: "X-Debug", Op: "exists"}},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req.Header.Set("X-Debug", "1")
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, want 409 (the status the when[] itself selected); body=%s", rec.Code, rec.Body)
	}
	if rec.Body.String() != `{"error":"debug"}` {
		t.Errorf("body = %s, want the pinned body bound to the SAME status the when[] selected — this is the \"keys off the FINAL status\" property", rec.Body)
	}
}

// TestServeGenerated_RouteOff_ConsumesNoLiveStateCounter is
// TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute's P1c2 sibling:
// route_off must win over a live-state force AND must never call
// [livestate.Store.Apply] at all — proven here by asserting the fail
// directive's own counter afterward, not merely the response status.
func TestServeGenerated_RouteOff_ConsumesNoLiveStateCounter(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, RouteOff: true,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"},
		Action: livestate.ActionFail, Status: 500, N: 2,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route_off); body=%s", rec.Code, rec.Body)
	}

	directives := store.List(ws.ID)
	if len(directives) != 1 || directives[0].N != 2 {
		t.Fatalf("directives = %+v, want the fail directive UNCONSUMED at n=2 — route_off must never reach livestate.Apply", directives)
	}
}

// TestServeGenerated_LiveStateFailDirective_AnswersTwiceThenNormal proves
// Apply's own consuming contract end to end through serveGenerated: a fail
// directive with n=2 forces the SAME status on exactly the next two matched
// requests, and the third request is served exactly as if no directive
// existed at all.
func TestServeGenerated_LiveStateFailDirective_AnswersTwiceThenNormal(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"},
		Action: livestate.ActionFail, Status: 500, N: 2,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	want := []int{http.StatusInternalServerError, http.StatusInternalServerError, http.StatusOK}
	for i, wantStatus := range want {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
		if rec.Code != wantStatus {
			t.Errorf("request %d: status = %d, want %d; body=%s", i+1, rec.Code, wantStatus, rec.Body)
		}
	}
}

// TestServeGenerated_BodyPredicate_TruncatedBodyNeverMatches is the
// end-to-end proof (reqbody_test.go's TestOverridesInputFor already proves
// the narrower unit) that a truncated capture can never satisfy an
// in:"body" when[] condition — active_status is the observable fallback
// that fires instead.
func TestServeGenerated_BodyPredicate_TruncatedBodyNeverMatches(t *testing.T) {
	altStatus := 299
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, ActiveStatus: &altStatus,
			Responses: map[string]overrides.Variant{
				"409": {When: []overrides.Condition{{In: "body", Name: "flag", Op: "exists"}}},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req = attachCapturedBody(req, &capturedBody{bytes: []byte(`{"flag"`), truncated: true})
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != altStatus {
		t.Fatalf("status = %d, want %d (active_status — the truncated body must never satisfy the when[]); body=%s",
			rec.Code, altStatus, rec.Body)
	}
}
