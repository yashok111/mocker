package designscenario

import (
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDataFlowRepeatedOperationPreservesResponseSelection(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders","responses":{"200":{"content":{"application/json":{"schema":{"type":"integer"}}}},"201":{"content":{"application/json":{"schema":{"type":"string"}}}}}}}}}`)
	other := r.Document.Contracts[0]
	other.ID = "other"
	other.Document = jsonx.RawMessage(`{"paths":{"/orders":{"get":{"x-mocker-canvas-operation-id":"orders","responses":{"200":{"content":{"application/json":{"schema":{"type":"boolean"}}}}}}}}}`)
	r.Document.Contracts = append(r.Document.Contracts, other)
	for _, contract := range r.Document.Contracts {
		if !jsonx.Valid(contract.Document) {
			t.Fatal("invalid fixture JSON")
		}
	}
	cases := []struct {
		name, contract string
		status         *int
		want           string
	}{
		{"all first", "api", nil, "unknown"},
		{"200 first", "api", new(200), "integer"},
		{"201 first", "api", new(201), "string"},
		{"200 repeated", "api", new(200), "integer"},
		{"all repeated", "api", nil, "unknown"},
		{"other contract", "other", new(200), "boolean"},
		{"201 repeated", "api", new(201), "string"},
	}
	r.Document.Messages = nil
	for _, tt := range cases {
		r.Document.Messages = append(r.Document.Messages, Message{ID: tt.name, Kind: "request", Operation: &OperationBinding{ContractID: tt.contract, OperationKey: "orders"}, Execution: &StepExecution{ExpectedStatus: tt.status}})
	}
	analysis := AnalyzeDataFlow(r.Document)
	if len(analysis.Messages) != len(cases) {
		t.Fatalf("messages: %d", len(analysis.Messages))
	}
	for i, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			fields := analysis.Messages[i].ResponseFields
			if len(fields) != 1 || fields[0].Type != tt.want {
				t.Fatalf("fields %#v, want type %s", fields, tt.want)
			}
		})
	}
	// A separate analysis must observe a changed snapshot, rather than a global cache.
	r.Document.Contracts[0].Document = other.Document
	analysis = AnalyzeDataFlow(r.Document)
	if got := analysis.Messages[1].ResponseFields[0].Type; got != "boolean" {
		t.Fatalf("stale schema cache: %s", got)
	}
}

func TestDataFlowOperationIndexKeepsDeterministicFirstMatch(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"components":{"pathItems":{"First":{"parameters":[{"in":"query","name":"inherited","schema":{"type":"string"}}],"get":{"x-mocker-canvas-operation-id":"duplicate","responses":{"200":{"content":{"application/json":{"schema":{"type":"integer"}}}}}},"post":{"x-mocker-canvas-operation-id":"duplicate","responses":{"200":{"content":{"application/json":{"schema":{"type":"boolean"}}}}}}}}},"paths":{"/z":{"get":{"x-mocker-canvas-operation-id":"duplicate","responses":{"200":{"content":{"application/json":{"schema":{"type":"string"}}}}}}},"/a":{"$ref":"#/components/pathItems/First"}}}`)
	r.Document.Messages = []Message{{ID: "first", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "duplicate"}}, {ID: "second", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "duplicate"}}}
	analysis := AnalyzeDataFlow(r.Document)
	for _, m := range analysis.Messages {
		if len(m.ResponseFields) != 1 || m.ResponseFields[0].Type != "integer" || len(m.RequestFields) != 1 || m.RequestFields[0].Name != "inherited" {
			t.Fatalf("wrong indexed operation: %#v", m)
		}
	}
}
