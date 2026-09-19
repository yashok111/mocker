package apidesign

import (
	"context"
	"database/sql"
)

func (r *Repo) List(ctx context.Context) ([]Design, error) {
	rows, err := r.db.R.QueryContext(ctx, "SELECT "+designColumns+" FROM api_designs ORDER BY id DESC")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []Design{}
	for rows.Next() {
		d, err := scanDesign(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, rows.Err()
}

// Detail reads every moving pointer and history list from one SQLite snapshot.
func (r *Repo) Detail(ctx context.Context, id int64) (*Detail, error) {
	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = tx.Rollback() }()
	out, err := detailTx(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return out, nil
}
func detailTx(ctx context.Context, q queryer, id int64) (*Detail, error) {
	d, err := getDesign(ctx, q, id)
	if err != nil {
		return nil, err
	}
	draft, err := getRevision(ctx, q, id, d.DraftRevisionID)
	if err != nil {
		return nil, err
	}
	result := &Detail{Design: d, Draft: draft, Revisions: []RevisionSummary{}, ChangeSets: []ChangeSet{}, Reviews: []Review{}, Releases: []Release{}}
	if d.PublishedRevisionID != nil {
		published, err := getRevision(ctx, q, id, *d.PublishedRevisionID)
		if err != nil {
			return nil, err
		}
		result.Published = &published
	}
	result.Revisions, err = readRows(ctx, q, "SELECT "+revisionColumns+" FROM api_design_revisions WHERE design_id=? ORDER BY version DESC", id, func(row scanner) (RevisionSummary, error) { v, err := scanRevision(row); return v.RevisionSummary, err })
	if err != nil {
		return nil, err
	}
	result.ChangeSets, err = readRows(ctx, q, "SELECT "+changeSetColumns+" FROM api_design_change_sets WHERE design_id=? ORDER BY id DESC", id, scanChangeSet)
	if err != nil {
		return nil, err
	}
	result.Reviews, err = readRows(ctx, q, "SELECT "+reviewColumns+" FROM api_design_reviews WHERE design_id=? ORDER BY id DESC", id, scanReview)
	if err != nil {
		return nil, err
	}
	result.Releases, err = readRows(ctx, q, "SELECT "+releaseColumns+" FROM api_design_releases WHERE design_id=? ORDER BY number DESC", id, scanRelease)
	if err != nil {
		return nil, err
	}
	return result, nil
}

func readRows[T any](ctx context.Context, q queryer, query string, id int64, scan func(scanner) (T, error)) ([]T, error) {
	rows, err := q.QueryContext(ctx, query, id)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	result := []T{}
	for rows.Next() {
		v, err := scan(rows)
		if err != nil {
			return nil, err
		}
		result = append(result, v)
	}
	return result, rows.Err()
}

func (r *Repo) Revision(ctx context.Context, designID, id int64) (Revision, error) {
	return getRevision(ctx, r.db.R, designID, id)
}
