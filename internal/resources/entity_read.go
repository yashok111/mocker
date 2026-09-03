// Entity reads: the row type, its scanner and the four read shapes the mock
// plane and the admin plane take. Split out of repo.go 2026-09-03; the text
// is unchanged.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
)

// CountEntities is the ADMIN side's own read: how many rows the family holds
// across EVERY scope. It exists because EntityStore.List is scope-scoped and
// this count is not (P3e D6.2) — SELECT COUNT(*) on the reader pool, and NOT
// part of the EntityStore interface, which the mock plane owns.
//
// THREE near-identical names now live in this package at three different
// grains, and picking the wrong one is a silent wrong number: this one is per
// RESOURCE across every scope; countEntitiesTx (reset.go) is per WORKSPACE and
// unexported; entityCount (repo_test.go) is a test helper. The doc comment
// says the grain because the name cannot.
func (r *Repo) CountEntities(ctx context.Context, resourceID int64) (int64, error) {
	var n int64
	if err := r.db.R.QueryRowContext(ctx, "SELECT COUNT(*) FROM entities WHERE resource_id = ?", resourceID).Scan(&n); err != nil {
		return 0, fmt.Errorf("count entities for resource %d: %w", resourceID, err)
	}
	return n, nil
}

// Entity is one entities row.
type Entity struct {
	ID             int64
	ResourceID     int64
	ParentEntityID *int64
	// BaseScopeKey is the declared basePath-parameter value tuple this row
	// belongs to, encoded through [EncodeScope] exactly as ScopeKey is — ""
	// for a workspace whose basePath carries no parameter (D3.1's
	// empty-tuple encoding), which is every row this build wrote before
	// P3h and every row of a workspace that never parameterises basePath.
	// [Repo.List] does not surface it (a caller of that method always
	// pins base itself, by the same value it filters on); [Repo.ListFiltered]
	// does, because a row a wildcarded base filter returns is otherwise
	// unable to say which base scope it came from (D12).
	BaseScopeKey string
	// ScopeKey is the ordered outer path-parameter VALUES of a nested
	// family's row, encoded through [EncodeScope] — "" for a family with no
	// outer parameter (D3.3, the encoding of the empty tuple), which is
	// every row of every family this build derives with no "{}" segment.
	ScopeKey string
	// EntityKey is the DECIMAL STRING form of the entity's allocated seq
	// (R15) — never a client-supplied value, and never re-derived from
	// Data[idField]: the seq IS the identity, id_field is only which JSON
	// property carries it in the body (R35). A GET/DELETE by id therefore
	// always looks this column up by the same decimal string the id was
	// minted as, regardless of id_field or id_type.
	EntityKey string
	// Data is the stored JSON object, already carrying the forced id
	// (R35/R23) — raw bytes, never round-tripped through a second decode
	// on the read path, so a caller (the mock plane's collection/detail
	// assembly) can embed it directly.
	Data      jsonx.RawMessage
	CreatedAt time.Time
	UpdatedAt time.Time
}

// --- the four-method entity store ------------------------------------

// scanEntity scans one entities row.
func scanEntity(row interface{ Scan(dest ...any) error }) (Entity, error) {
	var (
		e         Entity
		parent    sql.NullInt64
		data      string
		createdAt int64
		updatedAt int64
	)
	if err := row.Scan(&e.ID, &e.ResourceID, &parent, &e.BaseScopeKey, &e.ScopeKey, &e.EntityKey, &data, &createdAt, &updatedAt); err != nil {
		return Entity{}, err
	}
	if parent.Valid {
		v := parent.Int64
		e.ParentEntityID = &v
	}
	e.Data = jsonx.RawMessage(data)
	e.CreatedAt = time.Unix(createdAt, 0).UTC()
	e.UpdatedAt = time.Unix(updatedAt, 0).UTC()
	return e, nil
}

// selectEntity is [Repo.List]/[Repo.Get]/[Repo.ListFiltered]'s shared column
// list. base_scope_key is included even though [Repo.List]'s callers already
// know the value they filtered on — [Repo.ListFiltered] (D12) wildcards
// either scope axis, and a row it returns must be able to say which base
// scope it came from; a second, narrower SELECT for List/Get would be a
// second place for the column list to drift out of sync with [scanEntity].
const selectEntity = "SELECT id, resource_id, parent_entity_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at FROM entities"

// resourceRowExists reports whether resourceID still names a row in the
// resources table, over q — the reader pool for List/Get's pre-query check
// below, or the write transaction itself for [Repo.Delete]'s authoritative,
// race-free one.
//
// This exists because List/Get/Delete otherwise query ONLY the entities
// table, which cannot on its own tell "resource still exists, entity
// legitimately absent" apart from "resource was declined, entities
// cascade-wiped with it" (0001_init.sql's `ON DELETE CASCADE` on
// entities.resource_id) — the exact distinction R37/D6 needs: a request
// parked in a session pause/delay (up to ~40s) can resume after an operator
// declines the family it is about to serve, and [resourceBranch]
// (internal/mockplane/resource.go) is written to treat ErrResourceGone from
// this package as "not taken over, fall through to the generator" at five
// call sites — a query that answered "0 rows" instead would read as a
// genuinely empty collection, or a genuinely missing entity, and the branch
// would answer its OWN 404/empty body rather than falling through, silently
// contradicting R37 without ever raising an error.
//
// Read on the reader pool via two round trips (this check, then the
// entities query) rather than one snapshot-consistent query: the residual
// window — a decline landing on the writer between the two — is the same
// accepted race R37 itself exists for (a parked request can already observe
// a decline mid-flight), not a new one this function introduces.
func (r *Repo) resourceRowExists(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, resourceID int64) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM resources WHERE id = ?", resourceID).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check resource %d exists: %w", resourceID, err)
	}
}

// List returns every entity of resourceID IN base and scope, ordered by
// insertion (id ASC) — the same order [entities_list]'s index gives. base
// is D18.2's own inserted parameter, ahead of scope: "" for a workspace
// whose basePath carries no parameter, exactly the value every row already
// held before P3h (D3.1's empty-tuple encoding). scope is "" for a
// top-level family's one implicit route scope; a caller that wants every
// scope of a nested family (the admin roster screen, D6.2) uses
// [Repo.CountEntities] instead, never this method with a fan-out over
// scopes. ErrResourceGone (via [Repo.resourceRowExists]) when the
// resources row itself is gone — R37's "declined out from under a parked
// request" case, never a bare empty slice, which is reserved for a scope
// that legitimately has zero entities.
func (r *Repo) List(ctx context.Context, resourceID int64, base, scope ScopeKey) ([]Entity, error) {
	exists, err := r.resourceRowExists(ctx, r.db.R, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list entities for resource %d: %w", resourceID, err)
	}
	if !exists {
		return nil, fmt.Errorf("list entities for resource %d: %w", resourceID, ErrResourceGone)
	}

	rows, err := r.db.R.QueryContext(ctx,
		selectEntity+" WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? ORDER BY id ASC",
		resourceID, string(base), string(scope))
	if err != nil {
		return nil, fmt.Errorf("list entities for resource %d: %w", resourceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan entity row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate entities for resource %d: %w", resourceID, err)
	}
	return out, nil
}

// ListFiltered returns a page of resourceID's entity rows, ordered by id
// ASC, for D4's structured admin read (`GET
// .../resources/{family}/entities`) — the read [Repo.List] cannot serve,
// because that method requires an EXACT (base, scope) pair and has no
// cursor or limit at all.
//
// base and scope are independently optional: a nil pointer means "any base
// scope" / "any scope" and is therefore left OUT of the WHERE entirely — a
// non-nil pointer, including one pointing at "", pins that axis to the
// exact value it holds (the empty base/scope tuple is itself a real,
// addressable scope, not the absence of a filter). Each present filter adds
// its own predicate to the SQL rather than post-filtering rows in Go: the
// two together are what lets a wildcarded call still use the `entities_list`
// index's leading columns (D4's own Shape), and what makes the filtering
// verifiable by reading the query rather than by reading a Go loop (D12's
// own Fails-if).
//
// afterID is an ID cursor, never optional: 0 means "from the start" (no
// entities.id is ever <= 0), and any other value means "strictly after this
// id" — entities.id, an INTEGER PRIMARY KEY, is what [Repo.List] already
// orders by, and comparing it directly is what D4's own Shape requires:
// entity_key is unpadded decimal TEXT, and a cursor built from it (a CAST
// included) reorders past the ninth row under SQLite's BINARY collation.
// limit bounds the page size; a caller reaching this method with a limit
// that has not already been clamped to a sane ceiling gets exactly that
// many rows back — clamping the value on the wire belongs to the caller
// (D4's own admin handler), not to this method's SQL.
//
// ErrResourceGone (via [Repo.resourceRowExists]) when the resources row
// itself is gone, exactly as [Repo.List] and [Repo.Get] already answer it —
// never a bare empty page, which is reserved for a page that legitimately
// has no more rows.
func (r *Repo) ListFiltered(ctx context.Context, resourceID int64, base, scope *ScopeKey, afterID int64, limit int) ([]Entity, error) {
	exists, err := r.resourceRowExists(ctx, r.db.R, resourceID)
	if err != nil {
		return nil, fmt.Errorf("list filtered entities for resource %d: %w", resourceID, err)
	}
	if !exists {
		return nil, fmt.Errorf("list filtered entities for resource %d: %w", resourceID, ErrResourceGone)
	}

	query := selectEntity + " WHERE resource_id = ?"
	args := []any{resourceID}
	if base != nil {
		query += " AND base_scope_key = ?"
		args = append(args, string(*base))
	}
	if scope != nil {
		query += " AND scope_key = ?"
		args = append(args, string(*scope))
	}
	query += " AND id > ? ORDER BY id ASC LIMIT ?"
	args = append(args, afterID, limit)

	rows, err := r.db.R.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list filtered entities for resource %d: %w", resourceID, err)
	}
	defer func() { _ = rows.Close() }()

	var out []Entity
	for rows.Next() {
		e, err := scanEntity(rows)
		if err != nil {
			return nil, fmt.Errorf("scan entity row: %w", err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate filtered entities for resource %d: %w", resourceID, err)
	}
	return out, nil
}

// Get returns the entity of resourceID whose EntityKey is entityKey (the
// decimal seq string — see [Entity.EntityKey]'s doc comment), IN base and
// scope, and whether it was found at all. ErrResourceGone (via
// [Repo.resourceRowExists]) when the resources row itself is gone — found
// stays false in that case too, but the caller must check the error first
// (R37: a vanished resource falls through to the generator, a merely-absent
// entity answers 404).
func (r *Repo) Get(ctx context.Context, resourceID int64, base, scope ScopeKey, entityKey string) (Entity, bool, error) {
	exists, err := r.resourceRowExists(ctx, r.db.R, resourceID)
	if err != nil {
		return Entity{}, false, fmt.Errorf("get entity %q for resource %d: %w", entityKey, resourceID, err)
	}
	if !exists {
		return Entity{}, false, fmt.Errorf("get entity %q for resource %d: %w", entityKey, resourceID, ErrResourceGone)
	}

	row := r.db.R.QueryRowContext(ctx,
		selectEntity+" WHERE resource_id = ? AND base_scope_key = ? AND scope_key = ? AND entity_key = ?",
		resourceID, string(base), string(scope), entityKey)
	e, err := scanEntity(row)
	switch {
	case err == nil:
		return e, true, nil
	case errors.Is(err, sql.ErrNoRows):
		return Entity{}, false, nil
	default:
		return Entity{}, false, fmt.Errorf("get entity %q for resource %d: %w", entityKey, resourceID, err)
	}
}
