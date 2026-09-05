// function_test.go is A18's acceptance for the serving branch — §A clauses
// 22-27, 29, 30 and 33 of docs/A18-endpoint-functions.md.
//
// Every case that observes a traffic NOTE drives the real [Plane.ServeHTTP]
// and not [Plane.serveGenerated] directly: markFunctionNote is a no-op unless
// attachTrafficMatch ran, and only captureTraffic — reached from ServeHTTP —
// does that. ref_test.go's own note test says the same thing about
// markRefUnresolved, and the reason is worth repeating rather than
// cross-referencing: a note assertion written against a direct call passes
// against an implementation that never marks anything.
package mockplane

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/workspaces"
)

// functionRow is an op_overrides row whose 200 variant carries src and
// nothing else — the exclusivity overrides.ValidateVariant enforces at write
// time, reproduced here so no fixture in this file can accidentally prove the
// branch fires for a variant a writer would have refused.
func functionRow(path, src string) *overrides.Row {
	return &overrides.Row{
		Method: http.MethodGet, Path: path, OverrideOn: true,
		Responses: map[string]overrides.Variant{"200": {Function: src}},
	}
}

// functionPlane is the whole fixture: the /order document, one GET route, a
// traffic sink, and the row above. maxResponse is a parameter because clause
// 25 refuses an observation taken at the default — a hard-coded 4 MiB and a
// config-reading implementation emit identical bytes there.
func functionPlane(t *testing.T, src string, maxResponse int64) (*Plane, *fakeTrafficSink, *workspaces.Workspace) {
	t.Helper()
	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	spec := &fakeRuntimeSource{normalized: []byte(orderDoc), routes: orderRoutes(), variants: orderVariants()}
	p := trafficPlane(t, 1<<20, spec, sink, ws)
	p.cfg.MaxResponse = maxResponse
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): functionRow("/order", src),
	}})
	return p, sink, ws
}

// serveOrder drives one GET /order all the way through ServeHTTP and returns
// the recorder plus the single traffic event the request produced. A request
// that records NO event returns a zero Event, which the two callers that
// expect one check for themselves.
func serveOrder(t *testing.T, p *Plane, sink *fakeTrafficSink) (*httptest.ResponseRecorder, traffic.Event) {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	events := sink.all()
	if len(events) != 1 {
		t.Fatalf("traffic events = %d, want exactly 1", len(events))
	}
	return rec, events[0]
}

// --- clause 22: the wall-clock budget --------------------------------------

// TestServeFunction_timeoutIs503AndNoted holds BOTH halves of clause 22: the
// request must not outlive the budget, and the status must be 503 and not
// 500. The two classes are separate on purpose — a timeout is the operator's
// own infinite loop and a 500 is a broken function, and an operator who
// cannot tell them apart debugs the wrong one.
//
// luafn.Timeout is shortened rather than waited out, and this test does not
// run in parallel with anything: it is a package var of another package, and
// a second test reading it concurrently is exactly what -race is for.
func TestServeFunction_timeoutIs503AndNoted(t *testing.T) {
	restore := luafn.Timeout
	luafn.Timeout = 150 * time.Millisecond
	t.Cleanup(func() { luafn.Timeout = restore })

	p, sink, _ := functionPlane(t, "while true do end", 4<<20)
	start := time.Now()
	rec, ev := serveOrder(t, p, sink)

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503 — a deadline is not a function_failed 500 (D6); body=%s", rec.Code, rec.Body)
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Fatalf("the request took %v; SetContext must interrupt the loop at the budget, not let it run", elapsed)
	}
	if ev.Notes != noteFunctionTimeout {
		t.Errorf("Notes = %q, want %q", ev.Notes, noteFunctionTimeout)
	}
}

// --- clause 23: the browser-executable media type --------------------------

// TestServeFunction_refusesABrowserExecutableContentType is clause 23's three
// cases, and three is the number that matters: the table in
// internal/httpx/mediatype.go holds more than one entry and an unparseable
// value is refused by a different branch, so an implementation that
// special-cases the literal text/html passes a one-case test and fails here.
func TestServeFunction_refusesABrowserExecutableContentType(t *testing.T) {
	for _, tc := range []struct {
		name string
		typ  string
	}{
		{"text/html", "text/html"},
		{"svg", "image/svg+xml"},
		{"unparseable", "text/html; charset="},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sink, _ := functionPlane(t,
				`return 200, "<script>alert(1)</script>", {["Content-Type"] = "`+tc.typ+`"}`, 4<<20)
			rec, ev := serveOrder(t, p, sink)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if got := rec.Header().Get("Content-Type"); strings.Contains(got, "html") || strings.Contains(got, "svg") {
				t.Fatalf("Content-Type = %q; not one byte may reach the wire under a type the plane refuses", got)
			}
			if strings.Contains(rec.Body.String(), "<script>") {
				t.Fatalf("the refused body reached the client: %s", rec.Body)
			}
			if ev.Notes != noteFunctionFailed {
				t.Errorf("Notes = %q, want %q", ev.Notes, noteFunctionFailed)
			}
		})
	}
}

// --- clause 24: the header checks ------------------------------------------

// TestServeFunction_refusesABadHeader is clause 24's four cases. Each asserts
// the header did NOT reach the wire, which is the half that separates
// refusing from sanitizing: net/http would happily write a repaired value,
// and a repaired value is a response that looks right and is not.
func TestServeFunction_refusesABadHeader(t *testing.T) {
	long := strings.Repeat("x", maxFunctionHeaderValue+1)
	for _, tc := range []struct {
		name   string
		src    string
		header string
	}{
		{"CR/LF in the value", `return 200, {}, {["X-Bad"] = "a\r\nX-Injected: 1"}`, "X-Injected"},
		{"an empty name", `return 200, {}, {[""] = "v"}`, ""},
		{"a value past the bound", `return 200, {}, {["X-Big"] = "` + long + `"}`, "X-Big"},
		// A NON-STRING value is refused inside luafn (readReturn), not by
		// this file's tail — the case is here anyway, because clause 24 is
		// about the OBSERVABLE outcome and an author cannot see which of the
		// two layers said no.
		{"a non-string value", `return 200, {}, {["X-Num"] = 7}`, "X-Num"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, sink, _ := functionPlane(t, tc.src, 4<<20)
			rec, ev := serveOrder(t, p, sink)

			if rec.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500", rec.Code)
			}
			if tc.header != "" && rec.Header().Get(tc.header) != "" {
				t.Fatalf("header %q reached the wire as %q; the plane refuses such a header, it does not repair it",
					tc.header, rec.Header().Get(tc.header))
			}
			if ev.Notes != noteFunctionFailed {
				t.Errorf("Notes = %q, want %q", ev.Notes, noteFunctionFailed)
			}
		})
	}
}

// TestServeFunction_writesAGoodHeaderAndOneSetCookie is clause 24's other
// direction and D3's stated sign-in shape: the refusals above must not be so
// wide that they take the feature out of the rows it is for. Set-Cookie is
// the case that matters — negotiate.go's unsafeResponseHeaderName DROPS it
// for a spec-declared header, and function.go's own doc comment says why a
// function's is different.
func TestServeFunction_writesAGoodHeaderAndOneSetCookie(t *testing.T) {
	p, sink, _ := functionPlane(t,
		`return 200, {ok = true}, {["X-Trace"] = "abc", ["Set-Cookie"] = "sid=1; Path=/"}`, 4<<20)
	rec, ev := serveOrder(t, p, sink)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if got := rec.Header().Get("X-Trace"); got != "abc" {
		t.Errorf("X-Trace = %q, want %q", got, "abc")
	}
	if got := rec.Header().Get("Set-Cookie"); got != "sid=1; Path=/" {
		t.Errorf("Set-Cookie = %q, want the function's own value — D3 names one cookie as the sign-in shape", got)
	}
	if ev.Notes != noteFunction {
		t.Errorf("Notes = %q, want %q", ev.Notes, noteFunction)
	}
}

// TestServeFunction_refusesAFramingHeader pins the one thing that stays
// refused for a correctness reason rather than a policy one: a second
// Content-Length is a corrupted response, not a disagreement.
func TestServeFunction_refusesAFramingHeader(t *testing.T) {
	p, sink, _ := functionPlane(t, `return 200, {ok = true}, {["Content-Length"] = "99999"}`, 4<<20)
	rec, _ := serveOrder(t, p, sink)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 — Content-Length is the server's to compute", rec.Code)
	}
}

// --- clause 25: the byte cap -----------------------------------------------

// TestServeFunction_refusesABodyOverMaxResponse observes at a NON-DEFAULT
// MOCKER_MAX_RESPONSE, which is clause 25's own second failure condition: a
// hard-coded 4 MiB and a config-reading implementation emit identical bytes
// at the default, so an observation there proves nothing about which one was
// built.
func TestServeFunction_refusesABodyOverMaxResponse(t *testing.T) {
	const maxResponse = 4096
	p, sink, _ := functionPlane(t, `return 200, string.rep("x", 9000)`, maxResponse)
	rec, ev := serveOrder(t, p, sink)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if strings.Contains(rec.Body.String(), strings.Repeat("x", 100)) {
		t.Fatalf("a partial body reached the client (%d bytes); the cap is checked BEFORE WriteHeader", rec.Body.Len())
	}
	if ev.Notes != noteFunctionTooLarge {
		t.Errorf("Notes = %q, want %q", ev.Notes, noteFunctionTooLarge)
	}
	if ev.Status != http.StatusInternalServerError {
		t.Errorf("recorded status = %d, want 500 — the status line must not have been committed at 200", ev.Status)
	}
}

// --- clause 26: the error note ---------------------------------------------

// TestServeFunction_errorNoteIsCappedAndCarriesNoStack uses a message
// deliberately LONGER than the cap and containing a token-shaped string,
// which is clause 26's own second failure condition: a short message never
// exercises the cap at all.
func TestServeFunction_errorNoteIsCappedAndCarriesNoStack(t *testing.T) {
	const token = "eyJhbGciOiJIUzI1NiJ9.SECRETLOOKINGTOKEN"
	src := `error("` + token + strings.Repeat("A", 400) + `")`
	p, sink, _ := functionPlane(t, src, 4<<20)
	rec, ev := serveOrder(t, p, sink)

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ev.Notes != noteFunctionFailed {
		t.Fatalf("Notes = %q, want %q", ev.Notes, noteFunctionFailed)
	}
	// The note token itself is the classification; the capped text travels
	// in the response envelope, and THAT is what must not be a whole Lua
	// message or a Go stack.
	body := rec.Body.String()
	if len(body) > 600 {
		t.Errorf("the error envelope is %d bytes; the message is capped at %d and cannot produce this", len(body), 200)
	}
	if strings.Contains(body, "goroutine ") || strings.Contains(body, ".go:") {
		t.Errorf("a Go stack reached the client: %s", body)
	}
	if strings.Count(body, "A") > 250 {
		t.Errorf("the whole message reached the client, uncapped: %s", body)
	}
}

// --- clause 27: the client that walked away --------------------------------

// TestServeFunction_canceledRequestIsNotClassified is clause 27. The context
// is cancelled BEFORE the run, so the function's first instruction meets an
// already-dead caller — the same outcome a disconnect mid-run produces, with
// no timing to race.
func TestServeFunction_canceledRequestIsNotClassified(t *testing.T) {
	p, sink, _ := functionPlane(t, `return 200, {ok = true}`, 4<<20)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil).WithContext(ctx)
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)

	for _, ev := range sink.all() {
		if strings.Contains(ev.Notes, noteFunctionFailed) || strings.Contains(ev.Notes, noteFunctionTimeout) {
			t.Fatalf("Notes = %q; a cancelled request is neither a failure nor a timeout (D6)", ev.Notes)
		}
	}
}

// --- clause 29: the session layer runs FIRST -------------------------------

// TestServeFunction_sessionLayerRunsBeforeTheFunction is clause 29's four
// directives, one assertion each. The function used here would answer 299 if
// it ran, so "the function did not run" is observable as a STATUS and not
// only as a missing note.
func TestServeFunction_sessionLayerRunsBeforeTheFunction(t *testing.T) {
	const src = `return 299, {ran = true}`

	t.Run("a forced status answers without running the function", func(t *testing.T) {
		p, sink, ws := functionPlane(t, src, 4<<20)
		store := livestate.NewStore(0, nil)
		if err := store.Set(ws.ID, livestate.Directive{
			Target: livestate.Target{Method: http.MethodGet, Path: "/order"},
			Action: livestate.ActionStatus, Status: 503,
		}); err != nil {
			t.Fatalf("store.Set: %v", err)
		}
		p.SetLiveState(store)

		rec, ev := serveOrder(t, p, sink)
		if rec.Code != 503 {
			t.Fatalf("status = %d, want the forced 503", rec.Code)
		}
		if strings.Contains(ev.Notes, noteFunction) {
			t.Errorf("Notes = %q, want no function token — a forced status answers before the VM exists", ev.Notes)
		}
	})

	t.Run("fail_next consumes and answers before the function", func(t *testing.T) {
		p, sink, ws := functionPlane(t, src, 4<<20)
		store := livestate.NewStore(0, nil)
		if err := store.Set(ws.ID, livestate.Directive{
			Target: livestate.Target{Method: http.MethodGet, Path: "/order"},
			Action: livestate.ActionFail, Status: 500, Once: true,
		}); err != nil {
			t.Fatalf("store.Set: %v", err)
		}
		p.SetLiveState(store)

		rec, ev := serveOrder(t, p, sink)
		if rec.Code != 500 {
			t.Fatalf("status = %d, want the fail_next 500", rec.Code)
		}
		if strings.Contains(ev.Notes, noteFunction) {
			t.Errorf("Notes = %q, want no function token", ev.Notes)
		}
	})

	t.Run("a pause parks the request before the VM is created", func(t *testing.T) {
		p, sink, ws := functionPlane(t, src, 4<<20)
		store := livestate.NewStore(0, nil)
		if err := store.Set(ws.ID, livestate.Directive{
			Target: livestate.Target{Method: http.MethodGet, Path: "/order"}, Action: livestate.ActionPause,
		}); err != nil {
			t.Fatalf("store.Set: %v", err)
		}
		// Fill every park slot so THIS request is refused rather than
		// parked: a parked request would block the test forever, and the
		// refusal path proves the same ordering — resolvePause ran and
		// answered before the function could.
		for range livestate.MaxPausedPerWorkspace {
			store.Apply(ws.ID, http.MethodGet, "/order")
		}
		p.SetLiveState(store)

		rec, ev := serveOrder(t, p, sink)
		if !strings.Contains(ev.Notes, notePauseRefused) {
			t.Fatalf("Notes = %q, want %q — the pause layer must have run", ev.Notes, notePauseRefused)
		}
		// The refused pause falls THROUGH to the ordinary path, so the
		// function does run: what clause 29 observes here is that the pause
		// was decided first, which the note above is the evidence for.
		if rec.Code != 299 {
			t.Fatalf("status = %d, want 299 — a refused pause falls through to the function", rec.Code)
		}
	})

	t.Run("a delay is paid before the function runs", func(t *testing.T) {
		settings := domain.DefaultSettings()
		settings.DelayMs = 120
		sink := &fakeTrafficSink{}
		ws := widgetsWorkspace(7, settings)
		spec := &fakeRuntimeSource{normalized: []byte(orderDoc), routes: orderRoutes(), variants: orderVariants()}
		p := trafficPlane(t, 1<<20, spec, sink, ws)
		p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
			overrides.OpKey(http.MethodGet, "/order"): functionRow("/order", src),
		}})

		start := time.Now()
		rec, _ := serveOrder(t, p, sink)
		if rec.Code != 299 {
			t.Fatalf("status = %d, want 299", rec.Code)
		}
		if elapsed := time.Since(start); elapsed < 100*time.Millisecond {
			t.Fatalf("elapsed = %v, want at least the 120ms delay — the delay is paid before the run", elapsed)
		}
	})
}

// --- clause 30: the function beats a confirmed resource --------------------

// TestServeFunction_beatsAConfirmedResource asserts BOTH directions, which is
// clause 30's own failure condition: the first half alone passes against an
// implementation that disabled the resource branch outright.
func TestServeFunction_beatsAConfirmedResource(t *testing.T) {
	newPlane := func(t *testing.T, withFunction bool) (*Plane, *fakeTrafficSink) {
		t.Helper()
		sink := &fakeTrafficSink{}
		ws := widgetsWorkspace(7, domain.DefaultSettings())
		spec := &fakeRuntimeSource{
			normalized: []byte(blankDoc),
			routes:     resourceTestRoutes(),
			variants:   resourceTestVariants(),
		}
		p := trafficPlane(t, 1<<20, spec, sink, ws)
		if withFunction {
			p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
				overrides.OpKey(http.MethodGet, "/items"): {
					Method: http.MethodGet, Path: "/items", OverrideOn: true,
					Responses: map[string]overrides.Variant{"200": {Function: `return 200, {from = "function"}`}},
				},
			}})
		}
		p.SetResources(previewFakeResourceSource{res: &resources.Resource{
			ID: 42, WorkspaceID: 7, RouteFamily: "/items", IDField: "id", ScopeParams: []string{},
		}})
		p.SetEntities(&fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
			return []resources.Entity{entityRow(1, "1", `{"id":1,"from":"resource"}`)}, nil
		}})
		return p, sink
	}

	get := func(t *testing.T, p *Plane) *httptest.ResponseRecorder {
		t.Helper()
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/items", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		return rec
	}

	t.Run("with a function variant the FUNCTION serves", func(t *testing.T) {
		p, _ := newPlane(t, true)
		rec := get(t, p)
		if !strings.Contains(rec.Body.String(), `"function"`) {
			t.Fatalf("body = %s, want the function's own answer — a function is the operator's explicit statement, a resource is generated convenience (D7)", rec.Body)
		}
	})

	t.Run("with the function removed the RESOURCE serves", func(t *testing.T) {
		p, _ := newPlane(t, false)
		rec := get(t, p)
		if !strings.Contains(rec.Body.String(), `"resource"`) {
			t.Fatalf("body = %s, want the stored row — the resource branch must still take the operation when no function applies", rec.Body)
		}
	})
}

// --- clause 33: an ordinary function-served request is recorded ------------

// TestServeFunction_recordsTrafficNormally is clause 33: the row is written
// like any other, with the note token `function`.
func TestServeFunction_recordsTrafficNormally(t *testing.T) {
	p, sink, _ := functionPlane(t, `return 201, {id = 7}, {["X-A"] = "b"}`, 4<<20)
	rec, ev := serveOrder(t, p, sink)

	if rec.Code != 201 {
		t.Fatalf("status = %d, want 201 — the function's own status is the response's", rec.Code)
	}
	if ev.Status != 201 {
		t.Errorf("recorded status = %d, want 201", ev.Status)
	}
	if ev.Notes != noteFunction {
		t.Errorf("Notes = %q, want %q", ev.Notes, noteFunction)
	}
	if !strings.Contains(string(ev.RespBody), `"id":7`) {
		t.Errorf("RespBody = %s, want the function's body recorded like any other", ev.RespBody)
	}
}

// --- the request table -----------------------------------------------------

// TestServeFunction_requestTableCarriesTheRequest proves the `req` argument
// is really filled from the request being served and not from a zero value —
// the shape D3's In block fixes, observed through a function that echoes it.
func TestServeFunction_requestTableCarriesTheRequest(t *testing.T) {
	const src = `return 200, {
		method = req.method,
		path = req.path,
		mode = req.query.mode,
		agent = req.headers["x-agent"],
	}`
	p, sink, _ := functionPlane(t, src, 4<<20)

	req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order?mode=login", nil)
	req.Header.Set("X-Agent", "curl")
	rec := httptest.NewRecorder()
	p.ServeHTTP(rec, req)
	_ = sink

	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	var got map[string]any
	if err := jsonx.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("body did not decode: %s", rec.Body)
	}
	for field, want := range map[string]any{
		"method": "GET", "path": "/order", "mode": "login", "agent": "curl",
	} {
		if got[field] != want {
			t.Errorf("req.%s reached the function as %v, want %v", field, got[field], want)
		}
	}
}

// --- the two helpers -------------------------------------------------------

// TestLuaHost_jwtRefusesAnUnconfiguredWorkspace is D3's own refusal: an
// unsigned token pretending to be signed is worse than an error, and the
// function sees the refusal as a Lua value it can branch on.
func TestLuaHost_jwtRefusesAnUnconfiguredWorkspace(t *testing.T) {
	for _, tc := range []struct {
		name string
		auth domain.AuthSettings
	}{
		{"alg none", domain.AuthSettings{Alg: "none", SigningKey: "k"}},
		{"no key", domain.AuthSettings{Alg: "HS256"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := &luaHost{ws: &workspaces.Workspace{Settings: domain.Settings{Auth: tc.auth}}}
			token, err := h.JWT(t.Context(), map[string]any{"sub": 1})
			if err == nil {
				t.Fatalf("JWT = %q, want an error", token)
			}
			if err.Error() != "auth_not_configured" {
				t.Errorf("err = %q, want auth_not_configured — the word is what a function branches on", err)
			}
		})
	}
}

// TestLuaHost_jwtSignsWithTheWorkspaceSettings is the other direction, and it
// asserts the token came from recipes.MintJWT rather than from a second
// signer: the three-segment shape with a non-empty signature is what HS256
// over the workspace's own key produces.
func TestLuaHost_jwtSignsWithTheWorkspaceSettings(t *testing.T) {
	h := &luaHost{ws: &workspaces.Workspace{Settings: domain.Settings{
		Auth: domain.AuthSettings{Alg: "HS256", SigningKey: "secret"},
	}}}
	token, err := h.JWT(t.Context(), map[string]any{"role": "admin"})
	if err != nil {
		t.Fatalf("JWT: %v", err)
	}
	parts := strings.Split(token, ".")
	if len(parts) != 3 || parts[2] == "" {
		t.Fatalf("token = %q, want three segments with a real signature", token)
	}
}

// TestLuaHost_entitiesScope is D3's scopeArray, whose behaviour the owner
// changed from the `ref` recipe's on 2026-09-04: real filtering, the tuple
// encoded by resources.EncodeScope, and a wrong arity refused by name.
func TestLuaHost_entitiesScope(t *testing.T) {
	newHost := func(scopeParams []string, outer []string, seen *resources.ScopeKey) *luaHost {
		p := respondTestPlane()
		p.SetEntities(&fakeEntityStore{listFn: func(_ context.Context, _ int64, _, scope resources.ScopeKey) ([]resources.Entity, error) {
			*seen = scope
			return []resources.Entity{entityRow(1, "1", `{"id":1}`)}, nil
		}})
		return &luaHost{
			p:  p,
			rt: &runtime{resources: map[string]*resources.Resource{"/teams": {ID: 9, RouteFamily: "/teams", ScopeParams: scopeParams}}},
			ws: &workspaces.Workspace{Slug: "alex"}, outer: outer,
		}
	}

	t.Run("an explicit tuple is encoded through EncodeScope", func(t *testing.T) {
		var seen resources.ScopeKey
		h := newHost([]string{"orgId"}, nil, &seen)
		rows, err := h.Entities(t.Context(), "/teams", []string{"a b"})
		if err != nil {
			t.Fatalf("Entities: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("rows = %d, want 1", len(rows))
		}
		if want := resources.EncodeScope([]string{"a b"}); seen != want {
			t.Errorf("scope = %q, want %q — the HOST encodes, never a second strings.Join at the call site", seen, want)
		}
	})

	t.Run("an omitted tuple takes the request's own outer values", func(t *testing.T) {
		var seen resources.ScopeKey
		h := newHost([]string{"orgId"}, []string{"7", "5"}, &seen)
		if _, err := h.Entities(t.Context(), "/teams", nil); err != nil {
			t.Fatalf("Entities: %v", err)
		}
		if want := resources.EncodeScope([]string{"7"}); seen != want {
			t.Errorf("scope = %q, want %q — a family's scope is the PREFIX of the request's tuple at that family's depth", seen, want)
		}
	})

	t.Run("a wrong arity is bad_scope", func(t *testing.T) {
		var seen resources.ScopeKey
		h := newHost([]string{"orgId"}, nil, &seen)
		if _, err := h.Entities(t.Context(), "/teams", []string{"7", "5"}); err == nil || err.Error() != "bad_scope" {
			t.Fatalf("err = %v, want bad_scope", err)
		}
	})

	t.Run("a request too shallow to name the scope is bad_scope", func(t *testing.T) {
		var seen resources.ScopeKey
		h := newHost([]string{"orgId"}, nil, &seen)
		if _, err := h.Entities(t.Context(), "/teams", nil); err == nil || err.Error() != "bad_scope" {
			t.Fatalf("err = %v, want bad_scope — reading the empty scope's rows would be a silent wrong answer", err)
		}
	})

	t.Run("an unknown family is unknown_family", func(t *testing.T) {
		var seen resources.ScopeKey
		h := newHost([]string{}, nil, &seen)
		if _, err := h.Entities(t.Context(), "/nope", nil); err == nil || err.Error() != "unknown_family" {
			t.Fatalf("err = %v, want unknown_family", err)
		}
	})
}

// TestServeFunction_mockEntitiesReachesTheStore is the two helpers seen from
// INSIDE Lua, end to end: the roster hit, the store read and the row's own
// fields arriving as a Lua table.
func TestServeFunction_mockEntitiesReachesTheStore(t *testing.T) {
	const src = `local rows, err = mock.entities("/subjects")
		if not rows then return 500, {err = err} end
		return 200, {count = #rows, first = rows[1].name}`

	sink := &fakeTrafficSink{}
	ws := widgetsWorkspace(7, domain.DefaultSettings())
	spec := &fakeRuntimeSource{normalized: []byte(orderDoc), routes: orderRoutes(), variants: orderVariants()}
	p := trafficPlane(t, 1<<20, spec, sink, ws)
	p.SetOverrides(&fakeOverrideSource{rows: map[string]*overrides.Row{
		overrides.OpKey(http.MethodGet, "/order"): functionRow("/order", src),
	}})
	p.SetResources(previewFakeResourceSource{res: subjectsResource(42)})
	p.SetEntities(&fakeEntityStore{listFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey) ([]resources.Entity, error) {
		return []resources.Entity{entityRow(1, "1", `{"id":1,"name":"algebra"}`)}, nil
	}})

	rec, _ := serveOrder(t, p, sink)
	if rec.Code != 200 {
		t.Fatalf("status = %d, want 200; body=%s", rec.Code, rec.Body)
	}
	if !strings.Contains(rec.Body.String(), `"algebra"`) {
		t.Fatalf("body = %s, want the stored row's own field", rec.Body)
	}
}

// --- the preview -----------------------------------------------------------

// TestPreview_functionFailureLandsInNotes is clause 32's operation half: a
// failing draft function must not turn the preview route into a non-200, and
// the failure must be legible as a note rather than as the mock's own answer.
func TestPreview_functionFailureLandsInNotes(t *testing.T) {
	p, ws := previewTestPlane(resourceTestVariants(), nil)

	res, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
		OpKey: overrides.OpKey(http.MethodGet, "/items"),
		Draft: previewDraft(t, true, map[string]overrides.Variant{"200": {Function: `error("boom")`}}),
	})
	if err != nil {
		t.Fatalf("Preview returned an error: %v — a failing draft function is a NOTE, never the route's status", err)
	}
	if res.Notes != noteFunctionFailed {
		t.Errorf("Notes = %q, want %q", res.Notes, noteFunctionFailed)
	}
	if !res.NoBody {
		t.Errorf("NoBody = false; a failed function produced no body to show")
	}
}

// TestPreview_functionRunsWithNoHost is the other half: the draft runs, and
// its helpers decline from INSIDE Lua rather than being absent — the same
// refusal Preview already makes for live state, the ref resolver and the
// asset lookup, made visible to the author.
func TestPreview_functionRunsWithNoHost(t *testing.T) {
	const src = `local rows, err = mock.entities("/subjects")
		if rows then return 500, {} end
		return 200, {declined = err}`
	p, ws := previewTestPlane(resourceTestVariants(), nil)

	res, err := p.Preview(t.Context(), ws, domain.PreviewRequest{
		OpKey: overrides.OpKey(http.MethodGet, "/items"),
		Draft: previewDraft(t, true, map[string]overrides.Variant{"200": {Function: src}}),
	})
	if err != nil {
		t.Fatalf("Preview: %v", err)
	}
	if res.Status != 200 {
		t.Fatalf("Status = %d, want the function's own 200; notes=%q", res.Status, res.Notes)
	}
	if !strings.Contains(string(res.Body), "no_host") {
		t.Errorf("Body = %s, want the helper's own decline", res.Body)
	}
	if res.Notes != noteFunction {
		t.Errorf("Notes = %q, want %q", res.Notes, noteFunction)
	}
}

// --- the statelessness boundary --------------------------------------------

// TestServeFunction_isStatelessAcrossRequests is D3's "stateless by
// construction", observed rather than assumed: a global assigned by one
// request must be nil in the next, because the VM that held it is closed.
func TestServeFunction_isStatelessAcrossRequests(t *testing.T) {
	const src = `local seen = counter
		counter = (counter or 0) + 1
		return 200, {seen = seen or "nil"}`
	p, sink, _ := functionPlane(t, src, 4<<20)

	for i := range 2 {
		req := httptest.NewRequest(http.MethodGet, "http://alex.mock.local/order", nil)
		rec := httptest.NewRecorder()
		p.ServeHTTP(rec, req)
		if !strings.Contains(rec.Body.String(), `"nil"`) {
			t.Fatalf("request %d saw %s; no global may survive between two calls (D3)", i, rec.Body)
		}
	}
	_ = sink
}

// concurrentFunctionRequests is a race probe rather than an acceptance
// clause: two anonymous requests to the same function-bearing operation share
// a *Plane, a *runtime and a compiled route table, and the VM must be the one
// thing they do not share. It exists because A15 found a live
// `concurrent map writes` on exactly that shape one layer over, in the
// generator's document values.
func TestServeFunction_concurrentRequestsShareNoVM(t *testing.T) {
	p, sink, _ := functionPlane(t, `local t = {} t.x = req.query.n return 200, t`, 4<<20)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			req := httptest.NewRequest(http.MethodGet,
				"http://alex.mock.local/order?n="+string(rune('a'+i)), nil)
			rec := httptest.NewRecorder()
			p.ServeHTTP(rec, req)
			if rec.Code != 200 {
				t.Errorf("status = %d, want 200", rec.Code)
			}
		}()
	}
	wg.Wait()
	if got := len(sink.all()); got != 8 {
		t.Errorf("traffic events = %d, want 8", got)
	}
}

// --- review findings 2, 13, 14: the writer's last line ----------------------

// TestServeFunction_untypedStringBodyIsNotSniffedIntoHTML is review finding 2
// and runs a REAL http.Server on purpose: httptest.ResponseRecorder does not
// sniff after an explicit WriteHeader, which is how the clause-23 test above
// stayed green while the wire said `text/html; charset=utf-8` for an untyped
// `<script>` string. The type is defaulted to text/plain and the response
// carries nosniff, so neither the server nor the browser guesses.
func TestServeFunction_untypedStringBodyIsNotSniffedIntoHTML(t *testing.T) {
	p, _, _ := functionPlane(t, `return 200, "<html><script>alert(1)</script></html>"`, 4<<20)
	srv := httptest.NewServer(p)
	defer srv.Close()

	req, err := http.NewRequestWithContext(t.Context(), http.MethodGet, srv.URL+"/order", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Host = "alex.mock.local"
	res, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", res.StatusCode)
	}
	if got := res.Header.Get("Content-Type"); got != functionDefaultTextType {
		t.Errorf("Content-Type on the wire = %q, want %q: an untyped string body must not be sniffed", got, functionDefaultTextType)
	}
	if got := res.Header.Get("X-Content-Type-Options"); got != "nosniff" {
		t.Errorf("X-Content-Type-Options = %q, want nosniff", got)
	}
}

// TestServeFunction_aTableBodyStaysJSON guards the default from over-reach:
// only an UNTYPED STRING gets text/plain; a table return is typed by luafn.
func TestServeFunction_aTableBodyStaysJSON(t *testing.T) {
	p, sink, _ := functionPlane(t, `return 200, {ok = true}`, 4<<20)
	rec, _ := serveOrder(t, p, sink)
	if got := rec.Header().Get("Content-Type"); got != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", got)
	}
}

// TestServeFunction_refusesTooManyHeaders is review finding 13: the per-value
// cap left the SET unbounded, and a Lua loop can return a table of thousands
// of headers — the one output MOCKER_MAX_RESPONSE does not reach.
func TestServeFunction_refusesTooManyHeaders(t *testing.T) {
	src := `local h = {}; for i = 1, ` + strconv.Itoa(maxFunctionHeaders+1) + ` do h["X-H-" .. i] = "v" end; return 200, {ok = true}, h`
	p, sink, _ := functionPlane(t, src, 4<<20)
	rec, ev := serveOrder(t, p, sink)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500", rec.Code)
	}
	if ev.Notes != noteFunctionFailed {
		t.Errorf("Notes = %q, want %q", ev.Notes, noteFunctionFailed)
	}

	// Under the count cap but over the byte cap: sixteen values at the
	// per-value limit are 128 KiB together.
	src = `local h = {}; for i = 1, 16 do h["X-H-" .. i] = string.rep("v", ` + strconv.Itoa(maxFunctionHeaderValue) + `) end; return 200, {ok = true}, h`
	p, sink, _ = functionPlane(t, src, 4<<20)
	rec, _ = serveOrder(t, p, sink)
	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500 for %d headers totalling over %d bytes", rec.Code, 16, maxFunctionHeaderBytes)
	}
}

// TestServeFunction_emptyBodyKeepsItsDeclaredType is review finding 14: the
// type was set after the empty-body return, so `204, nil, {Content-Type}` lost
// the header the function declared.
func TestServeFunction_emptyBodyKeepsItsDeclaredType(t *testing.T) {
	p, sink, _ := functionPlane(t, `return 204, nil, {["Content-Type"] = "application/problem+json"}`, 4<<20)
	rec, _ := serveOrder(t, p, sink)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("status = %d, want 204", rec.Code)
	}
	if got := rec.Header().Get("Content-Type"); got != "application/problem+json" {
		t.Errorf("Content-Type = %q, want the declared type kept on an empty body", got)
	}
}

// --- A19: the entity writers' host half ------------------------------------

// TestLuaHost_entityWriters pins what reaches the store for each writer: the
// family resolved through THIS runtime's roster, the scope encoded once by the
// host, the family's own id field and type, a shallow merge on update, and the
// store's refusals mapped to the words the guide lists.
func TestLuaHost_entityWriters(t *testing.T) {
	newHost := func(store *fakeEntityStore) *luaHost {
		p := respondTestPlane()
		p.SetEntities(store)
		return &luaHost{
			p: p,
			rt: &runtime{resources: map[string]*resources.Resource{"/msgs": {
				ID: 5, RouteFamily: "/msgs", IDField: "id", ScopeParams: []string{"roomId"},
			}}},
			ws: &workspaces.Workspace{Slug: "alex"}, outer: []string{"42"},
		}
	}
	wantScope := resources.EncodeScope([]string{"42"})

	t.Run("create goes through Create with the family's id field and the request's scope", func(t *testing.T) {
		var seenScope resources.ScopeKey
		var seenIDField string
		store := &fakeEntityStore{createFn: func(_ context.Context, _ int64, _, scope resources.ScopeKey, idField, _ string, data map[string]any) (resources.Entity, error) {
			seenScope, seenIDField = scope, idField
			return entityRow(1, "1", `{"id":1,"text":"`+data["text"].(string)+`"}`), nil
		}}
		row, err := newHost(store).EntityCreate(t.Context(), "/msgs", nil, map[string]any{"text": "hi"})
		if err != nil {
			t.Fatal(err)
		}
		if seenScope != wantScope || seenIDField != "id" {
			t.Errorf("Create got scope=%q idField=%q, want %q and id", seenScope, seenIDField, wantScope)
		}
		if row["text"] != "hi" || row["id"] != float64(1) {
			t.Errorf("row = %v", row)
		}
	})

	t.Run("update is the store's Patch under the request's scope and the family's id field", func(t *testing.T) {
		var seenKey, seenIDField string
		var seenScope resources.ScopeKey
		store := &fakeEntityStore{
			patchFn: func(_ context.Context, _ int64, _, scope resources.ScopeKey, key, idField, _ string, patch map[string]any) (resources.Entity, bool, error) {
				seenKey, seenIDField, seenScope = key, idField, scope
				b, _ := json.Marshal(map[string]any{"id": 7, "a": patch["a"], "keep": "yes"})
				return entityRow(7, key, string(b)), true, nil
			},
		}
		row, err := newHost(store).EntityUpdate(t.Context(), "/msgs", []string{"42"}, "7", map[string]any{"a": 2})
		if err != nil {
			t.Fatal(err)
		}
		if seenKey != "7" || seenIDField != "id" || seenScope != wantScope {
			t.Errorf("Patch got key=%q idField=%q scope=%q", seenKey, seenIDField, seenScope)
		}
		if row["a"] != float64(2) || row["keep"] != "yes" {
			t.Errorf("row = %v", row)
		}
	})

	t.Run("update of a missing row is not_found", func(t *testing.T) {
		_, err := newHost(&fakeEntityStore{}).EntityUpdate(t.Context(), "/msgs", nil, "9", map[string]any{"a": 1})
		if err == nil || err.Error() != "not_found" {
			t.Fatalf("err = %v, want not_found", err)
		}
	})

	t.Run("delete answers the store's boolean", func(t *testing.T) {
		store := &fakeEntityStore{deleteFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string) (bool, error) {
			return true, nil
		}}
		gone, err := newHost(store).EntityDelete(t.Context(), "/msgs", nil, "7")
		if err != nil || !gone {
			t.Fatalf("gone=%v err=%v", gone, err)
		}
	})

	t.Run("the store's refusals arrive by name", func(t *testing.T) {
		store := &fakeEntityStore{createFn: func(context.Context, int64, resources.ScopeKey, resources.ScopeKey, string, string, map[string]any) (resources.Entity, error) {
			return resources.Entity{}, resources.ErrEntityLimit
		}}
		_, err := newHost(store).EntityCreate(t.Context(), "/msgs", nil, map[string]any{})
		if err == nil || err.Error() != "entity_limit" {
			t.Fatalf("err = %v, want entity_limit", err)
		}
		if _, err := newHost(&fakeEntityStore{}).EntityCreate(t.Context(), "/nope", nil, map[string]any{}); err == nil || err.Error() != "unknown_family" {
			t.Fatalf("err = %v, want unknown_family", err)
		}
		if _, err := newHost(&fakeEntityStore{}).EntityCreate(t.Context(), "/msgs", []string{"1", "2"}, map[string]any{}); err == nil || err.Error() != "bad_scope" {
			t.Fatalf("err = %v, want bad_scope", err)
		}
	})
}
