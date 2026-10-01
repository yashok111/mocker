package mcp

import (
	"strings"
	"testing"
)

func TestBackendFlowSDKStrictVariantsAndExactPins(t *testing.T) {
	base := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `",`
	for _, suffix := range []string{`"view":"entrypoints"}`, `"view":"steps","flowId":"` + backendTestID + `"}`, `"view":"transitions","flowId":"` + backendTestID + `"}`, `"view":"accesses","dataNodeId":"` + backendTestID + `","accessKind":"writes"}`, `"view":"accesses","entrypointId":"` + backendTestID + `"}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{"coverage":{"inventory":[{"knownCount":9007199254740993}]}}`)}
		raw, msg := callTool(t, calls, "query_backend_flow", base+suffix)
		if msg != "" || calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/flow/query" || !strings.Contains(string(calls.sent), `"revisionId":"`+backendTestID+`"`) || strings.Contains(string(calls.sent), "projectId") || !strings.Contains(string(raw), "9007199254740993") {
			t.Errorf("variant lost pin/raw bytes: %s %s %s %s", msg, calls.path, calls.sent, raw)
		}
	}
	for _, suffix := range []string{`"view":"entrypoints","flowId":""}`, `"view":"accesses"}`, `"view":"accesses","entrypointId":"` + backendTestID + `","dataNodeId":"` + backendTestID + `"}`, `"view":"entrypoints","limit":null}`, `"view":"entrypoints","limit":0}`, `"view":"entrypoints","limit":501}`, `"view":"steps","flowId":null}`, `"view":"entrypoints","proposal":{}}`, `"view":"entrypoints","view":"entrypoints"}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "query_backend_flow", base+suffix)
		if msg == "" || calls.method != "" {
			t.Errorf("invalid variant reached route: %s %s", suffix, msg)
		}
	}
}
