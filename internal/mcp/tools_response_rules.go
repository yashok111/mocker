package mcp

import (
	"errors"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/responserules"
)

type responseRuleInput struct {
	DesignID        int64                   `json:"designId"`
	RuleID          string                  `json:"ruleId,omitempty"`
	ExpectedVersion int64                   `json:"expectedVersion,omitempty"`
	Rule            *responserules.Rule     `json:"rule,omitempty"`
	Commands        []responserules.Command `json:"commands,omitempty"`
	Document        *string                 `json:"document,omitempty"`
	Request         *responserules.Request  `json:"request,omitempty"`
}

func addResponseRuleTools(s *sdk.Server, lb *loopback) {
	const base = "/api/designs/{id}/response-rules"
	for _, action := range []struct {
		name, method, suffix, description string
		readOnly                          bool
	}{
		{"list_response_rules", "GET", "", "Lists response-rule graphs in the API draft with design/version/revision identity. Authoring metadata only; no live mock behavior changes.", true},
		{"get_response_rule", "GET", "/{rid}", "Reads one complete response-rule graph and API version. Read before editing; node and edge IDs are stable in separate namespaces.", true},
		{"create_response_rule", "POST", "", "Creates a graph in the API draft, preserving unrelated fields. Incomplete graphs can be saved. Supply current expectedVersion; after a lost response, inspect list_response_rules before retrying.", false},
		{"save_response_rule", "PUT", "/{rid}", "Replaces one COMPLETE graph; rule.id must match ruleId. Requires current whole-API expectedVersion. On 409 reread and reconcile. Does not enable live execution.", false},
		{"delete_response_rule", "DELETE", "/{rid}", "Removes one graph from the API draft, retaining revision history. Requires expectedVersion; conflicts must be reconciled.", false},
		{"apply_response_rule_commands", "POST", "/{rid}/commands", "Applies an ordered atomic batch to one graph. Updates replace complete node/edge fields; remove_node also removes incident edges. set_rule without binding clears it. Requires current expectedVersion; no revision on failed batch.", false},
		{"validate_response_rule", "POST", "/{rid}/validate", "Validates a saved graph or one in exact proposed document text. Checks the selected graph and its binding conflicts; unrelated incomplete graphs do not block it. Returns diagnostics and source identity without saving or touching a mock.", true},
		{"simulate_response_rule", "POST", "/{rid}/simulate", "Simulates a saved or proposed graph against explicit ordered query/header rows and optional bodyJSON. Returns visited nodes/edges, predicate results, accumulated delay and static response or fallback. Numbers compare by original JSON spelling; headers use the first case-insensitive value. No sleeping, HTTP, entity/session/traffic mutation. Fallback does not calculate a generated HTTP response.", true},
	} {
		destructive := action.method == "DELETE"
		// The raw adapter validates a typed schema before decoding original bytes.
		// SDK reflection would round int64 version fences through float64.
		addRawDesignScenarioTool(s, lb, &sdk.Tool{
			Name: action.name, Description: action.description,
			InputSchema: responseRuleInputSchema(action.name),
			Annotations: &sdk.ToolAnnotations{ReadOnlyHint: action.readOnly, IdempotentHint: action.readOnly, DestructiveHint: &destructive},
		}, action.method+" "+base+action.suffix, func(in responseRuleInput) (designScenarioCall, error) {
			call, err := apiDesignRead(in.DesignID)
			if !action.readOnly {
				call, err = apiDesignWrite(in.DesignID, in.ExpectedVersion)
			}
			out := designScenarioCall{params: call.params}
			if err != nil {
				return out, err
			}
			if action.suffix != "" {
				if !responserules.ValidID(in.RuleID) {
					return out, errors.New("ruleId must contain 1-80 letters, digits, hyphens or underscores")
				}
				out.params = append(out.params, in.RuleID)
			}
			if action.method == "GET" {
				return out, nil
			}
			body := map[string]any{}
			if !action.readOnly {
				body["expectedVersion"] = in.ExpectedVersion
			}
			if in.Rule != nil {
				body["rule"] = in.Rule
			}
			if in.Commands != nil {
				body["commands"] = in.Commands
			}
			if in.Document != nil {
				body["document"] = *in.Document
			}
			if in.Request != nil {
				body["request"] = in.Request
			}
			out.body = body
			return out, nil
		})
	}
}
