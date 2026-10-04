package backendmodel

import (
	"maps"
	"slices"
	"strings"
)

func sourceCurrentness(a ProviderAssertion) SourceClaimCurrentness {
	return SourceClaimCurrentness{RecordType: a.RecordType, RecordID: a.RecordID, RepositoryID: a.Owner.RepositoryID, ProviderNamespace: a.Owner.ProviderNamespace, AssertionHash: a.AssertionHash, Own: a.Freshness, Dependency: AssertionFreshness{Status: "current", Reasons: []string{}}, Fields: slices.Clone(a.FieldCurrentness)}
}

type sourceAssertionMerger struct {
	tentative   bool
	base        *SourceGraphSnapshot
	candidate   *composedCandidate
	current     map[string]SourceClaimCurrentness
	requested   map[string]SourceAssertionResolution
	used        map[string]bool
	diagnostics []ImportDiagnostic
}

func mergeSourceAssertions(base *SourceGraphSnapshot, c *composedCandidate, decisions []SourceDecision) ([]ImportDiagnostic, error) {
	return mergeSourceAssertionsMode(base, c, decisions, false)
}

func mergeSourceAssertionsMode(base *SourceGraphSnapshot, c *composedCandidate, decisions []SourceDecision, tentative bool) ([]ImportDiagnostic, error) {
	m := sourceAssertionMerger{tentative: tentative, base: base, candidate: c, current: map[string]SourceClaimCurrentness{}, requested: map[string]SourceAssertionResolution{}, used: map[string]bool{}, diagnostics: []ImportDiagnostic{}}
	groups := map[string][]ProviderAssertion{}
	for _, current := range c.Source.Currentness {
		m.current[sourceClaimKey(current.RecordType, current.RecordID, current.RepositoryID, current.ProviderNamespace)] = current
	}
	for _, a := range c.Source.Assertions {
		key := a.RecordType + "\x00" + a.RecordID
		groups[key] = append(groups[key], a)
	}
	for _, decision := range decisions {
		if decision.State == "active" && decision.Command.Resolution != nil {
			r := *decision.Command.Resolution
			m.requested[r.RecordType+"\x00"+r.ID+"\x00"+sourcePropertyKey(r.Property)] = r
		}
	}
	c.Source.Selections = []SourceAssertionResolution{}
	c.Conflicts = []SourceAssertionConflict{}
	c.Graph.Nodes = []Node{}
	c.Graph.Edges = []Edge{}
	for _, key := range slices.Sorted(maps.Keys(groups)) {
		if err := m.mergeRecord(groups[key]); err != nil {
			return nil, err
		}
	}
	for key := range m.requested {
		if !m.tentative && !m.used[key] {
			return nil, semantic("resolution", "Resolution targets no current divergent property")
		}
	}
	return m.diagnostics, nil
}

// This projection is never published. Exact contender choices establish the
// values needed for dependency analysis before final conflict pins are checked.
func sourceResolutionProjection(base *SourceGraphSnapshot, c *composedCandidate, decisions []SourceDecision) (*graphCandidate, error) {
	source := *c.Source
	projection := &composedCandidate{Source: &source, Graph: &graphCandidate{}}
	_, err := mergeSourceAssertionsMode(base, projection, decisions, true)
	return projection.Graph, err
}

func (m *sourceAssertionMerger) mergeRecord(claims []ProviderAssertion) error {
	slices.SortFunc(claims, func(a, b ProviderAssertion) int { return strings.Compare(sourceAssertionKey(a), sourceAssertionKey(b)) })
	payloads := make([]SourceAssertionPayload, 0, len(claims))
	proof := []string{}
	for _, a := range claims {
		if a.Payload.Kind != claims[0].Payload.Kind {
			return semantic("claimIdentity", "Shared identity cannot change record kind")
		}
		payloads = append(payloads, a.Payload)
		proof = append(proof, a.EvidenceIDs...)
	}
	slices.Sort(proof)
	proof = slices.Compact(proof)
	payload := claims[0].Payload
	selectors, err := sourceSelectors(payloads)
	if err != nil {
		return err
	}
	for _, selector := range selectors {
		conflict, err := m.conflict(claims, selector)
		if err != nil {
			return err
		}
		if conflict == nil {
			continue
		}
		m.candidate.Conflicts = append(m.candidate.Conflicts, *conflict)
		resolution, value, err := m.resolution(*conflict, claims)
		if err != nil {
			return err
		}
		if resolution == nil {
			m.diagnostics = append(m.diagnostics, ImportDiagnostic{Code: "backend_assertion_conflict", Path: claims[0].RecordType + "/" + claims[0].RecordID + "/" + sourcePropertyKey(selector), Message: "Divergent provider values require an exact typed selection"})
			continue
		}
		payload, err = ApplySourceProperty(payload, selector, value)
		if err != nil {
			return err
		}
		m.candidate.Source.Selections = append(m.candidate.Source.Selections, *resolution)
		m.candidate.Graph.ReconciliationGaps = append(m.candidate.Graph.ReconciliationGaps, "provider_disagreement: "+claims[0].RecordID+" "+sourcePropertyKey(selector))
	}
	g := m.candidate.Graph
	if payload.RecordType == "node" {
		g.Nodes = append(g.Nodes, Node{ID: claims[0].RecordID, Kind: payload.Kind, Name: payload.Name, ParentID: payload.ParentID, Attributes: sourceStructuralAttributes(payload), EvidenceIDs: proof})
	} else {
		g.Edges = append(g.Edges, Edge{ID: claims[0].RecordID, Kind: payload.Kind, From: payload.From, To: payload.To, Attributes: sourceStructuralAttributes(payload), EvidenceIDs: proof})
	}
	return nil
}

func (m *sourceAssertionMerger) conflict(claims []ProviderAssertion, property TypedSourcePropertySelector) (*SourceAssertionConflict, error) {
	contenders := make([]SourceAssertionContender, 0, len(claims))
	hashes := map[string]bool{}
	for _, a := range claims {
		value, err := SelectSourceProperty(a.Payload, property)
		if err != nil {
			return nil, err
		}
		hash, err := sourceDomainHash("backend-source6-value-v1", value)
		if err != nil {
			return nil, err
		}
		hashes[hash] = true
		contenders = append(contenders, SourceAssertionContender{Owner: a.Owner, AssertionHash: a.AssertionHash, Value: value, EvidenceIDs: a.EvidenceIDs, Currentness: m.current[sourceAssertionKey(a)]})
	}
	if len(hashes) < 2 {
		return nil, nil
	}
	conflict := SourceAssertionConflict{RecordType: claims[0].RecordType, ID: claims[0].RecordID, Property: property, Contenders: contenders}
	hash, err := sourceDomainHash("backend-source6-conflict-v1", struct {
		Vector   *SourceVector           `json:"baseVector"`
		Conflict SourceAssertionConflict `json:"conflict"`
	}{m.base.SourceVector, conflict})
	conflict.ConflictHash = hash
	return &conflict, err
}

func (m *sourceAssertionMerger) resolution(conflict SourceAssertionConflict, claims []ProviderAssertion) (*SourceAssertionResolution, SourcePropertyValue, error) {
	address := conflict.RecordType + "\x00" + conflict.ID + "\x00" + sourcePropertyKey(conflict.Property)
	resolution, ok := m.requested[address]
	if ok {
		m.used[address] = true
		if !m.tentative && resolution.ConflictHash != conflict.ConflictHash {
			return nil, SourcePropertyValue{}, nil
		}
	} else {
		for _, prior := range m.base.Selections {
			if prior.RecordType == conflict.RecordType && prior.ID == conflict.ID && prior.Property == conflict.Property && sourceContendersMatch(m.base, claims, m.current, !m.tentative) {
				resolution, ok = prior, true
				break
			}
		}
	}
	if !ok {
		return nil, SourcePropertyValue{}, nil
	}
	for i, contender := range conflict.Contenders {
		if contender.Owner.RepositoryID != resolution.Select.RepositoryID || contender.Owner.ProviderNamespace != resolution.Select.ProviderNamespace || contender.AssertionHash != resolution.Select.AssertionHash {
			continue
		}
		if !sourceSemanticSupport(m.candidate.Source, claims[i], conflict.Property) {
			if m.tentative {
				return nil, SourcePropertyValue{}, nil
			}
			return nil, SourcePropertyValue{}, semantic("resolution.select", "Selected claim lacks own semantic support")
		}
		return &resolution, contender.Value, nil
	}
	if m.tentative {
		return nil, SourcePropertyValue{}, nil
	}
	return nil, SourcePropertyValue{}, semantic("resolution.select", "Selected exact contender is missing")
}

func sourceContendersMatch(base *SourceGraphSnapshot, claims []ProviderAssertion, current map[string]SourceClaimCurrentness, compareCurrentness bool) bool {
	old := map[string]ProviderAssertion{}
	for _, a := range base.Assertions {
		if a.RecordType == claims[0].RecordType && a.RecordID == claims[0].RecordID {
			old[sourceAssertionKey(a)] = a
		}
	}
	if len(old) != len(claims) {
		return false
	}
	for _, a := range claims {
		before, ok := old[sourceAssertionKey(a)]
		if !ok || before.AssertionHash != a.AssertionHash {
			return false
		}
		if !compareCurrentness {
			continue
		}
		prior := sourceCurrentness(before)
		for _, f := range base.Currentness {
			if sourceClaimKey(f.RecordType, f.RecordID, f.RepositoryID, f.ProviderNamespace) == sourceAssertionKey(a) {
				prior = f
				break
			}
		}
		left, _ := requestDigest(prior)
		right, _ := requestDigest(current[sourceAssertionKey(a)])
		if left != right {
			return false
		}
	}
	return true
}

func sourceSemanticSupport(graph *SourceGraphSnapshot, a ProviderAssertion, property TypedSourcePropertySelector) bool {
	index, err := sourceProofs(graph)
	if err != nil {
		return false
	}
	for _, eid := range a.EvidenceIDs {
		if basis, legacy := index.legacy[eid]; legacy {
			if basis.Support == "legacy_record" || basis.Support == "legacy_semantic" && basis.Property != nil && *basis.Property == property {
				return true
			}
			continue
		}
		proof, ok := index.evidence[eid]
		if !ok {
			continue
		}
		if proof.PropertyPath == nil {
			return true
		}
		selected := sourcePropertyForPointer(a.Payload, *proof.PropertyPath)
		if selected != nil && *selected == property {
			return true
		}
	}
	return false
}
