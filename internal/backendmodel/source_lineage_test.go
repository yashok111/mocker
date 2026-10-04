package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
)

func TestSource6LineageSelectedProofAndRepresentationTraversal(t *testing.T) {
	t.Parallel()
	g := representationChain(t)
	snapshot := &SourceGraphSnapshot{State: RevisionState{Revision: Revision{ID: lineageQueryID(1), ProjectID: lineageQueryID(2), SchemaVersion: ComposedSchemaVersion, SemanticHash: "pinned"}, Nodes: g.Nodes, Edges: g.Edges}, SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, RawEvidence: map[string]jsontext.Value{}}
	for _, n := range g.Nodes {
		a := ProviderAssertion{RecordType: "node", RecordID: n.ID, Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider"}, AssertionHash: n.ID, Payload: SourceAssertionPayload{RecordType: "node", Kind: n.Kind, Name: n.Name, ParentID: n.ParentID, Attributes: n.Attributes}, EvidenceIDs: []string{n.ID}, Freshness: AssertionFreshness{Status: "current"}}
		snapshot.Assertions = append(snapshot.Assertions, a)
		snapshot.Currentness = append(snapshot.Currentness, populateSourceFields(a, sourceCurrentness(a), false, true, ""))
		raw, _ := json.Marshal(Evidence{ID: n.ID, Status: "explicit"})
		snapshot.RawEvidence[n.ID] = raw
	}
	for _, e := range g.Edges {
		a := ProviderAssertion{RecordType: "edge", RecordID: e.ID, Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "provider"}, AssertionHash: e.ID, Payload: SourceAssertionPayload{RecordType: "edge", Kind: e.Kind, From: e.From, To: e.To, Attributes: e.Attributes}, EvidenceIDs: []string{e.ID}, Freshness: AssertionFreshness{Status: "current"}}
		snapshot.Assertions = append(snapshot.Assertions, a)
		snapshot.Currentness = append(snapshot.Currentness, populateSourceFields(a, sourceCurrentness(a), false, true, ""))
		raw, _ := json.Marshal(Evidence{ID: e.ID, Status: "explicit"})
		snapshot.RawEvidence[e.ID] = raw
	}
	seed := LineageValueRef{Kind: "representation_field", NodeID: "00000000-0000-4000-8000-000000000011"}
	in := LineageQueryInput{RevisionID: snapshot.State.Revision.ID, Seed: seed, Direction: "forward"}
	page, err := projectSourceLineage(t.Context(), snapshot, in)
	if err != nil {
		t.Fatal(err)
	}
	if page.Policy != "field-lineage-traversal-source6-v1" || len(page.Items) == 0 {
		t.Fatalf("missing representation traversal: %+v", page)
	}
	for _, item := range page.Items {
		if item.Mapping.Ownership != nil || item.Mapping.Freshness != nil || item.Mapping.ExternalKey != "" {
			t.Fatalf("fictional owner: %+v", item.Mapping)
		}
	}
}
