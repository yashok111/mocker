package mcp

import (
	"strings"
	"testing"
)

func TestBackendProposalToolsRoutesAndPrecision(t *testing.T) {
	const id = backendTestID
	const base = `"projectId":"` + id + `"`
	const pins = `"proposalId":"` + id + `","proposalRevisionId":"` + id + `"`
	const edit = `"expectedVersion":9007199254740993,"draftRevisionId":"` + id + `","commands":[{"type":"alter_column","commandId":"c","reason":"Required","columnId":"` + id + `","nullable":false}]`
	for _, tt := range []struct{ name, args, method, path string }{
		{"list_backend_proposals", `{` + base + `,"status":"draft","limit":500}`, "GET", "/proposals?limit=500&status=draft"},
		{"create_backend_proposal", `{` + base + `,"name":"Require user","baseRevisionId":"` + id + `","repositoryId":"` + id + `","datastoreId":"` + id + `","facetKey":"sql","idempotencyKey":"create"}`, "POST", "/proposals"},
		{"get_backend_proposal", `{` + base + `,` + pins + `}`, "GET", "/proposals/" + id + "?proposalRevisionId=" + id},
		{"preview_backend_proposal_commands", `{` + base + `,"proposalId":"` + id + `",` + edit + `}`, "POST", "/proposals/" + id + "/preview"},
		{"apply_backend_proposal_commands", `{` + base + `,"proposalId":"` + id + `",` + edit + `,"candidateHash":"` + strings.Repeat("a", 64) + `","idempotencyKey":"save"}`, "POST", "/proposals/" + id + "/commands"},
		{"query_backend_graph", `{` + base + `,"proposal":{` + pins + `},"recordType":"nodes"}`, "POST", "/graph/query"},
		{"query_backend_database", `{` + base + `,"proposal":{` + pins + `},"datastoreId":"` + id + `","facetKey":"sql","recordType":"tables"}`, "POST", "/database/query"},
		{"get_backend_node", `{` + base + `,"proposal":{` + pins + `},"nodeId":"` + id + `"}`, "GET", "/proposals/" + id + "/revisions/" + id + "/nodes/" + id},
		{"get_backend_evidence", `{` + base + `,"proposal":{` + pins + `},"subjectId":"` + id + `"}`, "GET", "/proposals/" + id + "/revisions/" + id + "/evidence?subjectId=" + id},
		{"get_backend_coverage", `{` + base + `,"proposal":{` + pins + `}}`, "GET", "/proposals/" + id + "/revisions/" + id + "/coverage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"version":9007199254740995}`)}
			out, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tt.method || calls.path != "/api/backend-projects/"+id+tt.path {
				t.Fatalf("route: %s %s", calls.method, calls.path)
			}
			if strings.Contains(tt.args, "expectedVersion") && !strings.Contains(string(calls.sent), `"expectedVersion":9007199254740993`) {
				t.Fatalf("rounded: %s", calls.sent)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatalf("rounded result: %s", out)
			}
		})
	}
}

func TestBackendProposalToolsStrictTargets(t *testing.T) {
	const base = `"projectId":"` + backendTestID + `"`
	for _, args := range []string{`{` + base + `}`, `{` + base + `,"revisionId":"` + backendTestID + `","proposal":{"proposalId":"` + backendTestID + `","proposalRevisionId":"` + backendTestID + `"}}`, `{` + base + `,"proposal":null}`, `{` + base + `,"proposal":{"proposalId":"` + backendTestID + `"}}`, `{` + base + `,"proposal":{"proposalId":"` + backendTestID + `","proposalRevisionId":"` + backendTestID + `","unknown":true}}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "get_backend_coverage", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid target reached server: %s", args)
		}
	}
}
