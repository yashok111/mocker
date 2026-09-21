package designscenario

import (
	"database/sql"
	"testing"
)

func colorHistoryDocument() Document {
	document := validDocument("Colors")
	document.Participants = []Participant{{ID: "client", Name: "Клиент", Kind: "client"}, {ID: "api", Name: "API", Kind: "service"}}
	document.Messages = []Message{{ID: "call", FromID: "client", ToID: "api", Kind: "request", Label: "POST /login"}}
	return document
}

func TestRepo_WritesGeneratedColorHistorySummaries(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: colorHistoryDocument(), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	assertSummary := func(detail *Detail, want string) {
		t.Helper()
		if detail.Draft.Summary != want || detail.Revisions[0].Summary != want {
			t.Fatalf("draft/history summary = %q/%q, want %q", detail.Draft.Summary, detail.Revisions[0].Summary, want)
		}
		if err := repo.db.Read(t.Context(), func(tx *sql.Tx) error {
			stored, err := getRevision(t.Context(), tx, detail.Scenario.ID, detail.Draft.ID)
			if err == nil && stored.Summary != want {
				t.Errorf("stored summary = %q, want %q", stored.Summary, want)
			}
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	assertSummary(created, "Создан сценарий")
	current := created
	for _, tt := range []struct {
		name    string
		change  func(*Document)
		summary string
		want    string
	}{
		{"object", func(doc *Document) { doc.Participants[1].Color = "#abcdef" }, "", "Цвет объекта «API»: #ABCDEF"},
		{"message", func(doc *Document) { doc.Messages[0].Color = "#123456" }, "", "Цвет карточки сообщения «POST /login»: #123456"},
		{"arrow", func(doc *Document) { doc.Messages[0].ArrowColor = "#654321" }, "", "Цвет стрелки сообщения «POST /login»: #654321"},
		{"reset", func(doc *Document) { doc.Messages[0].ArrowColor = "" }, "", "Сброшен цвет стрелки сообщения «POST /login»"},
		{"explicit", func(doc *Document) { doc.Participants[1].Color = "#012345" }, "Проверено аналитиком", "Проверено аналитиком"},
		{"non-color", func(doc *Document) { doc.Title = "Renamed" }, "  ", "Изменён сценарий"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			document, err := cloneDocument(current.Draft.Document)
			if err != nil {
				t.Fatal(err)
			}
			tt.change(&document)
			current, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: current.Scenario.Version, Document: document, Source: "ui", Summary: tt.summary})
			if err != nil {
				t.Fatal(err)
			}
			assertSummary(current, tt.want)
		})
	}
	restored, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: current.Scenario.Version, RevisionID: created.Draft.ID, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	assertSummary(restored, "Восстановлена версия 1")
}

func TestRepo_CommandsGenerateColorHistorySummary(t *testing.T) {
	repo := newTestRepo(t)
	document := colorHistoryDocument()
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Summary: "Начальный проект", Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	if created.Draft.Summary != "Начальный проект" {
		t.Fatalf("explicit create summary = %q", created.Draft.Summary)
	}
	document.Participants[1].Color = "#abcdef"
	updated, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
		ExpectedVersion: 1, Source: "mcp",
		Commands: []Command{{Type: "upsert_participant", Participant: &document.Participants[1]}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Draft.Summary != "Цвет объекта «API»: #ABCDEF" {
		t.Fatalf("command summary = %q", updated.Draft.Summary)
	}
	restored, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: 2, RevisionID: created.Draft.ID, Source: "mcp", Summary: "Вернули согласованный вариант"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Draft.Summary != "Вернули согласованный вариант" {
		t.Fatalf("explicit restore summary = %q", restored.Draft.Summary)
	}
}
