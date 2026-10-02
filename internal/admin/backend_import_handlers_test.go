package admin

import (
	"bytes"
	"encoding/json/v2"
	"github.com/santhosh-tekuri/jsonschema/v6"
	"github.com/yashok111/mocker/internal/jsonx"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/config"
)

func TestBackendImportRouteInventory(t *testing.T) {
	s := loopbackTestServer(t, nil)
	patterns := []string{"POST /api/backend-projects/{id}/imports", "GET /api/backend-projects/{id}/imports", "GET /api/backend-projects/{id}/imports/{iid}", "PUT /api/backend-projects/{id}/imports/{iid}/batches/{bid}", "POST /api/backend-projects/{id}/imports/{iid}/preview", "POST /api/backend-projects/{id}/imports/{iid}/commit", "POST /api/backend-projects/{id}/imports/{iid}/abort", "POST /api/backend-projects/{id}/graph/query", "GET /api/backend-projects/{id}/revisions/{rid}/nodes/{nid}", "GET /api/backend-projects/{id}/revisions/{rid}/evidence", "GET /api/backend-projects/{id}/revisions/{rid}/coverage"}
	for _, pattern := range patterns {
		found := false
		for _, route := range s.routes() {
			if route.pattern == pattern {
				found = true
			}
		}
		if !found {
			t.Errorf("missing route %s", pattern)
		}
	}
}

func TestBackendImportReadsAndStrictTransport(t *testing.T) {
	s := loopbackTestServer(t, nil)
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v want %d", method, path, status, data, err, want)
		}
		if want == 200 {
			validateBackendImportResponse(t, method, path, data)
		}
		return data
	}
	var p backendmodel.Project
	if err := json.Unmarshal(call("POST", "/api/backend-projects", `{"name":"Import","idempotencyKey":"imports"}`, 201), &p); err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	call("GET", base+"/imports", "", 200)
	call("POST", base+"/graph/query", `{"revisionId":"`+p.CurrentRevisionID+`","recordType":"nodes"}`, 200)
	for _, limit := range []string{"0", "-1", "501"} {
		call("POST", base+"/graph/query", `{"revisionId":"`+p.CurrentRevisionID+`","recordType":"nodes","limit":`+limit+`}`, 400)
	}
	call("POST", base+"/graph/query", `{"revisionId":"`+p.CurrentRevisionID+`","recordType":"nodes","limit":500}`, 200)
	call("GET", base+"/revisions/"+p.CurrentRevisionID+"/evidence", "", 200)
	call("GET", base+"/revisions/"+p.CurrentRevisionID+"/coverage", "", 200)
	for _, suffix := range []string{"?evidenceId=x", "?evidenceId=" + p.ID + "&evidenceId=" + p.ID, "?evidenceId=" + p.ID + "&subjectId=" + p.ID, "?evidenceId=" + p.ID + "&cursor=", "?subjectId=x&subjectId=y", "?limit=501", "?limit=0", "?unknown=x", "?cursor=x&cursor=y"} {
		call("GET", base+"/revisions/"+p.CurrentRevisionID+"/evidence"+suffix, "", 400)
	}
	call("GET", base+"/revisions/"+p.CurrentRevisionID+"/coverage?unknown=x", "", 400)
	for _, input := range []string{`null`, `{"revisionId":"` + p.CurrentRevisionID + `","recordType":"nodes","extra":true}`, `{"recordType":"nodes","recordType":"edges"}`, `{} {}`} {
		call("POST", base+"/graph/query", input, 400)
	}
	call("PUT", base+"/imports/"+p.ID+"/batches/batch", strings.Repeat(" ", backendmodel.MaxImportBatchBytes+1), http.StatusRequestEntityTooLarge)
}

func transportImportFixture(p backendmodel.Project) backendmodel.BeginImportInput {
	in := backendmodel.BeginImportInput{ExpectedVersion: p.Version, BaseRevisionID: p.CurrentRevisionID, IdempotencyKey: "begin"}
	// Decode the fixture through the same strict types used at the HTTP boundary.
	const manifest = `{"repositoryName":"orders","provider":{"name":"collector","version":"1","namespace":"test","method":"ast","profiles":["foundation-graph-v1"],"limitations":[]},"snapshot":{"commit":"abc","dirty":false,"consistency":"verified","capturedAt":"2026-09-30T10:00:00Z","files":[{"path":"main.go","contentHash":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa","fileType":"go","analysisStatus":"analyzed"}]}}`
	if err := json.Unmarshal([]byte(manifest), &in.Manifest); err != nil {
		panic(err)
	}
	for _, category := range strings.Fields("files endpoints datastores migrations producers consumers jobs contracts tests") {
		item := backendmodel.InventoryItem{Category: category, Status: "unsupported", KnownCount: 0, Denominator: nil, DiscoverySource: "collector", Gaps: []string{}, Reason: "outside collector profile"}
		if category == "files" {
			item.Status = "complete"
			item.KnownCount = 1
			item.Denominator = new(int64(1))
			item.Reason = ""
		}
		in.Inventory = append(in.Inventory, item)
	}
	return in
}

func TestBackendImportLifecycleOverCallAsMCP(t *testing.T) {
	s := loopbackTestServer(t, nil)
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		var body []byte
		if input != nil {
			var err error
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, body)
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v want %d", method, path, status, data, err, want)
		}
		if want == 200 {
			validateBackendImportResponse(t, method, path, data)
		}
		return data
	}
	var p backendmodel.Project
	if err := json.Unmarshal(call("POST", "/api/backend-projects", backendmodel.CreateInput{Name: "First Import", IdempotencyKey: "create-import"}, 201), &p); err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	begin := transportImportFixture(p)
	begin.Inventory[1].KnownCount = 9007199254740993
	begin.Inventory[1].Denominator = new(int64(9007199254740994))
	var session backendmodel.ImportSession
	first := call("POST", base+"/imports", begin, 200)
	if !strings.Contains(string(first), `"knownCount":9007199254740993`) {
		t.Fatalf("rounded inventory %s", first)
	}
	if err := json.Unmarshal(first, &session); err != nil {
		t.Fatal(err)
	}
	if replay := call("POST", base+"/imports", begin, 200); string(replay) != string(first) {
		t.Fatal("begin replay changed")
	}
	path := base + "/imports/" + session.ID
	call("GET", base+"/imports?limit=1", nil, 200)
	call("GET", path+"?limit=500", nil, 200)
	var commands []backendmodel.ImportCommand
	raw := `[{"op":"upsert_node","node":{"externalKey":"symbol","kind":"symbol","name":"Execute","attributes":{"description":null},"evidenceKeys":["proof"]}},{"op":"upsert_evidence","evidence":{"externalKey":"proof","subjectType":"node","subjectKey":"symbol","method":"ast","status":"explicit","source":{"repositoryId":"` + session.RepositoryID + `","snapshotId":"` + session.SnapshotID + `","file":"main.go","contentHash":"` + strings.Repeat("a", 64) + `","startLine":9007199254740993,"endLine":9007199254740994},"explanation":"declaration"}}]`
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch := backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands}
	var receipt backendmodel.BatchReceipt
	accepted := call("PUT", path+"/batches/b1", batch, 200)
	if err := json.Unmarshal(accepted, &receipt); err != nil {
		t.Fatal(err)
	}
	var preview backendmodel.ImportPreview
	if err := json.Unmarshal(call("POST", path+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: receipt.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID}, 200), &preview); err != nil {
		t.Fatal(err)
	}
	if preview.State != "ready" || preview.CandidateHash == nil {
		t.Fatalf("preview: %+v", preview)
	}
	commit := backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"}
	var result backendmodel.ImportCommitResult
	committed := call("POST", path+"/commit", commit, 200)
	if err := json.Unmarshal(committed, &result); err != nil {
		t.Fatal(err)
	}
	if replay := call("POST", path+"/commit", commit, 200); string(replay) != string(committed) {
		t.Fatal("commit receipt changed")
	}
	if replay := call("PUT", path+"/batches/b1", batch, 200); string(replay) != string(accepted) {
		t.Fatal("post-commit batch receipt changed")
	}
	var graph backendmodel.GraphPage
	if err := json.Unmarshal(call("POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: result.Revision.ID, RecordType: "nodes", Limit: 1}, 200), &graph); err != nil {
		t.Fatal(err)
	}
	if len(graph.Nodes) != 1 {
		t.Fatalf("graph %+v", graph)
	}
	call("GET", base+"/revisions/"+result.Revision.ID+"/nodes/"+graph.Nodes[0].ID, nil, 200)
	evidence := call("GET", base+"/revisions/"+result.Revision.ID+"/evidence?subjectId="+graph.Nodes[0].ID+"&limit=500", nil, 200)
	if !strings.Contains(string(evidence), `"startLine":9007199254740993`) {
		t.Fatalf("rounded locator: %s", evidence)
	}
	call("GET", base+"/revisions/"+result.Revision.ID+"/coverage", nil, 200)
	initial := call("POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: p.CurrentRevisionID, RecordType: "nodes"}, 200)
	if !strings.Contains(string(initial), `"nodes":[]`) {
		t.Fatalf("initial revision changed %s", initial)
	}
	call("POST", path+"/abort", backendmodel.AbortImportInput{ExpectedImportVersion: preview.Version, IdempotencyKey: "abort-committed"}, 409)
	second := transportImportFixture(result.Project)
	second.IdempotencyKey = "reimport"
	call("POST", base+"/imports", second, 409)
	second.Mode = "reconcile"
	second.RepositoryID = new(session.RepositoryID)
	second.GraphScope = &backendmodel.GraphScope{Profile: backendmodel.GraphProfile, Status: "partial", Gaps: []string{"collector scope partial"}}
	var repeated backendmodel.ImportSession
	if err := json.Unmarshal(call("POST", base+"/imports", second, 200), &repeated); err != nil {
		t.Fatal(err)
	}
	if repeated.RepositoryID != session.RepositoryID || repeated.SnapshotID == session.SnapshotID {
		t.Fatalf("repeat identities: %+v", repeated)
	}
	var repeatedPreview backendmodel.ImportPreview
	if err := json.Unmarshal(call("POST", base+"/imports/"+repeated.ID+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: repeated.Version, BaseRevisionID: result.Revision.ID}, 200), &repeatedPreview); err != nil {
		t.Fatal(err)
	}
	for _, recordType := range []string{"source", "identity", "deletion"} {
		call("GET", base+"/imports/"+repeated.ID+"/changes?previewVersion="+strconv.FormatInt(repeatedPreview.Version, 10)+"&recordType="+recordType, nil, 200)
	}
	call("GET", base+"/imports/"+repeated.ID+"/changes?previewVersion="+strconv.FormatInt(repeatedPreview.Version-1, 10)+"&recordType=source", nil, 409)
	// Reopen the same SQLite file and verify saved-preview pages remain pinned.
	dataDir := s.cfg.DataDir
	if err := s.db.Close(); err != nil {
		t.Fatal(err)
	}
	s = loopbackTestServer(t, func(cfg *config.Config) { cfg.DataDir = dataDir })
	call("GET", base+"/imports/"+repeated.ID+"/changes?previewVersion="+strconv.FormatInt(repeatedPreview.Version, 10)+"&recordType=source", nil, 200)
	call("GET", base+"/imports/"+repeated.ID, nil, 200)

	if repeatedPreview.State != "ready" || repeatedPreview.CandidateHash == nil {
		t.Fatalf("repeat preview: %+v", repeatedPreview)
	}
	var repeatedResult backendmodel.ImportCommitResult
	if err := json.Unmarshal(call("POST", base+"/imports/"+repeated.ID+"/commit", backendmodel.CommitImportInput{ExpectedVersion: result.Project.Version, ExpectedImportVersion: repeatedPreview.Version, CandidateHash: *repeatedPreview.CandidateHash, IdempotencyKey: "repeat-commit"}, 200), &repeatedResult); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/revisions/compare", backendmodel.CompareRevisionsInput{FromRevisionID: result.Revision.ID, ToRevisionID: repeatedResult.Revision.ID}, 200)
	status := call("GET", path, nil, 200)
	var oldStatus backendmodel.ImportStatus
	if err := json.Unmarshal(status, &oldStatus); err != nil {
		t.Fatal(err)
	}
	if oldStatus.CommittedRevisionID == nil || *oldStatus.CommittedRevisionID != result.Revision.ID || oldStatus.Preview == nil {
		t.Fatalf("original receipt/saved preview lost after head changed: %s", status)
	}
	// Accepted changes clear a saved preview and make its old page pin stale.
	third := transportImportFixture(repeatedResult.Project)
	third.Mode = "reconcile"
	third.RepositoryID = new(session.RepositoryID)
	third.GraphScope = second.GraphScope
	third.IdempotencyKey = "clear-preview"
	var cleared backendmodel.ImportSession
	if err := json.Unmarshal(call("POST", base+"/imports", third, 200), &cleared); err != nil {
		t.Fatal(err)
	}
	var clearPreview backendmodel.ImportPreview
	if err := json.Unmarshal(call("POST", base+"/imports/"+cleared.ID+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: cleared.Version, BaseRevisionID: repeatedResult.Revision.ID}, 200), &clearPreview); err != nil {
		t.Fatal(err)
	}
	removeCommands := []backendmodel.ImportCommand{{Op: "remove", Remove: &backendmodel.ImportRemove{RecordType: "node", ExternalKey: "not-staged"}}}
	removeHash, err := backendmodel.ImportBatchHash(removeCommands)
	if err != nil {
		t.Fatal(err)
	}
	call("PUT", base+"/imports/"+cleared.ID+"/batches/clear", backendmodel.ImportBatchInput{ExpectedImportVersion: clearPreview.Version, PayloadHash: removeHash, Commands: removeCommands}, 200)
	call("GET", base+"/imports/"+cleared.ID+"/changes?previewVersion="+strconv.FormatInt(clearPreview.Version, 10)+"&recordType=source", nil, 409)
	// A separate empty project exercises abort and its receipt without changing a head.
	if err := json.Unmarshal(call("POST", "/api/backend-projects", backendmodel.CreateInput{Name: "Abort", IdempotencyKey: "abort-create"}, 201), &p); err != nil {
		t.Fatal(err)
	}
	base = "/api/backend-projects/" + p.ID
	if err := json.Unmarshal(call("POST", base+"/imports", transportImportFixture(p), 200), &session); err != nil {
		t.Fatal(err)
	}
	abort := backendmodel.AbortImportInput{ExpectedImportVersion: session.Version, IdempotencyKey: "abort"}
	aborted := call("POST", base+"/imports/"+session.ID+"/abort", abort, 200)
	if replay := call("POST", base+"/imports/"+session.ID+"/abort", abort, 200); string(replay) != string(aborted) {
		t.Fatal("abort receipt changed")
	}
}

// Check real handler responses against the complete generated-client contract.
func validateBackendImportResponse(t *testing.T, method, path string, data []byte) {
	t.Helper()
	path, _, _ = strings.Cut(path, "?")
	parts := strings.Split(strings.Trim(path, "/"), "/")
	name := ""
	if len(parts) == 4 && parts[3] == "imports" {
		if method == "GET" {
			name = "BackendImportPage"
		} else {
			name = "BackendImportSession"
		}
	}
	if len(parts) == 5 && parts[3] == "imports" {
		name = "BackendImportStatus"
	}
	if len(parts) == 7 && parts[5] == "batches" {
		name = "BackendBatchReceipt"
	}
	if len(parts) == 6 && parts[3] == "imports" {
		switch parts[5] {
		case "changes":
			name = "BackendImportChangesPage"
		case "preview":
			name = "BackendImportPreview"
		case "commit":
			name = "BackendImportCommitResult"
		case "abort":
			name = "BackendImportSession"
		}
	}
	if len(parts) == 5 && parts[3] == "revisions" && parts[4] == "compare" {
		name = "BackendRevisionComparison"
	}
	if len(parts) == 5 && parts[3] == "graph" {
		name = "BackendGraphPage"
	}
	if len(parts) == 7 && parts[5] == "nodes" {
		name = "BackendNode"
	}
	if len(parts) == 6 && parts[3] == "revisions" {
		switch parts[5] {
		case "coverage":
			name = "BackendRevisionCoverage"
		case "evidence":
			name = "BackendEvidencePage"
		}
	}

	if len(parts) >= 4 && parts[3] == "proposals" {
		if len(parts) == 4 {
			if method == "GET" {
				name = "BackendProposalPage"
			} else {
				name = "BackendProposalDetail"
			}
		}
		if len(parts) == 5 {
			name = "BackendProposalDetail"
		}
		if len(parts) == 6 {
			if parts[5] == "preview" {
				name = "BackendProposalPreview"
			} else {
				name = "BackendProposalApplyResult"
			}
		}
		if len(parts) == 8 {
			if parts[7] == "coverage" {
				name = "BackendRevisionCoverage"
			} else if parts[7] == "evidence" {
				name = "BackendEvidencePage"
			}
		}
		if len(parts) == 9 {
			name = "BackendProposalNodeRead"
		}
	}
	if len(parts) == 3 && parts[2] == "capabilities" {
		name = "BackendCapabilities"
	}
	if len(parts) == 5 && parts[3] == "revisions" && parts[4] != "compare" {
		name = "BackendRevision"
	}
	if len(parts) == 5 && parts[4] == "query" {
		switch parts[3] {
		case "lineage":
			name = "BackendLineagePage"
		case "flow":
			name = "BackendFlowPage"
		case "database":
			name = "BackendDatabasePage"
		}
	}
	if len(parts) >= 4 && parts[3] == "saved-views" {
		if len(parts) == 4 && method == "GET" {
			name = "BackendSavedViewPage"
		} else {
			name = "BackendSavedView"
		}
	}
	if len(parts) == 5 && parts[3] == "api-artifacts" {
		name = map[string]string{"query": "BackendAPIArtifactPage", "preview": "BackendAPIPinsPreview", "commands": "BackendAPIPinsResult"}[parts[4]]
	}
	if len(parts) == 6 && parts[1] == "designs" && parts[5] == "artifact-snapshot" {
		name = "APIArtifactSnapshot"
	}
	if len(parts) == 5 && parts[3] == "artifacts" {
		name = map[string]string{"query": "ArtifactProjectionPage", "preview": "ArtifactPinsPreview", "commands": "ArtifactPinsResult"}[parts[4]]
	}
	if len(parts) == 6 && parts[1] == "design-scenarios" && parts[5] == "artifact-snapshot" {
		name = "DesignScenarioArtifactSnapshot"
	}
	if name == "" {
		t.Fatalf("No response schema selected for %s %s", method, path)
	}
	raw, err := os.ReadFile(specPath)
	if err != nil {
		t.Fatal(err)
	}
	decoder := jsonx.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var document any
	if err := decoder.Decode(&document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/openapi.json"
	if err := compiler.AddResource(uri, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + "#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	decoder = jsonx.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	if err := schema.Validate(value); err != nil {
		t.Fatalf("%s %s response violates %s: %v\n%s", method, path, name, err, data)
	}
}

func TestBackendImportStrictRequiredAndNullableWireFields(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects", []byte(`{"name":"Wire","idempotencyKey":"wire"}`))
	if err != nil || status != 201 {
		t.Fatalf("create: %d %s %v", status, data, err)
	}
	var p backendmodel.Project
	if err := json.Unmarshal(data, &p); err != nil {
		t.Fatal(err)
	}
	valid, err := json.Marshal(transportImportFixture(p))
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	for _, input := range []string{strings.Replace(string(valid), `"expectedVersion":1`, `"mode":null,"expectedVersion":1`, 1), strings.Replace(string(valid), `"expectedVersion":1`, `"mode":"","expectedVersion":1`, 1), strings.Replace(string(valid), `"dirty":false,`, "", 1), strings.Replace(string(valid), `"limitations":[]`, `"limitations":null`, 1), strings.Replace(string(valid), `"commit":"abc"`, `"commit":null`, 1), strings.Replace(string(valid), `"expectedVersion":1`, `"expectedVersion":null`, 1)} {
		status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", base+"/imports", []byte(input))
		if err != nil || status != 400 {
			t.Fatalf("missing/nonnullable required member: %d %s %v", status, data, err)
		}
	}
	for _, input := range []string{`{"expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[{"op":"upsert_node","node":{"externalKey":"x","kind":"symbol","name":"X","parentKey":null,"attributes":{},"evidenceKeys":[]}}]}`, `{"expectedImportVersion":1,"payloadHash":"` + strings.Repeat("a", 64) + `","commands":[{"op":"upsert_node","node":{"externalKey":"x","kind":"symbol","name":"X","evidenceKeys":[]}}]}`} {
		status, data, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "PUT", base+"/imports/"+p.ID+"/batches/b", []byte(input))
		if err != nil || status != 400 {
			t.Fatalf("nullable/missing node fields accepted: %d %s %v", status, data, err)
		}
	}
}
