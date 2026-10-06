package mcp

import (
	"strings"
	"testing"
)

func TestBackendMaterializationToolRoutes(t *testing.T) {
	for _, action := range []string{"preview", "apply"} {
		args := `{"projectId":"` + backendTestID + `","profileVersion":"backend-http-draft-v1","target":{"changeProposal":{"proposalId":"` + backendTestID + `","proposalRevisionId":"` + backendTestID + `"}},"targetHash":"` + strings.Repeat("a", 64) + `","sourceScope":["` + backendTestID + `"],"targets":[{"key":"api","kind":"api_design","name":"API","expectedVersion":0,"commands":[{"type":"replace_api_document","apiDocument":"{}"}]}],"translations":[],"partialSimulation":false,"excludedIds":[],"reason":"authored"`
		if action == "apply" {
			args += `,"candidateHash":"` + strings.Repeat("b", 64) + `","idempotencyKey":"test"`
		}
		args += "}"
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, action+"_backend_materialization", args)
		if message != "" || calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/materializations/"+action {
			t.Fatal(calls.method, calls.path, message)
		}
	}
}
