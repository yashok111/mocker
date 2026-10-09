package backendmodel

import "context"

func selectedSavedViewVersion(version string) string {
	if version == "" {
		return SavedViewDocumentVersion
	}
	return version
}
func validateSavedViewVersion(version string) error {
	if version != "" && version != SavedViewV2DocumentVersion && version != SavedViewDocumentVersion {
		return invalid("documentVersion", "Select saved-view-v2 explicitly or omit legacy version")
	}
	return nil
}
func validateSavedViewVersionTarget(version string, target BackendReadTarget) error {
	if err := validateSavedViewVersion(version); err != nil {
		return err
	}
	if selectedSavedViewVersion(version) == SavedViewDocumentVersion {
		return validateSavedViewTarget(target)
	}
	if err := target.Validate(); err != nil {
		return err
	}
	if target.ImportCandidate != nil {
		return savedUnsupported()
	}
	return nil
}
func resolveSavedVersionReferences(ctx context.Context, q importReader, pid, version string, target BackendReadTarget, state SavedViewState) (*SavedViewPins, error) {
	if err := validateSavedViewVersionTarget(version, target); err != nil {
		return nil, err
	}
	if selectedSavedViewVersion(version) == SavedViewDocumentVersion {
		return resolveSavedReferences(ctx, q, pid, target, state)
	}
	graph, err := loadNativeProjectionGraph(ctx, q, pid, target)
	if err != nil {
		return nil, err
	}
	if target.Proposal != nil {
		pins, err := resolveSavedReferences(ctx, q, pid, target, state)
		if err != nil {
			return nil, err
		}
		pins.Effective = new(graph.Pins)
		return pins, nil
	}
	effective := graph.State
	effective.Revision.SchemaVersion = graph.Pins.StructuralSchemaVersion
	pins := &SavedViewPins{RevisionID: graph.Pins.BaseRevisionID, SemanticHash: graph.Pins.EffectiveSemanticHash, Effective: new(graph.Pins)}
	return validateSavedGraphReferences(&resolvedBackendTarget{revisionID: graph.Pins.BaseRevisionID}, &effective, pins, state, target.ChangeProposal != nil || graph.Pins.StructuralSchemaVersion == ComposedSchemaVersion)
}
