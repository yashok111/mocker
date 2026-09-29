package apidesign

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"

	"github.com/yashok111/mocker/internal/specs"
)

// AnalyzeImpact reads immutable API snapshots in one transaction, then analyzes
// them after releasing the reader. Scenario snapshots are read separately.
func (r *Repo) AnalyzeImpact(ctx context.Context, id int64, in ImpactInput) (*ImpactReport, error) {
	report, _, err := r.AnalyzeImpactWithDocuments(ctx, id, in)
	return report, err
}

// AnalyzeImpactWithDocuments also returns the compared bytes for in-process
// consumers. The report does not serialize these potentially large snapshots.
func (r *Repo) AnalyzeImpactWithDocuments(ctx context.Context, id int64, in ImpactInput) (*ImpactReport, ImpactDocumentPair, error) {
	if in.FromRevisionID <= 0 {
		return nil, ImpactDocumentPair{}, invalidField("/fromRevisionId", "Укажите положительный fromRevisionId")
	}
	if (in.Document == nil) == (in.ToRevisionID == nil) {
		return nil, ImpactDocumentPair{}, invalidField("", "Укажите ровно одно из document или toRevisionId")
	}
	if in.ToRevisionID != nil && *in.ToRevisionID <= 0 {
		return nil, ImpactDocumentPair{}, invalidField("/toRevisionId", "Укажите положительный toRevisionId")
	}
	if in.Document != nil && int64(len(*in.Document)) > r.cfg.MaxBody {
		return nil, ImpactDocumentPair{}, specs.ErrTooLarge
	}
	design, before, after, err := r.impactSnapshots(ctx, id, in)
	if err != nil {
		return nil, ImpactDocumentPair{}, err
	}
	if int64(len(before)) > r.cfg.MaxBody || int64(len(after)) > r.cfg.MaxBody {
		return nil, ImpactDocumentPair{}, specs.ErrTooLarge
	}
	analysis, err := AnalyzeImpactDocuments(ctx, before, after)
	if err != nil {
		return nil, ImpactDocumentPair{}, err
	}
	return &ImpactReport{
		ImpactAnalysis: analysis,
		FieldImpacts:   []ImpactFieldImpact{},
		DesignID:       id, Version: design.Version,
		FromRevisionID: in.FromRevisionID, ToRevisionID: in.ToRevisionID,
		FromHash:     fmt.Sprintf("%x", sha256.Sum256([]byte(before))),
		ProposedHash: fmt.Sprintf("%x", sha256.Sum256([]byte(after))),
	}, ImpactDocumentPair{Before: before, Proposed: after}, nil
}

func (r *Repo) impactSnapshots(ctx context.Context, id int64, in ImpactInput) (Design, string, string, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return Design{}, "", "", err
	}
	defer func() { _ = tx.Rollback() }()
	design, err := getDesign(ctx, tx, id)
	if err != nil {
		return Design{}, "", "", err
	}
	before, err := r.impactRevisionDocument(ctx, tx, id, in.FromRevisionID)
	if err != nil {
		return Design{}, "", "", err
	}
	var after string
	if in.Document != nil {
		after = *in.Document
	} else {
		document, err := r.impactRevisionDocument(ctx, tx, id, *in.ToRevisionID)
		if err != nil {
			return Design{}, "", "", err
		}
		after = document
	}
	if err := tx.Commit(); err != nil {
		return Design{}, "", "", err
	}
	return design, before, after, nil
}

// Read stored bytes directly: getRevision enriches legacy operation identities
// and re-serializes documents, which would change both comparison and hash.
func (r *Repo) impactRevisionDocument(
	ctx context.Context,
	tx *sql.Tx,
	designID int64,
	revisionID int64,
) (string, error) {
	var bytes int64
	err := tx.QueryRowContext(
		ctx,
		`SELECT length(CAST(document AS BLOB)) FROM api_design_revisions WHERE design_id=? AND id=?`,
		designID,
		revisionID,
	).Scan(&bytes)
	if err != nil {
		return "", notFound(err)
	}
	if bytes > r.cfg.MaxBody {
		return "", specs.ErrTooLarge
	}
	var document string
	err = tx.QueryRowContext(
		ctx,
		`SELECT document FROM api_design_revisions WHERE design_id=? AND id=?`,
		designID,
		revisionID,
	).Scan(&document)
	return document, notFound(err)
}
