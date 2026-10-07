package designscenario

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
)

func TestDataBindingUsesSourceOccurrence(t *testing.T) {
	t.Parallel()
	r := runRevision()
	r.Document.Messages[1].Execution.PathParams["id"] = "{{missing}}"
	r.Document.Messages[1].Execution.Bindings = []DataBinding{{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "path", Name: "id"}}}
	initial := prepareTestRun(t, r)
	report := Run(t.Context(), r, initial, func(_ context.Context, request StepRequest) (StepResponse, error) {
		if request.MessageID == "profile" && request.PathParams["id"] != "42" {
			t.Fatalf("request: %#v", request)
		}
		return runResponse(`{"id":42}`), nil
	}, nil)
	if report.Status != "passed" {
		t.Fatal(report.Reason)
	}
	result := report.Steps[1].BindingResults[0]
	if result.SourceOccurrence != 1 || result.ValueJSON != "42" {
		t.Fatalf("result: %#v", result)
	}
}

func TestDataBindingStrictJSON(t *testing.T) {
	t.Parallel()
	valid := `{"id":"id","sourceMessageId":"login","sourcePointer":"","target":{"kind":"body","pointer":""}}`
	var binding DataBinding
	if err := jsonx.Unmarshal([]byte(valid), &binding); err != nil {
		t.Fatal(err)
	}
	encoded, err := jsonx.Marshal(binding)
	if err != nil || !strings.Contains(string(encoded), `"pointer":""`) {
		t.Fatalf("encoded %s, %v", encoded, err)
	}
	for _, test := range []struct{ name, raw string }{
		{"missing body pointer", strings.Replace(valid, `,"pointer":""`, "", 1)},
		{"irrelevant name", strings.Replace(valid, `"pointer":""`, `"pointer":"","name":""`, 1)},
		{"null prefix", strings.TrimSuffix(valid, "}") + `,"prefix":null}`},
		{"unknown field", strings.TrimSuffix(valid, "}") + `,"unknown":true}`},
		{"null source", strings.Replace(valid, `"sourcePointer":""`, `"sourcePointer":null`, 1)},
		{"null binding", "null"},
	} {
		t.Run(test.name, func(t *testing.T) {
			var got DataBinding
			if err := jsonx.Unmarshal([]byte(test.raw), &got); err == nil {
				t.Fatalf("accepted %s", test.raw)
			}
		})
	}
	r := runRevision()
	data, _ := jsonx.Marshal(r.Document.Messages[0].Execution)
	data = []byte(strings.TrimSuffix(string(data), "}") + `,"bindings":null}`)
	var config StepExecution
	if err := jsonx.Unmarshal(data, &config); err == nil {
		t.Fatal("accepted null bindings")
	}
}

func TestDataBindingTransformStrictJSON(t *testing.T) {
	const base = `{"id":"id","sourceMessageId":"login","sourcePointer":"/id","target":{"kind":"body","pointer":""}`
	var binding DataBinding
	if err := jsonx.Unmarshal([]byte(base+`,"transforms":[{"kind":"trim"},{"kind":"to_integer"}]}`), &binding); err != nil {
		t.Fatalf("valid chain: %v", err)
	}
	encoded, err := jsonx.Marshal(binding)
	if err != nil || !strings.Contains(string(encoded), `"transforms":[{"kind":"trim"},{"kind":"to_integer"}]`) {
		t.Fatalf("round trip = %s, %v", encoded, err)
	}
	for _, tc := range []struct{ name, transforms string }{
		{"null array", `null`},
		{"null entry", `[null]`},
		{"missing kind", `[{}]`},
		{"unknown kind", `[{"kind":"eval"}]`},
		{"unknown field", `[{"kind":"trim","expression":"x"}]`},
		{"nine entries", `[{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"},{"kind":"trim"}]`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if err := jsonx.Unmarshal([]byte(base+`,"transforms":`+tc.transforms+`}`), &binding); err == nil {
				t.Fatalf("accepted transforms %s", tc.transforms)
			}
		})
	}
}

func TestDataBindingStructuralValidation(t *testing.T) {
	t.Parallel()
	base := DataBinding{ID: "id", SourceMessageID: "login", SourcePointer: "/id", Target: DataBindingTarget{Kind: "header", Name: "Authorization"}}
	for _, test := range []struct {
		name     string
		bindings []DataBinding
	}{
		{"duplicate IDs", []DataBinding{base, base}},
		{"header case", []DataBinding{base, {ID: "other", SourceMessageID: "login", Target: DataBindingTarget{Kind: "header", Name: "authorization"}}}},
		{"overlapping body", []DataBinding{{ID: "one", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body", Pointer: "/a"}}, {ID: "two", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body", Pointer: "/a/b"}}}},
		{"whole body overlap", []DataBinding{{ID: "one", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body"}}, {ID: "two", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body", Pointer: "/a"}}}},
		{"invalid pointer", []DataBinding{{ID: "one", SourceMessageID: "login", SourcePointer: "/~2", Target: base.Target}}},
		{"body prefix", []DataBinding{{ID: "one", SourceMessageID: "login", Target: DataBindingTarget{Kind: "body"}, Prefix: "x"}}},
		{"unknown transform", []DataBinding{{ID: "one", SourceMessageID: "login", Target: base.Target, Transforms: []DataBindingTransform{{Kind: "eval"}}}}},
		{"too many transforms", []DataBinding{{ID: "one", SourceMessageID: "login", Target: base.Target, Transforms: make([]DataBindingTransform, MaxBindingTransforms+1)}}},
		{"too many", make([]DataBinding, 101)},
	} {
		t.Run(test.name, func(t *testing.T) {
			r := runRevision()
			r.Document.Messages[1].Execution.Bindings = test.bindings
			if len(executionDiagnostics(r.Document)) == 0 {
				t.Fatal("invalid binding accepted")
			}
		})
	}
}

func TestDataBindingCommandsPreserveAndRollback(t *testing.T) {
	repo := newTestRepo(t)
	doc := validDocument("bindings")
	doc.Participants = []Participant{{ID: "api", Kind: "service"}}
	config := runRevision().Document.Messages[1].Execution
	config.Enabled = false
	config.Body = `{"literal":true}`
	doc.Messages = []Message{{ID: "target", FromID: "api", ToID: "api", Kind: "request", Execution: config}}
	created, err := repo.Create(t.Context(), CreateInput{Document: doc, Source: "mcp"})
	if err != nil {
		t.Fatal(err)
	}
	binding := DataBinding{ID: "id", SourceMessageID: "removed", SourcePointer: "/id", Target: DataBindingTarget{Kind: "body", Pointer: "/literal"}, Transforms: []DataBindingTransform{{Kind: "trim"}}}
	command := Command{Type: "upsert_data_binding", MessageID: "target", Binding: &binding}
	applied, err := repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 1, Source: "mcp", Commands: []Command{command}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Diagnostics) == 0 || applied.Draft.Document.Messages[0].Execution.Enabled || applied.Draft.Document.Messages[0].Execution.Body != config.Body || applied.Draft.Document.Messages[0].Execution.Bindings[0].Transforms[0].Kind != "trim" {
		t.Fatalf("lost settings or warnings: %#v", applied)
	}
	binding.SourcePointer = "/new"
	binding.Transforms = []DataBindingTransform{{Kind: "trim"}, {Kind: "to_integer"}}
	applied, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 2, Source: "mcp", Commands: []Command{command}})
	if err != nil {
		t.Fatal(err)
	}
	if len(applied.Draft.Document.Messages[0].Execution.Bindings) != 1 || applied.Draft.Document.Messages[0].Execution.Bindings[0].SourcePointer != "/new" || len(applied.Draft.Document.Messages[0].Execution.Bindings[0].Transforms) != 2 {
		t.Fatal("upsert did not retain identity")
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 3, Source: "mcp", Commands: []Command{{Type: "remove_data_binding", MessageID: "target", ID: "id"}, {Type: "remove_data_binding", MessageID: "target", ID: "missing"}}})
	if err == nil {
		t.Fatal("expected batch failure")
	}
	detail, err := repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil {
		t.Fatal(err)
	}
	if detail.Scenario.Version != 3 || len(detail.Revisions) != 3 || len(detail.Draft.Document.Messages[0].Execution.Bindings) != 1 || detail.Draft.Document.Messages[0].Execution.Bindings[0].Transforms[1].Kind != "to_integer" {
		t.Fatal("failed batch changed draft")
	}
	invalid := binding
	invalid.ID = "broken"
	invalid.Transforms = make([]DataBindingTransform, MaxBindingTransforms+1)
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 3, Source: "mcp", Commands: []Command{
		{Type: "upsert_data_binding", MessageID: "target", Binding: &DataBinding{ID: "another", SourceMessageID: "removed", SourcePointer: "/id", Target: DataBindingTarget{Kind: "header", Name: "X-ID"}, Transforms: []DataBindingTransform{{Kind: "to_string"}}}},
		{Type: "upsert_data_binding", MessageID: "target", Binding: &invalid},
	}})
	if err == nil {
		t.Fatal("accepted invalid transform chain in atomic batch")
	}
	detail, err = repo.Detail(t.Context(), created.Scenario.ID)
	if err != nil || detail.Scenario.Version != 3 || len(detail.Draft.Document.Messages[0].Execution.Bindings) != 1 || detail.Draft.Document.Messages[0].Execution.Bindings[0].Transforms[1].Kind != "to_integer" {
		t.Fatalf("invalid transform batch changed draft: %+v, %v", detail, err)
	}
	_, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 2, Source: "mcp", Commands: []Command{command}})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("stale: %v", err)
	}
	detail, err = repo.Apply(t.Context(), created.Scenario.ID, CommandsInput{ExpectedVersion: 3, Source: "mcp", Commands: []Command{{Type: "remove_data_binding", MessageID: "target", ID: "id"}}})
	if err != nil {
		t.Fatal(err)
	}
	if len(detail.Draft.Document.Messages[0].Execution.Bindings) != 0 {
		t.Fatal("remove failed")
	}
}
