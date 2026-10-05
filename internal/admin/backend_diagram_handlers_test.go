package admin

import (
	"encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/testauth"
)

func TestBackendDiagramRESTOperationsAndStrictAdmission(t *testing.T) {
	s := loopbackTestServer(t, nil)
	p, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Architecture", IdempotencyKey: "create-project"})
	if err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/" + p.ID
	call := func(method, path string, in any, want int) []byte {
		t.Helper()
		var raw []byte
		if value, ok := in.(string); ok {
			raw = []byte(value)
		} else if in != nil {
			raw, _ = json.Marshal(in)
		}
		status, body, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), method, path, raw)
		if err != nil || status != want {
			t.Fatalf("%s %s: %d %s %v", method, path, status, body, err)
		}
		return body
	}
	doc := backendmodel.DiagramDocument{Format: "backend-diagram-v1", Kind: "architecture", Target: backendmodel.BackendReadTarget{RevisionID: p.CurrentRevisionID}, Payload: backendmodel.ArchitecturePayload{PrimarySystemID: p.ID, Elements: []backendmodel.ArchitectureElement{{ID: p.ID, Label: "Orders", Role: "software_system", Origin: backendmodel.DiagramOrigin{Kind: "authored", Reason: "Explicit boundary"}, Refs: []backendmodel.DiagramRef{}}}, Links: []backendmodel.ArchitectureLink{}}}
	raw := call("POST", base+"/diagrams", backendmodel.DiagramCreateInput{Document: doc, IdempotencyKey: "diagram"}, 200)
	var v backendmodel.DiagramVersion
	if err = json.Unmarshal(raw, &v); err != nil {
		t.Fatal(err)
	}
	path := base + "/diagrams/" + v.Pin.ID
	for _, op := range []struct{ method, path string }{{"GET", base + "/diagrams"}, {"POST", base + "/diagrams"}, {"POST", path + "/save"}, {"POST", base + "/diagrams/fork"}, {"POST", base + "/diagrams/query"}, {"POST", base + "/diagrams/compare"}, {"GET", path + "/versions/1?hash=" + v.Pin.ContentHash}, {"POST", base + "/diagram-views"}, {"POST", base + "/diagram-views/" + p.ID + "/save"}, {"GET", base + "/diagram-views"}, {"GET", base + "/diagram-views/" + p.ID + "/versions/1"}} {
		req := httptest.NewRequest(op.method, "http://mocker.local"+op.path, nil)
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Origin", "http://mocker.local")
		rec := httptest.NewRecorder()
		s.Handler().ServeHTTP(rec, req)
		if rec.Code != 401 {
			t.Fatalf("auth %s: %d", op.path, rec.Code)
		}
	}
	call("GET", base+"/diagrams?limit=1", nil, 200)
	exact := call("GET", path+"/versions/1?hash="+v.Pin.ContentHash, nil, 200)
	if string(raw) != string(exact) {
		t.Fatal("exact read changed receipt")
	}
	call("POST", path+"/save", backendmodel.DiagramSaveInput{Document: doc, ExpectedVersion: 1, IdempotencyKey: "noop"}, 200)
	call("POST", base+"/diagrams/fork", backendmodel.DiagramForkInput{Source: v.Pin, Target: doc.Target, Reason: "Fork", IdempotencyKey: "fork"}, 200)
	call("POST", base+"/diagrams/query", backendmodel.DiagramQueryInput{Pin: v.Pin, Level: "context", RootID: p.ID, Origin: "all", Section: "elements", Limit: 100}, 200)
	call("POST", base+"/diagrams/compare", backendmodel.DiagramCompareInput{Before: v.Pin, After: v.Pin, Limit: 100}, 200)
	state := backendmodel.DiagramViewState{Diagram: v.Pin, Level: "context", RootID: p.ID, Origin: "all", Positions: []backendmodel.DiagramPosition{}, CollapsedIDs: []string{}}
	saved := call("POST", base+"/diagram-views", backendmodel.DiagramCreateViewInput{Name: "Pinned", State: state, IdempotencyKey: "view"}, 200)
	var view backendmodel.DiagramView
	if err = json.Unmarshal(saved, &view); err != nil {
		t.Fatal(err)
	}
	call("POST", base+"/diagram-views/"+view.ID+"/save", backendmodel.DiagramSaveViewInput{Name: "Changed", State: state, ExpectedVersion: 1, IdempotencyKey: "layout"}, 200)
	call("GET", base+"/diagram-views", nil, 200)
	call("GET", base+"/diagram-views/"+view.ID+"/versions/1", nil, 200)
	for _, query := range []string{"?limit=0", "?limit=501", "?limit=1&limit=2", "?unknown=x", "?kind=lifecycle"} {
		call("GET", base+"/diagrams"+query, nil, 422)
	}
	call("GET", path+"/versions/1", nil, 422)
	call("GET", path+"/versions/1?hash="+strings.Repeat("a", 64), nil, 409)
	call("GET", path+"/versions/9223372036854775808?hash="+v.Pin.ContentHash, nil, 422)
	call("POST", base+"/diagrams", `{"document":null,"idempotencyKey":"bad"}`, 422)
	call("POST", base+"/diagrams", strings.Repeat(" ", 1<<20+1), 413)
	other, err := s.backendRepo.Create(t.Context(), backendmodel.CreateInput{Name: "Other", IdempotencyKey: "other"})
	if err != nil {
		t.Fatal(err)
	}
	call("GET", "/api/backend-projects/"+other.ID+"/diagrams/"+v.Pin.ID+"/versions/1?hash="+v.Pin.ContentHash, nil, 404)
}

func TestBackendDiagramCapabilitiesAdmittedKinds(t *testing.T) {
	s := loopbackTestServer(t, nil)
	status, raw, err := s.CallAsMCP(t.Context(), loopbackTestSrc(), "GET", "/api/backend-projects/capabilities", nil)
	if err != nil || status != 200 {
		t.Fatalf("capabilities %d %s %v", status, raw, err)
	}
	var value struct {
		DiagramSupport struct {
			Kinds               []string `json:"kinds"`
			DocumentVersion     string   `json:"documentVersion"`
			ViewDocumentVersion string   `json:"viewDocumentVersion"`
		} `json:"diagramSupport"`
	}
	if err = json.Unmarshal(raw, &value); err != nil {
		t.Fatal(err)
	}
	if len(value.DiagramSupport.Kinds) != 2 || value.DiagramSupport.Kinds[1] != "interactions" || value.DiagramSupport.Kinds[0] != "architecture" || value.DiagramSupport.DocumentVersion != "backend-diagram-v1" || value.DiagramSupport.ViewDocumentVersion != "diagram-view-v1" {
		t.Fatalf("architecture capability missing/overclaims future kinds: %+v", value)
	}
}

func TestBackendDiagramRESTCSRF(t *testing.T) {
	s := loopbackTestServer(t, nil)
	handler := s.Handler()
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", strings.NewReader(`{"name":"diagram-csrf","password":"`+testauth.Password+`"}`))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, login)
	if response.Code != 200 {
		t.Fatal(response.Code, response.Body.String())
	}
	cookies := response.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatal("session missing")
	}
	var auth struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &auth); err != nil {
		t.Fatal(err)
	}
	base := "/api/backend-projects/10000000-0000-4000-8000-000000000001"
	for _, suffix := range []string{"/diagrams/interactions/build", "/diagrams", "/diagrams/fork", "/diagrams/query", "/diagrams/compare", "/diagrams/10000000-0000-4000-8000-000000000002/save", "/diagram-views", "/diagram-views/10000000-0000-4000-8000-000000000002/save"} {
		for _, foreign := range []bool{false, true} {
			req := httptest.NewRequest(http.MethodPost, "http://mocker.local"+base+suffix, strings.NewReader(`{}`))
			req.Header.Set("Content-Type", "application/json")
			req.AddCookie(cookies[0])
			req.Header.Set("Origin", "http://mocker.local")
			if foreign {
				req.Header.Set("Origin", "http://foreign.invalid")
				req.Header.Set("X-CSRF-Token", auth.CSRFToken)
			}
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, req)
			if rec.Code != 403 {
				t.Fatalf("CSRF %s foreign=%v: %d %s", suffix, foreign, rec.Code, rec.Body.String())
			}
		}
	}
}
