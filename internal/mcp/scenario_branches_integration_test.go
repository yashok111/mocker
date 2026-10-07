package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/scenarioexport"
	"github.com/yashok111/mocker/internal/testauth"
)

func TestScenarioBranchesPersistedRESTAndMCP(t *testing.T) {
	t.Parallel()
	cfg := resourcesTestConfig(t)
	srv, _ := newResourcesTestServer(t, cfg)
	doc := designscenario.Document{
		FormatVersion: 2, Title: "Decision", Contracts: []designscenario.Contract{},
		Participants: []designscenario.Participant{{ID: "p", Kind: "service", Name: "P"}},
		Messages:     []designscenario.Message{{ID: "a", FromID: "p", ToID: "p", Kind: "request", Label: "A"}, {ID: "b", FromID: "p", ToID: "p", Kind: "request", Label: "B"}},
		Fragments: []designscenario.Fragment{
			{ID: "root", Kind: "alt", FromMessageID: "a", ToMessageID: "b", Branches: []designscenario.FragmentBranch{{ID: "yes", Label: "ok", FromMessageID: "a", ToMessageID: "a"}, {ID: "no", Label: "else", FromMessageID: "b", ToMessageID: "b"}}},
			{ID: "child", Kind: "loop", Label: "retry", FromMessageID: "a", ToMessageID: "a", ParentFragmentID: "root", ParentBranchID: "yes"},
		},
	}
	var created designscenario.Detail
	if msg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": doc}, &created); msg != "" {
		t.Fatal(msg)
	}
	var reread designscenario.Detail
	if msg := callDesignScenarioTool(t, srv, "get_design_scenario", map[string]any{"scenarioId": created.Scenario.ID}, &reread); msg != "" {
		t.Fatal(msg)
	}
	if reread.Draft.Document.Fragments[1].ParentBranchID != "yes" {
		t.Fatal("lost parent branch")
	}
	var viaMCP scenarioexport.Artifact
	if msg := callDesignScenarioTool(t, srv, "export_design_scenario", map[string]any{"scenarioId": created.Scenario.ID, "revisionId": created.Draft.ID, "format": "mermaid"}, &viaMCP); msg != "" {
		t.Fatal(msg)
	}
	handler := srv.Handler()
	login := httptest.NewRequest(http.MethodPost, "http://mocker.local/api/auth/login", strings.NewReader(fmt.Sprintf(`{"name":"Analyst","password":%q}`, testauth.Password)))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	auth := httptest.NewRecorder()
	handler.ServeHTTP(auth, login)
	if auth.Code != 200 {
		t.Fatal(auth.Body.String())
	}
	request := httptest.NewRequest(http.MethodGet, fmt.Sprintf("http://mocker.local/api/design-scenarios/%d/revisions/%d/exports/mermaid", created.Scenario.ID, created.Draft.ID), nil)
	request.AddCookie(auth.Result().Cookies()[0])
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	var viaREST scenarioexport.Artifact
	if err := jsonx.Unmarshal(response.Body.Bytes(), &viaREST); err != nil {
		t.Fatal(err)
	}
	if viaREST.Content != viaMCP.Content || !strings.Contains(viaREST.Content, "alt ok\nloop retry\np0 ->> p0: A\nend\nelse else\n") {
		t.Fatalf("incorrect export: %+v", viaREST)
	}
}
