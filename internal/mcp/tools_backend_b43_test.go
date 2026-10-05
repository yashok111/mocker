package mcp

import (
	"strings"
	"testing"
)

func TestBackendB43SDKClosedArmsAndList(t *testing.T) {
	id := backendTestID
	for _, kind := range []string{"change_package", "conformance", "endpoint_review"} {
		calls := &recordingCaller{status: 200, body: []byte(`{"items":[]}`)}
		_, msg := callTool(t, calls, "list_backend_analysis", `{"projectId":"`+id+`","kind":"`+kind+`"}`)
		if msg != "" || !strings.Contains(calls.path, "kind="+kind) {
			t.Fatal(kind, msg, calls.path)
		}
	}
	proposal := `{"proposalId":"` + id + `","proposalRevisionId":"` + id + `"}`
	raw := `{"projectId":"` + id + `","kind":"conformance","changeProposal":` + proposal + `,"resultRevisionId":"` + id + `","identityMap":[],"testAttachments":[],"limits":{},"observationMode":"none","idempotencyKey":"key"}`
	calls := &recordingCaller{status: 202, body: []byte(`{}`)}
	if _, msg := callTool(t, calls, "start_backend_analysis", raw); msg != "" || calls.method != "POST" {
		t.Fatal(msg, calls.method)
	}
	for _, bad := range []string{strings.Replace(raw, `"identityMap":[],`, "", 1), strings.Replace(raw, `"testAttachments":[]`, `"testAttachments":null`, 1), strings.Replace(raw, `"limits":{}`, `"limits":{},"target":{}`, 1), strings.Replace(raw, `"identityMap":[]`, `"identityMap":[],"identityMap":[]`, 1)} {
		calls := &recordingCaller{status: 202}
		_, msg := callTool(t, calls, "start_backend_analysis", bad)
		if msg == "" || calls.method != "" {
			t.Fatal("accepted", bad, msg)
		}
	}
}

func TestBackendB43SDKLifecycleRawInt64(t *testing.T) {
	id := backendTestID
	for _, action := range []string{"implemented", "archive", "unarchive"} {
		var raw string
		for _, version := range []string{"9007199254740993", "9223372036854775807"} {
			raw = `{"projectId":"` + id + `","proposalId":"` + id + `","expectedVersion":` + version + `,"proposalRevisionId":"` + id + `","action":"` + action + `","idempotencyKey":"key"`
			if action == "implemented" {
				raw += `,"report":{"jobId":"` + id + `","resultVersion":` + version + `,"inputHash":"` + strings.Repeat("a", 64) + `","resultHash":"` + strings.Repeat("b", 64) + `"},"resultRevisionId":"` + id + `","exceptions":[]`
			}
			raw += "}"
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			_, msg := callTool(t, calls, "apply_backend_change_proposal_lifecycle", raw)
			if msg != "" || !strings.Contains(string(calls.sent), `"expectedVersion":`+version) {
				t.Fatal(msg, string(calls.sent))
			}
		}
		// Replacing expectedVersion erases its original value. Exercise each
		// invalid token once per action, not once per valid boundary value.
		const version = "9223372036854775807"
		for _, badVersion := range []string{"1.0", "1e0", "9223372036854775808", "null"} {
			bad := strings.Replace(raw, `"expectedVersion":`+version, `"expectedVersion":`+badVersion, 1)
			calls := &recordingCaller{status: 200}
			_, msg := callTool(t, calls, "apply_backend_change_proposal_lifecycle", bad)
			if msg == "" || calls.method != "" {
				t.Fatal("accepted", bad)
			}
		}
		for _, bad := range []string{strings.Replace(raw, `"action":"`+action+`"`, `"action":"`+action+`","acknowledgedGapIds":[]`, 1), strings.Replace(raw, `"expectedVersion":`+version, `"expectedVersion":`+version+`,"expectedVersion":1`, 1)} {
			calls := &recordingCaller{status: 200}
			_, msg := callTool(t, calls, "apply_backend_change_proposal_lifecycle", bad)
			if msg == "" || calls.method != "" {
				t.Fatal("accepted", bad)
			}
		}
	}
}
