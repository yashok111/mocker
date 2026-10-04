package mcp

import (
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
)

func addBackendProposalTools(s *sdk.Server, lb *loopback) {
	const base = "/api/backend-projects/{id}/proposals"
	for _, spec := range []struct {
		name, route, contract, description string
		readOnly                           bool
	}{
		{"list_backend_proposals", "GET " + base, "", "Lists draft database proposals, optionally for an exact source baseline. Pages are bound to project and filters.", true},
		{"create_backend_proposal", "POST " + base, "CreateBackendProposalRequest", "Creates an isolated draft bound to one source revision, repository, datastore and facet. Pin database workflow2 and proposal-relational-v1 before writing. Retry uncertain responses with the identical key and request; source is immutable.", false},
		{"get_backend_proposal", "GET " + base + "/{pid}", "", "Reads current proposal metadata and one immutable draft with history, criteria and baseline warning. Graph reads require the returned exact proposal revision ID.", true},
		{"preview_backend_proposal_commands", "POST " + base + "/{pid}/preview", "PreviewBackendProposalCommandsRequest", "Pure preview of typed nullable/FK/criteria edits at exact proposal version and draft. Returns candidate hash or diagnostics, desired changes and unverified checks; never writes or executes DDL.", true},
		{"apply_backend_proposal_commands", "POST " + base + "/{pid}/commands", "ApplyBackendProposalCommandsRequest", "Atomically saves typed edits using proposal CAS and the exact preview hash. Retry the identical request and key after a lost response. Source head is unchanged. On CAS reread, reconcile commands and preview again.", false},
	} {
		var schema map[string]any
		if spec.contract != "" {
			var err error
			schema, err = api.BackendSchema(spec.contract)
			if err != nil {
				panic(err)
			}
			ids := []string{"projectId"}
			if spec.name != "create_backend_proposal" {
				ids = append(ids, "proposalId")
			}
			backendToolPathSchema(schema, ids)
		} else {
			id := map[string]any{"type": "string", "format": "uuid"}
			properties := map[string]any{"projectId": id, "limit": map[string]any{"type": "integer", "minimum": 1, "maximum": 500}, "cursor": map[string]any{"type": "string", "maxLength": 1024}}
			required := []string{"projectId"}
			if spec.name == "get_backend_proposal" {
				required = append(required, "proposalId")
				properties["proposalId"], properties["proposalRevisionId"] = id, id
			} else {
				properties["baseRevisionId"] = id
				properties["status"] = map[string]any{"type": "string", "const": "draft"}
			}
			schema = designScenarioSchemaObject(required, properties)
		}
		addBackendImportTool(s, lb, &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.readOnly, IdempotentHint: true}}, spec.route)
	}
}

func backendReadToolTarget(schema map[string]any) {
	properties := schema["properties"].(map[string]any)
	for key, name := range map[string]string{"proposal": "BackendProposalReadTarget", "changeProposal": "BackendProposalReadTarget", "importCandidate": "BackendImportCandidateReadTarget"} {
		value, err := api.BackendSchema(name)
		if err != nil {
			panic(err)
		}
		properties[key] = value
	}
	var required []any
	switch values := schema["required"].(type) {
	case []any:
		required = values
	case []string:
		for _, value := range values {
			required = append(required, value)
		}
	default:
		panic(fmt.Sprintf("unexpected required schema %T", values))
	}
	var kept []any
	for _, member := range required {
		if member != "revisionId" {
			kept = append(kept, member)
		}
	}
	schema["required"] = kept
	alternatives := make([]any, 0, 4)
	for _, key := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
		var excluded []any
		for _, other := range []string{"revisionId", "proposal", "changeProposal", "importCandidate"} {
			if other != key {
				excluded = append(excluded, map[string]any{"required": []string{other}})
			}
		}
		alternatives = append(alternatives, map[string]any{"required": []string{key}, "not": map[string]any{"anyOf": excluded}})
	}
	schema["oneOf"] = alternatives
}
