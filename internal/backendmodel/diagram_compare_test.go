package backendmodel

import (
	"slices"
	"testing"
)

func TestDiagramCompareStableIdentity(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "compare")
	doc := diagramTestDocument(p.CurrentRevisionID)
	before, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	empty, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: before.Pin, After: before.Pin, Limit: 100})
	if err != nil || len(empty.Items) != 0 {
		t.Fatalf("empty comparison: %+v %v", empty, err)
	}
	doc.Payload.Elements[0].Label = "Renamed"
	after, err := r.SaveDiagram(t.Context(), p.ID, before.Pin.ID, DiagramSaveInput{Document: doc, ExpectedVersion: 1, IdempotencyKey: "save"})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: before.Pin, After: after.Pin, Limit: 100})
	if err != nil {
		t.Fatal(err)
	}
	if len(diff.Items) != 1 || diff.Items[0].Kind != "changed" || len(diff.Items[0].Fields) != 1 || diff.Items[0].Fields[0] != "label" {
		t.Fatalf("diff: %+v", diff)
	}
	historical, err := r.GetDiagram(t.Context(), p.ID, before.Pin)
	if err != nil || historical.Document.Payload.Elements[0].Label != "Orders" {
		t.Fatal("before-side witness lost")
	}
}

func TestDiagramCompareReportsEffectiveDependencyRemoval(t *testing.T) {
	before, g, expected := ordersProjectionFixture(t)
	after := *before
	after.Document.Target = BackendReadTarget{RevisionID: "10000000-0000-4000-8000-000000000091"}
	after.Pin.Version = 2
	after.Pin.ContentHash, _ = requestDigest(after.Document)
	changed := *g
	changed.State = g.State
	changed.State.Edges = append([]Edge{}, g.State.Edges[1:]...)
	changed.Pins = g.Pins
	changed.Pins.TargetHash = "dddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddddd"
	changed.Pins.BaseRevisionID = after.Document.Target.RevisionID
	after.TargetHash = changed.Pins.TargetHash
	oldRows, err := diagramComparisonRows(t.Context(), before, g, "")
	if err != nil {
		t.Fatal(err)
	}
	newRows, err := diagramComparisonRows(t.Context(), &after, &changed, "")
	if err != nil {
		t.Fatal(err)
	}
	changes := diagramDifferences(oldRows, newRows)
	found := false
	for _, change := range changes {
		if change.Kind == "changed" && slices.Contains(change.Fields, "members") {
			q := DiagramQueryInput{Pin: before.Pin, Level: "context", RootID: before.Document.Payload.PrimarySystemID, Origin: "all", Section: "members", SubjectID: change.ID, Limit: 100}
			page, err := ProjectArchitecture(t.Context(), before, g, q)
			if err != nil {
				t.Fatal(err)
			}
			if page.Total == 2 && page.Items[0].Member.Ref.ID == expected.PaymentMembers[0] {
				found = true
			}
		}
	}
	if !found {
		t.Fatalf("removed dependency invisible in source/fork comparison: %+v", changes)
	}
}
