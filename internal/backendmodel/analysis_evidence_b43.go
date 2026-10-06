package backendmodel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json/v2"
	"errors"
	"maps"
	"slices"
	"strings"
)

type AnalysisEvidenceDocumentPin struct {
	RevisionID  string `json:"revisionId"`
	Kind        string `json:"kind"`
	ContentHash string `json:"contentHash"`
}
type AnalysisEvidenceRequest struct {
	Targets            []BackendReadTarget
	BaselineRevisionID string
	ResultRevisionID   string
	Attachments        []TestAttachmentRef
}

// AnalysisProposalBaseRevision reads only immutable metadata, before graph admission.
func (r *Repo) AnalysisProposalBaseRevision(ctx context.Context, pid string, p ProposalReadTarget) (string, error) {
	if !ValidID(p.ProposalID) || !ValidID(p.ProposalRevisionID) {
		return "", invalid("changeProposal", "Exact saved proposal required")
	}
	var rid string
	err := r.db.R.QueryRowContext(ctx, `SELECT base_revision_id FROM backend_change_proposal_revisions_documents WHERE project_id=? AND proposal_id=? AND id=?`, pid, p.ProposalID, p.ProposalRevisionID).Scan(&rid)
	if errors.Is(err, sql.ErrNoRows) {
		return "", &FaultError{Status: 404, Code: "backend_change_not_found", Message: "Change proposal revision not found"}
	}
	return rid, err
}
func evidenceTable(kind string) string {
	if kind == "revision_decisions" {
		return "backend_revision_decisions_documents"
	}
	return "backend_revision_sources_documents"
}
func evidenceRevisionIDs(req AnalysisEvidenceRequest) []string {
	ids := []string{}
	for _, id := range []string{req.BaselineRevisionID, req.ResultRevisionID} {
		if id != "" {
			ids = append(ids, id)
		}
	}
	for _, a := range req.Attachments {
		if a.Kind == "source" {
			ids = append(ids, a.RevisionID)
		}
	}
	slices.Sort(ids)
	return slices.Compact(ids)
}
func (r *Repo) AnalysisEvidenceInputFootprint(ctx context.Context, pid string, req AnalysisEvidenceRequest) (AnalysisInputFootprint, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return AnalysisInputFootprint{}, err
	}
	defer func() { _ = tx.Rollback() }()
	b := analysisFootprintBuilder{ctx: ctx, q: tx, pid: pid, documents: map[string]int64{}}
	for _, target := range req.Targets {
		if err = b.target(target); err != nil {
			return AnalysisInputFootprint{}, err
		}
	}
	for _, attachment := range req.Attachments {
		if err = attachment.Validate(); err != nil {
			return AnalysisInputFootprint{}, err
		}
		raw, e := json.Marshal(attachment)
		if e != nil {
			return AnalysisInputFootprint{}, e
		}
		if err = b.references(raw); err != nil {
			return AnalysisInputFootprint{}, err
		}
	}
	for _, rid := range evidenceRevisionIDs(req) {
		if err = b.source(rid); err != nil {
			return AnalysisInputFootprint{}, err
		}
	}
	var revisions []string
	for key := range b.documents {
		if rid, ok := strings.CutPrefix(key, "source:"); ok {
			revisions = append(revisions, rid)
		}
	}
	slices.Sort(revisions)
	for _, rid := range revisions {
		if err = b.source(rid); err != nil {
			return AnalysisInputFootprint{}, err
		}
		for _, kind := range []string{"revision_decisions", "revision_coverage"} {
			var size int64
			err = tx.QueryRowContext(ctx, `SELECT length(CAST(d.document AS BLOB)) FROM `+evidenceTable(kind)+` d JOIN backend_revisions_documents r ON r.id=d.revision_id WHERE r.project_id=? AND r.id=?`, pid, rid).Scan(&size)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return AnalysisInputFootprint{}, err
			}
			// Coverage is included in the source document aggregate. A zero-cost alias
			// would break byte admission; count its physical bytes only once instead.
			if kind == "revision_coverage" {
				continue
			}
			if err = b.add(kind+":"+rid, size); err != nil {
				return AnalysisInputFootprint{}, err
			}
		}
	}
	f := AnalysisInputFootprint{TotalBytes: b.total, repo: r, projectID: pid}
	for _, key := range slices.Sorted(maps.Keys(b.documents)) {
		f.Documents = append(f.Documents, AnalysisDocumentBytes{Key: key, Bytes: b.documents[key]})
	}
	f.seal, err = requestDigest(f.Documents)
	return f, err
}
func (r *Repo) readAnalysisEvidenceDocument(ctx context.Context, pid, rid, kind string) ([]byte, error) {
	lease := analysisLease(ctx)
	if lease == nil {
		return nil, invalid("analysisInput", "Evidence requires a reserved immutable input")
	}
	if err := lease.valid(r, pid); err != nil {
		return nil, err
	}
	query := ` FROM ` + evidenceTable(kind) + ` d JOIN backend_revisions_documents r ON r.id=d.revision_id WHERE r.project_id=? AND r.id=?`
	var size int64
	err := r.db.R.QueryRowContext(ctx, `SELECT length(CAST(d.document AS BLOB))`+query, pid, rid).Scan(&size)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	key := kind + ":" + rid
	if kind == "revision_coverage" {
		key = "source:" + rid
	}
	if err = lease.admit(key, size); err != nil {
		return nil, err
	}
	var raw []byte
	err = r.db.R.QueryRowContext(ctx, `SELECT d.document`+query, pid, rid).Scan(&raw)
	return raw, err
}
func (r *Repo) ReadAnalysisEvidencePins(ctx context.Context, pid string, req AnalysisEvidenceRequest) ([]AnalysisEvidenceDocumentPin, error) {
	pins := []AnalysisEvidenceDocumentPin{}
	footprint, err := r.AnalysisEvidenceInputFootprint(ctx, pid, req)
	if err != nil {
		return nil, err
	}
	var revisions []string
	for _, doc := range footprint.Documents {
		if rid, ok := strings.CutPrefix(doc.Key, "source:"); ok {
			revisions = append(revisions, rid)
		}
	}
	for _, rid := range revisions {
		for _, kind := range []string{"revision_decisions", "revision_coverage"} {
			raw, err := r.readAnalysisEvidenceDocument(ctx, pid, rid, kind)
			if err != nil {
				return nil, err
			}
			if raw == nil {
				continue
			}
			hash := sha256.Sum256(raw)
			pins = append(pins, AnalysisEvidenceDocumentPin{RevisionID: rid, Kind: kind, ContentHash: hex.EncodeToString(hash[:])})
		}
	}
	slices.SortFunc(pins, func(a, b AnalysisEvidenceDocumentPin) int {
		if c := strings.Compare(a.RevisionID, b.RevisionID); c != 0 {
			return c
		}
		return strings.Compare(a.Kind, b.Kind)
	})
	return pins, nil
}
