package designscenario

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

const aliasContract = `{
  "paths": {
    "/orders/{id}": {"$ref":"#/components/pathItems/Chain", "x-mocker-canvas-operation-ids":{"get":"orders"}, "parameters":[{"in":"query","name":"mode","schema":{"type":"string"}}]},
    "/archived/{id}": {"$ref":"#/components/pathItems/Shared", "x-mocker-canvas-operation-ids":{"get":"archived"}},
    "/local": {"$ref":"#/components/pathItems/Shared", "get":{"x-mocker-canvas-operation-id":"local", "responses":{"200":{"content":{"application/json":{"schema":{"type":"boolean"}}}}}}}
  },
  "components": {"pathItems": {
    "Chain":{"$ref":"#/components/pathItems/Shared", "parameters":[{"in":"header","name":"X-Trace","schema":{"type":"string"}}]},
    "Shared":{"parameters":[{"in":"path","name":"id","required":true,"schema":{"type":"integer"}},{"in":"query","name":"mode","schema":{"type":"integer"}}],"get":{"x-mocker-canvas-operation-id":"shared", "responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}}}}}}}
  }}
}`

func TestScenarioPathItemAliasKeysAndSchemas(t *testing.T) {
	t.Parallel()
	keys, diagnostics := operationKeys([]byte(aliasContract), "/contract")
	if len(diagnostics) != 0 || !reflect.DeepEqual(keys, map[string]struct{}{"orders": {}, "archived": {}, "local": {}}) {
		t.Fatalf("keys %#v, diagnostics %#v", keys, diagnostics)
	}
	revision := runRevision()
	revision.Document.Contracts[0].Document = jsonx.RawMessage(aliasContract)
	revision.Document.Messages = []Message{
		{ID: "orders", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "orders"}},
		{ID: "archived", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "archived"}},
		{ID: "local", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "local"}},
	}
	schemas := bindingSchemas(revision.Document)
	if got := schemas[0].responseType("/id"); got != "integer" {
		t.Fatalf("inherited response id type %q", got)
	}
	for _, test := range []struct {
		index  int
		target DataBindingTarget
		want   string
	}{
		{0, DataBindingTarget{Kind: "path", Name: "id"}, "integer"},
		{0, DataBindingTarget{Kind: "query", Name: "mode"}, "string"},
		{0, DataBindingTarget{Kind: "header", Name: "x-trace"}, "string"},
		{1, DataBindingTarget{Kind: "query", Name: "mode"}, "integer"},
	} {
		if got := schemas[test.index].targetType(test.target); got != test.want {
			t.Errorf("message %d target %+v: %s, want %s", test.index, test.target, got, test.want)
		}
	}
	if got := schemas[2].responseType(""); got != "boolean" {
		t.Fatalf("local sibling response: %s", got)
	}
	if string(revision.Document.Contracts[0].Document) != aliasContract {
		t.Fatal("reading rewrote the authored contract")
	}
}

func TestScenarioPathItemAliasDiagnostics(t *testing.T) {
	t.Parallel()
	for _, test := range []struct{ name, raw, pointer string }{
		{"duplicate", strings.Replace(aliasContract, `"get":"archived"`, `"get":"orders"`, 1), "/paths/~1orders~1{id}/x-mocker-canvas-operation-ids/get"},
		{"invalid", strings.Replace(aliasContract, `"get":"orders"`, `"get":42`, 1), "/paths/~1orders~1{id}/x-mocker-canvas-operation-ids/get"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, ds := operationKeys([]byte(test.raw), "")
			if len(ds) != 1 || ds[0].Severity != "error" || ds[0].Pointer != test.pointer {
				t.Fatalf("diagnostics %#v", ds)
			}
		})
	}
}

func TestScenarioRunAcceptsSeparatePathItemAliases(t *testing.T) {
	t.Parallel()
	revision := runRevision()
	revision.Document.Contracts[0].Document = jsonx.RawMessage(aliasContract)
	revision.Document.Messages[0].Operation.OperationKey = "orders"
	revision.Document.Messages[1].Operation.OperationKey = "archived"
	revision.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
	initial := prepareTestRun(t, revision)
	calls := 0
	report := Run(t.Context(), revision, initial, func(_ context.Context, request StepRequest) (StepResponse, error) {
		calls++
		if calls == 2 && request.PathParams["id"] != "9007199254740993" {
			t.Fatalf("binding value %#v", request.PathParams)
		}
		return runResponse(`{"id":9007199254740993}`), nil
	}, nil)
	if calls != 2 || report.Status != "passed" {
		t.Fatalf("calls %d, result %s: %s", calls, report.Status, report.Reason)
	}
}

func TestScenarioPathItemSiblingOverrideKeepsKnownSchema(t *testing.T) {
	t.Parallel()
	revision := runRevision()
	revision.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/local":{"$ref":"#/components/pathItems/Shared","get":{"x-mocker-canvas-operation-id":"local","responses":{"200":{"content":{"application/json":{"schema":{"type":"integer"}}}}}}}},"components":{"pathItems":{"Shared":{"get":{"responses":{"200":{"content":{"application/json":{"schema":{"type":"boolean"}}}}}}}}}}`)
	revision.Document.Messages = []Message{{ID: "local", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "local"}}}
	analysis := AnalyzeDataFlow(revision.Document)
	if len(analysis.Diagnostics) != 0 || len(analysis.Messages) != 1 || analysis.Messages[0].ResponseFields[0].Type != "integer" {
		t.Fatalf("known local schema reported as unknown: %+v", analysis)
	}
}

func TestScenarioPathItemBrokenReferenceWarnsWithoutBlockingValidSibling(t *testing.T) {
	t.Parallel()
	raw := `{"paths":{"/local":{"$ref":"#/missing","get":{"x-mocker-canvas-operation-id":"local"}}}}`
	keys, ds := operationKeys([]byte(raw), "/contract")
	if len(keys) != 1 || len(ds) != 1 || ds[0].Severity != "warning" || ds[0].Pointer != "/contract/paths/~1local/$ref" {
		t.Fatalf("missing reference explanation: keys %#v, diagnostics %+v", keys, ds)
	}
	revision := runRevision()
	revision.Document.Contracts[0].Document = jsonx.RawMessage(raw)
	revision.Document.Messages = revision.Document.Messages[:1]
	revision.Document.Messages[0].Operation.OperationKey = "local"
	if _, err := PrepareRun(revision, "local", "", "ui", nil); err != nil {
		t.Fatalf("valid sibling blocked by reference warning: %v", err)
	}
}
