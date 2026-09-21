package designscenario

import (
	"errors"
	"testing"

	"github.com/yashok111/mocker/internal/apidesign"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/testkit"
)

func TestRepo_ApplyColorsSurviveBindingAndResetByOmission(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("Colors"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	participant := Participant{ID: "api", Kind: "service", Color: "#abcdef"}
	message := Message{ID: "call", FromID: "api", ToID: "api", Kind: "request", Color: "#123456", ArrowColor: "#FEDCBA"}
	colored, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
		ExpectedVersion: 1,
		Source:          "mcp",
		Commands: []Command{
			{Type: "upsert_participant", Participant: &participant},
			{Type: "upsert_message", Message: &message},
			{Type: "create_operation", MessageID: "call", Method: "get", Path: "/status"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	gotMessage := colored.Draft.Document.Messages[0]
	if colored.Draft.Document.Participants[0].Color != participant.Color || gotMessage.Color != message.Color || gotMessage.ArrowColor != message.ArrowColor || gotMessage.Operation == nil {
		t.Fatalf("creating an operation lost colors or binding: %+v", colored.Draft.Document)
	}
	participant.Color = ""
	gotMessage.Color = ""
	gotMessage.ArrowColor = ""
	reset, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
		ExpectedVersion: 2,
		Source:          "mcp",
		Commands: []Command{
			{Type: "upsert_participant", Participant: &participant},
			{Type: "upsert_message", Message: &gotMessage},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if reset.Draft.Document.Participants[0].Color != "" || reset.Draft.Document.Messages[0].Color != "" || reset.Draft.Document.Messages[0].ArrowColor != "" || reset.Draft.Document.Messages[0].Operation == nil {
		t.Fatalf("reset did not clear only colors: %+v", reset.Draft.Document)
	}
}

func TestRepo_ApplyMutatesACommandBatchAndPreservesDrafts(t *testing.T) {
	repo := newTestRepo(t)
	document := validDocument("before")
	document.Participants = []Participant{{ID: "user", Name: "User", Kind: "user"}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, FormDrafts: map[string]string{"all": `{"pending":true}`}, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	result, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
		ExpectedVersion: 1,
		Source:          "mcp",
		Commands: []Command{
			{Type: "set_title", Title: "after"},
			{Type: "upsert_participant", Participant: &Participant{ID: "api", Name: "API", Kind: "service"}},
			{Type: "upsert_message", Message: &Message{ID: "call", FromID: "user", ToID: "api", Kind: "request", Label: "Call"}},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if result.Scenario.Version != 2 || result.Scenario.Name != "after" || len(result.Draft.Document.Messages) != 1 {
		t.Fatalf("detail = %+v", result)
	}
	if result.Draft.FormDrafts["all"] != `{"pending":true}` {
		t.Fatalf("form drafts = %#v", result.Draft.FormDrafts)
	}
}

func TestRepo_ApplyRollsBackTheWholeBatch(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("before"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{
		ExpectedVersion: 1,
		Source:          "mcp",
		Commands: []Command{
			{Type: "set_title", Title: "after"},
			{Type: "remove_participant", ID: "missing"},
		},
	})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("Apply error = %v", err)
	}
	detail, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Scenario.Version != 1 || detail.Draft.Document.Title != "before" || len(detail.Revisions) != 1 {
		t.Fatalf("detail after failed batch = %+v", detail)
	}
}

func TestRepo_LinkedSaveStaysPinnedAndRejectsStaleSharedWrite(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("old"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	document := validDocument("scenario")
	document.Contracts = []Contract{{
		ID:       "orders",
		Name:     "Orders",
		Mode:     "linked",
		Document: jsonx.RawMessage(api.Draft.Document),
		Source:   &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version},
	}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := designs.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: apiDocument("new"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	titleOnly := created.Draft.Document
	titleOnly.Title = "renamed"
	pinned, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: titleOnly, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if pinned.Draft.Document.Contracts[0].Source.RevisionID != api.Draft.ID {
		t.Fatalf("unchanged linked contract moved to revision %d, want %d", pinned.Draft.Document.Contracts[0].Source.RevisionID, api.Draft.ID)
	}

	changed := pinned.Draft.Document
	changed.Contracts[0].Document = jsonx.RawMessage(apiDocument("canvas edit"))
	_, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 2, Document: changed, Source: "ui"})
	var conflict *LinkedConflictError
	if !errors.As(err, &conflict) || conflict.ContractID != "orders" || conflict.Version != advanced.Design.Version {
		t.Fatalf("linked conflict = %#v", err)
	}
	after, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Scenario.Version != 2 || len(after.Revisions) != 2 {
		t.Fatalf("failed linked save changed scenario: %+v", after)
	}
}

func TestRepo_CreateRejectsLinkedSnapshotThatDiffersFromPinnedRevision(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("pinned"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	document := validDocument("scenario")
	document.Contracts = []Contract{{
		ID: "orders", Name: "Orders", Mode: "linked", Document: jsonx.RawMessage(apiDocument("edited")),
		Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version},
	}}

	if _, err = repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Create error = %v, want invalid mismatch", err)
	}
	list, err := repo.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 0 {
		t.Fatalf("mismatched create wrote scenarios: %+v", list)
	}
}

func TestEqualJSONPreservesLargeIntegerDifferences(t *testing.T) {
	equal, err := equalJSON([]byte(`{"value":9007199254740992}`), []byte(`{"value":9007199254740993}`))
	if err != nil {
		t.Fatal(err)
	}
	if equal {
		t.Fatal("equalJSON conflated distinct integers above IEEE-754 exact range")
	}
}

func TestRepo_CopySaveNeverMutatesSourceAPI(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("old"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	document := validDocument("scenario")
	document.Contracts = []Contract{{ID: "orders", Name: "Orders", Mode: "copy", Document: jsonx.RawMessage(api.Draft.Document), Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: 0}}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	changed := created.Draft.Document
	changed.Contracts[0].Document = jsonx.RawMessage(apiDocument("copy edit"))
	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: changed, Source: "ui"}); err != nil {
		t.Fatal(err)
	}
	after, err := designs.Detail(t.Context(), api.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if after.Design.Version != 1 || after.Draft.ID != api.Draft.ID {
		t.Fatalf("copy edit changed source API: %+v", after.Design)
	}
}

func TestRepo_LinkedSaveUpdatesAPIAndScenarioAtomically(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("old"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	document := validDocument("scenario")
	document.Contracts = []Contract{{ID: "orders", Name: "Orders", Mode: "linked", Document: jsonx.RawMessage(api.Draft.Document), Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	changed := created.Draft.Document
	changed.Contracts[0].Document = jsonx.RawMessage(apiDocument("canvas edit"))

	result, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: changed, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	apiAfter, err := designs.Detail(t.Context(), api.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	if result.Scenario.Version != 2 || apiAfter.Design.Version != 2 {
		t.Fatalf("scenario version=%d API version=%d", result.Scenario.Version, apiAfter.Design.Version)
	}
	source := result.Draft.Document.Contracts[0].Source
	if source.Version != apiAfter.Draft.Version || source.RevisionID != apiAfter.Draft.ID {
		t.Fatalf("source = %+v, API draft = %+v", source, apiAfter.Draft.RevisionSummary)
	}
}

func TestRepo_LinkedAPISaveRollsBackWhenScenarioValidationFails(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("old"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	document := validDocument("scenario")
	document.Contracts = []Contract{{ID: "orders", Name: "Orders", Mode: "linked", Document: jsonx.RawMessage(api.Draft.Document), Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	changed := created.Draft.Document
	changed.Contracts[0].Document = jsonx.RawMessage(apiDocument("canvas edit"))
	changed.Messages = []Message{{ID: "invalid", FromID: "missing", ToID: "missing", Kind: "request"}}

	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: changed, Source: "mcp"}); !errors.Is(err, ErrInvalid) {
		t.Fatalf("Save error = %v", err)
	}
	apiAfter, err := designs.Detail(t.Context(), api.Design.ID)
	if err != nil {
		t.Fatal(err)
	}
	scenarioAfter, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if apiAfter.Design.Version != 1 || len(apiAfter.Revisions) != 1 {
		t.Fatalf("failed scenario save changed API: %+v", apiAfter)
	}
	if scenarioAfter.Scenario.Version != 1 || len(scenarioAfter.Revisions) != 1 {
		t.Fatalf("failed scenario save changed scenario: %+v", scenarioAfter)
	}
}

func TestRepo_RefreshKeepsBrokenOperationBinding(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	api, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("old"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	operationKey := firstOperationKey(t, api.Draft.Document)
	document := validDocument("scenario")
	document.Participants = []Participant{{ID: "a", Kind: "client"}, {ID: "b", Kind: "service"}}
	document.Messages = []Message{{ID: "call", FromID: "a", ToID: "b", Kind: "request", Operation: &OperationBinding{ContractID: "orders", OperationKey: operationKey}}}
	document.Contracts = []Contract{{ID: "orders", Name: "Orders", Mode: "linked", Document: jsonx.RawMessage(api.Draft.Document), Source: &ContractSource{DesignID: api.Design.ID, RevisionID: api.Draft.ID, Version: api.Draft.Version}}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	advanced, err := designs.Save(t.Context(), api.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{}}`, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	revisionID := advanced.Draft.ID

	refreshed, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "refresh_contract", ContractID: "orders", RevisionID: &revisionID}}})
	if err != nil {
		t.Fatal(err)
	}
	if refreshed.Draft.Document.Messages[0].Operation.OperationKey != operationKey {
		t.Fatalf("binding was remapped: %+v", refreshed.Draft.Document.Messages[0].Operation)
	}
	if len(refreshed.Diagnostics) != 1 || refreshed.Diagnostics[0].Severity != "warning" {
		t.Fatalf("diagnostics = %+v", refreshed.Diagnostics)
	}
}

func TestRepo_ImportHistoricalRevisionUsesPinnedRevisionVersion(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	apiV1, err := designs.Create(t.Context(), apidesign.CreateInput{Name: "Orders", Document: apiDocument("v1"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	apiV2, err := designs.Save(t.Context(), apiV1.Design.ID, apidesign.SaveInput{ExpectedVersion: 1, Document: apiDocument("v2"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("scenario"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	revisionID := apiV1.Draft.ID
	imported, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{
		Type: "import_contract", ID: "orders", DesignID: apiV1.Design.ID, RevisionID: &revisionID, Mode: "linked",
	}}})
	if err != nil {
		t.Fatal(err)
	}
	source := imported.Draft.Document.Contracts[0].Source
	if source.Version != apiV1.Draft.Version || source.Version == apiV2.Draft.Version {
		t.Fatalf("historical source = %+v; v1=%d current=%d", source, apiV1.Draft.Version, apiV2.Draft.Version)
	}

	titleOnly := imported.Draft.Document
	titleOnly.Title = "renamed"
	if _, err = repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 2, Document: titleOnly, Source: "ui"}); err != nil {
		t.Fatalf("unchanged historical pin should save without touching current API: %v", err)
	}
}

func TestRepo_MaterializeIsAtomic(t *testing.T) {
	db := testkit.NewDB(t)
	cfg := &config.Config{MaxBody: 2_000_000}
	designs := apidesign.NewRepo(db, cfg)
	repo := NewRepo(db, cfg, designs)
	document := validDocument("scenario")
	document.Contracts = []Contract{{ID: "orders", Name: "Orders", Mode: "copy", Document: jsonx.RawMessage(apiDocument("copy"))}}
	created, err := repo.Create(t.Context(), CreateInput{Document: document, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{
		{Type: "materialize_contract", ContractID: "orders"},
		{Type: "remove_participant", ID: "missing"},
	}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("failed materialize error = %v", err)
	}
	designList, err := designs.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(designList) != 0 {
		t.Fatalf("failed materialize left API designs: %+v", designList)
	}

	result, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "materialize_contract", ContractID: "orders"}}})
	if err != nil {
		t.Fatal(err)
	}
	contract := result.Draft.Document.Contracts[0]
	if contract.Mode != "linked" || contract.Source == nil || contract.Source.Version != 1 {
		t.Fatalf("materialized contract = %+v", contract)
	}
	designList, err = designs.List(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if len(designList) != 1 || designList[0].ID != contract.Source.DesignID {
		t.Fatalf("designs = %+v", designList)
	}
}

func apiDocument(summary string) string {
	return `{"openapi":"3.1.0","info":{"title":"Orders","version":"1"},"paths":{"/orders":{"get":{"summary":"` + summary + `","responses":{"200":{"description":"OK"}}}}}}`
}

func firstOperationKey(t *testing.T, raw string) string {
	t.Helper()
	value, err := decodeJSONValue([]byte(raw))
	if err != nil {
		t.Fatal(err)
	}
	document := value.(map[string]any)
	operation := document["paths"].(map[string]any)["/orders"].(map[string]any)["get"].(map[string]any)
	key, _ := operation[apidesign.OperationKey].(string)
	if key == "" {
		t.Fatal("API operation has no stable key")
	}
	return key
}
