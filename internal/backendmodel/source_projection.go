package backendmodel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"slices"
)

// sourceSelectedClaims retains every equivalent contender when no conflict
// selection exists. A divergent value requires its explicit exact selection.
func sourceSelectedClaims(graph *SourceGraphSnapshot, typ, id string, property TypedSourcePropertySelector) ([]ProviderAssertion, error) {
	claims := []ProviderAssertion{}
	var selection *SourceAssertionSelection
	for _, s := range graph.Selections {
		if s.RecordType == typ && s.ID == id && s.Property == property {
			selection = new(s.Select)
			break
		}
	}
	valueHash := ""
	for _, a := range graph.Assertions {
		if a.RecordType != typ || a.RecordID != id {
			continue
		}
		if selection != nil && (a.Owner.RepositoryID != selection.RepositoryID || a.Owner.ProviderNamespace != selection.ProviderNamespace || a.AssertionHash != selection.AssertionHash) {
			continue
		}
		value, err := SelectSourceProperty(a.Payload, property)
		if err != nil {
			return nil, err
		}
		hash, err := sourceDomainHash("backend-source6-value-v1", value)
		if err != nil {
			return nil, err
		}
		if valueHash != "" && valueHash != hash {
			return nil, semantic("source.selection", "Divergent source property has no exact selection")
		}
		valueHash = hash
		claims = append(claims, a)
	}
	return claims, nil
}
func sourcePropertyProof(graph *SourceGraphSnapshot, typ, id string, property TypedSourcePropertySelector) (lineageProof, error) {
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	claims, err := sourceSelectedClaims(graph, typ, id, property)
	if err != nil {
		return p, err
	}
	if len(claims) == 0 {
		p.status = "unresolved"
		p.add("missing_source_claim", true)
		return p, nil
	}
	index, err := sourceProofs(graph)
	if err != nil {
		return p, err
	}
	for _, a := range claims {
		current := sourceReadCurrentness(graph, a)
		own, dependency := current.Own, current.Dependency
		found := false
		for _, field := range current.Fields {
			if field.Property == property {
				own, dependency = field.Own, field.Dependency
				found = true
				break
			}
		}
		if !found {
			dependency = sourceStaleReason(dependency, "property_currentness_missing")
		}
		sourceFreshnessProof(&p, own)
		sourceFreshnessProof(&p, dependency)
		supported := false
		for _, eid := range a.EvidenceIDs {
			evidence, ok := index.evidence[eid]
			if !ok {
				p.status = runtimeWorseStatus(p.status, "unresolved")
				p.add("unresolved_evidence", true)
				continue
			}
			if !sourceEvidenceSupports(index, a, evidence, property) {
				continue
			}
			supported = true
			p.evidenceIDs = append(p.evidenceIDs, eid)
			p.status = runtimeWorseStatus(p.status, evidence.Status)
			if evidence.Status == "stale" || evidence.Status == "unresolved" {
				p.add(evidence.Status+"_evidence", true)
			} else if evidence.Status != "explicit" {
				p.add("inferred_evidence", false)
			}
		}
		if supported {
			p.claims = append(p.claims, sourceAssertionRef(a))
		}
		if !supported {
			p.status = runtimeWorseStatus(p.status, "inferred")
			p.add("missing_semantic_support", false)
		}
	}
	return p, nil
}
func mergeSourceProof(p *lineageProof, other lineageProof) {
	p.status = runtimeWorseStatus(p.status, other.status)
	p.boundary = p.boundary || other.boundary
	p.evidenceIDs = append(p.evidenceIDs, other.evidenceIDs...)
	slices.Sort(p.evidenceIDs)
	p.evidenceIDs = slices.Compact(p.evidenceIDs)
	for _, claim := range other.claims {
		if !slices.Contains(p.claims, claim) {
			p.claims = append(p.claims, claim)
		}
	}
	for reason := range other.reasons {
		p.reasons[reason] = true
	}
}
func sourceRecordProof(graph *SourceGraphSnapshot, typ, id string, ref *LineageValueRef) (lineageProof, error) {
	p := lineageProof{status: "explicit", reasons: map[string]bool{}}
	payloads := []SourceAssertionPayload{}
	for _, a := range graph.Assertions {
		if a.RecordType == typ && a.RecordID == id {
			payloads = append(payloads, a.Payload)
		}
	}
	if len(payloads) == 0 {
		p.status = "unresolved"
		p.add("missing_source_claim", true)
		return p, nil
	}
	selectors, err := sourceSelectors(payloads)
	if err != nil {
		return p, err
	}
	for _, property := range selectors {
		if ref != nil && property.Kind == "relational_facet" && (ref.Kind != "column" || property.FacetKey != ref.FacetKey) {
			continue
		}
		if ref != nil && property.Kind == "flow_ports" && (ref.Kind != "port" || property.Collection != ref.Collection) {
			continue
		}
		present := false
		for _, payload := range payloads {
			value, err := SelectSourceProperty(payload, property)
			if err != nil {
				return p, err
			}
			present = present || value.Present
		}
		if !present {
			continue
		}
		next, err := sourcePropertyProof(graph, typ, id, property)
		if err != nil {
			return p, err
		}
		mergeSourceProof(&p, next)
	}
	return p, nil
}

// sourceSnapshotForCandidate detaches the source context while preserving the
// real base revision identity. Candidate pins are owned by the effective target.
func sourceSnapshotForCandidate(candidate *graphCandidate) (*SourceGraphSnapshot, error) {
	if candidate == nil || candidate.Composed == nil || candidate.Composed.Source == nil {
		return nil, semantic("candidate", "Composed candidate required")
	}
	source := candidate.Composed.Source
	raw, err := json.Marshal(struct {
		State       RevisionState
		Vector      *SourceVector
		Assertions  []ProviderAssertion
		Selections  []SourceAssertionResolution
		Currentness []SourceClaimCurrentness
		Identities  []QualifiedSourceIdentity
		Bases       []LegacyProofBasis
	}{source.State, source.SourceVector, source.Assertions, source.Selections, source.Currentness, source.Identities, source.LegacyProofBases})
	if err != nil {
		return nil, err
	}
	var detached struct {
		State       RevisionState
		Vector      *SourceVector
		Assertions  []ProviderAssertion
		Selections  []SourceAssertionResolution
		Currentness []SourceClaimCurrentness
		Identities  []QualifiedSourceIdentity
		Bases       []LegacyProofBasis
	}
	if err := json.Unmarshal(raw, &detached); err != nil {
		return nil, err
	}
	out := &SourceGraphSnapshot{State: detached.State, SourceVector: detached.Vector, Assertions: detached.Assertions, Selections: detached.Selections, Currentness: detached.Currentness, Identities: detached.Identities, LegacyProofBases: detached.Bases, SourceContentHash: source.SourceContentHash, RawEvidence: map[string]jsontext.Value{}}
	out.rawAssertions = map[string]jsontext.Value{}
	for key, raw := range source.rawAssertions {
		out.rawAssertions[key] = slices.Clone(raw)
	}
	for id, raw := range source.RawEvidence {
		out.RawEvidence[id] = slices.Clone(raw)
	}
	return out, nil
}

func sourceReadContext(graph *SourceGraphSnapshot, typ, id, evidenceID string) *SourceReadContext {
	out := &SourceReadContext{SourceVector: graph.SourceVector, SourceContentHash: graph.SourceContentHash, Identities: []QualifiedSourceIdentity{}, AssertionRefs: []BaseAssertionRef{}, Selections: []SourceAssertionResolution{}, Currentness: []SourceClaimCurrentness{}, LegacyProofBases: []LegacyProofBasis{}}
	selectedIDs := map[string]bool{}
	for _, a := range graph.Assertions {
		if typ != "" && a.RecordType != typ || id != "" && a.RecordID != id || evidenceID != "" && !slices.Contains(a.EvidenceIDs, evidenceID) {
			continue
		}
		out.AssertionRefs = append(out.AssertionRefs, sourceAssertionRef(a))
		out.Currentness = append(out.Currentness, sourceReadCurrentness(graph, a))
		selectedIDs[a.RecordType+"\x00"+a.RecordID] = true
	}
	for _, identity := range graph.Identities {
		if selectedIDs[identity.RecordType+"\x00"+identity.ID] {
			out.Identities = append(out.Identities, identity)
		}
	}
	for _, selection := range graph.Selections {
		if selectedIDs[selection.RecordType+"\x00"+selection.ID] {
			out.Selections = append(out.Selections, selection)
		}
	}
	for _, basis := range graph.LegacyProofBases {
		if (id == "" || basis.RecordID == id) && (evidenceID == "" || basis.EvidenceID == evidenceID) {
			out.LegacyProofBases = append(out.LegacyProofBases, basis)
		}
	}
	return out
}

func sourceFreshnessProof(p *lineageProof, fresh AssertionFreshness) {
	if fresh.Status == "current" {
		return
	}
	p.status = runtimeWorseStatus(p.status, "stale")
	p.add("stale_assertion", true)
	for _, reason := range fresh.Reasons {
		p.add(reason, true)
	}
}
func sourceEvidenceSupports(index *sourceProofIndex, a ProviderAssertion, evidence Evidence, property TypedSourcePropertySelector) bool {
	if basis, ok := index.legacy[evidence.ID]; ok {
		return basis.Support == "legacy_record" || basis.Support == "legacy_semantic" && basis.Property != nil && *basis.Property == property
	}
	if evidence.PropertyPath == nil {
		return true
	}
	selected := sourcePropertyForPointer(a.Payload, *evidence.PropertyPath)
	return selected != nil && *selected == property
}

func sourceProjectionLimitations(graph *SourceGraphSnapshot, legacy *SourceSnapshot) []string {
	if graph == nil {
		return slices.Clone(legacy.Provider.Limitations)
	}
	out := []string{}
	for _, part := range graph.SourceVector.Partitions {
		out = append(out, part.Provider.Limitations...)
	}
	return out
}

// sourceRecordReadContext is bounded by one record's surviving claims. The
// enclosing graph page owns the manifest-bearing source vector.
func sourceRecordReadContext(graph *SourceGraphSnapshot, typ, id string) *SourceReadContext {
	out := sourceReadContext(graph, typ, id, "")
	out.SourceVector = nil
	return out
}
func sourceVectorReadContext(graph *SourceGraphSnapshot) *SourceReadContext {
	return &SourceReadContext{SourceVector: graph.SourceVector, SourceContentHash: graph.SourceContentHash, Identities: []QualifiedSourceIdentity{}, AssertionRefs: []BaseAssertionRef{}, Selections: []SourceAssertionResolution{}, Currentness: []SourceClaimCurrentness{}, LegacyProofBases: []LegacyProofBasis{}}
}

// sourceEvidenceReadContext bounds provenance to the page's raw proof IDs and
// their exact owning claims, retaining selections for those claim subjects.
func sourceEvidenceReadContext(graph *SourceGraphSnapshot, evidenceIDs []string) *SourceReadContext {
	out := sourceVectorReadContext(graph)
	evidence := map[string]bool{}
	for _, id := range evidenceIDs {
		evidence[id] = true
	}
	claims := map[string]bool{}
	subjects := map[string]bool{}
	for _, a := range graph.Assertions {
		included := false
		for _, id := range a.EvidenceIDs {
			included = included || evidence[id]
		}
		if !included {
			continue
		}
		claims[sourceAssertionKey(a)] = true
		subjects[a.RecordType+"\x00"+a.RecordID] = true
		out.AssertionRefs = append(out.AssertionRefs, sourceAssertionRef(a))
		out.Currentness = append(out.Currentness, sourceReadCurrentness(graph, a))
	}
	for _, identity := range graph.Identities {
		if claims[sourceClaimKey(identity.RecordType, identity.ID, identity.RepositoryID, identity.ProviderNamespace)] {
			out.Identities = append(out.Identities, identity)
		}
	}
	for _, selection := range graph.Selections {
		if subjects[selection.RecordType+"\x00"+selection.ID] {
			out.Selections = append(out.Selections, selection)
		}
	}
	for _, basis := range graph.LegacyProofBases {
		if evidence[basis.EvidenceID] {
			out.LegacyProofBases = append(out.LegacyProofBases, basis)
		}
	}
	return out
}
