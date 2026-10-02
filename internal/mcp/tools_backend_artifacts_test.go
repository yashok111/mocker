package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/jsonrpc"
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestBackendArtifactToolsRoutesAndPrecision(t *testing.T) {
	const id = backendTestID
	const commands = `"commands":[{"type":"set_artifact_pin","artifact":{"kind":"api_design","id":"9007199254740993"},"revisionId":"9223372036854775807","editorBindings":[],"apiBindings":[{"sourceNodeId":"` + id + `","selector":{"objectKey":"orders"}}],"reason":"exact"}]`
	for _, tc := range []struct{ name, args, method, path string }{
		{"query_backend_artifacts", `{"projectId":"` + id + `","revisionId":"` + id + `","artifact":{"kind":"api_design","id":"9007199254740993"},"view":"states"}`, "POST", "/api/backend-projects/" + id + "/artifacts/query"},
		{"preview_backend_artifact_pins", `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":9007199254740993,` + commands + `}`, "POST", "/api/backend-projects/" + id + "/artifacts/preview"},
		{"apply_backend_artifact_pins", `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":9007199254740993,` + commands + `,"candidateHash":"` + strings.Repeat("a", 64) + `","idempotencyKey":"apply"}`, "POST", "/api/backend-projects/" + id + "/artifacts/commands"},
		{"get_design_scenario_artifact_snapshot", `{"scenarioId":"9007199254740993","revisionId":"9223372036854775807"}`, "GET", "/api/design-scenarios/9007199254740993/revisions/9223372036854775807/artifact-snapshot"},
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
			if tc.name == "query_backend_artifacts" && !strings.Contains(string(calls.sent), `"revisionId":"`+id+`"`) {
				t.Fatal("body revision removed")
			}
		})
	}
}

func TestBackendArtifactToolsStrictAdmissionAndRawCarrier(t *testing.T) {
	const id = backendTestID
	const valid = `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":1,"commands":[{"type":"set_artifact_pin","artifact":{"kind":"api_design","id":"9007199254740993"},"revisionId":"9223372036854775807","editorBindings":[],"apiBindings":[{"sourceNodeId":"` + id + `","selector":{"objectKey":"orders"}}],"reason":"manual"}]}`
	for _, tc := range []struct{ old, next string }{
		{`"reason":"manual"`, `"reason":null`},
		{`"objectKey":"orders"`, `"objectKey":"orders","jsonPointer":"/components/schemas/A"`},
		{`"objectKey":"orders"`, `"objectKey":"orders","objectKey":"orders"`},
		{`"objectKey":"orders"`, `"objectKey":"orders","unknown":1`},
		{`"id":"9007199254740993"`, `"id":9007199254740993`},
		{`"id":"9007199254740993"`, `"id":"09223372036854775807"`},
		{`"revisionId":"9223372036854775807"`, `"revisionId":"9223372036854775808"`},
		{`"expectedVersion":1`, `"expectedVersion":"1"`},
		{`"expectedVersion":1`, `"expectedVersion":null`},
		{`"reason":"manual"`, `"reason":"manual","unknown":true`},
		{`"reason":"manual"`, `"reason":"` + strings.Repeat("é", 2049) + `"`},
	} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "preview_backend_artifact_pins", strings.Replace(valid, tc.old, tc.next, 1))
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid input reached admin: %s => %s", tc.old, tc.next)
		}
	}
	for _, value := range []string{`1`, `"01"`, `"0"`, `"9223372036854775808"`, `null`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "get_design_scenario_artifact_snapshot", `{"scenarioId":`+value+`,"revisionId":"1"}`)
		if msg == "" || calls.method != "" {
			t.Fatal("invalid exact ID admitted", value)
		}
	}
	calls := &recordingCaller{status: 200, body: []byte("{\n \"version\":9007199254740995, \"name\":\"<literal>&\"\n}\n")}
	endpoint := New(calls, testKey, testConfig(), nil).Handler()
	rec := doMCP(t, endpoint, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"preview_backend_artifact_pins","arguments":`+valid+`}}`, map[string]string{"Authorization": "Bearer " + testKey})
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

func TestBackendArtifactToolsPublishSchemasAndHints(t *testing.T) {
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
	expected := map[string]bool{"query_backend_artifacts": true, "preview_backend_artifact_pins": true, "apply_backend_artifact_pins": false, "get_design_scenario_artifact_snapshot": true}
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
func TestBackendArtifactToolsPublicParity(t *testing.T) {
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
	var lastText string
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		handler := New(server, testKey, testConfig(), nil).Handler()
		rec := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(raw)+`}}`, map[string]string{"Authorization": "Bearer " + testKey})
		var env struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				StructuredContent jsontext.Value `json:"structuredContent"`
			} `json:"result"`
		}
		if e = json.Unmarshal(rec.Body.Bytes(), &env); e != nil {
			t.Fatal(e)
		}
		if len(env.Result.Content) != 1 || env.Result.IsError {
			t.Fatalf("%s %s", name, rec.Body.String())
		}
		lastText = env.Result.Content[0].Text
		response := []byte(env.Result.StructuredContent)
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

	scenarioOwner := designscenario.NewRepo(db, cfg, owner)
	for _, table := range []string{"design_scenarios", "design_scenario_revisions"} {
		if _, err := db.W.ExecContext(t.Context(), `INSERT INTO sqlite_sequence(name,seq) VALUES(?,9007199254740992)`, table); err != nil {
			t.Fatal(err)
		}
	}
	doc := designscenario.Document{FormatVersion: 3, Title: "MCP exact", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "c", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{{ID: "m", FromID: "c", ToID: "p", Kind: "request", Label: "Request"}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	scenario, err := scenarioOwner.Create(t.Context(), designscenario.CreateInput{Document: doc, FormDrafts: map[string]string{"p": "inert unfinished form"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	artifact := backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(scenario.Scenario.ID, 10)}
	var scenarioSnapshot map[string]jsontext.Value
	snapshotArgs := map[string]any{"scenarioId": artifact.ID, "revisionId": strconv.FormatInt(scenario.Draft.ID, 10)}
	snapshotRaw := call("get_design_scenario_artifact_snapshot", snapshotArgs, &scenarioSnapshot)
	restPath := "/api/design-scenarios/" + artifact.ID + "/revisions/" + strconv.FormatInt(scenario.Draft.ID, 10) + "/artifact-snapshot"
	status, rest, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "GET", restPath, nil)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(snapshotRaw), bytes.TrimSpace(rest)) {
		t.Fatal("exact snapshot REST/MCP parity", status, string(rest), err)
	}
	pin := map[string]any{"projectId": project.ID, "baseRevisionId": imported.Revision.ID, "expectedVersion": imported.Project.Version, "commands": []backendmodel.ArtifactPinCommand{
		{Type: "set_artifact_pin", Artifact: artifact, RevisionID: strconv.FormatInt(scenario.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{node}}, {Selector: backendmodel.EditorSelector{Kind: "sequence_message", MessageID: "m"}, SourceNodeIDs: []string{node}}}, Reason: "manual"},
		{Type: "set_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "api_design", ID: snapshot.ArtifactID}, RevisionID: snapshot.RevisionID, APIBindings: []backendmodel.APIPinBindingInput{{SourceNodeID: node, Selector: backendmodel.APIArtifactSelector{ObjectKey: "exact"}}}, EditorBindings: []backendmodel.EditorBindingInput{}, Reason: "manual"},
	}}
	var pinPreview backendmodel.ArtifactPinsPreview
	call("preview_backend_artifact_pins", pin, &pinPreview)
	if !pinPreview.CanApply || len(pinPreview.Pins) != 2 || len(pinPreview.EditorBindings) != 2 || len(pinPreview.APIBindings) != 1 {
		t.Fatal("blocked", pinPreview)
	}
	body, err := json.Marshal(backendmodel.PreviewArtifactPinsInput{BaseRevisionID: imported.Revision.ID, ExpectedVersion: imported.Project.Version, Commands: pin["commands"].([]backendmodel.ArtifactPinCommand)})
	if err != nil {
		t.Fatal(err)
	}
	root := "/api/backend-projects/" + project.ID + "/artifacts"
	status, rest, err = server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", root+"/preview", body)
	var restPreview backendmodel.ArtifactPinsPreview
	if err != nil || status != 200 {
		t.Fatal(status, string(rest), err)
	}
	if err = json.Unmarshal(rest, &restPreview); err != nil {
		t.Fatal(err)
	}
	if restPreview.CandidateHash != pinPreview.CandidateHash || restPreview.SemanticHash != pinPreview.SemanticHash {
		t.Fatal("REST/MCP candidate parity")
	}
	pin["candidateHash"] = pinPreview.CandidateHash
	pin["idempotencyKey"] = "mcp-exact"
	var result backendmodel.ArtifactPinsResult
	first := call("apply_backend_artifact_pins", pin, &result)
	firstText := lastText
	var page backendmodel.ArtifactProjectionPage
	query := map[string]any{"projectId": project.ID, "revisionId": result.Revision.ID, "artifact": artifact, "view": "sequence", "limit": 1}
	call("query_backend_artifacts", query, &page)
	if !page.BindingsComplete || len(page.EditorBindings) != 2 || len(page.Items) != 1 || page.SelectedPin.ID != artifact.ID {
		t.Fatal("paged roster", page)
	}
	rosterBefore, err := json.Marshal(page.EditorBindings)
	if err != nil {
		t.Fatal(err)
	}
	call("apply_backend_project_commands", map[string]any{"projectId": project.ID, "expectedVersion": result.Project.Version, "idempotencyKey": "mcp-advance", "commands": []backendmodel.Command{{Type: "rename_project", Name: "Advanced"}}}, nil)
	replay := call("apply_backend_artifact_pins", pin, nil)
	if !bytes.Equal(first, replay) || lastText != firstText {
		t.Fatal("MCP receipt replay changed")
	}
	stored := "\n " + firstText + "\n"
	if _, err = db.W.ExecContext(t.Context(), `UPDATE backend_command_receipts SET response=? WHERE scope=? AND key=?`, stored, "artifact-pins:"+project.ID, "mcp-exact"); err != nil {
		t.Fatal(err)
	}
	// Simulate owner disappearance only in this disposable synthetic database.
	if _, err = db.W.ExecContext(t.Context(), `PRAGMA foreign_keys=OFF`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.W.ExecContext(t.Context(), `DELETE FROM design_scenarios WHERE id=?`, scenario.Scenario.ID); err != nil {
		t.Fatal(err)
	}
	replay = call("apply_backend_artifact_pins", pin, nil)
	if !bytes.Equal(first, replay) || lastText != stored {
		t.Fatal("MCP dependency-free receipt replay changed")
	}
	call("query_backend_artifacts", query, &page)
	rosterAfter, err := json.Marshal(page.EditorBindings)
	if err != nil || !bytes.Equal(rosterBefore, rosterAfter) {
		t.Fatal("missing owner changed full frozen roster", err)
	}
	if !page.BindingsComplete || len(page.EditorBindings) != 2 || page.Resolution.Status != "unavailable" {
		t.Fatal("missing owner lost roster", page)
	}
	if _, err = db.W.ExecContext(t.Context(), `DELETE FROM backend_projects WHERE id=?`, project.ID); err != nil {
		t.Fatal(err)
	}
	replay = call("apply_backend_artifact_pins", pin, nil)
	if !bytes.Equal(first, replay) || lastText != stored {
		t.Fatal("MCP receipt depended on live project")
	}

}

func TestBackendArtifactToolsWholeBodyAndEditorUnion(t *testing.T) {
	const id = backendTestID
	valid := `{"projectId":"` + id + `","baseRevisionId":"` + id + `","expectedVersion":1,"commands":[{"type":"set_artifact_pin","artifact":{"kind":"design_scenario","id":"9007199254740993"},"revisionId":"9007199254740993","editorBindings":[{"selector":{"kind":"participant","participantId":"p"},"sourceNodeIds":["` + id + `"]}],"reason":"manual"}]}`
	cases := []string{strings.Replace(valid, `"editorBindings":`, `"apiBindings":[],"editorBindings":`, 1), strings.Replace(valid, `"sourceNodeIds":["`+id+`"]`, `"sourceNodeIds":null`, 1), strings.Replace(valid, `"participantId":"p"`, `"participantId":null`, 1), strings.Replace(valid, `"participantId":"p"`, `"participantId":"p","messageId":"m"`, 1), strings.Replace(valid, `"sourceNodeIds":`, `"origin":"manual","sourceNodeIds":`, 1), strings.Replace(valid, `"participantId":"p"`, `"participantId":"p","participantId":"p"`, 1), "{" + strings.Repeat(" ", backendmodel.MaxAPIPinBodyBytes) + valid[1:]}
	for _, raw := range cases {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "preview_backend_artifact_pins", raw)
		if msg == "" || calls.method != "" {
			t.Fatal("invalid union/body forwarded", len(raw), msg)
		}
	}
}

func TestBackendArtifactApplyReservationSDKFraming(t *testing.T) {
	const id = backendTestID
	input := map[string]any{"projectId": id, "baseRevisionId": id, "expectedVersion": 1, "commands": []backendmodel.ArtifactPinCommand{{Type: "remove_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "design_scenario", ID: "1"}, Reason: "x"}}, "candidateHash": strings.Repeat("a", 64), "idempotencyKey": strings.Repeat(`\`, backendmodel.MaxKeyLength)}
	arguments, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	for _, rpcID := range []float64{1, math.MinInt64} {
		id, err := jsonrpc.MakeID(rpcID)
		if err != nil {
			t.Fatal(err)
		}
		params, err := json.Marshal(sdk.CallToolParams{Name: "apply_backend_artifact_pins", Arguments: jsontext.Value(arguments)})
		if err != nil {
			t.Fatal(err)
		}
		body, err := jsonrpc.EncodeMessage(&jsonrpc.Request{ID: id, Method: "tools/call", Params: params})
		if err != nil {
			t.Fatal(err)
		}
		overhead := len(body) - len(arguments)
		if rpcID == 1 && overhead != 107 || rpcID == math.MinInt64 && int64(overhead) != backendmodel.ArtifactApplyRPCFramingBytes {
			t.Fatal("actual SDK framing reserve drift", rpcID, overhead)
		}
		cfg := testConfig()
		cfg.MaxBody = int64(len(body))
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		rec := doMCP(t, New(calls, testKey, cfg, nil).Handler(), string(body), map[string]string{"Authorization": "Bearer " + testKey})
		if rec.Code != 200 || calls.method == "" {
			t.Fatal("exact canonical full-RPC boundary", rec.Code, rec.Body.String())
		}
		cfg.MaxBody--
		calls = &recordingCaller{status: 200, body: []byte(`{}`)}
		rec = doMCP(t, New(calls, testKey, cfg, nil).Handler(), string(body), map[string]string{"Authorization": "Bearer " + testKey})
		if rec.Code != 413 || calls.method != "" {
			t.Fatal("original global SDK cap changed", rec.Code, rec.Body.String())
		}
	}
}

func TestBackendArtifactApplyReservationCompactMCP(t *testing.T) {
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
	var lastText string
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		handler := New(server, testKey, testConfig(), nil).Handler()
		rec := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(raw)+`}}`, map[string]string{"Authorization": "Bearer " + testKey})
		var env struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				StructuredContent jsontext.Value `json:"structuredContent"`
			} `json:"result"`
		}
		if e = json.Unmarshal(rec.Body.Bytes(), &env); e != nil {
			t.Fatal(e)
		}
		if len(env.Result.Content) != 1 || env.Result.IsError {
			t.Fatalf("%s %s", name, rec.Body.String())
		}
		lastText = env.Result.Content[0].Text
		response := []byte(env.Result.StructuredContent)
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

	scenarioOwner := designscenario.NewRepo(db, cfg, owner)
	doc := designscenario.Document{FormatVersion: 3, Title: "Compact boundary", Participants: []designscenario.Participant{}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	for i, n := range []int{50000, 50000, 30000} {
		doc.Participants = append(doc.Participants, designscenario.Participant{ID: strings.Repeat(string(rune('a'+i)), n), Name: strconv.Itoa(i), Kind: "service"})
	}
	d, err := scenarioOwner.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := backendmodel.PreviewArtifactPinsInput{BaseRevisionID: imported.Revision.ID, ExpectedVersion: imported.Project.Version, Commands: []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(d.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(d.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{}, Reason: "x"}}}
	for _, p := range doc.Participants {
		in.Commands[0].EditorBindings = append(in.Commands[0].EditorBindings, backendmodel.EditorBindingInput{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: p.ID}, SourceNodeIDs: []string{node}})
	}
	envelope := func() map[string]any {
		return map[string]any{"projectId": project.ID, "baseRevisionId": in.BaseRevisionID, "expectedVersion": in.ExpectedVersion, "commands": in.Commands}
	}
	raw, err = json.Marshal(envelope())
	if err != nil {
		t.Fatal(err)
	}
	delta := backendmodel.MaxAPIPinBodyBytes - 1 - len(raw)
	doc.Participants[2].ID += strings.Repeat("c", delta)
	in.Commands[0].EditorBindings[2].Selector.ParticipantID = doc.Participants[2].ID
	d, err = scenarioOwner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in.Commands[0].RevisionID = strconv.FormatInt(d.Draft.ID, 10)
	raw, err = json.Marshal(envelope())
	if err != nil || len(raw) != backendmodel.MaxAPIPinBodyBytes-1 {
		t.Fatal("compact MCP arguments", len(raw), err)
	}
	before, err := scenarioOwner.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	_, msg := callTool(t, server, "preview_backend_artifact_pins", string(raw))
	restBody, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	status, rest, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", "/api/backend-projects/"+project.ID+"/artifacts/preview", restBody)
	if err != nil || status != 413 || !strings.Contains(msg, "backend_artifact_apply_body_limit") || strings.TrimPrefix(msg, "HTTP 413: ") != string(rest) {
		t.Fatal("REST/MCP reserved envelope parity", status, string(rest), msg, err)
	}
	after, err := scenarioOwner.ArtifactInspectionSnapshot(t.Context(), d.Scenario.ID, d.Draft.ID)
	if err != nil || before.DocumentJSON != after.DocumentJSON || before.FormDraftsJSON != after.FormDraftsJSON || before.StoredContentHash != after.StoredContentHash || before.Version != after.Version {
		t.Fatal("MCP reservation wrote owner", err)
	}
	p, err := backendmodel.NewRepo(db).Get(t.Context(), project.ID)
	if err != nil || p.Version != imported.Project.Version || p.CurrentRevisionID != imported.Revision.ID {
		t.Fatal("MCP reservation wrote backend", err)
	}
	_ = lastText
}
func TestBackendArtifactApplyReservationConfiguredMCP(t *testing.T) {
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
	var lastText string
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, e := json.Marshal(input)
		if e != nil {
			t.Fatal(e)
		}
		handler := New(server, testKey, testConfig(), nil).Handler()
		rec := doMCP(t, handler, `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"`+name+`","arguments":`+string(raw)+`}}`, map[string]string{"Authorization": "Bearer " + testKey})
		var env struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
				StructuredContent jsontext.Value `json:"structuredContent"`
			} `json:"result"`
		}
		if e = json.Unmarshal(rec.Body.Bytes(), &env); e != nil {
			t.Fatal(e)
		}
		if len(env.Result.Content) != 1 || env.Result.IsError {
			t.Fatalf("%s %s", name, rec.Body.String())
		}
		lastText = env.Result.Content[0].Text
		response := []byte(env.Result.StructuredContent)
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

	scenarioOwner := designscenario.NewRepo(db, cfg, owner)
	doc := designscenario.Document{FormatVersion: 3, Title: "Global boundary", Participants: []designscenario.Participant{{ID: "p", Name: "Service", Kind: "service"}, {ID: "c", Name: "Client", Kind: "client"}}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	scenario, err := scenarioOwner.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	pinCommands := []backendmodel.ArtifactPinCommand{{Type: "set_artifact_pin", Artifact: backendmodel.ArtifactKey{Kind: "design_scenario", ID: strconv.FormatInt(scenario.Scenario.ID, 10)}, RevisionID: strconv.FormatInt(scenario.Draft.ID, 10), EditorBindings: []backendmodel.EditorBindingInput{{Selector: backendmodel.EditorSelector{Kind: "participant", ParticipantID: "p"}, SourceNodeIDs: []string{node}}}, Reason: "manual"}}
	previewInput := map[string]any{"projectId": project.ID, "baseRevisionId": imported.Revision.ID, "expectedVersion": imported.Project.Version, "commands": pinCommands}
	applyInput := map[string]any{"projectId": project.ID, "baseRevisionId": imported.Revision.ID, "expectedVersion": imported.Project.Version, "commands": pinCommands, "candidateHash": strings.Repeat("a", 64), "idempotencyKey": strings.Repeat(`\`, backendmodel.MaxKeyLength)}
	reserve, err := json.Marshal(applyInput)
	if err != nil {
		t.Fatal(err)
	}
	global := int64(len(reserve)) + backendmodel.ArtifactApplyRPCFramingBytes
	configured := func(limit int64) (*admin.Server, *config.Config) {
		copyCfg := *cfg
		copyCfg.MaxBody = limit
		return admin.New(&copyCfg, auth.NewManager(db, &copyCfg, auth.NewSharedPassword(&copyCfg)), workspaces.NewRepo(db), db, slog.New(slog.NewTextHandler(io.Discard, nil))), &copyCfg
	}
	invoke := func(server *admin.Server, cfg *config.Config, name string, args any, rpcID float64) (string, bool, int) {
		t.Helper()
		argBytes, e := json.Marshal(args)
		if e != nil {
			t.Fatal(e)
		}
		params, e := json.Marshal(sdk.CallToolParams{Name: name, Arguments: jsontext.Value(argBytes)})
		if e != nil {
			t.Fatal(e)
		}
		id, e := jsonrpc.MakeID(rpcID)
		if e != nil {
			t.Fatal(e)
		}
		request, e := jsonrpc.EncodeMessage(&jsonrpc.Request{ID: id, Method: "tools/call", Params: params})
		if e != nil {
			t.Fatal(e)
		}
		rec := doMCP(t, New(server, testKey, cfg, nil).Handler(), string(request), map[string]string{"Authorization": "Bearer " + testKey})
		if rec.Code != 200 {
			t.Fatalf("SDK rejected %s canonicalbody%d/global%d: %d %s", name, len(request), cfg.MaxBody, rec.Code, rec.Body.String())
		}
		var env struct {
			Result struct {
				IsError bool `json:"isError"`
				Content []struct {
					Text string `json:"text"`
				} `json:"content"`
			} `json:"result"`
		}
		if e = json.Unmarshal(rec.Body.Bytes(), &env); e != nil || len(env.Result.Content) != 1 {
			t.Fatal(rec.Body.String(), e)
		}
		return env.Result.Content[0].Text, env.Result.IsError, len(request)
	}
	small, smallCfg := configured(global - 1)
	denied, isError, _ := invoke(small, smallCfg, "preview_backend_artifact_pins", previewInput, 1)
	if !isError || !strings.Contains(denied, "backend_artifact_apply_body_limit") {
		t.Fatal("plus-one global reserve", denied)
	}
	var fault struct {
		Error struct {
			Details struct {
				Allowed  int64 `json:"allowedBytes"`
				Reserved int64 `json:"reservedApplyBytes"`
				Global   int64 `json:"globalMaxBodyBytes"`
				Frame    int64 `json:"rpcFramingBytes"`
				RPC      int64 `json:"reservedRPCBytes"`
			} `json:"details"`
		} `json:"error"`
	}
	if err = json.Unmarshal([]byte(strings.TrimPrefix(denied, "HTTP 413: ")), &fault); err != nil || fault.Error.Details.Allowed != int64(len(reserve))-1 || fault.Error.Details.Reserved != int64(len(reserve)) || fault.Error.Details.Global != global-1 || fault.Error.Details.Frame != 126 || fault.Error.Details.RPC != global {
		t.Fatal("explicit framed budget", denied, err)
	}
	at, atCfg := configured(global)
	previewText, isError, _ := invoke(at, atCfg, "preview_backend_artifact_pins", previewInput, math.MinInt64)
	var pinPreview backendmodel.ArtifactPinsPreview
	if isError || json.Unmarshal([]byte(previewText), &pinPreview) != nil || !pinPreview.CanApply {
		t.Fatal("inclusive framed preview", previewText)
	}
	applyInput["candidateHash"] = pinPreview.CandidateHash
	receipt, isError, requestSize := invoke(at, atCfg, "apply_backend_artifact_pins", applyInput, math.MinInt64)
	if isError || int64(requestSize) != global {
		t.Fatal("maxescaped key did not fit exact canonicalRPC", requestSize, global, receipt)
	}
	replay, isError, _ := invoke(small, smallCfg, "apply_backend_artifact_pins", applyInput, 1)
	if isError || replay != receipt {
		t.Fatal("new framed reservation preceded oldreceipt", replay)
	}
	_ = lastText
}
