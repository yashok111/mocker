package backendanalysis

import (
	"encoding/json/jsontext"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func TestAnalysisRuleHonestCompatibility(t *testing.T) {
	for _, tc := range []struct{ name, kind, operation, path, after, rule, status string }{
		{"native", "column", "modified", "/attributes/facets/sql/nativeType/value", `{"attributes":{"facets":{"sql":{"nativeType":{"status":"known","value":"mystery"}}}}}`, "native-type-change", "unknown"},
		{"notnull", "column", "modified", "/attributes/facets/sql/nullable/value", `{"attributes":{"facets":{"sql":{"nullable":{"status":"known","value":false}}}}}`, "not-null-data-check", "check_required"},
		{"unique", "constraint", "added", "", `{"attributes":{"facets":{"sql":{"constraintKind":"unique"}}}}`, "unique-data-check", "check_required"},
		{"index", "index", "removed", "", `null`, "index-removal-performance-check", "check_required"},
		{"fk", "foreign_key", "modified", "/attributes/facets/sql/columnPairs", `{}`, "foreign-key-change", "potential"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ruleFor(DiffChange{Facet: "behavior", Kind: tc.kind, Operation: tc.operation, Paths: []string{tc.path}, After: jsontext.Value(tc.after)})
			if r.RuleID != tc.rule || r.Status != tc.status || len(r.Prerequisites) < 2 || r.Version != "1" {
				t.Fatalf("%+v", r)
			}
			if r.Status == "violation" {
				t.Fatal("invented runtime failure")
			}
		})
	}
}

func TestAnalysisRuleMultipleIndependentChecks(t *testing.T) {
	before := graphNode(`{"sql":{"nativeType":{"status":"known","value":"integer"},"nullable":{"status":"known","value":true}}}`)
	after := graphNode(`{"sql":{"nativeType":{"status":"known","value":"mystery"},"nullable":{"status":"known","value":false}}}`)
	changes, err := structuralChanges(t.Context(), &before, &after)
	if err != nil {
		t.Fatal(err)
	}
	r := newReport(engineInput())
	if _, err = r.addChanges(t.Context(), changes, &before, &after); err != nil {
		t.Fatal(err)
	}
	if len(r.records["checks"]) != 1 {
		t.Fatal("native-type uncertainty suppressed independent NOT NULL check")
	}
}

func TestAnalysisRuleDeclaredFieldRequirement(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "handler", Name: "new"}}, nil)
	g.Criteria = []backendmodel.ChangeCriterion{{Key: "required-name", Kind: "field_equals", Required: true, Description: "Exact consumer contract", RecordType: "node", ID: revisionID, Selector: jsontext.Value(`{"kind":"source","source":{"kind":"name"}}`), Expected: &backendmodel.SourcePropertyValue{Present: true, Value: []byte(`"old"`)}}}
	checks := declaredChecks(g)
	if len(checks) != 1 || checks[0].Status != "violation" || len(checks[0].Evidence) == 0 {
		t.Fatalf("declared supported requirement: %+v", checks)
	}
}

func TestAnalysisRulePartialCoverageCannotProveAbsence(t *testing.T) {
	g := supportedGraph(nil, nil)
	g.State.Revision.Coverage.Status = "partial"
	g.Criteria = []backendmodel.ChangeCriterion{{Key: "required", Kind: "object_exists", Required: true, RecordType: "node", ID: revisionID, ObjectKind: "column", Description: "Required column"}}
	checks := declaredChecks(g)
	if checks[0].Status != "unknown" {
		t.Fatalf("partial source asserted absence: %+v", checks[0])
	}
}
