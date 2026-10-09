package backendmodel

import (
	"encoding/json/v2"
	"reflect"
	"testing"
)

func TestArchitectureGapClassificationFiltersDetailsNotSummary(t *testing.T) {
	r, pid, q := storedArchitectureFixture(t, 4, 6, 6)
	q.Section, q.Limit = "gaps", 1
	q.GapScope = &ArchitectureGapScope{Format: "exact-node-scope-v1", NodeIDs: []string{"20000000-0000-4000-8000-000000000002"}}
	all, err := r.QueryDiagram(t.Context(), pid, q)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(q)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(raw, &fields); err != nil {
		t.Fatal(err)
	}
	fields["gapClassification"] = "boundary"
	raw, err = json.Marshal(fields)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &q); err != nil {
		t.Fatalf("classification query rejected: %v", err)
	}
	count := 0
	for {
		page, err := r.QueryDiagram(t.Context(), pid, q)
		if err != nil {
			t.Fatal(err)
		}
		if page.Total != 2 || !reflect.DeepEqual(page.GapSummary, all.GapSummary) {
			t.Fatalf("filter changed full summary or selected total: %+v", page)
		}
		for _, row := range page.Items {
			if row.Gap == nil || row.Gap.Scope != "boundary" {
				t.Fatal("another scope leaked into filtered details")
			}
			count++
		}
		q.Cursor = page.NextCursor
		if q.Cursor == "" {
			break
		}
		other := q
		other.GapScope = nil
		if _, err := r.QueryDiagram(t.Context(), pid, other); err == nil {
			t.Fatal("classification without exact scope accepted")
		}
	}
	if count != 2 {
		t.Fatalf("read %d boundary details, want 2", count)
	}
}

func TestArchitectureGapScopeIsExplicitPinnedAndComplete(t *testing.T) {
	r, pid, q := storedArchitectureFixture(t, 4, 6, 6)
	q.Section, q.Limit = "gaps", 1
	q.GapScope = &ArchitectureGapScope{Format: "exact-node-scope-v1", NodeIDs: []string{"20000000-0000-4000-8000-000000000002"}}
	first, err := r.QueryDiagram(t.Context(), pid, q)
	if err != nil {
		t.Fatal(err)
	}
	if first.GapSummary.ByScope["boundary"] != 2 {
		t.Fatalf("classification: %+v", first.GapSummary)
	}
	cursor := first.NextCursor
	q.GapScope.NodeIDs = []string{"20000000-0000-4000-8000-000000000001", "20000000-0000-4000-8000-000000000002"}
	q.Cursor = cursor
	_, err = r.QueryDiagram(t.Context(), pid, q)
	assertFault(t, err, "backend_diagram_cursor_mismatch")
	q.Cursor = ""
	both, err := r.QueryDiagram(t.Context(), pid, q)
	if err != nil {
		t.Fatal(err)
	}
	if both.GapSummary.ByScope["in_scope"] != 2 {
		t.Fatal("cache ignored explicit scope")
	}
	q.GapScope.NodeIDs = []string{"20000000-0000-4000-8000-000000000003"}
	outside, err := r.QueryDiagram(t.Context(), pid, q)
	if err != nil || outside.GapSummary.ByScope["unrelated"] != 2 {
		t.Fatalf("outside scope: %+v %v", outside, err)
	}
	q.GapScope = nil
	legacy, err := r.QueryDiagram(t.Context(), pid, q)
	if err != nil || len(legacy.GapSummary.ByScope) != 0 || legacy.GapSummary.Total != first.GapSummary.Total {
		t.Fatal("default changed or gap union lost")
	}
}

func TestArchitectureGapScopeDoesNotConfuseDiagramAndSourceSubjects(t *testing.T) {
	p := &architectureProjection{gaps: []DiagramGap{{SubjectID: "shared-id", Code: "authored_unresolved"}, {SubjectID: "shared-id", Code: "unresolved_membership"}, {SubjectID: "aggregate", Code: "collapsed_internal"}}}
	graph := &EffectiveGraphSnapshot{State: RevisionState{Nodes: []Node{{ID: "selected"}}, Edges: []Edge{{ID: "shared-id", From: "other", To: "another"}}}}
	if err := qualifyArchitectureGaps(t.Context(), p, graph, &ArchitectureGapScope{NodeIDs: []string{"selected"}}); err != nil {
		t.Fatal(err)
	}
	if p.gaps[0].Scope != "unqualified" || p.gaps[1].Scope != "unrelated" || p.gaps[2].Scope != "collapsed_internal" {
		t.Fatal(p.gaps)
	}
}
