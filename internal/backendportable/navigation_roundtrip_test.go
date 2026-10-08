package backendportable

import (
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
)

func TestArchitectureNavigationPortableClosureAndRoundtrip(t *testing.T) {
	t.Parallel()
	f := makeRoundtripFixture(t)
	child := f.diagrams[0]
	doc := child.Document
	doc.Payload.Elements[0].Navigation = []bm.ArchitectureNavigation{{Format: bm.ArchitectureNavigationVersion, Kind: "diagram", Label: "Exact details", Diagram: &child.Pin, Level: "components", RootID: fixedID(2)}}
	doc.Payload.Elements[1].Refs = []bm.DiagramRef{}
	doc.Payload.Elements[1].Membership = &bm.ArchitectureMembership{Format: bm.ArchitectureMembershipVersion, NodeIDs: []string{f.sourceIDs["handler"]}}
	parent, err := f.service.models.CreateDiagram(f.artifacts.DiagramContext(t.Context()), child.ProjectID, bm.DiagramCreateInput{Document: doc, IdempotencyKey: "navigation-parent"})
	check(t, err)
	view, err := f.service.models.CreateDiagramView(t.Context(), child.ProjectID, bm.DiagramCreateViewInput{Name: "Navigation parent", State: bm.DiagramViewState{Diagram: parent.Pin, Origin: "all", Level: "containers", RootID: fixedID(1), Positions: []bm.DiagramPosition{}, CollapsedIDs: []string{}}, IdempotencyKey: "navigation-view"})
	check(t, err)
	f.selection.DiagramViews = []SVGInput{{ViewID: view.ID, ViewVersion: view.Version}}
	_, session := stageRoundtrip(t, f)
	preview, err := f.service.Preview(t.Context(), session.ID, PreviewInput{ExpectedVersion: session.Version, Name: "Navigation copy", ArtifactMappings: []bm.PortableArtifactMapping{}, IdempotencyKey: "preview"})
	check(t, err)
	result, err := f.service.Commit(t.Context(), session.ID, CommitInput{ExpectedVersion: preview.Session.Version, CandidateHash: preview.CandidateHash, IdempotencyKey: "commit"})
	check(t, err)
	var localView string
	for _, mapping := range result.IDMap {
		if mapping.Origin.Kind == "diagram_view_version" && mapping.Origin.ID == view.ID {
			localView = mapping.Local.ID
		}
	}
	if localView == "" {
		t.Fatal("missing parent mapping")
	}
	importedView, err := f.service.models.GetDiagramView(t.Context(), result.Project.ID, localView, 1)
	check(t, err)
	imported, err := f.service.models.GetDiagram(t.Context(), result.Project.ID, importedView.State.Diagram)
	check(t, err)
	destination := imported.Document.Payload.Elements[0].Navigation[0]
	if destination.Diagram.ID == child.Pin.ID || destination.RootID == fixedID(2) {
		t.Fatal("navigation retained foreign IDs")
	}
	detail, err := f.service.models.GetDiagram(t.Context(), result.Project.ID, *destination.Diagram)
	check(t, err)
	found := false
	for _, e := range detail.Document.Payload.Elements {
		if e.ID == destination.RootID && e.Role == "application" {
			found = true
		}
	}
	if !found {
		t.Fatal("navigation root does not resolve in exact imported destination")
	}
	membership := imported.Document.Payload.Elements[1].Membership
	if membership == nil || len(membership.NodeIDs) != 1 || membership.NodeIDs[0] == f.sourceIDs["handler"] {
		t.Fatal("membership not remapped")
	}
}
