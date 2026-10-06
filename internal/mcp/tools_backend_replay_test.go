package mcp

import (
	"strings"
	"testing"
)

func TestBackendReplayClosedConsentAndExactVersion(t *testing.T) {
	raw := `{"projectId":"` + backendTestID + `","configuredTargetId":"orders","expectedIdentityHash":"` + strings.Repeat("a", 64) + `","allowReset":true,"idempotencyKey":"` + backendTestID + `"}`
	caller := &recordingCaller{status: 200, body: []byte(`{}`)}
	if _, msg := callTool(t, caller, "connect_backend_replay_profile", raw); msg != "" || caller.method != "POST" || !strings.HasSuffix(caller.path, "/replay/profiles") {
		t.Fatal(msg, caller.path)
	}
	for _, bad := range []string{strings.Replace(raw, `"allowReset":true`, `"allowReset":false`, 1), strings.Replace(raw, `"allowReset":true,`, "", 1), strings.Replace(raw, `"configuredTargetId":"orders"`, `"configuredTargetId":"orders","origin":"http://attacker"`, 1)} {
		caller := &recordingCaller{status: 200}
		if _, msg := callTool(t, caller, "connect_backend_replay_profile", bad); msg == "" || caller.method != "" {
			t.Fatal("invalid consent dispatched", msg)
		}
	}
	caller = &recordingCaller{status: 200, body: []byte(`{}`)}
	if _, msg := callTool(t, caller, "get_backend_replay_package", `{"projectId":"`+backendTestID+`","replayItemId":"`+backendTestID+`","version":9007199254740993}`); msg != "" || !strings.HasSuffix(caller.path, "/versions/9007199254740993") {
		t.Fatal(msg, caller.path)
	}
}

func TestBackendReplayRunControlRoutes(t *testing.T) {
	for _, action := range []string{"cancel", "reconcile"} {
		caller := &recordingCaller{status: 200, body: []byte(`{}`)}
		if _, msg := callTool(t, caller, action+"_backend_replay_run", `{"projectId":"`+backendTestID+`","replayRunId":"`+backendTestID+`"}`); msg != "" || !strings.HasSuffix(caller.path, "/runs/"+backendTestID+"/"+action) || string(caller.sent) != "{}" {
			t.Fatal(msg, caller.path, string(caller.sent))
		}
	}
}

func TestBackendReplayScopeReadOnlyContract(t *testing.T) {
	raw := `{"projectId":"` + backendTestID + `","pin":{"id":"` + backendTestID + `","version":2,"contentHash":"` + strings.Repeat("a", 64) + `"},"selectors":[{"kind":"semantic","id":"` + backendTestID + `"}]}`
	caller := &recordingCaller{status: 200, body: []byte(`{}`)}
	if _, msg := callTool(t, caller, "resolve_backend_diagram_scope", raw); msg != "" || caller.path != "/api/backend-projects/"+backendTestID+"/diagrams/resolve-scope" {
		t.Fatal(msg, caller.path)
	}
	caller = &recordingCaller{status: 200}
	bad := strings.Replace(raw, `"selectors":`, `"allowReset":true,"selectors":`, 1)
	if _, msg := callTool(t, caller, "resolve_backend_diagram_scope", bad); msg == "" || caller.method != "" {
		t.Fatal("scope accepted executable consent fields", msg)
	}
}
