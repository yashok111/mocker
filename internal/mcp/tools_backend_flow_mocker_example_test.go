package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"os"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// The endpoint is imported from product source bytes. No imported source or
// reconstructed program is executed; SDK calls use the normal product import/
// read protocol on a separate store, without treating it as a runtime observation.
func TestBackendFlowRealMockerEndpointSourceFixture(t *testing.T) {
	server, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	encode := func(value any) []byte {
		t.Helper()
		raw, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		return raw
	}
	call := func(name string, input map[string]any, out any) {
		t.Helper()
		raw, message := callTool(t, server, name, string(encode(input)))
		if message != "" {
			t.Fatalf("%s: %s", name, message)
		}
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatal(err)
		}
	}
	attrs := func(value map[string]any) map[string]jsontext.Value {
		t.Helper()
		var out map[string]jsontext.Value
		if err := json.Unmarshal(encode(value), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	files := map[string][]byte{}
	for _, path := range []string{"internal/admin/backend_flow_handlers.go", "internal/admin/route_table.go"} {
		raw, err := os.ReadFile("../../" + path)
		if err != nil {
			t.Fatal(err)
		}
		files[path] = raw
	}
	route := `{"POST /api/backend-projects/{id}/flow/query", s.handleQueryBackendFlow, mcpAllow, cpNeverTouchesLayer}`
	native := `out, err := s.backendRepo.QueryFlow(r.Context(), r.PathValue("id"), in)`
	if !bytes.Contains(files["internal/admin/route_table.go"], []byte(route)) || !bytes.Contains(files["internal/admin/backend_flow_handlers.go"], []byte(native)) {
		t.Fatal("expected independently selected route and repository call changed")
	}
	manifestFiles := []backendmodel.ManifestFile{}
	hashes := map[string]string{}
	for _, path := range []string{"internal/admin/backend_flow_handlers.go", "internal/admin/route_table.go"} {
		sum := sha256.Sum256(files[path])
		hashes[path] = hex.EncodeToString(sum[:])
		manifestFiles = append(manifestFiles, backendmodel.ManifestFile{Path: path, ContentHash: hashes[path], FileType: "go", AnalysisStatus: "analyzed"})
	}
	var project backendmodel.Project
	call("create_backend_project", map[string]any{"name": "Mocker source-only flow example", "idempotencyKey": "mocker-example"}, &project)
	in := backendmodel.BeginImportInput{
		Profile: backendmodel.RuntimeProfile, ExpectedVersion: project.Version,
		BaseRevisionID: project.CurrentRevisionID, IdempotencyKey: "mocker-source",
		Manifest: backendmodel.SourceManifest{
			RepositoryName: "mocker-source-example",
			Provider:       backendmodel.SourceProvider{Name: "mocker-source-example", Version: "1", Namespace: "mocker-source-example", Method: "agent", Profiles: []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile}, Limitations: []string{"Selected endpoint only; transitive repository calls not expanded"}},
			Snapshot:       backendmodel.SnapshotManifest{Consistency: "verified", CapturedAt: time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC), Files: manifestFiles},
		},
	}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		n := int64(0)
		if category == "files" {
			n = 2
		} else if category == "endpoints" {
			n = 1
		}
		in.Inventory = append(in.Inventory, backendmodel.InventoryItem{Category: category, Status: "partial", KnownCount: n, DiscoverySource: "Selected endpoint source files", Gaps: []string{"Other product files were not inspected"}})
	}
	var body map[string]any
	if err := json.Unmarshal(encode(in), &body); err != nil {
		t.Fatal(err)
	}
	body["projectId"] = project.ID
	var session backendmodel.ImportSession
	call("begin_backend_import", body, &session)
	commands := []backendmodel.ImportCommand{}
	proof := func(key, kind, path string) {
		lines := int64(bytes.Count(files[path], []byte{'\n'}))
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_evidence", Evidence: &backendmodel.ImportEvidence{ExternalKey: "proof:" + key, SubjectType: kind, SubjectKey: key, Method: "agent", Status: "explicit", Source: backendmodel.EvidenceSource{RepositoryID: session.RepositoryID, SnapshotID: session.SnapshotID, File: path, ContentHash: hashes[path], StartLine: new(int64(1)), EndLine: new(lines)}}})
	}
	node := func(key, kind, parent, path string, attributes map[string]any) {
		var parentKey *string
		if parent != "" {
			parentKey = new(parent)
		}
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_node", Node: &backendmodel.ImportNode{ExternalKey: key, Kind: kind, Name: key, ParentKey: parentKey, Attributes: attrs(attributes), EvidenceKeys: []string{"proof:" + key}}})
		proof(key, "node", path)
	}
	node("operation:flow-query", "http_operation", "", "internal/admin/route_table.go", map[string]any{"method": "POST", "path": "/api/backend-projects/{id}/flow/query"})
	node("handler:flow-query", "handler", "", "internal/admin/backend_flow_handlers.go", map[string]any{"language": "go"})
	node("flow:flow-query", "flow", "handler:flow-query", "internal/admin/backend_flow_handlers.go", map[string]any{"analysisStatus": "partial", "gaps": []string{"Authentication, input guards, error exits and repository body are not expanded"}, "entryStepKey": "step:repository", "exitStepKeys": []string{}, "exitStatus": "unknown"})
	node("step:repository", "flow_step", "flow:flow-query", "internal/admin/backend_flow_handlers.go", map[string]any{"analysisStatus": "unsupported", "gaps": []string{"Repository body is outside this selected source scope"}, "stepKind": "opaque", "inputs": []any{}, "outputs": []any{}, "transactionContext": map[string]any{"status": "unknown", "reason": "Repository internals were not inspected"}, "nativeText": native, "reason": "Transitive repository implementation is not expanded"})
	for _, relation := range []struct{ key, kind, from, to string }{
		{"handles:flow-query", "handles", "operation:flow-query", "handler:flow-query"},
		{"contains:flow-query", "contains", "handler:flow-query", "flow:flow-query"},
		{"contains:repository", "contains", "flow:flow-query", "step:repository"},
	} {
		commands = append(commands, backendmodel.ImportCommand{Op: "upsert_edge", Edge: &backendmodel.ImportEdge{ExternalKey: relation.key, Kind: relation.kind, FromKey: relation.from, ToKey: relation.to, Attributes: map[string]jsontext.Value{}, EvidenceKeys: []string{"proof:" + relation.key}}})
		path := "internal/admin/backend_flow_handlers.go"
		if relation.kind == "handles" {
			path = "internal/admin/route_table.go"
		}
		proof(relation.key, "edge", path)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	var batch backendmodel.BatchReceipt
	call("put_backend_import_batch", map[string]any{"projectId": project.ID, "importId": session.ID, "batchId": "source", "expectedImportVersion": session.Version, "payloadHash": hash, "commands": commands}, &batch)
	ids := map[string]string{}
	for _, identity := range batch.Identities {
		ids[identity.ExternalKey] = identity.ID
	}
	var preview backendmodel.ImportPreview
	call("preview_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedImportVersion": batch.AcceptedVersion, "baseRevisionId": project.CurrentRevisionID}, &preview)
	if preview.CandidateHash == nil {
		t.Fatalf("source example preview: %+v", preview)
	}
	var committed backendmodel.ImportCommitResult
	call("commit_backend_import", map[string]any{"projectId": project.ID, "importId": session.ID, "expectedVersion": project.Version, "expectedImportVersion": preview.Version, "candidateHash": *preview.CandidateHash, "idempotencyKey": "mocker-source-commit"}, &committed)
	var entries backendmodel.FlowPage
	call("query_backend_flow", map[string]any{"projectId": project.ID, "revisionId": committed.Revision.ID, "view": "entrypoints"}, &entries)
	if len(entries.EntryPointItems) != 1 || !slices.Equal(entries.EntryPointItems[0].FlowIDs, []string{ids["flow:flow-query"]}) {
		t.Fatalf("source route/handler/flow mismatch: %+v", entries)
	}
	var steps backendmodel.FlowPage
	call("query_backend_flow", map[string]any{"projectId": project.ID, "revisionId": committed.Revision.ID, "view": "steps", "flowId": ids["flow:flow-query"]}, &steps)
	if len(steps.StepItems) != 1 || !bytes.Equal(steps.StepItems[0].Attributes["nativeText"], encode(native)) {
		t.Fatalf("source native call mismatch: %+v", steps)
	}
	var accesses backendmodel.FlowPage
	call("query_backend_flow", map[string]any{"projectId": project.ID, "revisionId": committed.Revision.ID, "view": "accesses", "entrypointId": ids["operation:flow-query"]}, &accesses)
	if len(accesses.AccessItems) != 0 || len(accesses.Limitations) == 0 {
		t.Fatalf("opaque repository boundary must not invent DB access or complete scope: %+v", accesses)
	}
}
