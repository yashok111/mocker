package mcp

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"strconv"
	"strings"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/api"
	"github.com/yashok111/mocker/internal/backendmodel"
	"github.com/yashok111/mocker/internal/jsonx"
)

type backendToolInput struct {
	AnnotationID    string                 `json:"annotationId,omitempty"`
	TargetID        string                 `json:"targetId,omitempty"`
	RecordType      string                 `json:"recordType,omitempty"`
	Orphaned        *bool                  `json:"orphaned,omitempty"`
	ProjectID       string                 `json:"projectId,omitempty"`
	RevisionID      string                 `json:"revisionId,omitempty"`
	Name            string                 `json:"name,omitempty"`
	IdempotencyKey  string                 `json:"idempotencyKey,omitempty"`
	ExpectedVersion int64                  `json:"expectedVersion,omitzero"`
	Commands        []backendmodel.Command `json:"commands,omitempty"`
	Limit           int                    `json:"limit,omitzero"`
	Cursor          string                 `json:"cursor,omitempty"`
}

func addBackendTools(s *sdk.Server, lb *loopback) {
	addBackendImportTools(s, lb)
	addBackendAnnotationTools(s, lb)
	addBackendFlowTools(s, lb)
	addBackendLineageTools(s, lb)
	addBackendEventsTools(s, lb)
	addBackendProposalTools(s, lb)
	addBackendChangeProposalTools(s, lb)
	addBackendAnalysisTools(s, lb)
	addBackendSavedViewTools(s, lb)
	addBackendDiagramTools(s, lb)
	addBackendFindingTools(s, lb)
	addBackendAPIArtifactTools(s, lb)
	addBackendArtifactTools(s, lb)
	const base = "/api/backend-projects"
	id := map[string]any{"type": "string", "format": "uuid"}
	name := map[string]any{"type": "string", "minLength": 1, "maxLength": backendmodel.MaxNameLength}
	key := map[string]any{"type": "string", "minLength": 1, "maxLength": backendmodel.MaxKeyLength, "pattern": "^[!-~]+$"}
	limit := map[string]any{"type": "integer", "minimum": 1, "maximum": backendmodel.MaxPageSize}
	cursor := map[string]any{"type": "string", "maxLength": 512}
	command, err := api.BackendSchema("BackendProjectCommand")
	if err != nil {
		panic(err)
	}
	for _, spec := range []struct {
		name, route, description string
		required                 []string
		properties               map[string]any
	}{
		{"get_backend_capabilities", "GET " + base + "/capabilities", "Discovers implemented backend features, model/profile/view versions, pinned guides and actual limits. Select a workflow using readTargetSupport, sourceScopes and syncPolicies. changeProposalCommands lists full graph edits; proposalCommands remains the separate legacy database vocabulary. Capabilities do not imply runtime execution.", nil, map[string]any{}},
		{"list_backend_projects", "GET " + base, "Lists backend projects with opaque pagination. Use these UUID project IDs for the workbench; these are separate from mock workspaces and API designs.", nil, map[string]any{"limit": limit, "cursor": cursor}},
		{"create_backend_project", "POST " + base, "Creates a backend project and empty immutable revision with partial, unknown source coverage. Choose a compatible pinned workflow first. Supply a new idempotencyKey; retry a lost response with the exact same name and key. Returns the original receipt on replay. Open /backend-projects/{id} in the UI.", []string{"name", "idempotencyKey"}, map[string]any{"name": name, "idempotencyKey": key}},
		{"get_backend_project", "GET " + base + "/{id}", "Reads project metadata and the current revision ID without loading a graph. Read before metadata edits.", []string{"projectId"}, map[string]any{"projectId": id}},
		{"apply_backend_project_commands", "POST " + base + "/{id}/commands", "Applies at most 100 metadata commands total, including annotation create/update/remove and at most one rename_project, in an atomic batch. Observe advertised byte limits. Metadata changes preserve the model revision. Requires expectedVersion and idempotencyKey; retry an uncertain response with the identical request/key. On 409 reread and reconcile before new commands.", []string{"projectId", "expectedVersion", "idempotencyKey", "commands"}, map[string]any{"projectId": id, "expectedVersion": designScenarioPositiveIntegerSchema(), "idempotencyKey": key, "commands": map[string]any{"type": "array", "minItems": 1, "maxItems": backendmodel.MaxProjectCommands, "items": command}}},
		{"list_backend_revisions", "GET " + base + "/{id}/revisions", "Lists immutable model revision metadata and coverage for this project. A cursor belongs to this project and resource.", []string{"projectId"}, map[string]any{"projectId": id, "limit": limit, "cursor": cursor}},
		{"get_backend_revision", "GET " + base + "/{id}/revisions/{rid}", "Reads immutable revision metadata, semantic hash and coverage. revisionId must belong to projectId. Empty pre-import coverage is unknown, not a completed inventory.", []string{"projectId", "revisionId"}, map[string]any{"projectId": id, "revisionId": id}},
	} {
		if spec.required == nil {
			spec.required = []string{}
		}
		tool := &sdk.Tool{Name: spec.name, Description: spec.description, InputSchema: designScenarioSchemaObject(spec.required, spec.properties), Annotations: &sdk.ToolAnnotations{ReadOnlyHint: strings.HasPrefix(spec.route, "GET "), IdempotentHint: true}}
		addBackendTool(s, lb, tool, spec.route)
	}
}

// Reuse schema validation from the raw adapter, without the generic SDK's float64 conversion.
func addBackendTool(s *sdk.Server, lb *loopback, tool *sdk.Tool, route string) {
	schema, err := compileDesignScenarioToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var in backendToolInput
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &in, schema); err != nil {
			return designScenarioToolErrorResult(fmt.Errorf("%s: invalid arguments: %w", tool.Name, err)), nil
		}
		if len(req.Params.Arguments) > 0 {
			if err := json.Unmarshal(req.Params.Arguments, &in, json.RejectUnknownMembers(true)); err != nil {
				return designScenarioToolErrorResult(fmt.Errorf("%s: invalid arguments: %w", tool.Name, err)), nil
			}
		}
		call, err := backendToolCall(tool.Name, in)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		method, path := toolPath(tool.Name, route, call.params...)
		if call.query != "" {
			path += "?" + call.query
		}
		var body []byte
		if call.body != nil {
			body, err = json.Marshal(call.body)
			if err != nil {
				return designScenarioToolErrorResult(err), nil
			}
		}
		status, response, err := lb.do(ctx, method, path, body)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		if status < 200 || status >= 300 {
			if status >= 400 && status < 500 {
				// Keep exact conflict versions and retry metadata in the new envelope.
				return designScenarioToolErrorResult(fmt.Errorf("HTTP %d: %s", status, response)), nil
			}
			return designScenarioToolErrorResult(toolErr(status, response)), nil
		}
		return &sdk.CallToolResult{StructuredContent: jsonx.RawMessage(response), Content: []sdk.Content{&sdk.TextContent{Text: string(response)}}}, nil
	})
}

func backendToolCall(name string, in backendToolInput) (designScenarioCall, error) {
	if name == "list_backend_annotations" {
		return backendAnnotationToolCall(in)
	}
	call := designScenarioCall{}
	for _, id := range []string{in.ProjectID, in.RevisionID} {
		if id == "" {
			continue
		}
		if !backendmodel.ValidID(id) {
			return call, errors.New("projectId and revisionId must be canonical UUID strings")
		}
		call.params = append(call.params, id)
	}
	switch name {
	case "create_backend_project":
		call.body = backendmodel.CreateInput{Name: in.Name, IdempotencyKey: in.IdempotencyKey}
	case "apply_backend_project_commands":
		call.body = backendmodel.CommandsInput{ExpectedVersion: in.ExpectedVersion, IdempotencyKey: in.IdempotencyKey, Commands: in.Commands}
	case "list_backend_projects", "list_backend_revisions":
		q := url.Values{}
		if in.Limit > 0 {
			q.Set("limit", strconv.Itoa(in.Limit))
		}
		if in.Cursor != "" {
			q.Set("cursor", in.Cursor)
		}
		call.query = q.Encode()
	}
	return call, nil
}
