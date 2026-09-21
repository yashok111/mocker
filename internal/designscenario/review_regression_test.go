package designscenario

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestRepo_DiffDefaultsToPreviousAndCurrentRevision(t *testing.T) {
	repo := newTestRepo(t)
	first, err := repo.Create(t.Context(), CreateInput{Document: validDocument("first"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	diff, err := repo.Diff(t.Context(), first.Scenario.ID, 0, 0)
	if err != nil {
		t.Fatal(err)
	}
	if diff.FromRevisionID != first.Draft.ID || diff.ToRevisionID != first.Draft.ID || len(diff.Changes) != 0 {
		t.Fatalf("first revision diff = %+v", diff)
	}
	second, err := repo.Save(t.Context(), first.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: validDocument("second"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	for _, ids := range [][2]int64{{0, 0}, {first.Draft.ID, 0}, {0, second.Draft.ID}} {
		diff, err := repo.Diff(t.Context(), first.Scenario.ID, ids[0], ids[1])
		if err != nil {
			t.Fatal(err)
		}
		if diff.FromRevisionID != first.Draft.ID || diff.ToRevisionID != second.Draft.ID || len(diff.Changes) != 1 || diff.Changes[0].Pointer != "/document/title" {
			t.Fatalf("default diff = %+v", diff)
		}
	}
	if _, err := repo.Diff(t.Context(), first.Scenario.ID+100, 0, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing scenario error = %v", err)
	}
}

func TestRepo_EmptyOperationBindingIsInvalidButMissingOperationIsWarning(t *testing.T) {
	repo := newTestRepo(t)
	document := validDocument("binding")
	document.Participants = []Participant{{ID: "client", Kind: "client"}, {ID: "api", Kind: "service"}}
	document.Contracts = []Contract{{ID: "contract", Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"API","version":"1"},"paths":{}}`)}}
	document.Messages = []Message{{ID: "request", FromID: "client", ToID: "api", Kind: "request", Operation: &OperationBinding{ContractID: "contract"}}}
	if _, err := repo.Validate(t.Context(), document); !errors.Is(err, ErrInvalid) {
		t.Fatalf("empty operation key error = %v", err)
	}
	document.Messages[0].Operation.OperationKey = "removed-operation"
	diagnostics, err := repo.Validate(t.Context(), document)
	if err != nil || len(diagnostics) != 1 || diagnostics[0].Severity != "warning" {
		t.Fatalf("missing nonempty operation = %+v, %v", diagnostics, err)
	}
}

func TestRepo_RejectsMissingOrNullDocumentArrays(t *testing.T) {
	for _, field := range []string{"participants", "messages", "fragments", "contracts"} {
		t.Run(field, func(t *testing.T) {
			repo := newTestRepo(t)
			document := validDocument("invalid")
			switch field {
			case "participants":
				document.Participants = nil
			case "messages":
				document.Messages = nil
			case "fragments":
				document.Fragments = nil
			case "contracts":
				document.Contracts = nil
			}
			if _, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "mcp"}); !errors.Is(err, ErrInvalid) {
				t.Fatalf("missing %s error = %v", field, err)
			}
			list, err := repo.List(t.Context())
			if err != nil || len(list) != 0 {
				t.Fatalf("invalid create wrote scenario: %+v, %v", list, err)
			}
		})
	}
}
