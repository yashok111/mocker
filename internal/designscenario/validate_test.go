package designscenario

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestRepo_ValidateAllowsBrokenOperationAsWarning(t *testing.T) {
	repo := newTestRepo(t)
	document := validDocument("broken")
	document.Participants = []Participant{
		{ID: "client", Kind: "client"},
		{ID: "server", Kind: "service"},
	}
	document.Contracts = []Contract{{ID: "api", Name: "API", Mode: "copy", Document: jsonx.RawMessage(apiDocument("test"))}}
	document.Messages = []Message{{
		ID: "call", FromID: "client", ToID: "server", Kind: "request",
		Operation: &OperationBinding{ContractID: "api", OperationKey: "removed"},
	}}

	diagnostics, err := repo.Validate(t.Context(), document)
	if err != nil {
		t.Fatal(err)
	}
	if len(diagnostics) != 1 || diagnostics[0].Severity != "warning" || diagnostics[0].Pointer != "/messages/0/operation/operationKey" {
		t.Fatalf("diagnostics = %+v", diagnostics)
	}
}

func TestValidateDocument_RejectsInvalidProgrammaticColors(t *testing.T) {
	t.Parallel()
	for _, value := range []HexColor{"red", "#123", "#abcdef00", "#abcdez", "#123456\n"} {
		t.Run(string(value), func(t *testing.T) {
			document := validDocument("Colors")
			document.Participants = []Participant{{ID: "api", Kind: "service", Color: value}}
			document.Messages = []Message{{ID: "call", FromID: "api", ToID: "api", Kind: "request", Color: value, ArrowColor: value}}
			diagnostics := validateDocument(document)
			want := []string{"/participants/0/color", "/messages/0/color", "/messages/0/arrowColor"}
			if len(diagnostics) != len(want) {
				t.Fatalf("diagnostics = %+v", diagnostics)
			}
			for index, pointer := range want {
				if diagnostics[index].Pointer != pointer || diagnostics[index].Severity != "error" {
					t.Fatalf("diagnostic = %+v, want error at %s", diagnostics[index], pointer)
				}
			}
		})
	}
}

func TestRepo_InvalidAndOversizedSaveLeaveHistoryUnchanged(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 400}
	repo := NewRepo(db, cfg, apidesign.NewRepo(db, cfg))
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("base"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	invalid := validDocument("invalid")
	invalid.Messages = []Message{{ID: "missing", FromID: "a", ToID: "b", Kind: "request"}}
	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: invalid, Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("invalid save error = %v", err)
	}
	oversized := validDocument(string(make([]byte, 500)))
	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: oversized, Source: "ui"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("oversized save error = %v", err)
	}

	after, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Scenario.Version != 1 || after.Scenario.DraftRevisionID != created.Draft.ID || len(after.Revisions) != 1 {
		t.Fatalf("failed writes changed history: %+v", after)
	}
}
