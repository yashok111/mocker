package schemamodel

import (
	"fmt"
	"testing"
)

func TestPathItemsBoundCyclesAndRetainEncounteredMethods(t *testing.T) {
	root := document(t, `{"paths":{"/a":{"$ref":"#/components/pathItems/A","post":{}}},"components":{"pathItems":{"A":{"get":{},"$ref":"#/components/pathItems/B"},"B":{"delete":{},"$ref":"#/components/pathItems/A"}}}}`)
	nodes, diagnostics := PathItems(root, object(root["paths"])["/a"], "/paths/~1a")
	operations := PathItemOperations(nodes)
	if len(nodes) != 3 || len(operations) != 3 || len(diagnostics) != 1 || diagnostics[0].Code != "path_item_ref_cycle" {
		t.Fatalf("cycle lost siblings or failed to stop: %+v %+v %+v", nodes, operations, diagnostics)
	}
	if operations[0].Method != "delete" || operations[1].Pointer != "/components/pathItems/A/get" || operations[2].Pointer != "/paths/~1a/post" {
		t.Fatalf("wrong effective sources: %+v", operations)
	}
}

func TestPathItemsBoundLongReferenceChains(t *testing.T) {
	items := map[string]any{}
	for i := range 140 {
		items[fmt.Sprint(i)] = map[string]any{"$ref": fmt.Sprintf("#/items/%d", i+1)}
	}
	items["1"].(map[string]any)["get"] = map[string]any{}
	root := map[string]any{"items": items}
	nodes, diagnostics := PathItems(root, items["0"], "/items/0")
	if len(nodes) != 128 || len(diagnostics) != 1 || diagnostics[0].Code != "path_item_ref_depth" || len(PathItemOperations(nodes)) != 1 {
		t.Fatalf("unbounded traversal or discarded valid methods: %d %+v", len(nodes), diagnostics)
	}
}

func TestPathItemsReportInvalidReferenceKinds(t *testing.T) {
	for _, test := range []struct{ value, code string }{
		{`{"$ref":false}`, "path_item_ref_invalid"},
		{`{"$ref":"#/missing"}`, "path_item_ref_missing"},
		{`{"$ref":"other.json#/path"}`, "path_item_ref_unsupported"},
		{`{"$ref":"#anchor"}`, "path_item_ref_unsupported"},
		{`{"$ref":"#/target"}`, "path_item_ref_invalid"},
	} {
		root := document(t, `{"item":`+test.value+`,"target":false}`)
		_, diagnostics := PathItems(root, root["item"], "/item")
		if len(diagnostics) != 1 || diagnostics[0].Code != test.code || diagnostics[0].Pointer != "/item/$ref" {
			t.Fatalf("%s diagnostics: %+v", test.value, diagnostics)
		}
	}
}

func TestPathItemsDistinguishNullTargetFromMissingTarget(t *testing.T) {
	root := document(t, `{"item":{"$ref":"#/target"},"target":null}`)
	_, diagnostics := PathItems(root, root["item"], "/item")
	if len(diagnostics) != 1 || diagnostics[0].Code != "path_item_ref_invalid" {
		t.Fatalf("null Path Item is present but invalid: %+v", diagnostics)
	}
}

func TestPathItemsRejectNonPathItemObjectsAtReferenceSite(t *testing.T) {
	for _, ref := range []string{"#/components/schemas/Order", "#"} {
		root := document(t, `{"openapi":"3.1.0","paths":{"/orders":{"$ref":"`+ref+`","post":{}}},"components":{"schemas":{"Order":{"type":"object"}}}}`)
		nodes, diagnostics := PathItems(root, object(root["paths"])["/orders"], "/paths/~1orders")
		if len(diagnostics) != 1 || diagnostics[0].Code != "path_item_ref_invalid" || diagnostics[0].Pointer != "/paths/~1orders/$ref" {
			t.Fatalf("invalid target %s accepted: %+v", ref, diagnostics)
		}
		operations := PathItemOperations(nodes)
		if len(operations) != 1 || operations[0].Method != "post" || operations[0].Pointer != "/paths/~1orders/post" {
			t.Fatalf("valid sibling lost: %+v", operations)
		}
	}
}

func TestPathItemsAcceptEmptyAndExtensionTargets(t *testing.T) {
	for _, target := range []string{`{}`, `{"summary":"Orders","description":"Reusable","servers":[],"parameters":[],"get":{},"x-data":{"anything":true}}`} {
		root := document(t, `{"paths":{"/orders":{"$ref":"#/x-path-item"}},"x-path-item":`+target+`}`)
		nodes, diagnostics := PathItems(root, object(root["paths"])["/orders"], "/paths/~1orders")
		if len(nodes) != 2 || len(diagnostics) != 0 {
			t.Fatalf("valid target %s rejected: %+v", target, diagnostics)
		}
	}
}
