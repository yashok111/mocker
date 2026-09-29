package scenarioexport

import (
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/designscenario"
	"github.com/yashok111/mocker/internal/jsonx"
)

func httpFixture(base string) designscenario.Revision {
	rev := revisionFixture()
	raw, _ := jsonx.Marshal(map[string]any{
		"openapi": "3.1.0", "info": map[string]any{"title": "API", "version": "1"},
		"servers": []any{map[string]any{"url": base}},
		"paths": map[string]any{"/items/{id}": map[string]any{"post": map[string]any{
			"x-mocker-canvas-operation-id": "create",
			"parameters":                   []any{map[string]any{"name": "id", "in": "path", "required": true}},
			"responses":                    map[string]any{"201": map[string]any{"description": "created"}},
		}}},
	})
	rev.Document.Contracts = []designscenario.Contract{{ID: "api", Document: raw}}
	rev.Document.Messages[0].Operation = &designscenario.OperationBinding{ContractID: "api", OperationKey: "create"}
	rev.Document.Messages[0].Execution = &designscenario.StepExecution{
		Enabled: true, PathParams: designscenario.ExecutionValues{"id": "{{id}}"},
		Query: designscenario.ExecutionValues{"q": "{{query}}"}, Headers: designscenario.ExecutionValues{"X-Test": "{{header}}"},
		Body: "{{body}}", Assertions: []designscenario.ExecutionAssertion{}, Extract: []designscenario.ExecutionExtraction{}, ExpectedStatus: new(201),
	}
	rev.Document.Execution = &designscenario.Execution{Variables: designscenario.ExecutionValues{
		"id": "a/b?#[x]", "query": "a b&c=1", "header": "quoted ' \" $HOME", "body": "@/etc/passwd\n$(touch owned) `touch owned` ' \" \\ 9007199254740993",
	}}
	return rev
}

func TestHTTPFormatsExportImmutableRevision(t *testing.T) {
	rev := httpFixture("https://example.test/v1")
	before, _ := jsonx.Marshal(rev)
	svc := New(validContract, 1<<20)
	for _, format := range []Format{"postman", "curl"} {
		t.Run(string(format), func(t *testing.T) {
			artifact, err := svc.Export(rev, Request{Format: format})
			if err != nil {
				t.Fatal(err)
			}
			if artifact.RevisionID != 11 || artifact.SourceHash != "saved-hash" {
				t.Fatal(artifact)
			}
			if !strings.Contains(artifact.Content, "9007199254740993") {
				t.Fatal("saved numeric text changed")
			}
			if _, err := svc.Export(rev, Request{Format: format, ContractID: "api"}); !errors.Is(err, ErrInvalidRequest) {
				t.Fatal(err)
			}
		})
	}
	after, _ := jsonx.Marshal(rev)
	if string(before) != string(after) {
		t.Fatal("export mutated revision")
	}
}

func TestCURLRunsWithoutEvaluatingUserData(t *testing.T) {
	if _, err := exec.LookPath("curl"); err != nil {
		t.Skip("curl unavailable")
	}
	type request struct{ method, path, query, header, body string }
	requests := make(chan request, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- request{r.Method, r.URL.EscapedPath(), r.URL.Query().Get("q"), r.Header.Get("X-Test"), string(body)}
		w.WriteHeader(201)
	}))
	defer server.Close()
	rev := httpFixture(server.URL + "/v1")
	second := rev.Document.Messages[0]
	second.ID = "repeat"
	rev.Document.Messages = append(rev.Document.Messages, second)
	artifact, err := New(validContract, 1<<20).Export(rev, Request{Format: "curl"})
	if err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	file := filepath.Join(dir, "requests.sh")
	if err := os.WriteFile(file, []byte(artifact.Content), 0600); err != nil {
		t.Fatal(err)
	}
	command := exec.CommandContext(t.Context(), "sh", file)
	command.Dir = dir
	if out, err := command.CombinedOutput(); err != nil {
		t.Fatalf("%v: %s\n%s", err, out, artifact.Content)
	}
	if _, err := os.Stat(filepath.Join(dir, "owned")); !errors.Is(err, os.ErrNotExist) {
		t.Fatal("shell injection executed")
	}
	for range 2 {
		select {
		case got := <-requests:
			if got.method != "POST" || got.path != "/v1/items/a%2Fb%3F%23%5Bx%5D" || got.query != "a b&c=1" || got.header != rev.Document.Execution.Variables["header"] || got.body != rev.Document.Execution.Variables["body"] {
				t.Fatalf("wrong request: %+v", got)
			}
		default:
			t.Fatal("repeated request was omitted")
		}
	}
}

func TestHTTPReadinessDiagnostics(t *testing.T) {
	cases := []struct {
		name, code string
		change     func(*designscenario.Revision)
	}{
		{"missing path", "path_parameter_missing", func(r *designscenario.Revision) { delete(r.Document.Messages[0].Execution.PathParams, "id") }},
		{"missing variable", "variable_missing", func(r *designscenario.Revision) { delete(r.Document.Execution.Variables, "id") }},
		{"binding", "binding_missing", func(r *designscenario.Revision) { r.Document.Messages[0].Operation.OperationKey = "gone" }},
		{"fragments", "execution_fragments_unsupported", func(r *designscenario.Revision) {
			r.Document.Fragments = []designscenario.Fragment{{ID: "loop", Kind: "loop"}}
		}},
		{"forms", "api_forms_pending", func(r *designscenario.Revision) {
			r.FormDrafts["all"] = `{"/canvas-contract/api/x":{"source":"x","propertySource":""}}`
		}},
		{"header newline", "header_invalid", func(r *designscenario.Revision) { r.Document.Execution.Variables["header"] = "a\r\nInjected: yes" }},
		{"disabled", "http_requests_empty", func(r *designscenario.Revision) { r.Document.Messages[0].Execution.Enabled = false }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			tc.change(&rev)
			for _, format := range []Format{"postman", "curl"} {
				_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
				blocked, ok := errors.AsType[*BlockedError](err)
				if !ok {
					t.Fatalf("%s: expected blocking diagnostics, got %v", format, err)
				}
				if !hasDiagnostic(blocked.Diagnostics, tc.code) {
					t.Fatalf("missing %s: %+v", tc.code, blocked.Diagnostics)
				}
			}
		})
	}
}

func hasDiagnostic(ds []Diagnostic, code string) bool {
	for _, d := range ds {
		if d.Code == code {
			return true
		}
	}
	return false
}

func TestCURLRejectsDataBindings(t *testing.T) {
	t.Parallel()
	for _, format := range []Format{CURL} {
		t.Run(string(format), func(t *testing.T) {
			rev := httpFixture("https://example.test")
			// Decode the public document shape so this regression also covers
			// loading a saved binding before an export is requested.
			config := `{"enabled":true,"pathParams":{"id":"old-id"},"query":{},"headers":{},"body":"","assertions":[],"extract":[],"bindings":[{"id":"order-id","sourceMessageId":"create-order","sourcePointer":"/id","target":{"kind":"path","name":"id"}}]}`
			if err := jsonx.Unmarshal([]byte(config), rev.Document.Messages[0].Execution); err != nil {
				t.Fatal(err)
			}
			before, err := jsonx.Marshal(rev)
			if err != nil {
				t.Fatal(err)
			}
			svc := New(validContract, 1<<20)
			artifact, err := svc.Export(rev, Request{Format: format})
			blocked, ok := errors.AsType[*BlockedError](err)
			if !ok || !hasDiagnostic(blocked.Diagnostics, "data_bindings_unsupported") {
				t.Fatalf("binding exported as a literal: artifact=%+v error=%v", artifact, err)
			}
			if artifact.Content != "" {
				t.Fatal("blocked export returned a runnable artifact")
			}
			for _, diagnostic := range blocked.Diagnostics {
				if diagnostic.Code == "data_bindings_unsupported" &&
					(diagnostic.Target == nil || diagnostic.Target.ID != "call" || diagnostic.Severity != "error") {
					t.Fatalf("binding diagnostic lacks message target: %+v", diagnostic)
				}
			}
			after, err := jsonx.Marshal(rev)
			if err != nil || string(before) != string(after) {
				t.Fatal("export changed saved bindings")
			}
		})
	}
}

func TestHTTPBaseURLAndBudgets(t *testing.T) {
	for _, format := range []Format{"postman", "curl"} {
		rev := httpFixture("")
		a, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
		if err != nil || !hasDiagnostic(a.Diagnostics, "base_url_missing") {
			t.Fatalf("%s: %+v %v", format, a, err)
		}
		if _, err := New(validContract, int64(len(a.Content))).Export(rev, Request{Format: format}); !errors.Is(err, ErrTooLarge) {
			t.Fatalf("wire budget not enforced: %v", err)
		}
	}
}

func TestCURLBlocksDependenciesOnResponseExtraction(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "id", Pointer: "/id"}}
	second := rev.Document.Messages[0]
	second.ID = "next"
	rev.Document.Messages = append(rev.Document.Messages, second)
	_, err := New(validContract, 1<<20).Export(rev, Request{Format: "curl"})
	blocked, ok := errors.AsType[*BlockedError](err)
	if !ok || !hasDiagnostic(blocked.Diagnostics, "dynamic_variable_unsupported") {
		t.Fatalf("%v", err)
	}
	if _, err := New(validContract, 1<<20).Export(rev, Request{Format: "postman"}); err != nil {
		t.Fatal(err)
	}
}

func TestPostmanScriptsPreserveTemplatesAndResolveSafely(t *testing.T) {
	rev := httpFixture("https://example.test/v1")
	rev.Document.Messages[0].Execution.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/a~1b/~0x", Equals: jsonx.RawMessage(`{"__proto__":{"safe":true},"id":42}`)}}
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "created", Pointer: "/a~1b/~0x/id"}}
	a, err := New(validContract, 1<<20).Export(rev, Request{Format: "postman"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(a.Content, "{{id}}") || !strings.Contains(a.Content, "{{body}}") {
		t.Fatal("variable references lost")
	}
	result := runPostman(t, a.Content, `{"a/b":{"~x":{"__proto__":{"safe":true},"id":42}}}`)
	if result["url"] != "https://example.test/v1/items/a%2Fb%3F%23%5Bx%5D?q=a%20b%26c%3D1" {
		t.Fatal(result)
	}
	if result["body"] != rev.Document.Execution.Variables["body"] {
		t.Fatal("body interpolation altered bytes", result)
	}
	if result["created"] != "42" || result["error"] != "" {
		t.Fatalf("assertions/extraction failed: %+v", result)
	}
}

func runPostman(t *testing.T, artifact, response string) map[string]any {
	t.Helper()
	if _, err := exec.LookPath("node"); err != nil {
		t.Skip("node unavailable")
	}
	script := `
const fs = require('node:fs');
const assert = require('node:assert/strict');
const input = JSON.parse(fs.readFileSync(0, 'utf8'));
const collection = JSON.parse(input.artifact);
assert.equal(collection.info.schema, 'https://schema.getpostman.com/json/collection/v2.1.0/collection.json');
const variables = new Map(collection.variable.map(v => [v.key, v.value]));
const output = {error: '', skipped: false};
const expected = value => ({to: {equal: other => assert.equal(value, other), deep: {equal: other => assert.deepEqual(value, other)}, be: {within: (low, high) => assert.ok(value >= low && value <= high)}}});
const pm = {
 collectionVariables: {get: name => variables.get(name), set: (name, value) => variables.set(name, value)},
 request: {url: {update: value => output.url = value}, headers: {upsert: ({key, value}) => output[key] = value}, body: {update: value => output.body = value}},
 response: {code: 201, text: () => input.response},
 execution: {skipRequest: () => output.skipped = true, setNextRequest: value => output.nextStopped = value === null},
 test: (_, callback) => {try {callback();} catch (error) {output.error = error.message;}}, expect: expected
};
try { for (const item of collection.item) { if (output.nextStopped) break; for (const event of item.event) { if (output.skipped) break; new Function('pm', event.script.exec.join('\n'))(pm); } } }
catch (error) {output.error = error.message;}
output.created = variables.get('created');
process.stdout.write(JSON.stringify(output));`
	command := exec.CommandContext(t.Context(), "node", "-e", script)
	data, _ := jsonx.Marshal(map[string]string{"artifact": artifact, "response": response})
	command.Stdin = strings.NewReader(string(data))
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("node: %v: %s", err, output)
	}
	result := map[string]any{}
	if err := jsonx.Unmarshal(output, &result); err != nil {
		t.Fatal(err, string(output))
	}
	return result
}

func TestPostmanRejectsUnsafeJSONNumbersBeforeRounding(t *testing.T) {
	for _, value := range []string{"9007199254740993", "9007199254740991.1", "0.10000000000000001", "1e999", "-0"} {
		t.Run(value, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			rev.Document.Messages[0].Execution.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage(value)}}
			_, err := New(validContract, 1<<20).Export(rev, Request{Format: "postman"})
			blocked, ok := errors.AsType[*BlockedError](err)
			if !ok || !hasDiagnostic(blocked.Diagnostics, "assertion_number_unsupported") {
				t.Fatal(err)
			}
			rev.Document.Messages[0].Execution.Assertions[0].Equals = jsonx.RawMessage("1")
			a, err := New(validContract, 1<<20).Export(rev, Request{Format: "postman"})
			if err != nil {
				t.Fatal(err)
			}
			result := runPostman(t, a.Content, `{"id":`+value+`}`)
			if !strings.Contains(result["error"].(string), "safe integer") {
				t.Fatal("response rounded", result)
			}
		})
	}
}

func TestHTTPRequiredInputsAndUnsupportedSerialization(t *testing.T) {
	cases := []struct{ name, extra, code string }{
		{"query", `"parameters":[{"in":"query","name":"required","required":true}]`, "required_input_missing"},
		{"body", `"requestBody":{"required":true,"content":{"application/json":{}}}`, "required_body_missing"},
		{"reference", `"parameters":[{"$ref":"#/components/parameters/id"}]`, "http_reference_unsupported"},
		{"array", `"parameters":[{"in":"query","name":"q","schema":{"type":"array"}}]`, "parameter_serialization_unsupported"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			rev.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/items/{id}":{"post":{"x-mocker-canvas-operation-id":"create",` + tc.extra + `}}}}`)
			rev.Document.Messages[0].Execution.Body = ""
			for _, format := range []Format{"postman", "curl"} {
				_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
				blocked, ok := errors.AsType[*BlockedError](err)
				if !ok || !hasDiagnostic(blocked.Diagnostics, tc.code) {
					t.Fatalf("%s: %v", format, err)
				}
			}
		})
	}
}

func TestHTTPServerDefaultsAndServiceVariables(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Contracts[0].Document = jsonx.RawMessage(`{"servers":[{"url":"/relative"},{"url":"https://{host}/{version}","variables":{"host":{"default":"saved.test"},"version":{"default":"v2"}}}],"paths":{"/status":{"get":{"x-mocker-canvas-operation-id":"create"}}}}`)
	other := rev.Document.Contracts[0]
	other.ID = "other"
	other.Document = jsonx.RawMessage(strings.ReplaceAll(string(other.Document), "saved.test", "other.test"))
	rev.Document.Contracts = append(rev.Document.Contracts, other)
	second := rev.Document.Messages[0]
	second.ID = "other"
	second.Operation = &designscenario.OperationBinding{ContractID: "other", OperationKey: "create"}
	rev.Document.Messages = append(rev.Document.Messages, second)
	for _, format := range []Format{"postman", "curl"} {
		a, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"https://saved.test/v2", "https://other.test/v2", "MOCKER_BASE_URL_1", "MOCKER_BASE_URL_2"} {
			if !strings.Contains(a.Content, value) {
				t.Fatal("missing", value)
			}
		}
	}
}

func TestPostmanDoesNotTreatParameterKeysAsJavaScriptPrototype(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Messages[0].Execution.Headers["__proto__"] = "literal-header"
	a, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	if err != nil {
		t.Fatal(err)
	}
	// A query named __proto__ must remain an own property after script decoding.
	rev.Document.Messages[0].Execution.Query["__proto__"] = "literal-query"
	a, err = New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	if err != nil {
		t.Fatal(err)
	}
	result := runPostman(t, a.Content, `{}`)
	if !strings.Contains(result["url"].(string), "__proto__=literal-query") {
		t.Fatal(result)
	}
}

func TestHTTPRejectsUnrepresentableInputs(t *testing.T) {
	cases := []struct {
		name   string
		change func(*designscenario.Revision)
	}{
		{"nul body", func(r *designscenario.Revision) { r.Document.Messages[0].Execution.Body = "a\x00b" }},
		{"nul variable body", func(r *designscenario.Revision) { r.Document.Execution.Variables["body"] = "a\x00b" }},
		{"nul query name", func(r *designscenario.Revision) { r.Document.Messages[0].Execution.Query["bad\x00key"] = "v" }},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			for _, format := range []Format{Postman, CURL} {
				rev := httpFixture("https://example.test")
				tc.change(&rev)
				_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
				if _, ok := errors.AsType[*BlockedError](err); !ok {
					t.Fatalf("%s accepted %s: %v", format, tc.name, err)
				}
			}
		})
	}
}

func TestHTTPBaseURLsAreIndependentForSharedContractServices(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Participants = append(rev.Document.Participants, designscenario.Participant{ID: "other", Name: "Second service", Kind: "service"})
	second := rev.Document.Messages[0]
	second.ID = "second"
	second.ToID = "other"
	rev.Document.Messages = append(rev.Document.Messages, second)
	for _, format := range []Format{Postman, CURL} {
		a, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(a.Content, "MOCKER_BASE_URL_2") {
			t.Fatal("services share a base URL variable")
		}
	}
}

func TestHTTPWarnsAboutImplicitBodyContentType(t *testing.T) {
	for _, format := range []Format{Postman, CURL} {
		a, err := New(validContract, 1<<20).Export(httpFixture("https://example.test"), Request{Format: format})
		if err != nil {
			t.Fatal(err)
		}
		if !hasDiagnostic(a.Diagnostics, "content_type_missing") {
			t.Fatal(a.Diagnostics)
		}
	}
}

func TestHTTPRequiredBodyChecksSavedVariableValue(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Execution.Variables["body"] = ""
	rev.Document.Contracts[0].Document = jsonx.RawMessage(strings.Replace(string(rev.Document.Contracts[0].Document), `"responses":`, `"requestBody":{"required":true},"responses":`, 1))
	for _, format := range []Format{Postman, CURL} {
		_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
		blocked, ok := errors.AsType[*BlockedError](err)
		if !ok || !hasDiagnostic(blocked.Diagnostics, "required_body_missing") {
			t.Fatalf("%s: %v", format, err)
		}
	}
}

func TestHTTPRejectsHeaderControlCharacters(t *testing.T) {
	for _, value := range []string{"\x01", "\x7f"} {
		rev := httpFixture("https://example.test")
		rev.Document.Execution.Variables["header"] = value
		for _, format := range []Format{Postman, CURL} {
			_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
			blocked, ok := errors.AsType[*BlockedError](err)
			if !ok || !hasDiagnostic(blocked.Diagnostics, "header_invalid") {
				t.Fatalf("%s: accepted control %q: %v", format, value, err)
			}
		}
	}
}

func TestHTTPBlocksReferencedAndComposedParameterSchemas(t *testing.T) {
	for _, schema := range []string{`{"$ref":"#/components/schemas/Tags"}`, `{"allOf":[{"type":"array","items":{"type":"string"}}]}`, `{"anyOf":[{"type":"string"},{"type":"array"}]}`, `{"oneOf":[{"type":"string"},{"type":"array"}]}`} {
		t.Run(schema, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			rev.Document.Contracts[0].Document = jsonx.RawMessage(`{"components":{"schemas":{"Tags":{"type":"array","items":{"type":"string"}}}},"paths":{"/items/{id}":{"post":{"x-mocker-canvas-operation-id":"create","parameters":[{"in":"query","name":"tags","schema":` + schema + `}]}}}}`)
			rev.Document.Messages[0].Execution.Query["tags"] = "a,b"
			for _, format := range []Format{Postman, CURL} {
				_, err := New(validContract, 1<<20).Export(rev, Request{Format: format})
				blocked, ok := errors.AsType[*BlockedError](err)
				if !ok || !hasDiagnostic(blocked.Diagnostics, "parameter_serialization_unsupported") {
					t.Fatalf("%s changed array serialization: %v", format, err)
				}
			}
		})
	}
}

func TestPostmanExtractionMatchesGoSerializationInNextRequest(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "created", Pointer: "/payload"}}
	next := rev.Document.Messages[0]
	next.ID = "next"
	next.Execution = new(*next.Execution)
	next.Execution.Body = "{{created}}"
	next.Execution.Extract = []designscenario.ExecutionExtraction{}
	rev.Document.Messages = []designscenario.Message{rev.Document.Messages[0], next}
	// UTF-16 ordering differs from UTF-8 for these keys; also cover HTML,
	// JavaScript line separators, scalar strings, arrays and nested objects.
	response := `{"payload":{"b":1,"a":{"z":"<>&\u2028\u2029","a":[true,null,"x"]},"\ud800\udc00":2,"\ue000":3,"lone":"\ud800","\udc00":"key"}}`
	var body map[string]jsonx.RawMessage
	if err := jsonx.Unmarshal([]byte(response), &body); err != nil {
		t.Fatal(err)
	}
	decoder := jsonx.NewDecoder(strings.NewReader(string(body["payload"])))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		t.Fatal(err)
	}
	expected, err := jsonx.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	artifact, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	if err != nil {
		t.Fatal(err)
	}
	result := runPostman(t, artifact.Content, response)
	if result["error"] != "" || result["body"] != string(expected) {
		t.Fatalf("next request body differs from runner: got %+v; expected %s", result, expected)
	}
}

func TestPostmanRejectsNegativeZeroBeforeLosingLexeme(t *testing.T) {
	rev := httpFixture("https://example.test")
	rev.Document.Messages[0].Execution.Extract = []designscenario.ExecutionExtraction{{Name: "created", Pointer: "/id"}}
	artifact, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
	if err != nil {
		t.Fatal(err)
	}
	result := runPostman(t, artifact.Content, `{"id":-0}`)
	if result["error"] == "" {
		t.Fatal("negative zero silently normalized", result)
	}
}

func TestPostmanStopsBeforeExtractionAndNextRequestOnFailedChecks(t *testing.T) {
	for _, reason := range []string{"status", "assertion", "pointer", "prerequest"} {
		t.Run(reason, func(t *testing.T) {
			rev := httpFixture("https://example.test")
			config := rev.Document.Messages[0].Execution
			config.Extract = []designscenario.ExecutionExtraction{{Name: "created", Pointer: "/id"}}
			response := `{"id":"saved"}`
			switch reason {
			case "status":
				config.ExpectedStatus = new(200)
			case "assertion":
				config.Assertions = []designscenario.ExecutionAssertion{{Pointer: "/id", Equals: jsonx.RawMessage(`"different"`)}}
			case "pointer":
				config.Extract = append(config.Extract, designscenario.ExecutionExtraction{Name: "missing", Pointer: "/missing"})
			case "prerequest":
				rev.Document.Contracts[0].Document = jsonx.RawMessage(strings.ReplaceAll(string(rev.Document.Contracts[0].Document), "https://example.test", "/relative"))
			}
			next := rev.Document.Messages[0]
			next.ID = "next"
			next.Execution = new(*config)
			next.Execution.Body = "next-request"
			next.Execution.Extract = []designscenario.ExecutionExtraction{}
			rev.Document.Messages = append(rev.Document.Messages, next)
			artifact, err := New(validContract, 1<<20).Export(rev, Request{Format: Postman})
			if err != nil {
				t.Fatal(err)
			}
			result := runPostman(t, artifact.Content, response)
			if result["error"] == "" || result["nextStopped"] != true || result["created"] != nil || result["body"] == "next-request" {
				t.Fatalf("failed step continued: %+v", result)
			}
		})
	}
}
