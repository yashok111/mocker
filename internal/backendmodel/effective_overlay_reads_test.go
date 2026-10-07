package backendmodel

import (
	"encoding/json/jsontext"
	"errors"
	"slices"
	"strings"
	"testing"
)

// review 2026-10-06, F121: a legacy proposal's overlays replace State after
// loadSourceEffectiveGraph already cached the read index, so every index
// consumer (service filters, record proof, intent-origin binding) saw the
// baseline payloads. A record the draft creates must be in the index.
func TestEffectiveLegacyOverlayReadIndexSeesCreatedRecords(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	d, err := r.CreateProposal(t.Context(), base.Project.ID, proposalCreateInput(base, ids, "legacy-index"))
	if err != nil {
		t.Fatal(err)
	}
	applied, err := r.ApplyProposal(t.Context(), base.Project.ID, d.Proposal.ID, proposalApplyInput(t, r, d, "legacy-index-fk", proposalFK(ids)))
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	known := map[string]bool{}
	for _, n := range baseline.State.Nodes {
		known["node\x00"+n.ID] = true
	}
	for _, e := range baseline.State.Edges {
		known["edge\x00"+e.ID] = true
	}
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{Proposal: &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: applied.Revision.ID}})
	if err != nil {
		t.Fatal(err)
	}
	created := []string{}
	for _, n := range graph.State.Nodes {
		if !known["node\x00"+n.ID] {
			created = append(created, "node\x00"+n.ID)
		}
	}
	for _, e := range graph.State.Edges {
		if !known["edge\x00"+e.ID] {
			created = append(created, "edge\x00"+e.ID)
		}
	}
	if len(created) == 0 {
		t.Fatal("fixture: the FK draft created no record")
	}
	index := graph.indexedReads()
	for _, key := range created {
		if _, ok := index.payloads[key]; !ok {
			t.Fatalf("created record %q missing from the effective read index", key)
		}
	}
}

// review 2026-10-06, F110: a full change proposal over a source5 (events)
// baseline reads with StructuralSchemaVersion "6" and NO native source graph,
// so the transitions view's `schema == EventsSchemaVersion || sourceGraph !=
// nil` gate dropped every emits edge the same flow shows at the revision.
func TestEffectiveFullFiveFlowTransitionsKeepEmits(t *testing.T) {
	s := eventsRuntimeState(t)
	s.Nodes[3].Attributes["stepKind"] = jsontext.Value(`"emit"`)
	runtimeQueryAddNode(t, s, 30, "message", 0, nil)
	runtimeQueryAddEdge(t, s, 130, "emits", 4, 30, map[string]any{"channelId": runtimeQueryID(31), "deliveryStatus": "declared"})
	effective := &EffectiveGraphSnapshot{Target: BackendReadTarget{ChangeProposal: &ProposalReadTarget{ProposalID: runtimeQueryID(900), ProposalRevisionID: runtimeQueryID(901)}}, State: *s, Source: &SourceGraphSnapshot{State: *s}, Pins: EffectiveGraphPins{StructuralSchemaVersion: ComposedSchemaVersion, EffectiveSemanticHash: s.Revision.SemanticHash}}
	p, err := projectRuntimeFlowWithEffective(t.Context(), s, nil, effective, FlowQueryInput{RevisionID: s.Revision.ID, View: "transitions", FlowID: runtimeQueryID(3)})
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(p.TransitionItems, func(e Edge) bool { return e.ID == runtimeQueryID(130) }) {
		t.Fatal("full-over-source5 transitions dropped the emits edge")
	}
}

// review 2026-10-06, F115 / F20: a live Flow, Events or Lineage query with a
// legacy proposal target was refused with the saved-view message, which sent
// the caller looking for a saved view that does not exist.
func TestEffectiveAdvancedReadsRefuseLegacyProposalByName(t *testing.T) {
	t.Parallel()
	r, base, ids := effectiveFiveRelationalFixture(t)
	d, err := r.CreateProposal(t.Context(), base.Project.ID, proposalCreateInput(base, ids, "legacy-advanced"))
	if err != nil {
		t.Fatal(err)
	}
	pin := &ProposalReadTarget{ProposalID: d.Proposal.ID, ProposalRevisionID: d.Revision.ID}
	_, flowErr := r.QueryFlow(t.Context(), base.Project.ID, FlowQueryInput{Proposal: pin, View: "entrypoints"})
	_, eventsErr := r.QueryEvents(t.Context(), base.Project.ID, EventsQueryInput{Proposal: pin, View: "routes"})
	_, lineageErr := r.QueryLineage(t.Context(), base.Project.ID, LineageQueryInput{Proposal: pin, Seed: LineageValueRef{Kind: "column", NodeID: ids["column:orders:user_id"], FacetKey: "sql"}, Direction: "forward"})
	for name, err := range map[string]error{"flow": flowErr, "events": eventsErr, "lineage": lineageErr} {
		var fault *FaultError
		if !errors.As(err, &fault) || fault.Status != 422 || fault.Code != "backend_unsupported_scope" {
			t.Fatalf("%s: legacy proposal not refused as unsupported scope: %v", name, err)
		}
		if strings.Contains(fault.Message, "Saved view") || !strings.Contains(fault.Message, "legacy proposal") {
			t.Fatalf("%s: refusal does not name the real limitation: %q", name, fault.Message)
		}
	}
}

// review 2026-10-06, F116: page-level limitations were collected from every
// matching entrypoint and every step before the cursor window was cut, so
// page 1 reported gaps of records shown only on a later page.
func TestRuntimeFlowPageLimitationsBelongToTheWindow(t *testing.T) {
	s := runtimeQueryFixture(t)
	runtimeQueryAddNode(t, s, 40, "http_operation", 0, map[string]any{"method": "GET", "path": "/later"})
	runtimeQueryAddNode(t, s, 41, "flow_step", 3, map[string]any{"stepKind": "query", "analysisStatus": "complete", "gaps": []string{"later step gap"}})
	runtimeQueryAddEdge(t, s, 141, "contains", 3, 41, nil)
	runtimeQueryAddEdge(t, s, 142, "next", 41, 5, nil)
	later := "No imported HTTP handle for " + runtimeQueryID(40)
	first := runtimeQueryPage(t, s, FlowQueryInput{View: "entrypoints", Limit: 1})
	if len(first.EntryPointItems) != 1 || first.EntryPointItems[0].Operation.ID != runtimeQueryID(1) || first.NextCursor == "" {
		t.Fatalf("fixture: unexpected first entrypoint page %+v", first.EntryPointItems)
	}
	if slices.Contains(first.Limitations, later) {
		t.Fatal("entrypoint page 1 carries a limitation of an entrypoint on page 2")
	}
	second := runtimeQueryPage(t, s, FlowQueryInput{View: "entrypoints", Limit: 1, Cursor: first.NextCursor})
	if !slices.Contains(second.Limitations, later) {
		t.Fatal("entrypoint page 2 lost its own limitation")
	}
	stepGap := runtimeQueryID(41) + ": later step gap"
	for _, view := range []string{"steps", "transitions"} {
		page := runtimeQueryPage(t, s, FlowQueryInput{View: view, FlowID: runtimeQueryID(3), Limit: 1})
		if page.NextCursor == "" {
			t.Fatalf("%s fixture: expected more than one page", view)
		}
		shown := slices.ContainsFunc(page.StepItems, func(n Node) bool { return n.ID == runtimeQueryID(41) }) || slices.ContainsFunc(page.TransitionItems, func(e Edge) bool { return e.From == runtimeQueryID(41) })
		if !shown && slices.Contains(page.Limitations, stepGap) {
			t.Fatalf("%s page 1 carries a limitation of a step it does not show", view)
		}
	}
}

// review 2026-10-06, F120: a serviceId naming no service was refused only
// when some record survived the kind/search prefilters, so the same bad
// selector answered 400 or an empty 200 depending on the data.
func TestEffectiveGraphRejectsNonServiceFilterUpFront(t *testing.T) {
	t.Parallel()
	r, base, _ := changeFixture(t)
	graph, err := r.ResolveEffectiveGraph(t.Context(), base.Project.ID, BackendReadTarget{RevisionID: base.Revision.ID})
	if err != nil {
		t.Fatal(err)
	}
	notService := ""
	for _, n := range graph.State.Nodes {
		if n.Kind != "service" {
			notService = n.ID
			break
		}
	}
	if notService == "" {
		t.Fatal("fixture: no non-service node")
	}
	for _, in := range []GraphQueryInput{
		{RevisionID: base.Revision.ID, RecordType: "nodes", ServiceID: notService},
		{RevisionID: base.Revision.ID, RecordType: "nodes", ServiceID: notService, Search: "no record is named like this"},
	} {
		_, err := r.QueryGraph(t.Context(), base.Project.ID, in)
		var fault *FaultError
		if !errors.As(err, &fault) || fault.Status != 400 {
			t.Fatalf("serviceId naming a %q-less record answered %v for %+v", "service", err, in)
		}
	}
}

// review 2026-10-06, F119 (uncertain): when the selected claims cannot be
// computed (here: two providers diverge and no selection picks one), the
// origin came back as a source field with no claims and NO freshness —
// indistinguishable from a proof-less field. It must say its proof is not
// current.
func TestEffectiveSourceOriginMarksUnprovableProofStale(t *testing.T) {
	property := TypedSourcePropertySelector{Kind: "name"}
	one := ProviderAssertion{RecordType: "node", RecordID: "id", AssertionHash: "one", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "one"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "one"}, Freshness: AssertionFreshness{Status: "current"}}
	two := one
	two.AssertionHash, two.Owner.ProviderNamespace, two.Payload.Name = "two", "two", "two"
	source := &SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, Assertions: []ProviderAssertion{one, two}}
	origin := effectiveEvaluationOrigin(source, ChangeEvaluationFieldOrigin{ChangeRecordRef: ChangeRecordRef{RecordType: "node", ID: "id"}, Selector: EffectivePropertySelector{Kind: "source", Source: new(property)}, Origin: EffectiveOrigin{Kind: "source"}})
	if origin.Freshness == nil || origin.Freshness.Status != "stale" || !slices.Contains(origin.Freshness.Reasons, "source_proof_unavailable") {
		t.Fatalf("unprovable source origin looks proof-less instead of stale: %+v", origin)
	}
}

// review 2026-10-06, F111: Flow and Lineage put the source vector and the
// content hash on coverage.source for a source6 read; Events never did, so a
// client could not cross-check the vector it got from Flow on the same pin.
func TestSource6EventsCoverageCarriesSourceVector(t *testing.T) {
	t.Parallel()
	graph := sourceReadFixtureSnapshot(t, eventsQueryState(t))
	page, err := projectEventsWithSource(t.Context(), &graph.State, graph, EventsQueryInput{RevisionID: graph.State.Revision.ID, View: "routes"})
	if err != nil {
		t.Fatal(err)
	}
	if page.Coverage.Source == nil || page.Coverage.Source.SourceVector == nil || page.Coverage.Source.SourceContentHash != graph.SourceContentHash {
		t.Fatalf("events coverage lost the source vector: %+v", page.Coverage.Source)
	}
}
