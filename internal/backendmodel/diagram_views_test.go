package backendmodel

import "testing"

func TestDiagramViewsKeepExactPinAndIndependentCatalog(t *testing.T) {
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
