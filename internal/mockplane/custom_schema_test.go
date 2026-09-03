package mockplane_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// noSpecWorkspace is a workspace bound to nothing — P7a's "design from
// nothing" (DESIGN §34.4): the runtime builds its generator over
// design.Skeleton and a custom endpoint's schema still generates.
func noSpecWorkspace(slug string, revision int64) *workspaces.Workspace {
	return &workspaces.Workspace{ID: 1, Slug: slug, Revision: revision, Settings: domain.DefaultSettings()}
}

const thingSchema = `{"type":"object","required":["id","name","tags"],"properties":{"id":{"type":"integer"},"name":{"type":"string"},"tags":{"type":"array","items":{"type":"string"},"minItems":1}}}`

// TestServeCustom_SchemaGeneratesWithoutASpec is P7a D4/D5 end to end: a
// custom endpoint whose variant carries a schema and no pinned body
// answers a GENERATED body that satisfies the schema, on a workspace with
// no spec at all — where, before P7a, the same row answered an empty body
// (custom.go's former `default` arm) and rt.gen was nil.
func TestServeCustom_SchemaGeneratesWithoutASpec(t *testing.T) {
	row := customRow(1, "GET", "/things", 1)
	row.Responses["200"] = overrides.Variant{Schema: json.RawMessage(thingSchema)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	ws := noSpecWorkspace("alex", 1)
	p := newPlane(ws)
	p.SetCustomEndpoints(custom)

	get := func() (map[string]any, []byte) {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/things", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "application/json") {
			t.Fatalf("Content-Type = %q, want application/json", ct)
		}
		return decodeJSON(t, rec.Body.Bytes()), rec.Body.Bytes()
	}
	body, raw := get()
	if _, ok := body["id"].(float64); !ok {
		t.Errorf("id = %v (%T), want an integer the schema declares", body["id"], body["id"])
	}
	if _, ok := body["name"].(string); !ok {
		t.Errorf("name = %v, want a string", body["name"])
	}
	tags, ok := body["tags"].([]any)
	if !ok || len(tags) == 0 {
		t.Errorf("tags = %v, want a non-empty array (minItems 1)", body["tags"])
	}

	// Deterministic: the same seed and revision give the same bytes.
	_, again := get()
	if string(again) != string(raw) {
		t.Errorf("second request differs from the first:\n%s\n%s", raw, again)
	}

	// And the seed moves the body — the schema is walked by the
	// generator, not answered from a fixed example.
	ws.Settings.Seed = ws.Settings.Seed + 1
	ws.Revision++
	_, reseeded := get()
	if string(reseeded) == string(raw) {
		t.Errorf("body did not change with the seed: %s", raw)
	}
}

// TestServeCustom_PinnedOutranksSchema is §8's mode rule read for P7a: a
// variant carrying BOTH a schema and a pinned body serves the pinned
// bytes; the schema is the export's declared shape only.
func TestServeCustom_PinnedOutranksSchema(t *testing.T) {
	row := customRow(1, "GET", "/things", 1)
	row.Responses["200"] = overrides.Variant{Mode: "pinned", Body: json.RawMessage(`{"pinned":true}`), Schema: json.RawMessage(thingSchema)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	p := newPlane(noSpecWorkspace("alex", 1))
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/things", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); body["pinned"] != true {
		t.Errorf("body = %v, want the pinned body", body)
	}
}

// TestServeCustom_UnresolvableRefGeneratesEmptyObject is D6's serve-time
// half: a `$ref` the bound document cannot resolve (here: the skeleton,
// which has no components) does not fail the build or the request — the
// node generates as {} and the plane answers 200 with a JSON body.
func TestServeCustom_UnresolvableRefGeneratesEmptyObject(t *testing.T) {
	row := customRow(1, "GET", "/me", 1)
	row.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object","required":["profile","ok"],"properties":{"profile":{"$ref":"#/components/schemas/Nope"},"ok":{"type":"boolean"}}}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	p := newPlane(noSpecWorkspace("alex", 1))
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/me", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the plane always answers); body=%s", rec.Code, rec.Body)
	}
	body := decodeJSON(t, rec.Body.Bytes())
	if _, ok := body["ok"].(bool); !ok {
		t.Errorf("ok = %v, want a boolean beside the emptied node", body["ok"])
	}
	profile, present := body["profile"]
	if !present {
		t.Fatalf("profile is absent: %v — the unresolvable node should generate as an (empty) value, not vanish", body)
	}
	// {} means an empty OBJECT: an emptied schema map is untyped and the
	// generator would pick a string for it, which is not what D6 promises.
	if obj, ok := profile.(map[string]any); !ok || len(obj) != 0 {
		t.Errorf("profile = %#v, want an empty object {}", profile)
	}
}

// TestServeCustom_RefIntoTheBoundSpecResolves is §34.3's headline use —
// "reuse the base's schema in one line" — at both places a `$ref` can sit:
// as the whole schema (the root, which gen.Body takes verbatim and never
// resolves on its own — buildCustomInline chases it first) and as a
// property (walkNode's own hop). Both bodies must carry the referenced
// schema's required properties, not a random string.
func TestServeCustom_RefIntoTheBoundSpecResolves(t *testing.T) {
	var thing map[string]any
	if err := json.Unmarshal([]byte(thingSchema), &thing); err != nil {
		t.Fatal(err)
	}
	spec := &fakeSpecSource{
		routes:     map[int64][]router.Route{1: {route("GET", "/users", "listUsers", 1)}},
		components: map[string]any{"schemas": map[string]any{"Thing": thing}},
	}
	rootRow := customRow(1, "GET", "/thing", 1)
	rootRow.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"$ref":"#/components/schemas/Thing"}`)}
	nestedRow := customRow(2, "GET", "/wrapped", 1)
	nestedRow.Responses["200"] = overrides.Variant{Schema: json.RawMessage(`{"type":"object","required":["thing"],"properties":{"thing":{"$ref":"#/components/schemas/Thing"}}}`)}
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {rootRow, nestedRow}}}

	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(custom)

	isThing := func(v any) bool {
		obj, ok := v.(map[string]any)
		if !ok {
			return false
		}
		_, idNum := obj["id"].(float64)
		_, nameStr := obj["name"].(string)
		tags, _ := obj["tags"].([]any)
		return idNum && nameStr && len(tags) >= 1
	}

	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/thing", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("root $ref: status = %d; body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); !isThing(body) {
		t.Errorf("root $ref: body = %v, want a Thing (integer id, string name, tags)", body)
	}

	rec = httptest.NewRecorder()
	p.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/wrapped", nil))
	if rec.Code != http.StatusOK {
		t.Fatalf("nested $ref: status = %d; body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); !isThing(body["thing"]) {
		t.Errorf("nested $ref: body = %v, want thing to be a Thing", body)
	}
}

// TestServeCustom_SchemaWithSpecBoundFollowsTheDocument: with a spec bound,
// the custom row's schema generates through the SAME generator the spec's
// operations use, and the spec's own route beside it is untouched.
func TestServeCustom_SchemaWithSpecBound(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/users", "listUsers", 1)},
	}}
	row := customRow(1, "GET", "/widgets", 1)
	row.Responses["201"] = overrides.Variant{Schema: json.RawMessage(thingSchema)}
	row.ActiveStatus = 201
	custom := &fakeCustomSource{rows: map[int64][]*customep.Row{1: {row}}}

	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))
	p.SetCustomEndpoints(custom)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201 (the row's activeStatus); body=%s", rec.Code, rec.Body)
	}
	if body := decodeJSON(t, rec.Body.Bytes()); body["name"] == nil {
		t.Errorf("body = %v, want a generated name", body)
	}

	req2 := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/users", nil)
	rec2 := httptest.NewRecorder()
	p.ServeHTTP(rec2, req2)
	if rec2.Code != http.StatusOK {
		t.Fatalf("spec route status = %d, want 200; body=%s", rec2.Code, rec2.Body)
	}
}
