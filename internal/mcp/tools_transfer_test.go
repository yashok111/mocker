package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// P4b's three tools over a recording caller: the path each one builds, the
// body it sends, and the projection of the route's answer.

const workspaceViewJSON = `{"id":9,"slug":"copy","name":"Src (копия)","url":"http://copy.mock.local","specId":3,` +
	`"revision":1,"forkedFrom":4,"settings":{"basePath":"/v1"}}`

func TestExportWorkspace_buildsTheQueryAndReturnsTheDocumentVerbatim(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"mockerBundle":4,"workspace":{"name":"Src"},"data":{"mockerData":2,"families":[]}}`)}
	out, errMsg := callTool(t, calls, "export_workspace", `{"workspaceId":4,"includeData":true,"includeSpec":true}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "GET" || calls.path != "/api/workspaces/4/export?includeData=true&includeSpec=true" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var got struct {
		Document map[string]any `json:"document"`
	}
	if err := json.Unmarshal(out, &got); err != nil || got.Document["mockerBundle"] != float64(4) || got.Document["data"] == nil {
		t.Errorf("out = %s (err %v); want the document under document", out, err)
	}

	calls = &recordingCaller{status: http.StatusOK, body: []byte(`{"mockerBundle":4}`)}
	if _, errMsg := callTool(t, calls, "export_workspace", `{"workspaceId":4}`); errMsg != "" || calls.path != "/api/workspaces/4/export" {
		t.Errorf("plain export: err %q path %s; want no query at all", errMsg, calls.path)
	}
}

func TestImportWorkspace_postsTheBundleAndProjectsTheView(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusCreated,
		body:   []byte(`{"workspace":` + workspaceViewJSON + `,"specId":3,"specCreated":true,"entitiesRestored":2}`),
	}
	out, errMsg := callTool(t, calls, "import_workspace",
		`{"bundle":{"mockerBundle":4,"workspace":{"name":"Src","settings":{}}},"name":"Imported","slug":"imp","specId":3}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "POST" || calls.path != "/api/workspaces/import" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var sent struct {
		Bundle map[string]any `json:"bundle"`
		Name   string         `json:"name"`
		Slug   string         `json:"slug"`
		SpecID *int64         `json:"specId"`
	}
	if err := json.Unmarshal(calls.sent, &sent); err != nil || sent.Bundle["mockerBundle"] != float64(4) || sent.Name != "Imported" || sent.Slug != "imp" || sent.SpecID == nil || *sent.SpecID != 3 {
		t.Errorf("sent = %s (err %v)", calls.sent, err)
	}
	var got ImportWorkspaceOutput
	if err := json.Unmarshal(out, &got); err != nil || got.Workspace.ID != 9 || got.Workspace.BasePath != "/v1" || !got.SpecCreated || got.EntitiesRestored != 2 || got.SpecID == nil || *got.SpecID != 3 {
		t.Errorf("out = %s (err %v)", out, err)
	}

	// A 409 spec_not_found reaches the agent with the route's own sentence.
	calls = &recordingCaller{status: http.StatusConflict, body: []byte(`{"error":{"code":"spec_not_found","message":"the document names a spec this installation does not hold","details":{"hash":"abc"}}}`)}
	if _, errMsg := callTool(t, calls, "import_workspace", `{"bundle":{"mockerBundle":4}}`); !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, "does not hold") {
		t.Errorf("409 error = %q; want the status and the message", errMsg)
	}
	// No bundle: refused before any call.
	calls = &recordingCaller{status: http.StatusCreated, body: []byte(`{}`)}
	if _, errMsg := callTool(t, calls, "import_workspace", `{"bundle":{}}`); errMsg == "" || calls.method != "" {
		t.Errorf("empty bundle: err %q, called %q; want a refusal without a call", errMsg, calls.method)
	}
}

func TestForkWorkspace_postsTheOptionsAndProjectsTheCopy(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusCreated, body: []byte(workspaceViewJSON)}
	out, errMsg := callTool(t, calls, "fork_workspace", `{"workspaceId":4,"name":"Src (копия)","includeData":false}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "POST" || calls.path != "/api/workspaces/4/fork" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var sent map[string]any
	if err := json.Unmarshal(calls.sent, &sent); err != nil || sent["includeData"] != false || sent["name"] != "Src (копия)" {
		t.Errorf("sent = %s (err %v)", calls.sent, err)
	}
	var got ForkWorkspaceOutput
	if err := json.Unmarshal(out, &got); err != nil || got.Workspace.ID != 9 || got.Workspace.Slug != "copy" || got.Workspace.URL != "http://copy.mock.local" {
		t.Errorf("out = %s (err %v)", out, err)
	}

	// includeData omitted is not sent as false: the route's default (true)
	// must stay the route's.
	calls = &recordingCaller{status: http.StatusCreated, body: []byte(workspaceViewJSON)}
	if _, errMsg := callTool(t, calls, "fork_workspace", `{"workspaceId":4}`); errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	sent = nil
	_ = json.Unmarshal(calls.sent, &sent)
	if _, present := sent["includeData"]; present {
		t.Errorf("sent includeData when the input omitted it: %s", calls.sent)
	}
}
