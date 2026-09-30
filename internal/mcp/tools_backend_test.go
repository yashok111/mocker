package mcp

import (
	"net/http"
	"strings"
	"testing"
)

const backendTestID = "0197aaf9-5555-7000-8000-000000000001"

func TestBackendToolsUseRoutesAndExactVersions(t *testing.T) {
	for _, tt := range []struct{ name, args, method, path string }{
		{"get_backend_capabilities", `{}`, "GET", "/api/backend-projects/capabilities"},
		{"list_backend_projects", `{"limit":5}`, "GET", "/api/backend-projects?limit=5"},
		{"create_backend_project", `{"name":"Orders","idempotencyKey":"create"}`, "POST", "/api/backend-projects"},
		{"get_backend_project", `{"projectId":"` + backendTestID + `"}`, "GET", "/api/backend-projects/" + backendTestID},
		{"list_backend_revisions", `{"projectId":"` + backendTestID + `"}`, "GET", "/api/backend-projects/" + backendTestID + "/revisions"},
		{"get_backend_revision", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `"}`, "GET", "/api/backend-projects/" + backendTestID + "/revisions/" + backendTestID},
		{"apply_backend_project_commands", `{"projectId":"` + backendTestID + `","expectedVersion":9007199254740993,"idempotencyKey":"rename","commands":[{"type":"rename_project","name":"Shipping"}]}`, "POST", "/api/backend-projects/" + backendTestID + "/commands"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: http.StatusOK, body: []byte(`{"version":9007199254740995}`)}
			out, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tt.method || calls.path != tt.path {
				t.Fatalf("called %s %s", calls.method, calls.path)
			}
			if !strings.Contains(string(out), `9007199254740995`) {
				t.Fatalf("rounded result: %s", out)
			}
			if tt.name == "apply_backend_project_commands" && !strings.Contains(string(calls.sent), `"expectedVersion":9007199254740993`) {
				t.Fatalf("rounded CAS: %s", calls.sent)
			}
			if strings.Contains(string(calls.sent), "projectId") {
				t.Fatalf("path parameter in body: %s", calls.sent)
			}
		})
	}
}

func TestBackendToolsRejectInvalidInputsBeforeCallingAdmin(t *testing.T) {
	for _, args := range []string{`{}`, `{"projectId":"../../workspaces/1"}`, `{"projectId":"` + backendTestID + `","unknown":true}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "get_backend_project", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid input reached admin: %q %q", args, msg)
		}
	}
}

func TestBackendToolsKeepConflictRecoveryFields(t *testing.T) {
	calls := &recordingCaller{status: 409, body: []byte(`{"error":{"code":"backend_version_conflict","message":"Changed","currentVersion":9007199254740993,"retryable":false}}`)}
	_, msg := callTool(t, calls, "get_backend_project", `{"projectId":"`+backendTestID+`"}`)
	if !strings.Contains(msg, "9007199254740993") || !strings.Contains(msg, "retryable") {
		t.Fatalf("recovery fields lost: %s", msg)
	}
}
