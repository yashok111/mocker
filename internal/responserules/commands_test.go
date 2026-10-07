package responserules

import (
	"encoding/json"
	"testing"

	"github.com/yashok111/mocker/internal/overrides"
)

func TestCommandsAtomic(t *testing.T) {
	r := baseRule()
	before, _ := json.Marshal(r)
	_, err := ApplyCommands(t.Context(), r, []Command{{Type: "remove_node", NodeID: "s"}, {Type: "remove_edge", EdgeID: "missing"}})
	after, _ := json.Marshal(r)
	if err == nil || string(before) != string(after) {
		t.Fatalf("rollback: %v %s", err, after)
	}
	result, err := ApplyCommands(t.Context(), r, []Command{{Type: "remove_node", NodeID: "s"}})
	if err != nil || len(result.Nodes) != 1 || len(result.Edges) != 0 {
		t.Fatalf("cascade: %+v %v", result, err)
	}
	_, err = ApplyCommands(t.Context(), r, []Command{{Type: "move_nodes", Positions: []Position{{NodeID: "s", X: 12}, {NodeID: "missing", Y: 9}}}})
	after, _ = json.Marshal(r)
	if err == nil || string(before) != string(after) {
		t.Fatalf("move rollback: %v", err)
	}
}

func TestCommandUnionStrict(t *testing.T) {
	for _, raw := range []string{`{"type":"remove_node","nodeId":"s","edgeId":"e"}`, `{"type":"set_rule","name":null}`, `{"type":"move_nodes"}`, `{"type":"add_node","node":null}`, `{"type":"set_rule","name":"R","binding":null}`, `{"type":"move_nodes","positions":[{"nodeId":"s","x":0}]}`} {
		var c Command
		if json.Unmarshal([]byte(raw), &c) == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestCommandsEmptyMoveAndOwnership(t *testing.T) {
	r := baseRule()
	result, err := ApplyCommands(t.Context(), r, []Command{{Type: "move_nodes", Positions: []Position{}}})
	if err != nil || len(result.Nodes) != 2 {
		t.Fatalf("empty move: %+v %v", result, err)
	}
	node := Node{ID: "c", Type: "condition", Name: "Condition", Condition: &overrides.Condition{In: "query", Name: "x", Op: "equals", Value: "before"}}
	result, err = ApplyCommands(t.Context(), r, []Command{{Type: "add_node", Node: &node}})
	if err != nil {
		t.Fatal(err)
	}
	result.Nodes[2].Condition.Value = "after"
	if node.Condition.Value != "before" {
		t.Fatal("inserted payload aliases caller")
	}
	result.Binding.Path = "/changed"
	if r.Binding.Path != "/orders" {
		t.Fatal("binding aliases caller")
	}
}
