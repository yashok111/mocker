package mcp

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

// addDesignScenarioTools exposes the persisted sequence canvas through the
// same admin handlers the UI uses. A command batch is the composable mutation
// surface; full-document save remains available for the interactive editor.
func addDesignScenarioTools(s *sdk.Server, lb *loopback) {
	addDesignScenarioTool(s, lb, "run_design_scenario", "POST /api/design-scenarios/{id}/runs",
		"Starts an asynchronous server run of an immutable saved scenario revision. Executes enabled HTTP steps sequentially, substitutes variables, checks expected status and JSON assertions, and extracts variables for later steps. Returns a persisted report; poll get_design_scenario_run until terminal. Supply a unique runId for each intentional run or variable variant. Repeating the SAME runId with identical revisionId, variables and name returns the original run without dispatching again, including after a lost response. Changed input returns 409; a pruned report returns 410 and never replays. Variable overrides affect only this run; the scenario revision stays unchanged. May mutate mock runtime state.", false,
		func(in runDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			call.body = in.runDesignScenarioBody
			return call, err
		})
	addDesignScenarioTool(s, lb, "list_design_scenario_runs", "GET /api/design-scenarios/{id}/runs",
		"Lists up to 50 recent persisted run summaries for a scenario, newest first. Active runs are included. Use get_design_scenario_run for steps, resolved requests, responses, assertions and variables.", true,
		func(in designScenarioIDInput) (designScenarioCall, error) { return designScenarioRead(in.ScenarioID) })
	addDesignScenarioTool(s, lb, "get_design_scenario_run", "GET /api/design-scenarios/{id}/runs/{runId}",
		"Reads a full persisted run report scoped to its scenario. Poll while status is running; terminal statuses are passed, failed and cancelled. A pruned report returns 410. Includes the saved document, input and extracted variables, resolved requests, responses and assertion results.", true,
		func(in designScenarioRunInput) (designScenarioCall, error) { return designScenarioRunCall(in) })
	addDesignScenarioTool(s, lb, "cancel_design_scenario_run", "POST /api/design-scenarios/{id}/runs/{runId}/cancel",
		"Requests cancellation of an active server run. Idempotent; terminal reports are never changed. The returned report may still be running while the current step stops; poll get_design_scenario_run for the final cancelled report.", false,
		func(in designScenarioRunInput) (designScenarioCall, error) {
			call, err := designScenarioRunCall(in)
			call.body = struct{}{}
			return call, err
		})
	addDesignScenarioTool(s, lb, "execute_design_scenario_step", "POST /api/design-scenarios/{id}/execute-step",
		"Executes one HTTP request from an immutable scenario revision against its linked current API draft mock, in-process. Supply resolved parameter/header/body values, never a URL. Refuses detached or stale contracts, unfinished forms, disabled requests and opt/loop fragments. Returns mock HTTP errors in status; may affect runtime state. It does not evaluate assertions or extract variables. Do not retry a lost response blindly.", false,
		func(in executeDesignScenarioStepInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			call.body = in.executeDesignScenarioStepBody
			return call, err
		})
	addDesignScenarioTool(s, lb, "list_design_scenarios", "GET /api/design-scenarios",
		"Lists persisted sequence-design scenarios with their current versions and draft revision ids.", true,
		func(_ struct{}) (designScenarioCall, error) { return designScenarioCall{}, nil })
	addDesignScenarioTool(s, lb, "create_design_scenario", "POST /api/design-scenarios",
		"Creates a persisted sequence-design scenario from a complete canvas document and optional unfinished API form buffers. Returns version 1 and its immutable draft revision. Not idempotent: after a lost response, inspect list_design_scenarios before creating again.", false,
		func(in createDesignScenarioInput) (designScenarioCall, error) {
			return designScenarioCall{body: in}, nil
		})
	addDesignScenarioTool(s, lb, "get_design_scenario", "GET /api/design-scenarios/{id}",
		"Reads the current complete canvas document, unfinished form buffers, diagnostics, available contract updates and immutable revision history. Read this immediately before any write and preserve its expectedVersion.", true,
		func(in designScenarioIDInput) (designScenarioCall, error) {
			return designScenarioRead(in.ScenarioID)
		})
	addDesignScenarioTool(s, lb, "save_design_scenario_draft", "PUT /api/design-scenarios/{id}/draft",
		"Replaces the COMPLETE canvas document and form buffers. Read get_design_scenario first and send its exact expectedVersion. The save, linked API edits and immutable scenario revision are atomic. A 409 identifies the current scenario or linked API version: re-read and reconcile; never blindly retry with a newer number.", false,
		func(in saveDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			call.body = struct {
				ExpectedVersion int64                   `json:"expectedVersion"`
				Document        designscenario.Document `json:"document"`
				FormDrafts      map[string]string       `json:"formDrafts,omitempty"`
				Summary         string                  `json:"summary,omitempty"`
			}{
				ExpectedVersion: in.ExpectedVersion,
				Document:        in.Document,
				FormDrafts:      in.FormDrafts,
				Summary:         in.Summary,
			}
			return call, err
		})
	addDesignScenarioTool(s, lb, "apply_design_scenario_commands", "POST /api/design-scenarios/{id}/commands",
		"Applies an ordered atomic command batch to participants, messages, order, fragments and API contracts. Supported types: set_title, upsert_participant, remove_participant, move_participant, upsert_message, remove_message, move_message, upsert_fragment, remove_fragment, bind_operation, create_operation, create_contract, import_contract, refresh_contract, detach_contract and materialize_contract. create_contract accepts a new independent copy with a unique id and no source; combine it with bind_operation and materialize_contract to create an API project from multiple calls atomically. Upserts carry complete objects. The batch preserves form buffers, checks one expectedVersion and either saves every command plus linked API work or saves nothing.", false,
		func(in applyDesignScenarioCommandsInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			if err == nil && len(in.Commands) == 0 {
				err = errors.New("commands must contain at least one command")
			}
			call.body = struct {
				ExpectedVersion int64                    `json:"expectedVersion"`
				Commands        []designscenario.Command `json:"commands"`
				Summary         string                   `json:"summary,omitempty"`
			}{
				ExpectedVersion: in.ExpectedVersion,
				Commands:        in.Commands,
				Summary:         in.Summary,
			}
			return call, err
		})
	addDesignScenarioTool(s, lb, "get_design_scenario_revision", "GET /api/design-scenarios/{id}/revisions/{rid}",
		"Reads one immutable canvas revision, including its exact document and unfinished form buffers. revisionId must belong to this scenario.", true,
		func(in designScenarioRevisionInput) (designScenarioCall, error) {
			return designScenarioRead(in.ScenarioID, in.RevisionID)
		})
	addDesignScenarioTool(s, lb, "get_design_scenario_diff", "GET /api/design-scenarios/{id}/diff",
		"Compares two immutable canvas revisions and returns JSON-pointer changes with absent values kept distinct from JSON null. fromRevisionId defaults to the previous revision and toRevisionId defaults to the current draft.", true,
		func(in designScenarioDiffInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			if err != nil {
				return call, err
			}
			query := url.Values{}
			for name, id := range map[string]*int64{"from": in.FromRevisionID, "to": in.ToRevisionID} {
				if id == nil {
					continue
				}
				if *id <= 0 {
					return call, fmt.Errorf("%sRevisionId must be positive", name)
				}
				query.Set(name, strconv.FormatInt(*id, 10))
			}
			call.query = query.Encode()
			return call, nil
		})
	addDesignScenarioTool(s, lb, "restore_design_scenario_revision", "POST /api/design-scenarios/{id}/restore",
		"Restores an immutable revision as a NEW draft revision. Existing history remains intact and shared APIs are not rolled back. expectedVersion protects concurrent UI or MCP edits.", false,
		func(in restoreDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioWrite(in.ScenarioID, in.ExpectedVersion)
			if err == nil && in.RevisionID <= 0 {
				err = errors.New("revisionId must be positive")
			}
			call.body = struct {
				ExpectedVersion int64  `json:"expectedVersion"`
				RevisionID      int64  `json:"revisionId"`
				Summary         string `json:"summary,omitempty"`
			}{
				ExpectedVersion: in.ExpectedVersion,
				RevisionID:      in.RevisionID,
				Summary:         in.Summary,
			}
			return call, err
		})
	addDesignScenarioTool(s, lb, "validate_design_scenario", "POST /api/design-scenarios/{id}/validate",
		"Validates a proposed complete canvas document without saving it or changing linked APIs. Broken operation bindings are returned as diagnostics so incomplete design work remains representable.", true,
		func(in validateDesignScenarioInput) (designScenarioCall, error) {
			call, err := designScenarioRead(in.ScenarioID)
			call.body = struct {
				Document designscenario.Document `json:"document"`
			}{Document: in.Document}
			return call, err
		})
}

func designScenarioRunCall(in designScenarioRunInput) (designScenarioCall, error) {
	call, err := designScenarioRead(in.ScenarioID)
	if err != nil {
		return call, err
	}
	call.params = append(call.params, url.PathEscape(in.RunID))
	return call, nil
}

type designScenarioCall struct {
	params []any
	query  string
	body   any
}

func designScenarioRead(ids ...int64) (designScenarioCall, error) {
	call := designScenarioCall{params: make([]any, len(ids))}
	for i, id := range ids {
		if id <= 0 {
			return call, errors.New("scenario and revision IDs must be positive integers")
		}
		call.params[i] = id
	}
	return call, nil
}

func designScenarioWrite(scenarioID, expectedVersion int64) (designScenarioCall, error) {
	if expectedVersion <= 0 {
		return designScenarioCall{}, errors.New("expectedVersion must be the positive version from get_design_scenario")
	}
	return designScenarioRead(scenarioID)
}

func addDesignScenarioTool[Input any](
	s *sdk.Server,
	lb *loopback,
	name string,
	route string,
	description string,
	readOnly bool,
	build func(Input) (designScenarioCall, error),
) {
	tool := &sdk.Tool{
		Name:        name,
		Description: description,
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly},
	}
	if name == "run_design_scenario" || name == "cancel_design_scenario_run" {
		tool.Annotations.IdempotentHint = true
	}
	if schema := designScenarioInputSchema(name); schema != nil {
		tool.InputSchema = schema
	}
	if name == "create_design_scenario" || name == "save_design_scenario_draft" || name == "validate_design_scenario" || name == "apply_design_scenario_commands" {
		addRawDesignScenarioTool(s, lb, tool, route, build)
		return
	}
	sdk.AddTool(s, tool, func(ctx context.Context, _ *sdk.CallToolRequest, in Input) (*sdk.CallToolResult, any, error) {
		call, err := build(in)
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", name, err)
		}
		result, err := executeDesignScenarioCall(ctx, lb, name, route, call)
		return result, nil, err
	})
}

func addRawDesignScenarioTool[Input any](
	s *sdk.Server,
	lb *loopback,
	tool *sdk.Tool,
	route string,
	build func(Input) (designScenarioCall, error),
) {
	schema, err := compileDesignScenarioToolSchema(tool)
	if err != nil {
		panic(fmt.Errorf("AddTool %q: input schema: %w", tool.Name, err))
	}
	s.AddTool(tool, func(ctx context.Context, req *sdk.CallToolRequest) (*sdk.CallToolResult, error) {
		var input Input
		if err := decodeDesignScenarioToolInput(req.Params.Arguments, &input, schema); err != nil {
			return designScenarioToolErrorResult(fmt.Errorf("%s: invalid arguments: %w", tool.Name, err)), nil
		}
		call, err := build(input)
		if err != nil {
			return designScenarioToolErrorResult(fmt.Errorf("%s: %w", tool.Name, err)), nil
		}
		result, err := executeDesignScenarioCall(ctx, lb, tool.Name, route, call)
		if err != nil {
			return designScenarioToolErrorResult(err), nil
		}
		return result, nil
	})
}

func compileDesignScenarioToolSchema(tool *sdk.Tool) (*jsonschema.Schema, error) {
	raw, err := jsonx.Marshal(tool.InputSchema)
	if err != nil {
		return nil, err
	}
	var document any
	if err := jsonx.Unmarshal(raw, &document); err != nil {
		return nil, err
	}
	compiler := jsonschema.NewCompiler()
	uri := "https://mocker.invalid/mcp/" + tool.Name + ".schema.json"
	if err := compiler.AddResource(uri, document); err != nil {
		return nil, err
	}
	return compiler.Compile(uri)
}

func decodeDesignScenarioToolInput(data []byte, out any, schema *jsonschema.Schema) error {
	if len(data) == 0 {
		data = []byte(`{}`)
	}
	// Raw SDK handlers do not validate InputSchema. Validate a separate view,
	// then decode the original bytes so arbitrary contract numbers stay exact.
	decoder := jsonx.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return err
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return errors.New("unexpected trailing data")
	}
	if err := schema.Validate(value); err != nil {
		return err
	}
	decoder = jsonx.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	return decoder.Decode(out)
}

func designScenarioToolErrorResult(err error) *sdk.CallToolResult {
	result := &sdk.CallToolResult{}
	result.SetError(err)
	return result
}

func executeDesignScenarioCall(
	ctx context.Context,
	lb *loopback,
	name string,
	route string,
	call designScenarioCall,
) (*sdk.CallToolResult, error) {
	method, path := toolPath(name, route, call.params...)
	if call.query != "" {
		path += "?" + call.query
	}
	var body []byte
	var err error
	if call.body != nil {
		body, err = jsonx.Marshal(call.body)
		if err != nil {
			return nil, fmt.Errorf("%s: encode input: %w", name, err)
		}
	}
	status, response, err := lb.do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	if status < http.StatusOK || status >= http.StatusMultipleChoices {
		return nil, designScenarioToolError(status, response)
	}
	var out map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal(response, &out); err != nil {
		return nil, fmt.Errorf("%s: decode response: %w", name, err)
	}
	return &sdk.CallToolResult{
		StructuredContent: jsonx.RawMessage(response),
		Content:           []sdk.Content{&sdk.TextContent{Text: string(response)}},
	}, nil
}

// designScenarioInputSchema overrides inference where the Go wire type is
// intentionally more permissive than its JSON meaning. jsonx.RawMessage is a
// byte slice to reflection, but a contract document is a JSON object; without
// this schema the SDK advertises and validates it as an array. Commands also
// need a discriminated union so a model sees which fields each type requires.
func designScenarioInputSchema(name string) any {
	switch name {
	case "run_design_scenario":
		return designScenarioSchemaObject([]string{"scenarioId", "runId", "revisionId"}, map[string]any{
			"scenarioId": designScenarioPositiveIntegerSchema(), "revisionId": designScenarioPositiveIntegerSchema(),
			"runId": designScenarioRunIDSchema(), "variables": designScenarioExecutionMapSchema(), "name": map[string]any{"type": "string", "maxLength": 200},
		})
	case "get_design_scenario_run", "cancel_design_scenario_run":
		return designScenarioSchemaObject([]string{"scenarioId", "runId"}, map[string]any{"scenarioId": designScenarioPositiveIntegerSchema(), "runId": designScenarioRunIDSchema()})
	case "list_design_scenario_runs":
		return designScenarioSchemaObject([]string{"scenarioId"}, map[string]any{"scenarioId": designScenarioPositiveIntegerSchema()})
	case "execute_design_scenario_step":
		return designScenarioSchemaObject([]string{"scenarioId", "revisionId", "messageId", "pathParams", "query", "headers", "body"}, map[string]any{
			"scenarioId": designScenarioPositiveIntegerSchema(), "revisionId": designScenarioPositiveIntegerSchema(),
			"messageId":  map[string]any{"type": "string", "minLength": 1},
			"pathParams": designScenarioExecutionMapSchema(), "query": designScenarioExecutionMapSchema(), "headers": designScenarioExecutionMapSchema(),
			"body": map[string]any{"type": "string"},
		})
	case "create_design_scenario":
		return designScenarioSchemaObject(
			[]string{"document"},
			map[string]any{
				"document":   designScenarioDocumentSchema(),
				"formDrafts": designScenarioFormDraftsSchema(),
				"summary":    map[string]any{"type": "string"},
			},
		)
	case "save_design_scenario_draft":
		return designScenarioSchemaObject(
			[]string{"scenarioId", "expectedVersion", "document"},
			map[string]any{
				"scenarioId":      designScenarioPositiveIntegerSchema(),
				"expectedVersion": designScenarioPositiveIntegerSchema(),
				"document":        designScenarioDocumentSchema(),
				"formDrafts":      designScenarioFormDraftsSchema(),
				"summary":         map[string]any{"type": "string"},
			},
		)
	case "apply_design_scenario_commands":
		return designScenarioSchemaObject(
			[]string{"scenarioId", "expectedVersion", "commands"},
			map[string]any{
				"scenarioId":      designScenarioPositiveIntegerSchema(),
				"expectedVersion": designScenarioPositiveIntegerSchema(),
				"commands": map[string]any{
					"type":        "array",
					"minItems":    1,
					"description": "Ordered atomic batch; all commands commit or none commit.",
					"items":       map[string]any{"oneOf": designScenarioCommandSchemas()},
				},
				"summary": map[string]any{"type": "string"},
			},
		)
	case "validate_design_scenario":
		return designScenarioSchemaObject(
			[]string{"scenarioId", "document"},
			map[string]any{
				"scenarioId": designScenarioPositiveIntegerSchema(),
				"document":   designScenarioDocumentSchema(),
			},
		)
	default:
		return nil
	}
}

func designScenarioRunIDSchema() map[string]any {
	return map[string]any{"type": "string", "pattern": "^[A-Za-z0-9_-]{1,100}$", "minLength": 1, "maxLength": 100}
}

func designScenarioSchemaObject(required []string, properties map[string]any) map[string]any {
	return map[string]any{
		"type":                 "object",
		"required":             required,
		"properties":           properties,
		"additionalProperties": false,
	}
}

func designScenarioPositiveIntegerSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1}
}

func designScenarioFormDraftsSchema() map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": map[string]any{"type": "string"},
	}
}

func designScenarioColorSchema(description string) map[string]any {
	return map[string]any{
		"type":        "string",
		"pattern":     designscenario.HexColorPattern,
		"minLength":   7,
		"maxLength":   7,
		"description": description + " Omit to use the default color.",
	}
}

func designScenarioDocumentSchema() map[string]any {
	participant := designScenarioSchemaObject(
		[]string{"id", "name", "kind", "description"},
		map[string]any{
			"id":          map[string]any{"type": "string", "minLength": 1},
			"name":        map[string]any{"type": "string"},
			"kind":        map[string]any{"type": "string", "enum": []string{"user", "client", "service", "external", "database", "queue", "other"}},
			"description": map[string]any{"type": "string"},
			"color":       designScenarioColorSchema("Object card fill as opaque #RRGGBB."),
		},
	)
	operation := designScenarioSchemaObject(
		[]string{"contractId", "operationKey"},
		map[string]any{
			"contractId":   map[string]any{"type": "string", "minLength": 1},
			"operationKey": map[string]any{"type": "string", "minLength": 1},
		},
	)
	message := designScenarioSchemaObject(
		[]string{"id", "fromId", "toId", "kind", "label", "description"},
		map[string]any{
			"id":          map[string]any{"type": "string", "minLength": 1},
			"fromId":      map[string]any{"type": "string", "minLength": 1},
			"toId":        map[string]any{"type": "string", "minLength": 1},
			"kind":        map[string]any{"type": "string", "enum": []string{"request", "response", "event", "note"}},
			"label":       map[string]any{"type": "string"},
			"description": map[string]any{"type": "string"},
			"color":       designScenarioColorSchema("Message card fill as opaque #RRGGBB."),
			"arrowColor":  designScenarioColorSchema("Message arrow stroke and arrowhead as opaque #RRGGBB; not displayed for notes."),
			"replyToId":   map[string]any{"type": "string"},
			"operation":   operation,
			"execution":   designScenarioStepExecutionSchema(),
		},
	)
	fragment := designScenarioSchemaObject(
		[]string{"id", "kind", "label", "fromMessageId", "toMessageId"},
		map[string]any{
			"id":            map[string]any{"type": "string", "minLength": 1},
			"kind":          map[string]any{"type": "string", "enum": []string{"opt", "loop"}},
			"label":         map[string]any{"type": "string"},
			"fromMessageId": map[string]any{"type": "string", "minLength": 1},
			"toMessageId":   map[string]any{"type": "string", "minLength": 1},
		},
	)
	contractSource := designScenarioSchemaObject(
		[]string{"designId", "revisionId"},
		map[string]any{
			"designId":   designScenarioPositiveIntegerSchema(),
			"revisionId": designScenarioPositiveIntegerSchema(),
			"version": map[string]any{
				"type":        "integer",
				"minimum":     0,
				"description": "Optional for copy provenance; linked contracts require a positive source version.",
			},
		},
	)
	contract := designScenarioSchemaObject(
		[]string{"id", "name", "document"},
		map[string]any{
			"id":       map[string]any{"type": "string", "minLength": 1},
			"name":     map[string]any{"type": "string"},
			"document": map[string]any{"type": "object", "additionalProperties": map[string]any{}},
			"mode":     map[string]any{"type": "string", "enum": []string{"copy", "linked"}},
			"source":   contractSource,
		},
	)
	return designScenarioSchemaObject(
		[]string{"formatVersion", "title", "participants", "messages", "fragments", "contracts"},
		map[string]any{
			"formatVersion": map[string]any{"type": "integer", "const": 1},
			"title":         map[string]any{"type": "string"},
			"participants":  map[string]any{"type": "array", "items": participant},
			"messages":      map[string]any{"type": "array", "items": message},
			"fragments":     map[string]any{"type": "array", "items": fragment},
			"contracts":     map[string]any{"type": "array", "items": contract},
			"execution":     designScenarioSchemaObject([]string{"variables"}, map[string]any{"variables": designScenarioExecutionMapSchema()}),
		},
	)
}

func designScenarioExecutionMapSchema() map[string]any {
	return map[string]any{"type": "object", "maxProperties": designscenario.MaxExecutionEntries, "additionalProperties": map[string]any{"type": "string", "maxLength": 50_000}}
}

func designScenarioStepExecutionSchema() map[string]any {
	pointer := map[string]any{"type": "string", "maxLength": 2000}
	assertion := designScenarioSchemaObject([]string{"pointer", "equals"}, map[string]any{"pointer": pointer, "equals": map[string]any{}})
	extraction := designScenarioSchemaObject([]string{"name", "pointer"}, map[string]any{"name": map[string]any{"type": "string", "pattern": "^[A-Za-z_][A-Za-z0-9_]{0,99}$"}, "pointer": pointer})
	return designScenarioSchemaObject([]string{"enabled", "pathParams", "query", "headers", "body", "assertions", "extract"}, map[string]any{
		"enabled":    map[string]any{"type": "boolean"},
		"pathParams": designScenarioExecutionMapSchema(), "query": designScenarioExecutionMapSchema(), "headers": designScenarioExecutionMapSchema(),
		"body":           map[string]any{"type": "string"},
		"expectedStatus": map[string]any{"type": "integer", "minimum": 100, "maximum": 599},
		"assertions":     map[string]any{"type": "array", "maxItems": designscenario.MaxExecutionEntries, "items": assertion},
		"extract":        map[string]any{"type": "array", "maxItems": designscenario.MaxExecutionEntries, "items": extraction},
	})
}

func designScenarioCommandSchemas() []any {
	stringField := func() map[string]any { return map[string]any{"type": "string"} }
	command := func(commandType string, required []string, properties map[string]any) any {
		properties["type"] = map[string]any{"type": "string", "const": commandType}
		return designScenarioSchemaObject(append([]string{"type"}, required...), properties)
	}
	participant := designScenarioDocumentSchema()["properties"].(map[string]any)["participants"].(map[string]any)["items"]
	message := designScenarioDocumentSchema()["properties"].(map[string]any)["messages"].(map[string]any)["items"]
	fragment := designScenarioDocumentSchema()["properties"].(map[string]any)["fragments"].(map[string]any)["items"]
	newContract := designScenarioSchemaObject([]string{"id", "name", "document"}, map[string]any{
		"id":       map[string]any{"type": "string", "minLength": 1},
		"name":     stringField(),
		"document": map[string]any{"type": "object", "additionalProperties": map[string]any{}},
		"mode":     map[string]any{"type": "string", "const": "copy"},
	})
	return []any{
		command("set_title", []string{"title"}, map[string]any{"title": stringField()}),
		command("upsert_participant", []string{"participant"}, map[string]any{"participant": participant}),
		command("remove_participant", []string{"id"}, map[string]any{"id": stringField()}),
		command("move_participant", []string{"id", "index"}, map[string]any{"id": stringField(), "index": map[string]any{"type": "integer", "minimum": 0}}),
		command("upsert_message", []string{"message"}, map[string]any{"message": message}),
		command("remove_message", []string{"id"}, map[string]any{"id": stringField()}),
		command("move_message", []string{"id", "index"}, map[string]any{"id": stringField(), "index": map[string]any{"type": "integer", "minimum": 0}}),
		command("upsert_fragment", []string{"fragment"}, map[string]any{"fragment": fragment}),
		command("remove_fragment", []string{"id"}, map[string]any{"id": stringField()}),
		command("bind_operation", []string{"messageId", "contractId", "operationKey"}, map[string]any{"messageId": stringField(), "contractId": stringField(), "operationKey": stringField()}),
		command("create_operation", []string{"messageId", "method", "path"}, map[string]any{"messageId": stringField(), "contractId": stringField(), "method": stringField(), "path": stringField(), "label": stringField()}),
		command("create_contract", []string{"contract"}, map[string]any{"contract": newContract}),
		command("import_contract", []string{"designId", "mode"}, map[string]any{"id": stringField(), "designId": designScenarioPositiveIntegerSchema(), "revisionId": designScenarioPositiveIntegerSchema(), "mode": map[string]any{"type": "string", "enum": []string{"copy", "linked"}}}),
		command("refresh_contract", []string{"contractId"}, map[string]any{"contractId": stringField(), "revisionId": designScenarioPositiveIntegerSchema()}),
		command("detach_contract", []string{"contractId"}, map[string]any{"contractId": stringField()}),
		command("materialize_contract", []string{"contractId"}, map[string]any{"contractId": stringField()}),
	}
}

func designScenarioToolError(status int, body []byte) error {
	err := toolErr(status, body)
	if status < http.StatusBadRequest || status >= http.StatusInternalServerError {
		return err
	}
	var envelope struct {
		Error struct {
			Details jsonx.RawMessage `json:"details"`
		} `json:"error"`
	}
	if jsonx.Unmarshal(body, &envelope) == nil && len(envelope.Error.Details) > 0 {
		return fmt.Errorf("%w; details: %s", err, envelope.Error.Details)
	}
	return err
}
