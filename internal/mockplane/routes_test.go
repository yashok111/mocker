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

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// fakeSpecSource is an in-memory [mockplane.SpecSource]: routes keyed by
// specID, with a call counter so tests can assert how many times the
// underlying "database" was actually asked to build a table — the whole
// point of the cache in routes.go.
type fakeSpecSource struct {
	mu     sync.Mutex
	routes map[int64][]router.Route
	err    error
	calls  atomic.Int32

	// components, when non-nil, is written into the fake document under
	// "components" — P7a's custom-schema tests need a `$ref` target the
	// resolver can actually find.
	components map[string]any

	// delay, if non-zero, is slept inside Routes before it returns. Widening
	// the build's wall-clock time gives concurrent callers a realistic window
	// to pile up on the same cold key, which is what the single-flight test
	// needs to actually exercise (as opposed to each request simply never
	// overlapping another).
	delay time.Duration
}

func (f *fakeSpecSource) Routes(_ context.Context, specID int64) ([]router.Route, error) {
	f.calls.Add(1)
	if f.delay > 0 {
		time.Sleep(f.delay)
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	return f.routes[specID], nil
}

// buildFakeDoc synthesizes a minimal OAS 3.0 document whose paths/operations
// mirror routes: each operation's 200 response declares an application/json
// media-type example {"op": "<operationLabel>"}. That is enough for
// [gen.Generator] to hand back a real, per-route-DISTINGUISHING body (via
// DESIGN §9's example priority, ahead of schema-driven generation) without
// this fake needing to hand-author a full JSON Schema per fixture route —
// exactly what TestServeRoute_MatchAndNoMatch needs to keep proving THE
// RIGHT route was chosen, now that the old 501's operationId detail is gone.
func buildFakeDoc(routes []router.Route) []byte {
	type opEntry struct {
		method, opID string
	}
	byPath := make(map[string][]opEntry)
	for _, rt := range routes {
		byPath[rt.Path] = append(byPath[rt.Path], opEntry{strings.ToLower(rt.Method), rt.OperationLabel})
	}

	paths := make(map[string]any, len(byPath))
	for path, ops := range byPath {
		methods := make(map[string]any, len(ops))
		for _, op := range ops {
			methods[op.method] = map[string]any{
				"operationId": op.opID,
				"responses": map[string]any{
					"200": map[string]any{
						"description": "ok",
						"content": map[string]any{
							"application/json": map[string]any{
								"schema":  map[string]any{"type": "object"},
								"example": map[string]any{"op": op.opID},
							},
						},
					},
				},
			}
		}
		paths[path] = methods
	}

	doc := map[string]any{
		"openapi": "3.0.3",
		"info":    map[string]any{"title": "fake", "version": "1.0.0"},
		"paths":   paths,
	}
	b, err := json.Marshal(doc)
	if err != nil {
		panic("buildFakeDoc: marshal fixture: " + err.Error())
	}
	return b
}

// fakePointerEscape mirrors internal/gen and internal/specs' own RFC-6901
// token escaping ("~" then "/"), duplicated here for the same reason those
// leaf packages duplicate it rather than import one another: this is a test
// fixture building the exact pointer shape the real indexer produces, not
// production code that could just import it.
func fakePointerEscape(tok string) string {
	tok = strings.ReplaceAll(tok, "~", "~0")
	tok = strings.ReplaceAll(tok, "/", "~1")
	return tok
}

// Normalized satisfies the SpecSource method [runtimeFor]'s build needs
// alongside Routes: a real, per-specID document built from that spec's own
// routes (buildFakeDoc), so Variants' pointers below actually resolve.
func (f *fakeSpecSource) Normalized(_ context.Context, specID int64) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	doc := buildFakeDoc(f.routes[specID])
	if f.components == nil {
		return doc, nil
	}
	var root map[string]any
	if err := json.Unmarshal(doc, &root); err != nil {
		panic("fakeSpecSource: decode fixture: " + err.Error())
	}
	root["components"] = f.components
	out, err := json.Marshal(root)
	if err != nil {
		panic("fakeSpecSource: marshal fixture: " + err.Error())
	}
	return out, nil
}

// Variants returns one trivial default-200 variant per route this fake
// serves for specID, keyed by OpRowID exactly as router.Route.OpRowID and
// gen.ResponseVariant.OpRowID both do — so a runtime built over any of this
// file's route fixtures always has a variant for every route in its own
// table, matching the routes it already serves. Each variant's OpPointer/
// SchemaPtr point into buildFakeDoc's own document, so [gen.Generator.Body]
// resolves a real, route-distinguishing {"op": "<operationLabel>"} body.
func (f *fakeSpecSource) Variants(_ context.Context, specID int64) (map[int64][]gen.ResponseVariant, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.err != nil {
		return nil, f.err
	}
	routes := f.routes[specID]
	out := make(map[int64][]gen.ResponseVariant, len(routes))
	for _, rt := range routes {
		opPointer := "#/paths/" + fakePointerEscape(rt.Path) + "/" + strings.ToLower(rt.Method)
		out[rt.OpRowID] = []gen.ResponseVariant{{
			OpRowID:    rt.OpRowID,
			Selector:   "200",
			HTTPStatus: 200,
			IsDefault:  true,
			MediaType:  "application/json",
			SchemaPtr:  opPointer + "/responses/200/content/" + fakePointerEscape("application/json") + "/schema",
			OpPointer:  opPointer,
		}}
	}
	return out, nil
}

func (f *fakeSpecSource) callCount() int32 { return f.calls.Load() }

// route is a tiny fixture builder for [router.Route], mirroring
// internal/router's own test helper: CanonicalPath is derived the same way
// the real indexer derives it, so tests exercise the same computation
// production code uses instead of a hand-typed guess.
func route(method, path, operationID string, sourceOrder int64) router.Route {
	return router.Route{
		// OpRowID deliberately mirrors sourceOrder so a recognizable value
		// (see TestServeRoute_OpRowIDNeverLeaks) sits in the one field that
		// must never reach a response body.
		OpRowID:        sourceOrder,
		OperationLabel: operationID,
		Method:         method,
		Path:           path,
		CanonicalPath:  router.CanonicalPath(path),
		SourceOrder:    sourceOrder,
	}
}

// newPlaneWithSpec builds a Plane wired to spec as its SpecSource, serving
// the given workspaces — the routes.go counterpart to plane_test.go's
// newPlane, which always passes a nil SpecSource.
func newPlaneWithSpec(spec mockplane.SpecSource, wss ...*workspaces.Workspace) *mockplane.Plane {
	src := &fakeSource{bySlug: make(map[string]*workspaces.Workspace, len(wss))}
	for _, ws := range wss {
		src.bySlug[ws.Slug] = ws
	}
	return mockplane.New(testConfig(), src, spec, testLogger())
}

func specWorkspace(slug string, specID int64, revision int64) *workspaces.Workspace {
	return &workspaces.Workspace{
		ID:       1,
		Slug:     slug,
		SpecID:   &specID,
		Revision: revision,
		Settings: domain.DefaultSettings(),
	}
}

// TestServeRoute_MatchAndNoMatch is the table-driven core: one fixed route
// table, exercised against requests that should match — answering 200 with
// buildFakeDoc's {"op": "<operationLabel>"} body, still proving THE RIGHT
// route was chosen, the same guarantee the old 501's operationId detail used
// to carry — and requests that should not (404).
func TestServeRoute_MatchAndNoMatch(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {
			route("GET", "/users/{id}", "getUser", 1),
			route("GET", "/users/me", "getCurrentUser", 2),
			route("POST", "/users", "createUser", 3),
		},
	}}
	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(spec, ws)

	tests := []struct {
		name        string
		method      string
		path        string
		wantStatus  int
		wantOpID    string // checked only when wantStatus == 200
		wantErrCode string // checked only when wantStatus == 404
	}{
		{"static beats param", http.MethodGet, "/users/me", http.StatusOK, "getCurrentUser", ""},
		{"param route matches", http.MethodGet, "/users/42", http.StatusOK, "getUser", ""},
		{"different method same path", http.MethodPost, "/users", http.StatusOK, "createUser", ""},
		{"unmatched path", http.MethodGet, "/orgs/1", http.StatusNotFound, "", "not_found"},
		{"matched path wrong method", http.MethodDelete, "/users/me", http.StatusNotFound, "", "not_found"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req := httptest.NewRequest(tt.method, "http://alex.mock.local"+tt.path, nil)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Fatalf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}

			switch tt.wantStatus {
			case http.StatusOK:
				var body struct {
					Op string `json:"op"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode body: %v; body=%s", err, rec.Body)
				}
				if body.Op != tt.wantOpID {
					t.Errorf("body.op = %q, want %q (proves which route actually matched)", body.Op, tt.wantOpID)
				}
			case http.StatusNotFound:
				var body struct {
					Error struct {
						Code string `json:"code"`
					} `json:"error"`
				}
				if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
					t.Fatalf("decode body: %v; body=%s", err, rec.Body)
				}
				if body.Error.Code != tt.wantErrCode {
					t.Errorf("error.code = %q, want %q", body.Error.Code, tt.wantErrCode)
				}
			}
		})
	}
}

// TestServeRoute_OpRowIDNeverLeaks proves the database row id never reaches
// the wire, even though it is right there on router.Route next to the fields
// that do get serialized.
func TestServeRoute_OpRowIDNeverLeaks(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 999)},
	}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Body.String(); containsDigits999(got) {
		t.Errorf("response body contains the OpRowID (999), must never leak it: %s", got)
	}
}

// containsDigits999 is a tiny helper kept local to this one regression check;
// a real substring search is all the guarantee needs.
func containsDigits999(s string) bool {
	for i := 0; i+3 <= len(s); i++ {
		if s[i:i+3] == "999" {
			return true
		}
	}
	return false
}

// TestServeRoute_NoSpecAttached covers "no spec attached": a workspace whose
// SpecID is nil gets the plain 404, even with a real SpecSource wired in —
// there's simply nothing to look up.
func TestServeRoute_NoSpecAttached(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{}}
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()} // SpecID nil
	p := newPlaneWithSpec(spec, ws)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/anything", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if spec.callCount() != 0 {
		t.Errorf("Routes called %d times, want 0 (no spec attached, nothing to build)", spec.callCount())
	}
}

// TestServeRoute_NilSpecSourceBehavesAsToday is the direct regression for
// "a nil SpecSource behaves as today": even a workspace WITH a SpecID must
// still fall back to the plain pre-P1a 404 when the runtime itself was built
// with no SpecSource at all.
func TestServeRoute_NilSpecSourceBehavesAsToday(t *testing.T) {
	specID := int64(1)
	ws := &workspaces.Workspace{ID: 1, Slug: "alex", SpecID: &specID, Settings: domain.DefaultSettings()}
	p := newPlaneWithSpec(nil, ws) // nil SpecSource

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/anything", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
}

// TestServeRoute_CORSOnGenerated is the generated-response twin of
// plane_test.go's TestServeHTTP_CORSOnNotFound: DESIGN §8 requires CORS
// headers on every response shape, generated bodies included — CORS is set
// in Step 2 of serveResolved, before Step 5 ever reaches the route table, so
// it must survive completely unrelated to what Step 5 answers with.
func TestServeRoute_CORSOnGenerated(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 1)},
	}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want echoed origin on a generated response", got)
	}
}

// TestServeRoute_CORSOnRouteTableNotFound is the routed-404 twin: CORS must
// still be set when a spec is attached but the request matches nothing in
// its table, not just on the pre-P1a "no spec at all" 404.
func TestServeRoute_CORSOnRouteTableNotFound(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 1)},
	}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/no-such-route", nil)
	req.Header.Set("Origin", "https://app.example")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("Access-Control-Allow-Origin"); got != "https://app.example" {
		t.Errorf("Access-Control-Allow-Origin = %q, want echoed origin even on this 404", got)
	}
}

// TestServeRoute_HEADMatchesGETWithEmptyBody proves HEAD resolves against the
// GET route (router.Table.Match's own contract) all the way through to the
// generated body being suppressed by the same headWriter the reserved
// endpoints already use — no special-casing needed in routes.go or
// respond.go — while Content-Type/Content-Length still land as if this had
// been the GET request.
func TestServeRoute_HEADMatchesGETWithEmptyBody(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 1)},
	}}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))

	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/ping", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if got := rec.Header().Get("Content-Type"); !strings.Contains(got, "json") {
		t.Errorf("HEAD Content-Type = %q, want the JSON type a GET would have sent", got)
	}
	if got := rec.Header().Get("Content-Length"); got == "" || got == "0" {
		t.Errorf("HEAD Content-Length = %q, want the length a GET would have sent", got)
	}
}

// TestRouteCache_SameTableAcrossRequestsThenRebuildsOnRevisionBump is the
// direct test for the cache's whole reason to exist: two requests against
// the same (workspace, revision) must build only once, and bumping Revision
// must force a rebuild — proven from the outside, through the number of
// times the fake's Routes was actually called, since Plane exposes no way to
// compare *router.Table pointers directly (nor should it: that would leak an
// implementation detail into the public API for a test's convenience).
func TestRouteCache_SameTableAcrossRequestsThenRebuildsOnRevisionBump(t *testing.T) {
	spec := &fakeSpecSource{routes: map[int64][]router.Route{
		1: {route("GET", "/ping", "ping", 1)},
	}}
	ws := specWorkspace("alex", 1, 1)
	p := newPlaneWithSpec(spec, ws)

	get := func() int {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec.Code
	}

	if code := get(); code != http.StatusOK {
		t.Fatalf("first request status = %d, want 200", code)
	}
	if code := get(); code != http.StatusOK {
		t.Fatalf("second request status = %d, want 200", code)
	}
	if got := spec.callCount(); got != 1 {
		t.Errorf("Routes called %d times across two requests at the same revision, want 1 (cache hit)", got)
	}

	// The fake looks up by the SAME ws pointer each request (fakeSource just
	// stores it), so mutating Revision here is exactly what
	// workspaces.Repo.Update's bump looks like to this cache.
	ws.Revision++

	if code := get(); code != http.StatusOK {
		t.Fatalf("post-bump request status = %d, want 200", code)
	}
	if got := spec.callCount(); got != 2 {
		t.Errorf("Routes called %d times after a revision bump, want 2 (bump must force a rebuild)", got)
	}
}

// TestRouteCache_ColdBuildIsSingleFlighted is the concurrent regression for
// DESIGN §20's stalled-event-loop risk: many goroutines racing a cold
// (workspace, revision) must trigger exactly one call to Routes, never one
// per goroutine. Run with -race, per the package's finish command.
func TestRouteCache_ColdBuildIsSingleFlighted(t *testing.T) {
	spec := &fakeSpecSource{
		routes: map[int64][]router.Route{1: {route("GET", "/ping", "ping", 1)}},
		delay:  20 * time.Millisecond,
	}
	p := newPlaneWithSpec(spec, specWorkspace("alex", 1, 1))

	const goroutines = 32
	var wg sync.WaitGroup
	wg.Add(goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/ping", nil)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
		}()
	}
	wg.Wait()

	if got := spec.callCount(); got != 1 {
		t.Errorf("Routes called %d times by %d concurrent requests, want 1 (build must be single-flighted)",
			got, goroutines)
	}
}
