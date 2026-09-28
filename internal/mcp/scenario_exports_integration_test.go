package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/scenarioexport"
	"github.com/yashok111/mocker/internal/testauth"
)

func TestScenarioExportsPersistedRESTAndMCP(t *testing.T) {
	cfg := resourcesTestConfig(t)
	srv, db := newResourcesTestServer(t, cfg)
	designs := apidesign.NewRepo(db, cfg)
	repo := designscenario.NewRepo(db, cfg, designs)
	const raw = `{ "openapi":"3.1.0", "info":{"title":"First","version":"1"}, "paths":{"/status":{"get":{"x-mocker-canvas-operation-id":"status","responses":{"200":{"description":"ok"}}}}}, "x-number":9007199254740993 }`
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "First", Document: raw, Source: "ui"})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	doc := designscenario.Document{FormatVersion: 1, Title: "Two APIs", Participants: []designscenario.Participant{{ID: "client", Name: "Client", Kind: "client"}, {ID: "a", Name: "A", Kind: "service"}, {ID: "b", Name: "B", Kind: "service"}}, Messages: []designscenario.Message{{ID: "a-call", FromID: "client", ToID: "a", Kind: "request", Label: "GET /status", Operation: &designscenario.OperationBinding{ContractID: "a", OperationKey: "status"}}, {ID: "b-call", FromID: "client", ToID: "b", Kind: "request", Label: "GET /status", Operation: &designscenario.OperationBinding{ContractID: "b", OperationKey: "status"}}}, Contracts: []designscenario.Contract{}, Fragments: []designscenario.Fragment{}}
	created, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	// Bindings require contracts, so create the complete snapshot below.
	if err == nil {
		t.Fatal("invalid document unexpectedly accepted")
	}
	doc.Messages[0].Operation = nil
	doc.Messages[1].Operation = nil
	created, err = repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	linked, err := repo.Apply(t.Context(), created.Scenario.ID, designscenario.CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []designscenario.Command{{Type: "import_contract", ID: "a", DesignID: api.Design.ID, Mode: "linked"}, {Type: "create_contract", Contract: &designscenario.Contract{ID: "b", Name: "Second", Document: jsonx.RawMessage(strings.ReplaceAll(raw, "First", "Second"))}}, {Type: "bind_operation", MessageID: "a-call", ContractID: "a", OperationKey: "status"}, {Type: "bind_operation", MessageID: "b-call", ContractID: "b", OperationKey: "status"}}})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	handler := srv.Handler()
	login := httptest.NewRequest("POST", "http://mocker.local/api/auth/login", strings.NewReader(fmt.Sprintf(`{"name":"Analyst","password":%q}`, testauth.Password)))
	login.Header.Set("Content-Type", "application/json")
	login.Header.Set("Origin", "http://mocker.local")
	auth := httptest.NewRecorder()
	handler.ServeHTTP(auth, login)
	if auth.Code != 200 {
		t.Fatal(auth.Body.String())
	}
	cookie := auth.Result().Cookies()[0]
	var loginBody struct {
		CSRFToken string `json:"csrfToken"`
	}
	if err := jsonx.Unmarshal(auth.Body.Bytes(), &loginBody); err != nil {
		t.Fatal(err)
	}
	archiveRequest := func(rev designscenario.Revision, csrf string) *httptest.ResponseRecorder {
		t.Helper()
		path := fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/archive", rev.ScenarioID, rev.ID)
		r := httptest.NewRequest("POST", "http://mocker.local"+path, strings.NewReader(`{"items":[{"format":"mermaid"},{"format":"openapi-json","contractId":"a"},{"format":"markdown"}]}`))
		r.AddCookie(cookie)
		r.Header.Set("Content-Type", "application/json")
		r.Header.Set("Origin", "http://mocker.local")
		r.Header.Set("X-CSRF-Token", csrf)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	archive := func(rev designscenario.Revision) scenarioexport.ArchiveArtifact {
		t.Helper()
		response := archiveRequest(rev, loginBody.CSRFToken)
		if response.Code != 200 {
			t.Fatalf("archive: %d %s", response.Code, response.Body.String())
		}
		var viaHTTP, viaMCP scenarioexport.ArchiveArtifact
		if err := jsonx.Unmarshal(response.Body.Bytes(), &viaHTTP); err != nil {
			t.Fatal(err)
		}
		args := map[string]any{"scenarioId": rev.ScenarioID, "revisionId": rev.ID, "items": []scenarioexport.Request{{Format: scenarioexport.Mermaid}, {Format: scenarioexport.OpenAPIJSON, ContractID: "a"}, {Format: scenarioexport.Markdown}}}
		if msg := callDesignScenarioTool(t, srv, "export_design_scenario_archive", args, &viaMCP); msg != "" {
			t.Fatal(msg)
		}
		if !reflect.DeepEqual(viaHTTP, viaMCP) || viaMCP.RevisionID != rev.ID || viaMCP.SourceHash != rev.Hash || len(viaMCP.Manifest.Files) != 3 {
			t.Fatal("REST/MCP archives differ or wrong snapshot")
		}
		return viaMCP
	}
	if response := archiveRequest(linked.Draft, ""); response.Code != 403 {
		t.Fatalf("archive missing CSRF: %d", response.Code)
	}

	read := func(path string) *httptest.ResponseRecorder {
		t.Helper()
		r := httptest.NewRequest("GET", "http://mocker.local"+path, nil)
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		return w
	}
	export := func(rev designscenario.Revision, contract string) scenarioexport.Artifact {
		t.Helper()
		args := map[string]any{"scenarioId": rev.ScenarioID, "revisionId": rev.ID, "format": "openapi-json", "contractId": contract}
		var viaMCP scenarioexport.Artifact
		if msg := callDesignScenarioTool(t, srv, "export_design_scenario", args, &viaMCP); msg != "" {
			t.Fatal(msg)
		}
		response := read(fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/exports/openapi-json?contractId=%s", rev.ScenarioID, rev.ID, contract))
		if response.Code != 200 {
			t.Fatal(response.Body.String())
		}
		var viaHTTP scenarioexport.Artifact
		if err := jsonx.Unmarshal(response.Body.Bytes(), &viaHTTP); err != nil {
			t.Fatalf("%#v", err)
		}
		if !reflect.DeepEqual(viaHTTP, viaMCP) {
			t.Fatal("REST/MCP differ")
		}
		var snapshot string
		for _, c := range rev.Document.Contracts {
			if c.ID == contract {
				snapshot = string(c.Document)
			}
		}
		if viaMCP.Content != snapshot || !strings.Contains(snapshot, "9007199254740993") || viaMCP.SourceHash != rev.Hash || viaMCP.RevisionID != rev.ID {
			t.Fatalf("snapshot changed: %+v", viaMCP)
		}
		return viaMCP
	}
	beforeCounts := func() map[string]int {
		t.Helper()
		out := map[string]int{}
		for _, table := range []string{"design_scenarios", "design_scenario_revisions", "api_designs", "api_design_revisions", "workspaces"} {
			var n int
			if err := db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&n); err != nil {
				t.Fatalf("%#v", err)
			}
			out[table] = n
		}
		return out
	}
	counts := beforeCounts()
	archiveBefore := archive(linked.Draft)
	httpExport := func(rev designscenario.Revision, format string) scenarioexport.Artifact {
		t.Helper()
		var viaMCP scenarioexport.Artifact
		args := map[string]any{"scenarioId": rev.ScenarioID, "revisionId": rev.ID, "format": format}
		if message := callDesignScenarioTool(t, srv, "export_design_scenario", args, &viaMCP); message != "" {
			t.Fatal(message)
		}
		response := read(fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/exports/%s", rev.ScenarioID, rev.ID, format))
		if response.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", format, response.Code, response.Body.String())
		}
		var viaHTTP scenarioexport.Artifact
		if err := jsonx.Unmarshal(response.Body.Bytes(), &viaHTTP); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(viaHTTP, viaMCP) || viaMCP.RevisionID != rev.ID || viaMCP.SourceHash != rev.Hash {
			t.Fatalf("%s changed snapshot or transport content", format)
		}
		if !strings.Contains(viaMCP.Content, "/status") {
			t.Fatalf("%s lost the HTTP operation", format)
		}
		return viaMCP
	}
	markdownBefore := httpExport(linked.Draft, "markdown")
	htmlBefore := httpExport(linked.Draft, "html")
	for _, artifact := range []scenarioexport.Artifact{markdownBefore, htmlBefore} {
		if !strings.Contains(artifact.Content, "9007199254740993") {
			t.Fatal("documentation rounded saved contract")
		}
	}
	postmanBefore := httpExport(linked.Draft, "postman")
	curlBefore := httpExport(linked.Draft, "curl")
	first := export(linked.Draft, "a")
	second := export(linked.Draft, "b")
	if first.Content == second.Content {
		t.Fatal("two services collapsed")
	}
	if !reflect.DeepEqual(counts, beforeCounts()) {
		t.Fatal("export created resources")
	}
	after, err := repo.Detail(t.Context(), linked.Scenario.ID)
	if err != nil {
		t.Fatalf("%#v", err)
	}
	if !reflect.DeepEqual(after.Scenario, linked.Scenario) || !reflect.DeepEqual(after.Revisions, linked.Revisions) {
		t.Fatal("export changed scenario history")
	}
	_, err = designs.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: api.Design.Version, Document: strings.ReplaceAll(raw, "First", "Changed"), Source: "ui"})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	if !reflect.DeepEqual(first, export(linked.Draft, "a")) {
		t.Fatal("linked API changed historical export")
	}
	newer, err := repo.Apply(t.Context(), linked.Scenario.ID, designscenario.CommandsInput{ExpectedVersion: linked.Scenario.Version, Source: "ui", Commands: []designscenario.Command{{Type: "refresh_contract", ContractID: "a"}, {Type: "set_title", Title: "New revision"}}})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	if !strings.Contains(export(newer.Draft, "a").Content, "Changed") {
		t.Fatal("new revision did not update contract")
	}
	if !reflect.DeepEqual(first, export(linked.Draft, "a")) {
		t.Fatal("draft edit changed historical export")
	}
	if !reflect.DeepEqual(postmanBefore, httpExport(linked.Draft, "postman")) || !reflect.DeepEqual(curlBefore, httpExport(linked.Draft, "curl")) {
		t.Fatal("API or scenario edit changed historical HTTP exports")
	}
	if !reflect.DeepEqual(markdownBefore, httpExport(linked.Draft, "markdown")) || !reflect.DeepEqual(htmlBefore, httpExport(linked.Draft, "html")) {
		t.Fatal("API or scenario edit changed historical documentation")
	}

	if !reflect.DeepEqual(archiveBefore, archive(linked.Draft)) {
		t.Fatal("edits changed historical ZIP")
	}
	other, err := repo.Create(t.Context(), designscenario.CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	response := read(fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/exports/mermaid", other.Scenario.ID, linked.Draft.ID))
	if response.Code != http.StatusNotFound {
		t.Fatalf("cross-scenario read: %d", response.Code)
	}

	wrongScenario := linked.Draft
	wrongScenario.ScenarioID = other.Scenario.ID
	if response := archiveRequest(wrongScenario, loginBody.CSRFToken); response.Code != 404 {
		t.Fatalf("cross-scenario archive: %d", response.Code)
	}
	// Pending forms produce identical structured diagnostics through both transports.
	pending, err := repo.Save(t.Context(), newer.Scenario.ID, designscenario.SaveInput{ExpectedVersion: newer.Scenario.Version, Document: newer.Draft.Document, FormDrafts: map[string]string{"pending": "{"}, Source: "ui"})
	if err != nil {
		t.Fatalf("%#v", err)
	}
	path := fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/exports/openapi-json?contractId=a", pending.Scenario.ID, pending.Draft.ID)
	response = read(path)
	if response.Code != 422 || !strings.Contains(response.Body.String(), "api_forms_pending") {
		t.Fatalf("pending: %d %s", response.Code, response.Body.String())
	}
	var ignored any
	args := map[string]any{"scenarioId": pending.Scenario.ID, "revisionId": pending.Draft.ID, "format": "openapi-json", "contractId": "a"}
	if msg := callDesignScenarioTool(t, srv, "export_design_scenario", args, &ignored); !strings.Contains(msg, "422") || !strings.Contains(msg, "api_forms_pending") {
		t.Fatalf("MCP pending: %s", msg)
	}

	if response := archiveRequest(pending.Draft, loginBody.CSRFToken); response.Code != 422 || !strings.Contains(response.Body.String(), "api_forms_pending") || strings.Contains(response.Body.String(), "contentBase64") {
		t.Fatalf("atomic archive failure: %d %s", response.Code, response.Body.String())
	}
	archiveArgs := map[string]any{"scenarioId": pending.Scenario.ID, "revisionId": pending.Draft.ID, "items": []scenarioexport.Request{{Format: scenarioexport.Mermaid}, {Format: scenarioexport.OpenAPIJSON, ContractID: "a"}}}
	if msg := callDesignScenarioTool(t, srv, "export_design_scenario_archive", archiveArgs, &ignored); !strings.Contains(msg, "422") || !strings.Contains(msg, "api_forms_pending") {
		t.Fatalf("MCP archive diagnostics: %s", msg)
	}
	for _, format := range []string{"markdown", "html"} {
		artifact := httpExport(pending.Draft, format)
		if !strings.Contains(strings.ReplaceAll(artifact.Content, `\_`, `_`), "api_forms_pending") {
			t.Fatal("pending forms warning absent")
		}
	}
	cfg.MaxBody = 300
	if response := archiveRequest(linked.Draft, loginBody.CSRFToken); response.Code != 413 {
		t.Fatalf("archive output budget: %d %s", response.Code, response.Body.String())
	}
	if msg := callDesignScenarioTool(t, srv, "export_design_scenario_archive", archiveArgs, &ignored); !strings.Contains(msg, "413") {
		t.Fatalf("MCP archive budget: %s", msg)
	}

	if response = read(path); response.Code != 413 {
		t.Fatalf("diagnostic limit: %d %s", response.Code, response.Body.String())
	}
	if msg := callDesignScenarioTool(t, srv, "export_design_scenario", args, &ignored); !strings.Contains(msg, "413") {
		t.Fatalf("MCP budget: %s", msg)
	}
}
