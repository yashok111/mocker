package backendmodel

import "testing"

func TestArchitectureSource5ViewValidationHasBoundedReads(t *testing.T) {
	t.Parallel()
	r, base, _ := effectiveFiveRelationalFixture(t)
	doc := diagramTestDocument(base.Revision.ID)
	d, err := r.CreateDiagram(t.Context(), base.Project.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "diagram"})
	if err != nil {
		t.Fatal(err)
	}
	state := DiagramViewState{Diagram: d.Pin, Level: "context", RootID: doc.Payload.PrimarySystemID, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}
	reader, writer := diagramCountReads(t, r)
	view, err := r.CreateDiagramView(t.Context(), base.Project.ID, DiagramCreateViewInput{Name: "Overview", State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.reads.Load(); n > 20 {
		t.Errorf("create used %d SQL reads; source5 view validation must use bulk reads", n)
	}
	reader.reads.Store(0)
	writer.reads.Store(0)
	input := DiagramSaveViewInput{Name: "Updated overview", State: state, ExpectedVersion: view.Version, IdempotencyKey: "save"}
	updated, err := r.SaveDiagramView(t.Context(), base.Project.ID, view.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.reads.Load(); n > 20 {
		t.Errorf("save used %d SQL reads; source5 view validation must use bulk reads", n)
	}
	replay, err := r.SaveDiagramView(t.Context(), base.Project.ID, view.ID, input)
	if err != nil || replay.Version != updated.Version || replay.State.Diagram != d.Pin {
		t.Fatalf("exact replay changed view pin/version: %+v %v", replay, err)
	}
	input.IdempotencyKey = "stale-cas"
	if _, err := r.SaveDiagramView(t.Context(), base.Project.ID, view.ID, input); err == nil {
		t.Fatal("stale view CAS accepted")
	}
	for _, bad := range []DiagramViewState{
		{Diagram: d.Pin, Level: "context", Origin: "all", Selection: &DiagramSelection{Type: "element", ID: "absent"}, Positions: []DiagramPosition{}, CollapsedIDs: []string{}},
		{Diagram: d.Pin, Level: "context", Origin: "all", Positions: []DiagramPosition{{ID: "absent", X: 1, Y: 2}}, CollapsedIDs: []string{}},
	} {
		if err := validateDiagramViewRead(t.Context(), r.db.R, base.Project.ID, "Invalid layout", bad); err == nil {
			t.Fatal("foreign layout member accepted")
		}
	}
}

func TestDiagramViewsKeepExactPinAndIndependentCatalog(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "views")
	doc := diagramTestDocument(p.CurrentRevisionID)
	d, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "create"})
	if err != nil {
		t.Fatal(err)
	}
	state := DiagramViewState{Diagram: d.Pin, Level: "context", RootID: doc.Payload.PrimarySystemID, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}
	v1, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "Original", State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	v2, err := r.SaveDiagramView(t.Context(), p.ID, v1.ID, DiagramSaveViewInput{Name: "Layout", State: state, ExpectedVersion: 1, IdempotencyKey: "layout"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Label = "New mapping"
	d2, err := r.SaveDiagram(t.Context(), p.ID, d.Pin.ID, DiagramSaveInput{Document: doc, ExpectedVersion: 1, IdempotencyKey: "advance"})
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range []*DiagramView{v1, v2} {
		got, err := r.GetDiagramView(t.Context(), p.ID, v.ID, v.Version)
		if err != nil || got.State.Diagram != d.Pin {
			t.Fatalf("view repinned: %+v %v", got, err)
		}
	}
	state.Diagram = d2.Pin
	if _, err := r.SaveDiagramView(t.Context(), p.ID, v1.ID, DiagramSaveViewInput{Name: "Repin", State: state, ExpectedVersion: 2, IdempotencyKey: "repin"}); err == nil {
		t.Fatal("view silently repinned")
	}
	list, err := r.ListDiagrams(t.Context(), p.ID, DiagramListInput{Limit: 1})
	if err != nil || len(list.Items) != 1 || list.Items[0].Pin != d2.Pin {
		t.Fatalf("catalog: %+v %v", list, err)
	}
	views, err := r.ListDiagramViews(t.Context(), p.ID, DiagramListInput{Limit: 1})
	if err != nil || len(views.Items) != 1 || views.Items[0].Version != 2 {
		t.Fatalf("view catalog: %+v %v", views, err)
	}
}
