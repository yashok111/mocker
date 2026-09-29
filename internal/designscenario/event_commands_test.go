package designscenario

import (
	"errors"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestEventCommandsUpsertRoutesAndSemanticLinksAtomically(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: eventFixture(t), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	retry := created.Draft.Document.EventModel.Channels[0]
	retry.ID, retry.Name, retry.Address = "retry", "Retry", "orders.retry"
	api := EventAPILink{ContractID: "http-orders", OperationKey: "opaque-key"}
	state := EventStateLink{ContractID: "http-orders", DiagramID: "order", TransitionID: "created"}
	updated, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{
		{Type: "upsert_event_channel", EventChannel: &retry},
		{Type: "set_event_failure_routes", ContractID: "notificationsContract", ID: "consumeOrder", FailureRoutes: &EventFailureRoutes{RetryChannelID: "retry"}},
		{Type: "upsert_event_api_link", ContractID: "notificationsContract", ID: "consumeOrder", APILink: &api},
		{Type: "upsert_event_api_link", ContractID: "notificationsContract", ID: "consumeOrder", APILink: &api},
		{Type: "upsert_event_state_link", ContractID: "notificationsContract", ID: "consumeOrder", StateLink: &state},
		{Type: "upsert_event_state_link", ContractID: "notificationsContract", ID: "consumeOrder", StateLink: &state},
	}})
	if err != nil {
		t.Fatal(err)
	}
	operation := updated.Draft.Document.EventModel.Contracts[1].Operations[0]
	if updated.Scenario.Version != 2 || operation.FailureRoutes.RetryChannelID != "retry" || !reflect.DeepEqual(operation.APILinks, []EventAPILink{api}) || !reflect.DeepEqual(operation.StateLinks, []EventStateLink{state}) {
		t.Fatalf("updated=%+v", updated)
	}
	cleared, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 2, Source: "ui", Commands: []Command{
		{Type: "set_event_failure_routes", ContractID: "notificationsContract", ID: "consumeOrder"},
		{Type: "remove_event_api_link", ContractID: "notificationsContract", ID: "consumeOrder", APILink: &api},
		{Type: "remove_event_state_link", ContractID: "notificationsContract", ID: "consumeOrder", StateLink: &state},
	}})
	if err != nil {
		t.Fatal(err)
	}
	operation = cleared.Draft.Document.EventModel.Contracts[1].Operations[0]
	if operation.FailureRoutes != nil || len(operation.APILinks) != 0 || len(operation.StateLinks) != 0 {
		t.Fatalf("clear=%+v", operation)
	}
}

func TestEventCommandsGuardDependenciesAndRollbackBatch(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: eventFixture(t), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "set_title", Title: "must rollback"}, {Type: "remove_event_channel", ID: "ordersEvents"}}})
	var invalid *InvalidError
	if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Diagnostics, func(d Diagnostic) bool { return strings.Contains(d.Message, "ordersContract/publishOrder") }) {
		t.Fatalf("dependency error=%v", err)
	}
	got, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil || got.Scenario.Version != 1 || got.Draft.Document.Title != created.Draft.Document.Title {
		t.Fatalf("rollback=%+v err=%v", got, err)
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "remove_event_schema", ID: "orderPayload"}}})
	if !errors.As(err, &invalid) || !slices.ContainsFunc(invalid.Diagnostics, func(d Diagnostic) bool { return strings.Contains(d.Message, "message:orderCreated") }) {
		t.Fatalf("schema dependency=%v", err)
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 0, Source: "ui", Commands: []Command{{Type: "set_title", Title: "stale"}}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("CAS error=%v", err)
	}
}

func TestEventCommandsInitializeModelAndRejectAbsentRemoval(t *testing.T) {
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: validDocument("Empty"), Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "remove_event_server", ID: "missing"}}})
	if !errors.Is(err, ErrInvalid) {
		t.Fatalf("remove absent=%v", err)
	}
	server := EventServer{ID: "kafka", Name: "Kafka", Host: "localhost:9092", Protocol: "kafka", Auth: "none"}
	updated, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{{Type: "upsert_event_server", EventServer: &server}}})
	if err != nil || updated.Draft.Document.FormatVersion != 3 || updated.Draft.Document.EventModel == nil || len(updated.Draft.Document.EventModel.Servers) != 1 {
		t.Fatalf("initialize=%+v err=%v", updated, err)
	}
	if updated.Draft.Document.EventModel.Channels == nil || updated.Draft.Document.EventModel.Contracts == nil {
		t.Fatal("empty collections were not initialized")
	}
}

func TestEventCommandJSONVariantsAreStrict(t *testing.T) {
	for _, raw := range []string{
		`{"type":"upsert_event_server"}`,
		`{"type":"upsert_event_channel","eventChannel":null}`,
		`{"type":"set_event_failure_routes","contractId":"c","id":"o","failureRoutes":null}`,
		`{"type":"upsert_event_api_link","contractId":"c","id":"o","apiLink":{"contractId":"http","operationKey":"op"},"stateLink":{"contractId":"http","diagramId":"d","transitionId":"t"}}`,
	} {
		var command Command
		if err := jsonx.Unmarshal([]byte(raw), &command); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var command Command
	if err := jsonx.Unmarshal([]byte(`{"type":"set_event_failure_routes","contractId":"c","id":"o"}`), &command); err != nil || command.FailureRoutes != nil {
		t.Fatalf("omitted clear=%+v %v", command, err)
	}
}
