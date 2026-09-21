package designscenario

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDocument_JSONRoundTripsColors(t *testing.T) {
	const raw = `{"formatVersion":1,"title":"Colors","participants":[{"id":"api","name":"API","kind":"service","description":"","color":"#aBcDeF"}],"messages":[{"id":"call","fromId":"api","toId":"api","kind":"request","label":"Call","description":"","color":"#123456","arrowColor":"#FEDCBA"}],"fragments":[],"contracts":[]}`
	var document Document
	if err := jsonx.Unmarshal([]byte(raw), &document); err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonx.Marshal(document)
	if err != nil {
		t.Fatal(err)
	}
	if string(encoded) != raw {
		t.Fatalf("colors changed in JSON round-trip: %s", encoded)
	}
}

func TestDocument_JSONRejectsInvalidSuppliedColors(t *testing.T) {
	for _, color := range []string{`""`, `"red"`, `"#123"`, `"#12345678"`, `"#abcdez"`, `"#123456\n"`, `" #123456"`, `null`, `12`, `{}`} {
		for _, target := range []struct {
			name string
			raw  string
		}{
			{"participant", `{"participants":[{"color":COLOR}]}`},
			{"message", `{"messages":[{"color":COLOR}]}`},
			{"arrow", `{"messages":[{"arrowColor":COLOR}]}`},
		} {
			t.Run(target.name+"/"+color, func(t *testing.T) {
				var document Document
				if err := jsonx.Unmarshal([]byte(strings.ReplaceAll(target.raw, "COLOR", color)), &document); err == nil {
					t.Fatal("invalid supplied color was accepted")
				}
			})
		}
	}
}

func TestRevision_JSONKeepsDocumentAsObjectAndDistinguishesNullDiffValues(t *testing.T) {
	null := jsonx.RawMessage("null")
	revision := Revision{
		RevisionSummary: RevisionSummary{ID: 2, ScenarioID: 1, Version: 1},
		Document:        Document{FormatVersion: 1, Participants: []Participant{}, Messages: []Message{}, Fragments: []Fragment{}, Contracts: []Contract{}},
		FormDrafts:      map[string]string{},
	}
	diff := Diff{FromRevisionID: 1, ToRevisionID: 2, Changes: []Change{{Pointer: "/title", Before: &null}}}

	gotRevision, err := jsonx.Marshal(revision)
	if err != nil {
		t.Fatal(err)
	}
	gotDiff, err := jsonx.Marshal(diff)
	if err != nil {
		t.Fatal(err)
	}

	if string(gotRevision) != `{"id":2,"scenarioId":1,"version":1,"hash":"","source":"","summary":"","createdAt":0,"document":{"formatVersion":1,"title":"","participants":[],"messages":[],"fragments":[],"contracts":[]},"formDrafts":{}}` {
		t.Fatalf("revision JSON = %s", gotRevision)
	}
	if string(gotDiff) != `{"fromRevisionId":1,"toRevisionId":2,"changes":[{"pointer":"/title","before":null}]}` {
		t.Fatalf("diff JSON = %s", gotDiff)
	}
}

func TestConflictErrors_UnwrapScenarioConflict(t *testing.T) {
	tests := []struct {
		name string
		err  error
	}{
		{name: "scenario", err: &ConflictError{Version: 3, DraftRevisionID: 9}},
		{name: "linked API", err: &LinkedConflictError{ContractID: "api", DesignID: 4, Version: 7, DraftRevisionID: 12}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !errors.Is(tt.err, ErrConflict) {
				t.Fatalf("errors.Is(%v, ErrConflict) = false", tt.err)
			}
		})
	}
}
