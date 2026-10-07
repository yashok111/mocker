package designscenario

import (
	"errors"
	"os"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func eventFixture(t *testing.T) Document {
	t.Helper()
	raw, err := os.ReadFile("testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := jsonx.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestDocumentDecoderRejectsUnknownRootFields(t *testing.T) {
	for _, version := range []string{"1", "2", "3"} {
		t.Run(version, func(t *testing.T) {
			raw := `{"formatVersion":` + version + `,"title":"Example","participants":[],"messages":[],"fragments":[],"contracts":[],"unknownRoot":true}`
			var doc Document
			if err := jsonx.Unmarshal([]byte(raw), &doc); err == nil {
				t.Fatal("document decoder silently accepted an unknown root field")
			}
		})
	}
}

func hasEventError(doc Document, pointer string) bool {
	for _, d := range validateDocument(doc) {
		if d.Pointer == pointer && d.Severity == "error" {
			return true
		}
	}
	return false
}

func TestEventModelStructuralValidation(t *testing.T) {
	for _, tc := range []struct {
		name, pointer string
		change        func(*Document)
	}{
		{"legacy model", "/eventModel", func(d *Document) { d.FormatVersion = 2 }},
		{"legacy binding", "/messages/0/eventBindings", func(d *Document) { d.FormatVersion = 2; d.EventModel = nil }},
		{"missing array", "/eventModel/channels", func(d *Document) { d.EventModel.Channels = nil }},
		{"invalid id", "/eventModel/servers/0/id", func(d *Document) { d.EventModel.Servers[0].ID = "bad/id" }},
		{"missing owner", "/eventModel/contracts/0/participantId", func(d *Document) { d.EventModel.Contracts[0].ParticipantID = "gone" }},
		{"queue owner", "/eventModel/contracts/0/participantId", func(d *Document) { d.EventModel.Contracts[0].ParticipantID = "broker" }},
		{"duplicate owner", "/eventModel/contracts/1/participantId", func(d *Document) { d.EventModel.Contracts[1].ParticipantID = "orders" }},
		{"missing server", "/eventModel/channels/0/serverIds/0", func(d *Document) { d.EventModel.Channels[0].ServerIDs[0] = "gone" }},
		{"missing schema", "/eventModel/messages/0/payloadSchemaId", func(d *Document) { d.EventModel.Messages[0].PayloadSchemaID = "gone" }},
		{"missing channel", "/eventModel/contracts/0/operations/0/channelId", func(d *Document) { d.EventModel.Contracts[0].Operations[0].ChannelID = "gone" }},
		{"wrong channel message", "/eventModel/contracts/0/operations/0/messageId", func(d *Document) { d.EventModel.Channels[0].MessageIDs = []string{} }},
		{"wrong action", "/eventModel/contracts/0/operations/0/action", func(d *Document) { d.EventModel.Contracts[0].Operations[0].Action = "publish" }},
		{"wrong direction", "/messages/0/eventBindings/0", func(d *Document) { d.EventModel.Contracts[0].Operations[0].Action = "receive" }},
		{"HTTP overlap", "/messages/0/eventBindings", func(d *Document) { d.Messages[0].Operation = &OperationBinding{ContractID: "api", OperationKey: "op"} }},
		{"non event binding", "/messages/0/eventBindings", func(d *Document) { d.Messages[0].Kind = "note" }},
		{"removed operation", "/messages/0/eventBindings/0/operationId", func(d *Document) { d.EventModel.Contracts[0].Operations = nil }},
		{"duplicate channel", "/eventModel/channels/1/address", func(d *Document) {
			c := d.EventModel.Channels[0]
			c.ID = "duplicate"
			d.EventModel.Channels = append(d.EventModel.Channels, c)
		}},
		{"bad schema JSON", "/eventModel/schemas/0/schemaJSON", func(d *Document) { d.EventModel.Schemas[0].SchemaJSON = `{` }},
		{"bad example JSON", "/eventModel/messages/0/examples/0/payloadJSON", func(d *Document) { d.EventModel.Messages[0].Examples[0].PayloadJSON = `1 2` }},
		{"empty example JSON", "/eventModel/messages/0/examples/0/payloadJSON", func(d *Document) { d.EventModel.Messages[0].Examples[0].PayloadJSON = "" }},
		{"empty server protocol", "/eventModel/servers/0/protocol", func(d *Document) { d.EventModel.Servers[0].Protocol = "" }},
		{"empty server auth", "/eventModel/servers/0/auth", func(d *Document) { d.EventModel.Servers[0].Auth = "" }},
		{"negative partitions", "/eventModel/channels/0/kafka/partitions", func(d *Document) { n := int32(-1); d.EventModel.Channels[0].Kafka = &EventChannelKafka{Partitions: &n} }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc := eventFixture(t)
			tc.change(&doc)
			if !hasEventError(doc, tc.pointer) {
				t.Fatalf("missing error at %s: %+v", tc.pointer, validateDocument(doc))
			}
		})
	}
}

func TestEventContractAcceptsLegacyParticipantID(t *testing.T) {
	doc := eventFixture(t)
	for i := range doc.Participants {
		if doc.Participants[i].ID == "orders" {
			doc.Participants[i].ID = "orders:legacy"
		}
	}
	for i := range doc.Messages {
		if doc.Messages[i].FromID == "orders" {
			doc.Messages[i].FromID = "orders:legacy"
		}
		if doc.Messages[i].ToID == "orders" {
			doc.Messages[i].ToID = "orders:legacy"
		}
	}
	doc.EventModel.Contracts[0].ParticipantID = "orders:legacy"
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("legacy participant ID rejected: %+v", ds)
	}
}

func TestEventServerRequiresProtocolAndAuth(t *testing.T) {
	for _, field := range []string{"protocol", "auth"} {
		t.Run(field, func(t *testing.T) {
			doc := eventFixture(t)
			raw, err := jsonx.Marshal(doc.EventModel.Servers[0])
			if err != nil {
				t.Fatal(err)
			}
			var fields map[string]any
			if err := jsonx.Unmarshal(raw, &fields); err != nil {
				t.Fatal(err)
			}
			delete(fields, field)
			raw, err = jsonx.Marshal(fields)
			if err != nil {
				t.Fatal(err)
			}
			var server EventServer
			if err := jsonx.Unmarshal(raw, &server); err != nil {
				t.Fatal(err)
			}
			doc.EventModel.Servers[0] = server
			if !hasEventError(doc, "/eventModel/servers/0/"+field) {
				t.Fatalf("missing %s was accepted", field)
			}
		})
	}
}

func TestEventIncompleteBusinessFieldsAreSaveable(t *testing.T) {
	doc := eventFixture(t)
	doc.EventModel.Messages[0].PayloadSchemaID = ""
	doc.EventModel.Schemas[0].SchemaJSON = ""
	doc.EventModel.Channels[0].Address = ""
	doc.EventModel.Servers[0].Host = ""
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("incomplete model rejected: %+v", ds)
	}
	// Unknown schema keywords are diagnosed by export readiness, not save.
	doc.EventModel.Schemas[0].SchemaJSON = `{"futureKeyword":true}`
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("unknown schema keyword blocked save: %+v", ds)
	}
}

func TestEventCommandMigratesAndBatchIsAtomic(t *testing.T) {
	repo := newTestRepo(t)
	base := validDocument("Migrating")
	fixture := eventFixture(t)
	base.Participants = fixture.Participants
	base.Messages = slices.Clone(fixture.Messages)
	for i := range base.Messages {
		base.Messages[i].EventBindings = nil
	}
	created, err := repo.Create(t.Context(), CreateInput{Document: base, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	updated, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "ui", Commands: []Command{
		{Type: "set_event_model", EventModel: fixture.EventModel},
		{Type: "upsert_message", Message: &fixture.Messages[0]},
		{Type: "upsert_message", Message: &fixture.Messages[1]},
	}})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Draft.Document.FormatVersion != 3 || !reflect.DeepEqual(updated.Draft.Document.EventModel, fixture.EventModel) {
		t.Fatalf("bad migration: %+v", updated.Draft.Document)
	}
	bad := fixture.Messages[0]
	bad.EventBindings[0].OperationID = "gone"
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 2, Source: "ui", Commands: []Command{{Type: "set_title", Title: "should rollback"}, {Type: "upsert_message", Message: &bad}}})
	var invalid *InvalidError
	if !errors.As(err, &invalid) {
		t.Fatalf("expected validation error: %v", err)
	}
	got, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Scenario.Version != 2 || got.Draft.Document.Title != "Migrating" {
		t.Fatalf("batch changed revision: %+v", got)
	}
}

func TestEventRunMarksKafkaStepsSkipped(t *testing.T) {
	revision := runRevision()
	revision.Document.FormatVersion = 3
	event := eventFixture(t).Messages[0]
	revision.Document.Messages = append(revision.Document.Messages, event)
	initial, err := PrepareRun(revision, "run-event", "mixed", "ui", nil)
	if err != nil {
		t.Fatal(err)
	}
	step := initial.Steps[len(initial.Steps)-1]
	if step.Status != "skipped" || step.Reason != "event_execution_unsupported" {
		t.Fatalf("event step: %+v", step)
	}
	revision.Document.Messages = []Message{event}
	if _, err := PrepareRun(revision, "run-only-event", "event", "ui", nil); err == nil || !strings.Contains(err.Error(), "Kafka runtime") {
		t.Fatalf("pure event run: %v", err)
	}
}

func TestEventJSONLimitsAndExactNumbers(t *testing.T) {
	doc := eventFixture(t)
	doc.EventModel.Schemas[0].SchemaJSON = `{"const":900719925474099312345678901234567890}`
	doc.EventModel.Messages[0].Examples[0].PayloadJSON = `{"id":900719925474099312345678901234567890}`
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("precise numbers rejected: %+v", ds)
	}
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if got := created.Draft.Document.EventModel.Schemas[0].SchemaJSON; got != doc.EventModel.Schemas[0].SchemaJSON {
		t.Fatalf("schema number changed: %s", got)
	}
	for _, tc := range []struct{ name, source, pointer string }{
		{"depth", `{"nested":` + strings.Repeat("[", 65) + `null` + strings.Repeat("]", 65) + `}`, "/eventModel/schemas/0/schemaJSON"},
		{"nodes", `{"nodes":[` + strings.Repeat(`null,`, 10000) + `null]}`, "/eventModel/schemas/0/schemaJSON"},
		{"bytes", `{"x":"` + strings.Repeat("a", maxEventJSONBytes) + `"}`, "/eventModel/schemas/0/schemaJSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture := eventFixture(t)
			fixture.EventModel.Schemas[0].SchemaJSON = tc.source
			if !hasEventError(fixture, tc.pointer) {
				t.Fatalf("accepted %s", tc.name)
			}
		})
	}
}

func TestEventHistoryDiffRestoreAndClone(t *testing.T) {
	repo := newTestRepo(t)
	base := validDocument("History")
	created, err := repo.Create(t.Context(), CreateInput{Document: base, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	originalHash := created.Draft.Hash
	doc := eventFixture(t)
	doc.Title = "History"
	updated, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Draft.Hash == originalHash || updated.Draft.Summary != "Изменена событийная модель" {
		t.Fatalf("hash or summary: %s, %s", updated.Draft.Hash, updated.Draft.Summary)
	}
	old, err := repo.Revision(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if old.Hash != originalHash || old.Document.FormatVersion != 1 || old.Document.EventModel != nil {
		t.Fatalf("legacy revision changed: %+v", old)
	}
	diff, err := repo.Diff(t.Context(), created.Scenario.ID, created.Draft.ID, updated.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, change := range diff.Changes {
		if change.Pointer == "/document/eventModel" {
			found = true
		}
	}
	if !found {
		t.Fatalf("eventModel absent from diff: %+v", diff.Changes)
	}
	restored, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: 2, RevisionID: created.Draft.ID, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Draft.Document.FormatVersion != 1 || restored.Draft.Document.EventModel != nil || restored.Draft.Hash != originalHash {
		t.Fatalf("restore changed legacy: %+v", restored.Draft)
	}
	cloned := cloneRunDocument(doc)
	cloned.EventModel.Channels[0].ServerIDs[0] = "other"
	cloned.EventModel.Contracts[0].Operations[0].ID = "other"
	cloned.Messages[0].EventBindings[0].OperationID = "other"
	if !reflect.DeepEqual(doc, eventFixture(t)) { // Title intentionally differs.
		doc.Title = "Order events"
		if !reflect.DeepEqual(doc, eventFixture(t)) {
			t.Fatal("run clone shares event data")
		}
	}
}

func TestEventCommandWireRules(t *testing.T) {
	for _, raw := range []string{
		`{"type":"set_event_model"}`,
		`{"type":"set_event_model","eventModel":null}`,
		`{"type":"set_event_model","eventModel":{},"title":"extra"}`,
		`{"type":"set_event_model","eventModel":{"servers":[],"channels":[],"messages":[],"schemas":[],"contracts":[],"extra":1}}`,
	} {
		var command Command
		if err := jsonx.Unmarshal([]byte(raw), &command); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
	var command Command
	if err := jsonx.Unmarshal([]byte(`{"type":"set_event_model","eventModel":{"servers":[],"channels":[],"messages":[],"schemas":[],"contracts":[]}}`), &command); err != nil || command.EventModel == nil {
		t.Fatalf("valid command: %+v, %v", command, err)
	}
	for _, raw := range []string{
		`{"formatVersion":2,"eventModel":null,"title":"","participants":[],"messages":[],"fragments":[],"contracts":[]}`,
		`{"formatVersion":2,"title":"","participants":[],"messages":[{"id":"e","eventBindings":null}],"fragments":[],"contracts":[]}`,
		`{"formatVersion":3,"title":"","participants":[],"messages":[{"id":"e","eventBindings":null}],"fragments":[],"contracts":[]}`,
		`{"formatVersion":3,"title":"","participants":[],"messages":[],"fragments":[],"contracts":[],"eventModel":{"servers":[],"channels":[],"messages":[],"schemas":[],"contracts":[],"unsupported":1}}`,
	} {
		var doc Document
		if err := jsonx.Unmarshal([]byte(raw), &doc); err == nil {
			t.Fatalf("accepted legacy event field: %s", raw)
		}
	}
}

func TestEventV3KeepsFragmentTreeRules(t *testing.T) {
	doc := branchDocument(t, strings.Replace(branchFixture, `"formatVersion":2`, `"formatVersion":3`, 1))
	if ds := ValidateFragments(doc); len(ds) != 0 {
		t.Fatalf("valid v3 fragment tree: %+v", ds)
	}
	doc.Fragments[1].ParentBranchID = "gone"
	if !hasEventError(doc, "/fragments/1/parentBranchId") {
		t.Fatal("v3 skipped fragment tree validation")
	}
	if ds := ValidateFragments(doc); len(ds) == 0 {
		t.Fatal("v3 skipped public fragment validation")
	}
}

func TestEventDirectAndRepeatedBindings(t *testing.T) {
	doc := eventFixture(t)
	doc.Messages = []Message{{
		ID: "direct", FromID: "orders", ToID: "notifications", Kind: "event",
		EventBindings: []EventBinding{
			{ContractID: "ordersContract", OperationID: "publishOrder"},
			{ContractID: "notificationsContract", OperationID: "consumeOrder"},
		},
	}}
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("direct event rejected: %+v", ds)
	}
	doc.Messages = append(doc.Messages, Message{ID: "repeat", FromID: "orders", ToID: "notifications", Kind: "event", EventBindings: slices.Clone(doc.Messages[0].EventBindings)})
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("repeat rejected: %+v", ds)
	}
	doc.Messages[0].EventBindings[1].OperationID = "publishOrder"
	if !hasEventError(doc, "/messages/0/eventBindings/1/operationId") {
		t.Fatal("cross-contract operation accepted")
	}
}

func TestEventRemovalRequiresReferenceCleanup(t *testing.T) {
	doc := eventFixture(t)
	check := func(pointer string) {
		t.Helper()
		if !hasEventError(doc, pointer) {
			t.Fatalf("missing error at %s: %+v", pointer, validateDocument(doc))
		}
	}
	doc.EventModel.Servers = nil
	check("/eventModel/channels/0/serverIds/0")
	doc = eventFixture(t)
	doc.EventModel.Schemas = nil
	check("/eventModel/messages/0/payloadSchemaId")
	doc = eventFixture(t)
	doc.EventModel.Messages = nil
	check("/eventModel/channels/0/messageIds/0")
	doc = eventFixture(t)
	doc.EventModel.Channels = nil
	check("/eventModel/contracts/0/operations/0/channelId")
	doc = eventFixture(t)
	doc.EventModel.Contracts = nil
	check("/messages/0/eventBindings/0/contractId")
	doc = eventFixture(t)
	doc.Participants = doc.Participants[1:]
	check("/eventModel/contracts/0/participantId")
}

func TestEventDraftsCASAndRestore(t *testing.T) {
	repo := newTestRepo(t)
	doc := eventFixture(t)
	drafts := map[string]string{"/event-schema/orderPayload": `{"schemaJSON":"{"}`}
	created, err := repo.Create(t.Context(), CreateInput{Document: doc, FormDrafts: drafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(created.Draft.FormDrafts, drafts) {
		t.Fatalf("drafts changed: %+v", created.Draft.FormDrafts)
	}
	doc.EventModel.Schemas[0].SchemaJSON = `{"type":"string"}`
	updated, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, FormDrafts: drafts, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := repo.Save(t.Context(), created.Scenario.ID, SaveInput{ExpectedVersion: 1, Document: doc, Source: "ui"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale event save: %v", err)
	}
	restored, err := repo.Restore(t.Context(), created.Scenario.ID, RestoreInput{ExpectedVersion: 2, RevisionID: created.Draft.ID, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	if restored.Draft.Document.EventModel.Schemas[0].SchemaJSON != created.Draft.Document.EventModel.Schemas[0].SchemaJSON || !reflect.DeepEqual(restored.Draft.FormDrafts, drafts) || updated.Draft.Hash == restored.Draft.Hash {
		t.Fatalf("restore lost model or drafts: %+v", restored.Draft)
	}
}

func TestEventDocumentRoundTrip(t *testing.T) {
	doc := eventFixture(t)
	if ds := validateDocument(doc); len(ds) != 0 {
		t.Fatalf("v3 rejected: %+v", ds)
	}
	repo := newTestRepo(t)
	created, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "ui"})
	if err != nil {
		t.Fatal(err)
	}
	got, err := repo.Revision(t.Context(), created.Scenario.ID, created.Draft.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got.Document, doc) {
		t.Fatalf("round trip changed document: got %+v", got.Document)
	}
}
