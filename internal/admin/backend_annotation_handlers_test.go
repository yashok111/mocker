package admin

import (
	"bytes"
	"encoding/json/v2"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"uuid"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testauth"
)

func TestBackendAnnotationListRouteAndStrictQueries(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Notes", IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + p.ID + "/annotations"
	status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", path, nil)
	if err != nil || status != 200 {
		t.Fatalf("annotation list: %d %s %v", status, body, err)
	}
	validateBackendImportResponse(t, "GET", path, body)
	for _, query := range []string{"?unknown=x", "?limit=0", "?limit=501", "?limit=1&limit=2", "?cursor=a&cursor=b", "?annotationId=", "?annotationId=null", "?targetId=" + p.ID, "?recordType=nodes", "?revisionId=bad", "?orphaned=1", "?orphaned=null", "?orphaned=true&orphaned=false"} {
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", path+query, nil)
		if err != nil || status != 400 {
			t.Errorf("query %s: %d %s %v", query, status, body, err)
		}
	}
	for _, query := range []string{"?orphaned=false", "?limit=500", "?annotationId=" + p.ID, "?recordType=node&targetId=" + p.ID} {
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", path+query, nil)
		if err != nil || status != 200 {
			t.Errorf("query %s: %d %s %v", query, status, body, err)
		}
	}
	for _, route := range s.routes() {
		if route.pattern == "GET /api/backend-projects/{id}/annotations" {
			if route.mcp != mcpAllow || route.checkpoint != cpRead {
				t.Fatalf("route policy %+v", route)
			}
			w := httptest.NewRecorder()
			route.handler(w, httptest.NewRequest(http.MethodGet, path, nil))
			if w.Code != 401 {
				t.Fatalf("unauthorized: %d", w.Code)
			}
		}
	}
}

func TestBackendAnnotationCommandBodyBound(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Notes", IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	request := fmt.Sprintf(`{"expectedVersion":1,"idempotencyKey":"rename","commands":[{"type":"rename_project","name":"Renamed"}]}%s`, strings.Repeat(" ", 1<<20))
	status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", "/api/backend-projects/"+p.ID+"/commands", []byte(request))
	if err != nil || status != 413 {
		t.Fatalf("body limit: %d %s %v", status, body, err)
	}
}

func annotationTransportSource(t *testing.T, s *Server) (*backendmodel.Project, backendmodel.AnnotationTarget) {
	t.Helper()
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Notes", IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	session, err := s.backendRepo.BeginImport(t.Context(), p.ID, transportImportFixture(*p))
	if err != nil {
		t.Fatal(err)
	}
	raw := `[{"op":"upsert_node","node":{"externalKey":"symbol","kind":"symbol","name":"Execute","attributes":{},"evidenceKeys":["proof"]}},{"op":"upsert_evidence","evidence":{"externalKey":"proof","subjectType":"node","subjectKey":"symbol","method":"ast","status":"explicit","source":{"repositoryId":"` + session.RepositoryID + `","snapshotId":"` + session.SnapshotID + `","file":"main.go","contentHash":"` + strings.Repeat("a", 64) + `"},"explanation":"declaration"}}]`
	var commands []backendmodel.ImportCommand
	if err := json.Unmarshal([]byte(raw), &commands); err != nil {
		t.Fatal(err)
	}
	hash, err := backendmodel.ImportBatchHash(commands)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.backendRepo.PutImportBatch(t.Context(), p.ID, session.ID, "source", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: commands})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.backendRepo.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("preview %+v %v", preview, err)
	}
	out, err := s.backendRepo.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "commit"})
	if err != nil {
		t.Fatal(err)
	}
	graph, err := s.backendRepo.QueryGraph(t.Context(), p.ID, backendmodel.GraphQueryInput{RevisionID: out.Revision.ID, RecordType: "nodes"})
	if err != nil || len(graph.Nodes) != 1 {
		t.Fatalf("graph %+v %v", graph, err)
	}
	return &out.Project, backendmodel.AnnotationTarget{RecordType: "node", ID: graph.Nodes[0].ID}
}

func TestBackendAnnotationAuthenticatedActorCSRFAndReplay(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	p, target := annotationTransportSource(t, s)
	h := s.Handler()
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", strings.NewReader(`{"name":"Reviewer","password":"`+testauth.Password+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	signed := httptest.NewRecorder()
	h.ServeHTTP(signed, login)
	if signed.Code != 200 {
		t.Fatalf("login %d %s", signed.Code, signed.Body)
	}
	var identity authResponse
	if err := json.Unmarshal(signed.Body.Bytes(), &identity); err != nil {
		t.Fatal(err)
	}
	input := backendmodel.CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "note", Commands: []backendmodel.Command{{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "human note"}}}
	raw, err := json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	path := "/api/backend-projects/" + p.ID + "/commands"
	request := func(body []byte, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest(http.MethodPost, "http://mocker.local"+path, bytes.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("X-CSRF-Token", csrf)
		for _, cookie := range signed.Result().Cookies() {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		return w
	}
	if w := request(raw, ""); w.Code != 403 {
		t.Fatalf("missing CSRF %d %s", w.Code, w.Body)
	}
	first := request(raw, identity.CSRFToken)
	if first.Code != 200 {
		t.Fatalf("write %d %s", first.Code, first.Body)
	}
	page, err := s.backendRepo.ListAnnotations(t.Context(), p.ID, backendmodel.AnnotationListInput{})
	if err != nil || len(page.Items) != 1 || page.Items[0].Author != "Reviewer" {
		t.Fatalf("actor %+v %v", page, err)
	}
	status, replay, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, raw)
	if err != nil || status != 200 || string(replay) != first.Body.String() {
		t.Fatalf("replay %d %s %v", status, replay, err)
	}
	page, err = s.backendRepo.ListAnnotations(t.Context(), p.ID, backendmodel.AnnotationListInput{})
	if err != nil || page.Items[0].Author != "Reviewer" {
		t.Fatalf("replay actor %+v %v", page, err)
	}
	invalid := bytes.Replace(raw, []byte(`"body":"human note"`), []byte(`"body":"human note","author":"forged"`), 1)
	if w := request(invalid, identity.CSRFToken); w.Code != 400 {
		t.Fatalf("forged author %d %s", w.Code, w.Body)
	}
	invalid = bytes.Replace(raw, []byte(`"body":"human note"`), []byte(`"body":null`), 1)
	if w := request(invalid, identity.CSRFToken); w.Code != 400 {
		t.Fatalf("null body %d %s", w.Code, w.Body)
	}
	if _, err := s.db.W.ExecContext(t.Context(), `UPDATE backend_projects SET version=9007199254740993 WHERE id=?`, p.ID); err != nil {
		t.Fatal(err)
	}
	input.ExpectedVersion = 9007199254740993
	input.IdempotencyKey = "edit"
	input.Commands[0].Type = "update_annotation"
	input.Commands[0].Body = "edited"
	raw, err = json.Marshal(input)
	if err != nil {
		t.Fatal(err)
	}
	status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "POST", path, raw)
	if err != nil || status != 200 || !bytes.Contains(body, []byte(`"version":9007199254740994`)) {
		t.Fatalf("exact version %d %s %v", status, body, err)
	}
	page, err = s.backendRepo.ListAnnotations(t.Context(), p.ID, backendmodel.AnnotationListInput{})
	user, e := s.mcpIdentity(t.Context())
	if err != nil || e != nil || page.Items[0].Author != user.Name {
		t.Fatalf("MCP actor %+v %v %v", page, err, e)
	}
}

func TestBackendAnnotationImportedDeletionOrphanEditAndCursorConflict(t *testing.T) {
	t.Parallel()
	s := loopbackTestServer(t, nil)
	p, target := annotationTransportSource(t, s)
	commands := []backendmodel.Command{{Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "keep me"}, {Type: "create_annotation", AnnotationID: uuid.NewV7().String(), Target: &target, Body: "second"}}
	call := func(method, path string, input any, want int) []byte {
		t.Helper()
		var raw []byte
		var err error
		if input != nil {
			raw, err = json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
		}
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, body, err)
		}
		return body
	}
	base := "/api/backend-projects/" + p.ID
	raw := call("POST", base+"/commands", backendmodel.CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "notes", Commands: commands}, 200)
	if err := json.Unmarshal(raw, &p); err != nil {
		t.Fatal(err)
	}
	var page backendmodel.AnnotationPage
	if err := json.Unmarshal(call("GET", base+"/annotations?limit=1", nil, 200), &page); err != nil || page.NextCursor == "" {
		t.Fatalf("page %+v %v", page, err)
	}
	staleCursor := page.NextCursor
	in := transportImportFixture(*p)
	in.Mode = "reconcile"
	in.IdempotencyKey = "delete-source"
	in.RepositoryID = new(p.Repositories[0].ID)
	in.GraphScope = &backendmodel.GraphScope{Profile: backendmodel.GraphProfile, Status: "complete", Gaps: []string{}}
	for i := range in.Inventory {
		in.Inventory[i].Status = "complete"
		in.Inventory[i].Reason = ""
		in.Inventory[i].Denominator = new(in.Inventory[i].KnownCount)
	}
	session, err := s.backendRepo.BeginImport(t.Context(), p.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	deletion := []backendmodel.ImportCommand{{Op: "delete_assertion", Deletion: &backendmodel.ImportDeletion{RecordType: "node", ExternalKey: "symbol", ExpectedID: target.ID, Reason: "removed"}}}
	hash, err := backendmodel.ImportBatchHash(deletion)
	if err != nil {
		t.Fatal(err)
	}
	batch, err := s.backendRepo.PutImportBatch(t.Context(), p.ID, session.ID, "deletion", backendmodel.ImportBatchInput{ExpectedImportVersion: session.Version, PayloadHash: hash, Commands: deletion})
	if err != nil {
		t.Fatal(err)
	}
	preview, err := s.backendRepo.PreviewImport(t.Context(), p.ID, session.ID, backendmodel.PreviewImportInput{ExpectedImportVersion: batch.AcceptedVersion, BaseRevisionID: p.CurrentRevisionID})
	if err != nil || preview.CandidateHash == nil {
		t.Fatalf("delete preview %+v %v", preview, err)
	}
	out, err := s.backendRepo.CommitImport(t.Context(), p.ID, session.ID, backendmodel.CommitImportInput{ExpectedVersion: p.Version, ExpectedImportVersion: preview.Version, CandidateHash: *preview.CandidateHash, IdempotencyKey: "delete-commit"})
	if err != nil {
		t.Fatal(err)
	}
	p = &out.Project
	call("GET", base+"/annotations?limit=1&cursor="+staleCursor, nil, 409)
	if err := json.Unmarshal(call("GET", base+"/annotations?orphaned=true", nil, 200), &page); err != nil || len(page.Items) != 2 {
		t.Fatalf("orphan list %+v %v", page, err)
	}
	commands[0].Type = "update_annotation"
	commands[0].Body = "still useful"
	edit := backendmodel.CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "orphan-edit", Commands: commands[:1]}
	first := call("POST", base+"/commands", edit, 200)
	if err := json.Unmarshal(first, &p); err != nil {
		t.Fatal(err)
	}
	if p.CurrentRevisionID != out.Revision.ID {
		t.Fatal("orphan edit changed source head")
	}
	if err := json.Unmarshal(call("GET", base+"/annotations?annotationId="+commands[0].AnnotationID, nil, 200), &page); err != nil || len(page.Items) != 1 || page.Items[0].Body != "still useful" || page.Items[0].TargetStatus != "orphaned" {
		t.Fatalf("orphan edit %+v %v", page, err)
	}
	if replay := call("POST", base+"/commands", edit, 200); !bytes.Equal(first, replay) {
		t.Fatal("orphan receipt changed")
	}
	bad := commands[0]
	missing := target
	missing.ID = uuid.NewV7().String()
	bad.Target = &missing
	call("POST", base+"/commands", backendmodel.CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "bad-rebind", Commands: []backendmodel.Command{bad}}, 404)
}
