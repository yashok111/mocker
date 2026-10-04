package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"maps"
	"slices"
	"strings"
	"testing"
)

func representationOwnerAttrs() map[string]jsontext.Value {
	return map[string]jsontext.Value{"qualifiedName": jsontext.Value(`"orders.Order"`), "analysisStatus": jsontext.Value(`"complete"`), "gaps": jsontext.Value(`[]`)}
}

func representationFieldAttrs() map[string]jsontext.Value {
	return map[string]jsontext.Value{
		"selector":       jsontext.Value(`[{"property":"address"},{"property":"postalCode"}]`),
		"nativeType":     jsontext.Value(`{"status":"known","value":"string"}`),
		"nullable":       jsontext.Value(`{"status":"known","value":false}`),
		"cardinality":    jsontext.Value(`{"status":"known","value":"one"}`),
		"analysisStatus": jsontext.Value(`"complete"`), "gaps": jsontext.Value(`[]`),
	}
}

func TestRepresentationSourceProfileVocabulary(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"domain_entity", "dto", "api_schema", "representation_field"} {
		if slices.Contains(SupportedNodeKindsForProfile(EventsProfile), kind) {
			t.Fatalf("source5 widened to %s", kind)
		}
		if !slices.Contains(SupportedNodeKindsForProfile(ComposedProfile), kind) {
			t.Errorf("source6 does not admit %s", kind)
		}
	}
}

func TestRepresentationStrictAttributes(t *testing.T) {
	t.Parallel()
	for _, kind := range []string{"domain_entity", "dto", "api_schema", "representation_field"} {
		t.Run(kind, func(t *testing.T) {
			a := representationOwnerAttrs()
			if kind == "representation_field" {
				a = representationFieldAttrs()
			}
			if err := validateSourceStructuralAttributes(ComposedProfile, kind, a, false); err != nil {
				t.Fatal(err)
			}
			if err := validateSourceStructuralAttributes(EventsProfile, kind, a, false); err == nil {
				t.Fatal("source5 admitted representation attributes")
			}
		})
	}
	mutations := map[string]func(map[string]jsontext.Value){
		"empty selector": func(a map[string]jsontext.Value) { a["selector"] = jsontext.Value(`[]`) },
		"items selector": func(a map[string]jsontext.Value) { a["selector"] = jsontext.Value(`[{"items":true}]`) },
		"empty property": func(a map[string]jsontext.Value) { a["selector"] = jsontext.Value(`[{"property":""}]`) },
		"long property": func(a map[string]jsontext.Value) {
			a["selector"], _ = json.Marshal([]map[string]string{{"property": strings.Repeat("x", 201)}})
		},
		"long path": func(a map[string]jsontext.Value) {
			a["selector"] = jsontext.Value(`[` + strings.Repeat(`{"property":"x"},`, 32) + `{"property":"x"}]`)
		},
		"null nullable": func(a map[string]jsontext.Value) { a["nullable"] = jsontext.Value(`null`) },
		"string nullable": func(a map[string]jsontext.Value) {
			a["nullable"] = jsontext.Value(`{"status":"known","value":"false"}`)
		},
		"missing nullable": func(a map[string]jsontext.Value) { delete(a, "nullable") },
		"unknown has value": func(a map[string]jsontext.Value) {
			a["nativeType"] = jsontext.Value(`{"status":"unknown","reason":"not declared","value":"string"}`)
		},
		"empty reason": func(a map[string]jsontext.Value) { a["nullable"] = jsontext.Value(`{"status":"unknown","reason":""}`) },
		"cardinality enum": func(a map[string]jsontext.Value) {
			a["cardinality"] = jsontext.Value(`{"status":"known","value":"array"}`)
		},
		"unknown member":    func(a map[string]jsontext.Value) { a["facetKey"] = jsontext.Value(`"sql"`) },
		"old analysis enum": func(a map[string]jsontext.Value) { a["analysisStatus"] = jsontext.Value(`"unsupported"`) },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			a := representationFieldAttrs()
			mutate(a)
			if err := validateSourceStructuralAttributes(ComposedProfile, "representation_field", a, false); err == nil {
				t.Fatal("invalid attributes admitted")
			}
		})
	}
	a := representationFieldAttrs()
	path := make([]map[string]string, 32)
	for i := range path {
		path[i] = map[string]string{"property": strings.Repeat("я", 200)}
	}
	a["selector"], _ = json.Marshal(path)
	if err := validateSourceStructuralAttributes(ComposedProfile, "representation_field", a, false); err != nil {
		t.Fatalf("maximum UTF-8 property path: %v", err)
	}
	for _, key := range []string{"nativeType", "nullable", "cardinality"} {
		a[key] = jsontext.Value(`{"status":"unknown","reason":"not declared"}`)
	}
	a["analysisStatus"], a["gaps"] = jsontext.Value(`"unknown"`), jsontext.Value(`["not declared"]`)
	if err := validateSourceStructuralAttributes(ComposedProfile, "representation_field", a, false); err != nil {
		t.Fatal(err)
	}
}

func representationStructure() SourceStructuralGraph {
	return SourceStructuralGraph{SchemaVersion: ComposedSchemaVersion, Nodes: []Node{
		{ID: "00000000-0000-4000-8000-000000000001", Kind: "service", Name: "orders", Attributes: map[string]jsontext.Value{}},
		{ID: "00000000-0000-4000-8000-000000000002", Kind: "domain_entity", Name: "Order", ParentID: new("00000000-0000-4000-8000-000000000001"), Attributes: representationOwnerAttrs()},
		{ID: "00000000-0000-4000-8000-000000000003", Kind: "representation_field", Name: "postalCode", ParentID: new("00000000-0000-4000-8000-000000000002"), Attributes: representationFieldAttrs()},
	}, Edges: []Edge{
		{ID: "00000000-0000-4000-8000-000000000004", Kind: "contains", From: "00000000-0000-4000-8000-000000000001", To: "00000000-0000-4000-8000-000000000002", Attributes: map[string]jsontext.Value{}},
		{ID: "00000000-0000-4000-8000-000000000005", Kind: "contains", From: "00000000-0000-4000-8000-000000000002", To: "00000000-0000-4000-8000-000000000003", Attributes: map[string]jsontext.Value{}},
	}}
}

func TestRepresentationDesiredStructure(t *testing.T) {
	t.Parallel()
	g := representationStructure()
	d, err := ValidateSourceStructure(t.Context(), g)
	if err != nil || len(d) != 0 {
		t.Fatalf("proof-free desired structure: %v %+v", err, d)
	}
	for _, kind := range []string{"dto", "api_schema"} {
		g.Nodes[1].Kind = kind
		d, err = ValidateSourceStructure(t.Context(), g)
		if err != nil || len(d) != 0 {
			t.Fatalf("%s: %v %+v", kind, err, d)
		}
	}
	t.Run("wrong parent", func(t *testing.T) {
		g := representationStructure()
		g.Nodes[2].ParentID = new(g.Nodes[0].ID)
		g.Edges[1].From = g.Nodes[0].ID
		d, err := ValidateSourceStructure(t.Context(), g)
		if err != nil || len(d) == 0 {
			t.Fatalf("wrong parent admitted: %v %+v", err, d)
		}
	})
	t.Run("duplicate selector", func(t *testing.T) {
		g := representationStructure()
		n := g.Nodes[2]
		n.ID = "00000000-0000-4000-8000-000000000006"
		n.Attributes = maps.Clone(n.Attributes)
		g.Nodes = append(g.Nodes, n)
		e := g.Edges[1]
		e.ID = "00000000-0000-4000-8000-000000000007"
		e.To = n.ID
		g.Edges = append(g.Edges, e)
		d, err := ValidateSourceStructure(t.Context(), g)
		if err != nil || len(d) == 0 {
			t.Fatalf("duplicate admitted: %v %+v", err, d)
		}
	})
	for _, kind := range []string{"domain_entity", "representation_field"} {
		t.Run("missing parent "+kind, func(t *testing.T) {
			g := representationStructure()
			for i := range g.Nodes {
				if g.Nodes[i].Kind == kind {
					g.Nodes[i].ParentID = nil
				}
			}
			d, err := ValidateSourceStructure(t.Context(), g)
			if err != nil || len(d) == 0 {
				t.Fatalf("missing parent admitted: %v %+v", err, d)
			}
		})
	}
}
