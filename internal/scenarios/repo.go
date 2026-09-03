// Package scenarios owns the scenarios table (0001_init.sql:120, already
// created by migration 0001 — this package writes no migration of its
// own) and the ONE column on workspaces that names which scenario, if any,
// is active (workspaces.scenario_id, 0001_init.sql:106).
//
// A scenario is a named [bundle.Bundle] snapshot saved from a workspace's
// current state (DESIGN §4, §12): activating one is a LAYER composed at
// request time over the workspace's own rows, never a restore written back
// over them. That composition — the pure function that overlays a
// decoded snapshot's overrides onto the workspace's own — is
// internal/mockplane's job, not this package's: this package only ever
// stores, retrieves and (de)activates a snapshot; it does not know what a
// "runtime" is and does not import internal/mockplane (importing it would
// be backwards — mockplane depends on this package, via
// [mockplane.ScenarioSource], not the other way around).
//
// Like internal/overrides and internal/customep beside it, this package
// never imports internal/workspaces: HARD RULE 5 (documented in
// internal/customep/repo.go and internal/overrides/overrides.go) is that
// workspaces.Repo.Update opens its OWN write transaction, and calling it
// from inside another db.Write callback deadlocks the single-connection
// writer pool. Every place this package needs the workspaces row — a
// coherent read for CreateFromCurrentState (A11), or the scenario_id
// column [SetActive] writes — talks to the `workspaces` table directly
// with hand-written SQL, transaction-scoped exactly the way
// internal/overrides/repo.go's own workspaceRevisionTx/bumpRevisionTx do.
package scenarios

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// Summary is one entry of [Repo.List]'s result: everything a scenario
// picker screen needs and NOTHING from the snapshot itself. §C: a list
// endpoint that returned every scenario's full BLOB would be a page-load
// cost that grows with the workspace's history, for a screen that only
// ever shows a name, a date and which one is active.
type Summary struct {
	ID        int64
	Name      string
	CreatedAt time.Time
	// IsActive is workspaces.scenario_id == ID for the workspace this
	// listing was scoped to — computed by List by reading that column
	// alongside the scenario rows, never stored in the scenarios table
	// itself (a scenario has no notion of "is this me?" — the workspace is
	// what points AT one).
	IsActive bool
	// EditVersion is this row's A3 compare-and-swap token (D5:
	// scenarioSummaryView, `GET .../scenarios`, gains editVersion). List
	// reads it straight off the row alongside id/name/created_at.
	EditVersion int64
}

// Scenario is [Repo.Get]/[Repo.ByName]/[Repo.CreateFromCurrentState]'s
// result: the full decoded snapshot, for the ONE screen that actually
// needs it — the detail view (which is also what screen 5's A18 banner
// reads to learn which settings/overrides keys a running scenario masks)
// and internal/mockplane's runtime build, which needs the whole bundle to
// compose over the workspace's own layer.
type Scenario struct {
	ID          int64
	WorkspaceID int64
	Name        string
	CreatedAt   time.Time
	Bundle      bundle.Bundle
	// EditVersion is this row's A3 compare-and-swap token (D5: every
	// single-object scenario read/write response carries editVersion).
	// scanScenario reads it off the row; CreateFromCurrentState and
	// CloneFrom set it to the version they just allocated (store's
	// AllocateEditVersion) rather than re-reading, and RenameExpecting sets
	// it via scanScenario's own re-read, which already picks up the fresh
	// value the rename itself just wrote.
	EditVersion int64
}

// ErrNotFound is returned by Get/ByName/Delete/SetActive when no scenario
// row matches — including, deliberately, a scenario id or name that DOES
// exist but belongs to a DIFFERENT workspace (A8). Every lookup in this
// package scopes its WHERE clause to the given workspaceID, so "exists
// elsewhere" and "does not exist at all" are indistinguishable from the
// caller's workspace's point of view, and answer the same 404 rather than
// leaking which is which.
var ErrNotFound = errors.New("scenarios: scenario not found")

// ErrWorkspaceNotFound is returned when the target workspace itself does
// not exist — distinct from ErrNotFound (a workspace that exists but has
// no matching scenario), exactly as internal/overrides separates the two.
var ErrWorkspaceNotFound = errors.New("scenarios: workspace not found")

// ErrInvalidName is returned when a scenario name is empty (after
// trimming). UNIQUE (workspace_id, name) already rejects a duplicate; this
// catches the other degenerate case before it ever reaches SQL.
var ErrInvalidName = errors.New("scenarios: invalid name")

// ErrDuplicateName is returned by CreateFromCurrentState when
// UNIQUE (workspace_id, name) refuses the insert.
var ErrDuplicateName = errors.New("scenarios: a scenario with this name already exists")

// ErrScenarioActive is A10: CreateFromCurrentState refuses outright,
// before ever touching the overrides table, while a scenario is already
// active for the target workspace. There is no bounded retry for this one
// the way there is for ErrConcurrentEdit below — a scenario being active
// is not a transient race to wait out, it is the workspace's actual
// current state, and the UI's own answer to it ("деактивировать и
// сохранить") requires a human decision, not a retry loop.
var ErrScenarioActive = errors.New("scenarios: a scenario is already active for this workspace")

// ErrConcurrentEdit is A11's fence: CreateFromCurrentState's coherent read
// detected the workspace's revision moving between its two reads, on
// every one of [maxCoherentReadAttempts] tries. See CreateFromCurrentState's
// own doc comment for why this is bounded rather than retried forever, and
// why it is not reachable from a live database in this function's own
// test suite.
var ErrConcurrentEdit = errors.New("scenarios: workspace kept changing while snapshotting it")

// maxCoherentReadAttempts bounds A11's retry loop: three tries, then
// ErrConcurrentEdit. Chosen the same way DESIGN's other small retry bounds
// are — generous for the ordinary case (a single concurrent admin edit
// landing in the same few-millisecond window this function's two reads
// span) while still bounded, so a workspace under genuinely constant
// concurrent editing fails fast and visibly rather than the request
// hanging while this loops.
const maxCoherentReadAttempts = 3

// Repo is the scenarios table's data-access layer, plus the one write this
// package makes to workspaces.scenario_id ([SetActive]).
type Repo struct {
	db        *store.DB
	overrides *overrides.Repo

	// coherentRead is [Repo.readWorkspaceCore] by default (set in NewRepo).
	// CreateFromCurrentState calls THIS field, never the method directly,
	// so a test can substitute a fake that reports a moving revision on
	// every call. A11's three-attempt exhaustion is not reachable against a
	// real database driven from a single goroutine: nothing else can
	// commit between this function's two sequential reads without a
	// SECOND goroutine racing that exact window, which is a flaky test to
	// write and prove nothing this seam does not already prove more
	// directly. See TestCreateFromCurrentState_exhaustsRetriesAgainstAFakeSource.
	coherentRead func(ctx context.Context, workspaceID int64) (workspaceCore, bool, error)
}

// NewRepo builds a Repo over db, reading override rows through
// overridesRepo (internal/scenarios must not open its own second notion of
// "read every op_overrides row for a workspace" — [overrides.Repo.ForWorkspace]
// already exists and is the same query internal/mockplane's runtime build
// uses).
func NewRepo(db *store.DB, overridesRepo *overrides.Repo) *Repo {
	r := &Repo{db: db, overrides: overridesRepo}
	r.coherentRead = r.readWorkspaceCore
	return r
}

// List returns every scenario saved for workspaceID, oldest first, as
// [Summary] — never the snapshot (§C). A workspace that does not exist (or
// has scenario_id unset) simply reads back IsActive=false for everything:
// existence is the caller's concern (the admin handler already loaded the
// workspace before reaching here), not this read's.
func (r *Repo) List(ctx context.Context, workspaceID int64) ([]Summary, error) {
	var activeID sql.NullInt64
	switch err := r.db.R.QueryRowContext(ctx,
		"SELECT scenario_id FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&activeID); {
	case err == nil, errors.Is(err, sql.ErrNoRows):
		// Missing workspace and NULL scenario_id both mean "nothing is
		// active" for this listing's purposes.
	default:
		return nil, fmt.Errorf("list scenarios: read workspace %d: %w", workspaceID, err)
	}

	rows, err := r.db.R.QueryContext(ctx,
		"SELECT id, name, created_at, edit_version FROM scenarios WHERE workspace_id = ? ORDER BY id ASC", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list scenarios for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Summary
	for rows.Next() {
		var (
			id          int64
			name        string
			createdAt   int64
			editVersion int64
		)
		if serr := rows.Scan(&id, &name, &createdAt, &editVersion); serr != nil {
			return nil, fmt.Errorf("scan scenario for workspace %d: %w", workspaceID, serr)
		}
		out = append(out, Summary{
			ID:          id,
			Name:        name,
			CreatedAt:   time.Unix(createdAt, 0).UTC(),
			IsActive:    activeID.Valid && activeID.Int64 == id,
			EditVersion: editVersion,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate scenarios for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// Get looks up one scenario by id, scoped to workspaceID (A8: a scenario
// id belonging to another workspace answers ErrNotFound, exactly like one
// that does not exist at all — the WHERE clause makes the two
// indistinguishable by construction, not by a check afterwards a future
// edit could accidentally skip).
func (r *Repo) Get(ctx context.Context, workspaceID, scenarioID int64) (*Scenario, error) {
	row := r.db.R.QueryRowContext(ctx,
		selectScenario+" WHERE id = ? AND workspace_id = ?", scenarioID, workspaceID)
	return scanScenario(row)
}

// ByName looks up one scenario by NAME, scoped to workspaceID — the mock
// plane's `/__mocker/state {"scenario":"<name>"}` directive names a
// scenario, never an id (DESIGN §17 activation is always by name on that
// plane), so [mockplane.ScenarioSource]'s by-name resolver (§B seam 3)
// calls this to translate before it ever reaches [SetActive].
func (r *Repo) ByName(ctx context.Context, workspaceID int64, name string) (*Scenario, error) {
	row := r.db.R.QueryRowContext(ctx,
		selectScenario+" WHERE workspace_id = ? AND name = ?", workspaceID, name)
	return scanScenario(row)
}

// CreateFromCurrentState snapshots workspaceID's CURRENT settings and
// op_overrides rows into a new named scenario (A1: overrides only, never
// custom endpoints — see the package doc comment and bundle.Bundle's own
// field comments for why).
//
// A10: refused outright with ErrScenarioActive while a scenario is already
// active for this workspace — there is no reading that makes sense of
// "save the composed view" (nobody could attribute the result back to a
// layer) or "save the workspace's own rows while masking it" (screen 9
// would then lie about what it just saved).
//
// A11: the read has to be COHERENT — settings, the override rows and the
// spec identity all have to describe the SAME instant — without opening a
// second transaction on top of the write pool this package otherwise never
// needs one for (A12). The fence: read (revision, name, settings, spec_id,
// scenario_id) from workspaces ONCE, do the (potentially several-query)
// work of reading overrides and the spec's identity, then read
// workspaces.revision again. workspaces.Repo.Update bumps revision by
// exactly 1 on EVERY edit regardless of what changed (its own doc
// comment), and internal/overrides' Put/PutMany/Delete all bump it too
// (via bumpRevisionTx) — so if nothing committed against this workspace
// between the two reads, the revision cannot have moved; if it moved,
// something did, and this retries from the top rather than risking a
// snapshot that mixes pre-edit settings with post-edit overrides or vice
// versa. Bounded at [maxCoherentReadAttempts]: an unbounded retry loop on
// an admin-authenticated, non-hot-path request is worse than a 409 telling
// the operator to try again.
func (r *Repo) CreateFromCurrentState(ctx context.Context, workspaceID int64, name string) (*Scenario, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrInvalidName)
	}

	var lastRetryErr error
	for attempt := 1; attempt <= maxCoherentReadAttempts; attempt++ {
		scenario, retry, err := r.tryCreateFromCurrentState(ctx, workspaceID, name)
		switch {
		case err != nil:
			return nil, err
		case retry != nil:
			lastRetryErr = retry
			continue
		default:
			return scenario, nil
		}
	}

	// A11: bounded exhaustion, not reachable against a real database from
	// this function alone — see coherentRead's own doc comment on the test
	// seam that actually covers this branch.
	return nil, fmt.Errorf("%w after %d attempts: %w", ErrConcurrentEdit, maxCoherentReadAttempts, lastRetryErr)
}

// tryCreateFromCurrentState is ONE attempt of CreateFromCurrentState's
// retry loop, split out so the loop body above stays a plain three-way
// branch instead of the whole read-build-insert sequence living inside it
// (gocyclo: this project holds golangci-lint at zero new suppressions —
// A5's own reasoning about buildRuntime applies just as much here).
//
// Its three return shapes: (scenario, nil, nil) on success; (nil, nil,
// err) for anything the caller should stop and return immediately (A10,
// ErrWorkspaceNotFound, an actual read/write failure); (nil, retryErr,
// nil) when the revision moved and the caller should loop again.
func (r *Repo) tryCreateFromCurrentState(ctx context.Context, workspaceID int64, name string) (scenario *Scenario, retry error, err error) {
	before, exists, err := r.coherentRead(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, fmt.Errorf("create scenario for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	}
	if before.scenarioID != nil {
		return nil, nil, fmt.Errorf("%w: workspace %d", ErrScenarioActive, workspaceID)
	}

	settings, err := domain.ParseSettings([]byte(before.settingsJSON))
	if err != nil {
		return nil, nil, fmt.Errorf("create scenario: parse workspace %d settings: %w", workspaceID, err)
	}

	rowsByKey, err := r.overrides.ForWorkspace(ctx, workspaceID)
	if err != nil {
		return nil, nil, fmt.Errorf("create scenario: %w", err)
	}
	entries := make([]bundle.OverrideEntry, 0, len(rowsByKey))
	for _, row := range rowsByKey {
		entries = append(entries, bundle.NewOverrideEntry(row))
	}

	var specRef bundle.SpecRef
	if before.specID != nil {
		specRef, err = r.readSpecRef(ctx, *before.specID)
		if err != nil {
			return nil, nil, fmt.Errorf("create scenario: %w", err)
		}
	}

	after, exists, err := r.coherentRead(ctx, workspaceID)
	if err != nil {
		return nil, nil, err
	}
	if !exists {
		return nil, nil, fmt.Errorf("create scenario for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	}
	if after.revision != before.revision {
		return nil, fmt.Errorf("%w: workspace %d revision moved from %d to %d",
			ErrConcurrentEdit, workspaceID, before.revision, after.revision), nil
	}

	// New sorts entries deterministically on its own (bundle.go's own doc
	// comment) — rowsByKey above came from a Go map, so entries is in
	// whatever order this run's map iteration happened to produce, and
	// that must never leak into the stored snapshot's bytes.
	b := bundle.New(before.name, settings, specRef, entries)
	data, err := bundle.Encode(b)
	if err != nil {
		return nil, nil, fmt.Errorf("create scenario: encode snapshot: %w", err)
	}

	now := time.Now().UTC()
	var id int64
	var editVersion int64
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		// D4/D9: a created row still needs a version nobody else holds — a
		// create is one of the writes this token exists to guard, even
		// though there is no prior version to compare against (D2/D7).
		v, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		res, ierr := tx.ExecContext(ctx,
			"INSERT INTO scenarios (workspace_id, name, snapshot, created_at, edit_version) VALUES (?, ?, ?, ?, ?)",
			workspaceID, name, data, now.Unix(), v,
		)
		if ierr != nil {
			if isUniqueViolation(ierr) {
				return fmt.Errorf("%w: %q", ErrDuplicateName, name)
			}
			return fmt.Errorf("insert scenario: %w", ierr)
		}
		lastID, ierr := res.LastInsertId()
		if ierr != nil {
			return fmt.Errorf("scenario id: %w", ierr)
		}
		id = lastID
		editVersion = v
		return nil
	})
	if err != nil {
		return nil, nil, err
	}

	// Decode what was just written, rather than returning b directly: b's
	// RawMessage payloads (Body, SchemaPatch, the settings' own
	// NotFoundBody) are whatever the LIVE rows happened to contain, not yet
	// run through Encode's canonicalisation pass — decoding the bytes
	// actually stored is the only way this return value is guaranteed to
	// equal what a subsequent Get(id) reads back.
	decoded, err := bundle.Decode(data)
	if err != nil {
		return nil, nil, fmt.Errorf("create scenario: decode just-written snapshot: %w", err)
	}
	return &Scenario{ID: id, WorkspaceID: workspaceID, Name: name, CreatedAt: now, Bundle: decoded, EditVersion: editVersion}, nil, nil
}

// CloneFrom copies sourceID's stored snapshot into a NEW scenario named
// name, in the same workspace, without ever reading the workspace's own
// layer — so, unlike CreateFromCurrentState, neither A10's active-scenario
// refusal nor A11's coherent-read retry applies here: there is no "current
// state" this function looks at, only bytes another scenario row already
// has, so nothing about which scenario (if any) is active elsewhere in the
// workspace is relevant to copying them (§3 SIG-CLONE, P2d).
//
// The whole copy is ONE statement, `INSERT ... SELECT ... snapshot ...
// FROM scenarios WHERE id = ? AND workspace_id = ?`: the WHERE clause scopes
// sourceID to workspaceID the same way every other lookup in this package
// does (A8), so a sourceID that exists but belongs to a DIFFERENT workspace
// makes the SELECT match zero rows, indistinguishable from a sourceID that
// does not exist at all — both surface as ErrNotFound, and neither writes a
// row. A UNIQUE (workspace_id, name) violation on the INSERT half is
// ErrDuplicateName, exactly as CreateFromCurrentState's own insert reports
// it.
//
// name is trimmed and checked against ErrInvalidName BEFORE the statement
// ever runs — the same guard CreateFromCurrentState uses, checked first so
// {"from":N,"name":"   "} can never reach the INSERT at all: the snapshot
// column has no CHECK constraint of its own, and a row created that way
// would exist in the table but be unreachable through ByName.
//
// The inserted row is RE-READ AND DECODED INSIDE THIS SAME TRANSACTION,
// through tx — never through r.db.R the way Get does. r.db.R is the reader
// pool, a DIFFERENT connection from the single-connection writer this
// transaction is open on (internal/store/store.go), and cannot see a row an
// still-open, uncommitted transaction just inserted: it would read back
// sql.ErrNoRows, scanScenario would turn that into ErrNotFound, and a
// perfectly correct clone would 404. The handler answers 201 with a detail
// view built from the returned *Scenario's Bundle
// (internal/admin/scenario_handlers.go's newScenarioDetailView reads
// .Bundle.Workspace.Settings, .BasePath, .Spec and .Overrides) — a
// re-read that skipped the decode, or read through the wrong connection,
// would hand back a zero Bundle and an empty 201 body, with go test still
// green because nothing at THIS layer checks the field the handler needs.
func (r *Repo) CloneFrom(ctx context.Context, workspaceID, sourceID int64, name string) (*Scenario, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrInvalidName)
	}

	now := time.Now().UTC()
	var out *Scenario
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		// A3: check the workspace exists BEFORE allocating — the INSERT...
		// SELECT below already answers ErrNotFound when it matches zero
		// rows (sourceID absent, or present under a DIFFERENT workspace),
		// and a nonexistent workspaceID produces exactly that same
		// zero-rows result (no scenario row can have workspace_id equal to
		// an id that names no workspace). Calling
		// store.AllocateEditVersion first would let ITS OWN zero-rows
		// failure — a bare wrapped sql.ErrNoRows, neither ErrNotFound nor
		// store.ErrEditConflict — leak out instead, breaking this route's
		// existing 404 for a bad workspace id. Same check-then-allocate
		// ordering the sibling guarded verbs already use
		// (overrides.PutExpecting, customep.UpdateExpecting via
		// workspaceRevisionTx).
		var one int
		switch werr := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE id = ?", workspaceID).Scan(&one); {
		case werr == nil:
		case errors.Is(werr, sql.ErrNoRows):
			return fmt.Errorf("%w: scenario %d in workspace %d", ErrNotFound, sourceID, workspaceID)
		default:
			return fmt.Errorf("check workspace %d: %w", workspaceID, werr)
		}

		// D4/D9: same as CreateFromCurrentState's own INSERT — a clone is a
		// create, so it allocates a fresh version rather than copying the
		// source row's edit_version (that number belongs to a DIFFERENT
		// row and would let a token read from the source match the clone).
		v, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}
		res, ierr := tx.ExecContext(ctx,
			`INSERT INTO scenarios (workspace_id, name, snapshot, created_at, edit_version)
			 SELECT workspace_id, ?, snapshot, ?, ? FROM scenarios WHERE id = ? AND workspace_id = ?`,
			name, now.Unix(), v, sourceID, workspaceID,
		)
		if ierr != nil {
			if isUniqueViolation(ierr) {
				return fmt.Errorf("%w: %q", ErrDuplicateName, name)
			}
			return fmt.Errorf("clone scenario %d: %w", sourceID, ierr)
		}
		n, ierr := res.RowsAffected()
		if ierr != nil {
			return fmt.Errorf("clone scenario %d: rows affected: %w", sourceID, ierr)
		}
		if n == 0 {
			// The SELECT matched no row: sourceID either does not exist at
			// all, or belongs to a different workspace (A8) — the WHERE
			// clause above makes the two indistinguishable by construction.
			return fmt.Errorf("%w: scenario %d in workspace %d", ErrNotFound, sourceID, workspaceID)
		}
		id, ierr := res.LastInsertId()
		if ierr != nil {
			return fmt.Errorf("clone scenario %d: new id: %w", sourceID, ierr)
		}

		sc, serr := scanScenario(tx.QueryRowContext(ctx, selectScenario+" WHERE id = ?", id))
		if serr != nil {
			return fmt.Errorf("clone scenario %d: re-read inserted row: %w", sourceID, serr)
		}
		out = sc
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// Rename changes a scenario's name and nothing else, scoped
// `WHERE id = ? AND workspace_id = ?` exactly like every other lookup in
// this package (A8: a scenarioID belonging to another workspace answers
// ErrNotFound). This is the first `UPDATE scenarios` in the package.
//
// It deliberately does NOT bump workspaces.revision (§4): the runtime
// cache keys on (workspace_id, revision)
// (internal/mockplane/runtime.go's routeCacheKey), and the composed layer
// is looked up by Get(scenarioID), never by name — the one place a
// scenario's NAME is a live runtime key is the mock plane's
// `POST {prefix}/state {"scenario":"<name>"}` directive, which resolves
// through ByName fresh on every request, so a rename takes effect there
// immediately without any cache to evict. A defensive bump here would evict
// every cached runtime of the workspace for a change none of them contain.
//
// name is trimmed and checked against ErrInvalidName BEFORE the UPDATE
// runs, the same guard CreateFromCurrentState and CloneFrom both use. The
// renamed row is re-read and decoded through the SAME transaction the
// UPDATE ran in, for the same reason CloneFrom's is (see its own comment):
// r.db.R cannot see this transaction's own uncommitted write.
// handleRenameScenario answers 200 with the same detail view the clone path
// uses, so an undecoded return here would ship an empty body just like an
// undecoded clone would — it is simply easier to ship green by accident,
// because the acceptance's rename observation re-reads `.name`, a top-level
// column that comes back populated whether or not the bundle was decoded.
// Rename is the unguarded entry point: it delegates to RenameExpecting
// passing "no expectation" (nil), so every existing call site — including
// this package's own 4 test call sites (D8's table) — keeps compiling and
// keeps today's behaviour unchanged: no compare-and-swap check, and a
// zero-rows UPDATE answers ErrNotFound exactly as before (A8: a cross-
// workspace scenarioID and a scenarioID that plain does not exist are
// indistinguishable from this call's point of view, and both stay 404 —
// see RenameExpecting's own doc comment for why an expectation changes
// that answer and "no expectation" does not).
func (r *Repo) Rename(ctx context.Context, workspaceID, scenarioID int64, name string) (*Scenario, error) {
	return r.RenameExpecting(ctx, workspaceID, scenarioID, name, nil)
}

// RenameExpecting is A3's compare-and-swap sibling of Rename (D8's table):
// PUT .../scenarios/{sid} calls this with the caller's expected
// edit_version; Rename's own body above is the one-line delegation that
// keeps every other caller passing "no expectation" (nil) unchanged.
//
// expect == nil ("no expectation"): behaves exactly like the old Rename —
// no compare-and-swap clause, zero rows means ErrNotFound. This is the
// state a nil pointer means ONLY at this repository verb (D7): no wire in
// this tree can produce it, because the four routes' request bodies use
// *int64 exactly so "omitted" (400) is distinguishable from "sent as 0"
// (legal, see below), and the field is REQUIRED on every one of them (D10).
//
// expect != nil: the UPDATE gains `AND edit_version = ?`, and the row is
// ALWAYS re-stamped with a freshly allocated edit_version (D4/D9 — a
// rename changes `name`, which the guarded PUT route can write, so it
// allocates like any other guarded write) — never expect+1 in place,
// because rows here can be deleted and later re-created by a DIFFERENT
// scenario reusing the freed id-adjacent slot only in the loose sense that
// row lifetimes overlap; the allocator's per-workspace sequence is what
// keeps a stale token from ever matching a row it was not read from.
//
// Zero rows affected is not one state, it is three, and only an
// expectation being present makes them distinguishable at all (D7/D8):
//
//   - expect == nil: collapses to today's ErrNotFound, as above — this is
//     the one caller for whom "the scenario is gone" (in EITHER of the two
//     senses below) is the whole story, so there is nothing to tell apart.
//   - expect != nil and the row exists in ANOTHER workspace ("not yours"):
//     the scoped UPDATE (`... WHERE id = ? AND workspace_id = ?`) cannot
//     tell this apart from "gone entirely" by itself — A8's existing
//     comment already calls the two indistinguishable by construction —
//     so this function re-reads the id ALONE, unscoped by workspace,
//     inside the SAME transaction (the same idiom this file's own success
//     path already uses one statement below the scoped UPDATE:
//     `selectScenario+" WHERE id = ?"`). A row under someone else's
//     workspace answers ErrNotFound: the tombstone `{"gone": true}` a
//     conflict carries means "deleted, retry as a create", which would be
//     FALSE about a row that exists and simply is not the caller's — this
//     keeps the 404 the plain not-found route already answers.
//   - expect != nil and the row is truly gone (no row at all for that id,
//     under any workspace) OR the row exists under THIS workspace at a
//     DIFFERENT version: both answer store.ErrEditConflict, uniformly, per
//     D7's "expected N, no row present -> conflict, not not-found" — the
//     row was deleted (or never matched the caller's version) out from
//     under a caller that made a claim about its state, and that is a lost
//     update, not a missing resource. The version-mismatch case carries
//     the row it lost to in EditConflictError.Current; the truly-gone case
//     carries EditConflictError.Gone = true and no row.
//
// expect's zero value (&0) is legal and means "I expect no row" (D7) — but
// a scenario row addressed by PUT .../scenarios/{sid} always already
// exists (unlike op_overrides' Put, which legitimately upserts from
// nothing), so `AND edit_version = 0` can never match a live row: the
// migration's own comment states edit_version's DEFAULT of 0 is never a
// live row's value, and every create here allocates. So &0 always falls
// into the zero-rows branch and is REFUSED with a conflict rather than
// silently ignored (D7's "0 is meaningful only for op_overrides") —
// without a single extra line, because the natural CAS comparison already
// produces exactly that answer.
func (r *Repo) RenameExpecting(ctx context.Context, workspaceID, scenarioID int64, name string, expect *int64) (*Scenario, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return nil, fmt.Errorf("%w: name must not be empty", ErrInvalidName)
	}

	var out *Scenario
	err := r.db.Write(ctx, func(tx *sql.Tx) error {
		// A3: check the workspace exists BEFORE allocating. The UPDATE
		// below is already scoped `WHERE id = ? AND workspace_id = ?`, so
		// a nonexistent workspaceID makes it match zero rows exactly like
		// a scenario belonging to a different (real) workspace does — the
		// same "target row absent" state renameZeroRowsErr below already
		// resolves correctly (D7's target-row qualifier: a missing
		// WORKSPACE must stay a plain not-found, never an edit conflict).
		// Calling store.AllocateEditVersion first would let ITS OWN
		// zero-rows failure — a bare wrapped sql.ErrNoRows, neither
		// ErrNotFound nor store.ErrEditConflict — leak out instead of
		// that resolution, breaking this route's existing 404 for a bad
		// workspace id. Same check-then-allocate ordering the sibling
		// guarded verbs already use (overrides.PutExpecting,
		// customep.UpdateExpecting via workspaceRevisionTx).
		var one int
		switch werr := tx.QueryRowContext(ctx, "SELECT 1 FROM workspaces WHERE id = ?", workspaceID).Scan(&one); {
		case werr == nil:
		case errors.Is(werr, sql.ErrNoRows):
			return renameZeroRowsErr(ctx, tx, workspaceID, scenarioID, expect)
		default:
			return fmt.Errorf("check workspace %d: %w", workspaceID, werr)
		}

		// D4/D9: renaming writes `name`, a field the guarded PUT route can
		// send, so it allocates a fresh edit_version regardless of whether
		// a check is being performed at all (the unguarded, expect==nil
		// path allocates too — see the type's own doc comment).
		newVersion, aerr := store.AllocateEditVersion(ctx, tx, workspaceID)
		if aerr != nil {
			return aerr
		}

		query := "UPDATE scenarios SET name = ?, edit_version = ? WHERE id = ? AND workspace_id = ?"
		args := []any{name, newVersion, scenarioID, workspaceID}
		if expect != nil {
			query += " AND edit_version = ?"
			args = append(args, *expect)
		}
		res, uerr := tx.ExecContext(ctx, query, args...)
		if uerr != nil {
			if isUniqueViolation(uerr) {
				return fmt.Errorf("%w: %q", ErrDuplicateName, name)
			}
			return fmt.Errorf("rename scenario %d: %w", scenarioID, uerr)
		}
		n, uerr := res.RowsAffected()
		if uerr != nil {
			return fmt.Errorf("rename scenario %d: rows affected: %w", scenarioID, uerr)
		}
		if n == 0 {
			return renameZeroRowsErr(ctx, tx, workspaceID, scenarioID, expect)
		}

		sc, serr := scanScenario(tx.QueryRowContext(ctx, selectScenario+" WHERE id = ?", scenarioID))
		if serr != nil {
			return fmt.Errorf("rename scenario %d: re-read renamed row: %w", scenarioID, serr)
		}
		out = sc
		return nil
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// renameZeroRowsErr resolves RenameExpecting's three-state zero-rows
// branch (see that function's own doc comment for the full case analysis).
// Split out because gocyclo would otherwise flag RenameExpecting for
// exactly the branching this comment already explains (this project holds
// golangci-lint at zero new suppressions, CLAUDE.md).
func renameZeroRowsErr(ctx context.Context, tx *sql.Tx, workspaceID, scenarioID int64, expect *int64) error {
	if expect == nil {
		return fmt.Errorf("%w: scenario %d in workspace %d", ErrNotFound, scenarioID, workspaceID)
	}

	// One unscoped-by-workspace read settles all three zero-rows states at
	// once (D7/D8): no row at all -> Gone; a row under a DIFFERENT
	// workspace -> not yours, keep the 404; a row under THIS workspace ->
	// the version mismatch, carried as Current.
	row, serr := scanScenario(tx.QueryRowContext(ctx, selectScenario+" WHERE id = ?", scenarioID))
	switch {
	case errors.Is(serr, ErrNotFound):
		return &store.EditConflictError{Gone: true}
	case serr != nil:
		return fmt.Errorf("rename scenario %d: re-read to resolve conflict: %w", scenarioID, serr)
	case row.WorkspaceID != workspaceID:
		return fmt.Errorf("%w: scenario %d in workspace %d", ErrNotFound, scenarioID, workspaceID)
	default:
		return &store.EditConflictError{Current: row}
	}
}

// Delete removes one scenario, scoped to workspaceID (A8, same reasoning
// as Get). A9: if scenarioID was the workspace's ACTIVE scenario,
// workspaces.scenario_id ON DELETE SET NULL (0001_init.sql) clears it as
// part of the very same DELETE statement — but that FK-driven clear cannot
// bump workspaces.revision, and the runtime cache keys on revision
// (routes.go:57-61), so without an EXPLICIT bump here every subsequent
// request keeps being served from a runtime built over a scenario that no
// longer exists. "Was it active" has to be read BEFORE the DELETE: SQLite
// fires ON DELETE triggers synchronously as part of the statement, so
// reading workspaces.scenario_id AFTER cannot distinguish "was scenarioID,
// now NULL" from "was already something else, unrelated to this delete".
func (r *Repo) Delete(ctx context.Context, workspaceID, scenarioID int64) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		var activeID sql.NullInt64
		switch err := tx.QueryRowContext(ctx,
			"SELECT scenario_id FROM workspaces WHERE id = ?", workspaceID,
		).Scan(&activeID); {
		case err == nil:
		case errors.Is(err, sql.ErrNoRows):
			return fmt.Errorf("delete scenario for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		default:
			return fmt.Errorf("read workspace %d: %w", workspaceID, err)
		}
		wasActive := activeID.Valid && activeID.Int64 == scenarioID

		res, err := tx.ExecContext(ctx,
			"DELETE FROM scenarios WHERE id = ? AND workspace_id = ?", scenarioID, workspaceID)
		if err != nil {
			return fmt.Errorf("delete scenario %d: %w", scenarioID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete scenario %d: rows affected: %w", scenarioID, err)
		}
		if n == 0 {
			return ErrNotFound
		}

		if wasActive {
			if berr := bumpRevisionTx(ctx, tx, workspaceID, time.Now().UTC()); berr != nil {
				return berr
			}
		}
		return nil
	})
}

// SetActive is the ONE function that ever writes workspaces.scenario_id.
// Activation (scenarioID non-nil) and deactivation (scenarioID nil) are
// the SAME write with a different target value, and this is called by
// BOTH the admin routes (.../scenarios/{sid}/activate,
// .../scenarios/deactivate) and the mock plane's directive
// (`/__mocker/state {"scenario": "<name>"}` resolves a name to an id via
// [ByName] first, then calls this; `{"scenario": ""}` calls this with
// nil) — §B seam 2 and A6: a second copy of this write is how A7's
// idempotence or A8's ownership check would end up existing in two
// versions that drift.
//
// A8: a non-nil scenarioID is checked for ownership INSIDE the same
// transaction this writes in (scenarios.workspace_id must equal
// workspaceID) — a scenario id from another workspace answers ErrNotFound,
// never activates silently, and nothing can be raced between the check and
// the write because both happen in one db.Write callback.
//
// A7: if the workspace is ALREADY on scenarioID (both nil — already
// inactive — or both the same non-nil id), this returns the CURRENT
// revision with NO write and NO bump at all. This is load-bearing, not an
// optimisation: DESIGN §18 forbids rate-limiting the unauthenticated mock
// plane, and every real bump costs a full runtime rebuild including a spec
// re-parse (routes.go's cache keys on revision) — an idempotent repeat
// must cost nothing, or a caller retrying the same activation becomes a
// rebuild loop nobody can throttle.
func (r *Repo) SetActive(ctx context.Context, workspaceID int64, scenarioID *int64) (revision int64, err error) {
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		if scenarioID != nil {
			var one int
			switch qerr := tx.QueryRowContext(ctx,
				"SELECT 1 FROM scenarios WHERE id = ? AND workspace_id = ?", *scenarioID, workspaceID,
			).Scan(&one); {
			case qerr == nil:
			case errors.Is(qerr, sql.ErrNoRows):
				return fmt.Errorf("%w: scenario %d in workspace %d", ErrNotFound, *scenarioID, workspaceID)
			default:
				return fmt.Errorf("check scenario %d ownership: %w", *scenarioID, qerr)
			}
		}

		var (
			currentRev int64
			current    sql.NullInt64
		)
		switch werr := tx.QueryRowContext(ctx,
			"SELECT revision, scenario_id FROM workspaces WHERE id = ?", workspaceID,
		).Scan(&currentRev, &current); {
		case werr == nil:
		case errors.Is(werr, sql.ErrNoRows):
			return fmt.Errorf("set active scenario for workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
		default:
			return fmt.Errorf("read workspace %d: %w", workspaceID, werr)
		}

		same := (scenarioID == nil && !current.Valid) ||
			(scenarioID != nil && current.Valid && current.Int64 == *scenarioID)
		if same {
			revision = currentRev // A7: no write, no bump
			return nil
		}

		var newScenario any
		if scenarioID != nil {
			newScenario = *scenarioID
		}
		if _, uerr := tx.ExecContext(ctx,
			"UPDATE workspaces SET scenario_id = ?, revision = revision + 1, updated_at = ? WHERE id = ?",
			newScenario, time.Now().UTC().Unix(), workspaceID,
		); uerr != nil {
			return fmt.Errorf("set active scenario for workspace %d: %w", workspaceID, uerr)
		}
		revision = currentRev + 1
		return nil
	})
	if err != nil {
		return 0, err
	}
	return revision, nil
}

// --- transaction-scoped and read-pool helpers -------------------------------

// workspaceCore is the coherent-read fence's payload: exactly the columns
// A11 names as needing to describe the same instant (revision, settings,
// spec_id, scenario_id), plus name — WorkspaceInfo.Name needs a value too,
// and reading it as part of the SAME row read is free and keeps it under
// the identical revision fence (a rename bumps revision like any other
// workspaces.Repo.Update edit, so capturing name at the first read and
// only using it once the second read confirms the revision held is
// already correct, not an oversight).
type workspaceCore struct {
	revision     int64
	name         string
	settingsJSON string
	specID       *int64
	scenarioID   *int64
}

// readWorkspaceCore is [Repo.coherentRead]'s real, DB-backed
// implementation — read through the reader pool, not a transaction: A11's
// fence works by comparing two INDEPENDENT reads' revisions, not by
// holding a lock across them (a coherent read that held the writer
// connection for the whole overrides-and-spec read in between would be
// exactly the kind of long-held write transaction this project's
// single-connection pool cannot afford).
func (r *Repo) readWorkspaceCore(ctx context.Context, workspaceID int64) (workspaceCore, bool, error) {
	var (
		c          workspaceCore
		specID     sql.NullInt64
		scenarioID sql.NullInt64
	)
	err := r.db.R.QueryRowContext(ctx,
		"SELECT revision, name, settings, spec_id, scenario_id FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&c.revision, &c.name, &c.settingsJSON, &specID, &scenarioID)
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
		return c, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return workspaceCore{}, false, nil
	default:
		return workspaceCore{}, false, fmt.Errorf("read workspace %d: %w", workspaceID, err)
	}
}

// readSpecRef reads the name+hash [bundle.SpecRef] needs for one spec,
// through the reader pool directly rather than internal/specs.Repo.ByID:
// that method selects the FULL specs row, including the raw and normalized
// document columns (~300KB apiece on the acceptance corpus,
// internal/testspec), for a call site that only ever needs
// two short strings. Pulling in internal/specs at all would also drag its
// much heavier dependency graph (internal/config, internal/gen,
// internal/openapi, internal/router) into this package for the same two
// columns.
//
// specID not resolving is not handled as a soft "provenance unavailable"
// case: workspaces.spec_id REFERENCES specs(id) with foreign_keys=ON and
// NO ON DELETE clause, and specs.Repo.Delete refuses to remove a spec any
// workspace still references (its own doc comment) — so a LIVE workspace's
// spec_id pointing at a missing row means that invariant broke somewhere
// else, not a normal runtime condition for this function to paper over.
func (r *Repo) readSpecRef(ctx context.Context, specID int64) (bundle.SpecRef, error) {
	var name, hash string
	if err := r.db.R.QueryRowContext(ctx,
		"SELECT name, hash FROM specs WHERE id = ?", specID,
	).Scan(&name, &hash); err != nil {
		return bundle.SpecRef{}, fmt.Errorf("read spec %d for scenario snapshot: %w", specID, err)
	}
	return bundle.SpecRef{Name: name, Hash: hash}, nil
}

// bumpRevisionTx mirrors internal/overrides/repo.go's helper of the same
// name verbatim (HARD RULE 5: never workspaces.Repo.Update from inside a
// db.Write callback — see this file's package doc comment). Copied rather
// than shared because neither package may import the other for it: sharing
// would mean one of internal/overrides/internal/customep/internal/scenarios
// importing another purely for a four-line SQL helper, which is a
// backwards dependency for at least two of the three.
func bumpRevisionTx(ctx context.Context, tx *sql.Tx, workspaceID int64, now time.Time) error {
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET revision = revision + 1, updated_at = ? WHERE id = ?",
		now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("bump revision for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// isUniqueViolation mirrors internal/workspaces/repo.go's helper of the
// same name and reasoning: modernc.org/sqlite reports a UNIQUE failure as
// a plain error whose message contains "UNIQUE constraint failed", matched
// by substring so this package does not need to import the driver just to
// compare an error code.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// --- scanning ----------------------------------------------------------------

const selectScenario = "SELECT id, workspace_id, name, snapshot, created_at, edit_version FROM scenarios"

func scanScenario(row *sql.Row) (*Scenario, error) {
	var (
		s         Scenario
		snapshot  []byte
		createdAt int64
	)
	if err := row.Scan(&s.ID, &s.WorkspaceID, &s.Name, &snapshot, &createdAt, &s.EditVersion); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("scan scenario: %w", err)
	}

	// A13's second half, read side: re-validated on every read, exactly
	// like op_overrides' own scan() re-checks a row's responses on every
	// unauthenticated mock-plane request — a snapshot that got into
	// storage some other way fails HERE with a returned error, never as a
	// panic further up in internal/mockplane's runtime build.
	b, err := bundle.Decode(snapshot)
	if err != nil {
		return nil, fmt.Errorf("scenario %d: decode snapshot: %w", s.ID, err)
	}
	// A scenario cannot carry a custom endpoint (§0, package doc comment):
	// runtime.custom is keyed by a custom_endpoints DB row id, and a row
	// living inside a BLOB has no id to be keyed by. This used to be
	// bundle.Validate's job — P2c's gate (C2) moved it HERE deliberately,
	// because P2c reuses this identical v3 format for checkpoints, which
	// DO legitimately carry endpoints, so the format itself can no longer
	// refuse a non-empty Endpoints array outright. scanScenario is every
	// scenario read path (Get, ByName, and — through them — the runtime
	// composition and the admin detail route), so one check here is one
	// check for all of them; there is deliberately NO matching write-side
	// guard (C2): [bundle.New]'s signature is unchanged and still
	// hard-codes an empty Endpoints slice, so this package's own write
	// path (CreateFromCurrentState) can never produce a row that would
	// reach this branch at all — only a row this package did not itself
	// write (a hand-run UPDATE, or a future bug) could.
	if len(b.Endpoints) != 0 {
		return nil, fmt.Errorf("scenario %d: %w: endpoints must be empty (a scenario cannot carry a custom endpoint)",
			s.ID, bundle.ErrInvalid)
	}
	s.Bundle = b
	s.CreatedAt = time.Unix(createdAt, 0).UTC()
	return &s, nil
}
