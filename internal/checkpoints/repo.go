package checkpoints

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/store"
)

// keepNone is [pruneRetentionTx]'s "there is no rollback target to spare"
// value. SQLite assigns rowids from 1 upward and nothing in this tree ever
// inserts an explicit checkpoints.id, so 0 can never match a real row.
const keepNone int64 = 0

// Repo is the checkpoints table's data-access layer, plus the two writes
// this package makes to the workspaces table (the wholesale settings
// restore and the revision bump — see the package doc comment on why they
// are hand-written SQL rather than workspaces.Repo.Update).
type Repo struct {
	db        *store.DB
	overrides *overrides.Repo
	customep  *customep.Repo
	retention int
}

// NewRepo builds a Repo over db.
//
// overridesRepo and customepRepo are SHARED instances, not ones this
// constructor makes: "one instance per data-access layer per plane is the
// existing pattern here" (admin/server.go:100-112), and this package reads
// both tables through the same ForWorkspace queries the runtime build uses
// rather than opening a second notion of "every override row for a
// workspace".
//
// retention is a PARAMETER and not a package default, and this constructor
// deliberately does NOT normalise a zero away (C7). traffic.NewRecorder
// does exactly that — `if rec.retention <= 0 { rec.retention =
// DefaultRetention }` (recorder.go:98-100) — so MOCKER_TRAFFIC_RETENTION=0
// prunes to the newest 1000 rows and that package's own `retention <= 0`
// guard is unreachable from any configured value. Copying that here would
// be wrong: the two knobs differ ON PURPOSE. Traffic is a hot-path firehose
// whose zero cannot sensibly mean "keep everything" (that is unbounded
// growth on the write path), while a history knob is the opposite —
// "keep everything" is the one thing an operator might genuinely want from
// an undo log, and a checkpoint is written at human rate.
// MOCKER_CHECKPOINT_RETENTION=0 therefore means PRUNE NOTHING; §G obs 19 is
// a unit test because smoke.sh runs at 3 and cannot see it.
func NewRepo(db *store.DB, overridesRepo *overrides.Repo, customepRepo *customep.Repo, retention int) *Repo {
	return &Repo{db: db, overrides: overridesRepo, customep: customepRepo, retention: retention}
}

// List returns every checkpoint of one workspace as [Summary] — never the
// snapshot (§C) — newest first.
//
// Newest-first, unlike scenarios.List's oldest-first, for two reasons that
// point the same way: it is the order a history screen renders, and it is
// the order the checkpoints_ws index already stores
// (workspace_id, id DESC — 0001_init.sql:226), so the read is an index
// walk rather than a sort.
func (r *Repo) List(ctx context.Context, workspaceID int64) ([]Summary, error) {
	rows, err := r.db.R.QueryContext(ctx,
		"SELECT id, kind, label, created_at, created_by, data_snap IS NOT NULL FROM checkpoints WHERE workspace_id = ? ORDER BY id DESC",
		workspaceID)
	if err != nil {
		return nil, fmt.Errorf("list checkpoints for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Summary
	for rows.Next() {
		var (
			s         Summary
			createdAt int64
			createdBy sql.NullInt64
		)
		if serr := rows.Scan(&s.ID, &s.Kind, &s.Label, &createdAt, &createdBy, &s.HasData); serr != nil {
			return nil, fmt.Errorf("scan checkpoint for workspace %d: %w", workspaceID, serr)
		}
		s.CreatedAt = time.Unix(createdAt, 0).UTC()
		if createdBy.Valid {
			v := createdBy.Int64
			s.CreatedBy = &v
		}
		out = append(out, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate checkpoints for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// Get reads one checkpoint by id, scoped to workspaceID, and decodes its
// snapshot: gunzip under C18's ceiling FIRST, then [bundle.Decode], which
// runs bundle.Validate on every read exactly as scenarios' own scanScenario
// does — a blob that got into storage some other way fails here as a
// returned error, never as a panic further up.
//
// A checkpoint id belonging to another workspace answers [ErrNotFound],
// like one that does not exist at all: the WHERE clause makes the two
// indistinguishable by construction.
func (r *Repo) Get(ctx context.Context, workspaceID, checkpointID int64) (*Checkpoint, error) {
	var (
		c         Checkpoint
		blob      []byte
		dataBlob  []byte
		createdAt int64
		createdBy sql.NullInt64
	)
	err := r.db.R.QueryRowContext(ctx, `
		SELECT id, workspace_id, kind, label, config_snap, data_snap, created_at, created_by
		FROM checkpoints WHERE id = ? AND workspace_id = ?`, checkpointID, workspaceID,
	).Scan(&c.ID, &c.WorkspaceID, &c.Kind, &c.Label, &blob, &dataBlob, &createdAt, &createdBy)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return nil, fmt.Errorf("%w: checkpoint %d in workspace %d", ErrNotFound, checkpointID, workspaceID)
	default:
		return nil, fmt.Errorf("read checkpoint %d: %w", checkpointID, err)
	}
	c.CreatedAt = time.Unix(createdAt, 0).UTC()
	if createdBy.Valid {
		v := createdBy.Int64
		c.CreatedBy = &v
	}
	// dataBlob is nil exactly when data_snap IS NULL — database/sql's
	// []byte destination treats a NULL column as a nil slice with no error,
	// so no sql.Null wrapper is needed here. HasData mirrors [Repo.List]'s
	// own derivation rather than being read off blob a second way.
	c.DataBlob = dataBlob
	c.HasData = dataBlob != nil

	doc, err := decompressSnapshot(blob)
	if err != nil {
		return nil, fmt.Errorf("checkpoint %d: %w", checkpointID, err)
	}
	b, derr := bundle.Decode(doc)
	if derr != nil {
		return nil, fmt.Errorf("checkpoint %d: decode snapshot: %w", checkpointID, derr)
	}
	c.Bundle = b
	return &c, nil
}

// Create writes a MANUAL checkpoint — the operator's own button, DESIGN
// §12:770-772's third trigger — and prunes, all under C5's fence.
//
// It does NOT bump workspaces.revision (C12). Nothing served changes when a
// history entry is written, and a bump costs a full runtime rebuild
// including a spec re-parse (routes.go's cache keys on revision); an
// implementer copying customep.Repo.Create's always-bump pattern gets this
// wrong.
//
// It runs the FULL comparison and the bounded retry anyway, and that is not
// belt-and-braces. A gate round exempted this path from the revision half
// to avoid a spurious 409 on a non-mutating button, and that was WRONG:
// [captureSnapshot]'s four statements run on a MULTI-connection reader pool
// (store.go:83), so without the comparison a manual checkpoint can store
// settings read BEFORE an edit next to overrides read AFTER it — the torn
// snapshot scenarios/repo.go:243-245 names as its fence's entire reason
// ("this retries from the top rather than risking a snapshot that mixes
// pre-edit settings with post-edit overrides or vice versa"). The retry
// bound, not an exemption, is what keeps the button from failing
// spuriously.
//
// createdBy is the session user (C15): the column REFERENCES users(id), the
// handler holds the user and this repo cannot reach it, so it is passed
// down exactly as spec_handlers.go:197-206 already passes one.
func (r *Repo) Create(ctx context.Context, workspaceID int64, label string, createdBy int64) (*Summary, error) {
	label, err := validateLabel(label)
	if err != nil {
		return nil, err
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
			// D5: every checkpoint captures entity data, on every kind — a
			// manual one included, even though it protects nothing about
			// to be destroyed. The degrade signal is not consumed here:
			// only a restoreData:true rollback needs it (D7's 413), and a
			// manual checkpoint is never a rollback target for that check.
			dataBlob, _, derr := captureEntitiesTx(ctx, tx, workspaceID)
			if derr != nil {
				return derr
			}
			now := time.Now().UTC()
			s, ierr := insertCheckpointTx(ctx, tx, workspaceID, KindManual, label, snap.blob, dataBlob, createdBy, now)
			if ierr != nil {
				return ierr
			}
			// C7: no rollback target here, so the plain rule — keep the
			// newest N machine-made rows. A manual row is never pruned, so
			// the one just inserted cannot be its own victim.
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

// Delete removes one checkpoint of a workspace by id. It bumps no revision and
// takes no pre-destructive checkpoint of its own.
//
// Unlike [Repo.Rollback] and [Repo.Reset] it needs neither C5's fence nor
// [Repo.captureSnapshot]: deleting a history row changes nothing about the
// workspace those two protect, so there is no torn read to guard against —
// a single scoped DELETE, in its own [store.DB.Write], is the whole
// operation. Any kind may be deleted, including the newest row and the
// last one: an empty history is a legal state, the one every workspace
// starts in.
//
// Scoped `WHERE id = ? AND workspace_id = ?`, so a checkpoint id belonging
// to another workspace answers [ErrNotFound] exactly like one that does
// not exist at all — the same by-construction indistinguishability [Get]'s
// doc comment states, for the same reason. A zero-row delete is
// [ErrNotFound]; the handler answers 404. Success has nothing to report
// back, so the handler answers 204.
func (r *Repo) Delete(ctx context.Context, workspaceID, checkpointID int64) error {
	return r.db.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx,
			"DELETE FROM checkpoints WHERE id = ? AND workspace_id = ?", checkpointID, workspaceID)
		if err != nil {
			return fmt.Errorf("delete checkpoint %d: %w", checkpointID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("delete checkpoint %d: rows affected: %w", checkpointID, err)
		}
		if n == 0 {
			return fmt.Errorf("%w: checkpoint %d in workspace %d", ErrNotFound, checkpointID, workspaceID)
		}
		return nil
	})
}
