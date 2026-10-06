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
		for i := range model.Proposals {
			p := &model.Proposals[i]
			if p.Full == nil || p.Full.ID != target.ChangeProposal.ProposalID {
				continue
			}
			for j := range p.FullRevisions {
				v := &p.FullRevisions[j]
				if v.ID == target.ChangeProposal.ProposalRevisionID {
					full = v
					rid = v.BaseRevisionID
				}
			}
		}
		if full == nil {
			return EffectiveGraphPins{}, invalid("target", "Full proposal pin missing from portable closure")
		}
	}
	if target.Proposal != nil {
		for i := range model.Proposals {
			p := &model.Proposals[i]
			if p.Legacy == nil || p.Legacy.ID != target.Proposal.ProposalID {
				continue
			}
			for j := range p.LegacyRevisions {
				v := &p.LegacyRevisions[j]
				if v.ID == target.Proposal.ProposalRevisionID {
					legacy = v
					rid = v.BaseRevisionID
				}
			}
		}
		if legacy == nil {
			return EffectiveGraphPins{}, invalid("target", "Legacy proposal pin missing from portable closure")
		}
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
		out.Pins.BaseSemanticHash = full.BaseSemanticHash
		out.Pins.EffectiveSemanticHash = full.SemanticHash
		out.Pins.StructuralSchemaVersion = ComposedSchemaVersion
		out.Pins.ViewSchemaVersion = ChangeProposalDocumentVersion
		out.Pins.SourceSnapshotIDs = full.SourceSnapshotIDs
		out.Pins.ArtifactPins = full.ArtifactPins
		out.Pins.ArtifactContext = &full.ArtifactContext
		out.Pins.ArtifactContextV3 = full.ArtifactContextV3
		if full.ArtifactContextV3 != nil {
			out.Pins.ArtifactContext = nil
		}
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
