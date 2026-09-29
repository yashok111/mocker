package scenarioexport

import (
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"unicode"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

type bindingRuntimeInput struct {
	PathParams   map[string]string            `json:"pathParams"`
	Query        map[string]string            `json:"query"`
	Headers      map[string]string            `json:"headers"`
	Body         string                       `json:"body"`
	Bindings     []designscenario.DataBinding `json:"bindings"`
	BindingTypes map[string]string            `json:"bindingTypes"`
}

type bindingRuntimeOutput struct {
	Source bindingRuntimeInput `json:"source"`
	Error  string              `json:"error"`
}

func runBindingRuntime(t *testing.T, source bindingRuntimeInput, responses map[string]string) bindingRuntimeOutput {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	data, err := jsonx.Marshal(map[string]any{
		"source": source, "responses": responses, "runtime": postmanBindingRuntime(source.Bindings),
	})
	if err != nil {
		t.Fatal(err)
	}
	script := `const input = JSON.parse(require('node:fs').readFileSync(0, 'utf8'));
let error = '';
try { new Function('source', 'responses', input.runtime + '\napplyPostmanBindings(source, responses);')(input.source, input.responses); }
catch (failure) { error = failure.message; }
delete input.source.bindings;
process.stdout.write(JSON.stringify({source: input.source, error}));`
	command := exec.CommandContext(t.Context(), "node", "-e", script)
	command.Stdin = strings.NewReader(string(data))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, output)
	}
	var result bindingRuntimeOutput
	if err := jsonx.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	return result
}

func runtimeSource(kind, pointer, want string, transforms ...string) bindingRuntimeInput {
	binding := designscenario.DataBinding{
		ID: "value", SourceMessageID: "producer", SourcePointer: pointer,
		Target: designscenario.DataBindingTarget{Kind: kind},
	}
	if kind != "body" {
		binding.Target.Name = "value"
	}
	for _, transform := range transforms {
		binding.Transforms = append(binding.Transforms, designscenario.DataBindingTransform{Kind: transform})
	}
	return bindingRuntimeInput{
		PathParams: map[string]string{}, Query: map[string]string{}, Headers: map[string]string{},
		Bindings: []designscenario.DataBinding{binding}, BindingTypes: map[string]string{"value": want},
	}
}

func requireBindingRuntime(t *testing.T, source bindingRuntimeInput, response string) bindingRuntimeInput {
	t.Helper()
	result := runBindingRuntime(t, source, map[string]string{"producer": response})
	if result.Error != "" {
		t.Fatal(result.Error)
	}
	return result.Source
}

func TestPostmanBindingRuntimeLosslessValues(t *testing.T) {
	source := runtimeSource("body", "/a~1b/~0key/0", "object")
	response := `{"a/b":{"~key":[{"id":9007199254740993,"fraction":0.10000000000000001,"exp":1E+999,"zero":-0,"array":[true,null,"<>&\u2028\u2029"]}]}}`
	want := `{"array":[true,null,"\u003c\u003e\u0026\u2028\u2029"],"exp":1E+999,"fraction":0.10000000000000001,"id":9007199254740993,"zero":-0}`
	if got := requireBindingRuntime(t, source, response).Body; got != want {
		t.Fatalf("lossless body:\ngot  %s\nwant %s", got, want)
	}
	for _, target := range []string{"path", "query", "header"} {
		t.Run(target, func(t *testing.T) {
			result := requireBindingRuntime(t, runtimeSource(target, "", "string"), "9007199254740993")
			values := map[string]map[string]string{"path": result.PathParams, "query": result.Query, "header": result.Headers}
			if values[target]["value"] != "9007199254740993" {
				t.Fatalf("rounded or missing value: %+v", values[target])
			}
		})
	}
}

func TestPostmanBindingRuntimePointersAndPrototypeKeys(t *testing.T) {
	source := runtimeSource("body", "/__proto__", "object")
	source.Body = `{"nested":[0],"keep":1e999}`
	source.Bindings[0].Target.Pointer = "/nested/0"
	response := `{"__proto__":{"constructor":9007199254740993,"raw":"7","__number":"8","toString":false}}`
	want := `{"keep":1e999,"nested":[{"__number":"8","constructor":9007199254740993,"raw":"7","toString":false}]}`
	if got := requireBindingRuntime(t, source, response).Body; got != want {
		t.Fatalf("got %s, want %s", got, want)
	}
	source.Bindings[0].Target.Pointer = "/__proto__"
	source.Body = `{}`
	if got := requireBindingRuntime(t, source, response).Body; !strings.HasPrefix(got, `{"__proto__":{`) {
		t.Fatalf("prototype key lost: %s", got)
	}
	source = runtimeSource("query", "/constructor", "string")
	source.Bindings[0].Target.Name = "__proto__"
	if got := requireBindingRuntime(t, source, `{"constructor":"own"}`).Query["__proto__"]; got != "own" {
		t.Fatalf("prototype text target lost: %s", got)
	}
}

func TestPostmanBindingRuntimeGoStrings(t *testing.T) {
	for _, transform := range []string{"lower", "upper"} {
		t.Run(transform, func(t *testing.T) {
			var input strings.Builder
			input.WriteString("ßİıſΣσςKﬃ\ufeff")
			for _, span := range unicode.CaseRanges {
				for r := span.Lo; r <= span.Hi; r++ {
					input.WriteRune(rune(r))
				}
			}
			value := input.String()
			want := strings.ToLower(value)
			if transform == "upper" {
				want = strings.ToUpper(value)
			}
			raw, _ := jsonx.Marshal(value)
			got := requireBindingRuntime(t, runtimeSource("query", "", "string", transform), string(raw))
			if got.Query["value"] != want {
				t.Fatalf("%s differs from Go simple Unicode case", transform)
			}
		})
	}
	for _, input := range []string{"\u0085\u00a0\u1680\u2000\u200a\u2028\u2029\u202f\u205f\u3000trim\u0085", "\ufeffkeep\ufeff"} {
		raw, _ := jsonx.Marshal(input)
		got := requireBindingRuntime(t, runtimeSource("query", "", "string", "trim"), string(raw))
		if got.Query["value"] != strings.TrimSpace(input) {
			t.Fatalf("trim differs: %q", got.Query["value"])
		}
	}
	source := runtimeSource("body", "", "object")
	got := requireBindingRuntime(t, source, `{"\ud800":"\ud800","\ue000":1,"😀":2,"\udfff":3}`).Body
	if got != "{\"\ue000\":1,\"�\":3,\"😀\":2}" {
		t.Fatalf("surrogate normalization/key order: %s", got)
	}
}

func TestPostmanBindingRuntimeNumbersAndTransforms(t *testing.T) {
	for _, number := range []string{"9007199254740993", "1.0", "100e-2", "0e-999999999999999999999", "-0", "1e99999999999999999999"} {
		t.Run(number, func(t *testing.T) {
			if got := requireBindingRuntime(t, runtimeSource("body", "", "integer"), number).Body; got != number {
				t.Fatalf("got %s, want %s", got, number)
			}
		})
	}
	for _, number := range []string{"0.10000000000000001", "1e-999999999999999999999", "-12.340e-1"} {
		if got := requireBindingRuntime(t, runtimeSource("body", "", "number"), number).Body; got != number {
			t.Fatalf("got %s, want %s", got, number)
		}
	}
	for _, tc := range []struct {
		response, want string
		transforms     []string
	}{
		{`" 9007199254740993 "`, "9007199254740993", []string{"trim", "to_integer", "to_string"}},
		{`" -0.10000000000000001e+999 "`, "-0.10000000000000001e+999", []string{"trim", "to_number", "to_string"}},
		{`false`, "false", []string{"to_string"}},
		{`" ABC "`, "abc", []string{"trim", "lower"}},
	} {
		source := runtimeSource("header", "", "string", tc.transforms...)
		source.Bindings[0].Prefix = "prefix "
		if got := requireBindingRuntime(t, source, tc.response).Headers["value"]; got != "prefix "+tc.want {
			t.Fatalf("got %s, want prefix %s", got, tc.want)
		}
	}
}

func TestPostmanBindingRuntimeErrors(t *testing.T) {
	for _, tc := range []struct {
		name, response, kind, pointer, want, body, targetPointer, reason string
		transforms                                                       []string
	}{
		{name: "not JSON", response: `x`, reason: "source response is not valid JSON"},
		{name: "trailing JSON", response: `{} {}`, reason: "source response is not valid JSON"},
		{name: "number grammar", response: `01`, reason: "source response is not valid JSON"},
		{name: "trailing comma", response: `[1,]`, reason: "source response is not valid JSON"},
		{name: "bad escape", response: `"\x41"`, reason: "source response is not valid JSON"},
		{name: "control", response: "\"\n\"", reason: "source response is not valid JSON"},
		{name: "missing source pointer", response: `{}`, pointer: "/missing", reason: "source JSON Pointer is missing"},
		{name: "invalid source pointer", response: `{}`, pointer: "/~2", reason: "source JSON Pointer is missing"},
		{name: "numeric wrapper read", response: `1`, pointer: "/raw", reason: "source JSON Pointer is missing"},
		{name: "array property", response: `[1]`, pointer: "/length", reason: "source JSON Pointer is missing"},
		{name: "array leading zero", response: `[1]`, pointer: "/00", reason: "source JSON Pointer is missing"},
		{name: "array newline", response: `[1]`, pointer: "/0\n", reason: "source JSON Pointer is missing"},
		{name: "prototype read", response: `{}`, pointer: "/constructor", reason: "source JSON Pointer is missing"},
		{name: "text null", response: `null`, reason: "text targets require a non-null scalar"},
		{name: "text object", response: `{}`, reason: "text targets require a non-null scalar"},
		{name: "text array", response: `[]`, reason: "text targets require a non-null scalar"},
		{name: "type mismatch", response: `"x"`, want: "integer", reason: "incompatible binding types: string → integer"},
		{name: "numeric type", response: `1.2`, want: "integer", reason: "incompatible binding types: number → integer"},
		{name: "transform result type", response: `"1"`, want: "integer", transforms: []string{"to_number"}, reason: "incompatible binding types: number → integer"},
		{name: "transform input type", response: `1`, transforms: []string{"trim"}, reason: "преобразование 1 (trim) не принимает тип integer"},
		{name: "transform chain input", response: `"1"`, transforms: []string{"to_number", "upper"}, reason: "преобразование 2 (upper) не принимает тип number"},
		{name: "invalid numeric string", response: `"+1"`, transforms: []string{"to_number"}, reason: "некорректное число JSON"},
		{name: "noninteger string", response: `"1.0"`, transforms: []string{"to_integer"}, reason: "некорректное число JSON"},
		{name: "numeric whitespace", response: `"1\n"`, transforms: []string{"to_number"}, reason: "некорректное число JSON"},
		{name: "bad body", response: `1`, kind: "body", body: "x", reason: "request body is not valid JSON"},
		{name: "invalid target pointer", response: `1`, kind: "body", body: `{}`, targetPointer: "/~2", reason: "invalid target JSON Pointer"},
		{name: "missing intermediate", response: `1`, kind: "body", body: `{}`, targetPointer: "/a/b", reason: "target intermediate container is missing"},
		{name: "numeric wrapper write", response: `1`, kind: "body", body: `1`, targetPointer: "/raw", reason: "target parent must be an object or array"},
		{name: "array append", response: `1`, kind: "body", body: `[]`, targetPointer: "/-", reason: "target array index is missing"},
		{name: "array newline write", response: `1`, kind: "body", body: `[0]`, targetPointer: "/0\n", reason: "target array index is missing"},
		{name: "nested template", response: `"{{secret}}"`, reason: "Unsupported template or NUL"},
		{name: "NUL text", response: `"\u0000"`, reason: "Unsupported template or NUL"},
		{name: "header CRLF", response: `"a\r\nb"`, kind: "header", reason: "Invalid HTTP header"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.kind == "" {
				tc.kind = "query"
			}
			if tc.want == "" {
				tc.want = "unknown"
			}
			source := runtimeSource(tc.kind, tc.pointer, tc.want, tc.transforms...)
			source.Body = tc.body
			source.Bindings[0].Target.Pointer = tc.targetPointer
			result := runBindingRuntime(t, source, map[string]string{"producer": tc.response})
			if !strings.HasPrefix(result.Error, `binding "value": `) || !strings.Contains(result.Error, tc.reason) {
				t.Fatalf("error %q, want binding ID and %q", result.Error, tc.reason)
			}
		})
	}
	result := runBindingRuntime(t, runtimeSource("query", "", "string"), map[string]string{})
	if !strings.Contains(result.Error, `binding "value": successful source occurrence is unavailable`) {
		t.Fatalf("missing source: %s", result.Error)
	}
}

func TestPostmanBindingRuntimeLimits(t *testing.T) {
	for _, n := range []int{50_000, 50_001} {
		raw, _ := jsonx.Marshal(strings.Repeat("😀", n))
		result := runBindingRuntime(t, runtimeSource("query", "", "string"), map[string]string{"producer": string(raw)})
		if (result.Error == "") != (n == 50_000) {
			t.Fatalf("rune count %d: %s", n, result.Error)
		}
	}
	for _, tc := range []struct {
		name, response, reason string
		transforms             []string
	}{
		{"body bytes", `"` + strings.Repeat("😀", 1<<18) + `"`, "resolved body exceeds 1 MiB", nil},
		{"transform bytes", `"` + strings.Repeat("<", 1<<18) + `"`, "результат преобразования превышает допустимый размер", []string{"trim"}},
		{"transform count", `"x"`, "допускается не более 8 преобразований", []string{"trim", "trim", "trim", "trim", "trim", "trim", "trim", "trim", "trim"}},
		{"depth", strings.Repeat("[", 10_001) + "0" + strings.Repeat("]", 10_001), "nesting", nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			result := runBindingRuntime(t, runtimeSource("body", "", "unknown", tc.transforms...), map[string]string{"producer": tc.response})
			if !strings.Contains(result.Error, tc.reason) {
				t.Fatalf("error %q, want %q", result.Error, tc.reason)
			}
		})
	}
}

func TestPostmanBindingRuntimeGoSerialization(t *testing.T) {
	for i, raw := range []string{
		`{"z":-0,"a":[1e-999,9007199254740993,0.10000000000000001]}`,
		`{"2":2,"10":10,"1":1,"<":"&>","arr":[null,true,false,"\b\f\n\r\t\u0001"]}`,
		`{"dup":1,"dup":2,"unicode":"\ud800\udc00\ud800x\udfff"}`,
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			decoder := jsonx.NewDecoder(strings.NewReader(raw))
			decoder.UseNumber()
			var value any
			if err := decoder.Decode(&value); err != nil {
				t.Fatal(err)
			}
			want, err := jsonx.Marshal(value)
			if err != nil {
				t.Fatal(err)
			}
			got := requireBindingRuntime(t, runtimeSource("body", "", "unknown"), raw).Body
			if got != string(want) {
				t.Fatalf("got %s, want %s", got, want)
			}
		})
	}
}

func TestPostmanBindingRuntimeFullJSONDepth(t *testing.T) {
	for _, delimiters := range [][2]string{{"[", "]"}, {`{"a":`, "}"}} {
		raw := strings.Repeat(delimiters[0], 10_000) + "9007199254740993" + strings.Repeat(delimiters[1], 10_000)
		got := requireBindingRuntime(t, runtimeSource("body", "", "unknown"), raw).Body
		if got != raw {
			t.Fatal("JSON at the Go decoder nesting limit did not round-trip")
		}
	}
}
