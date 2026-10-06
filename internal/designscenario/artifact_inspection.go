package designscenario

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"

	"github.com/yashok111/mocker/internal/jsonx"
)

// ArtifactInspectionSnapshot is an exact bounded raw read with explicit typed
// and envelope status. StoredContentHash is metadata, not proof of verification.
// ContentHash is present only when the unchanged owner encoder verifies it.
type ArtifactInspectionSnapshot struct {
	ScenarioID, RevisionID, Version              int64
	StoredContentHash, ContentHash, DocumentHash string
	DocumentJSON, FormDraftsJSON                 string
	TypedStatus, EnvelopeVerification            string
	Document                                     Document
}

func (r *Repo) ArtifactInspectionSnapshot(ctx context.Context, scenarioID, revisionID int64) (*ArtifactInspectionSnapshot, error) {
	return r.artifactInspectionSnapshotFrom(ctx, r.db.R, scenarioID, revisionID)
}
func (r *Repo) ArtifactInspectionSnapshotTx(ctx context.Context, tx *sql.Tx, scenarioID, revisionID int64) (*ArtifactInspectionSnapshot, error) {
	return r.artifactInspectionSnapshotFrom(ctx, tx, scenarioID, revisionID)
}
func (r *Repo) artifactInspectionSnapshotFrom(ctx context.Context, q queryer, scenarioID, revisionID int64) (*ArtifactInspectionSnapshot, error) {
	raw, err := r.readArtifactSnapshotFrom(ctx, q, scenarioID, revisionID)
	if err != nil {
		return nil, err
	}
	// Validate JSON syntax and duplicate names at every nesting level before
	// unsupported typed content may be returned as a qualified raw inspection.
	var fields map[string]jsontext.Value
	if err := json.Unmarshal([]byte(raw.DocumentJSON), &fields); err != nil {
		return nil, fmt.Errorf("decode inspection document: %w", err)
	}
	if fields == nil {
		return nil, artifactInvalid("/document", "Ожидается JSON-объект")
	}
	var draftFields map[string]jsontext.Value
	if err := json.Unmarshal([]byte(raw.FormDraftsJSON), &draftFields); err != nil {
		return nil, fmt.Errorf("decode inspection drafts: %w", err)
	}
	var drafts map[string]string
	// Use the exact old string-map decode policy, including whole null and null
	// members normalized by jsonx. Numbers/arrays/objects remain hard errors.
	if err := jsonx.Unmarshal([]byte(raw.FormDraftsJSON), &drafts); err != nil {
		return nil, fmt.Errorf("decode inspection drafts: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := &ArtifactInspectionSnapshot{ScenarioID: raw.ScenarioID, RevisionID: raw.RevisionID, Version: raw.Version, StoredContentHash: raw.ContentHash, DocumentHash: fmt.Sprintf("%x", sha256.Sum256([]byte(raw.DocumentJSON))), DocumentJSON: raw.DocumentJSON, FormDraftsJSON: raw.FormDraftsJSON, TypedStatus: "unsupported", EnvelopeVerification: "unavailable"}
	if err := jsonx.Unmarshal([]byte(raw.DocumentJSON), &out.Document); err != nil {
		// The current encoder cannot establish the old envelope hash for an unknown
		// typed document. Retain raw bytes without inventing a verification result.
		out.Document = Document{}
		return out, ctx.Err()
	}
	_, hash, err := encodeScenarioEnvelope(out.Document, drafts)
	if err != nil {
		return nil, err
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if hash != raw.ContentHash {
		return nil, artifactInvalid("/contentHash", "Хеш сохранённого сценария не совпадает")
	}
	out.TypedStatus, out.EnvelopeVerification, out.ContentHash = "supported", "verified", hash
	return out, nil
}
