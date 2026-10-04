package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestSource6SelectorRejectsForbiddenMemberPresence(t *testing.T) {
	for _, raw := range []string{
		`{"kind":"name","group":""}`, `{"kind":"name","group":null}`,
		`{"kind":"mapping_sources","collection":""}`, `{"kind":"parent","facetKey":null}`,
		`{"kind":"attributes","group":"description","facetKey":""}`,
		`{"kind":"relational_facet","group":"nativeType","facetKey":"sql","collection":null}`,
		`{"kind":"flow_ports","collection":"inputs","group":""}`,
	} {
		var selector TypedSourcePropertySelector
		if err := json.Unmarshal([]byte(raw), &selector); err == nil {
			t.Errorf("accepted forbidden selector member: %s", raw)
		}
	}
	for _, selector := range []TypedSourcePropertySelector{
		{Kind: "name"}, {Kind: "parent"}, {Kind: "edge_endpoints"}, {Kind: "mapping_sources"}, {Kind: "mapping_destination"}, {Kind: "representation_selector"},
		{Kind: "attributes", Group: "description"}, {Kind: "relational_facet", Group: "nativeType", FacetKey: "sql"}, {Kind: "flow_ports", Collection: "inputs"},
	} {
		raw, err := json.Marshal(selector)
		if err != nil {
			t.Fatal(err)
		}
		var decoded TypedSourcePropertySelector
		if err := json.Unmarshal(raw, &decoded); err != nil || decoded != selector {
			t.Fatalf("valid persisted selector changed: %s %+v %v", raw, decoded, err)
		}
	}
}
