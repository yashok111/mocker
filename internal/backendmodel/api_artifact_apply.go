package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"slices"
	"strings"
	"time"
	"uuid"
)

// ReceiptBytes is the exact original response stored by the successful command.
// Transports must write these bytes directly for byte-for-byte receipt replay.
func (out APIPinsResult) ReceiptBytes() []byte { return []byte(out.receiptJSON) }

func readAPIPinsReceipt(ctx context.Context, q importReader, scope, key, digest string, out *APIPinsResult) (bool, error) {
	var previous, response string
	err := q.QueryRowContext(ctx, `SELECT request_hash,response FROM backend_command_receipts WHERE scope=? AND key=?`, scope, key).Scan(&previous, &response)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if previous != digest {
		return true, importConflict("backend_idempotency_conflict", "Idempotency key was already used with a different request", 0)
	}
	if err := json.Unmarshal([]byte(response), out); err != nil {
		return true, err
	}
	out.receiptJSON = response
	return true, nil
}

func (s *APIArtifactService) Apply(ctx context.Context, pid string, in ApplyAPIPinsInput) (*APIPinsResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if len(body) > MaxAPIPinBodyBytes {
		return nil, apiPinsLimit()
	}
	if !ValidID(pid) {
		return nil, notFound()
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "api-pins:" + pid
	out := new(APIPinsResult)
	found, err := readAPIPinsReceipt(ctx, s.repo.db.R, scope, in.IdempotencyKey, digest, out)
	if err != nil || found {
		return out, err
	}
	prepared, err := s.prepare(ctx, pid, PreviewAPIPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands})
	if err != nil {
		return nil, err
	}
	if prepared.preview.CandidateHash != in.CandidateHash {
		return nil, importConflict("backend_api_pins_hash_conflict", "Candidate differs from the exact preview", in.ExpectedVersion)
	}
	if !prepared.preview.CanApply {
		return nil, apiPinsBlocked(prepared.preview.Diagnostics)
	}
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		found, err := readAPIPinsReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out)
		if err != nil || found {
			return err
		}
		p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
		if err != nil {
			return err
		}
		if err := requireProjectVersion(p, in.ExpectedVersion); err != nil {
			return err
		}
		if p.CurrentRevisionID != in.BaseRevisionID {
			return importConflict("backend_api_pins_base_conflict", "Current source baseline changed", p.Version)
		}
		var document string
		if err := tx.QueryRowContext(ctx, `SELECT document FROM backend_revisions WHERE project_id=? AND id=?`, pid, in.BaseRevisionID).Scan(&document); err != nil {
			return err
		}
		var revision Revision
		if err := json.Unmarshal([]byte(document), &revision); err != nil {
			return err
		}
		context, err := loadAPIArtifactContext(ctx, tx, in.BaseRevisionID)
		if err != nil {
			return err
		}
		baselineHash, err := apiPinsBaselineDigest(ctx, tx, pid, in.BaseRevisionID, revision, context, prepared)
		if err != nil {
			return err
		}
		if err := s.checkFullArtifactDigests(ctx, tx, prepared, p.Version, baselineHash, revision.SchemaVersion); err != nil {
			return err
		}
		keys := []string{}
		for key := range prepared.digests {
			keys = append(keys, key)
		}
		slices.Sort(keys)
		for _, key := range keys {
			if err := ctx.Err(); err != nil {
				return err
			}
			id, rid, _ := strings.Cut(key, "/")
			if s.artifacts == nil {
				return importConflict("backend_api_pins_hash_conflict", "Exact artifact digest changed", p.Version)
			}
			current, e := s.artifacts.ArtifactDigestTx(ctx, tx, apiArtifactID(id), apiArtifactID(rid))
			if ctx.Err() != nil {
				return ctx.Err()
			}
			if e != nil || current != prepared.digests[key] {
				return importConflict("backend_api_pins_hash_conflict", "Exact artifact digest changed", p.Version)
			}
		}
		now := time.Now().UTC()
		revision.ID = uuid.NewV7().String()
		revision.ParentRevisionID = new(in.BaseRevisionID)
		revision.SemanticHash = prepared.preview.SemanticHash
		revision.ArtifactPins = prepared.preview.Pins
		revision.Author = "user"
		revision.Summary = "Explicit API artifact pin commands"
		revision.CreatedAt = now
		encoded, err := json.Marshal(revision)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, revision.ID, pid, string(encoded)); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,document) SELECT project_id,?,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,document FROM backend_graph_records WHERE project_id=? AND revision_id=?`, revision.ID, pid, in.BaseRevisionID); err != nil {
			return err
		}
		for _, table := range []string{"backend_revision_sources", "backend_revision_decisions"} {
			if _, err := tx.ExecContext(ctx, `INSERT INTO `+table+`(revision_id,document) SELECT ?,document FROM `+table+` WHERE revision_id=?`, revision.ID, in.BaseRevisionID); err != nil {
				return err
			}
		}
		if err := savePreparedAPIPinsContext(ctx, tx, revision, prepared); err != nil {
			return err
		}
		p.Version++
		p.CurrentRevisionID = revision.ID
		p.UpdatedAt = now
		if _, err := tx.ExecContext(ctx, `UPDATE backend_projects SET version=?,current_revision_id=?,updated_at=? WHERE id=?`, p.Version, revision.ID, now.Format(time.RFC3339Nano), pid); err != nil {
			return err
		}
		*out = APIPinsResult{Project: *p, Revision: revision}
		response, err := json.Marshal(out)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, in.IdempotencyKey, digest, string(response)); err != nil {
			return err
		}
		out.receiptJSON = string(response)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

func saveAPIArtifactContext(ctx context.Context, tx *sql.Tx, rid string, frozen APIArtifactContext) error {
	doc, err := json.Marshal(frozen)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES(?,?,?,?)`, rid, frozen.SourceContentHash, frozen.SourceSemanticHash, string(doc))
	return err
}

func apiPinsBaselineDigest(ctx context.Context, tx *sql.Tx, pid, revisionID string, revision Revision, context *APIArtifactContext, prepared *preparedAPIPins) (string, error) {
	baselineHash, err := requestDigest(struct {
		Revision Revision
		Context  *APIArtifactContext
	}{revision, context})
	if prepared.fullContext != nil {
		baselineHash, err = artifactBaselineDigest(ctx, tx, pid, revisionID)
	}
	return baselineHash, err
}

func (s *APIArtifactService) checkFullArtifactDigests(ctx context.Context, tx *sql.Tx, prepared *preparedAPIPins, version int64, baselineHash, schemaVersion string) error {
	if baselineHash != prepared.baselineHash || !isLineageSchema(schemaVersion) {
		return importConflict("backend_api_pins_base_conflict", "Frozen source baseline changed", version)
	}
	if prepared.fullContext != nil {
		if err := NewArtifactService(s.repo, s.artifacts, nil).checkArtifactDigests(ctx, tx, prepared.artifactDigests, version); err != nil {
			return err
		}
	}
	return nil
}

func savePreparedAPIPinsContext(ctx context.Context, tx *sql.Tx, revision Revision, prepared *preparedAPIPins) error {
	if prepared.fullContext != nil {
		if err := saveArtifactContext(ctx, tx, revision.ID, *prepared.fullContext, revision.ArtifactPins); err != nil {
			return err
		}
	} else {
		if err := saveAPIArtifactContext(ctx, tx, revision.ID, prepared.frozen); err != nil {
			return err
		}
	}
	return nil
}
