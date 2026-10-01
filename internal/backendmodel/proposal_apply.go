package backendmodel

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"math"
	"time"
	"uuid"

	"github.com/yashok111/mocker/internal/store"
)

type PreviewProposalInput struct {
	ExpectedVersion int64             `json:"expectedVersion"`
	DraftRevisionID string            `json:"draftRevisionId"`
	Commands        []ProposalCommand `json:"commands"`
}

type ApplyProposalInput struct {
	ExpectedVersion int64             `json:"expectedVersion"`
	DraftRevisionID string            `json:"draftRevisionId"`
	Commands        []ProposalCommand `json:"commands"`
	CandidateHash   string            `json:"candidateHash"`
	IdempotencyKey  string            `json:"idempotencyKey"`
}

func decodeProposalOperation(b []byte, apply bool, result any) error {
	m, err := relationalObject(b)
	if err != nil {
		return invalid("body", "Expected a strict proposal command request")
	}
	fields := []string{"expectedVersion", "draftRevisionId", "commands"}
	if apply {
		fields = append(fields, "candidateHash", "idempotencyKey")
	}
	if err := relationalFields(m, fields, nil); err != nil {
		return invalid("body", err.Error())
	}
	for key, raw := range m {
		raw = bytes.TrimSpace(raw)
		switch key {
		case "expectedVersion":
			var n int64
			if string(raw) == "null" || json.Unmarshal(raw, &n) != nil || n < 1 {
				return invalid(key, "Expected a positive int64 version")
			}
		case "commands":
			if len(raw) == 0 || raw[0] != '[' {
				return invalid(key, "Expected an array of typed commands")
			}
		default:
			if len(raw) == 0 || raw[0] != '"' {
				return invalid(key, "Expected a string")
			}
		}
	}
	if err := json.Unmarshal(b, result, json.RejectUnknownMembers(true)); err != nil {
		if f, ok := errors.AsType[*FaultError](err); ok {
			return f
		}
		return invalid("body", err.Error())
	}
	return nil
}

func (in *PreviewProposalInput) UnmarshalJSON(b []byte) error {
	type input PreviewProposalInput
	return decodeProposalOperation(b, false, (*input)(in))
}

func (in *ApplyProposalInput) UnmarshalJSON(b []byte) error {
	type input ApplyProposalInput
	return decodeProposalOperation(b, true, (*input)(in))
}

type ProposalPreview struct {
	ProposalID         string               `json:"proposalId"`
	BaseRevisionID     string               `json:"baseRevisionId"`
	BaseSemanticHash   string               `json:"baseSemanticHash"`
	DraftRevisionID    string               `json:"draftRevisionId"`
	DraftHash          string               `json:"draftHash"`
	ExpectedVersion    int64                `json:"expectedVersion"`
	CandidateHash      *string              `json:"candidateHash"`
	CandidateGraphHash *string              `json:"candidateGraphHash"`
	Changes            []ProposalChange     `json:"changes"`
	Criteria           []ProposalCriterion  `json:"criteria"`
	Diagnostics        []ProposalDiagnostic `json:"diagnostics"`
	Limitations        []string             `json:"limitations"`
}

type ProposalApplyResult struct {
	receiptJSON        string
	Proposal           Proposal            `json:"proposal"`
	Revision           ProposalRevision    `json:"revision"`
	Changes            []ProposalChange    `json:"changes"`
	Criteria           []ProposalCriterion `json:"criteria"`
	CandidateGraphHash string              `json:"candidateGraphHash"`
}

func (out ProposalApplyResult) MarshalJSON() ([]byte, error) {
	if out.receiptJSON != "" {
		return []byte(out.receiptJSON), nil
	}
	type result ProposalApplyResult
	return json.Marshal(result(out))
}

func proposalConflict(p *Proposal, code, message string) error {
	return &FaultError{Status: 409, Code: code, Message: message, CurrentVersion: p.Version, Details: map[string]any{"proposalId": p.ID, "version": p.Version, "draftRevisionId": p.DraftRevisionID, "draftHash": p.DraftHash}}
}

func requireProposalDraft(p *Proposal, version int64, draftID string) error {
	if p.Version != version || p.Version == math.MaxInt64 {
		return proposalConflict(p, "backend_proposal_version_conflict", "Proposal version changed or cannot be incremented")
	}
	if p.DraftRevisionID != draftID {
		return proposalConflict(p, "backend_proposal_preview_conflict", "Draft revision differs from the preview input")
	}
	return nil
}

func proposalCommandHistory(ctx context.Context, q importReader, proposalID string, commands []ProposalCommand) error {
	requested := map[string]bool{}
	for _, c := range commands {
		requested[c.CommandID] = true
	}
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_proposal_revisions WHERE proposal_id=?`, proposalID)
	if err != nil {
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return err
		}
		var revision ProposalRevision
		if err := json.Unmarshal([]byte(raw), &revision); err != nil {
			return err
		}
		for _, c := range revision.Commands {
			if requested[c.CommandID] {
				return &FaultError{Status: 422, Code: "backend_proposal_invalid", Message: "Command ID already exists in saved proposal history", Details: map[string]any{"commandId": c.CommandID, "property": "/commandId", "revisionId": revision.ID}}
			}
		}
	}
	return rows.Err()
}

type preparedProposal struct {
	proposal      *Proposal
	draft         *ProposalRevision
	candidate     *ProposalCandidate
	reservedBytes int64
	reservation   *store.TransientReservation
}

func (r *Repo) prepareProposal(ctx context.Context, pid, proposalID string, in PreviewProposalInput) (*preparedProposal, error) {
	if in.ExpectedVersion < 1 {
		return nil, invalid("expectedVersion", "Expected a positive proposal version")
	}
	if !ValidID(in.DraftRevisionID) {
		return nil, notFound()
	}
	if err := validateProposalCommands(in.Commands); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	p, err := loadProposal(ctx, tx, pid, proposalID)
	if err != nil {
		return nil, err
	}
	if err := requireProposalDraft(p, in.ExpectedVersion, in.DraftRevisionID); err != nil {
		return nil, err
	}
	draft, err := loadProposalRevision(ctx, tx, p.ID, in.DraftRevisionID)
	if err != nil {
		return nil, err
	}
	if err := proposalCommandHistory(ctx, tx, p.ID, in.Commands); err != nil {
		return nil, err
	}
	if _, err := proposalBase(ctx, tx, pid, CreateProposalInput{BaseRevisionID: p.BaseRevisionID, RepositoryID: p.RepositoryID, DatastoreID: p.DatastoreID, FacetKey: p.FacetKey}); err != nil {
		return nil, err
	}
	// Account for in-flight work before materializing the graph. The estimate
	// uses pinned serialized inputs; reconcile it with the measured payload
	// before publishing a prepared result. Both admissions share the writer
	// with import staging mutations.
	var inputBytes int64
	if err := tx.QueryRowContext(ctx, `SELECT
	 (SELECT COALESCE(SUM(length(CAST(document AS BLOB))),0) FROM backend_graph_records WHERE project_id=? AND revision_id=?) +
	 (SELECT COALESCE(SUM(length(CAST(document AS BLOB))),0) FROM backend_revision_sources WHERE revision_id=?) +
	 (SELECT length(CAST(document AS BLOB)) FROM backend_proposal_revisions WHERE id=?)`, pid, p.BaseRevisionID, p.BaseRevisionID, draft.ID).Scan(&inputBytes); err != nil {
		return nil, err
	}
	commandJSON, err := json.Marshal(in.Commands)
	if err != nil {
		return nil, err
	}
	estimate := min(int64(MaxRevisionBytes), 4*inputBytes+8*int64(len(commandJSON))+65536)
	var reservation *store.TransientReservation
	retained := false
	defer func() {
		if reservation != nil && !retained {
			reservation.Release()
		}
	}()
	if err := r.db.Write(ctx, func(writer *sql.Tx) error {
		staged, err := stagingBytes(ctx, writer, pid)
		if err != nil {
			return err
		}
		var ok bool
		reservation, ok = r.db.ReserveTransient("backend:"+pid, estimate, MaxProjectStagingBytes-staged)
		if !ok {
			return proposalLimit("Project transient proposal preparation exceeds 512 MiB")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	state, err := loadRevisionState(ctx, tx, pid, p.BaseRevisionID)
	if err != nil {
		return nil, err
	}
	if state.Revision.SemanticHash != p.BaseSemanticHash {
		return nil, proposalConflict(p, "backend_proposal_preview_conflict", "Pinned baseline hash changed")
	}
	base := &graphCandidate{Nodes: state.Nodes, Edges: state.Edges, Evidence: state.Evidence, Sources: state.Sources, Coverage: state.Revision.Coverage}
	candidate, err := evaluateProposal(base, *p, *draft, in.Commands)
	if err != nil {
		return nil, err
	}
	baseJSON, err := json.Marshal(base)
	if err != nil {
		return nil, err
	}
	candidateJSON, err := json.Marshal(candidate)
	if err != nil {
		return nil, err
	}
	if len(baseJSON)+len(candidateJSON) > MaxRevisionBytes {
		return nil, proposalLimit("Effective proposal semantic payload exceeds 256 MiB")
	}
	reserved := int64(len(baseJSON) + len(candidateJSON))
	if err := r.db.Write(ctx, func(writer *sql.Tx) error {
		staged, err := stagingBytes(ctx, writer, pid)
		if err != nil {
			return err
		}
		if !reservation.Resize(reserved, MaxProjectStagingBytes-staged) {
			return proposalLimit("Project transient proposal preparation exceeds 512 MiB")
		}
		return nil
	}); err != nil {
		return nil, err
	}
	retained = true
	return &preparedProposal{proposal: p, draft: draft, candidate: candidate, reservedBytes: reserved, reservation: reservation}, nil
}

func (r *Repo) checkProposalStaging(ctx context.Context, tx *sql.Tx, pid string, bytes int64) error {
	n, err := stagingBytes(ctx, tx, pid)
	if err != nil {
		return err
	}
	if n+r.db.TransientBytes("backend:"+pid)+bytes > MaxProjectStagingBytes {
		return proposalLimit("Project transient proposal preparation exceeds 512 MiB")
	}
	return nil
}

func (r *Repo) PreviewProposal(ctx context.Context, pid, proposalID string, in PreviewProposalInput) (*ProposalPreview, error) {
	prepared, err := r.prepareProposal(ctx, pid, proposalID, in)
	if err != nil {
		return nil, err
	}
	defer prepared.reservation.Release()
	p, draft, candidate := prepared.proposal, prepared.draft, prepared.candidate
	return &ProposalPreview{ProposalID: p.ID, BaseRevisionID: p.BaseRevisionID, BaseSemanticHash: p.BaseSemanticHash, DraftRevisionID: draft.ID, DraftHash: draft.SemanticHash, ExpectedVersion: p.Version, CandidateHash: candidate.CandidateHash, CandidateGraphHash: candidate.GraphHash, Changes: candidate.Changes, Criteria: candidate.Criteria, Diagnostics: candidate.Diagnostics, Limitations: candidate.Limitations}, nil
}

func (r *Repo) ApplyProposal(ctx context.Context, pid, proposalID string, in ApplyProposalInput) (*ProposalApplyResult, error) {
	if !ValidID(pid) || !ValidID(proposalID) {
		return nil, notFound()
	}
	if err := validateKey(in.IdempotencyKey); err != nil {
		return nil, err
	}
	if in.ExpectedVersion < 1 || !ValidID(in.DraftRevisionID) || !validHash(in.CandidateHash) {
		return nil, invalid("body", "Expected a positive version, canonical draft UUID and candidate SHA-256")
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "proposal-apply:" + pid + ":" + proposalID
	out := new(ProposalApplyResult)
	if found, err := readProposalReceipt(ctx, r.db.R, scope, in.IdempotencyKey, digest, out); found || err != nil {
		return out, err
	}
	// Retain preparation refusals. A receipt published concurrently must win
	// before a stale-version, command-history, compatibility or quota refusal.
	prepared, preparationErr := r.prepareProposal(ctx, pid, proposalID, PreviewProposalInput{ExpectedVersion: in.ExpectedVersion, DraftRevisionID: in.DraftRevisionID, Commands: in.Commands})
	if prepared != nil {
		defer prepared.reservation.Release()
	}
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if found, err := readProposalReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out); found || err != nil {
			return err
		}
		current, err := loadProposal(ctx, tx, pid, proposalID)
		if err != nil {
			return err
		}
		if err := requireProposalDraft(current, in.ExpectedVersion, in.DraftRevisionID); err != nil {
			return err
		}
		if err := proposalCommandHistory(ctx, tx, proposalID, in.Commands); err != nil {
			return err
		}
		if preparationErr != nil {
			return preparationErr
		}
		p, draft, candidate := prepared.proposal, prepared.draft, prepared.candidate
		if current.BaseRevisionID != p.BaseRevisionID || current.BaseSemanticHash != p.BaseSemanticHash || current.RepositoryID != p.RepositoryID || current.DatastoreID != p.DatastoreID || current.FacetKey != p.FacetKey || current.DraftHash != draft.SemanticHash {
			return proposalConflict(current, "backend_proposal_preview_conflict", "Prepared proposal pins changed")
		}
		if candidate.CandidateHash == nil {
			return &FaultError{Status: 422, Code: "backend_proposal_invalid", Message: "Invalid final proposal graph", Details: map[string]any{"diagnostics": candidate.Diagnostics}}
		}
		if *candidate.CandidateHash != in.CandidateHash {
			return proposalConflict(current, "backend_proposal_preview_conflict", "Candidate hash differs from the exact preview")
		}
		if err := r.checkProposalStaging(ctx, tx, pid, 0); err != nil {
			return err
		}
		now := time.Now().UTC()
		rev := ProposalRevision{ID: uuid.NewV7().String(), ProposalID: proposalID, ParentRevisionID: new(draft.ID), DocumentVersion: ProposalDocumentVersion, SemanticHash: *candidate.CandidateHash, BaseRevisionID: p.BaseRevisionID, BaseSemanticHash: p.BaseSemanticHash, SourceSnapshotIDs: draft.SourceSnapshotIDs, ArtifactPins: draft.ArtifactPins, Commands: in.Commands, Overlays: candidate.Overlays, Criteria: candidate.Criteria, Author: "user", Summary: proposalChangeSummary(candidate.Changes), CreatedAt: now}
		document, err := json.Marshal(rev)
		if err != nil {
			return err
		}
		if len(document) > MaxRevisionBytes {
			return proposalLimit("Proposal revision exceeds the semantic payload limit")
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_proposal_revisions(id,proposal_id,parent_revision_id,document) VALUES(?,?,?,?)`, rev.ID, proposalID, draft.ID, string(document)); err != nil {
			return err
		}
		current.Version++
		current.DraftRevisionID, current.DraftHash, current.UpdatedAt = rev.ID, rev.SemanticHash, now
		if _, err := tx.ExecContext(ctx, `UPDATE backend_proposals SET version=?,draft_revision_id=?,draft_hash=?,updated_at=? WHERE id=?`, current.Version, rev.ID, rev.SemanticHash, now.Format(time.RFC3339Nano), proposalID); err != nil {
			return err
		}
		*out = ProposalApplyResult{Proposal: *current, Revision: rev, Changes: candidate.Changes, Criteria: candidate.Criteria, CandidateGraphHash: *candidate.GraphHash}
		response, err := json.Marshal(out)
		if err != nil {
			return err
		}
		_, err = tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, in.IdempotencyKey, digest, string(response))
		if err == nil {
			out.receiptJSON = string(response)
		}
		return err
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
