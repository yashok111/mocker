package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/resourcemap"
)

type resourceMapInput struct {
	DesignID        int64                 `json:"designId"`
	ExpectedVersion int64                 `json:"expectedVersion,omitempty"`
	Document        *string               `json:"document,omitempty"`
	Commands        []jsonx.RawMessage    `json:"commands,omitempty"`
	Resource        *resourcemap.Resource `json:"resource,omitempty"`
	ResourceID      string                `json:"resourceId,omitempty"`
	OperationKey    string                `json:"operationKey,omitempty"`
	Relation        *resourcemap.Relation `json:"relation,omitempty"`
	RelationID      string                `json:"relationId,omitempty"`
	X               *float64              `json:"x,omitempty"`
	Y               *float64              `json:"y,omitempty"`
}

type resourceMapAction struct {
	name, kind, description string
	required                []string
}

var resourceMapActions = []resourceMapAction{
	{
		name: "auto_layout_api_resources", kind: "auto_layout", required: []string{},
		description: "Arranges all resource cards deterministically from their relationships. Preserves resource metadata and operation assignments. Preview first with preview_api_resource_map and the auto_layout command when approval is needed.",
	},
	{"upsert_api_resource", "upsert_resource", "Creates or replaces a complete resource annotation; names and assignments are retained across operation renames by stable key.", []string{"resource"}},
	{"remove_api_resource", "remove_resource", "Removes a persisted resource annotation and its relations; operations return to automatic grouping.", []string{"resourceId"}},
	{"assign_api_resource_operation", "assign_operation", "Assigns one operation key to a resource; empty resourceId restores automatic grouping.", []string{"operationKey", "resourceId"}},
	{"upsert_api_resource_relation", "upsert_relation", "Creates or replaces one explicit directed relationship between resources.", []string{"relation"}},
	{"remove_api_resource_relation", "remove_relation", "Removes one explicit relationship.", []string{"relationId"}},
	{"move_api_resource", "move_resource", "Sets a resource card position; an inferred resource is materialized.", []string{"resourceId", "x", "y"}},
}

func addResourceMapTools(s *sdk.Server, lb *loopback) {
	const base = "/api/designs/{id}/resource-map"
	addResourceMapTool(s, lb, "get_api_resource_map", "GET "+base,
		"Reads the saved API resource projection and current saved scenario draft usages. Usages include pinned source revision and copy/linked mode; usagesTruncated marks a bounded scan.", true, "", resourceMapObject([]string{"designId"}, nil))
	addResourceMapTool(s, lb, "preview_api_resource_map", "POST "+base+"/preview",
		"Projects an optional unsaved OpenAPI document and 0-100 ordered resource commands without saving. Returns normalized document, model, validity and diagnostics.", true, "",
		resourceMapObject([]string{"designId"}, map[string]any{"document": map[string]any{"type": "string"}, "commands": resourceMapCommandsSchema(0)}))
	addResourceMapTool(s, lb, "apply_api_resource_map_commands", "POST "+base+"/commands",
		"Atomically applies 1-100 commands to the current API draft. Requires expectedVersion; reread and reconcile conflicts. Returns API detail without publishing.", false, "",
		resourceMapObject([]string{"designId", "expectedVersion", "commands"}, map[string]any{"expectedVersion": schemaModelVersionSchema(), "commands": resourceMapCommandsSchema(1)}))
	for _, action := range resourceMapActions {
		properties := resourceMapActionProperties(action)
		properties["expectedVersion"] = schemaModelVersionSchema()
		required := append([]string{"designId", "expectedVersion"}, action.required...)
		addResourceMapTool(s, lb, action.name, "POST "+base+"/commands", action.description+
			" Requires current expectedVersion and returns updated API detail without publishing.", false, action.kind, resourceMapObject(required, properties))
	}
}

func addResourceMapTool(s *sdk.Server, lb *loopback, name, route, description string, readOnly bool, kind string, schema any) {
	addRawDesignScenarioTool(s, lb, &sdk.Tool{Name: name, Description: description, InputSchema: schema,
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly}}, route,
		func(in resourceMapInput) (designScenarioCall, error) {
			call, err := apiDesignRead(in.DesignID)
			if !readOnly {
				call, err = apiDesignWrite(in.DesignID, in.ExpectedVersion)
			}
			out := designScenarioCall{params: call.params}
			if err != nil || name == "get_api_resource_map" {
				return out, err
			}
			if name == "preview_api_resource_map" {
				out.body = struct {
					Document *string            `json:"document,omitempty"`
					Commands []jsonx.RawMessage `json:"commands,omitempty"`
				}{in.Document, in.Commands}
			} else {
				var commands any = in.Commands
				if kind != "" {
					command := map[string]any{"kind": kind}
					switch kind {
					case "upsert_resource":
						command["resource"] = in.Resource
					case "remove_resource":
						command["resourceId"] = in.ResourceID
					case "assign_operation":
						command["operationKey"], command["resourceId"] = in.OperationKey, in.ResourceID
					case "upsert_relation":
						command["relation"] = in.Relation
					case "remove_relation":
						command["relationId"] = in.RelationID
					case "move_resource":
						command["resourceId"], command["x"], command["y"] = in.ResourceID, in.X, in.Y
					}
					commands = []any{command}
				}
				out.body = struct {
					ExpectedVersion int64 `json:"expectedVersion"`
					Commands        any   `json:"commands"`
				}{in.ExpectedVersion, commands}
			}
			return out, nil
		})
}

func resourceMapObject(required []string, properties map[string]any) map[string]any {
	if properties == nil {
		properties = make(map[string]any)
	}
	properties["designId"] = designScenarioPositiveIntegerSchema()
	return designScenarioSchemaObject(required, properties)
}

func resourceMapIDSchema() map[string]any {
	return map[string]any{"type": "string", "minLength": 1, "maxLength": 200}
}
func resourceMapCoordinateSchema() map[string]any {
	return map[string]any{"type": "number", "minimum": -100000, "maximum": 100000}
}

func resourceMapResourceSchema() map[string]any {
	return designScenarioSchemaObject([]string{"id", "name", "service", "description", "operationKeys", "x", "y"}, map[string]any{
		"id": resourceMapIDSchema(), "name": map[string]any{"type": "string", "maxLength": 200},
		"service": map[string]any{"type": "string", "maxLength": 200}, "description": map[string]any{"type": "string", "maxLength": 2000},
		"operationKeys": map[string]any{"type": "array", "maxItems": 1000, "items": resourceMapIDSchema()},
		"x":             resourceMapCoordinateSchema(), "y": resourceMapCoordinateSchema(),
	})
}

func resourceMapRelationSchema() map[string]any {
	return designScenarioSchemaObject([]string{"id", "fromResourceId", "toResourceId", "label"}, map[string]any{
		"id": resourceMapIDSchema(), "fromResourceId": resourceMapIDSchema(), "toResourceId": resourceMapIDSchema(),
		"label": map[string]any{"type": "string", "maxLength": 200},
	})
}

func resourceMapActionProperties(action resourceMapAction) map[string]any {
	properties := make(map[string]any)
	for _, key := range action.required {
		switch key {
		case "resource":
			properties[key] = resourceMapResourceSchema()
		case "relation":
			properties[key] = resourceMapRelationSchema()
		case "x", "y":
			properties[key] = resourceMapCoordinateSchema()
		case "resourceId":
			if action.kind == "assign_operation" {
				properties[key] = map[string]any{"type": "string", "maxLength": 200}
			} else {
				properties[key] = resourceMapIDSchema()
			}
		default:
			properties[key] = resourceMapIDSchema()
		}
	}
	return properties
}

func resourceMapCommandsSchema(minItems int) map[string]any {
	variants := make([]any, 0, len(resourceMapActions))
	for _, action := range resourceMapActions {
		properties := resourceMapActionProperties(action)
		properties["kind"] = map[string]any{"type": "string", "const": action.kind}
		variants = append(variants, designScenarioSchemaObject(append([]string{"kind"}, action.required...), properties))
	}
	return map[string]any{"type": "array", "minItems": minItems, "maxItems": 100, "items": map[string]any{"oneOf": variants}}
}
