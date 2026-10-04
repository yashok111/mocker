package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
)

func resolveImportCandidate(ctx context.Context, q importReader, pid string, target ImportCandidateReadTarget) (*EffectiveGraphSnapshot, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	session, err := loadSession(ctx, q, pid, target.ImportID)
	if err != nil {
		return nil, err
	}
	conflict := func(message string) error {
		return importConflict("backend_import_hash_conflict", message, session.Version)
	}
	if session.State != "ready" || session.Version != target.ImportVersion || session.CandidateHash == nil || *session.CandidateHash != target.CandidateHash {
		return nil, conflict("Current READY candidate changed")
	}
	candidate, raw, err := rebuildReadyCandidate(ctx, q, session, target, conflict)
	if err != nil {
		return nil, err
	}
	base, err := loadSourceGraph(ctx, q, pid, session.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	state, err := detachedEffectiveState(base.State)
	if err != nil {
		return nil, err
	}
	state.Nodes, state.Edges, state.Evidence = candidate.Nodes, candidate.Edges, candidate.Evidence
	state.Sources, state.Inventory = candidate.Sources, slices.Clone(session.Inventory)
	state.APIArtifactContext, state.ArtifactContext = candidate.APIArtifactContext, candidate.ArtifactContext
	source := base
	if candidate.Composed != nil {
		source, err = sourceSnapshotForCandidate(candidate)
		if err != nil {
			return nil, err
		}
	}
	semantic := hashBytes(raw)
	if candidate.Composed != nil {
		semantic, err = source6SemanticHash(candidate.Composed.Source)
	} else if candidate.APIArtifactContext != nil || candidate.ArtifactContext != nil {
		semantic, err = importedArtifactSemanticHash(candidate)
	}
	if err != nil {
		return nil, err
	}
	out := &EffectiveGraphSnapshot{Target: BackendReadTarget{ImportCandidate: new(target)}, State: state, Source: source, Origins: []EffectiveFieldOrigin{}, Criteria: []ChangeCriterion{}, BaselineEvidence: []EffectiveEvidenceBasis{}, EdgeNames: map[string]string{}, Identities: []EffectiveIdentity{}}
	out.coverage = &RevisionCoverage{Coverage: candidate.Coverage, StaleCounts: candidate.StaleCounts, Inventory: slices.Clone(session.Inventory), Snapshots: slices.Clone(candidate.Sources), ReconciliationGaps: slices.Clone(candidate.ReconciliationGaps)}
	out.Pins = EffectiveGraphPins{BaseRevisionID: session.BaseRevisionID, BaseSemanticHash: base.State.Revision.SemanticHash, EffectiveSemanticHash: semantic, ViewSchemaVersion: "import-candidate-v1", StructuralSchemaVersion: modelSchemaVersion(session.Profile), SourceSnapshotIDs: sourceIDs(candidate.Sources), ArtifactPins: slices.Clone(candidate.ArtifactPins), ArtifactContext: revisionArtifactContext(&state)}
	if err := finishEffectivePins(out, source.SourceVector); err != nil {
		return nil, err
	}
	return out, nil
}

func rebuildReadyCandidate(ctx context.Context, q importReader, session *ImportSession, target ImportCandidateReadTarget, conflict func(string) error) (*graphCandidate, []byte, error) {
	var document string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_import_previews WHERE session_id=? AND version=?`, session.ID, target.ImportVersion).Scan(&document)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil, conflict("Current READY preview is unavailable")
	}
	if err != nil {
		return nil, nil, err
	}
	var preview ImportPreview
	if err := json.Unmarshal([]byte(document), &preview); err != nil {
		return nil, nil, err
	}
	if preview.State != "ready" || preview.CandidateHash == nil || *preview.CandidateHash != target.CandidateHash {
		return nil, nil, conflict("Saved READY preview differs")
	}
	candidate, diagnostics, err := prepareGraph(ctx, q, session)
	if err != nil {
		return nil, nil, err
	}
	if len(diagnostics) != 0 {
		return nil, nil, conflict("Rebuilt candidate differs from READY preview")
	}
	raw, err := candidateJSON(session, candidate)
	if err != nil {
		return nil, nil, err
	}
	if hashBytes(raw) != target.CandidateHash {
		return nil, nil, conflict("Rebuilt candidate hash differs")
	}
	return candidate, raw, nil
}
