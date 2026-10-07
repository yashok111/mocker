package mcp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestBackendImportToolsRawRoutes(t *testing.T) {
	const ids = `"projectId":"` + backendTestID + `","importId":"` + backendTestID + `"`
	for _, tt := range []struct{ name, args, method, path string }{
		{"list_backend_imports", `{"projectId":"` + backendTestID + `"}`, "GET", "/imports"},
		{"get_backend_import", `{` + ids + `,"limit":500}`, "GET", "/imports/" + backendTestID + "?limit=500"},
		{"preview_backend_import", `{` + ids + `,"expectedImportVersion":9007199254740993,"baseRevisionId":"` + backendTestID + `"}`, "POST", "/imports/" + backendTestID + "/preview"},
		{"commit_backend_import", `{` + ids + `,"expectedVersion":9007199254740993,"expectedImportVersion":9007199254740993,"candidateHash":"` + strings.Repeat("a", 64) + `","idempotencyKey":"commit"}`, "POST", "/imports/" + backendTestID + "/commit"},
		{"abort_backend_import", `{` + ids + `,"expectedImportVersion":9007199254740993,"idempotencyKey":"abort"}`, "POST", "/imports/" + backendTestID + "/abort"},
		{"put_backend_import_batch", `{` + ids + `,"batchId":"b1","expectedImportVersion":9007199254740993,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[{"op":"remove","remove":{"recordType":"node","externalKey":"x"}}]}`, "PUT", "/imports/" + backendTestID + "/batches/b1"},
		{"query_backend_graph", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"nodes","limit":500}`, "POST", "/graph/query"},
		{"get_backend_node", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","nodeId":"` + backendTestID + `"}`, "GET", "/revisions/" + backendTestID + "/nodes/" + backendTestID},
		{"get_backend_evidence", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","subjectId":"` + backendTestID + `","limit":500}`, "GET", "/revisions/" + backendTestID + "/evidence?limit=500&subjectId=" + backendTestID},
		{"get_backend_coverage", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `"}`, "GET", "/revisions/" + backendTestID + "/coverage"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"version":9007199254740995}`)}
			out, msg := callTool(t, calls, tt.name, tt.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tt.method || calls.path != "/api/backend-projects/"+backendTestID+tt.path {
				t.Fatalf("route: %s %s", calls.method, calls.path)
			}
			if strings.Contains(tt.args, "expectedImportVersion") && !strings.Contains(string(calls.sent), `"expectedImportVersion":9007199254740993`) {
				t.Fatalf("rounded input %s", calls.sent)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatalf("rounded output %s", out)
			}
			if strings.Contains(string(calls.sent), "projectId") || strings.Contains(string(calls.sent), "importId") {
				t.Fatalf("path fields leaked %s", calls.sent)
			}
		})
	}
}

func TestBackendImportToolsRejectUnknownAndWrongSelectors(t *testing.T) {
	for _, args := range []string{`{}`, `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"nodes","from":"` + backendTestID + `"}`, `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"edges","search":"x"}`, `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"nodes","unknown":true}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "query_backend_graph", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid arguments reached admin: %s", args)
		}
	}
}

func TestBeginBackendImportToolExactInventory(t *testing.T) {
	const manifest = `"manifest":{"repositoryName":"orders","provider":{"name":"collector","version":"1","namespace":"test","method":"ast","profiles":["foundation-graph-v1"],"limitations":[]},"snapshot":{"dirty":false,"consistency":"verified","capturedAt":"2026-09-30T10:00:00Z","files":[]}}`
	items := []string{}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		items = append(items, `{"category":"`+category+`","status":"unsupported","knownCount":9007199254740993,"denominator":9007199254740994,"discoverySource":"collector","gaps":[],"reason":"unsupported"}`)
	}
	args := `{"projectId":"` + backendTestID + `","baseRevisionId":"` + backendTestID + `","expectedVersion":9007199254740993,"idempotencyKey":"begin",` + manifest + `,"inventory":[` + strings.Join(items, ",") + `]}`
	calls := &recordingCaller{status: 200, body: []byte(`{"acceptedBatchCount":9007199254740995}`)}
	out, msg := callTool(t, calls, "begin_backend_import", args)
	if msg != "" {
		t.Fatal(msg)
	}
	if calls.method != "POST" || calls.path != "/api/backend-projects/"+backendTestID+"/imports" {
		t.Fatalf("wrong route %s %s", calls.method, calls.path)
	}
	for _, want := range []string{`"expectedVersion":9007199254740993`, `"knownCount":9007199254740993`, `"denominator":9007199254740994`} {
		if !strings.Contains(string(calls.sent), want) {
			t.Fatalf("rounded %s in %s", want, calls.sent)
		}
	}
	if !strings.Contains(string(out), `9007199254740995`) {
		t.Fatalf("rounded output %s", out)
	}
}

func TestBackendImportSDKInt64BoundsAndDuplicateMembers(t *testing.T) {
	for _, version := range []string{"9223372036854775807", "9223372036854775808", "9007199254740993"} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","expectedImportVersion":` + version + `,"idempotencyKey":"abort"}`
		_, msg := callTool(t, calls, "abort_backend_import", args)
		if version == "9223372036854775808" {
			if msg == "" || calls.method != "" {
				t.Fatalf("out-of-int64 reached admin %s", calls.sent)
			}
			continue
		}
		if msg != "" || !strings.Contains(string(calls.sent), version) {
			t.Fatalf("exact int64 rejected/rounded: %s %s", calls.sent, msg)
		}
	}
	calls := &recordingCaller{status: 200, body: []byte(`{}`)}
	_, msg := callTool(t, calls, "abort_backend_import", `{"projectId":"`+backendTestID+`","importId":"`+backendTestID+`","expectedImportVersion":1,"expectedImportVersion":2,"idempotencyKey":"abort"}`)
	if msg == "" || calls.method != "" {
		t.Fatalf("duplicate member reached admin %s", calls.sent)
	}
}

func TestBackendImportToolsPublishExactSchemasAndHints(t *testing.T) {
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string           `json:"name"`
				Schema      jsonx.RawMessage `json:"inputSchema"`
				Annotations struct {
					ReadOnly   bool `json:"readOnlyHint"`
					Idempotent bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"query_backend_events": true, "query_backend_lineage": true, "begin_backend_import": false, "list_backend_imports": true, "get_backend_import": true, "put_backend_import_batch": false, "preview_backend_import": false, "commit_backend_import": false, "abort_backend_import": false, "query_backend_graph": true, "query_backend_database": true, "get_backend_node": true, "get_backend_evidence": true, "get_backend_coverage": true, "compare_backend_revisions": true, "get_backend_import_changes": true}
	for _, tool := range envelope.Result.Tools {
		readOnly, ok := expected[tool.Name]
		if !ok {
			continue
		}
		if tool.Annotations.ReadOnly != readOnly || tool.Annotations.Idempotent != (tool.Name != "preview_backend_import") {
			t.Errorf("wrong hints %s %+v", tool.Name, tool.Annotations)
		}
		if strings.Contains(string(tool.Schema), "expectedImportVersion") && !strings.Contains(string(tool.Schema), `"maximum":9223372036854775807`) {
			t.Errorf("int64 maximum rounded in SDK schema %s: %s", tool.Name, tool.Schema)
		}
		delete(expected, tool.Name)
	}
	if len(expected) != 0 {
		t.Fatalf("unpublished import tools %+v", expected)
	}
}

func TestBackendImportSDKRejectsNullableOptionalKeys(t *testing.T) {
	for _, command := range []string{
		`{"op":"upsert_node","node":{"externalKey":"n","kind":"symbol","name":"N","parentKey":null,"attributes":{},"evidenceKeys":[]}}`,
		`{"op":"upsert_evidence","evidence":{"externalKey":"e","subjectType":"node","subjectKey":"n","propertyPath":null,"method":"ast","status":"explicit","source":{"repositoryId":"` + backendTestID + `","snapshotId":"` + backendTestID + `","file":"main.go","contentHash":"` + strings.Repeat("a", 64) + `"},"explanation":""}}`,
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"b","expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[` + command + `]}`
		_, msg := callTool(t, calls, "put_backend_import_batch", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("nullable optional key reached admin %s", calls.sent)
		}
	}
}

func TestBackendImportMCPBodySchemasMatchOpenAPI(t *testing.T) {
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var envelope struct {
		Result struct {
			Tools []struct {
				Name        string           `json:"name"`
				InputSchema jsonx.RawMessage `json:"inputSchema"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	decode := func(raw []byte) map[string]any {
		t.Helper()
		decoder := jsonx.NewDecoder(bytes.NewReader(raw))
		decoder.UseNumber()
		var value map[string]any
		if err := decoder.Decode(&value); err != nil {
			t.Fatal(err)
		}
		return value
	}
	document := decode(raw)
	schemas := document["components"].(map[string]any)["schemas"].(map[string]any)
	var resolve func(any) any
	resolve = func(value any) any {
		switch value := value.(type) {
		case map[string]any:
			if ref, ok := value["$ref"].(string); ok {
				return resolve(schemas[strings.TrimPrefix(ref, "#/components/schemas/")])
			}
			out := map[string]any{}
			for key, item := range value {
				out[key] = resolve(item)
			}
			return out
		case []any:
			out := make([]any, len(value))
			for i, item := range value {
				out[i] = resolve(item)
			}
			return out
		default:
			return value
		}
	}
	contracts := map[string]string{"query_backend_events": "QueryBackendEventsRequest", "query_backend_lineage": "QueryBackendLineageRequest", "begin_backend_import": "BeginBackendImportRequest", "put_backend_import_batch": "PutBackendImportBatchRequest", "preview_backend_import": "PreviewBackendImportRequest", "commit_backend_import": "CommitBackendImportRequest", "abort_backend_import": "AbortBackendImportRequest", "query_backend_graph": "QueryBackendGraphRequest", "query_backend_database": "QueryBackendDatabaseRequest", "compare_backend_revisions": "CompareBackendRevisionsRequest"}
	for _, tool := range envelope.Result.Tools {
		name, ok := contracts[tool.Name]
		if !ok {
			continue
		}
		// tools/list publishes repeated subtrees through $defs (review
		// 2026-10-06, F23); the contract comparison reads the expanded form.
		got := inlineLocalDefs(decode(tool.InputSchema))
		stripPaths := func(branch map[string]any) {
			props := branch["properties"].(map[string]any)
			for _, key := range []string{"projectId", "importId", "batchId"} {
				delete(props, key)
			}
			required := []any{}
			for _, key := range branch["required"].([]any) {
				if key != "projectId" && key != "importId" && key != "batchId" {
					required = append(required, key)
				}
			}
			branch["required"] = required
		}
		if branches, ok := got["oneOf"].([]any); ok {
			for _, branch := range branches {
				stripPaths(branch.(map[string]any))
			}
		} else {
			stripPaths(got)
		}
		want := resolve(schemas[name]).(map[string]any)
		want["type"] = "object"
		if !reflect.DeepEqual(got, want) {
			t.Errorf("MCP %s input body diverges from OpenAPI %s", tool.Name, name)
		}
		delete(contracts, tool.Name)
	}
	if len(contracts) != 0 {
		t.Fatalf("missing tools %+v", contracts)
	}
}

func TestBackendImportBatchPathSegments(t *testing.T) {
	for _, tt := range []struct{ id, path string }{{"a/b", "a%2Fb"}, {"a?x=1", "a%3Fx=1"}, {"a#b", "a%23b"}, {"a%b", "a%25b"}, {".", "%2E"}, {"..", "%2E%2E"}} {
		t.Run(tt.id, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{}`)}
			args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"` + tt.id + `","expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[{"op":"remove","remove":{"recordType":"node","externalKey":"x"}}]}`
			_, msg := callTool(t, calls, "put_backend_import_batch", args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.path != "/api/backend-projects/"+backendTestID+"/imports/"+backendTestID+"/batches/"+tt.path {
				t.Fatalf("batch ID corrupted path: %s", calls.path)
			}
		})
	}
}

func TestBackendImportBatchIDsSurviveRealSDKLoopback(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	invoke := func(t *testing.T, name string, args any) []byte {
		t.Helper()
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatal(err)
		}
		out, msg := callTool(t, server, name, string(raw))
		if msg != "" {
			t.Fatalf("%s: %s", name, msg)
		}
		return out
	}
	var project backendmodel.Project
	if err := json.Unmarshal(invoke(t, "create_backend_project", backendmodel.CreateInput{Name: "Batch IDs", IdempotencyKey: "batch-ids"}), &project); err != nil {
		t.Fatal(err)
	}
	begin := backendmodel.BeginImportInput{ExpectedVersion: project.Version, BaseRevisionID: project.CurrentRevisionID, IdempotencyKey: "begin", Manifest: backendmodel.SourceManifest{RepositoryName: "repo", Provider: backendmodel.SourceProvider{Name: "collector", Version: "1", Namespace: "test", Method: "agent", Profiles: []string{backendmodel.GraphProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Dirty: false, Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []backendmodel.ManifestFile{}}}}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		begin.Inventory = append(begin.Inventory, backendmodel.InventoryItem{Category: category, Status: "unsupported", KnownCount: 0, Denominator: nil, DiscoverySource: "collector", Gaps: []string{}, Reason: "unsupported"})
	}
	var session backendmodel.ImportSession
	type beginFields backendmodel.BeginImportInput
	if err := json.Unmarshal(invoke(t, "begin_backend_import", struct {
		ProjectID string `json:"projectId"`
		beginFields
	}{ProjectID: project.ID, beginFields: beginFields(begin)}), &session); err != nil {
		t.Fatal(err)
	}
	version := session.Version
	for _, id := range []string{"a/b", "a?x=1", "a#b", "a%b", ".", ".."} {
		t.Run(id, func(t *testing.T) {
			commands := []backendmodel.ImportCommand{{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "unknown", Kind: "unresolved_target", Name: "Unknown", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"symbol"`), "reason": jsontext.Value(`"not resolved"`), "searchScope": jsontext.Value(`"repository"`)}, EvidenceKeys: []string{}}}}
			hash, err := backendmodel.ImportBatchHash(commands)
			if err != nil {
				t.Fatal(err)
			}
			args := struct {
				ProjectID string `json:"projectId"`
				ImportID  string `json:"importId"`
				BatchID   string `json:"batchId"`
				backendmodel.ImportBatchInput
			}{ProjectID: project.ID, ImportID: session.ID, BatchID: id, ImportBatchInput: backendmodel.ImportBatchInput{ExpectedImportVersion: version, PayloadHash: hash, Commands: commands}}
			first := invoke(t, "put_backend_import_batch", args)
			var receipt backendmodel.BatchReceipt
			if err := json.Unmarshal(first, &receipt); err != nil {
				t.Fatal(err)
			}
			if receipt.BatchID != id {
				t.Fatalf("batch ID changed: %q want %q", receipt.BatchID, id)
			}
			if replay := invoke(t, "put_backend_import_batch", args); string(replay) != string(first) {
				t.Fatalf("replay changed %s vs %s", first, replay)
			}
			version = receipt.AcceptedVersion
		})
	}
}

func TestBackendGraphSDKExplicitLimitBounds(t *testing.T) {
	for _, limit := range []string{"0", "-1", "501", "1", "500", ""} {
		args := `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"nodes"`
		if limit != "" {
			args += `,"limit":` + limit
		}
		args += `}`
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, "query_backend_graph", args)
		if limit == "0" || limit == "-1" || limit == "501" {
			if message == "" || calls.method != "" {
				t.Fatalf("invalid explicit limit reached admin: %s", args)
			}
		} else if message != "" || calls.method != "POST" {
			t.Fatalf("valid/omitted limit rejected: %s %s", args, message)
		}
	}
}

func TestBackendReconcileReadToolsRawPins(t *testing.T) {
	for _, tt := range []struct{ name, args, method, suffix string }{
		{"compare_backend_revisions", `{"projectId":"` + backendTestID + `","fromRevisionId":"` + backendTestID + `","toRevisionId":"` + backendTestID + `","limit":500}`, "POST", "/revisions/compare"},
		{"get_backend_import_changes", `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","previewVersion":9007199254740993,"recordType":"identity","limit":500}`, "GET", "/imports/" + backendTestID + "/changes?limit=500&previewVersion=9007199254740993&recordType=identity"},
		{"query_backend_graph", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"edges","id":"` + backendTestID + `"}`, "POST", "/graph/query"},
		{"get_backend_evidence", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","evidenceId":"` + backendTestID + `"}`, "GET", "/revisions/" + backendTestID + "/evidence?evidenceId=" + backendTestID},
	} {
		t.Run(tt.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"sourceChangeCount":9007199254740995}`)}
			out, message := callTool(t, calls, tt.name, tt.args)
			if message != "" {
				t.Fatal(message)
			}
			if calls.method != tt.method || calls.path != "/api/backend-projects/"+backendTestID+tt.suffix {
				t.Fatalf("wrong route: %s %s", calls.method, calls.path)
			}
			if tt.name == "compare_backend_revisions" && (!strings.Contains(string(calls.sent), `"fromRevisionId"`) || !strings.Contains(string(calls.sent), `"toRevisionId"`)) {
				t.Fatalf("body pins missing: %s", calls.sent)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatalf("rounded output: %s", out)
			}
		})
	}
}

func TestBackendReconcileCommandUnion(t *testing.T) {
	validIdentity := `{"op":"map_identity","identity":{"recordType":"node","fromExternalKey":"old","toExternalKey":"new","expectedId":"` + backendTestID + `","reason":"same subject","evidenceKeys":["proof"]}}`
	validDeletion := `{"op":"delete_assertion","deletion":{"recordType":"edge","externalKey":"old","expectedId":"` + backendTestID + `","reason":"removed"}}`
	for _, tt := range []struct {
		command string
		valid   bool
	}{
		{validIdentity, true}, {validDeletion, true},
		{strings.Replace(validIdentity, `"node"`, `"evidence"`, 1), false},
		{strings.Replace(validIdentity, `"expectedId":"`+backendTestID+`"`, `"expectedId":null`, 1), false},
		{strings.Replace(validIdentity, `"expectedId":"`+backendTestID+`"`, `"expectedId":"bad"`, 1), false},
		{strings.Replace(validDeletion, `"deletion":{`, `"identity":{`, 1), false},
		{strings.TrimSuffix(validDeletion, "}") + `,"remove":{"recordType":"edge","externalKey":"old"}}`, false},
		{strings.Replace(validDeletion, `"reason":"removed"`, `"reason":"removed","reason":"again"`, 1), false},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		args := `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","batchId":"b","expectedImportVersion":9007199254740993,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[` + tt.command + `]}`
		_, message := callTool(t, calls, "put_backend_import_batch", args)
		if tt.valid && (message != "" || calls.method != "PUT") {
			t.Fatalf("valid command rejected: %s %s", tt.command, message)
		}
		if !tt.valid && (message == "" || calls.method != "") {
			t.Fatalf("invalid command reached admin: %s", tt.command)
		}
	}
}

func TestBackendReconcileReadToolSelectors(t *testing.T) {
	for _, tt := range []struct{ name, args string }{
		{"compare_backend_revisions", `{"projectId":"` + backendTestID + `","fromRevisionId":"` + backendTestID + `","toRevisionId":"` + backendTestID + `","limit":0}`},
		{"compare_backend_revisions", `{"projectId":"` + backendTestID + `","fromRevisionId":"bad","toRevisionId":"` + backendTestID + `"}`},
		{"get_backend_import_changes", `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","previewVersion":9223372036854775808,"recordType":"source"}`},
		{"get_backend_import_changes", `{"projectId":"` + backendTestID + `","importId":"` + backendTestID + `","previewVersion":null,"recordType":"source"}`},
		{"get_backend_evidence", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","evidenceId":"` + backendTestID + `","subjectId":"` + backendTestID + `"}`},
		{"query_backend_graph", `{"projectId":"` + backendTestID + `","revisionId":"` + backendTestID + `","recordType":"nodes","id":"` + backendTestID + `","search":""}`},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, message := callTool(t, calls, tt.name, tt.args)
		if message == "" || calls.method != "" {
			t.Fatalf("invalid selector reached admin: %s %s", tt.name, tt.args)
		}
	}
}
