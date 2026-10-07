package mcp

import (
	sdk "github.com/modelcontextprotocol/go-sdk/mcp"

	"github.com/yashok111/mocker/internal/schemamodel"
)

// Strings carry schema documents unchanged through the SDK and JSON decoder,
// including numbers larger than float64 can represent. Pointer fields preserve
// omitted required membership and explicit false or zero values.
type schemaModelCommand = schemamodel.Command

type schemaModelInput struct {
	DesignID        int64                `json:"designId"`
	ExpectedVersion int64                `json:"expectedVersion,omitempty"`
	Document        *string              `json:"document,omitempty"`
	Commands        []schemaModelCommand `json:"commands,omitempty"`
	schemaModelCommand
}

type schemaModelAction struct {
	name, kind, description string
	required, optional      []string
}

var schemaModelActions = []schemaModelAction{
	{"create_api_schema", "create_schema", "Creates a new component schema. schemaJSON is the complete JSON Schema object or boolean; the name must be absent.", []string{"schemaName", "schemaJSON"}, nil},
	{"replace_api_schema", "replace_schema", "Replaces one existing component with the complete schemaJSON object or boolean. Read its schemaJSON first and retain keywords you still need.", []string{"schemaName", "schemaJSON"}, nil},
	{"rename_api_schema", "rename_schema", "Renames a component and updates local references, JSON Pointer suffixes, discriminator mappings and layout. Example values and opaque extensions are retained.", []string{"schemaName", "newName"}, nil},
	{"delete_api_schema", "delete_schema", "Deletes a component only when no other consumer references it. Refused deletions include actionable JSON pointers.", []string{"schemaName"}, nil},
	{"upsert_api_schema_property", "upsert_property", "Adds or replaces one field with complete schemaJSON, retaining other fields. Omitted required keeps existing membership; false removes membership. Parent must be object-compatible.", []string{"schemaName", "propertyName", "schemaJSON"}, []string{"required"}},
	{"rename_api_schema_property", "rename_property", "Renames a field and updates required membership and local references into it. Parent must be object-compatible.", []string{"schemaName", "propertyName", "newName"}, nil},
	{"delete_api_schema_property", "delete_property", "Deletes a field and its required membership only when no consumer references it; returns pointers for blocked deletions.", []string{"schemaName", "propertyName"}, nil},
	{"set_api_schema_reference", "set_reference", "Links a field to an existing component. false or omitted array sets $ref and removes the field's old type/items. array=true sets type=array, removes the field's $ref, and sets items.$ref after removing the element's old type/items. Other keywords are retained. Resource scopes with $id or removal of nested resources are refused; inspect their consumers and use explicit schemaJSON edits.", []string{"schemaName", "propertyName", "targetSchema"}, []string{"array"}},
	{"move_api_schema", "move_schema", "Sets a component card position in x-mocker-schema-layout. Supply finite x and y between -100000 and 100000.", []string{"schemaName", "x", "y"}, nil},
}

func addSchemaModelTools(s *sdk.Server, lb *loopback) {
	const base = "/api/designs/{id}/schema-model"
	addSchemaModelTool(s, lb, "get_schema_model", "GET "+base,
		"Reads component schemas, fields, reference sites, transitive operation usages and card positions with designId/version/revisionId. External references are shown without fetching them.", true, "", schemaModelObject([]string{"designId"}, nil))
	preview := schemaModelObject([]string{"designId"}, map[string]any{
		"document": map[string]any{"type": "string", "description": "Optional complete proposed OpenAPI text; omission reads the saved draft."},
		"commands": schemaModelCommandsSchema(0),
	})
	addSchemaModelTool(s, lb, "preview_schema_model_changes", "POST "+base+"/preview",
		"Previews ordered atomic schema edits against document or the saved draft. Returns document, model, valid and diagnostics without saving, adding history or changing mocks. Omit commands to inspect an unsaved document.", true, "", preview)
	batch := schemaModelObject([]string{"designId", "expectedVersion", "commands"}, map[string]any{
		"expectedVersion": schemaModelVersionSchema(), "commands": schemaModelCommandsSchema(1),
	})
	addSchemaModelTool(s, lb, "apply_schema_model_commands", "POST "+base+"/commands",
		"Atomically applies 1-100 ordered schema commands to the latest API draft and returns API detail. Requires expectedVersion; reread and reconcile a 409. The complete contract must validate before saving. Does not publish.", false, "", batch)
	for _, action := range schemaModelActions {
		properties := schemaModelActionProperties(action)
		properties["expectedVersion"] = schemaModelVersionSchema()
		required := append([]string{"designId", "expectedVersion"}, action.required...)
		addSchemaModelTool(s, lb, action.name, "POST "+base+"/commands", action.description+
			" Requires current expectedVersion; reread and reconcile conflicts. Returns updated API detail, preserving unrelated document fields. Does not publish.", false, action.kind, schemaModelObject(required, properties))
	}
}

func addSchemaModelTool(s *sdk.Server, lb *loopback, name, route, description string, readOnly bool, kind string, schema any) {
	// Reuse the validated raw SDK adapter: reflection-based SDK input handling
	// otherwise rounds int64 version fences through float64 before decoding.
	addRawDesignScenarioTool(s, lb, &sdk.Tool{
		Name: name, Description: description, InputSchema: schema,
		Annotations: &sdk.ToolAnnotations{ReadOnlyHint: readOnly, IdempotentHint: readOnly},
	}, route, func(in schemaModelInput) (designScenarioCall, error) {
		call, err := apiDesignRead(in.DesignID)
		if !readOnly {
			call, err = apiDesignWrite(in.DesignID, in.ExpectedVersion)
		}
		out := designScenarioCall{params: call.params}
		if err != nil || name == "get_schema_model" {
			return out, err
		}
		if name == "preview_schema_model_changes" {
			out.body = struct {
				Document *string              `json:"document,omitempty"`
				Commands []schemaModelCommand `json:"commands,omitempty"`
			}{in.Document, in.Commands}
		} else {
			if kind != "" {
				in.schemaModelCommand.Kind = kind
				in.Commands = []schemaModelCommand{in.schemaModelCommand}
			}
			out.body = struct {
				ExpectedVersion int64                `json:"expectedVersion"`
				Commands        []schemaModelCommand `json:"commands"`
			}{in.ExpectedVersion, in.Commands}
		}
		return out, nil
	})
}

func schemaModelObject(required []string, properties map[string]any) map[string]any {
	if properties == nil {
		properties = make(map[string]any)
	}
	properties["designId"] = designScenarioPositiveIntegerSchema()
	return designScenarioSchemaObject(required, properties)
}

func schemaModelVersionSchema() map[string]any {
	return map[string]any{"type": "integer", "minimum": 1, "description": "Current API design version from get_api_design or get_schema_model. On 409, reread and reconcile before retrying."}
}

func schemaModelActionProperties(action schemaModelAction) map[string]any {
	properties := make(map[string]any)
	for _, key := range append(append([]string{}, action.required...), action.optional...) {
		switch key {
		case "required", "array":
			properties[key] = map[string]any{"type": "boolean"}
		case "x", "y":
			properties[key] = map[string]any{"type": "number", "minimum": -100000, "maximum": 100000}
		case "schemaJSON":
			properties[key] = map[string]any{"type": "string", "minLength": 1, "maxLength": 65536, "description": "Complete JSON Schema object or boolean as text (64 KiB maximum). Retain advanced keywords and exact numbers in this string."}
		default:
			properties[key] = map[string]any{"type": "string", "minLength": 1}
		}
	}
	return properties
}

func schemaModelCommandsSchema(minItems int) map[string]any {
	variants := make([]any, 0, len(schemaModelActions))
	for _, action := range schemaModelActions {
		properties := schemaModelActionProperties(action)
		properties["kind"] = map[string]any{"type": "string", "const": action.kind}
		variants = append(variants, designScenarioSchemaObject(append([]string{"kind"}, action.required...), properties))
	}
	return map[string]any{"type": "array", "minItems": minItems, "maxItems": 100, "items": map[string]any{"oneOf": variants}}
}
