package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"slices"
)

// Source revisions already store a resolved immutable graph. Navigation needs neither
// bootstrapSource5's provider assertions nor evidence/snippets. Reading those
// and cloning them twice made an eight-card overview take twelve seconds on the
// Education corpus. Keep the same effective pin derivation, with lean records.
func (r *Repo) exploreSource(ctx context.Context, pid string, revision *Revision, in ExploreInput) (*ExplorePage, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	state, err := loadSourceState(ctx, tx, pid, revision.ID)
	if err != nil {
		return nil, err
	}
	state.Nodes, err = readExploreSourceNodes(ctx, tx, pid, revision.ID)
	if err != nil {
		return nil, err
	}
	state.Edges, err = readExploreSourceEdges(ctx, tx, pid, revision.ID)
	if err != nil {
		return nil, err
	}
	pins := EffectiveGraphPins{BaseRevisionID: revision.ID, BaseSemanticHash: revision.SemanticHash, EffectiveSemanticHash: revision.SemanticHash, StructuralSchemaVersion: revision.SchemaVersion, ViewSchemaVersion: revision.SchemaVersion, SourceSnapshotIDs: slices.Clone(revision.SourceSnapshotIDs), ArtifactPins: slices.Clone(revision.ArtifactPins), ArtifactContext: revisionArtifactContext(state), ArtifactContextV3: state.ArtifactContextV3}
	var vector *SourceVector
	if revision.SchemaVersion == ComposedSchemaVersion {
		var document string
		if err = tx.QueryRowContext(ctx, `SELECT document FROM backend_revision_sources_documents WHERE revision_id=?`, revision.ID).Scan(&document); err != nil {
			return nil, err
		}
		var context struct {
			SourceVector *SourceVector `json:"sourceVector"`
		}
		if err = json.Unmarshal([]byte(document), &context); err != nil {
			return nil, err
		}
		if context.SourceVector == nil {
			return nil, invalid("sourceVector", "Missing source vector for exact revision")
		}
		vector = context.SourceVector
	}
	pins.SourceVectorHash, err = requestDigest(vector)
	if err != nil {
		return nil, err
	}
	hash, err := requestDigest(struct {
		Target BackendReadTarget
		Pins   EffectiveGraphPins
	}{in.Target, pins})
	if err != nil {
		return nil, err
	}
	pins.TargetHash = hash
	return projectExplore(&EffectiveGraphSnapshot{Target: in.Target, State: *state, Pins: pins}, in)
}

func readExploreSourceNodes(ctx context.Context, q importReader, pid, rid string) ([]Node, error) {
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type='node'`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	nodes := []Node{}
	for rows.Next() {
		if err = ctx.Err(); err != nil {
			return nil, err
		}
		var document string
		if err = rows.Scan(&document); err != nil {
			return nil, err
		}
		var n Node
		if err = json.Unmarshal([]byte(document), &n); err != nil {
			return nil, err
		}
		n.Source = nil
		n.EvidenceIDs = nil
		n.Ownership = nil
		n.Freshness = nil
		n.FacetComparison = nil
		nodes = append(nodes, n)
	}
	return nodes, rows.Err()
}
func readExploreSourceEdges(ctx context.Context, q importReader, pid, rid string) ([]Edge, error) {
	rows, err := q.QueryContext(ctx, `SELECT id,kind,from_id,to_id FROM backend_graph_records WHERE project_id=? AND revision_id=? AND record_type='edge'`, pid, rid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	edges := []Edge{}
	for rows.Next() {
		var e Edge
		if err = rows.Scan(&e.ID, &e.Kind, &e.From, &e.To); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}
