package backendanalysis

import (
	"fmt"
	"slices"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

// review 2026-10-06, F138: the unused_table rule rescanned every edge per
// table with one visit each, so 60 unused tables over 1,000 edges spent
// 60,000 visits and exhausted the default 50,000 budget: the report came back
// incomplete and later rules never ran.
func TestDiagnosticUnusedTablesDoNotExhaustVisitBudget(t *testing.T) {
	nodes := make([]backendmodel.Node, 0, 62)
	nodes = append(nodes, backendmodel.Node{ID: "a", Kind: "symbol"}, backendmodel.Node{ID: "b", Kind: "symbol"})
	for i := range 60 {
		nodes = append(nodes, backendmodel.Node{ID: fmt.Sprintf("table-%03d", i), Kind: "table"})
	}
	edges := make([]backendmodel.Edge, 0, 1000)
	for i := range 1000 {
		edges = append(edges, backendmodel.Edge{ID: fmt.Sprintf("calls-%04d", i), Kind: "calls", From: "a", To: "b"})
	}
	r := diagnosticRun(t, supportedGraph(nodes, edges))
	if !r.Complete || slices.Contains(r.Gaps, "diagnostic_visit_budget") {
		t.Fatalf("unused-table rule exhausted the visit budget: complete=%v gaps=%v", r.Complete, r.Gaps)
	}
}

// review 2026-10-06, F147: the edge loop ticked before asking whether the
// edge is in scope, so a narrow service scope was marked incomplete by edges
// it would skip anyway.
func TestDiagnosticOutOfScopeEdgesDoNotSpendVisits(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "svc", Kind: "service"}, {ID: "inside", Kind: "symbol", ParentID: new("svc")}, {ID: "x", Kind: "symbol"}, {ID: "y", Kind: "symbol"}}
	edges := make([]backendmodel.Edge, 0, 20)
	for i := range 20 {
		edges = append(edges, backendmodel.Edge{ID: fmt.Sprintf("calls-%02d", i), Kind: "calls", From: "x", To: "y"})
	}
	r, err := EvaluateDiagnostics(t.Context(), supportedGraph(nodes, edges), DiagnosticInput{Scope: Scope{Service: "svc"}, Limits: Limits{DependencyVisits: 10}})
	if err != nil {
		t.Fatal(err)
	}
	if !r.Complete || slices.Contains(r.Gaps, "diagnostic_visit_budget") {
		t.Fatalf("out-of-scope edges spent the visit budget: complete=%v gaps=%v", r.Complete, r.Gaps)
	}
}
