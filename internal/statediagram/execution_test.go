package statediagram

import (
	"context"
	"errors"
	"maps"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/responserules"
)

func executionFixture(t *testing.T) (map[string]any, Diagram) {
	t.Helper()
	d := decodeConfigured(t)
	d.Transitions[0].Binding = &Binding{Method: "post", Path: "/orders/{orderId}/pay"}
	d.Transitions[0].Guard = &Guard{Pointer: "/amount", EqualsJSON: "9007199254740993"}
	root := map[string]any{"openapi": "3.1.0", "info": map[string]any{"title": "Orders", "version": "1"}, "paths": map[string]any{d.Transitions[0].Binding.Path: map[string]any{"post": map[string]any{"responses": map[string]any{"200": map[string]any{"content": map[string]any{"application/json": map[string]any{"schema": map[string]any{"type": "object"}}}}}}}}, ExecutionExtension: Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}}
	return root, d
}
func executionProgram(t *testing.T, root map[string]any) *Program {
	t.Helper()
	programs, err := CompileExecution(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	p := programs[overrides.OpKey("POST", "/orders/{orderId}/pay")]
	if p == nil {
		t.Fatalf("consumer operation missing: %v", programs)
	}
	return p
}

func TestExecutionSelectOwnsPatchAndPreservesExactData(t *testing.T) {
	t.Parallel()
	root, d := executionFixture(t)
	d.Transitions[0].PatchJSON = `{"receipt":{"amount":9007199254740993},"status":"paid"}`
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	p := executionProgram(t, root)
	d.Entity.StateField = "mutated"
	d.States[1].Value = new("mutated")
	data := map[string]any{"status": "created", "amount": jsonx.Number("9007199254740993")}
	choice, err := p.Select(t.Context(), data)
	if err != nil || choice.TransitionID != "pay" || choice.FromStateID != "start" || choice.ToStateID != "end" || choice.ResponseStatus != 200 || choice.Patch["status"] != "paid" {
		t.Fatalf("choice %+v %v", choice, err)
	}
	if data["status"] != "created" || len(data) != 2 || p.ID() != "order" || p.Entity().StateField != "status" || p.Admission() != (Admission{MediaType: "application/json"}) {
		t.Fatalf("mutated data/program: %+v %+v", data, p)
	}
	next := maps.Clone(data)
	maps.Copy(next, choice.Patch)
	if next["amount"] != jsonx.Number("9007199254740993") {
		t.Fatalf("lost amount: %v", next)
	}
	choice.Patch["receipt"].(map[string]any)["amount"] = "edited"
	again, err := p.Select(t.Context(), data)
	if err != nil || again.Patch["receipt"].(map[string]any)["amount"] != jsonx.Number("9007199254740993") {
		t.Fatalf("patch aliases program: %+v %v", again, err)
	}
}
func TestExecutionStateAndTransitionConflicts(t *testing.T) {
	t.Parallel()
	root, d := executionFixture(t)
	d.States = append(d.States, State{ID: "other", Name: "Other"})
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	p := executionProgram(t, root)
	for _, tt := range []struct {
		name        string
		data        map[string]any
		code, state string
	}{
		{"missing", map[string]any{"amount": jsonx.Number("9007199254740993")}, "", "start"},
		{"null", map[string]any{"status": nil}, "invalid_state", ""},
		{"number", map[string]any{"status": jsonx.Number("1")}, "invalid_state", ""},
		{"unknown", map[string]any{"status": "gone"}, "invalid_state", ""},
		{"terminal", map[string]any{"status": "paid"}, "terminal_state", "end"},
		{"unavailable", map[string]any{"status": "other"}, "transition_unavailable", "other"},
		{"guard", map[string]any{"status": "created", "amount": jsonx.Number("9007199254740992")}, "guard_failed", "start"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before, _ := jsonx.Marshal(tt.data)
			choice, err := p.Select(t.Context(), tt.data)
			after, _ := jsonx.Marshal(tt.data)
			if string(before) != string(after) {
				t.Fatal("mutated input")
			}
			if tt.code == "" {
				if err != nil || choice.FromStateID != tt.state {
					t.Fatalf("%+v %v", choice, err)
				}
				return
			}
			conflict, ok := errors.AsType[*TransitionError](err)
			if !ok || conflict.Code != tt.code || conflict.StateID != tt.state {
				t.Fatalf("%+v %v", conflict, err)
			}
		})
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := p.Select(ctx, map[string]any{}); !errors.Is(err, context.Canceled) {
		t.Fatalf("cancel: %v", err)
	}
}
func TestAppliedExecutionRejectsUnsafeConfiguration(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		edit func(map[string]any, *Diagram)
	}{
		{"missing entity", func(_ map[string]any, d *Diagram) { d.Entity = nil }},
		{"invalid key", func(_ map[string]any, d *Diagram) { d.Entity.KeyParam = "wrong" }},
		{"unrelated family", func(_ map[string]any, d *Diagram) { d.Entity.Family = "/customers" }},
		{"collection", func(root map[string]any, d *Diagram) {
			root["paths"].(map[string]any)["/orders"] = root["paths"].(map[string]any)[d.Transitions[0].Binding.Path]
			d.Transitions[0].Binding.Path = "/orders"
		}},
		{"read method", func(_ map[string]any, d *Diagram) { d.Transitions[0].Binding.Method = "get" }},
		{"informational", func(_ map[string]any, d *Diagram) { d.Transitions[0].ResponseStatus = 199 }},
		{"duplicate values", func(_ map[string]any, d *Diagram) { d.States[1].Value = new("created") }},
		{"wrong state patch", func(_ map[string]any, d *Diagram) { d.Transitions[0].PatchJSON = `{"status":"wrong"}` }},
		{"no bindings", func(_ map[string]any, d *Diagram) { d.Transitions[0].Binding = nil }},
		{"duplicate source", func(_ map[string]any, d *Diagram) {
			tr := d.Transitions[0]
			tr.ID = "duplicate"
			d.Transitions = append(d.Transitions, tr)
		}},
		{"mixed body", func(_ map[string]any, d *Diagram) {
			tr := d.Transitions[0]
			tr.ID = "alternate"
			tr.From = "end"
			tr.To = "start"
			tr.ResponseStatus = 204
			d.States[1].Terminal = false
			d.Transitions = append(d.Transitions, tr)
		}},
		{"missing response", func(root map[string]any, d *Diagram) {
			root["paths"].(map[string]any)[d.Transitions[0].Binding.Path].(map[string]any)["post"].(map[string]any)["responses"] = map[string]any{}
		}},
		{"non json", func(root map[string]any, d *Diagram) {
			root["paths"].(map[string]any)[d.Transitions[0].Binding.Path].(map[string]any)["post"].(map[string]any)["responses"] = map[string]any{"200": map[string]any{"content": map[string]any{"text/plain": map[string]any{}}}}
		}},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, d := executionFixture(t)
			tt.edit(root, &d)
			root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
			if _, err := CompileExecution(t.Context(), root); err == nil {
				t.Fatal("unsafe applied copy accepted")
			}
		})
	}
}
func TestExecutionReferencesAdmissionAndCopyClaims(t *testing.T) {
	t.Parallel()
	root, d := executionFixture(t)
	item := root["paths"].(map[string]any)[d.Transitions[0].Binding.Path]
	root["paths"].(map[string]any)[d.Transitions[0].Binding.Path] = map[string]any{"$ref": "#/components/pathItems/Pay"}
	root["components"] = map[string]any{"pathItems": map[string]any{"Pay": item}, "responses": map[string]any{"Done": map[string]any{"content": map[string]any{"application/json": map[string]any{}}}}}
	item.(map[string]any)["post"].(map[string]any)["responses"] = map[string]any{"2XX": map[string]any{"$ref": "#/components/responses/Done"}}
	root[Extension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{{ID: "incomplete", Name: "Unfinished", States: []State{}, Transitions: []Transition{}}}}
	if p := executionProgram(t, root); p.ID() != "order" {
		t.Fatalf("%+v", p)
	}
	second := d
	second.ID = "other"
	second.Entity = new(*d.Entity)
	second.Entity.StateField = "otherStatus"
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d, second}}
	if _, err := CompileExecution(t.Context(), root); err == nil {
		t.Fatal("cross diagram claim accepted")
	}
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	root[responserules.ExecutionExtension] = responserules.Envelope{FormatVersion: 1, Rules: []responserules.Rule{{ID: "rule", Name: "Rule", Binding: &responserules.Binding{Method: "POST", Path: d.Transitions[0].Binding.Path}, Nodes: []responserules.Node{}, Edges: []responserules.Edge{}}}}
	if _, err := CompileExecution(t.Context(), root); err == nil {
		t.Fatal("response rule claim accepted")
	}
	delete(root, responserules.ExecutionExtension)
	d.Transitions[0].ResponseStatus = 204
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	if p := executionProgram(t, root); !p.Admission().NoBody {
		t.Fatalf("admission %+v", p.Admission())
	}
}
func TestSettingsEntityCommandsAreStrictAndOwned(t *testing.T) {
	t.Parallel()
	d := orderDiagram()
	e := EntityBinding{Family: "/orders", KeyParam: "orderId", StateField: "status"}
	got, err := ApplyCommands(d, []Command{{Kind: "settings", Entity: &e}})
	if err != nil || got.Entity == nil || d.Entity != nil {
		t.Fatalf("%+v %v", got, err)
	}
	e.StateField = "changed"
	if got.Entity.StateField != "status" {
		t.Fatal("entity alias")
	}
	if _, err := ApplyCommands(got, []Command{{Kind: "settings", Entity: &e, ClearEntity: true}}); err == nil {
		t.Fatal("set and clear accepted")
	}
	cleared, err := ApplyCommands(got, []Command{{Kind: "settings", ClearEntity: true}})
	if err != nil || cleared.Entity != nil {
		t.Fatalf("%+v %v", cleared, err)
	}
	for _, raw := range []string{`{"kind":"settings","entity":{"family":"/orders","keyParam":"id","stateField":"status","unknown":true}}`, `{"kind":"settings","unknown":true}`, `{"kind":"settings","entity":null}`} {
		var c Command
		if err := jsonx.Unmarshal([]byte(raw), &c); err == nil {
			t.Fatalf("unknown command accepted: %s", raw)
		}
	}
}

func TestExecutionMultipleSourcesAndScopedFamily(t *testing.T) {
	t.Parallel()
	root, d := executionFixture(t)
	original := d.Transitions[0].Binding.Path
	path := "/accounts/{accountId}/orders/{orderId}/pay"
	d.Entity.Family = "/accounts/{}/orders"
	d.Transitions[0].Binding.Path = path
	d.States[1].Terminal = false
	second := d.Transitions[0]
	second.ID = "refund"
	second.From = "end"
	second.To = "start"
	second.Guard = nil
	d.Transitions = append(d.Transitions, second)
	paths := root["paths"].(map[string]any)
	paths[path] = paths[original]
	delete(paths, original)
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	programs, err := CompileExecution(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	p := programs[overrides.OpKey("POST", path)]
	choice, err := p.Select(t.Context(), map[string]any{"status": "paid"})
	if err != nil || choice.TransitionID != "refund" || choice.Patch["status"] != "created" {
		t.Fatalf("choice %+v %v", choice, err)
	}
	// A parent parameter cannot be selected as this family's detail key.
	d.Entity.KeyParam = "accountId"
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d}}
	if _, err := CompileExecution(t.Context(), root); err == nil {
		t.Fatal("parent parameter admitted as entity key")
	}
}

func TestExecutionResponsePrecedenceAndBoundedRefs(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name                  string
		responses, components map[string]any
		valid                 bool
	}{
		{"default", map[string]any{"default": map[string]any{"content": map[string]any{"application/json": map[string]any{}}}}, nil, true},
		{"exact wins", map[string]any{"200": map[string]any{"content": map[string]any{"text/plain": map[string]any{}}}, "2XX": map[string]any{"content": map[string]any{"application/json": map[string]any{}}}}, nil, false},
		{"missing ref", map[string]any{"200": map[string]any{"$ref": "#/components/responses/Missing"}}, nil, false},
		{"external ref", map[string]any{"200": map[string]any{"$ref": "https://example.invalid/response.json"}}, nil, false},
		{"ref cycle", map[string]any{"200": map[string]any{"$ref": "#/components/responses/A"}}, map[string]any{"responses": map[string]any{"A": map[string]any{"$ref": "#/components/responses/B"}, "B": map[string]any{"$ref": "#/components/responses/A"}}}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			root, d := executionFixture(t)
			root["paths"].(map[string]any)[d.Transitions[0].Binding.Path].(map[string]any)["post"].(map[string]any)["responses"] = tt.responses
			if tt.components != nil {
				root["components"] = tt.components
			}
			_, err := CompileExecution(t.Context(), root)
			if (err == nil) != tt.valid {
				t.Fatalf("valid=%t err=%v", tt.valid, err)
			}
		})
	}
	root, d := executionFixture(t)
	root["paths"].(map[string]any)[d.Transitions[0].Binding.Path] = map[string]any{"$ref": "#/components/pathItems/A"}
	root["components"] = map[string]any{"pathItems": map[string]any{"A": map[string]any{"$ref": "#/components/pathItems/A"}}}
	if _, err := CompileExecution(t.Context(), root); err == nil {
		t.Fatal("Path Item reference cycle accepted")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := CompileExecution(ctx, root); !errors.Is(err, context.Canceled) {
		t.Fatalf("compile cancel %v", err)
	}
}

func TestAppliedCopiesCannotShareFamilyStateField(t *testing.T) {
	t.Parallel()
	root, d := executionFixture(t)
	second := d
	second.ID = "duplicate"
	second.Entity = new(*d.Entity)
	second.Transitions = append([]Transition{}, d.Transitions...)
	second.Transitions[0].Binding = &Binding{Method: "post", Path: "/orders/{orderId}/other"}
	paths := root["paths"].(map[string]any)
	paths[second.Transitions[0].Binding.Path] = paths[d.Transitions[0].Binding.Path]
	root[ExecutionExtension] = Envelope{FormatVersion: 1, Diagrams: []Diagram{d, second}}
	if _, err := CompileExecution(t.Context(), root); err == nil {
		t.Fatal("duplicate family field accepted")
	}
}
