package mcp

import (
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResponseRuleEntitySchemasAgree(t *testing.T) {
	t.Parallel()
	const key = `{"source":"literal","valueJSON":"1"}`
	const data = `{"source":"body"}`
	for _, tt := range []struct {
		name, kind, entity string
		valid              bool
	}{
		{"get", "entity_read", `{"family":"/orders","operation":"get","key":` + key + `}`, true},
		{"list", "entity_read", `{"family":"/orders","operation":"list","scope":[]}`, true},
		{"create", "entity_create", `{"family":"/orders","data":` + data + `}`, true},
		{"update", "entity_update", `{"family":"/orders","key":` + key + `,"data":` + data + `}`, true},
		{"read missing operation", "entity_read", `{"family":"/orders","key":` + key + `}`, false},
		{"get missing key", "entity_read", `{"family":"/orders","operation":"get"}`, false},
		{"get data", "entity_read", `{"family":"/orders","operation":"get","key":` + key + `,"data":` + data + `}`, false},
		{"list key", "entity_read", `{"family":"/orders","operation":"list","key":` + key + `}`, false},
		{"list data", "entity_read", `{"family":"/orders","operation":"list","data":` + data + `}`, false},
		{"create missing data", "entity_create", `{"family":"/orders"}`, false},
		{"create key", "entity_create", `{"family":"/orders","key":` + key + `,"data":` + data + `}`, false},
		{"create operation", "entity_create", `{"family":"/orders","operation":"get","data":` + data + `}`, false},
		{"update missing key", "entity_update", `{"family":"/orders","data":` + data + `}`, false},
		{"update missing data", "entity_update", `{"family":"/orders","key":` + key + `}`, false},
		{"update operation", "entity_update", `{"family":"/orders","operation":"get","key":` + key + `,"data":` + data + `}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var entity any
			if err := jsonx.Unmarshal([]byte(tt.entity), &entity); err != nil {
				t.Fatal(err)
			}
			assertResponseRuleNodeSchemas(t, map[string]any{
				"id": "entity", "type": tt.kind, "name": "Entity", "x": 0, "y": 0, "entity": entity,
			}, tt.valid)
		})
	}
}

func TestResponseRuleBodySourceSchemasAgree(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name, response string
		valid          bool
	}{
		{"no body", `{"status":200,"mediaType":"application/json","headers":[]}`, true},
		{"literal body", `{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"null"}`, true},
		{"referenced body", `{"status":200,"mediaType":"application/json","headers":[],"bodyFrom":{"source":"body"}}`, true},
		{"both sources", `{"status":200,"mediaType":"application/json","headers":[],"bodyJSON":"null","bodyFrom":{"source":"body"}}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var response any
			if err := jsonx.Unmarshal([]byte(tt.response), &response); err != nil {
				t.Fatal(err)
			}
			assertResponseRuleNodeSchemas(t, map[string]any{
				"id": "response", "type": "response", "name": "Response", "x": 0, "y": 0, "response": response,
			}, tt.valid)
		})
	}
}

func assertResponseRuleNodeSchemas(t *testing.T, node map[string]any, valid bool) {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var api any
	if err := jsonx.Unmarshal(raw, &api); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/response-rule-entities.json"
	if err := compiler.AddResource(uri, api); err != nil {
		t.Fatal(err)
	}
	rest, err := compiler.Compile(uri + "#/components/schemas/ResponseRuleNode")
	if err != nil {
		t.Fatal(err)
	}
	if err := rest.Validate(node); (err == nil) != valid {
		t.Errorf("REST valid=%t, error=%v", valid, err)
	}
	rule := map[string]any{"id": "rule", "name": "Rule", "nodes": []any{node}, "edges": []any{}}
	for _, name := range []string{"create_response_rule", "save_response_rule", "apply_response_rule_commands"} {
		schema, err := compileDesignScenarioToolSchema(&sdk.Tool{Name: name, InputSchema: responseRuleInputSchema(name)})
		if err != nil {
			t.Fatal(err)
		}
		input := map[string]any{"designId": 1, "expectedVersion": 1}
		if name != "create_response_rule" {
			input["ruleId"] = "rule"
		}
		if name == "apply_response_rule_commands" {
			input["commands"] = []any{map[string]any{"type": "update_node", "node": node}}
		} else {
			input["rule"] = rule
		}
		if err := schema.Validate(input); (err == nil) != valid {
			t.Errorf("MCP %s valid=%t, error=%v", name, valid, err)
		}
	}
}
