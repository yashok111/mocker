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

func addBackendAPIArtifactTools(s *sdk.Server, lb *loopback) {
	for _, spec := range []struct {
		name, route, contract, description string
		readOnly                           bool
	}{
		{"query_backend_api_artifacts", "POST /api/backend-projects/{id}/api-artifacts/query", "QueryBackendAPIArtifactsRequest", "Reads frozen API associations for exact source1 through source6 or full changeProposal with source5/6 baseline. Useful artifact models are source4 through source6 and require actual frozen pins. Legacy proposal and importCandidate targets are unsupported. Broken/orphaned refs keep IDs/labels; current owner draft is advisory.", true},
		{"preview_backend_api_pins", "POST /api/backend-projects/{id}/api-artifacts/preview", "PreviewBackendAPIPinsRequest", "Previews the full API association vector without writes. Preserve every diff row, including missing prior selectors; truncation blocks apply.", true},
		{"apply_backend_api_pins", "POST /api/backend-projects/{id}/api-artifacts/commands", "ApplyBackendAPIPinsRequest", "Atomically applies manual exact API pins with CAS, candidate hash and a required idempotency key. Retry the identical body/key for the original receipt bytes.", false},
	} {
		schema, err := api.BackendSchema(spec.contract)
		if err != nil {
			panic(err)
		}
		backendToolPathSchema(schema, []string{"projectId"})
		tool := &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: spec.readOnly, IdempotentHint: true}}
		addBackendAPIArtifactTool(s, lb, tool, spec.route, false)
	}
	id, err := api.BackendSchema("APIArtifactID")
	if err != nil {
		panic(err)
	}
	schema := designScenarioSchemaObject([]string{"artifactId", "revisionId"}, map[string]any{"artifactId": id, "revisionId": id})
	addBackendAPIArtifactTool(s, lb, &sdk.Tool{Name: "get_api_artifact_snapshot", Description: "Reads exact raw immutable owner bytes using canonical decimal int64 strings. The content hash belongs to the raw document; IDs never round.", InputSchema: schema, Annotations: &sdk.ToolAnnotations{ReadOnlyHint: true, IdempotentHint: true}}, "GET /api/designs/{id}/revisions/{rid}/artifact-snapshot", true)
}

func addBackendAPIArtifactTool(s *sdk.Server, lb *loopback, tool *sdk.Tool, route string, snapshot bool) {
	schema, err := compileBackendImportToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		fail := func(err error) (*sdk.CallToolResult, error) { return designScenarioToolErrorResult(err), nil }
		// Admit size before allocating maps/DTOs. The sole extra body-external
		// member has a fixed UUID width; the REST body has its own exact limit.
		if len(req.Params.Arguments) > backendmodel.MaxAPIPinBodyBytes+64 {
			return fail(fmt.Errorf("API artifact request exceeds 128 KiB body limit"))
		}
		var in map[string]jsonx.RawMessage
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return fail(err)
		}
		if err := json.Unmarshal(req.Params.Arguments, &in); err != nil {
			return fail(err)
		}
		params, err := backendAPIArtifactParams(in, snapshot)
		if err != nil {
			return fail(err)
		}
		method, path := toolPath(tool.Name, route, params...)
		var body []byte
		if !snapshot {
			if body, err = backendAPIArtifactBody(tool.Name, in); err != nil {
				return fail(err)
			}
		}
		status, response, err := lb.do(ctx, method, path, body)
		if err != nil {
			return fail(err)
		}
		if status < 200 || status >= 300 {
			return fail(backendStatusError(status, response))
		}
		return backendToolResult(response), nil
	})
}

// backendAPIArtifactParams takes the path identifiers out of in: the two
// decimal int64 strings of a snapshot, or the project UUID otherwise (which
// then leaves the body).
func backendAPIArtifactParams(in map[string]jsonx.RawMessage, snapshot bool) ([]any, error) {
	if snapshot {
		params := make([]any, 0, 2)
		for _, key := range []string{"artifactId", "revisionId"} {
			var id string
			if err := json.Unmarshal(in[key], &id); err != nil || !backendmodel.ValidAPIArtifactID(id) {
				return nil, fmt.Errorf("%s must be a canonical positive decimal int64 string", key)
			}
			params = append(params, id)
		}
		return params, nil
	}
	var id string
	if err := json.Unmarshal(in["projectId"], &id); err != nil || !backendmodel.ValidID(id) {
		return nil, fmt.Errorf("projectId must be a canonical UUID")
	}
	delete(in, "projectId")
	return []any{id}, nil
}

// backendAPIArtifactBody re-encodes the remaining arguments and admits them
// against the REST body limit and the route's own DTO before any call is made.
func backendAPIArtifactBody(name string, in map[string]jsonx.RawMessage) ([]byte, error) {
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(body) > backendmodel.MaxAPIPinBodyBytes {
		return nil, fmt.Errorf("API artifact request exceeds 128 KiB body limit")
	}
	switch name {
	case "query_backend_api_artifacts":
		var dto backendmodel.APIArtifactQueryInput
		err = json.Unmarshal(body, &dto)
	case "preview_backend_api_pins":
		var dto backendmodel.PreviewAPIPinsInput
		err = json.Unmarshal(body, &dto)
	case "apply_backend_api_pins":
		var dto backendmodel.ApplyAPIPinsInput
		err = json.Unmarshal(body, &dto)
	}
	if err != nil {
		return nil, err
	}
	return body, nil
}
