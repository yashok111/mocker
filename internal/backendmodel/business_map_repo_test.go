package backendmodel

import (
	"encoding/json/v2"
	"slices"
	"testing"
)

func TestBusinessMapEventIdentity(t *testing.T) {
	r, db := testRepo(t)
	p := createProject(t, r, "business-identity")
	s := runtimeQueryFixture(t)
	runtimeQueryAddNode(t, s, 30, "message", 0, nil)
	runtimeQueryAddNode(t, s, 31, "message", 0, nil)
	s.Revision.ProjectID = p.ID
	s.Revision.ParentRevisionID = new(p.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	d := businessMapDecode(t, businessMapRaw(t))
	d.Target = BackendReadTarget{RevisionID: s.Revision.ID}
	event := &d.BusinessMap.Elements[2]
	event.Refs = []DiagramRef{{Kind: "record", RecordType: "node", ID: runtimeQueryID(30)}, {Kind: "record", RecordType: "node", ID: runtimeQueryID(31)}}
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Document.BusinessMap.Elements) != 8 {
		t.Fatal("transport refs multiplied events")
	}
	before := diagramTableCounts(t, db)
	q := DiagramQueryInput{Pin: v.Pin, Section: "members", SubjectID: event.ID, Origin: "all", Limit: 1}
	a, err := r.QueryDiagram(t.Context(), p.ID, q)
	if err != nil || a.Total != 2 || len(a.Items) != 1 || a.NextCursor == "" {
		t.Fatalf("members: %+v %v", a, err)
	}
	q.Cursor = a.NextCursor
	b, err := r.QueryDiagram(t.Context(), p.ID, q)
	if err != nil || len(b.Items) != 1 || b.Items[0].Member.Ref.ID == a.Items[0].Member.Ref.ID {
		t.Fatal("cursor lost identity", err)
	}
	q.Search = "changed"
	if _, err = r.QueryDiagram(t.Context(), p.ID, q); err == nil {
		t.Fatal("cursor filter mutation accepted")
	}
	if before != diagramTableCounts(t, db) {
		t.Fatal("query writes")
	}
	view, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "Old pin", State: DiagramViewState{Diagram: v.Pin, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	d.BusinessMap.Elements[0].Label = "Changed actor"
	input := DiagramSaveInput{ExpectedVersion: 1, Document: d, IdempotencyKey: "save"}
	v2, err := r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(v2)
	d3, _ := normalizeDiagram(v2.Document)
	d3.BusinessMap.Elements[0].Label = "Third actor"
	if _, err = r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 2, Document: d3, IdempotencyKey: "save3"}); err != nil {
		t.Fatal(err)
	}
	replay, err := r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	again, _ := json.Marshal(replay)
	if string(raw) != string(again) {
		t.Fatal("receipt replay changed")
	}
	old, err := r.GetDiagramView(t.Context(), p.ID, view.ID, 1)
	if err != nil || old.State.Diagram != v.Pin {
		t.Fatal("historical view changed", err)
	}
	cmp, err := r.CompareDiagrams(t.Context(), p.ID, DiagramCompareInput{Before: v.Pin, After: v2.Pin, Limit: 100})
	if err != nil || len(cmp.Items) != 1 || !slices.Equal(cmp.Items[0].Fields, []string{"label"}) {
		t.Fatalf("comparison: %+v %v", cmp, err)
	}
	fork, err := r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: v.Pin, Target: BackendReadTarget{RevisionID: p.CurrentRevisionID}, Reason: "Missing new source", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(fork.Gaps, func(g DiagramGap) bool {
		return g.SubjectID == event.ID && len(g.Code) > 15 && g.Code[:15] == "historical_ref:"
	}) {
		t.Fatal("missing historical gap")
	}
	bad, _ := normalizeDiagram(fork.Document)
	bad.BusinessMap.Elements[2].Refs = append(bad.BusinessMap.Elements[2].Refs, DiagramRef{Kind: "record", RecordType: "node", ID: runtimeQueryID(999)})
	if _, err = r.SaveDiagram(t.Context(), p.ID, fork.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: bad, IdempotencyKey: "foreign"}); err == nil {
		t.Fatal("foreign ref admitted")
	}
	other := createProject(t, r, "other")
	if _, err = r.GetDiagram(t.Context(), other.ID, v.Pin); err == nil {
		t.Fatal("cross-project pin admitted")
	}
}

func TestBusinessMapArchitectureMembership(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "business-c4")
	a := diagramTestDocument(p.CurrentRevisionID)
	av, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: a, IdempotencyKey: "architecture"})
	if err != nil {
		t.Fatal(err)
	}
	d := businessMapDecode(t, businessMapRaw(t))
	d.Target = a.Target
	d.BusinessMap.Architecture = &av.Pin
	d.BusinessMap.Elements[0].ArchitectureElementID = a.Payload.PrimarySystemID
	v, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "map"})
	if err != nil {
		t.Fatal(err)
	}
	d.BusinessMap.Elements[0].ArchitectureElementID = runtimeQueryID(999)
	if _, err = r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: d, IdempotencyKey: "bad-member"}); err == nil {
		t.Fatal("unknown C4 member admitted")
	}
	d, _ = normalizeDiagram(v.Document)
	d.BusinessMap.Architecture = nil
	d.BusinessMap.Elements[0].ArchitectureElementID = ""
	if _, err = r.SaveDiagram(t.Context(), p.ID, v.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: d, IdempotencyKey: "dependency"}); err == nil {
		t.Fatal("dependency changed in place")
	}
	s := runtimeQueryFixture(t)
	s.Revision.ProjectID = p.ID
	s.Revision.ParentRevisionID = new(p.CurrentRevisionID)
	runtimeQueryPersist(t, r, s)
	if _, err = r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: v.Pin, Target: BackendReadTarget{RevisionID: s.Revision.ID}, Reason: "new target", IdempotencyKey: "missing-pin"}); err == nil {
		t.Fatal("new target without C4 pin admitted")
	}
	d, _ = normalizeDiagram(v.Document)
	d.Target = BackendReadTarget{RevisionID: s.Revision.ID}
	if _, err = r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: d, IdempotencyKey: "mixed"}); err == nil {
		t.Fatal("different targetHash C4 admitted")
	}
	a.Target = d.Target
	a.Payload.Elements[0].ID = runtimeQueryID(998)
	a.Payload.PrimarySystemID = runtimeQueryID(998)
	av2, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: a, IdempotencyKey: "new-c4"})
	if err != nil {
		t.Fatal(err)
	}
	fork, err := r.ForkDiagram(t.Context(), p.ID, DiagramForkInput{Source: v.Pin, Target: d.Target, Architecture: &av2.Pin, Reason: "new C4", IdempotencyKey: "fork"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(fork.Gaps, func(g DiagramGap) bool { return g.Code == "unresolved_membership" }) {
		t.Fatal("historical C4 missing gap")
	}
	if _, err = r.SaveDiagram(t.Context(), p.ID, fork.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: fork.Document, IdempotencyKey: "retain"}); err != nil {
		t.Fatal(err)
	}
}
