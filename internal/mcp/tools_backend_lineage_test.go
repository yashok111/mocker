package mcp

import (
	"strings"
	"testing"
)

func TestBackendLineageSDKStrictVariantsAndExactPins(t *testing.T) {
	base := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","direction":"forward","seed":`
	for _, seed := range []string{`{"kind":"api_field","nodeId":"` + backendTestID + `"}`, `{"kind":"column","nodeId":"` + backendTestID + `","facetKey":"sql"}`, `{"kind":"port","nodeId":"` + backendTestID + `","collection":"outputs","portKey":"amount"}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{"visitedValueCount":9007199254740993}`)}
		raw, msg := callTool(t, calls, "query_backend_lineage", base+seed+`}`)
		if msg != "" || calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/lineage/query" || !strings.Contains(string(calls.sent), `"revisionId":"`+backendTestID+`"`) || strings.Contains(string(calls.sent), "projectId") || !strings.Contains(string(raw), "9007199254740993") {
			t.Errorf("pin/raw loss %s %s %s %s", msg, calls.path, calls.sent, raw)
		}
	}
	for _, seed := range []string{`{"kind":"api_field","nodeKey":"key"}`, `{"kind":"api_field","nodeId":"` + backendTestID + `","facetKey":"sql"}`, `{"kind":"column","nodeId":"` + backendTestID + `"}`, `{"kind":"port","nodeId":"` + backendTestID + `","collection":"outputs","portKey":""}`, `{"kind":"port","nodeId":"` + backendTestID + `","collection":"outputs","portKey":"amount","extra":true}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "query_backend_lineage", base+seed+`}`)
		if msg == "" || calls.method != "" {
			t.Errorf("invalid seed reached route %s %s", seed, msg)
		}
	}
	valid := base + `{"kind":"api_field","nodeId":"` + backendTestID + `"}`
	for _, suffix := range []string{`,"proposal":{}}`, `,"limit":null}`, `,"limit":0}`, `,"limit":101}`, `,"maxDepth":33}`, `,"revisionId":"` + backendTestID + `"}`, `,"other":true}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "query_backend_lineage", valid+suffix)
		if msg == "" || calls.method != "" {
			t.Errorf("invalid query reached route %s %s", suffix, msg)
		}
	}

}

func TestBackendLineageReconcileMCPProfileSchema(t *testing.T) {
	manifest := `"manifest":{"repositoryName":"orders","provider":{"name":"collector","version":"1","namespace":"test","method":"ast","profiles":["foundation-graph-v1","relational-graph-v1","runtime-flow-v1","field-lineage-v1"],"limitations":[]},"snapshot":{"dirty":false,"consistency":"verified","capturedAt":"2026-09-30T10:00:00Z","files":[]}}`
	categories := strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests")
	items := make([]string, 0, len(categories))
	for _, category := range categories {
		items = append(items, `{"category":"`+category+`","status":"unsupported","knownCount":0,"denominator":null,"discoverySource":"collector","gaps":[],"reason":"outside profile"}`)
	}
	args := `{"projectId":"` + backendTestID + `","baseRevisionId":"` + backendTestID + `","expectedVersion":1,"idempotencyKey":"reconcile","profile":"field-lineage-v1","mode":"reconcile","repositoryId":"` + backendTestID + `","graphScope":{"profile":"field-lineage-v1","status":"partial","gaps":["bounded analysis"]},` + manifest + `,"inventory":[` + strings.Join(items, ",") + `]`
	for _, suffix := range []string{`}`, `,"profileExtension":{"fromProfile":"runtime-flow-v1","toProfile":"field-lineage-v1"}}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "begin_backend_import", args+suffix)
		if msg != "" || calls.method != "POST" || !strings.Contains(string(calls.sent), `"profile":"field-lineage-v1"`) {
			t.Fatalf("source4 reconcile refused %s %s", msg, calls.sent)
		}
	}
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	_, msg := callTool(t, calls, "begin_backend_import", args+`,"profileExtension":{"fromProfile":"relational-graph-v1","toProfile":"field-lineage-v1"}}`)
	if msg == "" || calls.method != "" {
		t.Fatal("skipped upgrade reached route")
	}
}
