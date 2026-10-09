package backendmodel

import (
	"context"
	"database/sql"
)

func (r *Repo) readArchitectureGraph(ctx context.Context, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	return r.readNativeProjectionGraph(ctx, pid, target)
}

// Diagram projections validate native records and pins; source6 and proposals
// retain the full resolver. No source5 migration proof is consumed here.
func (r *Repo) readNativeProjectionGraph(ctx context.Context, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
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
	return loadNativeProjectionGraph(ctx, q, pid, target)
}

func loadNativeProjectionGraph(ctx context.Context, q importReader, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
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
	// Use only for projections/reference validation consuming State and Pins.
	// Proof-basis consumers, composed and proposed targets need the full resolver.
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
