package responserules

import (
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func exampleRuleJSON(t *testing.T, examples string) []byte {
	t.Helper()
	raw, err := jsonx.Marshal(baseRule())
	if err != nil {
		t.Fatal(err)
	}
	return []byte(strings.TrimSuffix(string(raw), "}") + `,"examples":` + examples + `}`)
}

func TestExamplesPreserveAuthoredRequestText(t *testing.T) {
	body := " {\"n\":9007199254740993,\"exponent\":1e10000} \n"
	row := " {\"amount\":9007199254740993,\"exp\":1e10000} "
	request := Request{
		Query:    []Field{{Name: "q", Value: "first"}, {Name: "q", Value: "second"}},
		Headers:  []Field{{Name: "X-Token", Value: "first"}, {Name: "x-token", Value: "second"}},
		BodyJSON: new(body), Path: []Field{{Name: "id", Value: "1"}},
		Entities: []EntityFixture{{Family: "/orders", IDField: "id", IDType: "integer", Rows: []EntityFixtureRow{{Key: "1", Scope: []string{}, DataJSON: row}}}},
	}
	examples, err := jsonx.Marshal([]any{map[string]any{"id": "case_1", "name": "Large numbers", "request": request}})
	if err != nil {
		t.Fatal(err)
	}
	var rule Rule
	if err := jsonx.Unmarshal(exampleRuleJSON(t, string(examples)), &rule); err != nil {
		t.Fatalf("saved example rejected: %v", err)
	}
	raw, err := jsonx.Marshal(rule)
	if err != nil {
		t.Fatal(err)
	}
	var output struct {
		Examples []struct {
			ID      string  `json:"id"`
			Request Request `json:"request"`
		} `json:"examples"`
	}
	if err := jsonx.Unmarshal(raw, &output); err != nil {
		t.Fatal(err)
	}
	if len(output.Examples) != 1 || output.Examples[0].ID != "case_1" {
		t.Fatalf("example lost: %s", raw)
	}
	got := output.Examples[0].Request
	if got.BodyJSON == nil || *got.BodyJSON != body || got.Entities[0].Rows[0].DataJSON != row {
		t.Fatalf("authored JSON normalized: %+v", got)
	}
	if got.Query[0].Value != "first" || got.Query[1].Value != "second" || got.Headers[1].Name != "x-token" {
		t.Fatalf("ordered duplicate fields changed: %+v", got)
	}
	// Intrinsic fixture validation must not make incomplete graph authoring fail.
	rule.Nodes, rule.Edges = []Node{}, []Edge{}
	if err := CheckStructure(rule); err != nil {
		t.Fatalf("incomplete graph blocked by examples: %v", err)
	}
}

func TestExamplesStrictAdmission(t *testing.T) {
	valid := `{"id":"case_1","name":"Example","request":{"query":[],"headers":[]}}`
	for _, tc := range []struct{ name, examples, pointer string }{
		{"null list", `null`, "/examples"},
		{"null item", `[null]`, "/examples/0"},
		{"missing request", `[{"id":"case_1","name":"Example"}]`, "/examples/0/request"},
		{"extra field", `["invalid"]`, "/examples/0"},
		{"unknown example field", `[` + strings.Replace(valid, `"name":"Example"`, `"name":"Example","extra":1`, 1) + `]`, "/examples/0/extra"},
		{"invalid id", `[` + strings.Replace(valid, "case_1", "../case", 1) + `]`, "/examples/0/id"},
		{"duplicate id", `[` + valid + `,` + valid + `]`, "/examples/1/id"},
		{"blank name", `[` + strings.Replace(valid, "Example", " ", 1) + `]`, "/examples/0/name"},
		{"UTF-8 name bytes", `[` + strings.Replace(valid, "Example", strings.Repeat("я", 101), 1) + `]`, "/examples/0/name"},
		{"null name", `[` + strings.Replace(valid, `"name":"Example"`, `"name":null`, 1) + `]`, "/examples/0/name"},
		{"empty parameter", `[` + strings.Replace(valid, `"query":[]`, `"query":[{"name":"","value":"x"}]`, 1) + `]`, "/examples/0/request/query/0"},
		{"duplicate path", `[` + strings.Replace(valid, `"headers":[]`, `"headers":[],"path":[{"name":"id","value":"1"},{"name":"id","value":"2"}]`, 1) + `]`, "/examples/0/request/path/1/name"},
		{"invalid body", `[` + strings.Replace(valid, `"headers":[]`, `"headers":[],"bodyJSON":"{\"x\":1,\"x\":2}"`, 1) + `]`, "/examples/0/request/bodyJSON"},
		{"non-object fixture row", `[` + strings.Replace(valid, `"headers":[]`, `"headers":[],"entities":[{"family":"/orders","idField":"id","idType":"integer","rows":[{"key":"1","scope":[],"dataJSON":"null"}]}]`, 1) + `]`, "/examples/0/request/entities/0/rows/0/dataJSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var rule Rule
			err := jsonx.Unmarshal(exampleRuleJSON(t, tc.examples), &rule)
			field, ok := errors.AsType[*FieldError](err)
			if !ok || !strings.HasPrefix(field.Pointer, tc.pointer) {
				t.Fatalf("error=%v, want pointer %s", err, tc.pointer)
			}
		})
	}
	for _, examples := range []string{`[]`, `[` + valid + `,` + strings.Replace(valid, "case_1", "case_2", 1) + `]`} {
		var rule Rule
		if err := jsonx.Unmarshal(exampleRuleJSON(t, examples), &rule); err != nil {
			t.Fatalf("empty examples or duplicate names rejected: %v", err)
		}
	}
}

func TestExamplesCountAndAggregateLimits(t *testing.T) {
	examples := []any{}
	for i := range 21 {
		examples = append(examples, map[string]any{"id": fmt.Sprintf("case_%d", i), "name": "Case", "request": Request{Query: []Field{}, Headers: []Field{}}})
	}
	raw, _ := jsonx.Marshal(examples)
	var rule Rule
	if err := jsonx.Unmarshal(exampleRuleJSON(t, string(raw)), &rule); err == nil {
		t.Fatal("21 examples accepted")
	}
	raw, _ = jsonx.Marshal(examples[:20])
	if err := jsonx.Unmarshal(exampleRuleJSON(t, string(raw)), &rule); err != nil {
		t.Fatalf("20 bounded examples rejected: %v", err)
	}
	fields := []Field{}
	for range 25 {
		fields = append(fields, Field{Name: "large", Value: strings.Repeat("a", 4096)})
	}
	examples = []any{}
	for i := range 3 {
		examples = append(examples, map[string]any{"id": fmt.Sprintf("case_%d", i), "name": "Case", "request": Request{Query: fields, Headers: []Field{}}})
	}
	raw, _ = jsonx.Marshal(examples)
	if err := jsonx.Unmarshal(exampleRuleJSON(t, string(raw)), &rule); err == nil {
		t.Fatal("aggregate examples above 256 KiB accepted")
	}
	raw, _ = jsonx.Marshal(examples[:2])
	if err := jsonx.Unmarshal(exampleRuleJSON(t, string(raw)), &rule); err != nil {
		t.Fatalf("two bounded requests rejected: %v", err)
	}
	for range 10 {
		fields = append(fields, Field{Name: "large", Value: strings.Repeat("a", 4096)})
	}
	raw, _ = jsonx.Marshal([]any{map[string]any{"id": "large", "name": "Case", "request": Request{Query: fields, Headers: []Field{}}}})
	err := jsonx.Unmarshal(exampleRuleJSON(t, string(raw)), &rule)
	field, ok := errors.AsType[*FieldError](err)
	if !ok || field.Pointer != "/examples/0/request" {
		t.Fatalf("per-request 128 KiB limit not enforced: %v", err)
	}
}

func TestExampleCommandsAtomicAndPreserveGraph(t *testing.T) {
	base := baseRule()
	before, _ := jsonx.Marshal(base)
	var commands []Command
	if err := jsonx.Unmarshal([]byte(`[{"type":"add_example","example":{"id":"first","name":"First","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993"}}},{"type":"add_example","example":{"id":"second","name":"Second","request":{"query":[],"headers":[]}}},{"type":"update_example","example":{"id":"first","name":"Renamed","request":{"query":[],"headers":[],"bodyJSON":"1e10000"}}},{"type":"remove_example","exampleId":"second"},{"type":"set_rule","name":"Graph edit"}]`), &commands); err != nil {
		t.Fatalf("example commands rejected: %v", err)
	}
	result, err := ApplyCommands(base, commands)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := jsonx.Marshal(result)
	if !strings.Contains(string(raw), `"name":"Renamed"`) || !strings.Contains(string(raw), `"bodyJSON":"1e10000"`) || strings.Contains(string(raw), `"id":"second"`) || result.Name != "Graph edit" || len(result.Nodes) != 2 {
		t.Fatalf("case commands lost graph or case: %s", raw)
	}
	for _, batch := range []string{
		`[{"type":"remove_example","exampleId":"first"},{"type":"remove_example","exampleId":"missing"}]`,
		`[{"type":"add_example","example":{"id":"first","name":"Duplicate","request":{"query":[],"headers":[]}}}]`,
		`[{"type":"update_example","example":{"id":"missing","name":"Missing","request":{"query":[],"headers":[]}}}]`,
	} {
		if err := jsonx.Unmarshal([]byte(batch), &commands); err != nil {
			t.Fatal(err)
		}
		if _, err := ApplyCommands(result, commands); err == nil {
			t.Fatalf("invalid batch accepted: %s", batch)
		}
		after, _ := jsonx.Marshal(result)
		if string(after) != string(raw) {
			t.Fatal("failed example batch modified original")
		}
	}
	after, _ := jsonx.Marshal(base)
	if string(before) != string(after) {
		t.Fatal("example commands modified original graph")
	}
}

func TestExecutionRejectsAuthoringSimulationExamples(t *testing.T) {
	for _, examples := range []string{`[]`, `[{"id":"one","name":"Authoring case","request":{"query":[],"headers":[]}}]`} {
		raw := exampleRuleJSON(t, examples)
		envelope := jsonx.RawMessage(`{"formatVersion":1,"rules":[` + string(raw) + `]}`)
		_, err := DecodeExecution(map[string]any{ExecutionExtension: envelope})
		field, ok := errors.AsType[*FieldError](err)
		if !ok || field.Pointer != "/"+ExecutionExtension+"/rules/0/examples" {
			t.Fatalf("execution accepted authoring fixtures: %v", err)
		}
	}
}

func TestSavedExampleSurvivesGraphInvalidation(t *testing.T) {
	var rule Rule
	if err := jsonx.Unmarshal(exampleRuleJSON(t, `[{"id":"one","name":"Saved case","request":{"query":[],"headers":[],"bodyJSON":"9007199254740993"}}]`), &rule); err != nil {
		t.Fatal(err)
	}
	changed, err := ApplyCommands(rule, []Command{{Type: "remove_edge", EdgeID: "e"}})
	if err != nil {
		t.Fatalf("rule edit rejected retained example: %v", err)
	}
	raw, _ := jsonx.Marshal(changed)
	if !strings.Contains(string(raw), `"bodyJSON":"9007199254740993"`) {
		t.Fatal("graph edit dropped named example")
	}
	result, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{changed}}, changed.ID, rootDocument(), changed.Examples[0].Request)
	if err != nil || result.Valid || result.Outcome != "invalid" || len(result.Diagnostics) == 0 {
		t.Fatalf("invalidated case not reported clearly: %+v %v", result, err)
	}
}
