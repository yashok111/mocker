package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestAnalysisCompleteTraversalWithPartialCoverage(t *testing.T) {
	before, after := supportedGraph(nil, nil), supportedGraph(nil, nil)
	before.State.Revision.Coverage.Status = "partial"
	after.State.Revision.Coverage.Status = "partial"
	out, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	m := out.Snapshot.Manifest
	if !m.Complete || len(m.TruncationReasons) != 0 || m.Verdict != "unknown" || m.RuntimeVerified {
		t.Errorf("source coverage conflated with traversal completion: %+v", m)
	}
	foundManifest, foundRecord := false, false
	for _, gap := range m.Gaps {
		foundManifest = foundManifest || gap.Code == "source_coverage"
	}
	for _, chunk := range out.Snapshot.Chunks {
		if chunk.Section != "gaps" {
			continue
		}
		var records []ResultRecord
		if err = json.Unmarshal(chunk.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, record := range records {
			var gap Diagnostic
			if err = json.Unmarshal(record.Detail, &gap); err != nil {
				t.Fatal(err)
			}
			foundRecord = foundRecord || gap.Code == "source_coverage"
		}
	}
	if !foundManifest || !foundRecord {
		t.Errorf("source gap missing from manifest=%v or paged records=%v", foundManifest, foundRecord)
	}
}

func graphNode(attrs string) backendmodel.EffectiveGraphSnapshot {
	return backendmodel.EffectiveGraphSnapshot{State: backendmodel.RevisionState{Nodes: []backendmodel.Node{{ID: revisionID, Kind: "column", Name: "amount", Attributes: map[string]jsontext.Value{"facets": []byte(attrs)}}}}}
}
func TestAnalysisDiffExactFacetPaths(t *testing.T) {
	before := graphNode(`{"sql":{"nativeType":{"status":"known","value":"numeric"},"ordinal":{"status":"known","value":9007199254740993},"evidenceIds":["old"]}}`)
	after := graphNode(`{"sql":{"evidenceIds":["new"],"ordinal":{"status":"known","value":9007199254740992},"nativeType":{"status":"known","value":"numeric"}}}`)
	changes, err := structuralChanges(t.Context(), &before, &after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatalf("separate proof and behavior facets: %+v", changes)
	}
	want := map[string][]string{"behavior": {"/attributes/facets/sql/ordinal/value"}, "proof": {"/attributes/facets/sql/evidenceIds"}}
	for _, c := range changes {
		if !reflect.DeepEqual(c.Paths, want[c.Facet]) {
			t.Fatalf("%s: %v", c.Facet, c.Paths)
		}
		if c.Object.ID != revisionID {
			t.Fatal(c.Object)
		}
	}
}
func TestAnalysisDiffEvidenceAndCriteriaAreNotBehavior(t *testing.T) {
	before := graphNode(`{"sql":{"evidenceIds":["old"]}}`)
	after := graphNode(`{"sql":{"evidenceIds":["new"]}}`)
	after.Criteria = []backendmodel.ChangeCriterion{{Key: "check", Kind: "object_exists", ID: revisionID, RecordType: "node", ObjectKind: "column", Required: true, Description: "Column required"}}
	changes, err := structuralChanges(t.Context(), &before, &after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 2 {
		t.Fatal(changes)
	}
	for _, c := range changes {
		if c.Facet == "behavior" {
			t.Fatal("fabricated behavior seed", c)
		}
	}
}
func TestAnalysisDiffAtomicArraysAndMapOrder(t *testing.T) {
	before := graphNode(`{"sql":{"columnPairs":[{"fromColumnId":"a","toColumnId":"b"},{"fromColumnId":"c","toColumnId":"d"}]}}`)
	after := graphNode(`{"sql":{"columnPairs":[{"toColumnId":"d","fromColumnId":"c"},{"toColumnId":"b","fromColumnId":"a"}]}}`)
	changes, err := structuralChanges(t.Context(), &before, &after)
	if err != nil {
		t.Fatal(err)
	}
	if len(changes) != 1 || !reflect.DeepEqual(changes[0].Paths, []string{"/attributes/facets/sql/columnPairs"}) {
		t.Fatal(changes)
	}
	before = graphNode(`{"sql":{"nativeType":{"status":"known","value":"integer"}}}`)
	after = graphNode(`{"sql":{"nativeType":{"value":"integer","status":"known"}}}`)
	changes, err = structuralChanges(t.Context(), &before, &after)
	if err != nil || len(changes) != 0 {
		t.Fatalf("map order produced difference %v %v", changes, err)
	}
}
