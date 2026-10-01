package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
)

func runtimeQueryAddNode(t *testing.T, s *RevisionState, id int, kind string, parent int, attrs map[string]any) {
	t.Helper()
	n := Node{ID: runtimeQueryID(id), Kind: kind, Name: fmt.Sprintf("node %d", id), Attributes: runtimeQueryAttrs(t, attrs), EvidenceIDs: []string{runtimeQueryID(id + 300000)}, Freshness: &AssertionFreshness{Status: "current"}}
	if parent != 0 {
		n.ParentID = new(runtimeQueryID(parent))
	}
	s.Nodes = append(s.Nodes, n)
	s.Evidence = append(s.Evidence, Evidence{ID: n.EvidenceIDs[0], SubjectID: n.ID, Status: "explicit", Freshness: &AssertionFreshness{Status: "current"}})
}

func runtimeQueryID(n int) string { return fmt.Sprintf("%08x-0000-4000-8000-%012x", n, n) }

func runtimeQueryAttrs(t *testing.T, values map[string]any) map[string]jsontext.Value {
	t.Helper()
	attrs := map[string]jsontext.Value{}
	for key, value := range values {
		b, err := json.Marshal(value)
		if err != nil {
			t.Fatal(err)
		}
		attrs[key] = b
	}
	return attrs
}

func runtimeQueryFixture(t *testing.T) *RevisionState {
	t.Helper()
	s := &RevisionState{Revision: Revision{ID: runtimeQueryID(900), ProjectID: runtimeQueryID(901), SchemaVersion: "3", SemanticHash: strings.Repeat("a", 64), Coverage: Coverage{Status: "partial", Gaps: []string{"Source only"}}}, Sources: []SourceSnapshot{{Role: "primary", ID: runtimeQueryID(903), Provider: SourceProvider{Profiles: []string{GraphProfile, RelationalProfile, RuntimeProfile}}}}, Inventory: []InventoryItem{}}
	add := func(id int, kind string, parent int, attrs map[string]any) {
		n := Node{ID: runtimeQueryID(id), Kind: kind, Name: fmt.Sprintf("node %d", id), Attributes: runtimeQueryAttrs(t, attrs), EvidenceIDs: []string{runtimeQueryID(id + 2000)}, Freshness: &AssertionFreshness{Status: "current"}}
		if parent != 0 {
			n.ParentID = new(runtimeQueryID(parent))
		}
		s.Nodes = append(s.Nodes, n)
		s.Evidence = append(s.Evidence, Evidence{ID: n.EvidenceIDs[0], SubjectID: n.ID, Status: "explicit", Freshness: &AssertionFreshness{Status: "current"}})
	}
	add(1, "http_operation", 0, map[string]any{"method": "POST", "path": "/orders/cancel"})
	add(2, "handler", 0, nil)
	add(3, "flow", 2, map[string]any{"entryStepId": runtimeQueryID(4), "analysisStatus": "complete", "gaps": []string{}, "exitStatus": "complete"})
	add(4, "flow_step", 3, map[string]any{"stepKind": "input", "analysisStatus": "complete", "gaps": []string{}})
	add(5, "flow_step", 3, map[string]any{"stepKind": "condition", "analysisStatus": "complete", "gaps": []string{}})
	add(6, "flow_step", 3, map[string]any{"stepKind": "query", "analysisStatus": "complete", "gaps": []string{}})
	add(7, "flow_step", 3, map[string]any{"stepKind": "query", "analysisStatus": "complete", "gaps": []string{}})
	add(8, "query", 2, map[string]any{"analysisStatus": "complete", "columnScope": "complete", "gaps": []string{}})
	add(9, "query", 2, map[string]any{"analysisStatus": "complete", "columnScope": "unknown", "gaps": []string{"Dynamic columns"}})
	add(10, "table", 12, nil)
	add(11, "column", 10, nil)
	add(12, "datastore", 0, nil)
	addEdge := func(id int, kind string, from, to int, attrs map[string]any) {
		runtimeQueryAddEdge(t, s, id, kind, from, to, attrs)
	}
	addEdge(100, "handles", 1, 2, nil)
	addEdge(101, "contains", 2, 3, nil)
	addEdge(102, "contains", 3, 4, nil)
	for _, step := range []int{5, 6, 7} {
		addEdge(100+step, "contains", 3, step, nil)
	}
	addEdge(110, "next", 4, 5, nil)
	addEdge(111, "branch", 5, 6, map[string]any{"label": "authorized"})
	addEdge(112, "calls", 6, 8, nil)
	addEdge(113, "reads", 8, 11, map[string]any{"accessMode": "read", "datastoreId": runtimeQueryID(12), "facetKey": "sql", "columnScope": "listed"})
	addEdge(114, "calls", 7, 9, nil)
	addEdge(115, "writes", 9, 10, map[string]any{"accessMode": "update", "datastoreId": runtimeQueryID(12), "facetKey": "sql", "columnScope": "unknown", "scopeReason": "Dynamic columns"})
	return s
}

func runtimeQueryAddEdge(t *testing.T, s *RevisionState, id int, kind string, from, to int, attrs map[string]any) {
	t.Helper()
	e := Edge{ID: runtimeQueryID(id), Kind: kind, From: runtimeQueryID(from), To: runtimeQueryID(to), Attributes: runtimeQueryAttrs(t, attrs), EvidenceIDs: []string{runtimeQueryID(id + 2000)}, Freshness: &AssertionFreshness{Status: "current"}}
	s.Edges = append(s.Edges, e)
	s.Evidence = append(s.Evidence, Evidence{ID: e.EvidenceIDs[0], SubjectID: e.ID, Status: "explicit", Freshness: &AssertionFreshness{Status: "current"}})
}

func runtimeQueryPage(t *testing.T, s *RevisionState, in FlowQueryInput) *FlowPage {
	t.Helper()
	in.RevisionID = s.Revision.ID
	p, err := projectRuntimeFlow(t.Context(), s, in)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func TestRuntimeQueryVariantsAndExactWitness(t *testing.T) {
	s := runtimeQueryFixture(t)
	before, _ := json.Marshal(s, json.Deterministic(true))
	entry := runtimeQueryPage(t, s, FlowQueryInput{View: "entrypoints", Search: "POST"})
	if len(entry.EntryPointItems) != 1 || !slices.Equal(entry.EntryPointItems[0].HandlerIDs, []string{runtimeQueryID(2)}) || !slices.Equal(entry.EntryPointItems[0].FlowIDs, []string{runtimeQueryID(3)}) {
		t.Fatalf("entrypoint %+v", entry)
	}
	steps := runtimeQueryPage(t, s, FlowQueryInput{View: "steps", FlowID: runtimeQueryID(3)})
	if len(steps.StepItems) != 4 || steps.StepItems[3].ID != runtimeQueryID(7) {
		t.Fatalf("dead step missing %+v", steps)
	}
	transitions := runtimeQueryPage(t, s, FlowQueryInput{View: "transitions", FlowID: runtimeQueryID(3)})
	if len(transitions.TransitionItems) != 4 || string(transitions.TransitionItems[1].Attributes["label"]) != `"authorized"` {
		t.Fatalf("transitions %+v", transitions)
	}
	access := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if len(access.AccessItems) != 1 {
		t.Fatalf("unreachable step became endpoint access: %+v", access)
	}
	a := access.AccessItems[0]
	wantEdges := []string{}
	for _, id := range []int{100, 101, 102, 110, 111, 112, 113} {
		wantEdges = append(wantEdges, runtimeQueryID(id))
	}
	wantNodes := []string{}
	for _, id := range []int{1, 2, 3, 4, 5, 6, 8, 11} {
		wantNodes = append(wantNodes, runtimeQueryID(id))
	}
	if !slices.Equal(a.PathEdgeIDs, wantEdges) || !slices.Equal(a.PathNodeIDs, wantNodes) || a.Status != "explicit" || a.Relation != "direct" || a.FlowID == nil || *a.FlowID != runtimeQueryID(3) {
		t.Fatalf("witness %+v", a)
	}
	for _, id := range append([]int{1, 2, 3, 4, 5, 6, 8, 11}, 100, 101, 102, 110, 111, 112, 113) {
		if !slices.Contains(a.EvidenceIDs, runtimeQueryID(id+2000)) {
			t.Fatalf("missing record proof%d", id)
		}
	}
	for _, p := range []*FlowPage{entry, steps, transitions, access} {
		b, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]jsontext.Value
		_ = json.Unmarshal(b, &m)
		count := 0
		for _, k := range []string{"entrypointItems", "stepItems", "transitionItems", "accessItems"} {
			if _, ok := m[k]; ok {
				count++
			}
		}
		if count != 1 || p.RevisionID != s.Revision.ID || p.SemanticHash != s.Revision.SemanticHash {
			t.Fatalf("variant/pin %s", b)
		}
	}
	after, _ := json.Marshal(s, json.Deterministic(true))
	if string(before) != string(after) {
		t.Fatal("pure projection mutated pinned state")
	}
}

func TestRuntimeQueryStrictPresence(t *testing.T) {
	id := runtimeQueryID(1)
	rev := runtimeQueryID(900)
	for _, raw := range []string{
		`{"revisionId":"` + rev + `","view":"entrypoints","flowId":""}`,
		`{"revisionId":"` + rev + `","view":"steps","flowId":"` + id + `","search":""}`,
		`{"revisionId":"` + rev + `","view":"accesses","entrypointId":"` + id + `","dataNodeId":""}`,
		`{"revisionId":"` + rev + `","view":"accesses"}`,
		`{"revisionId":"` + rev + `","view":"accesses","entrypointId":"` + id + `","accessKind":""}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","proposal":{}}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","unknown":true}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","search":null}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","limit":0}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","limit":1e2}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","limit":501}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","cursor":null}`,
		`{"revisionId":"` + rev + `","view":"entrypoints","view":"entrypoints"}`,
		`{"revisionId":null,"view":"entrypoints"}`,
		`{"view":"entrypoints"}`,
	} {
		t.Run(raw, func(t *testing.T) {
			var in FlowQueryInput
			if err := json.Unmarshal([]byte(raw), &in); err == nil {
				t.Fatal("invalid query accepted")
			}
		})
	}
	for _, raw := range []string{
		`{"revisionId":"` + rev + `","view":"entrypoints","search":"","limit":500}`,
		`{"revisionId":"` + rev + `","view":"steps","flowId":"` + id + `"}`,
		`{"revisionId":"` + rev + `","view":"transitions","flowId":"` + id + `"}`,
		`{"revisionId":"` + rev + `","view":"accesses","dataNodeId":"` + id + `","accessKind":"reads"}`,
	} {
		var in FlowQueryInput
		if err := json.Unmarshal([]byte(raw), &in); err != nil {
			t.Fatal(raw, err)
		}
	}
}

func TestRuntimeAccessReversePossibleUnattachedAndProof(t *testing.T) {
	s := runtimeQueryFixture(t)
	p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", DataNodeID: runtimeQueryID(11)})
	if len(p.AccessItems) != 2 {
		t.Fatalf("reverse access facts %+v", p)
	}
	if p.AccessItems[0].Relation != "possible" || p.AccessItems[0].EntrypointID != nil || len(p.AccessItems[0].PathEdgeIDs) != 0 || p.AccessItems[1].Relation != "direct" || p.AccessItems[1].EntrypointID == nil {
		t.Fatalf("possible/unattached %+v", p.AccessItems)
	}
	if !strings.Contains(strings.Join(p.AccessItems[0].Limitations, " "), "unattached") {
		t.Fatal("unattached scope not stated")
	}
	filtered := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", DataNodeID: runtimeQueryID(11), AccessKind: "reads"})
	if len(filtered.AccessItems) != 1 || filtered.AccessItems[0].AccessEdgeID != runtimeQueryID(113) {
		t.Fatal("accessKind filter ignored")
	}
	for i := range s.Evidence {
		if s.Evidence[i].ID == runtimeQueryID(2111) {
			s.Evidence[i].Freshness.Status = "stale"
		}
	}
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if p.AccessItems[0].Status != "stale" {
		t.Fatal("stale branch evidence disappeared")
	}
	for i := range s.Evidence {
		if s.Evidence[i].ID == runtimeQueryID(2111) {
			s.Evidence[i].Freshness.Status = "current"
			s.Evidence[i].Status = "inferred"
		}
	}
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if p.AccessItems[0].Status != "inferred" {
		t.Fatal("inferred branch promoted to explicit")
	}
}

func TestRuntimePaginationIndependentAndPinned(t *testing.T) {
	s := runtimeQueryFixture(t)
	for i := range 510 {
		id := 3000 + i
		s.Nodes = append(s.Nodes, Node{ID: runtimeQueryID(id), Kind: "flow_step", ParentID: new(runtimeQueryID(3)), Attributes: map[string]jsontext.Value{}, EvidenceIDs: []string{}})
	}
	all := runtimeQueryPage(t, s, FlowQueryInput{View: "steps", FlowID: runtimeQueryID(3), Limit: 500})
	if len(all.StepItems) != 500 || all.NextCursor == "" {
		t.Fatalf("large list %+v", all)
	}
	ids := []string{}
	in := FlowQueryInput{View: "steps", FlowID: runtimeQueryID(3), Limit: 71}
	for {
		p := runtimeQueryPage(t, s, in)
		for _, n := range p.StepItems {
			ids = append(ids, n.ID)
		}
		if p.NextCursor == "" {
			break
		}
		in.Cursor = p.NextCursor
	}
	if len(ids) != 514 || len(slices.Compact(slices.Clone(ids))) != 514 {
		t.Fatal("pagination lost/duplicated steps", len(ids))
	}
	for _, change := range []func(*FlowQueryInput, *RevisionState){
		func(in *FlowQueryInput, _ *RevisionState) { in.FlowID = runtimeQueryID(4) },
		func(in *FlowQueryInput, _ *RevisionState) { in.View = "transitions" },
		func(_ *FlowQueryInput, s *RevisionState) { s.Revision.SemanticHash = strings.Repeat("b", 64) },
	} {
		copyState := *s
		bad := FlowQueryInput{RevisionID: s.Revision.ID, View: "steps", FlowID: runtimeQueryID(3), Cursor: all.NextCursor}
		change(&bad, &copyState)
		if _, err := projectRuntimeFlow(t.Context(), &copyState, bad); err == nil {
			t.Fatal("foreign cursor accepted")
		}
	}
}

func TestRuntimeTraversalDepthSensitiveCombinedBudget(t *testing.T) {
	// Independent graph: a short chain reaches shared query_step at 32 calls.
	// The longer control-only route reaches the same node at zero calls, and can
	// still call its query. A node-only visited set loses this valid access.
	s := runtimeQueryFixture(t)
	s.Nodes = s.Nodes[:0]
	s.Edges = s.Edges[:0]
	s.Evidence = s.Evidence[:0]
	add := func(id int, kind string, parent int, attrs map[string]any) {
		n := Node{ID: runtimeQueryID(id), Kind: kind, Attributes: runtimeQueryAttrs(t, attrs), EvidenceIDs: []string{}, Freshness: &AssertionFreshness{Status: "current"}}
		if parent != 0 {
			n.ParentID = new(runtimeQueryID(parent))
		}
		s.Nodes = append(s.Nodes, n)
	}
	add(1, "http_operation", 0, nil)
	add(2, "handler", 0, nil)
	add(3, "flow", 2, map[string]any{"entryStepId": runtimeQueryID(4)})
	add(4, "flow_step", 3, nil)
	runtimeQueryAddEdge(t, s, 100, "handles", 1, 2, nil)
	runtimeQueryAddEdge(t, s, 101, "contains", 2, 3, nil)
	runtimeQueryAddEdge(t, s, 102, "contains", 3, 4, nil)
	prev := 4
	// First call goes from entry to handler, then 31 more calls reach shared.
	for i := range 32 {
		h := 1000 + i*3
		f := h + 1
		step := h + 2
		add(h, "handler", 0, nil)
		add(f, "flow", h, map[string]any{"entryStepId": runtimeQueryID(step)})
		add(step, "flow_step", f, nil)
		runtimeQueryAddEdge(t, s, 4000+i*3, "calls", prev, h, nil)
		runtimeQueryAddEdge(t, s, 4001+i*3, "contains", h, f, nil)
		runtimeQueryAddEdge(t, s, 4002+i*3, "contains", f, step, nil)
		prev = step
	}
	shared := prev
	query := 2000
	target := 2001
	add(query, "query", 2, nil)
	add(target, "column", 2002, nil)
	add(2002, "table", 2003, nil)
	add(2003, "datastore", 0, nil)
	runtimeQueryAddEdge(t, s, 6000, "calls", shared, query, nil)
	runtimeQueryAddEdge(t, s, 6001, "reads", query, target, map[string]any{"accessMode": "read", "datastoreId": runtimeQueryID(2003), "facetKey": "sql", "columnScope": "listed"})
	// 101 edges are longer than the short route's 96 call/structure edges.
	prev = 4
	shallowEdges := []string{runtimeQueryID(100), runtimeQueryID(101), runtimeQueryID(102)}
	for i := range 100 {
		id := 3000 + i
		add(id, "flow_step", 3, nil)
		runtimeQueryAddEdge(t, s, 7000+i, "next", prev, id, nil)
		shallowEdges = append(shallowEdges, runtimeQueryID(7000+i))
		prev = id
	}
	// Cross-flow control is avoided by invoking the shared owner once. The
	// shallow route therefore has one call to the shared handler and one query call.
	owner := 1000 + 31*3
	runtimeQueryAddEdge(t, s, 7100, "calls", prev, owner, nil)
	shallowEdges = append(shallowEdges, runtimeQueryID(7100), runtimeQueryID(4001+31*3), runtimeQueryID(4002+31*3), runtimeQueryID(6000), runtimeQueryID(6001))
	in := FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1), Limit: 1}
	p := runtimeQueryPage(t, s, in)
	if len(p.AccessItems) != 1 || !slices.Equal(p.AccessItems[0].PathEdgeIDs, shallowEdges) {
		t.Fatalf("admissible shallower route lost %+v", p.AccessItems)
	}
	if !p.Truncated || !slices.Contains(p.TruncationReasons, "call_hops") {
		t.Fatalf("quota refusal hidden %+v", p)
	}
	in.Limit = 500
	q := runtimeQueryPage(t, s, in)
	if !reflect.DeepEqual(p.AccessItems, q.AccessItems) || !slices.Equal(p.TruncationReasons, q.TruncationReasons) {
		t.Fatal("page size changed witness/truncation")
	}
}

func TestRuntimeTraversalReportsSimultaneousWitnessAndCallRefusal(t *testing.T) {
	s := runtimeQueryFixture(t)
	s.Nodes = s.Nodes[:4]
	s.Edges = s.Edges[:3]
	prev := 4
	for i := range 157 {
		node := 10000 + i
		runtimeQueryAddNode(t, s, node, "flow_step", 3, nil)
		runtimeQueryAddEdge(t, s, 40000+i, "next", prev, node, nil)
		prev = node
	}
	for i := range 32 {
		handler := 20000 + i*3
		flow := handler + 1
		step := handler + 2
		runtimeQueryAddNode(t, s, handler, "handler", 0, nil)
		runtimeQueryAddNode(t, s, flow, "flow", handler, map[string]any{"entryStepId": runtimeQueryID(step)})
		runtimeQueryAddNode(t, s, step, "flow_step", flow, nil)
		runtimeQueryAddEdge(t, s, 50000+i*3, "calls", prev, handler, nil)
		runtimeQueryAddEdge(t, s, 50001+i*3, "contains", handler, flow, nil)
		runtimeQueryAddEdge(t, s, 50002+i*3, "contains", flow, step, nil)
		prev = step
	}
	runtimeQueryAddNode(t, s, 30000, "query", 2, nil)
	runtimeQueryAddEdge(t, s, 60000, "calls", prev, 30000, nil)
	p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if !slices.Contains(p.TruncationReasons, "call_hops") || !slices.Contains(p.TruncationReasons, "witness_length") {
		t.Fatal("simultaneous refusal lost a reason", p.TruncationReasons)
	}
}

func TestRuntimeWitnessShortestLexicalCyclesAndUnknown(t *testing.T) {
	s := runtimeQueryFixture(t)
	// Two equal-length branch edges choose the smallest actual edge UUID.
	runtimeQueryAddEdge(t, s, 109, "branch", 5, 6, map[string]any{"label": "alternate"})
	runtimeQueryAddEdge(t, s, 116, "next", 5, 4, nil)
	runtimeQueryAddNode(t, s, 14, "unresolved_target", 0, map[string]any{"reason": "Dynamic dispatch", "searchScope": "callsite"})
	runtimeQueryAddEdge(t, s, 117, "calls", 5, 14, nil)
	p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if p.Truncated || len(p.AccessItems) != 1 || p.AccessItems[0].PathEdgeIDs[4] != runtimeQueryID(109) {
		t.Fatalf("tie/cycle %+v", p)
	}
	if !strings.Contains(strings.Join(p.Limitations, " "), "Dynamic dispatch") {
		t.Fatal("reachable unknown boundary vanished")
	}
	// A shorter route wins even though its first distinguishing edge is larger.
	runtimeQueryAddEdge(t, s, 118, "calls", 4, 8, nil)
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if len(p.AccessItems[0].PathEdgeIDs) != 5 || p.AccessItems[0].PathEdgeIDs[3] != runtimeQueryID(118) {
		t.Fatal("longer lexical route won")
	}
	// Independent HTTP callers produce separate result pairs for one query.
	runtimeQueryAddNode(t, s, 15, "http_operation", 0, map[string]any{"method": "GET", "path": "/orders"})
	runtimeQueryAddEdge(t, s, 119, "handles", 15, 2, nil)
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", DataNodeID: runtimeQueryID(11)})
	if len(p.AccessItems) != 3 || p.AccessItems[1].EntrypointID == nil || p.AccessItems[2].EntrypointID == nil || *p.AccessItems[1].EntrypointID == *p.AccessItems[2].EntrypointID {
		t.Fatalf("shared callers merged %+v", p.AccessItems)
	}
	// Handler/symbol recursion retains depth states and refuses the 33rd call.
	runtimeQueryAddEdge(t, s, 120, "calls", 4, 2, nil)
	p = runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if !p.Truncated || !slices.Contains(p.TruncationReasons, "call_hops") || len(p.AccessItems[0].PathEdgeIDs) != 5 {
		t.Fatal("recursive cumulative budget/witness incorrect")
	}
}

func TestRuntimeAccessSelectedFacetProofIsolation(t *testing.T) {
	s := runtimeQueryFixture(t)
	for i := range s.Nodes {
		if s.Nodes[i].ID != runtimeQueryID(11) {
			continue
		}
		s.Nodes[i].EvidenceIDs = append(s.Nodes[i].EvidenceIDs, runtimeQueryID(9999))
		s.Nodes[i].Attributes = runtimeQueryAttrs(t, map[string]any{"facets": map[string]any{
			"sql": map[string]any{"analysisStatus": "complete", "gaps": []string{}, "evidenceIds": []string{runtimeQueryID(2011)}},
			"orm": map[string]any{"analysisStatus": "partial", "gaps": []string{"Not current"}, "evidenceIds": []string{runtimeQueryID(9999)}},
		}})
	}
	s.Evidence = append(s.Evidence, Evidence{ID: runtimeQueryID(9999), SubjectID: runtimeQueryID(11), PropertyPath: new("/attributes/facets/orm/nullable"), Status: "explicit", Freshness: &AssertionFreshness{Status: "stale"}})
	p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
	if p.AccessItems[0].Status != "explicit" || slices.Contains(p.AccessItems[0].EvidenceIDs, runtimeQueryID(9999)) {
		t.Fatalf("different-facet proof leaked into selected source %+v", p.AccessItems[0])
	}
}

func TestRuntimeTraversalQuotas(t *testing.T) {
	minimal := func() *RevisionState {
		s := runtimeQueryFixture(t)
		s.Nodes = s.Nodes[:4]
		s.Edges = s.Edges[:3]
		return s
	}
	t.Run("states count initial and each newly admitted target", func(t *testing.T) {
		for _, extra := range []int{4996, 4997} {
			s := minimal()
			for i := range extra {
				runtimeQueryAddNode(t, s, 10000+i, "flow_step", 3, nil)
				runtimeQueryAddEdge(t, s, 40000+i, "next", 4, 10000+i, nil)
			}
			p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
			if p.Truncated != (extra == 4997) || extra == 4997 && !slices.Contains(p.TruncationReasons, "state_limit") {
				t.Fatalf("initial state was not charged %d %+v", extra, p.TruncationReasons)
			}
		}
	})
	t.Run("edges count every examination", func(t *testing.T) {
		for _, extra := range []int{19997, 19998} {
			s := minimal()
			runtimeQueryAddNode(t, s, 5, "flow_step", 3, nil)
			for i := range extra {
				runtimeQueryAddEdge(t, s, 40000+i, "branch", 4, 5, map[string]any{"label": fmt.Sprintf("candidate%d", i)})
			}
			p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
			if p.Truncated != (extra == 19998) || extra == 19998 && !slices.Contains(p.TruncationReasons, "edge_limit") {
				t.Fatalf("examined edges not bounded %d %+v", extra, p.TruncationReasons)
			}
		}
	})
	t.Run("same edges charged in separate call depth states", func(t *testing.T) {
		s := minimal()
		runtimeQueryAddEdge(t, s, 200, "calls", 4, 2, nil)
		for i := range 700 {
			runtimeQueryAddEdge(t, s, 40000+i, "next", 4, 4, nil)
		}
		p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
		if !p.Truncated || !slices.Contains(p.TruncationReasons, "edge_limit") || slices.Contains(p.TruncationReasons, "state_limit") {
			t.Fatalf("depth reexamination not charged %+v", p.TruncationReasons)
		}
	})
	t.Run("witness charges structure and final access edge", func(t *testing.T) {
		for _, length := range []int{251, 252} {
			s := runtimeQueryFixture(t)
			s.Edges = s.Edges[:3]
			prev := 4
			for i := range length {
				id := 10000 + i
				runtimeQueryAddNode(t, s, id, "flow_step", 3, nil)
				runtimeQueryAddEdge(t, s, 40000+i, "next", prev, id, nil)
				prev = id
			}
			runtimeQueryAddEdge(t, s, 50000, "calls", prev, 8, nil)
			runtimeQueryAddEdge(t, s, 50001, "reads", 8, 11, map[string]any{"accessMode": "read", "datastoreId": runtimeQueryID(12), "facetKey": "sql", "columnScope": "listed"})
			p := runtimeQueryPage(t, s, FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1)})
			if length == 251 {
				if p.Truncated || len(p.AccessItems) != 1 || len(p.AccessItems[0].PathEdgeIDs) != 256 {
					t.Fatal("256 actual edges refused", p.TruncationReasons)
				}
			} else if !p.Truncated || !slices.Contains(p.TruncationReasons, "witness_length") || len(p.AccessItems) != 0 {
				t.Fatal("257 actual edges accepted", p.TruncationReasons)
			}
		}
	})
	t.Run("result pairs bounded without unique target states", func(t *testing.T) {
		s := runtimeQueryFixture(t)
		s.Edges = s.Edges[:len(s.Edges)-1]
		for i := range 5000 {
			runtimeQueryAddEdge(t, s, 40000+i, "reads", 8, 11, map[string]any{"accessMode": "read", "datastoreId": runtimeQueryID(12), "facetKey": "sql", "columnScope": "listed"})
		}
		in := FlowQueryInput{View: "accesses", EntrypointID: runtimeQueryID(1), Limit: 500}
		count := 0
		for {
			p := runtimeQueryPage(t, s, in)
			if !p.Truncated || !slices.Contains(p.TruncationReasons, "result_limit") || slices.Contains(p.TruncationReasons, "state_limit") {
				t.Fatal("result pair cap incorrect", p.TruncationReasons)
			}
			count += len(p.AccessItems)
			if p.NextCursor == "" {
				break
			}
			in.Cursor = p.NextCursor
		}
		if count != 5000 {
			t.Fatal("bounded result enumeration", count)
		}
	})
}

func TestRuntimePaginationAccessAndTransitionBeyondCanvas(t *testing.T) {
	s := runtimeQueryFixture(t)
	for i := range 620 {
		runtimeQueryAddEdge(t, s, 40000+i, "branch", 5, 6, map[string]any{"label": fmt.Sprintf("outcome%d", i)})
	}
	for i := range 520 {
		runtimeQueryAddEdge(t, s, 50000+i, "reads", 8, 11, map[string]any{"accessMode": "read", "datastoreId": runtimeQueryID(12), "facetKey": "sql", "columnScope": "listed"})
	}
	for _, view := range []string{"transitions", "accesses"} {
		in := FlowQueryInput{View: view, Limit: 79}
		if view == "transitions" {
			in.FlowID = runtimeQueryID(3)
		} else {
			in.EntrypointID = runtimeQueryID(1)
		}
		count := 0
		for {
			p := runtimeQueryPage(t, s, in)
			if p.Truncated {
				t.Fatal("canvas limits truncated source", p.TruncationReasons)
			}
			count += len(p.TransitionItems) + len(p.AccessItems)
			if p.NextCursor == "" {
				break
			}
			in.Cursor = p.NextCursor
		}
		want := 624
		if view == "accesses" {
			want = 521
		}
		if count != want {
			t.Fatalf("%s enumeration%d want%d", view, count, want)
		}
	}
}

func TestRuntimeQueryCancellationAndUnsupportedSource(t *testing.T) {
	s := runtimeQueryFixture(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err := projectRuntimeFlow(ctx, s, FlowQueryInput{RevisionID: s.Revision.ID, View: "entrypoints"}); err != context.Canceled {
		t.Fatalf("canceled projection %v", err)
	}
	for _, version := range []string{"1", "2", "4"} {
		s.Revision.SchemaVersion = version
		_, err := projectRuntimeFlow(t.Context(), s, FlowQueryInput{RevisionID: s.Revision.ID, View: "entrypoints"})
		if assertFault(t, err, "backend_unsupported_scope").Status != 422 {
			t.Fatal("wrong source scope status")
		}
	}
	s.Revision.SchemaVersion = "3"
	s.Sources[0].Provider.Profiles = []string{GraphProfile, RelationalProfile}
	_, err := projectRuntimeFlow(t.Context(), s, FlowQueryInput{RevisionID: s.Revision.ID, View: "entrypoints"})
	assertFault(t, err, "backend_unsupported_scope")
}
