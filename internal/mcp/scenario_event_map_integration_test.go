package mcp

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestEventMapPersistedMCPParityHistoryAndAtomicCommands(t *testing.T) {
	t.Parallel()
	srv, _ := newResourcesTestServer(t, resourcesTestConfig(t))
	raw, err := os.ReadFile("../designscenario/testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var created designscenario.Detail
	if msg := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": jsonx.RawMessage(raw)}, &created); msg != "" {
		t.Fatal(msg)
	}
	id := created.Scenario.ID
	commands := []map[string]any{
		{"type": "upsert_event_channel", "eventChannel": map[string]any{"id": "retry", "name": "Retry", "description": "", "address": "orders.retry", "serverIds": []string{"localKafka"}, "messageIds": []string{"orderCreated"}}},
		{"type": "set_event_failure_routes", "contractId": "notificationsContract", "id": "consumeOrder", "failureRoutes": map[string]any{"retryChannelId": "retry"}},
		{"type": "upsert_event_api_link", "contractId": "notificationsContract", "id": "consumeOrder", "apiLink": map[string]any{"contractId": "missing-api", "operationKey": "create-order"}},
	}
	var updated designscenario.Detail
	apply := func(version int64, batch any) string {
		return callDesignScenarioTool(t, srv, "apply_design_scenario_event_map_commands", map[string]any{"scenarioId": id, "expectedVersion": version, "commands": batch}, &updated)
	}
	if msg := apply(1, commands); msg != "" {
		t.Fatal(msg)
	}
	if updated.Scenario.Version != 2 || updated.Draft.Document.EventModel.Contracts[1].Operations[0].FailureRoutes.RetryChannelID != "retry" {
		t.Fatalf("metadata lost: %+v", updated)
	}
	updatedRevision := updated.Draft.ID
	for _, rid := range []int64{created.Draft.ID, updatedRevision} {
		var report designscenario.EventMapReport
		if msg := callDesignScenarioTool(t, srv, "get_design_scenario_event_map", map[string]any{"scenarioId": id, "revisionId": rid}, &report); msg != "" {
			t.Fatal(msg)
		}
		src := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
		status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodGet, fmt.Sprintf("/api/design-scenarios/%d/event-map?revisionId=%d", id, rid), nil)
		if err != nil || status != 200 {
			t.Fatalf("HTTP: %d %s %v", status, body, err)
		}
		var viaHTTP designscenario.EventMapReport
		if err := jsonx.Unmarshal(body, &viaHTTP); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(report, viaHTTP) || report.RevisionID != rid || report.Version != 2 || report.Proposed {
			t.Fatalf("wrong map snapshot: %+v", report)
		}
		hasRetry := false
		for _, edge := range report.Edges {
			hasRetry = hasRetry || edge.Kind == "retry"
		}
		if hasRetry != (rid == updatedRevision) || (rid == updatedRevision && report.Complete) {
			t.Fatalf("route or broken-link diagnostic missing: %+v", report)
		}
	}
	var proposed designscenario.EventMapReport
	if msg := callDesignScenarioTool(t, srv, "analyze_design_scenario_event_map", map[string]any{"scenarioId": id, "document": created.Draft.Document}, &proposed); msg != "" {
		t.Fatal(msg)
	}
	if !proposed.Proposed || proposed.RevisionID != 0 || proposed.Version != 2 {
		t.Fatalf("wrong proposed identity: %+v", proposed)
	}
	if msg := apply(1, commands[:1]); !strings.Contains(msg, "409") {
		t.Fatalf("CAS: %s", msg)
	}
	if msg := apply(2, []map[string]any{{"type": "remove_event_channel", "id": "retry"}}); msg == "" {
		t.Fatal("referenced channel removed")
	}
	if msg := apply(2, []map[string]any{{"type": "set_event_failure_routes", "contractId": "notificationsContract", "id": "consumeOrder"}, {"type": "remove_event_schema", "id": "orderPayload"}}); msg == "" {
		t.Fatal("invalid batch committed")
	}
	var readback designscenario.Detail
	if msg := callDesignScenarioTool(t, srv, "get_design_scenario", map[string]any{"scenarioId": id}, &readback); msg != "" {
		t.Fatal(msg)
	}
	if readback.Scenario.Version != 2 || readback.Draft.ID != updatedRevision || len(readback.Revisions) != 2 || readback.Draft.Document.EventModel.Contracts[1].Operations[0].FailureRoutes.RetryChannelID != "retry" {
		t.Fatalf("failed writes or reads changed saved state: %+v", readback)
	}
	// The generic union exposes the same variants and supports ordered dependency removal.
	if msg := callDesignScenarioTool(t, srv, "apply_design_scenario_commands", map[string]any{"scenarioId": id, "expectedVersion": 2, "commands": []map[string]any{{"type": "set_event_failure_routes", "contractId": "notificationsContract", "id": "consumeOrder"}, {"type": "remove_event_channel", "id": "retry"}}}, &updated); msg != "" {
		t.Fatal(msg)
	}
	if updated.Scenario.Version != 3 || updated.Draft.Document.EventModel.Contracts[1].Operations[0].FailureRoutes != nil {
		t.Fatal("clear route failed")
	}
}
