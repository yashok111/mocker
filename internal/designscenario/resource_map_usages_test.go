package designscenario

import (
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestResourceMapUsagesReadsCurrentDraftAndPinnedSource(t *testing.T) {
	t.Parallel()
	repo := newTestRepo(t)
	api, err := repo.designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("orders"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	key := firstOperationKey(t, api.Draft.Document)
	doc := validDocument("Checkout")
	doc.Participants = []Participant{{ID: "client", Kind: "client"}, {ID: "api", Kind: "service"}}
	doc.Contracts = []Contract{{ID: "orders", Name: "Orders", Document: jsonx.RawMessage(api.Draft.Document), Mode: "copy", Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Design.Version}}}
	doc.Messages = []Message{{ID: "call", FromID: "client", ToID: "api", Kind: "request", Operation: &OperationBinding{ContractID: "orders", OperationKey: key}}}
	created, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	got, truncated, err := repo.ResourceMapUsages(t.Context(), api.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if truncated || len(got) != 1 || got[0].ScenarioID != created.Scenario.ID || got[0].RevisionID != created.Draft.ID || got[0].OperationKey != key || got[0].ContractRevisionID != api.Draft.ID || got[0].Mode != "copy" {
		t.Fatalf("usages=%+v truncated=%v", got, truncated)
	}
	got, _, err = repo.ResourceMapUsages(t.Context(), api.Design.ID+100)
	if err != nil || len(got) != 0 {
		t.Fatalf("unrelated design usages=%+v err=%v", got, err)
	}
	doc.Contracts[0].Mode = "linked"
	saved, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	got, truncated, err = repo.ResourceMapUsages(t.Context(), api.Design.ID)
	if err != nil || truncated || len(got) != 1 || got[0].Mode != "linked" || got[0].RevisionID != saved.Draft.ID || got[0].ContractRevisionID != api.Draft.ID {
		t.Fatalf("linked current draft usages=%+v truncated=%v err=%v", got, truncated, err)
	}
	doc.Messages = []Message{}
	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 2, Document: doc, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	got, _, err = repo.ResourceMapUsages(t.Context(), api.Design.ID)
	if err != nil || len(got) != 0 {
		t.Fatalf("historical usage leaked: %+v %v", got, err)
	}
}
