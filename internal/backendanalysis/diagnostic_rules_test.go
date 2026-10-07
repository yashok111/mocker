package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func diagnosticAttrs(raw string) map[string]jsontext.Value {
	var a map[string]jsontext.Value
	_ = json.Unmarshal([]byte(raw), &a)
	return a
}
func diagnosticRun(t *testing.T, g *backendmodel.EffectiveGraphSnapshot) *DiagnosticReport {
	t.Helper()
	r, e := EvaluateDiagnostics(t.Context(), g, DiagnosticInput{})
	if e != nil {
		t.Fatal(e)
	}
	return r
}
func diagnosticRule(r *DiagnosticReport, rule string) *backendmodel.Finding {
	for _, f := range r.Findings {
		if f.Rule == rule {
			return &f
		}
	}
	return nil
}
func TestDiagnosticUnusedInventoryAndStableIdentity(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: "table", Kind: "table"}}, nil)
	first := diagnosticRule(diagnosticRun(t, g), "unused_table")
	if first == nil || first.Certainty != "confirmed" {
		t.Fatal(first)
	}
	g.State.Nodes[0].Name = "renamed"
	next := diagnosticRule(diagnosticRun(t, g), "unused_table")
	if first.Fingerprint != next.Fingerprint || first.BasisHash != next.BasisHash {
		t.Fatal("label changed identity/basis")
	}
	g.State.Revision.Coverage.Status = "partial"
	if got := diagnosticRule(diagnosticRun(t, g), "unused_table"); got == nil || got.Certainty != "unknown" {
		t.Fatal(got)
	}
	g = supportedGraph([]backendmodel.Node{{ID: "table", Kind: "table"}, {ID: "query", Kind: "query"}}, []backendmodel.Edge{{ID: "access", Kind: "reads", From: "query", To: "table"}})
	if got := diagnosticRule(diagnosticRun(t, g), "unused_table"); got != nil {
		t.Fatal("positive use declared unused", got)
	}
}
func TestDiagnosticTransactionRequiresCriterion(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: "write", Kind: "flow_step", Attributes: diagnosticAttrs(`{"stepKind":"query","transactionContext":{"status":"none","reason":"single atomic write"}}`)}}, nil)
	if got := diagnosticRule(diagnosticRun(t, g), "transaction_expectation"); got != nil {
		t.Fatal("invented transaction expectation")
	}
}
func TestDiagnosticLoopAndCounterexample(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "loop", Kind: "flow_step", ParentID: new("flow"), Attributes: diagnosticAttrs(`{"stepKind":"loop"}`)}, {ID: "query", Kind: "flow_step", ParentID: new("flow"), Attributes: diagnosticAttrs(`{"stepKind":"query"}`)}}
	edges := []backendmodel.Edge{{ID: "forward", Kind: "next", From: "loop", To: "query"}, {ID: "back", Kind: "next", From: "query", To: "loop"}}
	r := diagnosticRun(t, supportedGraph(nodes, edges))
	f := diagnosticRule(r, "possible_n_plus_one")
	if f == nil || f.Certainty != "possible" || !slices.Contains(f.Gaps, "batching_unknown") {
		t.Fatal(f)
	}
	if diagnosticRule(diagnosticRun(t, supportedGraph(nodes, edges[:1])), "possible_n_plus_one") != nil {
		t.Fatal("query after loop treated as loop body")
	}
}
func TestDiagnosticEmitCurrentAndStale(t *testing.T) {
	a := `{"transactionContext":{"status":"known","transactionId":"tx"},"stepKind":"call"}`
	b := `{"transactionContext":{"status":"known","transactionId":"tx"},"stepKind":"transaction_commit"}`
	g := supportedGraph([]backendmodel.Node{{ID: "emit", Kind: "flow_step", ParentID: new("flow"), Attributes: diagnosticAttrs(a)}, {ID: "commit", Kind: "flow_step", ParentID: new("flow"), Attributes: diagnosticAttrs(b)}, {ID: "tx", Kind: "transaction"}}, []backendmodel.Edge{{ID: "control", Kind: "next", From: "emit", To: "commit"}, {ID: "publish", Kind: "emits", From: "emit", To: "message"}})
	f := diagnosticRule(diagnosticRun(t, g), "possible_emit_before_commit")
	if f == nil || f.Certainty != "possible" {
		t.Fatal(f)
	}
	g.State.Edges[0].Freshness.Status = "stale"
	if diagnosticRule(diagnosticRun(t, g), "possible_emit_before_commit") != nil {
		t.Fatal("stale control proof accepted")
	}
}
func TestDiagnosticUnhandledNeedsCompleteExits(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "raise", Kind: "flow_step", ParentID: new("flow"), Attributes: diagnosticAttrs(`{"stepKind":"raise"}`)}, {ID: "flow", Kind: "flow", Attributes: diagnosticAttrs(`{"exitStatus":"complete","exitStepIds":[],"analysisStatus":"complete"}`)}}
	g := supportedGraph(nodes, nil)
	f := diagnosticRule(diagnosticRun(t, g), "unhandled_outcome")
	if f == nil || f.Certainty != "confirmed" {
		t.Fatal(f)
	}
	g.State.Nodes[1].Attributes["exitStatus"] = []byte(`"partial"`)
	f = diagnosticRule(diagnosticRun(t, g), "unhandled_outcome")
	if f == nil || f.Certainty != "unknown" {
		t.Fatal(f)
	}
	g.State.Nodes[1].Attributes["exitStatus"] = []byte(`"complete"`)
	g.State.Nodes[1].Attributes["exitStepIds"] = []byte(`["raise"]`)
	if diagnosticRule(diagnosticRun(t, g), "unhandled_outcome") != nil {
		t.Fatal("declared exit is handled")
	}
}
func TestDiagnosticAmbiguousDispatch(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: "call", Kind: "call_site", Attributes: diagnosticAttrs(`{"dispatchStatus":"partial","dispatchReason":"dynamic receiver"}`)}}, nil)
	if diagnosticRule(diagnosticRun(t, g), "ambiguous_dispatch") == nil {
		t.Fatal("missing remainder")
	}
	g = supportedGraph([]backendmodel.Node{{ID: "call", Kind: "flow_step", Attributes: diagnosticAttrs(`{"dispatchStatus":"complete"}`)}}, nil)
	if diagnosticRule(diagnosticRun(t, g), "ambiguous_dispatch") != nil {
		t.Fatal("complete dispatch finding")
	}
}
func TestDiagramDiagnosticTargetMismatch(t *testing.T) {
	g := supportedGraph(nil, nil)
	_, err := EvaluateDiagnostics(t.Context(), g, DiagnosticInput{DiagramScope: &backendmodel.DiagramScope{TargetHash: "other"}, Diagram: &backendmodel.DiagramVersion{}})
	requireStatus(t, err, 422)
}

func TestDiagnosticRelationalKnownDriftAndCounterexample(t *testing.T) {
	attrs := diagnosticAttrs(`{"facets":{"sql":{"sourceKind":"sql","dialect":"postgresql","analysisStatus":"complete","gaps":[],"evidenceIds":["11111111-1111-4111-8111-111111111111"],"qualifiedName":"public.orders","nativeDefinition":"","columnsStatus":"complete","constraintsStatus":"complete","freshness":{"status":"current","confirmedSnapshotId":"11111111-1111-4111-8111-111111111111","reasons":[]},"sourceSnapshotId":"11111111-1111-4111-8111-111111111111"},"orm":{"sourceKind":"orm","dialect":"postgresql","analysisStatus":"complete","gaps":[],"evidenceIds":["11111111-1111-4111-8111-111111111111"],"qualifiedName":"public.other_orders","nativeDefinition":"","columnsStatus":"complete","constraintsStatus":"complete","freshness":{"status":"current","confirmedSnapshotId":"11111111-1111-4111-8111-111111111111","reasons":[]},"sourceSnapshotId":"11111111-1111-4111-8111-111111111111"}}}`)
	if _, err := backendmodel.CompareRelationalFacets("table", attrs, false); err != nil {
		t.Fatal(err)
	}
	makeGraph := func() *backendmodel.EffectiveGraphSnapshot {
		g := supportedGraph([]backendmodel.Node{{ID: "table", Kind: "table", Attributes: attrs}}, nil)
		g.State.Nodes[0].EvidenceIDs = []string{revisionID}
		g.State.Evidence = []backendmodel.Evidence{{ID: revisionID, SubjectID: "table", Status: "explicit"}}
		g.Source.State = g.State
		return g
	}
	g := makeGraph()
	f := diagnosticRule(diagnosticRun(t, g), "relational_drift")
	if f == nil || f.Certainty != "confirmed" {
		t.Fatal(f)
	}
	raw := strings.ReplaceAll(string(attrs["facets"]), "public.other_orders", "public.orders")
	attrs["facets"] = []byte(raw)
	g = makeGraph()
	if diagnosticRule(diagnosticRun(t, g), "relational_drift") != nil {
		t.Fatal("equal known facets drifted")
	}
}
func TestDiagnosticExplicitTransactionCriterion(t *testing.T) {
	g := supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "flow_step", Attributes: diagnosticAttrs(`{"transactionContext":{"status":"none","reason":"standalone"}}`)}}, nil)
	g.Criteria = []backendmodel.ChangeCriterion{{Key: "transaction", Kind: "field_equals", Required: true, Description: "Must be local", RecordType: "node", ID: revisionID, Selector: []byte(`{"kind":"source","source":{"kind":"attributes","group":"transactionContext"}}`), Expected: &backendmodel.SourcePropertyValue{Present: true, Value: []byte(`{"status":"known","transactionId":"tx"}`)}}}
	f := diagnosticRule(diagnosticRun(t, g), "transaction_expectation")
	if f == nil || f.Certainty != "confirmed" {
		t.Fatal(f)
	}
}
