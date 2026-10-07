package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

type preparedChangeRebase struct {
	proposal    ChangeProposal
	draft       ChangeProposalRevision
	evaluation  *changeEvaluation
	candidate   *ChangeProposalRebaseCandidate
	reservation *store.TransientReservation
}

func validateChangeRebaseInput(in PreviewChangeProposalRebaseInput) error {
	if in.ExpectedVersion < 1 || !ValidID(in.ProposalRevisionID) || !ValidID(in.NewBaseRevisionID) {
		return invalid("rebase", "Exact draft, source revision and positive version required")
	}
	if len(in.RepairCommands) > 0 {
		if err := validateChangeCommands(in.RepairCommands); err != nil {
			return err
		}
	}
	if len(in.IdentityResolutions) > MaxRevisionNodes+MaxRevisionEdges || len(in.Resolutions) > MaxRevisionNodes+MaxRevisionEdges {
		return limitFault("Too many rebase resolutions")
	}
	seen := map[string]bool{}
	for _, r := range in.Resolutions {
		if !validHash(r.ConflictID) || !slices.Contains([]string{"take_source", "keep_proposal", "replace"}, r.Choice) || !validChangeReason(r.Reason) || seen[r.ConflictID] {
			return invalid("resolutions", "Duplicate or invalid resolution")
		}
		if (r.Choice == "replace") != (len(r.Value) > 0) {
			return invalid("resolutions/value", "Replacement value is exclusive to replace")
		}
		seen[r.ConflictID] = true
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return err
	}
	if len(raw) > MaxChangeProposalCommandBytes {
		return limitFault("Rebase request exceeds byte bound")
	}
	return nil
}

func (r *Repo) prepareChangeRebase(ctx context.Context, pid, id string, in PreviewChangeProposalRebaseInput) (*preparedChangeRebase, error) {
	if err := validateChangeRebaseInput(in); err != nil {
		return nil, err
	}
	// A caller-supplied analysis lease cannot authorize this separate preparation.
	if analysisLease(ctx) != nil {
		return nil, invalid("analysisInput", "Rebase requires its own exact input reservation")
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	p, err := loadChangeProposal(ctx, tx, pid, id)
	if err != nil {
		return nil, err
	}
	if err = requireChangeRebaseDraft(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return nil, err
	}
	if err = checkChangeCommandHistory(ctx, tx, id, in.RepairCommands); err != nil {
		return nil, err
	}
	b, err := buildChangeRebaseFootprint(ctx, tx, pid, id, in)
	if err != nil {
		return nil, err
	}
	reservation, err := r.reserveChangeInput(ctx, pid, b.total)
	if err != nil {
		return nil, err
	}
	retained := false
	defer func() {
		if !retained {
			reservation.Release()
		}
	}()
	sides, err := loadChangeRebaseSides(ctx, tx, pid, id, in)
	if err != nil {
		return nil, err
	}
	draft, next, base, ours, result := sides.draft, sides.next, sides.base, sides.ours, sides.result
	result.readBudget = &changeReadBudget{repo: r, pid: pid, reservation: reservation, bytes: b.total, seen: map[string]bool{}}
	for key := range b.documents {
		result.readBudget.seen[key] = true
	}
	m := &changeRebaseMerge{input: in, draft: *draft, next: next, conflicts: []ChangeRebaseConflict{}, resolutions: map[string]ChangeRebaseResolution{}, seen: map[string]bool{}}
	for _, resolution := range in.Resolutions {
		m.resolutions[resolution.ConflictID] = resolution
	}
	if err = m.merge(ctx, base, ours, result); err != nil {
		return nil, err
	}
	if err = r.prepareChangeArtifacts(ctx, result, in.RepairCommands); err != nil {
		return nil, err
	}
	prepared, err := r.finishChangeRebase(ctx, tx, pid, id, p, m, result, reservation)
	retained = err == nil
	return prepared, err
}

func (r *Repo) PreviewChangeProposalRebase(ctx context.Context, pid, id string, in PreviewChangeProposalRebaseInput) (*ChangeProposalRebaseCandidate, error) {
	p, err := r.prepareChangeRebase(ctx, pid, id, in)
	if err != nil {
		return nil, err
	}
	defer p.reservation.Release()
	return p.candidate, nil
}
func (r *Repo) ApplyChangeProposalRebase(ctx context.Context, pid, id string, in ApplyChangeProposalRebaseInput) (*ChangeProposalApplyResult, error) {
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "change-proposal-rebase:" + pid + ":" + id
	out := new(ChangeProposalApplyResult)
	if found, err := readChangeReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return out, err
	}
	prepared, prepareErr := r.prepareChangeRebase(ctx, pid, id, in.PreviewChangeProposalRebaseInput)
	if prepared != nil {
		defer prepared.reservation.Release()
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		return r.applyChangeRebaseTx(ctx, tx, pid, id, scope, digest, in, prepared, prepareErr, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func requireChangeRebaseDraft(p *ChangeProposal, version int64, rid string) error {
	return requireChangeDraft(p, version, rid)
}

func buildChangeRebaseFootprint(ctx context.Context, tx importReader, pid, id string, in PreviewChangeProposalRebaseInput) (*analysisFootprintBuilder, error) {
	var err error
	b := analysisFootprintBuilder{ctx: ctx, q: tx, pid: pid, documents: map[string]int64{}}
	if err = rebaseDraftFootprint(&b, id, in.ProposalRevisionID); err != nil {
		return nil, err
	}
	if err = b.source(in.NewBaseRevisionID); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err = b.references(raw); err != nil {
		return nil, err
	}
	for _, c := range in.RepairCommands {
		if c.Type == "set_artifact_pin" {
			if err = b.owner(c.Artifact.Kind, c.Artifact.ID, c.RevisionID); err != nil {
				return nil, err
			}
		}
	}
	if err = addChangeHistoricalMembership(&b, id, in.ProposalRevisionID); err != nil {
		return nil, err
	}

	return &b, nil
}

type changeRebaseSides struct {
	draft              *ChangeProposalRevision
	next               *SourceGraphSnapshot
	base, ours, result *changeEvaluation
}

func loadChangeRebaseSides(ctx context.Context, tx importReader, pid, id string, in PreviewChangeProposalRebaseInput) (*changeRebaseSides, error) {
	draft, err := loadChangeProposalRevision(ctx, tx, pid, id, in.ProposalRevisionID)
	if err != nil {
		return nil, err
	}
	source, err := loadChangeSourceForDraft(ctx, tx, pid, draft)
	if err != nil {
		return nil, err
	}
	next, err := loadComposedBase(ctx, tx, pid, in.NewBaseRevisionID)
	if err != nil {
		return nil, err
	}
	schema := next.State.Revision.SchemaVersion
	if schema != EventsSchemaVersion && schema != ComposedSchemaVersion || draft.BaseSchemaVersion == ComposedSchemaVersion && schema != ComposedSchemaVersion {
		return nil, &FaultError{Status: 422, Code: "backend_unsupported_scope", Message: "Rebase requires source5/source6 without provenance downgrade"}
	}
	used, err := loadChangeIdentities(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = reserveChangeHistoricalIDs(ctx, tx, pid, id, used); err != nil {
		return nil, err
	}
	ours, err := newChangeEvaluation(source, *draft, used)
	if err != nil {
		return nil, err
	}
	baseDraft := *draft
	baseDraft.Delta = emptyChangeDelta()
	baseDraft.Criteria = []ChangeCriterion{}
	base, err := newChangeEvaluation(source, baseDraft, used)
	if err != nil {
		return nil, err
	}
	if draft.ArtifactContextV3 != nil || next.State.ArtifactContextV3 != nil {
		return nil, invalid("context", "Rebase of namespaced artifacts requires an explicit namespace mapping")
	}
	nextDraft := *draft
	nextDraft.BaseRevisionID = next.State.Revision.ID
	nextDraft.BaseSemanticHash = next.State.Revision.SemanticHash
	nextDraft.BaseSchemaVersion = schema
	nextDraft.SourceSnapshotIDs = slices.Clone(next.State.Revision.SourceSnapshotIDs)
	nextDraft.SourceVector = *next.SourceVector
	nextDraft.Delta = emptyChangeDelta()
	nextDraft.ArtifactPins = slices.Clone(next.State.Revision.ArtifactPins)
	nextContext := revisionArtifactContext(&next.State)
	if nextContext == nil {
		content, anchor, err := artifactSourceAnchors(ctx, tx, &next.State)
		if err != nil {
			return nil, err
		}
		nextContext = &ArtifactContext{SourceContentHash: content, SourceSemanticHash: anchor, APIBindings: []APIArtifactBinding{}, EditorBindings: []EditorBinding{}}
	}
	nextDraft.ArtifactContext = *nextContext
	result, err := newChangeEvaluation(next, nextDraft, used)
	if err != nil {
		return nil, err
	}

	return &changeRebaseSides{draft, next, base, ours, result}, nil
}

func (m *changeRebaseMerge) merge(ctx context.Context, base, ours, result *changeEvaluation) error {
	var err error
	in := m.input
	if err = m.correspondence(base, ours, result); err != nil {
		return err
	}
	if err = m.records(base, ours, result); err != nil {
		return err
	}
	if err = m.identities(base, ours, result); err != nil {
		return err
	}
	if err = m.artifacts(base, ours, result); err != nil {
		return err
	}
	result.revision.Delta.EdgeNames = slices.Clone(ours.revision.Delta.EdgeNames)
	result.revision.Criteria = slices.Clone(ours.revision.Criteria)
	if err = retargetChangeRebaseCriteria(result); err != nil {
		return err
	}
	preserveChangeRebaseTombstones(ours, result)
	for id := range m.resolutions {
		if !m.seen[id] {
			return invalid("resolutions", "Unknown, stale or unrelated conflict ID")
		}
	}
	for _, c := range in.RepairCommands {
		if err = ctx.Err(); err != nil {
			return err
		}
		if err = result.apply(c); err != nil {
			return err
		}
	}

	return nil
}

func (r *Repo) finishChangeRebase(ctx context.Context, tx importReader, pid, id string, p *ChangeProposal, m *changeRebaseMerge, result *changeEvaluation, reservation *store.TransientReservation) (*preparedChangeRebase, error) {
	draft, next, in := &m.draft, m.next, m.input
	snapshot, diagnostics, err := result.validate(ctx)
	if err != nil {
		return nil, err
	}
	for _, check := range []func() error{
		func() error { return validateChangeRebaseArtifacts(result) },
		func() error { return validateChangeHistoricalReferences(ctx, tx, pid, result) },
		func() error { return r.validateChangeCriteriaReferences(ctx, tx, pid, result) },
		func() error { return validateChangeRebaseCarry(ctx, tx, pid, result) },
	} {
		// Only content refusals become diagnostics (review 2026-10-06, F40).
		if diagnostics, err = changeCheckDiagnostic(diagnostics, "rebase", check()); err != nil {
			return nil, err
		}
	}
	vectorHash, err := requestDigest(next.SourceVector)
	if err != nil {
		return nil, err
	}
	candidate := &ChangeProposalRebaseCandidate{ProposalID: id, ProposalRevisionID: draft.ID, ExpectedVersion: p.Version, OldBaseRevisionID: draft.BaseRevisionID, OldBaseSemanticHash: draft.BaseSemanticHash, NewBaseRevisionID: next.State.Revision.ID, NewBaseSemanticHash: next.State.Revision.SemanticHash, Conflicts: m.conflicts, Diagnostics: diagnostics, SourcePins: AnalysisSourcePins{RevisionID: next.State.Revision.ID, SemanticHash: next.State.Revision.SemanticHash, ContentHash: next.SourceContentHash, SourceVectorHash: vectorHash, SourceSnapshotIDs: next.State.Revision.SourceSnapshotIDs}, ArtifactPins: result.revision.ArtifactPins}
	if len(m.conflicts) == 0 && len(diagnostics) == 0 {
		semantic, err := changeSemanticHash(snapshot)
		if err != nil {
			return nil, err
		}
		candidate.SemanticHash = new(semantic)
		result.revision.SemanticHash = semantic
		hash, err := requestDigest(struct {
			Protocol  string
			DraftHash string
			Input     PreviewChangeProposalRebaseInput
			Candidate *ChangeProposalRebaseCandidate
		}{changeRebaseProtocol, draft.SemanticHash, in, candidate})
		if err != nil {
			return nil, err
		}
		candidate.CandidateHash = new(hash)
	}
	if candidate.CandidateHash != nil {
		result.revision.Rebase = &ChangeRebaseAction{Protocol: changeRebaseProtocol, Input: in, CandidateHash: *candidate.CandidateHash}
	}
	encoded, err := json.Marshal(result.revision)
	if err != nil {
		return nil, err
	}
	if len(encoded) > MaxRevisionBytes {
		return nil, limitFault("Rebased draft exceeds materialization bounds")
	}
	if err = r.db.Write(ctx, func(w *sql.Tx) error {
		staged, err := stagingBytes(ctx, w, pid)
		if err != nil {
			return err
		}
		if !reservation.Resize(4*result.readBudget.bytes+2*int64(len(encoded))+MaxChangeProposalCommandBytes, MaxProjectStagingBytes-staged) {
			return limitFault("Prepared rebase exceeds transient budget")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	return &preparedChangeRebase{proposal: *p, draft: *draft, evaluation: result, candidate: candidate, reservation: reservation}, nil
}

// requireRebaseCandidate admits only a conflict-free, diagnostic-free
// candidate whose hash is the one the caller previewed.
func requireRebaseCandidate(candidate *ChangeProposalRebaseCandidate, previewed string) error {
	// Unresolved B/O/N conflicts leave CandidateHash nil with possibly no
	// diagnostic at all; answering changeInvalid then said "unresolved
	// validation diagnostics" with an empty list (review 2026-10-06, F42).
	if conflicts := candidate.Conflicts; candidate.CandidateHash == nil && len(conflicts) > 0 {
		ids := make([]string, len(conflicts))
		for i, c := range conflicts {
			ids[i] = c.ID
		}
		return &FaultError{Status: 409, Code: "backend_change_rebase_conflict", Message: "Rebase has unresolved conflicts; preview again with a resolution for each conflictId", Details: map[string]any{"conflictIds": ids, "diagnostics": candidate.Diagnostics}}
	}
	if candidate.CandidateHash == nil {
		return changeInvalid(candidate.Diagnostics)
	}
	if previewed != *candidate.CandidateHash {
		return &FaultError{Status: 409, Code: "backend_change_preview_conflict", Message: "Rebase candidate differs from exact preview"}
	}
	return nil
}

func (r *Repo) applyChangeRebaseTx(ctx context.Context, tx *sql.Tx, pid, id, scope, digest string, in ApplyChangeProposalRebaseInput, prepared *preparedChangeRebase, prepareErr error, out *ChangeProposalApplyResult) error {
	if found, err := readChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); err != nil || found {
		return err
	}
	if prepareErr != nil {
		return prepareErr
	}
	p, err := loadChangeProposal(ctx, tx, pid, id)
	if err != nil {
		return err
	}
	if err = requireChangeRebaseDraft(p, in.ExpectedVersion, in.ProposalRevisionID); err != nil {
		return err
	}
	if err = checkChangeCommandHistory(ctx, tx, id, in.RepairCommands); err != nil {
		return err
	}
	if err = requireRebaseCandidate(prepared.candidate, in.CandidateHash); err != nil {
		return err
	}
	var hash string
	if err = tx.QueryRowContext(ctx, `SELECT json_extract(document,'$.semanticHash') FROM backend_revisions_documents WHERE project_id=? AND id=?`, pid, in.NewBaseRevisionID).Scan(&hash); err != nil {
		return err
	}
	if hash != prepared.candidate.NewBaseSemanticHash {
		return invalid("newBaseRevisionId", "Selected source hash changed")
	}
	if err = prepared.evaluation.checkArtifactDigests(ctx, tx, p.Version); err != nil {
		return err
	}
	rev := prepared.evaluation.revision
	rev.ParentRevisionID = new(prepared.draft.ID)
	rev.ID = uuid.NewV7().String()
	rev.AcceptedBatchRevisionID = rev.ID
	rev.CreatedAt = time.Now().UTC()
	rev.Author = "user"
	rev.Summary = "Rebase desired graph onto exact source revision"
	stampChangeRebaseOrigins(&rev, in.Resolutions, prepared.evaluation.newIDs)
	rev.Rebase = &ChangeRebaseAction{Protocol: changeRebaseProtocol, Input: in.PreviewChangeProposalRebaseInput, CandidateHash: in.CandidateHash}
	rev.ImportOrigin = nil // a local rebase is not an import (F38)
	p.Version++
	p.Status = "draft"
	p.ReadyReference = nil
	p.CurrentDraftRevisionID = rev.ID
	p.CurrentDraftHash = rev.SemanticHash
	p.UpdatedAt = rev.CreatedAt
	commands := in.RepairCommands
	if commands == nil {
		commands = []ChangeProposalCommand{}
	}
	if err = persistChangeRevision(ctx, tx, *p, rev, "rebase", "", commands, prepared.evaluation.newIDs); err != nil {
		return err
	}
	if err = updateChangeAggregate(ctx, tx, *p); err != nil {
		return err
	}
	*out = ChangeProposalApplyResult{Proposal: *p, Revision: rev, Changes: prepared.evaluation.changes, SemanticHash: rev.SemanticHash}
	raw, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if err = writeChangeReceipt(ctx, tx, scope, in.IdempotencyKey, digest, string(raw)); err != nil {
		return err
	}
	if err = checkChangeProposalQuota(ctx, tx, pid, id); err != nil {
		return err
	}
	out.receiptJSON = string(raw)
	return nil
}
