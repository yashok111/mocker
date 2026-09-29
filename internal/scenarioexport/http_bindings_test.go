package scenarioexport

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func httpBindingFixture() designscenario.Revision {
	rev := revisionFixture()
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: jsonx.RawMessage(`{"openapi":"3.1.0","info":{"title":"Bindings","version":"1"},"servers":[{"url":"https://example.test"}],"paths":{"/source":{"get":{"x-mocker-canvas-operation-id":"source","responses":{"200":{"description":"ok","content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"},"token":{"type":"string"},"payload":{"type":"object"}}}}}}}}},"/items/{id}":{"post":{"x-mocker-canvas-operation-id":"target","parameters":[{"in":"path","name":"id","required":true,"schema":{"type":"integer"}},{"in":"query","name":"q","required":true,"schema":{"type":"number"}},{"in":"header","name":"X-Token","required":true,"schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"type":"object"}}}},"responses":{"200":{"description":"ok"}}}}}}`)}}
	rev.Document.Messages = []designscenario.Message{
		{ID: "producer", Kind: "request", FromID: "client", ToID: "api", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "source"}, Execution: &designscenario.StepExecution{Enabled: true}},
		{ID: "consumer", Kind: "request", FromID: "client", ToID: "api", Operation: &designscenario.OperationBinding{ContractID: "api", OperationKey: "target"}, Execution: &designscenario.StepExecution{
			Enabled: true, PathParams: designscenario.ExecutionValues{"id": "old"}, Query: designscenario.ExecutionValues{"q": "1"}, Headers: designscenario.ExecutionValues{"X-Token": "saved", "Content-Type": "application/json"}, Body: `{}`,
			Bindings: []designscenario.DataBinding{{ID: "id", SourceMessageID: "producer", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "path", Name: "id"}}},
		}},
	}
	return rev
}

func TestPostmanBindingPreflightAcceptsEarlierSourceWithoutMutatingRevision(t *testing.T) {
	rev := httpBindingFixture()
	before, _ := jsonx.Marshal(rev)
	svc := New(validContract, 1<<20)
	if _, err := svc.Export(rev, Request{Format: Postman}); err != nil {
		t.Fatal(err)
	}
	prepared, diagnostics, err := svc.prepareHTTP(rev, Postman)
	if err != nil {
		t.Fatal(err)
	}
	for _, diagnostic := range diagnostics {
		if diagnostic.Severity == "error" {
			t.Fatal(diagnostic)
		}
	}
	if len(prepared.Requests) != 2 || prepared.Requests[0].MessageID != "producer" || prepared.Requests[1].MessageID != "consumer" || prepared.Requests[1].BindingTypes["id"] != "integer" {
		t.Fatalf("missing request identity or binding target type: %+v", prepared.Requests)
	}
	after, _ := jsonx.Marshal(rev)
	if string(before) != string(after) {
		t.Fatal("binding export mutated revision")
	}
}

func TestPostmanBindingPreflightRejectsInvalidDependencyWithPrecisePointer(t *testing.T) {
	for _, tc := range []struct {
		name, message, pointer string
		change                 func(*designscenario.Revision)
	}{
		{"missing", "does not exist", "/messages/1/execution/bindings/0/sourceMessageId", func(r *designscenario.Revision) {
			r.Document.Messages[1].Execution.Bindings[0].SourceMessageID = "gone"
		}},
		{"later", "precede", "/messages/0/execution/bindings/0/sourceMessageId", func(r *designscenario.Revision) {
			r.Document.Messages[0], r.Document.Messages[1] = r.Document.Messages[1], r.Document.Messages[0]
		}},
		{"disabled", "enabled HTTP", "/messages/1/execution/bindings/0/sourceMessageId", func(r *designscenario.Revision) { r.Document.Messages[0].Execution.Enabled = false }},
		{"event", "enabled HTTP", "/messages/1/execution/bindings/0/sourceMessageId", func(r *designscenario.Revision) { r.Document.Messages[0].Kind = "event" }},
		{"type mismatch", "incompatible", "/messages/1/execution/bindings/0/target", func(r *designscenario.Revision) {
			r.Document.Messages[1].Execution.Bindings[0].SourcePointer = "/token"
		}},
		{"pointer syntax", "invalid JSON Pointer", "/messages/1/execution/bindings/0/sourcePointer", func(r *designscenario.Revision) { r.Document.Messages[1].Execution.Bindings[0].SourcePointer = "id" }},
		{"missing operation", "recipient must be an HTTP", "/messages/1/execution/bindings/0", func(r *designscenario.Revision) { r.Document.Messages[1].Operation = nil }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := httpBindingFixture()
			tc.change(&rev)
			_, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
			blocked, ok := errors.AsType[*BlockedError](err)
			if !ok {
				t.Fatalf("expected blocked export: %v", err)
			}
			for _, d := range blocked.Diagnostics {
				if d.Severity == "error" && d.Pointer == tc.pointer && strings.Contains(d.Message, tc.message) && d.Target != nil && d.Target.ID == "consumer" {
					return
				}
			}
			t.Fatalf("missing precise dependency diagnostic: %+v", blocked.Diagnostics)
		})
	}
}

func TestPostmanBindingPreflightSuppliesRequiredInputsAndIgnoresOverwrittenTemplates(t *testing.T) {
	rev := httpBindingFixture()
	config := rev.Document.Messages[1].Execution
	config.PathParams["id"] = "{{missing_path}}"
	config.Query["q"] = "{{missing_query}}"
	config.Headers = designscenario.ExecutionValues{"x-token": "{{missing_header}}", "X-TOKEN": "\r\ninvalid old value"}
	config.Body = ""
	config.Bindings = append(config.Bindings,
		designscenario.DataBinding{ID: "query", SourceMessageID: "producer", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "query", Name: "q"}},
		designscenario.DataBinding{ID: "header", SourceMessageID: "producer", SourcePointer: "/token", Target: designscenario.DataBindingTarget{Kind: "header", Name: "X-Token"}},
		designscenario.DataBinding{ID: "body", SourceMessageID: "producer", SourcePointer: "/payload", Target: designscenario.DataBindingTarget{Kind: "body"}},
	)
	before, _ := jsonx.Marshal(rev)
	prepared, diagnostics, err := New(validContract, 1<<20).prepareHTTP(rev, Postman)
	if err != nil {
		t.Fatal(err)
	}
	for _, d := range diagnostics {
		if d.Severity == "error" {
			t.Fatalf("binding inputs rejected: %+v", diagnostics)
		}
	}
	if len(prepared.Requests) != 2 || prepared.Requests[1].Execution.Body != "" {
		t.Fatal("validation placeholder leaked into emitted request", prepared.Requests)
	}
	for _, values := range []designscenario.ExecutionValues{prepared.Requests[1].Execution.PathParams, prepared.Requests[1].Execution.Query, prepared.Requests[1].Execution.Headers} {
		if len(values) != 0 {
			t.Fatal("overwritten text target remains in emitted input", values)
		}
	}
	after, _ := jsonx.Marshal(rev)
	if string(before) != string(after) {
		t.Fatal("preflight modified saved inputs")
	}
}

func TestPostmanBindingPreflightSkipsNonExecutedRecipients(t *testing.T) {
	for _, kind := range []string{"disabled", "event", "response"} {
		t.Run(kind, func(t *testing.T) {
			rev := httpBindingFixture()
			recipient := &rev.Document.Messages[1]
			recipient.Execution.Bindings[0].SourceMessageID = "gone"
			if kind == "disabled" {
				recipient.Execution.Enabled = false
			} else {
				recipient.Kind = kind
			}
			if _, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman}); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestPostmanBindingPreflightValidatesBoundHeaderNames(t *testing.T) {
	rev := httpBindingFixture()
	rev.Document.Messages[1].Execution.Bindings = append(rev.Document.Messages[1].Execution.Bindings, designscenario.DataBinding{ID: "header", SourceMessageID: "producer", SourcePointer: "/token", Target: designscenario.DataBindingTarget{Kind: "header", Name: "Bad Header"}})
	_, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	blocked, ok := errors.AsType[*BlockedError](err)
	if !ok || !hasDiagnostic(blocked.Diagnostics, "header_invalid") {
		t.Fatalf("invalid supplied header accepted: %v", err)
	}
}

func TestPostmanBindingPreflightKeepsBodyTemplateValidation(t *testing.T) {
	rev := httpBindingFixture()
	config := rev.Document.Messages[1].Execution
	config.Body = "{{missing}}"
	config.Bindings = append(config.Bindings, designscenario.DataBinding{ID: "body", SourceMessageID: "producer", SourcePointer: "/payload", Target: designscenario.DataBindingTarget{Kind: "body"}})
	_, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	blocked, ok := errors.AsType[*BlockedError](err)
	if !ok || !hasDiagnostic(blocked.Diagnostics, "variable_missing") {
		t.Fatalf("whole-body binding bypassed authored template validation: %v", err)
	}
}

func TestHTTPBindingPreflightIgnoresOversizedBindingsOnDisabledRecipient(t *testing.T) {
	rev := httpBindingFixture()
	config := rev.Document.Messages[1].Execution
	config.Enabled = false
	config.Bindings = make([]designscenario.DataBinding, designscenario.MaxExecutionEntries+1)
	for _, format := range []Format{Postman, CURL} {
		if _, err := New(validContract, 1<<20).Export(rev, Request{Format: format}); err != nil {
			t.Fatalf("%s: disabled bindings blocked active requests: %v", format, err)
		}
	}
}
