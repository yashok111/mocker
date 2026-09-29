package resourcemap

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func layoutFixture(t *testing.T, count int) map[string]any {
	t.Helper()
	resources := make([]Resource, count)
	for i := range resources {
		resources[i] = Resource{
			ID: fmt.Sprintf("r%03d", i), Name: fmt.Sprintf("Resource %03d", i),
			Service: "Orders", Description: "Keep description", OperationKeys: []string{},
			X: 12, Y: 34,
		}
	}
	return map[string]any{
		"paths": map[string]any{}, Extension: storedObject(stored{
			FormatVersion: 1, Resources: resources, Relations: []Relation{},
		}), "x-opaque": jsonx.Number("9007199254740993"),
	}
}

func assertLayoutClear(t *testing.T, resources []ResourceView) {
	t.Helper()
	for i, a := range resources {
		if !validFloat(a.X) || !validFloat(a.Y) {
			t.Fatalf("coordinates out of bounds: %+v", a)
		}
		for _, b := range resources[i+1:] {
			if a.X < b.X+220 && a.X+220 > b.X && a.Y < b.Y+196 && a.Y+196 > b.Y {
				t.Fatalf("cards overlap: %s and %s", a.ID, b.ID)
			}
		}
	}
}

func TestAutoLayoutCommandIsStrictAndRoundTrips(t *testing.T) {
	var command Command
	if err := jsonx.Unmarshal([]byte(`{"kind":"auto_layout"}`), &command); err != nil {
		t.Fatal(err)
	}
	raw, err := jsonx.Marshal(command)
	if err != nil || string(raw) != `{"kind":"auto_layout"}` {
		t.Fatalf("round-trip: %s, %v", raw, err)
	}
	if err := jsonx.Unmarshal([]byte(`{"kind":"auto_layout","x":1}`), &command); err == nil {
		t.Fatal("auto layout accepted an unrelated field")
	}
}

func TestAutoLayoutIsDeterministicAndPreservesMetadata(t *testing.T) {
	root := layoutFixture(t, 5)
	before, _ := jsonx.Marshal(root)
	first, err := Apply(root, []Command{{Kind: "auto_layout"}})
	if err != nil {
		t.Fatal(err)
	}
	after, _ := jsonx.Marshal(root)
	if string(before) != string(after) {
		t.Fatal("layout mutated its input")
	}
	second, err := Apply(first, []Command{{Kind: "auto_layout"}})
	if err != nil {
		t.Fatal(err)
	}
	a, _ := jsonx.Marshal(first)
	b, _ := jsonx.Marshal(second)
	if string(a) != string(b) || !strings.Contains(string(a), "9007199254740993") {
		t.Fatalf("layout is not idempotent or lost opaque data: %s / %s", a, b)
	}
	model, err := Project(first)
	if err != nil {
		t.Fatal(err)
	}
	assertLayoutClear(t, model.Resources)
	for _, r := range model.Resources {
		if r.Service != "Orders" || r.Description != "Keep description" || r.Inferred {
			t.Fatalf("metadata lost: %+v", r)
		}
	}
	// Unconnected imports should remain a compact grid instead of one long column.
	if model.Resources[0].Y != model.Resources[1].Y || model.Resources[0].X == model.Resources[1].X {
		t.Fatal("disconnected resources were not packed into rows")
	}
}

func TestAutoLayoutPreservesOpaqueMapAndElementMetadata(t *testing.T) {
	root := layoutFixture(t, 2)
	extension := object(root[Extension])
	extension["x-owner"] = map[string]any{"id": jsonx.Number("9007199254740993")}
	resource := object(extension["resources"].([]any)[0])
	resource["x-colour"] = "red"
	extension["relations"] = []any{map[string]any{
		"id": "link", "fromResourceId": "r000", "toResourceId": "r001", "label": "uses",
		"x-details": map[string]any{"weight": jsonx.Number("9007199254740993")},
	}}
	before, _ := jsonx.Marshal(root)
	changed, err := Apply(root, []Command{{Kind: "auto_layout"}, {Kind: "move_resource", ResourceID: "r000", X: new(900.0), Y: new(800.0)}})
	if err != nil {
		t.Fatal(err)
	}
	got := object(changed[Extension])
	if !reflect.DeepEqual(got["x-owner"], extension["x-owner"]) {
		t.Fatal("map metadata lost")
	}
	if object(got["resources"].([]any)[0])["x-colour"] != "red" {
		t.Fatal("resource metadata lost")
	}
	if !reflect.DeepEqual(got["relations"], extension["relations"]) {
		t.Fatal("relation metadata changed")
	}
	after, _ := jsonx.Marshal(root)
	if string(before) != string(after) {
		t.Fatal("metadata merge mutated the input")
	}
}

func TestAutoLayoutFollowsRelationsAndHandlesCycles(t *testing.T) {
	root := layoutFixture(t, 6)
	s, err := readStored(root)
	if err != nil {
		t.Fatal(err)
	}
	s.Relations = []Relation{
		{ID: "a", FromResourceID: "r000", ToResourceID: "r001"},
		{ID: "b", FromResourceID: "r001", ToResourceID: "r002"},
		{ID: "c", FromResourceID: "r003", ToResourceID: "r004"},
		{ID: "d", FromResourceID: "r004", ToResourceID: "r003"},
		{ID: "stale", FromResourceID: "r005", ToResourceID: "missing"},
	}
	root[Extension] = storedObject(s)
	changed, err := Apply(root, []Command{{Kind: "auto_layout"}})
	if err != nil {
		t.Fatal(err)
	}
	model, err := Project(changed)
	if err != nil {
		t.Fatal(err)
	}
	assertLayoutClear(t, model.Resources)
	if !(model.Resources[0].X < model.Resources[1].X && model.Resources[1].X < model.Resources[2].X) {
		t.Fatal("directed chain was not arranged left to right")
	}
	if !reflect.DeepEqual(model.Relations, s.Relations) {
		t.Fatal("relations changed")
	}
	// Input ordering does not change the proposal.
	slices.Reverse(s.Resources)
	slices.Reverse(s.Relations)
	root[Extension] = storedObject(s)
	reordered, err := Apply(root, []Command{{Kind: "auto_layout"}})
	if err != nil {
		t.Fatal(err)
	}
	other, _ := Project(reordered)
	if !reflect.DeepEqual(model.Resources, other.Resources) {
		t.Fatal("layout depends on input order")
	}
}

func TestAutoLayoutHandlesLimitsEmptyAndAtomicFailure(t *testing.T) {
	for _, n := range []int{0, MaxResources} {
		root := layoutFixture(t, n)
		changed, err := Apply(root, []Command{{Kind: "auto_layout"}})
		if err != nil {
			t.Fatal(err)
		}
		model, _ := Project(changed)
		if len(model.Resources) != n {
			t.Fatalf("resource count = %d, want %d", len(model.Resources), n)
		}
		assertLayoutClear(t, model.Resources)
		before, _ := jsonx.Marshal(root)
		_, err = Apply(root, []Command{{Kind: "auto_layout"}, {Kind: "remove_resource", ResourceID: "missing"}})
		if err == nil {
			t.Fatal("invalid batch accepted")
		}
		after, _ := jsonx.Marshal(root)
		if string(before) != string(after) {
			t.Fatal("failed batch changed input")
		}
	}
}

func TestAutoLayoutMaterializesOnlyPositionsForInferredResources(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"op-1"}},"/items":{"get":{"x-mocker-canvas-operation-id":"op-2"}}}}`)
	changed, err := Apply(root, []Command{{Kind: "auto_layout"}})
	if err != nil {
		t.Fatal(err)
	}
	saved, err := readStored(changed)
	if err != nil || len(saved.Resources) != 2 {
		t.Fatalf("materialized resources: %+v, %v", saved, err)
	}
	for _, r := range saved.Resources {
		if len(r.OperationKeys) != 0 {
			t.Fatal("layout converted automatic grouping into explicit assignment")
		}
	}
	model, _ := Project(changed)
	if len(model.Operations) != 2 || len(model.Resources[0].OperationKeys) != 1 {
		t.Fatal("projected operation assignments were lost")
	}
}
