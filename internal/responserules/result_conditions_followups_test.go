package responserules

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const followupSource = `{"source":"result","nodeId":"a","pointer":"/n"}`

func followupCondition(t *testing.T, raw string) ResultCondition {
	t.Helper()
	var c ResultCondition
	if err := jsonx.Unmarshal([]byte(raw), &c); err != nil {
		t.Fatalf("decode predicate: %v", err)
	}
	return c
}

func followupNumber(t *testing.T, op, expected string) ResultCondition {
	t.Helper()
	raw, err := jsonx.Marshal(map[string]any{"source": map[string]any{"source": "result", "nodeId": "a", "pointer": "/n"}, "op": op, "valueJSON": expected})
	if err != nil {
		t.Fatal(err)
	}
	return followupCondition(t, string(raw))
}

func TestResultConditionFollowupsExactOrdering(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, a, b string
		comparison int
	}{
		{"large neighbors", "9007199254740993", "9007199254740992", 1},
		{"close fractions", "0.1000000000000000001", "0.1000000000000000002", -1},
		{"equivalent forms", "1.00", "1e0", 0},
		{"negative forms", "-1.20", "-1.1999999999999999999", -1},
		{"signed zero", "-0e999999999999999999999999", "0", 0},
		{"tiny versus zero", "1e-1000000000000000000000000", "0", 1},
		{"negative versus zero", "-1e-1000000000000000000000000", "-0", -1},
		{"huge exponent", "1e999999999999999999999999", "9e999999999999999999999998", 1},
		{"huge equivalent", "1e1000000000000000000000000", "10e999999999999999999999999", 0},
		{"same magnitude padding", "12e9", "123e8", -1},
		{"exponent padding", "1E+000000000000000000000003", "1000", 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			for _, op := range []string{"greater_than", "greater_or_equal", "less_than", "less_or_equal"} {
				c := followupNumber(t, op, tc.b)
				matched, trace, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{"n": jsonx.Number(tc.a)}})
				want := map[string]bool{"greater_than": tc.comparison > 0, "greater_or_equal": tc.comparison >= 0, "less_than": tc.comparison < 0, "less_or_equal": tc.comparison <= 0}[op]
				if err != nil || matched != want || trace == nil || trace.ActualJSON == nil || *trace.ActualJSON != tc.a {
					t.Fatalf("%s %s %s: matched=%v trace=%+v err=%v", tc.a, op, tc.b, matched, trace, err)
				}
			}
		})
	}
}

func TestResultConditionFollowupsResultRHS(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, op, left, right string
		want                  bool
	}{
		{"exact numbers", "equals", "9007199254740993", "9007199254740993.0", true},
		{"ordering", "greater_than", "9007199254740993", "9007199254740992", true},
		{"strings", "not_equals", `"paid"`, `"draft"`, true},
		{"null", "equals", "null", "null", true},
		{"null and scalar", "equals", "null", "false", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := followupCondition(t, `{"source":`+followupSource+`,"op":"`+tc.op+`","valueFrom":{"source":"result","nodeId":"b","pointer":"/expected"}}`)
			left, err := resultConditionScalar(t.Context(), tc.left)
			if err != nil {
				t.Fatal(err)
			}
			right, err := resultConditionScalar(t.Context(), tc.right)
			if err != nil {
				t.Fatal(err)
			}
			matched, trace, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{"n": left}, "b": map[string]any{"expected": right}})
			if err != nil || matched != tc.want || trace == nil || trace.ExpectedJSON == nil || *trace.ExpectedJSON != tc.right {
				t.Fatalf("matched=%v trace=%+v err=%v", matched, trace, err)
			}
			raw, err := jsonx.Marshal(trace)
			if err != nil || !strings.Contains(string(raw), `"valueFrom":{"source":"result","nodeId":"b","pointer":"/expected"}`) || !strings.Contains(string(raw), fmt.Sprintf(`"matched":%t`, tc.want)) {
				t.Fatalf("trace=%s err=%v", raw, err)
			}
		})
	}
}

func TestResultConditionFollowupsShortCircuitAndTrace(t *testing.T) {
	t.Parallel()
	missing := `{"source":` + followupSource + `,"op":"greater_than","valueJSON":"0"}`
	presence := `{"source":` + followupSource + `,"op":"exists"}`
	absence := `{"source":` + followupSource + `,"op":"not_exists"}`
	for _, tc := range []struct {
		name, raw, expected string
		want                bool
	}{
		{"AND guard", `{"all":[` + presence + `,` + missing + `]}`, `{"op":"all","matched":false,"children":[{"sourceNodeId":"a","pointer":"/n","op":"exists","present":false,"matched":false}],"shortCircuited":true}`, false},
		{"OR guard", `{"any":[` + absence + `,` + missing + `]}`, `{"op":"any","matched":true,"children":[{"sourceNodeId":"a","pointer":"/n","op":"not_exists","present":false,"matched":true}],"shortCircuited":true}`, true},
		{"nested", `{"all":[` + absence + `,{"any":[` + absence + `,` + missing + `]}]}`, `{"op":"all","matched":true,"children":[{"sourceNodeId":"a","pointer":"/n","op":"not_exists","present":false,"matched":true},{"op":"any","matched":true,"children":[{"sourceNodeId":"a","pointer":"/n","op":"not_exists","present":false,"matched":true}],"shortCircuited":true}]}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			c := followupCondition(t, tc.raw)
			matched, trace, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{}})
			if err != nil || matched != tc.want {
				t.Fatalf("matched=%v err=%v", matched, err)
			}
			raw, err := jsonx.Marshal(trace)
			if err != nil || string(raw) != tc.expected {
				t.Fatalf("trace=%s err=%v; want %s", raw, err, tc.expected)
			}
		})
	}
	for _, raw := range []string{`{"all":[` + missing + `,` + presence + `]}`, `{"any":[` + presence + `,` + missing + `]}`} {
		c := followupCondition(t, raw)
		_, _, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{}})
		field, ok := errors.AsType[*FieldError](err)
		if !ok || !strings.Contains(field.Pointer, "/source/pointer") || !strings.Contains(field.Pointer, "/1/") && !strings.Contains(field.Pointer, "/0/") {
			t.Fatalf("nested error=%v", err)
		}
	}
}

func TestResultConditionFollowupsStrictUnion(t *testing.T) {
	t.Parallel()
	leaf := `{"source":` + followupSource + `,"op":"exists"}`
	for _, raw := range []string{
		`{"source":` + followupSource + `,"op":"exists","op":"not_exists"}`,
		`{"source":{"source":"result","nodeId":"a","nodeId":"b"},"op":"exists"}`,
		`{"source":` + followupSource + `,"op":"equals","valueJSON":"1","valueFrom":` + followupSource + `}`,
		`{"source":` + followupSource + `,"op":"exists","valueFrom":` + followupSource + `}`,
		`{"source":` + followupSource + `,"op":"equals","valueFrom":{"source":"query","name":"n"}}`,
		`{"source":` + followupSource + `,"op":"greater_than","valueJSON":"null"}`,
		`{"source":` + followupSource + `,"op":"less_than","valueJSON":"\"1\""}`,
		`{"source":` + followupSource + `,"op":"less_or_equal","valueJSON":"true"}`,
		`{"all":[]}`, `{"all":[` + leaf + `]}`, `{"all":null}`, `{"all":[null,` + leaf + `]}`,
		`{"all":[` + leaf + `,` + leaf + `],"source":` + followupSource + `}`, `{"all":[` + leaf + `,` + leaf + `],"any":[` + leaf + `,` + leaf + `]}`,
	} {
		var c ResultCondition
		if err := jsonx.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	for _, depth := range []int{3, 4} {
		raw := leaf
		for range depth {
			raw = `{"all":[` + leaf + `,` + raw + `]}`
		}
		var c ResultCondition
		err := jsonx.Unmarshal([]byte(raw), &c)
		if (err == nil) != (depth == 3) {
			t.Fatalf("group depth=%d err=%v", depth, err)
		}
	}
	for _, count := range []int{16, 17} {
		raw := `{"all":[` + strings.TrimSuffix(strings.Repeat(leaf+",", count), ",") + `]}`
		var c ResultCondition
		err := jsonx.Unmarshal([]byte(raw), &c)
		if (err == nil) != (count == 16) {
			t.Fatalf("leaf count=%d err=%v", count, err)
		}
	}
}

func TestResultConditionFollowupsRHSRuntimeErrors(t *testing.T) {
	t.Parallel()
	c := followupCondition(t, `{"source":`+followupSource+`,"op":"greater_than","valueFrom":{"source":"result","nodeId":"b","pointer":"/n"}}`)
	for _, tc := range []struct {
		name string
		b    any
	}{
		{"missing field", map[string]any{}},
		{"null", map[string]any{"n": nil}},
		{"string", map[string]any{"n": "1"}},
		{"object", map[string]any{"n": map[string]any{}}},
		{"array", map[string]any{"n": []any{}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, _, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{"n": jsonx.Number("1")}, "b": tc.b})
			field, ok := errors.AsType[*FieldError](err)
			if !ok || !strings.HasPrefix(field.Pointer, "/valueFrom/") || !strings.Contains(field.Message, "/n") {
				t.Fatalf("err=%v", err)
			}
		})
	}
	_, _, err := evaluateResultCondition(t.Context(), c, map[string]any{"a": map[string]any{"n": jsonx.Number("1")}})
	field, ok := errors.AsType[*FieldError](err)
	if !ok || field.Pointer != "/valueFrom/nodeId" {
		t.Fatalf("missing producer=%v", err)
	}
}

func TestResultConditionFollowupsAllReferencesRequireDominance(t *testing.T) {
	t.Parallel()
	for _, right := range []bool{false, true} {
		r := resultConditionRule(t, "/n", "exists", nil)
		bad := `{"source":` + followupSource + `,"op":"equals","valueFrom":{"source":"result","nodeId":"future","pointer":"/n"}}`
		if !right {
			bad = `{"source":{"source":"result","nodeId":"future"},"op":"exists"}`
		}
		r.Nodes[2].ResultCondition = new(followupCondition(t, `{"any":[{"source":`+followupSource+`,"op":"exists"},`+bad+`]}`))
		v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
		if err != nil || v.Valid || !slices.ContainsFunc(v.Diagnostics, func(d Diagnostic) bool {
			return d.Code == "invalid_result_reference" && strings.Contains(d.Pointer, "/resultCondition/any/1/")
		}) {
			t.Fatalf("validation=%+v err=%v", v, err)
		}
	}
}

func TestResultConditionFollowupsCancellation(t *testing.T) {
	t.Parallel()
	c := followupCondition(t, `{"all":[{"source":`+followupSource+`,"op":"exists"},{"source":`+followupSource+`,"op":"greater_than","valueJSON":"0"}]}`)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, _, err := evaluateResultCondition(ctx, c, map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("err=%v", err)
	}
	midway := &followupCancelContext{Context: t.Context()}
	if _, _, err := evaluateResultCondition(midway, c, map[string]any{"a": map[string]any{"n": jsonx.Number("1")}}); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-group cancellation=%v", err)
	}
}

// Count context checkpoints so cancellation occurs between group children,
// without relying on a timer racing an exact-number comparison.
type followupCancelContext struct {
	context.Context
	calls int
}

func (c *followupCancelContext) Err() error {
	c.calls++
	if c.calls >= 5 {
		return context.Canceled
	}
	return nil
}

func TestResultConditionFollowupsTypedLimits(t *testing.T) {
	t.Parallel()
	leaf := ResultCondition{Source: ValueRef{Source: "result", NodeID: "a", Pointer: "/n"}, Op: "exists"}
	for _, c := range []ResultCondition{
		{All: []ResultCondition{}},
		{All: []ResultCondition{leaf}},
		{All: []ResultCondition{leaf, leaf}, Any: []ResultCondition{leaf, leaf}},
		{All: []ResultCondition{leaf, leaf}, Source: leaf.Source},
		{All: []ResultCondition{leaf, leaf}, Op: "exists"},
		{All: []ResultCondition{leaf, leaf}, ValueJSON: new("1")},
		{All: []ResultCondition{leaf, leaf}, ValueFrom: new(leaf.Source)},
		{All: slices.Repeat([]ResultCondition{leaf}, 17)},
		{All: []ResultCondition{{All: slices.Repeat([]ResultCondition{leaf}, 9)}, {Any: slices.Repeat([]ResultCondition{leaf}, 8)}}},
	} {
		if err := checkResultCondition(t.Context(), c); err == nil {
			t.Fatalf("accepted typed predicate=%+v", c)
		}
	}
	deep := leaf
	for range 4 {
		deep = ResultCondition{Any: []ResultCondition{leaf, deep}}
	}
	if err := checkResultCondition(t.Context(), deep); err == nil {
		t.Fatal("accepted deep typed predicate")
	}
	cyclic := ResultCondition{All: make([]ResultCondition, 2)}
	cyclic.All[0], cyclic.All[1] = leaf, cyclic
	if err := checkResultCondition(t.Context(), cyclic); err == nil {
		t.Fatal("accepted cyclic typed predicate")
	}
	encoded, err := jsonx.Marshal(ResultCondition{All: []ResultCondition{leaf, leaf}})
	if err != nil || strings.Contains(string(encoded), `"source":{}`) || strings.Contains(string(encoded), `"op":""`) {
		t.Fatalf("group roundtrip=%s err=%v", encoded, err)
	}
	var restored ResultCondition
	if err := jsonx.Unmarshal(encoded, &restored); err != nil || checkResultCondition(t.Context(), restored) != nil {
		t.Fatalf("restore err=%v", err)
	}
}

func TestResultConditionFollowupsTraceBudgetStopsBeforeDelay(t *testing.T) {
	t.Parallel()
	r := resultConditionRule(t, "/text", "exists", nil)
	previous := "c"
	for i := range 14 {
		id := fmt.Sprintf("budget%d", i)
		for j := range r.Edges {
			if r.Edges[j].From == previous && r.Edges[j].Port == "true" {
				r.Edges[j].To = id
			}
		}
		r.Nodes = append(r.Nodes, Node{ID: id, Type: "condition", Name: "Budget", ResultCondition: r.Nodes[2].ResultCondition})
		r.Edges = append(r.Edges, Edge{ID: id + "-true", From: id, Port: "true", To: "wait"}, Edge{ID: id + "-false", From: id, Port: "false", To: "no"})
		previous = id
	}
	r.Nodes = append(r.Nodes, Node{ID: "wait", Type: "delay", Name: "Delay", DelayMs: new(1)})
	r.Edges = append(r.Edges, Edge{ID: "done", From: "wait", Port: "next", To: "yes"})
	q := resultConditionFixture(`{"text":"` + strings.Repeat("x", 40000) + `"}`)
	host, err := newFixtureEntities(t.Context(), q, r.Binding)
	if err != nil {
		t.Fatal(err)
	}
	delayed := false
	_, err = newProgram(r).EvaluateWithEntities(t.Context(), EvaluationInput{}, EvaluationOptions{Entities: host, Delay: func(context.Context, int) error { delayed = true; return nil }})
	if err == nil || !strings.Contains(err.Error(), "512") || delayed {
		t.Fatalf("trace budget err=%v delayed=%v", err, delayed)
	}
}

func TestResultConditionFollowupsRHSFoundDominanceAndParity(t *testing.T) {
	t.Parallel()
	r := resultConditionRule(t, "/n", "exists", nil)
	other := r.Nodes[1]
	other.ID = "b"
	r.Nodes = append(r.Nodes, other)
	r.Edges[1].To = "b"
	r.Edges = append(r.Edges, Edge{ID: "b-found", From: "b", Port: "found", To: "c"}, Edge{ID: "b-missing", From: "b", Port: "missing", To: "missing"})
	r.Nodes[2].ResultCondition = new(followupCondition(t, `{"all":[{"source":`+followupSource+`,"op":"exists"},{"source":`+followupSource+`,"op":"greater_than","valueFrom":{"source":"result","nodeId":"b","pointer":"/limit"}}]}`))
	q := resultConditionFixture(`{"n":9007199254740993,"limit":9007199254740992}`)
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
	live, err := programs["GET%20%2Forders"].EvaluateWithEntities(t.Context(), EvaluationInput{Request: input}, EvaluationOptions{Entities: host})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := jsonx.Marshal(simulated.Trace)
	b, _ := jsonx.Marshal(live.Trace)
	if string(a) != string(b) || live.TerminalNodeID != "yes" {
		t.Fatalf("simulation=%s compiled=%s", a, b)
	}
	for i := range r.Edges {
		if r.Edges[i].ID == "b-missing" {
			r.Edges[i].To = "c"
		}
	}
	v, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument())
	if err != nil || v.Valid || !slices.ContainsFunc(v.Diagnostics, func(d Diagnostic) bool {
		return d.Code == "missing_result_reference" && strings.HasSuffix(d.Pointer, "/all/1/valueFrom")
	}) {
		t.Fatalf("RHS found dominance=%+v err=%v", v, err)
	}
}
