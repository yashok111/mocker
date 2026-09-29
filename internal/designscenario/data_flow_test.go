package designscenario

import (
	"fmt"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func bindingRevision() Revision {
	r := runRevision()
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
	return r
}
func TestDataFlowAvailability(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name    string
		change  func(*Revision)
		invalid bool
	}{
		{"linear", func(*Revision) {}, false},
		{"removed", func(r *Revision) { r.Document.Messages[1].Execution.Bindings[0].SourceMessageID = "gone" }, true},
		{"self", func(r *Revision) { r.Document.Messages[1].Execution.Bindings[0].SourceMessageID = "profile" }, true},
		{"later", func(r *Revision) {
			r.Document.Messages[0], r.Document.Messages[1] = r.Document.Messages[1], r.Document.Messages[0]
		}, true},
		{"disabled source", func(r *Revision) { r.Document.Messages[0].Execution.Enabled = false }, true},
		{"decorative source", func(r *Revision) { r.Document.Messages[0].Kind = "response" }, true},
		{"missing source operation", func(r *Revision) { r.Document.Messages[0].Operation = nil }, true},
		{"exit loop", func(r *Revision) {
			r.Document.FormatVersion = 2
			r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Iterations: 2}}}
		}, true},
		{"same loop", func(r *Revision) {
			r.Document.FormatVersion = 2
			r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "login", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 2}}}
		}, false},
		{"outside into loop", func(r *Revision) {
			r.Document.FormatVersion = 2
			r.Document.Fragments = []Fragment{{ID: "loop", Kind: "loop", FromMessageID: "profile", ToMessageID: "profile", Execution: &FragmentExecution{Iterations: 2}}}
		}, false},
		{"exit opt", func(r *Revision) {
			r.Document.Fragments = []Fragment{{ID: "opt", Kind: "opt", FromMessageID: "login", ToMessageID: "login", Execution: &FragmentExecution{Condition: &ExecutionCondition{Variable: "flag", Operator: "exists"}}}}
		}, true},
		{"different alt branch", func(r *Revision) {
			r.Document.FormatVersion = 2
			r.Document.Fragments = []Fragment{{ID: "alt", Kind: "alt", FromMessageID: "login", ToMessageID: "profile", Branches: []FragmentBranch{{ID: "a", FromMessageID: "login", ToMessageID: "login", Execution: &BranchExecution{Condition: &ExecutionCondition{Variable: "flag", Operator: "exists"}}}, {ID: "b", FromMessageID: "profile", ToMessageID: "profile", Execution: &BranchExecution{Otherwise: true}}}}}
		}, true},
		{"ambiguous legacy", func(r *Revision) {
			r.Document.Fragments = []Fragment{{ID: "a", Kind: "loop", FromMessageID: "login", ToMessageID: "profile"}, {ID: "b", Kind: "opt", FromMessageID: "login", ToMessageID: "profile"}}
		}, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := bindingRevision()
			test.change(&r)
			analysis := AnalyzeDataFlow(r.Document)
			failed := false
			for _, d := range analysis.Diagnostics {
				failed = failed || d.Severity == "error"
			}
			if failed != test.invalid {
				t.Fatalf("diagnostics: %#v", analysis.Diagnostics)
			}
			if test.invalid {
				if _, err := PrepareRun(r, "id", "", "mcp", nil); err == nil {
					t.Fatal("invalid analysis passed preflight")
				}
			}
		})
	}
}
func TestDataFlowCatalogTypesRefsAndParameters(t *testing.T) {
	t.Parallel()
	r := bindingRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"components":{"schemas":{"Order":{"type":"object","required":["id"],"properties":{"id":{"type":"integer"},"items":{"type":"array","items":{"type":"string"}},"a/b":{"type":"string"}}}},"parameters":{"ID":{"in":"path","name":"id","required":true,"schema":{"type":"number"}}}},"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}},"/profile":{"parameters":[{"$ref":"#/components/parameters/ID"},{"in":"query","name":"q","schema":{"type":"integer"}}],"get":{"x-mocker-canvas-operation-id":"profile","parameters":[{"in":"query","name":"q","schema":{"type":"string"}}],"requestBody":{"required":true,"content":{"application/json":{"schema":{"$ref":"#/components/schemas/Order"}}}}}}}}`)
	analysis := AnalyzeDataFlow(r.Document)
	if len(analysis.Diagnostics) != 0 {
		t.Fatalf("diagnostics: %#v", analysis.Diagnostics)
	}
	fields := analysis.Messages[0].ResponseFields
	if len(fields) != 4 || fields[1].Pointer != "/a~1b" || fields[2].Type != "integer" || !fields[2].Required || fields[3].Type != "array" {
		t.Fatalf("response fields: %#v", fields)
	}
	fields = analysis.Messages[1].RequestFields
	if fields[0].Kind != "path" || fields[0].Type != "number" || !fields[0].Required || fields[1].Type != "string" {
		t.Fatalf("request fields: %#v", fields)
	}
	schemas := bindingSchemas(r.Document)
	if schemas[0].responseType("/items/0") != "string" || schemas[0].responseType("/items/01") != "unknown" {
		t.Fatal("manual array index schema")
	}
	r.Document.Messages[1].Execution.Bindings[0].Target = DataBindingTarget{Kind: "body", Pointer: "/a~1b"}
	analysis = AnalyzeDataFlow(r.Document)
	found := false
	for _, d := range analysis.Diagnostics {
		found = found || d.Severity == "error" && strings.Contains(d.Message, "incompatible")
	}
	if !found {
		t.Fatal("did not reject incompatible types")
	}
}
func TestDataFlowUnknownAndTruncatedSchemas(t *testing.T) {
	t.Parallel()
	for _, test := range []struct {
		name, schema, defs string
		truncated          bool
	}{
		{"external ref", `{"$ref":"https://example.invalid/schema"}`, `{}`, false},
		{"cycle", `{"$ref":"#/components/schemas/A"}`, `{"A":{"$ref":"#/components/schemas/A"}}`, false},
		{"recursive object", `{"$ref":"#/components/schemas/A"}`, `{"A":{"type":"object","properties":{"next":{"$ref":"#/components/schemas/A"}}}}`, true},
		{"union", `{"oneOf":[{"type":"string"},{"type":"number"}]}`, `{}`, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := bindingRevision()
			r.Document.Contracts[0].Document = jsonx.RawMessage(fmt.Sprintf(`{"components":{"schemas":%s},"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":%s}}}}}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile"}}}}`, test.defs, test.schema))
			analysis := AnalyzeDataFlow(r.Document)
			warn, truncated := false, false
			for _, d := range analysis.Diagnostics {
				if d.Severity == "error" {
					t.Fatal(d)
				}
				warn = true
				truncated = truncated || strings.Contains(d.Message, "truncated")
			}
			if !warn || truncated != test.truncated {
				t.Fatalf("diagnostics: %#v", analysis.Diagnostics)
			}
		})
	}
	r := bindingRevision()
	properties := map[string]any{}
	for i := range 2100 {
		properties[fmt.Sprintf("field%04d", i)] = map[string]any{"type": "string"}
	}
	raw, _ := jsonx.Marshal(map[string]any{"type": "object", "properties": properties})
	r.Document.Contracts[0].Document = jsonx.RawMessage(fmt.Sprintf(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":%s}}}}}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile"}}}}`, raw))
	analysis := AnalyzeDataFlow(r.Document)
	if len(analysis.Messages[0].ResponseFields) != 2000 {
		t.Fatalf("field limit: %d", len(analysis.Messages[0].ResponseFields))
	}
	found := false
	for _, d := range analysis.Diagnostics {
		found = found || strings.Contains(d.Message, "truncated")
	}
	if !found {
		t.Fatal("no truncation warning")
	}
}
func TestDataFlowSuccessResponseSelection(t *testing.T) {
	t.Parallel()
	r := bindingRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","required":["id"],"properties":{"id":{"type":"integer"}}}}}},"201":{"content":{"application/json":{"schema":{"type":"object","properties":{"other":{"type":"string"}}}}}},"400":{"content":{"application/json":{"schema":{"type":"string"}}}}}}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile"}}}}`)
	analysis := AnalyzeDataFlow(r.Document)
	for _, field := range analysis.Messages[0].ResponseFields {
		if field.Pointer == "/id" && (field.Required || field.Type != "unknown") {
			t.Fatalf("union field %#v", field)
		}
	}
	status := 200
	r.Document.Messages[0].Execution.ExpectedStatus = &status
	analysis = AnalyzeDataFlow(r.Document)
	for _, field := range analysis.Messages[0].ResponseFields {
		if field.Pointer == "/id" && (!field.Required || field.Type != "integer") {
			t.Fatalf("selected field %#v", field)
		}
	}
}

func TestDataFlowAnalysisBounds(t *testing.T) {
	t.Parallel()
	t.Run("too many messages", func(t *testing.T) {
		r := runRevision()
		r.Document.Messages = make([]Message, maxMessages+1)
		analysis := AnalyzeDataFlow(r.Document)
		if len(analysis.Messages) != 0 || len(analysis.Diagnostics) != 1 || analysis.Diagnostics[0].Severity != "error" {
			t.Fatalf("unbounded input: %#v", analysis)
		}
	})
	t.Run("global field count", func(t *testing.T) {
		r := runRevision()
		properties := map[string]any{}
		for i := range 1900 {
			properties[fmt.Sprintf("field%04d", i)] = map[string]any{"type": "string"}
		}
		raw, _ := jsonx.Marshal(map[string]any{"type": "object", "properties": properties})
		r.Document.Contracts[0].Document = jsonx.RawMessage(fmt.Sprintf(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":%s}}}}}}}}`, raw))
		original := r.Document.Messages[0]
		r.Document.Messages = nil
		for i := range 12 {
			m := original
			m.ID = fmt.Sprintf("m%d", i)
			r.Document.Messages = append(r.Document.Messages, m)
		}
		analysis := AnalyzeDataFlow(r.Document)
		count := 0
		warn := false
		for _, m := range analysis.Messages {
			count += len(m.ResponseFields) + len(m.RequestFields)
		}
		for _, d := range analysis.Diagnostics {
			warn = warn || strings.Contains(d.Message, "truncated")
		}
		if count != 20000 || !warn {
			t.Fatalf("global count %d warnings %#v", count, analysis.Diagnostics)
		}
	})
	t.Run("diagnostic ceiling blocks", func(t *testing.T) {
		r := bindingRevision()
		original := r.Document.Messages[1]
		r.Document.Messages = []Message{r.Document.Messages[0]}
		for i := range 1100 {
			m := original
			m.ID = fmt.Sprintf("m%d", i)
			copy := *m.Execution
			copy.Bindings = []DataBinding{{ID: "id", SourceMessageID: m.ID, SourcePointer: "/x", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
			m.Execution = &copy
			r.Document.Messages = append(r.Document.Messages, m)
		}
		analysis := AnalyzeDataFlow(r.Document)
		if len(analysis.Diagnostics) > 2001 {
			t.Fatalf("too many diagnostics %d", len(analysis.Diagnostics))
		}
		found := false
		for _, d := range analysis.Diagnostics {
			found = found || (d.Severity == "error" && strings.Contains(d.Message, "limit"))
		}
		if !found {
			t.Fatal("limit silently truncated")
		}
	})
	t.Run("large field names block output", func(t *testing.T) {
		r := runRevision()
		parameters := []any{}
		for i := range 100 {
			parameters = append(parameters, map[string]any{"in": "query", "name": fmt.Sprint(i) + strings.Repeat("a", 50000), "schema": map[string]any{"type": "string"}})
		}
		root := map[string]any{"paths": map[string]any{"/login": map[string]any{"post": map[string]any{"x-mocker-canvas-operation-id": "login", "parameters": parameters}}}}
		raw, _ := jsonx.Marshal(root)
		r.Document.Contracts[0].Document = raw
		analysis := AnalyzeDataFlow(r.Document)
		encoded, _ := jsonx.Marshal(analysis)
		if len(encoded) > 4<<20 {
			t.Fatalf("output too large %d", len(encoded))
		}
		found := false
		for _, d := range analysis.Diagnostics {
			found = found || (d.Severity == "error" && strings.Contains(d.Message, "limit"))
		}
		if !found {
			t.Fatal("no output limit error")
		}
	})
}

func TestDataFlowAmbiguousJSONMediaSchema(t *testing.T) {
	t.Parallel()
	r := bindingRevision()
	r.Document.Contracts[0].Document = jsonx.RawMessage(`{"paths":{"/login":{"post":{"x-mocker-canvas-operation-id":"login","responses":{"200":{"content":{"application/json":{"schema":{"type":"object","properties":{"id":{"type":"integer"}}}},"application/vendor+json":{"schema":{"type":"object","properties":{"id":{"type":"string"}}}}}}}}},"/profile":{"get":{"x-mocker-canvas-operation-id":"profile","parameters":[{"name":"id","in":"path","schema":{"type":"integer"}}]}}}}`)
	analysis := AnalyzeDataFlow(r.Document)
	unknown := false
	for _, d := range analysis.Diagnostics {
		if d.Severity == "error" {
			t.Fatal(d)
		}
		unknown = unknown || strings.Contains(d.Message, "тип не удалось определить")
	}
	if !unknown || bindingSchemas(r.Document)[0].responseType("/id") != "unknown" {
		t.Fatal("ambiguous media types reported as compatible")
	}
}

func TestDataBindingLegacyRunIgnoresCatalogBudget(t *testing.T) {
	t.Parallel()
	r := runRevision()
	parameters := []any{}
	for i := range 100 {
		parameters = append(parameters, map[string]any{"in": "query", "name": fmt.Sprint(i) + strings.Repeat("a", 50000), "schema": map[string]any{"type": "string"}})
	}
	root := map[string]any{"paths": map[string]any{"/login": map[string]any{"post": map[string]any{"x-mocker-canvas-operation-id": "login", "parameters": parameters}}, "/profile": map[string]any{"get": map[string]any{"x-mocker-canvas-operation-id": "profile"}}}}
	raw, _ := jsonx.Marshal(root)
	r.Document.Contracts[0].Document = raw
	if _, err := PrepareRun(r, "legacy", "", "mcp", nil); err != nil {
		t.Fatalf("legacy run blocked by catalog: %v", err)
	}
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
	if _, err := PrepareRun(r, "binding", "", "mcp", nil); err == nil || !strings.Contains(err.Error(), "limit") {
		t.Fatalf("binding run must report limit: %v", err)
	}
}
