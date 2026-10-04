package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"testing"
)

func TestRepresentationTypedAttributeRoundTrip(t *testing.T) {
	t.Parallel()
	raw, err := json.Marshal(representationFieldAttrs())
	if err != nil {
		t.Fatal(err)
	}
	var field RepresentationFieldAttributes
	if err := json.Unmarshal(raw, &field); err != nil {
		t.Fatal(err)
	}
	if field.Nullable.Value == nil || *field.Nullable.Value || field.Cardinality.Value == nil || *field.Cardinality.Value != "one" {
		t.Fatalf("explicit values lost: %+v", field)
	}
	encoded, err := json.Marshal(field)
	if err != nil {
		t.Fatal(err)
	}
	if !sourceContractJSONEqual(t, raw, encoded) {
		t.Fatalf("typed round trip changed attributes: %s", encoded)
	}
	var owner RepresentationAttributes
	if err := json.Unmarshal([]byte(`{"qualifiedName":"orders.Order","analysisStatus":"complete","gaps":[]}`), &owner); err != nil {
		t.Fatal(err)
	}
	for _, raw := range []string{`{"qualifiedName":"orders.Order","analysisStatus":"complete","gaps":[],"nullable":false}`, `{"qualifiedName":"orders.Order","analysisStatus":"unknown","gaps":[]}`} {
		if err := json.Unmarshal([]byte(raw), &owner); err == nil {
			t.Fatalf("typed owner accepted %s", raw)
		}
	}
}

// Literal IDs and expected mapping values keep this fixture independent of the
// source normalizer, UUID allocator and selector registry under test.
func representationChain(t *testing.T) SourceStructuralGraph {
	t.Helper()
	g := representationStructure()
	add := func(id, kind, parent string, attrs map[string]jsontext.Value) {
		g.Nodes = append(g.Nodes, Node{ID: id, Kind: kind, Name: "Order", ParentID: new(parent), Attributes: attrs})
		g.Edges = append(g.Edges, Edge{ID: fmt.Sprintf("a0000000-0000-4000-8000-%012d", len(g.Edges)+1), Kind: "contains", From: parent, To: id, Attributes: map[string]jsontext.Value{}})
	}
	const service = "00000000-0000-4000-8000-000000000001"
	add("00000000-0000-4000-8000-000000000010", "dto", service, representationOwnerAttrs())
	add("00000000-0000-4000-8000-000000000011", "representation_field", "00000000-0000-4000-8000-000000000010", representationFieldAttrs())
	add("00000000-0000-4000-8000-000000000020", "api_schema", service, representationOwnerAttrs())
	add("00000000-0000-4000-8000-000000000021", "representation_field", "00000000-0000-4000-8000-000000000020", representationFieldAttrs())
	add("00000000-0000-4000-8000-000000000030", "http_operation", service, sourceContractAttrs(t, `{"method":"GET","path":"/orders"}`))
	add("00000000-0000-4000-8000-000000000031", "api_field", "00000000-0000-4000-8000-000000000030", sourceContractAttrs(t, `{"direction":"response","location":"body","selector":{"kind":"body","path":[{"property":"postalCode"}]},"responseStatus":"200","mediaType":"application/json","nativeType":{"status":"known","value":"string"},"analysisStatus":"complete","gaps":[]}`))
	// Same labels/native types with a different field identity provide no mapping.
	add("00000000-0000-4000-8000-000000000022", "representation_field", "00000000-0000-4000-8000-000000000020", sourceContractAttrs(t, `{"selector":[{"property":"unmapped"}],"nativeType":{"status":"known","value":"string"},"nullable":{"status":"known","value":false},"cardinality":{"status":"known","value":"one"},"analysisStatus":"complete","gaps":[]}`))
	facet := func(kind, fields string) map[string]jsontext.Value {
		raw := `{"facets":{"sql":{"dialect":"sqlite","analysisStatus":"complete","gaps":[],` + fields + `}}}`
		if kind == "datastore" {
			raw = `{"relational":` + raw + `}`
		}
		return sourceContractAttrs(t, raw)
	}
	add("00000000-0000-4000-8000-000000000040", "datastore", service, facet("datastore", `"databaseName":"orders","qualifiedName":"orders","nativeDefinition":null`))
	add("00000000-0000-4000-8000-000000000041", "db_schema", "00000000-0000-4000-8000-000000000040", facet("db_schema", `"qualifiedName":"main","nativeDefinition":null`))
	add("00000000-0000-4000-8000-000000000042", "table", "00000000-0000-4000-8000-000000000041", facet("table", `"qualifiedName":"main.orders","nativeDefinition":null,"columnsStatus":"complete","constraintsStatus":"complete"`))
	for i, id := range []string{"00000000-0000-4000-8000-000000000043", "00000000-0000-4000-8000-000000000044"} {
		add(id, "column", "00000000-0000-4000-8000-000000000042", facet("column", fmt.Sprintf(`"nativeType":{"status":"known","value":"string"},"typeFamily":{"status":"known","value":"string"},"nullable":{"status":"known","value":false},"defaultExpression":{"status":"known","value":null},"generatedExpression":{"status":"known","value":null},"identity":{"status":"known","value":null},"ordinal":{"status":"known","value":%d}`, i+1)))
		g.Nodes[len(g.Nodes)-1].Name = fmt.Sprintf("part%d", i+1)
	}
	for _, mapping := range []struct{ id, parent, sources, destination, transform, status, gaps string }{
		{"00000000-0000-4000-8000-000000000050", "00000000-0000-4000-8000-000000000002", `[{"kind":"column","nodeId":"00000000-0000-4000-8000-000000000043","facetKey":"sql"},{"kind":"column","nodeId":"00000000-0000-4000-8000-000000000044","facetKey":"sql"}]`, `{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000003"}`, "compute", "complete", `[]`},
		{"00000000-0000-4000-8000-000000000051", "00000000-0000-4000-8000-000000000010", `[{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000003"}]`, `{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000011"}`, "flatten", "complete", `[]`},
		{"00000000-0000-4000-8000-000000000052", "00000000-0000-4000-8000-000000000020", `[{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000011"}]`, `{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000021"}`, "rename", "complete", `[]`},
		{"00000000-0000-4000-8000-000000000053", "00000000-0000-4000-8000-000000000020", `[{"kind":"representation_field","nodeId":"00000000-0000-4000-8000-000000000021"}]`, `{"kind":"api_field","nodeId":"00000000-0000-4000-8000-000000000031"}`, "unknown_transform", "partial", `["operation serialization is not resolved"]`},
	} {
		add(mapping.id, "field_mapping", mapping.parent, sourceContractAttrs(t, `{"sources":`+mapping.sources+`,"destination":`+mapping.destination+`,"transform":{"kind":"`+mapping.transform+`","description":"Explicit source mapping","redacted":false},"analysisStatus":"`+mapping.status+`","gaps":`+mapping.gaps+`}`))
	}
	return g
}

func TestRepresentationExplicitMappingChainStructure(t *testing.T) {
	t.Parallel()
	g := representationChain(t)
	d, err := ValidateSourceStructure(t.Context(), g)
	if err != nil || len(d) != 0 {
		t.Fatalf("chain structure: %v %+v", err, d)
	}
	want := map[string][]string{
		"00000000-0000-4000-8000-000000000050": {"00000000-0000-4000-8000-000000000043", "00000000-0000-4000-8000-000000000044", "00000000-0000-4000-8000-000000000003"},
		"00000000-0000-4000-8000-000000000051": {"00000000-0000-4000-8000-000000000003", "00000000-0000-4000-8000-000000000011"},
		"00000000-0000-4000-8000-000000000052": {"00000000-0000-4000-8000-000000000011", "00000000-0000-4000-8000-000000000021"},
		"00000000-0000-4000-8000-000000000053": {"00000000-0000-4000-8000-000000000021", "00000000-0000-4000-8000-000000000031"},
	}
	for _, n := range g.Nodes {
		if n.Kind != "field_mapping" {
			continue
		}
		refs, err := sourceAttributeReferences(n.Kind, n.Attributes, false, true)
		if err != nil {
			t.Fatal(err)
		}
		ids := []string{}
		for _, ref := range refs {
			ids = append(ids, ref.ID)
			if ref.ID == "00000000-0000-4000-8000-000000000022" {
				t.Fatal("same-name field gained an inferred reference")
			}
		}
		if !slices.Equal(ids, want[n.ID]) {
			t.Fatalf("mapping %s refs=%v want=%v", n.ID, ids, want[n.ID])
		}
		a, err := decodeLineageMappingForSchema(n.Attributes, ComposedSchemaVersion)
		if err != nil {
			t.Fatal(err)
		}
		if n.ID == "00000000-0000-4000-8000-000000000050" && !reflect.DeepEqual(a.Sources, []LineageValueRef{{Kind: "column", NodeID: "00000000-0000-4000-8000-000000000043", FacetKey: "sql"}, {Kind: "column", NodeID: "00000000-0000-4000-8000-000000000044", FacetKey: "sql"}}) {
			t.Fatalf("two-input order changed: %+v", a.Sources)
		}
	}
}
