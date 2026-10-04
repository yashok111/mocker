package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestRepresentationScopedReferences(t *testing.T) {
	t.Parallel()
	attrs := `{"analysisStatus":"complete","gaps":[],"sources":[{"kind":"event_field","nodeRef":{"localKey":"node-a"},"endpointRef":{"localKey":"node-b"},"routeRef":{"localKey":"edge-a"}},{"kind":"representation_field","nodeRef":{"localKey":"node-a"}}],"destination":{"kind":"representation_field","nodeRef":{"localKey":"node-b"}},"transform":{"kind":"compute","description":"Combine explicit fields","redacted":false}}`
	event := &LineageValueRef{Kind: "event_field", NodeID: sourceContractNode, EndpointID: sourceContractOther, RouteID: sourceContractSnapshot}
	field := &LineageValueRef{Kind: "representation_field", NodeID: sourceContractNode}
	dest := &LineageValueRef{Kind: "representation_field", NodeID: sourceContractOther}
	want := []sourceContractReference{
		{"/attributes/sources/0/nodeId", "node", "event_field", "explicit", "node-a", TypedSourcePropertySelector{Kind: "mapping_sources"}, event},
		{"/attributes/sources/0/endpointId", "node", "event_endpoint", "explicit", "node-b", TypedSourcePropertySelector{Kind: "mapping_sources"}, event},
		{"/attributes/sources/0/routeId", "edge", "event_route", "explicit", "edge-a", TypedSourcePropertySelector{Kind: "mapping_sources"}, event},
		{"/attributes/sources/1/nodeId", "node", "representation_field", "explicit", "node-a", TypedSourcePropertySelector{Kind: "mapping_sources"}, field},
		{"/attributes/destination/nodeId", "node", "representation_field", "explicit", "node-b", TypedSourcePropertySelector{Kind: "mapping_destination"}, dest},
	}
	sourceContractNormalize(t, ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: "field_mapping", Name: "map", Attributes: sourceContractAttrs(t, attrs)}}, want)
}

func TestRepresentationReferenceStrictVariants(t *testing.T) {
	t.Parallel()
	for _, extra := range []string{`"facetKey":"sql"`, `"collection":"inputs"`, `"portKey":"id"`, `"endpointId":"11111111-1111-4111-8111-111111111111"`, `"routeId":"11111111-1111-4111-8111-111111111111"`, `"nodeRef":{"localKey":"id"}`} {
		var ref LineageValueRef
		raw := `{"kind":"representation_field","nodeId":"22222222-2222-4222-8222-222222222222",` + extra + `}`
		if err := json.Unmarshal([]byte(raw), &ref); err == nil {
			t.Fatalf("accepted extra member: %s", raw)
		}
	}
	for _, member := range []string{`"nodeId":"22222222-2222-4222-8222-222222222222"`, `"nodeKey":"node-a"`, `"nodeRef":{"localKey":"node-a"},"facetKey":"sql"`, `"nodeRef":{"localKey":"node-a","base":null}`} {
		attrs := sourceContractAttrs(t, `{"sources":[],"destination":{"kind":"representation_field",`+member+`},"transform":{"kind":"constant","description":"fixed","redacted":false},"analysisStatus":"complete","gaps":[]}`)
		c := ImportCommand{Op: "upsert_node", Node: &ImportNode{Kind: "field_mapping", Name: "map", Attributes: attrs}}
		_, _, err := normalizeSourcePayload(c, &ImportSession{}, func(SourceReferenceSite) (BaseAssertionRef, error) {
			return BaseAssertionRef{ExpectedID: sourceContractNode}, nil
		}, func(string) (string, error) { return "", nil })
		if err == nil {
			t.Fatalf("source input bypassed scoped ref: %s", member)
		}
	}
	var ref ImportRepresentationValueRef
	if err := json.Unmarshal([]byte(`{"kind":"representation_field","nodeRef":{"localKey":"field"}}`), &ref); err != nil {
		t.Fatal(err)
	}
	if ref.NodeRef.LocalKey != "field" {
		t.Fatal("local scoped reference changed")
	}
}

func TestRepresentationSelectorPropertyIsAtomic(t *testing.T) {
	t.Parallel()
	p := SourceAssertionPayload{RecordType: "node", Kind: "representation_field", Attributes: representationFieldAttrs()}
	selector := TypedSourcePropertySelector{Kind: "representation_selector"}
	value, err := SelectSourceProperty(p, selector)
	if err != nil {
		t.Fatal(err)
	}
	if !value.Present || string(value.Value) != `[{"property":"address"},{"property":"postalCode"}]` {
		t.Fatalf("selector not atomic: %+v", value)
	}
	changed, err := ApplySourceProperty(p, selector, SourcePropertyValue{Present: true, Value: jsontext.Value(`[{"property":"zip"}]`)})
	if err != nil {
		t.Fatal(err)
	}
	if string(changed.Attributes["selector"]) != `[{"property":"zip"}]` || !reflect.DeepEqual(changed.Attributes["nullable"], p.Attributes["nullable"]) {
		t.Fatalf("wrong property replacement: %+v", changed)
	}
	for _, bad := range []TypedSourcePropertySelector{{Kind: "attributes", Group: "selector"}, {Kind: "representation_selector", Group: "property"}, {Kind: "attributes", Group: "facetKey"}} {
		if _, err := SelectSourceProperty(p, bad); err == nil {
			t.Fatalf("accepted selector %+v", bad)
		}
	}
	for _, kind := range []string{"column", "api_field", "event_field", "dto"} {
		p.Kind = kind
		if _, err := SelectSourceProperty(p, selector); err == nil {
			t.Fatalf("selector leaked to %s", kind)
		}
	}
}

func TestRepresentationMixedMappingKeepsEventValidation(t *testing.T) {
	t.Parallel()
	const parent = "11111111-1111-4111-8111-111111111111"
	const field = "22222222-2222-4222-8222-222222222222"
	const event = "33333333-3333-4333-8333-333333333333"
	const consumer = "44444444-4444-4444-8444-444444444444"
	const route = "55555555-5555-4555-8555-555555555555"
	const message = "66666666-6666-4666-8666-666666666666"
	const channel = "77777777-7777-4777-8777-777777777777"
	const mapping = "88888888-8888-4888-8888-888888888888"
	attrs := sourceContractAttrs(t, `{"sources":[{"kind":"event_field","nodeId":"`+event+`","endpointId":"`+consumer+`","routeId":"`+route+`"}],"destination":{"kind":"representation_field","nodeId":"`+field+`"},"transform":{"kind":"rename","description":"Deserialize","redacted":false},"analysisStatus":"complete","gaps":[]}`)
	g := &graphCandidate{Nodes: []Node{{ID: parent, Kind: "dto"}, {ID: field, Kind: "representation_field", ParentID: new(parent)}, {ID: event, Kind: "event_field", ParentID: new(message)}, {ID: message, Kind: "message"}, {ID: consumer, Kind: "consumer"}, {ID: channel, Kind: "channel"}, {ID: mapping, Kind: "field_mapping", ParentID: new(parent), Attributes: attrs}}, Edges: []Edge{{ID: route, Kind: "delivered_to", From: channel, To: consumer, Attributes: sourceContractAttrs(t, `{"messageId":"`+message+`","deliveryStatus":"declared"}`)}, {ID: "99999999-9999-4999-8999-999999999999", Kind: "contains", From: parent, To: mapping}}}
	d := []ImportDiagnostic{}
	if err := validateLineageGraphRules(t.Context(), ComposedProfile, nil, g, &d); err != nil || len(d) != 0 {
		t.Fatalf("valid mixed mapping: %v %+v", err, d)
	}
	g.Edges[0].Attributes["deliveryStatus"] = jsontext.Value(`"unknown"`)
	d = nil
	if err := validateLineageGraphRules(t.Context(), ComposedProfile, nil, g, &d); err != nil || len(d) == 0 {
		t.Fatalf("complete mapping over unknown route admitted: %v %+v", err, d)
	}
	g.Edges[0].Attributes["deliveryStatus"] = jsontext.Value(`"declared"`)
	attrs["transport"] = jsontext.Value(`{"emitsEdgeId":"` + route + `","deliveryEdgeId":"` + route + `"}`)
	d = nil
	if err := validateLineageGraphRules(t.Context(), ComposedProfile, nil, g, &d); err != nil || len(d) == 0 {
		t.Fatalf("invalid mixed transport accepted: %v %+v", err, d)
	}
}
