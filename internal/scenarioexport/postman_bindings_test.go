package scenarioexport

import (
	"errors"
	"os/exec"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func postmanBindingFixture() designscenario.Revision {
	rev := httpFixture("https://example.test")
	source := rev.Document.Messages[0]
	source.ID = "source"
	source.Execution = &designscenario.StepExecution{Enabled: true, PathParams: designscenario.ExecutionValues{"id": "source"}, ExpectedStatus: new(201)}
	next := source
	next.ID = "consumer"
	next.Execution = &designscenario.StepExecution{Enabled: true, Bindings: []designscenario.DataBinding{
		{ID: "id", SourceMessageID: "source", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "path", Name: "id"}},
		{ID: "body", SourceMessageID: "source", SourcePointer: "/payload", Target: designscenario.DataBindingTarget{Kind: "body"}},
	}}
	rev.Document.Messages = []designscenario.Message{source, next}
	return rev
}

type postmanRunRequest struct {
	URL, Body        string
	Headers          map[string]string
	Index, Iteration int
}

type postmanRunResult struct {
	Requests  []postmanRunRequest
	Errors    []string
	Local     map[string]any
	Variables map[string]any
	Skipped   bool
}

func runPostmanChain(t *testing.T, artifact string, options map[string]any) postmanRunResult {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	options["artifact"] = artifact
	data, err := jsonx.Marshal(options)
	if err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "node", "testdata/postman-runner.cjs")
	command.Stdin = strings.NewReader(string(data))
	out, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, out)
	}
	var result postmanRunResult
	if err := jsonx.Unmarshal(out, &result); err != nil {
		t.Fatal(err, string(out))
	}
	return result
}

func exportPostmanBindings(t *testing.T, rev designscenario.Revision) string {
	t.Helper()
	a, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	if err != nil {
		t.Fatal(err)
	}
	return a.Content
}

func TestPostmanClearsBodyWhenTemplateResolvesToEmpty(t *testing.T) {
	for _, withBinding := range []bool{false, true} {
		name := "without bindings"
		if withBinding {
			name = "with path binding"
		}
		t.Run(name, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			rev.Document.Messages = rev.Document.Messages[:1]
			if withBinding {
				rev = postmanBindingFixture()
				// Keep the response-to-path binding; the body is authored text.
				rev.Document.Messages[1].Execution.Bindings = rev.Document.Messages[1].Execution.Bindings[:1]
			}
			rev.Document.Execution.Variables["empty"] = ""
			consumer := len(rev.Document.Messages) - 1
			rev.Document.Messages[consumer].Execution.Body = "{{empty}}"
			result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{
				"responses": []string{`{"id":"bound"}`, `{}`},
			})
			if len(result.Errors) != 0 || len(result.Requests) != len(rev.Document.Messages) {
				t.Fatalf("request failed: %+v", result)
			}
			if body := result.Requests[consumer].Body; body != "" {
				t.Fatalf("resolved empty body retained authored text: %q", body)
			}
		})
	}
}

func TestPostmanBindingsExecutePreciseResponseChain(t *testing.T) {
	content := exportPostmanBindings(t, postmanBindingFixture())
	result := runPostmanChain(t, content, map[string]any{"responses": []string{`{"id":9007199254740993,"payload":{"n":0.10000000000000001,"zero":-0,"big":1e999}}`, `{}`}})
	if len(result.Errors) != 0 || len(result.Requests) != 2 {
		t.Fatalf("chain failed: %+v", result)
	}
	got := result.Requests[1]
	if got.URL != "https://example.test/items/9007199254740993" || got.Body != `{"big":1e999,"n":0.10000000000000001,"zero":-0}` {
		t.Fatalf("binding rounded: %+v", got)
	}
	if len(result.Local) != 0 {
		t.Fatalf("response cache retained after run: %+v", result.Local)
	}
}

func TestPostmanBindingsFreshIterationsAndManualConsumer(t *testing.T) {
	content := exportPostmanBindings(t, postmanBindingFixture())
	result := runPostmanChain(t, content, map[string]any{"iterations": 2, "responses": []string{`{"id":"first","payload":1}`, `{}`, `{"id":"second","payload":2}`, `{}`}})
	if len(result.Errors) != 0 || len(result.Requests) != 4 || result.Requests[3].URL != "https://example.test/items/second" {
		t.Fatalf("iteration state: %+v", result)
	}
	manual := runPostmanChain(t, content, map[string]any{"order": []int{1}, "responses": []string{`{}`}})
	if len(manual.Errors) == 0 || len(manual.Requests) != 0 || !manual.Skipped {
		t.Fatalf("manual consumer sent without producer: %+v", manual)
	}
}

func TestPostmanBindingsStopOnFailures(t *testing.T) {
	for _, reason := range []string{"status", "assertion", "extraction", "pointer", "invalid JSON", "nested template", "body pointer"} {
		t.Run(reason, func(t *testing.T) {
			rev := postmanBindingFixture()
			response := `{"id":"value","payload":{"ok":true}}`
			switch reason {
			case "status":
				rev.Document.Messages[0].Execution.ExpectedStatus = new(200)
			case "assertion":
				rev.Document.Messages[0].Execution.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage(`"other"`)}}
			case "extraction":
				rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "missing", Pointer: "/missing"}}
			case "pointer":
				response = `{"payload":1}`
			case "invalid JSON":
				response = `not JSON`
			case "nested template":
				response = `{"id":"{{secret}}","payload":1}`
			case "body pointer":
				rev.Document.Messages[1].Execution.Body = `{}`
				rev.Document.Messages[1].Execution.Bindings[1].Target.Pointer = "/missing/field"
			}
			result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{response, `{}`}})
			if len(result.Errors) == 0 || len(result.Requests) != 1 || len(result.Local) != 0 {
				t.Fatalf("failure continued or retained state: %+v", result)
			}
		})
	}
}

func TestPostmanBindingsUseNamedProducerAndEncodeParameters(t *testing.T) {
	rev := postmanBindingFixture()
	consumer := rev.Document.Messages[1]
	middle := rev.Document.Messages[0]
	middle.ID = "middle"
	rev.Document.Messages = []designscenario.Message{rev.Document.Messages[0], middle, consumer}
	config := consumer.Execution
	config.PathParams = designscenario.ExecutionValues{"id": "{{overwritten_missing}}"}
	config.Headers = designscenario.ExecutionValues{"AUTHORIZATION": "{{also_missing}}"}
	config.Body = `{"rows":[{"untouched":9007199254740993}]}`
	config.Bindings = []designscenario.DataBinding{
		{ID: "path", SourceMessageID: "source", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "path", Name: "id"}, Transforms: []designscenario.DataBindingTransform{{Kind: "trim"}}},
		{ID: "query", SourceMessageID: "source", SourcePointer: "/id", Target: designscenario.DataBindingTarget{Kind: "query", Name: "q"}},
		{ID: "header", SourceMessageID: "source", SourcePointer: "/token", Target: designscenario.DataBindingTarget{Kind: "header", Name: "Authorization"}, Prefix: "Bearer ", Transforms: []designscenario.DataBindingTransform{{Kind: "trim"}, {Kind: "upper"}}},
		{ID: "number", SourceMessageID: "source", SourcePointer: "/n", Target: designscenario.DataBindingTarget{Kind: "body", Pointer: "/rows/0/id"}, Transforms: []designscenario.DataBindingTransform{{Kind: "trim"}, {Kind: "to_integer"}}},
	}
	result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{`{"id":" a/b? ","token":" ßй ","n":" 9007199254740993 "}`, `{"id":"wrong"}`, `{}`}})
	if len(result.Errors) != 0 || len(result.Requests) != 3 {
		t.Fatalf("chain: %+v", result)
	}
	got := result.Requests[2]
	if got.URL != "https://example.test/items/a%2Fb%3F?q=%20a%2Fb%3F%20" || got.Headers["Authorization"] != "Bearer ßЙ" || got.Body != `{"rows":[{"id":9007199254740993,"untouched":9007199254740993}]}` {
		t.Fatalf("wrong producer/transform: %+v", got)
	}
	if len(result.Local) != 0 {
		t.Fatal("cache retained")
	}
}

func TestPostmanBindingsRejectSkippedIntermediateStep(t *testing.T) {
	rev := postmanBindingFixture()
	middle := rev.Document.Messages[0]
	middle.ID = "middle"
	rev.Document.Messages = []designscenario.Message{rev.Document.Messages[0], middle, rev.Document.Messages[1]}
	result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"order": []int{0, 2}, "responses": []string{`{"id":1,"payload":{}}`}})
	if len(result.Errors) == 0 || len(result.Requests) != 1 || !result.Skipped || len(result.Local) != 0 {
		t.Fatalf("skipped step accepted: %+v", result)
	}
}

func TestPostmanBindingsPreserveRevisionAndVariableNamespace(t *testing.T) {
	rev := postmanBindingFixture()
	rev.Document.Messages[0].ID = "__proto__"
	for i := range rev.Document.Messages[1].Execution.Bindings {
		rev.Document.Messages[1].Execution.Bindings[i].SourceMessageID = "__proto__"
	}
	rev.Document.Execution.Variables["__MOCKER_BINDING_STATE"] = "user value"
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "__MOCKER_BINDING_STATE_", Pointer: "/id"}}
	before, _ := jsonx.Marshal(rev)
	content := exportPostmanBindings(t, rev)
	if content != exportPostmanBindings(t, rev) {
		t.Fatal("export is nondeterministic")
	}
	after, _ := jsonx.Marshal(rev)
	if string(before) != string(after) {
		t.Fatal("export mutated revision")
	}
	result := runPostmanChain(t, content, map[string]any{"responses": []string{`{"id":"value","payload":{"__proto__":{"safe":true}}}`, `{}`}})
	if len(result.Errors) != 0 || len(result.Requests) != 2 || result.Variables["__MOCKER_BINDING_STATE"] != "user value" || result.Variables["__MOCKER_BINDING_STATE_"] != "value" || len(result.Local) != 0 {
		t.Fatalf("namespace collision: %+v", result)
	}
	if result.Requests[1].Body != `{"__proto__":{"safe":true}}` {
		t.Fatalf("unsafe property handling: %+v", result)
	}
	_, err := New(validContract, int64(len(content))).Export(rev, Request{Format: Postman})
	if !errors.Is(err, ErrTooLarge) {
		t.Fatalf("budget not enforced: %v", err)
	}
}

func TestPostmanBindingsOversizeSourceDoesNotPublishExtractions(t *testing.T) {
	rev := postmanBindingFixture()
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "captured", Pointer: "/id"}}
	response := `{"id":"value","payload":"` + strings.Repeat("a", designscenario.MaxExecutionBody) + `"}`
	result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{response}})
	if len(result.Errors) == 0 || len(result.Requests) != 1 || result.Variables["captured"] != nil || len(result.Local) != 0 {
		t.Fatalf("oversize source published partial state: errors=%v requests=%d captured=%v local=%d", result.Errors, len(result.Requests), result.Variables["captured"], len(result.Local))
	}
}
