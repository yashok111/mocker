package designscenario

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDataBindingTypedRuntime(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, source, pointer, body, want, reason string
		target                                    DataBindingTarget
		prefix                                    string
	}{
		{name: "quotes and large integer", source: `{"value":{"name":"a\"b","id":9007199254740993}}`, pointer: "/value", body: `{"payload":{}}`, target: DataBindingTarget{Kind: "body", Pointer: "/payload"}, want: `{"payload":{"id":9007199254740993,"name":"a\"b"}}`},
		{name: "escaped object key", source: `{"a/b":{"~":42}}`, pointer: "/a~1b/~0", body: `{"a/b":{}}`, target: DataBindingTarget{Kind: "body", Pointer: "/a~1b/~0"}, want: `{"a/b":{"~":42}}`},
		{name: "existing array item", source: `{"x":[42]}`, pointer: "/x/0", body: `[0]`, target: DataBindingTarget{Kind: "body", Pointer: "/0"}, want: `[42]`},
		{name: "whole body", source: `[1,true,null]`, body: "", target: DataBindingTarget{Kind: "body"}, want: `[1,true,null]`},
		{name: "body null", source: `null`, body: `{}`, target: DataBindingTarget{Kind: "body", Pointer: "/value"}, want: `{"value":null}`},
		{name: "header prefix", source: `{"token":"abc"}`, pointer: "/token", target: DataBindingTarget{Kind: "header", Name: "Authorization"}, prefix: "Bearer ", want: "Bearer abc"},
		{name: "missing source", source: `{}`, pointer: "/missing", target: DataBindingTarget{Kind: "query", Name: "q"}, reason: "missing"},
		{name: "null text", source: `null`, target: DataBindingTarget{Kind: "path", Name: "id"}, reason: "non-null scalar"},
		{name: "object text", source: `{}`, target: DataBindingTarget{Kind: "query", Name: "q"}, reason: "non-null scalar"},
		{name: "array text", source: `[]`, target: DataBindingTarget{Kind: "header", Name: "X-ID"}, reason: "non-null scalar"},
		{name: "missing intermediate", source: `42`, body: `{}`, target: DataBindingTarget{Kind: "body", Pointer: "/missing/id"}, reason: "container is missing"},
		{name: "missing array item", source: `42`, body: `[]`, target: DataBindingTarget{Kind: "body", Pointer: "/0"}, reason: "array index is missing"},
		{name: "trailing response JSON", source: `42 trailing`, target: DataBindingTarget{Kind: "body"}, reason: "not valid JSON"},
		{name: "invalid response JSON", source: `hello`, target: DataBindingTarget{Kind: "body"}, reason: "not valid JSON"},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			config := r.Document.Messages[1].Execution
			config.Body = test.body
			config.Bindings = []DataBinding{{ID: "value", SourceMessageID: "login", SourcePointer: test.pointer, Target: test.target, Prefix: test.prefix}}
			if test.target.Kind == "header" {
				config.Headers[strings.ToLower(test.target.Name)] = "{{missing}}"
			}
			calls := 0
			report := Run(t.Context(), r, prepareTestRun(t, r), func(_ context.Context, request StepRequest) (StepResponse, error) {
				calls++
				if calls == 1 {
					return runResponse(test.source), nil
				}
				actual := request.Body
				if test.target.Kind == "header" {
					actual = request.Headers[test.target.Name]
					if len(request.Headers) != 1 {
						t.Fatalf("headers: %#v", request.Headers)
					}
				}
				if actual != test.want {
					t.Fatalf("got %s, want %s", actual, test.want)
				}
				return runResponse(`{}`), nil
			}, nil)
			if test.reason != "" {
				if calls != 1 || report.Status != "failed" || !strings.Contains(report.Reason, test.reason) {
					t.Fatalf("calls %d, %s: %s", calls, report.Status, report.Reason)
				}
			} else if calls != 2 || report.Status != "passed" {
				t.Fatalf("calls %d: %s", calls, report.Reason)
			}
		})
	}
}

func TestDataBindingTransformsBeforeTargetAndTrace(t *testing.T) {
	for _, tc := range []struct {
		name, source, wantRequest, wantSource, wantTransformed string
		target                                                 DataBindingTarget
		transforms                                             []DataBindingTransform
		prefix                                                 string
	}{
		{"integer body exact", `{"id":" 9007199254740993 "}`, `{"id":9007199254740993}`, `" 9007199254740993 "`, `9007199254740993`, DataBindingTarget{Kind: "body", Pointer: "/id"}, []DataBindingTransform{{Kind: "trim"}, {Kind: "to_integer"}}, ""},
		{"integer path then prefix", `{"id":" 42 "}`, `item-42`, `" 42 "`, `42`, DataBindingTarget{Kind: "path", Name: "id"}, []DataBindingTransform{{Kind: "trim"}, {Kind: "to_integer"}}, "item-"},
		{"number to exact string", `{"id":"9007199254740993"}`, `{"id":"9007199254740993"}`, `"9007199254740993"`, `"9007199254740993"`, DataBindingTarget{Kind: "body", Pointer: "/id"}, []DataBindingTransform{{Kind: "to_number"}, {Kind: "to_string"}}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			revision := runRevision()
			config := revision.Document.Messages[1].Execution
			config.Body = `{}`
			config.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: tc.target, Prefix: tc.prefix, Transforms: tc.transforms}}
			calls := 0
			report := Run(t.Context(), revision, prepareTestRun(t, revision), func(_ context.Context, request StepRequest) (StepResponse, error) {
				calls++
				if calls == 1 {
					return runResponse(tc.source), nil
				}
				got := request.Body
				if tc.target.Kind == "path" {
					got = request.PathParams[tc.target.Name]
				}
				if got != tc.wantRequest {
					t.Fatalf("request value = %q, want %q", got, tc.wantRequest)
				}
				return runResponse(`{}`), nil
			}, nil)
			if report.Status != "passed" || calls != 2 {
				t.Fatalf("status=%s calls=%d reason=%s", report.Status, calls, report.Reason)
			}
			result := report.Steps[1].BindingResults[0]
			if result.ValueJSON != tc.wantSource || result.TransformedValueJSON == nil || *result.TransformedValueJSON != tc.wantTransformed {
				t.Fatalf("trace = %+v", result)
			}
		})
	}
}

func TestDataBindingTransformFailurePreventsRecipientDispatch(t *testing.T) {
	for _, tc := range []struct {
		source     string
		transforms []DataBindingTransform
	}{
		{`{"id":"not-a-number"}`, []DataBindingTransform{{Kind: "to_number"}}},
		{`{"id":null}`, []DataBindingTransform{{Kind: "to_string"}}},
		{`{"id":{"x":1}}`, []DataBindingTransform{{Kind: "trim"}}},
		{`{"id":" 42 "}`, []DataBindingTransform{{Kind: "to_integer"}}},
	} {
		revision := runRevision()
		revision.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "body"}, Transforms: tc.transforms}}
		calls := 0
		report := Run(t.Context(), revision, prepareTestRun(t, revision), func(_ context.Context, _ StepRequest) (StepResponse, error) {
			calls++
			return runResponse(tc.source), nil
		}, nil)
		if calls != 1 || report.Status != "failed" || strings.Contains(report.Reason, "not-a-number") || strings.Contains(report.Reason, " 42 ") {
			t.Fatalf("status=%s calls=%d reason=%q", report.Status, calls, report.Reason)
		}
	}
}

func TestDataBindingLoopCurrentIterationAndClones(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.FormatVersion = 2
	r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 2}}}
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}, Transforms: []DataBindingTransform{{Kind: "to_string"}}}}
	initial := prepareTestRun(t, r)
	calls := 0
	received := []string{}
	updates := []RunReport{}
	report := Run(t.Context(), r, initial, func(_ context.Context, request StepRequest) (StepResponse, error) {
		if request.MessageID == "login" {
			calls++
			return runResponse(fmt.Sprintf(`{"id":%d}`, 41+calls)), nil
		}
		received = append(received, request.PathParams["id"])
		return runResponse(`{}`), nil
	}, func(snapshot RunReport) error { updates = append(updates, snapshot); return nil })
	if report.Status != "passed" || strings.Join(received, ",") != "42,43" {
		t.Fatalf("%s: %s %#v", report.Status, report.Reason, received)
	}
	result := report.Steps[3].BindingResults[0]
	if result.SourceOccurrence != 2 || result.SourceIterations[0].Iteration != 2 {
		t.Fatalf("result: %#v", result)
	}
	for _, update := range updates {
		for _, step := range update.Steps {
			if len(step.BindingResults) > 0 {
				step.BindingResults[0].SourceIterations[0].Iteration = 999
				step.BindingResults[0].ValueJSON = "tampered"
				if step.BindingResults[0].TransformedValueJSON != nil {
					*step.BindingResults[0].TransformedValueJSON = "tampered"
				}
			}
		}
		update.Document.Messages[1].Execution.Bindings[0].ID = "tampered"
		update.Document.Messages[1].Execution.Bindings[0].Transforms[0].Kind = "lower"
	}
	if report.Steps[3].BindingResults[0].SourceIterations[0].Iteration != 2 || report.Steps[3].BindingResults[0].ValueJSON != "43" || report.Steps[3].BindingResults[0].TransformedValueJSON == nil || *report.Steps[3].BindingResults[0].TransformedValueJSON != `"43"` || report.Document.Messages[1].Execution.Bindings[0].ID != "id" || report.Document.Messages[1].Execution.Bindings[0].Transforms[0].Kind != "to_string" {
		t.Fatal("mutable snapshot alias")
	}
	// Even a passed source from another iteration is unavailable.
	engine := runEngine{report: report}
	if engine.bindingSource("login", []LoopIteration{{FragmentID: "loop", Iteration: 3}}) != nil {
		t.Fatal("stale source reused")
	}
	report.Steps[2].Status = "skipped"
	engine.report = report
	if engine.bindingSource("login", []LoopIteration{{FragmentID: "loop", Iteration: 2}}) != nil {
		t.Fatal("skipped source reused")
	}
}

func TestDataBindingRuntimeKnownTypeAndBounds(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login"}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile","parameters":[{"in":"path","name":"id","schema":{"type":"integer"}}]}}}}`)
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
	for _, test := range []struct{ name, body, reason string }{
		{"actual mismatch", `{"id":"42"}`, "incompatible"},
		{"fraction mismatch", `{"id":42.5}`, "incompatible"},
		{"oversize response", strings.Repeat(" ", MaxExecutionBody+1), "1 МиБ"},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			report := Run(t.Context(), r, prepareTestRun(t, r), func(context.Context, StepRequest) (StepResponse, error) { calls++; return runResponse(test.body), nil }, nil)
			if calls != 1 || report.Status != "failed" || !strings.Contains(report.Reason, test.reason) {
				t.Fatalf("%d %s %s", calls, report.Status, report.Reason)
			}
		})
	}
	config := r.Document.Messages[1].Execution
	config.Bindings[0].Target = DataBindingTarget{Kind: "body", Pointer: "/id"}
	config.Body = `{"large":"` + strings.Repeat("a", MaxExecutionBody-30) + `"}`
	report := Run(t.Context(), r, prepareTestRun(t, r), func(context.Context, StepRequest) (StepResponse, error) {
		return runResponse(`{"id":"` + strings.Repeat("a", 100) + `"}`), nil
	}, nil)
	if report.Status != "failed" || !strings.Contains(report.Reason, "1 MiB") {
		t.Fatalf("body size: %s", report.Reason)
	}
}

func TestDataBindingCancellationAndRetainedLimit(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body"}}}
	ctx, cancel := context.WithCancel(t.Context())
	calls := 0
	report := Run(ctx, r, prepareTestRun(t, r), func(context.Context, StepRequest) (StepResponse, error) {
		calls++
		cancel()
		return runResponse(`42`), nil
	}, nil)
	if report.Status != "cancelled" || calls != 1 {
		t.Fatalf("cancel: %s calls %d", report.Status, calls)
	}
	initial := prepareTestRun(t, r)
	initial.Steps[0].Status = "passed"
	response := runResponse(`42`)
	initial.Steps[0].Response = &response
	engine := runEngine{report: initial, retained: MaxRunRetainedData - 1}
	err := engine.executeStep(t.Context(), 1, r.Document.Messages[1], func(context.Context, StepRequest) (StepResponse, error) {
		t.Fatal("dispatch despite retained limit")
		return StepResponse{}, nil
	})
	if err != errRunDataLimit || len(engine.report.Steps[1].BindingResults) != 0 {
		t.Fatalf("limit: %v", err)
	}
}
