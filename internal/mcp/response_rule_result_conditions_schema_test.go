package mcp

import (
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func TestResponseRuleResultConditionSchemasAgree(t *testing.T) {
	t.Parallel()
	const source = `{"source":"result","nodeId":"read","pointer":"/n"}`
	for _, tt := range []struct {
		name, payload string
		valid         bool
	}{
		{"exact integer", `{"source":` + source + `,"op":"equals","valueJSON":"9007199254740993"}`, true},
		{"unequal fraction", `{"source":` + source + `,"op":"not_equals","valueJSON":"0.10000000000000000001"}`, true},
		{"null", `{"source":` + source + `,"op":"equals","valueJSON":"null"}`, true},
		{"boolean", `{"source":` + source + `,"op":"equals","valueJSON":"false"}`, true},
		{"escaped string", `{"source":` + source + `,"op":"equals","valueJSON":"\"a\\n\\u0041\""}`, true},
		{"huge exponent", `{"source":` + source + `,"op":"equals","valueJSON":" 1e999999999999999999999 "}`, true},
		{"exists", `{"source":` + source + `,"op":"exists"}`, true},
		{"not exists root", `{"source":{"source":"result","nodeId":"read"},"op":"not_exists"}`, true},
		{"future reference is structurally valid", `{"source":{"source":"result","nodeId":"future"},"op":"exists"}`, true},
		{"null payload", `null`, false},
		{"missing source", `{"op":"exists"}`, false},
		{"null source", `{"source":null,"op":"exists"}`, false},
		{"request source", `{"source":{"source":"query","name":"n"},"op":"exists"}`, false},
		{"literal source", `{"source":{"source":"literal","valueJSON":"1"},"op":"exists"}`, false},
		{"missing producer", `{"source":{"source":"result"},"op":"exists"}`, false},
		{"unexpected source name", `{"source":{"source":"result","nodeId":"read","name":"n"},"op":"exists"}`, false},
		{"unexpected source value", `{"source":{"source":"result","nodeId":"read","valueJSON":"1"},"op":"exists"}`, false},
		{"unsupported op", `{"source":` + source + `,"op":"contains","valueJSON":"1"}`, false},
		{"comparison missing expected", `{"source":` + source + `,"op":"equals"}`, false},
		{"comparison null field", `{"source":` + source + `,"op":"equals","valueJSON":null}`, false},
		{"comparison numeric field", `{"source":` + source + `,"op":"equals","valueJSON":1}`, false},
		{"object literal", `{"source":` + source + `,"op":"equals","valueJSON":"{}"}`, false},
		{"array literal", `{"source":` + source + `,"op":"equals","valueJSON":"[]"}`, false},
		{"empty literal", `{"source":` + source + `,"op":"equals","valueJSON":""}`, false},
		{"trailing literal", `{"source":` + source + `,"op":"equals","valueJSON":"1 true"}`, false},
		{"invalid number", `{"source":` + source + `,"op":"equals","valueJSON":"01"}`, false},
		{"invalid string escape", `{"source":` + source + `,"op":"equals","valueJSON":"\"\\x\""}`, false},
		{"existence value", `{"source":` + source + `,"op":"exists","valueJSON":"null"}`, false},
		{"existence null field", `{"source":` + source + `,"op":"not_exists","valueJSON":null}`, false},
		{"unknown field", `{"source":` + source + `,"op":"exists","extra":true}`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var payload any
			if err := json.Unmarshal([]byte(tt.payload), &payload); err != nil {
				t.Fatal(err)
			}
			assertResponseRuleNodeSchemas(t, resultConditionSchemaNode(payload), tt.valid)
		})
	}
	t.Run("both payloads", func(t *testing.T) {
		node := resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "result", "nodeId": "read"}, "op": "exists"})
		node["condition"] = map[string]any{"in": "query", "name": "n", "op": "exists"}
		assertResponseRuleNodeSchemas(t, node, false)
	})
	t.Run("neither payload", func(t *testing.T) {
		node := resultConditionSchemaNode(nil)
		delete(node, "resultCondition")
		assertResponseRuleNodeSchemas(t, node, false)
	})
	for _, kind := range []string{"start", "fallback", "delay", "response", "entity_read", "entity_create", "entity_update"} {
		t.Run("payload on "+kind, func(t *testing.T) {
			node := resultConditionSchemaNode(map[string]any{"source": map[string]any{"source": "result", "nodeId": "read"}, "op": "exists"})
			node["type"] = kind
			switch kind {
			case "delay":
				node["delayMs"] = 0
			case "response":
				node["response"] = map[string]any{"status": 200, "mediaType": "application/json", "headers": []any{}}
			case "entity_read":
				node["entity"] = map[string]any{"family": "/widgets", "operation": "list"}
			case "entity_create":
				node["entity"] = map[string]any{"family": "/widgets", "data": map[string]any{"source": "body"}}
			case "entity_update":
				node["entity"] = map[string]any{"family": "/widgets", "key": map[string]any{"source": "query", "name": "id"}, "data": map[string]any{"source": "body"}}
			}
			assertResponseRuleNodeSchemas(t, node, false)
		})
	}
}

func resultConditionSchemaNode(payload any) map[string]any {
	return map[string]any{"id": "check", "type": "condition", "name": "Check", "x": 0, "y": 0, "resultCondition": payload}
}

func TestResponseRuleResultConditionTraceSchema(t *testing.T) {
	t.Parallel()
	api, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var document any
	if err := json.Unmarshal(api, &document); err != nil {
		t.Fatal(err)
	}
	compiler := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/result-condition-trace.json"
	if err := compiler.AddResource(uri, document); err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile(uri + "#/components/schemas/ResponseRuleStep")
	if err != nil {
		t.Fatal(err)
	}
	for _, tt := range []struct {
		name, trace string
		valid       bool
	}{
		{"exact comparison", `{"sourceNodeId":"read","pointer":"/n","op":"equals","present":true,"actualJSON":"9007199254740993","expectedJSON":"9007199254740993.0"}`, true},
		{"null present", `{"sourceNodeId":"read","pointer":"/n","op":"exists","present":true,"actualJSON":"null"}`, true},
		{"missing pointer", `{"sourceNodeId":"read","pointer":"/missing","op":"not_exists","present":false}`, true},
		{"missing presence", `{"sourceNodeId":"read","pointer":"/n","op":"exists"}`, false},
		{"numeric JSON field", `{"sourceNodeId":"read","pointer":"/n","op":"equals","present":true,"actualJSON":9007199254740993,"expectedJSON":"1"}`, false},
		{"unsupported op", `{"sourceNodeId":"read","pointer":"/n","op":"contains","present":true}`, false},
		{"unknown field", `{"sourceNodeId":"read","pointer":"/n","op":"exists","present":false,"extra":false}`, false},
		{"null payload", `null`, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var trace any
			if err := json.Unmarshal([]byte(tt.trace), &trace); err != nil {
				t.Fatal(err)
			}
			step := map[string]any{"step": 3, "nodeId": "check", "matched": true, "edgeId": "yes", "resultCondition": trace}
			if err := schema.Validate(step); (err == nil) != tt.valid {
				t.Fatalf("valid=%t, error=%v", tt.valid, err)
			}
		})
	}
	t.Run("container trace exceeds literal limit", func(t *testing.T) {
		step := map[string]any{"step": 3, "nodeId": "check", "matched": true, "resultCondition": map[string]any{
			"sourceNodeId": "read", "pointer": "", "op": "exists", "present": true,
			"actualJSON": `["` + strings.Repeat("a", 66000) + `"]`,
		}}
		if err := schema.Validate(step); err != nil {
			t.Fatal(err)
		}
	})
}
