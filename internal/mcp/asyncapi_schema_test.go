package mcp

import (
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestAsyncAPIScenarioToolSchemas(t *testing.T) {
	const model = `{"servers":[],"channels":[],"messages":[],"schemas":[],"contracts":[]}`
	const document = `{"formatVersion":3,"title":"Events","participants":[],"messages":[],"fragments":[],"contracts":[],"eventModel":` + model + `}`
	for _, tt := range []struct {
		name, tool, input string
		valid             bool
	}{
		{"create v3", "create_design_scenario", `{"document":` + document + `}`, true},
		{"atomic model", "apply_design_scenario_commands", `{"scenarioId":1,"expectedVersion":1,"commands":[{"type":"set_event_model","eventModel":` + model + `}]}`, true},
		{"missing model", "apply_design_scenario_commands", `{"scenarioId":1,"expectedVersion":1,"commands":[{"type":"set_event_model"}]}`, false},
		{"null model", "apply_design_scenario_commands", `{"scenarioId":1,"expectedVersion":1,"commands":[{"type":"set_event_model","eventModel":null}]}`, false},
		{"legacy model", "create_design_scenario", `{"document":{"formatVersion":2,"title":"Events","participants":[],"messages":[],"fragments":[],"contracts":[],"eventModel":` + model + `}}`, false},
		{"unknown field", "create_design_scenario", `{"document":{"formatVersion":3,"title":"Events","participants":[],"messages":[],"fragments":[],"contracts":[],"eventModel":{"servers":[],"channels":[],"messages":[],"schemas":[],"contracts":[],"unknown":true}}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			schema, err := compileDesignScenarioToolSchema(&sdk.Tool{Name: tt.tool, InputSchema: designScenarioInputSchema(tt.tool)})
			if err != nil {
				t.Fatal(err)
			}
			var input any
			if err := jsonx.Unmarshal([]byte(tt.input), &input); err != nil {
				t.Fatal(err)
			}
			err = schema.Validate(input)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%t, validation error: %v", tt.valid, err)
			}
		})
	}
}

func TestAsyncAPIDocumentSchemasAgree(t *testing.T) {
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api any
	if err := jsonx.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/admin.json"
	if err := compiler.AddResource(uri, api); err != nil {
		t.Fatal(err)
	}
	rest, err := compiler.Compile(uri + "#/components/schemas/DesignScenarioDocument")
	if err != nil {
		t.Fatal(err)
	}
	mcp, err := compileDesignScenarioToolSchema(&sdk.Tool{Name: "create_design_scenario", InputSchema: designScenarioInputSchema("create_design_scenario")})
	if err != nil {
		t.Fatal(err)
	}
	fixture, err := os.ReadFile("../designscenario/testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name   string
		valid  bool
		mutate func(map[string]any, map[string]any, map[string]any)
	}{
		{"shared producer and consumer", true, func(_, _, _ map[string]any) {}},
		{"model in v1", false, func(doc, _, _ map[string]any) { doc["formatVersion"] = 1 }},
		{"model in v2", false, func(doc, _, _ map[string]any) { doc["formatVersion"] = 2 }},
		{"missing collection", false, func(_, model, _ map[string]any) { delete(model, "schemas") }},
		{"null collection", false, func(_, model, _ map[string]any) { model["schemas"] = nil }},
		{"empty bindings", false, func(_, _, arrow map[string]any) { arrow["eventBindings"] = []any{} }},
		{"bindings on HTTP arrow", false, func(_, _, arrow map[string]any) { arrow["kind"] = "request" }},
		{"HTTP and event binding", false, func(_, _, arrow map[string]any) {
			arrow["operation"] = map[string]any{"contractId": "http", "operationKey": "readOrders"}
		}},
		{"unknown nested field", false, func(_, model, _ map[string]any) { model["servers"].([]any)[0].(map[string]any)["password"] = "secret" }},
		{"partitions overflow", false, func(_, model, _ map[string]any) {
			model["channels"].([]any)[0].(map[string]any)["kafka"] = map[string]any{"partitions": 2147483648}
		}},
		{"schema object instead of text", false, func(_, model, _ map[string]any) {
			model["schemas"].([]any)[0].(map[string]any)["schemaJSON"] = map[string]any{"type": "object"}
		}},
		{"consumer newline", false, func(_, model, _ map[string]any) {
			operation := model["contracts"].([]any)[1].(map[string]any)["operations"].([]any)[0].(map[string]any)
			operation["kafka"] = map[string]any{"groupId": "bad\ngroup"}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var doc map[string]any
			if err := jsonx.Unmarshal(fixture, &doc); err != nil {
				t.Fatal(err)
			}
			tt.mutate(doc, doc["eventModel"].(map[string]any), doc["messages"].([]any)[0].(map[string]any))
			for name, validation := range map[string]error{"REST": rest.Validate(doc), "MCP": mcp.Validate(map[string]any{"document": doc})} {
				if (validation == nil) != tt.valid {
					t.Errorf("%s valid=%t, error=%v", name, tt.valid, validation)
				}
			}
		})
	}
}
