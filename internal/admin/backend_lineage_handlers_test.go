package admin

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendLineageRouteStrictPins(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Lineage", IdempotencyKey: "lineage"})
	if err != nil {
		t.Fatal(err)
	}
	route := "/api/backend-projects/" + p.ID + "/lineage/query"
	base := `{"revisionId":"` + p.CurrentRevisionID + `","seed":{"kind":"api_field","nodeId":"` + p.ID + `"},"direction":"forward"`
	for _, tc := range []struct {
		suffix string
		status int
	}{{`}`, 422}, {`,"proposal":{}}`, 400}, {`,"other":true}`, 400}, {`,"limit":0}`, 400}, {`,"maxDepth":null}`, 400}} {
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", route, []byte(base+tc.suffix))
		if err != nil || status != tc.status || !strings.Contains(string(raw), "backend_") {
			t.Errorf("%s: %d %s %v", tc.suffix, status, raw, err)
		}
	}
}
func TestBackendLineageNeverCreatesCheckpoint(t *testing.T) {
	for _, r := range (&Server{}).routes() {
		if r.pattern == "POST /api/backend-projects/{id}/lineage/query" {
			if r.checkpoint != cpNeverTouchesLayer {
				t.Fatal(r.checkpoint)
			}
			return
		}
	}
	t.Fatal("lineage route missing")
}

func lineageTransportFixture(t *testing.T, s *Server) (*backendmodel.ImportCommitResult, map[string]string) {
	t.Helper()
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Orders", IdempotencyKey: "orders"})
	if err != nil {
		t.Fatal(err)
	}
	in := transportImportFixture(*p)
	in.Profile = backendmodel.LineageProfile
	in.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile}
	in.Manifest.Provider.Method = "agent"
	in.Manifest.Snapshot.Files = []backendmodel.ManifestFile{{Path: "source.go.txt", ContentHash: "5fbc7deb9707b036ccc6cc6303de8d61c9a8b7e4adb4e05958f28e4e6df32f4c", FileType: "go", AnalysisStatus: "analyzed"}}
	for i := range in.Inventory {
		if in.Inventory[i].Category == "endpoints" || in.Inventory[i].Category == "datastores" {
			in.Inventory[i].Status = "complete"
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
			in.Inventory[i].Reason = ""
		}
	}
	// Exercise source4 profile acceptance at the public import boundary.
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/imports", raw)
	if err != nil || status != 200 {
		t.Fatalf("begin %d %s %v", status, raw, err)
	}
	validateBackendImportResponse(t, "POST", "/api/backend-projects/"+p.ID+"/imports", raw)
	var session backendmodel.ImportSession
	if err = json.Unmarshal(raw, &session); err != nil {
		t.Fatal(err)
	}
	raw, err = os.ReadFile("../backendmodel/testdata/lineage/orders/commands.json")
	if err != nil {
		t.Fatal(err)
	}
	raw = []byte(strings.NewReplacer("@repositoryId@", session.RepositoryID, "@snapshotId@", session.SnapshotID).Replace(string(raw)))
	var commands []backendmodel.ImportCommand
	if err = json.Unmarshal(raw, &commands); err != nil {
		t.Fatal(err)
	}
	call := func(method, path string, input, output any) {
		t.Helper()
		wire, err := json.Marshal(input)
		if err != nil {
			t.Fatal(err)
		}
		status, wire, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, wire)
		if err != nil || status != 200 {
			t.Fatalf("%s %s: %d %s %v", method, path, status, wire, err)
		}
		validateBackendImportResponse(t, method, path, wire)
		if err := json.Unmarshal(wire, output); err != nil {
			t.Fatal(err)
		}
	}
	version := session.Version
	importPath := "/api/backend-projects/" + p.ID + "/imports/" + session.ID
	for i := 0; i < len(commands); i += backendmodel.MaxImportCommands {
		batch := commands[i:min(i+backendmodel.MaxImportCommands, len(commands))]
		hash, err := backendmodel.ImportBatchHash(batch)
		if err != nil {
			t.Fatal(err)
		}
		var receipt backendmodel.BatchReceipt
		call("PUT", importPath+fmt.Sprintf("/batches/b%d", i), backendmodel.ImportBatchInput{ExpectedImportVersion: version, PayloadHash: hash, Commands: batch}, &receipt)
		version = receipt.AcceptedVersion
	}
	var preview backendmodel.ImportPreview
	call("POST", importPath+"/preview", backendmodel.PreviewImportInput{ExpectedImportVersion: version, BaseRevisionID: p.CurrentRevisionID}, &preview)
	if preview.State != "ready" {
		t.Fatal(preview.Diagnostics)
	}
	out := new(backendmodel.ImportCommitResult)
	call("POST", importPath+"/commit", backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"}, out)
	var graph backendmodel.GraphPage
	call("POST", "/api/backend-projects/"+p.ID+"/graph/query", backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes", Limit: 500}, &graph)
	ids := map[string]string{}
	for _, node := range graph.Nodes {
		ids[node.ExternalKey] = node.ID
	}
	return out, ids
}

func TestBackendLineagePersistedResponseCursorAndForeignPins(t *testing.T) {
	s := loopbackTestServer(t, nil)
	out, ids := lineageTransportFixture(t, s)
	route := "/api/backend-projects/" + out.Project.ID + "/lineage/query"
	in := backendmodel.LineageQueryInput{RevisionID: out.Revision.ID, Seed: backendmodel.LineageValueRef{Kind: "api_field", NodeID: ids["request"]}, Direction: "forward", Limit: 1}
	call := func(ctx context.Context, path string, in backendmodel.LineageQueryInput, want int) []byte {
		t.Helper()
		raw, err := json.Marshal(in)
		if err != nil {
			t.Fatal(err)
		}
		status, raw, err := s.CallAsMCP(ctx, loopbackTestSrc(), "POST", path, raw)
		if err != nil || status != want {
			t.Fatalf("query %d %s %v want%d", status, raw, err, want)
		}
		return raw
	}
	first := call(t.Context(), route, in, 200)
	user, err := s.mcpIdentity(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	wire, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest("POST", route, strings.NewReader(string(wire))).WithContext(withAuthContext(t.Context(), nil, user))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.routeMux().ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != string(first) {
		t.Fatalf("REST/loopback projection diverged %d %s", rec.Code, rec.Body)
	}

	validateBackendImportResponse(t, "POST", route, first)
	var page backendmodel.LineagePage
	if err := json.Unmarshal(first, &page); err != nil {
		t.Fatal(err)
	}
	if page.ProjectID != out.Project.ID || page.RevisionID != in.RevisionID || page.Seed != in.Seed || len(page.Items) != 1 || page.Items[0].Mapping.ExternalKey != "m01-request" || page.NextCursor == "" {
		t.Fatalf("lost pinned projection %+v", page)
	}
	if string(first) != string(call(t.Context(), route, in, 200)) {
		t.Fatal("repeat read changed")
	}
	in.Cursor = page.NextCursor
	next := call(t.Context(), route, in, 200)
	validateBackendImportResponse(t, "POST", route, next)
	if string(first) == string(next) {
		t.Fatal("cursor did not advance")
	}
	in.Direction = "reverse"
	call(t.Context(), route, in, 400)
	in.Direction = "forward"
	in.Limit = 2
	call(t.Context(), route, in, 400)
	in.Limit = 1
	in.Cursor = "malformed"
	call(t.Context(), route, in, 400)
	in.Cursor = ""
	in.Seed.NodeID = out.Project.ID
	call(t.Context(), route, in, 404)
	in.Seed = backendmodel.LineageValueRef{Kind: "port", NodeID: ids["input"], Collection: "inputs", PortKey: "missing"}
	call(t.Context(), route, in, 400)
	other, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Foreign", IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	in.Seed = page.Seed
	call(t.Context(), "/api/backend-projects/"+other.ID+"/lineage/query", in, 404)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	status, _, err := s.CallAsMCP(ctx, loopbackTestSrc(), "POST", route, raw)
	if err == nil && status == 200 {
		t.Fatal("cancelled request produced a successful page")
	}
}
func TestBackendLineageRequiresAuthentication(t *testing.T) {
	s := loopbackTestServer(t, nil)
	rec := httptest.NewRecorder()
	s.handleQueryBackendLineage(rec, httptest.NewRequest("POST", "/api/backend-projects/x/lineage/query", strings.NewReader(`{}`)))
	if rec.Code != 401 {
		t.Fatal(rec.Code)
	}
}

func TestBackendLineageRejectsActualSource3(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Runtime", IdempotencyKey: "runtime"})
	if err != nil {
		t.Fatal(err)
	}
	in := transportImportFixture(*p)
	in.Profile = backendmodel.RuntimeProfile
	in.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile}
	session, err := s.backendRepo.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.backendRepo.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: session.Version, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.State != "ready" {
		t.Fatalf("preview %+v %v", preview, err)
	}
	out, err := s.backendRepo.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit-runtime"})
	if err != nil {
		t.Fatal(err)
	}
	if out.Revision.SchemaVersion != "3" {
		t.Fatal(out.Revision.SchemaVersion)
	}
	body := `{"revisionId":"` + out.Revision.ID + `","seed":{"kind":"api_field","nodeId":"` + p.ID + `"},"direction":"forward"}`
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/lineage/query", []byte(body))
	if err != nil || status != 422 || !strings.Contains(string(raw), "backend_unsupported_scope") {
		t.Fatalf("source3 %d %s %v", status, raw, err)
	}
}

func TestBackendLineageInheritedPublicResponseSchemas(t *testing.T) {
	s := loopbackTestServer(t, nil)
	out, ids := lineageTransportFixture(t, s)
	base := "/api/backend-projects/" + out.Project.ID
	call := func(method, path string, input any) []byte {
		t.Helper()
		var raw []byte
		var err error
		if input != nil {
			raw, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
		if err != nil || status != 200 {
			t.Fatalf("%s %s: %d %s %v", method, path, status, raw, err)
		}
		validateBackendImportResponse(t, method, path, raw)
		return raw
	}
	call("GET", base+"/revisions/"+out.Revision.ID, nil)
	imports := call("GET", base+"/imports", nil)
	var importPage backendmodel.ImportPage
	if err := json.Unmarshal(imports, &importPage); err != nil {
		t.Fatal(err)
	}
	if len(importPage.Items) != 1 {
		t.Fatal("fixture import not found")
	}
	call("GET", base+"/imports/"+importPage.Items[0].ID, nil)
	call("POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes", Limit: 500})
	call("POST", base+"/graph/query", backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "edges", Limit: 500})
	for _, key := range []string{"request", "response", "m06-total", "m10-unknown", "input", "query", "column:orders:amount"} {
		call("GET", base+"/revisions/"+out.Revision.ID+"/nodes/"+ids[key], nil)
	}
	call("GET", base+"/revisions/"+out.Revision.ID+"/evidence?limit=500", nil)
	call("GET", base+"/revisions/"+out.Revision.ID+"/coverage", nil)
	for _, view := range []string{"tables", "relationships"} {
		call("POST", base+"/database/query", map[string]any{"revisionId": out.Revision.ID, "datastoreId": ids["database:orders"], "facetKey": "sql", "recordType": view})
	}
	for _, view := range []string{"entrypoints", "steps", "transitions", "accesses"} {
		input := map[string]any{"revisionId": out.Revision.ID, "view": view}
		if view == "steps" || view == "transitions" {
			input["flowId"] = ids["flow"]
		}
		if view == "accesses" {
			input["entrypointId"] = ids["http"]
		}
		call("POST", base+"/flow/query", input)
	}
	call("POST", base+"/lineage/query", backendmodel.LineageQueryInput{RevisionID: out.Revision.ID, Seed: backendmodel.LineageValueRef{Kind: "api_field", NodeID: ids["response"]}, Direction: "reverse"})
	state := map[string]any{"kind": "flow", "scope": map[string]any{}, "filters": map[string]any{"search": "", "accessKind": "", "reverseAccessKind": ""}, "selection": nil, "positions": []any{}, "collapsedGroupIds": []string{}}
	saved := call("POST", base+"/saved-views", map[string]any{"name": "Source4", "target": map[string]any{"revisionId": out.Revision.ID}, "state": state, "idempotencyKey": "view"})
	var view struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(saved, &view); err != nil {
		t.Fatal(err)
	}
	call("GET", base+"/saved-views", nil)
	call("GET", base+"/saved-views/"+view.ID, nil)

	dbState := map[string]any{"kind": "database", "scope": map[string]any{"datastoreId": ids["database:orders"], "facetKey": "sql"}, "filters": map[string]any{"search": ""}, "selection": nil, "positions": []any{}, "collapsedGroupIds": []string{}}
	dbSaved := call("POST", base+"/saved-views", map[string]any{"name": "Database source4", "target": map[string]any{"revisionId": out.Revision.ID}, "state": dbState, "idempotencyKey": "db-view"})
	var dbView struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(dbSaved, &dbView); err != nil {
		t.Fatal(err)
	}
	call("GET", base+"/saved-views/"+dbView.ID, nil)
	begin := transportImportFixture(out.Project)
	begin.Profile = backendmodel.LineageProfile
	begin.Mode = "reconcile"
	begin.RepositoryID = new(importPage.Items[0].RepositoryID)
	begin.GraphScope = &backendmodel.GraphScope{Profile: backendmodel.LineageProfile, Status: "partial", Gaps: []string{"bounded source"}}
	begin.IdempotencyKey = "reconcile-lineage"
	begin.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile}
	begin.Manifest.Provider.Method = "agent"
	sessionRaw := call("POST", base+"/imports", begin)
	var session backendmodel.ImportSession
	if err := json.Unmarshal(sessionRaw, &session); err != nil {
		t.Fatal(err)
	}
	if session.Profile != backendmodel.LineageProfile {
		t.Fatal(session.Profile)
	}
	call("GET", base+"/imports/"+session.ID, nil)
}
