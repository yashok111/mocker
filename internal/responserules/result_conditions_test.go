package responserules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
)

func TestResultConditionRawWireAcceptance(t *testing.T) {
	const payload = `{"id":"condition","type":"condition","name":"Paid","x":0,"y":0,"resultCondition":{"source":{"source":"result","nodeId":"read","pointer":"/status"},"op":"equals","valueJSON":"\"paid\""}}`
	var node Node
	if err := jsonx.Unmarshal([]byte(payload), &node); err != nil {
		t.Fatal(err)
	}
}

func resultConditionWire(t *testing.T, pointer, op string, expected *string) string {
	t.Helper()
	condition := map[string]any{"source": map[string]any{"source": "result", "nodeId": "a", "pointer": pointer}, "op": op}
	if expected != nil {
		condition["valueJSON"] = *expected
	}
	encoded, err := jsonx.Marshal(condition)
	if err != nil {
		t.Fatal(err)
	}
	return string(encoded)
}

func resultConditionRule(t *testing.T, pointer, op string, expected *string) Rule {
	t.Helper()
	return entityRuleWire(t, `{"id":"a","type":"entity_read","name":"Read","x":0,"y":0,"entity":{"family":"/orders","operation":"get","key":{"source":"literal","valueJSON":"1"}}},{"id":"c","type":"condition","name":"Check","x":0,"y":0,"resultCondition":`+resultConditionWire(t, pointer, op, expected)+`},{"id":"yes","type":"response","name":"Yes","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[]}},{"id":"no","type":"response","name":"No","x":0,"y":0,"response":{"status":409,"mediaType":"application/json","headers":[]}},{"id":"missing","type":"response","name":"Missing","x":0,"y":0,"response":{"status":404,"mediaType":"application/json","headers":[]}}`, `{"id":"read-found","from":"a","port":"found","to":"c"},{"id":"read-missing","from":"a","port":"missing","to":"missing"},{"id":"check-true","from":"c","port":"true","to":"yes"},{"id":"check-false","from":"c","port":"false","to":"no"}`)
}

func resultConditionFixture(data string) Request {
	q := fixture()
	q.Entities = []EntityFixture{{Family: "/orders", IDField: "id", IDType: "integer", Rows: []EntityFixtureRow{{Key: "1", Scope: []string{}, DataJSON: data}}}}
	return q
}

func TestResultConditionSimulationExactValuesAndPresence(t *testing.T) {
	for _, tt := range []struct {
		name, data, pointer, op string
		expected                *string
		matched, present        bool
		actual                  string
	}{
		{"string", `{"status":"paid"}`, "/status", "equals", new(`"paid"`), true, true, `"paid"`},
		{"not equals", `{"n":2}`, "/n", "not_equals", new("1"), true, true, "2"},
		{"large integers", `{"n":9007199254740993}`, "/n", "equals", new("9007199254740992"), false, true, "9007199254740993"},
		{"decimal value", `{"n":1.00}`, "/n", "equals", new("1e0"), true, true, "1.00"},
		{"signed zero", `{"n":-0}`, "/n", "equals", new("0"), true, true, "-0"},
		{"close fractions", `{"n":0.1000000000000000001}`, "/n", "equals", new("0.1000000000000000002"), false, true, "0.1000000000000000001"},
		{"huge exponent", `{"n":1e999999999999999999999999}`, "/n", "equals", new("10e999999999999999999999998"), true, true, "1e999999999999999999999999"},
		{"small exponent", `{"n":1e-1000000000000000000000000}`, "/n", "equals", new("10e-1000000000000000000000001"), true, true, "1e-1000000000000000000000000"},
		{"null", `{"n":null}`, "/n", "equals", new("null"), true, true, "null"},
		{"null versus number", `{"n":null}`, "/n", "equals", new("0"), false, true, "null"},
		{"scalar versus null", `{"n":false}`, "/n", "not_equals", new("null"), true, true, "false"},
		{"exists null", `{"n":null}`, "/n", "exists", nil, true, true, "null"},
		{"exists false", `{"n":false}`, "/n", "exists", nil, true, true, "false"},
		{"exists zero", `{"n":0}`, "/n", "exists", nil, true, true, "0"},
		{"exists empty string", `{"n":""}`, "/n", "exists", nil, true, true, `""`},
		{"exists object", `{"n":{}}`, "/n", "exists", nil, true, true, "{}"},
		{"exists array", `{"n":[]}`, "/n", "exists", nil, true, true, "[]"},
		{"absent exists", `{}`, "/n", "exists", nil, false, false, ""},
		{"absent not exists", `{}`, "/n", "not_exists", nil, true, false, ""},
		{"present not exists", `{"n":null}`, "/n", "not_exists", nil, false, true, "null"},
		{"escaped nested array", `{"a/b":{"~key":[7]}}`, "/a~1b/~0key/0", "equals", new("7"), true, true, "7"},
		{"noncanonical array index", `{"n":[7]}`, "/n/00", "not_exists", nil, true, false, ""},
		{"array out of bounds", `{"n":[7]}`, "/n/1", "not_exists", nil, true, false, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			r := resultConditionRule(t, tt.pointer, tt.op, tt.expected)
			s := run(t, r, resultConditionFixture(tt.data))
			terminal, edge := "no", "check-false"
			if tt.matched {
				terminal, edge = "yes", "check-true"
			}
			if !s.Valid || s.TerminalNodeID != terminal {
				t.Fatalf("simulation=%+v", s)
			}
			step := s.Trace[2]
			if step.Matched == nil || *step.Matched != tt.matched || step.EdgeID != edge {
				t.Fatalf("step=%+v", step)
			}
			raw, err := jsonx.Marshal(step)
			if err != nil {
				t.Fatal(err)
			}
			var trace struct {
				ResultCondition struct {
					SourceNodeID, Pointer, Op string
					Present                   bool
					ActualJSON, ExpectedJSON  *string
				}
			}
			if err := jsonx.Unmarshal(raw, &trace); err != nil {
				t.Fatal(err)
			}
			c := trace.ResultCondition
			if c.SourceNodeID != "a" || c.Pointer != tt.pointer || c.Op != tt.op || c.Present != tt.present {
				t.Fatalf("trace=%s", raw)
			}
			if tt.present && (c.ActualJSON == nil || *c.ActualJSON != tt.actual) || !tt.present && c.ActualJSON != nil {
				t.Fatalf("actual trace=%s", raw)
			}
			if tt.expected != nil && (c.ExpectedJSON == nil || *c.ExpectedJSON != *tt.expected) || tt.expected == nil && c.ExpectedJSON != nil {
				t.Fatalf("expected trace=%s", raw)
			}
		})
	}
}

func TestResultConditionComparisonErrorsNameNodeAndPointer(t *testing.T) {
	for _, tt := range []struct{ name, data, expected string }{
		{"missing", `{}`, "1"},
		{"string number", `{"n":"1"}`, "1"},
		{"number boolean", `{"n":1}`, "true"},
		{"boolean string", `{"n":true}`, `"true"`},
		{"object", `{"n":{}}`, "null"},
		{"array", `{"n":[]}`, "null"},
	} {
		for _, op := range []string{"equals", "not_equals"} {
			t.Run(tt.name+"/"+op, func(t *testing.T) {
				r := resultConditionRule(t, "/n", op, new(tt.expected))
				_, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument(), resultConditionFixture(tt.data))
				field, ok := errors.AsType[*FieldError](err)
				if !ok || !strings.HasPrefix(field.Pointer, "/nodes/2/resultCondition") || !strings.Contains(field.Message, "/n") {
					t.Fatalf("err=%v", err)
				}
			})
		}
	}
}

func TestResultConditionStrictWire(t *testing.T) {
	source := `{"source":"result","nodeId":"a","pointer":"/n"}`
	for _, bad := range []string{
		`null`, `{}`, `{"source":null,"op":"exists"}`,
		`{"source":` + source + `,"op":"gt","valueJSON":"1"}`,
		`{"source":` + source + `,"op":"equals"}`,
		`{"source":` + source + `,"op":"equals","valueJSON":null}`,
		`{"source":` + source + `,"op":"equals","valueJSON":"{}"}`,
		`{"source":` + source + `,"op":"equals","valueJSON":"[]"}`,
		`{"source":` + source + `,"op":"equals","valueJSON":"1 2"}`,
		`{"source":` + source + `,"op":"exists","valueJSON":"null"}`,
		`{"source":` + source + `,"op":"not_exists","valueJSON":null}`,
		`{"source":` + source + `,"op":"exists","extra":1}`,
		`{"source":{"source":"query","name":"n"},"op":"exists"}`,
		`{"source":{"source":"result","nodeId":"a","name":"n"},"op":"exists"}`,
		`{"source":{"source":"result","nodeId":"a","valueJSON":"1"},"op":"exists"}`,
		`{"source":{"source":"result","nodeId":"a","pointer":"/bad~2"},"op":"exists"}`,
	} {
		var n Node
		if err := jsonx.Unmarshal([]byte(`{"id":"c","type":"condition","name":"Check","x":0,"y":0,"resultCondition":`+bad+`}`), &n); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
	result := `"resultCondition":{"source":` + source + `,"op":"exists"}`
	for _, bad := range []string{
		`{"id":"c","type":"condition","name":"Check","x":0,"y":0}`,
		`{"id":"c","type":"condition","name":"Check","x":0,"y":0,"condition":{"in":"query","name":"n","op":"exists"},` + result + `}`,
		`{"id":"c","type":"condition","name":"Check","x":0,"y":0,"condition":null,` + result + `}`,
		`{"id":"c","type":"start","name":"Check","x":0,"y":0,` + result + `}`,
		`{"id":"c","type":"response","name":"Check","x":0,"y":0,"response":{"status":200,"mediaType":"application/json","headers":[]},` + result + `}`,
	} {
		var n Node
		if err := jsonx.Unmarshal([]byte(bad), &n); err == nil {
			t.Fatalf("accepted %s", bad)
		}
	}
}

func TestResultConditionSourceDominance(t *testing.T) {
	for _, op := range []string{"equals", "exists", "not_exists"} {
		expected := new("1")
		if op != "equals" {
			expected = nil
		}
		for _, tt := range []struct {
			name, producer, diagnostic string
			bypass                     bool
		}{
			{"self", "c", "invalid_result_reference", false},
			{"future", "yes", "invalid_result_reference", false},
			{"absent", "absent", "invalid_result_reference", false},
			{"missing exit", "a", "missing_result_reference", true},
		} {
			t.Run(op+"/"+tt.name, func(t *testing.T) {
				r := resultConditionRule(t, "/n", op, expected)
				r.Nodes[2].ResultCondition.Source.NodeID = tt.producer
				if tt.bypass {
					r.Edges[2].To = "c"
				}
				v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
				if err != nil || v.Valid || !slices.ContainsFunc(v.Diagnostics, func(d Diagnostic) bool {
					return d.Code == tt.diagnostic && d.NodeID == "c" && strings.HasSuffix(d.Pointer, "/resultCondition/source")
				}) {
					t.Fatalf("validation=%+v err=%v", v, err)
				}
			})
		}
	}
}

func TestResultConditionCompiledSimulationParityAndCancellation(t *testing.T) {
	r := resultConditionRule(t, "/n", "equals", new("1e0"))
	q := resultConditionFixture(`{"n":1.00}`)
	simulated := run(t, r, q)
	root := rootDocument()
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Rules: []Rule{r}}
	programs, err := CompileExecution(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	input, _, err := normalizeRequest(t.Context(), q)
	if err != nil {
		t.Fatal(err)
	}
	host, err := newFixtureEntities(t.Context(), q, r.Binding)
	if err != nil {
		t.Fatal(err)
	}
	program := programs["GET%20%2Forders"]
	live, err := program.EvaluateWithEntities(t.Context(), EvaluationInput{Request: input}, EvaluationOptions{Entities: host})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := jsonx.Marshal(simulated.Trace)
	b, _ := jsonx.Marshal(live.Trace)
	if string(a) != string(b) || live.TerminalNodeID != simulated.TerminalNodeID {
		t.Fatalf("simulation=%s compiled=%s", a, b)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := program.EvaluateWithEntities(ctx, EvaluationInput{}, EvaluationOptions{Entities: host}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
}

func TestResultConditionRoundtripPreservesLiteral(t *testing.T) {
	raw := resultConditionWire(t, "", "equals", new("  9007199254740993e+0000 \n"))
	var c ResultCondition
	if err := jsonx.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonx.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	var restored ResultCondition
	if err := jsonx.Unmarshal(encoded, &restored); err != nil {
		t.Fatal(err)
	}
	if restored.Source != c.Source || restored.ValueJSON == nil || *restored.ValueJSON != "  9007199254740993e+0000 \n" {
		t.Fatalf("restored=%s", encoded)
	}
	for _, literal := range []string{`"` + strings.Repeat("a", MaxBodyBytes) + `"`, strings.Repeat("[", MaxBodyDepth+1) + "0" + strings.Repeat("]", MaxBodyDepth+1)} {
		var bounded ResultCondition
		if err := jsonx.Unmarshal([]byte(resultConditionWire(t, "/n", "equals", new(literal))), &bounded); err == nil {
			t.Fatal("accepted oversized/deep literal")
		}
	}
}

func TestResultConditionWholeResultAndMissingProducer(t *testing.T) {
	for _, op := range []string{"equals", "not_equals", "exists", "not_exists"} {
		c := ResultCondition{Source: ValueRef{Source: "result", NodeID: "a"}, Op: op}
		if op == "equals" || op == "not_equals" {
			c.ValueJSON = new("1")
		}
		if _, _, err := evaluateResultCondition(t.Context(), c, map[string]any{}); err == nil || !strings.Contains(err.Error(), "a") {
			t.Fatalf("missing producer err=%v", err)
		}
		matched, trace, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": jsonx.Number("1.00")})
		if err != nil || trace.ActualJSON == nil || *trace.ActualJSON != "1.00" || trace.Pointer != "" || matched != (op == "equals" || op == "exists") {
			t.Fatalf("op=%s matched=%v trace=%+v err=%v", op, matched, trace, err)
		}
	}
	r := resultConditionRule(t, "", "exists", nil)
	s := run(t, r, resultConditionFixture(`{"n":null}`))
	if s.TerminalNodeID != "yes" || s.Trace[2].ResultCondition.ActualJSON == nil || *s.Trace[2].ResultCondition.ActualJSON != `{"id":1,"n":null}` {
		t.Fatalf("simulation=%+v", s)
	}
	r = resultConditionRule(t, "", "equals", new("null"))
	if _, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument(), resultConditionFixture(`{}`)); err == nil {
		t.Fatal("compared entire object")
	}
}

func TestResultConditionDominanceUsesEdgesAndRequiresFoundAfterUpdate(t *testing.T) {
	r := resultConditionRule(t, "/n", "equals", new("1"))
	slices.Reverse(r.Nodes)
	s := run(t, r, resultConditionFixture(`{"n":1}`))
	if !s.Valid || s.TerminalNodeID != "yes" {
		t.Fatalf("reordered=%+v", s)
	}
	r = resultConditionRule(t, "/n", "exists", nil)
	r.Nodes = append(r.Nodes, Node{ID: "b", Type: "condition", Name: "Bypass", Condition: &overrides.Condition{In: "query", Name: "b", Op: "exists"}})
	r.Edges[0].To = "b"
	r.Edges = append(r.Edges, Edge{ID: "b-true", From: "b", Port: "true", To: "a"}, Edge{ID: "b-false", From: "b", Port: "false", To: "c"})
	v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
	if err != nil || v.Valid || !slices.ContainsFunc(v.Diagnostics, func(d Diagnostic) bool { return d.Code == "invalid_result_reference" && d.NodeID == "c" }) {
		t.Fatalf("bypass=%+v err=%v", v, err)
	}
	r = resultConditionRule(t, "/n", "exists", nil)
	r.Nodes[1].Type = "entity_update"
	r.Nodes[1].Entity.Operation = ""
	r.Nodes[1].Entity.Data = &ValueRef{Source: "literal", ValueJSON: new("{}")}
	r.Edges[2].To = "c"
	v, err = Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
	if err != nil || v.Valid || !slices.ContainsFunc(v.Diagnostics, func(d Diagnostic) bool { return d.Code == "missing_result_reference" && d.NodeID == "c" }) {
		t.Fatalf("update missing=%+v err=%v", v, err)
	}
}

func TestResultConditionListPointersAndTraceBudget(t *testing.T) {
	r := resultConditionRule(t, "/0/a~1b/~0n", "equals", new("7"))
	r.Nodes[1].Entity.Operation = "list"
	r.Nodes[1].Entity.Key = nil
	r.Nodes = slices.DeleteFunc(r.Nodes, func(n Node) bool { return n.ID == "missing" })
	r.Edges = slices.DeleteFunc(r.Edges, func(e Edge) bool { return e.ID == "read-missing" })
	r.Edges[1].Port = "next"
	s := run(t, r, resultConditionFixture(`{"a/b":{"~n":7}}`))
	if !s.Valid || s.TerminalNodeID != "yes" {
		t.Fatalf("list=%+v", s)
	}
	r.Nodes[2].ResultCondition = &ResultCondition{Source: ValueRef{Source: "result", NodeID: "a"}, Op: "exists"}
	for i := range 5 {
		id := fmt.Sprintf("check%d", i)
		previous := "c"
		if i > 0 {
			previous = fmt.Sprintf("check%d", i-1)
		}
		for j := range r.Edges {
			if r.Edges[j].From == previous && r.Edges[j].Port == "true" {
				r.Edges[j].To = id
			}
		}
		r.Nodes = append(r.Nodes, Node{ID: id, Type: "condition", Name: "Exists", ResultCondition: &ResultCondition{Source: ValueRef{Source: "result", NodeID: "a"}, Op: "exists"}})
		r.Edges = append(r.Edges, Edge{ID: id + "-true", From: id, Port: "true", To: "yes"}, Edge{ID: id + "-false", From: id, Port: "false", To: "no"})
	}
	q := resultConditionFixture(`{"text":"` + strings.Repeat("a", 32000) + `"}`)
	q.Entities[0].Rows = append(q.Entities[0].Rows, EntityFixtureRow{Key: "2", Scope: []string{}, DataJSON: `{"text":"` + strings.Repeat("b", 32000) + `"}`}, EntityFixtureRow{Key: "3", Scope: []string{}, DataJSON: `{"text":"` + strings.Repeat("c", 32000) + `"}`})
	if _, err := Simulate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument(), q); err == nil || !strings.Contains(err.Error(), "512") {
		t.Fatalf("trace budget err=%v", err)
	}
}

// Counting Err calls makes cancellation during decoding deterministic without
// relying on a timer racing a fast scalar comparison.
type resultConditionCancelContext struct {
	context.Context
	calls int
}

func (c *resultConditionCancelContext) Err() error {
	c.calls++
	if c.calls >= 3 {
		return context.Canceled
	}
	return nil
}
func TestResultConditionCancellationDuringLiteralDecode(t *testing.T) {
	ctx := &resultConditionCancelContext{Context: t.Context()}
	c := ResultCondition{Source: ValueRef{Source: "result", NodeID: "a", Pointer: "/n"}, Op: "equals", ValueJSON: new("1")}
	if _, _, err := evaluateResultCondition(ctx, c, map[string]any{"a": map[string]any{"n": jsonx.Number("1")}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation=%v", err)
	}
}

func TestResultConditionProgrammaticShapeAndEntityWrites(t *testing.T) {
	for _, kind := range []string{"start", "delay", "response", "fallback", "entity_read", "entity_create", "entity_update"} {
		r := resultConditionRule(t, "/n", "exists", nil)
		r.Nodes[2].Type = kind
		if err := CheckStructure(r); err == nil {
			t.Fatalf("accepted result predicate on %s", kind)
		}
	}
	r := resultConditionRule(t, "/n", "exists", nil)
	r.Nodes[2].Condition = &overrides.Condition{In: "query", Name: "n", Op: "exists"}
	if err := CheckStructure(r); err == nil {
		t.Fatal("accepted both in-memory condition payloads")
	}
	r = resultConditionRule(t, "/n", "equals", new("7"))
	r.Nodes[1].Type = "entity_create"
	r.Nodes[1].Entity = &EntityOperation{Family: "/orders", Data: &ValueRef{Source: "literal", ValueJSON: new(`{"n":7}`)}}
	r.Nodes = slices.DeleteFunc(r.Nodes, func(n Node) bool { return n.ID == "missing" })
	r.Edges = slices.DeleteFunc(r.Edges, func(e Edge) bool { return e.ID == "read-missing" })
	r.Edges[1].Port = "next"
	q := resultConditionFixture(`{"n":0}`)
	q.Entities[0].Rows = []EntityFixtureRow{}
	s := run(t, r, q)
	if !s.Valid || s.TerminalNodeID != "yes" || len(s.Entities[0].Rows) != 1 {
		t.Fatalf("create result=%+v", s)
	}
	r.Nodes[2].ResultCondition.ValueJSON = new(`"7"`)
	host, err := newFixtureEntities(t.Context(), q, r.Binding)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newProgram(r).EvaluateWithEntities(t.Context(), EvaluationInput{}, EvaluationOptions{Entities: host}); err == nil {
		t.Fatal("accepted mismatched scalar after write")
	}
	if len(host.families[0].Rows) != 1 || host.families[0].Rows[0].DataJSON != `{"id":1,"n":7}` {
		t.Fatalf("earlier write rolled back: %+v", host.families)
	}
}

func TestResultConditionMissingEntityUsesMissingPort(t *testing.T) {
	r := resultConditionRule(t, "/n", "not_exists", nil)
	q := resultConditionFixture(`{}`)
	q.Entities[0].Rows = []EntityFixtureRow{}
	s := run(t, r, q)
	if !s.Valid || s.TerminalNodeID != "missing" || len(s.Trace) != 3 || s.Trace[1].EntityFound == nil || *s.Trace[1].EntityFound {
		t.Fatalf("missing entity=%+v", s)
	}
}
