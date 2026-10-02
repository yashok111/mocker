package admin

import (
	"bytes"
	"crypto/sha256"
	"encoding/json/v2"
	"fmt"
	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/testauth"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestBackendAPIArtifactRoutesAndAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	call := func(method, path, body string, want int) []byte {
		t.Helper()
		status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, []byte(body))
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v; want %d", method, path, status, raw, err, want)
		}
		return raw
	}
	var p backendmodel.Project
	json.Unmarshal(call("POST", "/api/backend-projects", `{"name":"API pins","idempotencyKey":"project"}`, 201), &p)
	base := "/api/backend-projects/" + p.ID + "/api-artifacts"
	valid := `{"revisionId":"` + p.CurrentRevisionID + `"}`
	call("POST", base+"/query", valid, 200)
	call("POST", base+"/query", `{"revisionId":"`+p.ID+`"}`, 404)
	for _, body := range []string{`null`, `{}`, strings.Replace(valid, `}`, `,"unknown":true}`, 1), strings.Replace(valid, `}`, `,"revisionId":"`+p.CurrentRevisionID+`"}`, 1), strings.Replace(valid, `}`, `,"limit":null}`, 1), strings.Replace(valid, `}`, `,"limit":"1"}`, 1)} {
		call("POST", base+"/query", body, 400)
	}
	call("POST", base+"/query?limit=1", valid, 400)
	call("POST", base+"/preview", `{"baseRevisionId":"`+p.CurrentRevisionID+`","expectedVersion":1,"commands":[{"type":"remove_api_pin","artifactId":"9007199254740993","reason":"detach"}]}`, 422)
	call("POST", base+"/commands", `{}`, 400)
	call("POST", base+"/preview", strings.Repeat(" ", backendmodel.MaxAPIPinBodyBytes+1), http.StatusRequestEntityTooLarge)
	call("GET", "/api/designs/9007199254740993/revisions/9223372036854775807/artifact-snapshot", "", 404)
	for _, id := range []string{"01", "+1", "0", "9223372036854775808"} {
		call("GET", "/api/designs/"+id+"/revisions/1/artifact-snapshot", "", 400)
	}
}

// Source4 fixture enters through the same persistent import operations as the
// public transport. Only its provider's inert source records are authored here.
func backendAPIArtifactFixture(t *testing.T, s *Server) (*backendmodel.ImportCommitResult, string) {
	t.Helper()
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Pinned source", IdempotencyKey: "create-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	in := transportImportFixture(*p)
	in.Profile = backendmodel.LineageProfile
	in.Manifest.Provider.Profiles = []string{backendmodel.GraphProfile, backendmodel.RelationalProfile, backendmodel.RuntimeProfile, backendmodel.LineageProfile}
	raw, err := os.ReadFile("../backendmodel/testdata/lineage/orders/source.go.txt")
	if err != nil {
		t.Fatal(err)
	}
	hash := fmt.Sprintf("%x", sha256.Sum256(raw))
	in.Manifest.Snapshot.Files = []backendmodel.ManifestFile{{Path: "source.go.txt", ContentHash: hash, FileType: "go", AnalysisStatus: "analyzed"}}
	for i := range in.Inventory {
		if slices.Contains([]string{"files", "endpoints", "datastores"}, in.Inventory[i].Category) {
			in.Inventory[i].KnownCount = 1
			in.Inventory[i].Denominator = new(int64(1))
		}
	}
	session, err := s.backendRepo.BeginImport(t.Context(), p.ID, in)
	if err != nil {
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
	batchHash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.backendRepo.PutImportBatch(t.Context(), p.ID, session.ID, "b", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: batchHash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.backendRepo.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("%+v %v", preview, err)
	}
	out, err := s.backendRepo.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit-fixture"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.backendRepo.QueryGraph(t.Context(), p.ID, backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range graph.Nodes {
		if node.ExternalKey == "http" {
			return out, node.ID
		}
	}
	t.Fatal("source endpoint missing")
	return nil, ""
}

func TestBackendAPIArtifactSnapshotAndRawReplay(t *testing.T) {
	s := loopbackTestServer(t, nil)
	// Move independent AUTOINCREMENT sequences before creation. Existing owner
	// Create allocates separate workspaces and preserves all foreign keys.
	for _, table := range []string{"api_designs", "api_design_revisions"} {
		if _, err := s.db.W.ExecContext(t.Context(), `INSERT INTO sqlite_sequence(name,seq) VALUES(?,9007199254740992)`, table); err != nil {
			t.Fatal(err)
		}
	}
	const document = "{\n \"openapi\":\"3.1.0\",\"info\":{\"title\":\"Exact\",\"version\":\"1\"},\"paths\":{\"/orders\":{\"get\":{\"x-mocker-canvas-operation-id\":\"exact-op\",\"responses\":{\"200\":{\"description\":\"OK\"}}}}},\"components\":{\"schemas\":{\"Meta\":true}}\n}"
	design, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Exact API", Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	id, rid := strconv.FormatInt(design.Design.ID, 10), strconv.FormatInt(design.Draft.ID, 10)
	// Owner ingestion formats input; independently seed stored raw bytes to
	// prove that this endpoint preserves owner whitespace and its exact digest.
	if _, err = s.db.W.ExecContext(t.Context(), `UPDATE api_design_revisions SET document=?,hash=? WHERE id=?`, document, fmt.Sprintf("%x", sha256.Sum256([]byte(document))), design.Draft.ID); err != nil {
		t.Fatal(err)
	}
	if id != "9007199254740993" || rid != "9007199254740993" {
		t.Fatal("wide fixture", id, rid)
	}
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		var body []byte
		if input != nil {
			body, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, raw, e := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, body)
		if e != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, raw, e)
		}
		if want == 200 {
			validateBackendImportResponse(t, method, path, raw)
		}
		return raw
	}
	path := "/api/designs/" + id + "/revisions/" + rid + "/artifact-snapshot"
	raw := call("GET", path, nil, 200)
	var snapshot struct {
		ArtifactID  string `json:"artifactId"`
		RevisionID  string `json:"revisionId"`
		ContentHash string `json:"contentHash"`
		Document    string `json:"document"`
	}
	if err = json.Unmarshal(raw, &snapshot); err != nil {
		t.Fatal(err)
	}
	if snapshot.ArtifactID != id || snapshot.RevisionID != rid || snapshot.Document != document || snapshot.ContentHash != fmt.Sprintf("%x", sha256.Sum256([]byte(document))) {
		t.Fatal("raw snapshot changed", string(raw))
	}
	foreign, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Foreign", Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/designs/"+id+"/revisions/"+strconv.FormatInt(foreign.Draft.ID, 10)+"/artifact-snapshot", nil, 404)
	// Advancing the owner draft cannot alter an association to the old exact ID.
	advanced, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: design.Design.Version, Document: strings.Replace(document, `"title":"Exact"`, `"title":"Advanced"`, 1), Source: "ui", Summary: "advance"})
	if err != nil {
		t.Fatal(err)
	}
	base, node := backendAPIArtifactFixture(t, s)
	root := "/api/backend-projects/" + base.Project.ID + "/api-artifacts"
	in := backendmodel.PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.APIPinCommand{{Type: "set_api_pin", ArtifactID: id, RevisionID: rid, Reason: "manual", Bindings: []backendmodel.APIPinBindingInput{{SourceNodeID: node, Selector: backendmodel.APIArtifactSelector{ObjectKey: "exact-op"}}}}}}
	graph, err := s.backendRepo.QueryGraph(t.Context(), base.Project.ID, backendmodel.GraphQueryInput{RevisionID: base.Revision.ID, RecordType: "nodes"})
	if err != nil {
		t.Fatal(err)
	}
	field := ""
	for _, n := range graph.Nodes {
		if n.ExternalKey == "request" {
			field = n.ID
		}
	}
	in.Commands = append(in.Commands, backendmodel.APIPinCommand{Type: "set_api_pin", ArtifactID: strconv.FormatInt(foreign.Design.ID, 10), RevisionID: strconv.FormatInt(foreign.Draft.ID, 10), Reason: "retained field schema", Bindings: []backendmodel.APIPinBindingInput{{SourceNodeID: field, Selector: backendmodel.APIArtifactSelector{JSONPointer: "/components/schemas/Meta"}}}})
	var preview backendmodel.APIPinsPreview
	if err = json.Unmarshal(call("POST", root+"/preview", in, 200), &preview); err != nil || !preview.CanApply {
		t.Fatalf("preview %+v %v", preview, err)
	}
	request := backendmodel.ApplyAPIPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "wire-receipt"}
	badHash := request
	badHash.CandidateHash = strings.Repeat("b", 64)
	badHash.IdempotencyKey = "bad-candidate"
	call("POST", root+"/commands", badHash, 409)
	if len(preview.Pins) != 2 || len(preview.Bindings) != 2 {
		t.Fatal("full pin vector lost", preview)
	}
	first := call("POST", root+"/commands", request, 200)
	var stored string
	if err = s.db.R.QueryRowContext(t.Context(), `SELECT response FROM backend_command_receipts WHERE scope=? AND key=?`, "api-pins:"+base.Project.ID, "wire-receipt").Scan(&stored); err != nil || string(first) != stored {
		t.Fatal("first wire is not stored receipt", err)
	}
	var result backendmodel.APIPinsResult
	if err = json.Unmarshal(first, &result); err != nil {
		t.Fatal(err)
	}
	call("POST", root+"/query", backendmodel.APIArtifactQueryInput{RevisionID: result.Revision.ID}, 200)
	call("POST", "/api/backend-projects/"+base.Project.ID+"/revisions/compare", backendmodel.CompareRevisionsInput{FromRevisionID: base.Revision.ID, ToRevisionID: result.Revision.ID, RecordType: "artifact"}, 200)
	contextOnly := in
	contextOnly.BaseRevisionID = result.Revision.ID
	contextOnly.ExpectedVersion = result.Project.Version
	contextOnly.Commands = slices.Clone(in.Commands[:1])
	contextOnly.Commands[0].RevisionID = strconv.FormatInt(advanced.Draft.ID, 10)
	var contextPreview backendmodel.APIPinsPreview
	if err = json.Unmarshal(call("POST", root+"/preview", contextOnly, 200), &contextPreview); err != nil || !contextPreview.CanApply || len(contextPreview.Diff) != 1 || !contextPreview.Diff[0].ContextChanged || contextPreview.Diff[0].Before.ObjectHash != contextPreview.Diff[0].After.ObjectHash {
		t.Fatalf("hash-only context change hidden %+v %v", contextPreview, err)
	}
	// Every escaped structural path must fit; unrepresentable changes are
	// explicitly truncated and cannot apply at the public boundary.
	oversizedDoc := strings.Replace(document, `"responses":`, `"x-`+strings.Repeat("p", 2048)+`":true,"responses":`, 1)
	oversized, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: advanced.Design.Version, Document: oversizedDoc, Source: "ui", Summary: "bounded diff"})
	if err != nil {
		t.Fatal(err)
	}
	changed := in
	changed.BaseRevisionID = result.Revision.ID
	changed.ExpectedVersion = result.Project.Version
	changed.Commands = slices.Clone(in.Commands[:1])
	changed.Commands[0].RevisionID = strconv.FormatInt(oversized.Draft.ID, 10)
	var truncated backendmodel.APIPinsPreview
	if err = json.Unmarshal(call("POST", root+"/preview", changed, 200), &truncated); err != nil || !truncated.DiffTruncated || truncated.CanApply {
		t.Fatalf("truncation %+v %v", truncated, err)
	}
	blocked := backendmodel.ApplyAPIPinsInput{BaseRevisionID: changed.BaseRevisionID, ExpectedVersion: changed.ExpectedVersion, Commands: changed.Commands, CandidateHash: truncated.CandidateHash, IdempotencyKey: "blocked"}
	if raw := call("POST", root+"/commands", blocked, 422); !bytes.Contains(raw, []byte("backend_api_diff_truncated")) {
		t.Fatal("truncation diagnostic lost", string(raw))
	}
	remapped, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: oversized.Design.Version, Document: strings.Replace(document, "exact-op", "remapped-op", 1), Source: "ui", Summary: "remap"})
	if err != nil {
		t.Fatal(err)
	}
	changed.Commands[0].RevisionID = strconv.FormatInt(remapped.Draft.ID, 10)
	changed.Commands[0].Bindings = []backendmodel.APIPinBindingInput{{SourceNodeID: node, Selector: backendmodel.APIArtifactSelector{ObjectKey: "remapped-op"}}}
	var remap backendmodel.APIPinsPreview
	if err = json.Unmarshal(call("POST", root+"/preview", changed, 200), &remap); err != nil || !remap.CanApply || len(remap.Diff) != 2 || remap.Diff[0].Status != "missing" || remap.Diff[0].SourceNodeID != remap.Diff[1].SourceNodeID {
		t.Fatalf("duplicate-source missing/remap rows lost %+v %v", remap, err)
	}
	remove := backendmodel.PreviewAPIPinsInput{BaseRevisionID: result.Revision.ID, ExpectedVersion: result.Project.Version, Commands: []backendmodel.APIPinCommand{{Type: "remove_api_pin", ArtifactID: id, Reason: "advance backend head"}}}
	var removal backendmodel.APIPinsPreview
	if err = json.Unmarshal(call("POST", root+"/preview", remove, 200), &removal); err != nil {
		t.Fatal(err)
	}
	if len(removal.Pins) != 1 || removal.Pins[0].ID != strconv.FormatInt(foreign.Design.ID, 10) || len(removal.Bindings) != 1 || removal.Bindings[0].SourceNodeID != field {
		t.Fatal("unrelated group lost", removal)
	}
	call("POST", root+"/commands", backendmodel.ApplyAPIPinsInput{BaseRevisionID: remove.BaseRevisionID, ExpectedVersion: remove.ExpectedVersion, Commands: remove.Commands, CandidateHash: removal.CandidateHash, IdempotencyKey: "remove"}, 200)
	s.backendAPIArtifacts = backendmodel.NewAPIArtifactService(s.backendRepo, nil)
	replay := call("POST", root+"/commands", request, 200)
	if !bytes.Equal(first, replay) {
		t.Fatalf("receipt bytes differ: %q %q", first, replay)
	}
	broken := call("POST", root+"/query", backendmodel.APIArtifactQueryInput{RevisionID: result.Revision.ID}, 200)
	if !bytes.Contains(broken, []byte(`"status":"broken"`)) {
		t.Fatal("unavailable reference lost", string(broken))
	}
	request.Commands[0].Reason = "conflict"
	call("POST", root+"/commands", request, 409)
	s.backendAPIArtifacts = nil
	if !slices.Contains(s.Ready(), "NewAPIArtifactService") {
		t.Fatal("missing service does not fail readiness")
	}
}

func TestBackendAPIArtifactAuthenticationAndCSRF(t *testing.T) {
	s := loopbackTestServer(t, nil)
	h := s.Handler()
	for _, path := range []string{"/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/query", "/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/preview", "/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/commands", "/api/designs/1/revisions/1/artifact-snapshot"} {
		method := "POST"
		if strings.Contains(path, "artifact-snapshot") {
			method = "GET"
		}
		r := httptest.NewRequest(method, "http://mocker.local"+path, strings.NewReader(`{}`))
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("Content-Type", "application/json")
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 401 {
			t.Fatalf("unauth %s %d %s", path, w.Code, w.Body)
		}
	}
	login := httptest.NewRequest("POST", "http://mocker.local/api/auth/login", strings.NewReader(`{"name":"API pins","password":"`+testauth.Password+`"}`))
	login.Header.Set("Origin", "http://mocker.local")
	login.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	h.ServeHTTP(w, login)
	if w.Code != 200 {
		t.Fatal(w.Code, w.Body)
	}
	cookie := w.Result().Cookies()[0]
	for _, suffix := range []string{"query", "preview", "commands"} {
		r := httptest.NewRequest("POST", "http://mocker.local/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/"+suffix, strings.NewReader(`{}`))
		r.AddCookie(cookie)
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("Content-Type", "application/json")
		w = httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != 403 {
			t.Fatalf("missing CSRF %s %d %s", suffix, w.Code, w.Body)
		}
	}
}

func TestBackendAPIArtifactBooleanSchemaRootDiff(t *testing.T) {
	s := loopbackTestServer(t, nil)
	base, _ := backendAPIArtifactFixture(t, s)
	graph, err := s.backendRepo.QueryGraph(t.Context(), base.Project.ID, backendmodel.GraphQueryInput{RevisionID: base.Revision.ID, RecordType: "nodes"})
	if err != nil {
		t.Fatal(err)
	}
	field := ""
	for _, n := range graph.Nodes {
		if n.ExternalKey == "request" {
			field = n.ID
		}
	}
	const doc = `{"openapi":"3.1.0","info":{"title":"Bool","version":"1"},"paths":{},"components":{"schemas":{"Bool":true}}}`
	design, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Boolean", Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	in := backendmodel.PreviewAPIPinsInput{BaseRevisionID: base.Revision.ID, ExpectedVersion: base.Project.Version, Commands: []backendmodel.APIPinCommand{{Type: "set_api_pin", ArtifactID: strconv.FormatInt(design.Design.ID, 10), RevisionID: strconv.FormatInt(design.Draft.ID, 10), Reason: "field schema", Bindings: []backendmodel.APIPinBindingInput{{SourceNodeID: field, Selector: backendmodel.APIArtifactSelector{JSONPointer: "/components/schemas/Bool"}}}}}}
	preview, err := s.backendAPIArtifacts.Preview(t.Context(), base.Project.ID, in)
	if err != nil || !preview.CanApply {
		t.Fatalf("boolean preview %+v %v", preview, err)
	}
	first, err := s.backendAPIArtifacts.Apply(t.Context(), base.Project.ID, backendmodel.ApplyAPIPinsInput{BaseRevisionID: in.BaseRevisionID, ExpectedVersion: in.ExpectedVersion, Commands: in.Commands, CandidateHash: preview.CandidateHash, IdempotencyKey: "bool"})
	if err != nil {
		t.Fatal(err)
	}
	next, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: design.Design.Version, Document: strings.Replace(doc, `"Bool":true`, `"Bool":false`, 1), Source: "ui", Summary: "bool root change"})
	if err != nil {
		t.Fatal(err)
	}
	in.BaseRevisionID = first.Revision.ID
	in.ExpectedVersion = first.Project.Version
	in.Commands[0].RevisionID = strconv.FormatInt(next.Draft.ID, 10)
	raw, err := json.Marshal(in)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + base.Project.ID + "/api-artifacts/preview"
	status, response, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, raw)
	if err != nil || status != 200 {
		t.Fatal(status, string(response), err)
	}
	validateBackendImportResponse(t, "POST", path, response)
	var out backendmodel.APIPinsPreview
	if err = json.Unmarshal(response, &out); err != nil || len(out.Diff) != 1 || len(out.Diff[0].Changes) != 1 || out.Diff[0].Changes[0].Pointer != "" || out.Diff[0].Changes[0].Kind != "changed" {
		t.Fatalf("root structural pointer %+v %v", out, err)
	}
}

type backendAPIArtifactReadProbe struct{ reads int }

func (p *backendAPIArtifactReadProbe) Read([]byte) (int, error) { p.reads++; return 0, io.EOF }
func (p *backendAPIArtifactReadProbe) Close() error             { return nil }

func TestBackendAPIArtifactBodyAdmissionBeforeRead(t *testing.T) {
	s := loopbackTestServer(t, nil)
	probe := new(backendAPIArtifactReadProbe)
	r := httptest.NewRequest("POST", "http://mocker.local/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/preview", nil)
	r.Body = probe
	r.ContentLength = backendmodel.MaxAPIPinBodyBytes + 1
	w := httptest.NewRecorder()
	var in backendmodel.PreviewAPIPinsInput
	if s.backendAPIArtifactBody(w, r, &in) || w.Code != 413 || probe.reads != 0 {
		t.Fatalf("oversized allocation admission: %d reads=%d", w.Code, probe.reads)
	}
	// Unknown/chunked length still uses the same bounded reader.
	r = httptest.NewRequest("POST", "http://mocker.local/api/backend-projects/00000000-0000-4000-8000-000000000001/api-artifacts/preview", strings.NewReader(strings.Repeat(" ", backendmodel.MaxAPIPinBodyBytes+1)))
	r.ContentLength = -1
	w = httptest.NewRecorder()
	if s.backendAPIArtifactBody(w, r, &in) || w.Code != 413 {
		t.Fatalf("stream limit: %d", w.Code)
	}
}

func TestBackendAPIArtifactSnapshotOwnerLimit(t *testing.T) {
	s := loopbackTestServer(t, nil)
	const doc = `{"openapi":"3.1.0","info":{"title":"Limit","version":"1"},"paths":{}}`
	d, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Limit", Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	s.cfg.MaxBody = 16
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/designs/"+strconv.FormatInt(d.Design.ID, 10)+"/revisions/"+strconv.FormatInt(d.Draft.ID, 10)+"/artifact-snapshot", nil)
	if err != nil || status != 413 {
		t.Fatal("owner raw body limit not propagated", status, string(raw), err)
	}
}
