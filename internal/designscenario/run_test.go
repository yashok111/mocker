package designscenario

import (
	"context"
	"errors"
	"fmt"
	"math"
	"reflect"
	"strings"
	"testing"
	"testing/synctest"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
)

func runRevision() Revision {
	config := func() *StepExecution {
		return &StepExecution{Enabled: true, PathParams: ExecutionValues{}, Query: ExecutionValues{}, Headers: ExecutionValues{}, Assertions: []ExecutionAssertion{}, Extract: []ExecutionExtraction{}}
	}
	return Revision{
		RevisionSummary: RevisionSummary{ID: 23, ScenarioID: 11, Version: 4},
		Document: Document{FormatVersion: 1, Title: "Login", Participants: []Participant{}, Fragments: []Fragment{},
			Execution: &Execution{Variables: ExecutionValues{"user": "original"}},
			Contracts: []Contract{{ID: "api", Mode: "linked", Source: &ContractSource{DesignID: 8, RevisionID: 9, Version: 1}, Document: jsonx.RawMessage(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login"}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile"}}}}`)}},
			Messages: []Message{
				{ID: "login", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "login"}, Execution: config()},
				{ID: "profile", Kind: "request", Operation: &OperationBinding{ContractID: "api", OperationKey: "profile"}, Execution: config()},
			},
		},
	}
}

func prepareTestRun(t *testing.T, revision Revision) RunReport {
	t.Helper()
	report, err := PrepareRun(revision, "run-1", "test", "mcp", nil)
	if err := err; err != nil {
		t.Fatal(err)
	}
	return report
}

func runResponse(body string) StepResponse {
	return StepResponse{ScenarioRevisionID: 23, DesignID: 8, DesignRevisionID: 9, WorkspaceRevision: 2, Method: "POST", Path: "/login", Status: 200, Headers: map[string]string{}, Body: body, DurationMS: 1}
}

func TestRunSequenceUsesOverridesAndAtomicExtractions(t *testing.T) {
	revision := runRevision()
	revision.Document.Messages[0].Execution.Body = `{"user":"{{user}}"}`
	revision.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "token", Pointer: "/token"}, {Name: "id", Pointer: "/id"}}
	revision.Document.Messages[1].Execution.Headers["Authorization"] = "Bearer {{token}}"
	revision.Document.Messages[1].Execution.PathParams["id"] = "{{id}}"
	revision.Document.Messages[1].Execution.Query["user"] = "{{user}}"
	revision.Document.Messages[1].Execution.Assertions = []ExecutionAssertion{{Pointer: "/name", Equals: jsonx.RawMessage(`"Ada"`)}}
	overrides := ExecutionValues{"user": "Ada"}
	initial, err := PrepareRun(revision, "seq", "variant", "mcp", overrides)
	if err := err; err != nil {
		t.Fatal(err)
	}
	overrides["user"] = "mutated"
	revision.Document.Messages[0].Execution.Body = "mutated"
	var requests []StepRequest
	var updates []RunReport
	report := Run(t.Context(), revision, initial, func(ctx context.Context, request StepRequest) (StepResponse, error) {
		requests = append(requests, request)
		deadline, ok := ctx.Deadline()
		if !(ok) {
			t.Fatal("condition is false")
		}
		if math.Abs(float64(30)-float64(time.Until(deadline).Seconds())) > 1 {
			t.Fatal("values exceed tolerance")
		}
		if request.MessageID == "login" {
			return runResponse(`{"token":"secret","id":9007199254740993}`), nil
		}
		return runResponse(`{"name":"Ada"}`), nil
	}, func(snapshot RunReport) error {
		updates = append(updates, snapshot)
		return nil
	})
	if got, want := report.Status, "passed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if report.FinishedAt == nil {
		t.Fatal("unexpected nil: report.FinishedAt")
	}
	if got := len(requests); got != 2 {
		t.Fatalf("length = %d; want %d", got, 2)
	}
	if got, want := requests[0].Body, `{"user":"Ada"}`; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := requests[1].Headers["Authorization"], "Bearer secret"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := requests[1].PathParams["id"], "9007199254740993"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := requests[1].Query["user"], "Ada"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := report.Document.Execution.Variables["user"], "original"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := report.InputVariables["user"], "Ada"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := initial.Steps[0].Status, "pending"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if _, ok := initial.Variables["token"]; ok {
		t.Fatal("unexpected map entry")
	}
	if got, want := report.Steps[1].Status, "passed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := *report.Steps[1].Assertions[0].ActualJSON, `"Ada"`; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	updates[0].Document.Title = "tampered"
	updates[0].Variables["user"] = "tampered"
	if got, want := report.Document.Title, "Login"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := report.Variables["user"], "Ada"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
}

func TestPrepareRunRejectsInvalidExecutionBeforeDispatch(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Revision)
	}{
		{"fragment", func(r *Revision) { r.Document.Fragments = []Fragment{{Kind: "loop"}} }},
		{"unfinished forms", func(r *Revision) { r.FormDrafts = map[string]string{"all": `{"dirty":"value"}`} }},
		{"no enabled HTTP", func(r *Revision) { r.Document.Messages = []Message{{ID: "note", Kind: "note"}} }},
		{"missing contract", func(r *Revision) { r.Document.Contracts = nil }},
		{"missing operation", func(r *Revision) { r.Document.Messages[0].Operation.OperationKey = "gone" }},
		{"missing source", func(r *Revision) { r.Document.Contracts[0].Source = nil }},
		{"copied API", func(r *Revision) { r.Document.Contracts[0].Mode = "copy" }},
		{"invalid pointer", func(r *Revision) {
			r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "token", Pointer: "/~2"}}
		}},
		{"missing config array", func(r *Revision) { r.Document.Messages[0].Execution.Assertions = nil }},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			test.change(&r)
			_, err := PrepareRun(r, "run", "", "ui", nil)
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("error = %v; want %v", err, ErrInvalid)
			}
		})
	}
}

func TestRunSkipsDisabledAndDescriptiveMessagesWithStaleBindings(t *testing.T) {
	r := runRevision()
	r.FormDrafts = map[string]string{"all": " { } "}
	r.Document.Messages[1].Execution.Enabled = false
	r.Document.Messages[1].Operation = &OperationBinding{ContractID: "deleted", OperationKey: "gone"}
	r.Document.Messages = append(r.Document.Messages, Message{ID: "note", Kind: "note", Operation: &OperationBinding{ContractID: "deleted", OperationKey: "gone"}}, Message{ID: "unbound", Kind: "request"})
	initial := prepareTestRun(t, r)
	calls := 0
	report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		calls++
		return runResponse(""), nil
	}, nil)
	if got, want := report.Status, "passed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if got, want := calls, 1; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	for _, step := range report.Steps[1:] {
		if got, want := step.Status, "skipped"; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
	}
}

func TestRunJSONAssertionsPreservePrecisionAndMissingNull(t *testing.T) {
	for _, test := range []struct {
		name, body, pointer, expected string
		passed, missing               bool
	}{
		{"equivalent numbers", `{"a":1e0}`, "/a", `1.00`, true, false},
		{"different large integers", `9007199254740993`, "", `9007199254740992`, false, false},
		{"equal huge exponents", `1e1000000000`, "", `10e999999999`, true, false},
		{"negative zero", `-0e9999999999`, "", `0`, true, false},
		{"object order", `{"a":1,"b":[false,null]}`, "", `{"b":[false,null],"a":1.0}`, true, false},
		{"escaped tokens", `{"a/b":{"~":[null]}}`, "/a~1b/~0/0", `null`, true, false},
		{"missing is not null", `{}`, "/missing", `null`, false, true},
		{"array leading zero", `[null]`, "/00", `null`, false, true},
		{"array negative", `[null]`, "/-1", `null`, false, true},
		{"array past end", `[null]`, "/1", `null`, false, true},
		{"array append token", `[null]`, "/-", `null`, false, true},
		{"prototype is own key", `{"__proto__":1}`, "/__proto__", `1`, true, false},
		{"array order", `[1,2]`, "", `[2,1]`, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			r.Document.Messages[0].Execution.Assertions = []ExecutionAssertion{{Pointer: test.pointer, Equals: jsonx.RawMessage(test.expected)}}
			initial := prepareTestRun(t, r)
			calls := 0
			report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(test.body), nil }, nil)
			if got := len(report.Steps[0].Assertions); got != 1 {
				t.Fatalf("length = %d; want %d", got, 1)
			}
			assertion := report.Steps[0].Assertions[0]
			if got, want := assertion.Passed, test.passed; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if got, want := assertion.ActualJSON == nil, test.missing; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if got, want := assertion.ExpectedJSON, test.expected; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if !test.passed {
				if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
					t.Fatalf("got %#v; want %#v", got, want)
				}
				if got, want := report.Steps[1].Status, "skipped"; !reflect.DeepEqual(got, want) {
					t.Fatalf("got %#v; want %#v", got, want)
				}
				if got, want := calls, 1; !reflect.DeepEqual(got, want) {
					t.Fatalf("got %#v; want %#v", got, want)
				}
			}
		})
	}
}

func TestRunFailedExtractionDoesNotCommitAnyVariable(t *testing.T) {
	for _, pointer := range []string{"/missing", "/tooLong"} {
		t.Run(pointer, func(t *testing.T) {
			r := runRevision()
			r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "token", Pointer: "/token"}, {Name: "last", Pointer: pointer}}
			initial := prepareTestRun(t, r)
			report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) {
				return runResponse(`{"token":"secret","tooLong":"` + strings.Repeat("a", 50001) + `"}`), nil
			}, nil)
			if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if got, want := report.Variables, (ExecutionValues{"user": "original"}); !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
		})
	}
}

func TestRunStopsOnFailureAndDoesNotTrustMismatchedResponse(t *testing.T) {
	for _, test := range []struct {
		name      string
		configure func(*Revision)
		response  StepResponse
		err       error
	}{
		{name: "executor error", err: errors.New("network failed")},
		{name: "default rejects 300", response: func() StepResponse { r := runResponse(""); r.Status = 300; return r }()},
		{name: "scenario revision", response: func() StepResponse { r := runResponse(""); r.ScenarioRevisionID++; return r }()},
		{name: "API revision", response: func() StepResponse { r := runResponse(""); r.DesignRevisionID++; return r }()},
		{name: "API source", response: func() StepResponse { r := runResponse(""); r.DesignID++; return r }()},
		{name: "unknown variable", configure: func(r *Revision) { r.Document.Messages[0].Execution.Body = "{{missing}}" }},
		{name: "invalid JSON", configure: func(r *Revision) {
			r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "token", Pointer: ""}}
		}, response: runResponse(`{} {}`)},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			if test.configure != nil {
				test.configure(&r)
			}
			initial := prepareTestRun(t, r)
			calls := 0
			report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return test.response, test.err }, nil)
			if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if report.Reason == "" {
				t.Fatal("expected nonempty value")
			}
			if got, want := report.Steps[0].Status, "failed"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if got, want := report.Steps[1].Status, "skipped"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if calls > 1 {
				t.Fatalf("got %v; want <= %v", calls, 1)
			}
		})
	}
}

func TestRunCancellationAndStepTimeout(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		r := runRevision()
		initial := prepareTestRun(t, r)
		ctx, cancel := context.WithCancel(t.Context())
		var report RunReport
		go func() {
			report = Run(ctx, r, initial, func(ctx context.Context, request StepRequest) (StepResponse, error) {
				<-ctx.Done()
				return StepResponse{}, ctx.Err()
			}, nil)
		}()
		synctest.Wait()
		cancel()
		synctest.Wait()
		if got, want := report.Status, "cancelled"; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
		if got, want := report.Steps[0].Status, "cancelled"; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
		if got, want := report.Steps[1].Status, "skipped"; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
	})
	synctest.Test(t, func(t *testing.T) {
		r := runRevision()
		initial := prepareTestRun(t, r)
		started := time.Now()
		report := Run(t.Context(), r, initial, func(ctx context.Context, request StepRequest) (StepResponse, error) {
			<-ctx.Done()
			return StepResponse{}, ctx.Err()
		}, nil)
		if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
		if got, want := time.Since(started), 30*time.Second; !reflect.DeepEqual(got, want) {
			t.Fatalf("got %#v; want %#v", got, want)
		}
	})
}

func TestRunProgressFailurePreventsFurtherDispatch(t *testing.T) {
	for _, failWhen := range []string{"running", "response", "passed"} {
		t.Run(failWhen, func(t *testing.T) {
			r := runRevision()
			initial := prepareTestRun(t, r)
			calls := 0
			report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(""), nil }, func(snapshot RunReport) error {
				step := snapshot.Steps[0]
				if (failWhen == "response" && step.Response != nil) || step.Status == failWhen {
					return errors.New("disk full")
				}
				return nil
			})
			if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if !strings.Contains(report.Reason, "disk full") {
				t.Fatalf("got %q; missing %q", report.Reason, "disk full")
			}
			if got, want := report.Steps[1].Status, "skipped"; !reflect.DeepEqual(got, want) {
				t.Fatalf("got %#v; want %#v", got, want)
			}
			if failWhen == "running" {
				if calls != 0 {
					t.Fatalf("got %v; want zero", calls)
				}
			} else {
				if got, want := calls, 1; !reflect.DeepEqual(got, want) {
					t.Fatalf("got %#v; want %#v", got, want)
				}
			}
		})
	}
}

func TestPrepareAndRunVariableAndBodyBounds(t *testing.T) {
	r := runRevision()
	_, err := PrepareRun(r, "run", "", "ui", ExecutionValues{"user": strings.Repeat("a", 50001)})
	if err == nil {
		t.Fatal("expected error")
	}
	tooMany := ExecutionValues{}
	for i := range 100 {
		tooMany[fmt.Sprintf("v%d", i)] = "x"
	}
	_, err = PrepareRun(r, "run", "", "ui", tooMany)
	if err == nil {
		t.Fatal("expected error")
	}
	r.Document.Messages[0].Execution.Body = strings.Repeat("{{user}}", 30)
	initial, err := PrepareRun(r, "run", "", "ui", ExecutionValues{"user": strings.Repeat("a", 50000)})
	if err := err; err != nil {
		t.Fatal(err)
	}
	calls := 0
	report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(""), nil }, nil)
	if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if calls != 0 {
		t.Fatalf("got %v; want zero", calls)
	}
	initial = prepareTestRun(t, runRevision())
	report = Run(t.Context(), runRevision(), initial, func(context.Context, StepRequest) (StepResponse, error) {
		return runResponse(strings.Repeat("a", (1<<20)+1)), nil
	}, nil)
	if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if report.Steps[0].Response != nil {
		t.Fatal("expected nil")
	}
}

func TestRunAggregateLimitCountsRequestsResponsesAndAssertions(t *testing.T) {
	r := runRevision()
	first := r.Document.Messages[0]
	first.Execution.Body = strings.Repeat("r", 1<<20)
	first.Execution.Assertions = []ExecutionAssertion{{Pointer: "", Equals: jsonx.RawMessage(`"` + strings.Repeat("a", (1<<20)-2) + `"`)}}
	r.Document.Messages = nil
	for i := range 8 {
		step := first
		step.ID = fmt.Sprintf("step%d", i)
		r.Document.Messages = append(r.Document.Messages, step)
	}
	initial := prepareTestRun(t, r)
	calls := 0
	report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		calls++
		return runResponse(`"` + strings.Repeat("a", (1<<20)-2) + `"`), nil
	}, nil)
	if got, want := report.Status, "failed"; !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v; want %#v", got, want)
	}
	if calls >= 8 {
		t.Fatalf("got %v; want < %v", calls, 8)
	}
	retained := 0
	for _, step := range report.Steps {
		for _, value := range []any{step.Request, step.Response, step.Assertions} {
			raw, err := jsonx.Marshal(value)
			if err := err; err != nil {
				t.Fatal(err)
			}
			retained += len(raw)
		}
	}
	if retained > 20<<20 {
		t.Fatalf("got %v; want <= %v", retained, 20<<20)
	}
}

func TestPrepareRunExplicitEnabledRequestRequiresOperation(t *testing.T) {
	r := runRevision()
	r.Document.Messages[0].Operation = nil
	if _, err := PrepareRun(r, "run", "", "ui", nil); !errors.Is(err, ErrInvalid) {
		t.Fatalf("explicit enabled request without operation: %v", err)
	}
	r.Document.Messages[0].Execution = nil
	initial := prepareTestRun(t, r)
	if initial.Steps[0].Status != "skipped" {
		t.Fatalf("descriptive message status = %s", initial.Steps[0].Status)
	}
}

func TestPrepareRunBoundsSerializedVariableSnapshots(t *testing.T) {
	variables := ExecutionValues{}
	for i := range 100 {
		variables[fmt.Sprintf("v%d", i)] = strings.Repeat("\x00", 50000)
	}
	r := runRevision()
	r.Document.Execution.Variables = ExecutionValues{}
	if _, err := PrepareRun(r, "run", "", "mcp", variables); err == nil {
		t.Fatal("accepted variable snapshots whose escaped JSON exceeds 20 MiB")
	}
}

func TestRunCopiesExecutorAndProgressData(t *testing.T) {
	r := runRevision()
	r.Document.Messages[0].Execution.Headers["X-Test"] = "original"
	r.Document.Messages[1].Execution.Assertions = []ExecutionAssertion{{Pointer: "", Equals: jsonx.RawMessage(`1`)}}
	initial := prepareTestRun(t, r)
	var responseHeaders map[string]string
	report := Run(t.Context(), r, initial, func(ctx context.Context, request StepRequest) (StepResponse, error) {
		request.Headers["X-Test"] = "changed by executor"
		if responseHeaders != nil {
			responseHeaders["X-Test"] = "changed later"
		}
		response := runResponse(`1`)
		response.Headers["X-Test"] = "original"
		responseHeaders = response.Headers
		return response, nil
	}, func(snapshot RunReport) error {
		snapshot.Document.Messages[1].Execution.Assertions[0].Equals[0] = '2'
		snapshot.Variables["user"] = "changed in callback"
		if snapshot.Steps[0].Request != nil {
			snapshot.Steps[0].Request.Headers["X-Test"] = "changed in callback"
		}
		return nil
	})
	if report.Status != "passed" || report.Variables["user"] != "original" || report.Steps[0].Request.Headers["X-Test"] != "original" || report.Steps[0].Response.Headers["X-Test"] != "original" {
		t.Fatalf("aliased runner state: %+v", report)
	}
}

func TestRunExpectedStatusAndAllExtractionValueTypes(t *testing.T) {
	r := runRevision()
	r.Document.Messages[0].Execution.ExpectedStatus = new(404)
	r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{
		{Name: "text", Pointer: "/text"}, {Name: "nothing", Pointer: "/nothing"},
		{Name: "flag", Pointer: "/flag"}, {Name: "nested", Pointer: "/nested"},
	}
	initial := prepareTestRun(t, r)
	report := Run(t.Context(), r, initial, func(ctx context.Context, request StepRequest) (StepResponse, error) {
		response := runResponse(`{"text":"plain","nothing":null,"flag":false,"nested":{"b":1,"a":[9007199254740993]}}`)
		if request.MessageID == "login" {
			response.Status = 404
		} else {
			response.Status = 204
		}
		return response, nil
	}, nil)
	want := ExecutionValues{"user": "original", "text": "plain", "nothing": "null", "flag": "false", "nested": `{"a":[9007199254740993],"b":1}`}
	if report.Status != "passed" || !reflect.DeepEqual(report.Variables, want) {
		t.Fatalf("status=%s variables=%v", report.Status, report.Variables)
	}
}

func TestRunCancelledBeforeDispatchAndRecovery(t *testing.T) {
	r := runRevision()
	initial := prepareTestRun(t, r)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	report := Run(ctx, r, initial, func(context.Context, StepRequest) (StepResponse, error) {
		t.Fatal("dispatched cancelled run")
		return StepResponse{}, nil
	}, nil)
	if report.Status != "cancelled" || report.Steps[0].Status != "skipped" {
		t.Fatalf("unexpected cancelled report: %+v", report)
	}
	initial.Steps[0].Status = "running"
	recovered := CancelRunReport(initial, "process interrupted")
	if recovered.Status != "cancelled" || recovered.Steps[0].Status != "cancelled" || recovered.Steps[1].Status != "skipped" || recovered.FinishedAt == nil || recovered.Reason != "process interrupted" {
		t.Fatalf("unexpected recovered report: %+v", recovered)
	}
	if initial.Steps[0].Status != "running" {
		t.Fatal("recovery mutated input report")
	}
}

func TestRunVariableCountAndResolvedParameterLimits(t *testing.T) {
	for _, test := range []struct {
		name   string
		change func(*Revision)
	}{
		{"runtime variables", func(r *Revision) {
			r.Document.Execution.Variables = ExecutionValues{}
			for i := range 100 {
				r.Document.Execution.Variables[fmt.Sprintf("v%d", i)] = "x"
			}
			r.Document.Messages[0].Execution.Extract = []ExecutionExtraction{{Name: "new", Pointer: ""}}
		}},
		{"expanded query", func(r *Revision) {
			r.Document.Execution.Variables["user"] = strings.Repeat("a", 50000)
			r.Document.Messages[0].Execution.Query["q"] = "{{user}}a"
		}},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			test.change(&r)
			initial := prepareTestRun(t, r)
			calls := 0
			report := Run(t.Context(), r, initial, func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(`1`), nil }, nil)
			if report.Status != "failed" || !reflect.DeepEqual(report.Variables, initial.Variables) {
				t.Fatalf("status=%s vars=%v", report.Status, report.Variables)
			}
			if test.name == "expanded query" && calls != 0 {
				t.Fatal("dispatched overlong resolved parameter")
			}
		})
	}
}
