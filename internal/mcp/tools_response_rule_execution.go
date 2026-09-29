package mcp

import (
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/responserules"
)

func addResponseRuleExecutionTools(s *sdk.Server, lb *loopback) {
	for _, action := range []struct {
		name, route, description string
		readOnly                 bool
	}{
		{"get_response_rule_execution", "GET /api/designs/{id}/response-rule-execution", "Lists executable rule copies in the saved API draft and whether each matches its authoring graph (current), differs (outdated), or has no authoring source (missing). Copies remain active independently of source edits. Publication alone transfers the reviewed execution snapshot to the published mock.", true},
		{"apply_response_rule", "PUT /api/designs/{id}/response-rules/{rid}/execution", "Copies a saved valid authoring rule into executable API configuration using whole-API expectedVersion. Immediately changes the draft HTTP mock; published mock changes only through normal review/publication. Reapply to copy later source edits. Active workspace/scenario overrides and session forced status take precedence. After a lost response, reread execution status and API version before retrying.", false},
		{"unapply_response_rule", "DELETE /api/designs/{id}/response-rules/{rid}/execution", "Removes one executable rule copy from the draft, restoring ordinary handling. Keeps the authoring graph and published revision. Also removes copies whose authoring graph was deleted. Requires expectedVersion even when already absent; reconcile 409 conflicts before retrying.", false},
	} {
		properties := map[string]any{"designId": designScenarioPositiveIntegerSchema()}
		required := []string{"designId"}
		if !action.readOnly {
			properties["ruleId"] = responseRuleIDSchema()
			properties["expectedVersion"] = schemaModelVersionSchema()
			required = append(required, "ruleId", "expectedVersion")
		}
		destructive := !action.readOnly
		addRawDesignScenarioTool(s, lb, &sdk.Tool{
			Name: action.name, Description: action.description,
			InputSchema: designScenarioSchemaObject(required, properties),
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: action.readOnly, IdempotentHint: action.readOnly, DestructiveHint: &destructive},
		}, action.route, func(in responseRuleInput) (designScenarioCall, error) {
			call, err := apiDesignRead(in.DesignID)
			if !action.readOnly {
				call, err = apiDesignWrite(in.DesignID, in.ExpectedVersion)
			}
			out := designScenarioCall{params: call.params}
			if err != nil {
				return out, err
			}
			if !action.readOnly {
				if !responserules.ValidID(in.RuleID) {
					return out, errors.New("ruleId must contain 1-80 letters, digits, hyphens or underscores")
				}
				out.params = append(out.params, in.RuleID)
				out.body = map[string]any{"expectedVersion": in.ExpectedVersion}
			}
			return out, nil
		})
	}
}
