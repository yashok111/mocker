package backendmodel

import (
	"context"
	"database/sql"
)

// InstallationID is provisioned by migration, never by a preview or owner lookup.
func (r *Repo) InstallationID(ctx context.Context) (string, error) {
	return installationID(ctx, r.db.R)
}

// InstallationIDTx reads it on the caller's transaction. A caller that already
// holds a reader or the single writer must use it: InstallationID takes a
// second reader-pool connection, so pool-width concurrent previews each waited
// for one, and a writer holder waited on readers whose holders waited for the
// writer (review 2026-10-06, F3/F183).
func (r *Repo) InstallationIDTx(ctx context.Context, tx *sql.Tx) (string, error) {
	return installationID(ctx, tx)
}

func installationID(ctx context.Context, q importReader) (string, error) {
	var id string
	if err := q.QueryRowContext(ctx, `SELECT installation_id FROM backend_installation_identity WHERE singleton=1`).Scan(&id); err != nil {
		return "", err
	}
	if !ValidID(id) {
		return "", invalid("installationId", "Invalid persisted installation UUID")
	}
	return id, nil
}

// ResolveEffectiveGraphTx reads exact immutable pins on the caller's snapshot.
func (r *Repo) ResolveEffectiveGraphTx(ctx context.Context, tx *sql.Tx, pid string, target BackendReadTarget) (*EffectiveGraphSnapshot, error) {
	if target.ImportCandidate != nil {
		return nil, invalid("target", "An immutable source or proposal target is required")
	}
	return resolveEffectiveGraph(ctx, tx, pid, target)
}

func (r *Repo) ResolveDiagramScopeTx(ctx context.Context, tx *sql.Tx, pid string, in DiagramScopeInput) (*DiagramScope, error) {
	if err := in.Validate(); err != nil {
		return nil, err
	}
	v, err := loadDiagram(ctx, tx, pid, in.Pin.ID, in.Pin.Version)
	if err != nil {
		return nil, err
	}
	g, err := loadDiagramReferenceGraph(ctx, tx, pid, v.Document)
	if err != nil {
		return nil, err
	}
	return resolveDiagramScope(ctx, v, g, in)
}

// CanonicalDocumentHash exposes the existing document/provenance hash domain.
func CanonicalDocumentHash(value any) (string, error) { return requestDigest(value) }

func (r *Repo) GetDiagramTx(ctx context.Context, tx *sql.Tx, pid string, pin DiagramPin) (*DiagramVersion, error) {
	if err := pin.Validate(); err != nil {
		return nil, err
	}
	v, err := loadDiagram(ctx, tx, pid, pin.ID, pin.Version)
	if err != nil {
		return nil, err
	}
	if v.Pin != pin {
		return nil, diagramPinMismatch()
	}
	return v, nil
}

func (r *Repo) GetDiagramViewTx(ctx context.Context, tx *sql.Tx, pid, id string, version int64) (*DiagramView, error) {
	return loadDiagramView(ctx, tx, pid, id, version)
}
