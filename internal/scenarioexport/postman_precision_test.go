package scenarioexport

import (
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func TestPostmanAssertionsCompareExactJSONValues(t *testing.T) {
	for _, tc := range []struct {
		name, expected, actual string
		equal                  bool
	}{
		{"large integers equal", "9007199254740993", "9007199254740993", true},
		{"nearby large integers", "9007199254740992", "9007199254740993", false},
		{"nearby fractions", "0.1", "0.10000000000000001", false},
		{"exponent equals integer", "1", "1e0", true},
		{"fraction equals integer", "1", "1.0", true},
		{"negative zero equals zero", "0", "-0", true},
		{"fraction and exponent shifts", "-0.0012300e+0005", "-123", true},
		{"leading exponent zeros", "1e+000999", "10e998", true},
		{"large exponent shift", "1e1000000000000000000000000000000", "10e999999999999999999999999999999", true},
		{"large negative exponent shift", "1e-1000000000000000000000000000000", "0.1e-999999999999999999999999999999", true},
		{"nearby huge exponents", "1e999999999999999999999999999999", "1e1000000000000000000000000000000", false},
		{"zero ignores exponent", "-0e999999999999999999999999999999", "0e-999999999999999999999999999999", true},
		{"nested numeric equality", `{"id":9007199254740993,"values":[1,-0,0.10000000000000001,1e999]}`, `{"values":[1.0,0,0.10000000000000001,10e998],"id":9007199254740993}`, true},
		{"nested numeric difference", `{"rows":[{"n":9007199254740992}]}`, `{"rows":[{"n":9007199254740993}]}`, false},
		{"number differs from string", "1", `"1"`, false},
		{"number differs from object", "1", `{}`, false},
		{"null differs from number", "null", "0", false},
		{"object differs from array", `{}`, `[]`, false},
		{"array order", `[1,2]`, `[2,1]`, false},
		{"missing object key", `{"a":null}`, `{"b":null}`, false},
		{"prototype keys", `{"__proto__":{"n":9007199254740993},"constructor":1}`, `{"constructor":1.0,"__proto__":{"n":9007199254740993}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			rev.Document.Messages[0].Execution.Assertions = []designscenario.ExecutionAssertion{{Pointer: "", Equals: jsonx.RawMessage(tc.expected)}}
			result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{tc.actual}})
			if passed := len(result.Errors) == 0; passed != tc.equal {
				t.Fatalf("%s against %s: passed=%v, want %v; errors=%v", tc.expected, tc.actual, passed, tc.equal, result.Errors)
			}
		})
	}
}

func TestPostmanPreciseExtractionsReachNextRequest(t *testing.T) {
	rev := httpFixture("https://example.test")
	source := rev.Document.Messages[0].Execution
	source.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/a~1b/~0key/1", Equals: jsonx.RawMessage("0.10000000000000001")}}
	source.Extract = []designscenario.ExecutionExtraction{
		{Name: "created", Pointer: "/a~1b/~0key"},
		{Name: "text", Pointer: "/text"},
		{Name: "root", Pointer: ""},
	}
	next := rev.Document.Messages[0]
	next.ID = "next"
	next.Execution = new(*source)
	next.Execution.Body = "{{created}}"
	next.Execution.Headers = designscenario.ExecutionValues{"X-Extracted": "{{text}}"}
	next.Execution.Assertions, next.Execution.Extract = nil, nil
	rev.Document.Messages = append(rev.Document.Messages, next)
	response := `{"text":"plain","a/b":{"~key":[9007199254740993,0.10000000000000001,1E+999,-0]}}`
	result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{response, `{}`}})
	if len(result.Errors) != 0 || len(result.Requests) != 2 {
		t.Fatalf("extraction chain failed: %+v", result)
	}
	if result.Requests[1].Body != `[9007199254740993,0.10000000000000001,1E+999,-0]` || result.Requests[1].Headers["X-Extracted"] != "plain" {
		t.Fatalf("extractions changed: %+v", result.Requests[1])
	}
	if result.Variables["root"] != `{"a/b":{"~key":[9007199254740993,0.10000000000000001,1E+999,-0]},"text":"plain"}` {
		t.Fatalf("root extraction changed: %v", result.Variables["root"])
	}
}

func TestPostmanExactChecksPublishExtractionsAtomically(t *testing.T) {
	for _, reason := range []string{"assertion", "missing pointer", "numeric token pointer", "invalid JSON"} {
		t.Run(reason, func(t *testing.T) {
			rev := postmanBindingFixture()
			rev.Document.Execution.Variables["captured"] = "old"
			config := rev.Document.Messages[0].Execution
			config.Extract = []designscenario.ExecutionExtraction{{Name: "captured", Pointer: "/id"}}
			response := `{"id":9007199254740993,"payload":1e999}`
			switch reason {
			case "assertion":
				config.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage("9007199254740992")}}
			case "missing pointer":
				config.Extract = append(config.Extract, designscenario.ExecutionExtraction{Name: "missing", Pointer: "/missing"})
			case "numeric token pointer":
				config.Extract = append(config.Extract, designscenario.ExecutionExtraction{Name: "missing", Pointer: "/id/raw"})
			case "invalid JSON":
				response = `{"id":9007199254740993,}`
			}
			result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"responses": []string{response, `{}`}})
			if len(result.Errors) == 0 || len(result.Requests) != 1 || result.Variables["captured"] != "old" || result.Variables["missing"] != nil || len(result.Local) != 0 {
				t.Fatalf("failure published variables or continued bindings: %+v", result)
			}
		})
	}
}

func TestPostmanExactAssertionsAndBindingsShareResponseSemantics(t *testing.T) {
	rev := postmanBindingFixture()
	rev.Document.Messages[0].Execution.Assertions = []designscenario.ExecutionAssertion{
		{Pointer: "/id", Equals: jsonx.RawMessage("9007199254740993")},
		{Pointer: "/payload", Equals: jsonx.RawMessage(`{"fraction":0.10000000000000001,"big":10e998,"zero":0}`)},
	}
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "captured", Pointer: "/payload/big"}}
	result := runPostmanChain(t, exportPostmanBindings(t, rev), map[string]any{"iterations": 2, "responses": []string{
		`{"id":9007199254740993,"payload":{"zero":-0,"big":1e999,"fraction":0.10000000000000001}}`, `{}`,
		`{"id":9007199254740993,"payload":{"zero":0.0,"big":1E+999,"fraction":0.10000000000000001}}`, `{}`,
	}})
	if len(result.Errors) != 0 || len(result.Requests) != 4 || len(result.Local) != 0 {
		t.Fatalf("exact checks interrupted binding state: %+v", result)
	}
	if result.Requests[1].Body != `{"big":1e999,"fraction":0.10000000000000001,"zero":-0}` || result.Requests[3].Body != `{"big":1E+999,"fraction":0.10000000000000001,"zero":0.0}` || result.Variables["captured"] != "1E+999" {
		t.Fatalf("bindings or extractions changed numeric lexemes: %+v", result)
	}
}

func TestPostmanExactJSONHandlesDeepNesting(t *testing.T) {
	// Exercise the full decoder limit directly through the collection renderer;
	// saving this assertion in a revision adds enclosing JSON object levels.
	value := strings.Repeat("[", 10000) + "9007199254740993" + strings.Repeat("]", 10000)
	svc := New(validContract, 1<<20)
	prepared, _, err := svc.prepareHTTP(t.Context(), httpFixture("https://example.test"), Postman)
	if err != nil {
		t.Fatal(err)
	}
	prepared.Requests[0].Execution.Assertions = []designscenario.ExecutionAssertion{{Pointer: "", Equals: jsonx.RawMessage(value)}}
	prepared.Requests[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "captured", Pointer: ""}}
	collection, err := svc.renderPostman("deep JSON", prepared)
	if err != nil {
		t.Fatal(err)
	}
	result := runPostmanChain(t, string(collection), map[string]any{"responses": []string{value}})
	if len(result.Errors) != 0 || result.Variables["captured"] != value {
		t.Fatalf("deep JSON failed: errors=%v captured=%v", result.Errors, result.Variables["captured"] != nil)
	}
}
