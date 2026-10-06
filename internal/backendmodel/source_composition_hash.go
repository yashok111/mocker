package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
	"strings"
)

func sourceDomainHash(domain string, value any) (string, error) {
	b, err := canonicalJSON(struct {
		Domain string `json:"domain"`
		Value  any    `json:"value"`
	}{domain, value})
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}

func sourceIntrinsicHash(a ProviderAssertion, proof map[string]jsontext.Value, bases []LegacyProofBasis) (string, error) {
	proofs := map[string]string{}
	for _, eid := range a.EvidenceIDs {
		raw, ok := proof[eid]
		if !ok {
			return "", semantic("evidence", "Assertion proof is missing")
		}
		proofs[eid] = string(raw)
	}
	basisHashes := []string{}
	for _, basis := range bases {
		if slices.Contains(a.EvidenceIDs, basis.EvidenceID) {
			basisHashes = append(basisHashes, basis.BasisHash)
		}
	}
	slices.Sort(basisHashes)
	return sourceDomainHash("backend-source6-assertion-v1", struct {
		Schema      string                 `json:"schema"`
		RecordType  string                 `json:"recordType"`
		RecordID    string                 `json:"recordId"`
		Owner       AssertionOwnership     `json:"owner"`
		ExternalKey string                 `json:"externalKey"`
		Payload     SourceAssertionPayload `json:"payload"`
		Proof       map[string]string      `json:"proof"`
		SnapshotID  string                 `json:"confirmedSnapshotId"`
		Legacy      []string               `json:"legacyBasisHashes"`
	}{ComposedSchemaVersion, a.RecordType, a.RecordID, a.Owner, a.ExternalKey, a.Payload, proofs, a.Freshness.ConfirmedSnapshotID, basisHashes})
}

func source6ContextJSON(graph *SourceGraphSnapshot) ([]byte, error) {
	for _, a := range graph.Assertions {
		if err := validateSourceBindings(a); err != nil {
			return nil, err
		}
	}
	if graph.SourceVector == nil {
		return nil, semantic("sourceVector", "Source6 requires full vector")
	}
	vector := *graph.SourceVector
	vector.Partitions = slices.Clone(vector.Partitions)
	vector.Snapshots = slices.Clone(vector.Snapshots)
	slices.SortFunc(vector.Partitions, func(a, b SourcePartition) int {
		return strings.Compare(a.RepositoryID+"\x00"+a.ProviderNamespace, b.RepositoryID+"\x00"+b.ProviderNamespace)
	})
	slices.SortFunc(vector.Snapshots, func(a, b SourceSnapshot) int { return strings.Compare(a.ID, b.ID) })
	assertions := slices.Clone(graph.Assertions)
	for i := range assertions {
		assertions[i].DependencyClaims = slices.Clone(assertions[i].DependencyClaims)
		slices.SortFunc(assertions[i].DependencyClaims, func(a, b SourceDependencyBinding) int { return strings.Compare(a.Site, b.Site) })
	}
	slices.SortFunc(assertions, func(a, b ProviderAssertion) int { return strings.Compare(sourceAssertionKey(a), sourceAssertionKey(b)) })
	selections := slices.Clone(graph.Selections)
	slices.SortFunc(selections, func(a, b SourceAssertionResolution) int {
		return strings.Compare(a.RecordType+a.ID+sourcePropertyKey(a.Property), b.RecordType+b.ID+sourcePropertyKey(b.Property))
	})
	current := slices.Clone(graph.Currentness)
	slices.SortFunc(current, func(a, b SourceClaimCurrentness) int {
		return strings.Compare(sourceClaimKey(a.RecordType, a.RecordID, a.RepositoryID, a.ProviderNamespace), sourceClaimKey(b.RecordType, b.RecordID, b.RepositoryID, b.ProviderNamespace))
	})
	bases := slices.Clone(graph.LegacyProofBases)
	slices.SortFunc(bases, func(a, b LegacyProofBasis) int {
		return strings.Compare(a.EvidenceID+a.BasisHash, b.EvidenceID+b.BasisHash)
	})
	nodes := slices.Clone(graph.State.Nodes)
	edges := slices.Clone(graph.State.Edges)
	slices.SortFunc(nodes, func(a, b Node) int { return strings.Compare(a.ID, b.ID) })
	slices.SortFunc(edges, func(a, b Edge) int { return strings.Compare(a.ID, b.ID) })
	proofs := map[string]string{}
	for id, raw := range graph.RawEvidence {
		proofs[id] = string(raw)
	}
	return canonicalJSON(struct {
		Domain      string                      `json:"domain"`
		Schema      string                      `json:"schema"`
		Vector      SourceVector                `json:"sourceVector"`
		Assertions  []ProviderAssertion         `json:"assertions"`
		Selections  []SourceAssertionResolution `json:"selections"`
		Currentness []SourceClaimCurrentness    `json:"currentness"`
		Identities  []QualifiedSourceIdentity   `json:"identities"`
		Legacy      []LegacyProofBasis          `json:"legacyProofBases"`
		Evidence    map[string]string           `json:"evidence"`
		Nodes       []Node                      `json:"nodes"`
		Edges       []Edge                      `json:"edges"`
	}{"backend-source6-context-v1", ComposedSchemaVersion, vector, assertions, selections, current, sourceIdentities(assertions), bases, proofs, nodes, edges})
}

func source6SemanticHash(graph *SourceGraphSnapshot) (string, error) {
	b, err := source6ContextJSON(graph)
	if err != nil {
		return "", err
	}
	content := hashBytes(b)
	anchor, err := sourceDomainHash("backend-source6-semantic-v1", struct {
		Schema  string `json:"schema"`
		Content string `json:"sourceContentHash"`
	}{ComposedSchemaVersion, content})
	if err != nil {
		return "", err
	}
	if c := graph.State.ArtifactContextV3; c != nil {
		if c.SourceContentHash != content || c.SourceSemanticHash != anchor {
			return "", invalid("context", "V3 source anchors differ from source graph")
		}
		return ArtifactContextV3SemanticHash(*c)
	}
	if c := graph.State.ArtifactContext; c != nil {
		return ArtifactSemanticHash(content, anchor, graph.State.Revision.ArtifactPins, c.APIBindings, c.EditorBindings)
	}
	if c := graph.State.APIArtifactContext; c != nil {
		return APIArtifactSemanticHash(content, anchor, graph.State.Revision.ArtifactPins, c.Bindings)
	}
	return anchor, nil
}

func source6CandidateJSON(s *ImportSession, c *composedCandidate) ([]byte, error) {
	hash, err := source6SemanticHash(c.Source)
	if err != nil {
		return nil, err
	}
	return canonicalJSON(struct {
		Domain          string                    `json:"domain"`
		ProjectID       string                    `json:"projectId"`
		BaseRevisionID  string                    `json:"baseRevisionId"`
		BaseHash        string                    `json:"baseHash"`
		BaseVectorHash  string                    `json:"baseVectorHash"`
		Version         int64                     `json:"version"`
		Scope           *SourceScope              `json:"scope"`
		ScopeStatus     *SourceScopeStatus        `json:"scopeStatus"`
		ChangeManifest  *ChangeManifest           `json:"changeManifest,omitzero"`
		AffectedScope   *IncrementalAffectedScope `json:"affectedScope,omitzero"`
		Policy          string                    `json:"syncPolicy"`
		Extension       *ImportProfileExtension   `json:"profileExtension,omitzero"`
		SemanticHash    string                    `json:"semanticHash"`
		Decisions       []SourceDecision          `json:"decisions"`
		LegacyDecisions []ImportCommand           `json:"legacyDecisions"`
		Batches         []SourceBatchCommitment   `json:"batches"`
	}{"backend-source6-candidate-v1", s.ProjectID, s.BaseRevisionID, c.Source.State.Revision.SemanticHash, s.BaseVectorHash, s.Version, s.SourceScope, s.ScopeStatus, s.ChangeManifest, c.IncrementalScope, s.SyncPolicy, s.ProfileExtension, hash, c.Decisions, c.LegacyDecisions, c.BatchCommitments})
}
func source6CandidateHash(s *ImportSession, c *composedCandidate) (string, error) {
	b, err := source6CandidateJSON(s, c)
	if err != nil {
		return "", err
	}
	return hashBytes(b), nil
}

func source6ArtifactBytes(g *graphCandidate) (int, error) {
	total := 0
	if len(g.ArtifactPins) > 0 {
		raw, err := json.Marshal(g.ArtifactPins)
		if err != nil {
			return 0, err
		}
		total += len(raw)
	}
	if g.ArtifactContextV3 != nil {
		raw, err := EncodeArtifactContextV3(*g.ArtifactContextV3)
		if err != nil {
			return 0, err
		}
		total += len(raw)
	} else if g.ArtifactContext != nil {
		raw, err := EncodeArtifactContext(*g.ArtifactContext, g.ArtifactPins)
		if err != nil {
			return 0, err
		}
		total += len(raw)
	} else if g.APIArtifactContext != nil {
		raw, err := json.Marshal(g.APIArtifactContext)
		if err != nil {
			return 0, err
		}
		total += len(raw)
	}
	return total, nil
}

func validateSourceDependencyBinding(binding SourceDependencyBinding) error {
	if binding.Basis != "candidate" && binding.Basis != "base" || binding.Basis == "candidate" && binding.BaseRevisionID != "" || binding.Basis == "base" && !ValidID(binding.BaseRevisionID) {
		return semantic("dependencyClaims", "Invalid dependency basis")
	}
	if err := validateBaseAssertionRef(binding.Target); err != nil {
		return err
	}
	if !strings.HasPrefix(binding.Site, "/") {
		return semantic("dependencyClaims.site", "Canonical site required")
	}
	return validateSourceSelector(binding.Property)
}
func (b *SourceDependencyBinding) UnmarshalJSON(raw []byte) error {
	type plain SourceDependencyBinding
	var value plain
	if err := json.Unmarshal(raw, &value, json.RejectUnknownMembers(true)); err != nil {
		return err
	}
	if err := validateSourceDependencyBinding(SourceDependencyBinding(value)); err != nil {
		return err
	}
	*b = SourceDependencyBinding(value)
	return nil
}
