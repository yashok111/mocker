package scenarioexport

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func TestAsyncAPIUsesDraft07WithoutDialectAnnotation(t *testing.T) {
	for _, tc := range []struct {
		name, schema, example string
		blocked               bool
	}{
		{"format assertion", `{"type":"string","format":"email"}`, `"not-an-email"`, true},
		{"tuple items", `{"type":"array","items":[{"type":"string"}],"additionalItems":false}`, `["order"]`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := eventFixture(t)
			rev.Document.EventModel.Schemas[0].SchemaJSON = tc.schema
			rev.Document.EventModel.Messages[0].Examples[0].PayloadJSON = tc.example
			_, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
			var blocked *BlockedError
			if tc.blocked && !errors.As(err, &blocked) {
				t.Fatalf("invalid Draft07 example accepted: %v", err)
			}
			if !tc.blocked && err != nil {
				t.Fatalf("valid Draft07 tuple rejected: %v", err)
			}
		})
	}
}

func TestAsyncAPIUnicodeKafkaIdentifiers(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.EventModel.Contracts[1].Operations[0].Kafka.GroupID = strings.Repeat("я", 256)
	if _, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "notificationsContract"}); err != nil {
		t.Fatalf("256-character consumer group rejected: %v", err)
	}
}

func TestAsyncAPIUnboundOperationHasNoticeAndRemainsExported(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.Messages = []designscenario.Message{}
	artifact, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(artifact.Content, "publishOrder") {
		t.Fatal("unbound operation omitted")
	}
	for _, diagnostic := range artifact.Diagnostics {
		if diagnostic.Code == "event_operation_unbound" && diagnostic.Severity == "info" {
			return
		}
	}
	t.Fatal("missing unbound operation notice")
}

func TestAsyncAPISchemaDiagnosticPointsAtSourceField(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.EventModel.Schemas[0].SchemaJSON = `{"properties":{"nested":{"unsupported":true}}}`
	_, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected schema error: %v", err)
	}
	for _, d := range blocked.Diagnostics {
		if d.Code == "event_schema_invalid" && d.Target != nil && d.Target.ID == "orderPayload" {
			if d.Pointer != "/eventModel/schemas/0/schemaJSON" {
				t.Fatalf("pointer must address the JSON text field: %s", d.Pointer)
			}
			return
		}
	}
	t.Fatal("missing schema diagnostic")
}

func TestAsyncAPIInvalidSchemaTypePointsAtSourceField(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.EventModel.Schemas[0].SchemaJSON = `{"type":"mystery"}`
	_, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) {
		t.Fatalf("expected schema error: %v", err)
	}
	for _, d := range blocked.Diagnostics {
		if d.Code == "event_schema_invalid" && d.Target != nil && d.Target.ID == "orderPayload" && d.Target.Kind == "event-schema" && d.Pointer == "/eventModel/schemas/0/schemaJSON" {
			return
		}
	}
	t.Fatalf("missing source schema diagnostic: %+v", blocked.Diagnostics)
}

func TestAsyncAPIDraftDiagnosticHasNavigableTarget(t *testing.T) {
	for _, tc := range []struct {
		name, draft, kind, id, pointer string
	}{
		{"schema", `{"/event-schema/orderPayload/schemaJSON":{"source":"{","propertySource":"{"}}`, "event-schema", "orderPayload", "/event-schema/orderPayload/schemaJSON"},
		{"example", `{"/event-message/orderCreated/examples/0/payloadJSON":{"source":"{","propertySource":"{"}}`, "event-message", "orderCreated", "/event-message/orderCreated/examples/0/payloadJSON"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := eventFixture(t)
			rev.FormDrafts = map[string]string{"all": tc.draft}
			_, err := New(nil, 1<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
			var blocked *BlockedError
			if !errors.As(err, &blocked) {
				t.Fatalf("expected draft error: %v", err)
			}
			for _, d := range blocked.Diagnostics {
				if d.Code == "event_forms_pending" && d.Target != nil && d.Target.Kind == tc.kind && d.Target.ID == tc.id && d.Pointer == tc.pointer {
					return
				}
			}
			t.Fatalf("missing navigable draft diagnostic: %+v", blocked.Diagnostics)
		})
	}
}

func TestAsyncAPIArchiveWithWarningsIsDeterministic(t *testing.T) {
	rev := eventFixture(t)
	model := rev.Document.EventModel
	model.Servers[0].Auth = "unspecified"
	for _, id := range []string{"second", "third", "fourth"} {
		server := model.Servers[0]
		server.ID = id
		model.Servers = append(model.Servers, server)
		model.Channels[0].ServerIDs = append(model.Channels[0].ServerIDs, id)
	}
	svc := New(nil, 1<<20)
	items := []Request{{Format: AsyncAPIJSON, ContractID: "ordersContract"}}
	first, err := svc.ExportArchive(rev, items)
	if err != nil {
		t.Fatal(err)
	}
	for range 12 {
		archive, err := svc.ExportArchive(rev, items)
		if err != nil {
			t.Fatal(err)
		}
		if archive.ContentBase64 != first.ContentBase64 {
			t.Fatal("same immutable revision produced different ZIP bytes")
		}
	}
}

func TestDocumentationCancellationStopsContractValidation(t *testing.T) {
	for _, format := range []Format{Markdown, HTML} {
		t.Run(string(format), func(t *testing.T) {
			rev := eventFixture(t)
			rev.Document.Contracts = []designscenario.Contract{{ID: "first"}, {ID: "second"}}
			ctx, cancel := context.WithCancel(t.Context())
			defer cancel()
			calls := 0
			svc := New(func(context.Context, string) ([]designscenario.Diagnostic, error) {
				calls++
				cancel()
				return nil, nil
			}, 1<<20)
			_, err := svc.ExportContext(ctx, rev, Request{Format: format})
			if !errors.Is(err, context.Canceled) || calls != 1 {
				t.Fatalf("canceled request kept validating: calls=%d err=%v", calls, err)
			}
		})
	}
}

func TestAsyncAPILargeContractDoesNotHideOtherOptions(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.EventModel.Contracts[0].Description = strings.Repeat("x", 12000)
	svc := New(nil, 10000)
	options, err := svc.Options(rev)
	if err != nil {
		t.Fatalf("large event artifact hid all export options: %v", err)
	}
	var diagramReady, producerBlocked bool
	for _, option := range options {
		if option.Format == Mermaid {
			diagramReady = option.Ready
		}
		if option.Format == AsyncAPIJSON && option.ContractID == "ordersContract" {
			producerBlocked = !option.Ready
		}
	}
	if !diagramReady || !producerBlocked {
		t.Fatalf("diagramReady=%t producerBlocked=%t", diagramReady, producerBlocked)
	}
	if _, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}); !errors.Is(err, ErrTooLarge) {
		t.Fatalf("single oversized export must return byte-budget error: %v", err)
	}
}
