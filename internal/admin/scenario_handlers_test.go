package admin_test

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/yashok111/mocker/internal/httpx"
)

// Tests for scenario_handlers.go (P2b). Deliberately narrow: this package's
// job is the ROUTING/wire-shape contract (§C) — that the two GETs answer
// different shapes, that the repo's error sentinels map to the wire codes
// §I requires, that every mutating route sits behind CSRF exactly like
// every other admin route does. The composition itself (A1-A5, A18) is
// internal/mockplane's unit-tested business, and internal/scenarios' repo
// (A7-A11) is its own package's; duplicating either here would be a second,
// weaker copy of a test that already exists closer to the code it covers.
//
// Helpers are prefixed scenarioTest* rather than reusing/redeclaring
// admin_test.go's own newTestServer/testServer or createWorkspace — this
// file's own instruction: don't shadow a name that already means something
// in this package.

// scenarioTestURL builds one of the six admin scenario routes under
// wsID/sid, omitting sid when it is zero — the two routes with no {sid}
// segment (list/create, deactivate) never pass one.
func scenarioTestURL(wsID int64, suffix string) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/scenarios%s", wsID, suffix)
}

// scenarioTestCreate POSTs a create-from-current-state request and decodes
// whatever the server answered (a [scenarioDetailView] on 201, an
// [httpx.ErrorBody] on anything else) into a bare map — a map, not the
// unexported view type this package cannot reach, is what lets one helper
// serve both the success and error paths a caller wants to assert on.
func scenarioTestCreate(t *testing.T, ts *testServer, cookie *http.Cookie, csrfToken string, wsID int64, name string, wantStatus int) map[string]any {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, scenarioTestURL(wsID, ""), map[string]string{"name": name}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("create scenario %q: status = %d, want %d; body = %s", name, rec.Code, wantStatus, rec.Body.String())
	}
	return scenarioTestDecode(t, rec)
}

// scenarioTestClone POSTs a create request WITH a `from` field — P2d's
// clone path (§0) — and decodes the response the same way scenarioTestCreate
// does. A separate helper rather than a `from *int64` parameter bolted onto
// scenarioTestCreate: every existing call site of that helper exercises the
// create-from-current-state path and would have to pass nil for no reason.
func scenarioTestClone(t *testing.T, ts *testServer, cookie *http.Cookie, csrfToken string, wsID, from int64, name string, wantStatus int) map[string]any {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, scenarioTestURL(wsID, ""), map[string]any{"name": name, "from": from}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("clone scenario %q from %d: status = %d, want %d; body = %s", name, from, rec.Code, wantStatus, rec.Body.String())
	}
	return scenarioTestDecode(t, rec)
}

// scenarioTestRename PUTs .../scenarios/{sid} with {"name": name,
// "editVersion": editVersion} — P2d's rename route, A3-guarded — and decodes
// the response the same way scenarioTestCreate does. editVersion is the
// caller's compare-and-swap expectation (D10: required on the wire), the
// value a preceding read of this same scenario returned.
func scenarioTestRename(t *testing.T, ts *testServer, cookie *http.Cookie, csrfToken string, wsID, sid, editVersion int64, name string, wantStatus int) map[string]any {
	t.Helper()
	target := scenarioTestURL(wsID, fmt.Sprintf("/%d", sid))
	req := jsonRequest(t, http.MethodPut, target, map[string]any{"name": name, "editVersion": editVersion}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("rename scenario %d to %q: status = %d, want %d; body = %s", sid, name, rec.Code, wantStatus, rec.Body.String())
	}
	return scenarioTestDecode(t, rec)
}

// scenarioTestActivate POSTs .../scenarios/{sid}/activate. Like
// scenarioTestCreate, it takes no request body on the wire (nil here, not
// an empty map) — [Server.handleActivateScenario] reads {sid} from the
// path alone, exactly like POST .../traffic/{tid}/to-override needs none.
func scenarioTestActivate(t *testing.T, ts *testServer, cookie *http.Cookie, csrfToken string, wsID, sid int64, wantStatus int) map[string]any {
	t.Helper()
	target := scenarioTestURL(wsID, fmt.Sprintf("/%d/activate", sid))
	req := jsonRequest(t, http.MethodPost, target, nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("activate scenario %d: status = %d, want %d; body = %s", sid, rec.Code, wantStatus, rec.Body.String())
	}
	return scenarioTestDecode(t, rec)
}

// scenarioTestGet issues a bare, cookie-only GET (no CSRF token needed — GET
// is not state-changing, so [Server.enforceCSRF] never looks at it).
func scenarioTestGet(t *testing.T, ts *testServer, cookie *http.Cookie, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	return ts.do(req)
}

// scenarioTestDecode decodes rec's JSON body into a bare map, or returns nil
// for a body-less response (204, or any other empty body a caller does not
// need to inspect).
func scenarioTestDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	if rec.Body.Len() == 0 {
		return nil
	}
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v; raw = %s", err, rec.Body.String())
	}
	return body
}

// scenarioTestErrorCode pulls error.code out of a decoded [httpx.ErrorBody]
// map — every non-2xx response on this plane uses that one envelope
// (internal/httpx/respond.go), so this is the one place a caller reaches
// into it rather than re-deriving the "error"/"code" path per test.
func scenarioTestErrorCode(t *testing.T, body map[string]any) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response body has no \"error\" object: %v", body)
	}
	code, _ := errObj["code"].(string)
	return code
}

// TestScenarios_listAndDetailShapesDiffer pins §C's central claim: LIST
// answers {id, name, createdAt, isActive} and NOTHING else, while DETAIL
// answers the full snapshot (settings/basePath/spec/overrides on top of
// those same four fields). A wrong implementation that composed the list
// from the same view type as the detail would leak the snapshot into every
// row of a workspace's history — exactly the page-load cost §C rejects.
func TestScenarios_listAndDetailShapesDiffer(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	created := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(created["id"].(float64))

	rec := scenarioTestGet(t, ts, cookie, scenarioTestURL(wsID, ""))
	if rec.Code != http.StatusOK {
		t.Fatalf("list scenarios: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Scenarios []map[string]any `json:"scenarios"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Scenarios) != 1 {
		t.Fatalf("len(scenarios) = %d, want 1", len(list.Scenarios))
	}

	entry := list.Scenarios[0]
	// A3: editVersion joins the list shape too (D5) — it is a per-row
	// compare-and-swap token, not snapshot data, so it does not violate §C's
	// claim the comment above states.
	wantListKeys := map[string]bool{"id": true, "name": true, "createdAt": true, "isActive": true, "editVersion": true}
	for k := range entry {
		if !wantListKeys[k] {
			t.Errorf("list entry carries key %q — the list shape must never carry snapshot data (§C): %v", k, entry)
		}
	}
	for k := range wantListKeys {
		if _, ok := entry[k]; !ok {
			t.Errorf("list entry missing key %q: %v", k, entry)
		}
	}
	if entry["isActive"] != false {
		t.Errorf("freshly created, unactivated scenario: isActive = %v, want false", entry["isActive"])
	}

	rec = scenarioTestGet(t, ts, cookie, scenarioTestURL(wsID, fmt.Sprintf("/%d", sid)))
	if rec.Code != http.StatusOK {
		t.Fatalf("get scenario: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	detail := scenarioTestDecode(t, rec)
	for _, k := range []string{"id", "name", "createdAt", "isActive", "settings", "basePath", "spec", "overrides"} {
		if _, ok := detail[k]; !ok {
			t.Errorf("detail missing key %q — the whole point of a SEPARATE detail shape (§C): %v", k, detail)
		}
	}
	// POST's own 201 answers the identical detail shape — screen 9 shows the
	// just-created scenario without a second round trip.
	for _, k := range []string{"settings", "basePath", "spec", "overrides"} {
		if _, ok := created[k]; !ok {
			t.Errorf("create response missing key %q: %v", k, created)
		}
	}
}

// TestScenarios_createWhileActiveAnswers409 pins A10 for the
// create-from-current-state path: a create body with NO `from` field is
// still refused while a scenario is already active, and the refusal leaves
// observable state unchanged — a warning-only implementation, or one that
// silently rolled back a partial insert, both leave a trace this test would
// catch (a second scenario in the list, or a name mismatch). This is no
// longer the WHOLE rule as of P2d — a body WITH `from` bypasses this refusal
// entirely (CloneFrom never reads the workspace's own layer), and that path
// gets its own test, TestScenarios_cloneWhileActiveAnswers201.
func TestScenarios_createWhileActiveAnswers409(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))
	scenarioTestActivate(t, ts, cookie, csrfToken, wsID, sid, http.StatusOK)

	body := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S2", http.StatusConflict)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeConflict {
		t.Errorf("create-while-active: wire code = %q, want %q (A10)", code, httpx.CodeConflict)
	}

	rec := scenarioTestGet(t, ts, cookie, scenarioTestURL(wsID, ""))
	list := scenarioTestDecode(t, rec)
	names := list["scenarios"].([]any)
	if len(names) != 1 {
		t.Fatalf("scenario list after refused create = %d entries, want 1 (S2 must not have been written): %v", len(names), names)
	}
	if got := names[0].(map[string]any)["name"]; got != "S1" {
		t.Errorf("surviving scenario name = %v, want %q", got, "S1")
	}
}

// TestScenarios_foreignScenarioIDAnswers404 pins A8: a scenario id that
// belongs to a DIFFERENT workspace is refused exactly like one that does
// not exist at all — the repo's WHERE clause makes the two indistinguishable
// by construction (internal/scenarios/repo.go's own doc comment on
// ErrNotFound). Checked on both GET and activate: two independent call
// sites into the same ownership check, and a bug in either one is a
// cross-workspace read or write this test exists to fail.
func TestScenarios_foreignScenarioIDAnswers404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookieA, csrfTokenA, wsAFloat, _ := ts.createWorkspace(t, "Alex", "WorkspaceA")
	wsA := int64(wsAFloat)
	// Same session acts on B too — this plane has no per-workspace owner
	// check (any authenticated user reaches any workspace id), so a second
	// login is not needed to prove A8's check is about WORKSPACE ownership
	// of the SCENARIO row, not about which human is asking.
	_, _, wsBFloat, _ := ts.createWorkspace(t, "Blair", "WorkspaceB")
	wsB := int64(wsBFloat)

	sb := scenarioTestCreate(t, ts, cookieA, csrfTokenA, wsB, "SB", http.StatusCreated)
	sbID := int64(sb["id"].(float64))

	rec := scenarioTestGet(t, ts, cookieA, scenarioTestURL(wsA, fmt.Sprintf("/%d", sbID)))
	if rec.Code != http.StatusNotFound {
		t.Fatalf("GET foreign scenario id: status = %d, want 404 (A8); body = %s", rec.Code, rec.Body.String())
	}
	if code := scenarioTestErrorCode(t, scenarioTestDecode(t, rec)); code != httpx.CodeNotFound {
		t.Errorf("GET foreign scenario id: wire code = %q, want %q", code, httpx.CodeNotFound)
	}

	body := scenarioTestActivate(t, ts, cookieA, csrfTokenA, wsA, sbID, http.StatusNotFound)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeNotFound {
		t.Errorf("activate foreign scenario id: wire code = %q, want %q (A8)", code, httpx.CodeNotFound)
	}
}

// TestScenarios_mutatingRoutesRequireCSRF pins §I/DESIGN §15 for all four
// state-changing scenario routes at once — mirrors
// TestOpenAPIContract_stateChangingRoutesRequireCSRF's CONTRACT-level check
// with a live request per route, since that test only proves the document
// DECLARES csrfToken, never that enforceCSRF actually rejects a request
// missing it. A valid session cookie is attached throughout; only the
// X-CSRF-Token header is withheld, so a 403 here can only be enforceCSRF's
// token check firing, never requireUser's unrelated 401.
//
// The scenario created before the loop survives it: enforceCSRF rejects
// before the mux ever dispatches to a handler (Handler()'s own chain order),
// so the rejected "delete" attempt never removes it, and "activate" still
// has a valid {sid} to name.
func TestScenarios_mutatingRoutesRequireCSRF(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)
	created := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(created["id"].(float64))

	tests := []struct {
		name   string
		method string
		target string
	}{
		{"create", http.MethodPost, scenarioTestURL(wsID, "")},
		{"delete", http.MethodDelete, scenarioTestURL(wsID, fmt.Sprintf("/%d", sid))},
		{"activate", http.MethodPost, scenarioTestURL(wsID, fmt.Sprintf("/%d/activate", sid))},
		{"deactivate", http.MethodPost, scenarioTestURL(wsID, "/deactivate")},
	}
	for _, tt := range tests {
		req := jsonRequest(t, tt.method, tt.target, nil, cookie, "" /* no csrfToken */)
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without CSRF token: status = %d, want 403; body = %s", tt.name, rec.Code, rec.Body.String())
		}
	}
}

// TestScenarios_cloneWhileActiveAnswers201 pins the very thing
// TestScenarios_createWhileActiveAnswers409's OLD prose overstated: a clone
// (`from` present) succeeds while a scenario is active, because
// [scenarios.Repo.CloneFrom] never reads the workspace's own layer, so
// neither A10's refusal nor A11's coherent-read retry can apply to it — see
// CloneFrom's own doc comment. A wrong implementation that routed `from`
// through CreateFromCurrentState, or added the refusal to CloneFrom by
// analogy, would 409 here instead.
func TestScenarios_cloneWhileActiveAnswers201(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))
	scenarioTestActivate(t, ts, cookie, csrfToken, wsID, sid, http.StatusOK)

	clone := scenarioTestClone(t, ts, cookie, csrfToken, wsID, sid, "S1 clone", http.StatusCreated)
	if got := clone["name"]; got != "S1 clone" {
		t.Errorf("clone name = %v, want %q", got, "S1 clone")
	}
	// Same detail shape as the current-state create path (§C) — the clone's
	// own snapshot fields must be populated, not a zero Bundle from an
	// undecoded re-read (SIG-CLONE's own warning).
	for _, k := range []string{"settings", "basePath", "spec", "overrides"} {
		if _, ok := clone[k]; !ok {
			t.Errorf("clone response missing key %q: %v", k, clone)
		}
	}
	if clone["isActive"] != false {
		t.Errorf("freshly cloned scenario: isActive = %v, want false", clone["isActive"])
	}
}

// TestScenarios_cloneForeignSourceAnswers404 pins A8 for the clone path: a
// `from` naming a scenario in a DIFFERENT workspace is refused exactly like
// one that does not exist at all (mirrors
// TestScenarios_foreignScenarioIDAnswers404's GET/activate coverage, one
// call site further).
func TestScenarios_cloneForeignSourceAnswers404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookieA, csrfTokenA, wsAFloat, _ := ts.createWorkspace(t, "Alex", "WorkspaceA")
	wsA := int64(wsAFloat)
	_, _, wsBFloat, _ := ts.createWorkspace(t, "Blair", "WorkspaceB")
	wsB := int64(wsBFloat)

	sb := scenarioTestCreate(t, ts, cookieA, csrfTokenA, wsB, "SB", http.StatusCreated)
	sbID := int64(sb["id"].(float64))

	body := scenarioTestClone(t, ts, cookieA, csrfTokenA, wsA, sbID, "clone of SB", http.StatusNotFound)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeNotFound {
		t.Errorf("clone with foreign from: wire code = %q, want %q (A8)", code, httpx.CodeNotFound)
	}
}

// TestScenarios_cloneDuplicateNameAnswers409 pins the UNIQUE(workspace_id,
// name) violation on CloneFrom's insert: cloning under a name that already
// exists in the workspace is refused, same as CreateFromCurrentState's own
// duplicate-name path.
func TestScenarios_cloneDuplicateNameAnswers409(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))

	body := scenarioTestClone(t, ts, cookie, csrfToken, wsID, sid, "S1", http.StatusConflict)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeConflict {
		t.Errorf("clone with duplicate name: wire code = %q, want %q", code, httpx.CodeConflict)
	}
}

// TestScenarios_cloneBlankNameAnswers400 pins that CloneFrom's name
// validation runs BEFORE the INSERT, not after — a body like
// {"from":N,"name":"   "} must never reach the snapshot table (SIG-CLONE's
// own warning: the column has no CHECK constraint, and a row created that
// way would be unreachable through ByName).
func TestScenarios_cloneBlankNameAnswers400(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))

	body := scenarioTestClone(t, ts, cookie, csrfToken, wsID, sid, "   ", http.StatusBadRequest)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeBadRequest {
		t.Errorf("clone with blank name: wire code = %q, want %q", code, httpx.CodeBadRequest)
	}
}

// TestScenarios_renameAnswersNewNameAndPersists pins the whole rename
// contract at once: the PUT itself answers 200 with the SAME detail shape
// GET returns, carrying the NEW name — not the old one, and not an empty
// body from an undecoded re-read (SIG-RENAME's own warning) — and a
// follow-up GET proves the rename actually persisted rather than being
// echoed back without being written.
func TestScenarios_renameAnswersNewNameAndPersists(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))

	renamed := scenarioTestRename(t, ts, cookie, csrfToken, wsID, sid, int64(s1["editVersion"].(float64)), "S1 renamed", http.StatusOK)
	if got := renamed["name"]; got != "S1 renamed" {
		t.Errorf("rename response name = %v, want %q", got, "S1 renamed")
	}
	for _, k := range []string{"id", "name", "createdAt", "isActive", "settings", "basePath", "spec", "overrides"} {
		if _, ok := renamed[k]; !ok {
			t.Errorf("rename response missing key %q — same detail shape as GET (§C): %v", k, renamed)
		}
	}

	rec := scenarioTestGet(t, ts, cookie, scenarioTestURL(wsID, fmt.Sprintf("/%d", sid)))
	if rec.Code != http.StatusOK {
		t.Fatalf("get renamed scenario: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	detail := scenarioTestDecode(t, rec)
	if got := detail["name"]; got != "S1 renamed" {
		t.Errorf("persisted scenario name = %v, want %q", got, "S1 renamed")
	}
}

// TestScenarios_renameDuplicateNameAnswers409 pins the same
// UNIQUE(workspace_id, name) violation on Rename's UPDATE that
// TestScenarios_cloneDuplicateNameAnswers409 pins for CloneFrom's INSERT.
func TestScenarios_renameDuplicateNameAnswers409(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	s2 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S2", http.StatusCreated)
	s2ID := int64(s2["id"].(float64))

	body := scenarioTestRename(t, ts, cookie, csrfToken, wsID, s2ID, int64(s2["editVersion"].(float64)), "S1", http.StatusConflict)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeConflict {
		t.Errorf("rename to taken name: wire code = %q, want %q", code, httpx.CodeConflict)
	}
}

// TestScenarios_renameForeignIDAnswers404 pins A8 for the rename path,
// mirroring TestScenarios_foreignScenarioIDAnswers404's GET/activate
// coverage one call site further, exactly like the clone test above does
// for create.
func TestScenarios_renameForeignIDAnswers404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookieA, csrfTokenA, wsAFloat, _ := ts.createWorkspace(t, "Alex", "WorkspaceA")
	wsA := int64(wsAFloat)
	_, _, wsBFloat, _ := ts.createWorkspace(t, "Blair", "WorkspaceB")
	wsB := int64(wsBFloat)

	sb := scenarioTestCreate(t, ts, cookieA, csrfTokenA, wsB, "SB", http.StatusCreated)
	sbID := int64(sb["id"].(float64))

	// Any non-nil editVersion resolves to 404 here (A8/D7): the UPDATE is
	// scoped to wsA and sbID belongs to wsB, so it never gets far enough to
	// compare the version at all — cross-tenant stays not-found, never an
	// edit conflict.
	body := scenarioTestRename(t, ts, cookieA, csrfTokenA, wsA, sbID, int64(sb["editVersion"].(float64)), "renamed SB", http.StatusNotFound)
	if code := scenarioTestErrorCode(t, body); code != httpx.CodeNotFound {
		t.Errorf("rename foreign scenario id: wire code = %q, want %q (A8)", code, httpx.CodeNotFound)
	}
}

// TestScenarios_renameEditVersionRequired pins A3/D10: a PUT body that
// omits editVersion is rejected by name (400), never silently accepted as
// an unguarded write.
func TestScenarios_renameEditVersionRequired(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))

	target := scenarioTestURL(wsID, fmt.Sprintf("/%d", sid))
	req := jsonRequest(t, http.MethodPut, target, map[string]any{"name": "Renamed"}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("rename with no editVersion: status = %d, want 400; body = %s", rec.Code, rec.Body.String())
	}
}

// TestScenarios_renameEditConflictMismatch pins D6's round-trippable
// conflict payload for the rename route: a second writer sends the version
// it originally read, now stale because a first writer already renamed the
// row — refused 409 edit_conflict, carrying the CURRENT stored name (never
// an echo of the second writer's own submitted name) plus the version the
// server actually holds.
func TestScenarios_renameEditConflictMismatch(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	s1 := scenarioTestCreate(t, ts, cookie, csrfToken, wsID, "S1", http.StatusCreated)
	sid := int64(s1["id"].(float64))
	seededVersion := int64(s1["editVersion"].(float64))

	// First writer renames using the version it read at create time.
	first := scenarioTestRename(t, ts, cookie, csrfToken, wsID, sid, seededVersion, "Renamed-First", http.StatusOK)

	// Second writer still believes the row is at the create-time version.
	target := scenarioTestURL(wsID, fmt.Sprintf("/%d", sid))
	req := jsonRequest(t, http.MethodPut, target, map[string]any{"name": "Renamed-Second", "editVersion": seededVersion}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second (stale) rename: status = %d, want 409; body = %s", rec.Code, rec.Body.String())
	}

	var conflict struct {
		Error struct {
			Code    string `json:"code"`
			Details struct {
				Name        string `json:"name"`
				EditVersion int64  `json:"editVersion"`
			} `json:"details"`
		} `json:"error"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &conflict); err != nil {
		t.Fatalf("decode conflict body: %v", err)
	}
	if conflict.Error.Code != "edit_conflict" {
		t.Errorf("conflict code = %q, want %q", conflict.Error.Code, "edit_conflict")
	}
	if conflict.Error.Details.Name != "Renamed-First" {
		t.Errorf("conflict details.name = %q, want %q (the server's current name, from the FIRST rename)", conflict.Error.Details.Name, "Renamed-First")
	}
	if conflict.Error.Details.EditVersion != int64(first["editVersion"].(float64)) {
		t.Errorf("conflict details.editVersion = %d, want %v (the version the server actually holds)", conflict.Error.Details.EditVersion, first["editVersion"])
	}
}
