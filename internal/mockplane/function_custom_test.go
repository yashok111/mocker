// function_custom_test.go is A18's custom-endpoint half of the serving
// branch, and it is a BLACK-BOX file (package mockplane_test) for the same
// reason custom_test.go is: the fixtures that stand up a real route table
// with a custom row — fakeSpecSource, newPlaneWithSpec, fakeCustomSource —
// live there, and the white-box function_test.go cannot see them.
package mockplane_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
)

// TestServeCustom_functionServes is D7's second matrix: the branch sits at
// the same logical position on a custom endpoint and BEFORE the 406 gate,
// because a custom endpoint's only declared media type belongs to a PINNED
// variant and a function variant is not pinned — there is nothing to
// negotiate against until the function has said what it produced.
//
// The Accept header here is the observation that separates the two matrices:
// a spec operation with a declared application/json would answer 406 for it,
// and this row must not.
func TestServeCustom_functionServes(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	row := customRow(1, http.MethodGet, "/sign-in", 1)
	row.Responses["200"] = overrides.Variant{Function: `return 200, {token = "t"}`}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/sign-in", nil)
	req.Header.Set("Accept", "text/plain")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"token"`) {
		t.Fatalf("body = %s, want the function's own answer", rec.Body)
	}
}

// TestServeCustom_functionRunsAfterTheSessionLayer is the custom half of
// clause 29: a forced status answers without the function running, exactly as
// it does for a spec operation and for a stream handshake.
func TestServeCustom_functionRunsAfterTheSessionLayer(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	row := customRow(1, http.MethodGet, "/sign-in", 1)
	row.Responses["200"] = overrides.Variant{Function: `return 299, {ran = true}`}
	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(spec, ws)
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})

	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: http.MethodGet, Path: "/sign-in"},
		Action: livestate.ActionStatus, Status: 503,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p.SetLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/sign-in", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != 503 {
		t.Fatalf("status = %d, want the forced 503 — the session layer runs before the VM exists", rec.Code)
	}
	if strings.Contains(rec.Body.String(), "ran") {
		t.Fatalf("the function ran: %s", rec.Body)
	}
}
