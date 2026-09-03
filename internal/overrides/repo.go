package overrides

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/store"
)

// Repo is the op_overrides table's data-access layer.
type Repo struct {
	db *store.DB
}

// NewRepo builds a Repo over db.
func NewRepo(db *store.DB) *Repo {
	return &Repo{db: db}
}

// ForWorkspace returns every override of one workspace in ONE query, keyed
// by OpKey(method, path). The runtime build calls this once per (workspace,
// revision); a query per operation on that path is the N+1 DESIGN §18
// forbids.
func (r *Repo) ForWorkspace(ctx context.Context, workspaceID int64) (map[string]*Row, error) {
	rows, err := r.db.R.QueryContext(ctx, selectRow+" WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list overrides for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make(map[string]*Row)
	for rows.Next() {
		row, serr := scan(rows)
		if serr != nil {
			return nil, fmt.Errorf("overrides for workspace %d: %w", workspaceID, serr)
		}
		out[OpKey(row.Method, row.Path)] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overrides for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// Get looks up one override by its OpKey.
func (r *Repo) Get(ctx context.Context, workspaceID int64, key string) (*Row, error) {
	method, path, err := ParseOpKey(key)
	if err != nil {
		return nil, fmt.Errorf("get override: %w", err)
	}
	return scanOne(r.db.R.QueryRowContext(ctx, selectRow+" WHERE workspace_id = ? AND method = ? AND path = ?",
		workspaceID, method, path))
}

// Put upserts one operation's override and bumps workspaces.revision IN THE
// SAME TRANSACTION, returning the new revision. mutate receives the
// existing row, or a zero row with Method/Path already filled when there is
// none.
//
// NEVER "INSERT OR REPLACE": op_overrides carries a resource_id column that
// Row deliberately does not model (it is P3's), and REPLACE deletes the row
// and inserts a new one, nulling it on every edit. This uses an explicit
// INSERT ... ON CONFLICT (workspace_id, method, path) DO UPDATE SET <the
// columns Row owns> — resource_id (and id) are never named in that SET
// clause, so an edit through this path cannot clobber P3's column.
func (r *Repo) Put(ctx context.Context, workspaceID int64, key string, mutate func(*Row) error) (*Row, int64, error) {
	return r.PutExpecting(ctx, workspaceID, key, nil, mutate)
}

// PutExpecting is Put's sibling with a per-row compare-and-swap (A3, D7/D8):
// expect is "no expectation" when nil (Put's own delegation, and
// internal/admin/from_traffic.go's conversion path, the ONE production
// caller this slice deliberately leaves unguarded), and otherwise the
// edit_version the caller last read for this (workspace, method, path).
//
// The five cases, checked against the row read INSIDE this transaction,
// before mutate runs:
//   - expect == nil: no check at all, proceeds exactly as Put always has.
//   - *expect == 0, no row present: proceeds -- this IS the first PUT of an
//     operation nobody has overridden yet, so "0" has to mean "I expect no
//     row" rather than being refused outright (D7 -- op_overrides is the one
//     table in this population where 0 is a legal expectation).
//   - *expect == 0, row present: store.EditConflictError{Current: current}.
//   - *expect == current.EditVersion (row present): proceeds.
//   - *expect != current.EditVersion (row present): store.EditConflictError
//     {Current: current}.
//   - *expect != 0, no row present: store.EditConflictError{Gone: true} --
//     the target row was deleted under the caller; D7's qualifier is what
//     makes this a conflict rather than the workspace-not-found or
//     cross-tenant 404s this function already raises for OTHER reasons.
//
// Every case that proceeds allocates a fresh edit_version from the
// workspace's sequence (store.AllocateEditVersion) and stamps it on the row
// this writes -- never expect+1 in place, because the row this call is about
// to touch may be a brand-new INSERT (D4/D9).
func (r *Repo) PutExpecting(ctx context.Context, workspaceID int64, key string, expect *int64, mutate func(*Row) error) (*Row, int64, error) {
	method, path, err := ParseOpKey(key)
	if err != nil {
		return nil, 0, fmt.Errorf("put override: %w", err)
	}

	var (
		result   *Row
		revision int64
	)
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		wsRev, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("put override for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		current, gerr := getTx(ctx, tx, workspaceID, method, path)
		var existed bool
		switch {
		case gerr == nil:
			existed = true
		case errors.Is(gerr, ErrNotFound):
			current = &Row{
				WorkspaceID: workspaceID,
				Method:      method,
				Path:        path,
				OverrideOn:  true,
				Responses:   map[string]Variant{},
			}
		default:
			return gerr
		}

		if expect != nil {
			if cerr := checkExpectedVersion(*expect, existed, current); cerr != nil {
				return cerr
			}
		}

		if merr := mutate(current); merr != nil {
			return fmt.Errorf("mutate override %s: %w", key, merr)
		}

		// The (workspace, method, path) identity comes from key, never from
		// whatever mutate leaves behind: a handler bug in mutate must not be
		// able to silently retarget the upsert at a different operation than
		// the one the caller asked to edit.
		current.WorkspaceID = workspaceID
		current.Method = method
		current.Path = path

		if verr := normalizeAndValidate(current); verr != nil {
			return verr
		}

		now := time.Now().UTC()
		newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		current.EditVersion = newVersion
		if uerr := upsertTx(ctx, tx, current, now); uerr != nil {
			return uerr
		}
		stored, serr := getTx(ctx, tx, workspaceID, method, path)
		if serr != nil {
			return fmt.Errorf("reload override after put: %w", serr)
		}

		newRev := wsRev + 1
		if berr := bumpRevisionTx(ctx, tx, workspaceID, now); berr != nil {
			return berr
		}

		result = stored
		revision = newRev
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return result, revision, nil
}

// checkExpectedVersion implements the five-case compare-and-swap rule (D7)
// against one row read inside the same transaction that is about to write
// it. existed distinguishes "no row" from "row present at version 0", which
// current.EditVersion alone cannot: current is a freshly-built zero row when
// !existed, and its EditVersion is Go's zero value, indistinguishable from a
// stored row's actual 0 without the separate existed flag.
func checkExpectedVersion(expect int64, existed bool, current *Row) error {
	switch {
	case !existed && expect == 0:
		return nil
	case !existed:
		return &store.EditConflictError{Gone: true}
	case expect == 0:
		return &store.EditConflictError{Current: *current}
	case current.EditVersion == expect:
		return nil
	default:
		return &store.EditConflictError{Current: *current}
	}
}

// PutMany is the auth preset's path: many rows, one transaction, ONE
// revision bump. Applying a 40-binding preset through Put would bump the
// revision 40 times and rebuild the runtime 40 times.
//
// UNLIKE Put, this does NOT read-then-mutate: each Row in rows is written
// to storage EXACTLY as given, via the same upsertTx ON CONFLICT DO UPDATE
// that Put uses, but with no getTx first to seed it from whatever is
// already stored for that (workspace, method, path). A Row that leaves
// DelayMs/ActiveStatus/ValidateReq/RouteOff/FailDirective/Responses at
// their zero values WIPES whatever an earlier Put wrote there — round-1
// finding #2, verified: Put a row with DelayMs/ActiveStatus/ValidateReq/
// FailDirective set, PutMany a bare Row for the same key, Get() — all four
// come back nil/empty. This is not a bug to route around by reading again
// in here (a per-row getTx inside this loop would make PutMany's own
// contract ambiguous about what "the caller didn't set this field" MEANS —
// "leave it alone" or "clear it" — a distinction a plain []*Row has no way
// to express, unlike Put's mutate callback, which receives the existing row
// directly and the caller decides). The caller owns the merge: read
// ForWorkspace/Get first and build each Row from what is already there,
// exactly as admin/preset_handlers.go's handleApplyAuthPreset does before
// its own PutMany call — a future caller that skips that step gets a
// silent wholesale replace, not a merge.
func (r *Repo) PutMany(ctx context.Context, workspaceID int64, rows []*Row) (int64, error) {
	_, revision, err := r.PutManyExpecting(ctx, workspaceID, nil, func(map[string]*Row) ([]*Row, error) {
		return rows, nil
	})
	return revision, err
}

// PutManyExpecting is PutMany's sibling with a per-row compare-and-swap over
// the WHOLE call (A3, D7/D8/D12) -- the auth preset's path, and the one
// set-valued check in this slice.
//
// expect is nil for "no expectation at all" (PutMany's own delegation): the
// call proceeds exactly as PutMany always has, no per-row check, and no
// caller anywhere on a wire can send nil -- that value exists only here, at
// the repository verb. expect is a non-nil, possibly EMPTY, opKey-keyed map
// otherwise: an empty map is "I expect these zero opKeys" and, under the
// five-case rule below, refuses any row this call would have to CREATE
// against a live one, because "0, no row" is the only case in the table
// that lets a key with no stated expectation proceed -- and an empty map
// states none.
//
// The workspace's current rows are read INSIDE this transaction, in full,
// before merge runs or anything is written -- see forWorkspaceTx's own
// comment for why the drain has to be complete before this same
// transaction issues a write. Every key expect names is checked against
// that read with the identical five cases PutExpecting applies to one row;
// on ANY mismatch the whole call is refused with one store.EditConflictError
// whose Current is a map[string]*int64 of ONLY the mismatching opKeys (nil
// value where the row is gone) -- never the first mismatch alone, and never
// a bare Gone:true for the call, because this is D12's set-valued exception
// to D6's single-row payload shape: nothing else can tell the caller which
// of many rows it lost.
//
// merge receives that same current map and returns the rows to write,
// exactly the merge admin/preset_handlers.go's apply handler already does --
// this package still never performs the merge itself (PutMany's own doc
// comment above is the reason: a merge that moved in here would contradict
// "the caller owns the merge").
//
// Every row merge returns is written at a FRESHLY ALLOCATED edit_version --
// this is the guarded route's own write, so D9's criterion always allocates
// here regardless of whether that row's key appeared in expect. The new
// versions are returned keyed by opKey, beside the revision, because the
// preset's own write response carries no rows and has no other source for
// them (D5/D8).
func (r *Repo) PutManyExpecting(ctx context.Context, workspaceID int64, expect map[string]int64,
	merge func(current map[string]*Row) ([]*Row, error),
) (map[string]int64, int64, error) {
	var (
		newVersions map[string]int64
		revision    int64
	)
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		wsRev, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("put overrides for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		current, rerr := forWorkspaceTx(ctx, tx, workspaceID)
		if rerr != nil {
			return rerr
		}

		if expect != nil {
			if cerr := checkManyExpectedVersions(expect, current); cerr != nil {
				return cerr
			}
		}

		rows, merr := merge(current)
		if merr != nil {
			return fmt.Errorf("merge overrides for workspace %d: %w", workspaceID, merr)
		}

		now := time.Now().UTC()
		newVersions = make(map[string]int64, len(rows))
		for _, row := range rows {
			if row == nil {
				return fmt.Errorf("%w: nil row", ErrInvalidRow)
			}
			row.WorkspaceID = workspaceID
			if verr := normalizeAndValidate(row); verr != nil {
				return verr
			}
			newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
			if aerr != nil {
				return aerr
			}
			row.EditVersion = newVersion
			if uerr := upsertTx(ctx, tx, row, now); uerr != nil {
				return uerr
			}
			newVersions[OpKey(row.Method, row.Path)] = newVersion
		}

		newRev := wsRev + 1
		if berr := bumpRevisionTx(ctx, tx, workspaceID, now); berr != nil {
			return berr
		}
		revision = newRev
		return nil
	})
	if err != nil {
		return nil, 0, err
	}
	return newVersions, revision, nil
}

// checkManyExpectedVersions applies the same five-case rule
// checkExpectedVersion does, once per opKey in expect, against current --
// the workspace's rows as read inside this same transaction (D12). It
// refuses the WHOLE call on any single mismatch, collecting every
// mismatching key into one map so the caller can retry with all of them
// rather than one at a time.
func checkManyExpectedVersions(expect map[string]int64, current map[string]*Row) error {
	var stale map[string]*int64
	for key, exp := range expect {
		row, existed := current[key]
		switch {
		case !existed && exp == 0:
			continue
		case !existed:
			if stale == nil {
				stale = make(map[string]*int64)
			}
			stale[key] = nil
		case exp == 0:
			if stale == nil {
				stale = make(map[string]*int64)
			}
			v := row.EditVersion
			stale[key] = &v
		case row.EditVersion == exp:
			continue
		default:
			if stale == nil {
				stale = make(map[string]*int64)
			}
			v := row.EditVersion
			stale[key] = &v
		}
	}
	if stale == nil {
		return nil
	}
	return &store.EditConflictError{Current: stale}
}

// ReplaceAllTx restores workspaceID's ENTIRE op_overrides table to exactly
// rows, inside the CALLER's transaction — internal/checkpoints' rollback and
// reset-overrides, which open the one db.Write for a whole multi-table
// restore and call this as one of the tx-scoped writes inside it (HARD RULE
// 5: this function must never open its own db.Write, or it would deadlock
// the single-connection writer pool the caller is already holding).
//
// It deliberately does NOT bump workspaces.revision (the caller bumps once
// for the whole restore — a manual checkpoint does not bump AT ALL, C12) and
// does NOT return a changed-count (a gate round added one for the no-op
// rule and it created an ordering contradiction: the count is only known
// after the apply, while reset-overrides' no-op decision has to be made
// from a read taken BEFORE the transaction opens — C4's closing paragraph).
//
// C4 lists six duties this function owes; the six are numbered in the
// comments below in the order they run.
func ReplaceAllTx(ctx context.Context, tx *sql.Tx, workspaceID int64, rows []*Row, now time.Time) error {
	// Duty 1: upper-case every row's Method FIRST, in a pass of ITS OWN,
	// before the delete set below is computed from it. This is deliberately
	// NOT a call to normalizeAndValidate — that function also validates,
	// and running full validation here, before any row is written, would
	// break duty 4 (validate-then-write ONE ROW AT A TIME, not validate-all
	// then write-all). upperASCII is this package's own existing unexported
	// helper (overrides.go:290); nothing new is exported for this pass.
	//
	// Why the pass exists at all: a snapshot row that reads "get /a" and a
	// live row stored as "GET /a" are the SAME operation, but the delete
	// query below matches on the (method, path) natural key BY VALUE. If it
	// ran before this pass, "get" and "GET" would look like two different
	// keys — the delete would remove the live "GET /a" row (its key is
	// absent from a rows set that still says "get"), and the upsert loop
	// would then INSERT "get /a" fresh, under a NEW id. That is precisely
	// what C1's delete-then-upsert order (see below) exists to avoid, and
	// no other check in a live run can see it happen: every row that ever
	// reached this table through the admin API was already normalized on
	// the way in (Put/PutMany both call normalizeAndValidate before
	// writing), so only a restored snapshot can carry a lower-case method.
	// §G obs 16(b) drives this directly, against ReplaceAllTx, because nothing
	// reachable through the admin API can.
	for _, row := range rows {
		if row == nil {
			return fmt.Errorf("%w: nil row", ErrInvalidRow)
		}
		row.Method = upperASCII(row.Method)
	}

	// Duty 3 (C1's order): DELETE every row whose (method, path) key is
	// absent from rows, THEN upsert what rows DOES hold — never the
	// reverse. op_overrides carries only the one natural-key index
	// (UNIQUE (workspace_id, method, path), 0001_init.sql:188), so this
	// package cannot itself demonstrate the UNIQUE-index collision C1 names
	// (that is customep's canonical_path index, and §G obs 16(a) drives it
	// there) — but the SAME order is followed here anyway, both because C1
	// states it as the restore's rule generally, not as a customep-specific
	// workaround, and so a caller composing both packages' ReplaceAllTx
	// calls sees one contract, not two.
	if err := deleteAbsentTx(ctx, tx, workspaceID, rows); err != nil {
		return err
	}

	// Duty 4: validate, THEN write, one row at a time — not a validate-all
	// pass followed by a write-all pass. §G obs 15's fault injection fails a
	// restore on its SECOND row (a row whose shape check passes bundle
	// decode but fails normalizeAndValidate here) and asserts the WHOLE
	// transaction — this write and the checkpoint insert alongside it —
	// rolls back with nothing committed. That only distinguishes "atomic"
	// from "already half-applied inside one still-open transaction" if the
	// first row can actually reach the table before the second one fails,
	// which requires interleaving validate-and-write per row exactly as
	// PutMany already does (repo.go:180-191) rather than validating the
	// whole slice up front.
	for _, row := range rows {
		// Duty 2: set WorkspaceID on every row ourselves. A row decoded from
		// a checkpoint/scenario BLOB carries none — bundle.EndpointEntry's
		// sibling OverrideEntry never stores one (C3) — so trusting whatever
		// is already on the Go value would silently write against whatever
		// workspace (or none) happened to be there. PutMany does the exact
		// same assignment at the exact same point in its own loop
		// (repo.go:184); this mirrors that shape on purpose.
		row.WorkspaceID = workspaceID
		if verr := normalizeAndValidate(row); verr != nil {
			return verr
		}
		// A3/D9: this upsert is INSERT ... ON CONFLICT DO UPDATE, so a row
		// that already lives here (restore over a live table, the dominant
		// case) would otherwise SURVIVE with its pre-restore edit_version --
		// a stale token matching a row whose content this statement just
		// replaced. Allocating fresh here, unconditionally, is what makes
		// both the INSERT and the UPDATE branch of the same statement safe:
		// a row absent live lands at a value never used before either way.
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

// Delete removes one override and bumps the revision. Deleting a row that
// is not there is not an error: the caller wanted no override, and there is
// none — and since nothing changed, the revision is left exactly where it
// was rather than bumped for an edit that had no effect.
func (r *Repo) Delete(ctx context.Context, workspaceID int64, key string) (revision int64, deleted bool, err error) {
	method, path, err := ParseOpKey(key)
	if err != nil {
		return 0, false, fmt.Errorf("delete override: %w", err)
	}

	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		wsRev, exists, werr := workspaceRevisionTx(ctx, tx, workspaceID)
		if werr != nil {
			return werr
		}
		if !exists {
			return fmt.Errorf("delete override for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		}

		res, derr := tx.ExecContext(ctx,
			"DELETE FROM op_overrides WHERE workspace_id = ? AND method = ? AND path = ?",
			workspaceID, method, path,
		)
		if derr != nil {
			return fmt.Errorf("delete override %s: %w", key, derr)
		}
		n, raErr := res.RowsAffected()
		if raErr != nil {
			return fmt.Errorf("delete override %s: rows affected: %w", key, raErr)
		}
		if n == 0 {
			// deleted stays false: the caller must not infer "nothing was
			// deleted" from revision == the one it read earlier, because
			// an anonymous POST {prefix}/state bumps revision too.
			revision = wsRev
			return nil
		}

		now := time.Now().UTC()
		newRev := wsRev + 1
		if berr := bumpRevisionTx(ctx, tx, workspaceID, now); berr != nil {
			return berr
		}
		revision = newRev
		deleted = true
		return nil
	})
	if err != nil {
		return 0, false, err
	}
	return revision, deleted, nil
}

// --- transaction-scoped helpers ---------------------------------------------

// workspaceRevisionTx reads the target workspace's current revision inside
// tx. Reading through tx rather than r.db.R matters here: Put/PutMany/Delete
// need the revision as it stands INSIDE this transaction, immediately
// before they bump it, not as of whatever the last committed read saw.
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

// bumpRevisionTx is HARD RULE 5's direct UPDATE, verbatim: never
// workspaces.Repo.Update, which opens its own write transaction and would
// deadlock the single-connection writer pool when called from inside the
// db.Write callback this runs in. The increment happens IN the UPDATE
// (revision = revision + 1) rather than as a value computed in Go and sent
// down — with BEGIN IMMEDIATE on a one-connection writer pool the two are
// equivalent (nothing else can be mid-transaction against this row when this
// runs), but writing it this way keeps this function correct even if that
// invariant ever changes upstream, instead of depending on it silently.
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// deleteAbsentTx deletes every op_overrides row for workspaceID whose
// (method, path) natural key is not present in rows — ReplaceAllTx's duty 3,
// C1's DELETE half, run before that function upserts what rows DOES hold.
//
// Built as ONE dynamic "NOT IN" statement over row values rather than a
// read-existing-then-diff-in-Go loop: a *sql.Tx pins a single connection, so
// a SELECT left half-drained while this same transaction then issues DELETEs
// is exactly the kind of thing that is easy to get subtly wrong (and every
// other read in this package already goes through the reader pool, never
// through tx, for unrelated reasons — see workspaceRevisionTx's comment).
// One statement avoids the question entirely, and a workspace's override
// count is bounded by its spec's operation count (the real customer spec
// has 130), so this is nowhere near a query-planner concern.
func deleteAbsentTx(ctx context.Context, tx *sql.Tx, workspaceID int64, rows []*Row) error {
	if len(rows) == 0 {
		// An empty snapshot keeps NOTHING: every row currently stored for
		// this workspace is, by definition, absent from it. "NOT IN ()" has
		// no SQL syntax (an empty list is not a value), so the empty case is
		// its own unconditional DELETE rather than a zero-length NOT IN.
		if _, err := tx.ExecContext(ctx, "DELETE FROM op_overrides WHERE workspace_id = ?", workspaceID); err != nil {
			return fmt.Errorf("delete all overrides for workspace %d: %w", workspaceID, err)
		}
		return nil
	}

	// SQLite has supported row-value IN/NOT IN ((a, b) NOT IN ((?,?), ...))
	// since 3.15 (2016), so this reads directly as "the pair (method, path)
	// is not one of these pairs" rather than reconstructing OpKey's
	// percent-encoding just to compare strings.
	var sb strings.Builder
	sb.WriteString("DELETE FROM op_overrides WHERE workspace_id = ? AND (method, path) NOT IN (")
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
		return fmt.Errorf("delete stale overrides for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// forWorkspaceTx reads every op_overrides row of workspaceID inside tx, keyed
// by OpKey -- PutManyExpecting's compare-and-swap read (A3, D8/D12).
//
// Reading through tx rather than r.db.R is the exception this package makes
// only where the transaction's OWN view is the point (workspaceRevisionTx's
// comment states that rule; a compare-and-swap read is that class by
// definition -- it must see this transaction's own uncommitted state, and
// nothing a concurrent writer might commit after it started).
//
// The rows.Close()'d loop below runs to completion, draining rows fully,
// BEFORE this transaction issues a single write. deleteAbsentTx's comment
// names the hazard this avoids: a *sql.Tx pins one connection, so a SELECT
// left half-drained while the same transaction then writes is a subtle bug
// waiting to happen. Nothing here writes until this function has returned.
func forWorkspaceTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (map[string]*Row, error) {
	rows, err := tx.QueryContext(ctx, selectRow+" WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list overrides for workspace %d: %w", workspaceID, err)
	}
	// A single deferred Close (sqlclosecheck), not one per exit path: the
	// drain-before-return property deleteAbsentTx's comment cares about is
	// unaffected either way, because this function does not return to its
	// (writing) caller until the loop below and rows.Err() have both
	// finished — the same idiom as every other multi-row read in this
	// package and its siblings (getTx's neighbours, customep/repo.go:42,
	// checkpoints/repo.go:90).
	defer func() { _ = rows.Close() }()

	out := make(map[string]*Row)
	for rows.Next() {
		row, serr := scan(rows)
		if serr != nil {
			return nil, fmt.Errorf("overrides for workspace %d: %w", workspaceID, serr)
		}
		out[OpKey(row.Method, row.Path)] = row
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate overrides for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

func getTx(ctx context.Context, tx *sql.Tx, workspaceID int64, method, path string) (*Row, error) {
	row := tx.QueryRowContext(ctx, selectRow+" WHERE workspace_id = ? AND method = ? AND path = ?",
		workspaceID, method, path)
	return scanOne(row)
}

// upsertTx writes row's columns and stamps row.UpdatedAt = now. The
// ON CONFLICT SET clause names every column Row owns and nothing else —
// leaving resource_id (and id) out of it is what keeps this from clobbering
// a column P3 owns.
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

	var operationID any
	if row.OperationID != nil {
		operationID = *row.OperationID
	}
	var activeStatus any
	if row.ActiveStatus != nil {
		activeStatus = *row.ActiveStatus
	}
	var validateReq any
	if row.ValidateReq != nil {
		validateReq = boolToInt(*row.ValidateReq)
	}
	// FailDirective is copied as a plain string, not re-run through
	// encoding/json — it is PRESERVED ONLY, and round-tripping it through a
	// jsonx.RawMessage Marshal/Unmarshal pair (as responses/listSize/delayMs
	// do) risks compacting whitespace this slice has no business touching.
	var failDirective any
	if len(row.FailDirective) > 0 {
		failDirective = string(row.FailDirective)
	}

	if _, err := tx.ExecContext(ctx, `
		INSERT INTO op_overrides
			(workspace_id, method, path, operation_id, override_on, route_off,
			 active_status, responses, list_size, delay_ms, fail_directive, validate_req, updated_at,
			 edit_version)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT (workspace_id, method, path) DO UPDATE SET
			operation_id   = excluded.operation_id,
			override_on    = excluded.override_on,
			route_off      = excluded.route_off,
			active_status  = excluded.active_status,
			responses      = excluded.responses,
			list_size      = excluded.list_size,
			delay_ms       = excluded.delay_ms,
			fail_directive = excluded.fail_directive,
			validate_req   = excluded.validate_req,
			updated_at     = excluded.updated_at,
			edit_version   = excluded.edit_version`,
		row.WorkspaceID, row.Method, row.Path, operationID, boolToInt(row.OverrideOn), boolToInt(row.RouteOff),
		activeStatus, responsesJSON, listSizeJSON, delayMsJSON, failDirective, validateReq, now.Unix(),
		row.EditVersion,
	); err != nil {
		return fmt.Errorf("upsert override %s %s: %w", row.Method, row.Path, err)
	}
	row.UpdatedAt = now
	return nil
}

func marshalResponses(m map[string]Variant) (string, error) {
	if len(m) == 0 {
		// The column's own DEFAULT is '{}', not the JSON literal "null" that
		// jsonx.Marshal(nil map) would produce — matching it here means a row
		// nobody has touched yet decodes back to an empty (not nil) map too.
		return "{}", nil
	}
	b, err := jsonx.Marshal(m)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func marshalListSize(ls *ListSize) (sql.NullString, error) {
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

// --- scanning ----------------------------------------------------------------

const selectRow = `
	SELECT id, workspace_id, method, path, operation_id, override_on, route_off,
	       active_status, responses, list_size, delay_ms, fail_directive, validate_req, updated_at,
	       edit_version
	FROM op_overrides`

// rowScanner is satisfied by both *sql.Row and *sql.Rows, so scan logic is
// written once (mirrors internal/workspaces' repo.go).
type rowScanner interface {
	Scan(dest ...any) error
}

// scan decodes one op_overrides row and validates it exactly as
// normalizeAndValidate would a freshly-mutated one. This is the "decode
// path" HARD RULE 4 and the recipes/base64/status-key validation exist for:
// the mock plane reaches this on every unauthenticated request, so a row
// that got into the table some other way (a hand run UPDATE, a future
// version writing a kind this build does not know) must fail as a returned
// error here, never as a panic three calls up the stack.
func scan(row rowScanner) (*Row, error) {
	var (
		r             Row
		operationID   sql.NullInt64
		overrideOn    int64
		routeOff      int64
		activeStatus  sql.NullInt64
		responsesJSON string
		listSizeJSON  sql.NullString
		delayMsJSON   sql.NullString
		failDirective sql.NullString
		validateReq   sql.NullInt64
		updatedAt     int64
	)
	if err := row.Scan(
		&r.ID, &r.WorkspaceID, &r.Method, &r.Path, &operationID, &overrideOn, &routeOff,
		&activeStatus, &responsesJSON, &listSizeJSON, &delayMsJSON, &failDirective, &validateReq, &updatedAt,
		&r.EditVersion,
	); err != nil {
		return nil, err
	}

	if operationID.Valid {
		v := operationID.Int64
		r.OperationID = &v
	}
	r.OverrideOn = overrideOn != 0
	r.RouteOff = routeOff != 0
	if activeStatus.Valid {
		v := int(activeStatus.Int64)
		r.ActiveStatus = &v
	}

	if err := jsonx.Unmarshal([]byte(responsesJSON), &r.Responses); err != nil {
		return nil, fmt.Errorf("override %d: decode responses: %w", r.ID, err)
	}
	if err := validateResponses(r.Responses); err != nil {
		return nil, fmt.Errorf("override %d: %w", r.ID, err)
	}

	if listSizeJSON.Valid {
		var ls ListSize
		if err := jsonx.Unmarshal([]byte(listSizeJSON.String), &ls); err != nil {
			return nil, fmt.Errorf("override %d: decode list_size: %w", r.ID, err)
		}
		r.ListSize = &ls
	}
	if delayMsJSON.Valid {
		var d int
		if err := jsonx.Unmarshal([]byte(delayMsJSON.String), &d); err != nil {
			return nil, fmt.Errorf("override %d: decode delay_ms: %w", r.ID, err)
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
	r.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return &r, nil
}

func scanOne(row *sql.Row) (*Row, error) {
	r, err := scan(row)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan override: %w", err)
	}
	return r, nil
}
