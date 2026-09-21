package apidesign

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/workspaces"
)

type CreateInput struct {
	Name, Document, Source string
	OwnerID                *int64
	// WorkspaceID/WorkspaceRevision fence a coherent legacy export at commit.
	WorkspaceID, WorkspaceRevision int64
}
type SaveInput struct {
	ExpectedVersion           int64
	Document, Summary, Source string
	ChangeSetID               *int64
	ForceRevision             bool
}

func invalidField(pointer, message string) error {
	return &InvalidError{Diagnostics: []Diagnostic{{Pointer: pointer, Message: message, Severity: "error"}}}
}

func (r *Repo) Create(ctx context.Context, in CreateInput) (*Detail, error) {
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = r.CreateTx(ctx, tx, in)
		return err
	})
	return result, err
}

// CreateTx participates in the caller's transaction, including the draft mock projection.
func (r *Repo) CreateTx(ctx context.Context, tx *sql.Tx, in CreateInput) (*Detail, error) {
	if err := checkSource(in.Source); err != nil {
		return nil, err
	}
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || utf8.RuneCountInString(in.Name) > 200 {
		return nil, invalidField("/name", "Название должно содержать от 1 до 200 символов")
	}
	if in.Document == "" {
		in.Document = emptyDocument
	}
	if int64(len(in.Document)) > r.cfg.MaxBody {
		return nil, specs.ErrTooLarge
	}
	keyed, err := withOperationKeys(in.Document, "", 0)
	if err != nil {
		return nil, err
	}
	prepared, err := r.prepare(keyed)
	if err != nil {
		return nil, err
	}
	if in.WorkspaceID != 0 {
		var revision int64
		if err := tx.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id=?", in.WorkspaceID).Scan(&revision); err != nil {
			return nil, notFound(err)
		}
		if revision != in.WorkspaceRevision {
			return nil, ErrConflict
		}
	}
	specID, err := r.importTx(ctx, tx, prepared)
	if err != nil {
		return nil, err
	}
	draft, err := workspaces.CreateTx(ctx, tx, workspaces.CreateInput{Name: in.Name + " draft", OwnerID: in.OwnerID, SpecID: &specID})
	if err != nil {
		return nil, err
	}
	published, err := workspaces.CreateTx(ctx, tx, workspaces.CreateInput{Name: in.Name + " published", OwnerID: in.OwnerID})
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	row, err := tx.ExecContext(ctx, `INSERT INTO api_designs(name,draft_workspace_id,published_workspace_id,created_at,updated_at) VALUES (?,?,?,?,?)`, in.Name, draft.ID, published.ID, now, now)
	if err != nil {
		return nil, err
	}
	id, err := row.LastInsertId()
	if err != nil {
		return nil, err
	}
	for _, member := range []struct {
		id   int64
		role string
	}{{draft.ID, "draft"}, {published.ID, "published"}} {
		if _, err = tx.ExecContext(ctx, "INSERT INTO api_design_workspaces(workspace_id,design_id,role) VALUES (?,?,?)", member.id, id, member.role); err != nil {
			return nil, err
		}
	}
	revisionID, err := insertRevision(ctx, tx, id, 1, nil, specID, prepared, in.Source, "Начальная версия", nil, now)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE api_designs SET draft_revision_id=? WHERE id=?", revisionID, id); err != nil {
		return nil, err
	}
	return detailTx(ctx, tx, id)
}

func (r *Repo) importTx(ctx context.Context, tx *sql.Tx, p *preparedDocument) (int64, error) {
	result, err := r.specs.ImportTx(ctx, tx, p.runtime)
	if err != nil && !errors.Is(err, specs.ErrDuplicate) {
		return 0, err
	}
	return result.Spec.ID, nil
}
func insertRevision(ctx context.Context, tx *sql.Tx, id, version int64, parent *int64, specID int64, p *preparedDocument, source, summary string, changeSetID *int64, now int64) (int64, error) {
	result, err := tx.ExecContext(ctx, `INSERT INTO api_design_revisions(design_id,version,parent_id,spec_id,hash,document,source,summary,change_set_id,created_at) VALUES (?,?,?,?,?,?,?,?,?,?)`, id, version, parent, specID, p.hash, p.document, source, summary, changeSetID, now)
	if err != nil {
		return 0, err
	}
	return result.LastInsertId()
}

func (r *Repo) Save(ctx context.Context, id int64, in SaveInput) (*Detail, error) {
	var result *Detail
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var err error
		result, err = r.SaveTx(ctx, tx, id, in)
		return err
	})
	return result, err
}

// SaveTx participates in the caller's transaction, including the draft mock projection.
func (r *Repo) SaveTx(ctx context.Context, tx *sql.Tx, id int64, in SaveInput) (*Detail, error) {
	if err := checkSource(in.Source); err != nil {
		return nil, err
	}
	d, err := getDesign(ctx, tx, id)
	if err != nil {
		return nil, err
	}
	if err = checkVersion(d, in.ExpectedVersion); err != nil {
		return nil, err
	}
	if in.ChangeSetID != nil {
		set, err := scanChangeSet(tx.QueryRowContext(ctx, "SELECT "+changeSetColumns+" FROM api_design_change_sets WHERE id=? AND design_id=?", *in.ChangeSetID, id))
		if err != nil {
			return nil, err
		}
		if set.Status != "open" {
			return nil, invalidField("/changeSetId", "Набор изменений закрыт")
		}
	}
	old, err := getRevision(ctx, tx, id, d.DraftRevisionID)
	if err != nil {
		return nil, err
	}
	if int64(len(in.Document)) > r.cfg.MaxBody {
		return nil, specs.ErrTooLarge
	}
	keyed, err := withOperationKeys(in.Document, old.Document, 0)
	if err != nil {
		return nil, err
	}
	p, err := r.prepare(keyed)
	if err != nil {
		return nil, err
	}
	if (old.Hash == p.hash || old.Document == p.document) && !in.ForceRevision {
		return detailTx(ctx, tx, id)
	}
	specID, err := r.importTx(ctx, tx, p)
	if err != nil {
		return nil, err
	}
	now := time.Now().Unix()
	revisionID, err := insertRevision(ctx, tx, id, d.Version+1, &d.DraftRevisionID, specID, p, in.Source, in.Summary, in.ChangeSetID, now)
	if err != nil {
		return nil, err
	}
	if _, err = tx.ExecContext(ctx, "UPDATE api_designs SET version=version+1,draft_revision_id=?,updated_at=? WHERE id=?", revisionID, now, id); err != nil {
		return nil, err
	}
	if err = bindWorkspace(ctx, tx, d.DraftWorkspaceID, specID, now); err != nil {
		return nil, err
	}
	return detailTx(ctx, tx, id)
}

func bindWorkspace(ctx context.Context, tx *sql.Tx, id, specID, now int64) error {
	_, err := tx.ExecContext(ctx, `UPDATE workspaces SET spec_id=?,revision=revision+1,edit_seq=edit_seq+1,edit_version=edit_seq+1,updated_at=? WHERE id=?`, specID, now, id)
	return err
}

func (r *Repo) Restore(ctx context.Context, id, revisionID, expected int64, summary, source string) (*Detail, error) {
	revision, err := r.Revision(ctx, id, revisionID)
	if err != nil {
		return nil, err
	}
	return r.Save(ctx, id, SaveInput{ExpectedVersion: expected, Document: revision.Document, Summary: summary, Source: source, ForceRevision: true})
}

func (r *Repo) CreateChangeSet(ctx context.Context, id, expected int64, title, source string) (ChangeSet, error) {
	var result ChangeSet
	if err := checkSource(source); err != nil {
		return result, err
	}
	title = strings.TrimSpace(title)
	if title == "" || utf8.RuneCountInString(title) > 500 {
		return result, invalidField("/title", "Укажите название набора изменений (до 500 символов)")
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(d, expected); err != nil {
			return err
		}
		row, err := tx.ExecContext(ctx, `INSERT INTO api_design_change_sets(design_id,title,base_revision_id,status,source,created_at) VALUES (?,?,?,'open',?,?)`, id, title, d.DraftRevisionID, source, time.Now().Unix())
		if err != nil {
			return err
		}
		setID, err := row.LastInsertId()
		if err != nil {
			return err
		}
		result, err = scanChangeSet(tx.QueryRowContext(ctx, "SELECT "+changeSetColumns+" FROM api_design_change_sets WHERE id=?", setID))
		return err
	})
	return result, err
}
func (r *Repo) CloseChangeSet(ctx context.Context, id, setID, expected int64) (ChangeSet, error) {
	var result ChangeSet
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(d, expected); err != nil {
			return err
		}
		result, err = scanChangeSet(tx.QueryRowContext(ctx, "SELECT "+changeSetColumns+" FROM api_design_change_sets WHERE id=? AND design_id=?", setID, id))
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_design_change_sets SET status='closed' WHERE id=?", setID); err != nil {
			return err
		}
		result.Status = "closed"
		return nil
	})
	return result, err
}

func (r *Repo) RequestReview(ctx context.Context, id, expected int64, summary, source string) (Review, error) {
	var result Review
	if err := checkSource(source); err != nil {
		return result, err
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		if err = checkVersion(d, expected); err != nil {
			return err
		}
		baseID, err := baseline(ctx, tx, d)
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_design_reviews SET status='superseded' WHERE design_id=? AND status='pending'", id); err != nil {
			return err
		}
		row, err := tx.ExecContext(ctx, `INSERT INTO api_design_reviews(design_id,revision_id,base_revision_id,status,summary,source,created_at) VALUES (?,?,?,'pending',?,?,?)`, id, d.DraftRevisionID, baseID, summary, source, time.Now().Unix())
		if err != nil {
			return err
		}
		reviewID, err := row.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_designs SET latest_review_id=? WHERE id=?", reviewID, id); err != nil {
			return err
		}
		result, err = scanReview(tx.QueryRowContext(ctx, "SELECT "+reviewColumns+" FROM api_design_reviews WHERE id=?", reviewID))
		return err
	})
	return result, err
}

func (r *Repo) Publish(ctx context.Context, id, reviewID, expected int64, source string) (Release, error) {
	var result Release
	if source != "ui" {
		return result, ErrForbidden
	}
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		d, err := getDesign(ctx, tx, id)
		if err != nil {
			return err
		}
		review, err := scanReview(tx.QueryRowContext(ctx, "SELECT "+reviewColumns+" FROM api_design_reviews WHERE id=? AND design_id=?", reviewID, id))
		if err != nil {
			return err
		}
		// Lost-response retries are scoped to this exact candidate and precede CAS.
		if review.Status == "published" {
			result, err = scanRelease(tx.QueryRowContext(ctx, "SELECT "+releaseColumns+" FROM api_design_releases WHERE review_id=? AND design_id=?", reviewID, id))
			return err
		}
		if err = checkVersion(d, expected); err != nil {
			return err
		}
		baseID, err := baseline(ctx, tx, d)
		if err != nil {
			return err
		}
		if review.Status != "pending" || d.LatestReviewID == nil || *d.LatestReviewID != review.ID || review.RevisionID != d.DraftRevisionID || review.BaseRevisionID != baseID {
			return &ConflictError{Version: d.Version, DraftRevisionID: d.DraftRevisionID}
		}
		var specID int64
		if err = tx.QueryRowContext(ctx, "SELECT spec_id FROM api_design_revisions WHERE design_id=? AND id=?", id, review.RevisionID).Scan(&specID); err != nil {
			return err
		}
		now := time.Now().Unix()
		if err = bindWorkspace(ctx, tx, d.PublishedWorkspaceID, specID, now); err != nil {
			return err
		}
		row, err := tx.ExecContext(ctx, `INSERT INTO api_design_releases(design_id,revision_id,review_id,number,created_at) SELECT ?,?,?,COALESCE(MAX(number),0)+1,? FROM api_design_releases WHERE design_id=?`, id, review.RevisionID, reviewID, now, id)
		if err != nil {
			return err
		}
		releaseID, err := row.LastInsertId()
		if err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_design_reviews SET status='published' WHERE id=?", reviewID); err != nil {
			return err
		}
		if _, err = tx.ExecContext(ctx, "UPDATE api_designs SET published_revision_id=?,updated_at=? WHERE id=?", review.RevisionID, now, id); err != nil {
			return err
		}
		result, err = scanRelease(tx.QueryRowContext(ctx, "SELECT "+releaseColumns+" FROM api_design_releases WHERE id=?", releaseID))
		return err
	})
	return result, err
}
