package backendmodel

import (
	"context"
	"encoding/json/v2"
	"maps"
	"slices"
)

func rebaseIdentityPartition(t ChangeIdentityTarget) string {
	ref := changeIdentityRef(t)
	key := ref.RecordType + "\x00" + ref.ID
	if t.Source != nil {
		return key + "\x00" + t.Source.RepositoryID + "\x00" + t.Source.ProviderNamespace
	}
	return key + "\x00intent"
}
func rebaseIdentityMap(e *changeEvaluation) (map[string]ChangeEvaluationIdentity, error) {
	s, err := e.snapshot()
	if err != nil {
		return nil, err
	}
	out := map[string]ChangeEvaluationIdentity{}
	for _, i := range s.Identities {
		out[rebaseIdentityPartition(i.Target)] = i
	}
	return out, nil
}
func (m *changeRebaseMerge) identities(base, ours, result *changeEvaluation) error {
	b, err := rebaseIdentityMap(base)
	if err != nil {
		return err
	}
	o, err := rebaseIdentityMap(ours)
	if err != nil {
		return err
	}
	if m.baseIdentities != nil {
		b = m.baseIdentities
		o = m.oursIdentities
	}
	n := rebaseSourceIdentityMap(result.source)
	keys := map[string]bool{}
	for _, side := range []map[string]ChangeEvaluationIdentity{b, o, n} {
		for key := range side {
			keys[key] = true
		}
	}
	for _, key := range slices.Sorted(maps.Keys(keys)) {
		bi, bok := b[key]
		oi, ook := o[key]
		ni, nok := n[key]
		if err = m.mergeIdentity(result, bi, bok, oi, ook, ni, nok); err != nil {
			return err
		}
	}
	return nil
}

//nolint:gocyclo // Keep presence and carry decisions together so no fallback can overwrite an explicit choice.
func (m *changeRebaseMerge) mergeIdentity(result *changeEvaluation, bi ChangeEvaluationIdentity, bok bool, oi ChangeEvaluationIdentity, ook bool, ni ChangeEvaluationIdentity, nok bool) error {
	target := bi.Target
	if nok {
		target = ni.Target
	}
	if ook {
		target = oi.Target
	}
	ref := changeIdentityRef(target)
	_, live := result.records[ref.ID]
	bv, err := rebaseValue(bok, bi.ExternalKey)
	if err != nil {
		return err
	}
	ov, err := rebaseValue(ook, oi.ExternalKey)
	if err != nil {
		return err
	}
	nv, err := rebaseValue(nok, ni.ExternalKey)
	if err != nil {
		return err
	}
	returning := ook && oi.Target.Kind == "carried_source_identity" && nok
	resurrected := !nok && ook && oi.Target.Source != nil && !changeSourceHasRecord(result.source, ref)
	var selected ChangeRebaseValue
	var resolution *RebaseResolutionOrigin
	if resurrected && sameRebaseValue(ov, bv) {
		// The whole-record keep already authorized its unchanged historical keys.
		selected = ov
	} else {
		selected, resolution, err = m.choose(ref, ChangeRebaseSelector{Kind: "identity", Identity: new(target)}, bv, ov, nv, returning)
		if err != nil {
			return err
		}
	}
	if !live {
		return nil
	}
	if selected.Presence == "absent_property" && !resurrected {
		return nil
	}
	if sameRebaseValue(selected, nv) && resolution == nil && !resurrected {
		return nil
	}
	origin := m.selectedOrigin(oi.Origin, resolution, ref, m.draft)
	keepCarry := returning && resolution != nil && m.resolutions[resolution.ResolutionID].Choice == "keep_proposal"
	if nok && !keepCarry {
		target = ni.Target
	} else if oi.Target.Source != nil {
		target, origin = m.carryIdentity(result, oi.Target, origin)
	}
	var external *string
	if selected.Presence != "absent_property" {
		if err = rebaseDecodeValue(selected, &external); err != nil {
			return err
		}
	}
	result.revision.Delta.IdentityIntents = append(result.revision.Delta.IdentityIntents, ChangeIdentityIntent{Target: target, ExternalKey: external, Origin: origin})
	return nil
}
func (m *changeRebaseMerge) carryIdentity(result *changeEvaluation, target ChangeIdentityTarget, origin EffectiveOrigin) (ChangeIdentityTarget, EffectiveOrigin) {
	if target.Kind != "carried_source_identity" {
		target.Kind = "carried_source_identity"
		target.Basis = &CarriedSourceBasis{RevisionID: m.draft.BaseRevisionID, SemanticHash: m.draft.BaseSemanticHash}
	}
	ref := changeIdentityRef(target)
	result.revision.Delta.CarriedIdentities = append(result.revision.Delta.CarriedIdentities, ChangeCarriedSourceIdentity{Source: *target.Source, Basis: *target.Basis})
	if origin.Kind != "intent" {
		origin = result.records[ref.ID].Origin
	}
	reserved, ok := result.used[ref.ID]
	if !ok || reserved.FirstRevisionID == "" {
		identity := ChangeObjectIdentity{ChangeRecordRef: ref, Kind: result.records[ref.ID].Payload.Kind, Origin: origin, AllocationKind: "carried_source"}
		if !slices.ContainsFunc(result.newIDs, func(i ChangeObjectIdentity) bool { return i.ID == ref.ID }) {
			result.newIDs = append(result.newIDs, identity)
		}
		result.used[ref.ID] = identity
	}
	return target, origin
}
func changeSourceHasRecord(source *SourceGraphSnapshot, ref ChangeRecordRef) bool {
	for _, n := range source.State.Nodes {
		if ref.RecordType == "node" && n.ID == ref.ID {
			return true
		}
	}
	for _, e := range source.State.Edges {
		if ref.RecordType == "edge" && e.ID == ref.ID {
			return true
		}
	}
	return false
}

func changeCarried(e *changeEvaluation, t ChangeIdentityTarget) bool {
	if t.Source == nil || t.Basis == nil {
		return false
	}
	return slices.ContainsFunc(e.revision.Delta.CarriedIdentities, func(c ChangeCarriedSourceIdentity) bool { return c.Source == *t.Source && c.Basis == *t.Basis })
}
func validateChangeRebaseCarry(ctx context.Context, q importReader, pid string, e *changeEvaluation) error {
	for _, c := range e.revision.Delta.CarriedIdentities {
		historical, err := e.referencedSource(ctx, q, pid, c.Basis.RevisionID)
		if err != nil {
			return err
		}
		if historical.State.Revision.SemanticHash != c.Basis.SemanticHash || !slices.Contains(historical.Identities, c.Source) {
			return invalid("carriedIdentities", "Exact historical source claim is missing")
		}
		live, ok := e.records[c.Source.ID]
		if !ok {
			continue
		}
		found := false
		for _, n := range historical.State.Nodes {
			if n.ID == live.ID && live.RecordType == "node" && n.Kind == live.Payload.Kind {
				found = true
			}
		}
		for _, n := range historical.State.Edges {
			if n.ID == live.ID && live.RecordType == "edge" && n.Kind == live.Payload.Kind {
				found = true
			}
		}
		if !found {
			return invalid("carriedIdentities", "Carried record must retain its original kind")
		}
	}
	return nil
}
func addChangeHistoricalMembership(b *analysisFootprintBuilder, id, rid string) error {
	size, err := changeHistoricalIdentityBytes(b.ctx, b.q, b.pid, id, rid)
	if err != nil {
		return err
	}
	return b.add("historical-identities:"+rid, size)
}

func reserveChangeHistoricalIDs(ctx context.Context, q importReader, pid, id string, used map[string]ChangeObjectIdentity) error {
	rows, err := q.QueryContext(ctx, `SELECT DISTINCT g.record_type,g.id,json_extract(g.document,'$.kind') FROM backend_graph_records g JOIN backend_change_proposal_revisions r ON r.base_revision_id=g.revision_id AND r.project_id=g.project_id WHERE r.project_id=? AND r.proposal_id=? AND g.record_type IN ('node','edge')`, pid, id)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var typ, id, kind string
		if err = rows.Scan(&typ, &id, &kind); err != nil {
			return err
		}
		if _, ok := used[id]; !ok {
			used[id] = ChangeObjectIdentity{ChangeRecordRef: ChangeRecordRef{RecordType: typ, ID: id}, Kind: kind, AllocationKind: "historical_source", Origin: EffectiveOrigin{Kind: "source"}}
		}
	}
	return rows.Err()
}
func stampChangeRebaseOrigins(rev *ChangeProposalRevision, resolutions []ChangeRebaseResolution, identities []ChangeObjectIdentity) {
	selected := map[string]bool{}
	for _, r := range resolutions {
		selected[r.ConflictID] = true
	}
	stamp := func(o *EffectiveOrigin) {
		if o.RebaseResolution != nil && selected[o.RebaseResolution.ResolutionID] {
			v := *o.RebaseResolution
			v.ProposalRevisionID = rev.ID
			o.RebaseResolution = &v
		}
	}
	for i := range identities {
		stamp(&identities[i].Origin)
	}
	for i := range rev.Delta.Created {
		stamp(&rev.Delta.Created[i].Origin)
	}
	for i := range rev.Delta.Removed {
		stamp(&rev.Delta.Removed[i].Origin)
	}
	for i := range rev.Delta.Properties {
		stamp(&rev.Delta.Properties[i].Origin)
	}
	for i := range rev.Delta.IdentityIntents {
		stamp(&rev.Delta.IdentityIntents[i].Origin)
	}
	for i := range rev.Delta.ArtifactIntents {
		stamp(&rev.Delta.ArtifactIntents[i].Origin)
	}
	for i := range rev.Delta.EdgeNames {
		stamp(&rev.Delta.EdgeNames[i].Origin)
	}
}
func changeIdentitySelector(t ChangeIdentityTarget) EffectivePropertySelector {
	ref := changeIdentityRef(t)
	s := EffectivePropertySelector{Kind: t.Kind, RecordType: ref.RecordType, ID: ref.ID}
	if t.Source != nil {
		s.RepositoryID = t.Source.RepositoryID
		s.ProviderNamespace = t.Source.ProviderNamespace
	}
	if t.Kind == "carried_source_identity" {
		s.CarriedSourceIdentity = &ChangeCarriedSourceIdentity{Source: *t.Source, Basis: *t.Basis}
	}
	return s
}
func changeAllocatedIntent(i ChangeObjectIdentity) bool {
	return i.AllocationKind == "" && i.Origin.Kind == "intent" || i.AllocationKind == "intent"
}
func changeCarriedRecord(rev ChangeProposalRevision, id string) bool {
	return slices.ContainsFunc(rev.Delta.CarriedIdentities, func(c ChangeCarriedSourceIdentity) bool { return c.Source.ID == id })
}

// The current base membership is already charged by its source document inventory.
// Only older proposal bases need an additional immutable ID/kind projection.
func changeHistoricalIdentityBytes(ctx context.Context, q importReader, pid, id, rid string) (int64, error) {
	var size int64
	err := q.QueryRowContext(ctx, analysisIdentityCTE+`SELECT COALESCE(sum(length(CAST(g.id AS BLOB))+length(CAST(g.record_type AS BLOB))+length(CAST(g.kind AS BLOB))+128),0) FROM backend_graph_records g WHERE g.project_id=? AND g.record_type IN ('node','edge') AND g.revision_id IN (SELECT DISTINCT r.base_revision_id FROM backend_change_proposal_revisions r JOIN ancestors a ON a.id=r.id) AND g.revision_id<>(SELECT base_revision_id FROM backend_change_proposal_revisions WHERE project_id=? AND proposal_id=? AND id=?)`, pid, id, rid, pid, id, pid, pid, id, rid).Scan(&size)
	return size, err
}

func retargetChangeRebaseCriteria(e *changeEvaluation) error {
	snapshot, err := e.snapshot()
	if err != nil {
		return err
	}
	for i := range e.revision.Criteria {
		criterion := &e.revision.Criteria[i]
		if criterion.Kind != "field_equals" {
			continue
		}
		var selector EffectivePropertySelector
		if err = json.Unmarshal(criterion.Selector, &selector); err != nil {
			return err
		}
		repository, provider := selector.RepositoryID, selector.ProviderNamespace
		if selector.CarriedSourceIdentity != nil {
			repository = selector.CarriedSourceIdentity.Source.RepositoryID
			provider = selector.CarriedSourceIdentity.Source.ProviderNamespace
		}
		if selector.Kind != "source_identity" && selector.Kind != "carried_source_identity" {
			continue
		}
		for _, identity := range snapshot.Identities {
			source := identity.Target.Source
			if source != nil && source.ID == criterion.ID && source.RecordType == criterion.RecordType && source.RepositoryID == repository && source.ProviderNamespace == provider {
				criterion.Selector, err = json.Marshal(changeIdentitySelector(identity.Target))
				if err != nil {
					return err
				}
				break
			}
		}
	}
	return nil
}
func preserveChangeRebaseTombstones(ours, result *changeEvaluation) {
	for _, removed := range ours.revision.Delta.Removed {
		if _, live := result.records[removed.ID]; live {
			continue
		}
		if !slices.ContainsFunc(result.revision.Delta.Removed, func(r ChangeRemoval) bool { return r.ChangeRecordRef == removed.ChangeRecordRef }) {
			result.revision.Delta.Removed = append(result.revision.Delta.Removed, removed)
		}
	}
}

func loadChangeReservedIdentities(ctx context.Context, q importReader, pid, id string) (map[string]ChangeObjectIdentity, error) {
	used, err := loadChangeIdentities(ctx, q, id)
	if err != nil {
		return nil, err
	}
	if err = reserveChangeHistoricalIDs(ctx, q, pid, id, used); err != nil {
		return nil, err
	}
	return used, nil
}

func rebaseSourceIdentityMap(source *SourceGraphSnapshot) map[string]ChangeEvaluationIdentity {
	out := map[string]ChangeEvaluationIdentity{}
	for _, claim := range source.Identities {
		target := ChangeIdentityTarget{Kind: "source_identity", Source: new(claim)}
		out[rebaseIdentityPartition(target)] = ChangeEvaluationIdentity{Target: target, ExternalKey: new(claim.ExternalKey), Origin: EffectiveOrigin{Kind: "source"}}
	}
	return out
}
func (m *changeRebaseMerge) recordIdentityEdits(base, ours, result *changeEvaluation) error {
	b, err := rebaseIdentityMap(base)
	if err != nil {
		return err
	}
	o, err := rebaseIdentityMap(ours)
	if err != nil {
		return err
	}
	if m.baseIdentities != nil {
		b = m.baseIdentities
		o = m.oursIdentities
	}
	n := rebaseSourceIdentityMap(result.source)
	m.oursIdentityEdits = rebaseIdentityEditedIDs(b, o)
	m.nextIdentityEdits = rebaseIdentityEditedIDs(b, n)
	for _, name := range ours.revision.Delta.EdgeNames {
		m.oursIdentityEdits[name.ID] = true
	}
	return nil
}
func rebaseIdentityEditedIDs(before, after map[string]ChangeEvaluationIdentity) map[string]bool {
	out := map[string]bool{}
	keys := map[string]bool{}
	for key := range before {
		keys[key] = true
	}
	for key := range after {
		keys[key] = true
	}
	for key := range keys {
		a, aok := before[key]
		b, bok := after[key]
		equal := aok == bok && (a.ExternalKey == nil && b.ExternalKey == nil || a.ExternalKey != nil && b.ExternalKey != nil && *a.ExternalKey == *b.ExternalKey)
		if equal {
			continue
		}
		if aok {
			out[changeIdentityRef(a.Target).ID] = true
		}
		if bok {
			out[changeIdentityRef(b.Target).ID] = true
		}
	}
	return out
}
