package apidesign

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/statediagram"
)

func TestStateDiagramEditsPreserveContractAndFenceVersions(t *testing.T) {
	t.Parallel()
	r, _ := testRepo(t)
	ctx := t.Context()
	d, err := r.Create(ctx, CreateInput{Name: "API", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	diagram := statediagram.Diagram{ID: "order", Name: "Order", States: []statediagram.State{}, Transitions: []statediagram.Transition{}}
	next, err := r.EditStateDiagram(ctx, d.Design.ID, 1, "mcp", "order", "create", &diagram, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(next.Draft.Document, "9007199254740993") || !strings.Contains(next.Draft.Document, "x-retained") || next.Draft.Source != "mcp" {
		t.Fatal("contract or actor lost")
	}
	if _, err = r.EditStateDiagram(ctx, d.Design.ID, 1, "ui", "order", "delete", nil, nil); err == nil {
		t.Fatal("stale delete accepted")
	}
	list, err := r.StateDiagrams(ctx, d.Design.ID)
	if err != nil || list.Version != 2 || len(list.Diagrams) != 1 {
		t.Fatalf("%+v %v", list, err)
	}
	if _, err = r.Save(ctx, d.Design.ID, SaveInput{ExpectedVersion: 2, Document: strings.Replace(next.Draft.Document, `"formatVersion": 1`, `"formatVersion": 999`, 1), Source: "ui"}); err == nil {
		t.Fatal("raw API write bypassed format validation")
	}
	restored, err := r.Restore(ctx, d.Design.ID, d.Draft.ID, 2, "restore", "ui")
	if err != nil || strings.Contains(restored.Draft.Document, statediagram.Extension) {
		t.Fatalf("restore %v", err)
	}
}
