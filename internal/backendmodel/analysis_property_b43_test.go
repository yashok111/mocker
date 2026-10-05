package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"strings"
	"testing"
	"uuid"
)

func TestB43PropertyReferenceTranslation(t *testing.T) {
	old, next, other := uuid.NewV7().String(), uuid.NewV7().String(), uuid.NewV7().String()
	mapping := map[string]string{old: next, other: other}
	attrs := fmt.Sprintf(`{"analysisStatus":"complete","gaps":[],"sources":[{"kind":"column","nodeId":%q,"facetKey":"sql"}],"destination":{"kind":"column","nodeId":%q,"facetKey":"sql"},"transform":{"kind":"copy","description":%q,"redacted":false}}`, old, other, old)
	facet := fmt.Sprintf(`{"facets":{"sql":{"sourceKind":"sql","dialect":"postgresql","analysisStatus":"complete","gaps":[],"evidenceIds":[],"constraintKind":"unique","columnIds":[%q,%q],"expression":{"status":"known","value":null},"nativeDefinition":%q,"deferrable":{"status":"known","value":false},"initiallyDeferred":{"status":"known","value":false}}}}`, old, other, old)
	cases := []struct {
		name, kind, attributes string
		selector               TypedSourcePropertySelector
		value, want            string
	}{
		{"parent", "handler", `{}`, TypedSourcePropertySelector{Kind: "parent"}, fmt.Sprintf(`%q`, old), fmt.Sprintf(`%q`, next)},
		{"mapping sources", "field_mapping", attrs, TypedSourcePropertySelector{Kind: "mapping_sources"}, fmt.Sprintf(`[{"kind":"column","nodeId":%q,"facetKey":"sql"}]`, old), fmt.Sprintf(`[{"kind":"column","nodeId":%q,"facetKey":"sql"}]`, next)},
		{"mapping destination", "field_mapping", attrs, TypedSourcePropertySelector{Kind: "mapping_destination"}, fmt.Sprintf(`{"kind":"column","nodeId":%q,"facetKey":"sql"}`, old), fmt.Sprintf(`{"kind":"column","nodeId":%q,"facetKey":"sql"}`, next)},
		{"ordered columns", "constraint", facet, TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "sql", Group: "columnIds"}, fmt.Sprintf(`[%q,%q]`, old, other), fmt.Sprintf(`[%q,%q]`, next, other)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var attrs map[string]jsontext.Value
			if err := json.Unmarshal([]byte(tc.attributes), &attrs); err != nil {
				t.Fatal(err)
			}
			payload := SourceAssertionPayload{RecordType: "node", Kind: tc.kind, Attributes: attrs}
			value, ok, err := TranslateAnalysisSourceProperty(payload, tc.selector, SourcePropertyValue{Present: true, Value: []byte(tc.value)}, mapping)
			if err != nil || !ok {
				candidate, applyErr := ApplySourceProperty(payload, tc.selector, SourcePropertyValue{Present: true, Value: []byte(tc.value)})
				_, refsErr := sourcePayloadReferenceSites(candidate)
				t.Fatalf("translation unavailable %v apply=%v refs=%v", err, applyErr, refsErr)
			}
			a, _ := canonicalJSON(value.Value)
			b, _ := canonicalJSON(jsontext.Value(tc.want))
			if !bytes.Equal(a, b) {
				t.Fatalf("got %s want %s", a, b)
			}
			_, ok, err = TranslateAnalysisSourceProperty(payload, tc.selector, SourcePropertyValue{Present: true, Value: []byte(tc.value)}, map[string]string{other: other})
			if err != nil || ok {
				t.Fatalf("unmapped reference became known %v", err)
			}
		})
	}
	// UUID-shaped native strings are opaque, never reference substitutions.
	payload := SourceAssertionPayload{RecordType: "node", Kind: "handler", Attributes: map[string]jsontext.Value{"qualifiedName": []byte(fmt.Sprintf(`%q`, old))}}
	value, ok, err := TranslateAnalysisSourceProperty(payload, TypedSourcePropertySelector{Kind: "attributes", Group: "qualifiedName"}, SourcePropertyValue{Present: true, Value: []byte(fmt.Sprintf(`%q`, old))}, mapping)
	if err != nil || !ok || !strings.Contains(string(value.Value), old) {
		t.Fatalf("opaque string rewritten %s %v", value.Value, err)
	}
}
