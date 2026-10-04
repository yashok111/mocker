package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"errors"
)

func loadArtifactContext(ctx context.Context, q importReader, rid string, pins []ArtifactPin) (*ArtifactContext, error) {
	var available int
	if err := q.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='backend_revision_api_artifacts'`).Scan(&available); err != nil {
		return nil, err
	}
	if available == 0 {
		return nil, nil
	}
	var doc, content, semantic string
	err := q.QueryRowContext(ctx, `SELECT source_content_hash,source_semantic_hash,document FROM backend_revision_api_artifacts WHERE revision_id=?`, rid).Scan(&content, &semantic, &doc)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	fields, err := relationalObject([]byte(doc))
	if err != nil {
		return nil, invalid("context", "Expected artifact context object")
	}
	if fields["documentVersion"] == nil && (fields["apiBindings"] != nil || fields["editorBindings"] != nil) {
		return nil, invalid("context", "Editor collections require a tagged artifact context")
	}
	c, err := DecodeArtifactContext([]byte(doc), pins)
	if err != nil {
		return nil, err
	}
	if c.SourceContentHash != content || c.SourceSemanticHash != semantic {
		return nil, invalid("context", "Frozen source anchors differ from row columns")
	}
	return c, nil
}

func legacyArtifactContext(c *ArtifactContext) *APIArtifactContext {
	if c == nil {
		return nil
	}
	return &APIArtifactContext{SourceContentHash: c.SourceContentHash, SourceSemanticHash: c.SourceSemanticHash, Bindings: c.APIBindings}
}

func revisionArtifactContext(s *RevisionState) *ArtifactContext {
	if s.ArtifactContext != nil {
		return s.ArtifactContext
	}
	if s.APIArtifactContext == nil {
		return nil
	}
	c := s.APIArtifactContext
	return &ArtifactContext{SourceContentHash: c.SourceContentHash, SourceSemanticHash: c.SourceSemanticHash, APIBindings: c.Bindings, EditorBindings: []EditorBinding{}}
}

func saveArtifactContext(ctx context.Context, tx *sql.Tx, rid string, c ArtifactContext, pins []ArtifactPin) error {
	raw, err := EncodeArtifactContext(c, pins)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO backend_revision_api_artifacts(revision_id,source_content_hash,source_semantic_hash,document) VALUES(?,?,?,?)`, rid, c.SourceContentHash, c.SourceSemanticHash, string(raw))
	return err
}

// CAS binds stored bytes, including the complete tagged roster and discriminator.
func artifactBaselineDigest(ctx context.Context, q importReader, pid, rid string) (string, error) {
	var revision, document string
	if err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions WHERE project_id=? AND id=?`, pid, rid).Scan(&revision); err != nil {
		return "", err
	}
	var sourceContent, sourceSemantic string
	err := q.QueryRowContext(ctx, `SELECT source_content_hash,source_semantic_hash,document FROM backend_revision_api_artifacts WHERE revision_id=?`, rid).Scan(&sourceContent, &sourceSemantic, &document)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", err
	}
	baseline := struct{ Revision, Context, SourceContent, SourceSemantic string }{revision, document, sourceContent, sourceSemantic}
	var descriptor Revision
	if err := json.Unmarshal([]byte(revision), &descriptor); err != nil {
		return "", err
	}
	if descriptor.SchemaVersion == ComposedSchemaVersion {
		sourceRows, err := source6ArtifactRowsDigest(ctx, q, rid)
		if err != nil {
			return "", err
		}
		return requestDigest(struct {
			Baseline   any
			SourceRows string
		}{baseline, sourceRows})
	}
	return requestDigest(baseline)
}

func loadLegacyArtifactContext(ctx context.Context, q importReader, rid string) (*APIArtifactContext, error) {
	var doc string
	err := q.QueryRowContext(ctx, `SELECT document FROM backend_revisions WHERE id=?`, rid).Scan(&doc)
	if err != nil {
		return nil, err
	}
	var revision Revision
	if err := json.Unmarshal([]byte(doc), &revision); err != nil {
		return nil, err
	}
	c, err := loadArtifactContext(ctx, q, rid, revision.ArtifactPins)
	return legacyArtifactContext(c), err
}
