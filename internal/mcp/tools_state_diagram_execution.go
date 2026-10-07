package mcp

import (
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/statediagram"
)

type stateDiagramExecutionInput struct {
	DesignID        int64  `json:"designId"`
	DiagramID       string `json:"diagramId,omitempty"`
	ExpectedVersion int64  `json:"expectedVersion,omitempty"`
}

func addStateDiagramExecutionTools(s *sdk.Server, lb *loopback) {
	for _, action := range []struct {
		name, route, description string
		readOnly                 bool
	}{
		{"get_state_diagram_execution", "GET /api/designs/{id}/state-diagram-execution", "Lists executable diagram copies in the saved API draft and whether each matches its authoring diagram (current), differs (outdated), or has no authoring source (missing). Copies remain active independently of source edits. Publication alone transfers the reviewed execution snapshot to the published mock.", true},
		{"apply_state_diagram", "PUT /api/designs/{id}/state-diagrams/{did}/execution", "Copies a saved valid authoring diagram into executable API configuration using whole-API expectedVersion. Immediately changes the draft HTTP mock; published mock changes only through normal review/publication. Reapply to copy later source edits. Active workspace/scenario overrides and session forced status take precedence. After a lost response, reread execution status and API version before retrying.", false},
		{"unapply_state_diagram", "DELETE /api/designs/{id}/state-diagrams/{did}/execution", "Removes one executable diagram copy from the draft, restoring ordinary handling. Keeps the authoring diagram and published revision. Also removes copies whose authoring diagram was deleted. Requires expectedVersion even when already absent; reconcile 409 conflicts before retrying.", false},
	} {
		properties := map[string]any{"designId": designScenarioPositiveIntegerSchema()}
		required := []string{"designId"}
		if !action.readOnly {
			properties["diagramId"] = responseRuleIDSchema()
			properties["expectedVersion"] = schemaModelVersionSchema()
			required = append(required, "diagramId", "expectedVersion")
		}
		destructive := !action.readOnly
		addRawDesignScenarioTool(s, lb, &sdk.Tool{
			Name: action.name, Description: action.description,
			InputSchema: designScenarioSchemaObject(required, properties),
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: action.readOnly, IdempotentHint: action.readOnly, DestructiveHint: &destructive},
		}, action.route, func(in stateDiagramExecutionInput) (designScenarioCall, error) {
			call, err := apiDesignRead(in.DesignID)
			if !action.readOnly {
				call, err = apiDesignWrite(in.DesignID, in.ExpectedVersion)
			}
			out := designScenarioCall{params: call.params}
			if err != nil {
				return out, err
			}
			if !action.readOnly {
				if !statediagram.ValidID(in.DiagramID) {
					return out, errors.New("diagramId must contain 1-80 letters, digits, hyphens or underscores")
				}
				out.params = append(out.params, in.DiagramID)
				out.body = map[string]any{"expectedVersion": in.ExpectedVersion}
			}
			return out, nil
		})
	}
}
