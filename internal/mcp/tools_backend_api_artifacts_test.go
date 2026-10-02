package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"os"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendAPIArtifactToolsRoutesAndPrecision(t *testing.T) {
	const id = backendTestID
	const commands = `"commands":[{"type":"set_api_pin","artifactId":"9007199254740993","revisionId":"9223372036854775807","bindings":[{"sourceNodeId":"` + id + `","selector":{"objectKey":"orders"}}],"reason":"exact"}]`
	for _, tc := range []struct{ name, args, method, path string }{
		{"query_backend_api_artifacts", `{"projectId":"` + id + `","revisionId":"` + id + `"}`, "POST", "/api/backend-projects/" + id + "/api-artifacts/query"},
		{"preview_backend_api_pins", `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":9007199254740993,` + commands + `}`, "POST", "/api/backend-projects/" + id + "/api-artifacts/preview"},
		{"apply_backend_api_pins", `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":9007199254740993,` + commands + `,"candidateHash":"` + strings.Repeat("a", 64) + `","idempotencyKey":"apply"}`, "POST", "/api/backend-projects/" + id + "/api-artifacts/commands"},
		{"get_api_artifact_snapshot", `{"artifactId":"9007199254740993","revisionId":"9223372036854775807"}`, "GET", "/api/designs/9007199254740993/revisions/9223372036854775807/artifact-snapshot"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte("{\n \"version\":9007199254740995\n}")}
			out, msg := callTool(t, calls, tc.name, tc.args)
			if msg != "" {
				t.Fatal(msg)
			}
			if calls.method != tc.method || calls.path != tc.path {
				t.Fatal(calls.method, calls.path)
			}
			if !strings.Contains(string(out), "9007199254740995") {
				t.Fatal("rounded result", string(out))
			}
			if strings.Contains(tc.args, "expectedVersion") && !strings.Contains(string(calls.sent), "9007199254740993") {
				t.Fatal("rounded CAS", string(calls.sent))
			}
			if tc.name == "query_backend_api_artifacts" && !strings.Contains(string(calls.sent), `"revisionId":"`+id+`"`) {
				t.Fatal("body revision removed")
			}
		})
	}
}

func TestBackendAPIArtifactToolsStrictAdmissionAndRawCarrier(t *testing.T) {
	const id = backendTestID
	const valid = `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":1,"commands":[{"type":"set_api_pin","artifactId":"9007199254740993","revisionId":"9223372036854775807","bindings":[{"sourceNodeId":"` + id + `","selector":{"objectKey":"orders"}}],"reason":"manual"}]}`
	for _, tc := range []struct{ old, next string }{
		{`"reason":"manual"`, `"reason":null`},
		{`"objectKey":"orders"`, `"objectKey":"orders","jsonPointer":"/components/schemas/A"`},
		{`"objectKey":"orders"`, `"objectKey":"orders","objectKey":"orders"`},
		{`"objectKey":"orders"`, `"objectKey":"orders","unknown":1`},
		{`"artifactId":"9007199254740993"`, `"artifactId":9007199254740993`},
		{`"artifactId":"9007199254740993"`, `"artifactId":"09223372036854775807"`},
		{`"revisionId":"9223372036854775807"`, `"revisionId":"9223372036854775808"`},
		{`"expectedVersion":1`, `"expectedVersion":"1"`},
		{`"expectedVersion":1`, `"expectedVersion":null`},
		{`"reason":"manual"`, `"reason":"manual","unknown":true`},
		{`"reason":"manual"`, `"reason":"` + strings.Repeat("é", 2049) + `"`},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "preview_backend_api_pins", strings.Replace(valid, tc.old, tc.next, 1))
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid input reached admin: %s => %s", tc.old, tc.next)
		}
	}
	for _, value := range []string{`1`, `"01"`, `"0"`, `"9223372036854775808"`, `null`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "get_api_artifact_snapshot", `{"artifactId":`+value+`,"revisionId":"1"}`)
		if msg == "" || calls.method != "" {
			t.Fatal("invalid exact ID admitted", value)
		}
	}
	calls := &recordingCaller{status: 200, body: []byte("{\n \"version\":9007199254740995, \"name\":\"<literal>&\"\n}\n")}
	endpoint := New(calls, testKey, testConfig(), nil).Handler()
	rec := doMCP(t, endpoint, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"preview_backend_api_pins","arguments":`+valid+`}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Content []struct {
				Text string `json:"text"`
			} `json:"content"`
			StructuredContent jsontext.Value `json:"structuredContent"`
		} `json:"result"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	if len(env.Result.Content) != 1 || env.Result.Content[0].Text != string(calls.body) {
		t.Fatalf("MCP text carrier changed raw body: %s", rec.Body.String())
	}
	if !strings.Contains(string(env.Result.StructuredContent), "9007199254740995") {
		t.Fatal("rounded structured carrier")
	}
}

func TestBackendAPIArtifactToolsPublishSchemasAndHints(t *testing.T) {
	response := doMCP(t, newTestEndpoint(t).Handler(), `{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}`, map[string]string{"Authorization": "Bearer " + testKey})
	var env struct {
		Result struct {
			Tools []struct {
				Name        string         `json:"name"`
				InputSchema map[string]any `json:"inputSchema"`
				Annotations struct {
					ReadOnlyHint   bool `json:"readOnlyHint"`
					IdempotentHint bool `json:"idempotentHint"`
				} `json:"annotations"`
			} `json:"tools"`
		} `json:"result"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &env); err != nil {
		t.Fatal(err)
	}
	expected := map[string]bool{"query_backend_api_artifacts": true, "preview_backend_api_pins": true, "apply_backend_api_pins": false, "get_api_artifact_snapshot": true}
	if len(env.Result.Tools) != 194 {
		t.Fatal("surface count", len(env.Result.Tools))
	}
	for _, tool := range env.Result.Tools {
		readOnly, ok := expected[tool.Name]
		if !ok {
			continue
		}
		if tool.Annotations.ReadOnlyHint != readOnly || !tool.Annotations.IdempotentHint {
			t.Fatal("wrong tool hints", tool.Name)
		}
		delete(expected, tool.Name)
	}
	if len(expected) != 0 {
		t.Fatal("missing tools", expected)
	}
}

func TestBackendAPIArtifactToolsPublicParity(t *testing.T) {
	cfg := resourcesTestConfig(t)
	server, db := newResourcesTestServer(t, cfg)
	owner := apidesign.NewRepo(db, cfg)
	for _, table := range []string{"api_designs", "api_design_revisions"} {
		if _, err := db.W.ExecContext(t.Context(), `INSERT INTO sqlite_sequence(name,seq) VALUES(?,9007199254740992)`, table); err != nil {
			t.Fatal(err)
		}
	}
	const document = `{"openapi":"3.1.0","info":{"title":"Exact","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"exact","responses":{"200":{"description":"OK"}}}}}}`
	design, err := owner.Create(t.Context(), apidesign.CreateInput{Name: "Wide API", Document: document, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		response, msg := callTool(t, server, name, string(raw))
		if msg != "" {
			t.Fatalf("%s %s", name, msg)
		}
		if out != nil {
			if e = json.Unmarshal(response, out); e != nil {
				t.Fatal(e)
			}
		}
		return response
	}
	var snapshot struct {
		ArtifactID  string `json:"artifactId"`
		RevisionID  string `json:"revisionId"`
		Document    string `json:"document"`
		ContentHash string `json:"contentHash"`
	}
	call("get_api_artifact_snapshot", map[string]string{"artifactId": "9007199254740993", "revisionId": "9007199254740993"}, &snapshot)
	if snapshot.ArtifactID != "9007199254740993" || snapshot.RevisionID != "9007199254740993" || snapshot.Document != design.Draft.Document || snapshot.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(snapshot.Document))) {
		t.Fatal("snapshot IDs/raw owner bytes changed", snapshot)
	}
	// Create source4 using existing inert lineage provider fixture, independently
	// of backendmodel's private test helpers and through public tools.
	var project backendmodel.Project
	call("create_backend_project", map[string]any{"name": "SDK pins", "idempotencyKey": "sdk-project"}, &project)
	source, err := os.ReadFile("../backendmodel/testdata/lineage/orders/source.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	inventory := []backendmodel.InventoryItem{}
	for category := range strings.FieldsSeq("files endpoints datastores migrations producers consumers jobs contracts tests") {
		item := backendmodel.InventoryItem{Category: category, Status: "unsupported", DiscoverySource: "fixture", Gaps: []string{}, Reason: "outside provider"}
		if slices.Contains([]string{"files", "endpoints", "datastores"}, category) {
			item.KnownCount = 1
			item.Denominator = new(int64(1))
		}
		inventory = append(inventory, item)
	}
	var session backendmodel.ImportSession
	begin := map[string]any{"projectId": project.ID, "expectedVersion": project.Version, "baseRevisionId": project.CurrentRevisionID, "idempotencyKey": "sdk-begin", "profile": "field-lineage-v1", "manifest": map[string]any{"repositoryName": "orders", "provider": map[string]any{"name": "fixture", "version": "1", "namespace": "sdk", "method": "agent", "profiles": []string{"foundation-graph-v1", "relational-graph-v1", "runtime-flow-v1", "field-lineage-v1"}, "limitations": []string{}}, "snapshot": map[string]any{"commit": "abc", "dirty": false, "consistency": "verified", "capturedAt": "2026-10-01T10:00:00Z", "files": []backendmodel.ManifestFile{{Path: "source.go.txt", ContentHash: fmt.Sprintf("%x", sha256.Sum256(source)), FileType: "go", AnalysisStatus: "analyzed"}}}}, "inventory": inventory}
	call("begin_backend_import", begin, &session)
	raw, err := os.ReadFile("../backendmodel/testdata/lineage/orders/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(raw)))
	var commands []backendmodel.ImportCommand
	if err = json.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "fixture", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &batch)
	var preview backendmodel.ImportPreview
	call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "baseRevisionId": project.CurrentRevisionID, "expectedImportVersion": batch.AcceptedVersion}, &preview)
	if preview.CandidateHash == nil {
		t.Fatal("fixture not ready", preview)
	}
	var imported backendmodel.ImportCommitResult
	call("commit_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "sdk-commit"}, &imported)
	var graph backendmodel.GraphPage
	call("query_backend_graph", map[string]any{"projectId": project.ID, "revisionId": imported.Revision.ID, "recordType": "nodes"}, &graph)
	node := ""
	for _, n := range graph.Nodes {
		if n.ExternalKey == "http" {
			node = n.ID
		}
	}
	if node == "" {
		t.Fatal("missing fixture endpoint")
	}
	pin := map[string]any{"projectId": project.ID, "baseRevisionId": imported.Revision.ID, "expectedVersion": imported.Project.Version, "commands": []backendmodel.APIPinCommand{{Type: "set_api_pin", ArtifactID: snapshot.ArtifactID, RevisionID: snapshot.RevisionID, Reason: "manual", Bindings: []backendmodel.APIPinBindingInput{{SourceNodeID: node, Selector: backendmodel.APIArtifactSelector{ObjectKey: "exact"}}}}}}
	var pinPreview backendmodel.APIPinsPreview
	call("preview_backend_api_pins", pin, &pinPreview)
	if !pinPreview.CanApply {
		t.Fatal("blocked", pinPreview)
	}
	// Direct REST loopback shares the exact DTO body after stripping projectId.
	restInput := backendmodel.PreviewAPIPinsInput{BaseRevisionID: imported.Revision.ID, ExpectedVersion: imported.Project.Version, Commands: pin["commands"].([]backendmodel.APIPinCommand)}
	body, err := json.Marshal(restInput)
	if err != nil {
		t.Fatal(err)
	}
	status, rest, err := server.CallAsMCP(t.Context(), httptest.NewRequest("POST", "http://mocker.local/mcp", nil), "POST", "/api/backend-projects/"+project.ID+"/api-artifacts/preview", body)
	if err != nil || status != 200 {
		t.Fatal(status, string(rest), err)
	}
	var restPreview backendmodel.APIPinsPreview
	if err = json.Unmarshal(rest, &restPreview); err != nil {
		t.Fatal(err)
	}
	if restPreview.CandidateHash != pinPreview.CandidateHash || restPreview.SemanticHash != pinPreview.SemanticHash {
		t.Fatal("REST/MCP candidate parity")
	}
	pin["candidateHash"] = pinPreview.CandidateHash
	pin["idempotencyKey"] = "sdk-apply"
	var result backendmodel.APIPinsResult
	first := call("apply_backend_api_pins", pin, &result)
	var page backendmodel.APIArtifactPage
	call("query_backend_api_artifacts", map[string]any{"projectId": project.ID, "revisionId": result.Revision.ID}, &page)
	if len(page.Items) != 1 || page.Items[0].Binding.Ref.ArtifactID != snapshot.ArtifactID || page.Items[0].Binding.Ref.RevisionID != snapshot.RevisionID {
		t.Fatal("exact pins rounded/lost", page)
	}
	call("apply_backend_project_commands", map[string]any{"projectId": project.ID, "expectedVersion": result.Project.Version, "idempotencyKey": "sdk-advance", "commands": []backendmodel.Command{{Type: "rename_project", Name: "Advanced"}}}, nil)
	replay := call("apply_backend_api_pins", pin, nil)
	if !bytes.Equal(first, replay) {
		t.Fatal("MCP receipt replay changed")
	}
	// Independently simulate a disappeared source in this disposable immutable
	// fixture. E7 covers genuine explicit deletion through source reconciliation.
	if _, err = db.W.ExecContext(t.Context(), `DELETE FROM backend_graph_records WHERE revision_id=? AND record_type='node' AND id=?`, result.Revision.ID, node); err != nil {
		t.Fatal(err)
	}
	call("query_backend_api_artifacts", map[string]any{"projectId": project.ID, "revisionId": result.Revision.ID, "sourceNodeId": node}, &page)
	if len(page.Items) != 1 || page.Items[0].Resolution.Status != "orphaned" || page.Items[0].Binding.SourceNodeID != node || page.Items[0].Binding.SourceLastKnownLabel == "" {
		t.Fatal("source filter hid orphan", page)
	}
}
