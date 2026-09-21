package mcp

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/yashok111/mocker/internal/admin"
	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/mockplane"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

const scenarioRunsAPIFixture = `{
  "openapi":"3.1.0","info":{"title":"MCP sequence","version":"1"},
  "paths":{
    "/login":{"post":{
      "x-mocker-canvas-operation-id":"login",
      "requestBody":{"content":{"application/json":{"schema":{"type":"object","properties":{"user":{"type":"string"}}}}}},
      "responses":{"200":{"description":"Logged in","content":{"application/json":{"schema":{
        "type":"object","required":["token","id"],"properties":{
          "token":{"type":"string","const":"token-1"},
          "id":{"type":"integer","const":9007199254740993}
        }
      }}}}}
    }},
    "/profile/{id}":{"get":{
      "x-mocker-canvas-operation-id":"profile",
      "parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"string"}}],
      "responses":{"200":{"description":"Profile","content":{"application/json":{"schema":{
        "type":"object","required":["active"],"properties":{"active":{"type":"boolean","const":true}}
      }}}}}
    }}
  }
}`

// This gate pauses the second real mockplane request so progress is observable
// without relying on database timing; the counter also detects request replay.
type scenarioRunsObservedExecutor struct {
	plane          *mockplane.Plane
	dispatched     atomic.Int32
	profileStarted chan struct{}
	releaseProfile chan struct{}
	releaseOnce    sync.Once
}

func (e *scenarioRunsObservedExecutor) release() {
	e.releaseOnce.Do(func() { close(e.releaseProfile) })
}

func (e *scenarioRunsObservedExecutor) ServeWorkspace(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace) {
	e.dispatched.Add(1)
	if strings.HasPrefix(r.URL.Path, "/profile/") {
		select {
		case e.profileStarted <- struct{}{}:
		default:
		}
		select {
		case <-e.releaseProfile:
		case <-r.Context().Done():
			return
		}
	}
	e.plane.ServeWorkspace(w, r, ws)
}

func scenarioRunsMCPFixture(t *testing.T) (*admin.Server, designscenario.Detail, *scenarioRunsObservedExecutor) {
	t.Helper()
	cfg := resourcesTestConfig(t)
	srv, db := newResourcesTestServer(t, cfg)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := srv.CloseScenarioRuns(ctx); err != nil {
			t.Errorf("close scenario runs: %v", err)
		}
	})
	executor := &scenarioRunsObservedExecutor{
		plane:          mockplane.New(cfg, workspaces.NewRepo(db), specs.NewRepo(db, cfg), slog.New(slog.NewTextHandler(io.Discard, nil))),
		profileStarted: make(chan struct{}, 1), releaseProfile: make(chan struct{}),
	}
	t.Cleanup(executor.release)
	srv.SetScenarioExecutor(executor)
	var api apidesign.Detail
	if errMsg := callDesignScenarioTool(t, srv, "create_api_design", map[string]any{"name": "MCP sequence", "document": scenarioRunsAPIFixture}, &api); errMsg != "" {
		t.Fatal(errMsg)
	}
	config := func() *designscenario.StepExecution {
		return &designscenario.StepExecution{Enabled: true, PathParams: designscenario.ExecutionValues{}, Query: designscenario.ExecutionValues{}, Headers: designscenario.ExecutionValues{}, Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}}
	}
	login, profile := config(), config()
	login.Headers["Content-Type"] = "application/json"
	login.Body = `{"user":"{{user}}"}`
	login.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage(`9007199254740993`)}, {Pointer: "/token", Equals: jsonx.RawMessage(`"token-1"`)}}
	login.Extract = []designscenario.ExecutionExtraction{{Name: "token", Pointer: "/token"}, {Name: "id", Pointer: "/id"}}
	profile.PathParams["id"] = "{{id}}"
	profile.Query["user"] = "{{user}}"
	profile.Headers["Authorization"] = "Bearer {{token}}"
	profile.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/active", Equals: jsonx.RawMessage(`true`)}}
	document := designscenario.Document{
		FormatVersion: 1, Title: "Login and profile",
		Participants: []designscenario.Participant{{ID: "client", Name: "Client", Kind: "client"}, {ID: "api", Name: "API", Kind: "service"}},
		Messages: []designscenario.Message{
			{ID: "login", FromID: "client", ToID: "api", Kind: "request", Label: "Login", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "login"}, Execution: login},
			{ID: "profile", FromID: "client", ToID: "api", Kind: "request", Label: "Read profile", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "profile"}, Execution: profile},
		},
		Fragments: []designscenario.Fragment{}, Execution: &designscenario.Execution{Variables: designscenario.ExecutionValues{"user": "saved-default"}},
		Contracts: []designscenario.Contract{{ID: "api", Name: "MCP API", Mode: "linked", Document: jsonx.RawMessage(api.Draft.Document), Source: &designscenario.ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}}},
	}
	var scenario designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": document, "formDrafts": map[string]string{"all": "{}"}}, &scenario); errMsg != "" {
		t.Fatal(errMsg)
	}
	return srv, scenario, executor
}

func awaitScenarioRunProfile(t *testing.T, executor *scenarioRunsObservedExecutor) {
	t.Helper()
	select {
	case <-executor.profileStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("run did not reach its profile request")
	case <-t.Context().Done():
		t.Fatal(t.Context().Err())
	}
}

func readScenarioRunMCP(t *testing.T, calls Caller, scenarioID int64, runID string) designscenario.RunReport {
	t.Helper()
	var report designscenario.RunReport
	if errMsg := callDesignScenarioTool(t, calls, "get_design_scenario_run", map[string]any{"scenarioId": scenarioID, "runId": runID}, &report); errMsg != "" {
		t.Fatal(errMsg)
	}
	return report
}

func awaitScenarioRunTerminalMCP(t *testing.T, calls Caller, scenarioID int64, runID string) designscenario.RunReport {
	t.Helper()
	deadline := time.NewTimer(5 * time.Second)
	defer deadline.Stop()
	poll := time.NewTicker(10 * time.Millisecond)
	defer poll.Stop()
	for {
		report := readScenarioRunMCP(t, calls, scenarioID, runID)
		if report.Status != "running" {
			return report
		}
		select {
		case <-poll.C:
		case <-deadline.C:
			t.Fatalf("run %s did not finish: %+v", runID, report)
		case <-t.Context().Done():
			t.Fatal(t.Context().Err())
		}
	}
}

func TestDesignScenarioMCPRunWholeSequenceAndVariableVariants(t *testing.T) {
	t.Parallel()
	srv, scenario, executor := scenarioRunsMCPFixture(t)
	before, err := jsonx.Marshal(scenario.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	start := map[string]any{"scenarioId": scenario.Scenario.ID, "revisionId": scenario.Draft.ID, "runId": "variant-ada", "name": "Ada", "variables": map[string]string{"user": "Ada"}}
	var initial designscenario.RunReport
	if errMsg := callDesignScenarioTool(t, srv, "run_design_scenario", start, &initial); errMsg != "" {
		t.Fatal(errMsg)
	}
	if initial.ID != "variant-ada" || initial.Status != "running" || initial.Source != "mcp" || initial.RevisionID != scenario.Draft.ID {
		t.Fatalf("initial report: %+v", initial)
	}
	awaitScenarioRunProfile(t, executor)
	progress := readScenarioRunMCP(t, srv, scenario.Scenario.ID, initial.ID)
	if progress.Status != "running" || len(progress.Steps) != 2 || progress.Steps[0].Status != "passed" || progress.Steps[1].Status != "running" {
		t.Fatalf("persisted progress: %+v", progress)
	}
	if progress.Steps[1].Request == nil || progress.Steps[1].Request.Headers["Authorization"] != "Bearer token-1" || progress.Variables["id"] != "9007199254740993" {
		t.Fatalf("extraction/substitution missing from persisted progress: %+v", progress)
	}
	executor.release()
	first := awaitScenarioRunTerminalMCP(t, srv, scenario.Scenario.ID, initial.ID)
	if first.Status != "passed" || first.FinishedAt == nil || first.Source != "mcp" || first.Version != scenario.Draft.Version || first.Name != "Ada" {
		t.Fatalf("terminal report: %+v", first)
	}
	if first.InputVariables["user"] != "Ada" || first.Variables["user"] != "Ada" || first.Document.Execution.Variables["user"] != "saved-default" {
		t.Fatalf("override leaked into document: %+v", first)
	}
	for _, step := range first.Steps {
		if step.Status != "passed" || step.Request == nil || step.Response == nil || len(step.Assertions) == 0 || step.Response.ScenarioRevisionID != scenario.Draft.ID {
			t.Fatalf("incomplete step result: %+v", step)
		}
		for _, assertion := range step.Assertions {
			if !assertion.Passed || assertion.ActualJSON == nil || assertion.ExpectedJSON == "" {
				t.Fatalf("incomplete assertion: %+v", assertion)
			}
		}
	}
	if first.Steps[0].Request.Body != `{"user":"Ada"}` || first.Steps[1].Response.Path != "/profile/9007199254740993?user=Ada" {
		t.Fatalf("resolved HTTP request was not executed: %+v", first.Steps)
	}
	if first.Steps[0].Assertions[0].ExpectedJSON != "9007199254740993" || *first.Steps[0].Assertions[0].ActualJSON != "9007199254740993" {
		t.Fatalf("MCP lost integer precision: %+v", first.Steps[0].Assertions[0])
	}
	var repeated designscenario.RunReport
	if errMsg := callDesignScenarioTool(t, srv, "run_design_scenario", start, &repeated); errMsg != "" {
		t.Fatal(errMsg)
	}
	if !reflect.DeepEqual(repeated, first) || executor.dispatched.Load() != 2 {
		t.Fatalf("idempotent start replayed execution: calls=%d report=%+v", executor.dispatched.Load(), repeated)
	}
	start["variables"] = map[string]string{"user": "Grace"}
	if errMsg := callDesignScenarioTool(t, srv, "run_design_scenario", start, &repeated); !strings.Contains(errMsg, "409") {
		t.Fatalf("changed payload under same ID error=%q", errMsg)
	}
	start["runId"], start["name"] = "variant-grace", "Grace"
	if errMsg := callDesignScenarioTool(t, srv, "run_design_scenario", start, &initial); errMsg != "" {
		t.Fatal(errMsg)
	}
	second := awaitScenarioRunTerminalMCP(t, srv, scenario.Scenario.ID, "variant-grace")
	if second.Status != "passed" || second.ID == first.ID || second.RevisionID != first.RevisionID || second.Steps[0].Request.Body != `{"user":"Grace"}` || second.Steps[1].Response.Path != "/profile/9007199254740993?user=Grace" || executor.dispatched.Load() != 4 {
		t.Fatalf("second variable variant: %+v calls=%d", second, executor.dispatched.Load())
	}
	var listed struct {
		Runs []designscenario.RunSummary `json:"runs"`
	}
	if errMsg := callDesignScenarioTool(t, srv, "list_design_scenario_runs", map[string]any{"scenarioId": scenario.Scenario.ID}, &listed); errMsg != "" {
		t.Fatal(errMsg)
	}
	if len(listed.Runs) != 2 || listed.Runs[0].ID != second.ID || listed.Runs[1].ID != first.ID {
		t.Fatalf("run history: %+v", listed.Runs)
	}
	var after designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "get_design_scenario", map[string]any{"scenarioId": scenario.Scenario.ID}, &after); errMsg != "" {
		t.Fatal(errMsg)
	}
	current, err := jsonx.Marshal(after.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	if after.Draft.ID != scenario.Draft.ID || after.Scenario.Version != scenario.Scenario.Version || len(after.Revisions) != len(scenario.Revisions) || string(current) != string(before) {
		t.Fatal("running variable variants modified the saved scenario or revision history")
	}
	if got := readScenarioRunMCP(t, srv, scenario.Scenario.ID, first.ID); !reflect.DeepEqual(got, first) {
		t.Fatal("later run changed the first persisted report")
	}
}

func TestDesignScenarioMCPRunScopedReadsAndCancellation(t *testing.T) {
	t.Parallel()
	srv, scenario, executor := scenarioRunsMCPFixture(t)
	var other designscenario.Detail
	document := designscenario.Document{FormatVersion: 1, Title: "Other", Participants: []designscenario.Participant{}, Messages: []designscenario.Message{}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{}}
	if errMsg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": document}, &other); errMsg != "" {
		t.Fatal(errMsg)
	}
	var report designscenario.RunReport
	if errMsg := callDesignScenarioTool(t, srv, "run_design_scenario", map[string]any{"scenarioId": scenario.Scenario.ID, "revisionId": scenario.Draft.ID, "runId": "cancel-me"}, &report); errMsg != "" {
		t.Fatal(errMsg)
	}
	awaitScenarioRunProfile(t, executor)
	for _, tool := range []string{"get_design_scenario_run", "cancel_design_scenario_run"} {
		for _, lookup := range []struct {
			scenarioID int64
			runID      string
		}{{other.Scenario.ID, "cancel-me"}, {scenario.Scenario.ID, "missing"}} {
			var ignored designscenario.RunReport
			if errMsg := callDesignScenarioTool(t, srv, tool, map[string]any{"scenarioId": lookup.scenarioID, "runId": lookup.runID}, &ignored); !strings.Contains(errMsg, "404") {
				t.Fatalf("%s scoped to %d/%s: %q", tool, lookup.scenarioID, lookup.runID, errMsg)
			}
		}
	}
	args := map[string]any{"scenarioId": scenario.Scenario.ID, "runId": "cancel-me"}
	if errMsg := callDesignScenarioTool(t, srv, "cancel_design_scenario_run", args, &report); errMsg != "" {
		t.Fatal(errMsg)
	}
	cancelled := awaitScenarioRunTerminalMCP(t, srv, scenario.Scenario.ID, "cancel-me")
	if cancelled.Status != "cancelled" || cancelled.FinishedAt == nil || cancelled.Steps[0].Status != "passed" || cancelled.Steps[1].Status != "cancelled" || executor.dispatched.Load() != 2 {
		t.Fatalf("cancelled run: %+v", cancelled)
	}
	if errMsg := callDesignScenarioTool(t, srv, "cancel_design_scenario_run", args, &report); errMsg != "" {
		t.Fatal(errMsg)
	}
	if !reflect.DeepEqual(report, cancelled) {
		t.Fatal("repeated cancel changed the terminal report")
	}
	if got := readScenarioRunMCP(t, srv, scenario.Scenario.ID, "cancel-me"); !reflect.DeepEqual(got, cancelled) {
		t.Fatalf("saved cancellation changed: %s", fmt.Sprint(got))
	}
}
