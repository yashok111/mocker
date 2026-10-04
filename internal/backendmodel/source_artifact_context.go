package backendmodel

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"hash"
	"io"
	"strconv"
)

func isArtifactSourceSchema(schema string) bool {
	return isLineageSchema(schema) || schema == ComposedSchemaVersion
}

func apiPinsSourceBaselineDigest(ctx context.Context, q importReader, pid, rid string, revision Revision, frozen *APIArtifactContext) (string, error) {
	if revision.SchemaVersion == ComposedSchemaVersion {
		return artifactBaselineDigest(ctx, q, pid, rid)
	}
	return requestDigest(struct {
		Revision Revision
		Context  *APIArtifactContext
	}{revision, frozen})
}

// Source6 anchors require the complete claim vector. A RevisionState alone has
// only the selected structural values and cannot represent losing provider claims.
func artifactSourceAnchors(ctx context.Context, q importReader, state *RevisionState) (string, string, error) {
	if state.Revision.SchemaVersion != ComposedSchemaVersion {
		if frozen := revisionArtifactContext(state); frozen != nil {
			return frozen.SourceContentHash, frozen.SourceSemanticHash, nil
		}
		coverage, err := loadAPIArtifactCoverage(ctx, q, state)
		if err != nil {
			return "", "", err
		}
		content, err := APIArtifactSourceContentHash(*state, *coverage)
		return content, state.Revision.SemanticHash, err
	}
	graph, err := loadSourceGraph(ctx, q, state.Revision.ProjectID, state.Revision.ID)
	if err != nil {
		return "", "", err
	}
	raw, err := source6ContextJSON(graph)
	if err != nil {
		return "", "", err
	}
	content := hashBytes(raw)
	if content != graph.SourceContentHash {
		return "", "", semantic("sourceContentHash", "Stored source6 context differs from its complete provider claims")
	}
	semanticHash, err := source6SemanticHash(graph)
	if err != nil {
		return "", "", err
	}
	if semanticHash != state.Revision.SemanticHash {
		return "", "", semantic("semanticHash", "Stored source6 revision differs from its source and artifact context")
	}
	graph.State.ArtifactContext = nil
	graph.State.APIArtifactContext = nil
	graph.State.Revision.ArtifactPins = nil
	anchor, err := source6SemanticHash(graph)
	if err != nil {
		return "", "", err
	}
	if frozen := revisionArtifactContext(state); frozen != nil &&
		(frozen.SourceContentHash != content || frozen.SourceSemanticHash != anchor) {
		return "", "", semantic("sourceHash", "Artifact context differs from the frozen source6 anchors")
	}
	return content, anchor, nil
}

// Graph and source documents are copied before this call. The ordering keeps the
// assertion/evidence guards satisfied without decoding and re-encoding proof.
func copySource6ArtifactContext(ctx context.Context, tx *sql.Tx, pid, from, to string) error {
	for _, query := range []string{
		`INSERT INTO backend_revision_assertions
		(project_id,revision_id,record_type,record_id,repository_id,provider_namespace,external_key,assertion_hash,document)
		SELECT project_id,?,record_type,record_id,repository_id,provider_namespace,external_key,assertion_hash,document
		FROM backend_revision_assertions WHERE project_id=? AND revision_id=?`,
		`INSERT INTO backend_revision_assertion_resolutions
		(project_id,revision_id,record_type,record_id,property_key,conflict_hash,document)
		SELECT project_id,?,record_type,record_id,property_key,conflict_hash,document
		FROM backend_revision_assertion_resolutions WHERE project_id=? AND revision_id=?`,
		`INSERT INTO backend_revision_legacy_proof_bases
		(project_id,revision_id,evidence_id,source_revision_id,basis_hash,document)
		SELECT project_id,?,evidence_id,source_revision_id,basis_hash,document
		FROM backend_revision_legacy_proof_bases WHERE project_id=? AND revision_id=?`,
	} {
		if _, err := tx.ExecContext(ctx, query, to, pid, from); err != nil {
			return err
		}
	}
	return nil
}

// Artifact CAS binds raw storage, even when a storage fault changes only JSON
// whitespace. Domain hashes intentionally bind semantic claims separately.
func source6ArtifactRowsDigest(ctx context.Context, q importReader, rid string) (string, error) {
	digest := sha256.New()
	for _, table := range []string{
		"backend_graph_records", "backend_revision_sources", "backend_revision_decisions",
		"backend_revision_assertions", "backend_revision_assertion_resolutions", "backend_revision_legacy_proof_bases",
	} {
		if err := hashSourceArtifactTable(ctx, q, digest, table, rid); err != nil {
			return "", err
		}
	}
	return hex.EncodeToString(digest.Sum(nil)), nil
}

func hashSourceArtifactTable(ctx context.Context, q importReader, digest hash.Hash, table, rid string) error {
	_, _ = io.WriteString(digest, table+"\x00")
	rows, err := q.QueryContext(ctx, "SELECT document FROM "+table+" WHERE revision_id=? ORDER BY document", rid)
	if err != nil {
		return err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return err
		}
		var document string
		if err := rows.Scan(&document); err != nil {
			return err
		}
		_, _ = io.WriteString(digest, strconv.Itoa(len(document))+"\x00")
		_, _ = io.WriteString(digest, document)
	}
	_, _ = io.WriteString(digest, "\x00")
	return rows.Err()
}
