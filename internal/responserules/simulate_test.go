package responserules

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

func ptr[T any](v T) *T { return &v }
func rootDocument() map[string]any {
	return map[string]any{"paths": map[string]any{"/orders": map[string]any{"get": map[string]any{"responses": map[string]any{"200": map[string]any{}, "4XX": map[string]any{}}}, "head": map[string]any{}}}}
}
func conditionRule(c overrides.Condition) Rule {
	r := baseRule()
	r.Nodes = []Node{{ID: "s", Type: "start", Name: "Start"}, {ID: "c", Type: "condition", Name: "Condition", Condition: &c}, {ID: "yes", Type: "response", Name: "Yes", Response: &Response{Status: 401, MediaType: "application/json", Headers: []Field{}, BodyJSON: ptr(`{"path":"yes"}`)}}, {ID: "no", Type: "response", Name: "No", Response: &Response{Status: 200, MediaType: "application/json", Headers: []Field{}, BodyJSON: ptr(`{"path":"no"}`)}}}
	r.Edges = []Edge{{ID: "s-c", From: "s", Port: "next", To: "c"}, {ID: "yes", From: "c", Port: "true", To: "yes"}, {ID: "no", From: "c", Port: "false", To: "no"}}
	return r
}
func fixture() Request { return Request{Query: []Field{}, Headers: []Field{}} }
func run(t *testing.T, r Rule, q Request) Simulation {
	t.Helper()
	s, err := Simulate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{r}}, r.ID, rootDocument(), q)
	if err != nil {
		t.Fatal(err)
	}
	return s
}
func TestSimulationPredicates(t *testing.T) {
	cases := []struct {
		name, in, op, value string
		req                 Request
		matched             bool
	}{
		{"query repeated", "query", "equals", "two", Request{Query: []Field{{Name: "x", Value: "one"}, {Name: "x", Value: "two"}}, Headers: []Field{}}, true},
		{"query empty exists", "query", "exists", "", Request{Query: []Field{{Name: "x", Value: ""}}, Headers: []Field{}}, true},
		{"header ordered case", "header", "equals", "second", Request{Query: []Field{}, Headers: []Field{{Name: "x", Value: "first"}, {Name: "X", Value: "second"}}}, false},
		{"header empty exists", "header", "exists", "", Request{Query: []Field{}, Headers: []Field{{Name: "x", Value: ""}, {Name: "X", Value: "second"}}}, false},
		{"large integer", "body", "equals", "9007199254740993", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`{"x":9007199254740993}`)}, true},
		{"decimal spelling", "body", "equals", "1", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`{"x":1.0}`)}, false},
		{"exponent spelling", "body", "equals", "1e0", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`{"x":1e0}`)}, true},
		{"null exists", "body", "exists", "", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`{"x":null}`)}, true},
		{"object equals", "body", "equals", "object", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`{"x":{}}`)}, false},
		{"array body", "body", "exists", "", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`[]`)}, false},
		{"scalar body", "body", "exists", "", Request{Query: []Field{}, Headers: []Field{}, BodyJSON: ptr(`1`)}, false},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			r := conditionRule(overrides.Condition{In: tt.in, Name: "x", Op: tt.op, Value: tt.value})
			s := run(t, r, tt.req)
			if !s.Valid || len(s.Trace) != 3 || s.Trace[1].Matched == nil || *s.Trace[1].Matched != tt.matched {
				t.Fatalf("%+v", s)
			}
			want := "no"
			if tt.matched {
				want = "yes"
			}
			if s.TerminalNodeID != want {
				t.Fatalf("selected %s", s.TerminalNodeID)
			}
		})
	}
}
func TestSimulationBodyErrors(t *testing.T) {
	for _, body := range []string{"", " ", `{} {}`, `{"x":1,"x":2}`, `{"x":{"a":1,"a":2}}`, strings.Repeat("[", 65) + "0" + strings.Repeat("]", 65), strings.Repeat(" ", MaxBodyBytes+1)} {
		q := fixture()
		q.BodyJSON = &body
		_, err := Simulate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", rootDocument(), q)
		if err == nil {
			t.Fatalf("accepted body %q", body[:min(80, len(body))])
		}
	}
}
func TestDelayAndFallback(t *testing.T) {
	r := baseRule()
	r.Nodes = append(r.Nodes, Node{ID: "d", Type: "delay", Name: "Delay", DelayMs: ptr(30000)})
	r.Edges = []Edge{{ID: "a", From: "s", Port: "next", To: "d"}, {ID: "b", From: "d", Port: "next", To: "f"}}
	s := run(t, r, fixture())
	if !s.Valid || s.Outcome != "fallback" || s.Response != nil || s.TotalDelayMs == nil || *s.TotalDelayMs != 30000 {
		t.Fatalf("%+v", s)
	}
}
func TestSameStatusUsesEdges(t *testing.T) {
	r := conditionRule(overrides.Condition{In: "query", Name: "x", Op: "exists"})
	r.Nodes[2].Response.Status = 200
	s := run(t, r, fixture())
	if s.Response == nil || *s.Response.BodyJSON != `{"path":"no"}` {
		t.Fatalf("%+v", s)
	}
}
func TestCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := Simulate(ctx, Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", rootDocument(), fixture())
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("%v", err)
	}
}
func TestRequestWire(t *testing.T) {
	for _, raw := range []string{`{}`, `{"query":null,"headers":[]}`, `{"query":[],"headers":[],"bodyJSON":null}`, `{"query":[],"headers":[],"other":true}`} {
		var r Request
		if json.Unmarshal([]byte(raw), &r) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestFixtureBoundsAndDepth64(t *testing.T) {
	q := fixture()
	q.BodyJSON = ptr(strings.Repeat("[", 64) + "0" + strings.Repeat("]", 64))
	if !run(t, baseRule(), q).Valid {
		t.Fatal("depth 64 rejected")
	}
	for _, edit := range []func(*Request){func(q *Request) { q.Headers = []Field{{Name: "Bad Name", Value: "x"}} }, func(q *Request) { q.Query = []Field{{Name: "", Value: "x"}} }, func(q *Request) { q.Headers = make([]Field, 101) }, func(q *Request) { q.Query = make([]Field, 101) }} {
		q = fixture()
		edit(&q)
		if _, err := Simulate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", rootDocument(), q); err == nil {
			t.Fatal("invalid fixture accepted")
		}
	}
	q = fixture()
	for i := 0; i < 100; i++ {
		q.Query = append(q.Query, Field{Name: fmt.Sprint("x", i), Value: strings.Repeat("a", 1400)})
	}
	if _, err := Simulate(context.Background(), Envelope{FormatVersion: 1, Rules: []Rule{baseRule()}}, "r", rootDocument(), q); err == nil {
		t.Fatal("oversized fixture accepted")
	}
}
