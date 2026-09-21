package designscenario

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const convertedContract = `{"id":"converted","name":"Converted API","document":{"openapi":"3.1.0","info":{"title":"Converted API","version":"1"},"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders-get","responses":{"200":{"description":"OK"}}}}},"x-big":9007199254740993}}`

func conversionCommands(t *testing.T) []Command {
	t.Helper()
	var commands []Command
	raw := `[{"type":"create_contract","contract":` + convertedContract + `},{"type":"bind_operation","messageId":"call","contractId":"converted","operationKey":"orders-get"},{"type":"materialize_contract","contractId":"converted"}]`
	if err := jsonx.Unmarshal([]byte(raw), &commands); err != nil {
		t.Fatalf("decode conversion commands: %v", err)
	}
	return commands
}

func conversionScenario(t *testing.T, repo *Repo) *Detail {
	t.Helper()
	document := validDocument("Checkout")
	document.Participants = []Participant{{ID: "client", Kind: "client"}, {ID: "api", Kind: "service"}}
	document.Messages = []Message{{ID: "call", FromID: "client", ToID: "api", Kind: "request", Label: "GET /orders"}}
	document.Contracts = []Contract{{ID: "original", Name: "Original", Document: jsonx.RawMessage(apiDocument("untouched"))}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, FormDrafts: map[string]string{"all": `{"kept":true}`}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func TestRepo_CreateContractBatchMaterializesAndBindsWithoutChangingOriginal(t *testing.T) {
	repo := newTestRepo(t)
	created := conversionScenario(t, repo)
	result, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "mcp", Commands: conversionCommands(t)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scenario.Version != 2 || len(result.Revisions) != 2 || result.Draft.FormDrafts["all"] != `{"kept":true}` {
		t.Fatalf("batch did not create one revision preserving form drafts: %+v", result)
	}
	contracts := result.Draft.Document.Contracts
	if len(contracts) != 2 || string(contracts[0].Document) != string(created.Draft.Document.Contracts[0].Document) {
		t.Fatalf("original contract changed: %+v", contracts)
	}
	converted := contracts[1]
	if converted.Mode != "linked" || converted.Source == nil || converted.Source.Version != 1 {
		t.Fatalf("converted contract not materialized: %+v", converted)
	}
	binding := result.Draft.Document.Messages[0].Operation
	if binding == nil || binding.ContractID != "converted" || binding.OperationKey != "orders-get" {
		t.Fatalf("request binding = %+v", binding)
	}
	api, err := repo.designs.Detail(t.Context(), converted.Source.DesignID)
	if err != nil {
		t.Fatal(err)
	}
	if api.Design.DraftWorkspaceID <= 0 || !strings.Contains(api.Draft.Document, `9007199254740993`) || firstOperationKey(t, api.Draft.Document) != "orders-get" {
		t.Fatalf("materialized API lost mock, precision or identity: %+v", api)
	}
}

func TestRepo_CreateContractBatchFailuresLeaveNoAPIOrMock(t *testing.T) {
	for _, failure := range []string{"later command", "final validation", "stale version"} {
		t.Run(failure, func(t *testing.T) {
			repo := newTestRepo(t)
			created := conversionScenario(t, repo)
			commands := conversionCommands(t)
			expectedVersion := int64(1)
			wantErr := ErrInvalid
			switch failure {
			case "later command":
				commands = append(commands, Command{Type: "remove_participant", ID: "missing"})
			case "final validation":
				commands = append(commands, Command{Type: "upsert_message", Message: &Message{ID: "invalid", FromID: "missing", ToID: "api", Kind: "request"}})
			case "stale version":
				expectedVersion = 2
				wantErr = ErrConflict
			}
			_, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: expectedVersion, Source: "ui", Commands: commands})
			if !errors.Is(err, wantErr) {
				t.Fatalf("Apply error = %v, want %v", err, wantErr)
			}
			after, err := repo.Detail(t.Context(), created.Scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Draft.Hash != created.Draft.Hash || after.Scenario.Version != 1 || len(after.Revisions) != 1 {
				t.Fatalf("failed conversion changed scenario: %+v", after)
			}
			for _, table := range []string{"api_designs", "api_design_revisions", "api_design_workspaces", "workspaces", "specs"} {
				var count int
				if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("failed conversion retained %d rows in %s", count, table)
				}
			}
		})
	}
}

func TestCommandCreateContractRejectsMissingAndIrrelevantFields(t *testing.T) {
	for _, raw := range []string{
		`{"type":"create_contract"}`,
		`{"type":"create_contract","contract":null}`,
		`{"type":"create_contract","contract":` + convertedContract + `,"contractId":"ignored"}`,
		`{"type":"create_contract","contract":{"id":"new","name":"API","document":{},"source":null}}`,
		`{"type":"create_contract","contract":{"id":"new","name":"API","document":{},"extra":true}}`,
	} {
		var command Command
		if err := jsonx.Unmarshal([]byte(raw), &command); err == nil {
			t.Errorf("accepted malformed create contract: %s", raw)
		}
	}
}

func TestRepo_CreateContractOnlyAcceptsANewIndependentCopy(t *testing.T) {
	for _, failure := range []string{"missing contract", "empty id", "duplicate id", "linked", "unknown mode", "source"} {
		t.Run(failure, func(t *testing.T) {
			repo := newTestRepo(t)
			created := conversionScenario(t, repo)
			contract := &Contract{ID: "new", Name: "New", Mode: "copy", Document: jsonx.RawMessage(apiDocument("new"))}
			switch failure {
			case "missing contract":
				contract = nil
			case "empty id":
				contract.ID = " "
			case "duplicate id":
				contract.ID = "original"
			case "linked":
				contract.Mode = "linked"
			case "unknown mode":
				contract.Mode = "unknown"
			case "source":
				contract.Source = &ContractSource{DesignID: 1, RevisionID: 1, Version: 1}
			}
			_, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "create_contract", Contract: contract}}})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid contract accepted: %v", err)
			}
			after, err := repo.Detail(t.Context(), created.Scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Draft.Hash != created.Draft.Hash || len(after.Revisions) != 1 {
				t.Fatalf("rejected creation changed scenario: %+v", after)
			}
		})
	}
}

func TestRepo_CreateContractKeepsExplicitCopyIndependent(t *testing.T) {
	repo := newTestRepo(t)
	created := conversionScenario(t, repo)
	contract := &Contract{ID: "new", Name: "New", Mode: "copy", Document: jsonx.RawMessage(apiDocument("new"))}
	result, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "create_contract", Contract: contract}}})
	if err != nil {
		t.Fatal(err)
	}
	copy := result.Draft.Document.Contracts[1]
	if copy.Mode != "copy" || copy.Source != nil || string(copy.Document) != apiDocument("new") {
		t.Fatalf("new copy unexpectedly linked or changed: %+v", copy)
	}
	designs, err := repo.designs.List(t.Context())
	if err != nil || len(designs) != 0 {
		t.Fatalf("copy created API designs: %+v, %v", designs, err)
	}
}

func TestCommandCreateContractRequiresNestedFields(t *testing.T) {
	t.Parallel()
	for _, field := range []string{"id", "name", "document"} {
		for _, value := range []string{"missing", "null", "42"} {
			t.Run(field+"/"+value, func(t *testing.T) {
				t.Parallel()
				var contract map[string]jsonx.RawMessage
				if err := jsonx.Unmarshal([]byte(convertedContract), &contract); err != nil {
					t.Fatal(err)
				}
				if value == "missing" {
					delete(contract, field)
				} else {
					contract[field] = jsonx.RawMessage(value)
				}
				raw, err := jsonx.Marshal(map[string]any{"type": "create_contract", "contract": contract})
				if err != nil {
					t.Fatal(err)
				}
				var command Command
				if err := jsonx.Unmarshal(raw, &command); err == nil {
					t.Fatalf("accepted create_contract with %s %s: %s", value, field, raw)
				}
			})
		}
	}
	for _, document := range []string{`[]`, `"OpenAPI"`, `true`} {
		t.Run("non-object/"+document, func(t *testing.T) {
			var command Command
			raw := `{"type":"create_contract","contract":{"id":"new","name":"New","document":` + document + `}}`
			if err := jsonx.Unmarshal([]byte(raw), &command); err == nil {
				t.Fatalf("accepted non-object contract document: %s", raw)
			}
		})
	}
}

func TestRepo_CreateContractRejectsMissingDocumentBeforeMaterializing(t *testing.T) {
	for _, document := range []string{"", "null", "[]", `"OpenAPI"`, "42", "true"} {
		t.Run(document, func(t *testing.T) {
			repo := newTestRepo(t)
			created := conversionScenario(t, repo)
			_, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
				ExpectedVersion: 1,
				Source:          "ui",
				Commands: []Command{
					{Type: "create_contract", Contract: &Contract{ID: "new", Name: "New", Document: jsonx.RawMessage(document)}},
					{Type: "materialize_contract", ContractID: "new"},
				},
			})
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("Apply error = %v, want invalid document", err)
			}
			after, err := repo.Detail(t.Context(), created.Scenario.ID)
			if err != nil {
				t.Fatal(err)
			}
			if after.Draft.Hash != created.Draft.Hash || after.Scenario.Version != 1 || len(after.Revisions) != 1 {
				t.Fatalf("invalid document changed scenario: %+v", after)
			}
			for _, table := range []string{"api_designs", "api_design_revisions", "api_design_workspaces", "workspaces", "specs"} {
				var count int
				if err := repo.db.R.QueryRowContext(t.Context(), "SELECT COUNT(*) FROM "+table).Scan(&count); err != nil {
					t.Fatal(err)
				}
				if count != 0 {
					t.Fatalf("invalid document retained %d rows in %s", count, table)
				}
			}
		})
	}
}
