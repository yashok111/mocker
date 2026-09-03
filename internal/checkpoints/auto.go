// Auto: the debounced checkpoint before a destructive admin route. Split out
// of repo.go 2026-09-03; the text is unchanged.
package checkpoints

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// queryRower is the read surface [autoWindowSuppressed] needs — satisfied
// by both *sql.DB (the reader pool, [Repo]'s outer probe) and *sql.Tx (the
// write transaction, the inner re-check) — so [Repo.Auto]'s two checks run
// the identical query on two different connections instead of two
// hand-copied SELECTs that could drift apart.
type queryRower interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// autoWindowSuppressed reports whether [Repo.Auto]'s debounce window has
// NOT yet elapsed for workspaceID: true when a KindAuto row already exists
// whose created_at is within window seconds of now.
//
// The comparison is `now - max >= window`, with >= and not >: a sleep of
// EXACTLY the window must re-arm (SIG-AUTO). A workspace with no auto row
// yet has nothing to debounce against, so MAX returning SQL NULL (an empty
// aggregate, not an error) is "never suppressed" rather than a read
// failure.
func autoWindowSuppressed(ctx context.Context, q queryRower, workspaceID int64, window int, now time.Time) (bool, error) {
	var lastAuto sql.NullInt64
	if err := q.QueryRowContext(ctx,
		"SELECT MAX(created_at) FROM checkpoints WHERE workspace_id = ? AND kind = ?",
		workspaceID, KindAuto,
	).Scan(&lastAuto); err != nil {
		return false, fmt.Errorf("read last auto checkpoint for workspace %d: %w", workspaceID, err)
	}
	if !lastAuto.Valid {
		return false, nil
	}
	elapsed := now.Unix() - lastAuto.Int64
	return elapsed < int64(window), nil
}

// Auto writes a debounce ("auto") checkpoint for workspaceID, unless one was
// written less than window seconds ago. It returns (nil, nil) when the window
// suppressed the write — suppression is an ordinary return, never an error.
// window <= 0 is not a valid call: the caller decides not to call.
//
// It reuses [Repo.Create]'s WHOLE sequence — [validateLabel], [retrying],
// [Repo.captureSnapshot], then one [store.DB.Write] containing [fenceTx],
// [insertCheckpointTx] and [pruneRetentionTx] — differing only in the kind
// it writes ([KindAuto]) and in the window check. Calling
// [insertCheckpointTx] directly is forbidden: it writes an UNFENCED
// snapshot and never prunes, both of which [Repo.Create]'s own doc comment
// already explains are load-bearing here, not belt-and-braces.
//
// The window is checked TWICE, at two different costs on purpose. The
// OUTER probe runs on the reader pool BEFORE [Repo.captureSnapshot]: a
// suppressed call costs one indexed read instead of a snapshot and a gzip
// — the common case, since ROUTER calls this after EVERY one of its eight
// labelled routes and most of those calls land inside the window. The
// IDENTICAL condition is re-evaluated INSIDE the write transaction,
// immediately before the insert, because two concurrent mutations that
// both passed the outer probe on two different reader-pool connections
// would otherwise both write — the inner check runs against the single
// writer connection, which serialises them.
func (r *Repo) Auto(ctx context.Context, workspaceID int64, label string, createdBy int64, window int) (*Summary, error) {
	label, err := validateLabel(label)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	if suppressed, serr := autoWindowSuppressed(ctx, r.db.R, workspaceID, window, now); serr != nil {
		return nil, serr
	} else if suppressed {
		return nil, nil
	}

	var out *Summary
	err = retrying(func() error {
		snap, cerr := r.captureSnapshot(ctx, workspaceID)
		if cerr != nil {
			return cerr
		}
		capturePreWriteHook()
		return r.db.Write(ctx, func(tx *sql.Tx) error {
			if _, ferr := fenceTx(ctx, tx, workspaceID, snap.core); ferr != nil {
				return ferr
			}
			insertNow := time.Now().UTC()
			// The inner re-check: same condition, same window, evaluated
			// against the write connection so a second racing caller that
			// also cleared the outer probe cannot also win the insert.
			suppressed, serr := autoWindowSuppressed(ctx, tx, workspaceID, window, insertNow)
			if serr != nil {
				return serr
			}
			if suppressed {
				out = nil
				return nil
			}
			// D5: always-capture, same as Create — an auto row is one of
			// the two capture sites D5 names as "having nobody to ask" and
			// gets the same unconditional call.
			dataBlob, _, derr := captureEntitiesTx(ctx, tx, workspaceID)
			if derr != nil {
				return derr
			}
			s, ierr := insertCheckpointTx(ctx, tx, workspaceID, KindAuto, label, snap.blob, dataBlob, createdBy, insertNow)
			if ierr != nil {
				return ierr
			}
			// C7: no rollback target here, so the plain rule — same as
			// [Repo.Create], and for the same reason: the row just
			// inserted is never its own prune's victim (keepNone).
			if perr := pruneRetentionTx(ctx, tx, workspaceID, r.retention, keepNone); perr != nil {
				return perr
			}
			out = s
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}
