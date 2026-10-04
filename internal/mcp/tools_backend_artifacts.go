package mcp

import (
	"context"
	"encoding/json/v2"
	"fmt"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

func addBackendArtifactTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract, description string
		readOnly                           bool
	}{
		{"query_backend_artifacts", "POST /api/backend-projects/{id}/artifacts/query", "QueryBackendArtifactsRequest", "Reads an exact pinned editor projection for source1 through source6 or full changeProposal with source5/6 baseline. Useful artifact models are source4 through source6; selected artifact/context must exist. Legacy proposal and importCandidate targets are unsupported. Every page keeps the full frozen binding roster, typed locators and exact desired owner revision/hash. Current draft is advisory.", true},
		{"preview_backend_artifact_pins", "POST /api/backend-projects/{id}/artifacts/preview", "PreviewBackendArtifactPinsRequest", "Previews complete API/editor pin groups without writes. API set requires both collections; scenario set requires editorBindings and forbids apiBindings. Missing prior selectors and truncation block apply.", true},
		{"apply_backend_artifact_pins", "POST /api/backend-projects/{id}/artifacts/commands", "ApplyBackendArtifactPinsRequest", "Atomically applies manual exact artifact pins with CAS, candidate hash and a required idempotency key. Retry the identical body/key for the original receipt bytes.", false},
	} {
		schema, err := api.BackendSchema(spec.contract)
		if err != nil {
			panic(err)
		}
		backendToolPathSchema(schema, []string{"projectId"})
		tool := &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.readOnly, IdempotentHint: true}}
		addBackendArtifactTool(s, lb, tool, spec.route, false)
	}
	id, err := api.BackendSchema("APIArtifactID")
	if err != nil {
		panic(err)
	}
	schema := designScenarioSchemaObject([]string{"scenarioId", "revisionId"}, map[string]any{"scenarioId": id, "revisionId": id})
	addBackendArtifactTool(s, lb, &sdk.Tool{Name: "get_design_scenario_artifact_snapshot", Description: "Reads exact raw scenario document and form drafts with decimal string IDs/version. storedContentHash is always metadata; contentHash is present only for a verified design-scenario-envelope-v1 envelope. documentHash hashes the raw document alone.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "GET /api/design-scenarios/{id}/revisions/{rid}/artifact-snapshot", true)
}

func addBackendArtifactTool(s *sdk.Server, lb *loopback, tool *sdk.Tool, route string, snapshot bool) {
	schema, err := compileBackendImportToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		fail := func(err error) (*sdk.CallToolResult, error) { return designScenarioToolErrorResult(err), nil }
		// Admit the complete original arguments, including outer whitespace,
		// before allocating maps/DTOs or forwarding to the REST handler.
		if len(req.Params.Arguments) > backendmodel.MaxAPIPinBodyBytes {
			return fail(artifactToolBodyLimitError{})
		}
		var in map[string]jsonx.RawMessage
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return fail(err)
		}
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return fail(err)
		}
		var params []any
		if snapshot {
			for _, key := range []string{"scenarioId", "revisionId"} {
				var id string
				if err := json.Unmarshal(in[key], &id); err != nil || !backendmodel.ValidAPIArtifactID(id) {
					return fail(fmt.Errorf("%s must be a canonical positive decimal int64 string", key))
				}
				params = append(params, id)
			}
		} else {
			var id string
			if err := json.Unmarshal(in["projectId"], &id); err != nil || !backendmodel.ValidID(id) {
				return fail(fmt.Errorf("projectId must be a canonical UUID"))
			}
			params = append(params, id)
			delete(in, "projectId")
		}
		method, path := toolPath(tool.Name, route, params...)
		var body []byte
		var err error
		if !snapshot {
			body, err = json.Marshal(in)
			if err != nil {
				return fail(err)
			}
			if len(body) > backendmodel.MaxAPIPinBodyBytes {
				return fail(artifactToolBodyLimitError{})
			}
			err = validateArtifactToolBody(tool.Name, body)
			if err != nil {
				return fail(err)
			}
		}
		status, response, err := lb.do(ctx, method, path, body)
		if err != nil {
			return fail(err)
		}
		if status < 200 || status >= 300 {
			return fail(fmt.Errorf("HTTP %d: %s", status, response))
		}
		return &sdk.CallToolResult{StructuredContent: jsonx.RawMessage(response), Content: []sdk.Content{&sdk.TextContent{Text: string(response)}}}, nil
	})
}

// Error text is part of the existing tool response contract.
type artifactToolBodyLimitError struct{}

func (artifactToolBodyLimitError) Error() string {
	return "Artifact request exceeds 128 KiB body limit"
}

func validateArtifactToolBody(name string, body []byte) error {
	var err error
	switch name {
	case "query_backend_artifacts":
		var dto backendmodel.ArtifactQueryInput
		err = json.Unmarshal(body, &dto)
	case "preview_backend_artifact_pins":
		var dto backendmodel.PreviewArtifactPinsInput
		err = json.Unmarshal(body, &dto)
	case "apply_backend_artifact_pins":
		var dto backendmodel.ApplyArtifactPinsInput
		err = json.Unmarshal(body, &dto)
	}
	return err
}
