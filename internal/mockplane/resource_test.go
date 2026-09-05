// resource_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [resourceBranch], [lookupResource], [marshalCollection],
// [parsePostBody] and [envelopeOverhead] are all unexported. It reuses
// respond_test.go's own harness (fixtureRuntime, mustMatch, respondTestPlane,
// respondTestWorkspace, blankDoc) rather than duplicating it — this file is
// still the same package and the same compilation unit.
//
// Scope: the D13 acceptance clauses this run assigns to section "Branch" —
// 4, 10, 11, 16, 19, 20, 21, 22, 23, 24, 27, 33, 34, 35, 36, 38, 44, 46, 47,
// 48, 49, 50 — each named where it is proven, so a clause with no comment
// pointing at it here is a property nobody in this file is holding.
package mockplane

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

// --- fixtures ---------------------------------------------------------

// resTestStrPtr is this file's own *string helper — respond_test.go and
// runtime_test.go have no equivalent to share.
func resTestStrPtr(s string) *string { return &s }

// resourceTestRoutes is a small family — GET/POST /items, GET/DELETE
// /items/{id} — with OpRowIDs matching resourceTestVariants below.
func resourceTestRoutes() []router.Route {
	return []router.Route{
		{OpRowID: 1, Method: http.MethodGet, Path: "/items", CanonicalPath: "/items", SourceOrder: 1},
		{OpRowID: 2, Method: http.MethodPost, Path: "/items", CanonicalPath: "/items", SourceOrder: 2},
		{OpRowID: 3, Method: http.MethodGet, Path: "/items/{id}", CanonicalPath: "/items/{}", SourceOrder: 3},
		{OpRowID: 4, Method: http.MethodDelete, Path: "/items/{id}", CanonicalPath: "/items/{}", SourceOrder: 4},
	}
}

// resourceTestVariants gives each of resourceTestRoutes' four operations
// exactly one declared 2xx variant: 200 for both GETs, 201 for POST, 204 for
// DELETE — 204 is what makes resolveVariant set rv.NoBody automatically,
// exactly like a real spec's bodyless DELETE would.
func resourceTestVariants() map[int64][]gen.ResponseVariant {
	return map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		2: {{OpRowID: 2, Selector: "201", HTTPStatus: 201, MediaType: "application/json"}},
		3: {{OpRowID: 3, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		4: {{OpRowID: 4, Selector: "204", HTTPStatus: 204, MediaType: ""}},
	}
}

// itemsResource builds a confirmed *resources.Resource over the /items
// family. writeForm nil means POST is never taken over (R14's own table).
func itemsResource(writeForm *string, idField string, wrapper specs.Wrapper) *resources.Resource {
	return &resources.Resource{
		ID:          42,
		WorkspaceID: 1,
		RouteFamily: "/items",
		Name:        "items",
		IDField:     idField,
		IDStrategy:  "seq",
		Wrapper:     wrapper,
		WriteForm:   writeForm,
	}
}

func bareWriteForm() *string { return resTestStrPtr("bare") }

// resourceTestRuntime is fixtureRuntime plus a resources map with exactly
// one confirmed family, keyed exactly as buildRuntime keys it (RouteFamily).
func resourceTestRuntime(t *testing.T, settings domain.Settings, res *resources.Resource) *runtime {
	t.Helper()
	rt := fixtureRuntime(t, blankDoc, resourceTestRoutes(), resourceTestVariants(), settings)
	rt.resources = map[string]*resources.Resource{res.RouteFamily: res}
	return rt
}

// fakeEntityStore is this file's [EntityStore]: every method defers to an
// optional function field, defaulting to the harmless "nothing found/
// nothing stored" zero value so a test only wires the one method its case
// actually exercises.
type fakeEntityStore struct {
	mu sync.Mutex

	// Every function field takes base AHEAD of scope, D18.2's own order —
	// the base scope (P3h) and the route scope (P3e/P3g) are the two
	// independent axes [Repo.EntityStore] widens for.
	listFn   func(ctx context.Context, resourceID int64, base, scope resources.ScopeKey) ([]resources.Entity, error)
	getFn    func(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (resources.Entity, bool, error)
	createFn func(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, idField, idType string, data map[string]any) (resources.Entity, error)
	deleteFn func(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (bool, error)
	setFn    func(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey, idField, idType string, data map[string]any) (resources.Entity, bool, error)

	createCalls int
	deleteCalls int
	setCalls    int
	createArgs  []map[string]any
	setArgs     []map[string]any
}

func (f *fakeEntityStore) List(ctx context.Context, resourceID int64, base, scope resources.ScopeKey) ([]resources.Entity, error) {
	if f.listFn != nil {
		return f.listFn(ctx, resourceID, base, scope)
	}
	return nil, nil
}

func (f *fakeEntityStore) Get(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (resources.Entity, bool, error) {
	if f.getFn != nil {
		return f.getFn(ctx, resourceID, base, scope, entityKey)
	}
	return resources.Entity{}, false, nil
}

func (f *fakeEntityStore) Create(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, idField, idType string, data map[string]any) (resources.Entity, error) {
	f.mu.Lock()
	f.createCalls++
	f.createArgs = append(f.createArgs, data)
	f.mu.Unlock()
	if f.createFn != nil {
		return f.createFn(ctx, resourceID, base, scope, idField, idType, data)
	}
	return resources.Entity{}, errors.New("fakeEntityStore: createFn not set")
}

func (f *fakeEntityStore) Set(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey, idField, idType string, data map[string]any) (resources.Entity, bool, error) {
	f.mu.Lock()
	f.setCalls++
	f.setArgs = append(f.setArgs, data)
	f.mu.Unlock()
	if f.setFn != nil {
		return f.setFn(ctx, resourceID, base, scope, entityKey, idField, idType, data)
	}
	return resources.Entity{}, false, errors.New("fakeEntityStore: setFn not set")
}

func (f *fakeEntityStore) Delete(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (bool, error) {
	f.mu.Lock()
	f.deleteCalls++
	f.mu.Unlock()
	if f.deleteFn != nil {
		return f.deleteFn(ctx, resourceID, base, scope, entityKey)
	}
	return false, nil
}

// stubResourceSource is a [ResourceSource] whose ForWorkspace is never
// called by anything this file tests directly (rt.resources is set by hand
// on the fixture runtime, resourceTestRuntime above) — it exists purely so
// p.resources is non-nil, which resourceBranch's own first guard checks.
type stubResourceSource struct{}

func (stubResourceSource) ForWorkspace(context.Context, int64) ([]*resources.Resource, error) {
	return nil, nil
}

// resourceTestPlane wires both P3a sources onto a fresh Plane at the given
// MaxResponse.
func resourceTestPlane(maxResponse int64, store EntityStore) *Plane {
	p := New(runtimeTestConfig(maxResponse, 32), nil, nil, runtimeTestLogger())
	p.SetResources(stubResourceSource{})
	p.SetEntities(store)
	return p
}

// decodeAny decodes b with UseNumber (via jsonx, exactly like production),
// so a numeric field's kind (json.Number vs string vs float) is exactly
// what the wire carried — an ordinary json.Unmarshal into `any` would
// collapse every number to float64 and hide the very distinction several of
// this file's cases exist to check.
func decodeAny(t *testing.T, b []byte) any {
	t.Helper()
	dec := jsonx.NewDecoder(bytes.NewReader(b))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		t.Fatalf("decode %s: %v", b, err)
	}
	return v
}

// newPostRequest builds a POST request already carrying a REAL captured
// body — [captureRequestBody] plus [attachCapturedBody], the exact Step 5
// [Plane.serveResolved] performs before serveGenerated ever runs. Every
// test in this file calls serveGenerated directly (bypassing serveResolved
// entirely, respond_test.go's own established pattern), so without this a
// POST test would see [capturedBodyFromContext] return nil and every one of
// [resourceServePost]'s cases would look identical to "no body at all".
func newPostRequest(t *testing.T, url, contentType, body string) *http.Request {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	cb, err := captureRequestBody(httptest.NewRecorder(), req, requestBodyCap(64<<10))
	if err != nil {
		t.Fatalf("captureRequestBody: %v", err)
	}
	return attachCapturedBody(req, cb)
}

// --- GET X: collection, wrapper shapes, count type (clauses 19, 48, 49) ---

func TestResourceServeCollection_WrapperShapesAndCountType(t *testing.T) {
	entities := []resources.Entity{
		{Data: jsonx.RawMessage(`{"id":1,"name":"a"}`)},
		{Data: jsonx.RawMessage(`{"id":2,"name":"b"}`)},
	}

	tests := []struct {
		name     string
		wrapper  specs.Wrapper
		wantBody string // decoded and compared structurally, never byte-for-byte
	}{
		{
			// Clause 19: arrayKey nil means a BARE array 200, nothing else.
			name:     "bare array",
			wrapper:  specs.Wrapper{},
			wantBody: `[{"id":1,"name":"a"},{"id":2,"name":"b"}]`,
		},
		{
			// Clause 19: arrayKey non-nil wraps {items: rows, total: count}
			// and NOTHING else — no limit/offset/page the generator used to
			// echo before confirm.
			name:     "wrapped, no countKey",
			wrapper:  specs.Wrapper{ArrayKey: resTestStrPtr("items")},
			wantBody: `{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}]}`,
		},
		{
			// Clause 49's DEFAULT branch: gen.CountValue's own switch gives
			// int64(n) for an empty countType — a real JSON number, never a
			// bare `len(rows)` masquerading as one.
			name:     "wrapped, untyped countKey (default -> number)",
			wrapper:  specs.Wrapper{ArrayKey: resTestStrPtr("items"), CountKey: resTestStrPtr("total")},
			wantBody: `{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"total":2}`,
		},
		{
			// Clause 49's own headline case: a count property whose
			// resolved type IS "string" serves a STRINGIFIED total, exactly
			// as gen.CountValue("string", ...) already serves it for the
			// generator before a confirm — writing len(rows) directly (an
			// int) is exactly what this case catches.
			name:     "wrapped, countType=string -> stringified total",
			wrapper:  specs.Wrapper{ArrayKey: resTestStrPtr("items"), CountKey: resTestStrPtr("total"), CountType: "string"},
			wantBody: `{"items":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"total":"2"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := itemsResource(nil, "id", tt.wrapper)
			rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
			m := mustMatch(t, rt, http.MethodGet, "/items")

			store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
				return entities, nil
			}}
			p := resourceTestPlane(4<<20, store)

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
			got := decodeAny(t, rec.Body.Bytes())
			want := decodeAny(t, []byte(tt.wantBody))
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Errorf("body = %s, want (structurally) %s", rec.Body.String(), tt.wantBody)
			}
		})
	}
}

// TestResourceServePost_BodyIsStoreOutputVerbatim is clause 48's proof at
// THIS layer: whatever [EntityStore.Create] returns is what reaches the
// wire, unchanged — run once with an entity whose id is a JSON NUMBER and
// once where it is a JSON STRING, so a re-marshal or re-typing anywhere in
// this file's own path (as opposed to the store's, which R39/clause 48's
// own repo_test.go coverage already holds) would flip one into the other.
func TestResourceServePost_BodyIsStoreOutputVerbatim(t *testing.T) {
	tests := []struct {
		name       string
		entityData string
	}{
		{"integer id", `{"id":9007199254740993,"name":"x"}`},
		{"string id", `{"id":"abc-123","name":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
			rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
			m := mustMatch(t, rt, http.MethodPost, "/items")

			store := &fakeEntityStore{createFn: func(_ context.Context, _ int64, _, _ resources.ScopeKey, _, _ string, _ map[string]any) (resources.Entity, error) {
				return resources.Entity{Data: jsonx.RawMessage(tt.entityData)}, nil
			}}
			p := resourceTestPlane(4<<20, store)

			req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1,"name":"client-sent"}`)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
			}
			if got := decodeAny(t, rec.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(tt.entityData))) {
				t.Errorf("body = %s, want exactly the store's own %s (no re-typing)", rec.Body.String(), tt.entityData)
			}
		})
	}
}

// --- POST X: clauses 4, 16, 27, 38, 46 -------------------------------

// TestResourceServePost_ClientSentIDIsIgnored is clause 4 (id_field "id")
// and clause 27 (a non-"id" id_field, "userId") in one table: the branch
// passes res.IDField to Create verbatim — never a hardcoded "id" — and the
// response body is whatever Create returned, not the client's own
// data[idField]. The fake Create plays the store's real role here (the
// overwrite itself is R39/clause 4/27's own repo_test.go territory): it
// asserts the idField it was CALLED WITH is res.IDField, and returns an
// entity whose id is a server-assigned value the client never sent.
func TestResourceServePost_ClientSentIDIsIgnored(t *testing.T) {
	tests := []struct {
		name         string
		idField      string
		clientBody   string
		serverEntity string
	}{
		{"id_field is id (clause 4)", "id", `{"id":999,"name":"x"}`, `{"id":7,"name":"x"}`},
		{"id_field is userId (clause 27)", "userId", `{"userId":"zzz","name":"x"}`, `{"userId":7,"name":"x"}`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := itemsResource(bareWriteForm(), tt.idField, specs.Wrapper{IDType: "integer"})
			rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
			m := mustMatch(t, rt, http.MethodPost, "/items")

			var gotIDField string
			store := &fakeEntityStore{createFn: func(_ context.Context, _ int64, _, _ resources.ScopeKey, idField, _ string, _ map[string]any) (resources.Entity, error) {
				gotIDField = idField
				return resources.Entity{Data: jsonx.RawMessage(tt.serverEntity)}, nil
			}}
			p := resourceTestPlane(4<<20, store)

			req := newPostRequest(t, "http://alex.mock.local/items", "application/json", tt.clientBody)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if gotIDField != tt.idField {
				t.Errorf("Create called with idField %q, want res.IDField %q — clause 27's own defeat is a hardcoded \"id\"", gotIDField, tt.idField)
			}
			if rec.Code != http.StatusCreated {
				t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
			}
			if got := decodeAny(t, rec.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(tt.serverEntity))) {
				t.Errorf("body = %s, want the server-assigned %s — the client-sent id must never survive", rec.Body.String(), tt.serverEntity)
			}
		})
	}
}

// TestResourceServePost_MalformedBodyWritesNothingNo400 is clause 16: a
// malformed, oversized (simulated by the capture's own truncated flag), a
// non-object (array/scalar/null) or unparseable body all write nothing and
// answer with no 400 — R23's "the generator answers exactly as it would
// with no resource confirmed" (blankDoc's own empty-body-200 answer).
func TestResourceServePost_MalformedBodyWritesNothingNo400(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{"malformed JSON", "application/json", `{"id":`},
		{"a bare array", "application/json", `[1,2,3]`},
		{"a scalar", "application/json", `42`},
		{"null", "application/json", `null`},
		{"unparseable content type", "application/xml", `<id>1</id>`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
			rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
			m := mustMatch(t, rt, http.MethodPost, "/items")

			store := &fakeEntityStore{}
			p := resourceTestPlane(4<<20, store)

			req := newPostRequest(t, "http://alex.mock.local/items", tt.contentType, tt.body)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code == http.StatusBadRequest {
				t.Errorf("status = 400 — DESIGN §8 forbids the mock plane answering 400 over an unparseable body")
			}
			if store.createCalls != 0 {
				t.Errorf("Create was called %d times, want 0 — %s must write nothing", store.createCalls, tt.name)
			}
		})
	}
}

// TestResourceServePost_TruncatedBodyWritesNothingNo400 is clause 16's
// "oversized" case, which the malformed-body test's own doc comment claims
// but whose table above has no entry for: [captureRequestBody] (reqbody.go)
// only attempts [tryParseBody] when the capture did NOT truncate, so
// cb.parseOK stays false — the same "nothing to work with" parsePostBody
// sees for a genuinely malformed body — whenever cb.truncated is true. This
// constructs that outcome directly (cb.truncated: true) rather than posting
// a real 64KiB+ body, which would just make the test slow for no extra
// coverage of the branch actually under test.
func TestResourceServePost_TruncatedBodyWritesNothingNo400(t *testing.T) {
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodPost, "/items")

	store := &fakeEntityStore{}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodPost, "http://alex.mock.local/items", strings.NewReader(`{"id":1,"name":"x"}`))
	req.Header.Set("Content-Type", "application/json")
	req = attachCapturedBody(req, &capturedBody{bytes: []byte(`{"id":1`), truncated: true})

	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code == http.StatusBadRequest {
		t.Errorf("status = 400 — an oversized/truncated body must not be answered with 400 either")
	}
	if store.createCalls != 0 {
		t.Errorf("Create was called %d times, want 0 — a truncated body must write nothing", store.createCalls)
	}
}

// TestResourceServePost_IntegerAbove2Pow53RoundTrips is clause 38: the
// re-parse must be jsonx.NewDecoder(...).UseNumber(), never the capture's
// own float64 `parsed` value, or a POST carrying an integer above 2^53
// would read back with the wrong digits.
func TestResourceServePost_IntegerAbove2Pow53RoundTrips(t *testing.T) {
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodPost, "/items")

	const bigCount = "9223372036854775807" // math.MaxInt64, well past 2^53
	var gotData map[string]any
	store := &fakeEntityStore{createFn: func(_ context.Context, _ int64, _, _ resources.ScopeKey, idField, _ string, data map[string]any) (resources.Entity, error) {
		gotData = data
		// The stored entity echoes exactly what was decoded, id
		// overwritten by a value that carries no digits of its own —
		// this test is about the OTHER field surviving the round trip.
		body, err := jsonx.Marshal(map[string]any{"id": 1, "count": data["count"]})
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		return resources.Entity{Data: jsonx.RawMessage(body)}, nil
	}}
	p := resourceTestPlane(4<<20, store)

	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", fmt.Sprintf(`{"id":1,"count":%s}`, bigCount))
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusCreated {
		t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
	}
	num, ok := gotData["count"].(jsonx.Number)
	if !ok {
		t.Fatalf("data[\"count\"] decoded as %T, want jsonx.Number (UseNumber) — a float64 decode loses precision past 2^53", gotData["count"])
	}
	if num.String() != bigCount {
		t.Errorf("count = %s, want %s — the digits must survive the round trip", num.String(), bigCount)
	}
	if got := decodeAny(t, rec.Body.Bytes()).(map[string]any)["count"].(json.Number).String(); got != bigCount {
		t.Errorf("response count = %s, want %s", got, bigCount)
	}
}

// TestResourceServePost_LiveForcedNoContentWritesNothing is clause 46: a
// session force of 204 on a bare-write_form family answers 204 and leaves
// the entity store untouched — the carrier is liveEffect.Status (passed
// straight from serveGenerated), never rv.StatusSource, which reads
// "default" for a live force and would never catch this if used instead.
func TestResourceServePost_LiveForcedNoContentWritesNothing(t *testing.T) {
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	// The variant table (resourceTestVariants) declares 201 for POST but
	// nothing for 204 — resolveVariant's own synthetic-no-body-variant
	// fallback answers a forced status the document never declared with an
	// honest empty body, which is exactly the shape a live-forced 204 takes
	// here too.
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodPost, "/items")

	store := &fakeEntityStore{}
	p := resourceTestPlane(4<<20, store)
	p.SetLiveState(fixedEffectSource{eff: livestate.Effect{Status: http.StatusNoContent}})

	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204 (the live force)", rec.Code)
	}
	if store.createCalls != 0 {
		t.Errorf("Create was called %d times, want 0 — a forced 204 must write nothing", store.createCalls)
	}
}

// fixedEffectSource is a [LiveStateSource] that always answers the same
// [livestate.Effect] — this file's minimal stand-in, since livestate.go's
// own test fakes live in a different, black-box _test package this
// white-box file cannot import.
type fixedEffectSource struct{ eff livestate.Effect }

func (f fixedEffectSource) Apply(int64, string, string) livestate.Effect { return f.eff }
func (f fixedEffectSource) Set(int64, livestate.Directive) error         { return nil }
func (f fixedEffectSource) List(int64) []livestate.Directive             { return nil }
func (f fixedEffectSource) Clear(int64) int                              { return 0 }
func (f fixedEffectSource) Delete(int64, livestate.Target, livestate.Action) (int, error) {
	return 0, nil
}

// --- POST/DELETE: clause 47 (non-numeric selector stops writes, not reads) --

// TestResourceBranch_NonNumericSelectorStopsWritesNotReads is clause 47: a
// family whose success responses are declared "2XX" still serves both GETs
// from storage, while POST/DELETE fall through to the generator — the
// numeric conjunct applies to the WRITE verbs only.
func TestResourceBranch_NonNumericSelectorStopsWritesNotReads(t *testing.T) {
	routes := resourceTestRoutes()
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "2XX", HTTPStatus: 200, MediaType: "application/json"}},
		2: {{OpRowID: 2, Selector: "2XX", HTTPStatus: 200, MediaType: "application/json"}},
		3: {{OpRowID: 3, Selector: "2XX", HTTPStatus: 200, MediaType: "application/json"}},
		4: {{OpRowID: 4, Selector: "2XX", HTTPStatus: 200, MediaType: "application/json"}},
	}
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := fixtureRuntime(t, blankDoc, routes, variants, domain.DefaultSettings())
	rt.resources = map[string]*resources.Resource{res.RouteFamily: res}

	store := &fakeEntityStore{
		listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return []resources.Entity{{Data: jsonx.RawMessage(`{"id":1}`)}}, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	// GET X still serves from storage.
	mGet := mustMatch(t, rt, http.MethodGet, "/items")
	recGet := httptest.NewRecorder()
	p.serveGenerated(recGet, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, mGet, resources.ScopeKey(""))
	if got := decodeAny(t, recGet.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(`[{"id":1}]`))) {
		t.Errorf("GET X body = %s, want the stored row — a 2XX selector must not stop the READS", recGet.Body.String())
	}

	// POST X falls through to the generator: no Create call.
	mPost := mustMatch(t, rt, http.MethodPost, "/items")
	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
	recPost := httptest.NewRecorder()
	p.serveGenerated(recPost, req, respondTestWorkspace(), rt, mPost, resources.ScopeKey(""))
	if store.createCalls != 0 {
		t.Errorf("Create was called on a 2XX-selector POST, want 0 — the numeric conjunct must stop the WRITE")
	}

	// DELETE X/{} falls through to the generator: no Delete call.
	mDel := mustMatch(t, rt, http.MethodDelete, "/items/1")
	recDel := httptest.NewRecorder()
	p.serveGenerated(recDel, httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/items/1", nil), respondTestWorkspace(), rt, mDel, resources.ScopeKey(""))
	if store.deleteCalls != 0 {
		t.Errorf("Delete was called on a 2XX-selector DELETE, want 0 — the numeric conjunct must stop the WRITE")
	}
}

// TestResourceBranch_ActiveStatusForcedToUndeclaredStatusStopsWrites is a
// review finding's own scenario, not the "2XX"/"default" case
// TestResourceBranch_NonNumericSelectorStopsWritesNotReads already covers:
// an active_status override forces POST/DELETE to a status the spec
// operation declares NO response for at all. resolveVariant answers that
// with its own SYNTHETIC gen.ResponseVariant (respond.go's forced-status
// fallback), whose Selector is the zero value "" — a THIRD non-literal form
// isSyntheticSelector must reject alongside "2XX"/"default", or a
// write-verb takeover fires under a status the document never sanctioned
// (D6 rule 5's write-verb numeric conjunct exists to prevent exactly this).
// Concretely, before this guard covered "": a DELETE X/{} forced to an
// undeclared 204 would pass resourceBranch's rule-4 gate (204 is 2xx) and
// resourceServeDelete's own guard, and actually delete the row — the same
// failure shape TestResourceServePost_LiveForcedNoContentWritesNothing
// proves for a LIVE force, now proven for an active_status force too.
func TestResourceBranch_ActiveStatusForcedToUndeclaredStatusStopsWrites(t *testing.T) {
	routes := resourceTestRoutes()
	// Neither write verb's variant table declares the status active_status
	// is about to force (204): POST only has 201, DELETE only has 200 (with
	// a body) — no 204 anywhere in this table.
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		2: {{OpRowID: 2, Selector: "201", HTTPStatus: 201, MediaType: "application/json"}},
		3: {{OpRowID: 3, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		4: {{OpRowID: 4, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
	}
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	forced := http.StatusNoContent
	rows := map[string]*overrides.Row{
		overrides.OpKey(http.MethodPost, "/items"):        {OverrideOn: true, ActiveStatus: &forced},
		overrides.OpKey(http.MethodDelete, "/items/{id}"): {OverrideOn: true, ActiveStatus: &forced},
	}
	rt := fixtureRuntimeWithOverrides(t, blankDoc, routes, variants, domain.DefaultSettings(), rows)
	rt.resources = map[string]*resources.Resource{res.RouteFamily: res}
	store := &fakeEntityStore{}
	p := resourceTestPlane(4<<20, store)

	// POST X: active_status forces 204, which POST's own variant table
	// never declares — must fall through to the generator, no Create.
	mPost := mustMatch(t, rt, http.MethodPost, "/items")
	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
	recPost := httptest.NewRecorder()
	p.serveGenerated(recPost, req, respondTestWorkspace(), rt, mPost, resources.ScopeKey(""))
	if recPost.Code != http.StatusNoContent {
		t.Fatalf("POST status = %d, want 204 (the active_status force still takes effect)", recPost.Code)
	}
	if store.createCalls != 0 {
		t.Errorf("Create was called %d times on a POST forced to an undeclared status via active_status, want 0", store.createCalls)
	}

	// DELETE X/{}: same forced, undeclared status — must fall through, no
	// Get/Delete at all (the guard fires before either is reached).
	mDel := mustMatch(t, rt, http.MethodDelete, "/items/1")
	recDel := httptest.NewRecorder()
	p.serveGenerated(recDel, httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/items/1", nil), respondTestWorkspace(), rt, mDel, resources.ScopeKey(""))
	if recDel.Code != http.StatusNoContent {
		t.Fatalf("DELETE status = %d, want 204 (the active_status force still takes effect)", recDel.Code)
	}
	if store.deleteCalls != 0 {
		t.Errorf("Delete was called %d times on a DELETE forced to an undeclared status via active_status, want 0", store.deleteCalls)
	}
}

// --- GET/DELETE X/{}: clauses 21, 34, 35, 36 ---------------------------

// TestResourceServeDetail_MissIsOwnEnvelope is clause 21: a miss answers
// this branch's OWN 404 (entity_not_found), never serveNoRoute's "no route"
// body (the route plainly exists) and never settings.NotFoundBody (that
// setting is for an UNROUTED path).
func TestResourceServeDetail_MissIsOwnEnvelope(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	settings := domain.DefaultSettings()
	settings.NotFoundBody = json.RawMessage(`{"custom":"not found"}`)
	rt := resourceTestRuntime(t, settings, res)
	m := mustMatch(t, rt, http.MethodGet, "/items/99")

	store := &fakeEntityStore{} // getFn unset: Get returns not-found
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items/99", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	var body httpx.ErrorBody
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode error body: %v; body=%s", err, rec.Body)
	}
	if body.Error.Code != "entity_not_found" {
		t.Errorf("error.code = %q, want entity_not_found", body.Error.Code)
	}
	if strings.Contains(rec.Body.String(), "custom") {
		t.Errorf("body = %s, want the branch's own envelope, not settings.NotFoundBody", rec.Body.String())
	}
	if strings.Contains(strings.ToLower(rec.Body.String()), "no route") {
		t.Errorf("body = %s, want entity_not_found, not serveNoRoute's \"no route\" lie (the route matched)", rec.Body.String())
	}
}

// TestResourceBranch_VanishedResourceFallsThrough is clause 34: a resource
// declined mid-request (its DB row gone) makes Create fail with
// [resources.ErrResourceGone] — the branch treats that as "not taken over"
// and lets the request fall through to the generator, never a 500.
func TestResourceBranch_VanishedResourceFallsThrough(t *testing.T) {
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodPost, "/items")

	store := &fakeEntityStore{createFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, map[string]any) (resources.Entity, error) {
		return resources.Entity{}, resources.ErrResourceGone
	}}
	p := resourceTestPlane(4<<20, store)

	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code >= 500 {
		t.Fatalf("status = %d, want never a 500 for a vanished resource", rec.Code)
	}
	if rec.Code == http.StatusConflict || rec.Code == http.StatusServiceUnavailable {
		t.Errorf("status = %d, want the ordinary generator answer (blankDoc's empty 200), not a resource refusal — the resource is gone, not full", rec.Code)
	}
}

// TestResourceBranch_406GateRunsFirst is clause 35: POST with an Accept
// header that excludes the variant's media type answers 406 and never
// reaches Create — the branch sits AFTER the 406 gate in serveGenerated.
func TestResourceBranch_406GateRunsFirst(t *testing.T) {
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodPost, "/items")

	store := &fakeEntityStore{}
	p := resourceTestPlane(4<<20, store)

	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
	req.Header.Set("Accept", "text/plain") // excludes the variant's application/json
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406; body=%s", rec.Code, rec.Body)
	}
	if store.createCalls != 0 {
		t.Errorf("Create was called %d times, want 0 — a 406 must never create a row first", store.createCalls)
	}
}

// TestResourceBranch_RefusalEnvelopeShape is clause 36: entity_limit and
// write_busy are the tree's ORDINARY error envelope — no workspace
// envelope, no declared headers — the same shape clause 21's 404 already
// carries.
func TestResourceBranch_RefusalEnvelopeShape(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	tests := []struct {
		name     string
		err      error
		wantCode int
		wantErr  string
	}{
		{"entity_limit", resources.ErrEntityLimit, http.StatusConflict, "entity_limit"},
		{"write_busy", resources.ErrWriteBusy, http.StatusServiceUnavailable, "write_busy"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
			rt := resourceTestRuntime(t, settings, res)
			m := mustMatch(t, rt, http.MethodPost, "/items")

			store := &fakeEntityStore{createFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, map[string]any) (resources.Entity, error) {
				return resources.Entity{}, tt.err
			}}
			p := resourceTestPlane(4<<20, store)

			req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":1}`)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != tt.wantCode {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantCode, rec.Body)
			}
			var body httpx.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode: %v; body=%s (want the plain error envelope, not the \"data\" workspace envelope)", err, rec.Body)
			}
			if body.Error.Code != tt.wantErr {
				t.Errorf("error.code = %q, want %q", body.Error.Code, tt.wantErr)
			}
		})
	}
}

// TestResourceBranch_EnvelopeWrapsStoredBody is clause 20's positive half —
// TestResourceBranch_RefusalEnvelopeShape right above proves only the
// OPPOSITE (a REFUSAL is not wrapped); this proves a SUCCESSFUL resource
// body — GET X, GET X/{} and POST X, all three of clause 20's own
// "stored body" cases — IS, exactly like a generator-produced one already
// was before this branch existed. assembleResponse's rv.PreBuilt case
// (respond.go) sets `body` and falls through to the SAME envelope-wrap code
// every other case shares; this is the test that would catch a future
// special case placed ahead of it that skips the wrap for PreBuilt alone.
func TestResourceBranch_EnvelopeWrapsStoredBody(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	t.Run("GET collection", func(t *testing.T) {
		res := itemsResource(nil, "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, settings, res)
		m := mustMatch(t, rt, http.MethodGet, "/items")

		entities := []resources.Entity{{Data: jsonx.RawMessage(`{"id":1,"name":"a"}`)}}
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return entities, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		got := decodeAny(t, rec.Body.Bytes())
		want := decodeAny(t, []byte(`{"data":[{"id":1,"name":"a"}]}`))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("body = %s, want wrapped under the configured envelope: %s", rec.Body.String(), `{"data":[{"id":1,"name":"a"}]}`)
		}
	})

	t.Run("GET detail", func(t *testing.T) {
		res := itemsResource(nil, "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, settings, res)
		m := mustMatch(t, rt, http.MethodGet, "/items/1")

		store := &fakeEntityStore{getFn: func(_ context.Context, _ int64, _, _ resources.ScopeKey, key string) (resources.Entity, bool, error) {
			if key == "1" {
				return resources.Entity{Data: jsonx.RawMessage(`{"id":1,"name":"a"}`)}, true, nil
			}
			return resources.Entity{}, false, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items/1", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
		}
		got := decodeAny(t, rec.Body.Bytes())
		want := decodeAny(t, []byte(`{"data":{"id":1,"name":"a"}}`))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("body = %s, want wrapped under the configured envelope: %s", rec.Body.String(), `{"data":{"id":1,"name":"a"}}`)
		}
	})

	t.Run("POST", func(t *testing.T) {
		res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, settings, res)
		m := mustMatch(t, rt, http.MethodPost, "/items")

		store := &fakeEntityStore{createFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, map[string]any) (resources.Entity, error) {
			return resources.Entity{Data: jsonx.RawMessage(`{"id":7,"name":"x"}`)}, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"name":"x"}`)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusCreated {
			t.Fatalf("status = %d, want 201; body=%s", rec.Code, rec.Body)
		}
		got := decodeAny(t, rec.Body.Bytes())
		want := decodeAny(t, []byte(`{"data":{"id":7,"name":"x"}}`))
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("body = %s, want wrapped under the configured envelope: %s", rec.Body.String(), `{"data":{"id":7,"name":"x"}}`)
		}
	})
}

// --- clause 11: the per-method switch is per method, not per family -------

// TestResourceBranch_WriteFormNil_POSTFromGeneratorBothGETsFromStorage is
// clause 11: a write_form=NULL resource still answers both GETs from
// storage while POST falls through to the generator untouched.
func TestResourceBranch_WriteFormNil_POSTFromGeneratorBothGETsFromStorage(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)

	store := &fakeEntityStore{
		listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return []resources.Entity{{Data: jsonx.RawMessage(`{"id":1}`)}}, nil
		},
		getFn: func(_ context.Context, _ int64, _, _ resources.ScopeKey, key string) (resources.Entity, bool, error) {
			if key == "1" {
				return resources.Entity{Data: jsonx.RawMessage(`{"id":1}`)}, true, nil
			}
			return resources.Entity{}, false, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	mList := mustMatch(t, rt, http.MethodGet, "/items")
	recList := httptest.NewRecorder()
	p.serveGenerated(recList, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, mList, resources.ScopeKey(""))
	if got := decodeAny(t, recList.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(`[{"id":1}]`))) {
		t.Errorf("GET X body = %s, want the stored row", recList.Body.String())
	}

	mDetail := mustMatch(t, rt, http.MethodGet, "/items/1")
	recDetail := httptest.NewRecorder()
	p.serveGenerated(recDetail, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items/1", nil), respondTestWorkspace(), rt, mDetail, resources.ScopeKey(""))
	if got := decodeAny(t, recDetail.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(`{"id":1}`))) {
		t.Errorf("GET X/1 body = %s, want the stored row", recDetail.Body.String())
	}

	mPost := mustMatch(t, rt, http.MethodPost, "/items")
	req := newPostRequest(t, "http://alex.mock.local/items", "application/json", `{"id":2}`)
	recPost := httptest.NewRecorder()
	p.serveGenerated(recPost, req, respondTestWorkspace(), rt, mPost, resources.ScopeKey(""))
	if store.createCalls != 0 {
		t.Errorf("Create was called %d times on a write_form=NULL family, want 0", store.createCalls)
	}
}

// --- clause 10: precedence — one observation per line of R21 --------------

// TestResourceBranch_Precedence covers the two lines of R21 that are
// resourceBranch's own responsibility to get right (the others — route_off,
// a session directive, a custom endpoint — are inherited unchanged from
// code this slice does not touch, and are already covered by this package's
// existing P1c/P1c2 tests).
func TestResourceBranch_Precedence(t *testing.T) {
	t.Run("active_status beats the resource: not-2xx means not consulted at all", func(t *testing.T) {
		res := itemsResource(nil, "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
		activeStatus := 404
		rt.overrides = map[string]*overrides.Row{
			overrides.OpKey("GET", "/items"): {OverrideOn: true, ActiveStatus: &activeStatus},
		}
		m := mustMatch(t, rt, http.MethodGet, "/items")

		listCalled := false
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			listCalled = true
			return nil, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != 404 {
			t.Fatalf("status = %d, want 404 (active_status, not the resource)", rec.Code)
		}
		if listCalled {
			t.Errorf("EntityStore.List was called — a non-2xx active_status must mean the resource is never consulted at all")
		}
	})

	t.Run("an inert override_on=false row does NOT beat the resource", func(t *testing.T) {
		res := itemsResource(nil, "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
		activeStatus := 404
		rt.overrides = map[string]*overrides.Row{
			// OverrideOn is false: overrideActive gates this row off
			// end-to-end (respond.go's own HARD RULE 6 extension) — an
			// operation with no row at all and this inert one must behave
			// identically, so the resource keeps serving.
			overrides.OpKey("GET", "/items"): {OverrideOn: false, ActiveStatus: &activeStatus},
		}
		m := mustMatch(t, rt, http.MethodGet, "/items")

		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return []resources.Entity{{Data: jsonx.RawMessage(`{"id":1}`)}}, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusOK {
			t.Fatalf("status = %d, want 200 (the resource still serves); body=%s", rec.Code, rec.Body)
		}
		if got := decodeAny(t, rec.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(`[{"id":1}]`))) {
			t.Errorf("body = %s, want the stored row — an OverrideOn=false row must be inert, not a beat", rec.Body.String())
		}
	})

	t.Run("a pinned variant beats the resource", func(t *testing.T) {
		res := itemsResource(nil, "id", specs.Wrapper{})
		rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
		rt.overrides = map[string]*overrides.Row{
			overrides.OpKey("GET", "/items"): {
				OverrideOn: true,
				Responses: map[string]overrides.Variant{
					"200": {Mode: "pinned", MediaType: "application/json", Body: jsonx.RawMessage(`{"pinned":true}`)},
				},
			},
		}
		m := mustMatch(t, rt, http.MethodGet, "/items")

		listCalled := false
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			listCalled = true
			return nil, nil
		}}
		p := resourceTestPlane(4<<20, store)

		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if listCalled {
			t.Errorf("EntityStore.List was called — a pinned variant must win before the resource is ever consulted")
		}
		if !strings.Contains(rec.Body.String(), "pinned") {
			t.Errorf("body = %s, want the pinned body", rec.Body.String())
		}
	})
}

// --- clause 22: the carve-out — ?limit=1 is ignored ------------------------

func TestResourceServeCollection_QueryParamsAreIgnored(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodGet, "/items")

	entities := []resources.Entity{
		{Data: jsonx.RawMessage(`{"id":1}`)},
		{Data: jsonx.RawMessage(`{"id":2}`)},
		{Data: jsonx.RawMessage(`{"id":3}`)},
	}
	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return entities, nil
	}}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items?limit=1&offset=0", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	var got []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if len(got) != 3 {
		t.Errorf("len(rows) = %d, want 3 — a declared ?limit=1 must be ignored (D14's own carve-out), not honoured", len(got))
	}
}

// --- clauses 33, 50: the byte cap -----------------------------------------

// TestResourceServeCollection_ByteCap is clauses 33 (still answers AT the
// cap) and 50 (refuses over it, with a NON-EMPTY body — never the pinned
// branch's empty 200, which on a collection route reads as "no rows").
func TestResourceServeCollection_ByteCap(t *testing.T) {
	entities := []resources.Entity{
		{Data: jsonx.RawMessage(`{"id":1,"pad":"0123456789012345678901234567890123456789"}`)},
		{Data: jsonx.RawMessage(`{"id":2,"pad":"0123456789012345678901234567890123456789"}`)},
	}
	res := itemsResource(nil, "id", specs.Wrapper{})
	body, err := marshalCollection(res, entities)
	if err != nil {
		t.Fatalf("marshalCollection: %v", err)
	}
	exact := int64(len(body))

	tests := []struct {
		name       string
		maxResp    int64
		wantStatus int
	}{
		{"exactly at the cap", exact, http.StatusOK},
		{"one byte under the body", exact - 1, http.StatusConflict},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
			m := mustMatch(t, rt, http.MethodGet, "/items")

			store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
				return entities, nil
			}}
			p := resourceTestPlane(tt.maxResp, store)

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
			if tt.wantStatus == http.StatusConflict {
				if rec.Body.Len() == 0 {
					t.Errorf("collection_too_large body is empty, want a non-empty error body — an empty 200-shaped answer would read as \"no rows\"")
				}
				var eb httpx.ErrorBody
				if err := json.Unmarshal(rec.Body.Bytes(), &eb); err != nil || eb.Error.Code != "collection_too_large" {
					t.Errorf("body = %s, want error.code=collection_too_large", rec.Body)
				}
			}
		})
	}
}

// --- clause 44: HEAD X and GET X agree -------------------------------------

func TestResourceServeCollection_HEADMatchesGETContentLength(t *testing.T) {
	entities := []resources.Entity{{Data: jsonx.RawMessage(`{"id":1,"name":"a"}`)}}
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodHead, "/items")
	if m.Route.Method != http.MethodGet {
		t.Fatalf("matched route method = %q, want GET (HEAD resolves against the GET route)", m.Route.Method)
	}

	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return entities, nil
	}}
	p := resourceTestPlane(4<<20, store)

	// GET, for the reference length.
	recGet := httptest.NewRecorder()
	p.serveGenerated(recGet, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/items"), resources.ScopeKey(""))
	wantLen := recGet.Header().Get("Content-Length")
	if wantLen == "" || wantLen == "0" {
		t.Fatalf("GET Content-Length = %q, want the real body length", wantLen)
	}

	// HEAD, through headWriter exactly as serveResolved wires it.
	recHead := httptest.NewRecorder()
	hw := &headWriter{ResponseWriter: recHead}
	p.serveGenerated(hw, httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if got := recHead.Header().Get("Content-Length"); got != wantLen {
		t.Errorf("HEAD Content-Length = %q, want %q (must agree with GET)", got, wantLen)
	}
	if recHead.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", recHead.Body.String())
	}

	// Both refuse together when the collection is over the ceiling.
	overCap := int64(len(recGet.Body.Bytes())) - 1
	pSmall := resourceTestPlane(overCap, store)

	recGetOver := httptest.NewRecorder()
	pSmall.serveGenerated(recGetOver, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, mustMatch(t, rt, http.MethodGet, "/items"), resources.ScopeKey(""))
	recHeadOver := httptest.NewRecorder()
	hwOver := &headWriter{ResponseWriter: recHeadOver}
	pSmall.serveGenerated(hwOver, httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if recGetOver.Code != http.StatusConflict || recHeadOver.Code != http.StatusConflict {
		t.Errorf("GET status=%d HEAD status=%d, want both 409 — a HEAD short-circuit would skip the byte-cap refusal", recGetOver.Code, recHeadOver.Code)
	}
}

// --- DELETE X/{}: exercised through the full switch above already covers
// clauses 34/35/36 for POST; this pair covers the DELETE-specific shapes.

// TestResourceServeDelete_SuccessfulBodiedDeleteCarriesTheDeletedEntity
// proves DELETE's own body rule from D7: a declared 200/202 hands back the
// entity that was just deleted.
func TestResourceServeDelete_SuccessfulBodiedDeleteCarriesTheDeletedEntity(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	// Override the DELETE variant (OpRowID 4) to a 200 with a body, instead
	// of the fixture's default 204/NoBody.
	rt.variants[4] = []gen.ResponseVariant{{OpRowID: 4, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}}
	m := mustMatch(t, rt, http.MethodDelete, "/items/1")

	store := &fakeEntityStore{
		getFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error) {
			return resources.Entity{Data: jsonx.RawMessage(`{"id":1,"name":"gone"}`)}, true, nil
		},
		deleteFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (bool, error) {
			return true, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/items/1", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := decodeAny(t, rec.Body.Bytes()); fmt.Sprint(got) != fmt.Sprint(decodeAny(t, []byte(`{"id":1,"name":"gone"}`))) {
		t.Errorf("body = %s, want the deleted entity", rec.Body.String())
	}
	if store.deleteCalls != 1 {
		t.Errorf("Delete was called %d times, want 1", store.deleteCalls)
	}
}

func TestResourceServeDelete_NoContentWritesNoPreBuilt(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodDelete, "/items/1")

	store := &fakeEntityStore{
		getFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error) {
			return resources.Entity{Data: jsonx.RawMessage(`{"id":1}`)}, true, nil
		},
		deleteFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (bool, error) {
			return true, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/items/1", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (a declared 204 relies on rv.NoBody, never PreBuilt)", rec.Body.String())
	}
	if store.deleteCalls != 1 {
		t.Errorf("Delete was called %d times, want 1", store.deleteCalls)
	}
}

// --- P3e (D13 P6, P14): the anchor check and the arity check on their own,
// beyond the two GET routes every test above this point exercises with an
// unscoped (top-level) family -------------------------------------------

// nestedTestRoutes is a small nested family — GET/POST /orgs/{orgId}/users,
// GET/DELETE /orgs/{orgId}/users/{id} — one level under a parent this file's
// tests never route to directly (D6.3's anchor check reads the parent
// through [EntityStore.Get] and rt.resources, never through a route of its
// own).
func nestedTestRoutes() []router.Route {
	return []router.Route{
		{OpRowID: 1, Method: http.MethodGet, Path: "/orgs/{orgId}/users", CanonicalPath: "/orgs/{}/users", SourceOrder: 1},
		{OpRowID: 2, Method: http.MethodPost, Path: "/orgs/{orgId}/users", CanonicalPath: "/orgs/{}/users", SourceOrder: 2},
		{OpRowID: 3, Method: http.MethodGet, Path: "/orgs/{orgId}/users/{id}", CanonicalPath: "/orgs/{}/users/{}", SourceOrder: 3},
		{OpRowID: 4, Method: http.MethodDelete, Path: "/orgs/{orgId}/users/{id}", CanonicalPath: "/orgs/{}/users/{}", SourceOrder: 4},
	}
}

func nestedTestVariants() map[int64][]gen.ResponseVariant {
	return map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		2: {{OpRowID: 2, Selector: "201", HTTPStatus: 201, MediaType: "application/json"}},
		3: {{OpRowID: 3, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		4: {{OpRowID: 4, Selector: "204", HTTPStatus: 204, MediaType: ""}},
	}
}

// nestedOrgResource is the PARENT family, confirmed but never routed to
// directly in this file — D6.3's anchor check reaches it through
// rt.resources and [EntityStore.Get] alone.
func nestedOrgResource() *resources.Resource {
	return &resources.Resource{ID: 1, WorkspaceID: 1, RouteFamily: "/orgs", Name: "orgs", IDField: "id", IDStrategy: "seq"}
}

// nestedUserResource is the CHILD family, one level under /orgs, ScopeParams
// exactly D5.6's own example — the outer parameter's NAME the detail route
// declared it under, never consulted by scopeOf itself (D5.6's positional
// rule), only by the arity check's length.
func nestedUserResource(writeForm *string) *resources.Resource {
	return &resources.Resource{
		ID: 2, WorkspaceID: 1, RouteFamily: "/orgs/{}/users", Name: "orgUsers",
		IDField: "id", IDStrategy: "seq", ScopeParams: []string{"orgId"}, WriteForm: writeForm,
	}
}

func nestedTestRuntime(t *testing.T, settings domain.Settings, org, user *resources.Resource) *runtime {
	t.Helper()
	rt := fixtureRuntime(t, blankDoc, nestedTestRoutes(), nestedTestVariants(), settings)
	rt.resources = map[string]*resources.Resource{org.RouteFamily: org, user.RouteFamily: user}
	return rt
}

// TestResourceBranch_AnchorCheck_POSTAndDELETE is P6: D6.3's 404 is armed on
// ALL FOUR verbs, not just the two GETs every other test in this file
// exercises with an unscoped family. Mutation: perform the anchor check on
// the detail route only (gate it on isDetail instead of len(outer) — this
// file's own resource.go comment names exactly that mutation). Under it,
// POST into an orphaned scope would reach [EntityStore.Create] and DELETE
// into one would reach [EntityStore.Get]/[EntityStore.Delete] for the
// CHILD — this test's fakeEntityStore fails the test outright the moment
// either happens, rather than merely asserting a call count of zero
// afterward.
func TestResourceBranch_AnchorCheck_POSTAndDELETE(t *testing.T) {
	org := nestedOrgResource()
	user := nestedUserResource(bareWriteForm())
	rt := nestedTestRuntime(t, domain.DefaultSettings(), org, user)

	store := &fakeEntityStore{
		getFn: func(_ context.Context, resourceID int64, _, _ resources.ScopeKey, _ string) (resources.Entity, bool, error) {
			if resourceID == org.ID {
				// No live org row anchors ANY key: every parent lookup misses.
				return resources.Entity{}, false, nil
			}
			t.Fatalf("EntityStore.Get called on the CHILD (resourceID=%d) — the anchor check must refuse before the child is ever reached", resourceID)
			return resources.Entity{}, false, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	t.Run("POST", func(t *testing.T) {
		m := mustMatch(t, rt, http.MethodPost, "/orgs/999/users")
		req := newPostRequest(t, "http://alex.mock.local/orgs/999/users", "application/json", `{"name":"x"}`)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v; body=%s", err, rec.Body)
		}
		if body.Error.Code != "entity_not_found" {
			t.Errorf("error.code = %q, want entity_not_found", body.Error.Code)
		}
		if store.createCalls != 0 {
			t.Errorf("EntityStore.Create was called %d times — the anchor check must refuse a scope no live parent row anchors BEFORE any write", store.createCalls)
		}
	})

	t.Run("DELETE", func(t *testing.T) {
		m := mustMatch(t, rt, http.MethodDelete, "/orgs/999/users/1")
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/orgs/999/users/1", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))

		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v; body=%s", err, rec.Body)
		}
		if body.Error.Code != "entity_not_found" {
			t.Errorf("error.code = %q, want entity_not_found", body.Error.Code)
		}
		if store.deleteCalls != 0 {
			t.Errorf("EntityStore.Delete was called %d times — the anchor check must refuse a scope no live parent row anchors BEFORE any write", store.deleteCalls)
		}
	})
}

// TestResourceBranch_ScopeArityMismatch_DeclinesToGenerator is P14,
// decisions.md's own pinned fixture verbatim: the EXISTING top-level
// /items resource (no outer path parameter at all — outer is empty) with
// ScopeParams set to ["orgId"] and nothing else changed, so the arity
// check's two lengths (0 and 1) disagree. Mutation: delete the arity check
// from scopeOf; outer stays empty either way, so D6.3's anchor check stays
// DISARMED (armed on len(outer), never len(res.ScopeParams) — resource.go's
// own comment), the request reaches the store under scope "", and it would
// serve the store's row instead of declining to the generator. This test's
// fakeEntityStore.List would return a body containing "stored-only" if it
// were EVER consulted; the correct implementation never calls it at all.
func TestResourceBranch_ScopeArityMismatch_DeclinesToGenerator(t *testing.T) {
	res := itemsResource(nil, "id", specs.Wrapper{})
	res.ScopeParams = []string{"orgId"} // decisions.md's own P14 fixture
	rt := resourceTestRuntime(t, domain.DefaultSettings(), res)
	m := mustMatch(t, rt, http.MethodGet, "/items")

	listCalled := false
	store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		listCalled = true
		return []resources.Entity{{Data: jsonx.RawMessage(`{"id":"stored-only"}`)}}, nil
	}}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the generator, not a refusal); body=%s", rec.Code, rec.Body)
	}
	if listCalled {
		t.Errorf("EntityStore.List was called — a scope-arity mismatch must decline to the generator before the store is EVER consulted")
	}
	// blankDoc's GET /items variant carries no SchemaPtr, so gen.Body
	// itself answers nil, nil (internal/gen/gen.go's own `v.SchemaPtr ==
	// ""` early return) — the generated body is empty, which is exactly
	// what tells this apart from the store's row.
	if rec.Body.Len() != 0 {
		t.Errorf("body = %s, want empty (the GENERATED body, not the store's row)", rec.Body.String())
	}
}

// --- P3g (D13 P9, P10, P16): the ancestor walk at depth 2 --------------
//
// nestedDeepTestRoutes is a depth-2 family — GET/POST
// /orgs/{orgId}/teams/{teamId}/users, GET/DELETE
// /orgs/{organizationId}/teams/{team}/users/{id} — two levels under two
// parents this file's tests never route to directly, the same shape
// nestedTestRoutes above gives one level (D6.2's anchor walk reads an
// ancestor through [EntityStore.Get] and rt.resources alone, never through
// a route of its own).
func nestedDeepTestRoutes() []router.Route {
	return []router.Route{
		{OpRowID: 1, Method: http.MethodGet, Path: "/orgs/{orgId}/teams/{teamId}/users", CanonicalPath: "/orgs/{}/teams/{}/users", SourceOrder: 1},
		{OpRowID: 2, Method: http.MethodPost, Path: "/orgs/{orgId}/teams/{teamId}/users", CanonicalPath: "/orgs/{}/teams/{}/users", SourceOrder: 2},
		{OpRowID: 3, Method: http.MethodGet, Path: "/orgs/{organizationId}/teams/{team}/users/{id}", CanonicalPath: "/orgs/{}/teams/{}/users/{}", SourceOrder: 3},
		{OpRowID: 4, Method: http.MethodDelete, Path: "/orgs/{organizationId}/teams/{team}/users/{id}", CanonicalPath: "/orgs/{}/teams/{}/users/{}", SourceOrder: 4},
	}
}

func nestedDeepTestVariants() map[int64][]gen.ResponseVariant {
	return map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		2: {{OpRowID: 2, Selector: "201", HTTPStatus: 201, MediaType: "application/json"}},
		3: {{OpRowID: 3, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
		4: {{OpRowID: 4, Selector: "204", HTTPStatus: 204, MediaType: ""}},
	}
}

// nestedDeepOrgResource and nestedDeepTeamResource are the ROOT and MIDDLE
// ancestors, confirmed but never routed to directly in this file — the
// anchor walk reaches both through rt.resources and [EntityStore.Get]
// alone, exactly as nestedOrgResource does for one level.
func nestedDeepOrgResource() *resources.Resource {
	return &resources.Resource{ID: 1, WorkspaceID: 1, RouteFamily: "/orgs", Name: "orgs", IDField: "id", IDStrategy: "seq"}
}

func nestedDeepTeamResource() *resources.Resource {
	return &resources.Resource{
		ID: 2, WorkspaceID: 1, RouteFamily: "/orgs/{}/teams", Name: "orgTeams",
		IDField: "id", IDStrategy: "seq", ScopeParams: []string{"orgId"},
	}
}

// nestedDeepUsersResource is the LEAF, two levels under /orgs. ScopeParams
// is the DETAIL route's own spelling (D5.6, D6.1), exactly the invariant
// nestedUserResource pins one level up.
func nestedDeepUsersResource(writeForm *string) *resources.Resource {
	return &resources.Resource{
		ID: 3, WorkspaceID: 1, RouteFamily: "/orgs/{}/teams/{}/users", Name: "teamUsers",
		IDField: "id", IDStrategy: "seq", ScopeParams: []string{"organizationId", "team"}, WriteForm: writeForm,
	}
}

func nestedDeepTestRuntime(t *testing.T, settings domain.Settings, org, team, users *resources.Resource) *runtime {
	t.Helper()
	rt := fixtureRuntime(t, blankDoc, nestedDeepTestRoutes(), nestedDeepTestVariants(), settings)
	rt.resources = map[string]*resources.Resource{
		org.RouteFamily: org, team.RouteFamily: team, users.RouteFamily: users,
	}
	return rt
}

// TestResourceBranch_AnchorWalk_DepthTwo_RefusesAtFirstMissNamingOutermost
// is P9 and P10 together: the anchor walk verifies EVERY ancestor, top
// down, refuses at the FIRST miss, and the 404 names the OUTERMOST missing
// id — not the innermost. Mutation (P9): check only the immediate parent
// (P3e's own shape, resource.go's own comment names it as the wrong
// implementation a reader is most likely to write) — under it, deleting the
// ROOT org row would leave a live-team-anchored request reaching the leaf
// store, which this test's fakeEntityStore fails the test outright over,
// rather than merely asserting a call count of zero afterward. Mutation
// (P10): name the innermost key instead of the outermost — the message
// assertions in each subtest catch that directly.
func TestResourceBranch_AnchorWalk_DepthTwo_RefusesAtFirstMissNamingOutermost(t *testing.T) {
	org := nestedDeepOrgResource()
	team := nestedDeepTeamResource()
	users := nestedDeepUsersResource(bareWriteForm())
	rt := nestedDeepTestRuntime(t, domain.DefaultSettings(), org, team, users)

	run := func(t *testing.T, getFn func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error), wantMissingKey string) {
		store := &fakeEntityStore{getFn: getFn}
		p := resourceTestPlane(4<<20, store)

		assertNotFoundNaming := func(t *testing.T, rec *httptest.ResponseRecorder) {
			t.Helper()
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
			}
			var body httpx.ErrorBody
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Fatalf("decode error body: %v; body=%s", err, rec.Body)
			}
			if body.Error.Code != "entity_not_found" {
				t.Errorf("error.code = %q, want entity_not_found", body.Error.Code)
			}
			if !strings.Contains(body.Error.Message, fmt.Sprintf("%q", wantMissingKey)) {
				t.Errorf("message = %q, want it to name the missing id %q", body.Error.Message, wantMissingKey)
			}
		}

		t.Run("GET collection", func(t *testing.T) {
			m := mustMatch(t, rt, http.MethodGet, "/orgs/999/teams/5/users")
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/orgs/999/teams/5/users", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))
			assertNotFoundNaming(t, rec)
		})

		t.Run("GET detail", func(t *testing.T) {
			m := mustMatch(t, rt, http.MethodGet, "/orgs/999/teams/5/users/1")
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local/orgs/999/teams/5/users/1", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))
			assertNotFoundNaming(t, rec)
		})

		t.Run("POST", func(t *testing.T) {
			m := mustMatch(t, rt, http.MethodPost, "/orgs/999/teams/5/users")
			req := newPostRequest(t, "http://alex.mock.local/orgs/999/teams/5/users", "application/json", `{"name":"x"}`)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
			assertNotFoundNaming(t, rec)
			if store.createCalls != 0 {
				t.Errorf("EntityStore.Create was called %d times — the walk must refuse before any write", store.createCalls)
			}
		})

		t.Run("DELETE", func(t *testing.T) {
			m := mustMatch(t, rt, http.MethodDelete, "/orgs/999/teams/5/users/1")
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, httptest.NewRequest(http.MethodDelete, "http://alex.mock.local/orgs/999/teams/5/users/1", nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))
			assertNotFoundNaming(t, rec)
			if store.deleteCalls != 0 {
				t.Errorf("EntityStore.Delete was called %d times — the walk must refuse before any write", store.deleteCalls)
			}
		})
	}

	t.Run("root missing: refuses at the first hop, names the org id, never reaches the team or the leaf", func(t *testing.T) {
		run(t, func(_ context.Context, resourceID int64, base, scope resources.ScopeKey, key string) (resources.Entity, bool, error) {
			switch resourceID {
			case org.ID:
				// No live org row anchors ANY key: the outermost ancestor
				// misses immediately, and the walk must stop right here.
				return resources.Entity{}, false, nil
			case team.ID:
				t.Fatalf("EntityStore.Get called on the MIDDLE ancestor (team) after the ROOT already missed — the walk must refuse at the FIRST miss, not keep checking")
			default:
				t.Fatalf("EntityStore.Get called on the LEAF (resourceID=%d) — the walk must refuse before the child is ever reached", resourceID)
			}
			return resources.Entity{}, false, nil
		}, "999")
	})

	t.Run("team missing, root present: walks past the live root, refuses at the middle hop, names the team id", func(t *testing.T) {
		run(t, func(_ context.Context, resourceID int64, base, scope resources.ScopeKey, key string) (resources.Entity, bool, error) {
			switch resourceID {
			case org.ID:
				// The root IS live — proves the walk genuinely checks it
				// (not merely the last hop) rather than skipping straight
				// to the middle ancestor.
				return resources.Entity{}, true, nil
			case team.ID:
				return resources.Entity{}, false, nil
			default:
				t.Fatalf("EntityStore.Get called on the LEAF (resourceID=%d) — the walk must refuse before the child is ever reached", resourceID)
			}
			return resources.Entity{}, false, nil
		}, "5")
	})
}

// TestResourceBranch_ScopeArityMismatch_DepthTwo_DeclinesToGenerator is
// P16's own depth-2 case, re-observed because at depth the arity check now
// guards a longer tuple: [nestedDeepUsersResource] is confirmed with only
// ONE ScopeParams entry while its route declares TWO outer path parameters,
// so scopeOf's own len(outer) != len(res.ScopeParams) cross-check disagrees
// (1 vs 2). Mutation: delete that check from scopeOf. Under it outer stays
// its real two-element value regardless, the anchor walk (armed on
// len(outer), never on len(res.ScopeParams) — D6.1) would run against a
// resource confirmed for a shorter tuple, and this test's fakeEntityStore
// fails the moment ANY method is called, because the correct behaviour
// declines to the generator before the store is ever consulted.
func TestResourceBranch_ScopeArityMismatch_DepthTwo_DeclinesToGenerator(t *testing.T) {
	org := nestedDeepOrgResource()
	team := nestedDeepTeamResource()
	users := nestedDeepUsersResource(nil)
	users.ScopeParams = []string{"organizationId"} // one, not two — the mismatch
	rt := nestedDeepTestRuntime(t, domain.DefaultSettings(), org, team, users)
	m := mustMatch(t, rt, http.MethodGet, "/orgs/7/teams/9/users")

	store := &fakeEntityStore{
		listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			t.Fatal("EntityStore.List was called — a scope-arity mismatch must decline to the generator before the store is EVER consulted")
			return nil, nil
		},
		getFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error) {
			t.Fatal("EntityStore.Get was called — a scope-arity mismatch must decline to the generator before the anchor walk EVER runs")
			return resources.Entity{}, false, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/orgs/7/teams/9/users", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the generator, not a refusal); body=%s", rec.Code, rec.Body)
	}
	// blankDoc's GET variant carries no SchemaPtr (same as the depth-1 case
	// above): the generated body is empty, which is exactly what tells this
	// apart from a stored row.
	if rec.Body.Len() != 0 {
		t.Errorf("body = %s, want empty (the GENERATED body, not a stored row)", rec.Body.String())
	}
}

// TestResourceBranch_NestedFamily_DepthTwo_SiblingScopesAnswerDisjointSets
// is P8: two sibling scopes of a depth-2 family — different ROOTS sharing
// the SAME innermost (immediate-parent) key — must answer disjoint row
// sets. /orgs/1/teams/1/users and /orgs/2/teams/1/users both anchor to a
// live team keyed "1", the identical innermost value, and differ only in
// the ROOT: exactly the fixture D13 names, and the one no single-level
// fixture can distinguish, because at depth 1 there is no root segment to
// collapse away.
//
// This file's fakeEntityStore keys its data by the exact [resources.ScopeKey]
// EntityStore.List receives, never by request path — so it exercises what
// actually reaches the store, not merely what the HTTP layer served. getFn
// authorizes the ORG ancestor for either root ("1" or "2") and the TEAM
// ancestor for entity_key "1" under either root's own scope prefix — both
// anchors the anchor walk itself needs (D6.3), computed from scopeOf's outer
// slice directly and therefore identical under the mutation below, so a
// failure here can only come from the FINAL scope handed to List.
//
// Mutation (P8): scopeOf's own EncodeScope(outer) call
// (internal/mockplane/resource.go) becomes
// EncodeScope(outer[len(outer)-1:]) — the scope reaching List collapses to
// the innermost value alone, "1" for BOTH requests, since the fixture's two
// roots deliberately share that innermost key. Neither collapsed scope is a
// key of this test's per-scope map, so listFn's own default case fails the
// test by name rather than returning a false pass — a mutation that instead
// aliased the two root scopes onto EACH OTHER (rather than off the map
// entirely) would still be caught by the disjointness assertion below.
func TestResourceBranch_NestedFamily_DepthTwo_SiblingScopesAnswerDisjointSets(t *testing.T) {
	org := nestedDeepOrgResource()
	team := nestedDeepTeamResource()
	users := nestedDeepUsersResource(nil)
	rt := nestedDeepTestRuntime(t, domain.DefaultSettings(), org, team, users)

	rootA, rootB, innerKey := "1", "2", "1"
	scopeA := resources.EncodeScope([]string{rootA, innerKey}) // /orgs/1/teams/1/users
	scopeB := resources.EncodeScope([]string{rootB, innerKey}) // /orgs/2/teams/1/users
	byScope := map[resources.ScopeKey][]resources.Entity{
		scopeA: {{EntityKey: "101", Data: jsonx.RawMessage(`{"id":101,"team":"root-a"}`)}},
		scopeB: {{EntityKey: "201", Data: jsonx.RawMessage(`{"id":201,"team":"root-b"}`)}},
	}

	store := &fakeEntityStore{
		getFn: func(_ context.Context, resourceID int64, _, scope resources.ScopeKey, key string) (resources.Entity, bool, error) {
			switch resourceID {
			case org.ID:
				// Both roots are live, regardless of scope (the root's own
				// scope is always the empty prefix).
				return resources.Entity{}, key == rootA || key == rootB, nil
			case team.ID:
				// The SAME innermost key ("1") is live under EITHER root's
				// own scope prefix — the sibling condition P8 names.
				return resources.Entity{}, key == innerKey, nil
			default:
				t.Fatalf("EntityStore.Get called on an unexpected resourceID=%d", resourceID)
				return resources.Entity{}, false, nil
			}
		},
		listFn: func(_ context.Context, resourceID int64, _, scope resources.ScopeKey) ([]resources.Entity, error) {
			if resourceID != users.ID {
				t.Fatalf("EntityStore.List called on an unexpected resourceID=%d, want the leaf (%d)", resourceID, users.ID)
			}
			rows, ok := byScope[scope]
			if !ok {
				t.Fatalf("EntityStore.List called with scope %q, want one of %q or %q — "+
					"the scope reaching the store must carry BOTH outer values, not the innermost alone", scope, scopeA, scopeB)
			}
			return rows, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	get := func(t *testing.T, path string) []any {
		t.Helper()
		m := mustMatch(t, rt, http.MethodGet, path)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, resources.ScopeKey(""))
		if rec.Code != http.StatusOK {
			t.Fatalf("GET %s: status = %d, want 200; body=%s", path, rec.Code, rec.Body)
		}
		rows, ok := decodeAny(t, rec.Body.Bytes()).([]any)
		if !ok {
			t.Fatalf("GET %s: body = %s, want a JSON array", path, rec.Body)
		}
		return rows
	}

	gotA := get(t, "/orgs/"+rootA+"/teams/"+innerKey+"/users")
	gotB := get(t, "/orgs/"+rootB+"/teams/"+innerKey+"/users")

	if fmt.Sprint(gotA) == fmt.Sprint(gotB) {
		t.Fatalf("sibling scopes /orgs/%s/teams/%s/users and /orgs/%s/teams/%s/users answered the SAME row set %v, want disjoint",
			rootA, innerKey, rootB, innerKey, gotA)
	}
	wantA := []any{map[string]any{"id": float64(101), "team": "root-a"}}
	wantB := []any{map[string]any{"id": float64(201), "team": "root-b"}}
	if fmt.Sprint(gotA) != fmt.Sprint(wantA) {
		t.Errorf("GET /orgs/%s/teams/%s/users = %v, want %v", rootA, innerKey, gotA, wantA)
	}
	if fmt.Sprint(gotB) != fmt.Sprint(wantB) {
		t.Errorf("GET /orgs/%s/teams/%s/users = %v, want %v", rootB, innerKey, gotB, wantB)
	}
}

// --- P3h: the base scope — D14's P6, P7, P8, P21 ------------------------

// baseTestSettings declares one base parameter (tenantId) with a single
// value declared, for the base-scope tests below.
func baseTestSettings() domain.Settings {
	s := domain.DefaultSettings()
	s.BasePath = "/tenants/{tenantId}"
	s.BasePathValues = []string{"7"}
	return s
}

// baseOf computes the base scope a real serveRoute would compute for path,
// under settings — router.BaseValues positionally, [resources.EncodeScope]
// to encode, the same two calls routes.go's own serveRoute makes.
func baseOf(t *testing.T, settings domain.Settings, path string) resources.ScopeKey {
	t.Helper()
	values, ok := router.BaseValues(settings.BasePath, NormalizeSegments(path))
	if !ok {
		t.Fatalf("router.BaseValues(%q, %q) = _, false", settings.BasePath, path)
	}
	return resources.EncodeScope(values)
}

// TestResourceBranch_UndeclaredBaseValueRefusesOnAllFourVerbs is P6: the
// family is not route_off, the request is acceptable to the 406 gate, and
// no session directive is set — the fixture D14 pins so the refusal
// observed here is the BRANCH's own (D7.3), not one of the exits ahead of
// it answering instead. An undeclared base value ("999", declared set is
// only "7") answers 404 entity_not_found on GET/GET-detail/POST/DELETE and
// writes nothing — the store is never even consulted (getFn/listFn/
// createFn/deleteFn all fail the test outright if called).
func TestResourceBranch_UndeclaredBaseValueRefusesOnAllFourVerbs(t *testing.T) {
	settings := baseTestSettings()
	res := itemsResource(bareWriteForm(), "id", specs.Wrapper{})
	rt := resourceTestRuntime(t, settings, res)

	store := &fakeEntityStore{
		listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			t.Fatal("EntityStore.List called — an undeclared base value must refuse before the store is ever consulted")
			return nil, nil
		},
		getFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (resources.Entity, bool, error) {
			t.Fatal("EntityStore.Get called — an undeclared base value must refuse before the store is ever consulted")
			return resources.Entity{}, false, nil
		},
		createFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, map[string]any) (resources.Entity, error) {
			t.Fatal("EntityStore.Create called — an undeclared base value must refuse before any write")
			return resources.Entity{}, nil
		},
		deleteFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (bool, error) {
			t.Fatal("EntityStore.Delete called — an undeclared base value must refuse before any write")
			return false, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	assertRefused := func(t *testing.T, rec *httptest.ResponseRecorder) {
		t.Helper()
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
		}
		var body httpx.ErrorBody
		if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
			t.Fatalf("decode error body: %v; body=%s", err, rec.Body)
		}
		if body.Error.Code != "entity_not_found" {
			t.Errorf("error.code = %q, want entity_not_found", body.Error.Code)
		}
		if !strings.Contains(body.Error.Message, `"999"`) {
			t.Errorf("message = %q, want it to name the undeclared base value %q", body.Error.Message, "999")
		}
	}

	t.Run("GET collection", func(t *testing.T) {
		path := "/tenants/999/items"
		m := mustMatch(t, rt, http.MethodGet, path)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		assertRefused(t, rec)
	})

	t.Run("GET detail", func(t *testing.T) {
		path := "/tenants/999/items/1"
		m := mustMatch(t, rt, http.MethodGet, path)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		assertRefused(t, rec)
	})

	t.Run("POST", func(t *testing.T) {
		path := "/tenants/999/items"
		m := mustMatch(t, rt, http.MethodPost, path)
		req := newPostRequest(t, "http://alex.mock.local"+path, "application/json", `{"name":"x"}`)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		assertRefused(t, rec)
	})

	t.Run("DELETE", func(t *testing.T) {
		path := "/tenants/999/items/1"
		m := mustMatch(t, rt, http.MethodDelete, path)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodDelete, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		assertRefused(t, rec)
	})
}

// TestResourceBranch_UndeclaredBaseValueOnlyRefusesResourceServedRoutes is
// P7: the declared set bounds STATE, not routing — a NON-resource
// operation of the same workspace, matched under the identical undeclared
// base value, still answers its ordinary generated 200.
func TestResourceBranch_UndeclaredBaseValueOnlyRefusesResourceServedRoutes(t *testing.T) {
	settings := baseTestSettings()
	routes := append(resourceTestRoutes(), router.Route{
		OpRowID: 5, Method: http.MethodGet, Path: "/health", CanonicalPath: "/health", SourceOrder: 5,
	})
	variants := resourceTestVariants()
	variants[5] = []gen.ResponseVariant{{OpRowID: 5, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}}
	rt := fixtureRuntime(t, blankDoc, routes, variants, settings)
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt.resources = map[string]*resources.Resource{res.RouteFamily: res} // "/health" deliberately NOT in the roster

	p := resourceTestPlane(4<<20, &fakeEntityStore{})

	path := "/tenants/999/health"
	m := mustMatch(t, rt, http.MethodGet, path)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))

	if rec.Code != http.StatusOK {
		t.Fatalf("non-resource route under an undeclared base value: status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
}

// specSourceMustNotBeCalled is a [SpecSource] whose three methods fail the
// test outright if ever invoked. TestResourceBranch_BaseValueReadPositionallyNotByName
// below seeds the route cache by hand with its own fixture runtime so that
// [Plane.serveRoute] takes the cache-HIT path inside [Plane.runtimeFor] and
// never calls build() — proving the request that follows is served by
// routes.go's own base-scope computation (routes.go:231) rather than by a
// runtime this fixture built independently. A call to any method here means
// the seed missed and the test would silently stop exercising that line at
// all — the exact vacuity a prior version of this test had, calling
// serveGenerated directly with a base value computed by the test's own
// baseOf helper instead of by routes.go.
type specSourceMustNotBeCalled struct{ t *testing.T }

func (s specSourceMustNotBeCalled) Routes(context.Context, int64) ([]router.Route, error) {
	s.t.Helper()
	s.t.Fatal("SpecSource.Routes called — the route cache should already hold the fixture runtime, seeded by hand")
	return nil, nil
}

func (s specSourceMustNotBeCalled) Normalized(context.Context, int64) ([]byte, error) {
	s.t.Helper()
	s.t.Fatal("SpecSource.Normalized called — the route cache should already hold the fixture runtime, seeded by hand")
	return nil, nil
}

func (s specSourceMustNotBeCalled) Variants(context.Context, int64) (map[int64][]gen.ResponseVariant, error) {
	s.t.Helper()
	s.t.Fatal("SpecSource.Variants called — the route cache should already hold the fixture runtime, seeded by hand")
	return nil, nil
}

// TestResourceBranch_BaseValueReadPositionallyNotByName is P8: a base
// parameter and a route parameter sharing the SAME name ("shared") must be
// told apart by POSITION, never by a lookup in Match.Params — which, on
// this exact collision, silently holds only the LATER (route-path) value
// (D4.3's own documented hazard). basePath="/{shared}", detail
// route="/items/{shared}"; the request /OUTER/items/7 makes
// m.Params["shared"] == "7" (the entity id, the route's own segment
// winning), while the correct positional base is "OUTER" — the ONLY
// declared value. A by-name read would compute base="7", which is
// undeclared, and refuse; the correct read serves the row.
//
// This drives the request through [Plane.serveRoute] — not serveGenerated
// directly, the pattern every other case in this file uses — because
// routes.go:231's own `router.BaseValues(ws.Settings.BasePath, segments)` is
// the ONLY production call site P8's mutation touches; a case that computes
// its own base value (however correctly) and hands it to serveGenerated
// never runs that line at all, so the mutation the property names produces
// no red anywhere in this file. Reaching serveRoute needs a *runtime the
// cache already holds (specSourceMustNotBeCalled above proves it does, by
// failing loudly the moment anything falls back to a real build) and a
// *workspaces.Workspace whose id/revision match the seeded cache key.
func TestResourceBranch_BaseValueReadPositionallyNotByName(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.BasePath = "/{shared}"
	settings.BasePathValues = []string{"OUTER"}

	routes := []router.Route{
		{OpRowID: 1, Method: http.MethodGet, Path: "/items", CanonicalPath: "/items", SourceOrder: 1},
		{OpRowID: 3, Method: http.MethodGet, Path: "/items/{shared}", CanonicalPath: "/items/{}", SourceOrder: 3},
	}
	variants := map[int64][]gen.ResponseVariant{
		3: {{OpRowID: 3, Selector: "200", HTTPStatus: 200, MediaType: "application/json"}},
	}
	rt := fixtureRuntime(t, blankDoc, routes, variants, settings)
	res := itemsResource(nil, "id", specs.Wrapper{})
	rt.resources = map[string]*resources.Resource{res.RouteFamily: res}

	wantBase := resources.EncodeScope([]string{"OUTER"})
	store := &fakeEntityStore{
		getFn: func(_ context.Context, _ int64, base, _ resources.ScopeKey, key string) (resources.Entity, bool, error) {
			if base != wantBase {
				t.Fatalf("base = %q, want %q (\"OUTER\", the positional read) — a by-name read out of Match.Params would compute %q instead", base, wantBase, "7")
			}
			if key != "7" {
				t.Fatalf("entity key = %q, want %q (router.DetailIDParam's own positional read)", key, "7")
			}
			return resources.Entity{Data: jsonx.RawMessage(`{"id":"7"}`)}, true, nil
		},
	}
	p := resourceTestPlane(4<<20, store)

	specID := int64(1)
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", SpecID: &specID, Revision: 1, Settings: settings}
	p.specs = specSourceMustNotBeCalled{t}
	p.routes.put(routeCacheKey{workspaceID: ws.ID, revision: ws.Revision}, rt)

	path := "/OUTER/items/7"
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil)
	rec := httptest.NewRecorder()
	p.serveRoute(rec, req, ws, NormalizeSegments(path))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — the base value must be read positionally (\"OUTER\", declared), not by name out of Match.Params (\"7\", undeclared); body=%s", rec.Code, rec.Body)
	}
}

// TestResourceBranch_PinnedAndForcedStatusWinOverMembershipRefusal is P21:
// the membership check sits AFTER D6 rule 4 (pinned / non-2xx), so a
// pinned response and a session-forced non-2xx status both win over an
// undeclared base value's refusal — while the SAME route under the SAME
// undeclared value, with NEITHER in force, does answer the refusal.
func TestResourceBranch_PinnedAndForcedStatusWinOverMembershipRefusal(t *testing.T) {
	settings := baseTestSettings()
	res := itemsResource(nil, "id", specs.Wrapper{})
	path := "/tenants/999/items"

	t.Run("a pinned variant wins over the undeclared base refusal", func(t *testing.T) {
		rt := resourceTestRuntime(t, settings, res)
		rt.overrides = map[string]*overrides.Row{
			overrides.OpKey("GET", "/items"): {
				OverrideOn: true,
				Responses: map[string]overrides.Variant{
					"200": {Mode: "pinned", MediaType: "application/json", Body: jsonx.RawMessage(`{"pinned":true}`)},
				},
			},
		}
		m := mustMatch(t, rt, http.MethodGet, path)
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			t.Fatal("EntityStore.List called — a pinned variant must win before the membership check is ever reached")
			return nil, nil
		}}
		p := resourceTestPlane(4<<20, store)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "pinned") {
			t.Fatalf("status=%d body=%s, want 200 with the pinned body", rec.Code, rec.Body.String())
		}
	})

	t.Run("a forced non-2xx status wins over the undeclared base refusal", func(t *testing.T) {
		forced := http.StatusInternalServerError
		rt := resourceTestRuntime(t, settings, res)
		rt.overrides = map[string]*overrides.Row{
			overrides.OpKey("GET", "/items"): {OverrideOn: true, ActiveStatus: &forced},
		}
		m := mustMatch(t, rt, http.MethodGet, path)
		store := &fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			t.Fatal("EntityStore.List called — a forced non-2xx status must win before the membership check is ever reached")
			return nil, nil
		}}
		p := resourceTestPlane(4<<20, store)
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		if rec.Code != http.StatusInternalServerError {
			t.Fatalf("status = %d, want 500 (the forced status)", rec.Code)
		}
	})

	t.Run("with neither in force, the same route under the same undeclared base value DOES refuse", func(t *testing.T) {
		rt := resourceTestRuntime(t, settings, res)
		m := mustMatch(t, rt, http.MethodGet, path)
		p := resourceTestPlane(4<<20, &fakeEntityStore{})
		rec := httptest.NewRecorder()
		p.serveGenerated(rec, httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+path, nil), respondTestWorkspace(), rt, m, baseOf(t, settings, path))
		if rec.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404 (the branch's own membership refusal)", rec.Code)
		}
	})
}
