// Rollback and reset: the two destructive verbs, each a pre-destructive
// snapshot plus one apply transaction. Split out of repo.go 2026-09-03; the
// text is unchanged.
package checkpoints

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/overrides"
)

// Rollback restores workspaceID's whole workspace layer — settings,
// op_overrides and custom_endpoints — to the state checkpointID holds, in
// ONE transaction that ALSO writes the pre-destructive checkpoint
// protecting what it is about to overwrite, and allocates a new revision.
//
// DESIGN §12:774-776: «Откат ВСЕГДА выделяет новую revision (max+1 …);
// номера никогда не переиспользуются» — max+1, not merely "greater", which
// is why the bump is a single revision+1 UPDATE and never a value the
// snapshot carries.
//
// The pre-destructive checkpoint captures the state being DESTROYED, so it
// is built from [captureSnapshot] BEFORE the apply, not after — an
// implementation that snapshots afterwards records the state it just
// restored and the undo of the undo becomes a no-op (§G obs 5 drives
// exactly that).
//
// Allowed while a scenario is active (C8), and the returned
// Outcome.ScenarioActive says so: mockplane/runtime.go loads custom
// endpoints unconditionally, OUTSIDE the scenario branch, and
// mockplane/scenario.go:113-116 makes a key ABSENT from the snapshot keep
// the WORKSPACE's row — so a rollback under a scenario is fully visible for
// every custom endpoint and every override key the scenario does not name.
// Refusing it would refuse a demonstrably visible operation DESIGN never
// asked anyone to refuse; the screen warns instead.
//
// restoreData and confirmSlug are D7's own pair, of the P3d decision
// document: restoreData false or absent changes no entity row (settings,
// overrides and endpoints still restore exactly as before this slice), and
// confirmSlug is read but not compared unless restoreData is true — a
// rollback that touches no entity row destroys nothing a slug needs to
// protect. restoreData true DELETES the workspace's current entity rows
// for every family the target checkpoint carries, which is why it takes
// the same confirmSlug a decline and a reset-data already require: the
// undo is real (this rollback's own pre-destructive checkpoint, written
// inside this same transaction, carries the rows being overwritten) but
// bounded by checkpoint retention, exactly as those two verbs' undos are
// not real at all.
func (r *Repo) Rollback(ctx context.Context, workspaceID, checkpointID, createdBy int64, restoreData bool, confirmSlug string) (Outcome, error) {
	var out Outcome
	err := retrying(func() error {
		// C5 step 3's expensive half, all on the reader pool and all
		// OUTSIDE the transaction: the capture's read/encode/gzip AND the
		// target's read/gunzip/decode. C17's exception buys atomicity for
		// the apply, not for the reading.
		snap, cerr := r.captureSnapshot(ctx, workspaceID)
		if cerr != nil {
			return cerr
		}
		target, terr := r.Get(ctx, workspaceID, checkpointID)
		if terr != nil {
			return terr
		}
		// The data half's gunzip/decode/validate (up to 8 MiB) is the
		// reading, not the apply, and belongs out here with the rest of
		// step 3 — it used to run INSIDE rollbackTx, holding the single
		// writer for the whole decode.
		var dataBundle bundle.DataBundle
		if restoreData {
			if target.DataBlob == nil {
				return fmt.Errorf("%w: checkpoint %d", ErrNoDataSnapshot, target.ID)
			}
			doc, derr := decompressSnapshot(target.DataBlob)
			if derr != nil {
				return derr
			}
			if dataBundle, derr = bundle.DecodeData(doc); derr != nil {
				return fmt.Errorf("%w: checkpoint %d: %w", ErrCorruptSnapshot, target.ID, derr)
			}
			if verr := bundle.ValidateData(dataBundle); verr != nil {
				return fmt.Errorf("%w: checkpoint %d: %w", ErrCorruptSnapshot, target.ID, verr)
			}
		}
		capturePreWriteHook()
		return r.db.Write(ctx, func(tx *sql.Tx) error {
			o, werr := r.rollbackTx(ctx, tx, workspaceID, createdBy, restoreData, confirmSlug, snap, target, dataBundle)
			if werr != nil {
				return werr
			}
			out = o
			return nil
		})
	})
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// rollbackTx is [Repo.Rollback]'s whole transaction body, in the order C5
// step 6 fixes: fence, protect, apply, prune, bump — with D6's one new step,
// [restoreEntitiesTx], inserted between upsertResourcesTx's family and
// customep.ReplaceAllTx (see that function's own doc for why both
// neighbours are load-bearing).
func (r *Repo) rollbackTx(ctx context.Context, tx *sql.Tx, workspaceID, createdBy int64, restoreData bool, confirmSlug string, snap capture, target *Checkpoint, dataBundle bundle.DataBundle) (Outcome, error) { //nolint:gocyclo // one branch per D7 refusal reason (fence, confirmSlug pair, target existence, no-data-snapshot, corrupt document, degraded pre-destructive capture) plus the ordered apply/prune/bump steps C5 step 6 fixes — P3d's own addition to an already-linear sequence, not incidental branching
	cur, err := fenceTx(ctx, tx, workspaceID, snap.core)
	if err != nil {
		return Outcome{}, err
	}

	// D7's slug pair, checked against cur.slug — the value THIS
	// transaction already holds, re-read by fenceTx a moment ago, never a
	// second SELECT. The reason is not the rename window (fenceTx already
	// refuses a rename outright): it is what CLAUDE.md gives every other
	// slug in this tree — stopping a successful call aimed at the WRONG
	// workspace, which no fence can see because nothing about it is stale.
	if restoreData {
		if confirmSlug == "" {
			return Outcome{}, fmt.Errorf("%w: workspace %d", ErrConfirmSlugRequired, workspaceID)
		}
		if confirmSlug != cur.slug {
			return Outcome{}, fmt.Errorf("%w: workspace %d", ErrConfirmSlugMismatch, workspaceID)
		}
	}

	// C5 step 4's second half: re-read the target for EXISTENCE. This is
	// cheap corroboration, not an independent proof — checkpoints.id has no
	// AUTOINCREMENT either, so a reused id could pass it — and the residual
	// window fenceTx documents already covers what it cannot.
	if err := checkpointExistsTx(ctx, tx, workspaceID, target.ID); err != nil {
		return Outcome{}, err
	}

	// D7's remaining two data refusals, both checked BEFORE anything is
	// written: a target with no data to restore, and a target document
	// that fails validation. Neither reads or writes an entity row.
	// dataBundle arrives decoded and validated by Rollback, on the reader
	// side of the transaction boundary; the two refusals that used to sit
	// here (no data snapshot, corrupt document) moved with the decode.

	now := time.Now().UTC()
	// D5: always-capture — this pre-destructive checkpoint carries the
	// entity rows a restoreData:true rollback is about to overwrite,
	// regardless of restoreData itself, so that the rollback remains
	// recoverable while its own row survives retention. D5.2's degrade is
	// consumed here: only a restoreData:true rollback needs it refused
	// (D7's 413) — a restoreData:false rollback still writes the
	// checkpoint with a degraded (NULL) data_snap and proceeds, exactly as
	// it always could.
	entityBlob, degraded, err := captureEntitiesTx(ctx, tx, workspaceID)
	if err != nil {
		return Outcome{}, err
	}
	if restoreData && degraded {
		return Outcome{}, fmt.Errorf("%w: workspace %d", ErrDataSnapshotTooLarge, workspaceID)
	}
	if _, err := insertCheckpointTx(ctx, tx, workspaceID, KindPreDestructive,
		rollbackLabel(target.ID), snap.blob, entityBlob, createdBy, now); err != nil {
		return Outcome{}, err
	}

	if err := writeSettingsTx(ctx, tx, workspaceID, target.Bundle.Workspace.Settings, now); err != nil {
		return Outcome{}, err
	}
	if err := overrides.ReplaceAllTx(ctx, tx, workspaceID, overrideRowsFromBundle(target.Bundle), now); err != nil {
		return Outcome{}, err
	}

	// P3b's three statements, and their POSITION is load-bearing: they sit
	// BEFORE customep.ReplaceAllTx, not after it. This package's only
	// failure-injection technique is a crafted snapshot whose second
	// endpoint fails customep's own validatePath INSIDE that call, so
	// anything placed after it can never be reached by an injected failure
	// — and "the whole restore is one transaction" would be green over a
	// resource half quietly running in its own.
	if err := upsertResourcesTx(ctx, tx, workspaceID, target.Bundle.Resources); err != nil {
		return Outcome{}, err
	}
	live, err := liveResourceFamiliesTx(ctx, tx, workspaceID, target.Bundle)
	if err != nil {
		return Outcome{}, err
	}
	if err := upsertDecisionsTx(ctx, tx, workspaceID, target.Bundle.Decisions, live); err != nil {
		return Outcome{}, err
	}

	// D6's one new step: it must run AFTER upsertResourcesTx (a
	// route_family only resolves to a live resources.id once that step has
	// run — including for a family declined after the capture, whose row
	// upsertResourcesTx has just re-inserted) and BEFORE
	// customep.ReplaceAllTx, for the identical injection-testability reason
	// the P3b statements above it were placed there.
	var dataRestored bool
	if restoreData {
		restored, rerr := restoreEntitiesTx(ctx, tx, workspaceID, dataBundle)
		if rerr != nil {
			return Outcome{}, rerr
		}
		dataRestored = restored > 0
	}

	endpointRows, err := endpointRowsFromBundle(target.Bundle)
	if err != nil {
		return Outcome{}, fmt.Errorf("restore endpoints for workspace %d: %w", workspaceID, err)
	}
	if err := customep.ReplaceAllTx(ctx, tx, workspaceID, endpointRows, now); err != nil {
		return Outcome{}, err
	}

	// C7's third case: a MACHINE-MADE target is spared even when it falls
	// outside the newest N, leaving N+1 rows for the duration of this one
	// rollback — deliberately, because the alternative is deleting the row
	// the user just returned to, in the same transaction that applies it,
	// leaving the history with no trace of the state they came back from.
	// The overflow corrects itself at the next machine-made insert whose
	// target is not that row. A MANUAL target gets no exemption and needs
	// none: it is not in the pruned population at all.
	keep := keepNone
	if target.Kind != KindManual {
		keep = target.ID
	}
	if err := pruneRetentionTx(ctx, tx, workspaceID, r.retention, keep); err != nil {
		return Outcome{}, err
	}

	if err := bumpRevisionTx(ctx, tx, workspaceID, now); err != nil {
		return Outcome{}, err
	}
	return Outcome{Revision: cur.revision + 1, ScenarioActive: cur.scenarioActive, Changed: true, DataRestored: dataRestored}, nil
}

// Reset is screen 10's «сбросить всё к спеке» (DESIGN §14:908): it deletes
// every op_overrides row AND every custom_endpoints row of the workspace,
// after writing the pre-destructive checkpoint that makes the blast radius
// acceptable, and allocates a new revision.
//
// Custom endpoints go too, and that is DESIGN's own words rather than an
// extension of them (C9): a custom endpoint is not in the spec, so
// «сбросить всё к спеке» includes it. So does the auth preset — every
// binding is materialised as recipes into op_overrides
// (admin/preset_handlers.go:239-245), so the frontend under test stops
// logging in. Neither consequence is guessable from the button, which is
// why the confirmation copy names both.
//
// settings are NOT reset: they are the workspace's identity (seed,
// basePath, auth config), not edits to the spec, and resetting basePath
// would move every route.
//
// C9's no-op: a reset that would delete nothing writes NO checkpoint, bumps
// NO revision and answers 200 with Changed false. The decision is made from
// the PRE-transaction read (both source lists empty) under the fence, never
// from a count returned by the apply — ReplaceAllTx returns none on
// purpose, and a round that added one created an ordering contradiction,
// since a count is only known after the apply while this decision has to be
// made before the checkpoint is written (C4's closing paragraph).
func (r *Repo) Reset(ctx context.Context, workspaceID, createdBy int64) (Outcome, error) {
	var out Outcome
	err := retrying(func() error {
		snap, cerr := r.captureSnapshot(ctx, workspaceID)
		if cerr != nil {
			return cerr
		}
		capturePreWriteHook()
		return r.db.Write(ctx, func(tx *sql.Tx) error {
			o, werr := r.resetTx(ctx, tx, workspaceID, createdBy, snap)
			if werr != nil {
				return werr
			}
			out = o
			return nil
		})
	})
	if err != nil {
		return Outcome{}, err
	}
	return out, nil
}

// resetTx is [Repo.Reset]'s transaction body. The no-op branch still opens
// and fences this transaction rather than returning before it: C5 step 5
// puts ALL THREE paths under the full comparison, and the emptiness the
// decision rests on was read on a multi-connection reader pool — without
// the fence, an override committed between that read and this point would
// be answered "nothing to reset" and left in place.
func (r *Repo) resetTx(ctx context.Context, tx *sql.Tx, workspaceID, createdBy int64, snap capture) (Outcome, error) {
	cur, err := fenceTx(ctx, tx, workspaceID, snap.core)
	if err != nil {
		return Outcome{}, err
	}
	if snap.overrideCount == 0 && snap.endpointCount == 0 {
		return Outcome{Revision: cur.revision, ScenarioActive: cur.scenarioActive, Changed: false}, nil
	}

	now := time.Now().UTC()
	// D5: always-capture, same reasoning as [Repo.Rollback]'s own call —
	// this is the second of the two capture sites D5 names as "having
	// nobody to ask", and Reset's no-op branch above already returned
	// before this point, so this call only ever runs on the path that is
	// actually about to destroy every op_overrides and custom_endpoints
	// row. Reset never restores entity data itself (there is no
	// restoreData argument on this verb), so the degrade signal is
	// discarded here exactly as it is on the manual and auto paths.
	entityBlob, _, derr := captureEntitiesTx(ctx, tx, workspaceID)
	if derr != nil {
		return Outcome{}, derr
	}
	if _, err := insertCheckpointTx(ctx, tx, workspaceID, KindPreDestructive,
		resetLabel, snap.blob, entityBlob, createdBy, now); err != nil {
		return Outcome{}, err
	}

	// An empty rows slice is how a restore says "keep nothing": both
	// ReplaceAllTx implementations turn it into one unconditional
	// workspace-scoped DELETE, which is the reset's whole apply.
	if err := overrides.ReplaceAllTx(ctx, tx, workspaceID, nil, now); err != nil {
		return Outcome{}, err
	}
	if err := customep.ReplaceAllTx(ctx, tx, workspaceID, nil, now); err != nil {
		return Outcome{}, err
	}

	// C7's first case: no rollback target, so the plain rule.
	if err := pruneRetentionTx(ctx, tx, workspaceID, r.retention, keepNone); err != nil {
		return Outcome{}, err
	}
	if err := bumpRevisionTx(ctx, tx, workspaceID, now); err != nil {
		return Outcome{}, err
	}
	return Outcome{Revision: cur.revision + 1, ScenarioActive: cur.scenarioActive, Changed: true}, nil
}
