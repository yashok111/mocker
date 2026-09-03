// Autocheckpoint_test.go is P2d's own test file (§4 of the P2d slice's
// context document): the debounce ("auto") checkpoint trigger [Server]
// installs at [Server.routeMux]'s mux.HandleFunc line. It is package admin
// (white-box), like openapi_contract_test.go and loopback_test.go, and for
// the same two reasons those files are: it needs [Server.routes] and
// [autoCheckpointLabels] directly (both unexported), and it reuses
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
	// from touching SQLite), so it is not a key of autoCheckpointLabels and
	// the wrapper was never attached to this pattern's handler. liveState
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
	wantLabel, ok := autoCheckpointLabels[pattern]
	if !ok || wantLabel == "" {
		t.Fatalf("test precondition broken: autoCheckpointLabels has no non-empty entry for %q", pattern)
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
// .../resource-decisions is a member of autoCheckpointExcludedRevisionOnly
// above, not a key of autoCheckpointLabels — this test is what catches an
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

// The four exclusion groups §4 names, each carrying its own reason, exactly
// as the table in that section lists them. TestAutoCheckpointLabels_
// pinsEveryMutatingRoute below builds its "decided" set from
// autoCheckpointLabels plus the union of these four rather than from a bare
// literal list, so a route that migrates between groups — or a group that
// silently grows past what §4 states — shows up as a per-group size
// mismatch, not just a passing total.

// autoCheckpointExcludedRevisionOnly is §4's group of six: these bump
// workspaces.revision but change nothing a snapshot HOLDS. Two of the six
// (rollback, reset-overrides) already write their own pre-destructive row
// for the same instant; a second, auto snapshot of the identical state
// would be noise in the screen an operator reads to find a state worth
// returning to. P3a's resource-decisions route joined this group rather
// than starting a fifth one of its own (D10, D13 clause 41), and P3b
// (decisions.md mocker-p3b-resources, D3 R25) rewrites the WARRANT below
// without moving it: since P3b, config_snap DOES carry this route's two
// tables (resources, resource_decisions) — the exclusion now rests on two
// narrower facts instead. The auto-checkpoint wrapper snapshots BEFORE the
// handler runs, so the row a CONFIRM leaves holds the PRE-confirm state,
// and P3b's restore is UPSERT-only (it never deletes a resources row), so
// rolling back to that row cannot undo the confirm it preceded — a label
// would promise exactly the undo this shape cannot perform. The one case
// where a label would genuinely help, a DECLINE, is the case
// MOCKER_CHECKPOINT_DEBOUNCE already eats in the common sequence (confirm
// then decline inside one window suppresses the decline's own row).
var autoCheckpointExcludedRevisionOnly = map[string]bool{
	"POST /api/workspaces/{id}/scenarios/{sid}/activate": true,
	"POST /api/workspaces/{id}/scenarios/deactivate":     true,
	"DELETE /api/workspaces/{id}/scenarios/{sid}":        true,
	"POST /api/workspaces/{id}/rollback/{cid}":           true,
	"POST /api/workspaces/{id}/reset-overrides":          true,
	"POST /api/workspaces/{id}/resource-decisions":       true,
}

// autoCheckpointExcludedNoLayerYet is §4's group of two: no workspace
// layer exists yet (create — the request IS what creates it) or none is
// left (delete — nothing survives to restore into).
var autoCheckpointExcludedNoLayerYet = map[string]bool{
	"POST /api/workspaces":        true,
	"DELETE /api/workspaces/{id}": true,
}

// autoCheckpointExcludedNeverTouchesLayer is §4's group, TWELVE since P6b
// (decisions.md mocker-p6b-sse-mock D13: POST .../endpoints/preview, a
// stream draft's first frames, writes nothing), ELEVEN since P3f
// (decisions.md mocker-p3f-rederive, D7.1/D7.3; was ten): these never touch
// a workspace layer at all. POST /api/specs/{id}/rederive is P3f's own
// addition — it resolves no workspace at all (it is spec-scoped, D4.1) and
// writes only resource_suggestions, a table config_snap does not carry, so
// there is nothing here for an auto checkpoint to snapshot or restore.
// Session directives (POST/DELETE .../session)
// are excluded categorically, under all circumstances — DESIGN.md:1098
// forbids them from touching SQLite, so they can never gain a label later
// without also violating that rule. POST .../preview joined this group in
// P2f for the identical reason: it reads a workspace layer (to build its
// own throwaway runtime) but never writes one, which is why it stays out
// of this map's opposite (autoCheckpointLabels). It is no longer out of
// mcpAllowedRoutes: A2 (mocker-a-mcp gate document, D7) allowlisted it as
// preview_operation. Only the checkpoint half of the original note
// survives, and it survives on its own argument — writing nothing is what
// keeps it unlabelled, and that has not changed. POST .../reset-data is
// P3b's own addition to this group — the exact precedent is
// "DELETE /api/workspaces/{id}/traffic" right below it: a destructive verb
// over a table the workspace layer does not hold. resources and
// resource_decisions ARE configuration and reset-data touches neither; it
// changes entities, which config_snap does not carry (D3 R12).
var autoCheckpointExcludedNeverTouchesLayer = map[string]bool{
	"POST " + loginPath:                                             true,
	"POST /api/auth/logout":                                         true,
	"POST /api/specs":                                               true,
	"DELETE /api/specs/{id}":                                        true,
	"POST /api/workspaces/{id}/probe":                               true,
	"POST /api/workspaces/{id}/session":                             true,
	"DELETE /api/workspaces/{id}/session":                           true,
	"DELETE /api/workspaces/{id}/traffic":                           true,
	"POST /api/workspaces/{id}/preview":                             true,
	"POST /api/workspaces/{id}/reset-data":                          true,
	"PUT /api/workspaces/{id}/resources/{family}/entities/{key}":    true,
	"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}": true,
	// P4b (2026-09-02): an import creates a NEW workspace and a fork
	// writes only the copy — neither touches a layer of the workspace
	// the route names (import names none), and both write the new row's
	// baseline checkpoint themselves, inside the creating transaction.
	"POST /api/workspaces/import":    true,
	"POST /api/workspaces/{id}/fork": true,
	"POST /api/specs/{id}/rederive":  true,
	// A6 (decisions.md mocker-a6-assets D3): an asset's bytes are not
	// configuration — config_snap carries no asset (DESIGN §32.4) — so a
	// checkpoint before an upload or a delete would capture nothing the
	// verb changes. Both bump revision on their own (D11).
	"PUT /api/workspaces/{id}/assets/{name}":    true,
	"DELETE /api/workspaces/{id}/assets/{name}": true,
	// P6b (decisions.md mocker-p6b-sse-mock D13): a stream draft's preview
	// writes no row, bumps no revision — the same reasoning as
	// POST .../preview above, one table over.
	"POST /api/workspaces/{id}/endpoints/preview": true,
	// P6c (decisions.md mocker-p6c-live-conns D9): a close cancels a
	// connection's context and a push queues a frame into its RAM inbox —
	// neither writes a row, bumps revision or touches a layer.
	"DELETE /api/workspaces/{id}/connections/{cid}":      true,
	"POST /api/workspaces/{id}/connections/{cid}/frames": true,
}

// autoCheckpointExcludedAnotherLayer is §4's group of four: these write a
// row of a DIFFERENT layer — a scenario row (including the clone) or a
// checkpoint row itself — not the workspace layer this trigger exists to
// snapshot.
var autoCheckpointExcludedAnotherLayer = map[string]bool{
	"POST /api/workspaces/{id}/scenarios":           true,
	"PUT /api/workspaces/{id}/scenarios/{sid}":      true,
	"POST /api/workspaces/{id}/checkpoints":         true,
	"DELETE /api/workspaces/{id}/checkpoints/{cid}": true,
}

// TestAutoCheckpointLabels_pinsEveryMutatingRoute is §4's requirement 4b,
// and its shape is deliberate: it derives every MUTATING pattern from
// [Server.routes] itself, not from a literal count or a copy of the map —
// so a route added later without deciding its label is caught here, in
// neither autoCheckpointLabels nor a named exclusion, rather than shipping
// silently unlabelled. The weaker shape ("keys resolve, the set equals
// these eight, the size is eight") is explicitly forbidden by §4: all three
// of those assertions stay green when a route is added with no label,
// which is the exact omission this test exists to catch.
//
// Built from a zero *Server exactly like openapi_contract_test.go's own
// route-table walk does: routes() never touches its receiver, so the
// contract test — and this one — can build the table without a live
// Server behind it.
func TestAutoCheckpointLabels_pinsEveryMutatingRoute(t *testing.T) {
	t.Parallel()

	groups := []struct {
		name string
		set  map[string]bool
		want int
	}{
		{"bumps revision only, changes nothing a snapshot holds", autoCheckpointExcludedRevisionOnly, 6},
		{"no workspace layer to snapshot yet or anymore", autoCheckpointExcludedNoLayerYet, 2},
		{"never touches a workspace layer at all", autoCheckpointExcludedNeverTouchesLayer, 20},
		{"writes a row of another layer", autoCheckpointExcludedAnotherLayer, 4},
	}

	// decided maps every pattern this run has an opinion about back to
	// WHICH opinion, so a pattern claimed twice (a copy-paste into the
	// wrong group, say) is a fatal test-data error rather than a silently
	// double-counted route.
	decided := make(map[string]string, len(autoCheckpointLabels))
	for pattern := range autoCheckpointLabels {
		decided[pattern] = "labelled"
	}
	for _, g := range groups {
		if len(g.set) != g.want {
			t.Errorf("exclusion group %q has %d entries, want %d (§4's own count)", g.name, len(g.set), g.want)
		}
		for pattern := range g.set {
			if prior, ok := decided[pattern]; ok {
				t.Fatalf("pattern %q is claimed by both %q and %q — that is a bug in this test's own data, not in the route table", pattern, prior, g.name)
			}
			decided[pattern] = g.name
		}
	}

	if got := len(autoCheckpointLabels); got != 9 {
		t.Errorf("len(autoCheckpointLabels) = %d, want 9", got)
	}

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

	// §4's own arithmetic: 8 labelled + 5 + 2 + 9 + 4 excluded = 28, plus A1's
	// PUT .../endpoints/{eid} (mocker-a-mcp gate document, D4 item 4) labelled
	// as a ninth: 9 + 5 + 2 + 9 + 4 = 29. P3a adds one more mutating route,
	// POST .../resource-decisions, into the revision-only group rather than
	// as a labelled tenth (D10, D13 clause 41): 9 + 6 + 2 + 9 + 4 = 30. P3b
	// (decisions.md mocker-p3b-resources, D3 R12) adds ONE more mutating
	// route, POST .../reset-data, into the never-touches-a-layer group
	// rather than a labelled tenth or a fifth exclusion group of its own:
	// 9 + 6 + 2 + 10 + 4 = 31. P3f (decisions.md mocker-p3f-rederive, D7.3)
	// adds ONE more mutating route, POST /api/specs/{id}/rederive, into the
	// never-touches-a-layer group rather than a labelled tenth or a fifth
	// exclusion group of its own: 9 + 6 + 2 + 11 + 4 = 32. P6b (decisions.md
	// mocker-p6b-sse-mock D13) adds ONE more, POST .../endpoints/preview,
	// into the same group: 9 + 6 + 2 + 12 + 4 = 33. P6c (decisions.md
	// mocker-p6c-live-conns D9) adds TWO more into the same group — DELETE
	// .../connections/{cid} and POST .../connections/{cid}/frames, a cancel
	// and a RAM inbox, no row either — 9 + 6 + 2 + 14 + 4 = 35; A6's PUT and
	// DELETE on assets join the never-touches-the-layer group (bytes are
	// not configuration), plus A11's two entity writes, plus P4b's import and fork — 41. A mismatch
	// here means the route table changed shape in a way this test's data
	// has not caught up with yet — a signal to look, not to bump the number
	// blindly.
	if len(mutating) != 41 {
		t.Fatalf("routes() registers %d mutating patterns, want 41", len(mutating))
	}

	for _, pattern := range mutating {
		if _, ok := decided[pattern]; !ok {
			t.Errorf("mutating route %q is neither a key of autoCheckpointLabels nor a member of a named exclusion group in this test — it needs one or the other", pattern)
		}
	}
}
