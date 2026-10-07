package backendmodel

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/statediagram"
)

// Review 2026-10-06, F102: the largest guard the state-diagram owner admits
// (a 2048-byte pointer and 64 KiB of equalsJSON, worst-case escaping) still
// builds a lifecycle candidate.
func TestLifecycleLargestSourceGuardBuilds(t *testing.T) {
	t.Parallel()
	d := lifecycleFixture(t)
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: strings.Repeat("a", 64)}
	locator := ArtifactProjectionLocator{Pin: pin, View: "states", Owner: ArtifactOwnerAddress{Pointer: "/x-mocker-state-diagrams/diagrams/0", DiagramID: "order"}}
	// A JSON string of quotes doubles under escaping: the worst case.
	equals := `"` + strings.Repeat(`\"`, (statediagram.MaxJSON-2)/2) + `"`
	guard := &statediagram.Guard{Pointer: "/" + strings.Repeat("a", 2047), EqualsJSON: equals}
	if _, err := statediagram.Value(guard.EqualsJSON); err != nil {
		t.Fatalf("fixture guard is not admitted by its owner: %v", err)
	}
	rows := []ArtifactProjectionItem{
		{ID: "diagram", Locator: locator, Kind: "state_diagram", Data: ArtifactProjectionData{Kind: "state_diagram", StateDiagram: &statediagram.Diagram{ID: "order", Name: "Order", InitialStateID: "created"}}},
		{ID: "created-row", Locator: locator, Kind: "state", Data: ArtifactProjectionData{Kind: "state", State: &statediagram.State{ID: "created", Name: "Created"}}},
		{ID: "paid-row", Locator: locator, Kind: "state", Data: ArtifactProjectionData{Kind: "state", State: &statediagram.State{ID: "paid", Name: "Paid"}}},
		{ID: "transition-row", Locator: locator, Kind: "state_transition", Data: ArtifactProjectionData{Kind: "state_transition", StateTransition: &statediagram.Transition{ID: "pay", Name: "Pay", From: "created", To: "paid", Guard: guard}}},
	}
	in := DiagramLifecycleBuildInput{Target: d.Target, StateDiagram: LifecycleArtifactSelection{Locator: locator, RowID: "diagram"}, Entity: d.Lifecycle.Entity, StateFields: d.Lifecycle.StateFields}
	if _, err := buildLifecycleRows(t.Context(), in, rows); err != nil {
		t.Fatalf("largest admitted source guard: %v", err)
	}
}

// Review 2026-10-06, F59: a scenario participant whose name exceeds the
// 4096-byte frozen-label cap is legal in its owner, so freezing it must
// bound the label instead of failing the whole preview with a bare 400.
func TestArtifactLongOwnerLabelIsBounded(t *testing.T) {
	t.Parallel()
	s, base, ids, d := artifactServiceFixture(t)
	owner := s.scenarios.(*designscenario.Repo)
	doc := d.Draft.Document
	doc.Participants[0].Name = strings.Repeat("é", 2500)
	next, err := owner.Save(t.Context(), d.Scenario.ID, designscenario.SaveInput{ExpectedVersion: d.Scenario.Version, Document: doc, FormDrafts: d.Draft.FormDrafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	p, err := s.Preview(t.Context(), base.Project.ID, scenarioSet(base, ids, next))
	if err != nil || !p.CanApply {
		t.Fatalf("long owner label: %+v %v", p, err)
	}
	for _, b := range p.EditorBindings {
		if len(b.LastKnownLabel) > MaxAPIArtifactLabelBytes {
			t.Fatalf("frozen label %d bytes", len(b.LastKnownLabel))
		}
	}
}
