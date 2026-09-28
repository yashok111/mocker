package scenarioexport

import (
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func branchRevision(t *testing.T) designscenario.Revision {
	t.Helper()
	var doc designscenario.Document
	raw := `{"formatVersion":2,"title":"Branches","participants":[{"id":"p","name":"P","kind":"service"}],"messages":[{"id":"a","fromId":"p","toId":"p","kind":"request","label":"A"},{"id":"b","fromId":"p","toId":"p","kind":"request","label":"B"},{"id":"c","fromId":"p","toId":"p","kind":"request","label":"C"}],"contracts":[],"fragments":[{"id":"child","kind":"loop","label":"retry","fromMessageId":"a","toMessageId":"b","parentFragmentId":"root","parentBranchId":"yes"},{"id":"root","kind":"alt","label":"Decision","fromMessageId":"a","toMessageId":"c","branches":[{"id":"yes","label":"ok","fromMessageId":"a","toMessageId":"b"},{"id":"no","label":"else","fromMessageId":"c","toMessageId":"c"}]}]}`
	if err := jsonx.Unmarshal([]byte(raw), &doc); err != nil {
		t.Fatal(err)
	}
	return designscenario.Revision{Document: doc}
}

func TestNestedBranchSequenceExport(t *testing.T) {
	t.Parallel()
	for _, version := range []int{2, 3} {
		for _, format := range []Format{PlantUML, Mermaid} {
			t.Run(string(format)+"-v"+strconv.Itoa(version), func(t *testing.T) {
				service := &Service{maxBytes: 1_000_000}
				rev := branchRevision(t)
				rev.Document.FormatVersion = version
				got, err := service.Export(rev, Request{Format: format})
				if err != nil {
					t.Fatal(err)
				}
				arrow := " -> "
				if format == Mermaid {
					arrow = " ->> "
				}
				want := "alt ok\nloop retry\np0" + arrow + "p0: A\np0" + arrow + "p0: B\nend\nelse else\np0" + arrow + "p0: C\nend\n"
				if !strings.Contains(got.Content, want) {
					t.Fatalf("unexpected export:\n%s", got.Content)
				}
			})
		}
	}
}

func TestBranchExportRejectsWrongParent(t *testing.T) {
	t.Parallel()
	rev := branchRevision(t)
	rev.Document.Fragments[0].ParentFragmentID = "child"
	_, err := (&Service{maxBytes: 1_000_000}).Export(rev, Request{Format: Mermaid})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected diagnostic, got %v", err)
	}
}

func TestBranchLabelsCannotInjectSequenceCommands(t *testing.T) {
	t.Parallel()
	rev := branchRevision(t)
	rev.Document.Fragments[1].Branches[0].Label = "ok\nend\n!include <secret>"
	for _, format := range []Format{PlantUML, Mermaid} {
		got, err := (&Service{maxBytes: 1_000_000}).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(got.Content, "\n!include") || strings.Count(got.Content, "\nend\n") != 2 {
			t.Fatalf("branch escaped into commands: %s", got.Content)
		}
	}
}
