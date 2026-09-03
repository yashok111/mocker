package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// A11's tool tests: the two write siblings render the family and the key
// as path segments (url.PathEscape'd, substituted raw by toolPath) and
// carry the scope in the body.

func callTool(t *testing.T, calls Caller, name, args string) (json.RawMessage, string) {
	t.Helper()
	h := New(calls, testKey, testConfig(), nil).Handler()
	rec := doMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+args+`}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			IsError           bool                    `json:"isError"`
			Content           []struct{ Text string } `json:"content"`
			StructuredContent json.RawMessage         `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Result.IsError {
		if len(env.Result.Content) > 0 {
			return nil, env.Result.Content[0].Text
		}
		return nil, "error with no content"
	}
	if len(env.Result.StructuredContent) == 0 {
		t.Fatalf("no structuredContent; body=%s", rec.Body.String())
	}
	return env.Result.StructuredContent, ""
}

func TestSetResourceEntity_rendersPathAndBody(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusOK,
		body:   []byte(`{"row":{"id":9,"entityKey":"42","scopeKey":"7","baseScopeKey":"","data":{"id":42,"status":"blocked"},"createdAt":"2026-09-02T10:00:00Z","updatedAt":"2026-09-02T10:00:00Z"},"created":false}`),
	}
	raw, errMsg := callTool(t, calls, "set_resource_entity",
		`{"workspaceId":3,"routeFamily":"/orgs/{orgId}/users","entityKey":"42","scopeKey":"7","data":{"status":"blocked"}}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "PUT" || calls.path != "/api/workspaces/3/resources/%2Forgs%2F%7BorgId%7D%2Fusers/entities/42" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var sent map[string]any
	if err := json.Unmarshal(calls.sent, &sent); err != nil {
		t.Fatal(err)
	}
	if sent["scopeKey"] != "7" || sent["baseScopeKey"] != "" || sent["data"].(map[string]any)["status"] != "blocked" {
		t.Errorf("sent = %v", sent)
	}
	var out SetResourceEntityOutput
	if err := json.Unmarshal(raw, &out); err != nil || out.Row.EntityKey != "42" || out.Created {
		t.Errorf("out = %+v err=%v", out, err)
	}
}

func TestSetResourceEntity_refusesWithoutDataOrKey(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{}`)}
	_, errMsg := callTool(t, calls, "set_resource_entity", `{"workspaceId":3,"routeFamily":"/w","entityKey":"1"}`)
	// The SDK validates required arguments before the handler runs, so the
	// refusal is the schema's own wording; what matters is that no call
	// reached the admin plane.
	if !strings.Contains(errMsg, `"data"`) || calls.method != "" {
		t.Errorf("errMsg=%q called=%q", errMsg, calls.method)
	}
	_, errMsg = callTool(t, calls, "set_resource_entity", `{"workspaceId":3,"routeFamily":"/w","data":{}}`)
	if !strings.Contains(errMsg, `"entityKey"`) || calls.method != "" {
		t.Errorf("errMsg=%q called=%q", errMsg, calls.method)
	}
}

func TestDeleteResourceEntity_deletesAndSurfacesNotFound(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusNoContent, body: nil}
	raw, errMsg := callTool(t, calls, "delete_resource_entity", `{"workspaceId":3,"routeFamily":"/widgets","entityKey":"5"}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "DELETE" || calls.path != "/api/workspaces/3/resources/%2Fwidgets/entities/5" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var out DeleteResourceEntityOutput
	if err := json.Unmarshal(raw, &out); err != nil || !out.Deleted || out.EntityKey != "5" {
		t.Errorf("out = %+v err=%v", out, err)
	}

	gone := &recordingCaller{status: http.StatusNotFound, body: []byte(`{"error":{"code":"entity_not_found","message":"no entity with that key in that scope"}}`)}
	_, errMsg = callTool(t, gone, "delete_resource_entity", `{"workspaceId":3,"routeFamily":"/widgets","entityKey":"5"}`)
	if !strings.Contains(errMsg, "no entity with that key") {
		t.Errorf("errMsg=%q", errMsg)
	}
}
