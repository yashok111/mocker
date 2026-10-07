package backendmodel

import (
	"context"
	"database/sql"
)

func (r *Repo) readArchitectureGraph(ctx context.Context, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	if err := r.validateAnalysisLeaseTarget(ctx, pid, target); err != nil {
		return nil, err
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	return loadArchitectureGraph(ctx, tx, pid, target)
}

func loadArchitectureGraph(ctx context.Context, q importReader, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	if err := target.Validate(); err != nil {
		return nil, err
	}
	if target.RevisionID == "" {
		return resolveEffectiveGraph(ctx, q, pid, target)
	}
	metadata, err := loadSourceState(ctx, q, pid, target.RevisionID)
	if err != nil {
		return nil, err
	}
	if metadata.Revision.SchemaVersion != EventsSchemaVersion {
		return resolveEffectiveGraph(ctx, q, pid, target)
	}
	// C4 consumes native source5 records and their evidence, not the source6
	// migration identities/proof bases. Bootstrapping those for every section
	// cost 14.6 seconds and 12.5 GiB of allocations on Education Platform.
	// Keep this snapshot private to projection reads: generic source consumers
	// still need the full resolver, as do composed and proposed targets.
	state, err := loadRevisionState(ctx, q, pid, target.RevisionID)
	if err != nil {
		return nil, err
	}
	graph := &EffectiveGraphSnapshot{Target: target, State: *state, Pins: sourceEffectivePins(state, target.RevisionID), Origins: []EffectiveFieldOrigin{}}
	if err := finishEffectivePins(graph, nil); err != nil {
		return nil, err
	}
	return graph, nil
}
