package mcp

import (
	"strings"
	"testing"
)

func TestBackendSource6DecisionSDKRawQualifiedRefs(t *testing.T) {
	hash := strings.Repeat("a", 64)
	base := `{"repositoryId":"` + backendTestID + `","providerNamespace":"provider-b","recordType":"node","externalKey":"foreign-key","expectedId":"` + backendTestID + `","assertionHash":"` + hash + `"}`
	claim := `{"op":"claim_identity","claimIdentity":{"decisionId":"` + backendTestID + `","recordType":"node","externalKey":"own-key","target":` + base + `,"reason":"Same object","evidenceKeys":["own-proof"]}}`
	resolution := `{"op":"resolve_assertion","resolution":{"decisionId":"` + backendTestID + `","recordType":"node","id":"` + backendTestID + `","property":{"kind":"name"},"conflictHash":"` + hash + `","select":{"repositoryId":"` + backendTestID + `","providerNamespace":"provider-b","assertionHash":"` + hash + `"},"reason":"Selected declaration"}}`
	for _, command := range []string{claim, resolution} {
		calls := &recordingCaller{status: 200, body: []byte(`{"acceptedVersion":9007199254740995}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"decision","expectedImportVersion":9007199254740993,"payloadHash":"` + hash + `","commands":[` + command + `]}`
		out, message := callTool(t, calls, "put_backend_import_batch", args)
		if message != "" || calls.method != "PUT" || calls.path != "/api/backend-projects/"+backendTestID+"/imports/"+backendTestID+"/batches/decision" {
			t.Fatalf("qualified decision dispatch failed: %s %s %s", message, calls.method, calls.path)
		}
		for _, fragment := range []string{`"expectedImportVersion":9007199254740993`, `"providerNamespace":"provider-b"`, `"assertionHash":"` + hash + `"`} {
			if !strings.Contains(string(calls.sent), fragment) {
				t.Fatalf("decision qualification/number changed: %s", calls.sent)
			}
		}
		if !strings.Contains(string(out), "9007199254740995") {
			t.Fatalf("receipt number rounded: %s", out)
		}
	}
	for _, command := range []string{
		strings.Replace(claim, `"providerNamespace":"provider-b",`, "", 1),
		strings.Replace(claim, `"evidenceKeys":["own-proof"]`, `"evidenceKeys":null`, 1),
		strings.Replace(resolution, `"property":{"kind":"name"}`, `"property":{"kind":"name","group":null}`, 1),
		strings.Replace(resolution, `"reason":"Selected declaration"`, `"reason":"Selected declaration","replacementValue":"New name"`, 1),
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"decision","expectedImportVersion":1,"payloadHash":"` + hash + `","commands":[` + command + `]}`
		_, message := callTool(t, calls, "put_backend_import_batch", args)
		if message == "" || calls.method != "" {
			t.Fatalf("invalid decision reached admin: %s", command)
		}
	}
}

func TestBackendSource6PreviewChangeSDKKinds(t *testing.T) {
	for _, kind := range []string{"assertion_conflict", "claim_identity", "migration"} {
		calls := &recordingCaller{status: 200, body: []byte(`{"items":[]}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","previewVersion":9007199254740993,"recordType":"` + kind + `"}`
		_, message := callTool(t, calls, "get_backend_import_changes", args)
		if message != "" || calls.method != "GET" || !strings.Contains(calls.path, "previewVersion=9007199254740993&recordType="+kind) {
			t.Fatalf("source6 preview paging rejected/changed %s: %s %s", kind, message, calls.path)
		}
	}
}
