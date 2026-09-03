package traffic

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/store"
)

// ErrNotFound is returned by [Repo.Get] when no row matches.
var ErrNotFound = errors.New("traffic: row not found")

// Repo is the traffic table's read (and Clear's one write) data-access layer.
// Every insert goes through [Recorder] instead — Repo never writes a row.
type Repo struct {
	db *store.DB
}

// NewRepo builds a Repo over db.
func NewRepo(db *store.DB) *Repo {
	return &Repo{db: db}
}

const selectRow = `
	SELECT id, ts, method, path, peer_ip, fwd_ip, matched_kind, matched_id,
	       status, duration_ms, req_headers, req_body, resp_body, notes, truncated
	FROM traffic`

// List returns up to limit rows of workspaceID, NEWEST first — the order the
// traffic_ws index (workspace_id, id DESC) already gives it, so this is a
// plain indexed range scan.
func (r *Repo) List(ctx context.Context, workspaceID int64, limit int) ([]Row, error) {
	rows, err := r.db.R.QueryContext(ctx,
		selectRow+" WHERE workspace_id = ? ORDER BY id DESC LIMIT ?",
		workspaceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list traffic for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanRows(rows, workspaceID)
}

// Since returns rows with id strictly greater than sinceID, OLDEST first (so
// a client applying them in order sees the same sequence they happened in),
// up to limit. Cursoring by id rather than ts is what keeps this servable by
// traffic_ws instead of a scan the index cannot answer.
func (r *Repo) Since(ctx context.Context, workspaceID, sinceID int64, limit int) ([]Row, error) {
	rows, err := r.db.R.QueryContext(ctx,
		selectRow+" WHERE workspace_id = ? AND id > ? ORDER BY id ASC LIMIT ?",
		workspaceID, sinceID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("traffic since %d for workspace %d: %w", sinceID, workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	return scanRows(rows, workspaceID)
}

// Get looks up one row by (workspaceID, id).
func (r *Repo) Get(ctx context.Context, workspaceID, id int64) (*Row, error) {
	row := r.db.R.QueryRowContext(ctx, selectRow+" WHERE workspace_id = ? AND id = ?", workspaceID, id)
	got, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get traffic row %d for workspace %d: %w", id, workspaceID, err)
	}
	return got, nil
}

// Clear deletes every row of workspaceID and reports how many were removed.
// It touches only that workspace: the DELETE is scoped by workspace_id, so
// it is the same bounded, indexed shape [pruneRetentionTx] uses, not a
// full-table scan.
func (r *Repo) Clear(ctx context.Context, workspaceID int64) (int64, error) {
	var n int64
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, "DELETE FROM traffic WHERE workspace_id = ?", workspaceID)
		if err != nil {
			return fmt.Errorf("clear traffic for workspace %d: %w", workspaceID, err)
		}
		n, err = res.RowsAffected()
		if err != nil {
			return fmt.Errorf("clear traffic for workspace %d: rows affected: %w", workspaceID, err)
		}
		return nil
	})
	if err != nil {
		return 0, err
	}
	return n, nil
}

// Rate1m counts workspaceID's rows with ts within the minute ending at now.
// This is the one query in this file NOT answered purely by traffic_ws's
// (workspace_id, id DESC) shape — it needs wall-clock time, which the index
// does not carry — but it stays a bounded scan: the workspace_id equality
// still narrows SQLite to that one workspace's rows (at most
// Options.Retention of them) before the ts filter runs, never a scan across
// every workspace's traffic.
func (r *Repo) Rate1m(ctx context.Context, workspaceID int64, now time.Time) (int, error) {
	since := now.Add(-time.Minute).Unix()
	var n int
	err := r.db.R.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM traffic WHERE workspace_id = ? AND ts >= ?",
		workspaceID, since,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("rate1m for workspace %d: %w", workspaceID, err)
	}
	return n, nil
}

// rowScanner is satisfied by both *sql.Row and *sql.Rows (mirrors
// internal/overrides/repo.go's helper of the same name), so scan logic is
// written once.
type rowScanner interface {
	Scan(dest ...any) error
}

// scan decodes one traffic row. Every optional column comes back through a
// sql.Null* type even though [insertEventTx] never writes a bare NULL for
// peer_ip/fwd_ip/matched_kind today — a hand-inserted or future-written row
// is not this package's to trust, and a NULL there must decode cleanly
// rather than panic on a non-nullable Scan target (the same discipline
// internal/overrides/repo.go's scan documents for its own frozen table).
func scan(row rowScanner) (*Row, error) {
	var (
		r                                             Row
		ts                                            int64
		peerIP, fwdIP, matchedKind                    sql.NullString
		matchedID                                     sql.NullInt64
		reqHeadersJSON, reqBody, respBody, notesField sql.NullString
		truncated                                     int64
	)
	if err := row.Scan(
		&r.ID, &ts, &r.Method, &r.Path, &peerIP, &fwdIP, &matchedKind, &matchedID,
		&r.Status, &r.DurationMS, &reqHeadersJSON, &reqBody, &respBody, &notesField, &truncated,
	); err != nil {
		return nil, err
	}

	r.TS = time.Unix(ts, 0).UTC()
	r.PeerIP = peerIP.String
	r.FwdIP = fwdIP.String
	r.MatchedKind = matchedKind.String
	if matchedID.Valid {
		v := matchedID.Int64
		r.MatchedID = &v
	}
	if reqHeadersJSON.Valid && reqHeadersJSON.String != "" {
		if err := jsonx.Unmarshal([]byte(reqHeadersJSON.String), &r.ReqHeaders); err != nil {
			return nil, fmt.Errorf("traffic row %d: decode req_headers: %w", r.ID, err)
		}
	}
	r.ReqBody = reqBody.String
	r.RespBody = respBody.String
	r.Notes = notesField.String
	r.Truncated = truncated != 0
	return &r, nil
}

func scanRows(rows *sql.Rows, workspaceID int64) ([]Row, error) {
	out := []Row{}
	for rows.Next() {
		row, err := scan(rows)
		if err != nil {
			return nil, fmt.Errorf("traffic rows for workspace %d: %w", workspaceID, err)
		}
		out = append(out, *row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate traffic rows for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}
