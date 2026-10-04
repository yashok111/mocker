package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"testing"
)

func sourceReadFixtureSnapshot(t *testing.T, state *RevisionState) *SourceGraphSnapshot {
	t.Helper()
	out := &SourceGraphSnapshot{State: *state, SourceVector: &SourceVector{DocumentVersion: "source-vector-v1", Snapshots: state.Sources}, RawEvidence: map[string]jsontext.Value{}}
	out.State.Revision.SchemaVersion = ComposedSchemaVersion
	out.State.Nodes = slices.Clone(state.Nodes)
	out.State.Edges = slices.Clone(state.Edges)
	add := func(typ, id string, payload SourceAssertionPayload, ids []string) {
		a := ProviderAssertion{RecordType: typ, RecordID: id, AssertionHash: id, Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider"}, Payload: payload, EvidenceIDs: ids, Freshness: AssertionFreshness{Status: "current"}}
		out.Assertions = append(out.Assertions, a)
		out.Currentness = append(out.Currentness, populateSourceFields(a, sourceCurrentness(a), false, true, ""))
	}
	for i, n := range out.State.Nodes {
		add("node", n.ID, sourceNodePayload(n), n.EvidenceIDs)
		out.State.Nodes[i].Ownership = nil
		out.State.Nodes[i].Freshness = nil
		out.State.Nodes[i].ExternalKey = ""
	}
	for i, e := range out.State.Edges {
		add("edge", e.ID, sourceEdgePayload(e), e.EvidenceIDs)
		out.State.Edges[i].Ownership = nil
		out.State.Edges[i].Freshness = nil
		out.State.Edges[i].ExternalKey = ""
	}
	for _, e := range state.Evidence {
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		out.RawEvidence[e.ID] = raw
	}
	return out
}
func sourceReadMarkPropertyDependency(t *testing.T, graph *SourceGraphSnapshot, typ, id, path string) {
	t.Helper()
	for _, a := range graph.Assertions {
		if a.RecordType != typ || a.RecordID != id {
			continue
		}
		property := sourcePropertyForPointer(a.Payload, path)
		if property == nil {
			t.Fatalf("no typed property for %s", path)
		}
		for i := range graph.Currentness {
			current := &graph.Currentness[i]
			if current.RecordType == typ && current.RecordID == id {
				for j := range current.Fields {
					if current.Fields[j].Property == *property {
						current.Fields[j].Dependency = AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}
						return
					}
				}
			}
		}
	}
	t.Fatal("missing exact property currentness")
}
func TestSource6EventsExactRouteDependencyBoundary(t *testing.T) {
	t.Parallel()
	graph := sourceReadFixtureSnapshot(t, eventsQueryState(t))
	input := EventsQueryInput{RevisionID: graph.State.Revision.ID, View: "routes"}
	before, err := projectEventsWithSource(t.Context(), &graph.State, graph, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(before.Items) != 1 || before.Items[0].Route == nil || before.Items[0].Route.Witness.Status != "explicit" {
		t.Fatalf("fresh source6 route: %+v", before)
	}
	sourceReadMarkPropertyDependency(t, graph, "edge", runtimeQueryID(101), "/attributes/messageId")
	after, err := projectEventsWithSource(t.Context(), &graph.State, graph, input)
	if err != nil {
		t.Fatal(err)
	}
	if len(after.Items) != 1 || after.Items[0].Boundary == nil || after.Items[0].Boundary.Witness.Status != "stale" || !slices.Contains(after.Items[0].Boundary.Witness.Limitations, "dependency_changed") {
		t.Fatalf("fresh route proof hid stale dependency: %+v", after.Items[0])
	}
	if after.Items[0].Boundary.References.EmitsEdgeID != runtimeQueryID(100) || after.Items[0].Boundary.References.DeliveryEdgeID != runtimeQueryID(101) {
		t.Fatal("route context changed")
	}
}
func TestSource6FlowAccessUsesPropertyCurrentness(t *testing.T) {
	t.Parallel()
	graph := sourceReadFixtureSnapshot(t, runtimeQueryFixture(t))
	sourceReadMarkPropertyDependency(t, graph, "edge", runtimeQueryID(113), "/to")
	page, err := projectRuntimeFlowWithSource(t.Context(), &graph.State, graph, FlowQueryInput{RevisionID: graph.State.Revision.ID, View: "accesses", EntrypointID: runtimeQueryID(1)})
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, item := range page.AccessItems {
		if item.AccessEdgeID == runtimeQueryID(113) {
			found = true
			if item.Status != "stale" || !slices.Contains(item.Limitations, "dependency_changed") {
				t.Fatalf("access lost source property currentness: %+v", item)
			}
		}
	}
	if !found {
		t.Fatal("declared access missing")
	}
}
