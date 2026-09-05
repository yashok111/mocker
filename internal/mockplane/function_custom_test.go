// function_custom_test.go is A18's custom-endpoint half of the serving
// branch, and it is a BLACK-BOX file (package mockplane_test) for the same
// reason custom_test.go is: the fixtures that stand up a real route table
// with a custom row — fakeSpecSource, newPlaneWithSpec, fakeCustomSource —
// live there, and the white-box function_test.go cannot see them.
package mockplane_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
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

// --- A19: mock.generate through a real generator ----------------------------

// TestServeCustom_functionGeneratesFromTheBoundSpec is mock.generate end to
// end: a `$ref` into the bound spec's components resolves through the same
// resolver an inline custom-endpoint schema's refs go through, an inline table
// is a schema in its own right, and a pointer the spec does not carry is the
// generator's own refusal — never a 500 on the route.
func TestServeCustom_functionGeneratesFromTheBoundSpec(t *testing.T) {
	var thing map[string]any
	if err := json.Unmarshal([]byte(thingSchema), &thing); err != nil {
		t.Fatal(err)
	}
	spec := &fakeSpecSource{
		routes:     map[int64][]router.Route{1: {}},
		components: map[string]any{"schemas": map[string]any{"Thing": thing}},
	}
	refRow := customRow(1, http.MethodGet, "/thing", 1)
	refRow.Responses["200"] = overrides.Variant{Function: `
		local t, err = mock.generate("#/components/schemas/Thing")
		if not t then return 500, {err = err} end
		t.name = "override"
		return 200, t`}
	inlineRow := customRow(2, http.MethodGet, "/inline", 2)
	inlineRow.Responses["200"] = overrides.Variant{Function: `
		local t = mock.generate({type = "object", required = {"n"}, properties = {n = {type = "integer"}}})
		return 200, t`}
	badRow := customRow(3, http.MethodGet, "/bad", 3)
	badRow.Responses["200"] = overrides.Variant{Function: `return 200, {err = select(2, mock.generate("#/components/schemas/Nope"))}`}
	nestedBadRow := customRow(4, http.MethodGet, "/nested-bad", 4)
	nestedBadRow.Responses["200"] = overrides.Variant{Function: `return 200, {err = select(2, mock.generate({
		type = "object", properties = {a = {type = "string"}, b = {["$ref"] = "#/components/schemas/Nope"}}}))}`}
	wholeDocRow := customRow(5, http.MethodGet, "/whole", 5)
	wholeDocRow.Responses["200"] = overrides.Variant{Function: `return 200, {err = select(2, mock.generate({["$ref"] = "#"}))}`}
	twiceRow := customRow(6, http.MethodGet, "/twice", 6)
	twiceRow.Responses["200"] = overrides.Variant{Function: `
		local a = mock.generate({type = "object", required = {"n"}, properties = {n = {type = "integer", minimum = 0, maximum = 1000000}}})
		local b = mock.generate({type = "object", required = {"n"}, properties = {n = {type = "integer", minimum = 0, maximum = 1000000}}})
		return 200, {a = a.n, b = b.n}`}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {refRow, inlineRow, badRow, nestedBadRow, wholeDocRow, twiceRow}}})

	get := func(path string) (int, map[string]any) {
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil))
		var body map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("%s: body %s is not an object: %v", path, rec.Body, err)
		}
		return rec.Code, body
	}

	code, body := get("/thing")
	if code != http.StatusOK {
		t.Fatalf("/thing: status = %d, body = %v", code, body)
	}
	if _, ok := body["id"].(float64); !ok {
		t.Errorf("/thing: id = %v, want the generated integer", body["id"])
	}
	if tags, _ := body["tags"].([]any); len(tags) == 0 {
		t.Errorf("/thing: tags = %v, want the generated array", body["tags"])
	}
	if body["name"] != "override" {
		t.Errorf("/thing: name = %v, want the function's own edit over the generated value", body["name"])
	}

	code, body = get("/inline")
	if code != http.StatusOK {
		t.Fatalf("/inline: status = %d, body = %v", code, body)
	}
	if _, ok := body["n"].(float64); !ok {
		t.Errorf("/inline: n = %v, want an integer generated from the inline schema", body["n"])
	}

	code, body = get("/bad")
	if code != http.StatusOK {
		t.Fatalf("/bad: status = %d, want the function's own 200 carrying the refusal", code)
	}
	if msg, _ := body["err"].(string); msg != "unresolved_ref: #/components/schemas/Nope" {
		t.Errorf("/bad: err = %v, want unresolved_ref naming the pointer", body["err"])
	}

	// checkRefs's own branch: a dead $ref NESTED in an inline schema is the
	// same refusal by pointer, never a silently empty object in the body.
	_, body = get("/nested-bad")
	if msg, _ := body["err"].(string); msg != "unresolved_ref: #/components/schemas/Nope" {
		t.Errorf("/nested-bad: err = %v, want unresolved_ref naming the nested pointer", body["err"])
	}
	// The #/ rule on the table form: "#" alone would be the whole document.
	_, body = get("/whole")
	if msg, _ := body["err"].(string); !strings.HasPrefix(msg, "bad_schema") {
		t.Errorf("/whole: err = %v, want bad_schema for a $ref that is not a #/ pointer", body["err"])
	}
	// Two calls in one request are the FIRST and SECOND draw, not the same
	// draw twice (A19 review: a tick calling generate emitted one frame
	// forever before the per-call ordinal).
	_, body = get("/twice")
	if body["a"] == body["b"] {
		t.Errorf("/twice: a = b = %v; consecutive generate calls must draw consecutive values", body["a"])
	}
	// And the whole request is still deterministic: the same two draws.
	_, again := get("/twice")
	if again["a"] != body["a"] || again["b"] != body["b"] {
		t.Errorf("/twice: second request drew %v/%v, first %v/%v — a request's generate calls must repeat", again["a"], again["b"], body["a"], body["b"])
	}
}

// TestServeCustom_functionCreatesAnEntity is the writer end to end: a function
// on a custom endpoint creates a row through the same store the mock plane's
// POST uses, and answers with the stored row.
func TestServeCustom_functionCreatesAnEntity(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{1: {}}}
	row := customRow(1, http.MethodPost, "/rooms/{roomId}/say", 1)
	row.Responses["201"] = overrides.Variant{Function: `
		local r, err = mock.entities.create("/messages", {text = req.body.text})
		if not r then return 500, {err = err} end
		return 201, r`}
	row.ActiveStatus = 201
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(&fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}})
	store := &scopeEntityStore{}
	p.SetResources(scopeResourceSource{res: &resources.Resource{
		ID: 5, WorkspaceID: 1, RouteFamily: "/messages", IDField: "id", ScopeParams: []string{"roomId"},
	}})
	p.SetEntities(store)

	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/rooms/42/say", strings.NewReader(`{"text":"hi"}`))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"text":"hi"`) {
		t.Errorf("body = %s, want the stored row", rec.Body)
	}
	store.mu.Lock()
	defer store.mu.Unlock()
	if want := resources.EncodeScope([]string{"42"}); len(store.created) != 1 || store.created[0] != want {
		t.Errorf("Create scopes = %v, want one call under %q — the URL's own room id", store.created, want)
	}
}
