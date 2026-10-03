package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestEventsLineageStrictContextualShapes(t *testing.T) {
	ref := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "emit", "routeKey": "emission"}
	a := runtimeAttrs(t, map[string]any{"sources": []any{ref}, "destination": ref, "transform": map[string]any{"kind": "copy", "description": "declared mapping", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}, "transport": map[string]any{"emitsEdgeKey": "emission", "deliveryEdgeKey": "delivery"}})
	if err := validateEventsAttributes("field_mapping", a, false, false); err != nil {
		t.Fatalf("source5 contextual mapping rejected: %v", err)
	}
	if err := validateLineageAttributes("field_mapping", a, false, false); err == nil {
		t.Fatal("source4 accepted contextual mapping")
	}
	var decoded ImportLineageMappingAttributes
	if err := json.Unmarshal(relationalRaw(t, a), &decoded); err != nil {
		t.Fatalf("context-aware union decoder: %v", err)
	}
	for _, key := range []string{"nodeKey", "endpointKey", "routeKey"} {
		bad := map[string]any{"kind": "event_field", "nodeKey": "field", "endpointKey": "emit", "routeKey": "emission"}
		delete(bad, key)
		broken := runtimeAttrs(t, map[string]any{"sources": []any{bad}, "destination": ref, "transform": map[string]any{"kind": "copy", "description": "declared mapping", "redacted": false}, "analysisStatus": "complete", "gaps": []string{}})
		if err := validateEventsAttributes("field_mapping", broken, false, false); err == nil {
			t.Fatalf("accepted missing %s", key)
		}
	}
}

func TestEventsLineageSource4DecoderBoundaryAndOldBytes(t *testing.T) {
	id := lineageQueryID(1)
	raw := relationalRaw(t, map[string]any{"revisionId": id, "direction": "forward", "seed": map[string]any{"kind": "event_field", "nodeId": lineageQueryID(100), "endpointId": lineageQueryID(101), "routeId": lineageQueryID(102)}})
	var in LineageQueryInput
	if err := json.Unmarshal(raw, &in); err != nil {
		t.Fatalf("union input decode: %v", err)
	}
	s := lineageQueryState(t, 1)
	s.Revision.ID = id
	if _, err := projectLineage(t.Context(), s, in); err == nil {
		t.Fatal("source4 query accepted event ref")
	}
	ordinary := LineageValueRef{Kind: "port", NodeID: id, Collection: "inputs", PortKey: "x"}
	serialized, err := json.Marshal(ordinary)
	if err != nil {
		t.Fatal(err)
	}
	if string(serialized) != `{"kind":"port","nodeId":"00000000-0000-4000-8000-000000000001","collection":"inputs","portKey":"x"}` {
		t.Fatalf("old ref bytes changed: %s", serialized)
	}
	a := runtimeAttrs(t, map[string]any{"sources": []any{map[string]any{"kind": "event_field", "nodeId": id, "endpointId": lineageQueryID(2), "routeId": lineageQueryID(3)}}, "destination": ordinary, "transform": LineageTransform{Kind: "copy", Description: "explicit"}, "analysisStatus": "complete", "gaps": []string{}})
	if _, err := decodeLineageMapping(a); err == nil {
		t.Fatal("strict4 decoder accepted new persisted contextual mapping")
	}
	if _, err := decodeLineageMappingForSchema(a, EventsSchemaVersion); err != nil {
		t.Fatal("strict5 decoder rejected contextual mapping", err)
	}
}
