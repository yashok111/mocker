package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"

	bm "github.com/yashok111/mocker/internal/backendmodel"
	o "github.com/yashok111/mocker/internal/backendobservations"
)

// Review 2026-10-06, F136: a found access edge proves the table is used, so an
// incomplete inventory must not turn it into an "unknown" unused-table finding.
func TestDiagnosticUsedTableWithPartialInventoryIsNotAFinding(t *testing.T) {
	g := supportedGraph([]bm.Node{{ID: "table", Kind: "table"}, {ID: "query", Kind: "query"}}, []bm.Edge{{ID: "access", Kind: "reads", From: "query", To: "table"}})
	g.State.Revision.Coverage.Status = "partial"
	r := diagnosticRun(t, g)
	if got := diagnosticRule(r, "unused_table"); got != nil {
		t.Fatalf("used table reported as possibly unused: %+v", got)
	}
	if slices.Contains(r.Gaps, "unused_table:table") {
		t.Fatalf("used table produced a gap: %v", r.Gaps)
	}
}

// Review 2026-10-06, F137: a certainty filter must select checks and findings by
// their real certainty, not drop every check (and with it every finding).
func TestDiagnosticCertaintyFilterKeepsMatchingFindings(t *testing.T) {
	g := supportedGraph([]bm.Node{{ID: "table", Kind: "table"}}, nil)
	in := engineInput()
	in.Kind = "diagnostics"
	in.Scope.Certainty = "confirmed"
	out, err := diagnosticSnapshot(t.Context(), in, g)
	if err != nil {
		t.Fatal(err)
	}
	findings := readRecords[bm.Finding](t, out.Snapshot, "findings")
	if len(findings) != 1 || findings[0].Rule != "unused_table" {
		t.Fatalf("confirmed finding dropped by a confirmed filter: %+v", findings)
	}
	if len(readRecords[bm.FindingCheck](t, out.Snapshot, "checks")) != 1 {
		t.Fatal("confirmed check dropped by a confirmed filter")
	}
	in.Scope.Certainty = "unknown"
	out, err = diagnosticSnapshot(t.Context(), in, g)
	if err != nil {
		t.Fatal(err)
	}
	if n := len(readRecords[bm.FindingCheck](t, out.Snapshot, "checks")); n != 0 {
		t.Fatalf("confirmed check kept by an unknown filter without its finding: %d", n)
	}
}

func endpointColumnGraphs(beforeAttrs, afterAttrs string) (*bm.EffectiveGraphSnapshot, *bm.EffectiveGraphSnapshot) {
	before, after := endpointReviewFixture()
	set := func(g *bm.EffectiveGraphSnapshot, raw string) {
		for i := range g.State.Nodes {
			if g.State.Nodes[i].ID == "table" {
				g.State.Nodes[i].Attributes = diagnosticAttrs(raw)
			}
		}
		g.Source.State = g.State
	}
	set(before, beforeAttrs)
	set(after, afterAttrs)
	return before, after
}

// Review 2026-10-06, F140 and F146: endpoint review evaluates one rule per
// changed path, like diff/impact, and an unknown rule makes the verdict unknown.
func TestB43EndpointMultiPathChangeKeepsEveryRuleAndUnknownVerdict(t *testing.T) {
	before, after := endpointColumnGraphs(`{"facets":{"sql":{"nativeType":{"status":"known","value":"integer"},"nullable":{"status":"known","value":true}}}}`, `{"facets":{"sql":{"nativeType":{"status":"known","value":"mystery"},"nullable":{"status":"known","value":false}}}}`)
	in := engineInput()
	in.Kind = "endpoint_review"
	out, err := analyzeEndpointReview(t.Context(), in, &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	rules := map[string]string{}
	for _, row := range readRecords[EndpointCheckDetail](t, out.Snapshot, "checks") {
		if row.Origin == "analysis_rule" && row.Check.Object.ID == "table" {
			rules[row.Check.RuleID] = row.Check.Status
		}
	}
	if rules["not-null-data-check"] != "check_required" || rules["native-type-change"] != "unknown" {
		t.Fatalf("per-path rules lost: %v", rules)
	}
	if v := out.Snapshot.Manifest.Verdict; v != "unknown" {
		t.Fatalf("unknown native-type rule left verdict %q", v)
	}
}

// Review 2026-10-06, F145: without an identity map, an object the proposal
// creates cannot be matched to the implementation's new UUID; such an added
// object is an unverified correspondence, not an outside-intent change.
func TestB43EndpointCreatedObjectIsUnverifiedNotOutsideIntent(t *testing.T) {
	before, _ := endpointReviewFixture()
	nodes := append(slices.Clone(before.State.Nodes), bm.Node{ID: "new-column", Kind: "column"})
	edges := append(slices.Clone(before.State.Edges), bm.Edge{ID: "new-write", Kind: "writes", From: "step", To: "new-column"})
	after := supportedGraph(nodes, edges)
	draftNodes := append(slices.Clone(before.State.Nodes), bm.Node{ID: "draft-column", Kind: "column"})
	draftEdges := append(slices.Clone(before.State.Edges), bm.Edge{ID: "draft-write", Kind: "writes", From: "step", To: "draft-column"})
	intent := supportedGraph(draftNodes, draftEdges)
	intentBase, _ := endpointReviewFixture()
	in := engineInput()
	in.Kind = "endpoint_review"
	out, err := analyzeEndpointReview(t.Context(), in, &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}, before, after, intentBase, intent)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range readRecords[OutsideIntentChangeDetail](t, out.Snapshot, "changes") {
		if row.Type == "outside_intent_change" {
			t.Fatalf("intended creation reported outside intent: %+v", row.Change.Object)
		}
	}
	if !slices.ContainsFunc(out.Snapshot.Manifest.Gaps, func(g Diagnostic) bool { return g.Code == "unverified_created_correspondence" }) {
		t.Fatalf("created correspondence gap missing: %+v", out.Snapshot.Manifest.Gaps)
	}
}

// Review 2026-10-06, F139: a criterion addressing an object outside the
// reviewed endpoint is not an endpoint check; only a criterion with neither an
// object nor targets is global.
func TestB43EndpointUnrelatedCriterionIsNotAnEndpointCheck(t *testing.T) {
	before, after := endpointReviewFixture()
	intentBase, _ := endpointReviewFixture()
	intent, _ := endpointReviewFixture()
	intent.Criteria = []bm.ChangeCriterion{
		{Key: "unrelated", Kind: "object_exists", RecordType: "node", ID: "unrelated", ObjectKind: "handler", Required: true, Description: "Unrelated"},
		{Key: "related", Kind: "object_exists", RecordType: "node", ID: "table", ObjectKind: "table", Required: true, Description: "Related"},
		{Key: "global", Kind: "runtime_check", Required: true, Description: "Global"},
	}
	in := engineInput()
	in.Kind = "endpoint_review"
	out, err := analyzeEndpointReview(t.Context(), in, &EndpointReviewPayload{BeforeEndpointID: "endpoint", AfterEndpointID: new("endpoint")}, before, after, intentBase, intent)
	if err != nil {
		t.Fatal(err)
	}
	keys := []string{}
	for _, row := range readRecords[EndpointCheckDetail](t, out.Snapshot, "checks") {
		if row.Origin == "criterion" {
			keys = append(keys, *row.CriterionKey)
		}
	}
	slices.Sort(keys)
	if !slices.Equal(keys, []string{"global", "related"}) {
		t.Fatalf("endpoint criteria %v", keys)
	}
}

// Review 2026-10-06, F141: an intended removal does not cover a modification
// of the same object.
func TestConformanceRemovalIntentDoesNotCoverModification(t *testing.T) {
	base := supportedGraph([]bm.Node{{ID: "x", Kind: "table", Name: "orders"}}, nil)
	draft := supportedGraph(nil, nil)
	result := supportedGraph([]bm.Node{{ID: "x", Kind: "table", Name: "renamed"}}, nil)
	outside, err := outsideIntentChanges(t.Context(), base, draft, result, map[string]string{"x": "x"})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(outside, func(c DiffChange) bool { return c.Object.ID == "x" && c.Operation == "modified" }) {
		t.Fatalf("modification hidden by removal intent: %+v", outside)
	}
}

// Review 2026-10-06, F148: a created proposal edge never corresponds to a
// parallel edge that already existed in the base.
func TestConformanceCreatedEdgeIgnoresParallelBaseEdge(t *testing.T) {
	nodes := []bm.Node{{ID: "a", Kind: "query"}, {ID: "b", Kind: "table"}}
	base := supportedGraph(slices.Clone(nodes), []bm.Edge{{ID: "e1", Kind: "reads", From: "a", To: "b"}})
	draft := supportedGraph(slices.Clone(nodes), []bm.Edge{{ID: "e1", Kind: "reads", From: "a", To: "b"}, {ID: "e2", Kind: "reads", From: "a", To: "b"}})
	result := supportedGraph(slices.Clone(nodes), []bm.Edge{{ID: "e1", Kind: "reads", From: "a", To: "b"}})
	mapping := map[string]string{"a": "a", "b": "b"}
	addCreatedEdgeCorrespondence(base, draft, result, mapping)
	if mapping["e2"] != "" {
		t.Fatalf("created edge bound to retained base edge %q", mapping["e2"])
	}
	result = supportedGraph(slices.Clone(nodes), []bm.Edge{{ID: "e1", Kind: "reads", From: "a", To: "b"}, {ID: "e3", Kind: "reads", From: "a", To: "b"}})
	addCreatedEdgeCorrespondence(base, draft, result, mapping)
	if mapping["e2"] != "e3" {
		t.Fatalf("new parallel edge not matched: %q", mapping["e2"])
	}
}

// Review 2026-10-06, F143: relaxing uniqueness or editing an unrelated
// constraint field cannot introduce duplicates.
func TestAnalysisRuleUniqueCheckOnlyWhenTightened(t *testing.T) {
	for _, tc := range []struct {
		name, kind, operation, path, after string
		want                               bool
	}{
		{"relaxed", "index", "modified", "/attributes/facets/sql/unique/value", `{"attributes":{"facets":{"sql":{"unique":{"status":"known","value":false}}}}}`, false},
		{"tightened", "index", "modified", "/attributes/facets/sql/unique/value", `{"attributes":{"facets":{"sql":{"unique":{"status":"known","value":true}}}}}`, true},
		{"description", "constraint", "modified", "/attributes/description", `{"attributes":{"description":"x","facets":{"sql":{"constraintKind":"unique"}}}}`, false},
		{"columns", "constraint", "modified", "/attributes/facets/sql/columnIds", `{"attributes":{"facets":{"sql":{"constraintKind":"unique"}}}}`, true},
		{"added", "constraint", "added", "", `{"attributes":{"facets":{"sql":{"constraintKind":"unique"}}}}`, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := ruleFor(DiffChange{Facet: "behavior", Kind: tc.kind, Operation: tc.operation, Paths: []string{tc.path}, After: jsontext.Value(tc.after)})
			if got := r.RuleID == "unique-data-check"; got != tc.want {
				t.Fatalf("unique-data-check=%v want %v (%+v)", got, tc.want, r)
			}
		})
	}
}

// Review 2026-10-06, F151: upstream traversal keeps a changed edge's seed
// transitions to its endpoints and never reports an edge address as an
// affected object reached from a node.
func TestAnalysisImpactUpstreamKeepsEdgeSeeds(t *testing.T) {
	nodes := []bm.Node{{ID: "query", Kind: "query"}, {ID: "column", Kind: "column"}, {ID: "step", Kind: "flow_step"}}
	g := supportedGraph(nodes, []bm.Edge{{ID: "access", Kind: "reads", From: "query", To: "column"}, {ID: "calls", Kind: "calls", From: "step", To: "query"}})
	for _, direction := range []string{"upstream", "both"} {
		t.Run(direction, func(t *testing.T) {
			in := engineInput()
			in.Scope.Direction = direction
			r := newReport(in)
			if err := r.traverse(t.Context(), g, g, []ObjectAddress{{RecordType: "edge", ID: "access"}, {RecordType: "node", ID: "column"}}); err != nil {
				t.Fatal(err)
			}
			terminal, err := r.finish(g, g, nil)
			if err != nil {
				t.Fatal(err)
			}
			edgeSeed := false
			for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
				if w.Affected.RecordType == "edge" {
					t.Fatalf("edge address reported as affected object: %+v", w)
				}
				edgeSeed = edgeSeed || w.Seed.ID == "access"
			}
			if !edgeSeed {
				t.Fatal("changed edge seed produced no witness")
			}
		})
	}
}

func observedImpactInput(t *testing.T, ref bm.DiagramRef) *ImmutableInput {
	t.Helper()
	data := metricData()
	data.Sets[0].Version.Context.Source.ServiceID = "orders-id"
	data.Sets[0].Correlation.Rows = []o.CorrelationRow{{RecordID: "root", Outcome: "explicit", Method: "instrumentation_id", Selected: &ref}}
	in := engineInput()
	in.ImpactObservations = &data
	return in
}

// Review 2026-10-06, F154: observed evidence is filtered by the object's real
// kind and by service id or name; a filtered row leaves a scope gap.
func TestObservedImpactScopeUsesRealKindServiceNameAndGap(t *testing.T) {
	g := supportedGraph([]bm.Node{{ID: "orders-id", Kind: "service", Name: "Orders"}, {ID: "col", Kind: "column", ParentID: new("orders-id")}}, nil)
	run := func(scope Scope) *reportBuilder {
		in := observedImpactInput(t, bm.DiagramRef{Kind: "record", RecordType: "node", ID: "col"})
		in.Scope = normalizedScope(scope)
		r := newReport(in)
		r.before, r.after = g, g
		if err := r.addObservedImpact(t.Context()); err != nil {
			t.Fatal(err)
		}
		return r
	}
	for _, scope := range []Scope{{Kind: "column"}, {Service: "Orders"}, {Service: "orders-id"}} {
		if r := run(scope); len(r.records["witnesses"]) != 1 {
			t.Fatalf("scope %+v dropped matching observed evidence", scope)
		}
	}
	r := run(Scope{Kind: "table"})
	if len(r.records["witnesses"]) != 0 || r.gaps["scope_omitted_observation"].Code == "" {
		t.Fatalf("filtered observation left no gap: %+v", r.gaps)
	}
}

// Review 2026-10-06, F188: an artifact ref is never addressed as the empty
// object {recordType:"", id:""}.
func TestObservedImpactArtifactRefHasAddress(t *testing.T) {
	g := supportedGraph(nil, nil)
	ref := bm.DiagramRef{Kind: "artifact", RowID: "row-1", Locator: &bm.ArtifactProjectionLocator{}}
	r := newReport(observedImpactInput(t, ref))
	r.before, r.after = g, g
	if err := r.addObservedImpact(t.Context()); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(g, g, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := 0
	for _, c := range terminal.Snapshot.Chunks {
		if c.Section != "witnesses" {
			continue
		}
		var records []ResultRecord
		if err := json.Unmarshal(c.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, rec := range records {
			found++
			if rec.Object.RecordType != "artifact_object" || len(rec.Object.ID) != 64 {
				t.Fatalf("artifact witness address %+v", rec.Object)
			}
		}
	}
	if found != 1 {
		t.Fatalf("artifact witnesses %d", found)
	}
}
