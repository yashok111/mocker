package backendmodel

import (
	"context"
	"database/sql"
	"errors"
)

type DiagramListInput struct {
	Kind   string `json:"kind,omitempty"`
	Limit  int    `json:"limit"`
	Cursor string `json:"cursor,omitempty"`
}
type DiagramSummary struct {
	ID         string     `json:"id"`
	Kind       string     `json:"kind"`
	Pin        DiagramPin `json:"pin"`
	TargetHash string     `json:"targetHash"`
}
type DiagramViewSummary struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Version int64  `json:"version"`
	Name    string `json:"name"`
}
type DiagramListPage struct {
	Items          []DiagramSummary `json:"items"`
	NextCursor     string           `json:"nextCursor"`
	CatalogVersion int64            `json:"catalogVersion"`
}
type DiagramViewListPage struct {
	Items          []DiagramViewSummary `json:"items"`
	NextCursor     string               `json:"nextCursor"`
	CatalogVersion int64                `json:"catalogVersion"`
}

func diagramListScope(ctx context.Context, q importReader, pid, kind string, in DiagramListInput) (string, int64, error) {
	if in.Kind != "" && in.Kind != "architecture" && in.Kind != "interactions" && in.Kind != "lifecycle" {
		return "", 0, diagramUnsupported()
	}
	if in.Limit < 1 || in.Limit > 500 {
		return "", 0, invalid("limit", "Use 1–500")
	}
	var owner string
	if err := q.QueryRowContext(ctx, `SELECT id FROM backend_projects WHERE id=?`, pid).Scan(&owner); errors.Is(err, sql.ErrNoRows) {
		return "", 0, notFound()
	} else if err != nil {
		return "", 0, err
	}
	var catalog int64
	err := q.QueryRowContext(ctx, `SELECT version FROM backend_diagram_catalog WHERE project_id=?`, pid).Scan(&catalog)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", 0, err
	}
	in.Cursor = ""
	hash, err := requestDigest(struct {
		Project, Kind string
		Page          DiagramListInput
		Catalog       int64
	}{pid, kind, in, catalog})
	return hash, catalog, err
}
func (r *Repo) ListDiagrams(ctx context.Context, pid string, in DiagramListInput) (*DiagramListPage, error) {
	if in.Limit == 0 {
		in.Limit = 100
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	scope, catalog, err := diagramListScope(ctx, tx, pid, "diagrams", in)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT d.id,d.kind,v.version,v.content_hash,v.target_hash FROM backend_diagrams d JOIN backend_diagram_versions v ON v.project_id=d.project_id AND v.diagram_id=d.id AND v.version=d.version WHERE d.project_id=? AND (?='' OR d.kind=?) ORDER BY d.kind,d.id`, pid, in.Kind, in.Kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	all := []DiagramSummary{}
	for rows.Next() {
		var item DiagramSummary
		if err = rows.Scan(&item.ID, &item.Kind, &item.Pin.Version, &item.Pin.ContentHash, &item.TargetHash); err != nil {
			return nil, err
		}
		item.Pin.ID = item.ID
		all = append(all, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	offset, err := diagramOffset(in.Cursor, scope, len(all))
	if err != nil {
		return nil, err
	}
	end := min(offset+in.Limit, len(all))
	return &DiagramListPage{Items: all[offset:end], NextCursor: diagramNext(scope, end, len(all)), CatalogVersion: catalog}, nil
}
func (r *Repo) ListDiagramViews(ctx context.Context, pid string, in DiagramListInput) (*DiagramViewListPage, error) {
	if in.Limit == 0 {
		in.Limit = 100
	}
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	scope, catalog, err := diagramListScope(ctx, tx, pid, "diagram-views", in)
	if err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT id,kind,version,name FROM backend_diagram_views WHERE project_id=? AND (?='' OR kind=?) ORDER BY kind,id`, pid, in.Kind, in.Kind)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	all := []DiagramViewSummary{}
	for rows.Next() {
		var item DiagramViewSummary
		if err = rows.Scan(&item.ID, &item.Kind, &item.Version, &item.Name); err != nil {
			return nil, err
		}
		all = append(all, item)
	}
	if err = rows.Err(); err != nil {
		return nil, err
	}
	offset, err := diagramOffset(in.Cursor, scope, len(all))
	if err != nil {
		return nil, err
	}
	end := min(offset+in.Limit, len(all))
	return &DiagramViewListPage{Items: all[offset:end], NextCursor: diagramNext(scope, end, len(all)), CatalogVersion: catalog}, nil
}
