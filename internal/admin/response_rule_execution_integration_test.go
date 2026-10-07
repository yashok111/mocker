package admin

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/responserules"
	"github.com/yashok111/mocker/internal/workspaces"
)

const executionHTTPBody = `{"n":1.0,"exp":1e0,"large":9007199254740993,"example":true,"nullable":true}`

func executionHTTPDocument(t *testing.T) string {
	t.Helper()
	var root map[string]json.RawMessage
	if err := json.Unmarshal([]byte(responseRuleTestDocument), &root); err != nil {
		t.Fatal(err)
	}
	var env responserules.Envelope
	if err := json.Unmarshal(root[responserules.Extension], &env); err != nil {
		t.Fatal(err)
	}
	env.Rules[0].Nodes[1] = responserules.Node{ID: "f", Type: "response", Name: "Response", X: 200, Response: &responserules.Response{Status: 201, MediaType: "application/json", Headers: []responserules.Field{}, BodyJSON: new(executionHTTPBody)}}
	var err error
	root[responserules.Extension], err = json.Marshal(env)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(root)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func executionTestPlane(s *Server) *mockplane.Plane {
	p := mockplane.New(s.cfg, s.ws, s.specsRepo, s.log)
	p.SetOverrides(s.overridesRepo)
	p.SetScenarios(s.scenariosRepo)
	p.SetCustomEndpoints(s.customepRepo)
	return p
}

func assertExecutionHTTP(t *testing.T, s *Server, p *mockplane.Plane, id int64, status int, body string) {
	t.Helper()
	ws, err := s.ws.ByID(t.Context(), id)
	if err != nil {
		t.Fatal(err)
	}
	w := httptest.NewRecorder()
	p.ServeWorkspace(w, httptest.NewRequest(http.MethodGet, "http://"+ws.Slug+".mock.local/orders", nil).WithContext(t.Context()), ws)
	if w.Code != status || (body != "" && w.Body.String() != body) {
		t.Fatalf("workspace %d: %d %s; want %d %s", id, w.Code, w.Body.String(), status, body)
	}
}

func TestResponseRuleExecutionDraftPublishRestore(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	d, err := s.designsRepo.Create(ctx, apidesign.CreateInput{Name: "Execution", Document: executionHTTPDocument(t), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	p := executionTestPlane(s)
	// Warm both runtime entries before activation, including the empty published mock.
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 200, "")
	assertExecutionHTTP(t, s, p, d.Design.PublishedWorkspaceID, 404, "")
	applied, err := s.designsRepo.EditResponseRuleExecution(ctx, d.Design.ID, 1, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 201, executionHTTPBody)
	assertExecutionHTTP(t, s, p, d.Design.PublishedWorkspaceID, 404, "")
	review, err := s.designsRepo.RequestReview(ctx, d.Design.ID, applied.Design.Version, "execute graph", "ui")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.designsRepo.Publish(ctx, d.Design.ID, review.ID, applied.Design.Version, "mcp"); !errors.Is(err, apidesign.ErrForbidden) {
		t.Fatalf("MCP publication bypass: %v", err)
	}
	if _, err := s.designsRepo.Publish(ctx, d.Design.ID, review.ID, applied.Design.Version, "ui"); err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, d.Design.PublishedWorkspaceID, 201, executionHTTPBody)
	list, err := s.designsRepo.ResponseRules(ctx, d.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	rule := list.Rules[0]
	rule.Nodes[1].Response.BodyJSON = new(`{"changed":true}`)
	edited, err := s.designsRepo.EditResponseRule(ctx, d.Design.ID, list.Version, "ui", "r", "save", &rule, nil)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 201, executionHTTPBody)
	newReview, err := s.designsRepo.RequestReview(ctx, d.Design.ID, edited.Design.Version, "pending before reapply", "ui")
	if err != nil {
		t.Fatal(err)
	}
	reapplied, err := s.designsRepo.EditResponseRuleExecution(ctx, d.Design.ID, edited.Design.Version, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.designsRepo.Publish(ctx, d.Design.ID, newReview.ID, reapplied.Design.Version, "ui"); !errors.Is(err, apidesign.ErrConflict) {
		t.Fatalf("old review published new graph: %v", err)
	}
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 201, `{"changed":true}`)
	assertExecutionHTTP(t, s, p, d.Design.PublishedWorkspaceID, 201, executionHTTPBody)
	removed, err := s.designsRepo.EditResponseRuleExecution(ctx, d.Design.ID, reapplied.Design.Version, "ui", "r", false)
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 200, "")
	if _, err := s.designsRepo.Restore(ctx, d.Design.ID, applied.Draft.ID, removed.Design.Version, "restore graph", "ui"); err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, d.Design.DraftWorkspaceID, 201, executionHTTPBody)
	assertExecutionHTTP(t, s, p, d.Design.PublishedWorkspaceID, 201, executionHTTPBody)
	// Both workspaces remain protected by the ordinary mutation guard.
	for _, id := range []int64{d.Design.DraftWorkspaceID, d.Design.PublishedWorkspaceID} {
		status, out, err := s.CallAsMCP(ctx, loopbackTestSrc(), "PUT", fmt.Sprintf("/api/workspaces/%d/operations/GET%%20%%2Forders", id), []byte(`{"editVersion":0,"overrideOn":true}`))
		if err != nil || status != 409 || !strings.Contains(string(out), "managed_workspace") {
			t.Fatalf("managed guard: %d %s %v", status, out, err)
		}
	}
}

func TestResponseRuleExecutionTransferAndWorkspaceHistory(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	d, err := s.designsRepo.Create(ctx, apidesign.CreateInput{Name: "Execution", Document: executionHTTPDocument(t), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	applied, err := s.designsRepo.EditResponseRuleExecution(ctx, d.Design.ID, 1, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	status, exported, err := s.CallAsMCP(ctx, loopbackTestSrc(), "GET", fmt.Sprintf("/api/workspaces/%d/export?includeSpec=true", d.Design.DraftWorkspaceID), nil)
	if err != nil || status != 200 || !strings.Contains(string(exported), responserules.ExecutionExtension) {
		t.Fatalf("export: %d %s %v", status, exported, err)
	}
	other := loopbackTestServer(t, nil)
	importBody, err := json.Marshal(map[string]any{"name": "Imported graph", "bundle": json.RawMessage(exported)})
	if err != nil {
		t.Fatal(err)
	}
	status, out, err := other.CallAsMCP(ctx, loopbackTestSrc(), "POST", "/api/workspaces/import", importBody)
	if err != nil || status != 201 {
		t.Fatalf("fresh installation import: %d %s %v", status, out, err)
	}
	var imported struct {
		Workspace struct {
			ID int64 `json:"id"`
		} `json:"workspace"`
		SpecCreated bool `json:"specCreated"`
	}
	if err := json.Unmarshal(out, &imported); err != nil {
		t.Fatal(err)
	}
	if !imported.SpecCreated {
		t.Fatal("test did not exercise inline spec import")
	}
	assertExecutionHTTP(t, other, executionTestPlane(other), imported.Workspace.ID, 201, executionHTTPBody)
	status, out, err = s.CallAsMCP(ctx, loopbackTestSrc(), "POST", fmt.Sprintf("/api/workspaces/%d/fork", d.Design.DraftWorkspaceID), []byte(`{"name":"Forked graph","includeData":false}`))
	if err != nil || status != 201 {
		t.Fatalf("fork: %d %s %v", status, out, err)
	}
	var fork struct {
		ID int64 `json:"id"`
	}
	if err := json.Unmarshal(out, &fork); err != nil {
		t.Fatal(err)
	}
	p := executionTestPlane(s)
	assertExecutionHTTP(t, s, p, fork.ID, 201, executionHTTPBody)
	cp, err := s.checkpointsRepo.Create(ctx, fork.ID, "before spec change", 1)
	if err != nil {
		t.Fatal(err)
	}
	scenario, err := s.scenariosRepo.CreateFromCurrentState(ctx, fork.ID, "before spec change")
	if err != nil {
		t.Fatal(err)
	}
	removed, err := s.designsRepo.EditResponseRuleExecution(ctx, d.Design.ID, applied.Design.Version, "ui", "r", false)
	if err != nil {
		t.Fatal(err)
	}
	// An API edit cannot mutate the fork's immutable spec.
	assertExecutionHTTP(t, s, p, fork.ID, 201, executionHTTPBody)
	var newSpecID int64
	if err := s.db.R.QueryRowContext(ctx, "SELECT spec_id FROM api_design_revisions WHERE id=?", removed.Draft.ID).Scan(&newSpecID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.ws.Update(ctx, fork.ID, func(ws *workspaces.Workspace) error { ws.SpecID = &newSpecID; return nil }); err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, fork.ID, 200, "")
	if _, err := s.checkpointsRepo.Rollback(ctx, fork.ID, cp.ID, 1, false, ""); err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, fork.ID, 200, "")
	if _, err := s.scenariosRepo.SetActive(ctx, fork.ID, &scenario.ID); err != nil {
		t.Fatal(err)
	}
	assertExecutionHTTP(t, s, p, fork.ID, 200, "")
}

func TestResponseRuleExecutionImportRoutesRejectInvalidCopies(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	ctx := t.Context()
	invalid := strings.TrimSuffix(responseRuleTestDocument, "}") + `,"x-mocker-response-rules-execution":{"formatVersion":1,"rules":[{"id":"broken","name":"Broken","nodes":[],"edges":[]}]}}`
	body, err := json.Marshal(map[string]string{"name": "Bad graph", "source": "upload", "document": invalid})
	if err != nil {
		t.Fatal(err)
	}
	before := responseRuleRows(t, s)
	status, out, err := s.CallAsMCP(ctx, loopbackTestSrc(), "POST", "/api/specs", body)
	if err != nil || status != 400 {
		t.Fatalf("invalid direct import: %d %s %v", status, out, err)
	}
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) {
		t.Fatal("invalid direct import changed storage")
	}
	ws, err := s.ws.Create(ctx, workspaces.CreateInput{Name: "For export"})
	if err != nil {
		t.Fatal(err)
	}
	exported, err := s.checkpointsRepo.Export(ctx, ws.ID, false, false)
	if err != nil {
		t.Fatal(err)
	}
	exported.Spec.Hash = strings.Repeat("a", 64)
	exported.Spec.Inline, err = jsonx.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	body, err = json.Marshal(map[string]any{"name": "Bad bundle graph", "bundle": exported})
	if err != nil {
		t.Fatal(err)
	}
	before = responseRuleRows(t, s)
	status, out, err = s.CallAsMCP(ctx, loopbackTestSrc(), "POST", "/api/workspaces/import", body)
	if err != nil || status != 400 {
		t.Fatalf("invalid bundle import: %d %s %v", status, out, err)
	}
	if !reflect.DeepEqual(before, responseRuleRows(t, s)) {
		t.Fatal("invalid bundle import changed storage")
	}
}
