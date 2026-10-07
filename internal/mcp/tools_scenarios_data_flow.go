package mcp

import (
	"errors"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/designscenario"
)

type upsertDesignScenarioDataBindingInput struct {
	ScenarioID      int64                      `json:"scenarioId"`
	ExpectedVersion int64                      `json:"expectedVersion"`
	MessageID       string                     `json:"messageId"`
	Binding         designscenario.DataBinding `json:"binding"`
	Summary         string                     `json:"summary,omitempty"`
}

type removeDesignScenarioDataBindingInput struct {
	ScenarioID      int64  `json:"scenarioId"`
	ExpectedVersion int64  `json:"expectedVersion"`
	MessageID       string `json:"messageId"`
	ID              string `json:"id"`
	Summary         string `json:"summary,omitempty"`
}

func addDesignScenarioDataFlowTools(s *sdk.Server, lb *loopback) {
	addDesignScenarioTool(s, lb, "get_design_scenario_data_flow", "GET /api/design-scenarios/{id}/data-flow",
		"Reads typed response/request fields, bindings and diagnostics from an immutable saved scenario revision; defaults to the current draft. Does not execute requests or write data.", true,
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
	addDesignScenarioTool(s, lb, "analyze_design_scenario_data_flow", "POST /api/design-scenarios/{id}/data-flow",
		"Analyzes a proposed complete document without saving or executing it. Returns bounded field catalogs and binding diagnostics; unknown types are warnings, invalid sources or incompatible known types are errors.", true,
		func(in validateDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			call.body = struct {
				Document designscenario.Document `json:"document"`
			}{in.Document}
			return call, err
		})
	addDesignScenarioTool(s, lb, "upsert_design_scenario_data_binding", "POST /api/design-scenarios/{id}/commands",
		"Creates or replaces a response-to-request binding by binding.id on one HTTP message through the atomic command path. Optional transforms apply up to eight ordered trim/lower/upper/to_string/to_number/to_integer operations before target validation and prefix; numbers retain exact precision. Read get_design_scenario and supply its expectedVersion; reconcile conflicts. Semantic errors remain editable but block runs.", false,
		func(in upsertDesignScenarioDataBindingInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			call.body = designScenarioExecutionCommandBody(in.ExpectedVersion, designscenario.Command{Type: "upsert_data_binding", MessageID: in.MessageID, Binding: &in.Binding}, in.Summary)
			return call, err
		})
	addDesignScenarioTool(s, lb, "remove_design_scenario_data_binding", "POST /api/design-scenarios/{id}/commands",
		"Removes one binding by id on a message through the atomic version-fenced command path. Read get_design_scenario first; reconcile a conflict instead of blindly retrying.", false,
		func(in removeDesignScenarioDataBindingInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			call.body = designScenarioExecutionCommandBody(in.ExpectedVersion, designscenario.Command{Type: "remove_data_binding", MessageID: in.MessageID, ID: in.ID}, in.Summary)
			return call, err
		})
}

func designScenarioDataBindingSchema() map[string]any {
	pointer := map[string]any{"type": "string", "maxLength": 2000, "pattern": `^(?:/(?:[^~/]|~[01])*)*$`}
	textTarget := designScenarioSchemaObject([]string{"kind", "name"}, map[string]any{
		"kind": map[string]any{"type": "string", "enum": []string{"path", "query", "header"}},
		"name": map[string]any{"type": "string", "minLength": 1, "maxLength": 256},
	})
	bodyTarget := designScenarioSchemaObject([]string{"kind", "pointer"}, map[string]any{"kind": map[string]any{"type": "string", "const": "body"}, "pointer": pointer})
	return designScenarioSchemaObject([]string{"id", "sourceMessageId", "sourcePointer", "target"}, map[string]any{
		"id": designScenarioRunIDSchema(), "sourceMessageId": map[string]any{"type": "string", "minLength": 1},
		"sourcePointer": pointer, "target": map[string]any{"oneOf": []any{textTarget, bodyTarget}}, "prefix": map[string]any{"type": "string", "maxLength": 50_000},
		"transforms": map[string]any{"type": "array", "maxItems": 8, "items": designScenarioSchemaObject([]string{"kind"}, map[string]any{
			"kind": map[string]any{"type": "string", "enum": []string{"trim", "lower", "upper", "to_string", "to_number", "to_integer"}},
		})},
	})
}

func designScenarioDataBindingInputSchema(upsert bool) map[string]any {
	required := []string{"scenarioId", "expectedVersion", "messageId"}
	properties := map[string]any{"scenarioId": designScenarioPositiveIntegerSchema(), "expectedVersion": designScenarioPositiveIntegerSchema(), "messageId": map[string]any{"type": "string", "minLength": 1}, "summary": map[string]any{"type": "string"}}
	if upsert {
		required = append(required, "binding")
		properties["binding"] = designScenarioDataBindingSchema()
	} else {
		required = append(required, "id")
		properties["id"] = designScenarioRunIDSchema()
	}
	return designScenarioSchemaObject(required, properties)
}
