package mcp

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"
)

// importSpecCall drives import_spec through the real handler over a
// scriptedCaller-style fake that records the request it received.
func importSpecCall(t *testing.T, calls Caller, args string) (ImportSpecOutput, string) {
	t.Helper()
	h := New(calls, testKey, testConfig(), nil).Handler()
	rec := doMCP(t, h,
		`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"import_spec","arguments":`+args+`}}`,
		map[string]string{"Authorization": "Bearer " + testKey})
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d; body=%s", rec.Code, rec.Body.String())
	}
	var env struct {
		Result struct {
			IsError           bool                    `json:"isError"`
			Content           []struct{ Text string } `json:"content"`
			StructuredContent json.RawMessage         `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Result.IsError {
		if len(env.Result.Content) > 0 {
			return ImportSpecOutput{}, env.Result.Content[0].Text
		}
		return ImportSpecOutput{}, "error with no content"
	}
	var out ImportSpecOutput
	if err := json.Unmarshal(env.Result.StructuredContent, &out); err != nil {
		t.Fatalf("decode structuredContent: %v; body=%s", err, rec.Body.String())
	}
	return out, ""
}

type recordingCaller struct {
	status int
	body   []byte
	method string
	path   string
	sent   []byte
}

func (r *recordingCaller) CallAsMCP(_ context.Context, _ *http.Request, method, path string, body []byte) (int, []byte, error) {
	r.method, r.path, r.sent = method, path, body
	return r.status, r.body, nil
}

func TestImportSpec_postsTheDocumentAsUploadAndProjectsTheReport(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusCreated,
		body: []byte(`{"id":12,"name":"Billing","version":"1.0","format":"oas30","source":"upload","sourceRef":"",` +
			`"basePath":"/v1","hash":"abc","createdAt":1,"createdBy":2,"duplicate":false,` +
			`"report":{"format":"oas30","basePath":"/v1","basePathOrigin":"servers","warnings":[{"pointer":"/paths/~1x","code":"ref_unresolved","message":"m"}],"operations":7,"degraded":1}}`),
	}
	out, errMsg := importSpecCall(t, calls, `{"name":"Billing","document":"openapi: 3.0.3\ninfo: {title: t, version: '1'}\npaths: {}\n"}`)
	if errMsg != "" {
		t.Fatalf("tool error: %s", errMsg)
	}
	if calls.method != "POST" || calls.path != "/api/specs" {
		t.Errorf("called %s %s, want POST /api/specs", calls.method, calls.path)
	}
	var sent map[string]string
	if err := json.Unmarshal(calls.sent, &sent); err != nil {
		t.Fatalf("sent body: %v", err)
	}
	if sent["source"] != "upload" || sent["name"] != "Billing" || !strings.HasPrefix(sent["document"], "openapi: 3.0.3") {
		t.Errorf("sent = %v", sent)
	}
	if out.Spec.ID != 12 || out.Spec.BasePath != "/v1" || out.Duplicate || out.Report == nil ||
		out.Report.Operations != 7 || out.Report.Degraded != 1 || len(out.Report.Warnings) != 1 {
		t.Errorf("out = %+v (report %+v)", out, out.Report)
	}
}

func TestImportSpec_duplicateIsNotAnError(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusOK,
		body:   []byte(`{"id":12,"name":"Billing","version":"1.0","format":"oas30","source":"upload","basePath":"","hash":"abc","createdAt":1,"duplicate":true,"report":{"format":"oas30","basePath":"","basePathOrigin":"none","warnings":null,"operations":7,"degraded":0}}`),
	}
	out, errMsg := importSpecCall(t, calls, `{"name":"Billing","document":"{\"openapi\":\"3.0.3\"}"}`)
	if errMsg != "" || !out.Duplicate || out.Report == nil || out.Report.Warnings == nil {
		t.Errorf("errMsg=%q out=%+v", errMsg, out)
	}
}

func TestImportSpec_refusesAnEmptyDocumentWithoutACall(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{status: http.StatusCreated, body: []byte(`{}`)}
	_, errMsg := importSpecCall(t, calls, `{"name":"x","document":"  "}`)
	if !strings.Contains(errMsg, "document is required") || calls.method != "" {
		t.Errorf("errMsg=%q called=%q", errMsg, calls.method)
	}
}

func TestImportSpec_surfacesTheServersOwnRefusal(t *testing.T) {
	t.Parallel()
	calls := &recordingCaller{
		status: http.StatusBadRequest,
		body:   []byte(`{"error":{"code":"bad_request","message":"unsupported format: Swagger 2.0 is converted in a later phase"}}`),
	}
	_, errMsg := importSpecCall(t, calls, `{"name":"x","document":"{\"swagger\":\"2.0\"}"}`)
	if !strings.Contains(errMsg, "Swagger 2.0") {
		t.Errorf("errMsg=%q", errMsg)
	}
}
