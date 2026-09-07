// Autocheckpoint_test.go is P2d's own test file (§4 of the P2d slice's
// context document): the debounce ("auto") checkpoint trigger [Server]
// installs at [Server.routeMux]'s mux.HandleFunc line. It is package admin
// (white-box), like openapi_contract_test.go and loopback_test.go, and for
// the same two reasons those files are: it needs [Server.routes] and the
// [checkpointPolicy] column on its rows directly (both unexported), and it reuses
// loopback_test.go's own loopbackTestServer/loopbackTestSrc helpers — the
// admin_test.go equivalents live in package admin_test, a different Go
// package this file cannot reach into.
//
// Every test here dispatches through [Server.CallAsMCP] rather than
// building cookies and a CSRF token by hand: CallAsMCP shares the SAME mux
// [Server.Handler] does (routeMux's own doc comment), so it exercises the
// installed wrapper exactly as a browser-origin request would, and it
// resolves a real, cached [auth.User] (loopback.go's mcpIdentity) that
// [Server.autoCheckpoint] can attribute a row to — a nil-session request
// with a real user attached, deliberately: the one shape
// [Server.autoCheckpoint]'s own doc comment says createdBy must come from.
package admin

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/checkpoints"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/testspec"
)

// filterCheckpointKind returns the subset of rows whose Kind matches.
func filterCheckpointKind(rows []checkpoints.Summary, kind string) []checkpoints.Summary {
	var out []checkpoints.Summary
	for _, r := range rows {
		if r.Kind == kind {
			out = append(out, r)
		}
	}
	return out
}

// TestAutoCheckpointWrapper_disabledWhenWindowIsZero is §4's own
// requirement: "the wrapper is not installed at all when the window is 0."
// loopbackTestConfig leaves CheckpointDebounce at its Go zero value, so this
// server is built exactly like every OTHER admin package test's server is
// (admin_test.go's testConfig makes the same choice, deliberately, for the
// same reason — see its own contract comment). Calling the SAME labelled
// route twice is the point: if the wrapper were merely a no-op at call time
// rather than absent from the mux entirely, this test would still pass —
// what it actually proves is that neither call, at any window state,
// leaves a row behind.
func TestAutoCheckpointWrapper_disabledWhenWindowIsZero(t *testing.T) {
	t.Parallel()
	srv := loopbackTestServer(t, nil)
	src := loopbackTestSrc()

	status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodPost, "/api/workspaces", []byte(`{"name":"debounce-off"}`))
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create workspace: status=%d err=%v body=%s", status, err, body)
	}
	var ws struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("decode create-workspace response: %v", err)
	}

	opKey := overrides.OpKey(http.MethodGet, "/widgets/sub")
	opPath := fmt.Sprintf("/api/workspaces/%d/operations/%s", ws.ID, opKey)
	// A3: each call needs the CURRENT expectation, not the same stale one
	// twice — the first call allocates a fresh edit_version, and the
	// second must send that back or hit the compare-and-swap refusal this
	// test is not about.
	editVersion := 0
	for range 2 {
		putBody := fmt.Appendf(nil, `{"overrideOn":true,"routeOff":false,"responses":{},"editVersion":%d}`, editVersion)
		status, body, err = srv.CallAsMCP(t.Context(), src, http.MethodPut, opPath, putBody)
		if err != nil || status != http.StatusOK {
			t.Fatalf("put operation (labelled route): status=%d err=%v body=%s", status, err, body)
		}
		var doc struct {
			EditVersion int64 `json:"editVersion"`
		}
		if err := json.Unmarshal(body, &doc); err != nil {
			t.Fatalf("decode put operation response: %v", err)
		}
		editVersion = int(doc.EditVersion)
	}

	rows, err := srv.checkpointsRepo.List(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	if n := len(filterCheckpointKind(rows, checkpoints.KindAuto)); n != 0 {
		t.Errorf("auto checkpoints with CheckpointDebounce=0 = %d, want 0 — the wrapper must not be installed at all", n)
	}
}

// TestAutoCheckpointWrapper_labelledWritesOneAndExcludedWritesNone is §4's
// requirement 4a: with a non-zero window, a labelled route writes exactly
// one auto checkpoint carrying that route's exact SIG-LABELS string, and an
// excluded route writes none. Without this test no bar in this run
// exercises an installed wrapper at all — every OTHER test in this package
// pins CheckpointDebounce to zero.
func TestAutoCheckpointWrapper_labelledWritesOneAndExcludedWritesNone(t *testing.T) {
	t.Parallel()
	// 300s: wide enough that nothing this test itself does could ever
	// re-arm it — re-arming is CHECKREPO's own table test over
	// [checkpoints.Repo.Auto]'s window arithmetic, not this test's job.
	const window = 300
	srv := loopbackTestServer(t, func(cfg *config.Config) {
		cfg.CheckpointDebounce = window
	})
	src := loopbackTestSrc()

	status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodPost, "/api/workspaces", []byte(`{"name":"debounce-on"}`))
	if err != nil || status != http.StatusCreated {
		t.Fatalf("create workspace: status=%d err=%v body=%s", status, err, body)
	}
	var ws struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(body, &ws); err != nil {
		t.Fatalf("decode create-workspace response: %v", err)
	}

	// POST .../session is one of §4's group-C exclusions: it never touches
	// a workspace layer at all (DESIGN.md:1098 forbids session directives
	// from touching SQLite), so its row carries cpNeverTouchesLayer, not a
	// label, and the wrapper was never attached to this pattern's handler. liveState
	// is nil on this bare test server, so the handler itself answers a
	// plain 503 — irrelevant here: this wrapper runs (or doesn't) before
	// the handler is ever reached, so what the handler answers proves
	// nothing about it either way.
	if _, _, err := srv.CallAsMCP(t.Context(), src, http.MethodPost,
		fmt.Sprintf("/api/workspaces/%d/session", ws.ID), nil); err != nil {
		t.Fatalf("post session directive (excluded route): %v", err)
	}
	rows, err := srv.checkpointsRepo.List(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	if n := len(filterCheckpointKind(rows, checkpoints.KindAuto)); n != 0 {
		t.Fatalf("auto checkpoints after one call on an EXCLUDED route = %d, want 0", n)
	}

	// PUT .../operations/{opKey} is labelled. One call must write exactly
	// one auto row, and that row must carry §4's exact label text and be
	// attributed to the authenticated caller — the MCP identity here,
	// never a session (there is none on this path).
	opKey := overrides.OpKey(http.MethodGet, "/widgets/sub")
	opPath := fmt.Sprintf("/api/workspaces/%d/operations/%s", ws.ID, opKey)
	putBody := []byte(`{"overrideOn":true,"routeOff":false,"responses":{},"editVersion":0}`)
	status, body, err = srv.CallAsMCP(t.Context(), src, http.MethodPut, opPath, putBody)
	if err != nil || status != http.StatusOK {
		t.Fatalf("put operation (labelled route): status=%d err=%v body=%s", status, err, body)
	}

	rows, err = srv.checkpointsRepo.List(t.Context(), ws.ID)
	if err != nil {
		t.Fatalf("list checkpoints: %v", err)
	}
	autos := filterCheckpointKind(rows, checkpoints.KindAuto)
	if len(autos) != 1 {
		t.Fatalf("auto checkpoints after one LABELLED call = %d, want exactly 1 (%+v)", len(autos), rows)
	}

	const pattern = "PUT /api/workspaces/{id}/operations/{opKey}"
	wantLabel := checkpointLabelsFromTable()[pattern]
	if wantLabel == "" {
		t.Fatalf("test precondition broken: %q carries no cpLabelled policy in routes()", pattern)
	}
	if autos[0].Label != wantLabel {
		t.Errorf("label = %q, want %q (§4's own text for %s)", autos[0].Label, wantLabel, pattern)
	}

	mcpUser, err := srv.mcpIdentity(t.Context())
	if err != nil {
		t.Fatalf("mcpIdentity(): %v", err)
	}
	if autos[0].CreatedBy == nil {
		t.Fatal("createdBy on the auto row is nil, want the MCP identity's id — createdBy must come from the authenticated USER, never a session")
	}
	if *autos[0].CreatedBy != mcpUser.ID {
		t.Errorf("createdBy = %d, want %d (the authenticated caller, not a session — there is none on the MCP loopback path)", *autos[0].CreatedBy, mcpUser.ID)
	}
}

// resourceCheckpointTestClient is a minimal cookie/CSRF-carrying HTTP client
// over an in-process handler — this file's own equivalent of
// admin_test.go's jsonRequest/login pair, which this package cannot reach
// into (see this file's own top-of-file doc comment for why). It exists
// only for TestAutoCheckpointWrapper_resourceDecisionRoute_noCheckpoint
// below, which needs the SESSION path (not CallAsMCP/mcpAllowedRoutes,
// which does not carry POST /api/specs — spec import is deliberately
// unreachable from MCP, "What is deliberately absent" in CLAUDE.md) to
// import a spec and bind a workspace to it before it can reach the
// resource-decisions route this test is actually about.
type resourceCheckpointTestClient struct {
	t         *testing.T
	handler   http.Handler
	cookie    *http.Cookie
	csrfToken string
}

func newResourceCheckpointTestClient(t *testing.T, srv *Server) *resourceCheckpointTestClient {
	t.Helper()
	handler := srv.Handler()
	loginReq := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login",
		strings.NewReader(`{"name":"Res","password":"correct horse battery staple"}`))
	loginReq.Header.Set("Content-Type", "application/json")
	loginReq.Header.Set("Origin", "http://mocker.local")
	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, loginReq)
	if rec.Code != http.StatusOK {
		t.Fatalf("login: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	cookies := rec.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("login: wrote %d cookies, want 1", len(cookies))
	}
	var body struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode login response: %v", err)
	}
	return &resourceCheckpointTestClient{t: t, handler: handler, cookie: cookies[0], csrfToken: body.CSRFToken}
}

func (c *resourceCheckpointTestClient) do(method, target, jsonBody string) *httptest.ResponseRecorder {
	c.t.Helper()
	var req *http.Request
	if jsonBody == "" {
		req = httptest.NewRequest(method, target, nil)
	} else {
		req = httptest.NewRequest(method, target, strings.NewReader(jsonBody))
		req.Header.Set("Content-Type", "application/json")
	}
	req.Header.Set("Origin", "http://mocker.local")
	req.AddCookie(c.cookie)
	if c.csrfToken != "" {
		req.Header.Set("X-CSRF-Token", c.csrfToken)
	}
	rec := httptest.NewRecorder()
	c.handler.ServeHTTP(rec, req)
	return rec
}

// TestAutoCheckpointWrapper_resourceDecisionRoute_noCheckpoint is D13's
// clause 41 (D10): confirming and then declining a resource-derived family
// each leave the auto checkpoint count UNCHANGED. POST
// .../resource-decisions carries cpRevisionOnly on its row in
// [Server.routes], not a label — this test is what catches an
// implementation that reached for the default (labelling the route)
// instead: since P3b (decisions.md mocker-p3b-resources, D3 R25),
// config_snap DOES carry a resource's configuration (the resources and
// resource_decisions rows), but a label here would still promise an undo
// this shape cannot perform — the wrapper snapshots BEFORE the handler
// runs, so a CONFIRM's own row holds the PRE-confirm state, and P3b's
// restore is UPSERT-only, so rolling back to it cannot remove the
// resources row the confirm created. P3b's original warrant went on to
// say such a rollback cannot bring back the entities a subsequent
// decline's cascade destroyed either — P3d (decisions.md
// mocker-p3d-datasnap, D6b) EXPIRES that second half: with data_snap now
// captured, the identical pre-handler snapshot carries the rows a later
// decline destroys, and a rollback to it with restoreData:true does bring
// them back (see internal/checkpoints' own restore tests). The route
// still gets no label, but the reason is the debounce, not the snapshot's
// shape (D6b) — a checkpoint fires at most once per
// MOCKER_CHECKPOINT_DEBOUNCE seconds, so a label here would make
// declining undoable only sometimes, which the shipped «НЕОБРАТИМО» copy
// cannot honestly soften to. This test's own assertions do not change.
func TestAutoCheckpointWrapper_resourceDecisionRoute_noCheckpoint(t *testing.T) {
	t.Parallel()
	const window = 300
	srv := loopbackTestServer(t, func(cfg *config.Config) {
		cfg.CheckpointDebounce = window
	})
	client := newResourceCheckpointTestClient(t, srv)

	specBody, err := json.Marshal(map[string]string{
		"name": "Derivation", "source": "upload", "document": string(testspec.DerivationDoc()),
	})
	if err != nil {
		t.Fatalf("marshal spec import body: %v", err)
	}
	specRec := client.do(http.MethodPost, "http://mocker.local/api/specs", string(specBody))
	if specRec.Code != http.StatusCreated {
		t.Fatalf("import spec: status = %d, want 201; body = %s", specRec.Code, specRec.Body.String())
	}
	var spec struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(specRec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("decode import spec response: %v", err)
	}

	wsRec := client.do(http.MethodPost, "http://mocker.local/api/workspaces",
		fmt.Sprintf(`{"name":"Res Demo","specId":%d}`, spec.ID))
	if wsRec.Code != http.StatusCreated {
		t.Fatalf("create workspace: status = %d, want 201; body = %s", wsRec.Code, wsRec.Body.String())
	}
	var ws struct {
		ID   int64  `json:"id"`
		Slug string `json:"slug"`
	}
	if err := json.Unmarshal(wsRec.Body.Bytes(), &ws); err != nil {
		t.Fatalf("decode create workspace response: %v", err)
	}

	countAuto := func() int {
		t.Helper()
		rows, err := srv.checkpointsRepo.List(t.Context(), ws.ID)
		if err != nil {
			t.Fatalf("list checkpoints: %v", err)
		}
		return len(filterCheckpointKind(rows, checkpoints.KindAuto))
	}
	if n := countAuto(); n != 0 {
		t.Fatalf("auto checkpoints before any resource decision = %d, want 0", n)
	}

	confirmRec := client.do(http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", ws.ID),
		fmt.Sprintf(`{"routeFamily":%q,"state":"confirmed"}`, testspec.FamilyWidgets))
	if confirmRec.Code != http.StatusOK {
		t.Fatalf("confirm resource: status = %d, want 200; body = %s", confirmRec.Code, confirmRec.Body.String())
	}
	if n := countAuto(); n != 0 {
		t.Errorf("auto checkpoints after CONFIRM = %d, want 0 (D13 clause 41)", n)
	}

	declineRec := client.do(http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", ws.ID),
		fmt.Sprintf(`{"routeFamily":%q,"state":"declined","confirmSlug":%q}`, testspec.FamilyWidgets, ws.Slug))
	if declineRec.Code != http.StatusOK {
		t.Fatalf("decline resource: status = %d, want 200; body = %s", declineRec.Code, declineRec.Body.String())
	}
	if n := countAuto(); n != 0 {
		t.Errorf("auto checkpoints after DECLINE = %d, want 0 (D13 clause 41)", n)
	}
}

// checkpointPolicyByPattern projects the route table's checkpoint column
// into a pattern → policy map, the shape the four hand-kept exclusion maps
// and the label map used to have. It is a PROJECTION now, not a second
// declaration: the maps this replaces were keyed by the same pattern
// strings [Server.routes] already spells, and a route could join a group
// here while its row said nothing — or say one thing here and another
// there. The reasons those maps carried moved to the rows themselves
// (route_table.go), where a reader asking "why does this verb take no undo
// point" is already looking.
func checkpointPolicyByPattern(t *testing.T) map[string]checkpointPolicy {
	t.Helper()
	out := make(map[string]checkpointPolicy)
	for _, rt := range (&Server{}).routes() {
		if _, dup := out[rt.pattern]; dup {
			t.Fatalf("route registered twice: %q", rt.pattern)
		}
		out[rt.pattern] = rt.checkpoint
	}
	return out
}

// checkpointLabelsFromTable is the label half of the same projection — the
// map [Server.routeMux] used to consult before the label moved onto the
// row. Only the tests need it now, which is the point.
func checkpointLabelsFromTable() map[string]string {
	out := make(map[string]string)
	for _, rt := range (&Server{}).routes() {
		if rt.checkpoint.group == cpGroupLabelled {
			out[rt.pattern] = rt.checkpoint.label
		}
	}
	return out
}

// TestAutoCheckpointPolicy_pinsEveryMutatingRoute is §4's requirement 4b,
// and its shape is deliberate: it derives every MUTATING pattern from
// [Server.routes] itself, not from a literal count or a copy of a map — so
// a route added later without deciding its policy is caught here, carrying
// the zero-value cpGroupUndecided, rather than shipping silently
// unlabelled. The weaker shape ("keys resolve, the set equals these eight,
// the size is eight") is explicitly forbidden by §4: all three of those
// assertions stay green when a route is added with no label, which is the
// exact omission this test exists to catch.
//
// Since the policy moved onto the row (2026-09-07) the "added without
// deciding" case is caught TWICE over — the compiler cannot make a caller
// supply a field, but the zero value is not a group, so the assertion below
// fires on it. What the per-group counts still buy is the OTHER defect: a
// route that quietly MIGRATES between groups, or a group that grows past
// what §4 states, changes a count here even though every route still has
// an opinion.
//
// Built from a zero *Server exactly like openapi_contract_test.go's own
// route-table walk does: routes() never touches its receiver, so the
// contract test — and this one — can build the table without a live
// Server behind it.
func TestAutoCheckpointPolicy_pinsEveryMutatingRoute(t *testing.T) {
	t.Parallel()

	// §4's own counts, group by group. cpGroupRead is the population
	// excluded by construction — a GET writes nothing — and is counted here
	// only so a read that acquired a mutating policy (or a mutating route
	// that acquired cpRead) shows up as two counts moving at once.
	//
	// The reason each group holds what it holds is at the ROWS, in
	// route_table.go, not here: this test counts, the table argues.
	want := map[checkpointGroup]int{
		// The nine labelled routes: §4's eight plus A1's PUT editor
		// (mocker-a-mcp gate document, D4 item 4).
		cpGroupLabelled: 9,
		// §4's group of six. P3a's resource-decisions joined it rather
		// than starting a fifth group of its own (D10, D13 clause 41).
		cpGroupRevisionOnly: 6,
		// §4's group of two: create and delete of the workspace itself.
		cpGroupNoLayerYet: 2,
		// §4's group, grown one slice at a time and never by a route
		// changing its mind: P3b's reset-data (D3 R12), P3f's rederive
		// (D7.3), P6b's endpoint preview (D13), P6c's close and push
		// (D9), A6's two asset writes (D3), A11's two entity writes,
		// P4b's import and fork — twenty.
		cpGroupNeverTouchesLayer: 20,
		// §4's group of four: a scenario row (including the clone) or a
		// checkpoint row itself.
		cpGroupAnotherLayer: 4,
		// Every GET in the table.
		cpGroupRead: 29,
	}

	byPattern := checkpointPolicyByPattern(t)
	got := make(map[checkpointGroup]int, len(want))
	for _, policy := range byPattern {
		got[policy.group]++
	}
	for group, n := range want {
		if got[group] != n {
			t.Errorf("group %q holds %d routes, want %d (§4's own count)", group, got[group], n)
		}
	}
	if n := got[cpGroupUndecided]; n != 0 {
		t.Errorf("%d route(s) carry the zero-value checkpoint policy; every row must decide", n)
	}
	if total, table := len(want), len(byPattern); sum(got) != table {
		t.Errorf("the %d counted groups cover %d of %d rows — a group is missing from want", total, sum(got), table)
	}

	// A label is the ONE thing [Server.routeMux] reads off the policy, so a
	// label outside cpGroupLabelled (or a labelled row with an empty label)
	// would silently wrap — or silently fail to wrap — the wrong handlers.
	for pattern, policy := range byPattern {
		switch {
		case policy.group == cpGroupLabelled && policy.label == "":
			t.Errorf("route %q is cpLabelled with an empty label", pattern)
		case policy.group != cpGroupLabelled && policy.label != "":
			t.Errorf("route %q carries label %q outside cpGroupLabelled", pattern, policy.label)
		}
	}

	// §4's own arithmetic, kept because the numbers are its argument and not
	// this test's: 8 labelled + 5 + 2 + 9 + 4 excluded = 28, plus A1's
	// PUT .../endpoints/{eid} (mocker-a-mcp gate document, D4 item 4) labelled
	// as a ninth: 9 + 5 + 2 + 9 + 4 = 29. P3a adds one more mutating route,
	// POST .../resource-decisions, into the revision-only group rather than
	// as a labelled tenth (D10, D13 clause 41): 9 + 6 + 2 + 9 + 4 = 30. P3b
	// (decisions.md mocker-p3b-resources, D3 R12) adds ONE more mutating
	// route, POST .../reset-data, into the never-touches-a-layer group
	// rather than a labelled tenth or a fifth exclusion group of its own:
	// 9 + 6 + 2 + 10 + 4 = 31. P3f (decisions.md mocker-p3f-rederive, D7.3)
	// adds ONE more mutating route, POST /api/specs/{id}/rederive, into the
	// never-touches-a-layer group: 9 + 6 + 2 + 11 + 4 = 32. P6b (decisions.md
	// mocker-p6b-sse-mock D13) adds ONE more, POST .../endpoints/preview,
	// into the same group: 9 + 6 + 2 + 12 + 4 = 33. P6c (decisions.md
	// mocker-p6c-live-conns D9) adds TWO more into the same group — DELETE
	// .../connections/{cid} and POST .../connections/{cid}/frames, a cancel
	// and a RAM inbox, no row either — 9 + 6 + 2 + 14 + 4 = 35; A6's PUT and
	// DELETE on assets join the never-touches-the-layer group (bytes are
	// not configuration), plus A11's two entity writes, plus P4b's import and
	// fork — 41. A mismatch here means the route table changed shape in a way
	// this test's data has not caught up with yet — a signal to look, not to
	// bump the number blindly.
	var mutating []string
	for _, rt := range (&Server{}).routes() {
		method, _, ok := strings.Cut(rt.pattern, " ")
		if !ok {
			t.Fatalf("route pattern %q has no method prefix", rt.pattern)
		}
		switch method {
		case http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
			mutating = append(mutating, rt.pattern)
		}
	}
	if len(mutating) != 41 {
		t.Fatalf("routes() registers %d mutating patterns, want 41", len(mutating))
	}

	// The two halves the group counts alone cannot state: a mutating route
	// must not claim cpRead (which would read as "excluded by construction"
	// for a verb that writes), and a read must not carry anything else.
	isMutating := make(map[string]bool, len(mutating))
	for _, pattern := range mutating {
		isMutating[pattern] = true
		switch byPattern[pattern].group {
		case cpGroupUndecided:
			t.Errorf("mutating route %q carries no checkpoint policy — it needs a label or one of the four exclusion groups", pattern)
		case cpGroupRead:
			t.Errorf("mutating route %q claims cpRead; a verb that writes is not excluded by construction", pattern)
		}
	}
	for pattern, policy := range byPattern {
		if !isMutating[pattern] && policy.group != cpGroupRead {
			t.Errorf("read route %q carries %q; a GET writes nothing and must say cpRead", pattern, policy.group)
		}
	}
}

// sum adds the counts of a group histogram — a one-liner, named only so the
// coverage assertion above reads as one sentence.
func sum(counts map[checkpointGroup]int) int {
	total := 0
	for _, n := range counts {
		total += n
	}
	return total
}
