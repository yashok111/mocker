package admin_test

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/testspec"
)

// Tests for checkpoint_handlers.go (P2c). Deliberately narrow, exactly like
// scenario_handlers_test.go's own stated scope: this package's job is the
// ROUTING/wire-shape contract (§C) — that the four routes answer the shapes
// §C draws, that checkpoints.Repo's error sentinels map to the wire codes
// this plane's other handlers already use, that every mutating route sits
// behind CSRF. The domain logic (C1-C18) is internal/checkpoints' own
// package's unit-tested business (repo_test.go); duplicating it here would
// be a second, weaker copy of a test that already exists closer to the code
// it covers.
//
// Helpers are prefixed checkpointTest* for the same reason
// scenario_handlers_test.go's are prefixed scenarioTest*: this file's own
// instruction not to shadow admin_test.go's newTestServer/createWorkspace.

func checkpointsURL(wsID int64) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/checkpoints", wsID)
}

func rollbackURL(wsID, cid int64) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/rollback/%d", wsID, cid)
}

func resetOverridesURL(wsID int64) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-overrides", wsID)
}

func checkpointURL(wsID, cid int64) string {
	return fmt.Sprintf("http://mocker.local/api/workspaces/%d/checkpoints/%d", wsID, cid)
}

// checkpointTestCreate POSTs a manual-checkpoint create request and decodes
// whatever the server answered into a bare map — a map, not the unexported
// view type this package cannot reach, exactly like scenarioTestCreate.
func checkpointTestCreate(t *testing.T, ts *testServer, cookie *http.Cookie, csrfToken string, wsID int64, label string, wantStatus int) map[string]any {
	t.Helper()
	req := jsonRequest(t, http.MethodPost, checkpointsURL(wsID), map[string]string{"label": label}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != wantStatus {
		t.Fatalf("create checkpoint %q: status = %d, want %d; body = %s", label, rec.Code, wantStatus, rec.Body.String())
	}
	return checkpointTestDecode(t, rec)
}

func checkpointTestGet(t *testing.T, ts *testServer, cookie *http.Cookie, target string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	req.AddCookie(cookie)
	return ts.do(req)
}

func checkpointTestDecode(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
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

func checkpointTestErrorCode(t *testing.T, body map[string]any) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response body has no \"error\" object: %v", body)
	}
	code, _ := errObj["code"].(string)
	return code
}

func checkpointTestErrorMessage(t *testing.T, body map[string]any) string {
	t.Helper()
	errObj, ok := body["error"].(map[string]any)
	if !ok {
		t.Fatalf("response body has no \"error\" object: %v", body)
	}
	msg, _ := errObj["message"].(string)
	return msg
}

// workspaceRevision reads .../workspaces/{id}'s current revision — used to
// prove C12: a manual checkpoint must NOT move it, while rollback and a
// destructive reset must move it by exactly one.
func workspaceRevision(t *testing.T, ts *testServer, cookie *http.Cookie, wsID int64) int64 {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d", wsID), nil)
	req.AddCookie(cookie)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("get workspace: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := checkpointTestDecode(t, rec)
	rev, ok := body["revision"].(float64)
	if !ok {
		t.Fatalf("workspace response has no numeric revision: %v", body)
	}
	return int64(rev)
}

// TestCheckpoints_createAnswersSummaryAndDoesNotBumpRevision pins §C's
// create shape (id/kind/label/createdAt/createdBy, kind:"manual") and C12:
// nothing served changes when a history entry is written, so an
// implementation that copied customep.Repo.Create's always-bump pattern
// would move the revision this test reads before and after.
func TestCheckpoints_createAnswersSummaryAndDoesNotBumpRevision(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	before := workspaceRevision(t, ts, cookie, wsID)

	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "перед экспериментом", http.StatusCreated)
	if got := created["kind"]; got != "manual" {
		t.Errorf("created checkpoint kind = %v, want %q", got, "manual")
	}
	if got := created["label"]; got != "перед экспериментом" {
		t.Errorf("created checkpoint label = %v, want %q", got, "перед экспериментом")
	}
	// C15: the handler supplies the session user; nothing else in §G reads
	// this field, so a handler that never wired it would pass every other
	// check while leaving the column NULL.
	if created["createdBy"] == nil {
		t.Error("created checkpoint createdBy is null, want the session user's id (C15)")
	}
	for _, k := range []string{"id", "kind", "label", "createdAt", "createdBy"} {
		if _, ok := created[k]; !ok {
			t.Errorf("created checkpoint missing key %q: %v", k, created)
		}
	}

	after := workspaceRevision(t, ts, cookie, wsID)
	if after != before {
		t.Errorf("revision after manual checkpoint = %d, want unchanged from %d (C12)", after, before)
	}
}

// TestCheckpoints_listIsNewestFirstAndNeverCarriesSnapshot pins §C's list
// shape directly: the wrapper key is "checkpoints", each entry carries
// EXACTLY the six summary fields — five from before P3d plus hasData
// (derived from data_snap IS NOT NULL, never from decompressing it) — and
// nothing from either snapshot's own bytes (a list returning N BLOBs is
// the page-load cost §C rejects), and the two rows come back newest first.
func TestCheckpoints_listIsNewestFirstAndNeverCarriesSnapshot(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	first := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "первая точка", http.StatusCreated)
	second := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "вторая точка", http.StatusCreated)

	rec := checkpointTestGet(t, ts, cookie, checkpointsURL(wsID))
	if rec.Code != http.StatusOK {
		t.Fatalf("list checkpoints: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	var list struct {
		Checkpoints []map[string]any `json:"checkpoints"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &list); err != nil {
		t.Fatalf("decode list: %v", err)
	}
	if len(list.Checkpoints) != 2 {
		t.Fatalf("len(checkpoints) = %d, want 2", len(list.Checkpoints))
	}

	wantKeys := map[string]bool{"id": true, "kind": true, "label": true, "createdAt": true, "createdBy": true, "hasData": true}
	for _, entry := range list.Checkpoints {
		for k := range entry {
			if !wantKeys[k] {
				t.Errorf("list entry carries key %q — the list shape must never carry snapshot data (§C): %v", k, entry)
			}
		}
		for k := range wantKeys {
			if _, ok := entry[k]; !ok {
				t.Errorf("list entry missing key %q: %v", k, entry)
			}
		}
	}
	if got := list.Checkpoints[0]["label"]; got != second["label"] {
		t.Errorf("newest-first: first list entry label = %v, want the most recently created %v", got, second["label"])
	}
	if got := list.Checkpoints[1]["label"]; got != first["label"] {
		t.Errorf("newest-first: second list entry label = %v, want the first-created %v", got, first["label"])
	}
}

// TestCheckpoints_labelRuneCap_cyrillic is §G observation 18(c), assigned to
// this package explicitly: a Cyrillic 201-rune label is refused and a
// 200-rune one is accepted. Cyrillic, not ASCII, is what separates a byte
// cap from a rune cap (each Cyrillic codepoint is two UTF-8 bytes) — an
// implementation checking len(label) in BYTES would reject the VALID
// 200-rune label here (400 bytes > 200) while this test's whole point is
// that it must be accepted.
func TestCheckpoints_labelRuneCap_cyrillic(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	// 'а' is Cyrillic U+0430, not Latin 'a' — two bytes each in UTF-8.
	atCap := strings.Repeat("а", 200)
	overCap := strings.Repeat("а", 201)

	body := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, overCap, http.StatusBadRequest)
	if code := checkpointTestErrorCode(t, body); code != httpx.CodeBadRequest {
		t.Errorf("201-rune Cyrillic label: wire code = %q, want %q", code, httpx.CodeBadRequest)
	}
	if msg := checkpointTestErrorMessage(t, body); !strings.Contains(msg, "label") {
		t.Errorf("201-rune Cyrillic label: error message %q does not name the label field (C14)", msg)
	}

	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, atCap, http.StatusCreated)
	if got := created["label"]; got != atCap {
		t.Errorf("200-rune Cyrillic label: stored label = %v, want the full 200-rune string accepted verbatim", got)
	}
}

// TestCheckpoints_createEmptyLabelAnswers400 pins C14's other half: a label
// that trims to empty is refused, not stored as an empty string.
func TestCheckpoints_createEmptyLabelAnswers400(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	body := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "   ", http.StatusBadRequest)
	if code := checkpointTestErrorCode(t, body); code != httpx.CodeBadRequest {
		t.Errorf("empty label: wire code = %q, want %q", code, httpx.CodeBadRequest)
	}
}

// TestCheckpoints_rollbackRestoreDataTrueRestoresEntities is P3d's inversion
// of what used to be TestCheckpoints_rollbackRestoreDataTrueAnswers400
// (D10's own table): restoreData:true no longer answers 400 naming a
// missing feature — it reaches [checkpoints.Repo.Rollback], and a family
// declined AFTER the checkpoint comes back both CONFIGURED and POPULATED,
// with dataRestored reporting that the restore actually ran (D7, D11
// property 6b/6c). restoreData:false against the SAME checkpoint still
// takes the ordinary path afterward — no confirmSlug, dataRestored stays
// false, nothing about the just-restored rows is touched again.
func TestCheckpoints_rollbackRestoreDataTrueRestoresEntities(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "before decline", http.StatusCreated)
	cid := int64(created["id"].(float64))
	// Property 8's server half, this site's own reading: the 201 of
	// POST .../checkpoints projects hasData from the SAME
	// newCheckpointSummaryView the list route uses (checked again below).
	if hasData, _ := created["hasData"].(bool); !hasData {
		t.Fatalf("create checkpoint over a confirmed, populated family: hasData = %v, want true", created["hasData"])
	}

	declineReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "declined", "confirmSlug": slug}, cookie, csrfToken)
	if rec := ts.do(declineReq); rec.Code != http.StatusOK {
		t.Fatalf("decline: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	req := jsonRequest(t, http.MethodPost, rollbackURL(wsID, cid),
		map[string]any{"restoreData": true, "confirmSlug": slug}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback restoreData:true: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := checkpointTestDecode(t, rec)
	if _, ok := body["revision"]; !ok {
		t.Errorf("rollback response missing \"revision\": %v", body)
	}
	if _, ok := body["scenarioActive"]; !ok {
		t.Errorf("rollback response missing \"scenarioActive\": %v", body)
	}
	if dr, ok := body["dataRestored"].(bool); !ok || !dr {
		t.Errorf("rollback restoreData:true: dataRestored = %v, want true (property 6b: decoded on rollbackResponseView)", body["dataRestored"])
	}

	// The entity rows are genuinely back, not just the switch (D7's
	// success row): GET .../resources reads the widgets family as
	// confirmed again with its original row count.
	familyGet := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/workspaces/%d/resources", wsID), nil)
	familyGet.AddCookie(cookie)
	familyRec := ts.do(familyGet)
	var familyBody struct {
		Families []resourceFamilyWire `json:"families"`
	}
	if err := json.Unmarshal(familyRec.Body.Bytes(), &familyBody); err != nil {
		t.Fatalf("decode resources response: %v", err)
	}
	var restored resourceFamilyWire
	for _, f := range familyBody.Families {
		if f.RouteFamily == testspec.FamilyWidgets {
			restored = f
		}
	}
	if restored.Decision == nil || *restored.Decision != "confirmed" {
		t.Errorf("widgets decision after restore = %v, want \"confirmed\"", restored.Decision)
	}
	if restored.EntityCount == nil || *restored.EntityCount != 5 {
		t.Errorf("widgets entityCount after restore = %v, want 5", restored.EntityCount)
	}

	req = jsonRequest(t, http.MethodPost, rollbackURL(wsID, cid), map[string]bool{"restoreData": false}, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback restoreData:false: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body = checkpointTestDecode(t, rec)
	if body["scenarioActive"] != false {
		t.Errorf("rollback scenarioActive = %v, want false (no scenario active in this test)", body["scenarioActive"])
	}
	if dr, ok := body["dataRestored"].(bool); !ok || dr {
		t.Errorf("rollback restoreData:false: dataRestored = %v, want false", body["dataRestored"])
	}
}

// TestCheckpoints_rollbackWithoutBodyAnswersOK is the SURFACE round's own
// regression, not a hypothetical: api/openapi.json declares
// .../rollback/{cid}'s requestBody optional (required:false) — the ONLY
// route in the whole contract that does — and C11 states "false and absent
// take the normal path". Every OTHER test in this file, including the two
// above, sends an explicit JSON object (map[string]bool{...} or {} at
// line ~306) via jsonRequest, which always encodes SOMETHING onto the wire
// even for an "empty" map. The real caller
// (web/src/components/HistoryPage.tsx:410's rollback.mutate) never passes
// `data`, so orval's generated rollbackWorkspace() fetches with ZERO bytes
// of body — a case none of those tests exercise. Passing jsonRequest's
// `body any` parameter the untyped nil (not map[string]any{}) reproduces
// that exactly: jsonRequest's own `if body != nil` guard then skips the
// json.Encoder call entirely, leaving the request's *bytes.Buffer at 0
// bytes, so decodeJSON's Decode call hits io.EOF on the first Read — the
// same error handleRollbackWorkspace must now treat as "no body supplied",
// not as malformed input.
func TestCheckpoints_rollbackWithoutBodyAnswersOK(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "RollbackNoBody")
	wsID := int64(wsFloat)

	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "M1", http.StatusCreated)
	cid := int64(created["id"].(float64))

	req := jsonRequest(t, http.MethodPost, rollbackURL(wsID, cid), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback with a truly empty body: status = %d, want 200 (requestBody.required:false, C11); body = %s", rec.Code, rec.Body.String())
	}
	body := checkpointTestDecode(t, rec)
	if _, ok := body["revision"]; !ok {
		t.Errorf("rollback with empty body response missing \"revision\": %v", body)
	}
	if _, ok := body["scenarioActive"]; !ok {
		t.Errorf("rollback with empty body response missing \"scenarioActive\": %v", body)
	}
}

// TestCheckpoints_rollbackForeignOrMissingIDAnswers404 mirrors
// TestScenarios_foreignScenarioIDAnswers404: a checkpoint id belonging to a
// DIFFERENT workspace, and one that never existed at all, both answer 404 —
// the repo's own WHERE clause makes the two indistinguishable by
// construction (checkpoints.ErrNotFound's own doc comment).
func TestCheckpoints_rollbackForeignOrMissingIDAnswers404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookieA, csrfTokenA, wsAFloat, _ := ts.createWorkspace(t, "Alex", "WorkspaceA")
	wsA := int64(wsAFloat)
	// Same session acts on B too — this plane has no per-workspace owner
	// check (any authenticated user reaches any workspace id), exactly like
	// TestScenarios_foreignScenarioIDAnswers404's identical comment.
	_, _, wsBFloat, _ := ts.createWorkspace(t, "Blair", "WorkspaceB")
	wsB := int64(wsBFloat)

	cb := checkpointTestCreate(t, ts, cookieA, csrfTokenA, wsB, "on B", http.StatusCreated)
	cbID := int64(cb["id"].(float64))

	body := map[string]any{}
	req := jsonRequest(t, http.MethodPost, rollbackURL(wsA, cbID), body, cookieA, csrfTokenA)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rollback to a checkpoint from a different workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if code := checkpointTestErrorCode(t, checkpointTestDecode(t, rec)); code != httpx.CodeNotFound {
		t.Errorf("rollback to a checkpoint from a different workspace: wire code = %q, want %q", code, httpx.CodeNotFound)
	}

	req = jsonRequest(t, http.MethodPost, rollbackURL(wsA, cbID+1_000_000), body, cookieA, csrfTokenA)
	rec = ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("rollback to a nonexistent checkpoint id: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
}

// TestCheckpoints_resetOverridesNoopAnswersChangedFalse pins C9's no-op
// signal on the wire: a fresh workspace has nothing to reset, so the call
// must answer changed:false with the revision unchanged — a handler that
// hard-codes true, or one whose repo call always writes, would pass every
// other test in this file.
func TestCheckpoints_resetOverridesNoopAnswersChangedFalse(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	before := workspaceRevision(t, ts, cookie, wsID)

	req := jsonRequest(t, http.MethodPost, resetOverridesURL(wsID), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("reset-overrides on a fresh workspace: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := checkpointTestDecode(t, rec)
	if body["changed"] != false {
		t.Errorf("reset-overrides no-op: changed = %v, want false (C9)", body["changed"])
	}
	if _, ok := body["scenarioActive"]; !ok {
		t.Errorf("reset-overrides response missing \"scenarioActive\": %v", body)
	}

	after := workspaceRevision(t, ts, cookie, wsID)
	if after != before {
		t.Errorf("revision after no-op reset = %d, want unchanged from %d (C9)", after, before)
	}
}

// TestCheckpoints_deleteRemovesRowAndRepeatAnswers404 pins P2d's SIG-DELCP
// shape: success is 204 with an empty body, the deleted row is gone from
// the list while its sibling survives, and deleting the SAME id again — now
// a zero-row DELETE — answers 404 exactly like a checkpoint that never
// existed at all ([checkpoints.ErrNotFound]'s own doc comment on why the two
// are indistinguishable by construction).
func TestCheckpoints_deleteRemovesRowAndRepeatAnswers404(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)

	kept := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "остаётся", http.StatusCreated)
	keptID := int64(kept["id"].(float64))
	doomed := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "удаляется", http.StatusCreated)
	doomedID := int64(doomed["id"].(float64))

	req := jsonRequest(t, http.MethodDelete, checkpointURL(wsID, doomedID), nil, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusNoContent {
		t.Fatalf("delete checkpoint: status = %d, want 204; body = %s", rec.Code, rec.Body.String())
	}
	if rec.Body.Len() != 0 {
		t.Errorf("delete checkpoint: body = %q, want empty (204)", rec.Body.String())
	}

	listRec := checkpointTestGet(t, ts, cookie, checkpointsURL(wsID))
	list := checkpointTestDecode(t, listRec)
	entries, _ := list["checkpoints"].([]any)
	if len(entries) != 1 {
		t.Fatalf("checkpoint list after delete = %d entries, want 1 (only the surviving row): %v", len(entries), entries)
	}
	if got := entries[0].(map[string]any)["id"]; got != float64(keptID) {
		t.Errorf("surviving checkpoint id = %v, want %d", got, keptID)
	}

	req = jsonRequest(t, http.MethodDelete, checkpointURL(wsID, doomedID), nil, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete already-deleted checkpoint: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if code := checkpointTestErrorCode(t, checkpointTestDecode(t, rec)); code != httpx.CodeNotFound {
		t.Errorf("delete already-deleted checkpoint: wire code = %q, want %q", code, httpx.CodeNotFound)
	}
}

// TestCheckpoints_deleteForeignWorkspaceIDAnswers404AndRowSurvives mirrors
// TestCheckpoints_rollbackForeignOrMissingIDAnswers404: a checkpoint id that
// exists but belongs to a DIFFERENT workspace answers 404 — the repo's own
// WHERE clause makes "exists elsewhere" indistinguishable from "does not
// exist" by construction — and, unlike a genuine delete, the row is left
// completely untouched: this is the one place in this file where a 404 must
// be proven to have had no side effect, since a successful delete IS this
// route's whole destructive purpose.
func TestCheckpoints_deleteForeignWorkspaceIDAnswers404AndRowSurvives(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookieA, csrfTokenA, wsAFloat, _ := ts.createWorkspace(t, "Alex", "WorkspaceA")
	wsA := int64(wsAFloat)
	// Same session acts on B too — this plane has no per-workspace owner
	// check (any authenticated user reaches any workspace id), exactly like
	// TestCheckpoints_rollbackForeignOrMissingIDAnswers404's identical comment.
	_, _, wsBFloat, _ := ts.createWorkspace(t, "Blair", "WorkspaceB")
	wsB := int64(wsBFloat)

	cb := checkpointTestCreate(t, ts, cookieA, csrfTokenA, wsB, "on B", http.StatusCreated)
	cbID := int64(cb["id"].(float64))

	req := jsonRequest(t, http.MethodDelete, checkpointURL(wsA, cbID), nil, cookieA, csrfTokenA)
	rec := ts.do(req)
	if rec.Code != http.StatusNotFound {
		t.Fatalf("delete a checkpoint from a different workspace: status = %d, want 404; body = %s", rec.Code, rec.Body.String())
	}
	if code := checkpointTestErrorCode(t, checkpointTestDecode(t, rec)); code != httpx.CodeNotFound {
		t.Errorf("delete a checkpoint from a different workspace: wire code = %q, want %q", code, httpx.CodeNotFound)
	}

	// The row must survive on B — the refused cross-workspace delete had no
	// effect at all, not a partial one.
	listRec := checkpointTestGet(t, ts, cookieA, checkpointsURL(wsB))
	list := checkpointTestDecode(t, listRec)
	entries, _ := list["checkpoints"].([]any)
	if len(entries) != 1 {
		t.Fatalf("workspace B's checkpoint list after the cross-workspace delete = %d entries, want 1 (untouched): %v", len(entries), entries)
	}
	if got := entries[0].(map[string]any)["id"]; got != float64(cbID) {
		t.Errorf("surviving checkpoint id = %v, want %d", got, cbID)
	}
}

// TestCheckpoints_mutatingRoutesRequireCSRF mirrors
// TestScenarios_mutatingRoutesRequireCSRF for all three of this file's
// mutating routes at once — a live request per route, since the contract
// test only proves the DOCUMENT declares csrfToken, never that enforceCSRF
// actually rejects a request missing it.
func TestCheckpoints_mutatingRoutesRequireCSRF(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken, wsFloat, _ := ts.createWorkspace(t, "Alex", "Demo")
	wsID := int64(wsFloat)
	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "M1", http.StatusCreated)
	cid := int64(created["id"].(float64))

	tests := []struct {
		name   string
		method string
		target string
	}{
		{"create", http.MethodPost, checkpointsURL(wsID)},
		{"rollback", http.MethodPost, rollbackURL(wsID, cid)},
		{"reset-overrides", http.MethodPost, resetOverridesURL(wsID)},
	}
	for _, tt := range tests {
		req := jsonRequest(t, tt.method, tt.target, nil, cookie, "" /* no csrfToken */)
		rec := ts.do(req)
		if rec.Code != http.StatusForbidden {
			t.Errorf("%s without CSRF token: status = %d, want 403; body = %s", tt.name, rec.Code, rec.Body.String())
		}
	}
	// The rejected "create" attempt above must not have written a row — the
	// list this leaves behind should still hold exactly the one checkpoint
	// created before the loop.
	rec := checkpointTestGet(t, ts, cookie, checkpointsURL(wsID))
	list := checkpointTestDecode(t, rec)
	entries, _ := list["checkpoints"].([]any)
	if len(entries) != 1 {
		t.Errorf("checkpoint list after CSRF-rejected create = %d entries, want 1: %v", len(entries), entries)
	}
}

// checkpointTestResourceID reads a confirmed family's resources.id directly
// — the same raw-SQL escape hatch resource_handlers_test.go's own ts.db.W
// already establishes for this package, used here only to reach state no
// admin route exposes: a resources row's own id, needed to insert an
// entity row directly rather than through 200 generated rows of POST X.
func checkpointTestResourceID(t *testing.T, ts *testServer, wsID int64, routeFamily string) int64 {
	t.Helper()
	var id int64
	if err := ts.db.R.QueryRowContext(t.Context(),
		"SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", wsID, routeFamily,
	).Scan(&id); err != nil {
		t.Fatalf("read resources.id for %q: %v", routeFamily, err)
	}
	return id
}

// checkpointTestEntityCount counts every entity row of the workspace,
// across every family — property 6a's "entity rows unchanged" half.
func checkpointTestEntityCount(t *testing.T, ts *testServer, wsID int64) int {
	t.Helper()
	var n int
	if err := ts.db.R.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM entities e JOIN resources r ON r.id = e.resource_id WHERE r.workspace_id = ?", wsID,
	).Scan(&n); err != nil {
		t.Fatalf("count entity rows for workspace %d: %v", wsID, err)
	}
	return n
}

// checkpointTestCount counts the workspace's checkpoint rows — property
// 6a's "checkpoint count unchanged" half: a refusal that wrote its
// pre-destructive row before failing would still show up here.
func checkpointTestCount(t *testing.T, ts *testServer, wsID int64) int {
	t.Helper()
	var n int
	if err := ts.db.R.QueryRowContext(t.Context(),
		"SELECT COUNT(*) FROM checkpoints WHERE workspace_id = ?", wsID,
	).Scan(&n); err != nil {
		t.Fatalf("count checkpoints for workspace %d: %v", wsID, err)
	}
	return n
}

// TestCheckpoints_rollbackDataRefusalsAnswerDistinctCodesAndChangeNothing is
// D11 property 6, its wire-code half and its clause (a): all FIVE rows of
// D7's refusal table, told apart by CODE rather than status — three of the
// five answer 409 — and each one leaves the workspace's entity rows AND its
// checkpoint count byte-for-byte unchanged. A refusal that half-ran is
// exactly what a status code alone cannot show.
func TestCheckpoints_rollbackDataRefusalsAnswerDistinctCodesAndChangeNothing(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	// A GOOD checkpoint, over the confirmed and populated family — the
	// target the confirmSlug refusals and the 413 both roll back to; only
	// its OWN data_snap is untouched.
	good := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "good", http.StatusCreated)
	goodCID := int64(good["id"].(float64))

	// A target whose OWN data_snap is NULL, forced directly: no live
	// workspace state reaches maxDataProbeBytes cheaply, and reproducing
	// the degrade path that would leave it NULL for real is
	// internal/checkpoints' own test
	// (TestCapture_degradesWhenTheProbeRefuses), not this file's — this
	// file's job is only that the CODE this state maps to is
	// no_data_snapshot.
	noData := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "no-data", http.StatusCreated)
	noDataCID := int64(noData["id"].(float64))
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE checkpoints SET data_snap = NULL WHERE id = ?", noDataCID); err != nil {
		t.Fatalf("null out data_snap: %v", err)
	}

	// A target whose data_snap is present but not a gzip stream at all —
	// decompressSnapshot fails on the magic bytes before a single byte of
	// JSON is read, landing on checkpoints.ErrCorruptSnapshot exactly as a
	// document corrupted at the JSON level does
	// (internal/checkpoints.TestRollback_refusesAHandBuiltDataDocumentOutsideValidateDatasDomain)
	// — this is the cheaper of the two ways there, and just as fatal.
	corrupt := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "corrupt", http.StatusCreated)
	corruptCID := int64(corrupt["id"].(float64))
	if _, err := ts.db.W.ExecContext(t.Context(), "UPDATE checkpoints SET data_snap = X'6E6F742D677A6970' WHERE id = ?", corruptCID); err != nil {
		t.Fatalf("corrupt data_snap: %v", err)
	}

	entitiesBefore := checkpointTestEntityCount(t, ts, wsID)
	checkpointsBefore := checkpointTestCount(t, ts, wsID)

	tests := []struct {
		name        string
		cid         int64
		confirmSlug string
		wantStatus  int
		wantCode    string
		// bloat inserts a single oversized entity row so THIS request's
		// own pre-destructive capture (of the CURRENT state, not the
		// target's) degrades past maxDataProbeBytes — entityDataProbeOverBudgetTx
		// sums payload bytes before captureEntitiesTx reads a single row,
		// so one row this large trips it without a bulk insert.
		bloat bool
	}{
		{name: "confirmSlug absent", cid: goodCID, confirmSlug: "", wantStatus: http.StatusConflict, wantCode: "confirm_slug_required"},
		{name: "confirmSlug wrong", cid: goodCID, confirmSlug: "not-the-slug", wantStatus: http.StatusConflict, wantCode: "confirm_slug_mismatch"},
		{name: "data_snap IS NULL", cid: noDataCID, confirmSlug: slug, wantStatus: http.StatusConflict, wantCode: "no_data_snapshot"},
		{name: "data_snap not gzip", cid: corruptCID, confirmSlug: slug, wantStatus: http.StatusInternalServerError, wantCode: httpx.CodeInternal},
		{name: "pre-destructive snapshot degrades", cid: goodCID, confirmSlug: slug, wantStatus: http.StatusRequestEntityTooLarge, wantCode: "data_snapshot_too_large", bloat: true},
	}
	for _, tt := range tests {
		if tt.bloat {
			resourceID := checkpointTestResourceID(t, ts, wsID, testspec.FamilyWidgets)
			huge := strings.Repeat("x", 6_300_000)
			if _, err := ts.db.W.ExecContext(t.Context(),
				"INSERT INTO entities (resource_id, scope_key, entity_key, data, created_at, updated_at) VALUES (?, '', 'huge', ?, 0, 0)",
				resourceID, huge,
			); err != nil {
				t.Fatalf("insert oversized entity row: %v", err)
			}
			t.Cleanup(func() {
				// context.Background(), not t.Context(): a t.Cleanup func
				// runs AFTER the test's own context is already canceled,
				// so a query bound to it would fail with "context
				// canceled" instead of actually deleting the row.
				if _, err := ts.db.W.ExecContext(context.Background(), "DELETE FROM entities WHERE entity_key = 'huge'"); err != nil {
					t.Fatalf("clean up oversized entity row: %v", err)
				}
			})
			entitiesBefore = checkpointTestEntityCount(t, ts, wsID)
			checkpointsBefore = checkpointTestCount(t, ts, wsID)
		}

		req := jsonRequest(t, http.MethodPost, rollbackURL(wsID, tt.cid),
			map[string]any{"restoreData": true, "confirmSlug": tt.confirmSlug}, cookie, csrfToken)
		rec := ts.do(req)
		if rec.Code != tt.wantStatus {
			t.Fatalf("%s: status = %d, want %d; body = %s", tt.name, rec.Code, tt.wantStatus, rec.Body.String())
		}
		if code := checkpointTestErrorCode(t, checkpointTestDecode(t, rec)); code != tt.wantCode {
			t.Errorf("%s: wire code = %q, want %q", tt.name, code, tt.wantCode)
		}
		if got := checkpointTestEntityCount(t, ts, wsID); got != entitiesBefore {
			t.Errorf("%s: entity rows after refusal = %d, want unchanged %d", tt.name, got, entitiesBefore)
		}
		if got := checkpointTestCount(t, ts, wsID); got != checkpointsBefore {
			t.Errorf("%s: checkpoint count after refusal = %d, want unchanged %d", tt.name, got, checkpointsBefore)
		}
	}
}

// TestCheckpoints_dataRestoredIsPerFamilyNotAnEchoOfTheRequest is D11
// property 6c: dataRestored is true when the restore RAN for at least one
// carried family and false when every one of them was skipped or the
// document carries none — NOT an echo of the request's own restoreData
// flag, and not "at least one row written or deleted" either. The dividing
// line is observed at the boundary D7 itself names: a RESOLVED family
// (its resources row still lives) whose stored and live relations are BOTH
// empty still counts as restored (dataRestored:true), while a document
// that carries no family at all counts as skipped (dataRestored:false) —
// two states an echo of the request could never distinguish, since both
// requests carry the identical restoreData:true.
func TestCheckpoints_dataRestoredIsPerFamilyNotAnEchoOfTheRequest(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)
	slug := ts.workspaceSlug(t, cookie, wsID)

	// dataRestored:false — a checkpoint taken BEFORE any family is
	// confirmed carries a document with no families at all (D10: since
	// P3d this is a non-NULL "families":[] document, not NULL — hasData
	// is still true, and D7's own false row is about the FAMILY SET the
	// document carries, not about data_snap's nullness).
	before := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "before any family", http.StatusCreated)
	beforeCID := int64(before["id"].(float64))
	if hasData, _ := before["hasData"].(bool); !hasData {
		t.Fatalf("checkpoint over zero confirmed families: hasData = %v, want true (a non-NULL empty document, not NULL)", before["hasData"])
	}

	req := jsonRequest(t, http.MethodPost, rollbackURL(wsID, beforeCID),
		map[string]any{"restoreData": true, "confirmSlug": slug}, cookie, csrfToken)
	rec := ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback to a zero-family checkpoint: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body := checkpointTestDecode(t, rec)
	if dr, ok := body["dataRestored"].(bool); !ok || dr {
		t.Errorf("dataRestored over a document carrying no families = %v, want false", body["dataRestored"])
	}

	// dataRestored:true — confirm the family, clear its rows straight
	// back to zero (reset-data mode "clear"; the resources row itself
	// SURVIVES), then checkpoint. The document now carries the family
	// with an EMPTY Rows slice, and the LIVE relation is empty too: both
	// sides are empty, and the family is still RESOLVED (its resources
	// row lives), so the restore still RUNS.
	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	clearRec := ts.do(jsonRequest(t, http.MethodPost, fmt.Sprintf("http://mocker.local/api/workspaces/%d/reset-data", wsID),
		map[string]string{"mode": "clear", "confirmSlug": slug}, cookie, csrfToken))
	if clearRec.Code != http.StatusOK {
		t.Fatalf("clear: status = %d, want 200; body = %s", clearRec.Code, clearRec.Body.String())
	}
	if got := checkpointTestEntityCount(t, ts, wsID); got != 0 {
		t.Fatalf("entity rows after clear = %d, want 0 (test precondition)", got)
	}

	empty := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "confirmed but empty", http.StatusCreated)
	emptyCID := int64(empty["id"].(float64))

	req = jsonRequest(t, http.MethodPost, rollbackURL(wsID, emptyCID),
		map[string]any{"restoreData": true, "confirmSlug": slug}, cookie, csrfToken)
	rec = ts.do(req)
	if rec.Code != http.StatusOK {
		t.Fatalf("rollback to the confirmed-but-empty checkpoint: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}
	body = checkpointTestDecode(t, rec)
	if dr, ok := body["dataRestored"].(bool); !ok || !dr {
		t.Errorf("dataRestored over a RESOLVED family whose stored and live relations are both empty = %v, want true (it RAN, D7's own boundary)", body["dataRestored"])
	}
}

// TestCheckpoints_listHasDataMatchesCreateHasData is D11 property 8's
// server half: hasData is produced by BOTH server sites that emit a
// checkpointSummaryView — the list query and the 201 of
// POST .../checkpoints — through the SAME [newCheckpointSummaryView], so a
// struct literal that forgets to project the field goes red on EITHER site,
// not just the one a careless fix happened to touch.
func TestCheckpoints_listHasDataMatchesCreateHasData(t *testing.T) {
	t.Parallel()
	ts := newTestServer(t)
	cookie, csrfToken := ts.login(t, "Alex")
	specID := ts.importDerivationSpec(t, cookie, csrfToken)
	wsID := ts.createWorkspaceWithSpec(t, cookie, csrfToken, "Res Demo", specID)

	decideTarget := fmt.Sprintf("http://mocker.local/api/workspaces/%d/resource-decisions", wsID)
	confirmReq := jsonRequest(t, http.MethodPost, decideTarget,
		map[string]string{"routeFamily": testspec.FamilyWidgets, "state": "confirmed"}, cookie, csrfToken)
	if rec := ts.do(confirmReq); rec.Code != http.StatusOK {
		t.Fatalf("confirm: status = %d, want 200; body = %s", rec.Code, rec.Body.String())
	}

	created := checkpointTestCreate(t, ts, cookie, csrfToken, wsID, "populated", http.StatusCreated)
	cid := int64(created["id"].(float64))
	createHasData, _ := created["hasData"].(bool)
	if !createHasData {
		t.Fatalf("201 hasData over a confirmed, populated family = %v, want true", created["hasData"])
	}

	listRec := checkpointTestGet(t, ts, cookie, checkpointsURL(wsID))
	list := checkpointTestDecode(t, listRec)
	entries, _ := list["checkpoints"].([]any)
	var listHasData bool
	var found bool
	for _, raw := range entries {
		entry, _ := raw.(map[string]any)
		if int64(entry["id"].(float64)) == cid {
			listHasData, _ = entry["hasData"].(bool)
			found = true
		}
	}
	if !found {
		t.Fatalf("checkpoint %d not found in the list", cid)
	}
	if listHasData != createHasData {
		t.Errorf("list hasData = %v, create hasData = %v — both server sites must agree", listHasData, createHasData)
	}
}
