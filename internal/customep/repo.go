package customep

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/store"
)

// Repo is the custom_endpoints table's data-access layer, the same shape as
// internal/overrides/repo.go over op_overrides — copy that file's patterns
// rather than inventing new ones here, especially bumpRevisionTx (HARD RULE
// 5: never call workspaces.Repo.Update from inside a db.Write callback, it
// deadlocks the one-connection writer pool).
type Repo struct {
	db *store.DB

	// MaxFrameBytes is the cap a stream frame's payload is validated
	// against on write (P6b D5): cmd/mocker/main.go and admin.New set it
	// from config.MaxResponse; zero means DefaultMaxFrameBytes, which is
	// the same number that variable defaults to. A field rather than a
	// NewRepo parameter because NewRepo's signature is shared with call
	// sites that never write a stream (the mock plane's own reader).
	MaxFrameBytes int64
}

// NewRepo builds a Repo over db.
func NewRepo(db *store.DB) *Repo {
	return &Repo{db: db}
}

// ForWorkspace returns every custom endpoint of one workspace in ONE query,
// ordered by source_order then id — the same order [router.compareRoutes]
// falls back to as its final tie-break, so a caller building a route table
// from this slice already sees them in the order DESIGN §8 rule 4 wants.
func (r *Repo) ForWorkspace(ctx context.Context, workspaceID int64) ([]*Row, error) {
	rows, err := r.db.R.QueryContext(ctx,
		selectRow+" WHERE workspace_id = ? ORDER BY source_order, id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list endpoints for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []*Row
	for rows.Next() {
		row, serr := scan(rows)
		if serr != nil {
			return nil, fmt.Errorf("endpoints for workspace %d: %w", workspaceID, serr)
		}
		out = append(out, row)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate endpoints for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// Get looks up one custom endpoint by its id, scoped to workspaceID so one
// workspace can never read another's row by guessing an id.
func (r *Repo) Get(ctx context.Context, workspaceID, id int64) (*Row, error) {
	return scanOne(r.db.R.QueryRowContext(ctx,
		selectRow+" WHERE workspace_id = ? AND id = ?", workspaceID, id))
}

// Create inserts one custom endpoint and bumps workspaces.revision IN THE
// SAME TRANSACTION as bumpRevisionTx does for op_overrides — without the
// bump the route cache, keyed (workspace_id, revision), never rebuilds and
// the endpoint this call just wrote 404s until some unrelated edit happens
// to bump it instead.
//
// row's ID, WorkspaceID, CanonicalPath, SourceOrder, CreatedAt and UpdatedAt
// are all ignored on input and assigned here: identity and ordering are this
// method's job, not the caller's guess. CanonicalPath is computed from the
// now-normalized Path via router.CanonicalPath; SourceOrder is
// max(source_order)+1 for the workspace, read inside this same transaction
// so a concurrent Create cannot race it onto the same value (SQLite's
// single-connection writer pool serializes the two transactions instead).
//
// A second row with the same (method, canonical_path) fails the UNIQUE
// index at migrations/0001_init.sql:211 and is reported as ErrConflict —
// mapped from the constraint violation, never checked as a SELECT-then-
// INSERT race that a concurrent Create could still slip through. A custom
// endpoint canonically equal to a SPEC operation is NOT a conflict at this
// layer (DESIGN §8 calls that the documented override); the cross-table rule
// against an op_overrides row on the same (method, path) is internal/admin's
// job, the one layer that holds both repos.
func (r *Repo) Create(ctx context.Context, workspaceID int64, row *Row) (*Row, error) {
	if row == nil {
		return nil, fmt.Errorf("create endpoint for workspace %d: %w: nil row", workspaceID, ErrInvalidRow)
	}

	input := *row // copy: Create must never mutate the caller's Row in place
	input.WorkspaceID = workspaceID
	// A newly created endpoint is ON, and the default lives HERE rather than in
	// each caller. internal/overrides/repo.go builds its brand-new rows with
	// OverrideOn true for exactly this reason, and the column itself is
	// DEFAULT 1 — but Row.OverrideOn is a plain bool, so any caller that simply
	// does not mention the field inserts 0, runtime.go then leaves the row out
	// of the route table, and the endpoint an operator just created answers 404
	// with no way to tell why. Both callers in this slice (the CRUD POST and
	// the create-from-a-traffic-row conversion) hit that; the acceptance test
	// caught it. Nothing here can express "create it switched off" — when PUT
	// arrives, the field becomes the caller's to set.
	input.OverrideOn = true

	var stored *Row
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		_, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("create endpoint for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		if verr := normalizeAndValidate(&input, r.MaxFrameBytes); verr != nil {
			return verr
		}
		if terr := refuseTakenOperationIDTx(ctx, tx, &input, 0); terr != nil {
			return fmt.Errorf("create endpoint %s %s: %w", input.Method, input.Path, terr)
		}
		input.CanonicalPath = router.CanonicalPath(input.Path)

		nextOrder, oerr := nextSourceOrderTx(ctx, tx, workspaceID)
		if oerr != nil {
			return oerr
		}
		input.SourceOrder = nextOrder

		now := time.Now().UTC()

		// A created row needs an edit_version nobody else holds, or two live
		// rows of this workspace carry the same number on the day it ships
		// (D4/D9: creates allocate too, even though D2 excludes them from the
		// compare-and-swap check itself -- a create has no prior version to
		// compare against).
		newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		input.EditVersion = newVersion

		id, ierr := insertTx(ctx, tx, &input, now)
		if ierr != nil {
			if isUniqueViolation(ierr) {
				return fmt.Errorf("create endpoint %s %s: %w", input.Method, input.Path, ErrConflict)
			}
			return fmt.Errorf("create endpoint %s %s: %w", input.Method, input.Path, ierr)
		}

		got, gerr := getTx(ctx, tx, workspaceID, id)
		if gerr != nil {
			return fmt.Errorf("reload endpoint after create: %w", gerr)
		}

		if berr := bumpRevisionTx(ctx, tx, workspaceID, now); berr != nil {
			return berr
		}

		stored = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// Update replaces one custom_endpoints row's editable definition — Method,
// Path, OverrideOn, RouteOff, ActiveStatus, Responses, ListSize and DelayMs —
// via mutate, which is called with the row exactly as [Get] would return it.
// ReqSchema, FailDirective and ValidateReq (this slice's PRESERVED-ONLY
// fields — no handler populates them, D4's wire shape doesn't carry them
// either) are left exactly as mutate finds them unless mutate itself changes
// them, and identity — ID and WorkspaceID — is reasserted AFTER mutate runs,
// the same guard [overrides.Repo.Put] applies to its own natural-key fields
// (overrides/repo.go:115-117): a bug in mutate must not be able to retarget
// the UPDATE below at a different row or workspace than the one the caller
// named.
//
// Unlike overrides.Repo.Put, Method and Path are NOT reasserted after
// mutate — they are exactly the two fields this route lets an operator edit
// (D4 3c: PUT is a full replacement of the definition, and the definition
// includes which method and path it answers on), so mutate setting them IS
// the intended effect, not a bug to guard against.
//
// Its own UPDATE ... WHERE workspace_id = ? AND id = ? — NOT upsertTx, whose
// conflict target is the natural key (workspace_id, method, path) (see that
// function's own ON CONFLICT clause below). Reusing upsertTx here would, on
// an edit that changes Path, INSERT a fresh row under a NEW id rather than
// update the row the caller named — exactly the id-stability failure GET
// /endpoints' callers depend on not happening, since that route hands the
// id out and holds onto it.
//
// CanonicalPath is recomputed from the (possibly just-edited) Path here,
// never carried from mutate's own assignment to it (mutate cannot even reach
// it — it is set by this method after mutate returns) — same reasoning as
// Create's identical recomputation, since it is the authority the SECOND
// unique index below depends on.
//
// An edit can violate EITHER of custom_endpoints' two unique indexes — the
// natural key (workspace_id, method, path) and the canonical key
// (workspace_id, method, canonical_path), migrations/0001_init.sql:210-211 —
// by moving the row onto a key another row already holds. Both come back
// through the SAME isUniqueViolation substring check Create already uses and
// are reported as the SAME ErrConflict: SQLite's own error message does not
// name which index fired, and a caller three layers up (the admin handler,
// answering 409 either way) has no use for the distinction even if it were
// available.
func (r *Repo) Update(ctx context.Context, workspaceID, id int64, mutate func(cur *Row) error) (*Row, error) {
	return r.UpdateExpecting(ctx, workspaceID, id, nil, mutate)
}

// UpdateExpecting is Update's sibling with a per-row compare-and-swap (A3,
// D7/D8): expect is "no expectation" when nil (Update's own delegation
// above -- there is no second production caller of this verb the way
// overrides.Put has from_traffic.go, so nil is reachable only from tests
// and from Update itself), and otherwise the edit_version the caller last
// read for this (workspace, id) from GET .../endpoints or from this same
// route's own prior write response.
//
// Unlike op_overrides, `0` is REFUSED here rather than meaningful: a custom
// endpoint row always exists by the time PUT .../endpoints/{eid} is
// reachable (the id came from a prior GET or Create response), so a caller
// expecting no row has misunderstood something (D7's "0 is meaningful only
// for op_overrides" paragraph).
//
// The five cases collapse to two once expect != nil, because this table
// never lets a write proceed against an absent target row:
//   - expect == nil: no check at all, proceeds exactly as Update always has
//     (including its existing 404 for a deleted row).
//   - expect != nil, target row present in this workspace at expect: proceeds.
//   - expect != nil, target row present in this workspace at a different
//     version (0 included): store.EditConflictError{Current: *current}.
//   - expect != nil, target row absent -- TWO STATES, and this workspace-
//     scoped verb cannot tell them apart from getTx's "no rows" alone.
//     getTx matches WHERE workspace_id = ? AND id = ?, so a deleted id and
//     an id belonging to ANOTHER workspace both land on the same branch
//     (D7/D8's "the target row" qualifier). This decides them apart with
//     ONE extra read of the id ALONE, unscoped by workspace, inside the
//     SAME transaction (the same idiom internal/scenarios/repo.go's Rename
//     already uses for its own re-read, one statement below its scoped
//     UPDATE): no row anywhere -> GONE, store.EditConflictError{Gone: true};
//     a row under a DIFFERENT workspace -> NOT YOURS, ErrNotFound, and the
//     route's existing 404 stands unchanged. That unscoped read leaks
//     nothing this tree does not already leak: internal/admin/security.go's
//     requireUser is the only authorization check any handler performs, and
//     once a caller is logged in every workspace is theirs to read and edit.
//
// Every case that proceeds allocates a fresh edit_version from the
// workspace's sequence (store.AllocateEditVersion) and stamps it on the row
// this writes -- never expect+1 in place (D4/D9).
func (r *Repo) UpdateExpecting(ctx context.Context, workspaceID, id int64, expect *int64, mutate func(cur *Row) error) (*Row, error) {
	if mutate == nil {
		return nil, fmt.Errorf("update endpoint %d for workspace %d: %w: nil mutate", id, workspaceID, ErrInvalidRow)
	}

	var stored *Row
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		_, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("update endpoint for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		current, gerr := getTx(ctx, tx, workspaceID, id)
		if gerr != nil {
			if !errors.Is(gerr, ErrNotFound) {
				return fmt.Errorf("update endpoint %d: %w", id, gerr)
			}
			// The workspace-scoped read found nothing. With no expectation
			// this is Update's original behaviour -- preserve the sentinel,
			// the handler's existing 404 stands.
			if expect == nil {
				return fmt.Errorf("update endpoint %d: %w", id, gerr)
			}
			// An expectation was sent: decide GONE from NOT-YOURS with one
			// unscoped read of the id alone, inside this same transaction.
			owner, oerr := ownerWorkspaceTx(ctx, tx, id)
			if oerr != nil {
				return oerr
			}
			if owner == nil {
				return &store.EditConflictError{Gone: true}
			}
			// A row exists but under a different workspace -- not the
			// caller's to conflict over, and not the caller's to see: the
			// original 404 stands, D7/D8's "target row" qualifier.
			return fmt.Errorf("update endpoint %d: %w", id, ErrNotFound)
		}

		if expect != nil {
			if *expect != current.EditVersion {
				return &store.EditConflictError{Current: *current}
			}
		}

		if merr := mutate(current); merr != nil {
			return fmt.Errorf("mutate endpoint %d: %w", id, merr)
		}
		current.ID = id
		current.WorkspaceID = workspaceID

		if verr := normalizeAndValidate(current, r.MaxFrameBytes); verr != nil {
			return verr
		}
		if terr := refuseTakenOperationIDTx(ctx, tx, current, id); terr != nil {
			return fmt.Errorf("update endpoint %d: %w", id, terr)
		}
		current.CanonicalPath = router.CanonicalPath(current.Path)

		now := time.Now().UTC()
		newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		current.EditVersion = newVersion

		if uerr := updateTx(ctx, tx, current, now); uerr != nil {
			if isUniqueViolation(uerr) {
				return fmt.Errorf("update endpoint %d to %s %s: %w", id, current.Method, current.Path, ErrConflict)
			}
			return fmt.Errorf("update endpoint %d: %w", id, uerr)
		}

		got, gerr2 := getTx(ctx, tx, workspaceID, id)
		if gerr2 != nil {
			return fmt.Errorf("reload endpoint after update: %w", gerr2)
		}

		if berr := bumpRevisionTx(ctx, tx, workspaceID, now); berr != nil {
			return berr
		}

		stored = got
		return nil
	})
	if err != nil {
		return nil, err
	}
	return stored, nil
}

// ReplaceAllTx restores workspaceID's ENTIRE custom_endpoints table to
// exactly rows, inside the CALLER's transaction — internal/checkpoints'
// rollback and reset-overrides, which open the one db.Write for a whole
// multi-table restore and call this as one of the tx-scoped writes inside
// it (HARD RULE 5, repo.go:20-22: this function must never open its own
// db.Write, or it would deadlock the single-connection writer pool the
// caller already holds — a tx-scoped function opens nothing, so the rule is
// not in tension with this at all).
//
// It deliberately does NOT bump workspaces.revision (the caller bumps once
// for the whole restore — C12) and does NOT return a changed-count (C4's
// closing paragraph: a gate round added one for reset-overrides' no-op rule
// and it created an ordering contradiction, since the count is only known
// after the apply while the no-op decision has to be made from a read taken
// BEFORE the transaction opens).
//
// overrides.ReplaceAllTx is this function's byte-identical twin in shape
// (mirroring the package's existing upperASCII/normalizeAndValidate
// twinning, customep.go:177); it exists as a separate function, in a
// separate package, because HARD RULE 5 forbids either package from writing
// through the other's db.Write, and neither package can express a single
// shared helper that writes two different tables under one caller-owned
// *sql.Tx without one of them importing storage internals it does not own.
func ReplaceAllTx(ctx context.Context, tx *sql.Tx, workspaceID int64, rows []*Row, now time.Time) error {
	// Duty 1 (C4): upper-case every row's Method FIRST, in a pass of ITS
	// OWN, before the delete set below is computed from it. Not a call to
	// normalizeAndValidate — that also validates, and duty 4 needs
	// validation to happen INSIDE the write loop, one row at a time, not up
	// front for the whole slice. upperASCII is this package's own existing
	// unexported helper (customep.go:183, overrides' byte-identical twin);
	// nothing new is exported for this pass.
	//
	// A snapshot row that reads "get /a" and a live row stored as "GET /a"
	// are the SAME operation, but the delete query below matches on the
	// (method, path) natural key BY VALUE. Without this pass running FIRST,
	// "get" and "GET" look like two different keys: the delete removes the
	// live "GET /a" row (its key is absent from a rows set that still says
	// "get"), and the upsert loop then INSERTs "get /a" fresh, under a NEW
	// id — exactly the id-stability failure C1 and §G obs 6 exist to
	// prevent, invisible everywhere else because every row that ever
	// reached this table through the admin API was already normalized on
	// the way in (Repo.Create calls normalizeAndValidate before storing,
	// repo.go:116). §G obs 16(b) drives this directly against ReplaceAllTx —
	// the fixture MUST also hold a row with a HIGHER rowid in a DIFFERENT
	// workspace (custom_endpoints.id has no AUTOINCREMENT, 0001_init.sql:191,
	// so a workspace-scoped delete-and-recreate would otherwise hand the row
	// back its own freed id and the check would pass vacuously).
	for _, row := range rows {
		if row == nil {
			return fmt.Errorf("%w: nil row", ErrInvalidRow)
		}
		row.Method = upperASCII(row.Method)
	}

	// Duty 3 (C1's order): DELETE every row whose (method, path) key is
	// absent from rows, THEN upsert. The order is load-bearing HERE
	// specifically, unlike in the sibling package: custom_endpoints carries
	// a SECOND unique index this restore does NOT key on —
	// UNIQUE (workspace_id, method, canonical_path), 0001_init.sql:211. A
	// snapshot row "GET /a/{id}" restored over a live row "GET /a/{x}" is
	// two DIFFERENT (method, path) keys that collapse to the SAME
	// canonical_path "/a/{}": upsert-before-delete would try to write the
	// snapshot row while the live one still occupies that canonical_path
	// and abort the whole transaction on the UNIQUE violation.
	// Delete-before-upsert clears it first — the live row's key is absent
	// from the snapshot, so it is gone before the upsert below ever runs.
	// §G obs 16(a) drives exactly this.
	if err := deleteAbsentTx(ctx, tx, workspaceID, rows); err != nil {
		return err
	}

	// Duty 4: validate, THEN write, one row at a time — not a validate-all
	// pass followed by a write-all pass. §G obs 15's fault injection fails a
	// restore on its SECOND row and asserts the WHOLE transaction (this
	// write and the checkpoint insert alongside it) rolls back with nothing
	// committed; that only distinguishes "atomic" from "already
	// half-applied" if the first row can actually reach the table before
	// the second one fails.
	for _, row := range rows {
		// Duty 2: set WorkspaceID on every row ourselves — a row decoded
		// from a checkpoint/scenario BLOB carries none, since
		// bundle.EndpointEntry never stores one (C3).
		row.WorkspaceID = workspaceID
		// A restore validates under the DEFAULT frame cap (0): ReplaceAllTx
		// is a package function with no Repo to read MOCKER_MAX_RESPONSE
		// from, and a snapshot written under the default must not be
		// refused by it.
		if verr := normalizeAndValidate(row, 0); verr != nil {
			return verr
		}
		// Duty 6: CanonicalPath is derived from the now-normalized Path via
		// router.CanonicalPath — the SAME computation Repo.Create uses
		// (repo.go:119), and nothing else on this path computes it. The
		// column is NOT NULL (0001_init.sql:196); a row restored with an
		// empty one would satisfy that constraint with garbage and simply
		// stop matching any request that reaches it. SourceOrder and
		// OverrideOn are NOT touched here — they are written VERBATIM from
		// the snapshot by upsertTx below, which is exactly why Repo.Create
		// cannot be reused for a restore: it forces OverrideOn true and
		// computes SourceOrder as max(source_order)+1, and a restore must
		// bring back a row that was switched off as switched off, in the
		// route-table tie-break position it actually held.
		row.CanonicalPath = router.CanonicalPath(row.Path)
		// Every restored row is allocated a FRESH edit_version here, never
		// carried over from the snapshot (the v3 format has no per-row
		// version to carry, and would not be trusted if it did): upsertTx's
		// ON CONFLICT SET list below does not name every column, so a row
		// present both live and in the snapshot is UPDATEd in place and
		// would otherwise keep the token a stale caller is still holding,
		// matching it against content this restore just replaced (D9's "the
		// dominant restore path does not reset the token to anything"
		// warning -- this closes it).
		newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		row.EditVersion = newVersion
		if uerr := upsertTx(ctx, tx, row, now); uerr != nil {
			return uerr
		}
	}
	return nil
}

// Delete removes one custom endpoint and bumps the revision in the same
// transaction — a deleted endpoint that keeps serving until the next
// unrelated edit rebuilds the route cache is the same bug as Create's missed
// bump, in reverse. Unlike op_overrides.Delete (which treats "nothing to
// delete" as a no-op, since an override's identity is optional to begin
// with), a custom endpoint only ever exists because Create made it, so
// deleting an id that is not there is ErrNotFound, and the revision is left
// untouched — nothing changed for the route table to need rebuilding over.
func (r *Repo) Delete(ctx context.Context, workspaceID, id int64) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		_, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("delete endpoint for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		res, derr := tx.ExecContext(ctx,
			"DELETE FROM custom_endpoints WHERE workspace_id = ? AND id = ?", workspaceID, id)
		if derr != nil {
			return fmt.Errorf("delete endpoint %d: %w", id, derr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("delete endpoint %d: rows affected: %w", id, raErr)
		}
		if n == 0 {
			return fmt.Errorf("delete endpoint %d: %w", id, ErrNotFound)
		}

		return bumpRevisionTx(ctx, tx, workspaceID, time.Now().UTC())
	})
}

// --- transaction-scoped helpers ---------------------------------------------

// workspaceRevisionTx is overrides/repo.go's helper of the same name,
// verbatim: reads the target workspace's current revision INSIDE tx, so
// Create/Delete see the revision as it stands immediately before they bump
// it, not as of the last committed read.
func workspaceRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (revision int64, exists bool, err error) {
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

// bumpRevisionTx is HARD RULE 5's direct UPDATE, copied from
// internal/overrides/repo.go verbatim (see that file's comment on the same
// name for the full deadlock rationale): never workspaces.Repo.Update, which
// opens its own write transaction and would deadlock the single-connection
// writer pool when called from inside the db.Write callback this runs in.
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// nextSourceOrderTx reads max(source_order) for the workspace INSIDE tx and
// returns one past it (or 0 for the workspace's first custom endpoint).
// Reading it inside the same transaction that then inserts the row is what
// keeps two concurrent Creates from computing the same next order: SQLite's
// one-connection writer pool serializes the two transactions, so the second
// one's SELECT MAX only runs after the first has committed its INSERT.
func nextSourceOrderTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (int64, error) {
	var maxOrder sql.NullInt64
	if err := tx.QueryRowContext(ctx,
		"SELECT MAX(source_order) FROM custom_endpoints WHERE workspace_id = ?", workspaceID,
	).Scan(&maxOrder); err != nil {
		return 0, fmt.Errorf("read max source_order for workspace %d: %w", workspaceID, err)
	}
	if !maxOrder.Valid {
		return 0, nil
	}
	return maxOrder.Int64 + 1, nil
}

// deleteAbsentTx deletes every custom_endpoints row for workspaceID whose
// (method, path) natural key is not present in rows — ReplaceAllTx's duty
// 3, C1's DELETE half, run before that function upserts what rows DOES
// hold. Keys on (method, path), NOT canonical_path: C1 is explicit that the
// restore keys on the natural key, and it is exactly the mismatch between
// this key and the SECOND unique index (workspace_id, method,
// canonical_path) that makes the delete-before-upsert order load-bearing
// here (see ReplaceAllTx's duty-3 comment).
//
// Same shape as overrides.deleteAbsentTx (one dynamic "NOT IN" statement
// over row values, rather than a read-then-diff loop against the same
// *sql.Tx connection this transaction is already using) — see that
// function's comment for why. modernc.org/sqlite has supported row-value
// NOT IN since SQLite 3.15.
func deleteAbsentTx(ctx context.Context, tx *sql.Tx, workspaceID int64, rows []*Row) error {
	if len(rows) == 0 {
		// An empty snapshot keeps NOTHING: every row currently stored for
		// this workspace is, by definition, absent from it. "NOT IN ()" has
		// no SQL syntax (an empty list is not a value), so the empty case is
		// its own unconditional DELETE rather than a zero-length NOT IN —
		// this is reset-overrides' custom-endpoint half (C9).
		if _, err := tx.ExecContext(ctx, "DELETE FROM custom_endpoints WHERE workspace_id = ?", workspaceID); err != nil {
			return fmt.Errorf("delete all endpoints for workspace %d: %w", workspaceID, err)
		}
		return nil
	}

	var sb strings.Builder
	sb.WriteString("DELETE FROM custom_endpoints WHERE workspace_id = ? AND (method, path) NOT IN (")
	args := make([]any, 0, 1+2*len(rows))
	args = append(args, workspaceID)
	for i, row := range rows {
		if i > 0 {
			sb.WriteString(", ")
		}
		sb.WriteString("(?, ?)")
		args = append(args, row.Method, row.Path)
	}
	sb.WriteString(")")
	if _, err := tx.ExecContext(ctx, sb.String(), args...); err != nil {
		return fmt.Errorf("delete stale endpoints for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// ownerWorkspaceTx reads only custom_endpoints.workspace_id for id, UNSCOPED
// by workspace -- the one read UpdateExpecting needs to tell "this id was
// deleted" from "this id belongs to a different workspace" apart, which a
// workspace-scoped getTx cannot do (both land on "no rows"). Returns nil,
// nil when no row anywhere carries id.
func ownerWorkspaceTx(ctx context.Context, tx *sql.Tx, id int64) (*int64, error) {
	var owner int64
	err := tx.QueryRowContext(ctx, "SELECT workspace_id FROM custom_endpoints WHERE id = ?", id).Scan(&owner)
	switch {
	case err == nil:
		return &owner, nil
	case errors.Is(err, sql.ErrNoRows):
		return nil, nil
	default:
		return nil, fmt.Errorf("read owning workspace for endpoint %d: %w", id, err)
	}
}

func getTx(ctx context.Context, tx *sql.Tx, workspaceID, id int64) (*Row, error) {
	row := tx.QueryRowContext(ctx, selectRow+" WHERE workspace_id = ? AND id = ?", workspaceID, id)
	return scanOne(row)
}

// insertTx writes row's columns and returns the new row's id. row.UpdatedAt
// and row.CreatedAt are stamped with now by the caller reload (getTx), not
// here, so this function has exactly one job: get the bytes into the table.
func insertTx(ctx context.Context, tx *sql.Tx, row *Row, now time.Time) (int64, error) {
	responsesJSON, err := marshalResponses(row.Responses)
	if err != nil {
		return 0, fmt.Errorf("marshal responses for %s %s: %w", row.Method, row.Path, err)
	}
	listSizeJSON, err := marshalListSize(row.ListSize)
	if err != nil {
		return 0, fmt.Errorf("marshal list_size for %s %s: %w", row.Method, row.Path, err)
	}
	delayMsJSON, err := marshalDelayMs(row.DelayMs)
	if err != nil {
		return 0, fmt.Errorf("marshal delay_ms for %s %s: %w", row.Method, row.Path, err)
	}

	var validateReq any
	if row.ValidateReq != nil {
		validateReq = boolToInt(*row.ValidateReq)
	}
	// ReqSchema and FailDirective are copied as plain strings, not re-run
	// through encoding/json — both are PRESERVED ONLY (their field comments
	// say so), and round-tripping through a jsonx.RawMessage Marshal/Unmarshal
	// pair risks compacting whitespace this slice has no business touching,
	// exactly as op_overrides/repo.go's upsertTx does for FailDirective.
	var reqSchema any
	if len(row.ReqSchema) > 0 {
		reqSchema = string(row.ReqSchema)
	}
	var failDirective any
	if len(row.FailDirective) > 0 {
		failDirective = string(row.FailDirective)
	}

	streamJSON, err := marshalStream(row.Stream)
	if err != nil {
		return 0, fmt.Errorf("marshal stream for %s %s: %w", row.Method, row.Path, err)
	}
	operationJSON, err := marshalOperation(row.Operation)
	if err != nil {
		return 0, fmt.Errorf("marshal operation for %s %s: %w", row.Method, row.Path, err)
	}

	res, err := tx.ExecContext(ctx, `
		INSERT INTO custom_endpoints
			(workspace_id, method, path, canonical_path, source_order, override_on, route_off,
			 active_status, responses, req_schema, list_size, delay_ms, fail_directive, validate_req,
			 created_at, updated_at, edit_version, kind, stream, operation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.WorkspaceID, row.Method, row.Path, row.CanonicalPath, row.SourceOrder,
		boolToInt(row.OverrideOn), boolToInt(row.RouteOff), row.ActiveStatus,
		responsesJSON, reqSchema, listSizeJSON, delayMsJSON, failDirective, validateReq,
		now.Unix(), now.Unix(), row.EditVersion, kindOrHTTP(row.Kind), streamJSON, operationJSON,
	)
	if err != nil {
		return 0, err
	}
	id, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("endpoint id for %s %s: %w", row.Method, row.Path, err)
	}
	return id, nil
}

// upsertTx writes row via INSERT ... ON CONFLICT (workspace_id, method,
// path) DO UPDATE. Unlike insertTx above (Repo.Create's INSERT-only path),
// this is ReplaceAllTx's writer ONLY — Repo.Create must stay INSERT-only
// because it owns two decisions this function must NOT make: it forces
// OverrideOn true (a fresh creation from the CRUD screen defaults on) and
// computes SourceOrder as max(source_order)+1 (a fresh creation goes last
// in the route table's tie-break order). C4 duty 6 requires a restore to
// carry BOTH verbatim from the snapshot instead — a row that was OFF must
// come back OFF, and a row's tie-break position must come back exactly
// where it was, not wherever "last" happens to land today — which is
// exactly why Repo.Create cannot be reused here.
//
// The ON CONFLICT SET clause follows the rule op_overrides' own upsertTx
// states for itself (overrides/repo.go:299-302): it names every column Row
// owns and nothing else. id, resource_id (P3's column; a restore has no
// business touching it) and created_at (a conflict means the row already
// existed — its original creation time is not this restore's to overwrite)
// all stay OUT of the SET clause. workspace_id, method and path are the
// conflict target itself and so are never re-assigned by it either.
// updated_at is stamped with now. On a genuine INSERT (no existing row),
// the VALUES clause still supplies created_at = now — this restore has no
// original creation timestamp to fall back to, since bundle.EndpointEntry
// never carries one (C3) — and that value is simply never touched again by
// any later conflict, exactly as a freshly Create()d row's created_at is
// never touched again either.
func upsertTx(ctx context.Context, tx *sql.Tx, row *Row, now time.Time) error {
	responsesJSON, err := marshalResponses(row.Responses)
	if err != nil {
		return fmt.Errorf("marshal responses for %s %s: %w", row.Method, row.Path, err)
	}
	listSizeJSON, err := marshalListSize(row.ListSize)
	if err != nil {
		return fmt.Errorf("marshal list_size for %s %s: %w", row.Method, row.Path, err)
	}
	delayMsJSON, err := marshalDelayMs(row.DelayMs)
	if err != nil {
		return fmt.Errorf("marshal delay_ms for %s %s: %w", row.Method, row.Path, err)
	}

	var validateReq any
	if row.ValidateReq != nil {
		validateReq = boolToInt(*row.ValidateReq)
	}
	// Same reasoning as insertTx above: ReqSchema and FailDirective are
	// copied as plain strings, never re-run through encoding/json.
	var reqSchema any
	if len(row.ReqSchema) > 0 {
		reqSchema = string(row.ReqSchema)
	}
	var failDirective any
	if len(row.FailDirective) > 0 {
		failDirective = string(row.FailDirective)
	}

	streamJSON, err := marshalStream(row.Stream)
	if err != nil {
		return fmt.Errorf("marshal stream for %s %s: %w", row.Method, row.Path, err)
	}
	operationJSON, err := marshalOperation(row.Operation)
	if err != nil {
		return fmt.Errorf("marshal operation for %s %s: %w", row.Method, row.Path, err)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO custom_endpoints
			(workspace_id, method, path, canonical_path, source_order, override_on, route_off,
			 active_status, responses, req_schema, list_size, delay_ms, fail_directive, validate_req,
			 created_at, updated_at, edit_version, kind, stream, operation)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, method, path) DO UPDATE SET
			canonical_path = excluded.canonical_path,
			source_order   = excluded.source_order,
			override_on    = excluded.override_on,
			route_off      = excluded.route_off,
			active_status  = excluded.active_status,
			responses      = excluded.responses,
			req_schema     = excluded.req_schema,
			list_size      = excluded.list_size,
			delay_ms       = excluded.delay_ms,
			fail_directive = excluded.fail_directive,
			validate_req   = excluded.validate_req,
			updated_at     = excluded.updated_at,
			edit_version   = excluded.edit_version,
			kind           = excluded.kind,
			stream         = excluded.stream,
			operation      = excluded.operation`,
		row.WorkspaceID, row.Method, row.Path, row.CanonicalPath, row.SourceOrder,
		boolToInt(row.OverrideOn), boolToInt(row.RouteOff), row.ActiveStatus,
		responsesJSON, reqSchema, listSizeJSON, delayMsJSON, failDirective, validateReq,
		now.Unix(), now.Unix(), row.EditVersion, kindOrHTTP(row.Kind), streamJSON, operationJSON,
	); err != nil {
		return fmt.Errorf("upsert endpoint %s %s: %w", row.Method, row.Path, err)
	}
	return nil
}

// updateTx writes row's full column set via UPDATE ... WHERE workspace_id =
// ? AND id = ? — [Repo.Update]'s own writer, keyed on the row's identity
// rather than the natural key upsertTx conflicts on (see that method's doc
// comment for why the two cannot be swapped). created_at is deliberately
// absent from the SET list: an edit is not a re-creation, so the row's
// original creation time is not this verb's to overwrite — same reasoning
// as upsertTx's own ON CONFLICT SET clause never touching it either.
//
// Zero RowsAffected reports ErrNotFound rather than silently succeeding —
// unreachable in the one caller ([Repo.Update], which already reloaded the
// row inside the SAME transaction moments earlier), kept only so this
// function is not silently wrong for some future second caller that skips
// that reload.
func updateTx(ctx context.Context, tx *sql.Tx, row *Row, now time.Time) error {
	responsesJSON, err := marshalResponses(row.Responses)
	if err != nil {
		return fmt.Errorf("marshal responses for %s %s: %w", row.Method, row.Path, err)
	}
	listSizeJSON, err := marshalListSize(row.ListSize)
	if err != nil {
		return fmt.Errorf("marshal list_size for %s %s: %w", row.Method, row.Path, err)
	}
	delayMsJSON, err := marshalDelayMs(row.DelayMs)
	if err != nil {
		return fmt.Errorf("marshal delay_ms for %s %s: %w", row.Method, row.Path, err)
	}

	var validateReq any
	if row.ValidateReq != nil {
		validateReq = boolToInt(*row.ValidateReq)
	}
	// Same reasoning as insertTx/upsertTx above: ReqSchema and FailDirective
	// are copied as plain strings, never re-run through encoding/json.
	var reqSchema any
	if len(row.ReqSchema) > 0 {
		reqSchema = string(row.ReqSchema)
	}
	var failDirective any
	if len(row.FailDirective) > 0 {
		failDirective = string(row.FailDirective)
	}

	streamJSON, err := marshalStream(row.Stream)
	if err != nil {
		return fmt.Errorf("marshal stream for %s %s: %w", row.Method, row.Path, err)
	}
	operationJSON, err := marshalOperation(row.Operation)
	if err != nil {
		return fmt.Errorf("marshal operation for %s %s: %w", row.Method, row.Path, err)
	}

	res, err := tx.ExecContext(ctx, `
		UPDATE custom_endpoints SET
			method = ?, path = ?, canonical_path = ?, source_order = ?,
			override_on = ?, route_off = ?, active_status = ?, responses = ?,
			req_schema = ?, list_size = ?, delay_ms = ?, fail_directive = ?,
			validate_req = ?, updated_at = ?, edit_version = ?, kind = ?, stream = ?, operation = ?
		WHERE workspace_id = ? AND id = ?`,
		row.Method, row.Path, row.CanonicalPath, row.SourceOrder,
		boolToInt(row.OverrideOn), boolToInt(row.RouteOff), row.ActiveStatus,
		responsesJSON, reqSchema, listSizeJSON, delayMsJSON, failDirective, validateReq,
		now.Unix(), row.EditVersion, kindOrHTTP(row.Kind), streamJSON, operationJSON,
		row.WorkspaceID, row.ID,
	)
	if err != nil {
		return err
	}
	n, raErr := res.RowsAffected()
	if raErr != nil {
		return fmt.Errorf("update endpoint %d: rows affected: %w", row.ID, raErr)
	}
	if n == 0 {
		return fmt.Errorf("update endpoint %d: %w", row.ID, ErrNotFound)
	}
	return nil
}

// marshalOperation encodes the operation document for the column, NULL
// for a nil one (P7a D3: an absent document is "no operation fields", and
// the column has no CHECK to pair it with).
func marshalOperation(op *Operation) (sql.NullString, error) {
	if op == nil {
		return sql.NullString{}, nil
	}
	b, err := jsonx.Marshal(op)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func marshalResponses(m map[string]overrides.Variant) (string, error) {
	if len(m) == 0 {
		// The column's own DEFAULT is '{}', not the JSON literal "null" that
		// jsonx.Marshal(nil map) would produce — matching it here means a row
		// nobody bound a response to yet decodes back to an empty (not nil)
		// map too, same as op_overrides.
		return "{}", nil
	}
	b, err := jsonx.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// marshalStream encodes a stream document for the column, NULL for a nil
// one — the CHECK constraint pairs NULL with kind 'http' and NOT NULL with
// 'sse', so kindOrHTTP and this function must agree, and they do because
// validateKind refused every other pairing before the write reached here.
func marshalStream(s *Stream) (sql.NullString, error) {
	if s == nil {
		return sql.NullString{}, nil
	}
	b, err := jsonx.Marshal(s)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

// kindOrHTTP is the column's own DEFAULT applied on the Go side for a Row
// whose Kind was never set (a restore of a pre-P6b snapshot row builds one).
func kindOrHTTP(kind string) string {
	if kind == "" {
		return KindHTTP
	}
	return kind
}

func marshalListSize(ls *overrides.ListSize) (sql.NullString, error) {
	if ls == nil {
		return sql.NullString{}, nil
	}
	b, err := jsonx.Marshal(ls)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func marshalDelayMs(d *int) (sql.NullString, error) {
	if d == nil {
		return sql.NullString{}, nil
	}
	b, err := jsonx.Marshal(*d)
	if err != nil {
		return sql.NullString{}, err
	}
	return sql.NullString{String: string(b), Valid: true}, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure.
// Copied from internal/workspaces/repo.go's helper of the same name:
// modernc.org/sqlite reports these as a plain error whose message contains
// "UNIQUE constraint failed" — matched by substring so this package does not
// need to import the driver just to compare an error code.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// --- scanning ----------------------------------------------------------------

const selectRow = `
	SELECT id, workspace_id, method, path, canonical_path, source_order, override_on, route_off,
	       active_status, responses, req_schema, list_size, delay_ms, fail_directive, validate_req,
	       created_at, updated_at, edit_version, kind, stream, operation
	FROM custom_endpoints`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scan logic is
// written once — mirrors internal/overrides/repo.go's identical helper.
type rowScanner interface {
	Scan(dest ...any) error
}

// scan decodes one custom_endpoints row and validates its Responses exactly
// as normalizeAndValidate would a freshly-written one. Like op_overrides'
// own scan (repo.go, same package family), this is the DECODE path HARD
// RULE 4 and the recipes/base64/status-key validation exist for: a row that
// got into the table some other way must fail here as a returned error,
// never as a panic partway through building the P2 route table.
func scan(row rowScanner) (*Row, error) {
	var (
		r             Row
		sourceOrder   int64
		overrideOn    int64
		routeOff      int64
		activeStatus  int64
		responsesJSON string
		reqSchema     sql.NullString
		listSizeJSON  sql.NullString
		delayMsJSON   sql.NullString
		failDirective sql.NullString
		validateReq   sql.NullInt64
		createdAt     int64
		updatedAt     int64
		streamJSON    sql.NullString
		operationJSON sql.NullString
	)
	if err := row.Scan(
		&r.ID, &r.WorkspaceID, &r.Method, &r.Path, &r.CanonicalPath, &sourceOrder, &overrideOn, &routeOff,
		&activeStatus, &responsesJSON, &reqSchema, &listSizeJSON, &delayMsJSON, &failDirective, &validateReq,
		&createdAt, &updatedAt, &r.EditVersion, &r.Kind, &streamJSON, &operationJSON,
	); err != nil {
		return nil, err
	}
	if streamJSON.Valid {
		var st Stream
		if err := jsonx.Unmarshal([]byte(streamJSON.String), &st); err != nil {
			return nil, fmt.Errorf("endpoint %d: decode stream: %w", r.ID, err)
		}
		r.Stream = &st
	}
	if operationJSON.Valid {
		var op Operation
		if err := jsonx.Unmarshal([]byte(operationJSON.String), &op); err != nil {
			return nil, fmt.Errorf("endpoint %d: decode operation: %w", r.ID, err)
		}
		r.Operation = &op
	}

	r.SourceOrder = sourceOrder
	r.OverrideOn = overrideOn != 0
	r.RouteOff = routeOff != 0
	r.ActiveStatus = int(activeStatus)

	if err := jsonx.Unmarshal([]byte(responsesJSON), &r.Responses); err != nil {
		return nil, fmt.Errorf("endpoint %d: decode responses: %w", r.ID, err)
	}
	if err := overrides.ValidateResponses(r.Responses); err != nil {
		return nil, fmt.Errorf("endpoint %d: %w", r.ID, err)
	}

	if reqSchema.Valid {
		r.ReqSchema = jsonx.RawMessage(reqSchema.String)
	}
	if listSizeJSON.Valid {
		var ls overrides.ListSize
		if err := jsonx.Unmarshal([]byte(listSizeJSON.String), &ls); err != nil {
			return nil, fmt.Errorf("endpoint %d: decode list_size: %w", r.ID, err)
		}
		r.ListSize = &ls
	}
	if delayMsJSON.Valid {
		var d int
		if err := jsonx.Unmarshal([]byte(delayMsJSON.String), &d); err != nil {
			return nil, fmt.Errorf("endpoint %d: decode delay_ms: %w", r.ID, err)
		}
		r.DelayMs = &d
	}
	if failDirective.Valid {
		r.FailDirective = jsonx.RawMessage(failDirective.String)
	}
	if validateReq.Valid {
		v := validateReq.Int64 != 0
		r.ValidateReq = &v
	}
	r.CreatedAt = time.Unix(createdAt, 0).UTC()
	r.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &r, nil
}

func scanOne(row *sql.Row) (*Row, error) {
	r, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan endpoint: %w", err)
	}
	return r, nil
}
