package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonx"
)

// Set (A11, 2026-09-02) is the admin plane's own write into a confirmed
// family: create-or-replace ONE row by its natural address (base, scope,
// entityKey). It is the operator's and the agent's "give user 42 status
// blocked", the one thing the mock plane's anonymous POST/DELETE could
// not express — POST mints the NEXT key, never a chosen one, and nothing
// on either plane replaced a row's data until now.
//
// Three rules keep it consistent with the rows Create mints and a
// checkpoint restores:
//
//   - data[idField] is OVERWRITTEN with the key, coerced to the family's
//     id type (gen.CoerceIDValue), exactly as Create overwrites it with
//     the allocated seq (R23): the key IS the identity, the id field is
//     only where the body shows it, and a body that disagreed with its
//     own address would serve one id under another.
//   - resources.seq is raised to MAX(seq, key) when the key is a decimal
//     integer — the identical rule restoreEntitiesTx applies after a
//     checkpoint restore — so the mock plane's next POST can never mint
//     a key an operator already placed and collide on the unique tuple.
//   - the two caps Create enforces (rows per family, bytes per family and
//     per entity) apply here too, the byte totals computed against the
//     row being REPLACED when one exists, so replacing a big row with a
//     small one is never refused for the size of the row it removes.
//
// Deliberately NOT here: validation against entity_schema (the mock
// plane's own POST does not validate either — R23 takes the body as
// given), the ancestor walk a nested family's serve path does (an admin
// write may place a row under a scope no live ancestor anchors; it is then
// unreachable until one does, and entityCount counts it — the same
// observable-orphan rule the nesting paragraph of CLAUDE.md already
// states for a rollback), and a revision bump (D13 clause 23: an entity
// write changes nothing the runtime cache keys on).
//
// created reports whether the row was inserted (true) or replaced.
func (r *Repo) Set(ctx context.Context, resourceID int64, base, scope ScopeKey, entityKey, idField, idType string, data map[string]any) (Entity, bool, error) {
	body, merr := r.keyedBody(resourceID, entityKey, idField, idType, data)
	if merr != nil {
		return Entity{}, false, merr
	}
	totalCap := r.entityByteCap()

	callerCtx := ctx
	wctx, cancel := context.WithTimeout(ctx, writeDeadline)
	defer cancel()

	now := time.Now().UTC()
	var out Entity
	var created bool
	writeErr := r.db.Write(wctx, func(tx *sql.Tx) error {
		exists, err := r.resourceRowExists(wctx, tx, resourceID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrResourceGone
		}

		var count int64
		var total sql.NullInt64
		if err := tx.QueryRowContext(wctx,
			"SELECT COUNT(*), SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID,
		).Scan(&count, &total); err != nil {
			return fmt.Errorf("read entity totals for resource %d: %w", resourceID, err)
		}

		var oldLen sql.NullInt64
		var oldCreated sql.NullInt64
		err = tx.QueryRowContext(wctx,
			"SELECT LENGTH(data), created_at FROM entities WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
			resourceID, string(base), string(scope), entityKey,
		).Scan(&oldLen, &oldCreated)
		switch {
		case err == nil:
			if total.Int64-oldLen.Int64+int64(len(body)) > totalCap {
				return ErrEntityLimit
			}
			if _, err := tx.ExecContext(wctx,
				"UPDATE entities SET data = ?, updated_at = ? WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
				string(body), now.Unix(), resourceID, string(base), string(scope), entityKey); err != nil {
				return fmt.Errorf("replace entity %q on resource %d: %w", entityKey, resourceID, err)
			}
		case errors.Is(err, sql.ErrNoRows):
			if count+1 > r.maxEntityRows {
				return ErrEntityLimit
			}
			if total.Int64+int64(len(body)) > totalCap {
				return ErrEntityLimit
			}
			if err := insertKeyedEntityTx(wctx, tx, resourceID, base, scope, entityKey, body, now); err != nil {
				return err
			}
			created = true
			oldCreated = sql.NullInt64{Int64: now.Unix(), Valid: true}
		default:
			return fmt.Errorf("read entity %q on resource %d: %w", entityKey, resourceID, err)
		}

		if n, perr := strconv.ParseInt(entityKey, 10, 64); perr == nil && n > 0 {
			if _, err := tx.ExecContext(wctx,
				"UPDATE resources SET seq = MAX(seq, ?) WHERE id = ?", n, resourceID); err != nil {
				return fmt.Errorf("raise seq for resource %d: %w", resourceID, err)
			}
		}

		id, err := lastEntityID(wctx, tx, resourceID, base, scope, entityKey)
		if err != nil {
			return err
		}
		out = Entity{
			ID: id, ResourceID: resourceID, BaseScopeKey: string(base), ScopeKey: string(scope), EntityKey: entityKey,
			Data: jsonx.RawMessage(body), CreatedAt: time.Unix(oldCreated.Int64, 0).UTC(), UpdatedAt: now,
		}
		return nil
	})
	if writeErr != nil {
		if writeBusyIfOurDeadline(callerCtx, writeErr) {
			return Entity{}, false, fmt.Errorf("set entity %q on resource %d: %w", entityKey, resourceID, ErrWriteBusy)
		}
		return Entity{}, false, writeErr
	}
	return out, created, nil
}

// allocateSeq mints resourceID's next seq (R15's own shape:
// internal/store.AllocateEditVersion's UPDATE ... RETURNING pattern, but
// on resources.seq, which is NOT an edit_version and carries no
// compare-and-swap semantics — it is a pure counter). ErrResourceGone when
// resourceID no longer names a row (0 rows updated): a family declined out
// from under an in-flight POST.
// Patch is A19's `mock.entities.update`: a SHALLOW merge of patch over the
// stored row, read, merged and written inside ONE write transaction. It is
// its own method and not Get-then-Set at the caller because that pair is a
// read-modify-write with the read outside the writer: two concurrent
// requests both read the old row and the second Set discards the first's
// patch, and a row deleted between the two is RESURRECTED by Set's
// create-or-replace — `update` is exactly the "flip a status, bump a
// counter" primitive, and a primitive that loses one of two increments is
// not one. Here the row is read under the single writer connection, so the
// merge sees the latest data and a vanished row is `found == false`, never a
// new row.
//
// What it shares with Set: keyedBody (the canonical-key check, the id
// field pinned to entityKey whatever patch says, the per-entity byte cap)
// and the family-wide byte total computed against the row being REPLACED.
// What it does not do: insert (not found is the answer), raise seq (the key
// already exists), validate against entity_schema (neither door does, R23).
func (r *Repo) Patch(ctx context.Context, resourceID int64, base, scope ScopeKey, entityKey, idField, idType string, patch map[string]any) (Entity, bool, error) {
	totalCap := r.entityByteCap()

	callerCtx := ctx
	wctx, cancel := context.WithTimeout(ctx, writeDeadline)
	defer cancel()

	now := time.Now().UTC()
	var out Entity
	var found bool
	writeErr := r.db.Write(wctx, func(tx *sql.Tx) error {
		exists, err := r.resourceRowExists(wctx, tx, resourceID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrResourceGone
		}

		var (
			id         int64
			oldData    string
			oldCreated int64
		)
		err = tx.QueryRowContext(wctx,
			"SELECT id, data, created_at FROM entities WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
			resourceID, string(base), string(scope), entityKey,
		).Scan(&id, &oldData, &oldCreated)
		switch {
		case errors.Is(err, sql.ErrNoRows):
			return nil // found stays false
		case err != nil:
			return fmt.Errorf("read entity %q on resource %d: %w", entityKey, resourceID, err)
		}
		found = true

		merged := map[string]any{}
		if err := jsonx.Unmarshal([]byte(oldData), &merged); err != nil {
			return fmt.Errorf("decode entity %q on resource %d: %w", entityKey, resourceID, err)
		}
		for k, v := range patch {
			merged[k] = v
		}
		body, merr := r.keyedBody(resourceID, entityKey, idField, idType, merged)
		if merr != nil {
			return merr
		}

		var total sql.NullInt64
		if err := tx.QueryRowContext(wctx,
			"SELECT SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID,
		).Scan(&total); err != nil {
			return fmt.Errorf("read entity totals for resource %d: %w", resourceID, err)
		}
		if total.Int64-int64(len(oldData))+int64(len(body)) > totalCap {
			return ErrEntityLimit
		}
		if _, err := tx.ExecContext(wctx,
			"UPDATE entities SET data = ?, updated_at = ? WHERE id = ?",
			string(body), now.Unix(), id); err != nil {
			return fmt.Errorf("patch entity %q on resource %d: %w", entityKey, resourceID, err)
		}
		out = Entity{
			ID: id, ResourceID: resourceID, BaseScopeKey: string(base), ScopeKey: string(scope), EntityKey: entityKey,
			Data: jsonx.RawMessage(body), CreatedAt: time.Unix(oldCreated, 0).UTC(), UpdatedAt: now,
		}
		return nil
	})
	if writeErr != nil {
		if writeBusyIfOurDeadline(callerCtx, writeErr) {
			return Entity{}, false, fmt.Errorf("patch entity %q on resource %d: %w", entityKey, resourceID, ErrWriteBusy)
		}
		return Entity{}, false, writeErr
	}
	return out, found, nil
}

func allocateSeq(ctx context.Context, tx *sql.Tx, resourceID int64) (int64, error) {
	var seq int64
	err := tx.QueryRowContext(ctx, "UPDATE resources SET seq = seq + 1 WHERE id = ? RETURNING seq", resourceID).Scan(&seq)
	switch {
	case err == nil:
		return seq, nil
	case errors.Is(err, sql.ErrNoRows):
		return 0, fmt.Errorf("resource %d: %w", resourceID, ErrResourceGone)
	default:
		return 0, fmt.Errorf("allocate seq for resource %d: %w", resourceID, err)
	}
}

// Create is R15/R25/R34's whole POST path: ONE write transaction, bounded
// by writeDeadline, that is the only place the caps, the allocation and
// the insert meet. data is MUTATED in place (data[idField] is overwritten
// — R23): the caller must not reuse it afterward expecting the
// client-supplied value to survive.
//
// Order, deliberately literal (D13 clause 32's concurrency guarantee comes
// from the writer pool being one connection, not from lock discipline
// here): re-read the row count and stored byte total, refuse over either
// cap using the AS-GIVEN body's size, allocate seq, overwrite the id field,
// marshal, and re-check the per-entity cap on the FINAL bytes — R23's
// narrow band, where the id overwrite grows a body that was within the
// capture cap into one that no longer is.
func (r *Repo) Create(ctx context.Context, resourceID int64, base, scope ScopeKey, idField, idType string, data map[string]any) (Entity, error) {
	preBody, merr := jsonx.Marshal(data)
	if merr != nil {
		return Entity{}, fmt.Errorf("create entity on resource %d: marshal: %w", resourceID, merr)
	}
	perCap := r.perEntityByteCap()
	totalCap := r.entityByteCap()

	callerCtx := ctx
	wctx, cancel := context.WithTimeout(ctx, writeDeadline)
	defer cancel()

	now := time.Now().UTC()
	var out Entity
	writeErr := r.db.Write(wctx, func(tx *sql.Tx) error {
		var count int64
		var total sql.NullInt64
		if err := tx.QueryRowContext(wctx,
			"SELECT COUNT(*), SUM(LENGTH(data)) FROM entities WHERE resource_id = ?", resourceID,
		).Scan(&count, &total); err != nil {
			return fmt.Errorf("read entity totals for resource %d: %w", resourceID, err)
		}
		if count+1 > r.maxEntityRows {
			return ErrEntityLimit
		}
		if total.Int64+int64(len(preBody)) > totalCap {
			return ErrEntityLimit
		}

		seq, err := allocateSeq(wctx, tx, resourceID)
		if err != nil {
			return err
		}

		data[idField] = gen.CoerceIDValue(strconv.FormatInt(seq, 10), idType, "")
		body, merr := jsonx.Marshal(data)
		if merr != nil {
			return fmt.Errorf("marshal entity for resource %d: %w", resourceID, merr)
		}
		if int64(len(body)) > perCap {
			return ErrEntityLimit
		}

		entityKey := strconv.FormatInt(seq, 10)
		if _, err := tx.ExecContext(wctx, `
			INSERT INTO entities (resource_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?)`,
			resourceID, string(base), string(scope), entityKey, string(body), now.Unix(), now.Unix()); err != nil {
			return fmt.Errorf("insert entity for resource %d: %w", resourceID, err)
		}
		// P8c: the read-back is scoped too — an omission here rolls back
		// EVERY nested POST silently, with every refusal test still
		// green, because the INSERT above already succeeded and only
		// this SELECT's miss turns it into an error.
		id, err := lastEntityID(wctx, tx, resourceID, base, scope, entityKey)
		if err != nil {
			return err
		}

		out = Entity{
			ID: id, ResourceID: resourceID, BaseScopeKey: string(base), ScopeKey: string(scope), EntityKey: entityKey,
			Data: jsonx.RawMessage(body), CreatedAt: now, UpdatedAt: now,
		}
		return nil
	})
	if writeErr != nil {
		if writeBusyIfOurDeadline(callerCtx, writeErr) {
			return Entity{}, fmt.Errorf("create entity on resource %d: %w", resourceID, ErrWriteBusy)
		}
		return Entity{}, writeErr
	}
	return out, nil
}

// lastEntityID re-reads the row Create just inserted, by its own natural
// key, to report a real database id rather than trusting
// sql.Result.LastInsertId across the modernc.org/sqlite driver — the same
// caution [internal/overrides] and [internal/scenarios] already apply to
// their own inserts under this driver.
func lastEntityID(ctx context.Context, tx *sql.Tx, resourceID int64, base, scope ScopeKey, entityKey string) (int64, error) {
	var id int64
	if err := tx.QueryRowContext(ctx,
		"SELECT id FROM entities WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
		resourceID, string(base), string(scope), entityKey,
	).Scan(&id); err != nil {
		return 0, fmt.Errorf("read back entity id for resource %d base %q scope %q key %q: %w", resourceID, base, scope, entityKey, err)
	}
	return id, nil
}

// Delete removes the entity of resourceID whose EntityKey is entityKey,
// bounded by writeDeadline exactly like Create. Deliberately does NOT bump
// workspaces.revision (D13 clause 23): an entity write changes nothing the
// runtime cache keys on. ok reports whether a row was actually deleted —
// false for both "never existed" and "already deleted", the same
// not-found the mock plane answers today; ErrResourceGone (checked INSIDE
// this call's own write transaction — the writer pool is one connection, so
// nothing can decline the family between this check and the DELETE below)
// when resourceID no longer names a row at all — R37's case, which
// [resourceServeDelete] must be able to tell apart from an ordinary
// not-found.
func (r *Repo) Delete(ctx context.Context, resourceID int64, base, scope ScopeKey, entityKey string) (bool, error) {
	callerCtx := ctx
	wctx, cancel := context.WithTimeout(ctx, writeDeadline)
	defer cancel()

	var deleted bool
	writeErr := r.db.Write(wctx, func(tx *sql.Tx) error {
		exists, err := r.resourceRowExists(wctx, tx, resourceID)
		if err != nil {
			return err
		}
		if !exists {
			return ErrResourceGone
		}

		res, err := tx.ExecContext(wctx,
			"DELETE FROM entities WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
			resourceID, string(base), string(scope), entityKey)
		if err != nil {
			return fmt.Errorf("delete entity %q on resource %d: %w", entityKey, resourceID, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return fmt.Errorf("rows affected: %w", err)
		}
		deleted = n > 0
		return nil
	})
	if writeErr != nil {
		if writeBusyIfOurDeadline(callerCtx, writeErr) {
			return false, fmt.Errorf("delete entity %q on resource %d: %w", entityKey, resourceID, ErrWriteBusy)
		}
		if errors.Is(writeErr, ErrResourceGone) {
			return false, fmt.Errorf("delete entity %q on resource %d: %w", entityKey, resourceID, ErrResourceGone)
		}
		return false, writeErr
	}
	return deleted, nil
}

// ErrEntityKeyConflict is Set's answer when the chosen key already exists
// in ANOTHER base scope of the family: the schema's UNIQUE does not span
// base_scope_key (see the comment at the insert), so the row cannot be
// stored, and the caller answers 409 rather than 500.
var ErrEntityKeyConflict = errors.New("resources: entity key already exists in another base scope of this family")

// ErrEntityKeyNotCanonical is Set's answer when the key is not the exact
// canonical rendering of a value of the family's id type: "abc" or "007"
// for an integer family. Set writes data[idField] from the key through
// gen.CoerceIDValue, which HASHES an unparsable key into a plausible id
// rather than failing (that is the right behaviour for a mock-plane
// detail route, where the URL is the client's), so without this check a
// stored row's key and its id would silently disagree and A11's "the key
// IS the identity" would be false for it.
var ErrEntityKeyNotCanonical = errors.New("resources: entity key is not the canonical form of the family's id type")

// CanonicalEntityKey reports whether key round-trips through the family's
// id type unchanged — parse, render, compare. A string family accepts any
// key the route's own segment rules already admitted.
func CanonicalEntityKey(key, idType string) bool {
	switch idType {
	case "integer":
		n, err := strconv.ParseInt(key, 10, 64)
		return err == nil && strconv.FormatInt(n, 10) == key
	case "number":
		f, err := strconv.ParseFloat(key, 64)
		return err == nil && strconv.FormatFloat(f, 'g', -1, 64) == key
	case "boolean":
		return key == "true" || key == "false"
	default:
		return true
	}
}

// isUniqueViolation reports whether err is a UNIQUE constraint failure —
// the same substring check internal/workspaces and internal/customep keep,
// copied rather than shared for the reason each gives: no package imports
// another for a one-line helper over the driver's error text.
func isUniqueViolation(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// keyedBody is Set's pre-transaction half: the key must be the canonical
// form of the family's id type (ErrEntityKeyNotCanonical), the id field is
// overwritten from it, and the body is measured against the per-row cap.
func (r *Repo) keyedBody(resourceID int64, entityKey, idField, idType string, data map[string]any) ([]byte, error) {
	if !CanonicalEntityKey(entityKey, idType) {
		return nil, ErrEntityKeyNotCanonical
	}
	data[idField] = gen.CoerceIDValue(entityKey, idType, "")
	body, err := jsonx.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("set entity %q on resource %d: marshal: %w", entityKey, resourceID, err)
	}
	if int64(len(body)) > r.perEntityByteCap() {
		return nil, ErrEntityLimit
	}
	return body, nil
}

// insertKeyedEntityTx is Set's insert. The table's UNIQUE is (resource_id,
// scope_key, entity_key) — 0003 deliberately left base_scope_key out of it
// on the premise that one counter per family can never mint the same key
// twice. A11 let the operator CHOOSE the key, so the same key under two
// declared base values collides here, and the caller needs a 409
// (ErrEntityKeyConflict), not a 500.
func insertKeyedEntityTx(ctx context.Context, tx *sql.Tx, resourceID int64, base, scope ScopeKey, entityKey string, body []byte, now time.Time) error {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO entities (resource_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		resourceID, string(base), string(scope), entityKey, string(body), now.Unix(), now.Unix()); err != nil {
		if isUniqueViolation(err) {
			return ErrEntityKeyConflict
		}
		return fmt.Errorf("insert entity %q on resource %d: %w", entityKey, resourceID, err)
	}
	return nil
}
