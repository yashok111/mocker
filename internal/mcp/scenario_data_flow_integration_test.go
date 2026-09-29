package mcp

import (
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDataBindingsPersistedMCPAtomicHistoryAndReadOnlyAnalysis(t *testing.T) {
	t.Parallel()
	srv, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	document := designscenario.Document{FormatVersion: 1, Title: "Bindings", Participants: []designscenario.Participant{{ID: "api", Name: "API", Kind: "service"}}, Messages: []designscenario.Message{{ID: "create", FromID: "api", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "create"}}, {ID: "get", FromID: "api", ToID: "api", Kind: "request", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "get"}}}, Fragments: []designscenario.Fragment{}, Contracts: []designscenario.Contract{{ID: "api", Name: "API", Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{"/orders":{"post":{"x-mocker-operation-key":"create","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}},"/orders/{id}":{"get":{"x-mocker-operation-key":"get","parameters":[{"name":"id","in":"path","required":true,"schema":{"type":"integer"}}],"responses":{"200":{"description":"ok"}}}}}}`)}}}
	var created designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": document}, &created); errMsg != "" {
		t.Fatal(errMsg)
	}
	id := created.Scenario.ID
	binding := designscenario.DataBinding{ID: "order-id", SourceMessageID: "create", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "path", Name: "id"}}
	binding.Transforms = []designscenario.DataBindingTransform{{Kind: "to_string"}, {Kind: "trim"}, {Kind: "to_integer"}}
	var updated designscenario.Detail
	if errMsg := callDesignScenarioTool(t, srv, "upsert_design_scenario_data_binding", map[string]any{"scenarioId": id, "expectedVersion": 1, "messageId": "get", "binding": binding}, &updated); errMsg != "" {
		t.Fatal(errMsg)
	}
	if updated.Scenario.Version != 2 || updated.Draft.Document.Messages[1].Execution == nil || len(updated.Draft.Document.Messages[1].Execution.Bindings) != 1 {
		t.Fatalf("binding lost: %+v", updated)
	}
	if !reflect.DeepEqual(updated.Draft.Document.Messages[1].Execution.Bindings[0].Transforms, binding.Transforms) {
		t.Fatalf("ordered transforms lost: %+v", updated.Draft.Document.Messages[1].Execution.Bindings)
	}
	// Read a saved historical revision, and analyze an unsaved replacement without saving it.
	for _, revisionID := range []int64{created.Draft.ID, updated.Draft.ID} {
		var analysis designscenario.DataFlowAnalysis
		if errMsg := callDesignScenarioTool(t, srv, "get_design_scenario_data_flow", map[string]any{"scenarioId": id, "revisionId": revisionID}, &analysis); errMsg != "" {
			t.Fatal(errMsg)
		}
		want := 0
		if revisionID == updated.Draft.ID {
			want = 1
		}
		if len(analysis.Bindings) != want {
			t.Fatalf("revision %d bindings=%+v", revisionID, analysis.Bindings)
		}
	}
	var analysis designscenario.DataFlowAnalysis
	if errMsg := callDesignScenarioTool(t, srv, "analyze_design_scenario_data_flow", map[string]any{"scenarioId": id, "document": document}, &analysis); errMsg != "" {
		t.Fatal(errMsg)
	}
	if len(analysis.Bindings) != 0 {
		t.Fatalf("analyzed saved document instead: %+v", analysis)
	}
	var ignored designscenario.Detail
	errMsg := callDesignScenarioTool(t, srv, "remove_design_scenario_data_binding", map[string]any{"scenarioId": id, "expectedVersion": 1, "messageId": "get", "id": "order-id"}, &ignored)
	if !strings.Contains(errMsg, "409") || !strings.Contains(errMsg, `"version":2`) {
		t.Fatalf("stale version: %s", errMsg)
	}
	// The first valid removal must roll back when a subsequent command fails.
	errMsg = callDesignScenarioTool(t, srv, "apply_design_scenario_commands", map[string]any{"scenarioId": id, "expectedVersion": 2, "commands": []designscenario.Command{{Type: "remove_data_binding", MessageID: "get", ID: "order-id"}, {Type: "upsert_data_binding", MessageID: "missing", Binding: &binding}}}, &ignored)
	if errMsg == "" {
		t.Fatal("invalid batch succeeded")
	}
	var readback designscenario.Detail
	if errMsg = callDesignScenarioTool(t, srv, "get_design_scenario", map[string]any{"scenarioId": id}, &readback); errMsg != "" {
		t.Fatal(errMsg)
	}
	if readback.Scenario.Version != 2 || readback.Draft.ID != updated.Draft.ID || len(readback.Revisions) != 2 || len(readback.Draft.Document.Messages[1].Execution.Bindings) != 1 {
		t.Fatalf("analysis or failed writes mutated document: %+v", readback)
	}
	if !reflect.DeepEqual(readback.Draft.Document.Messages[1].Execution.Bindings[0], binding) {
		t.Fatalf("failed batch changed binding: %+v", readback.Draft.Document.Messages[1].Execution.Bindings)
	}
	// Structural transform rejection must also leave the immutable revision intact.
	badBinding := map[string]any{"id": binding.ID, "sourceMessageId": "create", "sourcePointer": "/id", "target": map[string]any{"kind": "path", "name": "id"}, "transforms": []any{map[string]any{"kind": "eval"}}}
	errMsg = callDesignScenarioTool(t, srv, "upsert_design_scenario_data_binding", map[string]any{"scenarioId": id, "expectedVersion": 2, "messageId": "get", "binding": badBinding}, &ignored)
	if errMsg == "" {
		t.Fatal("invalid transform succeeded")
	}
	if errMsg = callDesignScenarioTool(t, srv, "get_design_scenario", map[string]any{"scenarioId": id}, &readback); errMsg != "" {
		t.Fatal(errMsg)
	}
	if readback.Scenario.Version != 2 || readback.Draft.ID != updated.Draft.ID {
		t.Fatal("invalid transform changed revision")
	}
	if errMsg = callDesignScenarioTool(t, srv, "remove_design_scenario_data_binding", map[string]any{"scenarioId": id, "expectedVersion": 2, "messageId": "get", "id": "order-id"}, &readback); errMsg != "" {
		t.Fatal(errMsg)
	}
	if readback.Scenario.Version != 3 || len(readback.Draft.Document.Messages[1].Execution.Bindings) != 0 {
		t.Fatalf("remove failed: %+v", readback)
	}
}
