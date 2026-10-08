package backendmodel

import "testing"

func TestArchitectureNavigationPinsExistingDetailAndRejectsWrongTarget(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "navigation")
	child, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: diagramTestDocument(p.CurrentRevisionID), IdempotencyKey: "child"})
	if err != nil {
		t.Fatal(err)
	}
	doc := diagramTestDocument(p.CurrentRevisionID)
	doc.Payload.Elements[0].Navigation = []ArchitectureNavigation{{Format: ArchitectureNavigationVersion, Kind: "diagram", Label: "Детали", Diagram: &child.Pin, Level: "context", RootID: child.Document.Payload.PrimarySystemID}}
	parent, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "parent"})
	if err != nil {
		t.Fatal(err)
	}
	changed := child.Document
	changed.Payload.Elements[0].Label = "New head"
	_, err = r.SaveDiagram(t.Context(), p.ID, child.Pin.ID, DiagramSaveInput{ExpectedVersion: 1, Document: changed, IdempotencyKey: "child-update"})
	if err != nil {
		t.Fatal(err)
	}
	read, err := r.GetDiagram(t.Context(), p.ID, parent.Pin)
	if err != nil {
		t.Fatal(err)
	}
	if *read.Document.Payload.Elements[0].Navigation[0].Diagram != child.Pin {
		t.Fatal("navigation advanced to target head")
	}
	other := createProject(t, r, "other-navigation")
	foreign, err := r.CreateDiagram(t.Context(), other.ID, DiagramCreateInput{Document: diagramTestDocument(other.CurrentRevisionID), IdempotencyKey: "foreign"})
	if err != nil {
		t.Fatal(err)
	}
	doc.Payload.Elements[0].Navigation[0].Diagram = &foreign.Pin
	if _, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "bad"}); err == nil {
		t.Fatal("foreign diagram navigation admitted")
	}
}

func TestArchitectureNavigationRejectsInventedFlow(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	p := createProject(t, r, "navigation-flow")
	doc := diagramTestDocument(p.CurrentRevisionID)
	doc.Payload.Elements[0].Navigation = []ArchitectureNavigation{{Format: ArchitectureNavigationVersion, Kind: "flow", Label: "Логика", FlowID: p.CurrentRevisionID, Target: new(doc.Target)}}
	if _, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "bad-flow"}); err == nil {
		t.Fatal("invented Flow admitted")
	}
}

func TestArchitectureFlowNavigationForkPreservesExactTarget(t *testing.T) {
	oldTarget := BackendReadTarget{RevisionID: "10000000-0000-4000-8000-000000000090"}
	doc := diagramTestDocument("10000000-0000-4000-8000-000000000091")
	flow := "10000000-0000-4000-8000-000000000092"
	doc.Payload.Elements[0].Navigation = []ArchitectureNavigation{{Format: ArchitectureNavigationVersion, Kind: "flow", Label: "Flow", FlowID: flow, Target: &oldTarget}}
	previous := &DiagramVersion{Document: doc}
	previous.Document.Target = oldTarget
	graph := &EffectiveGraphSnapshot{State: RevisionState{Nodes: []Node{{ID: flow, Kind: "flow"}}}}
	gaps, err := resolveArchitectureNavigation(t.Context(), nil, "project", graph, doc, previous)
	if err != nil || len(gaps) != 1 || gaps[0].Code != "navigation_historical_target" {
		t.Fatalf("inherited Flow silently repinned: %+v %v", gaps, err)
	}
}
