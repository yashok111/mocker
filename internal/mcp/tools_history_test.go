package mcp

import (
	"net/http"
	"testing"
)

// historyWorkspaceFixture is the confirmation GET's body every D6 tool test
// below queues first — the same fixture shape TestDeleteScenario_* in
// tools_endpoints_test.go uses for the identical purpose.
const historyWorkspaceFixture = `{"id":7,"slug":"orders-api","name":"Orders"}`

// ---- create_checkpoint ----

const checkpointSummaryFixture = `{
  "id": 12,
  "kind": "manual",
  "label": "before big edit",
  "createdAt": 1700000000,
  "createdBy": 3
}`

func TestCreateCheckpoint_happyPath(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusCreated, body: checkpointSummaryFixture},
	}}
	lb := newLoopback(fc)

	_, out, err := handleCreateCheckpoint(lb)(opsTestCtx(), nil, CreateCheckpointInput{WorkspaceID: 7, Label: "before big edit"})
	if err != nil {
		t.Fatalf("handleCreateCheckpoint: %v", err)
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodPost || fc.calls[0].path != "/api/workspaces/7/checkpoints" {
		t.Errorf("call = %s %s, want POST /api/workspaces/7/checkpoints", fc.calls[0].method, fc.calls[0].path)
	}
	sent := decodeBody(t, fc.calls[0].body)
	if sent["label"] != "before big edit" {
		t.Errorf("sent label = %v, want %q", sent["label"], "before big edit")
	}
	c := out.Checkpoint
	if c.ID != 12 || c.Kind != "manual" || c.Label != "before big edit" || c.CreatedAt != 1700000000 || c.CreatedBy == nil || *c.CreatedBy != 3 {
		t.Errorf("checkpoint = %+v, want id=12 kind=manual label=%q createdAt=1700000000 createdBy=3", c, "before big edit")
	}
}

func TestCreateCheckpoint_invalidLabelIsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusBadRequest, body: `{"error":{"code":"bad_request","message":"label must not be empty"}}`},
	}}
	_, _, err := handleCreateCheckpoint(newLoopback(fc))(opsTestCtx(), nil, CreateCheckpointInput{WorkspaceID: 7, Label: ""})
	if err == nil {
		t.Fatal("handleCreateCheckpoint returned no error for an empty label")
	}
}

// ---- delete_checkpoint ----

func TestDeleteCheckpoint_confirmedDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusNoContent, body: ""},
	}}
	lb := newLoopback(fc)

	_, out, err := handleDeleteCheckpoint(lb)(opsTestCtx(), nil, DeleteCheckpointInput{
		WorkspaceID: 7, CheckpointID: 12, ConfirmSlug: "orders-api",
	})
	if err != nil {
		t.Fatalf("handleDeleteCheckpoint: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then DELETE)", len(fc.calls))
	}
	if fc.calls[0].method != http.MethodGet || fc.calls[0].path != "/api/workspaces/7" {
		t.Errorf("first call = %s %s, want GET /api/workspaces/7", fc.calls[0].method, fc.calls[0].path)
	}
	wantPath := "/api/workspaces/7/checkpoints/12"
	if fc.calls[1].method != http.MethodDelete || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want DELETE %s", fc.calls[1].method, fc.calls[1].path, wantPath)
	}
	if !out.Deleted || out.CheckpointID != 12 {
		t.Errorf("output = %+v, want deleted=true checkpointId=12", out)
	}
}

func TestDeleteCheckpoint_mismatchedSlugNeverDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
	}}
	_, _, err := handleDeleteCheckpoint(newLoopback(fc))(opsTestCtx(), nil, DeleteCheckpointInput{
		WorkspaceID: 7, CheckpointID: 12, ConfirmSlug: "wrong-slug",
	})
	if err == nil {
		t.Fatal("handleDeleteCheckpoint returned no error for a mismatched confirmSlug")
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (confirm GET only — the DELETE must never be issued)", len(fc.calls))
	}
}

func TestDeleteCheckpoint_emptySlugNeverCallsAdminPlane(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: nil}
	_, _, err := handleDeleteCheckpoint(newLoopback(fc))(opsTestCtx(), nil, DeleteCheckpointInput{WorkspaceID: 7, CheckpointID: 12})
	if err == nil {
		t.Fatal("handleDeleteCheckpoint returned no error for an empty confirmSlug")
	}
	if len(fc.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(fc.calls))
	}
}

// ---- rollback_workspace ----

func TestRollbackWorkspace_confirmedRollsBack(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusOK, body: `{"revision": 9, "scenarioActive": true, "dataRestored": false}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleRollbackWorkspace(lb)(opsTestCtx(), nil, RollbackWorkspaceInput{
		WorkspaceID: 7, CheckpointID: 12, ConfirmSlug: "orders-api",
	})
	if err != nil {
		t.Fatalf("handleRollbackWorkspace: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then rollback POST)", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/rollback/12"
	if fc.calls[1].method != http.MethodPost || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want POST %s", fc.calls[1].method, fc.calls[1].path, wantPath)
	}
	// With RestoreData left at its zero value the tool still sends no body
	// at all — nil, not "{}" — which is now the sole reason the admin
	// route's io.EOF swallow exists (D9): the screen itself always sends
	// {restoreData: false} since P3d (D8), so this zero-byte POST is the
	// MCP caller's alone.
	if fc.calls[1].body != "" {
		t.Errorf("rollback body = %q, want empty (RestoreData unset)", fc.calls[1].body)
	}
	if out.Revision != 9 || !out.ScenarioActive || out.DataRestored {
		t.Errorf("output = %+v, want revision=9 scenarioActive=true dataRestored=false", out)
	}
}

func TestRollbackWorkspace_restoreDataForwardsBodyAndDecodesDataRestored(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusOK, body: `{"revision": 10, "scenarioActive": false, "dataRestored": true}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleRollbackWorkspace(lb)(opsTestCtx(), nil, RollbackWorkspaceInput{
		WorkspaceID: 7, CheckpointID: 12, ConfirmSlug: "orders-api", RestoreData: true,
	})
	if err != nil {
		t.Fatalf("handleRollbackWorkspace: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then rollback POST)", len(fc.calls))
	}
	// RestoreData:true must forward {restoreData, confirmSlug} on the wire
	// (D9/D7) — the tool's own confirmWorkspaceSlug call already refused a
	// mismatch before this request was built, but the route's own check is
	// what makes the refusal true of the workspace at write time.
	sent := decodeBody(t, fc.calls[1].body)
	if sent["restoreData"] != true {
		t.Errorf("sent restoreData = %v, want true", sent["restoreData"])
	}
	if sent["confirmSlug"] != "orders-api" {
		t.Errorf("sent confirmSlug = %v, want %q", sent["confirmSlug"], "orders-api")
	}
	if out.Revision != 10 || out.ScenarioActive || !out.DataRestored {
		t.Errorf("output = %+v, want revision=10 scenarioActive=false dataRestored=true", out)
	}
}

func TestRollbackWorkspace_mismatchedSlugNeverRollsBack(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
	}}
	_, _, err := handleRollbackWorkspace(newLoopback(fc))(opsTestCtx(), nil, RollbackWorkspaceInput{
		WorkspaceID: 7, CheckpointID: 12, ConfirmSlug: "wrong-slug",
	})
	if err == nil {
		t.Fatal("handleRollbackWorkspace returned no error for a mismatched confirmSlug")
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (confirm GET only — the rollback POST must never be issued)", len(fc.calls))
	}
}

func TestRollbackWorkspace_404IsToolError(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusNotFound, body: `{"error":{"code":"not_found","message":"checkpoint 999 in workspace 7"}}`},
	}}
	_, _, err := handleRollbackWorkspace(newLoopback(fc))(opsTestCtx(), nil, RollbackWorkspaceInput{
		WorkspaceID: 7, CheckpointID: 999, ConfirmSlug: "orders-api",
	})
	if err == nil {
		t.Fatal("handleRollbackWorkspace returned no error for a 404")
	}
}

// ---- reset_overrides ----

func TestResetOverrides_confirmedResets(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusOK, body: `{"revision": 5, "scenarioActive": false, "changed": true}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleResetOverrides(lb)(opsTestCtx(), nil, ResetOverridesInput{WorkspaceID: 7, ConfirmSlug: "orders-api"})
	if err != nil {
		t.Fatalf("handleResetOverrides: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then reset POST)", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/reset-overrides"
	if fc.calls[1].method != http.MethodPost || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want POST %s", fc.calls[1].method, fc.calls[1].path, wantPath)
	}
	if out.Revision != 5 || out.ScenarioActive || !out.Changed {
		t.Errorf("output = %+v, want revision=5 scenarioActive=false changed=true", out)
	}
}

// TestResetOverrides_noopReportsUnchanged proves the C9 no-op signal
// (changed:false, meaning no pre-destructive checkpoint was written) is
// carried through to the caller rather than collapsed into a bare success.
func TestResetOverrides_noopReportsUnchanged(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusOK, body: `{"revision": 5, "scenarioActive": false, "changed": false}`},
	}}
	_, out, err := handleResetOverrides(newLoopback(fc))(opsTestCtx(), nil, ResetOverridesInput{WorkspaceID: 7, ConfirmSlug: "orders-api"})
	if err != nil {
		t.Fatalf("handleResetOverrides: %v", err)
	}
	if out.Changed {
		t.Errorf("output.Changed = true, want false for a reset that deleted nothing")
	}
}

func TestResetOverrides_emptySlugNeverCallsAdminPlane(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: nil}
	_, _, err := handleResetOverrides(newLoopback(fc))(opsTestCtx(), nil, ResetOverridesInput{WorkspaceID: 7})
	if err == nil {
		t.Fatal("handleResetOverrides returned no error for an empty confirmSlug")
	}
	if len(fc.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(fc.calls))
	}
}

// ---- delete_workspace ----

func TestDeleteWorkspace_confirmedDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusNoContent, body: ""},
	}}
	lb := newLoopback(fc)

	_, out, err := handleDeleteWorkspace(lb)(opsTestCtx(), nil, DeleteWorkspaceInput{WorkspaceID: 7, ConfirmSlug: "orders-api"})
	if err != nil {
		t.Fatalf("handleDeleteWorkspace: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then DELETE)", len(fc.calls))
	}
	if fc.calls[1].method != http.MethodDelete || fc.calls[1].path != "/api/workspaces/7" {
		t.Errorf("second call = %s %s, want DELETE /api/workspaces/7", fc.calls[1].method, fc.calls[1].path)
	}
	if !out.Deleted || out.WorkspaceID != 7 {
		t.Errorf("output = %+v, want deleted=true workspaceId=7", out)
	}
}

func TestDeleteWorkspace_mismatchedSlugNeverDeletes(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
	}}
	_, _, err := handleDeleteWorkspace(newLoopback(fc))(opsTestCtx(), nil, DeleteWorkspaceInput{WorkspaceID: 7, ConfirmSlug: "some-other-workspace"})
	if err == nil {
		t.Fatal("handleDeleteWorkspace returned no error for a mismatched confirmSlug")
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (confirm GET only — the DELETE must never be issued)", len(fc.calls))
	}
}

func TestDeleteWorkspace_emptySlugNeverCallsAdminPlane(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: nil}
	_, _, err := handleDeleteWorkspace(newLoopback(fc))(opsTestCtx(), nil, DeleteWorkspaceInput{WorkspaceID: 7})
	if err == nil {
		t.Fatal("handleDeleteWorkspace returned no error for an empty confirmSlug")
	}
	if len(fc.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(fc.calls))
	}
}

// ---- clear_traffic ----

func TestClearTraffic_confirmedClears(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
		{status: http.StatusOK, body: `{"deleted": 431}`},
	}}
	lb := newLoopback(fc)

	_, out, err := handleClearTraffic(lb)(opsTestCtx(), nil, ClearTrafficInput{WorkspaceID: 7, ConfirmSlug: "orders-api"})
	if err != nil {
		t.Fatalf("handleClearTraffic: %v", err)
	}
	if len(fc.calls) != 2 {
		t.Fatalf("calls = %d, want 2 (confirm GET, then DELETE)", len(fc.calls))
	}
	wantPath := "/api/workspaces/7/traffic"
	if fc.calls[1].method != http.MethodDelete || fc.calls[1].path != wantPath {
		t.Errorf("second call = %s %s, want DELETE %s", fc.calls[1].method, fc.calls[1].path, wantPath)
	}
	if out.Deleted != 431 {
		t.Errorf("output.Deleted = %d, want 431", out.Deleted)
	}
}

func TestClearTraffic_mismatchedSlugNeverClears(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: []cannedResponse{
		{status: http.StatusOK, body: historyWorkspaceFixture},
	}}
	_, _, err := handleClearTraffic(newLoopback(fc))(opsTestCtx(), nil, ClearTrafficInput{WorkspaceID: 7, ConfirmSlug: "wrong-slug"})
	if err == nil {
		t.Fatal("handleClearTraffic returned no error for a mismatched confirmSlug")
	}
	if len(fc.calls) != 1 {
		t.Fatalf("calls = %d, want 1 (confirm GET only — the DELETE must never be issued)", len(fc.calls))
	}
}

func TestClearTraffic_emptySlugNeverCallsAdminPlane(t *testing.T) {
	t.Parallel()
	fc := &scriptedCaller{t: t, responses: nil}
	_, _, err := handleClearTraffic(newLoopback(fc))(opsTestCtx(), nil, ClearTrafficInput{WorkspaceID: 7})
	if err == nil {
		t.Fatal("handleClearTraffic returned no error for an empty confirmSlug")
	}
	if len(fc.calls) != 0 {
		t.Fatalf("calls = %d, want 0", len(fc.calls))
	}
}
