package backendanalysis

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"

	"github.com/yashok111/mocker/internal/backendmodel"
)

func supportedGraph(nodes []backendmodel.Node, edges []backendmodel.Edge) *backendmodel.EffectiveGraphSnapshot {
	state := backendmodel.RevisionState{Revision: backendmodel.Revision{SchemaVersion: "1", Coverage: backendmodel.Coverage{Status: "complete"}}, Nodes: nodes, Edges: edges}
	for i := range state.Nodes {
		n := &state.Nodes[i]
		if n.Name == "" {
			n.Name = n.ID
		}
		n.EvidenceIDs = []string{"proof-" + n.ID}
		state.Evidence = append(state.Evidence, backendmodel.Evidence{ID: n.EvidenceIDs[0], SubjectID: n.ID, Status: "explicit"})
		n.Freshness = &backendmodel.AssertionFreshness{Status: "current"}
	}
	for i := range state.Edges {
		e := &state.Edges[i]
		e.EvidenceIDs = []string{"proof-" + e.ID}
		state.Evidence = append(state.Evidence, backendmodel.Evidence{ID: e.EvidenceIDs[0], SubjectID: e.ID, Status: "explicit"})
		e.Freshness = &backendmodel.AssertionFreshness{Status: "current"}
	}
	return &backendmodel.EffectiveGraphSnapshot{State: state, Source: &backendmodel.SourceGraphSnapshot{State: state}, Pins: backendmodel.EffectiveGraphPins{StructuralSchemaVersion: "1", TargetHash: "target", EffectiveSemanticHash: "effective", BaseRevisionID: revisionID}}
}
func runtimeFixture() (*backendmodel.EffectiveGraphSnapshot, *backendmodel.EffectiveGraphSnapshot) {
	nodes := []backendmodel.Node{{ID: "column", Kind: "column"}, {ID: "query", Kind: "query"}, {ID: "step", Kind: "flow_step", ParentID: new("flow")}, {ID: "flow", Kind: "flow"}, {ID: "handler", Kind: "handler"}, {ID: "consumer", Kind: "consumer", Attributes: map[string]jsontext.Value{"dispatchStatus": []byte(`"complete"`)}}, {ID: "unrelated", Kind: "service"}}
	edges := []backendmodel.Edge{{ID: "reads", Kind: "reads", From: "query", To: "column"}, {ID: "calls", Kind: "calls", From: "step", To: "query"}, {ID: "owns", Kind: "contains", From: "handler", To: "flow"}, {ID: "handles", Kind: "handles", From: "consumer", To: "handler"}, {ID: "noise", Kind: "contains", From: "unrelated", To: "column"}, {ID: "proof-nav", Kind: "derived_from", From: "unrelated", To: "query"}}
	before := supportedGraph(nodes, edges)
	after := supportedGraph(slices.Clone(nodes[1:]), slices.Clone(edges[1:]))
	return before, after
}
func engineInput() *ImmutableInput {
	return &ImmutableInput{Kind: "impact", Limits: defaultLimits(), Scope: normalizedScope(Scope{}), RuleSetVersion: "b42-rules/v1", TraversalVersion: "b42-traversal/v1"}
}
func readRecords[T any](t *testing.T, s PreparedSnapshot, section string) []T {
	t.Helper()
	var out []T
	for _, c := range s.Chunks {
		if c.Section != section {
			continue
		}
		var records []ResultRecord
		if err := json.Unmarshal(c.ItemsJSON, &records); err != nil {
			t.Fatal(err)
		}
		for _, r := range records {
			var detail T
			if err := json.Unmarshal(r.Detail, &detail); err != nil {
				t.Fatal(err)
			}
			out = append(out, detail)
		}
	}
	return out
}
func TestAnalysisImpactRemovedColumnBeforeWitness(t *testing.T) {
	before, after := runtimeFixture()
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	witnesses := readRecords[Witness](t, terminal.Snapshot, "witnesses")
	found := false
	for _, w := range witnesses {
		if w.Affected.ID == "unrelated" {
			t.Fatal("containment/proof fanout")
		}
		if w.Side == "before" && w.Seed.ID == "column" && w.Affected.ID == "consumer" {
			found = true
			var path []string
			for _, step := range w.Steps {
				path = append(path, step.To.ID)
			}
			if !reflect.DeepEqual(path, []string{"query", "step", "flow", "handler", "consumer"}) {
				t.Fatal(path)
			}
		}
	}
	if !found {
		t.Fatalf("consumer witness missing: %+v", witnesses)
	}
	if terminal.Snapshot.Manifest.Verdict != "potential" {
		t.Fatal(terminal.Snapshot.Manifest.Verdict)
	}
	after.Criteria = []backendmodel.ChangeCriterion{{Key: "column-required", Kind: "object_exists", RecordType: "node", ID: "column", ObjectKind: "column", Required: true, Description: "Consumer requires the column"}}
	terminal, err = analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Snapshot.Manifest.Verdict != "incompatible" {
		t.Fatal(terminal.Snapshot.Manifest.Verdict)
	}
}
func TestAnalysisImpactUnknownBoundaryDoesNotHideSupportedPath(t *testing.T) {
	before, after := runtimeFixture()
	before.State.Edges = append([]backendmodel.Edge{{ID: "a-unknown", Kind: "reads", From: "query", To: "column", Freshness: &backendmodel.AssertionFreshness{Status: "stale"}}}, before.State.Edges...)
	before.Source.State = before.State
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var boundary, supported bool
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Seed.ID != "column" {
			continue
		}
		boundary = boundary || w.Status == "unknown"
		supported = supported || w.Affected.ID == "consumer" && w.Status != "unknown"
	}
	if !boundary || !supported {
		t.Fatalf("boundary=%v supported=%v", boundary, supported)
	}
}
func TestAnalysisLimitIndependent(t *testing.T) {
	for _, name := range []string{"states", "dependency_visits", "depth", "findings", "records", "witnesses", "bytes", "scope"} {
		t.Run(name, func(t *testing.T) {
			before, after := runtimeFixture()
			in := engineInput()
			switch name {
			case "states":
				in.Limits.States = 1
			case "dependency_visits":
				in.Limits.DependencyVisits = 1
			case "depth":
				in.Limits.Depth = 1
			case "findings":
				in.Limits.Findings = 1
			case "records":
				in.Limits.Records = 1
			case "witnesses":
				in.Limits.WitnessesPerObject = 1
			case "bytes":
				in.Limits.ResultBytes = terminalHeadroom + 64
			case "scope":
				in.Scope.ChangedIDs = []ObjectAddress{{RecordType: "node", ID: "query"}}
			}
			terminal, err := analyzeGraphs(t.Context(), in, before, after, nil, nil)
			if err != nil {
				t.Fatal(err)
			}
			m := terminal.Snapshot.Manifest
			if name == "scope" {
				// The selected scope was fully traversed, but it did not cover
				// the actual changed objects. Ready must check both separately.
				gap := slices.ContainsFunc(m.Gaps, func(g Diagnostic) bool { return g.Code == "scope_omitted_change" })
				if !m.Complete || !gap || len(m.ChangedIDs) == 0 || len(m.CoveredChangedIDs) != 0 || len(m.TruncationReasons) != 0 || m.Verdict != "unknown" || m.RuntimeVerified {
					t.Fatalf("scoped traversal lost explicit coverage boundary: %+v", m)
				}
				return
			}
			if m.Complete || len(m.TruncationReasons)+len(m.Gaps) == 0 {
				t.Fatalf("limit %s silently omitted %+v", name, m)
			}
			if m.RuntimeVerified {
				t.Fatal("runtime claim")
			}
		})
	}
}

func TestAnalysisImpactMappingRetainsRouteContexts(t *testing.T) {
	id := func(n int) string { return fmt.Sprintf("01900000-0000-7000-8000-%012d", n) }
	source, field, message, channel, consumer := id(1), id(2), id(3), id(4), id(5)
	nodes := append(make([]backendmodel.Node, 0, 7), []backendmodel.Node{{ID: source, Kind: "api_field"}, {ID: field, Kind: "event_field", ParentID: &message}, {ID: message, Kind: "message"}, {ID: channel, Kind: "channel"}, {ID: consumer, Kind: "consumer"}}...)
	edges := make([]backendmodel.Edge, 0, 2)
	for i := range 2 {
		route := id(10 + i)
		edges = append(edges, backendmodel.Edge{ID: route, Kind: "delivered_to", From: channel, To: consumer, Attributes: map[string]jsontext.Value{"messageId": []byte(fmt.Sprintf("%q", message)), "deliveryStatus": []byte(`"declared"`)}})
		mapping := backendmodel.LineageMappingAttributes{Sources: []backendmodel.LineageValueRef{{Kind: "api_field", NodeID: source}}, Destination: backendmodel.LineageValueRef{Kind: "event_field", NodeID: field, EndpointID: consumer, RouteID: route}, Transform: backendmodel.LineageTransform{Kind: "copy", Description: "Exact field copy"}, AnalysisStatus: "complete", Gaps: []string{}}
		raw, err := json.Marshal(mapping)
		if err != nil {
			t.Fatal(err)
		}
		var attrs map[string]jsontext.Value
		if err = json.Unmarshal(raw, &attrs); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, backendmodel.Node{ID: id(20 + i), Kind: "field_mapping", Attributes: attrs})
	}
	graph := supportedGraph(nodes, edges)
	graph.Pins.StructuralSchemaVersion = "5"
	r := newReport(engineInput())
	if err := r.traverse(t.Context(), graph, graph, []ObjectAddress{{RecordType: "node", ID: source}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(graph, graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	routes := map[string]bool{}
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Side == "before" && w.Affected.ID == field && w.Status != "unknown" {
			routes[w.Steps[0].Context.RouteID] = true
		}
	}
	if !routes[id(10)] || !routes[id(11)] {
		t.Fatalf("contexts lost: %+v", readRecords[Witness](t, terminal.Snapshot, "witnesses"))
	}
}
func TestAnalysisLimitFindingAndRecordCountersSeparate(t *testing.T) {
	r := newReport(engineInput())
	for range 10001 {
		r.add("findings", ObjectAddress{RecordType: "node", ID: "same"}, "column", "possible", 0, RuleResult{RuleID: "test"})
	}
	if r.progress.Findings != 10000 || r.progress.Records != 10000 || r.truncations["finding_limit"].Code == "" {
		t.Fatal(r.progress, r.truncations)
	}
	for range 10001 {
		r.add("checks", ObjectAddress{RecordType: "node", ID: "same"}, "column", "possible", 0, RuleResult{RuleID: "test"})
	}
	if r.progress.Records != 20000 || r.truncations["record_limit"].Code == "" {
		t.Fatal(r.progress, r.truncations)
	}
}
func TestAnalysisDeterminismAcrossInsertionOrder(t *testing.T) {
	before, after := runtimeFixture()
	first, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	before2, after2 := runtimeFixture()
	slices.Reverse(before2.State.Nodes)
	slices.Reverse(before2.State.Edges)
	slices.Reverse(after2.State.Nodes)
	slices.Reverse(after2.State.Edges)
	second, err := analyzeGraphs(t.Context(), engineInput(), before2, after2, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	a, err := prepareSnapshot(first.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	b, err := prepareSnapshot(second.Snapshot)
	if err != nil {
		t.Fatal(err)
	}
	if a.Manifest.SemanticResultHash != b.Manifest.SemanticResultHash {
		t.Fatal("insertion order changed report hash")
	}
}
func TestAnalysisImpactSource6SelectedStaleProof(t *testing.T) {
	before := supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "handler", Name: "old"}}, nil)
	after := supportedGraph([]backendmodel.Node{{ID: revisionID, Kind: "handler", Name: "new"}}, nil)
	for _, g := range []*backendmodel.EffectiveGraphSnapshot{before, after} {
		n := g.State.Nodes[0]
		g.Pins.StructuralSchemaVersion = "6"
		g.Source.SourceVector = &backendmodel.SourceVector{}
		a := backendmodel.ProviderAssertion{RecordType: "node", RecordID: n.ID, Owner: backendmodel.AssertionOwnership{RepositoryID: projectID, ProviderNamespace: "provider", Profile: backendmodel.ComposedProfile}, ExternalKey: "handler", AssertionHash: strings.Repeat("a", 64), Payload: backendmodel.SourceAssertionPayload{RecordType: "node", Kind: n.Kind, Name: n.Name, Attributes: map[string]jsontext.Value{}}, Freshness: backendmodel.AssertionFreshness{Status: "current"}, EvidenceIDs: n.EvidenceIDs}
		g.Source.Assertions = []backendmodel.ProviderAssertion{a}
		g.Source.Currentness = []backendmodel.SourceClaimCurrentness{{RecordType: "node", RecordID: n.ID, RepositoryID: projectID, ProviderNamespace: "provider", AssertionHash: a.AssertionHash, Own: a.Freshness, Dependency: a.Freshness, Fields: []backendmodel.TypedFieldCurrentness{{Property: backendmodel.TypedSourcePropertySelector{Kind: "name"}, Own: a.Freshness, Dependency: a.Freshness}}}}
	}
	after.Source.Currentness[0].Fields[0].Own = backendmodel.AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if terminal.Snapshot.Manifest.Verdict != "unknown" {
		t.Fatal("placeholder freshness proved compatibility")
	}
	proof, err := backendmodel.EffectivePropertyAnalysisProof(after, backendmodel.ChangeRecordRef{RecordType: "node", ID: revisionID}, backendmodel.EffectivePropertySelector{Source: &backendmodel.TypedSourcePropertySelector{Kind: "name"}})
	if err != nil || !proof.Boundary {
		t.Fatalf("typed proof %v %v", proof, err)
	}
}

func TestAnalysisWorkerPublishesBoundedProgress(t *testing.T) {
	graph := supportedGraph(nil, nil)
	in := engineInput()
	r := newReport(in)
	r.before, r.after = graph, graph
	published := 0
	r.emit = func(s PreparedSnapshot) error {
		published++
		if s.Manifest.Complete || len(s.Chunks) == 0 {
			t.Fatal("invalid progress prefix")
		}
		return nil
	}
	for range 2048 {
		r.add("checks", ObjectAddress{RecordType: "node", ID: revisionID}, "column", "possible", 0, RuleResult{RuleID: "data-check"})
	}
	if published != 1 {
		t.Fatalf("records buffered without progress: %d", published)
	}
	terminal, err := r.finish(graph, graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(readRecords[RuleResult](t, terminal.Snapshot, "checks")) != 2048 {
		t.Fatal("progress regrouping lost records")
	}
}

func TestAnalysisImpactConstraintUsesDeclaredColumns(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "constraint", Kind: "constraint", Attributes: map[string]jsontext.Value{"facets": []byte(fmt.Sprintf(`{"sql":{"columnIds":[%q]}}`, revisionID))}}, {ID: revisionID, Kind: "column", Attributes: map[string]jsontext.Value{"facets": []byte(`{"sql":{}}`)}}, {ID: "query", Kind: "query"}, {ID: "unrelated", Kind: "column"}}
	graph := supportedGraph(nodes, []backendmodel.Edge{{ID: "access", Kind: "reads", From: "query", To: revisionID}})
	r := newReport(engineInput())
	if err := r.traverse(t.Context(), graph, graph, []ObjectAddress{{RecordType: "node", ID: "constraint"}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(graph, graph, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Side == "before" && w.Affected.ID == "query" {
			found = true
		}
		if w.Affected.ID == "unrelated" {
			t.Fatal("unrelated column reached")
		}
	}
	if !found {
		t.Fatal("declared constraint columns not propagated")
	}
}

func TestAnalysisImpactUnknownDispatchStopsBeforeFlow(t *testing.T) {
	before, after := runtimeFixture()
	for i := range before.State.Nodes {
		if before.State.Nodes[i].ID == "consumer" {
			before.State.Nodes[i].Attributes["dispatchStatus"] = []byte(`"unknown"`)
		}
	}
	r := newReport(engineInput())
	if err := r.traverse(t.Context(), before, after, []ObjectAddress{{RecordType: "node", ID: "consumer"}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(before, after, nil)
	if err != nil {
		t.Fatal(err)
	}
	boundary := false
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Side != "before" {
			continue
		}
		if w.Affected.ID == "handler" && w.Status == "unknown" {
			boundary = true
		}
		if w.Affected.ID == "flow" {
			t.Fatal("unknown dispatch reached flow")
		}
	}
	if !boundary {
		t.Fatal("dispatch boundary missing")
	}
}
func TestAnalysisLimitNinthWitnessIsExplicit(t *testing.T) {
	nodes := make([]backendmodel.Node, 1, 10)
	nodes[0] = backendmodel.Node{ID: "query", Kind: "query"}
	edges := make([]backendmodel.Edge, 0, 9)
	seeds := make([]ObjectAddress, 0, 9)
	for i := range 9 {
		id := fmt.Sprintf("column-%d", i)
		nodes = append(nodes, backendmodel.Node{ID: id, Kind: "column"})
		edges = append(edges, backendmodel.Edge{ID: fmt.Sprintf("read-%d", i), Kind: "reads", From: "query", To: id})
		seeds = append(seeds, ObjectAddress{RecordType: "node", ID: id})
	}
	g := supportedGraph(nodes, edges)
	r := newReport(engineInput())
	if err := r.traverse(t.Context(), g, supportedGraph(nil, nil), seeds); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(g, g, nil)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Affected.ID == "query" {
			count++
		}
	}
	if count != 8 || r.truncations["witness_limit"].Code == "" || terminal.Snapshot.Manifest.Complete {
		t.Fatalf("ninth witness: %d %+v", count, terminal.Snapshot.Manifest)
	}
}

func TestAnalysisImpactMappingOnlyChangeSeedsDestinations(t *testing.T) {
	source, dest1, dest2, mappingID := revisionID, projectID, "44444444-4444-4444-8444-444444444444", "55555555-5555-4555-8555-555555555555"
	graph := func(dest string) *backendmodel.EffectiveGraphSnapshot {
		mapping := backendmodel.LineageMappingAttributes{Sources: []backendmodel.LineageValueRef{{Kind: "api_field", NodeID: source}}, Destination: backendmodel.LineageValueRef{Kind: "api_field", NodeID: dest}, Transform: backendmodel.LineageTransform{Kind: "copy", Description: "copy"}, AnalysisStatus: "complete", Gaps: []string{}}
		raw, err := json.Marshal(mapping)
		if err != nil {
			t.Fatal(err)
		}
		var attrs map[string]jsontext.Value
		if err = json.Unmarshal(raw, &attrs); err != nil {
			t.Fatal(err)
		}
		g := supportedGraph([]backendmodel.Node{{ID: source, Kind: "api_field"}, {ID: dest1, Kind: "api_field"}, {ID: dest2, Kind: "api_field"}, {ID: mappingID, Kind: "field_mapping", Attributes: attrs}}, nil)
		g.Pins.StructuralSchemaVersion = "4"
		return g
	}
	terminal, err := analyzeGraphs(t.Context(), engineInput(), graph(dest1), graph(dest2), nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	var old, next bool
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Seed.ID != mappingID {
			continue
		}
		old = old || w.Side == "before" && w.Affected.ID == dest1
		next = next || w.Side == "after" && w.Affected.ID == dest2
	}
	if !old || !next {
		t.Fatalf("mapping-only change lost destination: before=%v after=%v", old, next)
	}
}

func TestAnalysisImpactStaleSeedStopsConfirmedPropagation(t *testing.T) {
	before, after := runtimeFixture()
	before.State.Nodes[0].Freshness = &backendmodel.AssertionFreshness{Status: "stale", Reasons: []string{"not_reobserved"}}
	before.Source.State = before.State
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	boundary := false
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Seed.ID != "column" || w.Side != "before" {
			continue
		}
		if w.Affected.ID == "query" && w.Status == "unknown" {
			boundary = true
		}
		if w.Affected.ID == "consumer" {
			t.Fatal("stale seed reached consumer")
		}
	}
	if !boundary {
		t.Fatal("stale seed boundary missing")
	}
}

func TestAnalysisImpactServiceScopeIncludesOwnedEdge(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "service", Kind: "service", Name: "Orders"}, {ID: "query", Kind: "query", ParentID: new("service")}, {ID: "column", Kind: "column", ParentID: new("service")}}
	before := supportedGraph(slices.Clone(nodes), []backendmodel.Edge{{ID: "access", Kind: "reads", From: "query", To: "column"}})
	after := supportedGraph(slices.Clone(nodes), []backendmodel.Edge{{ID: "access", Kind: "writes", From: "query", To: "column"}})
	in := engineInput()
	in.Scope.Service = "service"
	terminal, err := analyzeGraphs(t.Context(), in, before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if !slices.Contains(terminal.Snapshot.Manifest.CoveredChangedIDs, ObjectAddress{RecordType: "edge", ID: "access"}) {
		t.Fatal("service-owned edge omitted")
	}
}

func TestAnalysisImpactProofAggregationNeverUpgradesInferred(t *testing.T) {
	inferred := backendmodel.EffectiveAnalysisProof{Status: "inferred", Reasons: []string{"missing_semantic_support"}}
	desired := backendmodel.EffectiveAnalysisProof{Status: "desired", Reasons: []string{"desired_structure"}}
	for _, pair := range [][2]backendmodel.EffectiveAnalysisProof{{inferred, desired}, {desired, inferred}} {
		p := combineProof(pair[0], pair[1])
		if p.Status != "inferred" || supported(p) {
			t.Fatalf("inferred input became supported: %+v", p)
		}
	}
}

func TestAnalysisImpactUnknownTransformKeepsIndependentMapping(t *testing.T) {
	nodes := make([]backendmodel.Node, 0, 4)
	nodes = append(nodes, backendmodel.Node{ID: revisionID, Kind: "api_field"}, backendmodel.Node{ID: projectID, Kind: "api_field"})
	for i, kind := range []string{"unknown_transform", "copy"} {
		status := "complete"
		gaps := []string{}
		if i == 0 {
			status = "partial"
			gaps = []string{"unanalysed transform"}
		}
		mapping := backendmodel.LineageMappingAttributes{Sources: []backendmodel.LineageValueRef{{Kind: "api_field", NodeID: revisionID}}, Destination: backendmodel.LineageValueRef{Kind: "api_field", NodeID: projectID}, Transform: backendmodel.LineageTransform{Kind: kind, Description: "Declared mapping"}, AnalysisStatus: status, Gaps: gaps}
		raw, err := json.Marshal(mapping)
		if err != nil {
			t.Fatal(err)
		}
		var attrs map[string]jsontext.Value
		if err = json.Unmarshal(raw, &attrs); err != nil {
			t.Fatal(err)
		}
		nodes = append(nodes, backendmodel.Node{ID: fmt.Sprintf("01900000-0000-7000-8000-%012d", i+20), Kind: "field_mapping", Attributes: attrs})
	}
	g := supportedGraph(nodes, nil)
	g.Pins.StructuralSchemaVersion = "4"
	r := newReport(engineInput())
	if err := r.traverse(t.Context(), g, supportedGraph(nil, nil), []ObjectAddress{{RecordType: "node", ID: revisionID}}); err != nil {
		t.Fatal(err)
	}
	terminal, err := r.finish(g, g, nil)
	if err != nil {
		t.Fatal(err)
	}
	unknown, known := false, false
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Affected.ID == projectID {
			unknown = unknown || w.Status == "unknown"
			known = known || w.Status == "confirmed"
		}
	}
	if !unknown || !known || r.gaps["unknown_transform"].Code == "" {
		t.Fatalf("unknown path suppressed independent mapping: unknown=%v supported=%v", unknown, known)
	}
}
func TestAnalysisImpactRemovedEventRouteRetainsBefore(t *testing.T) {
	nodes := []backendmodel.Node{{ID: "message", Kind: "message"}, {ID: "channel", Kind: "channel"}, {ID: "consumer", Kind: "consumer", Attributes: map[string]jsontext.Value{"dispatchStatus": []byte(`"complete"`)}}}
	before := supportedGraph(slices.Clone(nodes), []backendmodel.Edge{{ID: "delivery", Kind: "delivered_to", From: "channel", To: "consumer", Attributes: map[string]jsontext.Value{"messageId": []byte(`"message"`), "deliveryStatus": []byte(`"declared"`)}}})
	after := supportedGraph(slices.Clone(nodes), nil)
	before.Pins.StructuralSchemaVersion = "5"
	after.Pins.StructuralSchemaVersion = "5"
	terminal, err := analyzeGraphs(t.Context(), engineInput(), before, after, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, w := range readRecords[Witness](t, terminal.Snapshot, "witnesses") {
		if w.Seed.ID == "delivery" && w.Affected.ID == "consumer" && w.Side == "before" && w.Steps[0].ID == "delivery" {
			found = true
		}
	}
	if !found {
		t.Fatal("removed route lost before witness")
	}
}
