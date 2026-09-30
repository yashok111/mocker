package statediagram

import (
	"strings"
	"testing"
)

func orderDiagram() Diagram {
	return Diagram{ID: "order", Name: "Order", InitialStateID: "created", States: []State{{ID: "created", Name: "Created"}, {ID: "paid", Name: "Paid", Terminal: true}}, Transitions: []Transition{{ID: "pay", Name: "Pay", From: "created", To: "paid", ResponseStatus: 200, Guard: &Guard{Pointer: "/balance", EqualsJSON: "9007199254740993"}, PatchJSON: `{"paid":true}`}}}
}

func TestSimulationPrecisionAndGuard(t *testing.T) {
	t.Parallel()
	d := orderDiagram()
	got, err := Simulate(d, nil, `{"balance":9007199254740993}`, []string{"pay", "pay"})
	if err != nil {
		t.Fatal(err)
	}
	if got.StateID != "paid" || len(got.Steps) != 2 || !got.Steps[0].Accepted || got.Steps[1].Accepted || !strings.Contains(got.DataJSON, "9007199254740993") {
		t.Fatalf("%+v", got)
	}
	blocked, err := Simulate(d, nil, `{"balance":9007199254740992}`, []string{"pay"})
	if err != nil || blocked.StateID != "created" || blocked.Steps[0].Accepted {
		t.Fatalf("%+v %v", blocked, err)
	}
}

func TestMissingDiffersFromNullAndEscapedPointer(t *testing.T) {
	t.Parallel()
	d := orderDiagram()
	d.Transitions[0].Guard = &Guard{Pointer: "/a~1b/~0value", EqualsJSON: "null"}
	for _, tt := range []struct {
		data     string
		accepted bool
	}{{`{"a/b":{"~value":null}}`, true}, {`{"a/b":{}}`, false}} {
		got, err := Simulate(d, nil, tt.data, []string{"pay"})
		if err != nil || got.Steps[0].Accepted != tt.accepted {
			t.Fatalf("%+v %v", got, err)
		}
	}
}

func TestValidationAndCommands(t *testing.T) {
	t.Parallel()
	d := orderDiagram()
	d.Transitions[0].Binding = &Binding{Method: "post", Path: "/pay"}
	if !HasErrors(Validate(d, map[string]any{})) {
		t.Fatal("missing binding accepted")
	}
	next, err := ApplyCommands(d, []Command{{Kind: "remove_state", ID: "created"}})
	if err != nil || len(next.Transitions) != 0 || next.InitialStateID != "" || len(d.Transitions) != 1 {
		t.Fatalf("%+v %v", next, err)
	}
	d.Transitions[0].Guard.Pointer = "/bad~2escape"
	if err := CheckStructure(d); err == nil {
		t.Fatal("invalid pointer accepted")
	}
}

func TestExactNumericEqualityWithoutExponentExpansion(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		a, b string
		want bool
	}{
		{"1e1000000000", "10e999999999", true}, {"1.00", "1", true}, {"-0", "0", true}, {"9007199254740993", "9007199254740992", false}, {"1e-999999999", "10e-1000000000", true},
	} {
		a, _ := Value(tt.a)
		b, _ := Value(tt.b)
		if equal(a, b) != tt.want {
			t.Errorf("%s == %s", tt.a, tt.b)
		}
	}
}

func TestStateGuardCompositeEqualityKeepsExactKindsAndNumbers(t *testing.T) {
	t.Parallel()
	d := orderDiagram()
	d.Transitions[0].Guard = &Guard{Pointer: "/payload", EqualsJSON: `{"n":1e999999999999999999999999,"items":[null,9007199254740993,true]}`}
	for _, tt := range []struct {
		data     string
		accepted bool
	}{
		{`{"payload":{"items":[null,9007199254740993,true],"n":10e999999999999999999999998}}`, true},
		{`{"payload":{"items":[null,9007199254740992,true],"n":10e999999999999999999999998}}`, false},
		{`{"payload":{"items":[null,"9007199254740993",true],"n":10e999999999999999999999998}}`, false},
		{`{"payload":{"items":[null,9007199254740993,true]}}`, false},
	} {
		got, err := Simulate(d, nil, tt.data, []string{"pay"})
		if err != nil || len(got.Steps) != 1 || got.Steps[0].Accepted != tt.accepted {
			t.Fatalf("data=%s simulation=%+v err=%v", tt.data, got, err)
		}
	}
}

func TestInvalidModelsAndBoundedSimulation(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name string
		edit func(*Diagram)
	}{
		{"dangling", func(d *Diagram) { d.Transitions[0].To = "gone" }},
		{"terminal", func(d *Diagram) { d.States[0].Terminal = true }},
		{"missing initial", func(d *Diagram) { d.InitialStateID = "gone" }},
		{"bad patch", func(d *Diagram) { d.Transitions[0].PatchJSON = "[]" }},
		{"duplicate", func(d *Diagram) { d.States = append(d.States, d.States[0]) }},
	} {
		t.Run(tt.name, func(t *testing.T) {
			d := orderDiagram()
			tt.edit(&d)
			if !HasErrors(Validate(d, nil)) {
				t.Fatal("invalid diagram accepted")
			}
		})
	}
	d := orderDiagram()
	if _, err := Simulate(d, nil, "{}", make([]string, 101)); err == nil {
		t.Fatal("step cap missing")
	}
	if _, err := Simulate(d, nil, "[]", nil); err == nil {
		t.Fatal("non-object seed accepted")
	}
	result, err := Simulate(d, nil, "{}", nil)
	if err != nil || result.Steps == nil {
		t.Fatalf("empty trace %+v %v", result, err)
	}
}
