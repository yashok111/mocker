package backendmodel

import (
	"bytes"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
	"testing"
	"time"
)

const (
	sourceContractRepo     = "11111111-1111-4111-8111-111111111111"
	sourceContractNode     = "22222222-2222-4222-8222-222222222222"
	sourceContractOther    = "33333333-3333-4333-8333-333333333333"
	sourceContractProof    = "44444444-4444-4444-8444-444444444444"
	sourceContractSnapshot = "55555555-5555-4555-8555-555555555555"
	sourceContractBase     = "66666666-6666-4666-8666-666666666666"
)

func sourceContractHashGraph(t *testing.T) *SourceGraphSnapshot {
	t.Helper()
	a := ProviderAssertion{
		RecordType: "node", RecordID: sourceContractNode, ExternalKey: "provider:save",
		Owner:       AssertionOwnership{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", Profile: "code-structure-v1"},
		Payload:     SourceAssertionPayload{RecordType: "node", Kind: "handler", Name: "save", Attributes: sourceContractAttrs(t, `{"language":"go","description":"fixture"}`)},
		EvidenceIDs: []string{sourceContractProof}, DependencyClaims: []SourceDependencyBinding{},
		Freshness: AssertionFreshness{Status: "current", ConfirmedSnapshotID: sourceContractSnapshot, Reasons: []string{}}, FieldCurrentness: []TypedFieldCurrentness{},
	}
	g := &SourceGraphSnapshot{
		SourceVector: &SourceVector{DocumentVersion: "source-vector-v1", Partitions: []SourcePartition{{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", SnapshotID: sourceContractSnapshot, ScopeStatus: SourceScopeStatus{Status: "complete", Gaps: []string{}}}}, Snapshots: []SourceSnapshot{}},
		Assertions:   []ProviderAssertion{a}, Selections: []SourceAssertionResolution{}, Currentness: []SourceClaimCurrentness{}, LegacyProofBases: []LegacyProofBasis{},
		RawEvidence: map[string]jsontext.Value{sourceContractProof: jsontext.Value(`{"id":"44444444-4444-4444-8444-444444444444","subjectId":"22222222-2222-4222-8222-222222222222","propertyPath":"/name","source":{"file":"save.go","startLine":7,"endLine":9}}`)},
	}
	g.State.Revision.SchemaVersion = "6"
	g.State.Revision.ID = sourceContractBase
	g.State.Revision.SemanticHash = strings.Repeat("c", 64)
	g.State.Nodes = []Node{{ID: sourceContractNode, Kind: "handler", Name: "save", Attributes: sourceContractAttrs(t, `{"description":"fixture","language":"go"}`), EvidenceIDs: []string{sourceContractProof}}}
	g.State.Edges = []Edge{}
	h, err := sourceIntrinsicHash(a, g.RawEvidence, nil)
	if err != nil {
		t.Fatal(err)
	}
	g.Assertions[0].AssertionHash = h
	return g
}

func TestSource6IntrinsicHashExcludesDependencyAndRevisionCurrentness(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*ProviderAssertion)
	}{
		{"own stale status", func(a *ProviderAssertion) {
			a.Freshness.Status = "stale"
			a.Freshness.Reasons = []string{"file_changed"}
		}},
		{"field dependency currentness", func(a *ProviderAssertion) {
			a.FieldCurrentness = []TypedFieldCurrentness{{Property: TypedSourcePropertySelector{Kind: "name"}, Dependency: AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}}}
		}},
		{"self assertion hash", func(a *ProviderAssertion) { a.AssertionHash = strings.Repeat("f", 64) }},
		{"cyclic dependency hashes", func(a *ProviderAssertion) {
			a.DependencyClaims = []SourceDependencyBinding{{Basis: "candidate", Target: BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "provider:save", ExpectedID: sourceContractNode, AssertionHash: strings.Repeat("b", 64)}, Site: "/parentId", Property: TypedSourcePropertySelector{Kind: "parent"}}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sourceContractHashGraph(t)
			before := g.Assertions[0].AssertionHash
			tc.change(&g.Assertions[0])
			after, err := sourceIntrinsicHash(g.Assertions[0], g.RawEvidence, g.LegacyProofBases)
			if err != nil {
				t.Fatal(err)
			}
			if before != after {
				t.Errorf("intrinsic hash included %s", tc.name)
			}
		})
	}
}

func TestSource6IntrinsicHashBindsOwnPayloadAndExactProof(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*SourceGraphSnapshot)
	}{
		{"owner", func(g *SourceGraphSnapshot) { g.Assertions[0].Owner.ProviderNamespace = "provider-b" }},
		{"external key", func(g *SourceGraphSnapshot) { g.Assertions[0].ExternalKey = "provider:renamed" }},
		{"name", func(g *SourceGraphSnapshot) { g.Assertions[0].Payload.Name = "replace" }},
		{"confirmed snapshot", func(g *SourceGraphSnapshot) { g.Assertions[0].Freshness.ConfirmedSnapshotID = sourceContractOther }},
		{"proof whitespace bytes", func(g *SourceGraphSnapshot) {
			g.RawEvidence[sourceContractProof] = append([]byte(" "), g.RawEvidence[sourceContractProof]...)
		}},
		{"legacy basis", func(g *SourceGraphSnapshot) {
			g.LegacyProofBases = []LegacyProofBasis{{EvidenceID: sourceContractProof, BasisHash: strings.Repeat("d", 64)}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sourceContractHashGraph(t)
			before := g.Assertions[0].AssertionHash
			tc.change(g)
			after, err := sourceIntrinsicHash(g.Assertions[0], g.RawEvidence, g.LegacyProofBases)
			if err != nil {
				t.Fatal(err)
			}
			if before == after {
				t.Errorf("intrinsic hash omitted %s", tc.name)
			}
		})
	}
	g := sourceContractHashGraph(t)
	delete(g.RawEvidence, sourceContractProof)
	if _, err := sourceIntrinsicHash(g.Assertions[0], g.RawEvidence, nil); err == nil {
		t.Error("missing own proof was accepted")
	}
}

func sourceContractMappingGraph(t *testing.T, value LineageValueRef) *SourceGraphSnapshot {
	t.Helper()
	g := sourceContractHashGraph(t)
	a := &g.Assertions[0]
	a.Payload.Kind = "field_mapping"
	raw, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	a.Payload.Attributes = sourceContractAttrs(t, `{"analysisStatus":"complete","gaps":[],"sources":[],"destination":`+string(raw)+`,"transform":{"kind":"constant","description":"literal","redacted":false}}`)
	a.DependencyClaims = []SourceDependencyBinding{{Basis: "base", BaseRevisionID: sourceContractBase, Target: BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "value", ExpectedID: value.NodeID, AssertionHash: strings.Repeat("a", 64)}, Site: "/attributes/destination/nodeId", Property: TypedSourcePropertySelector{Kind: "mapping_destination"}, ValueContext: new(value)}}
	if value.Kind == "event_field" {
		a.DependencyClaims = append(a.DependencyClaims,
			SourceDependencyBinding{Basis: "base", BaseRevisionID: sourceContractBase, Target: BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "endpoint", ExpectedID: value.EndpointID, AssertionHash: strings.Repeat("b", 64)}, Site: "/attributes/destination/endpointId", Property: TypedSourcePropertySelector{Kind: "mapping_destination"}, ValueContext: new(value)},
			SourceDependencyBinding{Basis: "base", BaseRevisionID: sourceContractBase, Target: BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "edge", ExternalKey: "route", ExpectedID: value.RouteID, AssertionHash: strings.Repeat("c", 64)}, Site: "/attributes/destination/routeId", Property: TypedSourcePropertySelector{Kind: "mapping_destination"}, ValueContext: new(value)})
	}
	a.AssertionHash, err = sourceIntrinsicHash(*a, g.RawEvidence, nil)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestSource6ContextBindsDependencyMetadata(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*SourceDependencyBinding)
	}{
		{"basis", func(b *SourceDependencyBinding) { b.Basis = "candidate"; b.BaseRevisionID = "" }},
		{"base revision", func(b *SourceDependencyBinding) { b.BaseRevisionID = sourceContractSnapshot }},
		{"target repository", func(b *SourceDependencyBinding) { b.Target.RepositoryID = sourceContractOther }},
		{"target provider", func(b *SourceDependencyBinding) { b.Target.ProviderNamespace = "provider-b" }},
		{"target key", func(b *SourceDependencyBinding) { b.Target.ExternalKey = "column:other" }},
		{"target hash", func(b *SourceDependencyBinding) { b.Target.AssertionHash = strings.Repeat("d", 64) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sourceContractMappingGraph(t, LineageValueRef{Kind: "column", NodeID: sourceContractOther, FacetKey: "sql"})
			before, err := source6ContextJSON(g)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(&g.Assertions[0].DependencyClaims[0])
			after, err := source6ContextJSON(g)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, after) {
				t.Errorf("context omitted %s", tc.name)
			}
			intrinsic, err := sourceIntrinsicHash(g.Assertions[0], g.RawEvidence, nil)
			if err != nil {
				t.Fatal(err)
			}
			if intrinsic != g.Assertions[0].AssertionHash {
				t.Errorf("binding-only %s entered intrinsic hash", tc.name)
			}
		})
	}
}

func TestSource6ContextRejectsBindingPayloadMismatch(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*ProviderAssertion)
	}{
		{"missing binding", func(a *ProviderAssertion) { a.DependencyClaims = nil }},
		{"duplicate binding", func(a *ProviderAssertion) { a.DependencyClaims = append(a.DependencyClaims, a.DependencyClaims[0]) }},
		{"wrong record type", func(a *ProviderAssertion) { a.DependencyClaims[0].Target.RecordType = "edge" }},
		{"wrong UUID", func(a *ProviderAssertion) { a.DependencyClaims[0].Target.ExpectedID = sourceContractNode }},
		{"fabricated site", func(a *ProviderAssertion) { a.DependencyClaims[0].Site = "/attributes/notAReference" }},
		{"wrong property", func(a *ProviderAssertion) {
			a.DependencyClaims[0].Property = TypedSourcePropertySelector{Kind: "mapping_sources"}
		}},
		{"missing context", func(a *ProviderAssertion) { a.DependencyClaims[0].ValueContext = nil }},
		{"wrong facet", func(a *ProviderAssertion) { a.DependencyClaims[0].ValueContext.FacetKey = "orm" }},
		{"wrong context kind", func(a *ProviderAssertion) {
			a.DependencyClaims[0].ValueContext = &LineageValueRef{Kind: "port", NodeID: sourceContractOther, Collection: "outputs", PortKey: "result"}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sourceContractMappingGraph(t, LineageValueRef{Kind: "column", NodeID: sourceContractOther, FacetKey: "sql"})
			if _, err := source6ContextJSON(g); err != nil {
				t.Fatalf("valid baseline: %v", err)
			}
			tc.change(&g.Assertions[0])
			if _, err := source6ContextJSON(g); err == nil {
				t.Errorf("accepted %s", tc.name)
			}
		})
	}
}

func TestSource6ContextualValueChangesBindIntrinsicAndContext(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name          string
		before, after LineageValueRef
	}{
		{"node", LineageValueRef{Kind: "column", NodeID: sourceContractOther, FacetKey: "sql"}, LineageValueRef{Kind: "column", NodeID: sourceContractNode, FacetKey: "sql"}},
		{"facet", LineageValueRef{Kind: "column", NodeID: sourceContractOther, FacetKey: "sql"}, LineageValueRef{Kind: "column", NodeID: sourceContractOther, FacetKey: "orm"}},
		{"collection", LineageValueRef{Kind: "port", NodeID: sourceContractOther, Collection: "inputs", PortKey: "same"}, LineageValueRef{Kind: "port", NodeID: sourceContractOther, Collection: "outputs", PortKey: "same"}},
		{"port", LineageValueRef{Kind: "port", NodeID: sourceContractOther, Collection: "inputs", PortKey: "a"}, LineageValueRef{Kind: "port", NodeID: sourceContractOther, Collection: "inputs", PortKey: "b"}},
		{"endpoint", LineageValueRef{Kind: "event_field", NodeID: sourceContractOther, EndpointID: sourceContractNode, RouteID: sourceContractSnapshot}, LineageValueRef{Kind: "event_field", NodeID: sourceContractOther, EndpointID: sourceContractRepo, RouteID: sourceContractSnapshot}},
		{"route", LineageValueRef{Kind: "event_field", NodeID: sourceContractOther, EndpointID: sourceContractNode, RouteID: sourceContractSnapshot}, LineageValueRef{Kind: "event_field", NodeID: sourceContractOther, EndpointID: sourceContractNode, RouteID: sourceContractBase}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			left := sourceContractMappingGraph(t, tc.before)
			right := sourceContractMappingGraph(t, tc.after)
			if left.Assertions[0].AssertionHash == right.Assertions[0].AssertionHash {
				t.Errorf("intrinsic omitted %s", tc.name)
			}
			before, err := source6ContextJSON(left)
			if err != nil {
				t.Fatal(err)
			}
			after, err := source6ContextJSON(right)
			if err != nil {
				t.Fatal(err)
			}
			if bytes.Equal(before, after) {
				t.Errorf("context omitted %s", tc.name)
			}
		})
	}
}

func TestSource6ContextAndSemanticHashDomains(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		change  func(*SourceGraphSnapshot)
		changed bool
	}{
		{"owning revision", func(g *SourceGraphSnapshot) { g.State.Revision.ID = sourceContractOther }, false},
		{"stored semantic anchor", func(g *SourceGraphSnapshot) { g.State.Revision.SemanticHash = strings.Repeat("a", 64) }, false},
		{"stored content anchor", func(g *SourceGraphSnapshot) { g.SourceContentHash = strings.Repeat("e", 64) }, false},
		{"attribute map order", func(g *SourceGraphSnapshot) {
			g.Assertions[0].Payload.Attributes = sourceContractAttrs(t, `{"description":"fixture","language":"go"}`)
		}, false},
		{"empty active partition", func(g *SourceGraphSnapshot) {
			g.SourceVector.Partitions = append(g.SourceVector.Partitions, SourcePartition{RepositoryID: sourceContractOther, ProviderNamespace: "provider-b", SnapshotID: sourceContractOther, ScopeStatus: SourceScopeStatus{Status: "complete", Gaps: []string{}}})
		}, true},
		{"partition availability", func(g *SourceGraphSnapshot) {
			g.SourceVector.Partitions[0].ScopeStatus = SourceScopeStatus{Status: "partial", Gaps: []string{"unavailable"}}
		}, true},
		{"dependency currentness", func(g *SourceGraphSnapshot) {
			g.Currentness = []SourceClaimCurrentness{{RecordType: "node", RecordID: sourceContractNode, RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", Dependency: AssertionFreshness{Status: "stale", Reasons: []string{"dependency_changed"}}}}
		}, true},
		{"losing assertion", func(g *SourceGraphSnapshot) {
			a := g.Assertions[0]
			a.Owner.ProviderNamespace = "provider-b"
			a.ExternalKey = "other:save"
			a.Payload.Name = "replace"
			g.Assertions = append(g.Assertions, a)
		}, true},
		{"legacy basis document", func(g *SourceGraphSnapshot) {
			g.LegacyProofBases = []LegacyProofBasis{{EvidenceID: sourceContractProof, BasisHash: strings.Repeat("d", 64), Support: "historical_metadata"}}
		}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			g := sourceContractHashGraph(t)
			before, err := source6SemanticHash(g)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(g)
			after, err := source6SemanticHash(g)
			if err != nil {
				t.Fatal(err)
			}
			if (before != after) != tc.changed {
				t.Errorf("semantic hash changed=%v, want %v for %s", before != after, tc.changed, tc.name)
			}
		})
	}
}

func TestSource6ContextSortsUnorderedCollectionsWithoutMutating(t *testing.T) {
	t.Parallel()
	g := sourceContractHashGraph(t)
	a := g.Assertions[0]
	a.RecordID = sourceContractOther
	a.ExternalKey = "other:save"
	g.Assertions = append(g.Assertions, a)
	g.SourceVector.Partitions = append(g.SourceVector.Partitions, SourcePartition{RepositoryID: sourceContractOther, ProviderNamespace: "provider-z"})
	g.State.Nodes = append(g.State.Nodes, Node{ID: sourceContractOther, Kind: "handler", Name: "other"})
	first, err := source6ContextJSON(g)
	if err != nil {
		t.Fatal(err)
	}
	slices.Reverse(g.Assertions)
	slices.Reverse(g.SourceVector.Partitions)
	slices.Reverse(g.State.Nodes)
	inputFirst := g.Assertions[0].RecordID
	second, err := source6ContextJSON(g)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(first, second) {
		t.Error("context depended on unordered collection traversal")
	}
	if g.Assertions[0].RecordID != inputFirst {
		t.Error("hashing sorted caller assertions in place")
	}
}

func TestSource6CandidateHashBindsSessionInputs(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		change  func(*ImportSession, *composedCandidate)
		changed bool
	}{
		{"project", func(s *ImportSession, _ *composedCandidate) { s.ProjectID = sourceContractOther }, true},
		{"base revision", func(s *ImportSession, _ *composedCandidate) { s.BaseRevisionID = sourceContractOther }, true},
		{"base semantic hash", func(_ *ImportSession, c *composedCandidate) {
			c.Source.State.Revision.SemanticHash = strings.Repeat("d", 64)
		}, true},
		{"base vector", func(s *ImportSession, _ *composedCandidate) { s.BaseVectorHash = strings.Repeat("f", 64) }, true},
		{"import version", func(s *ImportSession, _ *composedCandidate) { s.Version++ }, true},
		{"scope", func(s *ImportSession, _ *composedCandidate) { s.SourceScope.Kind = "add_provider" }, true},
		{"policy", func(s *ImportSession, _ *composedCandidate) { s.SyncPolicy = "incremental-source-v1" }, true},
		{"extension", func(s *ImportSession, _ *composedCandidate) {
			s.ProfileExtension = &ImportProfileExtension{FromProfile: "events-service-v1", ToProfile: "composed-source-v1"}
		}, true},
		{"decision", func(_ *ImportSession, c *composedCandidate) {
			c.Decisions = []SourceDecision{{Sequence: 1, State: "active", Command: ImportCommand{Op: "resolve_assertion", Resolution: &SourceAssertionResolution{DecisionID: sourceContractOther, Reason: "own proof"}}}}
		}, true},
		{"request timestamp", func(s *ImportSession, _ *composedCandidate) { s.UpdatedAt = time.Unix(200, 0) }, false},
		{"batch counter", func(s *ImportSession, _ *composedCandidate) { s.AcceptedBatchCount++ }, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s := &ImportSession{ProjectID: sourceContractRepo, BaseRevisionID: sourceContractBase, BaseVectorHash: strings.Repeat("e", 64), Version: 3, SourceScope: &SourceScope{Kind: "reconcile", RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a"}, ScopeStatus: &SourceScopeStatus{Status: "complete", Gaps: []string{}}, SyncPolicy: WholeSourcePolicy}
			c := &composedCandidate{Source: sourceContractHashGraph(t), Decisions: []SourceDecision{}}
			before, err := source6CandidateHash(s, c)
			if err != nil {
				t.Fatal(err)
			}
			tc.change(s, c)
			after, err := source6CandidateHash(s, c)
			if err != nil {
				t.Fatal(err)
			}
			if (before != after) != tc.changed {
				t.Errorf("candidate changed=%v, want %v", before != after, tc.changed)
			}
		})
	}
}

func TestSource6DependencyBindingRoundTrip(t *testing.T) {
	t.Parallel()
	for _, context := range []LineageValueRef{
		{Kind: "column", NodeID: sourceContractNode, FacetKey: "sql/orm~1"},
		{Kind: "port", NodeID: sourceContractNode, Collection: "outputs", PortKey: "result/key"},
		{Kind: "event_field", NodeID: sourceContractNode, EndpointID: sourceContractOther, RouteID: sourceContractSnapshot},
	} {
		for _, basis := range []string{"candidate", "base"} {
			t.Run(context.Kind+"/"+basis, func(t *testing.T) {
				binding := SourceDependencyBinding{Basis: basis, Target: BaseAssertionRef{RepositoryID: sourceContractRepo, ProviderNamespace: "provider-a", RecordType: "node", ExternalKey: "field", ExpectedID: sourceContractNode, AssertionHash: strings.Repeat("a", 64)}, Site: "/attributes/sources/0/nodeId", Property: TypedSourcePropertySelector{Kind: "mapping_sources"}, ValueContext: &context}
				if basis == "base" {
					binding.BaseRevisionID = sourceContractBase
				}
				raw, err := json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				var got SourceDependencyBinding
				if err := json.Unmarshal(raw, &got); err != nil {
					t.Fatal(err)
				}
				if got.Basis != basis || got.BaseRevisionID != binding.BaseRevisionID || got.Target != binding.Target || got.Site != "/attributes/sources/0/nodeId" || got.Property != (TypedSourcePropertySelector{Kind: "mapping_sources"}) || got.ValueContext == nil || *got.ValueContext != context {
					t.Errorf("lost exact binding members: %+v", got)
				}
				if basis == "base" {
					binding.BaseRevisionID = ""
				} else {
					binding.BaseRevisionID = sourceContractBase
				}
				raw, err = json.Marshal(binding)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.Unmarshal(raw, &got); err == nil {
					t.Error("accepted forbidden basis/baseRevisionId combination")
				}
			})
		}
	}
}
