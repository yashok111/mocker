package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"time"
)

func findingConflict(message string) error {
	return &FaultError{Status: 409, Code: "backend_finding_conflict", Message: message}
}

// IndexFindingReportTx publishes occurrences and recurrence in the report's transaction.
func IndexFindingReportTx(ctx context.Context, tx *sql.Tx, pid string, ref FindingAnalysisRef, findings []Finding, checks []FindingCheck) error {
	positive := map[string]bool{}
	for _, c := range checks {
		positive[c.Fingerprint] = c.Status == "present"
		if _, err := backendblob.Exec(ctx, tx, `INSERT INTO backend_finding_checks(project_id,job_id,result_version,fingerprint,scope_key,status) VALUES(?,?,?,?,?,?)`, pid, ref.JobID, ref.ResultVersion, c.Fingerprint, c.ScopeKey, c.Status); err != nil {
			return err
		}
	}
	for _, f := range findings {
		raw, err := json.Marshal(f)
		if err != nil {
			return err
		}
		if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_finding_occurrences(project_id,job_id,result_version,fingerprint,basis_hash,document) VALUES(?,?,?,?,?,?)`, pid, ref.JobID, ref.ResultVersion, f.Fingerprint, f.BasisHash, string(raw)); err != nil {
			return err
		}
		var version int64
		var basis string
		err = tx.QueryRowContext(ctx, `SELECT version,basis_hash FROM backend_finding_reviews WHERE project_id=? AND fingerprint=?`, pid, f.Fingerprint).Scan(&version, &basis)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if basis == f.BasisHash || version > 0 && !positive[f.Fingerprint] {
			continue
		}
		// Replayed old evidence never reopens the current occurrence.
		var seen int
		if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_finding_occurrences_documents WHERE project_id=? AND fingerprint=? AND basis_hash=?`, pid, f.Fingerprint, f.BasisHash).Scan(&seen); err != nil {
			return err
		}
		if seen > 1 {
			continue
		}
		event := FindingReviewEvent{Version: version + 1, BasisHash: f.BasisHash, Status: "open", Reason: "New positive diagnostic basis", Author: "diagnostics", At: time.Now().UTC().Format(time.RFC3339Nano)}
		if err = writeFindingEvent(ctx, tx, pid, f.Fingerprint, event); err != nil {
			return err
		}
	}
	return nil
}
func writeFindingEvent(ctx context.Context, tx *sql.Tx, pid, fp string, event FindingReviewEvent) error {
	_, err := tx.ExecContext(ctx, `INSERT INTO backend_finding_reviews VALUES(?,?,?,?,?) ON CONFLICT(project_id,fingerprint) DO UPDATE SET version=excluded.version,basis_hash=excluded.basis_hash,status=excluded.status`, pid, fp, event.Version, event.BasisHash, event.Status)
	if err != nil {
		return err
	}
	raw, err := json.Marshal(event)
	if err != nil {
		return err
	}
	_, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_finding_events(project_id,fingerprint,version,document) VALUES(?,?,?,?)`, pid, fp, event.Version, string(raw))
	return err
}
func readFindingReview(ctx context.Context, tx *sql.Tx, pid, fp string) (*FindingReview, error) {
	out := &FindingReview{Fingerprint: fp, History: []FindingReviewEvent{}}
	err := tx.QueryRowContext(ctx, `SELECT version,basis_hash,status FROM backend_finding_reviews WHERE project_id=? AND fingerprint=?`, pid, fp).Scan(&out.Version, &out.BasisHash, &out.Status)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, notFound()
	}
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT document FROM backend_finding_events_documents WHERE project_id=? AND fingerprint=? ORDER BY version`, pid, fp)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var raw []byte
		var e FindingReviewEvent
		if err = rows.Scan(&raw); err != nil {
			return nil, err
		}
		if err = json.Unmarshal(raw, &e); err != nil {
			return nil, err
		}
		out.History = append(out.History, e)
	}
	return out, rows.Err()
}
func (r *Repo) ReviewFinding(ctx context.Context, pid, fp string, in FindingReviewInput) (*FindingReview, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	if !validHash(fp) {
		return nil, invalid("fingerprint", "Exact fingerprint required")
	}
	hash, err := requestDigest(struct {
		Fingerprint string
		Input       FindingReviewInput
	}{fp, in})
	if err != nil {
		return nil, err
	}
	var out *FindingReview
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		var savedHash string
		var raw []byte
		e := tx.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_finding_receipts WHERE project_id=? AND idempotency_key=?`, pid, in.IdempotencyKey).Scan(&savedHash, &raw)
		if e == nil {
			if savedHash != hash {
				return findingConflict("Idempotency key belongs to another request")
			}
			return json.Unmarshal(raw, &out)
		}
		if !errors.Is(e, sql.ErrNoRows) {
			return e
		}
		out, e = readFindingReview(ctx, tx, pid, fp)
		if e != nil {
			return e
		}
		if out.Version != in.ExpectedVersion || out.BasisHash != in.BasisHash {
			return findingConflict("Review version or positive basis changed; reload history")
		}
		if in.ResolutionAnalysis != nil {
			var status, scope string
			e = tx.QueryRowContext(ctx, `SELECT c.status,c.scope_key FROM backend_finding_checks c JOIN backend_analysis_jobs j ON j.project_id=c.project_id AND j.id=c.job_id WHERE c.project_id=? AND c.job_id=? AND c.result_version=? AND c.fingerprint=? AND j.status='completed' AND j.kind='diagnostics' AND j.result_version=c.result_version`, pid, in.ResolutionAnalysis.JobID, in.ResolutionAnalysis.ResultVersion, fp).Scan(&status, &scope)
			if errors.Is(e, sql.ErrNoRows) || e == nil && status != "absent" {
				return findingConflict("Resolution needs an exact completed recheck with sufficient absence coverage")
			}
			if e != nil {
				return e
			}
			var matches int
			e = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_finding_occurrences_documents o JOIN backend_finding_checks c USING(project_id,job_id,result_version,fingerprint) JOIN backend_analysis_jobs old ON old.project_id=o.project_id AND old.id=o.job_id JOIN backend_analysis_jobs recheck ON recheck.project_id=o.project_id AND recheck.id=? WHERE o.project_id=? AND o.fingerprint=? AND o.basis_hash=? AND c.scope_key=? AND recheck.created_at>old.created_at`, in.ResolutionAnalysis.JobID, pid, fp, in.BasisHash, scope).Scan(&matches)
			if e != nil {
				return e
			}
			if matches == 0 {
				return findingConflict("Recheck scope differs or predates this occurrence")
			}
		}
		if out.Version >= 1000 {
			return invalid("history", "Review history limit reached")
		}
		event := FindingReviewEvent{Version: out.Version + 1, BasisHash: in.BasisHash, Status: in.Status, Reason: in.Reason, Author: diagramActor(ctx), At: time.Now().UTC().Format(time.RFC3339Nano), ResolutionAnalysis: in.ResolutionAnalysis}
		if e = writeFindingEvent(ctx, tx, pid, fp, event); e != nil {
			return e
		}
		out.Version = event.Version
		out.Status = event.Status
		out.History = append(out.History, event)
		raw, e = json.Marshal(out)
		if e != nil {
			return e
		}
		_, e = tx.ExecContext(ctx, `INSERT INTO backend_finding_receipts VALUES(?,?,?,?)`, pid, in.IdempotencyKey, hash, string(raw))
		return e
	})
	return out, err
}

// ListBackendFindings reads an immutable result's occurrences and current reviews in one snapshot.
func (r *Repo) ListBackendFindings(ctx context.Context, pid string, ref FindingAnalysisRef, after string, limit int) (*FindingPage, error) {
	if !ValidID(ref.JobID) || ref.ResultVersion < 1 || limit < 1 || limit > 100 || after != "" && !validHash(after) {
		return nil, invalid("findings", "Exact result and bounded pagination required")
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	var exists int
	if err = tx.QueryRowContext(ctx, `SELECT count(*) FROM backend_analysis_manifests_documents m JOIN backend_analysis_jobs j ON j.project_id=m.project_id AND j.id=m.job_id WHERE m.project_id=? AND m.job_id=? AND m.result_version=? AND j.kind='diagnostics'`, pid, ref.JobID, ref.ResultVersion).Scan(&exists); err != nil {
		return nil, err
	}
	if exists != 1 {
		return nil, notFound()
	}
	rows, err := tx.QueryContext(ctx, `SELECT document FROM backend_finding_occurrences_documents WHERE project_id=? AND job_id=? AND result_version=? AND fingerprint>? ORDER BY fingerprint LIMIT ?`, pid, ref.JobID, ref.ResultVersion, after, limit+1)
	if err != nil {
		return nil, err
	}
	out := &FindingPage{Items: []FindingItem{}}
	for rows.Next() {
		var raw []byte
		var f Finding
		if err = rows.Scan(&raw); err != nil {
			rows.Close()
			return nil, err
		}
		if err = json.Unmarshal(raw, &f); err != nil {
			rows.Close()
			return nil, err
		}
		out.Items = append(out.Items, FindingItem{Finding: f, Analysis: ref})
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	if len(out.Items) > limit {
		out.Items = out.Items[:limit]
		out.NextCursor = out.Items[limit-1].Finding.Fingerprint
	}
	for i := range out.Items {
		review, e := readFindingReview(ctx, tx, pid, out.Items[i].Finding.Fingerprint)
		if e != nil {
			return nil, e
		}
		out.Items[i].Review = *review
	}
	return out, nil
}
