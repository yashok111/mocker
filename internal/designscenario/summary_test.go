package designscenario

import (
	"reflect"
	"slices"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestRevisionDescriptionColorsAndOtherChanges(t *testing.T) {
	t.Parallel()
	for _, tt := range []struct {
		name   string
		before func(*Revision)
		after  func(*Revision)
		want   string
	}{
		{"reset object", func(r *Revision) { r.Document.Participants[1].Color = "#abcdef" }, func(r *Revision) { r.Document.Participants[1].Color = "" }, "Сброшен цвет объекта «API»"},
		{"reset card", func(r *Revision) { r.Document.Messages[0].Color = "#abcdef" }, func(r *Revision) { r.Document.Messages[0].Color = "" }, "Сброшен цвет карточки сообщения «POST /login»"},
		{"reset arrow", func(r *Revision) { r.Document.Messages[0].ArrowColor = "#abcdef" }, func(r *Revision) { r.Document.Messages[0].ArrowColor = "" }, "Сброшен цвет стрелки сообщения «POST /login»"},
		{"multiple", nil, func(r *Revision) {
			r.Document.Participants[1].Color = "#123456"
			r.Document.Messages[0].Color = "#abcdef"
			r.Document.Messages[0].ArrowColor = "#654321"
		}, "Цвет объекта «API»: #123456; Цвет карточки сообщения «POST /login»: #ABCDEF + ещё 1 изменение цвета"},
		{"reorder only", func(r *Revision) { r.Document.Participants[1].Color = "#abcdef" }, func(r *Revision) { slices.Reverse(r.Document.Participants) }, "Изменён сценарий"},
		{"reorder and color", nil, func(r *Revision) {
			slices.Reverse(r.Document.Participants)
			r.Document.Participants[0].Color = "#abcdef"
		}, "Цвет объекта «API»: #ABCDEF + другие изменения"},
		{"message reorder", func(r *Revision) {
			r.Document.Messages[0].Color = "#abcdef"
			r.Document.Messages = append(r.Document.Messages, Message{ID: "second", FromID: "api", ToID: "api", Kind: "event", Label: "Saved"})
		}, func(r *Revision) { slices.Reverse(r.Document.Messages) }, "Изменён сценарий"},
		{"message reorder and color", func(r *Revision) {
			r.Document.Messages = append(r.Document.Messages, Message{ID: "second", FromID: "api", ToID: "api", Kind: "event", Label: "Saved"})
		}, func(r *Revision) {
			slices.Reverse(r.Document.Messages)
			r.Document.Messages[1].ArrowColor = "#abcdef"
		}, "Цвет стрелки сообщения «POST /login»: #ABCDEF + другие изменения"},
		{"title and color", nil, func(r *Revision) {
			r.Document.Title = "Renamed"
			r.Document.Participants[1].Color = "#abcdef"
		}, "Цвет объекта «API»: #ABCDEF + другие изменения"},
		{"API buffer and color", nil, func(r *Revision) {
			r.FormDrafts = map[string]string{"operation": "unfinished"}
			r.Document.Participants[1].Color = "#abcdef"
		}, "Цвет объекта «API»: #ABCDEF + другие изменения"},
		{"new object", nil, func(r *Revision) {
			r.Document.Participants = append(r.Document.Participants, Participant{ID: "db", Name: "DB", Color: "#abcdef"})
		}, "Изменён сценарий"},
		{"removed object", func(r *Revision) { r.Document.Participants[1].Color = "#abcdef" }, func(r *Revision) {
			r.Document.Participants = r.Document.Participants[:1]
		}, "Изменён сценарий"},
		{"case only", func(r *Revision) { r.Document.Participants[1].Color = "#abcdef" }, func(r *Revision) { r.Document.Participants[1].Color = "#ABCDEF" }, "Изменён сценарий"},
		{"case and actual color", func(r *Revision) { r.Document.Participants[1].Color = "#abcdef" }, func(r *Revision) {
			r.Document.Participants[1].Color = "#ABCDEF"
			r.Document.Messages[0].Color = "#123456"
		}, "Цвет карточки сообщения «POST /login»: #123456"},
		{"blank name", func(r *Revision) { r.Document.Participants[1].Name = "" }, func(r *Revision) {
			r.Document.Participants[1].Color = "#abcdef"
		}, "Цвет объекта «api»: #ABCDEF"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := Revision{Document: colorHistoryDocument(), FormDrafts: map[string]string{}}
			if tt.before != nil {
				tt.before(&before)
			}
			afterDocument, err := cloneDocument(before.Document)
			if err != nil {
				t.Fatal(err)
			}
			after := Revision{Document: afterDocument}
			tt.after(&after)
			beforeCopy, err := cloneDocument(before.Document)
			if err != nil {
				t.Fatal(err)
			}
			if got := revisionDescription(&before, after.Document, after.FormDrafts, ""); got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
			if !reflect.DeepEqual(before.Document, beforeCopy) {
				t.Fatal("summary generation mutated previous document")
			}
		})
	}
}

func TestRevisionDescriptionBoundsGeneratedSummariesAndPreservesExplicitText(t *testing.T) {
	before := Revision{Document: colorHistoryDocument()}
	before.Document.Participants[1].Name = strings.Repeat("Я", 50_000)
	before.Document.Messages[0].Label = strings.Repeat("Д", 50_000)
	after, err := cloneDocument(before.Document)
	if err != nil {
		t.Fatal(err)
	}
	after.Participants[1].Color = "#abcdef"
	after.Messages[0].Color = "#abcdef"
	after.Messages[0].ArrowColor = "#abcdef"
	after.Title = "Renamed"
	got := revisionDescription(&before, after, nil, "")
	if !utf8.ValidString(got) || utf8.RuneCountInString(got) > 256 || !strings.HasSuffix(got, " + другие изменения") || !strings.Contains(got, "+ ещё 1 изменение цвета") {
		t.Fatalf("summary is unbounded, malformed or hides changes: %q", got)
	}
	const explicit = "  Описание от аналитика  "
	if got := revisionDescription(&before, after, nil, explicit); got != explicit {
		t.Fatalf("explicit summary changed: %q", got)
	}
}

func TestRevisionDescriptionComparesContractJSONSemanticallyWithoutLosingNumbers(t *testing.T) {
	t.Parallel()
	const beforeJSON = `{
		"openapi": "3.1.0", "info": {"title": "API", "version": "1"},
		"paths": {"/login": {"post": {"responses": {"200": {"description": "OK"}}}}},
		"x-large": 9007199254740993
	}`
	const reorderedJSON = `{"x-large":9007199254740993,"paths":{"/login":{"post":{"responses":{"200":{"description":"OK"}}}}},"info":{"version":"1","title":"API"},"openapi":"3.1.0"}`
	for _, tt := range []struct {
		name     string
		document string
		want     string
	}{
		{"format and key order", reorderedJSON, "Цвет объекта «API»: #ABCDEF"},
		{"different large integer", strings.Replace(reorderedJSON, "9007199254740993", "9007199254740992", 1), "Цвет объекта «API»: #ABCDEF + другие изменения"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			before := Revision{Document: colorHistoryDocument()}
			before.Document.Contracts = []Contract{{ID: "api", Name: "API", Document: jsonx.RawMessage(beforeJSON)}}
			after, err := cloneDocument(before.Document)
			if err != nil {
				t.Fatal(err)
			}
			after.Participants[1].Color = "#abcdef"
			after.Contracts[0].Document = jsonx.RawMessage(tt.document)
			if got := revisionDescription(&before, after, nil, ""); got != tt.want {
				t.Fatalf("summary = %q, want %q", got, tt.want)
			}
		})
	}
}
