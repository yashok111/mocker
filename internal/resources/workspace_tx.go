// Workspace-level helpers every write transaction of this package shares:
// the identity core it fences on, the revision bump (HARD RULE 5's copy) and
// the writer deadline. Split out of repo.go 2026-09-03; the text is unchanged.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// --- write deadline (R24: Create/Delete are bounded, BeginTx included) ----

// writeDeadline bounds the WHOLE Create/Delete call, BeginTx included: the
// writer pool is ONE connection, so BeginTx QUEUES in database/sql rather
// than returning SQLITE_BUSY immediately, and the busy_timeout pragma
// (which happens to carry the same 5s) never fires for a queued
// connection acquisition — only for a lock wait on an already-open one.
var writeDeadline = 5 * time.Second

// writeBusyIfOurDeadline reports whether err is this call's OWN
// writeDeadline firing — never a client that disconnected or a caller
// whose own context ended earlier. callerCtx is the UNWRAPPED context the
// public method received; after the wrapped (deadline-bound) context times
// out, callerCtx.Err() is still nil exactly when writeDeadline itself was
// the cause (R24's own text: "claimed ONLY when that deadline is the
// cause").
func writeBusyIfOurDeadline(callerCtx context.Context, err error) bool {
	return errors.Is(err, context.DeadlineExceeded) && callerCtx.Err() == nil
}

// --- the shared revision bump (D4: this package's OWN copy) --------------

// bumpRevisionTx is internal/overrides/repo.go:562#bumpRevisionTx's shape,
// copied rather than shared: D4 is explicit that Confirm/Decline must NOT
// go through workspaces.Repo.Update, which opens its own transaction and
// also allocates a fresh edit_version — spuriously 409-ing every open
// admin form for an edit this route never touched. Entity writes
// (Create/Delete) do NOT call this (D13 clause 23: "revision moves on both
// decision transitions and NOT on an entity write").
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// --- workspace reads (mirrors internal/checkpoints' readWorkspaceCore/
// fenceTx pair, since this package reads the workspaces table directly
// rather than importing internal/workspaces — the same peer relationship
// internal/specs, internal/scenarios and internal/checkpoints already
// hold with it) -------------------------------------------------------

// workspaceCore is Confirm/Decline's pre-transaction read: everything
// needed to locate a family's suggestion/resource and to fence the
// generation half against staleness (D4/R36).
type workspaceCore struct {
	createdAt    int64
	slug         string
	specID       *int64
	scenarioID   *int64
	settingsJSON string
}

func (r *Repo) readWorkspaceCore(ctx context.Context, workspaceID int64) (workspaceCore, error) {
	var (
		c                  workspaceCore
		specID, scenarioID sql.NullInt64
	)
	err := r.db.R.QueryRowContext(ctx,
		"SELECT created_at, slug, spec_id, scenario_id, settings FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&c.createdAt, &c.slug, &specID, &scenarioID, &c.settingsJSON)
	switch {
	case err == nil:
		if specID.Valid {
			v := specID.Int64
			c.specID = &v
		}
		if scenarioID.Valid {
			v := scenarioID.Int64
			c.scenarioID = &v
		}
		return c, nil
	case errors.Is(err, sql.ErrNoRows):
		return workspaceCore{}, fmt.Errorf("workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	default:
		return workspaceCore{}, fmt.Errorf("read workspace %d: %w", workspaceID, err)
	}
}

// nullIntEqual reports whether a SQL-scanned nullable int and a *int64
// captured outside a transaction name the same value (both absent, or both
// present and equal) — the shape [fenceConfirmTx] needs for spec_id and
// scenario_id.
func nullIntEqual(a sql.NullInt64, b *int64) bool {
	if !a.Valid {
		return b == nil
	}
	return b != nil && a.Int64 == *b
}
