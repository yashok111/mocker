// The transaction helpers every write of this package shares: the identity
// fence, the row insert, retention, the revision bump (HARD RULE 5's copy)
// and the bounded retry. Split out of repo.go 2026-09-03; the text is
// unchanged.
package checkpoints

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// maxAttempts bounds C5's retry loop: three tries, then [ErrConcurrentEdit]
// and a 409. The bound is what keeps a non-mutating button from failing
// spuriously — a 409 needs three CONSECUTIVE concurrent commits, not one —
// while still being bounded, so a workspace under genuinely constant
// concurrent editing fails fast and visibly instead of the request hanging
// in a loop. Same bound and same status internal/scenarios uses for its own
// fence (scenarios/repo.go's maxCoherentReadAttempts).
const maxAttempts = 3

// errFenceMoved is C5 step 4's mismatch, raised INSIDE the transaction and
// caught by [retrying], which redoes the whole operation from the
// pre-transaction read. It is unexported: a caller never sees it, because
// either a retry succeeds or the loop exhausts and returns
// [ErrConcurrentEdit].
var errFenceMoved = errors.New("checkpoints: workspace moved between the snapshot read and the transaction")

// retrying runs one whole attempt — the pre-transaction read AND the
// transaction — up to [maxAttempts] times, redoing the read each time,
// and converts an exhausted fence into [ErrConcurrentEdit].
//
// The read has to be inside the loop, not hoisted above it: a retry exists
// precisely because the workspace moved, so re-running the transaction over
// the SAME stale snapshot would either fence again forever or, worse,
// store bytes that no longer describe the workspace.
func retrying(attempt func() error) error {
	var last error
	for i := 0; i < maxAttempts; i++ {
		err := attempt()
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errFenceMoved):
			last = err
		default:
			return err
		}
	}
	return fmt.Errorf("%w after %d attempts: %w", ErrConcurrentEdit, maxAttempts, last)
}

// wsFence is [fenceTx]'s in-transaction read: the identity triple it
// compares, plus whether a scenario is active — read here rather than
// before the transaction so the flag §C's response bodies carry describes
// the state at WRITE time, not at read time.
type wsFence struct {
	revision       int64
	createdAt      int64
	slug           string
	scenarioActive bool
}

// fenceTx is C5 step 4: re-read revision, created_at and slug INSIDE the
// transaction and compare ALL THREE against what the pre-transaction read
// recorded.
//
// revision ALONE cannot prove workspace identity. workspaces.id is INTEGER
// PRIMARY KEY WITHOUT AUTOINCREMENT (0001_init.sql:99), so a deleted
// workspace's id can be reused with revision back at 1 — reachable, not
// theoretical, precisely because a manual checkpoint does not bump (C12).
// created_at is written once and never updated (workspaces/repo.go:162-164,
// absent from every UPDATE) but is SECOND-resolution, so slug (UNIQUE) is
// compared alongside it.
//
// What the comparison PROVES: the writer pool is one connection
// (store.go:74) and every writer of the sources bumps revision in its own
// transaction (overrides/repo.go:285, customep/repo.go:214,
// workspaces/repo.go:329-338), so a value read after BeginTx is the
// committed head and cannot move before COMMIT.
//
// RESIDUAL WINDOW, accepted rather than closed: deleting a workspace and
// recreating one with the same id, the same slug, in the same second, at
// the same revision, while an operation is in flight. The alternative is a
// monotonic incarnation column — a migration this slice does not take.
func fenceTx(ctx context.Context, tx *sql.Tx, workspaceID int64, before workspaceCore) (wsFence, error) {
	var (
		f          wsFence
		scenarioID sql.NullInt64
	)
	err := tx.QueryRowContext(ctx,
		"SELECT revision, created_at, slug, scenario_id FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&f.revision, &f.createdAt, &f.slug, &scenarioID)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return wsFence{}, fmt.Errorf("workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	default:
		return wsFence{}, fmt.Errorf("read workspace %d inside transaction: %w", workspaceID, err)
	}
	f.scenarioActive = scenarioID.Valid

	if f.revision != before.revision || f.createdAt != before.createdAt || f.slug != before.slug {
		return wsFence{}, fmt.Errorf("%w: workspace %d (revision %d->%d, createdAt %d->%d, slug %q->%q)",
			errFenceMoved, workspaceID, before.revision, f.revision, before.createdAt, f.createdAt, before.slug, f.slug)
	}
	return f, nil
}

// checkpointExistsTx re-reads the rollback target for existence inside the
// transaction — see [Repo.rollbackTx] on why this is corroboration rather
// than proof.
func checkpointExistsTx(ctx context.Context, tx *sql.Tx, workspaceID, checkpointID int64) error {
	var one int
	err := tx.QueryRowContext(ctx,
		"SELECT 1 FROM checkpoints WHERE id = ? AND workspace_id = ?", checkpointID, workspaceID).Scan(&one)
	switch {
	case err == nil:
		return nil
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("%w: checkpoint %d in workspace %d", ErrNotFound, checkpointID, workspaceID)
	default:
		return fmt.Errorf("re-read checkpoint %d inside transaction: %w", checkpointID, err)
	}
}

// insertCheckpointTx writes one checkpoints row and returns its summary.
//
// dataBlob is written verbatim into data_snap, including nil — a nil
// []byte parameter binds to SQL NULL exactly the way a hand-written NULL
// literal used to (database/sql's own []byte handling, not a special case
// this function adds). Every caller passes the result of
// [captureEntitiesTx], which returns nil in exactly two cases: the
// four-term probe (D5.2) found the workspace's entity data over budget, or
// [compressSnapshot] refused the encoded document over [maxSnapshotBytes]
// — both a DEGRADE, never a hand-written omission the way the column used
// to be treated before P3d. HasData is set in the struct literal below
// FROM dataBlob, the same rule [Repo.List] and [Repo.Get] derive it by:
// this is the OTHER producer of a Summary, and Go zero-fills an omitted
// field silently, so a checkpoint returned straight from an insert must set
// it explicitly rather than rely on the caller re-fetching to find out.
func insertCheckpointTx(ctx context.Context, tx *sql.Tx, workspaceID int64, kind, label string, blob, dataBlob []byte, createdBy int64, now time.Time) (*Summary, error) {
	res, err := tx.ExecContext(ctx, `
		INSERT INTO checkpoints (workspace_id, kind, label, config_snap, data_snap, created_at, created_by)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		workspaceID, kind, label, blob, dataBlob, now.Unix(), createdBy)
	if err != nil {
		return nil, fmt.Errorf("insert %s checkpoint for workspace %d: %w", kind, workspaceID, err)
	}
	id, err := res.LastInsertId()
	if err != nil {
		return nil, fmt.Errorf("checkpoint id for workspace %d: %w", workspaceID, err)
	}
	user := createdBy
	return &Summary{
		ID:        id,
		Kind:      kind,
		Label:     label,
		CreatedAt: now.Truncate(time.Second),
		CreatedBy: &user,
		HasData:   dataBlob != nil,
	}, nil
}

// pruneRetentionTx is C7, in the SAME transaction as the insert that
// overflowed it. The shape — and only the shape — follows
// traffic/recorder.go:450-452's pruneRetentionTx: an inner SELECT of the
// ids to keep, both halves filtered on workspace_id first so this is a
// bounded walk of one workspace's rows through the checkpoints_ws index,
// never a full-table scan.
//
// Three things it does NOT copy from that precedent:
//
//   - `retention <= 0` returns early and that branch is REACHABLE here.
//     MOCKER_CHECKPOINT_RETENTION=0 means PRUNE NOTHING (C7) — see
//     [NewRepo] for why this knob deliberately differs from the traffic
//     one, whose zero is normalised away at construction and whose
//     identical guard is therefore dead.
//   - The population is machine-made rows ONLY. DESIGN §12:773 —
//     «Ретеншн — MOCKER_CHECKPOINT_RETENTION, именованные не удаляются» —
//     so a manual row is never counted and never deleted. The filter is
//     `kind <> 'manual'` rather than `kind = 'pre-destructive'` because the
//     column keeps accepting "auto" (C6) and P2d's rows must land in the
//     pruned population without a second edit here.
//   - keepID spares the row a rollback is restoring FROM, when it is a
//     machine-made one outside the newest N. That leaves N+1 rows on
//     purpose; [Repo.rollbackTx] carries the reason.
func pruneRetentionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, retention int, keepID int64) error {
	if retention <= 0 {
		return nil
	}
	if _, err := tx.ExecContext(ctx, `
		DELETE FROM checkpoints
		WHERE workspace_id = ? AND kind <> 'manual' AND id <> ? AND id NOT IN (
			SELECT id FROM checkpoints
			WHERE workspace_id = ? AND kind <> 'manual'
			ORDER BY id DESC LIMIT ?
		)`, workspaceID, keepID, workspaceID, retention); err != nil {
		return fmt.Errorf("prune checkpoints for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// bumpRevisionTx is HARD RULE 5's direct UPDATE, the FOURTH private copy in
// this tree (overrides/repo.go:283, customep/repo.go:212,
// scenarios/repo.go:589). Copied rather than shared for the reason those
// three already state: sharing would mean one of four sibling packages
// importing another purely for a four-line SQL helper, which is a backwards
// dependency for at least three of them. Never workspaces.Repo.Update,
// which opens its own write transaction and deadlocks the
// single-connection writer pool from inside a db.Write callback.
//
// It is called EXACTLY ONCE per rollback and per destructive reset, and NOT
// AT ALL for a manual checkpoint (C12) — and neither ReplaceAllTx bumps, so
// a caller that also bumped inside them would allocate two revisions for
// one operation (§G obs 2 fails on precisely that double bump).
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}
