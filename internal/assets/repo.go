package assets

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/store"
)

// Repo is the assets table's one door. Shape mirrors internal/scenarios:
// reads on db.R, every write inside ONE db.Write transaction, and the
// revision bump made by this package's own copy of bumpRevisionTx (HARD
// RULE 5 — never workspaces.Repo.Update from inside a write callback; it
// opens its own transaction on the single writer connection and the two
// deadlock rather than fail).
type Repo struct {
	db *store.DB

	// MaxAssetBytes caps one file, MaxTotalBytes a workspace's sum (DESIGN
	// §32.2, D2). Both are read by Put inside its transaction; the
	// constructor takes them from config so main.go and internal/admin's
	// own construction hand over the same two numbers.
	MaxAssetBytes int64
	MaxTotalBytes int64
}

// NewRepo builds a Repo over db with the two caps.
func NewRepo(db *store.DB, maxAssetBytes, maxTotalBytes int64) *Repo {
	return &Repo{db: db, MaxAssetBytes: maxAssetBytes, MaxTotalBytes: maxTotalBytes}
}

// Put stores data under name for workspaceID, replacing an existing row of
// that name. It answers created=true on an insert and false on a replace.
//
// The input-shape refusals (name, empty type, the per-file cap — cheap, and
// facts about the request alone) run first, outside any transaction. The
// two refusals that depend on what the table holds run INSIDE it, against
// that instant: the workspace's existence, then the quota — the sum of
// every OTHER row's size plus this one, so that re-uploading a name near the
// ceiling is not double-counted (round-1 #10). Two concurrent Puts that each fit alone and not together
// therefore leave exactly one row, because the single writer connection
// serialises them and the second one reads the first one's row.
func (r *Repo) Put(ctx context.Context, workspaceID int64, name, mediaType string, data []byte) (Meta, bool, error) {
	if !ValidName(name) {
		return Meta{}, false, fmt.Errorf("%w: %q", ErrInvalidName, name)
	}
	if mediaType == "" {
		return Meta{}, false, errors.New("assets: media type must not be empty")
	}
	if int64(len(data)) > r.MaxAssetBytes {
		return Meta{}, false, fmt.Errorf("%w: %d bytes over %d", ErrTooLarge, len(data), r.MaxAssetBytes)
	}

	sum := sha256.Sum256(data)
	digest := hex.EncodeToString(sum[:])
	now := time.Now().UTC()

	var (
		meta    Meta
		created bool
	)
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		var exists int
		switch err := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE id = ?", workspaceID).Scan(&exists); {
		case err == nil:
		case errors.Is(err, sql.ErrNoRows):
			return ErrWorkspaceNotFound
		default:
			return fmt.Errorf("read workspace %d: %w", workspaceID, err)
		}

		var others int64
		if err := tx.QueryRowContext(ctx,
			"SELECT COALESCE(SUM(size_bytes), 0) FROM assets WHERE workspace_id = ? AND name <> ?",
			workspaceID, name,
		).Scan(&others); err != nil {
			return fmt.Errorf("sum assets of workspace %d: %w", workspaceID, err)
		}
		if others+int64(len(data)) > r.MaxTotalBytes {
			return fmt.Errorf("%w: %d bytes stored elsewhere plus %d over %d",
				ErrQuota, others, len(data), r.MaxTotalBytes)
		}

		var createdAt int64
		switch err := tx.QueryRowContext(ctx,
			"SELECT created_at FROM assets WHERE workspace_id = ? AND name = ?", workspaceID, name,
		).Scan(&createdAt); {
		case err == nil:
			if _, err := tx.ExecContext(ctx,
				`UPDATE assets SET media_type = ?, size_bytes = ?, sha256 = ?, data = ?, updated_at = ?
				 WHERE workspace_id = ? AND name = ?`,
				mediaType, len(data), digest, data, now.Unix(), workspaceID, name,
			); err != nil {
				return fmt.Errorf("replace asset %q: %w", name, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			createdAt = now.Unix()
			created = true
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO assets (workspace_id, name, media_type, size_bytes, sha256, data, created_at, updated_at)
				 VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
				workspaceID, name, mediaType, len(data), digest, data, now.Unix(), now.Unix(),
			); err != nil {
				return fmt.Errorf("insert asset %q: %w", name, err)
			}
		default:
			return fmt.Errorf("read asset %q: %w", name, err)
		}

		meta = Meta{
			Name: name, MediaType: mediaType, SizeBytes: int64(len(data)), SHA256: digest,
			CreatedAt: time.Unix(createdAt, 0).UTC(), UpdatedAt: now,
		}
		return bumpRevisionTx(ctx, tx, workspaceID, now)
	})
	if err != nil {
		return Meta{}, false, err
	}
	return meta, created, nil
}

// metaColumns is the SELECT list Meta scans from — never data, so the list
// route and the ETag path move no BLOB through the reader pool.
const metaColumns = "name, media_type, size_bytes, sha256, created_at, updated_at"

func scanMeta(row interface{ Scan(dest ...any) error }) (Meta, error) {
	var (
		m                  Meta
		createdAt, updated int64
	)
	if err := row.Scan(&m.Name, &m.MediaType, &m.SizeBytes, &m.SHA256, &createdAt, &updated); err != nil {
		return Meta{}, err
	}
	m.CreatedAt = time.Unix(createdAt, 0).UTC()
	m.UpdatedAt = time.Unix(updated, 0).UTC()
	return m, nil
}

// Meta reads one asset's metadata without its bytes. ErrNotFound when
// there is no such name in the workspace.
func (r *Repo) Meta(ctx context.Context, workspaceID int64, name string) (Meta, error) {
	m, err := scanMeta(r.db.R.QueryRowContext(ctx,
		"SELECT "+metaColumns+" FROM assets WHERE workspace_id = ? AND name = ?", workspaceID, name))
	if errors.Is(err, sql.ErrNoRows) {
		return Meta{}, ErrNotFound
	}
	if err != nil {
		return Meta{}, fmt.Errorf("read asset %q: %w", name, err)
	}
	return m, nil
}

// Get reads one asset's metadata AND bytes.
func (r *Repo) Get(ctx context.Context, workspaceID int64, name string) (Meta, []byte, error) {
	var (
		m                  Meta
		data               []byte
		createdAt, updated int64
	)
	err := r.db.R.QueryRowContext(ctx,
		"SELECT "+metaColumns+", data FROM assets WHERE workspace_id = ? AND name = ?", workspaceID, name,
	).Scan(&m.Name, &m.MediaType, &m.SizeBytes, &m.SHA256, &createdAt, &updated, &data)
	if errors.Is(err, sql.ErrNoRows) {
		return Meta{}, nil, ErrNotFound
	}
	if err != nil {
		return Meta{}, nil, fmt.Errorf("read asset %q: %w", name, err)
	}
	m.CreatedAt = time.Unix(createdAt, 0).UTC()
	m.UpdatedAt = time.Unix(updated, 0).UTC()
	return m, data, nil
}

// List returns every asset of the workspace, by name, metadata only.
func (r *Repo) List(ctx context.Context, workspaceID int64) ([]Meta, error) {
	rows, err := r.db.R.QueryContext(ctx,
		"SELECT "+metaColumns+" FROM assets WHERE workspace_id = ? ORDER BY name", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list assets of workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	out := []Meta{}
	for rows.Next() {
		m, err := scanMeta(rows)
		if err != nil {
			return nil, fmt.Errorf("scan asset: %w", err)
		}
		out = append(out, m)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list assets of workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// TotalBytes is the workspace's stored sum — the list route's usage line,
// read in one query rather than summed from the rows it also returns.
func (r *Repo) TotalBytes(ctx context.Context, workspaceID int64) (int64, error) {
	var total int64
	if err := r.db.R.QueryRowContext(ctx,
		"SELECT COALESCE(SUM(size_bytes), 0) FROM assets WHERE workspace_id = ?", workspaceID,
	).Scan(&total); err != nil {
		return 0, fmt.Errorf("sum assets of workspace %d: %w", workspaceID, err)
	}
	return total, nil
}

// Delete removes one asset. confirmSlug must equal the workspace's slug,
// read INSIDE the same transaction as the delete — the identical guard
// reset-data and a decline use, because entity data and an asset alike are
// state no checkpoint restores (DESIGN §32.4). ErrNotFound when no such
// name; ErrConfirmSlug when the slug does not match (checked BEFORE
// existence, so a wrong slug learns nothing about which names exist).
// Bumps revision: a deleted asset is an observable change of the
// workspace, and {prefix}/health's revision is the one signal an external
// test has for that (D11).
func (r *Repo) Delete(ctx context.Context, workspaceID int64, name, confirmSlug string) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var slug string
		switch err := tx.QueryRowContext(ctx, "SELECT slug FROM workspaces WHERE id = ?", workspaceID).Scan(&slug); {
		case err == nil:
		case errors.Is(err, sql.ErrNoRows):
			return ErrWorkspaceNotFound
		default:
			return fmt.Errorf("read workspace %d: %w", workspaceID, err)
		}
		if confirmSlug != slug {
			return ErrConfirmSlug
		}

		res, err := tx.ExecContext(ctx, "DELETE FROM assets WHERE workspace_id = ? AND name = ?", workspaceID, name)
		if err != nil {
			return fmt.Errorf("delete asset %q: %w", name, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete asset %q: rows affected: %w", name, err)
		}
		if n == 0 {
			return ErrNotFound
		}
		return bumpRevisionTx(ctx, tx, workspaceID, time.Now().UTC())
	})
}

// bumpRevisionTx mirrors internal/overrides/repo.go's helper of the same
// name verbatim (HARD RULE 5: never workspaces.Repo.Update from inside a
// db.Write callback — see Repo's doc). The sixth copy in the tree
// (overrides, customep, checkpoints, resources, scenarios are the five),
// copied rather than shared for the reason each of them gives: no package
// may import another purely for a four-line SQL helper.
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}
