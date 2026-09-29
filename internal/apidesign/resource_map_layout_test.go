package apidesign

import (
	"errors"
	"reflect"
	"testing"

	"github.com/yashok111/mocker/internal/resourcemap"
)

func TestResourceAutoLayoutPreviewAndVersionedApply(t *testing.T) {
	t.Parallel()
	repo, _ := testRepo(t)
	detail, err := repo.Create(t.Context(), CreateInput{Name: "Layout", Document: testDocument, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	commands := []resourcemap.Command{{Kind: "auto_layout"}}
	proposal, err := repo.PreviewResourceMap(t.Context(), detail.Design.ID, ResourceMapProposal{Commands: commands})
	if err != nil || !proposal.Valid {
		t.Fatalf("preview: %+v, %v", proposal, err)
	}
	unchanged, err := repo.Detail(t.Context(), detail.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Design.Version != detail.Design.Version || unchanged.Draft.Document != detail.Draft.Document {
		t.Fatal("layout preview saved a revision")
	}
	changed, err := repo.ApplyResourceMapCommands(t.Context(), detail.Design.ID, detail.Design.Version, "mcp", commands)
	if err != nil || changed.Design.Version != detail.Design.Version+1 {
		t.Fatalf("apply: %+v, %v", changed, err)
	}
	actual, err := repo.ResourceMap(t.Context(), detail.Design.ID)
	if err != nil || !reflect.DeepEqual(actual.Model, proposal.Model) {
		t.Fatalf("preview and apply disagree: %+v / %+v, %v", proposal.Model, actual.Model, err)
	}
	if _, err := repo.ApplyResourceMapCommands(t.Context(), detail.Design.ID, detail.Design.Version, "mcp", commands); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale layout applied: %v", err)
	}
}
