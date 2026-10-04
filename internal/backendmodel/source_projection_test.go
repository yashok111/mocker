package backendmodel

import (
	"encoding/json/jsontext"
	"testing"
)

func TestSourceSelectedPropertyProofRejectsLosingAndMetadataClaims(t *testing.T) {
	t.Parallel()
	property := TypedSourcePropertySelector{Kind: "name"}
	selected := ProviderAssertion{RecordType: "node", RecordID: "id", AssertionHash: "selected", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "selected"}, Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "selected"}, EvidenceIDs: []string{"selected-proof"}, Freshness: AssertionFreshness{Status: "current"}}
	losing := selected
	losing.AssertionHash = "losing"
	losing.Owner.ProviderNamespace = "losing"
	losing.Payload.Name = "losing"
	losing.EvidenceIDs = []string{"losing-proof"}
	graph := &SourceGraphSnapshot{Assertions: []ProviderAssertion{losing, selected}, Selections: []SourceAssertionResolution{{RecordType: "node", ID: "id", Property: property, Select: SourceAssertionSelection{RepositoryID: "repo", ProviderNamespace: "selected", AssertionHash: "selected"}}}, RawEvidence: map[string]jsontext.Value{"selected-proof": jsontext.Value(`{"id":"selected-proof","status":"explicit"}`), "losing-proof": jsontext.Value(`{"id":"losing-proof","status":"stale"}`)}}
	selectedCurrent := sourceCurrentness(selected)
	selectedCurrent.Fields = []TypedFieldCurrentness{{Property: property, Own: selected.Freshness, Dependency: AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}}}
	graph.Currentness = []SourceClaimCurrentness{selectedCurrent}
	p, err := sourcePropertyProof(graph, "node", "id", property)
	if err != nil {
		t.Fatal(err)
	}
	if p.status != "stale" || !p.boundary || !p.reasons["dependency_changed"] || p.reasons["stale_evidence"] {
		t.Fatalf("not selected field proof: %+v", p)
	}
	graph.Currentness = nil
	graph.LegacyProofBases = []LegacyProofBasis{{EvidenceID: "selected-proof", Support: "historical_metadata"}}
	graph.proofIndex = nil
	p, err = sourcePropertyProof(graph, "node", "id", property)
	if err != nil {
		t.Fatal(err)
	}
	if p.status == "explicit" || !p.reasons["missing_semantic_support"] {
		t.Fatalf("metadata promoted: %+v", p)
	}
}

func TestSourceProofMissingCurrentnessRemainsUnknown(t *testing.T) {
	t.Parallel()
	a := ProviderAssertion{RecordType: "node", RecordID: "id", AssertionHash: "hash", Payload: SourceAssertionPayload{RecordType: "node", Kind: "system", Name: "system"}, EvidenceIDs: []string{"proof"}, Freshness: AssertionFreshness{Status: "current"}}
	graph := &SourceGraphSnapshot{Assertions: []ProviderAssertion{a}, RawEvidence: map[string]jsontext.Value{"proof": jsontext.Value(`{"id":"proof","status":"explicit"}`)}}
	proof, err := sourcePropertyProof(graph, "node", "id", TypedSourcePropertySelector{Kind: "name"})
	if err != nil {
		t.Fatal(err)
	}
	if proof.status == "explicit" || !proof.boundary || !proof.reasons["currentness_missing"] {
		t.Fatalf("missing sidecar inferred current: %+v", proof)
	}
}

func TestSourceEvidencePageProvenanceIsBoundedToExactOwners(t *testing.T) {
	t.Parallel()
	selected := ProviderAssertion{RecordType: "node", RecordID: "id", Owner: AssertionOwnership{RepositoryID: "repo", ProviderNamespace: "selected"}, AssertionHash: "selected", EvidenceIDs: []string{"selected-proof"}}
	losing := selected
	losing.Owner.ProviderNamespace = "losing"
	losing.AssertionHash = "losing"
	losing.EvidenceIDs = []string{"losing-proof"}
	graph := &SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1"}, Assertions: []ProviderAssertion{selected, losing}, LegacyProofBases: []LegacyProofBasis{{EvidenceID: "selected-proof", Support: "legacy_record"}, {EvidenceID: "losing-proof", Support: "historical_metadata"}}}
	graph.Identities = sourceIdentities(graph.Assertions)
	projection := sourceEvidenceReadContext(graph, []string{"selected-proof"})
	if len(projection.AssertionRefs) != 1 || projection.AssertionRefs[0].ProviderNamespace != "selected" || len(projection.Identities) != 1 || projection.Identities[0].ProviderNamespace != "selected" || len(projection.LegacyProofBases) != 1 || projection.LegacyProofBases[0].EvidenceID != "selected-proof" {
		t.Fatalf("page leaked unrelated claim provenance: %+v", projection)
	}
	empty := sourceEvidenceReadContext(graph, nil)
	if len(empty.AssertionRefs) != 0 || len(empty.Currentness) != 0 || len(empty.Identities) != 0 || len(empty.LegacyProofBases) != 0 || empty.SourceVector == nil {
		t.Fatalf("empty proof page lost vector or leaked provenance: %+v", empty)
	}
}
