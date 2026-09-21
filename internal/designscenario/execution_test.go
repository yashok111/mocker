package designscenario

import (
	"bytes"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const executionDocumentJSON = `{"formatVersion":1,"title":"Execute","participants":[{"id":"api","kind":"service","name":"API","description":""}],"messages":[{"id":"call","fromId":"api","toId":"api","kind":"request","label":"Call","description":"","execution":{"enabled":true,"pathParams":{},"query":{},"headers":{"Authorization":"Bearer {{token}}"},"body":"{}","expectedStatus":201,"assertions":[{"pointer":"/ok","equals":true},{"pointer":"/nullable","equals":null}],"extract":[{"name":"token","pointer":"/token"}]}}],"fragments":[],"contracts":[],"execution":{"variables":{"token":"start"}}}`

func TestExecutionPersistsThroughSaveCommandAndRestore(t *testing.T) {
	var document Document
	if err := jsonx.Unmarshal([]byte(executionDocumentJSON), &document); err != nil {
		t.Fatal(err)
	}
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "mcp", Commands: []Command{{Type: "set_title", Title: "Edited"}}})
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionJSON(t, updated.Draft.Document)
	_, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 2, Document: validDocument("Empty"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: 3, RevisionID: created.Draft.ID, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	assertExecutionJSON(t, restored.Draft.Document)
}

func assertExecutionJSON(t *testing.T, document Document) {
	t.Helper()
	raw, err := jsonx.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"variables":{"token":"start"}`, `"Authorization":"Bearer {{token}}"`, `"expectedStatus":201`, `"equals":null`, `"name":"token","pointer":"/token"`} {
		if !bytes.Contains(raw, []byte(want)) {
			t.Fatalf("execution data %s lost: %s", want, raw)
		}
	}
}

func TestExecutionValidationRejectsInvalidConfiguration(t *testing.T) {
	for _, tt := range []struct{ name, old, value string }{
		{"status", `"expectedStatus":201`, `"expectedStatus":600`},
		{"pointer", `"pointer":"/ok"`, `"pointer":"/bad~2"`},
		{"variable", `"name":"token"`, `"name":"bad name"`},
		{"missing comparison", `,"equals":true`, ``},
		{"null map value", `"token":"start"`, `"token":null`},
		{"body size", `"body":"{}"`, `"body":"` + strings.Repeat("a", 1_048_577) + `"`},
	} {
		t.Run(tt.name, func(t *testing.T) {
			var document Document
			if err := jsonx.Unmarshal([]byte(strings.Replace(executionDocumentJSON, tt.old, tt.value, 1)), &document); err != nil {
				return
			}
			if _, err := newTestRepo(t).Validate(t.Context(), document); err == nil {
				t.Fatal("invalid execution configuration accepted")
			}
		})
	}
}
