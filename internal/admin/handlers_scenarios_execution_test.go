package admin

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/workspaces"
)

func TestExecuteDesignScenarioStepRequiresAuthentication(t *testing.T) {
	s := &Server{}
	r := httptest.NewRequest(http.MethodPost, "/api/design-scenarios/1/execute-step", strings.NewReader(`{"revisionId":1,"messageId":"call","pathParams":{},"query":{},"headers":{},"body":""}`))
	w := httptest.NewRecorder()
	for _, route := range s.routes() {
		if route.pattern == "POST /api/design-scenarios/{id}/execute-step" {
			route.handler(w, r)
			if w.Code != http.StatusUnauthorized {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
			return
		}
	}
	t.Fatal("execute-step endpoint is missing")
}

type scenarioStepExecutorFunc func(http.ResponseWriter, *http.Request, *workspaces.Workspace)

func (f scenarioStepExecutorFunc) ServeWorkspace(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) {
	f(w, r, ws)
}

func executionFixture(t *testing.T, path string) (*Server, *designscenario.Detail, *apidesign.Detail) {
	t.Helper()
	s := loopbackTestServer(t, nil)
	parameters := ""
	if strings.Contains(path, "{id}") {
		parameters = `"parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],`
	}
	doc := fmt.Sprintf(`{
      "openapi":"3.1.0","info":{"title":"Execution","version":"1"},
      "paths":{%q:{%s"get":{
        "x-mocker-canvas-operation-id":"read",
        "responses":{"201":{"description":"ok","content":{"application/json":{"schema":{
          "type":"object","properties":{"ok":{"type":"boolean","const":true}},"required":["ok"]
        }}}}}
      }}}
    }`, path, parameters)
	design, err := s.designsRepo.Create(t.Context(), apidesign.CreateInput{Name: "Execution", Document: doc, Source: "ui"})
	if err != nil {
		t.Fatalf("create API: %#v document=%s", err, doc)
	}
	canvas := designscenario.Document{FormatVersion: 1, Title: "Run", Participants: []designscenario.Participant{{ID: "api", Kind: "service"}}, Messages: []designscenario.Message{{ID: "call", FromID: "api", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "read"}}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "api", Name: "API", Mode: "linked", Document: jsonx.RawMessage(design.Draft.Document), Source: &designscenario.ContractSource{DesignID: design.Design.ID, RevisionID: design.Draft.ID, Version: design.Draft.Version}}}}
	scenario, err := s.designScenariosRepo.Create(t.Context(), designscenario.CreateInput{Document: canvas, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	s.SetScenarioExecutor(mockplane.New(s.cfg, s.ws, s.specsRepo, s.log))
	return s, scenario, design
}

func executeFixtureStep(t *testing.T, s *Server, scenario *designscenario.Detail, request map[string]any, ctx context.Context) *httptest.ResponseRecorder {
	t.Helper()
	request["revisionId"] = scenario.Draft.ID
	request["messageId"] = "call"
	for _, name := range []string{"pathParams", "query", "headers"} {
		if request[name] == nil {
			request[name] = map[string]string{}
		}
	}
	if request["body"] == nil {
		request["body"] = ""
	}
	raw, err := jsonx.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	r := httptest.NewRequest(http.MethodPost, fmt.Sprintf("/api/design-scenarios/%d/execute-step", scenario.Scenario.ID), strings.NewReader(string(raw)))
	r.SetPathValue("id", fmt.Sprint(scenario.Scenario.ID))
	r.Header.Set("Cookie", "admin=secret")
	r.Header.Set("Authorization", "Bearer admin-secret")
	r = r.WithContext(withAuthContext(ctx, &auth.Session{}, &auth.User{ID: 1}))
	w := httptest.NewRecorder()
	s.handleExecuteDesignScenarioStep(w, r)
	return w
}

func TestExecuteDesignScenarioStepServesSavedRevisionAndReportsMockStatus(t *testing.T) {
	s, scenario, design := executionFixture(t, "/items/{id}")
	// Editing the scenario does not change what this saved revision executes.
	changed := scenario.Draft.Document
	changed.Messages = []designscenario.Message{}
	if _, err := s.designScenariosRepo.Save(t.Context(), scenario.Scenario.ID, designscenario.SaveInput{ExpectedVersion: 1, Document: changed, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	w := executeFixtureStep(t, s, scenario, map[string]any{"pathParams": map[string]string{"id": "a/b"}, "query": map[string]string{"x": "one & two"}}, t.Context())
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
	var response map[string]any
	if err := jsonx.Unmarshal(w.Body.Bytes(), &response); err != nil {
		t.Fatal(err)
	}
	if response["status"] != float64(201) || response["designRevisionId"] != float64(design.Draft.ID) || response["scenarioRevisionId"] != float64(scenario.Draft.ID) || response["path"] != "/items/a%2Fb?x=one+%26+two" || !strings.Contains(response["body"].(string), `"ok":true`) {
		t.Fatalf("response=%s", w.Body.String())
	}
}

func TestExecuteDesignScenarioStepRejectsUnsafeTargetsAndHeaders(t *testing.T) {
	for _, tt := range []struct {
		name, path string
		input      map[string]any
	}{
		{"reserved", "/__mocker/state", nil},
		{"encoded reserved", "/%5f%5fmocker/state", nil},
		{"traversal", "/items/{id}", map[string]any{"pathParams": map[string]string{"id": ".."}}},
		{"host", "/items", map[string]any{"headers": map[string]string{"Host": "attacker.invalid"}}},
		{"cookie", "/items", map[string]any{"headers": map[string]string{"Cookie": "secret"}}},
		{"control", "/items", map[string]any{"headers": map[string]string{"X-Mocker-Status": "500"}}},
		{"CRLF", "/items", map[string]any{"headers": map[string]string{"X-Foo": "ok\r\nHost: evil"}}},
		{"bad header name", "/items", map[string]any{"headers": map[string]string{"bad header": "value"}}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			s, scenario, _ := executionFixture(t, tt.path)
			s.SetScenarioExecutor(scenarioStepExecutorFunc(func(http.ResponseWriter, *http.Request, *workspaces.Workspace) {
				t.Error("unsafe request reached runtime")
			}))
			if tt.input == nil {
				tt.input = map[string]any{}
			}
			w := executeFixtureStep(t, s, scenario, tt.input, t.Context())
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestExecuteDesignScenarioStepRejectsStaleAPI(t *testing.T) {
	s, scenario, design := executionFixture(t, "/items")
	if _, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: strings.ReplaceAll(design.Draft.Document, "/items", "/other"), Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	w := executeFixtureStep(t, s, scenario, map[string]any{}, t.Context())
	if w.Code != 409 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExecuteDesignScenarioStepAcceptsEmptyUIFormEnvelope(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	saved, err := s.designScenariosRepo.Save(t.Context(), scenario.Scenario.ID, designscenario.SaveInput{ExpectedVersion: 1, Document: scenario.Draft.Document, FormDrafts: map[string]string{"all": "{}"}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	w := executeFixtureStep(t, s, saved, map[string]any{}, t.Context())
	if w.Code != 200 {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExecuteDesignScenarioStepReportsMockFailureWithoutForwardingAdminCredentials(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(w http.ResponseWriter, r *http.Request, _ *workspaces.Workspace) {
		if r.Header.Get("Cookie") != "" || r.Header.Get("Authorization") != "Bearer test-token" || r.Host != "" {
			t.Errorf("credentials or host leaked into mock request: %+v", r)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusTeapot)
		_, _ = w.Write([]byte(`{"error":"mock failure"}`))
	}))
	w := executeFixtureStep(t, s, scenario, map[string]any{"headers": map[string]string{"Authorization": "Bearer test-token"}}, t.Context())
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"status":418`) || !strings.Contains(w.Body.String(), `mock failure`) {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExecuteDesignScenarioStepBoundsResponseAndPropagatesCancellation(t *testing.T) {
	for _, overflow := range []bool{false, true} {
		t.Run(fmt.Sprint(overflow), func(t *testing.T) {
			s, scenario, _ := executionFixture(t, "/items")
			s.cfg.MaxResponse = 16
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			s.SetScenarioExecutor(scenarioStepExecutorFunc(func(w http.ResponseWriter, r *http.Request, _ *workspaces.Workspace) {
				if overflow {
					if _, err := w.Write([]byte(strings.Repeat("x", 17))); err == nil {
						t.Error("oversize write succeeded")
					}
				} else {
					cancel()
				}
				if r.Context().Err() == nil {
					t.Error("runtime context was not cancelled")
				}
			}))
			w := executeFixtureStep(t, s, scenario, map[string]any{}, ctx)
			want := http.StatusGatewayTimeout
			if overflow {
				want = http.StatusRequestEntityTooLarge
			}
			if w.Code != want {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestExecuteDesignScenarioStepDetectsAPIEditedDuringDispatch(t *testing.T) {
	s, scenario, design := executionFixture(t, "/items")
	s.SetScenarioExecutor(scenarioStepExecutorFunc(func(w http.ResponseWriter, _ *http.Request, snapshot *workspaces.Workspace) {
		if _, err := s.designsRepo.Save(t.Context(), design.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: strings.ReplaceAll(design.Draft.Document, "/items", "/new"), Source: "ui"}); err != nil {
			t.Fatal(err)
		}
		current, err := s.ws.ByID(t.Context(), snapshot.ID)
		if err != nil {
			t.Fatal(err)
		}
		if current.Revision == snapshot.Revision || *current.SpecID == *snapshot.SpecID {
			t.Error("workspace snapshot was not retained")
		}
		_, _ = w.Write([]byte("old response"))
	}))
	w := executeFixtureStep(t, s, scenario, map[string]any{}, t.Context())
	if w.Code != http.StatusConflict {
		t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
	}
}

func TestExecuteDesignScenarioStepRejectsNonExecutableSavedMessages(t *testing.T) {
	for _, kind := range []string{"detached", "missing operation", "disabled", "fragment", "unfinished form", "missing message"} {
		t.Run(kind, func(t *testing.T) {
			s, scenario, _ := executionFixture(t, "/items")
			document := scenario.Draft.Document
			drafts := map[string]string{}
			switch kind {
			case "detached":
				document.Contracts[0].Mode = "copy"
			case "missing operation":
				document.Messages[0].Operation.OperationKey = "missing"
			case "disabled":
				document.Messages[0].Execution = &designscenario.StepExecution{PathParams: map[string]string{}, Query: map[string]string{}, Headers: map[string]string{}, Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}}
			case "fragment":
				document.Fragments = []designscenario.Fragment{{ID: "loop", Kind: "loop", FromMessageID: "call", ToMessageID: "call"}}
			case "unfinished form":
				drafts["all"] = `{"/foo":{"source":"{","propertySource":""}}`
			case "missing message":
				document.Messages = []designscenario.Message{}
			}
			saved, err := s.designScenariosRepo.Save(t.Context(), scenario.Scenario.ID, designscenario.SaveInput{ExpectedVersion: 1, Document: document, FormDrafts: drafts, Source: "ui"})
			if err != nil {
				t.Fatal(err)
			}
			s.SetScenarioExecutor(scenarioStepExecutorFunc(func(http.ResponseWriter, *http.Request, *workspaces.Workspace) {
				t.Error("non-executable message reached runtime")
			}))
			w := executeFixtureStep(t, s, saved, map[string]any{}, t.Context())
			if w.Code != 400 {
				t.Fatalf("status=%d body=%s", w.Code, w.Body.String())
			}
		})
	}
}

func TestExecuteDesignScenarioStepRejectsMalformedWireInput(t *testing.T) {
	s, scenario, _ := executionFixture(t, "/items")
	for _, tail := range []string{`"headers":{}`, `"headers":{},"body":null`, `"headers":{"X-Test":null},"body":""`, `"headers":{},"body":"","url":"http://evil.invalid"`} {
		raw := fmt.Sprintf(`{"revisionId":%d,"messageId":"call","pathParams":{},"query":{},%s}`, scenario.Draft.ID, tail)
		r := httptest.NewRequest(http.MethodPost, "/", strings.NewReader(raw))
		r.SetPathValue("id", fmt.Sprint(scenario.Scenario.ID))
		r = r.WithContext(withAuthContext(t.Context(), &auth.Session{}, &auth.User{ID: 1}))
		w := httptest.NewRecorder()
		s.handleExecuteDesignScenarioStep(w, r)
		if w.Code != 400 {
			t.Errorf("body=%s status=%d response=%s", raw, w.Code, w.Body.String())
		}
	}
}
