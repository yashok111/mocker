package scenarioexport

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
)

func eventFixture(t *testing.T) designscenario.Revision {
	t.Helper()
	b, err := os.ReadFile("../designscenario/testdata/events/valid.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc designscenario.Document
	if err := json.Unmarshal(b, &doc); err != nil {
		t.Fatal(err)
	}
	return designscenario.Revision{RevisionSummary: designscenario.RevisionSummary{ID: 7, ScenarioID: 1, Hash: "hash"}, Document: doc}
}

func TestAsyncAPIProducerFixture(t *testing.T) {
	rev := eventFixture(t)
	a, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(a.Content), &doc); err != nil {
		t.Fatal(err)
	}
	if doc["asyncapi"] != "3.0.0" {
		t.Fatalf("asyncapi = %v", doc["asyncapi"])
	}
	ops := doc["operations"].(map[string]any)
	if len(ops) != 1 || ops["publishOrder"].(map[string]any)["action"] != "send" {
		t.Fatalf("operations = %v", ops)
	}
}

func TestAsyncAPIGrammarAndConsumer(t *testing.T) {
	rev := eventFixture(t)
	svc := New(nil, 16<<20)
	for _, id := range []string{"ordersContract", "notificationsContract"} {
		a, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: id})
		if err != nil {
			t.Fatal(err)
		}
		value, err := decodeJSON([]byte(a.Content))
		if err != nil {
			t.Fatal(err)
		}
		grammar, err := officialAsyncAPIGrammar()
		if err != nil {
			t.Fatal(err)
		}
		if err := grammar.Validate(value); err != nil {
			t.Fatalf("%s: %v", id, err)
		}
		doc := value.(map[string]any)
		ops := doc["operations"].(map[string]any)
		if len(ops) != 1 {
			t.Fatalf("%s operations: %v", id, ops)
		}
		if id == "notificationsContract" {
			op := ops["consumeOrder"].(map[string]any)
			if op["action"] != "receive" || op["messages"].([]any)[0].(map[string]any)["$ref"] != "#/channels/ordersEvents/messages/orderCreated" {
				t.Fatal(op)
			}
		}
	}
}

func TestAsyncAPIKeyAndExactNumbers(t *testing.T) {
	rev := eventFixture(t)
	model := rev.Document.EventModel
	model.Schemas = append(model.Schemas, designscenario.EventSchema{ID: "orderKey", Name: "Key", SchemaJSON: `{"type":"integer"}`})
	model.Messages[0].KeySchemaID = "orderKey"
	model.Schemas[0].SchemaJSON = `{"type":"object","properties":{"amount":{"const":123456789012345678901234567890.123456789}}}`
	model.Messages[0].Examples[0].PayloadJSON = `{"amount":123456789012345678901234567890.123456789}`
	svc := New(nil, 16<<20)
	a, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, "123456789012345678901234567890.123456789") {
		t.Fatal("large number changed")
	}
	if !strings.Contains(a.Content, `"allOf":[{"$ref":"#/components/schemas/orderKey"}]`) {
		t.Fatal("key lacks schema wrapper")
	}
	y, err := svc.Export(rev, Request{Format: AsyncAPIYAML, ContractID: "ordersContract"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(y.Content, "123456789012345678901234567890.123456789") {
		t.Fatal("YAML number changed")
	}
}

func TestAsyncAPIReadinessExamplesAndForms(t *testing.T) {
	rev := eventFixture(t)
	svc := New(nil, 16<<20)
	rev.Document.EventModel.Messages[0].Examples[0].PayloadJSON = `{"orderId":42}`
	rev.Document.EventModel.Schemas[0].SchemaJSON = `{"type":"object","required":["orderId"],"properties":{"orderId":{"type":"string"}}}`
	_, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_example_invalid") {
		t.Fatalf("example: %v", err)
	}
	rev.Document.EventModel.Messages[0].Examples[0].PayloadJSON = `{"orderId":"ok"}`
	rev.FormDrafts = map[string]string{"all": `{"/canvas-contract/http":{"source":"x","propertySource":"x"}}`}
	if _, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}); err != nil {
		t.Fatalf("independent HTTP form: %v", err)
	}
	rev.FormDrafts = map[string]string{"all": `{"/event-schema/orderPayload":{"source":"x","propertySource":"x"}}`}
	_, err = svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_forms_pending") {
		t.Fatalf("event form: %v", err)
	}
}

func TestAsyncAPIWrongFamilyCancellationAndArchive(t *testing.T) {
	rev := eventFixture(t)
	svc := New(nil, 16<<20)
	if _, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "missing"}); !errors.Is(err, ErrContractNotFound) {
		t.Fatal(err)
	}
	rev.Document.Contracts = []designscenario.Contract{{ID: "http", Name: "HTTP"}}
	if _, err := svc.Export(rev, Request{Format: AsyncAPIJSON, ContractID: "http"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	if _, err := svc.Export(rev, Request{Format: OpenAPIJSON, ContractID: "ordersContract"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := svc.OptionsContext(ctx, rev); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := svc.ExportContext(ctx, rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, err := svc.ExportArchiveContext(ctx, rev, []Request{{Format: AsyncAPIJSON, ContractID: "ordersContract"}}); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	req := Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}
	a, err := svc.Export(rev, req)
	if err != nil {
		t.Fatal(err)
	}
	archive, err := svc.ExportArchive(rev, []Request{req})
	if err != nil {
		t.Fatal(err)
	}
	data, err := base64.StdEncoding.DecodeString(archive.ContentBase64)
	if err != nil {
		t.Fatal(err)
	}
	z, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		t.Fatal(err)
	}
	if archive.Manifest.Files[0].ContractID != req.ContractID {
		t.Fatal(archive.Manifest.Files[0])
	}
	for _, f := range z.File {
		if f.Name == a.Filename {
			r, e := f.Open()
			if e != nil {
				t.Fatal(e)
			}
			b, e := io.ReadAll(r)
			r.Close()
			if e != nil || string(b) != a.Content {
				t.Fatalf("archive bytes differ: %v", e)
			}
			return
		}
	}
	t.Fatal("export absent from ZIP")
}

func hasCode(ds []Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestAsyncAPISASLBindingsAndDiscriminator(t *testing.T) {
	rev := eventFixture(t)
	m := rev.Document.EventModel
	m.Servers[0].Protocol = "kafka-secure"
	m.Servers[0].Auth = "scramSha256"
	partitions, replicas := int32(3), int32(2)
	m.Channels[0].Kafka = &designscenario.EventChannelKafka{Partitions: &partitions, Replicas: &replicas}
	m.Contracts[1].Operations[0].Kafka.ClientID = "notifications-client"
	m.Schemas = append(m.Schemas, designscenario.EventSchema{ID: "cancelPayload", Name: "Cancellation", SchemaJSON: `{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","const":"cancelled"}}}`})
	m.Messages = append(m.Messages, designscenario.EventMessage{ID: "cancelled", Name: "Cancelled", PayloadSchemaID: "cancelPayload"})
	m.Channels[0].MessageIDs = append(m.Channels[0].MessageIDs, "cancelled")
	m.Channels[0].DiscriminatorProperty = "kind"
	m.Schemas[0].SchemaJSON = `{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","const":"created"}}}`
	m.Messages[0].Examples[0].PayloadJSON = `{"kind":"created"}`
	a, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "notificationsContract"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, `"scramSha256"`) || !strings.Contains(a.Content, `"partitions":3`) || !strings.Contains(a.Content, `"clientId"`) {
		t.Fatal("bindings missing")
	}
	m.Schemas[1].SchemaJSON = `{"type":"object","required":["kind"],"properties":{"kind":{"type":"string","const":"created"}}}`
	_, err = New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "notificationsContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_channel_ambiguous") {
		t.Fatalf("duplicate discriminator: %v", err)
	}
}

func TestAsyncAPIReferencesAndUnsupportedKeywords(t *testing.T) {
	rev := eventFixture(t)
	m := rev.Document.EventModel
	m.Schemas[0].SchemaJSON = `{"type":"object","properties":{"next":{"$ref":"#/components/schemas/orderPayload"}},"definitions":{"detail":{"type":"string"}}}`
	if _, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}); err != nil {
		t.Fatal(err)
	}
	m.Schemas[0].SchemaJSON = `{"$ref":"https://external.test/schema"}`
	_, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_ref_unsupported") {
		t.Fatalf("external ref: %v", err)
	}
	m.Schemas[0].SchemaJSON = `{"properties":{"field":{"type":"string"}},"unknownKeyword":true}`
	_, err = New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_schema_invalid") {
		t.Fatalf("unknown keyword: %v", err)
	}
}

func TestAsyncAPIDocumentationEscapesEventContent(t *testing.T) {
	rev := eventFixture(t)
	rev.Document.EventModel.Schemas[0].SchemaJSON = `{"description":"<script>alert(1)</script>"}`
	a, err := New(nil, 16<<20).Export(rev, Request{Format: HTML})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, "Kafka · Событийные контракты") || strings.Contains(a.Content, "<script>alert(1)</script>") || !strings.Contains(a.Content, "&lt;script&gt;") {
		t.Fatal("event documentation escaping failed")
	}
}

func TestAsyncAPIServerScopesHeadersAndNestedRefs(t *testing.T) {
	rev := eventFixture(t)
	m := rev.Document.EventModel
	m.Channels[0].ServerIDs = nil
	a, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	if err != nil || !hasCode(a.Diagnostics, "event_servers_unspecified") {
		t.Fatalf("logical contract: %v, %+v", err, a.Diagnostics)
	}
	if strings.Contains(a.Content, `"servers":`) {
		t.Fatal("unassigned servers emitted")
	}
	m.Channels[0].ServerIDs = []string{"localKafka"}
	m.Schemas = append(m.Schemas, designscenario.EventSchema{ID: "headers", Name: "Headers", SchemaJSON: `{"$ref":"#/components/schemas/orderPayload/definitions/headers"}`})
	m.Schemas[0].SchemaJSON = `{"type":"object","definitions":{"headers":{"type":"object","properties":{"trace":{"type":"string"}}}}}`
	m.Messages[0].HeadersSchemaID = "headers"
	m.Messages[0].Examples[0].HeadersJSON = `{"trace":"abc"}`
	if _, err := New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"}); err != nil {
		t.Fatalf("nested headers ref: %v", err)
	}
	m.Schemas[1].SchemaJSON = `{"$ref":"#/components/schemas/orderPayload/definitions/missing"}`
	_, err = New(nil, 16<<20).Export(rev, Request{Format: AsyncAPIJSON, ContractID: "ordersContract"})
	var blocked *BlockedError
	if !errors.As(err, &blocked) || !hasCode(blocked.Diagnostics, "event_ref_unsupported") {
		t.Fatalf("missing nested pointer: %v", err)
	}
}

func TestAsyncAPIDiagnosticsTruncateWithoutLosingError(t *testing.T) {
	d := &eventDiagnostics{}
	for i := 0; i < 120; i++ {
		d.add(Diagnostic{Code: "warning", Severity: "warning"})
	}
	d.add(Diagnostic{Code: "failure", Severity: "error"})
	if len(d.list) != 100 || d.list[99].Code != "event_diagnostics_truncated" || d.list[99].Severity != "error" || !d.hasErrors() {
		t.Fatalf("truncation: %+v", d.list[99])
	}
}
