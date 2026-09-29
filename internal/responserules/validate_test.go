package responserules

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

func TestValidationGraph(t *testing.T) {
	cases := []struct {
		name, code string
		edit       func(*Rule)
	}{
		{"missing start", "missing_start", func(r *Rule) { r.Nodes[0].Type = "fallback"; r.Edges = []Edge{} }},
		{"multiple starts", "multiple_starts", func(r *Rule) { r.Nodes[1].Type = "start" }},
		{"binding", "invalid_binding", func(r *Rule) { r.Binding = nil }},
		{"dangling", "dangling_edge", func(r *Rule) { r.Edges[0].To = "missing" }},
		{"port", "invalid_port", func(r *Rule) { r.Edges[0].Port = "true" }},
		{"missing exit", "missing_exit", func(r *Rule) { r.Edges = []Edge{} }},
		{"duplicate exit", "duplicate_exit", func(r *Rule) { r.Edges = append(r.Edges, Edge{ID: "e2", From: "s", Port: "next", To: "f"}) }},
		{"terminal exit", "invalid_port", func(r *Rule) { r.Edges = append(r.Edges, Edge{ID: "e2", From: "f", Port: "next", To: "s"}) }},
		{"disconnected cycle", "cycle", func(r *Rule) {
			r.Nodes = append(r.Nodes, Node{ID: "d", Type: "delay", Name: "Delay", DelayMs: ptr(0)})
			r.Edges = append(r.Edges, Edge{ID: "e2", From: "d", Port: "next", To: "d"})
		}},
		{"unreachable", "unreachable_node", func(r *Rule) { r.Nodes = append(r.Nodes, Node{ID: "extra", Type: "fallback", Name: "Extra"}) }},
		{"condition", "invalid_condition", func(r *Rule) {
			r.Nodes[0].Type = "condition"
			r.Nodes[0].Condition = &overrides.Condition{In: "bogus", Name: "", Op: "equals"}
		}},
		{"delay", "delay_limit", func(r *Rule) { r.Nodes[0].Type = "delay"; r.Nodes[0].DelayMs = ptr(30001) }},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := baseRule()
			tt.edit(&r)
			s := run(t, r, fixture())
			if s.Valid || s.Outcome != "invalid" || len(s.Trace) != 0 || s.TotalDelayMs != nil || s.Response != nil {
				t.Fatalf("%+v", s)
			}
			found := false
			for _, d := range s.Diagnostics {
				if d.Code == tt.code {
					found = true
					if !strings.HasPrefix(d.Pointer, "/"+Extension+"/rules/0") {
						t.Fatalf("bad pointer %+v", d)
					}
				}
			}
			if !found {
				t.Fatalf("missing %s: %+v", tt.code, s.Diagnostics)
			}
		})
	}
}
func TestValidationSelectedAndStable(t *testing.T) {
	r := baseRule()
	other := Rule{ID: "other", Name: "", Nodes: []Node{}, Edges: []Edge{}}
	env := Envelope{FormatVersion: 1, Rules: []Rule{other, r}}
	v, err := Validate(context.Background(), env, "r", rootDocument())
	if err != nil || !v.Valid {
		t.Fatalf("%+v %v", v, err)
	}
	env.Rules[0].Binding = r.Binding
	v, err = Validate(context.Background(), env, "r", rootDocument())
	if err != nil || v.Valid || v.Diagnostics[0].Code != "duplicate_binding" {
		t.Fatalf("%+v %v", v, err)
	}
	for range 5 {
		again, _ := Validate(context.Background(), env, "r", rootDocument())
		if !reflect.DeepEqual(v, again) {
			t.Fatal("unstable diagnostics")
		}
	}
}
func TestValidationDelayAllPaths(t *testing.T) {
	r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	r.Nodes = append(r.Nodes, Node{ID: "d1", Type: "delay", Name: "D1", DelayMs: ptr(20000)}, Node{ID: "d2", Type: "delay", Name: "D2", DelayMs: ptr(10001)})
	r.Edges[1].To = "d1"
	r.Edges = append(r.Edges, Edge{ID: "d1-d2", From: "d1", Port: "next", To: "d2"}, Edge{ID: "d2-yes", From: "d2", Port: "next", To: "yes"})
	s := run(t, r, fixture())
	if s.Valid {
		t.Fatal("accepted unselected over-limit path")
	}
}
func TestValidationResponseAndReferences(t *testing.T) {
	for _, status := range []int{199, 600, 204, 205, 304} {
		r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
		r.Nodes[2].Response.Status = status
		s := run(t, r, fixture())
		if s.Valid {
			t.Fatalf("accepted status/body %d", status)
		}
	}
	r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	r.Binding.Method = "HEAD"
	s := run(t, r, fixture())
	if s.Valid {
		t.Fatal("HEAD body accepted")
	}
	root := rootDocument()
	root["paths"].(map[string]any)["/orders"].(map[string]any)["$ref"] = "#/components/pathItems/X"
	v, err := Validate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", root)
	if err != nil || !v.Valid {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestValidationInheritedOperationBinding(t *testing.T) {
	root := rootDocument()
	paths := root["paths"].(map[string]any)
	shared := paths["/orders"].(map[string]any)
	root["components"] = map[string]any{"pathItems": map[string]any{"Shared": shared}}
	paths["/orders"] = map[string]any{"$ref": "#/components/pathItems/Shared"}
	paths["/archive"] = map[string]any{"$ref": "#/components/pathItems/Shared"}
	for _, path := range []string{"/orders", "/archive"} {
		rule := baseRule()
		rule.Binding.Path = path
		result, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{rule}}, rule.ID, root)
		if err != nil || !result.Valid {
			t.Errorf("binding %s: %+v, %v", path, result, err)
		}
	}
	paths["/archive"] = map[string]any{"$ref": "#/components/pathItems/Missing", "get": shared["get"]}
	rule := baseRule()
	rule.Binding.Path = "/archive"
	result, err := Validate(t.Context(), Envelope{FormatVersion: 1, Rules: []Rule{rule}}, rule.ID, root)
	if err != nil || !result.Valid {
		t.Fatalf("valid local sibling beside missing ref: %+v, %v", result, err)
	}
}

func TestValidationEnvelopeAndDiagnosticBounds(t *testing.T) {
	env := Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}
	for i := 0; i < 19; i++ {
		r := baseRule()
		r.ID = fmt.Sprint("other", i)
		r.Binding = nil
		r.Nodes[1] = Node{ID: "f", Type: "response", Name: "Response", Response: &Response{Status: 200, MediaType: "application/json", Headers: []Field{}, BodyJSON: ptr(`"` + strings.Repeat("a", 60000) + `"`)}}
		env.Rules = append(env.Rules, r)
	}
	if _, err := Validate(context.Background(), env, "r", rootDocument()); err == nil {
		t.Fatal("accepted oversized entire envelope")
	}
	r := baseRule()
	r.Nodes = []Node{}
	r.Edges = []Edge{}
	for i := 0; i < 100; i++ {
		r.Nodes = append(r.Nodes, Node{ID: fmt.Sprint("n", i), Type: "condition", Name: "", Condition: &overrides.Condition{In: "query", Name: "", Op: "equals"}})
	}
	v, err := Validate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, "r", rootDocument())
	if err != nil || v.Valid || !v.DiagnosticsTruncated || len(v.Diagnostics) != MaxDiagnostics {
		t.Fatalf("%+v %v", v, err)
	}
}
func TestBothConditionPortsCanConverge(t *testing.T) {
	r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	r.Nodes = r.Nodes[:3]
	r.Edges[2].To = "yes"
	s := run(t, r, fixture())
	if !s.Valid || s.TerminalNodeID != "yes" {
		t.Fatalf("%+v", s)
	}
}
func TestWarningsDoNotBlockSimulation(t *testing.T) {
	r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	r.Nodes[3].Response.Status = 503
	s := run(t, r, fixture())
	if !s.Valid || s.Response == nil || s.Response.Status != 503 || len(s.Diagnostics) != 1 || s.Diagnostics[0].Code != "undocumented_status" {
		t.Fatalf("%+v", s)
	}
}
