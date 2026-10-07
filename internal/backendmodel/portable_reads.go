package backendmodel

import (
	"context"
	"database/sql"
	"time"
)

func (r *Repo) PortableProjectTx(ctx context.Context, tx *sql.Tx, pid string) (*Project, error) {
	return scanProject(tx.QueryRowContext(ctx, `SELECT `+projectColumns+` FROM backend_projects WHERE id=?`, pid))
}
func (r *Repo) GetSavedViewTx(ctx context.Context, tx *sql.Tx, pid, id string, version int64) (*SavedView, error) {
	if version <= 0 {
		return nil, invalid("version", "Exact saved view version required")
	}
	return loadSavedView(ctx, tx, pid, id, version)
}
func (r *Repo) PortableAnnotationsTx(ctx context.Context, tx *sql.Tx, pid string) ([]Annotation, error) {
	rows, err := tx.QueryContext(ctx, `SELECT id,record_type,target_id,revision_id,body,author,created_at,updated_at FROM backend_annotations WHERE project_id=? AND deleted_at IS NULL ORDER BY id`, pid)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	out := []Annotation{}
	for rows.Next() {
		var a Annotation
		var rid *string
		var created, updated string
		if err := rows.Scan(&a.ID, &a.Target.RecordType, &a.Target.ID, &rid, &a.Body, &a.Author, &created, &updated); err != nil {
			return nil, err
		}
		if rid != nil {
			a.Target.RevisionID = *rid
		}
		a.CreatedAt, err = time.Parse(time.RFC3339Nano, created)
		if err != nil {
			return nil, err
		}
		a.UpdatedAt, err = time.Parse(time.RFC3339Nano, updated)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
		if len(out) > MaxAnnotationIdentities {
			return nil, limitFault("Portable annotation quota")
		}
	}
	return out, rows.Err()
}
