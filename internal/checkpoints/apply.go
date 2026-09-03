// Apply: the write half of a rollback, a reset and an import — each layer's
// restore step in the one order that works (see rollbackTx). Split out of
// repo.go 2026-09-03; the text is unchanged.
package checkpoints

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/store"
)

// writeSettingsTx restores workspaces.settings WHOLESALE — the full
// domain.Settings value the bundle carries (bundle.go:125-133), replacing
// what is there, never merged over it.
//
// It ALLOCATES a fresh edit_version from the workspace's own edit_seq (D4,
// D9): this is a bare `UPDATE workspaces` that cannot go through
// workspaces.Repo.Update (see the comment three paragraphs down — that verb
// opens its own db.Write and would deadlock the single-writer pool), yet it
// writes `settings`, which IS the field PATCH /api/workspaces/{id} guards.
// Without the allocation here, a PATCH caller holding a pre-rollback token
// would pass the compare-and-swap check against the just-restored row and
// silently overwrite what the rollback just put back — the exact failure
// D9 names this site BY NAME to close.
//
// It deliberately does NOT call Settings.Normalize(). Two justifications
// were offered during the gate and BOTH are unreachable: the fresh signing
// key Normalize mints only when the field is EMPTY, which no snapshot's can
// be, and "yesterday's clamps", which cannot happen either because the
// settings were normalized at CAPTURE by the domain.ParseSettings this
// snapshot was built from. The choice is unobservable, so it is made on the
// cheaper rule — a package that does not own the normalization policy
// should not carry a second copy of the decision to apply it, and every
// reader of the column re-normalizes on the way out anyway.
//
// The 64 KiB maxSettingsJSON cap is not re-applied either: the constant is
// unexported in internal/workspaces, duplicating a policy number is a seam,
// and these bytes were capped when they were written.
//
// THREE FIELDS make a wholesale restore consequential, and none is visible
// in a list of labels: basePath — rolling back to a checkpoint taken at
// another prefix RELOCATES EVERY ROUTE, exactly what
// mockplane/scenario.go:144-149 refuses to let a SCENARIO do — auth.signingKey,
// where restoring a historical key invalidates every token the frontend
// under test is holding, and, since P3h, basePathValues (D9): it travels
// with basePath because the two are one configuration, and restoring the
// prefix without the declared values it takes would produce a workspace
// whose every resource-served request refuses with base_scope_undeclared
// until an operator notices and re-declares them by hand. This function
// does not carry basePathValues separately — [domain.Settings] already
// does, and MarshalJSONStable below serializes the struct whole, so the
// field rides in for free; naming it here is for the reader deciding
// whether "wholesale" quietly dropped a field, not because the code needs
// a second line. The confirmation copy names all three.
func writeSettingsTx(ctx context.Context, tx *sql.Tx, workspaceID int64, settings domain.Settings, now time.Time) error {
	settingsJSON, err := settings.MarshalJSONStable()
	if err != nil {
		return fmt.Errorf("marshal restored settings for workspace %d: %w", workspaceID, err)
	}
	ver, err := store.AllocateEditVersion(ctx, tx, workspaceID)
	if err != nil {
		return fmt.Errorf("allocate edit_version for workspace %d: %w", workspaceID, err)
	}
	if _, err := tx.ExecContext(ctx,
		"UPDATE workspaces SET settings = ?, edit_version = ?, updated_at = ? WHERE id = ?",
		string(settingsJSON), ver, now.Unix(), workspaceID,
	); err != nil {
		return fmt.Errorf("restore settings for workspace %d: %w", workspaceID, err)
	}
	return nil
}

// upsertResourcesTx restores the `resources` rows a snapshot names, and it
// NEVER DELETES ONE. That is the whole of P3b's cascade fix, and it is a
// rule about a STATEMENT rather than about an intention: entities.resource_id
// is ON DELETE CASCADE (0001_init.sql:150), so a single
// `DELETE FROM resources WHERE workspace_id = ?` inside this transaction —
// the DELETE-then-UPSERT shape both ReplaceAllTx calls around it use, and
// therefore the shape an implementer copies by default — destroys every
// entity row of every family it touches. config_snap alone holds nothing
// to put them back with, and — since P3d, where data_snap sometimes does —
// the rule is unweakened rather than made conditional: this function runs
// BEFORE [restoreEntitiesTx] in the same transaction (see [Repo.rollbackTx]
// on why the ordering is fixed), so a DELETE here would destroy rows the
// very next step is about to insert, restoreData true or false. Rows the
// snapshot does not name are left standing.
//
// Two consequences follow and neither is a side effect. A family confirmed
// AFTER the target checkpoint SURVIVES the rollback, where an override made
// after it would be deleted — an override that comes back is regenerated
// from the spec, and an entity set that goes away is gone regardless of
// restoreData (the checkpoint predates the family, so its data_snap has no
// opinion on it either). And a family DECLINED after the checkpoint comes
// back CONFIGURED and EMPTY on a restoreData:false rollback — its
// `resources` row is restored while the decline's own cascade already
// destroyed its entities, and this slice brings nothing back without being
// asked to. With restoreData: true and a target checkpoint captured before
// the decline, [restoreEntitiesTx] undoes exactly that gap: the family's
// row is restored here, and its rows are restored there, in the same
// transaction. The rollback modal says all of this in so many words.
//
// The DO UPDATE list is TEN columns, and "every column the snapshot
// carries" is wrong in BOTH directions: [bundle.ResourceEntry] declares
// twelve fields, of which ParentFamily maps to no `resources` column at all
// (it is resource_suggestions', wire-only, and always null by the P3e/P3g
// D9 decision) and RouteFamily is the conflict TARGET. `confidence` is
// likewise a resource_suggestions column and naming it here would be
// invalid SQL. `parent_id` is left at whatever the live row has, which is
// NULL for everything this build writes — by design, not pending: see D9
// for why a live self-FK on entities would cost more than it buys, an
// argument P3g re-makes at depth rather than inheriting unread (D9.2).
//
// `seq` is the exception, and it is R18: an UPSERT writing the snapshot's
// value verbatim could set the counter BELOW a live entity key, after which
// the next POST X mints an id that already exists and violates
// UNIQUE (resource_id, scope_key, entity_key). That failure is SILENT, not
// a 500 — mockplane's write path logs the insert error, declines the
// takeover and serves a GENERATED 201, so the caller is told the write
// succeeded and no row exists. The restored value is therefore
// max(current, snapshot), computed IN SQL inside this transaction and never
// in Go over the pre-transaction capture: that capture was read on the
// reader pool, an anonymous POST X moves `seq` with no revision bump, and
// fenceTx's triple cannot see it move — so a max taken over that stale read
// lands below a live key exactly as writing the snapshot's value would.
func upsertResourcesTx(ctx context.Context, tx *sql.Tx, workspaceID int64, entries []bundle.ResourceEntry) error {
	for _, e := range entries {
		scopeParams, err := jsonx.Marshal(e.ScopeParams)
		if err != nil {
			return fmt.Errorf("marshal scopeParams of %q for workspace %d: %w", e.RouteFamily, workspaceID, err)
		}
		if len(e.ScopeParams) == 0 {
			// The column is NOT NULL DEFAULT '[]' and internal/resources
			// decodes it on every read: a nil slice marshals to "null",
			// which is a legal string but not the empty ARRAY the rest of
			// the tree writes there.
			scopeParams = []byte("[]")
		}
		filterMap := string(e.FilterMap)
		if len(e.FilterMap) == 0 {
			filterMap = "{}"
		}
		var wrapper any
		if !isJSONNullBytes(e.Wrapper) {
			wrapper = string(e.Wrapper)
		}

		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resources
				(workspace_id, route_family, name, id_field, id_strategy, scope_params,
				 entity_schema, wrapper, filter_map, write_form, seq, seed_count)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT (workspace_id, route_family) DO UPDATE SET
				name          = excluded.name,
				id_field      = excluded.id_field,
				id_strategy   = excluded.id_strategy,
				scope_params  = excluded.scope_params,
				entity_schema = excluded.entity_schema,
				wrapper       = excluded.wrapper,
				filter_map    = excluded.filter_map,
				write_form    = excluded.write_form,
				seed_count    = excluded.seed_count,
				seq           = MAX(excluded.seq, resources.seq)`,
			workspaceID, e.RouteFamily, e.Name, e.IDField, e.IDStrategy, string(scopeParams),
			e.EntitySchema, wrapper, filterMap, e.WriteForm, e.Seq, e.SeedCount,
		); err != nil {
			return fmt.Errorf("restore resource %q for workspace %d: %w", e.RouteFamily, workspaceID, err)
		}
	}
	return nil
}

// liveResourceFamiliesTx re-reads, INSIDE the transaction and after
// [upsertResourcesTx] has run, which of the families the snapshot names in
// EITHER array currently have a `resources` row. It is the input to R17's
// rule in [upsertDecisionsTx], and it is deliberately narrowed to that
// union rather than reading the whole workspace: a family carried only in
// Decisions is by construction absent from Resources, and that population —
// declined in the snapshot, confirmed since — is precisely what the rule is
// about.
//
// One indexed lookup PER FAMILY, not one query with a built IN list: the
// list would be an SQL string assembled from a slice length (gosec G202,
// and this tree fixes those rather than annotating them), while the union
// is bounded by the families one spec derives and every probe here is a
// point read of UNIQUE (workspace_id, route_family) on a connection this
// transaction already holds.
//
// Only presence is read, never the row id: the rule asks "does this family
// have a resources row right now", and nothing downstream addresses the row
// by id.
func liveResourceFamiliesTx(ctx context.Context, tx *sql.Tx, workspaceID int64, b bundle.Bundle) (map[string]bool, error) {
	named := make(map[string]bool, len(b.Resources)+len(b.Decisions))
	for _, e := range b.Resources {
		named[e.RouteFamily] = true
	}
	for _, e := range b.Decisions {
		named[e.RouteFamily] = true
	}

	live := make(map[string]bool, len(named))
	for family := range named {
		var one int
		err := tx.QueryRowContext(ctx,
			"SELECT 1 FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, family).Scan(&one)
		switch {
		case err == nil:
			live[family] = true
		case errors.Is(err, sql.ErrNoRows):
		default:
			return nil, fmt.Errorf("re-read resource family %q of workspace %d inside transaction: %w",
				family, workspaceID, err)
		}
	}
	return live, nil
}

// decisionDeclined is the one resource_decisions.state value this package
// branches on. It is not a vocabulary this package owns — internal/resources
// writes both values (0001_init.sql:114's own comment lists them) — and the
// restore deliberately does NOT validate the column against an enumeration
// here: a state it does not recognise is written back verbatim, because the
// owning table is the authority on what is legal and a second copy of that
// list would be a driftable one. The only thing this constant decides is
// which decision R17 suppresses.
const decisionDeclined = "declined"

// upsertDecisionsTx restores the `resource_decisions` rows a snapshot
// carries — but only where each AGREES with the resource half (R17).
//
// Restoring them unconditionally by their key would recreate the very state
// carrying the two tables together exists to prevent: a snapshot taken
// while a family was DECLINED, an operator who then CONFIRMED it, and a
// rollback that writes `declined` back over a live `resources` row
// [upsertResourcesTx] is forbidden to delete. The workspace would render
// the family as declined while the confirm path answered
// `already_confirmed` and the mock plane went on serving it. So a
// snapshot's `declined` decision is NOT applied to a family that has a
// `resources` row right now.
//
// A `confirmed` decision has no such conflict — the resource half above has
// already put the row back, or the row was there all along — so it is
// written unconditionally.
func upsertDecisionsTx(ctx context.Context, tx *sql.Tx, workspaceID int64, entries []bundle.DecisionEntry, live map[string]bool) error {
	for _, e := range entries {
		if e.State == decisionDeclined && live[e.RouteFamily] {
			continue
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, ?, ?)
			ON CONFLICT (workspace_id, route_family) DO UPDATE SET state = excluded.state`,
			workspaceID, e.RouteFamily, e.State,
		); err != nil {
			return fmt.Errorf("restore resource decision %q for workspace %d: %w", e.RouteFamily, workspaceID, err)
		}
	}
	return nil
}

// restoreEntitiesTx is D6's one new restore step, run for every FAMILY in
// d, in order, after [upsertResourcesTx] and before customep.ReplaceAllTx
// (see [Repo.rollbackTx] for why both neighbours are load-bearing).
// restored counts the families it actually restored — the count
// [Outcome.DataRestored] is derived from ("ran for at least one carried
// family", never "wrote or deleted at least one row": D7 measured that a
// resolved family whose stored and live relations are already identical
// still executes step 3 and step 4 and changes nothing).
func restoreEntitiesTx(ctx context.Context, tx *sql.Tx, workspaceID int64, d bundle.DataBundle) (restored int, err error) {
	for _, f := range d.Families {
		// Step 1: resolve by (workspace_id, route_family) — a family is
		// addressed by its route path, never by resources.id (D4 of the
		// P3d decision document, the identical reason P3c's `ref` recipe
		// addresses a family by route_family). One point SELECT per
		// family, not an IN-list built from a slice length (gosec G202),
		// matching liveResourceFamiliesTx's own choice above.
		var resourceID int64
		err := tx.QueryRowContext(ctx,
			"SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, f.RouteFamily,
		).Scan(&resourceID)
		switch {
		case err == nil:
		case errors.Is(err, sql.ErrNoRows):
			// Step 2, REACHABLE: a family confirmed AFTER the two-instant
			// capture (D5.1a) is in the data half and not in the
			// configuration half, so upsertResourcesTx had no row to
			// create it from. Skip and count the skip — not an error.
			continue
		default:
			return restored, fmt.Errorf("checkpoint: resolve resource family %q of workspace %d: %w",
				f.RouteFamily, workspaceID, err)
		}

		// Step 3: DELETE at the entities grain, scoped to one resolved
		// resource_id — never at the resources grain, which
		// upsertResourcesTx's own doc explains stays UPSERT-only because
		// entities.resource_id is ON DELETE CASCADE. The cascade cannot
		// reach past a DELETE already scoped one level down. That is true
		// of this statement's WHERE, not of the schema in general: it holds
		// because no row this build writes carries a non-NULL
		// parent_entity_id (P3e D9, re-decided at every depth by P3g D9) —
		// the self-FK on entities exists in the schema but stays dormant,
		// on purpose, because a live one would let this same DELETE
		// cascade into a family the call never named — a DESCENDANT
		// family, not necessarily a sibling: at depth this DELETE, scoped
		// to one resolved resource_id, is still the whole guarantee that
		// stands between a config rollback naming one root and it reaching
		// two more levels of families it never named (D9.2, D9.3).
		if _, err := tx.ExecContext(ctx,
			"DELETE FROM entities WHERE resource_id = ?", resourceID,
		); err != nil {
			return restored, fmt.Errorf("checkpoint: clear entities of resource %q (workspace %d): %w",
				f.RouteFamily, workspaceID, err)
		}

		// Step 4: INSERT each stored row VERBATIM — the bytes, not a
		// decode-and-re-marshal, which would reorder object keys and stay
		// value-equal while changing every stored byte. Byte-level
		// determinism from one seed and one spec is what this whole
		// product is for.
		//
		// row.BaseScopeKey (P3h, D9) rides in verbatim beside scope_key: a
		// version-2 document carries the base scope a row was captured in,
		// and a version-1 document's rows all decode with BaseScopeKey ""
		// (the field did not exist in v1's JSON — [bundle.DecodeData]'s own
		// doc names this), which is the empty base scope, exactly where a
		// workspace whose base path carries no parameter has always served
		// from. No branch on the document's version is needed here: the
		// zero value already means what D9 asks for.
		for _, row := range f.Rows {
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO entities (resource_id, parent_entity_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
				VALUES (?, NULL, ?, ?, ?, ?, ?, ?)`,
				resourceID, row.BaseScopeKey, row.ScopeKey, row.EntityKey, string(row.Data), row.CreatedAt, row.UpdatedAt,
			); err != nil {
				return restored, fmt.Errorf("checkpoint: restore entity %q of resource %q (workspace %d): %w",
					row.EntityKey, f.RouteFamily, workspaceID, err)
			}
		}

		// Step 5: raise the family's counter in SQL, inside this same
		// transaction — never in Go over a pre-transaction read, for the
		// identical reason upsertResourcesTx's own `seq` column comment
		// gives for its MAX. Without this step the invariant "seq is at
		// least the largest restored key" holds only when the document's
		// two halves happen to agree (D5.1a's torn-checkpoint hazard); with
		// it, it holds BY CONSTRUCTION. COALESCE is not decoration:
		// SQLite's scalar max() returns NULL if ANY argument is NULL, and a
		// carried family whose relation is empty (D4 requires those
		// carried) makes the inner aggregate NULL — without COALESCE the
		// UPDATE would assign NULL to a NOT NULL column and the whole
		// transaction would die on a family that restored no rows at all.
		if _, err := tx.ExecContext(ctx, `
			UPDATE resources SET seq = max(seq, COALESCE(
				(SELECT max(CAST(entity_key AS INTEGER)) FROM entities
				  WHERE resource_id = ? AND entity_key NOT GLOB '*[^0-9]*' AND length(entity_key) <= 18), 0))
			WHERE id = ?`, resourceID, resourceID,
		); err != nil {
			return restored, fmt.Errorf("checkpoint: raise seq of resource %q (workspace %d): %w",
				f.RouteFamily, workspaceID, err)
		}

		restored++
	}
	return restored, nil
}

// isJSONNullBytes reports whether raw is absent or the JSON literal null —
// the test [upsertResourcesTx] uses to decide between an SQL NULL and a
// stored document in the nullable `wrapper` column. internal/bundle has the
// identical unexported helper for its own entities check; four lines
// duplicated here is cheaper than exporting a predicate from a package
// whose whole point is that it knows nothing about SQL.
func isJSONNullBytes(raw jsonx.RawMessage) bool {
	trimmed := strings.TrimSpace(string(raw))
	return trimmed == "" || trimmed == "null"
}
