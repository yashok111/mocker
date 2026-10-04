package mcp

import (
	"encoding/json/v2"
	"errors"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendanalysis"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

func addBackendChangeProposalTools(s *sdk.Server, lb *loopback) {
	const base = "/api/backend-projects/{id}/change-proposals"
	for _, spec := range []struct {
		name, route, contract string
		read                  bool
	}{
		{"list_backend_change_proposals", "GET " + base, "", true},
		{"create_backend_change_proposal", "POST " + base, "CreateBackendChangeProposalRequest", false},
		{"get_backend_change_proposal", "GET " + base + "/{pid}", "", true},
		{"preview_backend_change_proposal_commands", "POST " + base + "/{pid}/preview", "PreviewBackendChangeProposalCommandsRequest", true},
		{"apply_backend_change_proposal_commands", "POST " + base + "/{pid}/commands", "ApplyBackendChangeProposalCommandsRequest", false},
		{"preview_backend_change_proposal_rebase", "POST " + base + "/{pid}/rebase-preview", "PreviewBackendChangeProposalRebaseRequest", true},
		{"apply_backend_change_proposal_rebase", "POST " + base + "/{pid}/rebase", "ApplyBackendChangeProposalRebaseRequest", false},
		{"apply_backend_change_proposal_lifecycle", "POST " + base + "/{pid}/lifecycle", "ApplyBackendChangeProposalLifecycleRequest", false},
		{"restore_backend_change_proposal", "POST " + base + "/{pid}/restore", "RestoreBackendChangeProposalRequest", false},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if spec.name != "create_backend_change_proposal" {
				ids = append(ids, "proposalId")
			}
			backendToolPathSchema(schema, ids)
		} else {
			id := map[string]any{"type": "string", "format": "uuid"}
			fields := map[string]any{"projectId": id, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}, "cursor": map[string]any{"type": "string", "maxLength": 1024}}
			required := []string{"projectId"}
			if spec.name == "get_backend_change_proposal" {
				fields["proposalId"], fields["proposalRevisionId"] = id, id
				required = append(required, "proposalId")
			} else {
				fields["baseRevisionId"] = id
				fields["status"] = map[string]any{"type": "string", "enum": []string{"draft", "ready"}}
			}
			schema = designScenarioSchemaObject(required, fields)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: "Uses an isolated proposal-graph-v1 full graph proposal with an immutable source5/6 baseline and exact draft. Its sixteen typed command families are separate from legacy database proposalCommands. Preview is a pure read; mutations require version CAS and immutable idempotency receipts. Restore creates a new draft and preserves consumed command/object IDs. No source, DDL or broker execution.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.read, IdempotentHint: true}}, spec.route)
	}
	id := map[string]any{"type": "string", "format": "uuid"}
	schema := designScenarioSchemaObject([]string{"projectId", "revisionId"}, map[string]any{"projectId": id, "revisionId": id, "id": id, "repositoryId": id, "providerNamespace": map[string]any{"type": "string", "minLength": 1, "maxLength": 200}, "recordType": map[string]any{"type": "string", "enum": []string{"node", "edge"}}, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}, "cursor": map[string]any{"type": "string", "maxLength": 1024}})
	backendReadToolTarget(schema)
	addBackendImportTool(s, lb, &sdk.Tool{Name: "get_backend_assertions", Description: "Reads provider assertions and losing claims for an exact source6 revision, full proposal with source6 baseline, or current READY import candidate. Legacy proposal assertions are unsupported.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "GET /api/backend-projects/{id}/revisions/{rid}/assertions")
}

// Select targets by their explicit tag, never by probing object identifiers.
func backendPinnedToolRoute(name, route string, in map[string]jsonx.RawMessage) (string, *backendmodel.ProposalReadTarget, error) {
	suffix := map[string]string{"get_backend_node": "nodes/{nid}", "get_backend_evidence": "evidence", "get_backend_coverage": "coverage", "get_backend_assertions": "assertions"}[name]
	if suffix == "" {
		return route, nil, nil
	}
	rawTarget := map[string]jsonx.RawMessage{}
	for _, key := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
		if raw, ok := in[key]; ok {
			rawTarget[key] = raw
		}
	}
	raw, err := json.Marshal(rawTarget)
	if err != nil {
		return "", nil, err
	}
	var target backendmodel.BackendReadTarget
	if err = json.Unmarshal(raw, &target); err != nil {
		return "", nil, err
	}
	const base = "GET /api/backend-projects/{id}"
	switch {
	case target.Proposal != nil:
		if name == "get_backend_assertions" {
			return "", nil, &backendmodel.FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Assertions are not supported for legacy proposals"}
		}
		delete(in, "proposal")
		return base + "/proposals/{pid}/revisions/{prid}/" + suffix, target.Proposal, nil
	case target.ChangeProposal != nil:
		delete(in, "changeProposal")
		return base + "/change-proposals/{pid}/revisions/{prid}/" + suffix, target.ChangeProposal, nil
	case target.ImportCandidate != nil:
		var fields map[string]jsonx.RawMessage
		if err = json.Unmarshal(in["importCandidate"], &fields); err != nil {
			return "", nil, err
		}
		for key, value := range fields {
			in[key] = value
		}
		delete(in, "importCandidate")
		return base + "/imports/{iid}/candidate/" + suffix, nil, nil
	default:
		return route, nil, nil
	}
}

func backendAdmissionFault(err error) *sdk.CallToolResult {
	fault, ok := errors.AsType[*backendmodel.FaultError](err)
	if !ok {
		return designScenarioToolErrorResult(err)
	}
	body := map[string]any{"error": fault}
	return &sdk.CallToolResult{IsError: true, StructuredContent: body, Content: []sdk.Content{&sdk.TextContent{Text: fmt.Sprintf("%s (%d): %s", fault.Code, fault.Status, fault.Message)}}}
}

func validateBackendChangeToolBody(name string, body []byte) error {
	var input any
	switch name {
	case "start_backend_analysis":
		input = new(backendanalysis.StartInput)
	case "cancel_backend_analysis":
		input = new(backendanalysis.CancelInput)
	case "retry_backend_analysis":
		input = new(backendanalysis.RetryInput)
	case "preview_backend_change_proposal_rebase":
		input = new(backendmodel.PreviewChangeProposalRebaseInput)
	case "apply_backend_change_proposal_rebase":
		input = new(backendmodel.ApplyChangeProposalRebaseInput)
	case "apply_backend_change_proposal_lifecycle":
		input = new(backendmodel.ApplyChangeProposalLifecycleInput)
	case "create_backend_change_proposal":
		input = new(backendmodel.CreateChangeProposalInput)
	case "preview_backend_change_proposal_commands":
		input = new(backendmodel.PreviewChangeProposalInput)
	case "apply_backend_change_proposal_commands":
		input = new(backendmodel.ApplyChangeProposalInput)
	case "restore_backend_change_proposal":
		input = new(backendmodel.RestoreChangeProposalInput)
	default:
		return nil
	}
	maxBytes := backendmodel.MaxChangeProposalCommandBytes
	if name == "start_backend_analysis" || name == "cancel_backend_analysis" || name == "retry_backend_analysis" {
		maxBytes = 2 << 20
	}
	if len(body) > maxBytes {
		return fmt.Errorf("backend request exceeds operation body limit")
	}
	return json.Unmarshal(body, input, json.RejectUnknownMembers(true))
}
