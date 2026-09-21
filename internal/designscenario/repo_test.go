package designscenario

import (
	"errors"
	"reflect"
	"sync"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/testkit"
)

func newTestRepo(t *testing.T) *Repo {
	t.Helper()
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	return NewRepo(db, cfg, apidesign.NewRepo(db, cfg))
}

func validDocument(title string) Document {
	return Document{
		FormatVersion: 1,
		Title:         title,
		Participants:  []Participant{},
		Messages:      []Message{},
		Fragments:     []Fragment{},
		Contracts:     []Contract{},
	}
}

func TestRepo_CreateDetailAndListRoundTrip(t *testing.T) {
	repo := newTestRepo(t)

	created, err := repo.Create(t.Context(), CreateInput{
		Document:   validDocument("Checkout"),
		FormDrafts: map[string]string{"all": `{}`},
		Summary:    "initial",
		Source:     "ui",
	})
	if err != nil {
		t.Fatal(err)
	}
	if created.Scenario.Name != "Checkout" || created.Scenario.Version != 1 {
		t.Fatalf("scenario = %+v", created.Scenario)
	}
	if created.Draft.Document.Title != "Checkout" || created.Draft.FormDrafts["all"] != `{}` {
		t.Fatalf("draft = %+v", created.Draft)
	}

	got, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scenario.DraftRevisionID != got.Draft.ID || len(got.Revisions) != 1 {
		t.Fatalf("detail = %+v", got)
	}
	list, err := repo.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 || list[0] != got.Scenario {
		t.Fatalf("list = %+v, want %+v", list, got.Scenario)
	}
}

func TestRepo_ColorsPersistResetAndRestore(t *testing.T) {
	repo := newTestRepo(t)
	document := validDocument("Colors")
	document.Participants = []Participant{{ID: "api", Kind: "service", Color: "#abcdef"}}
	document.Messages = []Message{{ID: "call", FromID: "api", ToID: "api", Kind: "request", Color: "#123456", ArrowColor: "#FEDCBA"}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	assertDocument := func(want Document) {
		t.Helper()
		detail, err := repo.Detail(t.Context(), created.Scenario.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(detail.Draft.Document, want) {
			t.Fatalf("stored document = %#v, want %#v", detail.Draft.Document, want)
		}
	}
	assertDocument(document)

	document.Participants[0].Color = "#765432"
	document.Messages[0].Color = "#654321"
	document.Messages[0].ArrowColor = "#ABCDEF"
	saved, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	assertDocument(document)

	document.Participants[0].Color = ""
	document.Messages[0].Color = ""
	document.Messages[0].ArrowColor = ""
	if _, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 2, Document: document, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	assertDocument(document)
	if _, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: 3, RevisionID: saved.Draft.ID, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	assertDocument(saved.Draft.Document)
}

func TestRepo_SaveUsesCASAndNoOpKeepsRevision(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("v1"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	unchanged, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{
		ExpectedVersion: 1,
		Document:        validDocument("v1"),
		FormDrafts:      map[string]string{},
		Source:          "ui",
	})
	if err != nil {
		t.Fatal(err)
	}
	if unchanged.Scenario.Version != 1 || len(unchanged.Revisions) != 1 {
		t.Fatalf("no-op detail = %+v", unchanged)
	}

	updated, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{
		ExpectedVersion: 1,
		Document:        validDocument("v2"),
		FormDrafts:      map[string]string{},
		Summary:         "rename",
		Source:          "mcp",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Scenario.Version != 2 || updated.Scenario.Name != "v2" || len(updated.Revisions) != 2 {
		t.Fatalf("updated detail = %+v", updated)
	}

	_, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{
		ExpectedVersion: 1,
		Document:        validDocument("stale"),
		Source:          "ui",
	})
	var conflict *ConflictError
	if !errors.As(err, &conflict) || conflict.Version != 2 || conflict.DraftRevisionID != updated.Draft.ID {
		t.Fatalf("stale save error = %#v", err)
	}
}

func TestRepo_ConcurrentSavesHaveOneWinner(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("base"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	start := make(chan struct{})
	errs := make(chan error, 2)
	var wg sync.WaitGroup
	for _, title := range []string{"one", "two"} {
		wg.Go(func() {
			<-start
			_, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: validDocument(title), Source: "ui"})
			errs <- err
		})
	}
	close(start)
	wg.Wait()
	close(errs)

	var successes, conflicts int
	for err := range errs {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, ErrConflict):
			conflicts++
		default:
			t.Fatalf("save error = %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf("successes=%d conflicts=%d", successes, conflicts)
	}
}

func TestRepo_RevisionRejectsForeignRevisionAndRestoreAddsHistory(t *testing.T) {
	repo := newTestRepo(t)
	first, err := repo.Create(t.Context(), CreateInput{Document: validDocument("first"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	second, err := repo.Create(t.Context(), CreateInput{Document: validDocument("second"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = repo.Revision(t.Context(), first.Scenario.ID, second.Draft.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("foreign revision error = %v", err)
	}

	changed, err := repo.Save(t.Context(), first.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: validDocument("changed"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repo.Restore(t.Context(), first.Scenario.ID, RestoreInput{ExpectedVersion: 2, RevisionID: first.Draft.ID, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Scenario.Version != 3 || restored.Draft.Document.Title != "first" || len(restored.Revisions) != 3 {
		t.Fatalf("restored detail = %+v (changed=%+v)", restored, changed)
	}
}
