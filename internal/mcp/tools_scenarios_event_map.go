package mcp

import (
	"errors"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/designscenario"
)

func addDesignScenarioEventMapTools(s *sdk.Server, lb *loopback) {
	addDesignScenarioTool(s, lb, "get_design_scenario_event_map", "GET /api/design-scenarios/{id}/event-map",
		"Reads the saved Kafka topology of a scenario revision (current draft by default): services, topics, message schemas, producer/consumer groups, declared retry/DLQ routes and pinned HTTP/state links. Read-only; does not contact Kafka or simulate delivery. Inspect diagnostics and complete before concluding all links resolve.", true,
		func(in designScenarioCoverageInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			if in.RevisionID != nil {
				if *in.RevisionID <= 0 {
					return call, errors.New("revisionId must be positive")
				}
				call.query = url.Values{"revisionId": {strconv.FormatInt(*in.RevisionID, 10)}}.Encode()
			}
			return call, err
		})
	addDesignScenarioTool(s, lb, "analyze_design_scenario_event_map", "POST /api/design-scenarios/{id}/event-map",
		"Projects a proposed complete scenario document into bounded Kafka topology and diagnostics without saving or executing it. Resolves API and lifecycle links from embedded contract snapshots; the returned proposed view is not a saved revision.", true,
		func(in validateDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			call.body = struct {
				Document designscenario.Document `json:"document"`
			}{in.Document}
			return call, err
		})
	addDesignScenarioTool(s, lb, "apply_design_scenario_event_map_commands", "POST /api/design-scenarios/{id}/commands",
		"Applies 1–100 narrow event topology commands atomically to the existing scenario: upsert/remove servers, channels, messages, schemas, contracts and contract operations; set/clear receive failure routes; upsert/remove HTTP API or lifecycle links. Upserts carry complete entities. Missing cross-model links remain diagnostic; removals of referenced event definitions fail. Read get_design_scenario and use its expectedVersion. Reconcile conflicts; no publication or Kafka delivery.", false,
		func(in applyDesignScenarioCommandsInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			call.body = struct {
				ExpectedVersion int64                    `json:"expectedVersion"`
				Commands        []designscenario.Command `json:"commands"`
				Summary         string                   `json:"summary,omitempty"`
			}{in.ExpectedVersion, in.Commands, in.Summary}
			return call, err
		})
}
