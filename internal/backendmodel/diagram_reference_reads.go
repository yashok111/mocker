package backendmodel

import (
	"context"
	"database/sql"
	"encoding/json/v2"
	"maps"
	"slices"
)

// A native source5 interaction validates an explicit reference set. It does not
// traverse the source graph. Never pass this partial State to general builders
// or architecture projections. Other kinds, artifacts and composed/proposed
// targets keep the complete native/full resolver and its exact proof semantics.
func (r *Repo) readDiagramReferenceGraph(ctx context.Context, pid string, d DiagramDocument) (*EffectiveGraphSnapshot, error) {
	if err := r.validateAnalysisLeaseTarget(ctx, pid, d.Target); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return loadDiagramReferenceGraph(ctx, tx, pid, d)
}

func interactionReferenceIDs(d DiagramDocument) (map[string][]string, bool) {
	if d.Kind != "interactions" || d.Interactions == nil || d.Target.RevisionID == "" {
		return nil, false
	}
	sets := map[string]map[string]bool{"node": {}, "edge": {}, "evidence": {}}
	add := func(ref DiagramRef) bool {
		if ref.Kind != "record" || sets[ref.RecordType] == nil {
			return false
		}
		sets[ref.RecordType][ref.ID] = true
		return true
	}
	for _, ref := range d.Interactions.ScopeRefs {
		if !add(ref) {
			return nil, false
		}
	}
	for _, base := range diagramBases(d) {
		for _, ref := range base.refs {
			if !add(ref) {
				return nil, false
			}
		}
		for _, proof := range base.origin.Evidence {
			// Retained historical proof is handled as a gap by the existing resolver.
			// Loading a row from another revision would incorrectly upgrade it.
			if proof.RevisionID == d.Target.RevisionID {
				sets["evidence"][proof.EvidenceID] = true
			}
		}
	}
	out := map[string][]string{}
	for kind, ids := range sets {
		out[kind] = slices.Sorted(maps.Keys(ids))
	}
	return out, true
}

func loadDiagramReferenceGraph(ctx context.Context, q importReader, pid string, d DiagramDocument) (*EffectiveGraphSnapshot, error) {
	if err := d.Target.Validate(); err != nil {
		return nil, err
	}
	ids, scoped := interactionReferenceIDs(d)
	if !scoped {
		return loadNativeProjectionGraph(ctx, q, pid, d.Target)
	}
	state, err := loadSourceState(ctx, q, pid, d.Target.RevisionID)
	if err != nil {
		return nil, err
	}
	if state.Revision.SchemaVersion != EventsSchemaVersion {
		return loadNativeProjectionGraph(ctx, q, pid, d.Target)
	}
	remaining := MaxRevisionBytes
	state.Nodes, err = readDiagramRecords[Node](ctx, q, pid, d.Target.RevisionID, "node", ids["node"], &remaining)
	if err != nil {
		return nil, err
	}
	state.Edges, err = readDiagramRecords[Edge](ctx, q, pid, d.Target.RevisionID, "edge", ids["edge"], &remaining)
	if err != nil {
		return nil, err
	}
	state.Evidence, err = readDiagramRecords[Evidence](ctx, q, pid, d.Target.RevisionID, "evidence", ids["evidence"], &remaining)
	if err != nil {
		return nil, err
	}
	for i := range state.Nodes {
		deriveMetadata(*state, &state.Nodes[i].Ownership, &state.Nodes[i].Freshness)
	}
	for i := range state.Edges {
		deriveMetadata(*state, &state.Edges[i].Ownership, &state.Edges[i].Freshness)
	}
	for i := range state.Evidence {
		deriveMetadata(*state, &state.Evidence[i].Ownership, &state.Evidence[i].Freshness)
	}
	graph := &EffectiveGraphSnapshot{Target: d.Target, State: *state, Pins: sourceEffectivePins(state, d.Target.RevisionID), Origins: []EffectiveFieldOrigin{}}
	if err := finishEffectivePins(graph, nil); err != nil {
		return nil, err
	}
	return graph, nil
}

func readDiagramRecords[T any](ctx context.Context, q importReader, pid, rid, kind string, ids []string, remaining *int) ([]T, error) {
	out := []T{}
	if len(ids) == 0 {
		return out, nil
	}
	wanted, err := json.Marshal(ids)
	if err != nil {
		return nil, err
	}
	rows, err := q.QueryContext(ctx, `SELECT document FROM backend_graph_records_documents WHERE project_id=? AND revision_id=? AND record_type=? AND id IN (SELECT value FROM json_each(?)) ORDER BY id`, pid, rid, kind, string(wanted))
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		var raw string
		if err := rows.Scan(&raw); err != nil {
			return nil, err
		}
		*remaining -= len(raw)
		if *remaining < 0 {
			return nil, limitFault("Diagram reference graph exceeds source byte limit")
		}
		var value T
		if err := json.Unmarshal([]byte(raw), &value); err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	return out, rows.Err()
}
