package responserules

import (
	"context"
	"errors"
	"net/http"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

func executionRoot(rules ...Rule) map[string]any {
	root := rootDocument()
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Rules: rules}
	return root
}

func TestCompileExecutionAdmission(t *testing.T) {
	if programs, err := CompileExecution(t.Context(), rootDocument()); err != nil || len(programs) != 0 {
		t.Fatalf("absent extension: %v %v", programs, err)
	}
	for _, tc := range []struct {
		name string
		edit func(map[string]any)
	}{
		{"unknown version", func(root map[string]any) {
			root[ExecutionExtension] = map[string]any{"formatVersion": 2, "rules": []any{}}
		}},
		{"null", func(root map[string]any) { root[ExecutionExtension] = nil }},
		{"unknown field", func(root map[string]any) {
			root[ExecutionExtension] = map[string]any{"formatVersion": 1, "rules": []any{}, "enabled": true}
		}},
		{"invalid graph", func(root map[string]any) {
			r := baseRule()
			r.Edges = []Edge{}
			root[ExecutionExtension] = Envelope{FormatVersion: 1, Rules: []Rule{r}}
		}},
		{"duplicate binding", func(root map[string]any) {
			a, b := baseRule(), baseRule()
			b.ID = "other"
			root[ExecutionExtension] = Envelope{FormatVersion: 1, Rules: []Rule{a, b}}
		}},
		{"head", func(root map[string]any) {
			r := baseRule()
			r.Binding.Method = "HEAD"
			root[ExecutionExtension] = Envelope{FormatVersion: 1, Rules: []Rule{r}}
		}},
		{"missing operation", func(root map[string]any) { delete(root, "paths") }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := executionRoot(baseRule())
			tc.edit(root)
			programs, err := CompileExecution(t.Context(), root)
			field, ok := errors.AsType[*FieldError](err)
			if !ok || !strings.HasPrefix(field.Pointer, "/"+ExecutionExtension) || programs != nil {
				t.Fatalf("partial programs or wrong field error: %v %v", programs, err)
			}
		})
	}
}

func TestExecutionOwnsSnapshotAndResults(t *testing.T) {
	rule := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	root := executionRoot(rule)
	root[Extension] = Envelope{FormatVersion: 1, Rules: []Rule{{ID: "unfinished", Name: "", Nodes: []Node{}, Edges: []Edge{}}}}
	programs, err := CompileExecution(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	program := programs[overrides.OpKey("GET", "/orders")]
	if program == nil || program.ID() != rule.ID {
		t.Fatal("missing compiled operation")
	}
	*rule.Nodes[2].Response.BodyJSON = `{"changed":true}`
	rule.Nodes[1].Condition.Name = "changed"
	input := overrides.Input{Query: url.Values{"x": {"yes"}}}
	first, err := program.Evaluate(t.Context(), input)
	if err != nil || first.Response == nil || *first.Response.BodyJSON != `{"path":"yes"}` {
		t.Fatalf("snapshot changed: %+v %v", first, err)
	}
	*first.Response.BodyJSON = `{"caller":true}`
	first.Response.Headers = append(first.Response.Headers, Field{Name: "X-Test", Value: "changed"})
	second, err := program.Evaluate(t.Context(), input)
	if err != nil || *second.Response.BodyJSON != `{"path":"yes"}` || len(second.Response.Headers) != 0 {
		t.Fatalf("result aliases program: %+v %v", second, err)
	}
}

func TestCompiledAndSimulatedPathsAgree(t *testing.T) {
	for _, value := range []string{"1", "1.0", "1e0", "9007199254740993"} {
		t.Run(value, func(t *testing.T) {
			rule := conditionRule(overrides.Condition{In: "body", Name: "x", Op: "equals", Value: value})
			rule.Nodes[2].Response.Status = 200
			rule.Nodes[2].Response.BodyJSON = new(`{"n":1.0,"big":9007199254740993}`)
			programs, err := CompileExecution(t.Context(), executionRoot(rule))
			if err != nil {
				t.Fatal(err)
			}
			for _, body := range []string{`{"x":` + value + `}`, `{"x":false}`, `null`} {
				input, rejected, err := LiveInput(t.Context(), url.Values{}, http.Header{}, []byte(body), true)
				if err != nil || rejected {
					t.Fatalf("input: %v %v", rejected, err)
				}
				live, err := programs[overrides.OpKey("GET", "/orders")].Evaluate(t.Context(), input)
				if err != nil {
					t.Fatal(err)
				}
				sim := run(t, rule, Request{Query: []Field{}, Headers: []Field{}, BodyJSON: &body})
				sim.InputHash = ""
				if !reflect.DeepEqual(live, sim) {
					t.Fatalf("live %+v differs from simulation %+v", live, sim)
				}
			}
		})
	}
}

func TestLiveInputBoundsAndHTTPFields(t *testing.T) {
	query := url.Values{"x": {"first", "second"}}
	header := http.Header{"X-Test": {"\t" + strings.Repeat("a", 5000), "second"}}
	for _, body := range []string{"", "{", `{"x":1,"x":2}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), strings.Repeat(" ", MaxBodyBytes+1)} {
		input, rejected, err := LiveInput(t.Context(), query, header, []byte(body), true)
		if err != nil || !rejected || input.BodyOK || input.Body != nil {
			t.Fatalf("invalid body available: %+v %v %v", input, rejected, err)
		}
		if !(overrides.Condition{In: "query", Name: "x", Op: "equals", Value: "second"}).Match(input) || input.Header.Get("x-test") != header.Get("x-test") {
			t.Fatal("live fields were filtered")
		}
	}
	for _, available := range []bool{false, true} {
		input, rejected, err := LiveInput(t.Context(), query, header, []byte("null"), available)
		if err != nil || rejected || input.BodyOK != available || input.Body != nil {
			t.Fatalf("null availability: %+v %v %v", input, rejected, err)
		}
	}
}

func TestExecutionCancellation(t *testing.T) {
	programs, err := CompileExecution(t.Context(), executionRoot(baseRule()))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CompileExecution(ctx, executionRoot(baseRule())); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := programs[overrides.OpKey("GET", "/orders")].Evaluate(ctx, overrides.Input{}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := LiveInput(ctx, nil, nil, nil, false); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
