package backendmodel

// PortableTargetPins resolves only the supplied closed immutable model. It never
// falls back to a numerically colliding local owner/project or a mutable head.
func PortableTargetPins(model PortableModel, target BackendReadTarget) (EffectiveGraphPins, error) {
	if err := target.Validate(); err != nil {
		return EffectiveGraphPins{}, err
	}
	rid := target.RevisionID
	var full *ChangeProposalRevision
	var legacy *ProposalRevision
	if target.ChangeProposal != nil {
		full = portableFullRevision(model, target.ChangeProposal.ProposalID, target.ChangeProposal.ProposalRevisionID)
		if full == nil {
			return EffectiveGraphPins{}, invalid("target", "Full proposal pin missing from portable closure")
		}
		rid = full.BaseRevisionID
	}
	if target.Proposal != nil {
		legacy = portableLegacyRevision(model, target.Proposal.ProposalID, target.Proposal.ProposalRevisionID)
		if legacy == nil {
			return EffectiveGraphPins{}, invalid("target", "Legacy proposal pin missing from portable closure")
		}
		rid = legacy.BaseRevisionID
	}
	var source *PortableSource
	for i := range model.Sources {
		if model.Sources[i].Revision.ID == rid {
			source = &model.Sources[i]
		}
	}
	if source == nil {
		return EffectiveGraphPins{}, invalid("target", "Source pin missing from portable closure")
	}
	g, err := portableSourceGraph(source)
	if err != nil {
		return EffectiveGraphPins{}, err
	}
	out := &EffectiveGraphSnapshot{Target: target, State: g.State, Source: g}
	out.Pins = EffectiveGraphPins{BaseRevisionID: rid, BaseSemanticHash: source.Revision.SemanticHash, EffectiveSemanticHash: source.Revision.SemanticHash, StructuralSchemaVersion: source.Revision.SchemaVersion, ViewSchemaVersion: source.Revision.SchemaVersion, SourceSnapshotIDs: source.Revision.SourceSnapshotIDs, ArtifactPins: source.Revision.ArtifactPins, ArtifactContext: g.State.ArtifactContext, ArtifactContextV3: g.State.ArtifactContextV3}
	vector := source.SourceVector
	if full != nil {
		if full.BaseSemanticHash != source.Revision.SemanticHash {
			return EffectiveGraphPins{}, invalid("target", "Proposal base hash differs")
		}
		pinFullProposal(&out.Pins, full)
		vector = &full.SourceVector
	}
	if legacy != nil {
		if legacy.BaseSemanticHash != source.Revision.SemanticHash {
			return EffectiveGraphPins{}, invalid("target", "Legacy base hash differs")
		}
		out.Pins.EffectiveSemanticHash = legacy.SemanticHash
		out.Pins.ViewSchemaVersion = ProposalDocumentVersion
	}
	if err := finishEffectivePins(out, vector); err != nil {
		return EffectiveGraphPins{}, err
	}
	return out.Pins, nil
}

// portableFullRevision finds a change proposal revision inside the closure;
// the last match wins, as a closure never carries two.
func portableFullRevision(model PortableModel, proposalID, revisionID string) *ChangeProposalRevision {
	var full *ChangeProposalRevision
	for i := range model.Proposals {
		p := &model.Proposals[i]
		if p.Full == nil || p.Full.ID != proposalID {
			continue
		}
		for j := range p.FullRevisions {
			if p.FullRevisions[j].ID == revisionID {
				full = &p.FullRevisions[j]
			}
		}
	}
	return full
}

func portableLegacyRevision(model PortableModel, proposalID, revisionID string) *ProposalRevision {
	var legacy *ProposalRevision
	for i := range model.Proposals {
		p := &model.Proposals[i]
		if p.Legacy == nil || p.Legacy.ID != proposalID {
			continue
		}
		for j := range p.LegacyRevisions {
			if p.LegacyRevisions[j].ID == revisionID {
				legacy = &p.LegacyRevisions[j]
			}
		}
	}
	return legacy
}

// pinFullProposal moves the pins from the source revision to the change
// proposal revision layered on it.
func pinFullProposal(pins *EffectiveGraphPins, full *ChangeProposalRevision) {
	pins.BaseSemanticHash = full.BaseSemanticHash
	pins.EffectiveSemanticHash = full.SemanticHash
	pins.StructuralSchemaVersion = ComposedSchemaVersion
	pins.ViewSchemaVersion = ChangeProposalDocumentVersion
	pins.SourceSnapshotIDs = full.SourceSnapshotIDs
	pins.ArtifactPins = full.ArtifactPins
	pins.ArtifactContext = &full.ArtifactContext
	pins.ArtifactContextV3 = full.ArtifactContextV3
	if full.ArtifactContextV3 != nil {
		pins.ArtifactContext = nil
	}
}
