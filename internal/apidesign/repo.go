package apidesign

import (
	"context"
	"database/sql"
	"errors"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
)

type Repo struct {
	db    *store.DB
	cfg   *config.Config
	specs *specs.Repo
}

func NewRepo(db *store.DB, cfg *config.Config) *Repo {
	return &Repo{db: db, cfg: cfg, specs: specs.NewRepo(db, cfg)}
}

type queryer interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}
type scanner interface{ Scan(...any) error }

const designColumns = `id,name,version,draft_workspace_id,published_workspace_id,draft_revision_id,published_revision_id,latest_review_id,created_at,updated_at`

func scanDesign(row scanner) (Design, error) {
	var d Design
	err := row.Scan(&d.ID, &d.Name, &d.Version, &d.DraftWorkspaceID, &d.PublishedWorkspaceID, &d.DraftRevisionID, &d.PublishedRevisionID, &d.LatestReviewID, &d.CreatedAt, &d.UpdatedAt)
	return d, notFound(err)
}
func getDesign(ctx context.Context, q queryer, id int64) (Design, error) {
	return scanDesign(q.QueryRowContext(ctx, "SELECT "+designColumns+" FROM api_designs WHERE id=?", id))
}
func notFound(err error) error {
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	return err
}
func checkVersion(d Design, expected int64) error {
	if expected != d.Version {
		return &ConflictError{Version: d.Version, DraftRevisionID: d.DraftRevisionID}
	}
	return nil
}
func checkSource(source string) error {
	if source != "ui" && source != "mcp" {
		return &InvalidError{Diagnostics: []Diagnostic{{Pointer: "", Message: "invalid actor source", Severity: "error"}}}
	}
	return nil
}

const revisionColumns = `id,design_id,version,hash,source,summary,change_set_id,created_at,document`

func scanRevision(row scanner) (Revision, error) {
	var v Revision
	err := row.Scan(&v.ID, &v.DesignID, &v.Version, &v.Hash, &v.Source, &v.Summary, &v.ChangeSetID, &v.CreatedAt, &v.Document)
	return v, notFound(err)
}
func getRevision(ctx context.Context, q queryer, designID, id int64) (Revision, error) {
	return scanRevision(q.QueryRowContext(ctx, "SELECT "+revisionColumns+" FROM api_design_revisions WHERE design_id=? AND id=?", designID, id))
}

const reviewColumns = `id,design_id,revision_id,base_revision_id,status,summary,source,created_at`

func scanReview(row scanner) (Review, error) {
	var v Review
	err := row.Scan(&v.ID, &v.DesignID, &v.RevisionID, &v.BaseRevisionID, &v.Status, &v.Summary, &v.Source, &v.CreatedAt)
	return v, notFound(err)
}

const changeSetColumns = `id,design_id,title,base_revision_id,status,source,created_at`

func scanChangeSet(row scanner) (ChangeSet, error) {
	var v ChangeSet
	err := row.Scan(&v.ID, &v.DesignID, &v.Title, &v.BaseRevisionID, &v.Status, &v.Source, &v.CreatedAt)
	return v, notFound(err)
}

const releaseColumns = `id,design_id,revision_id,review_id,number,created_at`

func scanRelease(row scanner) (Release, error) {
	var v Release
	err := row.Scan(&v.ID, &v.DesignID, &v.RevisionID, &v.ReviewID, &v.Number, &v.CreatedAt)
	return v, notFound(err)
}

func baseline(ctx context.Context, q queryer, d Design) (int64, error) {
	if d.PublishedRevisionID != nil {
		return *d.PublishedRevisionID, nil
	}
	var id int64
	err := q.QueryRowContext(ctx, "SELECT id FROM api_design_revisions WHERE design_id=? ORDER BY version LIMIT 1", d.ID).Scan(&id)
	return id, err
}
