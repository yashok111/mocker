package backendmodel

import (
	"encoding/json/v2"
	"fmt"
	"testing"
)

func TestProjectStartViewPinsExactVersionAndReplays(t *testing.T) {
	r, _ := testRepo(t)
	p := createProject(t, r, "start-view")
	doc := diagramTestDocument(p.CurrentRevisionID)
	d, err := r.CreateDiagram(t.Context(), p.ID, DiagramCreateInput{Document: doc, IdempotencyKey: "diagram"})
	if err != nil {
		t.Fatal(err)
	}
	state := DiagramViewState{Diagram: d.Pin, Level: "context", RootID: doc.Payload.PrimarySystemID, Origin: "all", Positions: []DiagramPosition{}, CollapsedIDs: []string{}}
	v, err := r.CreateDiagramView(t.Context(), p.ID, DiagramCreateViewInput{Name: "Start", State: state, IdempotencyKey: "view"})
	if err != nil {
		t.Fatal(err)
	}
	var command Command
	if err := json.Unmarshal(fmt.Appendf(nil, `{"type":"set_start_view","startView":{"kind":"diagram_view","id":%q,"version":1}}`, v.ID), &command); err != nil {
		t.Fatal(err)
	}
	input := CommandsInput{ExpectedVersion: p.Version, IdempotencyKey: "set-start", Commands: []Command{command}}
	set, err := r.Apply(t.Context(), p.ID, input)
	if err != nil {
		t.Fatal(err)
	}
	if set.CurrentRevisionID != p.CurrentRevisionID {
		t.Fatal("presentation changed source revision")
	}
	if _, err := r.SaveDiagramView(t.Context(), p.ID, v.ID, DiagramSaveViewInput{Name: "New view", State: state, ExpectedVersion: 1, IdempotencyKey: "new-view"}); err != nil {
		t.Fatal(err)
	}
	got, err := r.Get(t.Context(), p.ID)
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(got)
	var wire struct {
		StartView struct {
			ID      string `json:"id"`
			Version int64  `json:"version"`
		} `json:"startView"`
	}
	if err := json.Unmarshal(raw, &wire); err != nil {
		t.Fatal(err)
	}
	if wire.StartView.ID != v.ID || wire.StartView.Version != 1 {
		t.Fatal("start view repinned to head")
	}
	if _, err := r.Apply(t.Context(), p.ID, CommandsInput{ExpectedVersion: set.Version, IdempotencyKey: "clear", Commands: []Command{{Type: "clear_start_view"}}}); err != nil {
		t.Fatal(err)
	}
	replay, err := r.Apply(t.Context(), p.ID, input)
	if err != nil || replay.Version != set.Version {
		t.Fatalf("start receipt replay failed: %+v %v", replay, err)
	}
	foreign := createProject(t, r, "foreign-start")
	_, err = r.Apply(t.Context(), foreign.ID, CommandsInput{ExpectedVersion: foreign.Version, IdempotencyKey: "foreign", Commands: []Command{{Type: "rename_project", Name: "Wrong"}, command}})
	if err == nil {
		t.Fatal("foreign view accepted")
	}
	unchanged, err := r.Get(t.Context(), foreign.ID)
	if err != nil || unchanged.Name != foreign.Name || unchanged.Version != foreign.Version {
		t.Fatal("invalid pin did not roll back metadata")
	}
}
