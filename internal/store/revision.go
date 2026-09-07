package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

// WorkspaceRevisionTx reads a workspace's current revision INSIDE tx: a
// caller about to bump the value needs it as it stands in THIS transaction,
// immediately before the bump, not as of whatever the last committed read
// saw. exists is false when workspaceID names no row.
//
// Until 2026-09-07 this was a private, byte-identical copy in
// internal/overrides and internal/customep (named workspaceRevisionTx), and
// [BumpRevisionTx] below was a private, byte-identical copy in SIX
// packages — overrides, customep, resources, checkpoints, scenarios, assets.
// Every copy's own comment gave the same reason: "no package may import
// another purely for a four-line SQL helper". That reason stopped applying
// the day all six started importing internal/store anyway — five of them
// for [AllocateEditVersion], assets for the *DB type alone — store is the
// one dependency none of them import FROM each other, so it is the legal
// shared home the six comments were
// each explaining the absence of. CLAUDE.md's "a package bumps revision
// with its own bumpRevisionTx" is superseded by this file; the sentence is
// updated alongside it.
func WorkspaceRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (revision int64, exists bool, err error) {
	err = tx.QueryRowContext(ctx, "SELECT revision FROM workspaces WHERE id = ?", workspaceID).Scan(&revision)
	switch {
	case err == nil:
		return revision, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return 0, false, nil
	default:
		return 0, false, fmt.Errorf("read workspace %d revision: %w", workspaceID, err)
	}
}

// BumpRevisionTx is HARD RULE 5's direct UPDATE, verbatim: never
// workspaces.Repo.Update, which opens its own write transaction and would
// deadlock the single-connection writer pool when called from inside the
// db.Write callback this runs in. The increment happens IN the UPDATE
// (revision = revision + 1) rather than as a value computed in Go and sent
// down — with BEGIN IMMEDIATE on a one-connection writer pool the two are
// equivalent (nothing else can be mid-transaction against this row when this
// runs), but writing it this way keeps this function correct even if that
// invariant ever changes upstream, instead of depending on it silently.
//
// See [WorkspaceRevisionTx]'s doc comment for why this now lives here
// instead of as six repeated private copies.
func BumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}
