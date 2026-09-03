// Capture: the read half of a checkpoint — the workspace core it fences on,
// the v4 bundle of the configuration layer and the entity rows behind it.
// Split out of repo.go 2026-09-03; the text is unchanged.
package checkpoints

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
)

// --- the pre-transaction read (C5 steps 1-3) ---------------------------------

// workspaceCore is C5 step 1's ONE SELECT. revision, created_at and slug
// are the identity triple [fenceTx] compares; name, settings, spec_id and
// scenario_id are read in the SAME statement because a second statement for
// them would be a second instant to reconcile, and this one is already the
// first thing the operation does.
//
// name is in the list because bundle.New's first parameter IS the workspace
// name (bundle.go:239) and nothing in bundle.Validate rejects an empty one
// — an omitted read would fail silently, storing a nameless snapshot no
// check anywhere would catch.
type workspaceCore struct {
	revision     int64
	createdAt    int64
	slug         string
	name         string
	settingsJSON string
	specID       *int64
}

// capture is one pre-transaction snapshot of the workspace layer: the core
// read, the gzipped document, and the two source counts C9's no-op decision
// is made from. The counts are here rather than recomputed later precisely
// because that decision must come from THIS read.
type capture struct {
	core          workspaceCore
	blob          []byte
	overrideCount int
	endpointCount int
}

// captureSnapshot is C5 steps 1 through 3, in that order and all on the
// reader pool: the workspaces row FIRST, then the spec row, then overrides
// and endpoints, then build, encode (which canonicalises and sorts) and
// gzip.
//
// The order is the decision. Reading revision LAST would make [fenceTx]'s
// comparison VACUOUS — a write landing between the overrides read and the
// revision read leaves the recorded value equal to the in-transaction one,
// so the fence would pass over exactly the torn snapshot it exists to
// catch. And doing all of this INSIDE db.Write instead would hold the
// single writer connection for the whole read and encode, which is what
// C17's exception is careful NOT to do.
func (r *Repo) captureSnapshot(ctx context.Context, workspaceID int64) (capture, error) {
	core, b, err := r.readBundle(ctx, workspaceID)
	if err != nil {
		return capture{}, err
	}
	doc, err := bundle.Encode(b)
	if err != nil {
		return capture{}, fmt.Errorf("checkpoint: encode snapshot for workspace %d: %w", workspaceID, err)
	}
	blob, err := compressSnapshot(doc)
	if err != nil {
		return capture{}, fmt.Errorf("checkpoint for workspace %d: %w", workspaceID, err)
	}
	return capture{
		core:          core,
		blob:          blob,
		overrideCount: len(b.Overrides),
		endpointCount: len(b.Endpoints),
	}, nil
}

// readBundle is [Repo.captureSnapshot]'s read half — the workspace's core
// row and its whole configuration layer as one decoded [bundle.Bundle], on
// the reader pool — split out by P4b (2026-09-02) so an export
// ([Repo.Export]) and a fork ([Repo.Fork]) read the identical document a
// checkpoint captures, through the identical code, without the gzip a
// checkpoint row needs and an HTTP response does not.
func (r *Repo) readBundle(ctx context.Context, workspaceID int64) (workspaceCore, bundle.Bundle, error) {
	core, err := r.readWorkspaceCore(ctx, workspaceID)
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, err
	}

	settings, err := domain.ParseSettings([]byte(core.settingsJSON))
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, fmt.Errorf("checkpoint: parse workspace %d settings: %w", workspaceID, err)
	}

	// Step 2. The same provenance a scenario records: DESIGN §17:1073 draws
	// spec populated and :1081-1082 says that document IS the config_snap.
	// Nothing in bundle.Validate rejects an empty SpecRef, which is why
	// this is a numbered step rather than an inference — an omitted spec
	// read fails silently.
	var specRef bundle.SpecRef
	if core.specID != nil {
		specRef, err = r.readSpecRef(ctx, *core.specID)
		if err != nil {
			return workspaceCore{}, bundle.Bundle{}, err
		}
	}

	overrideRows, err := r.overrides.ForWorkspace(ctx, workspaceID)
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, fmt.Errorf("checkpoint: %w", err)
	}
	entries := make([]bundle.OverrideEntry, 0, len(overrideRows))
	for _, row := range overrideRows {
		entries = append(entries, bundle.NewOverrideEntry(row))
	}

	endpointRows, err := r.customep.ForWorkspace(ctx, workspaceID)
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, fmt.Errorf("checkpoint: %w", err)
	}

	// bundle.New KEEPS its signature and still hard-codes an empty
	// Endpoints slice (C2/§B): a scenario can never carry a custom
	// endpoint, and giving New a fourth parameter would push that fact into
	// every scenario call site. A checkpoint legitimately carries them, so
	// this assigns them on the returned value, AFTER the call. Encode sorts
	// them by (Method, Path) inside canonicalize, so the DB ordering
	// customep.ForWorkspace returns (source_order, id) never leaks into the
	// document.
	b := bundle.New(core.name, settings, specRef, entries)
	b.Endpoints = make([]bundle.EndpointEntry, 0, len(endpointRows))
	for _, row := range endpointRows {
		b.Endpoints = append(b.Endpoints, endpointEntryFromRow(row))
	}

	// Steps 4 and 5 (P3b): the resources rows and the resource_decisions
	// rows, assigned after the call for the same reason Endpoints is —
	// bundle.New's callers include internal/scenarios, which must keep all
	// three empty. They are read through this package's OWN SQL on the
	// reader pool, the way readSpecRef reads the specs table, rather than
	// by holding an internal/resources repository: Repo carries `overrides`
	// and `customep` and nothing else, NewRepo keeps the signature it has,
	// and internal/resources publishes no resource_decisions reader to
	// borrow in any case.
	//
	// The two travel TOGETHER, always. A confirm writes both rows and a
	// decline clears both, so a snapshot carrying one without the other
	// restores a workspace whose decision row says `declined` beside a live
	// `resources` row — a state the confirm path answers `already_confirmed`
	// for while the screen renders it as declined.
	b.Resources, err = r.readResourceEntries(ctx, workspaceID)
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, err
	}
	b.Decisions, err = r.readDecisionEntries(ctx, workspaceID)
	if err != nil {
		return workspaceCore{}, bundle.Bundle{}, err
	}
	return core, b, nil
}

// readWorkspaceCore is C5 step 1's single SELECT, on the READER pool: the
// fence works by comparing two INDEPENDENT reads, not by holding a lock
// across them — a "coherent read" that held the writer connection for the
// whole overrides-and-spec read in between is exactly the long-held write
// transaction this project's single-connection pool cannot afford.
func (r *Repo) readWorkspaceCore(ctx context.Context, workspaceID int64) (workspaceCore, error) {
	var (
		c      workspaceCore
		specID sql.NullInt64
	)
	err := r.db.R.QueryRowContext(ctx,
		"SELECT revision, created_at, slug, name, settings, spec_id FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&c.revision, &c.createdAt, &c.slug, &c.name, &c.settingsJSON, &specID)
	switch {
	case err == nil:
		if specID.Valid {
			v := specID.Int64
			c.specID = &v
		}
		return c, nil
	case errors.Is(err, sql.ErrNoRows):
		return workspaceCore{}, fmt.Errorf("workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	default:
		return workspaceCore{}, fmt.Errorf("read workspace %d: %w", workspaceID, err)
	}
}

// readSpecRef reads the two short strings [bundle.SpecRef] needs, through
// the reader pool rather than internal/specs.Repo.ByID — the same choice
// and the same reason scenarios/repo.go's readSpecRef states: that method
// selects the FULL specs row, including raw and normalized documents (~350
// KB apiece on the real fixture), and importing internal/specs would drag
// internal/config, internal/gen, internal/openapi and internal/router in
// for two columns.
func (r *Repo) readSpecRef(ctx context.Context, specID int64) (bundle.SpecRef, error) {
	var name, hash string
	if err := r.db.R.QueryRowContext(ctx,
		"SELECT name, hash FROM specs WHERE id = ?", specID,
	).Scan(&name, &hash); err != nil {
		return bundle.SpecRef{}, fmt.Errorf("read spec %d for checkpoint snapshot: %w", specID, err)
	}
	return bundle.SpecRef{Name: name, Hash: hash}, nil
}

// readResourceEntries reads workspaceID's `resources` rows into the
// bundle's wire shape, on the reader pool.
//
// A resource row is CONFIGURATION — which family is confirmed, its id
// field, its wrapper, its write form — and 0001_init.sql:221 has said so
// since P0: `config_snap BLOB NOT NULL, -- settings + edits + endpoints +
// resources`. What is NOT read here is `entities`: those rows live in
// data_snap, captured separately by [captureEntitiesTx] INSIDE the write
// transaction (D5.1 of the P3d decision document), not on this reader-pool
// read alongside everything else config_snap carries — the two columns are
// captured at two different instants for a reason [captureEntitiesTx]'s own
// doc gives, and this function's job stays exactly what it always was:
// which family is confirmed, never what its rows hold.
//
// Three columns of the table are deliberately absent from the entry: `id`
// and `workspace_id` (a snapshot mints no row ids) and `parent_id` — NULL by
// design (P3e D9, re-decided the same way at every depth by P3g D9), not
// pending a slice: a stored `resources.id` does not survive
// decline-then-reconfirm, so a restore leaves whatever the live row has
// standing rather than writing one back. See [bundle.ResourceEntry] for the
// wire-only `parentFamily` that mirrors this in the other direction.
func (r *Repo) readResourceEntries(ctx context.Context, workspaceID int64) ([]bundle.ResourceEntry, error) {
	rows, err := r.db.R.QueryContext(ctx, `
		SELECT route_family, name, id_field, id_strategy, scope_params,
		       entity_schema, wrapper, filter_map, write_form, seq, seed_count
		FROM resources WHERE workspace_id = ? ORDER BY route_family`, workspaceID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read resources for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	// Non-nil from the start: an empty result must still encode as "[]",
	// never "null" — the same rule bundle.New follows for the three arrays
	// it hard-codes.
	out := make([]bundle.ResourceEntry, 0)
	for rows.Next() {
		var (
			e                  bundle.ResourceEntry
			scopeParams        string
			filterMap          string
			wrapper, writeForm sql.NullString
		)
		if err := rows.Scan(&e.RouteFamily, &e.Name, &e.IDField, &e.IDStrategy, &scopeParams,
			&e.EntitySchema, &wrapper, &filterMap, &writeForm, &e.Seq, &e.SeedCount); err != nil {
			return nil, fmt.Errorf("checkpoint: scan resource of workspace %d: %w", workspaceID, err)
		}
		if err := jsonx.Unmarshal([]byte(scopeParams), &e.ScopeParams); err != nil {
			return nil, fmt.Errorf("checkpoint: decode scope_params of %q in workspace %d: %w",
				e.RouteFamily, workspaceID, err)
		}
		if wrapper.Valid {
			e.Wrapper = jsonx.RawMessage(wrapper.String)
		}
		e.FilterMap = jsonx.RawMessage(filterMap)
		if writeForm.Valid {
			v := writeForm.String
			e.WriteForm = &v
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkpoint: read resources for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// readDecisionEntries reads workspaceID's `resource_decisions` rows — see
// [Repo.readResourceEntries] on why the two are captured together and never
// one without the other.
func (r *Repo) readDecisionEntries(ctx context.Context, workspaceID int64) ([]bundle.DecisionEntry, error) {
	rows, err := r.db.R.QueryContext(ctx,
		"SELECT route_family, state FROM resource_decisions WHERE workspace_id = ? ORDER BY route_family", workspaceID)
	if err != nil {
		return nil, fmt.Errorf("checkpoint: read resource decisions for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	out := make([]bundle.DecisionEntry, 0)
	for rows.Next() {
		var e bundle.DecisionEntry
		if err := rows.Scan(&e.RouteFamily, &e.State); err != nil {
			return nil, fmt.Errorf("checkpoint: scan resource decision of workspace %d: %w", workspaceID, err)
		}
		out = append(out, e)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("checkpoint: read resource decisions for workspace %d: %w", workspaceID, err)
	}
	return out, nil
}

// --- transaction-scoped helpers ----------------------------------------------

// entityDataProbeOverBudgetTx is D5.2's four-term probe, run FIRST and
// INSIDE the same transaction [captureEntitiesTx] will read from — never on
// the reader pool, and never after the rows are already in memory. It
// answers ONE question, cheaply, before a single entity row or byte of
// entity JSON is read: would the document this workspace's entity rows
// encode to plausibly exceed [maxDataProbeBytes].
//
// All four terms are load-bearing (D5.2 defeats each of a narrower probe by
// execution, not by argument): family count and row count bound the
// per-family and per-row JSON envelope an empty or sparse population would
// otherwise hide from a bare SUM(LENGTH(data)); the payload sum bounds the
// rows' own bytes; and the family-NAME byte sum is the one a three-term
// probe misses entirely — route_family has no length bound in the schema
// and is written into the document once per family.
//
// CAST(... AS BLOB) is not decoration: entities.data and resources.route_family
// are TEXT, and SQLite's length() over TEXT counts CHARACTERS, undercounting
// multibyte content by up to four times — always in the GENEROUS direction
// against a budget whose only job is an allocation ceiling, which is exactly
// backwards for a probe that exists to bound memory.
func entityDataProbeOverBudgetTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (bool, error) {
	var (
		familyCount     int64
		familyNameBytes int64
		rowCount        int64
		payloadBytes    int64
	)
	err := tx.QueryRowContext(ctx, `
		SELECT (SELECT COUNT(*) FROM resources WHERE workspace_id = ?),
		       (SELECT COALESCE(SUM(length(CAST(route_family AS BLOB))), 0)
		          FROM resources WHERE workspace_id = ?),
		       COUNT(e.id),
		       COALESCE(SUM(length(CAST(e.data AS BLOB))), 0)
		  FROM resources r JOIN entities e ON e.resource_id = r.id
		 WHERE r.workspace_id = ?`,
		workspaceID, workspaceID, workspaceID,
	).Scan(&familyCount, &familyNameBytes, &rowCount, &payloadBytes)
	if err != nil {
		return false, fmt.Errorf("checkpoint: probe entity data size for workspace %d: %w", workspaceID, err)
	}
	budget := payloadBytes + familyNameBytes +
		rowCount*dataProbeRowEnvelopeBytes +
		familyCount*dataProbeFamilyEnvelopeBytes +
		dataProbeDocumentEnvelopeBytes
	return budget > int64(maxDataProbeBytes), nil
}

// captureEntitiesTx is D5's entity-data half of a checkpoint, and D5.1's
// answer to "no counter-based fence is sound": it runs INSIDE the write
// transaction the caller already holds, never on the reader pool, so the
// read and the write become one instant BY CONSTRUCTION rather than by a
// fence that has to prove nothing moved between two separate reads. Every
// one of this package's four capture sites (Create, Auto, Rollback, Reset —
// D5's always-capture) calls it, unconditionally, from inside their own
// db.Write callback.
//
// degraded is true in exactly two cases, and the caller decides what a
// degrade means for ITS verb — this function only reports it, never logs
// it (this package has no logger, see [NewRepo]): the probe above found the
// workspace over [maxDataProbeBytes], in which case NO entity row is read
// at all; or the probe passed but the encoded document still exceeded
// [maxSnapshotBytes], in which case [compressSnapshot]'s own
// [ErrSnapshotTooLarge] is SWALLOWED rather than propagated — the opposite
// policy from [Repo.captureSnapshot]'s call to the same function, and
// [compressSnapshot]'s own doc comment states why: entity rows are created
// by an unauthenticated POST X, and propagating here would let any
// anonymous caller permanently break a workspace's checkpointing.
func captureEntitiesTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (blob []byte, degraded bool, err error) {
	over, err := entityDataProbeOverBudgetTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, false, err
	}
	if over {
		return nil, true, nil
	}

	d, err := readDataBundleTx(ctx, tx, workspaceID)
	if err != nil {
		return nil, false, err
	}
	doc, err := bundle.EncodeData(d)
	if err != nil {
		return nil, false, fmt.Errorf("checkpoint: encode entity data for workspace %d: %w", workspaceID, err)
	}
	blob, err = compressSnapshot(doc)
	if err != nil {
		if errors.Is(err, ErrSnapshotTooLarge) {
			// The swallow [compressSnapshot]'s own doc names: the probe
			// above is a generous ESTIMATE and can pass while the actual
			// encoded document still exceeds the write-side ceiling. An
			// ordinary degrade, not a special case (D5.2).
			return nil, true, nil
		}
		return nil, false, fmt.Errorf("checkpoint: compress entity data for workspace %d: %w", workspaceID, err)
	}
	return blob, false, nil
}

// readDataBundleTx is [captureEntitiesTx]'s read half, split out by P4b
// (2026-09-02) so an export can produce the SAME document without the
// gzip: the read runs on whatever transaction the caller holds — the write
// transaction for a checkpoint, a read-only one on the reader pool for an
// export — and the probe stays the caller's, because the two callers take
// opposite policies on it (a checkpoint degrades to NULL, an export
// refuses by name).
func readDataBundleTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (bundle.DataBundle, error) {
	// The read itself: workspace-scoped, LEFT JOIN, and `data` carried as
	// stored BYTES — all three load-bearing, none provable by a
	// single-workspace fixture (D5.2). WHERE r.workspace_id = ? is what
	// keeps a foreign family sharing a route path from being written INTO
	// this workspace's rows on restore — not a leak into a blob, a
	// cross-workspace WRITE. LEFT JOIN is what keeps a confirmed family
	// holding zero rows IN the document (an INNER JOIN would silently drop
	// it, which ValidateData's own "declared but empty" contract forbids).
	// e.data is read as TEXT and stored as jsonx.RawMessage without ever
	// being decoded and re-marshalled: [restoreEntitiesTx] step 4 depends
	// on it round-tripping BYTE for byte, not merely value-equal.
	//
	// e.base_scope_key rides beside scope_key (P3h, D9) — selected here and
	// carried verbatim into bundle.EntityRow.BaseScopeKey, never parsed or
	// re-derived: it is entities' own opaque column, encoded once at write
	// time by resources.EncodeScope (D3.1), and this function is a reader,
	// not a second owner of that encoding. ORDER BY stays on route_family
	// and the entity_key cast alone: entity_key is minted from ONE
	// family-wide counter across every base scope (P3g's rule), so it
	// already totally orders a family's rows with no tie a base scope could
	// break.
	rows, err := tx.QueryContext(ctx, `
		SELECT r.route_family, e.base_scope_key, e.scope_key, e.entity_key, e.data, e.created_at, e.updated_at
		  FROM resources r LEFT JOIN entities e ON e.resource_id = r.id
		 WHERE r.workspace_id = ?
		 ORDER BY r.route_family, CAST(e.entity_key AS INTEGER)`, workspaceID)
	if err != nil {
		return bundle.DataBundle{}, fmt.Errorf("checkpoint: read entity rows for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	// Non-nil from the start, matching [Repo.readResourceEntries]'s own
	// rule: an empty result must still encode as "[]", never "null".
	families := make([]bundle.FamilyEntry, 0)
	index := make(map[string]int, len(families))
	for rows.Next() {
		var (
			routeFamily  string
			baseScopeKey sql.NullString
			scopeKey     sql.NullString
			entityKey    sql.NullString
			data         sql.NullString
			createdAt    sql.NullInt64
			updatedAt    sql.NullInt64
		)
		if serr := rows.Scan(&routeFamily, &baseScopeKey, &scopeKey, &entityKey, &data, &createdAt, &updatedAt); serr != nil {
			return bundle.DataBundle{}, fmt.Errorf("checkpoint: scan entity row for workspace %d: %w", workspaceID, serr)
		}
		idx, ok := index[routeFamily]
		if !ok {
			idx = len(families)
			index[routeFamily] = idx
			families = append(families, bundle.FamilyEntry{RouteFamily: routeFamily, Rows: make([]bundle.EntityRow, 0)})
		}
		if !entityKey.Valid {
			// The LEFT JOIN produced no matching entities row: a confirmed
			// family with zero rows, carried with an empty Rows slice
			// rather than skipped (D4/D5.2's "declared but empty").
			continue
		}
		families[idx].Rows = append(families[idx].Rows, bundle.EntityRow{
			ScopeKey:     scopeKey.String,
			EntityKey:    entityKey.String,
			BaseScopeKey: baseScopeKey.String,
			Data:         jsonx.RawMessage(data.String),
			CreatedAt:    createdAt.Int64,
			UpdatedAt:    updatedAt.Int64,
		})
	}
	if err := rows.Err(); err != nil {
		return bundle.DataBundle{}, fmt.Errorf("checkpoint: iterate entity rows for workspace %d: %w", workspaceID, err)
	}

	return bundle.DataBundle{MockerData: bundle.DataVersion, Families: families}, nil
}
