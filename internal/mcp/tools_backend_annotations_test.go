package mcp

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendAnnotationToolsStrictInputsAndExactVersions(t *testing.T) {
	const command = `{"type":"create_annotation","annotationId":"` + backendTestID + `","target":{"recordType":"node","id":"` + backendTestID + `"},"body":"note"}`
	for _, tc := range []struct{ name, args, path string }{
		{"list_backend_annotations", `{"projectId":"` + backendTestID + `","orphaned":false,"limit":500}`, "/api/backend-projects/" + backendTestID + "/annotations?limit=500&orphaned=false"},
		{"apply_backend_project_commands", `{"projectId":"` + backendTestID + `","expectedVersion":9007199254740993,"idempotencyKey":"note","commands":[` + command + `]}`, "/api/backend-projects/" + backendTestID + "/commands"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := &recordingCaller{status: 200, body: []byte(`{"projectVersion":9007199254740995}`)}
			raw, msg := callTool(t, calls, tc.name, tc.args)
			if msg != "" || calls.path != tc.path || !strings.Contains(string(raw), "9007199254740995") {
				t.Fatalf("tool %s: %s %s %s", tc.name, calls.path, msg, raw)
			}
			if tc.name == "apply_backend_project_commands" && !strings.Contains(string(calls.sent), `"expectedVersion":9007199254740993`) {
				t.Fatalf("rounded: %s", calls.sent)
			}
		})
	}
	for _, args := range []string{`{"projectId":"` + backendTestID + `","orphaned":null}`, `{"projectId":"` + backendTestID + `","limit":501}`, `{"projectId":"` + backendTestID + `","targetId":"` + backendTestID + `"}`, `{"projectId":"` + backendTestID + `","unknown":true}`, `{"projectId":"` + backendTestID + `","orphaned":false,"orphaned":true}`} {
		calls := &recordingCaller{status: 200, body: []byte(`{}`)}
		_, msg := callTool(t, calls, "list_backend_annotations", args)
		if msg == "" || calls.method != "" {
			t.Fatalf("invalid reached admin: %s %s", args, msg)
		}
	}
}

func TestBackendAnnotationRealSDKRESTParityAndReplay(t *testing.T) {
	server, db := newResourcesTestServer(t, resourcesTestConfig(t))
	call := func(name string, input any, out any) []byte {
		t.Helper()
		raw, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		response, message := callTool(t, server, name, string(raw))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		if out != nil {
			if err := json.Unmarshal(response, out); err != nil {
				t.Fatal(err)
			}
		}
		return response
	}
	var p backendmodel.Project
	call("create_backend_project", backendmodel.CreateInput{Name: "SDK notes", IdempotencyKey: "create"}, &p)
	inventory := []backendmodel.InventoryItem{}
	for category := range strings.FieldsSeq("files endpoints datastores migrations producers consumers jobs contracts tests") {
		inventory = append(inventory, backendmodel.InventoryItem{Category: category, Status: "unsupported", DiscoverySource: "fixture", Gaps: []string{}, Reason: "outside fixture"})
	}
	var session backendmodel.ImportSession
	call("begin_backend_import", map[string]any{"projectId": p.ID, "expectedVersion": p.Version, "baseRevisionId": p.CurrentRevisionID, "idempotencyKey": "begin", "manifest": backendmodel.SourceManifest{RepositoryName: "repo", Provider: backendmodel.SourceProvider{Name: "fixture", Version: "1", Namespace: "annotation", Method: "agent", Profiles: []string{backendmodel.GraphProfile}, Limitations: []string{}}, Snapshot: backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Now().UTC(), Files: []backendmodel.ManifestFile{}}}, "inventory": inventory}, &session)
	commands := []backendmodel.ImportCommand{{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: "unknown", Kind: "unresolved_target", Name: "Unknown", Attributes: map[string]jsontext.Value{"expectedKind": jsontext.Value(`"symbol"`), "reason": jsontext.Value(`"not resolved"`), "searchScope": jsontext.Value(`"repository"`)}, EvidenceKeys: []string{}}}}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	call("put_backend_import_batch", map[string]any{"projectId": p.ID, "importId": session.ID, "batchId": "nodes", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &batch)
	var preview backendmodel.ImportPreview
	call("preview_backend_import", map[string]any{"projectId": p.ID, "importId": session.ID, "baseRevisionId": p.CurrentRevisionID, "expectedImportVersion": batch.AcceptedVersion}, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("preview %+v", preview)
	}
	var imported backendmodel.ImportCommitResult
	call("commit_backend_import", map[string]any{"projectId": p.ID, "importId": session.ID, "expectedVersion": p.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "commit"}, &imported)
	var graph backendmodel.GraphPage
	call("query_backend_graph", map[string]any{"projectId": p.ID, "revisionId": imported.Revision.ID, "recordType": "nodes"}, &graph)
	if len(graph.Nodes) != 1 {
		t.Fatalf("nodes %+v", graph)
	}
	if _, err := db.W.ExecContext(t.Context(), `UPDATE backend_projects SET version=9007199254740993 WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	target := backendmodel.AnnotationTarget{RecordType: "node", ID: graph.Nodes[0].ID}
	input := backendmodel.CommandsInput{ExpectedVersion: 9007199254740993, IdempotencyKey: "annotation", Commands: []backendmodel.Command{{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "<b>plain</b>"}}}
	args := map[string]any{"projectId": p.ID, "expectedVersion": input.ExpectedVersion, "idempotencyKey": input.IdempotencyKey, "commands": input.Commands}
	first := call("apply_backend_project_commands", args, &p)
	if p.Version != 9007199254740994 {
		t.Fatalf("version rounded: %+v", p)
	}
	body, _ := json.Marshal(input)
	status, rest, err := server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "POST", "/api/backend-projects/"+p.ID+"/commands", body)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(rest), bytes.TrimSpace(first)) {
		t.Fatalf("receipt parity %d %s %s %v", status, rest, first, err)
	}
	var page backendmodel.AnnotationPage
	listed := call("list_backend_annotations", map[string]any{"projectId": p.ID, "orphaned": false, "targetId": target.ID, "recordType": "node"}, &page)
	if len(page.Items) != 1 || page.Items[0].Body != "<b>plain</b>" || page.Items[0].Author == "system" {
		t.Fatalf("SDK annotation %+v", page)
	}
	status, rest, err = server.CallAsMCP(t.Context(), httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil), "GET", "/api/backend-projects/"+p.ID+"/annotations?orphaned=false&targetId="+target.ID+"&recordType=node", nil)
	if err != nil || status != 200 || !bytes.Equal(bytes.TrimSpace(rest), bytes.TrimSpace(listed)) {
		t.Fatalf("list parity %d %s %s %v", status, rest, listed, err)
	}
	edit := input.Commands[0]
	edit.Type = "update_annotation"
	edit.Body = "edited"
	call("apply_backend_project_commands", map[string]any{"projectId": p.ID, "expectedVersion": p.Version, "idempotencyKey": "edit", "commands": []backendmodel.Command{edit}}, &p)
	call("apply_backend_project_commands", map[string]any{"projectId": p.ID, "expectedVersion": p.Version, "idempotencyKey": "remove", "commands": []backendmodel.Command{{Type: "remove_annotation", AnnotationID: edit.AnnotationID}}}, &p)
	replay := call("apply_backend_project_commands", args, nil)
	if !bytes.Equal(first, replay) {
		t.Fatalf("lost-response receipt changed %s %s", first, replay)
	}
	call("list_backend_annotations", map[string]any{"projectId": p.ID, "annotationId": edit.AnnotationID}, &page)
	if len(page.Items) != 0 {
		t.Fatalf("removed note remains %+v", page)
	}
}
