package schemamodel

import (
	"github.com/yashok111/mocker/internal/jsonx"
	"strings"
	"testing"
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
func TestRenamePreservesValuesAndExactNumbers(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"User":{"type":"object","required":["id"],"properties":{"id":{"type":"integer","example":9007199254740993}},"example":{"$ref":"#/components/schemas/User"},"x-opaque":{"$ref":"#/components/schemas/User"}},"Order":{"properties":{"user":{"$ref":"#/components/schemas/User/properties/id"}},"discriminator":{"mapping":{"user":"User"}}}}},"x-mocker-schema-layout":{"formatVersion":1,"positions":{"User":{"x":3,"y":4}}}}`)
	out, err := Apply(root, []Command{{Kind: "rename_schema", SchemaName: "User", NewName: "Person"}, {Kind: "rename_property", SchemaName: "Person", PropertyName: "id", NewName: "key"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := jsonx.Marshal(out)
	got := string(raw)
	for _, want := range []string{`#/components/schemas/Person/properties/key`, `"required":["key"]`, `9007199254740993`, `"example":{"$ref":"#/components/schemas/User"}`, `"x-opaque":{"$ref":"#/components/schemas/User"}`, `"user":"Person"`, `"positions":{"Person"`} {
		if !strings.Contains(got, want) {
			t.Errorf("missing %s in %s", want, got)
		}
	}
	original, _ := jsonx.Marshal(root)
	if strings.Contains(string(original), "Person") {
		t.Fatal("input mutated")
	}
}
func TestDeleteSafetyAndAtomicBatch(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"User":{"properties":{"id":{"type":"integer"}}},"Order":{"properties":{"user":{"$ref":"#/components/schemas/User/properties/id"}}}}}}`)
	for _, c := range []Command{{Kind: "delete_schema", SchemaName: "User"}, {Kind: "delete_property", SchemaName: "User", PropertyName: "id"}} {
		if _, err := Apply(root, []Command{c}); err == nil || !strings.Contains(err.Error(), "/Order/properties/user/$ref") {
			t.Fatalf("missing actionable rejection: %v", err)
		}
	}
	if out, err := Apply(root, []Command{{Kind: "create_schema", SchemaName: "New", SchemaJSON: `true`}, {Kind: "delete_schema", SchemaName: "Missing"}}); err == nil || out != nil {
		t.Fatal("partial batch exposed")
	}
	if _, ok := root["components"].(map[string]any)["schemas"].(map[string]any)["New"]; ok {
		t.Fatal("input mutated")
	}
}
func TestReferencesCyclesAndArrayLinks(t *testing.T) {
	root := document(t, `{"paths":{"/users":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}}}}},"components":{"schemas":{"User":{"properties":{"orders":{"description":"keep","example":42}}},"Order":{"$ref":"#/components/schemas/User"}}}}`)
	out, err := Apply(root, []Command{{Kind: "set_reference", SchemaName: "User", PropertyName: "orders", TargetSchema: "Order", Array: new(true)}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Project(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Operations) != 1 || strings.Join(m.Operations[0].Schemas, ",") != "Order,User" {
		t.Fatalf("usage=%+v", m.Operations)
	}
	out, err = Apply(out, []Command{{Kind: "set_reference", SchemaName: "User", PropertyName: "orders", TargetSchema: "Order"}})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := jsonx.Marshal(out)
	if strings.Contains(string(raw), `"items"`) || !strings.Contains(string(raw), `"description":"keep"`) {
		t.Fatal(string(raw))
	}
}

func TestProjectOperationWithoutReferencesUsesEmptySchemaArray(t *testing.T) {
	t.Parallel()
	root := document(t, `{
		"paths": {"/orders/pay": {"post": {"responses": {"200": {"description": "Paid"}}}}},
		"components": {"schemas": {"Order": {"type": "object"}}}
	}`)
	model, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(model.Operations) != 1 {
		t.Fatalf("operations=%+v", model.Operations)
	}
	wire, err := jsonx.Marshal(model.Operations[0])
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(wire), `"schemas":[]`) {
		t.Fatalf("operation schema names must serialize as an array: %s", wire)
	}
}

func TestOpaqueKeywordsAndNamedProperties(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"properties":{"example":{"$ref":"#/components/schemas/B"},"x-name":{"$ref":"#/components/schemas/B"}},"examples":[{"$ref":"#/components/schemas/B"}],"unknown":{"$ref":"#/components/schemas/B"}},"B":true}}}`)
	m, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.References) != 2 {
		t.Fatalf("refs=%+v", m.References)
	}
}

func TestEncodedPointersAndDeletionOfSelfReference(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A/B~C":{"type":"object","properties":{"x/y":{"type":"string"},"self":{"$ref":"#/components/schemas/A~1B~0C"}}},"Other":{"$ref":"#/components/schemas/A~1B~0C/properties/x~1y"}}}}`)
	out, err := Apply(root, []Command{{Kind: "rename_schema", SchemaName: "A/B~C", NewName: "Percent% name"}, {Kind: "rename_property", SchemaName: "Percent% name", PropertyName: "x/y", NewName: "p%"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Project(out)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.References) != 2 {
		t.Fatalf("lost refs: %+v", m.References)
	}
	for _, ref := range m.References {
		if ref.TargetSchema != "Percent% name" {
			t.Fatalf("target=%+v", ref)
		}
	}
	out, err = Apply(out, []Command{{Kind: "delete_schema", SchemaName: "Other"}, {Kind: "delete_schema", SchemaName: "Percent% name"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(schemaMap(out)) != 0 {
		t.Fatal("self ref blocked delete")
	}
}
func TestEditorBoundsAndCommandShape(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"type":"object"}}}}`)
	cases := [][]Command{
		{{Kind: "upsert_property", SchemaName: "A", PropertyName: "p", SchemaJSON: `null`}},
		{{Kind: "create_schema", SchemaName: "B", SchemaJSON: `{"description":"` + strings.Repeat("a", MaxSchemaBytes) + `"}`}},
		{{Kind: "move_schema", SchemaName: "A", X: new(100001.0), Y: new(0.0)}},
		{{Kind: "rename_schema", SchemaName: "A", NewName: "B", Required: new(true)}},
		make([]Command, 101),
	}
	for _, commands := range cases {
		if _, err := Apply(root, commands); err == nil {
			t.Fatalf("accepted %+v", commands)
		}
	}
	large := map[string]any{}
	for i := range 201 {
		large[string(rune('A'+i))] = true
	}
	if _, err := Project(map[string]any{"components": map[string]any{"schemas": large}}); err == nil {
		t.Fatal("schema bounds bypassed")
	}
	props := map[string]any{}
	for i := range 201 {
		props[string(rune('A'+i))] = true
	}
	if _, err := Project(map[string]any{"components": map[string]any{"schemas": map[string]any{"A": map[string]any{"properties": props}}}}); err == nil {
		t.Fatal("property bounds bypassed")
	}
	var nested any = true
	for range 130 {
		nested = map[string]any{"items": nested}
	}
	if _, err := Project(map[string]any{"components": map[string]any{"schemas": map[string]any{"A": nested}}}); err == nil {
		t.Fatal("depth bounds bypassed")
	}
}
func TestDefaultLayoutDoesNotOverlapTallCards(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"properties":{}},"B":{},"C":{},"D":{}}}}`)
	props := object(object(schemaMap(root)["A"])["properties"])
	for i := range 40 {
		props[string(rune('A'+i))] = true
	}
	m, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if m.Schemas[3].Y < 54+40*30+40 {
		t.Fatal("default card rows overlap")
	}
}
func TestNestedSchemaResourceRetainsReferenceScope(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"User":{},"Resource":{"$id":"https://example.test/resource.json","properties":{"user":{"$ref":"#/components/schemas/User"}}}}}}`)
	out, err := Apply(root, []Command{{Kind: "rename_schema", SchemaName: "User", NewName: "Person"}})
	if err != nil {
		t.Fatal(err)
	}
	field := object(object(object(schemaMap(out)["Resource"])["properties"])["user"])
	if field["$ref"] != "#/components/schemas/User" {
		t.Fatal("nested resource ref rewritten")
	}
	m, err := Project(out)
	if err != nil || len(m.References) != 1 || m.References[0].TargetSchema != "https://example.test/resource.json#/components/schemas/User" {
		t.Fatalf("%+v %v", m.References, err)
	}
	if _, err = Apply(root, []Command{{Kind: "delete_schema", SchemaName: "User"}}); err != nil {
		t.Fatal("scoped reference blocks unrelated deletion", err)
	}
}
func TestOperationsFollowReusableResponsesAndPathItems(t *testing.T) {
	root := document(t, `{"paths":{"/users":{"$ref":"#/components/pathItems/Users"}},"components":{"pathItems":{"Users":{"parameters":[{"schema":{"$ref":"#/components/schemas/Query"}}],"get":{"responses":{"200":{"$ref":"#/components/responses/List"}}}}},"responses":{"List":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/User"}}}}},"schemas":{"User":{"properties":{"query":{"$ref":"#/components/schemas/Query"}}},"Query":true}}}`)
	m, err := Project(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Operations) != 1 || m.Operations[0].Pointer != "/paths/~1users/get" || strings.Join(m.Operations[0].Schemas, ",") != "Query,User" {
		t.Fatalf("%+v", m.Operations)
	}
}
func TestOpaqueOpenAPIExtensionsAreNotReferenceSites(t *testing.T) {
	root := document(t, `{"paths":{"x-metadata":{"$ref":"#/components/schemas/User"},"/users":{"get":{"responses":{"x-metadata":{"$ref":"#/components/schemas/User"},"200":{"description":"OK"}}}}},"components":{"schemas":{"User":{},"x-schema":{"properties":{"x-field":{"$ref":"#/components/schemas/User"}}}}}}`)
	out, err := Apply(root, []Command{{Kind: "rename_schema", SchemaName: "User", NewName: "Person"}})
	if err != nil {
		t.Fatal(err)
	}
	m, err := Project(out)
	if err != nil || len(m.References) != 1 || m.References[0].SourceProperty != "x-field" {
		t.Fatalf("%+v %v", m.References, err)
	}
	raw, _ := jsonx.Marshal(out)
	if strings.Count(string(raw), `"$ref":"#/components/schemas/User"`) != 2 {
		t.Fatal("opaque extension rewritten", string(raw))
	}
}
func TestReferenceBindingRefusesNestedResource(t *testing.T) {
	for _, schema := range []string{`{"$id":"nested.json","properties":{"p":{}}}`, `{"properties":{"p":{"$id":"nested.json"}}}`, `{"properties":{"p":{"items":{"$id":"nested.json"}}}}`} {
		root := document(t, `{"components":{"schemas":{"A":`+schema+`,"B":{}}}}`)
		if _, err := Apply(root, []Command{{Kind: "set_reference", SchemaName: "A", PropertyName: "p", TargetSchema: "B", Array: new(true)}}); err == nil {
			t.Fatal("unsafe binding accepted", schema)
		}
	}
}
func TestDynamicReferencesFollowRenameAndBlockDelete(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"User":{},"Ref":{"$dynamicRef":"#/components/schemas/User"}}}}`)
	if _, err := Apply(root, []Command{{Kind: "delete_schema", SchemaName: "User"}}); err == nil {
		t.Fatal("dynamic reference broken")
	}
	out, err := Apply(root, []Command{{Kind: "rename_schema", SchemaName: "User", NewName: "Person"}})
	if err != nil {
		t.Fatal(err)
	}
	if object(schemaMap(out)["Ref"])["$dynamicRef"] != "#/components/schemas/Person" {
		t.Fatal("dynamic reference not renamed")
	}
}
func TestResourceDestructionRequiresExplicitJSON(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"Resource":{"$id":"https://example.test/resource.json","properties":{"id":{},"self":{"$ref":"#/properties/id"}}}}}}`)
	for _, c := range []Command{{Kind: "delete_schema", SchemaName: "Resource"}, {Kind: "delete_property", SchemaName: "Resource", PropertyName: "id"}, {Kind: "rename_property", SchemaName: "Resource", PropertyName: "id", NewName: "key"}} {
		if _, err := Apply(root, []Command{c}); err == nil || !strings.Contains(err.Error(), "$id") {
			t.Fatal("unsafe resource mutation", err)
		}
	}
}
func TestDirectLinkReplacesScalarShapeAndKeepsMetadata(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"properties":{"p":{"type":"string","description":"keep","example":{"id":1},"x-meta":true}}},"B":{"type":"object"}}}}`)
	out, err := Apply(root, []Command{{Kind: "set_reference", SchemaName: "A", PropertyName: "p", TargetSchema: "B"}})
	if err != nil {
		t.Fatal(err)
	}
	field := object(object(object(schemaMap(out)["A"])["properties"])["p"])
	if _, ok := field["type"]; ok {
		t.Fatal("incompatible scalar type retained")
	}
	if field["description"] != "keep" || field["x-meta"] != true || field["$ref"] != "#/components/schemas/B" {
		t.Fatal("field metadata lost", field)
	}
}
func TestDirectReferenceDoesNotDropItemsResource(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"properties":{"p":{"type":"array","items":{"$id":"https://example.test/item","type":"object"}}}},"B":{}}}}`)
	if _, err := Apply(root, []Command{{Kind: "set_reference", SchemaName: "A", PropertyName: "p", TargetSchema: "B"}}); err == nil || !strings.Contains(err.Error(), "/items/$id") {
		t.Fatal("items resource silently deleted", err)
	}
}
func TestArrayLinkReplacesScalarItemShapeAndKeepsMetadata(t *testing.T) {
	root := document(t, `{"components":{"schemas":{"A":{"properties":{"p":{"type":"array","items":{"type":"string","description":"item metadata","example":{"id":1}}}}},"B":{"type":"object"}}}}`)
	out, err := Apply(root, []Command{{Kind: "set_reference", SchemaName: "A", PropertyName: "p", TargetSchema: "B", Array: new(true)}})
	if err != nil {
		t.Fatal(err)
	}
	field := object(object(object(schemaMap(out)["A"])["properties"])["p"])
	items := object(field["items"])
	if _, ok := items["type"]; ok {
		t.Fatal("incompatible scalar items type retained")
	}
	if field["type"] != "array" || items["description"] != "item metadata" || items["$ref"] != "#/components/schemas/B" {
		t.Fatal("array or metadata lost", field)
	}
}
