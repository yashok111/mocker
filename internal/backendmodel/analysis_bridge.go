package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
)

// The borrowing capability exists only inside Freeze after its exact footprint
// was admitted. It never escapes in a returned context or a persisted document.
type analysisChangePreparationKey struct{}
type analysisChangePreparation struct {
	lease                            *AnalysisInputReservation
	projectID, proposalID, inputHash string
}

func (r *Repo) validateAnalysisChangePreparation(ctx context.Context, pid, id string, in PreviewChangeProposalInput) error {
	lease := analysisLease(ctx)
	if lease == nil {
		return nil
	}
	if err := lease.valid(r, pid); err != nil {
		return err
	}
	admitted, _ := ctx.Value(analysisChangePreparationKey{}).(*analysisChangePreparation)
	if admitted == nil || admitted.lease != lease || admitted.projectID != pid || admitted.proposalID != id {
		return invalid("analysisInput", "Ordinary preparation cannot borrow an analysis reservation")
	}
	hash, err := requestDigest(in)
	if err != nil {
		return err
	}
	if hash != admitted.inputHash {
		return invalid("analysisInput", "Preparation differs from its admitted exact input")
	}
	return nil
}

const FrozenChangePreviewDocumentVersion = "backend-analysis-command-preview-v1"

func frozenPreviewHash(f FrozenChangePreview) (string, error) {
	f.AdmissionHash = ""
	return requestDigest(f)
}
func validateFrozenPreview(pid string, f *FrozenChangePreview) error {
	if f == nil || f.ProjectID != pid || f.DocumentVersion != FrozenChangePreviewDocumentVersion || f.ExpectedVersion < 1 {
		return invalid("preview", "Invalid frozen preview")
	}
	hash, err := frozenPreviewHash(*f)
	if err != nil {
		return err
	}
	if hash != f.AdmissionHash {
		return invalid("admissionHash", "Frozen preview integrity mismatch")
	}
	commandsHash, err := requestDigest(f.Commands)
	if err != nil {
		return err
	}
	if commandsHash != f.CommandsHash {
		return invalid("commandsHash", "Frozen commands differ")
	}
	return validateChangeCommands(f.Commands)
}
func (r *Repo) previewLease(ctx context.Context, pid string, pin ProposalReadTarget, in PreviewChangeProposalInput, base string) (context.Context, func(), error) {
	f, err := r.AnalysisPairInputFootprint(ctx, pid, base, BackendReadTarget{}, &AnalysisCommandPreviewInput{ChangeProposal: pin, Preview: in})
	if err != nil {
		return ctx, nil, err
	}
	if lease := analysisLease(ctx); lease != nil {
		if err = lease.valid(r, pid); err != nil {
			return ctx, nil, err
		}
		for _, d := range f.Documents {
			if err = lease.admit(d.Key, d.Bytes); err != nil {
				return ctx, nil, err
			}
		}
		return ctx, func() {}, nil
	}
	ctx, lease, err := r.ReserveAnalysisInput(ctx, pid, f)
	if err != nil {
		return ctx, nil, err
	}
	return ctx, lease.Release, nil
}
func (r *Repo) FreezeChangePreview(ctx context.Context, pid, id string, in PreviewChangeProposalInput, candidateHash string) (*FrozenChangePreview, error) {
	// Normalize and detach typed commands before hashing or preserving provenance.
	raw, err := json.Marshal(in.Commands)
	if err != nil {
		return nil, err
	}
	if err = json.Unmarshal(raw, &in.Commands); err != nil {
		return nil, err
	}
	var base string
	if err = r.db.R.QueryRowContext(ctx, `SELECT base_revision_id FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, pid, id, in.ProposalRevisionID).Scan(&base); err != nil {
		// Review 2026-10-06, F117: an unknown proposal revision is the caller's 404.
		return nil, footprintRow(err, changeRevisionNotFound())
	}
	pin := ProposalReadTarget{ProposalID: id, ProposalRevisionID: in.ProposalRevisionID}
	ctx, release, err := r.previewLease(ctx, pid, pin, in, base)
	if err != nil {
		return nil, err
	}
	defer release()
	preparationHash, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	ctx = context.WithValue(ctx, analysisChangePreparationKey{}, &analysisChangePreparation{lease: analysisLease(ctx), projectID: pid, proposalID: id, inputHash: preparationHash})
	p, err := r.prepareChangeProposal(ctx, pid, id, in)
	if err != nil {
		return nil, err
	}
	if p.candidate.CandidateHash == nil {
		return nil, changeInvalid(p.candidate.Diagnostics)
	}
	if *p.candidate.CandidateHash != candidateHash {
		return nil, &FaultError{Status: 409, Code: "backend_change_preview_conflict", Message: "Candidate differs from exact preview"}
	}
	source := p.evaluation.source
	vectorHash, err := requestDigest(source.SourceVector)
	if err != nil {
		return nil, err
	}
	commandsHash, err := requestDigest(in.Commands)
	if err != nil {
		return nil, err
	}
	f := &FrozenChangePreview{DocumentVersion: FrozenChangePreviewDocumentVersion, ProjectID: pid, ChangeProposal: pin, ExpectedVersion: in.ExpectedVersion, BaseRevisionID: p.draft.BaseRevisionID, BaseSemanticHash: p.draft.BaseSemanticHash, DraftHash: p.draft.SemanticHash, Commands: in.Commands, CommandsHash: commandsHash, CandidateHash: candidateHash, EffectiveSemanticHash: *p.candidate.SemanticHash, SourcePins: AnalysisSourcePins{RevisionID: source.State.Revision.ID, SemanticHash: source.State.Revision.SemanticHash, ContentHash: source.SourceContentHash, SourceVectorHash: vectorHash, SourceSnapshotIDs: slices.Clone(source.State.Revision.SourceSnapshotIDs)}, SourceSnapshotIDs: slices.Clone(p.evaluation.revision.SourceSnapshotIDs), ArtifactPins: slices.Clone(p.evaluation.revision.ArtifactPins), ArtifactContext: p.evaluation.revision.ArtifactContext}
	for _, c := range in.Commands {
		f.Admission.CommandIDs = append(f.Admission.CommandIDs, c.CommandID)
	}
	for _, created := range p.evaluation.newIDs {
		f.Admission.CreatedIDs = append(f.Admission.CreatedIDs, created.ChangeRecordRef)
	}
	f.AdmissionHash, err = frozenPreviewHash(*f)
	return f, err
}
func (r *Repo) ValidateFrozenChangePreviewAdmission(ctx context.Context, tx *sql.Tx, pid string, f *FrozenChangePreview) error {
	if err := validateFrozenPreview(pid, f); err != nil {
		return err
	}
	p, err := loadChangeProposal(ctx, tx, pid, f.ChangeProposal.ProposalID)
	if err != nil {
		return err
	}
	if err = requireChangeDraft(p, f.ExpectedVersion, f.ChangeProposal.ProposalRevisionID); err != nil {
		return err
	}
	if p.CurrentDraftHash != f.DraftHash {
		return invalid("draftHash", "Frozen draft hash differs")
	}
	if err = checkChangeCommandHistory(ctx, tx, p.ID, f.Commands); err != nil {
		return err
	}
	for _, created := range f.Admission.CreatedIDs {
		var count int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_change_proposal_identities_documents WHERE project_id=? AND proposal_id=? AND id=?`, pid, p.ID, created.ID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			return &FaultError{Status: 409, Code: "backend_change_identity_conflict", Message: "Admitted created ID was subsequently reserved"}
		}
	}
	var draftHash, baseID, baseHash string
	if err = tx.QueryRowContext(ctx, `SELECT json_extract(document,'$.semanticHash'),base_revision_id,json_extract(document,'$.baseSemanticHash') FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, pid, p.ID, f.ChangeProposal.ProposalRevisionID).Scan(&draftHash, &baseID, &baseHash); err != nil {
		return err
	}
	if draftHash != f.DraftHash || baseID != f.BaseRevisionID || baseHash != f.BaseSemanticHash {
		return invalid("preview", "Immutable draft pins differ")
	}
	return nil
}
func loadAnalysisIdentities(ctx context.Context, q importReader, pid string, pin ProposalReadTarget) (map[string]ChangeObjectIdentity, error) {
	rows, err := q.QueryContext(ctx, analysisIdentityCTE+`SELECT i.document FROM backend_change_proposal_identities_documents i JOIN ancestors a ON a.id=i.first_revision_id`, pid, pin.ProposalID, pin.ProposalRevisionID, pid, pin.ProposalID)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := map[string]ChangeObjectIdentity{}
	for rows.Next() {
		var raw string
		var id ChangeObjectIdentity
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal([]byte(raw), &id); err != nil {
			return nil, err
		}
		out[id.ID] = id
	}
	return out, rows.Err()
}
func (r *Repo) ResolveFrozenChangePreview(ctx context.Context, pid string, f *FrozenChangePreview) (*EffectiveGraphSnapshot, error) {
	if err := validateFrozenPreview(pid, f); err != nil {
		return nil, err
	}
	in := PreviewChangeProposalInput{ExpectedVersion: f.ExpectedVersion, ProposalRevisionID: f.ChangeProposal.ProposalRevisionID, Commands: f.Commands}
	ctx, release, err := r.previewLease(ctx, pid, f.ChangeProposal, in, f.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	defer release()
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	draft, err := loadChangeProposalRevision(ctx, tx, pid, f.ChangeProposal.ProposalID, f.ChangeProposal.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	if draft.SemanticHash != f.DraftHash || draft.BaseRevisionID != f.BaseRevisionID || draft.BaseSemanticHash != f.BaseSemanticHash {
		return nil, invalid("preview", "Frozen draft differs")
	}
	source, err := loadChangeSourceForDraft(ctx, tx, pid, draft)
	if err != nil {
		return nil, err
	}
	vectorHash, err := requestDigest(source.SourceVector)
	if err != nil {
		return nil, err
	}
	sourcePins := AnalysisSourcePins{RevisionID: source.State.Revision.ID, SemanticHash: source.State.Revision.SemanticHash, ContentHash: source.SourceContentHash, SourceVectorHash: vectorHash, SourceSnapshotIDs: source.State.Revision.SourceSnapshotIDs}
	actual, _ := requestDigest(sourcePins)
	expected, _ := requestDigest(f.SourcePins)
	if actual != expected {
		return nil, invalid("sourcePins", "Exact source pins differ")
	}
	used, err := loadAnalysisIdentities(ctx, tx, pid, f.ChangeProposal)
	if err != nil {
		return nil, err
	}
	evaluation, err := newChangeEvaluation(source, *draft, used)
	if err != nil {
		return nil, err
	}
	lease := analysisLease(ctx)
	// The replay keeps tx open across evaluation, so owner reads go through it
	// (review 2026-10-06, F3): a fresh reader per owner made pool-width
	// concurrent analysis jobs each wait for a second connection.
	evaluation.readBudget = &changeReadBudget{repo: r, pid: pid, reservation: lease.reservation, lease: lease, seen: map[string]bool{}, bytes: 0, tx: tx}
	prepared := &preparedChangeProposal{input: in, proposal: ChangeProposal{ID: f.ChangeProposal.ProposalID, Version: f.ExpectedVersion}, draft: *draft, evaluation: evaluation, reservation: lease.reservation}
	if err = r.evaluateChangeDraft(ctx, tx, pid, prepared); err != nil {
		return nil, err
	}
	if err = verifyFrozenPreviewReplay(f, prepared); err != nil {
		return nil, err
	}
	desired, err := evaluation.snapshot()
	if err != nil {
		return nil, err
	}
	return changeEffectiveSnapshot(ctx, tx, BackendReadTarget{ChangeProposal: new(f.ChangeProposal)}, &evaluation.revision, source, desired)
}

func verifyFrozenPreviewReplay(f *FrozenChangePreview, prepared *preparedChangeProposal) error {
	evaluation := prepared.evaluation
	if prepared.candidate.CandidateHash == nil || *prepared.candidate.CandidateHash != f.CandidateHash || prepared.candidate.SemanticHash == nil || *prepared.candidate.SemanticHash != f.EffectiveSemanticHash {
		return invalid("candidateHash", "Frozen replay differs from admitted candidate")
	}
	admission := PreviewAdmission{}
	for _, c := range f.Commands {
		admission.CommandIDs = append(admission.CommandIDs, c.CommandID)
	}
	for _, id := range evaluation.newIDs {
		admission.CreatedIDs = append(admission.CreatedIDs, id.ChangeRecordRef)
	}
	actual, _ := requestDigest(admission)
	expected, _ := requestDigest(f.Admission)
	if actual != expected {
		return invalid("admission", "Replay memberships differ")
	}
	actual, _ = requestDigest(struct {
		Pins    []ArtifactPin
		Context ArtifactContext
		Sources []string
	}{evaluation.revision.ArtifactPins, evaluation.revision.ArtifactContext, evaluation.revision.SourceSnapshotIDs})
	expected, _ = requestDigest(struct {
		Pins    []ArtifactPin
		Context ArtifactContext
		Sources []string
	}{f.ArtifactPins, f.ArtifactContext, f.SourceSnapshotIDs})
	if actual != expected {
		return invalid("artifactPins", "Frozen artifact pins differ")
	}
	return nil
}
