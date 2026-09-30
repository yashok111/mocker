package mcp

import (
	"encoding/json"
	"os"
	"testing"

	sdk "github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/santhosh-tekuri/jsonschema/v6"
)

func followupRESTSchema(t *testing.T, name string) *jsonschema.Schema {
	t.Helper()
	raw, err := os.ReadFile("../../api/openapi.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	c := jsonschema.NewCompiler()
	const uri = "https://mocker.invalid/followups.json"
	if err := c.AddResource(uri, doc); err != nil {
		t.Fatal(err)
	}
	s, err := c.Compile(uri + "#/components/schemas/" + name)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func followupMCPSchema(t *testing.T, value any) *jsonschema.Schema {
	t.Helper()
	s, err := compileDesignScenarioToolSchema(&sdk.Tool{Name: "followups", InputSchema: value})
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func TestResponseRuleFollowupPredicateSchemas(t *testing.T) {
	t.Parallel()
	ref := map[string]any{"source": "result", "nodeId": "left", "pointer": "/amount"}
	leaf := map[string]any{"source": ref, "op": "greater_than", "valueJSON": "9007199254740993"}
	exists := map[string]any{"source": ref, "op": "exists"}
	rhs := map[string]any{"source": ref, "op": "less_or_equal", "valueFrom": map[string]any{"source": "result", "nodeId": "right", "pointer": "/limit"}}
	group := func(key string, child any) map[string]any { return map[string]any{key: []any{exists, child}} }
	schemas := []*jsonschema.Schema{followupRESTSchema(t, "ResponseRuleResultCondition"), followupMCPSchema(t, responseRuleResultConditionSchema())}
	for _, tt := range []struct {
		name  string
		value any
		valid bool
	}{
		{"ordering", leaf, true}, {"result RHS", rhs, true}, {"nested", group("all", group("any", rhs)), true},
		{"four levels", group("all", group("any", group("all", leaf))), true},
		{"five levels", group("all", group("any", group("all", group("any", leaf)))), false},
		{"non-number ordering", map[string]any{"source": ref, "op": "greater_than", "valueJSON": "null"}, false},
		{"both RHS", map[string]any{"source": ref, "op": "equals", "valueJSON": "1", "valueFrom": ref}, false},
		{"request RHS", map[string]any{"source": ref, "op": "equals", "valueFrom": map[string]any{"source": "body"}}, false},
		{"exists RHS", map[string]any{"source": ref, "op": "exists", "valueFrom": ref}, false},
		{"one child", map[string]any{"all": []any{leaf}}, false},
		{"mixed group", map[string]any{"all": []any{leaf, exists}, "source": ref, "op": "exists"}, false},
		{"null group", map[string]any{"any": nil}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			for i, s := range schemas {
				if err := s.Validate(tt.value); (err == nil) != tt.valid {
					t.Errorf("schema %d valid=%t: %v", i, tt.valid, err)
				}
			}
		})
	}
}

func TestResponseRuleFollowupExamplesSchemas(t *testing.T) {
	t.Parallel()
	request := map[string]any{"query": []any{}, "headers": []any{}, "bodyJSON": "{\"n\":9007199254740993}"}
	example := map[string]any{"id": "paid", "name": "Paid", "request": request}
	for _, s := range []*jsonschema.Schema{followupRESTSchema(t, "ResponseRuleCommand"), followupMCPSchema(t, responseRuleCommandSchema())} {
		for _, kind := range []string{"add_example", "update_example"} {
			if err := s.Validate(map[string]any{"type": kind, "example": example}); err != nil {
				t.Error(err)
			}
		}
		if err := s.Validate(map[string]any{"type": "remove_example", "exampleId": "paid"}); err != nil {
			t.Error(err)
		}
		if err := s.Validate(map[string]any{"type": "remove_example", "example": example}); err == nil {
			t.Error("remove accepted example")
		}
	}
}

func TestResponseRuleFollowupSimulationSelectionSchemas(t *testing.T) {
	t.Parallel()
	rest := followupRESTSchema(t, "SimulateResponseRuleRequest")
	mcp := followupMCPSchema(t, responseRuleInputSchema("simulate_response_rule"))
	for _, tt := range []struct {
		name    string
		payload map[string]any
		valid   bool
	}{
		{"example", map[string]any{"exampleId": "paid"}, true},
		{"request", map[string]any{"request": map[string]any{"query": []any{}, "headers": []any{}}}, true},
		{"both", map[string]any{"exampleId": "paid", "request": map[string]any{"query": []any{}, "headers": []any{}}}, false},
		{"neither", map[string]any{}, false}, {"null", map[string]any{"exampleId": nil}, false}, {"empty", map[string]any{"exampleId": ""}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			if err := rest.Validate(tt.payload); (err == nil) != tt.valid {
				t.Errorf("REST: %v", err)
			}
			input := map[string]any{"designId": 1, "ruleId": "rule"}
			for k, v := range tt.payload {
				input[k] = v
			}
			if err := mcp.Validate(input); (err == nil) != tt.valid {
				t.Errorf("MCP: %v", err)
			}
		})
	}
}
