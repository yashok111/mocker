package backendmodel

import (
	"context"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

func portableSourceGraph(s *PortableSource) (*SourceGraphSnapshot, error) {
	state := RevisionState{Revision: s.Revision, Nodes: s.Nodes, Edges: s.Edges, Evidence: s.Evidence, Sources: s.Coverage.Snapshots, Inventory: s.Coverage.Inventory}
	if len(s.ArtifactContext) > 0 {
		c, err := DecodeVersionedArtifactContext(s.ArtifactContext, s.Revision.ArtifactPins)
		if err != nil {
			return nil, err
		}
		state.ArtifactContext, state.ArtifactContextV3 = c.Legacy, c.V3
		state.APIArtifactContext = legacyArtifactContext(c.Legacy)
	}
	raw := s.RawEvidence
	if raw == nil {
		raw = map[string]jsontext.Value{}
		for _, e := range s.Evidence {
			b, err := json.Marshal(e)
			if err != nil {
				return nil, err
			}
			raw[e.ID] = b
		}
	}
	return &SourceGraphSnapshot{State: state, SourceVector: s.SourceVector, Assertions: s.Assertions, Selections: s.Selections, Currentness: s.Currentness, LegacyProofBases: s.LegacyProofBases, RawEvidence: raw, SourceContentHash: s.SourceContentHash, Identities: sourceIdentities(s.Assertions), rawAssertions: map[string]jsontext.Value{}}, nil
}

func ValidatePortableSource(ctx context.Context, s *PortableSource) error {
	if err := validatePortableSourceShape(ctx, s); err != nil {
		return err
	}
	graph, err := portableSourceGraph(s)
	if err != nil {
		return err
	}
	sources, err := portableSourceSnapshots(s.Coverage.Snapshots)
	if err != nil {
		return err
	}
	records, err := portableSourceRecords(s)
	if err != nil {
		return err
	}
	proofs, err := validatePortableSourceEvidence(s.Evidence, sources, records)
	if err != nil {
		return err
	}
	if err := validatePortableProofClosure(records, proofs); err != nil {
		return err
	}
	if s.Revision.SchemaVersion != ComposedSchemaVersion {
		return nil
	}
	if err := validateComposedPortableClaims(s, graph); err != nil {
		return err
	}
	return validateComposedPortableHashes(s, graph)
}

// validatePortableSourceShape runs the checks that need no graph: identity,
// record and byte limits, and the structural graph rules.
func validatePortableSourceShape(ctx context.Context, s *PortableSource) error {
	if !ValidID(s.Revision.ID) || !ValidID(s.Revision.ProjectID) || !slices.Contains(SupportedModelSchemaVersions(), s.Revision.SchemaVersion) {
		return invalid("revision", "Invalid immutable source revision")
	}
	if len(s.Nodes) > MaxRevisionNodes || len(s.Edges) > MaxRevisionEdges || len(s.Evidence) > MaxRevisionEvidence {
		return limitFault("Portable source record limit")
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > MaxRevisionBytes {
		return limitFault("Portable source revision byte limit")
	}
	d, err := ValidateSourceStructure(ctx, SourceStructuralGraph{SchemaVersion: s.Revision.SchemaVersion, Nodes: s.Nodes, Edges: s.Edges})
	if err != nil {
		return err
	}
	if len(d) > 0 {
		return changeInvalid(d)
	}
	return nil
}

func portableSourceSnapshots(snapshots []SourceSnapshot) (map[string]SourceSnapshot, error) {
	sources := map[string]SourceSnapshot{}
	for _, src := range snapshots {
		if !ValidID(src.ID) || !ValidID(src.RepositoryID) || sources[src.ID].ID != "" {
			return nil, invalid("snapshots", "Invalid/duplicate source snapshot")
		}
		// Reuse manifest field/path/schema quotas without manufacturing source code.
		manifest := SourceManifest{RepositoryName: "portable", Provider: src.Provider, Snapshot: src.SnapshotManifest}
		if err := validateManifest(manifest); err != nil {
			return nil, err
		}
		sources[src.ID] = src
	}
	return sources, nil
}

// portableSourceRecords maps every record to the proof it cites; only an
// unresolved target may cite none.
func portableSourceRecords(s *PortableSource) (map[string][]string, error) {
	records := map[string][]string{}
	for _, n := range s.Nodes {
		if n.Kind != "unresolved_target" && len(n.EvidenceIDs) == 0 {
			return nil, invalid("evidenceIds", "Known source node requires evidence")
		}
		records[n.ID] = n.EvidenceIDs
	}
	for _, e := range s.Edges {
		if len(e.EvidenceIDs) == 0 {
			return nil, invalid("evidenceIds", "Source edge requires evidence")
		}
		records[e.ID] = e.EvidenceIDs
	}
	return records, nil
}

func validatePortableSourceEvidence(evidence []Evidence, sources map[string]SourceSnapshot, records map[string][]string) (map[string]Evidence, error) {
	proofs := map[string]Evidence{}
	for _, e := range evidence {
		if !ValidID(e.ID) || proofs[e.ID].ID != "" || !slices.Contains(records[e.SubjectID], e.ID) {
			return nil, invalid("evidence", "Missing subject or duplicate evidence")
		}
		source, ok := sources[e.Source.SnapshotID]
		if !ok || source.RepositoryID != e.Source.RepositoryID {
			return nil, invalid("evidence", "Missing exact snapshot owner")
		}
		input := ImportEvidence{ExternalKey: e.ExternalKey, SubjectType: "node", SubjectKey: "portable", PropertyPath: e.PropertyPath, Method: e.Method, Status: e.Status, Source: e.Source, Explanation: e.Explanation, Snippet: e.Snippet}
		session := &ImportSession{RepositoryID: source.RepositoryID, SnapshotID: source.ID, Manifest: SourceManifest{RepositoryName: "portable", Provider: source.Provider, Snapshot: source.SnapshotManifest}}
		// Historical status is retained; fresh-only admission is checked separately
		// by source proof membership, rather than falsifying a fresh observation.
		if input.Status == "stale" {
			input.Status = "explicit"
		}
		if err := validateEvidence(input, session); err != nil {
			return nil, err
		}
		proofs[e.ID] = e
	}
	return proofs, nil
}

// validatePortableProofClosure: every cited proof is cited once per record
// and belongs to the record that cites it.
func validatePortableProofClosure(records map[string][]string, proofs map[string]Evidence) error {
	for id, refs := range records {
		seen := map[string]bool{}
		for _, eid := range refs {
			if seen[eid] {
				return invalid("evidenceIds", "Duplicate proof reference")
			}
			seen[eid] = true
			if proofs[eid].SubjectID != id {
				return invalid("evidenceIds", "Evidence is outside subject closure")
			}
		}
	}
	return nil
}

// validateComposedPortableClaims checks a composed source's exact claims and
// recomputes the materialized projection from its provider selections.
func validateComposedPortableClaims(s *PortableSource, graph *SourceGraphSnapshot) error {
	if s.SourceVector == nil {
		return invalid("sourceVector", "Composed source requires complete vector")
	}
	seen := map[string]bool{}
	for _, a := range s.Assertions {
		if seen[sourceAssertionKey(a)] {
			return invalid("assertion", "Duplicate exact claim")
		}
		seen[sourceAssertionKey(a)] = true
		if err := validateSourceBindings(a); err != nil {
			return err
		}
		if err := validateSourceOwnProof(nil, a, graph, false, nil); err != nil {
			return err
		}
	}
	if err := validatePortableSelections(s, graph); err != nil {
		return err
	}
	for _, n := range s.Nodes {
		if err := validatePortableSelectedPayload(graph, "node", n.ID, sourceNodePayload(n)); err != nil {
			return err
		}
	}
	for _, e := range s.Edges {
		if err := validatePortableSelectedPayload(graph, "edge", e.ID, sourceEdgePayload(e)); err != nil {
			return err
		}
	}
	return nil
}

// validatePortableSelections: each selection names a claimed record and a
// property on which its claims actually diverge.
func validatePortableSelections(s *PortableSource, graph *SourceGraphSnapshot) error {
	current := map[string]SourceClaimCurrentness{}
	for _, v := range s.Currentness {
		current[sourceClaimKey(v.RecordType, v.RecordID, v.RepositoryID, v.ProviderNamespace)] = v
	}
	merger := sourceAssertionMerger{base: graph, current: current}
	groups := map[string][]ProviderAssertion{}
	for _, a := range s.Assertions {
		key := a.RecordType + ":" + a.RecordID
		groups[key] = append(groups[key], a)
	}
	for _, selection := range s.Selections {
		claims := groups[selection.RecordType+":"+selection.ID]
		if len(claims) == 0 {
			return invalid("selection", "Missing claim")
		}
		conflict, err := merger.conflict(claims, selection.Property)
		if err != nil {
			return err
		}
		if conflict == nil {
			return invalid("selection", "Selection targets no divergent property")
		}
	}
	return nil
}

// validateComposedPortableHashes recomputes every assertion hash, the source
// context hash and the revision's semantic hash from the portable content.
func validateComposedPortableHashes(s *PortableSource, graph *SourceGraphSnapshot) error {
	index, err := sourceProofs(graph)
	if err != nil {
		return err
	}
	for _, a := range graph.Assertions {
		hash, err := sourceIntrinsicHash(a, graph.RawEvidence, sourceClaimLegacyBases(a, index))
		if err != nil {
			return err
		}
		if hash != a.AssertionHash {
			return invalid("assertionHash", "Source assertion content does not match its exact hash")
		}
	}
	raw, err := source6ContextJSON(graph)
	if err != nil {
		return err
	}
	if hashBytes(raw) != s.SourceContentHash {
		return invalid("sourceContentHash", "Source context content hash differs")
	}
	semantic, err := source6SemanticHash(graph)
	if err != nil {
		return err
	}
	if semantic != s.Revision.SemanticHash {
		return invalid("semanticHash", "Source revision semantic hash differs")
	}
	return nil
}
func validatePortableSelectedPayload(graph *SourceGraphSnapshot, kind, id string, p SourceAssertionPayload) error {
	claims := []ProviderAssertion{}
	for _, a := range graph.Assertions {
		if a.RecordType == kind && a.RecordID == id {
			claims = append(claims, a)
		}
	}
	if len(claims) == 0 {
		return invalid("source", "Materialized record lacks provider claim")
	}
	selectors, err := sourceSelectors([]SourceAssertionPayload{p})
	if err != nil {
		return err
	}
	for _, selector := range selectors {
		selected, err := sourceSelectedClaims(graph, kind, id, selector)
		if err != nil {
			return err
		}
		if len(selected) == 0 {
			return invalid("selection", "No selected provider claim")
		}
		actual, err := SelectSourceProperty(p, selector)
		if err != nil {
			return err
		}
		expected, err := SelectSourceProperty(selected[0].Payload, selector)
		if err != nil {
			return err
		}
		ah, err := sourceDomainHash("portable-property", actual)
		if err != nil {
			return err
		}
		eh, err := sourceDomainHash("portable-property", expected)
		if err != nil {
			return err
		}
		if ah != eh {
			return invalid("projection", "Source projection differs from selected claim")
		}
	}
	return nil
}
