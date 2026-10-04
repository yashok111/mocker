package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"testing"
	"uuid"
)

func TestSource6SelectedFacetHasNoArbitraryProofWrapper(t *testing.T) {
	shared := uuid.NewV7().String()
	repo := uuid.NewV7().String()
	graph := &SourceGraphSnapshot{SourceVector: &SourceVector{DocumentVersion: "source-vector-v1", Partitions: []SourcePartition{}, Snapshots: []SourceSnapshot{}}, Assertions: []ProviderAssertion{}, RawEvidence: map[string]jsontext.Value{}}
	for _, namespace := range []string{"a", "b"} {
		eid := uuid.NewV7().String()
		snapshot := uuid.NewV7().String()
		nullable := jsontext.Value(`false`)
		if namespace == "b" {
			nullable = jsontext.Value(`true`)
		}
		known := func(raw string) *relationalScalar {
			return &relationalScalar{Status: "known", Value: jsontext.Value(raw)}
		}
		fresh := AssertionFreshness{Status: "current", ConfirmedSnapshotID: snapshot, Reasons: []string{}}
		facet := relationalFacet{relationalFacetCommon: relationalFacetCommon{SourceKind: "sql", Dialect: "postgresql", AnalysisStatus: "complete", Gaps: []string{}, EvidenceIDs: []string{eid}, SourceSnapshotID: snapshot, Freshness: &fresh}, NativeType: known(`"integer"`), TypeFamily: known(`"integer"`), Nullable: &relationalScalar{Status: "known", Value: nullable}, DefaultExpression: known(`null`), GeneratedExpression: known(`null`), Identity: known(`null`), Ordinal: known(`1`)}
		raw, _ := json.Marshal(map[string]relationalFacet{"db": facet})
		proof, _ := json.Marshal(Evidence{ID: eid, SubjectID: shared})
		graph.RawEvidence[eid] = proof
		a := ProviderAssertion{RecordType: "node", RecordID: shared, Owner: AssertionOwnership{RepositoryID: repo, ProviderNamespace: namespace, Profile: RelationalProfile}, ExternalKey: namespace + ":column", Payload: SourceAssertionPayload{RecordType: "node", Kind: "column", Name: "id", Attributes: map[string]jsontext.Value{"facets": raw}}, EvidenceIDs: []string{eid}, DependencyClaims: []SourceDependencyBinding{}, Freshness: fresh}
		hash, err := sourceIntrinsicHash(a, graph.RawEvidence, nil)
		if err != nil {
			t.Fatal(err)
		}
		a.AssertionHash = hash
		graph.Assertions = append(graph.Assertions, a)
		graph.Currentness = append(graph.Currentness, sourceCurrentness(a))
	}
	base := &SourceGraphSnapshot{SourceVector: graph.SourceVector}
	candidate := &composedCandidate{Graph: &graphCandidate{}, Source: graph}
	diagnostics, err := mergeSourceAssertions(base, candidate, nil)
	if err != nil || len(diagnostics) != 1 || len(candidate.Conflicts) != 1 {
		t.Fatalf("expected one nullable conflict: %+v %v", diagnostics, err)
	}
	conflict := candidate.Conflicts[0]
	if conflict.Property != (TypedSourcePropertySelector{Kind: "relational_facet", FacetKey: "db", Group: "nullable"}) {
		t.Fatalf("wrong property: %+v", conflict.Property)
	}
	choice := conflict.Contenders[1]
	resolution := SourceAssertionResolution{DecisionID: uuid.NewV7().String(), RecordType: "node", ID: shared, Property: conflict.Property, ConflictHash: conflict.ConflictHash, Select: SourceAssertionSelection{RepositoryID: choice.Owner.RepositoryID, ProviderNamespace: choice.Owner.ProviderNamespace, AssertionHash: choice.AssertionHash}, Reason: "choose provider b"}
	diagnostics, err = mergeSourceAssertions(base, candidate, []SourceDecision{{State: "active", Command: ImportCommand{Op: "resolve_assertion", Resolution: &resolution}}})
	if err != nil || len(diagnostics) != 0 {
		t.Fatalf("resolve=%+v %v", diagnostics, err)
	}
	facets, _, err := relationalFacetObject("column", candidate.Graph.Nodes[0].Attributes)
	if err != nil {
		t.Fatal(err)
	}
	effective, err := relationalObject(facets["db"])
	if err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"sourceKind", "sourceSnapshotId", "evidenceIds", "freshness"} {
		if _, ok := effective[key]; ok {
			t.Fatalf("effective facet fabricated provider proof member %s", key)
		}
	}
	var scalar relationalScalar
	if err := json.Unmarshal(effective["nullable"], &scalar); err != nil || string(scalar.Value) != "true" {
		t.Fatalf("selected value=%s %v", scalar.Value, err)
	}
	for _, a := range graph.Assertions {
		facets, _, err := relationalFacetObject("column", a.Payload.Attributes)
		if err != nil {
			t.Fatal(err)
		}
		var facet relationalFacet
		if err := json.Unmarshal(facets["db"], &facet); err != nil || facet.SourceSnapshotID == "" || len(facet.EvidenceIDs) != 1 {
			t.Fatal("own claim proof wrapper was lost")
		}
	}
}
