package mcp

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// TestExportOpenAPI_callsTheRouteAndReturnsTheDocument is P7a's tool: one
// GET on the openapi.json route, the raw document handed back under
// `document`, and a 404 projected as a tool error.
func TestExportOpenAPI_callsTheRouteAndReturnsTheDocument(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"openapi":"3.1.0","info":{"title":"d","version":"0.0.0-draft.3"},"paths":{}}`)}
	out, errMsg := callTool(t, calls, "export_openapi", `{"workspaceId":4}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "GET" || calls.path != "/api/workspaces/4/openapi.json" {
		t.Errorf("called %s %s", calls.method, calls.path)
	}
	var got struct {
		Document string `json:"document"`
	}
	if err := json.Unmarshal(out, &got); err != nil || got.Document != string(calls.body) {
		t.Errorf("out = %s (err %v); want the served bytes verbatim under document (the shape import_spec takes)", out, err)
	}

	calls = &recordingCaller{status: http.StatusNotFound, body: []byte(`{"error":{"code":"not_found","message":"workspace not found"}}`)}
	if _, errMsg := callTool(t, calls, "export_openapi", `{"workspaceId":9}`); !strings.Contains(errMsg, "404") {
		t.Errorf("404 error = %q; want the status", errMsg)
	}
}
