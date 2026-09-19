package mcp

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const apiDesignFixture = `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"operationId":"listOrders","responses":{"200":{"description":"Orders","content":{"application/json":{"schema":{"type":"array","items":{"type":"string"}}}}}}}}},"x-kept":{"owner":"analyst"}}`

func designToolObject(t *testing.T, calls Caller, name string, args any) map[string]any {
	t.Helper()
	encoded, err := jsonx.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	raw, errMsg := callTool(t, calls, name, string(encoded))
	if errMsg != "" {
		t.Fatalf("%s: %s", name, errMsg)
	}
	var out map[string]any
	if err := jsonx.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestAPIDesignMCPWorkflowUsesPersistentAdminState(t *testing.T) {
	t.Parallel()
	cfg := resourcesTestConfig(t)
	srv, _ := newResourcesTestServer(t, cfg)
	created := designToolObject(t, srv, "create_api_design", map[string]any{"name": "Orders", "document": apiDesignFixture})
	design := created["design"].(map[string]any)
	id := design["id"]
	if design["version"] != float64(1) || created["published"] != nil {
		t.Fatalf("new project was already published or had wrong version: %v", created)
	}
	set := designToolObject(t, srv, "create_api_design_change_set", map[string]any{"designId": id, "expectedVersion": 1, "title": "Describe orders"})
	changed := strings.Replace(apiDesignFixture, `"operationId":"listOrders"`, `"operationId":"listOrders","summary":"List orders"`, 1)
	saved := designToolObject(t, srv, "save_api_design_draft", map[string]any{"designId": id, "expectedVersion": 1, "document": changed, "summary": "Add description", "changeSetId": set["id"]})
	draft := saved["draft"].(map[string]any)
	if draft["version"] != float64(2) || draft["source"] != "mcp" || draft["changeSetId"] != set["id"] || !strings.Contains(draft["document"].(string), `"x-kept"`) {
		t.Fatalf("saved revision lost authorship, version or unrelated fields: %v", draft)
	}
	diff := designToolObject(t, srv, "get_api_design_diff", map[string]any{"designId": id})
	changes := diff["changes"].([]any)
	if len(changes) != 1 || changes[0].(map[string]any)["pointer"] != "/paths/~1orders/get/summary" {
		t.Fatalf("unexpected structural changes: %v", changes)
	}
	review := designToolObject(t, srv, "request_api_design_review", map[string]any{"designId": id, "expectedVersion": 2, "summary": "Review orders description"})
	if review["revisionId"] != draft["id"] || review["reviewUrl"] == "" {
		t.Fatalf("review did not freeze the edited document: %v", review)
	}
	stale, err := jsonx.Marshal(map[string]any{"designId": id, "expectedVersion": 1, "document": apiDesignFixture, "summary": "Stale write"})
	if err != nil {
		t.Fatal(err)
	}
	if _, errMsg := callTool(t, srv, "save_api_design_draft", string(stale)); !strings.Contains(errMsg, "409") {
		t.Fatalf("stale document was not refused: %q", errMsg)
	}
	after := designToolObject(t, srv, "get_api_design", map[string]any{"designId": id})
	if after["draft"].(map[string]any)["id"] != draft["id"] || len(after["revisions"].([]any)) != 2 || after["published"] != nil {
		t.Fatalf("stale save or review altered the document/publication: %v", after)
	}

	// The MCP identity cannot turn the candidate into a release, even if it
	// knows the HTTP route rather than going through a registered tool.
	path := "/api/designs/" + idString(t, id) + "/reviews/" + idString(t, review["id"]) + "/publish"
	src := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
	status, _, err := srv.CallAsMCP(t.Context(), src, http.MethodPost, path, []byte(`{"expectedVersion":2}`))
	deniedByAllowlist := err != nil && strings.Contains(err.Error(), "not an allowed route")
	if !deniedByAllowlist && (err != nil || (status != http.StatusForbidden && status != http.StatusNotFound)) {
		t.Fatalf("MCP publication was not refused: status=%d err=%v", status, err)
	}
	bypass, err := jsonx.Marshal(map[string]any{"workspaceId": design["draftWorkspaceId"], "method": "GET", "path": "/bypass", "status": 200, "body": map[string]any{"bypass": true}})
	if err != nil {
		t.Fatal(err)
	}
	if _, errMsg := callTool(t, srv, "create_endpoint", string(bypass)); !strings.Contains(errMsg, "409") {
		t.Fatalf("legacy tool bypassed managed workspace: %q", errMsg)
	}
}

func idString(t *testing.T, id any) string {
	t.Helper()
	encoded, err := jsonx.Marshal(id)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}
