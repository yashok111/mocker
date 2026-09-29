package resourcemap

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func document(t *testing.T, raw string) map[string]any {
	t.Helper()
	d := jsonx.NewDecoder(strings.NewReader(raw))
	d.UseNumber()
	var root map[string]any
	if err := d.Decode(&root); err != nil {
		t.Fatal(err)
	}
	return root
}

func TestProjectGroupsCollectionsAndNestedFamilies(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"post":{"x-mocker-canvas-operation-id":"a"}},"/orders/{id}":{"get":{"x-mocker-canvas-operation-id":"b"}},"/orders/{orderId}/items":{"get":{"x-mocker-canvas-operation-id":"c"}},"/orders/{orderId}/items/{itemId}":{"delete":{"x-mocker-canvas-operation-id":"d"}}}}`)
	model, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Resources) != 2 || len(model.Operations) != 4 {
		t.Fatalf("unexpected projection: %+v", model)
	}
	if model.Resources[0].Name != "/orders" || strings.Join(model.Resources[0].OperationKeys, ",") != "a,b" {
		t.Fatalf("orders family: %+v", model.Resources[0])
	}
	if model.Resources[1].Name != "/orders/{}/items" || strings.Join(model.Resources[1].OperationKeys, ",") != "c,d" {
		t.Fatalf("nested family: %+v", model.Resources[1])
	}
	if model.Resources[0].ID == model.Resources[1].ID || !model.Resources[0].Inferred {
		t.Fatal("automatic IDs/inferred marker incorrect")
	}
}

func TestCommandsPreserveKeysNumbersAndRollback(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"key-1"}}},"x-opaque":{"n":9007199254740993}}`)
	first, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	auto := first.Resources[0]
	changed, err := Apply(root, []Command{
		{Kind: "upsert_resource", Resource: &Resource{ID: auto.ID, Name: "Purchases", Service: "billing", Description: "Owner", OperationKeys: []string{}, X: 12, Y: 34}},
		{Kind: "assign_operation", OperationKey: "key-1", ResourceID: auto.ID},
		{Kind: "upsert_relation", Relation: &Relation{ID: "loop", FromResourceID: auto.ID, ToResourceID: auto.ID, Label: "uses"}},
	})
	if err == nil || changed != nil {
		t.Fatalf("invalid relation accepted: %v", err)
	}
	changed, err = Apply(root, []Command{{Kind: "upsert_resource", Resource: &Resource{ID: auto.ID, Name: "Purchases", Service: "billing", OperationKeys: []string{"key-1"}, X: 12, Y: 34}}})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := jsonx.Marshal(changed)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte("9007199254740993")) || !bytes.Contains(raw, []byte(`"x-opaque"`)) {
		t.Fatalf("unrelated data lost: %s", raw)
	}
	paths := changed["paths"].(map[string]any)
	delete(paths, "/orders")
	paths["/purchases"] = map[string]any{"get": map[string]any{"x-mocker-canvas-operation-id": "key-1"}}
	model, err := Project(changed)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Resources) != 1 || model.Resources[0].Name != "Purchases" || strings.Join(model.Resources[0].OperationKeys, ",") != "key-1" {
		t.Fatalf("rename detached assignment: %+v", model.Resources)
	}
}

func TestMalformedStoredAndStrictCommands(t *testing.T) {
	for _, raw := range []string{
		`{"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"a","name":"A","service":"","description":"","operationKeys":[],"x":100001,"y":0}],"relations":[]}}`,
		`{"x-mocker-resource-map":{"formatVersion":1,"resources":[],"relations":[{"id":"x","fromResourceId":"a","toResourceId":"a","label":""}]}}`,
	} {
		if err := ValidateStored(document(t, raw)); err == nil {
			t.Fatalf("accepted malformed extension %s", raw)
		}
	}
	var cmd Command
	if err := jsonx.Unmarshal([]byte(`{"kind":"remove_resource","resourceId":"a","surprise":true}`), &cmd); err == nil {
		t.Fatal("unknown command field accepted")
	}
	if err := jsonx.Unmarshal([]byte(`{"kind":"upsert_resource","resource":{"id":"a","name":"A","service":"","description":"","operationKeys":[],"x":0,"y":0,"surprise":1}}`), &cmd); err == nil {
		t.Fatal("unknown nested command field accepted")
	}
	for _, raw := range []string{
		`{"kind":"assign_operation","operationKey":"op","resourceId":null}`,
		`{"kind":"upsert_resource","resource":{"id":"a","name":"A","service":null,"description":"","operationKeys":[],"x":0,"y":0}}`,
		`{"kind":"upsert_resource","resource":{"id":"a","name":"A","service":"","description":"","operationKeys":[],"x":null,"y":0}}`,
	} {
		if err := jsonx.Unmarshal([]byte(raw), &cmd); err == nil {
			t.Fatalf("null command field accepted: %s", raw)
		}
	}
}

func TestAssignCommandJSONKeepsEmptyResourceID(t *testing.T) {
	command := Command{Kind: "assign_operation", OperationKey: "op", ResourceID: ""}
	raw, err := jsonx.Marshal(command)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(raw, []byte(`"resourceId":""`)) {
		t.Fatalf("reset command lost empty target: %s", raw)
	}
	var decoded Command
	if err := jsonx.Unmarshal(raw, &decoded); err != nil || decoded.Kind != "assign_operation" || decoded.ResourceID != "" {
		t.Fatalf("round-trip: %+v %v", decoded, err)
	}
}

func TestProjectReportsStaleLinksAndUnsupportedReferences(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"live","operationId":"orders","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"https://example.test/order.json"}}}}}}}},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"custom","name":"Custom","service":"","description":"","operationKeys":["gone"],"x":0,"y":0}],"relations":[{"id":"rel","fromResourceId":"custom","toResourceId":"deleted","label":"uses"}]}}`)
	model, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]bool{"stale_operation_assignment": false, "missing_relation_endpoint": false, "schema_analysis_unavailable": false}
	for _, d := range model.Diagnostics {
		if _, ok := want[d.Code]; ok {
			want[d.Code] = true
		}
	}
	for code, seen := range want {
		if !seen {
			t.Fatalf("missing %s diagnostic: %+v", code, model.Diagnostics)
		}
	}
	retained := false
	for _, r := range model.Resources {
		if r.ID == "custom" && len(r.OperationKeys) == 1 && r.OperationKeys[0] == "gone" {
			retained = true
		}
	}
	if len(model.Relations) != 1 || !retained {
		t.Fatalf("stale intent lost: %+v", model)
	}
}

func TestDuplicateAssignmentFailsAndInputRemainsIntact(t *testing.T) {
	root := document(t, `{"paths":{"/a":{"get":{"x-mocker-canvas-operation-id":"op"}}}}`)
	_, err := Apply(root, []Command{
		{Kind: "upsert_resource", Resource: &Resource{ID: "a", Name: "A", OperationKeys: []string{"op"}}},
		{Kind: "upsert_resource", Resource: &Resource{ID: "b", Name: "B", OperationKeys: []string{"op"}}},
	})
	if err == nil {
		t.Fatal("duplicate assignment accepted")
	}
	if _, exists := root[Extension]; exists {
		t.Fatal("failed batch mutated input")
	}
}

func TestCommandsMaterializeAndRestoreAutomaticGrouping(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"order"}},"/payments":{"post":{"x-mocker-canvas-operation-id":"payment"}}}}`)
	initial, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	var orders, payments string
	for _, r := range initial.Resources {
		switch r.Name {
		case "/orders":
			orders = r.ID
		case "/payments":
			payments = r.ID
		}
	}
	if orders == "" || payments == "" {
		t.Fatal(initial.Resources)
	}
	moved, err := Apply(root, []Command{{Kind: "move_resource", ResourceID: orders, X: new(80.0), Y: new(120.0)}, {Kind: "assign_operation", OperationKey: "order", ResourceID: payments}, {Kind: "upsert_relation", Relation: &Relation{ID: "uses", FromResourceID: orders, ToResourceID: payments, Label: "uses"}}})
	if err != nil {
		t.Fatal(err)
	}
	model, err := Project(moved)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Relations) != 1 {
		t.Fatal(model.Relations)
	}
	for _, r := range model.Resources {
		if r.ID == orders && (r.Inferred || r.X != 80 || r.Y != 120) {
			t.Fatalf("move lost: %+v", r)
		}
		if r.ID == payments && !strings.Contains(strings.Join(r.OperationKeys, ","), "order") {
			t.Fatalf("assignment lost: %+v", r)
		}
	}
	restored, err := Apply(moved, []Command{{Kind: "remove_relation", RelationID: "uses"}, {Kind: "remove_resource", ResourceID: payments}})
	if err != nil {
		t.Fatal(err)
	}
	model, err = Project(restored)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Relations) != 0 {
		t.Fatal(model.Relations)
	}
	for _, r := range model.Resources {
		if r.ID == payments {
			if !r.Inferred || strings.Join(r.OperationKeys, ",") != "payment" {
				t.Fatalf("automatic family not restored: %+v", r)
			}
		}
	}
}

func TestProjectIncludesTransitiveLocalSchemas(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"order","responses":{"200":{"description":"OK","content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}},"components":{"schemas":{"Order":{"type":"object","properties":{"item":{"$ref":"#/components/schemas/OrderItem"}}},"OrderItem":{"type":"object"}}}}`)
	model, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Operations) != 1 || strings.Join(model.Operations[0].Schemas, ",") != "Order,OrderItem" {
		t.Fatalf("schema traversal: %+v", model.Operations)
	}
}

func TestLongPathAssignedToNamedResourceDoesNotCreateOversizedAutoName(t *testing.T) {
	longPath := "/" + strings.Repeat("a", 210)
	root := document(t, `{"paths":{"`+longPath+`":{"get":{"x-mocker-canvas-operation-id":"op"}}},"x-mocker-resource-map":{"formatVersion":1,"resources":[{"id":"short","name":"Short","service":"","description":"","operationKeys":["op"],"x":0,"y":0}],"relations":[]}}`)
	model, err := Project(root)
	if err != nil || len(model.Resources) != 1 || model.Resources[0].ID != "short" {
		t.Fatalf("assigned long path: %+v %v", model.Resources, err)
	}
}

func TestPathItemReferenceKeepsSiblingsAndReportsBrokenLinks(t *testing.T) {
	for _, test := range []struct{ ref, target, code string }{
		{"#/components/pathItems/Missing", `{}`, "path_item_ref_missing"},
		{"https://example.test/path-item.json", `{}`, "path_item_ref_unsupported"},
		{"#/components/pathItems/Orders", `{"Orders":42}`, "path_item_ref_invalid"},
		{"#/components/pathItems/Orders", `{"Orders":{"$ref":"#/paths/~1orders"}}`, "path_item_ref_cycle"},
	} {
		root := document(t, `{"paths":{"/orders":{"$ref":"`+test.ref+`","get":{"x-mocker-canvas-operation-id":"local"}}},"components":{"pathItems":`+test.target+`}}`)
		model, err := Project(root)
		if err != nil || len(model.Operations) != 1 || model.Operations[0].Key != "local" {
			t.Fatalf("valid sibling omitted: %+v %v", model.Operations, err)
		}
		found := false
		for _, diagnostic := range model.Diagnostics {
			if diagnostic.Code == test.code && strings.Contains(diagnostic.Message, "/orders") {
				found = true
			}
		}
		if !found {
			t.Fatalf("path item %s missing diagnostic %s: %+v", test.ref, test.code, model.Diagnostics)
		}
	}
}

func TestReferencedPathItemEscapesAndSiblingPrecedence(t *testing.T) {
	root := document(t, `{"paths":{"/orders":{"$ref":"#/components/pathItems/chain","x-mocker-canvas-operation-ids":{"get":"inherited"},"post":{"x-mocker-canvas-operation-id":"local","summary":"Local"}}},"components":{"pathItems":{"chain":{"$ref":"#/components/pathItems/a~1b~0c%20d"},"a/b~c d":{"post":{"summary":"Far"},"get":{"summary":"Inherited","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}},"parameters":[{"name":"filter","in":"query","schema":{"$ref":"#/components/schemas/Filter"}}]}},"schemas":{"Order":{"type":"object"},"Filter":{"type":"string"}}}}`)
	m, err := Project(root)
	if err != nil || len(m.Operations) != 2 {
		t.Fatalf("local ref projection: %+v %v", m, err)
	}
	if m.Operations[0].Key != "inherited" || m.Operations[0].Summary != "Inherited" || m.Operations[0].SourcePointer != "/components/pathItems/a~1b~0c d/get" || strings.Join(m.Operations[0].Schemas, ",") != "Filter,Order" {
		t.Fatalf("bad inherited operation: %+v", m.Operations[0])
	}
	if m.Operations[1].Key != "local" || m.Operations[1].Summary != "Local" || m.Operations[1].SourcePointer != "" || strings.Join(m.Operations[1].Schemas, ",") != "Filter" {
		t.Fatalf("sibling lost precedence or inherited params: %+v", m.Operations[1])
	}
	conflict := false
	for _, diagnostic := range m.Diagnostics {
		conflict = conflict || diagnostic.Code == "path_item_method_conflict"
	}
	if !conflict {
		t.Fatal("ambiguous sibling method lacks warning")
	}
}
