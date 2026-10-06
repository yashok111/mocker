package mcp

import (
	"strings"
	"testing"
)

func TestBackendFindingToolRoutes(t *testing.T) {
	id := backendTestID
	hash := strings.Repeat("a", 64)
	for _, c := range []struct{ name, args, method, path string }{
		{"list_backend_findings", `{"projectId":"` + id + `","jobId":"` + id + `","resultVersion":9007199254740993}`, "GET", "/api/backend-projects/" + id + "/findings?jobId=" + id + "&resultVersion=9007199254740993"},
		{"review_backend_finding", `{"projectId":"` + id + `","fingerprint":"` + hash + `","expectedVersion":1,"basisHash":"` + hash + `","status":"accepted_risk","reason":"review","idempotencyKey":"key"}`, "PUT", "/api/backend-projects/" + id + "/findings/" + hash + "/review"},
	} {
		t.Run(c.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, msg := callTool(t, calls, c.name, c.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != c.method || calls.path != c.path {
				t.Fatal(calls.method, calls.path)
			}
		})
	}
}
