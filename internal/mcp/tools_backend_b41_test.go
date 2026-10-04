package mcp

import (
	"strings"
	"testing"
)

func TestBackendB41PinnedReadDispatch(t *testing.T) {
	for _, tool := range []string{"get_backend_node", "get_backend_evidence", "get_backend_coverage", "get_backend_assertions"} {
		for _, target := range []string{"changeProposal", "importCandidate"} {
			fields := `"` + target + `":{"proposalId":"` + backendTestID + `","proposalRevisionId":"` + backendTestID + `"}`
			path := "/change-proposals/" + backendTestID + "/revisions/" + backendTestID
			if target == "importCandidate" {
				fields = `"importCandidate":{"importId":"` + backendTestID + `","importVersion":9223372036854775807,"candidateHash":"` + strings.Repeat("a", 64) + `"}`
				path = "/imports/" + backendTestID + "/candidate"
			}
			if tool == "get_backend_node" {
				fields += `,"nodeId":"` + backendTestID + `"`
			}
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, message := callTool(t, calls, tool, `{"projectId":"`+backendTestID+`",`+fields+`}`)
			if message != "" || calls.method != "GET" || !strings.Contains(calls.path, path) {
				t.Fatalf("%s %s: %s %s", tool, target, message, calls.path)
			}
			if target == "importCandidate" && !strings.Contains(calls.path, "importVersion=9223372036854775807") {
				t.Fatal("candidate version rounded")
			}
		}
	}
}

func TestBackendB41LegacyAssertionsAdmission(t *testing.T) {
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	_, message := callTool(t, calls, "get_backend_assertions", `{"projectId":"`+backendTestID+`","proposal":{"proposalId":"`+backendTestID+`","proposalRevisionId":"`+backendTestID+`"}}`)
	if calls.method != "" || !strings.Contains(message, "backend_unsupported_scope") || strings.Contains(message, "HTTP") {
		t.Fatalf("legacy admission: %s %s", calls.method, message)
	}
}

func TestBackendB41RawCandidateAndAggregateNumbers(t *testing.T) {
	hash := strings.Repeat("a", 64)
	for _, version := range []string{"9007199254740993", "9223372036854775807"} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		args := `{"projectId":"` + backendTestID + `","proposalId":"` + backendTestID + `","expectedVersion":` + version + `,"proposalRevisionId":"` + backendTestID + `","restoreRevisionId":"` + backendTestID + `","idempotencyKey":"restore"}`
		_, message := callTool(t, calls, "restore_backend_change_proposal", args)
		if message != "" || !strings.Contains(string(calls.sent), `"expectedVersion":`+version) {
			t.Fatalf("raw aggregate number changed: %s %s", message, calls.sent)
		}
	}
	for _, version := range []string{"0", "-1", "1.0", "1e0", "9223372036854775808", "null"} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, "get_backend_coverage", `{"projectId":"`+backendTestID+`","importCandidate":{"importId":"`+backendTestID+`","importVersion":`+version+`,"candidateHash":"`+hash+`"}}`)
		if message == "" || calls.method != "" {
			t.Fatalf("invalid candidate number admitted: %s", version)
		}
	}
	for _, target := range []string{`"changeProposal":null`, `"revisionId":"` + backendTestID + `","changeProposal":{"proposalId":"` + backendTestID + `","proposalRevisionId":"` + backendTestID + `"}`, `"revisionId":"` + backendTestID + `","revisionId":"` + backendTestID + `"`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, "get_backend_assertions", `{"projectId":"`+backendTestID+`",`+target+`}`)
		if message == "" || calls.method != "" {
			t.Fatalf("invalid target admitted: %s", target)
		}
	}
}
