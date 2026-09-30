package apidesign

import (
	"errors"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/responserules"
)

func TestResponseRuleExamplesPersistWithVersionedAuthoring(t *testing.T) {
	t.Parallel()
	r, db := testRepo(t)
	requestText := " {\"n\":9007199254740993,\"amount\":1e10000} \n"
	example, err := jsonx.Marshal(map[string]any{"id": "one", "name": "Saved case", "request": responserules.Request{Query: []responserules.Field{}, Headers: []responserules.Field{}, BodyJSON: new(requestText)}})
	if err != nil {
		t.Fatal(err)
	}
	ruleText := strings.TrimSuffix(executionTestRule, "}") + `,"examples":[` + string(example) + `]}`
	document := strings.TrimSuffix(testDocument, "}") + `,"x-mocker-response-rules":{"formatVersion":1,"rules":[` + ruleText + `]}}`
	d, err := r.Create(t.Context(), CreateInput{Name: "Saved cases import", Document: document, Source: "ui"})
	if err != nil {
		t.Fatalf("authoring import rejected saved examples: %v", err)
	}
	list, err := r.ResponseRules(t.Context(), d.Design.ID)
	if err != nil || len(list.Rules) != 1 {
		t.Fatalf("saved cases unavailable: %+v %v", list, err)
	}
	encoded, _ := jsonx.Marshal(list.Rules[0])
	if !strings.Contains(string(encoded), string(example)) || !strings.Contains(d.Draft.Document, "9007199254740993") {
		t.Fatalf("case text changed: %s", encoded)
	}
	applied, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, 1, "ui", "r", true)
	if err != nil {
		t.Fatal(err)
	}
	root, err := decodeDocument(applied.Draft.Document)
	if err != nil {
		t.Fatal(err)
	}
	executionRaw, _ := jsonx.Marshal(root[responserules.ExecutionExtension])
	if strings.Contains(string(executionRaw), "examples") || strings.Contains(string(executionRaw), "9007199254740993") {
		t.Fatalf("authoring fixtures copied into execution: %s", executionRaw)
	}
	status, err := r.ResponseRuleExecution(t.Context(), d.Design.ID)
	if err != nil || len(status.Rules) != 1 || status.Rules[0].State != "current" {
		t.Fatalf("applied source with examples not current: %+v %v", status, err)
	}
	var commands []responserules.Command
	if err := jsonx.Unmarshal([]byte(`[{"type":"update_example","example":{"id":"one","name":"Renamed","request":{"query":[],"headers":[],"bodyJSON":"1e10000"}}}]`), &commands); err != nil {
		t.Fatal(err)
	}
	changed, err := r.EditResponseRule(t.Context(), d.Design.ID, applied.Design.Version, "mcp", "r", "commands", nil, commands)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.EditResponseRule(t.Context(), d.Design.ID, applied.Design.Version, "mcp", "r", "commands", nil, commands); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale case edit bypassed CAS: %v", err)
	}
	status, err = r.ResponseRuleExecution(t.Context(), d.Design.ID)
	if err != nil || status.Rules[0].State != "current" {
		t.Fatalf("example-only edit requires reapply: %+v %v", status, err)
	}
	unchanged, err := r.EditResponseRuleExecution(t.Context(), d.Design.ID, changed.Design.Version, "ui", "r", true)
	if err != nil || unchanged.Design.Version != changed.Design.Version || unchanged.Draft.ID != changed.Draft.ID {
		t.Fatalf("example-only reapply created revision: %+v %v", unchanged, err)
	}
	var runtimeEntities int
	if err := db.R.QueryRowContext(t.Context(), "SELECT count(*) FROM entities").Scan(&runtimeEntities); err != nil || runtimeEntities != 0 {
		t.Fatalf("saved fixtures touched runtime data: %d %v", runtimeEntities, err)
	}
	restored, err := r.Restore(t.Context(), d.Design.ID, d.Draft.ID, changed.Design.Version, "restore examples", "ui")
	if err != nil {
		t.Fatal(err)
	}
	list, err = r.ResponseRules(t.Context(), d.Design.ID)
	if err != nil || list.RevisionID != restored.Draft.ID || len(list.Rules) != 1 {
		t.Fatalf("restored examples unavailable: %+v %v", list, err)
	}
	encoded, _ = jsonx.Marshal(list.Rules[0])
	if !strings.Contains(string(encoded), string(example)) {
		t.Fatalf("restore changed exact fixture: %s", encoded)
	}
}
