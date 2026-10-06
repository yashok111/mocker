package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
	"github.com/yashok111/mocker/internal/backendblob"
	"slices"
	"strings"
	"time"
	"uuid"
)

func (out ArtifactPinsResult) ReceiptBytes() []byte { return []byte(out.receiptJSON) }
func readArtifactPinsReceipt(ctx context.Context, q importReader, scope, key, digest string, out *ArtifactPinsResult) (bool, error) {
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
	if err = json.Unmarshal([]byte(response), out); err != nil {
		return true, err
	}
	out.receiptJSON = response
	return true, nil
}
func (s *ArtifactService) Apply(ctx context.Context, pid string, in ApplyArtifactPinsInput) (*ArtifactPinsResult, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	body, err := json.Marshal(in)
	if err != nil {
		return nil, err
	}
	if err = artifactBodyLimit(body); err != nil {
		return nil, err
	}
	if !ValidID(pid) {
		return nil, notFound()
	}
	digest, err := requestDigest(in)
	if err != nil {
		return nil, err
	}
	scope := "artifact-pins:" + pid
	out := new(ArtifactPinsResult)
	found, err := readArtifactPinsReceipt(ctx, s.repo.db.R, scope, in.IdempotencyKey, digest, out)
	if found || err != nil {
		return out, err
	}
	if err := s.admitApplyBody(pid, PreviewArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands}); err != nil {
		return nil, err
	}
	prepared, err := s.prepareArtifacts(ctx, pid, PreviewArtifactPinsInput{in.BaseRevisionID, in.ExpectedVersion, in.Commands})
	if err != nil {
		return nil, err
	}
	if prepared.preview.CandidateHash != in.CandidateHash {
		return nil, importConflict("backend_artifact_pins_hash_conflict", "Candidate differs from the exact preview", in.ExpectedVersion)
	}
	if !prepared.preview.CanApply {
		return nil, artifactPinsBlocked(prepared.preview.Diagnostics)
	}
	err = s.repo.db.Write(ctx, func(tx *sql.Tx) error {
		return s.applyArtifactPinsTx(ctx, tx, pid, in, prepared, scope, digest, out)
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
func (s *ArtifactService) checkArtifactDigests(ctx context.Context, tx *sql.Tx, digests map[editorSnapshotKey]string, version int64) error {
	keys := make([]editorSnapshotKey, 0, len(digests))
	for key := range digests {
		keys = append(keys, key)
	}
	slices.SortFunc(keys, func(a, b editorSnapshotKey) int {
		if n := strings.Compare(a.Kind, b.Kind); n != 0 {
			return n
		}
		if n := strings.Compare(a.ID, b.ID); n != 0 {
			return n
		}
		return strings.Compare(a.revision, b.revision)
	})
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		var current string
		var err error
		if key.Kind == "api_design" && s.api != nil {
			current, err = s.api.ArtifactDigestTx(ctx, tx, apiArtifactID(key.ID), apiArtifactID(key.revision))
		} else if key.Kind == "design_scenario" && s.scenarios != nil {
			current, err = s.scenarios.ArtifactDigestTx(ctx, tx, apiArtifactID(key.ID), apiArtifactID(key.revision))
		} else {
			err = sql.ErrNoRows
		}
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil || current != digests[key] {
			return importConflict("backend_artifact_pins_hash_conflict", "Exact immutable artifact digest changed", version)
		}
	}
	return nil
}

func (s *ArtifactService) applyArtifactPinsTx(ctx context.Context, tx *sql.Tx, pid string, in ApplyArtifactPinsInput, prepared *preparedArtifactPins, scope, digest string, out *ArtifactPinsResult) error {
	found, err := readArtifactPinsReceipt(ctx, tx, scope, in.IdempotencyKey, digest, out)
	if found || err != nil {
		return err
	}
	p, err := scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
	if err != nil {
		return err
	}
	if err = requireProjectVersion(p, in.ExpectedVersion); err != nil {
		return err
	}
	if p.CurrentRevisionID != in.BaseRevisionID {
		return importConflict("backend_artifact_pins_base_conflict", "Current source baseline changed", p.Version)
	}
	baseline, err := artifactBaselineDigest(ctx, tx, pid, in.BaseRevisionID)
	if err != nil {
		return err
	}
	if baseline != prepared.baselineHash {
		return importConflict("backend_artifact_pins_base_conflict", "Frozen source baseline changed", p.Version)
	}
	var document string
	if err = tx.QueryRowContext(ctx, `SELECT document FROM backend_revisions_documents WHERE project_id=? AND id=?`, pid, in.BaseRevisionID).Scan(&document); err != nil {
		return err
	}
	var revision Revision
	if err = json.Unmarshal([]byte(document), &revision); err != nil {
		return err
	}
	if !isArtifactSourceSchema(revision.SchemaVersion) {
		return importConflict("backend_artifact_pins_base_conflict", "Source baseline schema changed", p.Version)
	}
	if _, err = loadArtifactContext(ctx, tx, in.BaseRevisionID, revision.ArtifactPins); err != nil {
		return err
	}
	if err = s.checkArtifactDigests(ctx, tx, prepared.digests, p.Version); err != nil {
		return err
	}
	return persistArtifactPins(ctx, tx, pid, in, prepared, scope, digest, out, p, revision)
}

func persistArtifactPins(ctx context.Context, tx *sql.Tx, pid string, in ApplyArtifactPinsInput, prepared *preparedArtifactPins, scope, digest string, out *ArtifactPinsResult, p *Project, revision Revision) error {
	now := time.Now().UTC()
	revision.ID = uuid.NewV7().String()
	revision.ParentRevisionID = new(in.BaseRevisionID)
	revision.SemanticHash = prepared.preview.SemanticHash
	revision.ArtifactPins = prepared.preview.Pins
	revision.Author = "user"
	revision.Summary = "Explicit artifact pin commands"
	revision.CreatedAt = now
	encoded, err := json.Marshal(revision)
	if err != nil {
		return err
	}
	if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_revisions(id,project_id,document) VALUES(?,?,?)`, revision.ID, pid, string(encoded)); err != nil {
		return err
	}
	if _, err = backendblob.Exec(ctx, tx, `INSERT INTO backend_graph_records(project_id,revision_id,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,payload_key) SELECT project_id,?,record_type,id,kind,name,parent_id,from_id,to_id,subject_id,payload_key FROM backend_graph_records WHERE project_id=? AND revision_id=?`, revision.ID, pid, in.BaseRevisionID); err != nil {
		return err
	}
	for _, query := range []string{
		`INSERT INTO backend_revision_sources(revision_id,payload_key) SELECT ?,payload_key FROM backend_revision_sources WHERE revision_id=?`,
		`INSERT INTO backend_revision_decisions(revision_id,payload_key) SELECT ?,payload_key FROM backend_revision_decisions WHERE revision_id=?`,
	} {
		if _, err = backendblob.Exec(ctx, tx, query, revision.ID, in.BaseRevisionID); err != nil {
			return err
		}
	}
	if revision.SchemaVersion == ComposedSchemaVersion {
		if err = copySource6ArtifactContext(ctx, tx, pid, in.BaseRevisionID, revision.ID); err != nil {
			return err
		}
	}
	if err = saveArtifactContext(ctx, tx, revision.ID, prepared.frozen, revision.ArtifactPins); err != nil {
		return err
	}
	p.Version++
	p.CurrentRevisionID = revision.ID
	p.UpdatedAt = now
	if _, err = tx.ExecContext(ctx, `UPDATE backend_projects SET version=?,current_revision_id=?,updated_at=? WHERE id=?`, p.Version, revision.ID, now.Format(time.RFC3339Nano), pid); err != nil {
		return err
	}
	*out = ArtifactPinsResult{Project: *p, Revision: revision}
	response, err := json.Marshal(out)
	if err != nil {
		return err
	}
	if _, err = tx.ExecContext(ctx, `INSERT INTO backend_command_receipts(scope,key,request_hash,response) VALUES(?,?,?,?)`, scope, in.IdempotencyKey, digest, string(response)); err != nil {
		return err
	}
	out.receiptJSON = string(response)
	return nil
}
