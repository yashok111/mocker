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
	"github.com/yashok111/mocker/internal/scenarioexport"
)

func TestAsyncAPIPersistedMCPAndRESTHandlers(t *testing.T) {
	cfg := resourcesTestConfig(t)
	srv, _ := newResourcesTestServer(t, cfg)
	raw, err := os.ReadFile("../designscenario/testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var created designscenario.Detail
	if message := callDesignScenarioTool(t, srv, "create_design_scenario", map[string]any{"document": jsonx.RawMessage(raw)}, &created); message != "" {
		t.Fatal(message)
	}
	src := httptest.NewRequest(http.MethodPost, "http://mocker.local/mcp", nil)
	export := func(revision designscenario.Revision, format, contractID string) scenarioexport.Artifact {
		t.Helper()
		args := map[string]any{"scenarioId": revision.ScenarioID, "revisionId": revision.ID, "format": format, "contractId": contractID}
		var viaMCP scenarioexport.Artifact
		if message := callDesignScenarioTool(t, srv, "export_design_scenario", args, &viaMCP); message != "" {
			t.Fatal(message)
		}
		path := fmt.Sprintf("/api/design-scenarios/%d/revisions/%d/exports/%s?contractId=%s", revision.ScenarioID, revision.ID, format, contractID)
		status, body, err := srv.CallAsMCP(t.Context(), src, http.MethodGet, path, nil)
		if err != nil || status != http.StatusOK {
			t.Fatalf("REST handler: status=%d error=%v body=%s", status, err, body)
		}
		var viaREST scenarioexport.Artifact
		if err := jsonx.Unmarshal(body, &viaREST); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(viaMCP, viaREST) || viaMCP.SourceHash != revision.Hash || viaMCP.RevisionID != revision.ID {
			t.Fatal("MCP and REST artifact differ or use the wrong revision")
		}
		return viaMCP
	}
	producer := export(created.Draft, "asyncapi-json", "ordersContract")
	consumer := export(created.Draft, "asyncapi-yaml", "notificationsContract")
	if !strings.Contains(producer.Content, `"send"`) || strings.Contains(producer.Content, "consumeOrder") || !strings.Contains(consumer.Content, "receive") {
		t.Fatal("application directions or contracts mixed")
	}
	var archive scenarioexport.ArchiveArtifact
	args := map[string]any{
		"scenarioId": created.Scenario.ID, "revisionId": created.Draft.ID,
		"items": []map[string]string{
			{"format": "asyncapi-json", "contractId": "ordersContract"},
			{"format": "asyncapi-yaml", "contractId": "notificationsContract"},
		},
	}
	if message := callDesignScenarioTool(t, srv, "export_design_scenario_archive", args, &archive); message != "" {
		t.Fatal(message)
	}
	if len(archive.Manifest.Files) != 2 || archive.Manifest.Scenario.SourceHash != created.Draft.Hash {
		t.Fatal("ZIP did not retain the pinned event contracts")
	}
	created.Draft.Document.EventModel.Schemas[0].SchemaJSON = `{"type":"object","properties":{"orderId":{"type":"string"},"counter":{"type":"integer","default":9007199254740993}}}`
	// Renaming the operation and updating its arrow must succeed in one batch:
	// either change alone would leave a dangling reference.
	created.Draft.Document.EventModel.Contracts[0].Operations[0].ID = "publishOrderV2"
	arrow := created.Draft.Document.Messages[0]
	arrow.EventBindings[0].OperationID = "publishOrderV2"
	var updated designscenario.Detail
	args = map[string]any{
		"scenarioId": created.Scenario.ID, "expectedVersion": created.Scenario.Version,
		"commands": []map[string]any{
			{"type": "set_event_model", "eventModel": created.Draft.Document.EventModel},
			{"type": "upsert_message", "message": arrow},
		},
	}
	if message := callDesignScenarioTool(t, srv, "apply_design_scenario_commands", args, &updated); message != "" {
		t.Fatal(message)
	}
	if updated.Scenario.Version != created.Scenario.Version+1 {
		t.Fatal("event edit did not create one revision")
	}
	if updated.Draft.Document.Messages[0].EventBindings[0].OperationID != "publishOrderV2" {
		t.Fatal("atomic batch did not retain the updated event binding")
	}
	if got := export(updated.Draft, "asyncapi-json", "ordersContract"); !strings.Contains(got.Content, "9007199254740993") {
		t.Fatal("saved schema lost its exact integer")
	}
	if got := export(created.Draft, "asyncapi-json", "ordersContract"); !reflect.DeepEqual(producer, got) {
		t.Fatal("event edit changed an older revision's artifact")
	}
}
