package designscenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ArtifactSnapshot retains the raw pair separately from its decoded document.
// ContentHash uses the owner's document/formDrafts envelope; DocumentHash is
// SHA256 of the raw stored document alone.
type ArtifactSnapshot struct {
	ScenarioID, RevisionID, Version int64
	ContentHash, DocumentHash       string
	DocumentJSON, FormDraftsJSON    string
	Document                        Document
}

// readArtifactSnapshot reads one exact immutable revision. SQL measures both UTF-8
// bodies as blobs and suppresses both before driver/Go allocation if their
// aggregate exceeds the configured owner bound. All metadata, lengths and
// guarded bodies come from the same row read, preventing a check/read race.
func (r *Repo) readArtifactSnapshot(ctx context.Context, scenarioID, revisionID int64) (*ArtifactSnapshot, error) {
	return r.readArtifactSnapshotFrom(ctx, r.db.R, scenarioID, revisionID)
}
func (r *Repo) readArtifactSnapshotFrom(ctx context.Context, q queryer, scenarioID, revisionID int64) (*ArtifactSnapshot, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if scenarioID <= 0 || revisionID <= 0 {
		return nil, artifactInvalid("", "Укажите положительные ID сценария и ревизии")
	}
	out := &ArtifactSnapshot{}
	var size int64
	err := q.QueryRowContext(ctx, `SELECT r.scenario_id,r.id,r.version,
		CASE WHEN length(CAST(r.hash AS BLOB))=64 THEN r.hash ELSE '' END,
		length(CAST(r.document AS BLOB))+length(CAST(r.form_drafts AS BLOB)),
		CASE WHEN ?<=0 OR length(CAST(r.document AS BLOB))+length(CAST(r.form_drafts AS BLOB))<=? THEN r.document ELSE '' END,
		CASE WHEN ?<=0 OR length(CAST(r.document AS BLOB))+length(CAST(r.form_drafts AS BLOB))<=? THEN r.form_drafts ELSE '' END
		FROM design_scenario_revisions r JOIN design_scenarios s ON s.id=r.scenario_id
		WHERE s.id=? AND r.id=?`, r.cfg.MaxBody, r.cfg.MaxBody, r.cfg.MaxBody, r.cfg.MaxBody, scenarioID, revisionID).
		Scan(&out.ScenarioID, &out.RevisionID, &out.Version, &out.ContentHash, &size, &out.DocumentJSON, &out.FormDraftsJSON)
	if err != nil {
		return nil, notFound(err)
	}
	if r.cfg.MaxBody > 0 && size > r.cfg.MaxBody {
		return nil, ErrTooLarge
	}
	if err := artifactDigestValid(out.ContentHash); err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ArtifactSnapshot keeps the strict typed decode and verified envelope contract.
func (r *Repo) ArtifactSnapshot(ctx context.Context, scenarioID, revisionID int64) (*ArtifactSnapshot, error) {
	out, err := r.readArtifactSnapshot(ctx, scenarioID, revisionID)
	if err != nil {
		return nil, err
	}
	if err := jsonx.Unmarshal([]byte(out.DocumentJSON), &out.Document); err != nil {
		return nil, fmt.Errorf("decode design scenario document: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var formDrafts map[string]string
	if err := jsonx.Unmarshal([]byte(out.FormDraftsJSON), &formDrafts); err != nil {
		return nil, fmt.Errorf("decode design scenario form drafts: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	_, hash, err := encodeScenarioEnvelope(out.Document, formDrafts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hash != out.ContentHash {
		return nil, artifactInvalid("/contentHash", "Хеш сохранённого сценария не совпадает")
	}
	out.DocumentHash = fmt.Sprintf("%x", sha256.Sum256([]byte(out.DocumentJSON)))
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

// ArtifactDigestTx checks exact ownership and bounded stored digest metadata in
// the caller's transaction. It does not decode bodies or verify their hashes.
func (r *Repo) ArtifactDigestTx(ctx context.Context, tx *sql.Tx, scenarioID, revisionID int64) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if scenarioID <= 0 || revisionID <= 0 {
		return "", artifactInvalid("", "Укажите положительные ID сценария и ревизии")
	}
	var digest string
	err := tx.QueryRowContext(ctx, `SELECT CASE WHEN length(CAST(r.hash AS BLOB))=64 THEN r.hash ELSE '' END
		FROM design_scenario_revisions r JOIN design_scenarios s ON s.id=r.scenario_id
		WHERE s.id=? AND r.id=?`, scenarioID, revisionID).Scan(&digest)
	if err != nil {
		return "", notFound(err)
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := artifactDigestValid(digest); err != nil {
		return "", err
	}
	return digest, nil
}

func artifactDigestValid(digest string) error {
	if len(digest) != 64 || strings.Trim(digest, "0123456789abcdef") != "" {
		return artifactInvalid("/contentHash", "Некорректный хеш сохранённой ревизии")
	}
	return nil
}

func artifactInvalid(pointer, message string) error {
	return &InvalidError{Diagnostics: []Diagnostic{{Pointer: pointer, Message: message, Severity: "error"}}}
}
