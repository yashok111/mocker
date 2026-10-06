package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
)

func (r *Repo) ResolveEffectiveGraph(ctx context.Context, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	if err := r.validateAnalysisLeaseTarget(ctx, pid, target); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return resolveEffectiveGraph(ctx, tx, pid, target)
}
func resolveEffectiveGraph(ctx context.Context, q importReader, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if !ValidID(pid) {
		return nil, notFound()
	}
	switch {
	case target.RevisionID != "":
		return loadSourceEffectiveGraph(ctx, q, pid, target.RevisionID)
	case target.ChangeProposal != nil:
		return loadChangeEffectiveGraph(ctx, q, pid, target)
	case target.Proposal != nil:
		return loadLegacyEffectiveGraph(ctx, q, pid, target)
	default:
		return resolveImportCandidate(ctx, q, pid, *target.ImportCandidate)
	}
}
func detachedEffectiveState(source RevisionState) (RevisionState, error) {
	frozen := source.ArtifactContext
	source.ArtifactContext = nil
	raw, err := json.Marshal(source)
	if err != nil {
		return RevisionState{}, err
	}
	var state RevisionState
	if err := json.Unmarshal(raw, &state); err != nil {
		return state, err
	}
	if frozen != nil {
		raw, err := EncodeArtifactContext(*frozen, source.Revision.ArtifactPins)
		if err != nil {
			return state, err
		}
		state.ArtifactContext, err = DecodeArtifactContext(raw, source.Revision.ArtifactPins)
		if err != nil {
			return state, err
		}
	}
	return state, nil
}
func loadSourceEffectiveGraph(ctx context.Context, q importReader, pid, rid string) (*EffectiveGraphSnapshot, error) {
	source, err := loadSourceGraph(ctx, q, pid, rid)
	if err != nil {
		return nil, err
	}
	state, err := detachedEffectiveState(source.State)
	if err != nil {
		return nil, err
	}
	out := &EffectiveGraphSnapshot{Target: BackendReadTarget{RevisionID: rid}, State: state, Source: source, Origins: []EffectiveFieldOrigin{}, Criteria: []ChangeCriterion{}, BaselineEvidence: []EffectiveEvidenceBasis{}, EdgeNames: map[string]string{}, Identities: []EffectiveIdentity{}}
	out.Pins = EffectiveGraphPins{BaseRevisionID: rid, BaseSemanticHash: state.Revision.SemanticHash, EffectiveSemanticHash: state.Revision.SemanticHash, StructuralSchemaVersion: state.Revision.SchemaVersion, ViewSchemaVersion: state.Revision.SchemaVersion, SourceSnapshotIDs: slices.Clone(state.Revision.SourceSnapshotIDs), ArtifactPins: slices.Clone(state.Revision.ArtifactPins), ArtifactContext: revisionArtifactContext(&state), ArtifactContextV3: state.ArtifactContextV3}
	if source.State.Revision.SchemaVersion == ComposedSchemaVersion {
		out.Origins, err = effectiveSourceOrigins(source, state)
		if err != nil {
			return nil, err
		}
		for _, identity := range source.Identities {
			origin := EffectiveFieldOrigin{RecordType: identity.RecordType, SubjectID: identity.ID, Selector: EffectivePropertySelector{Kind: "source_identity", RecordType: identity.RecordType, ID: identity.ID, RepositoryID: identity.RepositoryID, ProviderNamespace: identity.ProviderNamespace}, Kind: "source", BaseRef: &EffectiveBaseRef{RevisionID: source.State.Revision.ID, SemanticHash: source.State.Revision.SemanticHash, RecordType: identity.RecordType, SubjectID: identity.ID}, SourceClaims: []BaseAssertionRef{}, EvidenceIDs: []string{}}
			out.Identities = append(out.Identities, EffectiveIdentity{Target: ChangeIdentityTarget{Kind: "source_identity", Source: new(identity)}, ExternalKey: new(identity.ExternalKey), Origin: origin})
		}
	}
	out.coverage, err = loadAPIArtifactCoverage(ctx, q, &source.State)
	if err != nil {
		return nil, err
	}
	if err := finishEffectivePins(out, source.SourceVector); err != nil {
		return nil, err
	}
	return out, nil
}
func finishEffectivePins(out *EffectiveGraphSnapshot, vector *SourceVector) error {
	var err error
	out.Pins.SourceVectorHash, err = requestDigest(vector)
	if err != nil {
		return err
	}
	pins := out.Pins
	pins.TargetHash = ""
	out.indexedReads()
	out.Pins.TargetHash, err = requestDigest(struct {
		Target BackendReadTarget
		Pins   EffectiveGraphPins
	}{out.Target, pins})
	return err
}
func loadChangeEffectiveGraph(ctx context.Context, q importReader, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	pin := target.ChangeProposal
	revision, err := loadChangeProposalRevision(ctx, q, pid, pin.ProposalID, pin.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	source, err := loadSourceGraph(ctx, q, pid, revision.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	evaluation, err := newChangeEvaluation(source, *revision, map[string]ChangeObjectIdentity{})
	if err != nil {
		return nil, err
	}
	desired, err := evaluation.snapshot()
	if err != nil {
		return nil, err
	}
	return changeEffectiveSnapshot(ctx, q, target, revision, source, desired)
}

func changeEffectiveSnapshot(ctx context.Context, q importReader, target BackendReadTarget, revision *ChangeProposalRevision, source *SourceGraphSnapshot, desired *ChangeEvaluationSnapshot) (*EffectiveGraphSnapshot, error) {
	state, err := detachedEffectiveState(source.State)
	if err != nil {
		return nil, err
	}
	state.Nodes, state.Edges = desired.Nodes, desired.Edges
	state, err = detachedEffectiveState(state)
	if err != nil {
		return nil, err
	}
	out := &EffectiveGraphSnapshot{Target: target, State: state, Source: source, Criteria: desired.Criteria, Origins: []EffectiveFieldOrigin{}, BaselineEvidence: []EffectiveEvidenceBasis{}, Identities: []EffectiveIdentity{}, EdgeNames: map[string]string{}}
	out.Pins = EffectiveGraphPins{BaseRevisionID: revision.BaseRevisionID, BaseSemanticHash: revision.BaseSemanticHash, EffectiveSemanticHash: revision.SemanticHash, StructuralSchemaVersion: ComposedSchemaVersion, ViewSchemaVersion: ChangeProposalDocumentVersion, SourceSnapshotIDs: slices.Clone(revision.SourceSnapshotIDs), ArtifactPins: slices.Clone(revision.ArtifactPins), ArtifactContext: new(revision.ArtifactContext)}
	if revision.ArtifactContextV3 != nil {
		out.Pins.ArtifactContext = nil
		out.Pins.ArtifactContextV3 = revision.ArtifactContextV3
	}
	for _, name := range desired.EdgeNames {
		out.EdgeNames[name.ID] = name.Name
	}
	for _, origin := range desired.Origins {
		out.Origins = append(out.Origins, effectiveEvaluationOrigin(source, origin))
	}
	for _, identity := range desired.Identities {
		ref := changeIdentityRef(identity.Target)
		selector := changeIdentitySelector(identity.Target)
		origin := effectiveEvaluationOrigin(source, ChangeEvaluationFieldOrigin{ChangeRecordRef: ref, Selector: selector, Origin: identity.Origin})
		out.Identities = append(out.Identities, EffectiveIdentity{Target: identity.Target, ExternalKey: identity.ExternalKey, Origin: origin})
	}
	for _, e := range source.State.Evidence {
		typ := "node"
		for _, edge := range source.State.Edges {
			if edge.ID == e.SubjectID {
				typ = "edge"
				break
			}
		}
		out.BaselineEvidence = append(out.BaselineEvidence, EffectiveEvidenceBasis{RevisionID: revision.BaseRevisionID, SemanticHash: revision.BaseSemanticHash, RecordType: typ, SubjectID: e.SubjectID, EvidenceID: e.ID})
	}
	out.coverage, err = loadAPIArtifactCoverage(ctx, q, &source.State)
	if err != nil {
		return nil, err
	}
	if err := finishEffectivePins(out, &revision.SourceVector); err != nil {
		return nil, err
	}
	return out, nil
}
