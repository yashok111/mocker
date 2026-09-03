// respond_test.go is a WHITE-BOX test file (package mockplane, not
// mockplane_test): [chooseVariant], [acceptable], [wrapEnvelope],
// [clampedDelay]/[awaitDelay] and [setSafeHeader] are all unexported, and
// half of this file's value is proving exactly what those private pieces do
// in isolation before ever going through HTTP. (ListFamily and
// DetailIDParam moved to [router] — P3a's D3 — so their own unit tests live
// in router_test.go now; this file still exercises them through
// [Plane.serveGenerated], see TestServeGenerated_DetailRouteMatchesListRow
// below.) The other half calls [Plane.serveGenerated] directly — bypassing
// ServeHTTP's
// workspace/CORS/preflight machinery entirely, already covered end to end by
// routes_test.go and plane_test.go in the sibling _test package — so these
// tests are squarely about respond.go's own contract: variant choice,
// negotiation, 204/205/HEAD/degraded suppression, the envelope, the delay,
// and the list-row/detail-card identity DESIGN §9 promises.
package mockplane

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// ---------------------------------------------------------------------
// Pure unit tests: chooseVariant, acceptable, wrapEnvelope, clampedDelay,
// setSafeHeader.
// ---------------------------------------------------------------------

// TestChooseVariant is the direct table-driven proof of DESIGN §7 step 5's
// selection rule, independent of any HTTP plumbing: lowest numeric 2xx,
// then "2XX", then "default", then (a shape the real indexer never actually
// produces, since it always marks exactly one row IsDefault, but handled
// defensively anyway) the lowest status of any kind. Every case also checks
// that HTTPStatus — never the Selector string, never 0 — is what a caller
// would actually send: the blocker-grade defect the phase digest calls out
// by name.
func TestChooseVariant(t *testing.T) {
	v := func(sel string, status int) gen.ResponseVariant {
		return gen.ResponseVariant{Selector: sel, HTTPStatus: status}
	}

	tests := []struct {
		name       string
		variants   []gen.ResponseVariant
		wantOK     bool
		wantStatus int
		wantSel    string
	}{
		{"empty", nil, false, 0, ""},
		{
			"lowest numeric 2xx among several wins over a higher one and over 204",
			[]gen.ResponseVariant{v("204", 204), v("201", 201), v("default", 200)},
			true, 201, "201",
		},
		{
			"a bare 204 IS itself the lowest numeric 2xx when nothing lower exists",
			[]gen.ResponseVariant{v("204", 204), v("default", 200)},
			true, 204, "204",
		},
		{
			"no numeric 2xx: 2XX row wins over default",
			[]gen.ResponseVariant{v("2XX", 200), v("default", 200), v("404", 404)},
			true, 200, "2XX",
		},
		{
			"no numeric 2xx, no 2XX: default row wins",
			[]gen.ResponseVariant{v("default", 200), v("404", 404)},
			true, 200, "default",
		},
		{
			"nothing in 2xx/2XX/default shape at all: lowest status of any kind, defensively",
			[]gen.ResponseVariant{v("404", 404), v("500", 500)},
			true, 404, "404",
		},
		{
			"a single default-only operation",
			[]gen.ResponseVariant{v("default", 200)},
			true, 200, "default",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := chooseVariant(tt.variants)
			if ok != tt.wantOK {
				t.Fatalf("ok = %v, want %v", ok, tt.wantOK)
			}
			if !tt.wantOK {
				return
			}
			if got.HTTPStatus != tt.wantStatus {
				t.Errorf("HTTPStatus = %d, want %d (never the literal selector string, never 0)", got.HTTPStatus, tt.wantStatus)
			}
			if got.Selector != tt.wantSel {
				t.Errorf("chose selector %q, want %q", got.Selector, tt.wantSel)
			}
		})
	}
}

// TestAcceptable covers exactly what acceptable's own doc comment claims to
// implement: split-on-comma media ranges, a ";q=" weight defaulting to 1,
// most-specific-match-wins among candidates, and q=0 as an explicit refusal
// even when a less specific range would otherwise accept.
func TestAcceptable(t *testing.T) {
	tests := []struct {
		name      string
		accept    string
		mediaType string
		want      bool
	}{
		{"missing Accept accepts the variant's own type", "", "application/json", true},
		{"blank Accept accepts", "   ", "application/json", true},
		{"*/* accepts anything", "*/*", "application/json", true},
		{"exact match", "application/json", "application/json", true},
		{"type wildcard matches", "application/*", "application/json", true},
		{"case-insensitive match", "Application/JSON", "application/json", true},
		{"mismatched type refuses", "text/plain", "application/json", false},
		{"mismatched subtype refuses", "application/xml", "application/json", false},
		{"charset parameter on the declared type is ignored when comparing", "application/json", "application/json; charset=utf-8", true},
		{"explicit q=0 refuses", "application/json;q=0", "application/json", false},
		{"q=0 with no decimal still refuses", "application/json ; q=0", "application/json", false},
		{"nonzero q accepts", "application/json;q=0.1", "application/json", true},
		{
			"most specific match's q wins over a more general match's q",
			"application/json;q=0, */*;q=0.9",
			"application/json",
			false,
		},
		{
			"a more general acceptance does not rescue a specific exclusion",
			"*/*;q=1, application/json;q=0",
			"application/json",
			false,
		},
		{
			"multiple ranges: the one that matches decides, others are irrelevant",
			"text/plain, application/json",
			"application/json",
			true,
		},
		{"malformed q value falls back to the default (accepts)", "application/json;q=banana", "application/json", true},
		{"empty declared media type always accepts (nothing to negotiate)", "text/plain", "", true},
		{"malformed declared media type (no slash) always accepts", "text/plain", "not-a-media-type", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := acceptable(tt.accept, tt.mediaType); got != tt.want {
				t.Errorf("acceptable(%q, %q) = %v, want %v", tt.accept, tt.mediaType, got, tt.want)
			}
		})
	}
}

// TestWrapEnvelope proves the envelope helper wraps valid JSON under key and
// refuses (rather than corrupts) anything that is not valid JSON — the
// mechanism serveGenerated relies on to skip enveloping a non-JSON body
// (e.g. this document's one text/csv response, or a binary placeholder)
// without needing to branch on media type itself.
func TestWrapEnvelope(t *testing.T) {
	t.Run("wraps a JSON object", func(t *testing.T) {
		got, err := wrapEnvelope("data", []byte(`{"id":1}`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		want := `{"data":{"id":1}}`
		if string(got) != want {
			t.Errorf("wrapEnvelope = %s, want %s", got, want)
		}
	})

	t.Run("wraps a JSON array", func(t *testing.T) {
		got, err := wrapEnvelope("items", []byte(`[1,2,3]`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		want := `{"items":[1,2,3]}`
		if string(got) != want {
			t.Errorf("wrapEnvelope = %s, want %s", got, want)
		}
	})

	t.Run("escapes a key that needs it", func(t *testing.T) {
		got, err := wrapEnvelope(`we"ird`, []byte(`1`))
		if err != nil {
			t.Fatalf("wrapEnvelope: %v", err)
		}
		var decoded map[string]json.RawMessage
		if err := json.Unmarshal(got, &decoded); err != nil {
			t.Fatalf("wrapEnvelope produced invalid JSON: %v (%s)", err, got)
		}
		if string(decoded[`we"ird`]) != "1" {
			t.Errorf("wrapEnvelope = %s, want a key exactly %q", got, `we"ird`)
		}
	})

	t.Run("refuses non-JSON content instead of corrupting it", func(t *testing.T) {
		if _, err := wrapEnvelope("data", []byte("a,b,c\n1,2,3\n")); err == nil {
			t.Error("wrapEnvelope(csv-ish text) = nil error, want a refusal so the caller leaves it unwrapped")
		}
	})
}

// TestSetSafeHeader_DropsCRLF is the direct proof of the response-splitting
// guard: a header value containing a raw CR or LF — plausible from an
// attacker-controllable uploaded spec, DESIGN §15's threat model — is
// dropped entirely rather than set, mangled or otherwise reaching the wire.
func TestSetSafeHeader_DropsCRLF(t *testing.T) {
	tests := []struct {
		name  string
		value string
		want  string // "" means: header must not be set at all
	}{
		{"clean value passes through", "abc123", "abc123"},
		{"embedded CRLF is dropped", "abc\r\nX-Injected: evil", ""},
		{"bare LF is dropped", "abc\ninjected", ""},
		{"bare CR is dropped", "abc\rinjected", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rec := httptest.NewRecorder()
			setSafeHeader(rec, "X-Test", tt.value)
			if got := rec.Header().Get("X-Test"); got != tt.want {
				t.Errorf("header = %q, want %q", got, tt.want)
			}
		})
	}

	t.Run("empty name is a no-op", func(t *testing.T) {
		rec := httptest.NewRecorder()
		setSafeHeader(rec, "", "value")
		if len(rec.Header()) != 0 {
			t.Errorf("headers = %v, want none set for an empty name", rec.Header())
		}
	})
}

// TestClampedDelay proves the cap without ever having to sleep out
// maxSimulatedDelay itself.
func TestClampedDelay(t *testing.T) {
	tests := []struct {
		name    string
		delayMs int
		want    time.Duration
	}{
		{"zero", 0, 0},
		{"negative (Settings.Normalize should already prevent this, but defend anyway)", -50, 0},
		{"ordinary value passes through", 250, 250 * time.Millisecond},
		{"exactly the cap", int(maxSimulatedDelay / time.Millisecond), maxSimulatedDelay},
		{"far past the cap is clamped", 10_000_000, maxSimulatedDelay},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := clampedDelay(tt.delayMs); got != tt.want {
				t.Errorf("clampedDelay(%d) = %v, want %v", tt.delayMs, got, tt.want)
			}
		})
	}
}

// TestAwaitDelay covers the timer/select wrapper itself: it actually waits
// out a small delay, returns immediately for delayMs<=0, and bails out via
// ctx.Done() rather than the full delay when the caller's context is
// canceled first — DESIGN's "cancellable via the request context" (5a).
func TestAwaitDelay(t *testing.T) {
	t.Run("zero delay returns immediately", func(t *testing.T) {
		start := time.Now()
		if !awaitDelay(t.Context(), 0) {
			t.Fatal("awaitDelay(0) = false, want true")
		}
		if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
			t.Errorf("elapsed = %v, want effectively instant for delayMs<=0", elapsed)
		}
	})

	t.Run("positive delay actually waits", func(t *testing.T) {
		const delay = 30 * time.Millisecond
		start := time.Now()
		if !awaitDelay(t.Context(), int(delay/time.Millisecond)) {
			t.Fatal("awaitDelay = false, want true (context never canceled)")
		}
		if elapsed := time.Since(start); elapsed < delay {
			t.Errorf("elapsed = %v, want at least %v", elapsed, delay)
		}
	})

	t.Run("canceled context returns false well before a long delay elapses", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		start := time.Now()
		go func() {
			time.Sleep(10 * time.Millisecond)
			cancel()
		}()
		// A delay far longer than the cancellation: if awaitDelay ignored
		// ctx, this would block for the full 5s and the test would time out.
		if awaitDelay(ctx, 5000) {
			t.Fatal("awaitDelay = true, want false (context was canceled mid-sleep)")
		}
		if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
			t.Errorf("elapsed = %v, want well under the 5s delay (cancellation must cut it short)", elapsed)
		}
	})
}

// ---------------------------------------------------------------------
// serveGenerated: end-to-end through the real method, bypassing ServeHTTP.
// ---------------------------------------------------------------------

// respondTestPlane builds a *Plane with a nil Source and nil SpecSource:
// every test below calls p.serveGenerated directly with a hand-built
// *runtime, so neither is ever consulted — only p.cfg (unused by
// serveGenerated itself, but required by New) and p.log (used for error
// logging) matter here.
func respondTestPlane() *Plane {
	return New(runtimeTestConfig(4<<20, 32), nil, nil, runtimeTestLogger())
}

// respondTestPlaneWithLiveState is respondTestPlane's P1c2 counterpart: a
// Plane with src wired via [Plane.SetLiveState], for the tests below that
// prove the live-state layer's own precedence over when[]/active_status —
// every other respond_test.go test deliberately leaves this nil, proving
// HARD RULE 6 the other way (a Plane that never calls SetLiveState serves
// exactly as it did before this phase).
func respondTestPlaneWithLiveState(src LiveStateSource) *Plane {
	p := respondTestPlane()
	p.SetLiveState(src)
	return p
}

func respondTestWorkspace() *workspaces.Workspace {
	return &workspaces.Workspace{ID: 1, Slug: "alex", Settings: domain.DefaultSettings()}
}

// mustResolver loads doc and builds a resolver over it the same way
// buildRuntime does (runtime.go), for tests that construct a *runtime by
// hand instead of going through SpecSource/runtimeFor.
func mustResolver(t *testing.T, doc string) *openapi.Resolver {
	t.Helper()
	d, _, err := openapi.Load([]byte(doc))
	if err != nil {
		t.Fatalf("openapi.Load: %v", err)
	}
	return openapi.NewResolver(d, openapi.DefaultRefBudget)
}

// fixtureRuntime builds a *runtime directly from a document, route table and
// variants map — the respond_test.go counterpart to runtime_test.go's
// widgetsSource()/runtimeFor path, skipping SpecSource entirely since these
// tests call serveGenerated (not runtimeFor) and need full control over
// settings per case.
func fixtureRuntime(t *testing.T, doc string, routes []router.Route, variants map[int64][]gen.ResponseVariant, settings domain.Settings) *runtime {
	t.Helper()
	return &runtime{
		table: router.Build(routes, settings.BasePath),
		gen: gen.New(mustResolver(t, doc), gen.Options{
			Seed:     settings.Seed,
			ListSize: settings.ListSize,
			NullRate: settings.NullRate,
			MaxBytes: 4 << 20,
			// Identity/Auth mirror buildRuntime's own step 4 (runtime.go):
			// without them here, an identity/jwt recipe test would see a
			// zero domain.Identity/domain.AuthSettings no matter what the
			// fixture's own `settings` carries, and would be proving
			// nothing about the real wiring.
			Identity: settings.Identity,
			Auth:     settings.Auth,
		}),
		variants: variants,
		settings: settings,
	}
}

// fixtureRuntimeWithOverrides is fixtureRuntime's P1c counterpart: the same
// runtime, with op_overrides rows attached and compiled directly onto the
// unexported fields — this file is white-box (package mockplane), so it can
// do exactly what runtime_test.go's own fixtures do (e.g.
// TestLookupOverride_KeysOnPathNotCanonicalPath's `&runtime{overrides: ...}`)
// rather than standing up a real OverrideSource/SpecSource round trip just to
// exercise serveGenerated's application of them.
func fixtureRuntimeWithOverrides(t *testing.T, doc string, routes []router.Route, variants map[int64][]gen.ResponseVariant, settings domain.Settings, rows map[string]*overrides.Row) *runtime {
	t.Helper()
	rt := fixtureRuntime(t, doc, routes, variants, settings)
	rt.overrides = rows
	rt.recipeSets = buildRecipeSets(runtimeTestLogger(), "test", rows)
	return rt
}

func mustMatch(t *testing.T, rt *runtime, method, path string) *router.Match {
	t.Helper()
	m, ok := rt.table.Match(method, NormalizeSegments(path))
	if !ok {
		t.Fatalf("no match for %s %s in the fixture table", method, path)
	}
	return m
}

// blankDoc is used by every test below that never resolves a schema
// (SchemaPtr == "" on every variant it exercises) — status-selection,
// 204-suppression and no-variant tests care only about which HTTPStatus and
// how much (if any) body reaches the wire, not its content.
const blankDoc = `{"openapi":"3.0.3","info":{"title":"t","version":"1.0.0"},"paths":{}}`

// TestServeGenerated_VariantChoice is TestChooseVariant's end-to-end twin:
// the same DESIGN §7 step 5 cases, now proven through the real HTTP status
// serveGenerated actually writes, across a spread the task brief names by
// name: {200, 201, 204, default}.
func TestServeGenerated_VariantChoice(t *testing.T) {
	v := func(sel string, status int) gen.ResponseVariant {
		return gen.ResponseVariant{OpRowID: 1, Selector: sel, HTTPStatus: status}
	}
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}

	tests := []struct {
		name       string
		variants   []gen.ResponseVariant
		wantStatus int
	}{
		{"201 beats a higher numeric 2xx and default", []gen.ResponseVariant{v("201", 201), v("204", 204), v("default", 200)}, 201},
		{"a bare 204 wins when nothing lower exists", []gen.ResponseVariant{v("204", 204), v("default", 200)}, 204},
		{"2XX wins over default when no numeric 2xx exists", []gen.ResponseVariant{v("2XX", 200), v("default", 200)}, 200},
		{"default alone", []gen.ResponseVariant{v("default", 200)}, 200},
	}

	p := respondTestPlane()
	ws := respondTestWorkspace()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := fixtureRuntime(t, blankDoc, []router.Route{route},
				map[int64][]gen.ResponseVariant{1: tt.variants}, domain.DefaultSettings())
			m := mustMatch(t, rt, "GET", "/items")

			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

			if rec.Code != tt.wantStatus {
				t.Errorf("status = %d, want %d; body=%s", rec.Code, tt.wantStatus, rec.Body)
			}
		})
	}
}

// TestServeGenerated_NoVariants proves "the route exists, the document just
// declared nothing" answers 200 with no body rather than 500.
func TestServeGenerated_NoVariants(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/nothing", CanonicalPath: "/nothing", SourceOrder: 1}
	rt := fixtureRuntime(t, blankDoc, []router.Route{route}, map[int64][]gen.ResponseVariant{}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/nothing")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/nothing", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// TestServeGenerated_Degraded proves an operation the indexer could not
// parse always answers an empty 200 (DESIGN §7), regardless of whatever
// status or content the variant row otherwise claims.
func TestServeGenerated_Degraded(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/broken", CanonicalPath: "/broken", SourceOrder: 1}
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "201", HTTPStatus: 201, MediaType: "application/json", Degraded: true}},
	}
	rt := fixtureRuntime(t, blankDoc, []router.Route{route}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/broken")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/broken", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (degraded always answers 200, never the variant's own 201)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty", rec.Body.String())
	}
}

// itemSchemaDoc declares GET /items -> 200 application/json object
// {id, name}, both required — the fixture every negotiation/envelope test
// below reuses.
const itemSchemaDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                  "required": ["id", "name"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

func itemVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID:    1,
		Selector:   "200",
		HTTPStatus: 200,
		MediaType:  "application/json",
		SchemaPtr:  "#/paths/~1items/get/responses/200/content/application~1json/schema",
		OpPointer:  "#/paths/~1items/get",
	}
}

func itemRoute() router.Route {
	return router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}
}

// TestServeGenerated_406OnIncompatibleAccept and its accept twin cover
// DESIGN §9's "явный fallback и 406" at the real HTTP layer: an Accept that
// excludes the declared type gets 406 with mocker's own standard error
// body, never a 200 carrying the wrong Content-Type.
func TestServeGenerated_406OnIncompatibleAccept(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req.Header.Set("Accept", "text/plain")
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotAcceptable {
		t.Fatalf("status = %d, want 406; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Error struct {
			Code string `json:"code"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode 406 body: %v; body=%s", err, rec.Body)
	}
	if body.Error.Code != "not_acceptable" {
		t.Errorf("error.code = %q, want not_acceptable", body.Error.Code)
	}
}

func TestServeGenerated_AcceptCompatibleSucceeds(t *testing.T) {
	tests := []string{"", "*/*", "application/json", "application/*", "application/json;q=0.5, text/plain;q=0.1"}
	for _, accept := range tests {
		t.Run("Accept="+accept, func(t *testing.T) {
			rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
				map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
			m := mustMatch(t, rt, "GET", "/items")

			p := respondTestPlane()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			if accept != "" {
				req.Header.Set("Accept", accept)
			}
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
			}
		})
	}
}

// TestServeGenerated_EnvelopeWrapsWhenSet and its siblings cover DESIGN's
// {"<envelope>": ...} wrap: applied when configured and there is a body,
// never applied when unset, and never applied over an empty body.
func TestServeGenerated_EnvelopeWrapsWhenSet(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var wrapped struct {
		Data struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &wrapped); err != nil {
		t.Fatalf("decode enveloped body: %v; body=%s", err, rec.Body)
	}
	if wrapped.Data.Name == "" {
		t.Errorf("wrapped.data.name is empty, want the generated field to have survived the wrap: %s", rec.Body)
	}
}

func TestServeGenerated_NoEnvelopeWhenUnset(t *testing.T) {
	settings := domain.DefaultSettings() // Envelope is nil by default
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v; body=%s", err, rec.Body)
	}
	if _, wrapped := body["data"]; wrapped {
		t.Errorf("body = %s, want the bare object, not wrapped under any envelope key", rec.Body)
	}
	if _, ok := body["id"]; !ok {
		t.Errorf("body = %s, want the generated \"id\" field directly at the top level", rec.Body)
	}
}

// TestServeGenerated_EnvelopeNotAppliedToEmptyBody proves item 6's "not
// applied to an empty body": a 204 (never a body, DESIGN §9) with an
// envelope configured must still come back with a truly empty wire body,
// never something like {"data":null}.
func TestServeGenerated_EnvelopeNotAppliedToEmptyBody(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope
	variants := map[int64][]gen.ResponseVariant{
		1: {{OpRowID: 1, Selector: "204", HTTPStatus: 204}},
	}
	rt := fixtureRuntime(t, blankDoc, []router.Route{itemRoute()}, variants, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want truly empty, not an envelope wrapper around nothing", rec.Body.String())
	}
}

// declaredContent204Doc mirrors the real acceptance document's own measured
// spec bug (gen.go's Body doc comment): a 204 response that nonetheless
// declares application/json content with a real schema. [gen.Generator.Body]
// WILL generate a body for it — the point of this test is that serveGenerated
// suppresses it on the wire regardless, since 204/205 never have a body
// (DESIGN §9), full stop.
const declaredContent204Doc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "204": {
            "description": "no content, but a schema anyway (spec bug, measured real)",
            "content": {
              "application/json": {
                "schema": { "type": "object", "properties": { "id": { "type": "integer" } } }
              }
            }
          }
        }
      }
    }
  }
}`

func TestServeGenerated_204SuppressesBodyEvenWhenSchemaDeclared(t *testing.T) {
	variants := map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID:    1,
			Selector:   "204",
			HTTPStatus: 204,
			MediaType:  "application/json",
			SchemaPtr:  "#/paths/~1items/get/responses/204/content/application~1json/schema",
			OpPointer:  "#/paths/~1items/get",
		}},
	}
	rt := fixtureRuntime(t, declaredContent204Doc, []router.Route{itemRoute()}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	// Sanity check on the premise: gen.Generator really would produce a
	// non-empty body for this variant if asked directly — proving the
	// emptiness on the wire below comes from serveGenerated's own
	// suppression, not from gen happening to return nothing.
	if b, err := rt.gen.Body(variants[1][0], gen.Request{Method: "GET", CanonicalPath: "/items", Status: 204}); err != nil || len(b) == 0 {
		t.Fatalf("premise check: gen.Body = (%s, %v), want a real non-empty body for this declared schema", b, err)
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: 204 never has a body regardless of what the schema declares", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset on a 204", ct)
	}
}

// TestServeGenerated_HEADKeepsHeadersButSuppressesBody mirrors what
// plane.go's serveResolved actually does for a HEAD request — wrap w in
// headWriter before Step 5 ever runs — and proves the GET route's
// Content-Type/Content-Length still reach the recorder while zero body
// bytes do, satisfying requirement 4 without any HEAD-specific branch in
// serveGenerated itself.
func TestServeGenerated_HEADKeepsHeadersButSuppressesBody(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	// Table.Match resolves HEAD against the GET bucket itself (router's own
	// contract) — this is exactly what routes.go hands serveGenerated for a
	// real HEAD request.
	m := mustMatch(t, rt, http.MethodHead, "/items")
	if m.Route.Method != http.MethodGet {
		t.Fatalf("matched route method = %q, want GET (HEAD resolves against the GET route)", m.Route.Method)
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	hw := &headWriter{ResponseWriter: rec}
	p.serveGenerated(hw, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); !strings.Contains(ct, "json") {
		t.Errorf("Content-Type = %q, want the JSON type a GET would have sent", ct)
	}
	cl := rec.Header().Get("Content-Length")
	if cl == "" || cl == "0" {
		t.Errorf("Content-Length = %q, want the non-zero length a GET would have sent", cl)
	}
}

// widgetsFamilyDoc declares the pair DESIGN §9's list contract calls "list
// row == detail card": GET /widgets (a top-level array of {id, name}) and
// GET /widgets/{id} (the same item shape, singular).
const widgetsFamilyDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/widgets": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                    "required": ["id", "name"]
                  }
                }
              }
            }
          }
        }
      }
    },
    "/widgets/{id}": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                  "required": ["id", "name"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

// TestServeGenerated_DetailRouteMatchesListRow is the direct proof of the
// property the phase digest calls "getting it wrong silently produces two
// different people": a detail route's ListFamily, computed from the real
// route table (router.ListFamily), must make its generated object identical —
// same id, same name — to the list route's own row for that same id.
func TestServeGenerated_DetailRouteMatchesListRow(t *testing.T) {
	routes := []router.Route{
		{OpRowID: 1, Method: "GET", Path: "/widgets", CanonicalPath: "/widgets", SourceOrder: 1},
		{OpRowID: 2, Method: "GET", Path: "/widgets/{id}", CanonicalPath: "/widgets/{}", SourceOrder: 2},
	}
	listVariant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets/get",
	}
	detailVariant := gen.ResponseVariant{
		OpRowID: 2, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1widgets~1{id}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1widgets~1{id}/get",
	}
	variants := map[int64][]gen.ResponseVariant{1: {listVariant}, 2: {detailVariant}}

	settings := domain.DefaultSettings()
	settings.ListSize = 5
	rt := fixtureRuntime(t, widgetsFamilyDoc, routes, variants, settings)

	p := respondTestPlane()
	ws := respondTestWorkspace()

	// 1. Fetch the list, and pick one row's identity.
	listReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/widgets", nil)
	listRec := httptest.NewRecorder()
	p.serveGenerated(listRec, listReq, ws, rt, mustMatch(t, rt, "GET", "/widgets"), resources.ScopeKey(""))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body)
	}
	var items []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list body: %v; body=%s", err, listRec.Body)
	}
	if len(items) == 0 {
		t.Fatal("list returned no items, nothing to compare against")
	}
	row := items[2%len(items)]

	// 2. Fetch the detail route for that SAME id.
	detailPath := "/widgets/" + strconv.Itoa(row.ID)
	detailReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+detailPath, nil)
	detailRec := httptest.NewRecorder()
	p.serveGenerated(detailRec, detailReq, ws, rt, mustMatch(t, rt, "GET", detailPath), resources.ScopeKey(""))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", detailRec.Code, detailRec.Body)
	}
	var card struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode detail body: %v; body=%s", err, detailRec.Body)
	}

	if card.ID != row.ID {
		t.Errorf("detail card id = %d, want %d (the requested id, DESIGN §9's identity write-back)", card.ID, row.ID)
	}
	if card.Name != row.Name {
		t.Errorf("detail card name = %q, want %q (list row and detail card for the same id must be the SAME person)", card.Name, row.Name)
	}
}

// cohortsFamilyDoc mirrors the real acceptance-document route shape the
// finding names: GET /tenants/{tenantId}/cohorts (a list) and
// GET /tenants/{tenantId}/cohorts/{cohortId} (its detail route),
// where BOTH path parameters end in "Id" and so both look id-shaped by
// name alone.
const cohortsFamilyDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/tenants/{tenantId}/cohorts": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                    "required": ["id", "name"]
                  }
                }
              }
            }
          }
        }
      }
    },
    "/tenants/{tenantId}/cohorts/{cohortId}": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "id": { "type": "integer" }, "name": { "type": "string" } },
                  "required": ["id", "name"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

// TestServeGenerated_DetailRouteMatchesListRow_TwoIDShapedPathParams is the
// regression test for the BLOCKER finding: a detail route with TWO path
// parameters that both look id-shaped by name ("tenantId" and
// "cohortId") must still resolve its OWN id ("cohortId") rather than the
// outer, shared "tenantId" a lexicographic tie-break over an
// unordered PathParams map would have picked instead. Without the fix, the
// family seedList is keyed by the wrong parameter and the detail route
// answers a completely different object than the list row for the
// requested id.
func TestServeGenerated_DetailRouteMatchesListRow_TwoIDShapedPathParams(t *testing.T) {
	routes := []router.Route{
		{OpRowID: 1, Method: "GET", Path: "/tenants/{tenantId}/cohorts", CanonicalPath: "/tenants/{}/cohorts", SourceOrder: 1},
		{OpRowID: 2, Method: "GET", Path: "/tenants/{tenantId}/cohorts/{cohortId}", CanonicalPath: "/tenants/{}/cohorts/{}", SourceOrder: 2},
	}
	listVariant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1tenants~1{tenantId}~1cohorts/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1tenants~1{tenantId}~1cohorts/get",
	}
	detailVariant := gen.ResponseVariant{
		OpRowID: 2, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1tenants~1{tenantId}~1cohorts~1{cohortId}/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1tenants~1{tenantId}~1cohorts~1{cohortId}/get",
	}
	variants := map[int64][]gen.ResponseVariant{1: {listVariant}, 2: {detailVariant}}

	settings := domain.DefaultSettings()
	settings.ListSize = 5
	rt := fixtureRuntime(t, cohortsFamilyDoc, routes, variants, settings)

	p := respondTestPlane()
	ws := respondTestWorkspace()

	// 1. Fetch the list under tenant 5, and pick one row's identity.
	listReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/tenants/5/cohorts", nil)
	listRec := httptest.NewRecorder()
	p.serveGenerated(listRec, listReq, ws, rt, mustMatch(t, rt, "GET", "/tenants/5/cohorts"), resources.ScopeKey(""))
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, want 200; body=%s", listRec.Code, listRec.Body)
	}
	var items []struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(listRec.Body.Bytes(), &items); err != nil {
		t.Fatalf("decode list body: %v; body=%s", err, listRec.Body)
	}
	if len(items) == 0 {
		t.Fatal("list returned no items, nothing to compare against")
	}
	row := items[2%len(items)]

	// 2. Fetch the detail route for that SAME cohort id, under the SAME
	// tenant. A wrong id-param pick (tenantId instead of
	// cohortId) would key the family seedList differently and answer a
	// different object entirely — not merely a mismatched id.
	detailPath := "/tenants/5/cohorts/" + strconv.Itoa(row.ID)
	detailReq := httptest.NewRequest(http.MethodGet, "http://alex.mock.local"+detailPath, nil)
	detailRec := httptest.NewRecorder()
	p.serveGenerated(detailRec, detailReq, ws, rt, mustMatch(t, rt, "GET", detailPath), resources.ScopeKey(""))
	if detailRec.Code != http.StatusOK {
		t.Fatalf("detail status = %d, want 200; body=%s", detailRec.Code, detailRec.Body)
	}
	var card struct {
		ID   int    `json:"id"`
		Name string `json:"name"`
	}
	if err := json.Unmarshal(detailRec.Body.Bytes(), &card); err != nil {
		t.Fatalf("decode detail body: %v; body=%s", err, detailRec.Body)
	}

	if card.ID != row.ID {
		t.Errorf("detail card id = %d, want %d (the requested cohortId, not tenantId)", card.ID, row.ID)
	}
	if card.Name != row.Name {
		t.Errorf("detail card name = %q, want %q (list row and detail card for the same cohort must be the SAME person)", card.Name, row.Name)
	}
}

// unsatisfiableDoc's schema cannot be satisfied by any value
// (minimum > maximum): [gen.Generator.Body] returns [gen.ErrUnsatisfiable]
// for it, the fixture requirement 9 needs.
const unsatisfiableDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/bad": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": { "type": "integer", "minimum": 10, "maximum": 5 }
              }
            }
          }
        }
      }
    }
  }
}`

// TestServeGenerated_GenerationErrorNeverBecomesA500 proves item 9: a
// schema gen itself cannot satisfy answers the declared status with the
// declared Content-Type and an empty body — never a 500, and the error is
// only logged, never surfaced to the client.
func TestServeGenerated_GenerationErrorNeverBecomesA500(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/bad", CanonicalPath: "/bad", SourceOrder: 1}
	variants := map[int64][]gen.ResponseVariant{
		1: {{
			OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
			SchemaPtr: "#/paths/~1bad/get/responses/200/content/application~1json/schema",
			OpPointer: "#/paths/~1bad/get",
		}},
	}
	rt := fixtureRuntime(t, unsatisfiableDoc, []router.Route{route}, variants, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/bad")

	// Sanity check on the premise: this schema really does fail generation.
	if _, err := rt.gen.Body(variants[1][0], gen.Request{Method: "GET", CanonicalPath: "/bad", Status: 200}); err == nil {
		t.Fatal("premise check: gen.Body succeeded, want an error for an unsatisfiable schema")
	}

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/bad", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the declared status, never a 500)", rec.Code)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty (the most honest answer to an ungenerable schema)", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want the declared type even though generation failed", ct)
	}
}

// TestServeGenerated_ContentLengthMatchesBodyWithinMaxResponse covers item
// 10 end to end: Content-Type set exactly once (no duplicate/garbled
// header), Content-Length consistent with the actual bytes written, and the
// body never exceeding cfg.MaxResponse — asserted here, not re-enforced
// (the generator already bounds it, per gen.Options.MaxBytes).
func TestServeGenerated_ContentLengthMatchesBodyWithinMaxResponse(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ctValues := rec.Header().Values("Content-Type"); len(ctValues) != 1 {
		t.Errorf("Content-Type set %d times, want exactly once: %v", len(ctValues), ctValues)
	}
	wantLen := strconv.Itoa(rec.Body.Len())
	if got := rec.Header().Get("Content-Length"); got != wantLen {
		t.Errorf("Content-Length = %q, want %q (the actual byte count written)", got, wantLen)
	}
	const cfgMaxResponse = 4 << 20
	if rec.Body.Len() > cfgMaxResponse {
		t.Errorf("body = %d bytes, want at most cfg.MaxResponse (%d)", rec.Body.Len(), cfgMaxResponse)
	}
}

// TestServeGenerated_DelaySleepsBeforeWriting proves settings.DelayMs is
// actually applied before anything is written (5a): the wall-clock time for
// one call must be at least the configured delay.
func TestServeGenerated_DelaySleepsBeforeWriting(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 30
	rt := fixtureRuntime(t, blankDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {{OpRowID: 1, Selector: "default", HTTPStatus: 200}}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the configured 30ms delay", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestServeGenerated_DelayCanceledWritesNothing proves the other half of
// 5a: a context canceled mid-delay must leave serveGenerated writing
// nothing at all — no status, no headers, no body — rather than answering
// late to a client (or shutdown drain) that already gave up.
func TestServeGenerated_DelayCanceledWritesNothing(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 5000 // far longer than the cancellation below
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under the 5s delay (cancellation must cut it short)", elapsed)
	}

	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled delay", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset (nothing should have been written at all)", ct)
	}
}

// csvDoc declares a text/csv 200 response for GET /export — a non-JSON
// media type this document has exactly one real example of (DESIGN §9's
// "для не-JSON — плейсхолдер по типу").
const csvDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/export": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "text/csv": {
                "schema": { "type": "string" }
              }
            }
          }
        }
      }
    }
  }
}`

// TestServeGenerated_NonJSONMediaTypeGetsItsOwnContentType is item 8: a
// non-JSON media type gets a placeholder body of the RIGHT type — never
// JSON with the wrong Content-Type — and item 6's "not applied to
// non-JSON": an envelope configured on the workspace must not corrupt it.
func TestServeGenerated_NonJSONMediaTypeGetsItsOwnContentType(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope // must NOT apply to this non-JSON body

	route := router.Route{OpRowID: 1, Method: "GET", Path: "/export", CanonicalPath: "/export", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/csv",
		SchemaPtr: "#/paths/~1export/get/responses/200/content/text~1csv/schema",
		OpPointer: "#/paths/~1export/get",
	}
	rt := fixtureRuntime(t, csvDoc, []router.Route{route}, map[int64][]gen.ResponseVariant{1: {variant}}, settings)
	m := mustMatch(t, rt, "GET", "/export")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/export", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/csv" {
		t.Errorf("Content-Type = %q, want text/csv (the declared type, never application/json)", ct)
	}
	if json.Valid(rec.Body.Bytes()) {
		// A raw generated string can coincidentally look like valid JSON
		// (e.g. a bare quoted word), but it must never have been silently
		// re-encoded as a JSON string (which would double-quote it) or
		// wrapped in an envelope object — either would also happen to
		// produce "valid JSON" bytes here, so check the un-enveloped shape
		// directly instead of relying on json.Valid alone.
		var probe map[string]any
		if err := json.Unmarshal(rec.Body.Bytes(), &probe); err == nil {
			t.Errorf("body = %s, looks like a JSON object — envelope must not apply to a non-JSON media type", rec.Body)
		}
	}
}

// ---------------------------------------------------------------------
// P1c: op_overrides applied at request time — route_off, override_on,
// active_status, mode "pinned", mode "generated" with recipes, list_size,
// delay_ms. Every test below that wants "no override" as its baseline
// reuses fixtureRuntime/itemRoute/itemVariant/itemSchemaDoc unchanged, so a
// bare diff against the pre-P1c tests above IS the "no recipes must mean no
// change" proof for this file's own fixtures — HARD RULE 6's own guard
// (golden_p1b_test.go, internal/gen) covers the acceptance document.
// ---------------------------------------------------------------------

// itemOverrideKey is the op_overrides key every fixture below binds a row
// under: itemRoute/itemVariant's own GET /items, exactly as
// overrides.OpKey(route.Method, route.Path) computes it in production.
func itemOverrideKey() string { return overrides.OpKey("GET", "/items") }

// TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute is the direct
// proof of DESIGN §8: a switched-off operation must be indistinguishable
// from one the route table never had at all — compared byte-for-byte
// against what [Plane.serveNoRoute] itself answers for a genuinely
// unmatched path, not merely "some 404".
func TestServeGenerated_RouteOff_Answers404LikeUnmatchedRoute(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, RouteOff: true,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ws := respondTestWorkspace()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route_off answers the same 404 an unmatched route gets); body=%s", rec.Code, rec.Body)
	}

	want := httptest.NewRecorder()
	p.serveNoRoute(want, req, ws, NormalizeSegments(req.URL.EscapedPath()))
	if rec.Body.String() != want.Body.String() {
		t.Errorf("route_off body = %s, want the exact unmatched-route 404 body %s", rec.Body, want.Body)
	}
	if rec.Header().Get("Content-Type") != want.Header().Get("Content-Type") {
		t.Errorf("route_off Content-Type = %q, want %q", rec.Header().Get("Content-Type"), want.Header().Get("Content-Type"))
	}
}

// TestServeGenerated_OverrideOnFalse_IsInert proves the gate every other
// P1c branch depends on: a row that EXISTS but has OverrideOn=false must
// leave route_off, active_status, mode "pinned", delay_ms AND when[] (P1c2)
// all unconsulted — the operation serves exactly as if there were no row at
// all, not "the row minus whichever one field a caller happened to check".
func TestServeGenerated_OverrideOnFalse_IsInert(t *testing.T) {
	pinnedStatus := 201
	rowDelay := 300 // large enough to notice if wrongly applied, small enough to keep the test fast
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: false, // <- the row is switched off wholesale
			RouteOff:     true,
			ActiveStatus: &pinnedStatus,
			DelayMs:      &rowDelay,
			ListSize:     &overrides.ListSize{Min: 1, Max: 1},
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "pinned",
					Body: json.RawMessage(`{"id":999,"name":"should-not-appear"}`),
				},
				// A when[] that WOULD match the request built below, if
				// SelectWhen were ever consulted for a switched-off row —
				// it must not be: overrideActive gates that call exactly
				// like every other branch here (respond.go's own comment).
				"409": {When: []overrides.Condition{{In: "header", Name: "X-Debug", Op: "exists"}}},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	req.Header.Set("X-Debug", "1") // matches the "409" variant's own when[], were it ever consulted
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 150*time.Millisecond {
		t.Fatalf("elapsed = %v, want near-instant: OverrideOn=false must leave delay_ms unapplied too", elapsed)
	}

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 — RouteOff/when[]/ActiveStatus must all be ignored when OverrideOn is false; body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body["name"] == "should-not-appear" {
		t.Errorf("body = %s, want ordinary generation — a switched-off row must not leak its pinned body", rec.Body)
	}
}

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

// TestServeGenerated_PinnedMode_JSONBodyMediaTypeHeadersEnvelope covers mode
// "pinned" end to end: the row's own literal body served verbatim, its own
// MediaType (not the document's), its own Headers layered on top of the
// declared ones, and the workspace's envelope still applied — a pinned body
// is not exempt from the wire-shape contract the rest of the workspace
// agreed to.
func TestServeGenerated_PinnedMode_JSONBodyMediaTypeHeadersEnvelope(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":7,"name":"pinned-item"}`),
					MediaType: "application/vnd.custom+json",
					Headers:   map[string]string{"X-Pinned": "yes"},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/vnd.custom+json" {
		t.Errorf("Content-Type = %q, want the row's own pinned media type", ct)
	}
	if h := rec.Header().Get("X-Pinned"); h != "yes" {
		t.Errorf("X-Pinned = %q, want %q (the row's own header, layered on top of the declared ones)", h, "yes")
	}
	want := `{"data":{"id":7,"name":"pinned-item"}}`
	if rec.Body.String() != want {
		t.Errorf("body = %s, want %s (pinned body served verbatim, still enveloped)", rec.Body, want)
	}
}

// TestServeGenerated_PinnedMode_Base64Body covers the other body shape:
// BodyEncoding "base64" carries bytes that are not themselves valid JSON —
// decoded and served verbatim, and (since the media type is not JSON) never
// enveloped even though the workspace has one configured.
func TestServeGenerated_PinnedMode_Base64Body(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope // must NOT apply — non-JSON pinned media type

	const raw = "hello, pinned world"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	bodyJSON, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(bodyJSON),
					BodyEncoding: "base64",
					MediaType:    "text/plain",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want text/plain", ct)
	}
	if rec.Body.String() != raw {
		t.Errorf("body = %q, want the base64-decoded literal %q", rec.Body.String(), raw)
	}
}

// TestServeGenerated_PinnedMode_HEADKeepsHeadersButSuppressesBody is the
// pinned-mode twin of TestServeGenerated_HEADKeepsHeadersButSuppressesBody:
// HEAD against a pinned variant still reports the pinned Content-Type and a
// non-zero Content-Length, with zero body bytes actually written.
func TestServeGenerated_PinnedMode_HEADKeepsHeadersButSuppressesBody(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":1,"name":"x"}`),
					MediaType: "application/json",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, http.MethodHead, "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodHead, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	hw := &headWriter{ResponseWriter: rec}
	p.serveGenerated(hw, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("HEAD body = %q, want empty", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json (the pinned variant's own type)", ct)
	}
	cl := rec.Header().Get("Content-Length")
	if cl == "" || cl == "0" {
		t.Errorf("Content-Length = %q, want the non-zero length a GET would have sent", cl)
	}
}

// --- round-1 findings #3/#8/#9/#10: pinned mode's serve-time gates -------

// TestServeGenerated_PinnedMode_OversizedBodyRefused is round-1 findings
// #3/#8: a pinned body is written ONCE but served on every subsequent
// unauthenticated request, with nothing downstream re-measuring it unlike
// a generated body (bounded on every call by gen.Options.MaxBytes). This
// fixture attaches the override row directly (fixtureRuntimeWithOverrides
// is white-box and bypasses overrides.Repo.Put's own write-time cap
// entirely — see overrides.TestValidation_pinnedBodyOverLimit for that
// gate), so what is under test here is specifically respond.go's own
// serve-time re-check against the LIVE cfg.MaxResponse.
func TestServeGenerated_PinnedMode_OversizedBodyRefused(t *testing.T) {
	body := `{"id":7,"name":"` + strings.Repeat("a", 200) + `"}`
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {Mode: "pinned", Body: json.RawMessage(body), MediaType: "application/json"},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	// A live ceiling well under the pinned body's own size, proving the
	// CHECK is what refused it, not an empty body to begin with.
	p := New(runtimeTestConfig(64, 32), nil, nil, runtimeTestLogger())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %d bytes, want 0 — a pinned body over the live MaxResponse must be refused, not served", rec.Body.Len())
	}
}

// TestServeGenerated_PinnedMode_DangerousResolvedMediaTypeRefused is
// round-1 finding #9: admin's own write-time guard
// (dangerousMediaType) only ever inspects the OPERATOR's own
// MediaType — leaving the pinned variant's MediaType blank falls through
// to the document's declared type, which can itself be browser-executable.
// This must be refused at the EFFECTIVE (post-fallback) type, the one
// respond.go is actually about to send as Content-Type.
func TestServeGenerated_PinnedMode_DangerousResolvedMediaTypeRefused(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/page", CanonicalPath: "/page", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/html",
		OpPointer: "#/paths/~1page/get",
	}
	rows := map[string]*overrides.Row{
		overrides.OpKey("GET", "/page"): {
			Method: "GET", Path: "/page", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				// MediaType intentionally left blank: an operator relying on
				// the spec-declared type above, exactly the gap the write-time
				// guard cannot see.
				"200": {Mode: "pinned", Body: json.RawMessage(`"<script>alert(1)</script>"`)},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, blankDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/page")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/page", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html" {
		t.Errorf("Content-Type = %q, must never resolve to a browser-executable type for a pinned response with no mediaType of its own", ct)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — a pinned response resolving to a browser-executable media type must not be served", rec.Body.String())
	}
}

// TestServeGenerated_GeneratedMode_DangerousResolvedMediaTypeRefused is the
// sibling of the test above, and the one that used to pass while the plane was
// vulnerable. That gate read `pinned && dangerousResolvedMediaType(...)`, which
// is the same shape of qualifier the admin write gate carried as
// `Mode == "pinned" &&` — and it left the cheapest route wide open: a GENERATED
// body needs no override row at all. An imported document that declares
// text/html on a response whose example carries a <script> was served verbatim,
// status 200, straight out of gen.finalize (which returns a text/* string raw).
// The document is operator-supplied through POST /api/specs, so this was an
// admin write path to a same-origin script in MOCKER_ROUTING=path.
//
// Note there is NO overrides row here at all — that is the entire point.
//
// The document below declares a text/html response WITH a schema on purpose.
// The first version of this test used the blank fixture, whose generated body
// is empty for want of a schema — so "the body is empty" held whether the gate
// fired or not, and the test passed against the vulnerable build. A guard whose
// test cannot fail is the thing this whole file exists to prevent.
const dangerousHTMLDoc = `{
	"openapi": "3.0.3",
	"info": { "title": "t", "version": "1.0.0" },
	"paths": {
		"/page": {
			"get": {
				"responses": {
					"200": {
						"description": "ok",
						"content": {
							"text/html": {
								"schema": { "type": "string", "example": "<script>alert(document.cookie)</script>" }
							}
						}
					}
				}
			}
		}
	}
}`

func TestServeGenerated_GeneratedMode_DangerousResolvedMediaTypeRefused(t *testing.T) {
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/page", CanonicalPath: "/page", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "text/html",
		SchemaPtr: "#/paths/~1page/get/responses/200/content/text~1html/schema",
		OpPointer: "#/paths/~1page/get",
	}
	rt := fixtureRuntimeWithOverrides(t, dangerousHTMLDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), nil)
	m := mustMatch(t, rt, "GET", "/page")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/page", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if ct := rec.Header().Get("Content-Type"); ct == "text/html" {
		t.Errorf("Content-Type = %q — a GENERATED body must be refused at a browser-executable resolved type exactly as a pinned one is", ct)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty: nothing the mock plane produces may be served under a type a browser executes", rec.Body.String())
	}
}

// TestServeGenerated_PinnedMode_UnsafeHeaderNameDropped is round-1 finding
// #10: a declared/pinned header's VALUE was already sanitized for CR/LF,
// but its NAME was unrestricted — letting an override set Set-Cookie and
// (on DESIGN §16's one-origin path-routing mode) silently swap out or
// empty whichever admin teammate merely opens a shared mocked URL. An
// ordinary header name must still pass through untouched.
func TestServeGenerated_PinnedMode_UnsafeHeaderNameDropped(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:      "pinned",
					Body:      json.RawMessage(`{"id":7,"name":"pinned-item"}`),
					MediaType: "application/json",
					Headers: map[string]string{
						"Set-Cookie": "mocker_session=hijacked; Domain=mock.local; Path=/",
						"X-Pinned":   "yes",
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if sc := rec.Header().Get("Set-Cookie"); sc != "" {
		t.Errorf("Set-Cookie = %q, want empty — a pinned/declared header must never be able to set Set-Cookie", sc)
	}
	if h := rec.Header().Get("X-Pinned"); h != "yes" {
		t.Errorf("X-Pinned = %q, want %q — an ordinary header name must still pass through", h, "yes")
	}
}

// TestServeGenerated_GeneratedMode_ConstRecipeVisibleInOutput proves mode
// "generated" (the default) threads the row's own compiled recipe set
// through to gen.Request.Recipes: a const recipe bound to "name" must win
// over whatever gen would otherwise have generated there.
func TestServeGenerated_GeneratedMode_ConstRecipeVisibleInOutput(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"pinned-by-recipe"`)},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body.Name != "pinned-by-recipe" {
		t.Errorf("name = %q, want the const recipe's own value %q", body.Name, "pinned-by-recipe")
	}
}

// sessionSchemaDoc/sessionVariant/sessionRoute are the jwt-recipe fixture:
// GET /session -> 200 application/json {token: string} — separate from
// itemSchemaDoc because a jwt recipe needs a string-typed leaf to bind to,
// and item's own schema has none.
const sessionSchemaDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/session": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "object",
                  "properties": { "token": { "type": "string" } },
                  "required": ["token"]
                }
              }
            }
          }
        }
      }
    }
  }
}`

func sessionVariant() gen.ResponseVariant {
	return gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1session/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1session/get",
	}
}

func sessionRoute() router.Route {
	return router.Route{OpRowID: 1, Method: "GET", Path: "/session", CanonicalPath: "/session", SourceOrder: 1}
}

// TestServeGenerated_GeneratedMode_JWTRecipeProducesDecodableToken is the
// phase's own acceptance criterion (DESIGN §19 "фронт логинится") proven at
// the serving layer: a jwt recipe answers a structurally valid compact JWS
// whose payload decodes, and whose "sub" is the SAME identity id the
// workspace's own Settings.Identity carries — DESIGN §10's "the token's sub
// and the id served by the profile endpoint come from ONE identity".
func TestServeGenerated_GeneratedMode_JWTRecipeProducesDecodableToken(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.Identity = domain.Identity{ID: "u-42", Name: "Ada"}
	settings.Auth = domain.AuthSettings{JWTTTLSec: 900, Alg: "HS256", SigningKey: "test-signing-key"}

	rows := map[string]*overrides.Row{
		overrides.OpKey("GET", "/session"): {
			Method: "GET", Path: "/session", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"token": {Kind: recipes.KindJWT},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, sessionSchemaDoc, []router.Route{sessionRoute()},
		map[int64][]gen.ResponseVariant{1: {sessionVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/session")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/session", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	parts := strings.Split(body.Token, ".")
	if len(parts) != 3 {
		t.Fatalf("token = %q, want a 3-segment compact JWS", body.Token)
	}
	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		t.Fatalf("token payload does not decode as base64url: %v", err)
	}
	var claims map[string]any
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		t.Fatalf("token payload is not valid JSON: %v (%s)", err, payloadJSON)
	}
	if claims["sub"] != "u-42" {
		t.Errorf("claims[sub] = %v, want %q — the token's sub and the identity's own id must be the SAME (DESIGN §10)", claims["sub"], "u-42")
	}
	if claims["name"] != "Ada" {
		t.Errorf("claims[name] = %v, want %q", claims["name"], "Ada")
	}
	exp, ok := claims["exp"].(float64)
	if !ok || exp <= 0 {
		t.Fatalf("claims[exp] = %v, want a positive epoch-SECONDS expiry", claims["exp"])
	}
	iat, ok := claims["iat"].(float64)
	if !ok {
		t.Fatalf("claims[iat] missing")
	}
	if got, want := exp-iat, float64(900); got != want {
		t.Errorf("exp-iat = %v, want %v (the recipe's own workspace-default ttl, JWTTTLSec)", got, want)
	}
}

// TestServeGenerated_DelayMs_RowOverrideIsClamped is the direct, fast proof
// that a per-operation delay_ms goes through the exact same [clampedDelay]
// cap a workspace-wide settings.DelayMs already does — without a test ever
// having to wait out maxSimulatedDelay itself.
func TestServeGenerated_DelayMs_RowOverrideIsClamped(t *testing.T) {
	huge := 10_000_000 // far past maxSimulatedDelay
	if got := clampedDelay(effectiveDelayMs(0, &huge, 0)); got != maxSimulatedDelay {
		t.Errorf("clampedDelay(effectiveDelayMs(...)) = %v, want the shared cap %v — a per-operation delay_ms must not escape it", got, maxSimulatedDelay)
	}
}

// TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings proves
// the row's own delay_ms is what actually gets awaited when the workspace
// setting itself is zero — not merely that SOME delay happens.
func TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 0

	rowDelay := 30
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the row's own 30ms delay_ms (workspace settings.DelayMs is 0)", elapsed)
	}
}

// TestServeGenerated_DelayMs_RowOverrideCancellable mirrors
// TestServeGenerated_DelayCanceledWritesNothing for a row-sourced delay:
// canceling the request context mid-sleep must still cut it short and write
// nothing, proving the row's delay flows through the SAME awaitDelay
// call — not a second, uncancellable sleep path.
func TestServeGenerated_DelayMs_RowOverrideCancellable(t *testing.T) {
	settings := domain.DefaultSettings()
	rowDelay := 5000 // far longer than the cancellation below
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(10 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under the row's own 5s delay_ms (cancellation must cut it short)", elapsed)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled delay", rec.Body.String())
	}
}

// TestServeGenerated_ConcurrentRequestsWithRecipesBound is the -race
// regression the task brief asks for by name: many goroutines serving the
// SAME runtime, with a recipe bound, must never race on rt.recipeSets or
// anything serveGenerated derives from it.
func TestServeGenerated_ConcurrentRequestsWithRecipesBound(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode: "generated",
					Recipes: map[string]recipes.Recipe{
						"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"race-const"`)},
					},
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	ws := respondTestWorkspace()

	const n = 32
	var wg sync.WaitGroup
	wg.Add(n)
	for range n {
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
			rec := httptest.NewRecorder()
			p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
			if rec.Code != http.StatusOK {
				t.Errorf("status = %d, want 200", rec.Code)
				return
			}
			var body struct {
				Name string `json:"name"`
			}
			if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
				t.Errorf("decode: %v; body=%s", err, rec.Body)
				return
			}
			if body.Name != "race-const" {
				t.Errorf("name = %q, want %q", body.Name, "race-const")
			}
		}()
	}
	wg.Wait()
}

// ---------------------------------------------------------------------
// Pure unit tests for respond.go's own new small helpers: variantForStatus,
// pinnedBody, withListSizeRecipe.
// ---------------------------------------------------------------------

func TestVariantForStatus(t *testing.T) {
	variants := []gen.ResponseVariant{
		{Selector: "200", HTTPStatus: 200},
		{Selector: "404", HTTPStatus: 404},
	}
	if v, ok := variantForStatus(variants, 404); !ok || v.Selector != "404" {
		t.Errorf("variantForStatus(404) = (%+v, %v), want the 404 variant", v, ok)
	}
	if _, ok := variantForStatus(variants, 500); ok {
		t.Error("variantForStatus(500) found something, want ok=false: no variant declares it")
	}
}

func TestPinnedBody(t *testing.T) {
	t.Run("verbatim JSON body", func(t *testing.T) {
		ov := overrides.Variant{Body: json.RawMessage(`{"a":1}`)}
		got, err := pinnedBody(ov)
		if err != nil {
			t.Fatalf("pinnedBody: %v", err)
		}
		if string(got) != `{"a":1}` {
			t.Errorf("pinnedBody = %s, want the stored bytes verbatim", got)
		}
	})

	t.Run("base64 decodes to the original bytes", func(t *testing.T) {
		encoded, _ := json.Marshal(base64.StdEncoding.EncodeToString([]byte("raw bytes, not JSON")))
		ov := overrides.Variant{Body: json.RawMessage(encoded), BodyEncoding: "base64"}
		got, err := pinnedBody(ov)
		if err != nil {
			t.Fatalf("pinnedBody: %v", err)
		}
		if string(got) != "raw bytes, not JSON" {
			t.Errorf("pinnedBody = %q, want the decoded original", got)
		}
	})

	t.Run("malformed base64 fails cleanly, never panics", func(t *testing.T) {
		ov := overrides.Variant{Body: json.RawMessage(`"not-valid-base64!!"`), BodyEncoding: "base64"}
		if _, err := pinnedBody(ov); err == nil {
			t.Error("pinnedBody = nil error, want a decode failure surfaced, not silently swallowed")
		}
	})
}

func TestWithListSizeRecipe(t *testing.T) {
	t.Run("fixed size", func(t *testing.T) {
		set := withListSizeRecipe(nil, &overrides.ListSize{Min: 3, Max: 3})
		lo, hi, ok := set.ListSizeAt("")
		if !ok || lo != 3 || hi != 3 {
			t.Errorf("ListSizeAt(\"\") = (%d, %d, %v), want (3, 3, true)", lo, hi, ok)
		}
	})

	t.Run("range", func(t *testing.T) {
		set := withListSizeRecipe(nil, &overrides.ListSize{Min: 2, Max: 5})
		lo, hi, ok := set.ListSizeAt("")
		if !ok || lo != 2 || hi != 5 {
			t.Errorf("ListSizeAt(\"\") = (%d, %d, %v), want (2, 5, true)", lo, hi, ok)
		}
	})

	t.Run("layers on top of an existing compiled set without losing its other recipes", func(t *testing.T) {
		base, err := recipes.Compile(map[string]recipes.Recipe{
			"name": {Kind: recipes.KindConst, Data: json.RawMessage(`"x"`)},
		})
		if err != nil {
			t.Fatalf("recipes.Compile: %v", err)
		}
		merged := withListSizeRecipe(base, &overrides.ListSize{Min: 4, Max: 4})
		if _, ok := merged.Lookup("name"); !ok {
			t.Error("merged set lost the base recipe at \"name\"")
		}
		if lo, hi, ok := merged.ListSizeAt(""); !ok || lo != 4 || hi != 4 {
			t.Errorf("merged.ListSizeAt(\"\") = (%d, %d, %v), want (4, 4, true)", lo, hi, ok)
		}
	})
}

// TestEffectiveDelayMs is DESIGN §4's delay precedence table, pure and
// direct: SESSION beats ROW beats WORKSPACE settings — Session being the
// outermost of the four layers, exactly like a live-state status force
// already beats an override, which itself already beats the document.
func TestEffectiveDelayMs(t *testing.T) {
	rowDelay := 42
	tests := []struct {
		name       string
		sessionMs  int
		rowDelayMs *int
		settingsMs int
		want       int
	}{
		{"nothing set at all: the workspace setting wins", 0, nil, 10, 10},
		{"a row delay beats the workspace setting", 0, &rowDelay, 10, 42},
		{"a session delay beats a row delay AND the workspace setting", 300, &rowDelay, 10, 300},
		{"a session delay beats the workspace setting when there is no row", 300, nil, 10, 300},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := effectiveDelayMs(tt.sessionMs, tt.rowDelayMs, tt.settingsMs); got != tt.want {
				t.Errorf("effectiveDelayMs(%d, %v, %d) = %d, want %d", tt.sessionMs, tt.rowDelayMs, tt.settingsMs, got, tt.want)
			}
		})
	}
}

// ---------------------------------------------------------------------
// P2a: awaitPause/resolvePause, and serveGenerated's pause wiring.
// ---------------------------------------------------------------------

// planeP2aPause is this file's own tiny fixture builder for the pause
// tests below: a real [livestate.Store] with exactly one pause directive
// set on GET /items, and the [livestate.Effect] Apply hands back for it —
// [livestate.Store.Apply] itself never blocks (reserveParkLocked only
// increments a counter), so building this needs no goroutine of its own.
// Prefixed planeP2a, not just "pause...", so it cannot collide with a test
// helper another agent adds to this same package in this same run.
func planeP2aPause(t *testing.T, workspaceID int64) (*livestate.Store, livestate.Effect) {
	t.Helper()
	store := livestate.NewStore(0, nil)
	if err := store.Set(workspaceID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set(pause): %v", err)
	}
	eff := store.Apply(workspaceID, "GET", "/items")
	if !eff.Pause {
		t.Fatalf("Apply.Pause = false, want true")
	}
	return store, eff
}

// TestAwaitPause_NotPausedProceedsImmediately is the zero-Effect case every
// unpaused request gets: nothing to wait on, ctx/wake never touched.
func TestAwaitPause_NotPausedProceedsImmediately(t *testing.T) {
	if !awaitPause(t.Context(), livestate.Effect{}, time.Second) {
		t.Error("awaitPause(no pause) = false, want true")
	}
}

// TestAwaitPause_ReleasedByClearReturnsTrue is the ordinary release path: a
// pause matched, the operator clears it, and the wait ends without ever
// reaching the hold cap.
func TestAwaitPause_ReleasedByClearReturnsTrue(t *testing.T) {
	const wsID = 1
	store, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	done := make(chan bool, 1)
	go func() { done <- awaitPause(context.Background(), eff, 2*time.Second) }()

	time.Sleep(20 * time.Millisecond) // give the goroutine time to park
	store.Clear(wsID)

	select {
	case ok := <-done:
		if !ok {
			t.Error("awaitPause = false, want true (released, not a context cancel)")
		}
	case <-time.After(time.Second):
		t.Fatal("awaitPause never returned after Clear")
	}
}

// TestAwaitPause_UnrelatedSetWakesThenReparks is DESIGN §14 rule 1 end to
// end: a Set for a DIFFERENT target wakes the parked request (Wake is a
// change signal, not a pause-specific one), but the pause directive it was
// actually parked on still matches, so it re-parks rather than proceeding —
// only a real Clear ends the wait.
func TestAwaitPause_UnrelatedSetWakesThenReparks(t *testing.T) {
	const wsID = 1
	store, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	done := make(chan bool, 1)
	go func() { done <- awaitPause(context.Background(), eff, 2*time.Second) }()
	time.Sleep(20 * time.Millisecond)

	if err := store.Set(wsID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/other"}, Action: livestate.ActionStatus, Status: 500,
	}); err != nil {
		t.Fatalf("store.Set(unrelated): %v", err)
	}

	select {
	case <-done:
		t.Fatal("awaitPause returned after an unrelated Set; want it to re-check and re-park")
	case <-time.After(80 * time.Millisecond):
		// still parked — correct
	}

	store.Clear(wsID)
	select {
	case ok := <-done:
		if !ok {
			t.Error("awaitPause = false after Clear, want true")
		}
	case <-time.After(time.Second):
		t.Fatal("awaitPause never returned after Clear")
	}
}

// TestAwaitPause_ContextCanceledReturnsFalse proves ctx ends the wait even
// while the pause itself is still in force — the request is being torn
// down, not answered late.
func TestAwaitPause_ContextCanceledReturnsFalse(t *testing.T) {
	const wsID = 1
	_, eff := planeP2aPause(t, wsID)
	defer eff.Unpark()

	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	if awaitPause(ctx, eff, 5*time.Second) {
		t.Fatal("awaitPause = true, want false (context was canceled; the pause was never lifted)")
	}
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Errorf("elapsed = %v, want well under the 5s hold (cancellation must cut it short)", elapsed)
	}
}

// TestAwaitPause_HoldCapExpiresReturnsTrue is DESIGN §14 rule 3, proven
// without ever waiting out the real [livestate.MaxPauseHold]: hold is a
// parameter precisely so this is possible — the same split
// [clampedDelay]/[maxSimulatedDelay] already uses for the delay side.
func TestAwaitPause_HoldCapExpiresReturnsTrue(t *testing.T) {
	const wsID = 1
	_, eff := planeP2aPause(t, wsID) // never released: only the cap can end this wait
	defer eff.Unpark()

	const hold = 20 * time.Millisecond
	start := time.Now()
	if !awaitPause(context.Background(), eff, hold) {
		t.Fatal("awaitPause = false, want true (the cap must serve normally, never invent a status)")
	}
	if elapsed := time.Since(start); elapsed < hold {
		t.Errorf("elapsed = %v, want at least the %v hold", elapsed, hold)
	}
}

// TestResolvePause_NoEffectProceedsImmediately proves resolvePause is a
// no-op for a request with neither Pause nor Refused set — the zero Effect
// every request without a matching pause directive gets.
func TestResolvePause_NoEffectProceedsImmediately(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	if !resolvePause(req, livestate.Effect{}) {
		t.Error("resolvePause(zero Effect) = false, want true")
	}
}

// TestResolvePause_RefusedProceedsImmediately is DESIGN §14 rule 7: a
// Refused park (the bound was already full) is served normally with no
// wait at all — resolvePause never touches Wake/Recheck/Unpark for this
// case, which are all nil on a Refused Effect anyway (reserveParkLocked).
func TestResolvePause_RefusedProceedsImmediately(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	start := time.Now()
	if !resolvePause(req, livestate.Effect{Refused: true}) {
		t.Error("resolvePause(Refused) = false, want true (serve normally)")
	}
	if elapsed := time.Since(start); elapsed > 20*time.Millisecond {
		t.Errorf("elapsed = %v, want effectively instant — Refused must never wait", elapsed)
	}
}

// TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus is DESIGN
// §14's pause end to end through the real serving path: nothing is written
// to the response while parked (checked before any release fires), and once
// the operator clears the pause the request answers exactly the status it
// would have without ever having been paused at all.
func TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
		close(done)
	}()

	select {
	case <-done:
		t.Fatal("serveGenerated returned before the pause was ever released")
	case <-time.After(50 * time.Millisecond):
		// still parked — correct
	}
	if rec.Body.Len() != 0 || rec.Header().Get("Content-Type") != "" {
		t.Fatalf("wrote something while parked: body=%q content-type=%q", rec.Body.String(), rec.Header().Get("Content-Type"))
	}

	store.Clear(ws.ID)

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveGenerated never returned after Clear")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200 (the item route's own normal status, unaffected by having been paused)", rec.Code)
	}
}

// TestServeGenerated_Pause_ContextCanceledWritesNothing mirrors
// TestServeGenerated_DelayCanceledWritesNothing for a paused request:
// canceling r's own context while parked ends the wait writing nothing at
// all — the same "the request is being torn down" contract the delay side
// already has.
func TestServeGenerated_Pause_ContextCanceledWritesNothing(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	go func() {
		time.Sleep(15 * time.Millisecond)
		cancel()
	}()

	start := time.Now()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed > 500*time.Millisecond {
		t.Fatalf("elapsed = %v, want well under a second (cancellation must cut the park short)", elapsed)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want nothing written after a canceled park", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "" {
		t.Errorf("Content-Type = %q, want unset (nothing should have been written at all)", ct)
	}
}

// TestServeGenerated_Pause_DelayPaidOnceAfterRelease is DESIGN §14 rule 4:
// a matched delay is paid exactly once, AFTER the pause releases — never
// before the park, never per wakeup. A workspace holding both a pause and a
// delay is a legitimate "pause it, AND delay it once released" the operator
// asked for.
func TestServeGenerated_Pause_DelayPaidOnceAfterRelease(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 40
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings)
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"}, Action: livestate.ActionPause,
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	done := make(chan struct{})
	go func() {
		p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
		close(done)
	}()

	const parkFor = 60 * time.Millisecond
	time.Sleep(parkFor)
	store.Clear(ws.ID)

	// No mid-point peek at rec here: rec is owned by the background
	// goroutine from the moment it might start writing, and a bare
	// time.Sleep on this side establishes no happens-before edge the race
	// detector would honor — TestServeGenerated_Pause_ParksThenReleasedServesNormalStatus
	// already proves the "nothing written while parked" half, synchronized
	// through the Store's own locking. This test's job is purely the timing
	// math below: if the delay had been paid DURING the park instead of
	// after release, the two would overlap and total elapsed would fall
	// short of parkFor+40ms.
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("serveGenerated never returned")
	}
	if elapsed := time.Since(start); elapsed < parkFor+40*time.Millisecond {
		t.Errorf("elapsed = %v, want at least %v (parked %v, then the 40ms delay paid AFTER release)",
			elapsed, parkFor+40*time.Millisecond, parkFor)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// TestServeGenerated_DelayMs_SessionBeatsRowBeatsSettings is
// TestServeGenerated_DelayMs_RowOverrideAppliesOverWorkspaceSettings's P2a
// sibling: with a session (live-state) delay, a row delay AND a workspace
// setting all pinned on the same route, the session's own value is what
// actually gets awaited — DESIGN §4's Session is the outermost layer.
func TestServeGenerated_DelayMs_SessionBeatsRowBeatsSettings(t *testing.T) {
	settings := domain.DefaultSettings()
	settings.DelayMs = 10 // smallest: must lose to both the row and the session

	rowDelay := 40 // middle: must lose to the session
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true, DelayMs: &rowDelay,
			Responses: map[string]overrides.Variant{},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	ws := respondTestWorkspace()
	store := livestate.NewStore(0, nil)
	if err := store.Set(ws.ID, livestate.Directive{
		Target: livestate.Target{Method: "GET", Path: "/items"},
		Action: livestate.ActionDelay, Ms: 90, // largest: must win
	}); err != nil {
		t.Fatalf("store.Set: %v", err)
	}
	p := respondTestPlaneWithLiveState(store)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()

	start := time.Now()
	p.serveGenerated(rec, req, ws, rt, m, resources.ScopeKey(""))
	if elapsed := time.Since(start); elapsed < 90*time.Millisecond {
		t.Errorf("elapsed = %v, want at least the session's own 90ms delay (a row delay and a workspace setting are both pinned lower)", elapsed)
	}
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
}

// ---------------------------------------------------------------------
// P1c2: livestate + when[] status precedence.
// ---------------------------------------------------------------------

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

// --- P2f §C: branch cover added ahead of the serveGenerated split ---
//
// The five tests below close the gaps the branch enumeration in the P2f
// gate report found in respond_test.go's own suite (as opposed to coverage
// that exists only in internal/server's integration tests, which is not
// this package's guard — D1's "internal/mockplane's own suite" is scoped to
// this package). Written against CURRENT (pre-split) behaviour: the split
// itself must not change what any of these observe.

// TestServeGenerated_GeneratedMode_PatchedSchemaAppliesToGeneratedBody is
// the split's own reason d'être for reading rt.lookupPatchedSchema inside
// the generator-request block (respond.go:250-280): a patched root, looked
// up by the ALREADY-CHOSEN variant's (OpRowID, Selector), must reach
// gen.Request.PatchedSchema and visibly change the generated body — proven
// here directly against rt.patchedSchemas rather than through op_overrides'
// own SchemaPatch/jsonpatch pipeline (internal/server's C6 test already
// covers that construction step; this file's own fixtures never call
// buildPatchedSchemas at all, so this branch had no cover in this package
// before this test).
func TestServeGenerated_GeneratedMode_PatchedSchemaAppliesToGeneratedBody(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	// itemVariant()'s Selector is "200" and its OpRowID is 1 — the exact
	// key lookupPatchedSchema reads inside serveGenerated once the variant
	// is chosen.
	rt.patchedSchemas = map[patchedSchemaKey]map[string]any{
		{opRowID: 1, selector: "200"}: {
			"type": "object",
			"properties": map[string]any{
				"id":    map[string]any{"type": "integer"},
				"name":  map[string]any{"type": "string"},
				"patch": map[string]any{"type": "string", "const": "from-schema-patch"},
			},
			"required": []any{"id", "name", "patch"},
		},
	}
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if body["patch"] != "from-schema-patch" {
		t.Errorf(`body["patch"] = %v, want "from-schema-patch" — the patched root must reach gen.Body, not the unpatched spec schema; body=%s`, body["patch"], rec.Body)
	}
}

// TestServeGenerated_GeneratedMode_ListSizeOverrideConstrainsGeneratedList
// is the active twin of TestServeGenerated_OverrideOnFalse_IsInert's
// ListSize field: that test only proves ListSize does nothing when the row
// is switched off, never that it does something when the row is switched
// on. row.ListSize != nil (respond.go's withListSizeRecipe call inside the
// same overrideActive && !pinned block the patched-schema test above
// exercises) had no serveGenerated-level test that actually asserts the
// generated list length before this one.
func TestServeGenerated_GeneratedMode_ListSizeOverrideConstrainsGeneratedList(t *testing.T) {
	const listDoc = `{
  "openapi": "3.0.3",
  "info": { "title": "t", "version": "1.0.0" },
  "paths": {
    "/items": {
      "get": {
        "responses": {
          "200": {
            "description": "ok",
            "content": {
              "application/json": {
                "schema": {
                  "type": "array",
                  "items": {
                    "type": "object",
                    "properties": { "id": { "type": "integer" } },
                    "required": ["id"]
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
	route := router.Route{OpRowID: 1, Method: "GET", Path: "/items", CanonicalPath: "/items", SourceOrder: 1}
	variant := gen.ResponseVariant{
		OpRowID: 1, Selector: "200", HTTPStatus: 200, MediaType: "application/json",
		SchemaPtr: "#/paths/~1items/get/responses/200/content/application~1json/schema",
		OpPointer: "#/paths/~1items/get",
	}
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			ListSize: &overrides.ListSize{Min: 3, Max: 3},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, listDoc, []router.Route{route},
		map[int64][]gen.ResponseVariant{1: {variant}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var list []map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode: %v; body=%s", err, rec.Body)
	}
	if len(list) != 3 {
		t.Errorf("len(list) = %d, want 3 — row.ListSize must reach the generator through withListSizeRecipe; body=%s", len(list), rec.Body)
	}
}

// TestServeGenerated_PinnedMode_MalformedBase64AnswersEmptyBodyNot500 is
// pinnedBody's own malformed-base64 case (already unit-tested by
// TestPinnedBody), but reached through serveGenerated: the `case pinned:`
// arm's `genErr != nil` branch (respond.go's decode-pinned-response-body
// Error log, body set to nil) had no serveGenerated-level test — every
// existing pinned-mode test in this file decodes cleanly.
func TestServeGenerated_PinnedMode_MalformedBase64AnswersEmptyBodyNot500(t *testing.T) {
	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(`"not-valid-base64!!"`),
					BodyEncoding: "base64",
					MediaType:    "application/json",
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings(), rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (the declared status, never a 500); body=%s", rec.Code, rec.Body)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("body = %q, want empty — a pinned body that fails to decode must never be served", rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want the declared type even though decoding the pinned body failed", ct)
	}
}

// TestServeGenerated_PinnedMode_EnvelopeSkippedWhenDecodedBodyIsNotJSON is
// wrapEnvelope's own error path (respond.go's "skip envelope: body is not
// valid JSON" Debug log), reached through serveGenerated: a pinned base64
// body under a JSON-ish media type that decodes to bytes which are NOT
// themselves valid JSON. Every existing envelope test either has a JSON
// mediaType with genuinely-JSON bytes (wraps) or a non-JSON mediaType
// (httpx.IsJSONMediaType false, never calls wrapEnvelope at all) — this is the
// third combination: httpx.IsJSONMediaType true, wrapEnvelope itself refuses.
func TestServeGenerated_PinnedMode_EnvelopeSkippedWhenDecodedBodyIsNotJSON(t *testing.T) {
	settings := domain.DefaultSettings()
	envelope := "data"
	settings.Envelope = &envelope

	const raw = "not json at all"
	encoded := base64.StdEncoding.EncodeToString([]byte(raw))
	bodyJSON, err := json.Marshal(encoded)
	if err != nil {
		t.Fatal(err)
	}

	rows := map[string]*overrides.Row{
		itemOverrideKey(): {
			Method: "GET", Path: "/items", OverrideOn: true,
			Responses: map[string]overrides.Variant{
				"200": {
					Mode:         "pinned",
					Body:         json.RawMessage(bodyJSON),
					BodyEncoding: "base64",
					MediaType:    "application/json", // httpx.IsJSONMediaType true — wrapEnvelope IS attempted
				},
			},
		},
	}
	rt := fixtureRuntimeWithOverrides(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, settings, rows)
	m := mustMatch(t, rt, "GET", "/items")

	p := respondTestPlane()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
	rec := httptest.NewRecorder()
	p.serveGenerated(rec, req, respondTestWorkspace(), rt, m, resources.ScopeKey(""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if rec.Body.String() != raw {
		t.Errorf("body = %q, want the un-wrapped literal %q — a JSON-media-typed body that is not actually valid JSON must be left exactly as decoded, never corrupted by a failed wrap attempt", rec.Body.String(), raw)
	}
}

// failingResponseWriter is an http.ResponseWriter whose Write always fails,
// for the one branch httptest.ResponseRecorder can never reach on its own:
// respond.go's final w.Write(body) error path (the Debug "write generated
// response" log). Header/WriteHeader delegate to a real ResponseRecorder so
// status/headers stay observable; only the body write is made to fail.
type failingResponseWriter struct {
	*httptest.ResponseRecorder
}

func (w *failingResponseWriter) Write([]byte) (int, error) {
	return 0, fmt.Errorf("failingResponseWriter: simulated write failure")
}

// recordingHandler is a minimal slog.Handler that captures every record it
// is handed, guarded by a mutex because slog does not promise its caller
// runs handlers serially. It always reports Enabled — including Debug,
// which slog's own handlers filter out by default — so an absent record
// proves the call site never logged, never that the level was cut upstream.
// This is the only way to tell "checked the error and logged it" apart from
// "ignored it": status/headers/body are byte-identical either way, since
// Header()/WriteHeader() have already committed by the time Write runs.
type recordingHandler struct {
	mu      sync.Mutex
	records []slog.Record
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records = append(h.records, r.Clone())
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// debugRecord returns the first Debug-level record with the given message,
// or nil if none was emitted.
func (h *recordingHandler) debugRecord(msg string) *slog.Record {
	h.mu.Lock()
	defer h.mu.Unlock()
	for i := range h.records {
		if h.records[i].Level == slog.LevelDebug && h.records[i].Message == msg {
			rec := h.records[i]
			return &rec
		}
	}
	return nil
}

// TestServeGenerated_WriteFailureIsLoggedNotPanicked proves the final
// w.Write(asm.Body) error path (respond.go:238-240) does not panic, leaves
// the status/headers it already wrote intact, and — the discriminating half
// a bare status/body assertion cannot provide, since Write's error is
// otherwise unobservable once the response is already committed — actually
// emits the "write generated response" Debug record naming the failure. A
// second run over the SAME plane with a succeeding Write proves the record
// is specific to the failure, not emitted unconditionally: reverting the
// guard to a bare w.Write(asm.Body) turns the first subtest red without
// touching status, headers, or the no-panic guarantee.
func TestServeGenerated_WriteFailureIsLoggedNotPanicked(t *testing.T) {
	rt := fixtureRuntime(t, itemSchemaDoc, []router.Route{itemRoute()},
		map[int64][]gen.ResponseVariant{1: {itemVariant()}}, domain.DefaultSettings())
	m := mustMatch(t, rt, "GET", "/items")
	ws := respondTestWorkspace()

	t.Run("write fails: logs the failure", func(t *testing.T) {
		handler := &recordingHandler{}
		p := New(runtimeTestConfig(4<<20, 32), nil, nil, slog.New(handler))
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		w := &failingResponseWriter{ResponseRecorder: httptest.NewRecorder()}

		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Fatalf("serveGenerated panicked on a failing Write: %v", r)
				}
			}()
			p.serveGenerated(w, req, ws, rt, m, resources.ScopeKey(""))
		}()

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200 — already written before the failing Write call", w.Code)
		}
		if ct := w.Header().Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json — headers are set before the failing Write", ct)
		}

		rec := handler.debugRecord("write generated response")
		if rec == nil {
			t.Fatal(`no Debug "write generated response" record — the write failure went unlogged`)
		}
		attrs := map[string]string{}
		rec.Attrs(func(a slog.Attr) bool {
			attrs[a.Key] = a.Value.String()
			return true
		})
		if attrs["workspace"] != ws.Slug {
			t.Errorf(`log attr "workspace" = %q, want %q`, attrs["workspace"], ws.Slug)
		}
		if attrs["err"] == "" {
			t.Error(`log attr "err" is empty, want the Write failure's error text`)
		}
	})

	t.Run("write succeeds: logs nothing", func(t *testing.T) {
		handler := &recordingHandler{}
		p := New(runtimeTestConfig(4<<20, 32), nil, nil, slog.New(handler))
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		w := httptest.NewRecorder()

		p.serveGenerated(w, req, ws, rt, m, resources.ScopeKey(""))

		if w.Code != http.StatusOK {
			t.Errorf("status = %d, want 200", w.Code)
		}
		if rec := handler.debugRecord("write generated response"); rec != nil {
			t.Errorf(`unexpected Debug "write generated response" record on a successful write: %+v`, rec)
		}
	})
}
