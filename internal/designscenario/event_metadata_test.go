package designscenario

import (
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestEventMetadataValidRoutesAndLinks(t *testing.T) {
	doc := eventFixture(t)
	retry := doc.EventModel.Channels[0]
	retry.ID, retry.Name, retry.Address = "retry", "Retry", "orders.retry"
	dlq := retry
	dlq.ID, dlq.Name, dlq.Address = "dlq", "DLQ", "orders.dlq"
	doc.EventModel.Channels = append(doc.EventModel.Channels, retry, dlq)
	op := &doc.EventModel.Contracts[1].Operations[0]
	op.FailureRoutes = &EventFailureRoutes{RetryChannelID: "retry", DeadLetterChannelID: "dlq"}
	op.APILinks = []EventAPILink{{ContractID: "missing-http", OperationKey: "opaque-key"}}
	op.StateLinks = []EventStateLink{{ContractID: "missing-http", DiagramID: "order", TransitionID: "created"}}
	if diagnostics := validateForTest(t, doc); slices.ContainsFunc(diagnostics, func(d Diagnostic) bool { return d.Severity == "error" }) {
		t.Fatalf("metadata rejected: %+v", diagnostics)
	}
	raw, err := jsonx.Marshal(doc)
	if err != nil {
		t.Fatal(err)
	}
	var decoded Document
	if err := jsonx.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if got := decoded.EventModel.Contracts[1].Operations[0]; got.FailureRoutes.RetryChannelID != "retry" || len(got.APILinks) != 1 || len(got.StateLinks) != 1 {
		t.Fatalf("round trip=%+v", got)
	}
}

func TestEventMetadataStructuralErrors(t *testing.T) {
	base := eventFixture(t)
	for _, tc := range []struct {
		name, pointer string
		change        func(*Document)
	}{
		{"send route", "/eventModel/contracts/0/operations/0/failureRoutes", func(d *Document) {
			d.EventModel.Contracts[0].Operations[0].FailureRoutes = &EventFailureRoutes{RetryChannelID: "ordersEvents"}
		}},
		{"unknown target", "/eventModel/contracts/1/operations/0/failureRoutes/retryChannelId", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].FailureRoutes = &EventFailureRoutes{RetryChannelID: "gone"}
		}},
		{"self target", "/eventModel/contracts/1/operations/0/failureRoutes/retryChannelId", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].FailureRoutes = &EventFailureRoutes{RetryChannelID: "ordersEvents"}
		}},
		{"duplicate destinations", "/eventModel/contracts/1/operations/0/failureRoutes/deadLetterChannelId", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].FailureRoutes = &EventFailureRoutes{RetryChannelID: "ordersEvents", DeadLetterChannelID: "ordersEvents"}
		}},
		{"duplicate API link", "/eventModel/contracts/1/operations/0/apiLinks/1", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].APILinks = []EventAPILink{{ContractID: "http", OperationKey: "get"}, {ContractID: "http", OperationKey: "get"}}
		}},
		{"duplicate state link", "/eventModel/contracts/1/operations/0/stateLinks/1", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].StateLinks = []EventStateLink{{ContractID: "http", DiagramID: "order", TransitionID: "new"}, {ContractID: "http", DiagramID: "order", TransitionID: "new"}}
		}},
		{"empty API identity", "/eventModel/contracts/1/operations/0/apiLinks/0/operationKey", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].APILinks = []EventAPILink{{ContractID: "http"}}
		}},
		{"API link limit", "/eventModel/contracts/1/operations/0/apiLinks", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].APILinks = make([]EventAPILink, 101)
		}},
		{"state link limit", "/eventModel/contracts/1/operations/0/stateLinks", func(d *Document) {
			d.EventModel.Contracts[1].Operations[0].StateLinks = make([]EventStateLink, 101)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			doc, err := cloneDocument(base)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&doc)
			if !hasEventError(t, doc, tc.pointer) {
				t.Fatalf("missing %s: %+v", tc.pointer, validateForTest(t, doc))
			}
		})
	}
}

func TestEventMetadataRejectsPresentNullAndUnknownFields(t *testing.T) {
	for _, raw := range []string{
		`{"id":"op","failureRoutes":null}`,
		`{"id":"op","apiLinks":null}`,
		`{"id":"op","stateLinks":null}`,
		`{"id":"op","failureRoutes":{"retryChannelId":null}}`,
		`{"id":"op","apiLinks":[{"contractId":null,"operationKey":"key"}]}`,
		`{"id":"op","stateLinks":[{"contractId":"c","diagramId":"d","transitionId":null}]}`,
		`{"id":"op","failureRoutes":{"unknown":true}}`,
	} {
		var operation EventOperation
		if err := jsonx.Unmarshal([]byte(raw), &operation); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
	var operation EventOperation
	if err := jsonx.Unmarshal([]byte(`{"id":"op","failureRoutes":{},"apiLinks":[],"stateLinks":[]}`), &operation); err != nil || operation.FailureRoutes == nil || operation.APILinks == nil || operation.StateLinks == nil {
		t.Fatalf("empty metadata: %+v %v", operation, err)
	}
	if strings.Contains(string(mustJSON(t, eventFixture(t))), "failureRoutes") {
		t.Fatal("legacy document acquired metadata")
	}
}

func mustJSON(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestEventMetadataRunCloneOwnsNestedFields(t *testing.T) {
	doc := eventFixture(t)
	op := &doc.EventModel.Contracts[1].Operations[0]
	op.FailureRoutes = &EventFailureRoutes{RetryChannelID: "retry"}
	op.APILinks = []EventAPILink{{ContractID: "http", OperationKey: "orders"}}
	op.StateLinks = []EventStateLink{{ContractID: "http", DiagramID: "order", TransitionID: "created"}}
	cloned := cloneRunDocument(doc)
	copyOperation := &cloned.EventModel.Contracts[1].Operations[0]
	copyOperation.FailureRoutes.RetryChannelID = "other"
	copyOperation.APILinks[0].OperationKey = "other"
	copyOperation.StateLinks[0].TransitionID = "other"
	if op.FailureRoutes.RetryChannelID != "retry" || op.APILinks[0].OperationKey != "orders" || op.StateLinks[0].TransitionID != "created" {
		t.Fatalf("original changed: %+v", *op)
	}
}
