package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// ErrEditConflict is the sentinel every repository's write verb returns when
// a caller's expected edit_version does not match the row it is about to
// write, per D7/D8/D9 of the A3 decision document.
//
// It is declared ONCE, here, rather than once per repository (the pattern
// this tree otherwise uses for ErrNotFound) because internal/store is the
// only package op_overrides, custom_endpoints, workspaces and scenarios all
// four already import, and it must not import any of them back. Two
// near-namesakes already exist one screen from where a fifth would land --
// internal/scenarios.ErrConcurrentEdit and
// internal/checkpoints.ErrConcurrentEdit, both already answered as 409 under
// CodeConflict -- and a new sentinel with nearly that name, at the same
// status, under a different wire code, would read as a synonym for whichever
// of the two a reader met first.
var ErrEditConflict = errors.New("store: edit conflict")

// EditConflictError carries what a caller needs to retry a write that lost
// its compare-and-swap. It wraps [ErrEditConflict], so callers use
// errors.Is(err, store.ErrEditConflict) to detect the conflict and
// errors.As(err, &conflict) to reach the payload.
//
// internal/store can import none of the four repository packages (they all
// import it), so Current cannot be a typed row: it is the repository's own
// row value, boxed, and each admin handler type-switches it to build the
// wire "details" its route declares. Gone distinguishes "a row exists, but
// not at the version you expected" from "the row you expected is gone
// entirely" -- the two shapes D6 gives different wire payloads.
type EditConflictError struct {
	// Gone is true when the target row no longer exists: an expectation was
	// sent (so the caller is not claiming "I make no claim") but the row it
	// names cannot be found. False means the row exists at a version other
	// than the one the caller expected, and Current carries it.
	Gone bool
	// Current is the row as it exists now, boxed by the repository that
	// detected the conflict. Nil when Gone is true.
	Current any
}

func (e *EditConflictError) Error() string {
	if e.Gone {
		return "store: edit conflict: row no longer exists"
	}
	return "store: edit conflict: row was changed by another write"
}

// Unwrap lets errors.Is(err, ErrEditConflict) see through the payload.
func (e *EditConflictError) Unwrap() error { return ErrEditConflict }

// AllocateEditVersion mints the next edit_version for workspaceID from that
// workspace's own monotone sequence and returns it. Every repository write
// that changes a field a guarded route can send calls this, inside the same
// write transaction, to obtain the version it stamps on the row it is
// writing (D4/D9).
//
// The statement names edit_seq and NOTHING ELSE: it must not touch revision
// and must not touch updated_at. That is load-bearing, not tidiness --
// internal/scenarios.Repo.Rename deliberately does not write the workspaces
// row at all today, because CLAUDE.md holds that a rename must not bump
// revision (the mock plane's routeCache keys on it, and a rename is meant to
// be visible without a bump). An allocator built on the existing
// bumpRevisionTx helper would break that documented invariant on the first
// verb that reached for it, which is why this is its own statement instead
// of a reuse of that one.
//
// RETURNING, not a following SELECT: the read-back must not be able to see
// another transaction's value, and with the writer pool pinned to a single
// connection plus one transaction per [DB.Write] call, RETURNING inside that
// same transaction is what makes the read-back exact.
func AllocateEditVersion(ctx context.Context, tx *sql.Tx, workspaceID int64) (int64, error) {
	var next int64
	err := tx.QueryRowContext(
		ctx,
		`UPDATE workspaces SET edit_seq = edit_seq + 1 WHERE id = ? RETURNING edit_seq`,
		workspaceID,
	).Scan(&next)
	if err != nil {
		return 0, fmt.Errorf("allocate edit_version for workspace %d: %w", workspaceID, err)
	}
	return next, nil
}
