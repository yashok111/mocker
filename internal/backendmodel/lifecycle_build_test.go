package backendmodel

import (
	"testing"

	"github.com/yashok111/mocker/internal/statediagram"
)

func TestLifecyclePinnedArtifactIntent(t *testing.T) {
	d := lifecycleFixture(t)
	pin := ArtifactPin{Kind: "api_design", ID: "1", RevisionID: "1", ContentHash: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"}
	locator := ArtifactProjectionLocator{Pin: pin, View: "states", Owner: ArtifactOwnerAddress{Pointer: "/x-mocker-state-diagrams/diagrams/0", DiagramID: "order"}}
	rows := []ArtifactProjectionItem{
		{ID: "diagram", Locator: locator, Kind: "state_diagram", Data: ArtifactProjectionData{Kind: "state_diagram", StateDiagram: &statediagram.Diagram{ID: "order", Name: "Order", InitialStateID: "created"}}},
		{ID: "created-row", Locator: locator, Kind: "state", Data: ArtifactProjectionData{Kind: "state", State: &statediagram.State{ID: "created", Name: "Created", Value: new("9007199254740993")}}},
		{ID: "paid-row", Locator: locator, Kind: "state", Data: ArtifactProjectionData{Kind: "state", State: &statediagram.State{ID: "paid", Name: "Paid"}}},
		{ID: "transition-row", Locator: locator, Kind: "state_transition", Data: ArtifactProjectionData{Kind: "state_transition", StateTransition: &statediagram.Transition{ID: "pay", Name: "Pay", From: "created", To: "paid", Guard: &statediagram.Guard{Pointer: "/approved", EqualsJSON: "true"}}}},
	}
	in := DiagramLifecycleBuildInput{Target: d.Target, StateDiagram: LifecycleArtifactSelection{Locator: locator, RowID: "diagram"}, Entity: d.Lifecycle.Entity, StateFields: d.Lifecycle.StateFields}
	a, err := buildLifecycleRows(t.Context(), in, rows)
	if err != nil {
		t.Fatal(err)
	}
	if a.Lifecycle.Coverage != "partial" || len(a.Lifecycle.Transitions) != 1 || a.Lifecycle.Transitions[0].Guard.Kind != "opaque" {
		t.Fatal("invented runtime behavior")
	}
	for _, s := range a.Lifecycle.States {
		if s.Origin.Kind != "authored" || s.Origin.Reason != "pinned state diagram" || s.Refs[0].Locator.Pin != pin {
			t.Fatal("lost authored exact pin")
		}
		if s.Initial && s.Value.JSON != `"9007199254740993"` {
			t.Fatal("string value became a number")
		}
	}
	in.StateDiagram.Locator.Pin.RevisionID = "2"
	for i := range rows {
		rows[i].Locator.Pin = in.StateDiagram.Locator.Pin
	}
	b, err := buildLifecycleRows(t.Context(), in, rows)
	if err != nil {
		t.Fatal(err)
	}
	if a.Lifecycle.States[0].ID != b.Lifecycle.States[0].ID {
		t.Fatal("identity depended on revision")
	}
	in.StateDiagram.Locator.Pin.ID = "2"
	for i := range rows {
		rows[i].Locator.Pin = in.StateDiagram.Locator.Pin
	}
	c, err := buildLifecycleRows(t.Context(), in, rows)
	if err != nil {
		t.Fatal(err)
	}
	if a.Lifecycle.States[0].ID == c.Lifecycle.States[0].ID {
		t.Fatal("foreign owner collision")
	}
}
