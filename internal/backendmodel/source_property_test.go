package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestSource6PropertyPresenceAndNull(t *testing.T) {
	t.Parallel()
	selector := TypedSourcePropertySelector{Kind: "attributes", Group: "description"}
	for _, tc := range []struct {
		name    string
		attrs   string
		present bool
		value   string
	}{
		{"absent", `{}`, false, ""},
		{"explicit null", `{"description":null}`, true, "null"},
		{"empty string", `{"description":""}`, true, `""`},
		{"text", `{"description":"handler"}`, true, `"handler"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "save", Attributes: sourceContractAttrs(t, tc.attrs)}
			got, err := SelectSourceProperty(p, selector)
			if err != nil {
				t.Fatal(err)
			}
			if got.Present != tc.present || string(got.Value) != tc.value {
				t.Fatalf("property = %+v, want present=%v value=%s", got, tc.present, tc.value)
			}
		})
	}
}

func TestSource6ApplyPropertyPreservesAbsenceAndInput(t *testing.T) {
	t.Parallel()
	p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "save", ParentID: new("11111111-1111-4111-8111-111111111111"), Attributes: sourceContractAttrs(t, `{"description":"old","language":"go"}`)}
	before, err := json.Marshal(p)
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		name     string
		selector TypedSourcePropertySelector
		value    SourcePropertyValue
		want     string
	}{
		{"delete description", TypedSourcePropertySelector{Kind: "attributes", Group: "description"}, SourcePropertyValue{}, ""},
		{"delete parent", TypedSourcePropertySelector{Kind: "parent"}, SourcePropertyValue{}, ""},
		{"set explicit null", TypedSourcePropertySelector{Kind: "attributes", Group: "description"}, SourcePropertyValue{Present: true, Value: jsontext.Value(`null`)}, "null"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			updated, err := ApplySourceProperty(p, tc.selector, tc.value)
			if err != nil {
				t.Fatal(err)
			}
			got, err := SelectSourceProperty(updated, tc.selector)
			if err != nil {
				t.Fatal(err)
			}
			if got.Present != tc.value.Present || string(got.Value) != tc.want {
				t.Errorf("applied property = %+v, want presence=%v value=%s", got, tc.value.Present, tc.want)
			}
			language, err := SelectSourceProperty(updated, TypedSourcePropertySelector{Kind: "attributes", Group: "language"})
			if err != nil || string(language.Value) != `"go"` {
				t.Errorf("sibling changed: %+v, %v", language, err)
			}
			after, err := json.Marshal(p)
			if err != nil {
				t.Fatal(err)
			}
			if !sourceContractJSONEqual(t, after, before) {
				t.Errorf("ApplySourceProperty mutated its input: %s", after)
			}
		})
	}
}

func TestSource6PropertyGroupsAreTypedAndAtomic(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name, recordType, kind, attrs string
		selector                      TypedSourcePropertySelector
		want                          string
	}{
		{"step inputs", "node", "flow_step", `{"inputs":[{"key":"second"},{"key":"first"}]}`, TypedSourcePropertySelector{Kind: "flow_ports", Collection: "inputs"}, `[{"key":"second"},{"key":"first"}]`},
		{"query results", "node", "query", `{"results":[{"key":"z"},{"key":"a"}]}`, TypedSourcePropertySelector{Kind: "flow_ports", Collection: "results"}, `[{"key":"z"},{"key":"a"}]`},
		{"mapping sources", "node", "field_mapping", `{"sources":[{"kind":"column","nodeId":"b","facetKey":"sql"},{"kind":"column","nodeId":"a","facetKey":"orm"}]}`, TypedSourcePropertySelector{Kind: "mapping_sources"}, `[{"kind":"column","nodeId":"b","facetKey":"sql"},{"kind":"column","nodeId":"a","facetKey":"orm"}]`},
		{"foreign key pairs", "edge", "references", `{"facets":{"sql":{"columnPairs":[{"fromColumnId":"b","toColumnId":"d"},{"fromColumnId":"a","toColumnId":"c"}]}}}`, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "columnPairs"}, `[{"fromColumnId":"b","toColumnId":"d"},{"fromColumnId":"a","toColumnId":"c"}]`},
		{"column native type", "node", "column", `{"facets":{"sql/orm~v1":{"nativeType":{"status":"known","value":"NUMERIC(18,2)"}},"other":{"nativeType":{"status":"known","value":"TEXT"}}}}`, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql/orm~v1", Group: "nativeType"}, `{"status":"known","value":"NUMERIC(18,2)"}`},
		{"datastore facet root", "node", "datastore", `{"relational":{"facets":{"sql":{"databaseName":"main"}}}}`, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "databaseName"}, `"main"`},
		{"routine facet root", "node", "symbol", `{"databaseRoutine":{"facets":{"sql":{"definition":"SELECT sourceKey FROM tableKey"}}}}`, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "definition"}, `"SELECT sourceKey FROM tableKey"`},
		{"migration definition", "node", "migration", `{"facets":{"sql":{"definition":"ALTER TABLE orders ADD id int"}}}`, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "definition"}, `"ALTER TABLE orders ADD id int"`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := SourceAssertionPayload{RecordType: tc.recordType, Kind: tc.kind, Attributes: sourceContractAttrs(t, tc.attrs)}
			value, err := SelectSourceProperty(p, tc.selector)
			if err != nil {
				t.Fatal(err)
			}
			if !value.Present || !sourceContractJSONEqual(t, value.Value, []byte(tc.want)) {
				t.Errorf("selected %s, want %s", value.Value, tc.want)
			}
			groups, err := SemanticPropertyGroups("6", tc.recordType, tc.kind)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, group := range groups {
				selector := tc.selector
				if selector.Kind == "relational_facet" {
					selector.FacetKey = ""
				}
				if group.Selector == selector {
					found = true
					if !group.Atomic {
						t.Error("ordered/group property must be atomic")
					}
				}
			}
			if !found {
				t.Error("property missing from kind registry")
			}
		})
	}
}

func TestSource6PropertyRejectsMetadataAndWrongKinds(t *testing.T) {
	t.Parallel()
	p := SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "save", Attributes: sourceContractAttrs(t, `{}`)}
	for _, selector := range []TypedSourcePropertySelector{
		{Kind: "attributes", Group: "id"}, {Kind: "attributes", Group: "ownership"}, {Kind: "attributes", Group: "freshness"},
		{Kind: "attributes", Group: "evidenceIds"}, {Kind: "attributes", Group: "/name"}, {Kind: "edge_endpoints"},
		{Kind: "flow_ports", Collection: "inputs"}, {Kind: "mapping_sources"}, {Kind: "relational_facet", FacetKey: "sql", Group: "nativeType"},
	} {
		t.Run(selector.Kind+"/"+selector.Group+selector.Collection, func(t *testing.T) {
			if _, err := SelectSourceProperty(p, selector); err == nil {
				t.Error("accepted a nonsemantic or inapplicable selector")
			}
			if _, err := ApplySourceProperty(p, selector, SourcePropertyValue{Present: true, Value: jsontext.Value(`"invented"`)}); err == nil {
				t.Error("applied a nonsemantic or inapplicable selector")
			}
		})
	}
	for _, raw := range []string{`{"kind":"name","group":"id"}`, `{"kind":"flow_ports","collection":"inputs/0"}`, `{"kind":"attributes","group":"name","path":"/id"}`, `{"kind":"relational_facet","group":"nativeType"}`} {
		var selector TypedSourcePropertySelector
		if err := json.Unmarshal([]byte(raw), &selector); err == nil {
			t.Errorf("accepted selector %s", raw)
		}
	}
}

func TestSource6FacetSemanticsExcludeProviderProof(t *testing.T) {
	t.Parallel()
	left := sourceContractHashGraph(t).Assertions[0]
	left.Payload.Kind = "column"
	left.Payload.Attributes = sourceContractAttrs(t, `{"facets":{"sql":{"nativeType":{"status":"known","value":"integer"},"nullable":{"status":"known","value":true},"sourceKind":"sql","sourceSnapshotId":"55555555-5555-4555-8555-555555555555","evidenceIds":["44444444-4444-4444-8444-444444444444"],"freshness":{"status":"current"}}}}`)
	right := left
	right.Owner.ProviderNamespace = "provider-b"
	right.Payload.Attributes = sourceContractAttrs(t, `{"facets":{"sql":{"nativeType":{"status":"known","value":"integer"},"nullable":{"status":"known","value":false},"sourceKind":"orm","sourceSnapshotId":"66666666-6666-4666-8666-666666666666","evidenceIds":["33333333-3333-4333-8333-333333333333"],"freshness":{"status":"stale"}}}}`)
	for _, group := range []string{"sourceKind", "sourceSnapshotId", "evidenceIds", "freshness"} {
		selector := TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: group}
		if _, err := SelectSourceProperty(left.Payload, selector); err == nil {
			t.Errorf("proof wrapper %s became a semantic property", group)
		}
	}
	beforeLeft, err := json.Marshal(left.Payload)
	if err != nil {
		t.Fatal(err)
	}
	beforeRight, err := json.Marshal(right.Payload)
	if err != nil {
		t.Fatal(err)
	}
	candidate := &composedCandidate{Source: &SourceGraphSnapshot{Assertions: []ProviderAssertion{left, right}}, Graph: &graphCandidate{}}
	_, err = mergeSourceAssertions(&SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}}, candidate, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(candidate.Conflicts) != 1 {
		t.Fatalf("got %d conflicts, want only differing nullability: %+v", len(candidate.Conflicts), candidate.Conflicts)
	}
	conflict := candidate.Conflicts[0]
	if conflict.Property != (TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "nullable"}) || len(conflict.Contenders) != 2 {
		t.Fatalf("unexpected conflict: %+v", conflict)
	}
	if !sourceContractJSONEqual(t, conflict.Contenders[0].Value.Value, []byte(`{"status":"known","value":true}`)) || !sourceContractJSONEqual(t, conflict.Contenders[1].Value.Value, []byte(`{"status":"known","value":false}`)) {
		t.Errorf("mixed provider values: %+v", conflict.Contenders)
	}
	afterLeft, err := json.Marshal(candidate.Source.Assertions[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	afterRight, err := json.Marshal(candidate.Source.Assertions[1].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if !sourceContractJSONEqual(t, beforeLeft, afterLeft) || !sourceContractJSONEqual(t, beforeRight, afterRight) {
		t.Error("semantic projection rewrote own provider values or proof")
	}
}

func sourceContractAttrs(t *testing.T, raw string) map[string]jsontext.Value {
	t.Helper()
	var attrs map[string]jsontext.Value
	if err := json.Unmarshal([]byte(raw), &attrs); err != nil {
		t.Fatal(err)
	}
	return attrs
}

func sourceContractJSONEqual(t *testing.T, left, right []byte) bool {
	t.Helper()
	var l, r any
	if err := json.Unmarshal(left, &l); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(right, &r); err != nil {
		t.Fatal(err)
	}
	return reflect.DeepEqual(l, r)
}
