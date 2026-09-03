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
	"github.com/yashok111/mocker/internal/workspaces"
)

// P4b (2026-09-02): export, import and fork — DESIGN §17's third job for
// the one format, and §19's P4 "team scenarios". The three live HERE, in
// the checkpoint repository, and not in a package of their own, because
// they are the checkpoint's capture and restore pointed at a different
// row: an export is [Repo.captureSnapshot]'s read half without the gzip,
// an import is [Repo.rollbackTx]'s apply half into a workspace created in
// the same transaction, and a fork is the two back to back inside one
// installation. Exporting the eight unexported apply steps to a sibling
// package would create the second caller every one of their doc comments
// says must not exist ("one transaction for the whole restore", the
// UPSERT-only rule on resources, the order of the P3b statements); keeping
// them private and adding three methods beside them keeps one owner.
//
// What an export carries, and what it does not: the whole configuration
// layer (settings, overrides, custom endpoints, resources, decisions) and
// optionally the entity rows — never assets (DESIGN §32.4: "the bundle does
// not carry assets in v11"; a bodyRef whose asset is absent answers the
// variant's status with an empty body and `asset_missing` in the traffic),
// never scenarios (a scenario is a second snapshot of the same layer; an
// export is ONE state), never checkpoints or traffic (history is the
// source's). A fork, staying inside one installation, copies assets and
// scenarios by INSERT … SELECT in the same transaction — there the bytes
// cost nothing to carry and "a configured copy" without its pictures is
// not a copy.

// ErrSpecMissing is returned by [Repo.Import] when the document names a
// spec by hash and the caller resolved it to nothing — the handler decides
// whether that is a 409 or a spec import; this package only refuses to
// bind a workspace to a spec it was not handed.
var ErrSpecMissing = errors.New("checkpoints: import names a spec this installation does not hold")

// Export reads the workspace's configuration as the same v4 document a
// checkpoint's config_snap holds — decoded, uncompressed — and, when
// withData, its entity rows as the same [bundle.DataBundle] a checkpoint's
// data_snap holds. Both reads run on the reader pool; the data half inside
// ONE read transaction so the rows are one snapshot, not a family-by-family
// walk over a table the mock plane is writing into.
//
// The data half keeps the checkpoint capture's probe budget and takes the
// OPPOSITE policy on it: a checkpoint degrades to a NULL data_snap and
// still writes its row, an export refuses by name ([ErrDataSnapshotTooLarge])
// so the caller can ask again without the rows — an export that silently
// dropped the rows would hand a teammate a copy that serves empty lists.
func (r *Repo) Export(ctx context.Context, workspaceID int64, withData bool) (bundle.Export, error) {
	_, b, err := r.readBundle(ctx, workspaceID)
	if err != nil {
		return bundle.Export{}, err
	}
	out := bundle.Export{Bundle: b}
	if !withData {
		return out, nil
	}

	tx, err := r.db.R.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return bundle.Export{}, fmt.Errorf("export workspace %d: begin read: %w", workspaceID, err)
	}
	defer func() { _ = tx.Rollback() }()

	over, err := entityDataProbeOverBudgetTx(ctx, tx, workspaceID)
	if err != nil {
		return bundle.Export{}, err
	}
	if over {
		return bundle.Export{}, fmt.Errorf("%w: workspace %d", ErrDataSnapshotTooLarge, workspaceID)
	}
	d, err := readDataBundleTx(ctx, tx, workspaceID)
	if err != nil {
		return bundle.Export{}, err
	}
	// A workspace with no confirmed family exports as a plain bundle: an
	// empty families array would only say "there is nothing here" in a
	// second place.
	if len(d.Families) > 0 {
		out.Data = &d
	}
	return out, nil
}

// ImportInput is what [Repo.Import] needs beyond the document: the new
// workspace's identity, and the spec the CALLER resolved — this package
// reads `spec.hash` from the document only to refuse a caller that resolved
// nothing for a document that names one.
type ImportInput struct {
	// Name and Slug follow [workspaces.CreateInput]: Slug empty derives
	// one from Name and uniquifies it, non-empty is validated and refused
	// when taken.
	Name string
	Slug string
	// OwnerID is the importing user; CreatedBy is who the baseline
	// checkpoint is attributed to (the same id in practice).
	OwnerID   *int64
	CreatedBy int64
	// SpecID is the spec the workspace binds to: the handler resolves it
	// from the request's explicit specId, the document's hash, or the
	// document's inline copy, in that order. Nil is legal only when the
	// document names no spec at all (an export of a spec-less workspace).
	SpecID *int64
	// Document is the decoded, validated export. Its Workspace.Name is
	// overridden by Name; its settings are written as they are (the handler
	// validated basePath and basePathValues before calling).
	Document bundle.Export
	// Label names the baseline checkpoint the new workspace starts with.
	Label string
}

// ImportOutcome reports what [Repo.Import] wrote.
type ImportOutcome struct {
	Workspace *workspaces.Workspace
	// EntitiesRestored counts the families whose rows the document's data
	// half put in place — [restoreEntitiesTx]'s own count, so a family the
	// data half names and the config half does not is skipped, not
	// counted.
	EntitiesRestored int
}

// Import creates a workspace from an export document in ONE transaction:
// the row ([workspaces.CreateTx]), the configuration layer ([applyBundleTx]
// — the rollback's own apply steps, in the rollback's own order), the
// entity rows when the document carries them, and a baseline checkpoint of
// kind manual holding exactly what was written, so the new workspace's
// history starts with a state a rollback can return to. No fence: there is
// no source row that could move, and the new row is invisible to every
// other client until the transaction commits.
func (r *Repo) Import(ctx context.Context, in ImportInput) (ImportOutcome, error) {
	if in.SpecID == nil && in.Document.Spec.Hash != "" {
		return ImportOutcome{}, fmt.Errorf("%w: hash %s (%q)", ErrSpecMissing, in.Document.Spec.Hash, in.Document.Spec.Name)
	}
	b := in.Document.Bundle
	b.Workspace.Name = in.Name
	doc, err := bundle.Encode(b)
	if err != nil {
		return ImportOutcome{}, fmt.Errorf("import: encode baseline for %q: %w", in.Name, err)
	}
	blob, err := compressSnapshot(doc)
	if err != nil {
		return ImportOutcome{}, fmt.Errorf("import %q: %w", in.Name, err)
	}

	var out ImportOutcome
	err = r.db.Write(ctx, func(tx *sql.Tx) error {
		now := time.Now().UTC()
		settings := b.Workspace.Settings
		ws, cerr := workspaces.CreateTx(ctx, tx, workspaces.CreateInput{
			Name: in.Name, Slug: in.Slug, OwnerID: in.OwnerID, SpecID: in.SpecID, Settings: &settings,
		})
		if cerr != nil {
			return cerr
		}
		restored, aerr := applyBundleTx(ctx, tx, ws.ID, b, in.Document.Data, now)
		if aerr != nil {
			return aerr
		}
		if berr := baselineCheckpointTx(ctx, tx, ws.ID, in.Label, blob, in.CreatedBy, now); berr != nil {
			return berr
		}
		out = ImportOutcome{Workspace: ws, EntitiesRestored: restored}
		return nil
	})
	if err != nil {
		return ImportOutcome{}, err
	}
	return out, nil
}

// ForkInput is what [Repo.Fork] needs: the source and the copy's identity.
type ForkInput struct {
	SourceID int64
	Name     string
	Slug     string
	OwnerID  *int64
	// CreatedBy attributes the copy's baseline checkpoint.
	CreatedBy int64
	// IncludeData copies the source's entity rows; false forks the
	// configuration alone, every confirmed family starting empty exactly as
	// a confirm-then-clear would leave it.
	IncludeData bool
	// Label names the baseline checkpoint.
	Label string
}

// Fork is export-then-import inside one installation, plus what an export
// deliberately leaves behind: the source's assets and scenarios, copied by
// INSERT … SELECT in the same transaction, and — with IncludeData — its
// entity rows, copied the same way rather than through a [bundle.DataBundle]
// (no probe budget applies: the bytes never leave SQLite), with each
// family's `seq` raised to its highest copied key afterwards, the identical
// rule a restore applies. The active scenario, if any, is re-pointed at the
// copy's row of the same name, so the fork serves exactly what the source
// serves at the instant of the copy.
//
// The source is read on the reader pool and FENCED inside the write
// transaction (the checkpoint's own [fenceTx], retried the same way): the
// copy is one consistent state of the source, not a configuration from
// before an edit stitched to entity rows from after it. The source itself
// is not written: no revision bump, no checkpoint, no row.
func (r *Repo) Fork(ctx context.Context, in ForkInput) (*workspaces.Workspace, error) {
	var out *workspaces.Workspace
	err := retrying(func() error {
		core, b, err := r.readBundle(ctx, in.SourceID)
		if err != nil {
			return err
		}
		b.Workspace.Name = in.Name
		doc, err := bundle.Encode(b)
		if err != nil {
			return fmt.Errorf("fork workspace %d: encode baseline: %w", in.SourceID, err)
		}
		blob, err := compressSnapshot(doc)
		if err != nil {
			return fmt.Errorf("fork workspace %d: %w", in.SourceID, err)
		}
		return r.db.Write(ctx, func(tx *sql.Tx) error {
			if _, ferr := fenceTx(ctx, tx, in.SourceID, core); ferr != nil {
				return ferr
			}
			now := time.Now().UTC()
			settings := b.Workspace.Settings
			source := in.SourceID
			ws, cerr := workspaces.CreateTx(ctx, tx, workspaces.CreateInput{
				Name: in.Name, Slug: in.Slug, OwnerID: in.OwnerID, SpecID: core.specID,
				Settings: &settings, ForkedFrom: &source,
			})
			if cerr != nil {
				return cerr
			}
			if _, aerr := applyBundleTx(ctx, tx, ws.ID, b, nil, now); aerr != nil {
				return aerr
			}
			if in.IncludeData {
				if err := copyEntitiesTx(ctx, tx, in.SourceID, ws.ID); err != nil {
					return err
				}
			}
			if err := copyAssetsTx(ctx, tx, in.SourceID, ws.ID); err != nil {
				return err
			}
			if err := copyScenariosTx(ctx, tx, in.SourceID, ws.ID); err != nil {
				return err
			}
			if berr := baselineCheckpointTx(ctx, tx, ws.ID, in.Label, blob, in.CreatedBy, now); berr != nil {
				return berr
			}
			// Re-read: the scenario pointer was set by copyScenariosTx
			// after CreateTx built the value it returned.
			var scenarioID sql.NullInt64
			if err := tx.QueryRowContext(ctx, "SELECT scenario_id FROM workspaces WHERE id = ?", ws.ID).Scan(&scenarioID); err != nil {
				return fmt.Errorf("fork: re-read workspace %d: %w", ws.ID, err)
			}
			if scenarioID.Valid {
				v := scenarioID.Int64
				ws.ScenarioID = &v
			}
			out = ws
			return nil
		})
	})
	if err != nil {
		return nil, err
	}
	return out, nil
}

// applyBundleTx is [Repo.rollbackTx]'s apply half over a workspace that
// was created in this same transaction: overrides, then the P3b resource
// statements, then the entity rows, then the custom endpoints — the
// rollback's own order, kept because [restoreEntitiesTx] can only resolve a
// family after [upsertResourcesTx] wrote it. Settings are not written here
// (CreateTx wrote them) and revision is not bumped: the row is at 1 and
// nothing has cached it.
func applyBundleTx(ctx context.Context, tx *sql.Tx, workspaceID int64, b bundle.Bundle, data *bundle.DataBundle, now time.Time) (int, error) {
	if err := overrides.ReplaceAllTx(ctx, tx, workspaceID, overrideRowsFromBundle(b), now); err != nil {
		return 0, err
	}
	if err := upsertResourcesTx(ctx, tx, workspaceID, b.Resources); err != nil {
		return 0, err
	}
	live, err := liveResourceFamiliesTx(ctx, tx, workspaceID, b)
	if err != nil {
		return 0, err
	}
	if err := upsertDecisionsTx(ctx, tx, workspaceID, b.Decisions, live); err != nil {
		return 0, err
	}
	restored := 0
	if data != nil {
		restored, err = restoreEntitiesTx(ctx, tx, workspaceID, *data)
		if err != nil {
			return 0, err
		}
	}
	endpointRows, err := endpointRowsFromBundle(b)
	if err != nil {
		return 0, fmt.Errorf("import endpoints for workspace %d: %w", workspaceID, err)
	}
	if err := customep.ReplaceAllTx(ctx, tx, workspaceID, endpointRows, now); err != nil {
		return 0, err
	}
	return restored, nil
}

// baselineCheckpointTx writes the new workspace's first history row: kind
// manual (retention prunes machine-made rows only, and this one is the
// state the workspace was born in), config_snap the document that was just
// applied, data_snap captured from the rows that were just written — the
// same [captureEntitiesTx], with the same degrade to NULL over budget.
func baselineCheckpointTx(ctx context.Context, tx *sql.Tx, workspaceID int64, label string, blob []byte, createdBy int64, now time.Time) error {
	dataBlob, _, err := captureEntitiesTx(ctx, tx, workspaceID)
	if err != nil {
		return err
	}
	_, err = insertCheckpointTx(ctx, tx, workspaceID, KindManual, label, blob, dataBlob, createdBy, now)
	return err
}

// copyEntitiesTx copies every entity row of the source into the copy,
// family by family through route_family (never through resources.id: the
// copy's rows were minted by upsertResourcesTx a moment ago and have their
// own ids), and raises each family's seq to its highest copied key — R18's
// rule — because the source's `seq` in the bundle was read on the reader
// pool and an anonymous POST X may have moved it since.
func copyEntitiesTx(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entities (resource_id, parent_entity_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
		SELECT nr.id, NULL, e.base_scope_key, e.scope_key, e.entity_key, e.data, e.created_at, e.updated_at
		  FROM entities e
		  JOIN resources sr ON sr.id = e.resource_id
		  JOIN resources nr ON nr.workspace_id = ? AND nr.route_family = sr.route_family
		 WHERE sr.workspace_id = ?`, targetID, sourceID); err != nil {
		return fmt.Errorf("fork: copy entities %d -> %d: %w", sourceID, targetID, err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE resources SET seq = max(seq, COALESCE(
			(SELECT max(CAST(entity_key AS INTEGER)) FROM entities
			 WHERE resource_id = resources.id AND entity_key NOT GLOB '*[^0-9]*' AND length(entity_key) <= 18), 0))
		WHERE workspace_id = ?`, targetID); err != nil {
		return fmt.Errorf("fork: raise seq for workspace %d: %w", targetID, err)
	}
	return nil
}

// copyAssetsTx copies the source's assets — bytes included — so a bodyRef
// or an asset_url in the copied configuration resolves on the first
// request. Sizes and hashes are copied as stored; the copy's total is the
// source's total, which the source's own cap already admitted.
func copyAssetsTx(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO assets (workspace_id, name, media_type, size_bytes, sha256, data, created_at, updated_at)
		SELECT ?, name, media_type, size_bytes, sha256, data, created_at, updated_at
		  FROM assets WHERE workspace_id = ?`, targetID, sourceID); err != nil {
		return fmt.Errorf("fork: copy assets %d -> %d: %w", sourceID, targetID, err)
	}
	return nil
}

// copyScenariosTx copies the source's scenarios row by row — each copy
// gets its own edit_version from the NEW workspace's sequence (A3: the
// token is per workspace, and a value copied from the source would collide
// with nothing but would also prove nothing) — and re-points the copy's
// scenario_id at the row of the same name when the source has one active.
// A scenario's snapshot is P2d's clone shape: the bytes as stored, never
// re-read from the layer, which is what keeps this a copy and not a
// recapture.
func copyScenariosTx(ctx context.Context, tx *sql.Tx, sourceID, targetID int64) error {
	// Read into memory FIRST, closing the cursor before the first INSERT:
	// SQLite's single writer connection cannot run a write while a SELECT's
	// cursor is still open on it. The helper's defer is what closes it.
	list, err := readScenarioRowsTx(ctx, tx, sourceID)
	if err != nil {
		return err
	}
	var activeID sql.NullInt64
	if err := tx.QueryRowContext(ctx, "SELECT scenario_id FROM workspaces WHERE id = ?", sourceID).Scan(&activeID); err != nil {
		return fmt.Errorf("fork: read active scenario of workspace %d: %w", sourceID, err)
	}
	for _, s := range list {
		ver, err := store.AllocateEditVersion(ctx, tx, targetID)
		if err != nil {
			return fmt.Errorf("fork: allocate edit_version for workspace %d: %w", targetID, err)
		}
		res, err := tx.ExecContext(ctx,
			"INSERT INTO scenarios (workspace_id, name, snapshot, created_at, edit_version) VALUES (?, ?, ?, ?, ?)",
			targetID, s.name, s.snapshot, s.createdAt, ver)
		if err != nil {
			return fmt.Errorf("fork: copy scenario %q into workspace %d: %w", s.name, targetID, err)
		}
		if activeID.Valid && activeID.Int64 == s.id {
			newID, err := res.LastInsertId()
			if err != nil {
				return fmt.Errorf("fork: scenario id: %w", err)
			}
			if _, err := tx.ExecContext(ctx, "UPDATE workspaces SET scenario_id = ? WHERE id = ?", newID, targetID); err != nil {
				return fmt.Errorf("fork: activate scenario %q on workspace %d: %w", s.name, targetID, err)
			}
		}
	}
	return nil
}

// scenarioRow is one source scenario as copyScenariosTx needs it: the id
// (to recognise the active one), the name, the snapshot bytes as stored.
type scenarioRow struct {
	id        int64
	name      string
	snapshot  []byte
	createdAt int64
}

// readScenarioRowsTx reads and CLOSES — see copyScenariosTx for why the
// close must precede the inserts.
func readScenarioRowsTx(ctx context.Context, tx *sql.Tx, workspaceID int64) ([]scenarioRow, error) {
	rows, err := tx.QueryContext(ctx,
		"SELECT id, name, snapshot, created_at FROM scenarios WHERE workspace_id = ? ORDER BY id", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("fork: read scenarios of workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()
	var list []scenarioRow
	for rows.Next() {
		var s scenarioRow
		if err := rows.Scan(&s.id, &s.name, &s.snapshot, &s.createdAt); err != nil {
			return nil, fmt.Errorf("fork: scan scenario of workspace %d: %w", workspaceID, err)
		}
		list = append(list, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("fork: read scenarios of workspace %d: %w", workspaceID, err)
	}
	return list, nil
}
