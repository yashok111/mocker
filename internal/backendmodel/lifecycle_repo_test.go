package backendmodel

import (
	"encoding/json/v2"
	"testing"
)

func TestLifecycleHistoricalFork(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "lifecycle-history")
	s := runtimeQueryFixture(t)
	runtimeQueryAddRelationalFacets(t, s)
	s.Revision.ProjectID = p.ID
	s.Revision.ParentRevisionID = new(p.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	d := lifecycleFixture(t)
	d.Target = BackendReadTarget{RevisionID: s.Revision.ID}
	d.Lifecycle.Entity.ID = runtimeQueryID(10)
	d.Lifecycle.StateFields[0].ID = runtimeQueryID(11)
	for i := range d.Lifecycle.Transitions {
		d.Lifecycle.Transitions[i].Triggers[0].ID = runtimeQueryID(1)
	}
	d.Lifecycle.Rules[0].Trigger.ID = runtimeQueryID(1)
	input := DiagramCreateInput{Document: d, IdempotencyKey: "lifecycle-create"}
	v, err := r.CreateDiagram(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v)
	view, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "Original lifecycle", State: DiagramViewState{Diagram: v.Pin, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	d.Lifecycle.States[0].Label = "Created v2"
	next, err := r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: d, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	old, err := r.GetDiagram(t.Context(), p.ID, v.Pin)
	if err != nil {
		t.Fatal(err)
	}
	if old.Document.Lifecycle.States[0].Label == "Created v2" {
		t.Fatal("old pin changed")
	}
	gotView, err := r.GetDiagramView(t.Context(), p.ID, view.ID, view.Version)
	if err != nil || gotView.State.Diagram != v.Pin {
		t.Fatal("view repinned", err)
	}
	input.Document = old.Document
	replay, err := r.CreateDiagram(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(replay)
	if string(raw) != string(again) {
		t.Fatal("receipt changed")
	}
	fork, err := r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: next.Pin, Target: BackendReadTarget{RevisionID: p.CurrentRevisionID}, Reason: "Source removed", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if len(fork.Gaps) == 0 || fork.Document.Lifecycle.States[0].ID != next.Document.Lifecycle.States[0].ID {
		t.Fatal("historical mappings lost")
	}
	compare, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v.Pin, After: next.Pin, Limit: 100})
	if err != nil || len(compare.Items) != 1 {
		t.Fatal("comparison", err)
	}
	q, err := r.QueryDiagram(t.Context(), p.ID, DiagramQueryInput{Pin: v.Pin, Origin: "all", Section: "links", Limit: 100})
	if err != nil || q.Total != 2 {
		t.Fatal("links", err)
	}
	before := diagramTableCounts(t, db)
	bad := fork.Document
	bad.Lifecycle.Entity.ID = runtimeQueryID(999)
	if _, err = r.SaveDiagram(t.Context(), p.ID, fork.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: bad, IdempotencyKey: "foreign"}); err == nil {
		t.Fatal("new foreign ref accepted")
	}
	if diagramTableCounts(t, db) != before {
		t.Fatal("invalid save wrote rows")
	}
}
