// Confirm: the generate-then-fence transition that turns a suggested family
// into a stored one. Split out of repo.go 2026-09-03; the text is unchanged.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/bundle"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/specs"
)

// fenceConfirmTx is D4/R36's fence, run INSIDE Confirm's write transaction:
// re-read the workspace's identity (created_at, slug — the pair
// internal/checkpoints' own fenceTx uses, because revision alone does not
// prove workspace identity) and spec_id/scenario_id, and compare all four
// against before. A workspace deleted and re-created with the same id
// inside the window fails here (D13 clause 37), never on revision alone —
// revision is not part of this comparison at all (D13 clause 37's other
// half: a bare bump from a second family's confirm, or an anonymous
// POST {prefix}/state, must NOT refuse this one).
//
// scenario_id unchanged is treated as proof that the EFFECTIVE seed/
// listSize a scenario supplies also did not change: a scenario's snapshot
// is immutable after creation (no edit route — only CreateFromCurrentState,
// CloneFrom and Rename exist, and Rename never touches the snapshot), so
// the only way for a scenario's own seed/listSize to change is for
// scenario_id itself to point somewhere else, which the comparison above
// already catches. Only when NO scenario is active (both before and now)
// does this re-parse the workspace's own settings and compare Seed/
// ListSize directly — the one input source that CAN change without a
// scenario_id write.
//
// wantBasePath/wantBasePathValues join the UNCONDITIONAL half above, NEVER
// the `!scenarioID.Valid` branch below (D6.5): unlike Seed/ListSize,
// neither field is ever taken from an active scenario (D4.5), so the
// workspace's own settings blob — re-read here regardless of scenario
// state — is the sole source of truth either way, and the parse below now
// runs unconditionally rather than only inside that branch.
func fenceConfirmTx(ctx context.Context, tx *sql.Tx, workspaceID int64, before workspaceCore, wantSeed int64, wantListSize int, wantBasePath string, wantBasePathValues []string) error {
	var (
		createdAt          int64
		slug, settingsJSON string
		specID, scenarioID sql.NullInt64
	)
	err := tx.QueryRowContext(ctx,
		"SELECT created_at, slug, spec_id, scenario_id, settings FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&createdAt, &slug, &specID, &scenarioID, &settingsJSON)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	default:
		return fmt.Errorf("read workspace %d inside transaction: %w", workspaceID, err)
	}

	same := createdAt == before.createdAt && slug == before.slug &&
		nullIntEqual(specID, before.specID) && nullIntEqual(scenarioID, before.scenarioID)
	if !same {
		return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, ErrStaleConfig)
	}

	settings, perr := domain.ParseSettings([]byte(settingsJSON))
	if perr != nil {
		return fmt.Errorf("resource confirm: parse workspace %d settings: %w", workspaceID, perr)
	}

	// D6.5: basePath/basePathValues, unconditionally — a settings edit
	// landing between prepareConfirm's own pre-transaction read and this
	// one must be caught regardless of whether a scenario happens to be
	// active, because population was already prepared against `before`'s
	// declared set outside this transaction.
	if settings.BasePath != wantBasePath || !slices.Equal(settings.BasePathValues, wantBasePathValues) {
		return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, ErrStaleConfig)
	}
	// D6.4: an ABSOLUTE property of the current settings, not a
	// before/current comparison — checked on this same fresh read, because
	// a declared set that was empty at BOTH ends of the window (never
	// edited at all) passes the equality check above trivially and would
	// otherwise never be caught inside the transaction at all.
	if berr := checkBaseScopeDeclared(settings); berr != nil {
		return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, berr)
	}

	if !scenarioID.Valid {
		if settings.Seed != wantSeed || settings.ListSize != wantListSize {
			return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, ErrStaleConfig)
		}
	}
	return nil
}

// effectiveSettings resolves the settings generation actually reads:
// ws as given when no scenario is active, or the ACTIVE scenario's own
// snapshot settings otherwise (scenario composition, per D5 — "Seed from
// the workspace's EFFECTIVE settings, scenario composition included").
// Unlike internal/mockplane's composeScenarioLayer, this does not restore
// basePath/CORS/notFoundBody from the workspace: those three are never
// read by generation (gen.Options has no field for any of them), so there
// is nothing here for them to matter to.
//
// A scenario that fails to load or decode degrades to the workspace's own
// settings, exactly like internal/mockplane/scenario.go's scenarioSnapshot
// — "a stored BLOB is data this server wrote but cannot re-verify on the
// unauthenticated path", and a confirm that 500s because one scenario row
// went bad is worse than one that quietly generates from the workspace
// layer while an operator works out why.
func (r *Repo) effectiveSettings(ctx context.Context, workspaceID int64, ws domain.Settings, scenarioID *int64) domain.Settings {
	if scenarioID == nil {
		return ws
	}
	var raw []byte
	if err := r.db.R.QueryRowContext(ctx,
		"SELECT snapshot FROM scenarios WHERE id = ? AND workspace_id = ?", *scenarioID, workspaceID,
	).Scan(&raw); err != nil {
		return ws
	}
	b, err := bundle.Decode(raw)
	if err != nil {
		return ws
	}
	return b.Workspace.Settings
}

// --- Confirm ---------------------------------------------------------------

// Confirm is D4/D5's whole "confirmed" transition: locate routeFamily's
// suggestion, populate seed_count entities from the DETAIL route's
// generator (outside any transaction — R11), then in ONE write transaction
// fence the generation inputs, write the decision row, materialize the
// resources row, insert every entity and bump revision.
func (r *Repo) Confirm(ctx context.Context, workspaceID int64, routeFamily string) (*Resource, error) {
	prep, err := r.prepareConfirm(ctx, workspaceID, routeFamily)
	if err != nil {
		return nil, err
	}
	confirmPreWriteHook()

	now := time.Now().UTC()
	var resource *Resource
	writeErr := r.db.Write(ctx, func(tx *sql.Tx) error {
		if ferr := fenceConfirmTx(ctx, tx, workspaceID, prep.core, prep.effSettings.Seed, prep.effSettings.ListSize,
			prep.basePath, prep.basePathValues); ferr != nil {
			return ferr
		}

		// D5.1: a nested family's parent must be CONFIRMED, read INSIDE
		// this transaction — a decline of the parent landing in the
		// window between prepareConfirm's own read and this insert would
		// otherwise leave a confirmed child with no parent, the one state
		// D6.3's anchor check has no answer for. A no-op for a top-level
		// family (router.ParentFamily(routeFamily) == "").
		if ferr := r.fenceParentTx(ctx, tx, workspaceID, routeFamily, prep.bases, prep.pairs); ferr != nil {
			return ferr
		}

		exists, eerr := r.resourceExists(ctx, tx, workspaceID, routeFamily)
		if eerr != nil {
			return eerr
		}
		if exists {
			return ErrAlreadyConfirmed
		}

		resourceID, err := insertConfirmedResourceTx(ctx, tx, workspaceID, routeFamily, prep)
		if err != nil {
			return err
		}

		for i, body := range prep.bodies {
			if hookErr := confirmEntityHook(i, len(prep.bodies)); hookErr != nil {
				return hookErr
			}
			entityKey := strconv.Itoa(i + 1)
			base := prep.baseScopeKeys[i]
			scope := prep.scopeKeys[i]
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO entities (resource_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
				VALUES (?, ?, ?, ?, ?, ?, ?)`,
				resourceID, string(base), string(scope), entityKey, string(body), now.Unix(), now.Unix()); err != nil {
				return fmt.Errorf("insert entity %d of %d for %q: %w", i+1, len(prep.bodies), routeFamily, err)
			}
		}

		if err := bumpRevisionTx(ctx, tx, workspaceID, now); err != nil {
			return err
		}

		// Seq/SeedCount are written APART (D5.5 point 4): Seq is the
		// family-wide TOTAL just inserted (P×L for a nested family, L for
		// a top-level one — the two coincide when there is exactly one
		// scope), so the next POST X mints a key no scope already holds.
		// SeedCount stays the PER-SCOPE count a reseed regenerates per
		// scope (D8.2 rule 3's own writer of the same distinction).
		resource = &Resource{
			ID: resourceID, WorkspaceID: workspaceID, RouteFamily: routeFamily, Name: prep.sugg.Name,
			IDField: prep.sugg.IDField, IDStrategy: "seq", ScopeParams: prep.scopeParams, EntitySchema: prep.sugg.EntitySchema,
			Wrapper: prep.sugg.Wrapper, FilterMap: map[string]any{}, WriteForm: prep.writeForm,
			Seq: int64(len(prep.bodies)), SeedCount: int64(prep.seedCount),
		}
		return nil
	})
	if writeErr != nil {
		return nil, writeErr
	}
	return resource, nil
}

// confirmPrep is everything [Repo.prepareConfirm] computes OUTSIDE any
// transaction (R11) and [Repo.Confirm]'s write transaction then consumes:
// the fenced inputs, the populated bodies and the already-marshalled
// wrapper — split into its own type purely to keep Confirm's own
// cyclomatic complexity below the branching the sequential "resolve every
// input, refuse on the first that's wrong" shape would otherwise cost one
// function (the same reason internal/specs/derive.go's own deriveFamily
// extracts deriveCollectionShape and deriveDetailIDType, per CLAUDE.md's
// style note on that round).
type confirmPrep struct {
	core workspaceCore
	// basePath/basePathValues are the WORKSPACE's own raw settings values
	// (never [Repo.effectiveSettings]'s scenario-composed result, D4.5) —
	// what [DeclaredBaseScopes] computed [bases] from, and what
	// [fenceConfirmTx] re-compares element-wise inside the write
	// transaction (D6.5).
	basePath       string
	basePathValues []string
	sugg           *specs.ResourceSuggestion
	effSettings    domain.Settings
	// seedCount is the PER-PAIR count L (D5.5 point 4/D6.1) — the total for
	// a top-level family under one base value, whose only route scope is
	// the empty one.
	seedCount int
	writeForm *string
	// bodies is the family's WHOLE population, every (base, route-scope)
	// PAIR concatenated in pair order (D6.1) — base outer, route scope
	// inner — declared-base-order × P × L for a nested family, declared-
	// base-order × L for a top-level one.
	bodies [][]byte
	// baseScopeKeys/scopeKeys are parallel to bodies: body i belongs to
	// base baseScopeKeys[i] and route scope scopeKeys[i]. baseScopeKeys is
	// all "" (and scopeKeys all "" too) for a workspace whose basePath
	// carries no parameter — bit-identical to every workspace before P3h.
	baseScopeKeys []ScopeKey
	scopeKeys     []ScopeKey
	// scopeParams is D5.6's write: the outer path-parameter NAMES, in
	// order — []string{} for a top-level family.
	scopeParams []string
	// scopeParamsJSON is scopeParams already marshalled, ready for
	// [insertConfirmedResourceTx]'s INSERT.
	scopeParamsJSON string
	// bases is D4.1/D6.1's declared base-scope SET, in declared order —
	// [ScopeKey("")] alone for a basePath with no parameter (D3.3's
	// implicit singleton). Never re-derived inside the write transaction:
	// fenceConfirmTx's own basePath/basePathValues comparison is what
	// proves this list is still current by the time fenceParentTx reuses
	// it, rather than recomputing it a second time from a second read.
	bases []ScopeKey
	// pairs is the family's own whole (base, route-scope) SET — one entry
	// per resulting row set, before multiplying by seedCount — computed by
	// [chainPairs] over the reader pool (D6.1). This is the fenced input
	// [Repo.fenceParentTx] recomputes and compares against, element-wise,
	// inside the write transaction (D5.6/D6.1): a mismatch answers the
	// existing 409 stale_config, never a silent partial population.
	pairs       []basePair
	wrapperJSON string
}

// prepareConfirm runs D4/D5's whole generation half: locate the
// suggestion, resolve the effective settings and the generator, locate the
// family's detail/POST operations, compute write_form, populate every
// entity body and check the batch caps. Nothing here writes to the
// database — see [confirmPrep]'s own doc comment for why this is split out
// of [Repo.Confirm] at all.
func (r *Repo) prepareConfirm(ctx context.Context, workspaceID int64, routeFamily string) (*confirmPrep, error) { //nolint:gocyclo // one arm per refusal reason (already_confirmed, unknown_family, no default 200 variant, parent_not_confirmed) plus D5.3's own nested-vs-top-level population branch — the specification's own shape, not incidental branching
	wrap := func(err error) error {
		return fmt.Errorf("confirm %q for workspace %d: %w", routeFamily, workspaceID, err)
	}

	core, err := r.readWorkspaceCore(ctx, workspaceID)
	if err != nil {
		return nil, err
	}

	// D4: "the resources-row check is asked FIRST" — an already-confirmed
	// family (including an orphan the current spec no longer suggests)
	// answers already_confirmed before this function ever tries to locate
	// a suggestion for it.
	alreadyConfirmed, err := r.resourceExists(ctx, r.db.R, workspaceID, routeFamily)
	if err != nil {
		return nil, wrap(err)
	}
	if alreadyConfirmed {
		return nil, wrap(ErrAlreadyConfirmed)
	}

	sugg, err := r.findSuggestion(ctx, core.specID, routeFamily)
	if err != nil {
		return nil, wrap(err)
	}
	if sugg == nil {
		return nil, wrap(ErrUnknownFamily)
	}

	settings, err := domain.ParseSettings([]byte(core.settingsJSON))
	if err != nil {
		return nil, wrap(fmt.Errorf("parse settings: %w", err))
	}
	effSettings := r.effectiveSettings(ctx, workspaceID, settings, core.scenarioID)
	seedCount := clampSeedCount(effSettings.ListSize)

	generator, resolver, err := r.buildGenerator(ctx, *core.specID, effSettings)
	if err != nil {
		return nil, wrap(err)
	}

	routes, err := r.specs.Routes(ctx, *core.specID)
	if err != nil {
		return nil, wrap(fmt.Errorf("load routes: %w", err))
	}
	detailRoute, idParam, postRoute, err := locateFamilyOperations(routes, routeFamily)
	if err != nil {
		return nil, wrap(err)
	}

	variants, err := r.specs.Variants(ctx, *core.specID)
	if err != nil {
		return nil, wrap(fmt.Errorf("load variants: %w", err))
	}
	detailVariant, ok := defaultVariant200(variants[detailRoute.OpRowID])
	if !ok {
		return nil, wrap(fmt.Errorf("%w: no HTTP-200 default variant on the detail route", ErrPopulationFailed))
	}

	var writeForm *string
	if postRoute != nil {
		writeForm = computeWriteForm(resolver, variants[postRoute.OpRowID], sugg.EntitySchema)
	}

	// D5.1: a nested family's IMMEDIATE parent must be CONFIRMED — the
	// single hop that, by D5.2's induction proof, is enough to guarantee
	// the WHOLE chain is confirmed too (see [Repo.fenceParentTx]'s own
	// doc comment for the proof restated). router.ParentFamily is the ONE
	// owner of "which family is the parent" (D4.2) — never a stored copy.
	var chain []*Resource
	if parentFamily := router.ParentFamily(routeFamily); parentFamily != "" {
		parent, perr := r.resourceByFamily(ctx, r.db.R, workspaceID, parentFamily)
		if perr != nil {
			return nil, wrap(perr)
		}
		if parent == nil {
			return nil, wrap(ErrParentNotConfirmed)
		}

		// D5.3: population is one row set per live ancestor TUPLE, not
		// per immediate-parent key alone — ancestorChainTx resolves the
		// REST of the chain above the immediate parent, which D5.2's
		// proof lets it do with no further confirmation check of its
		// own (only a data fetch, not a second enforcement of D5.1's
		// rule).
		fullChain, cerr := r.ancestorChainTx(ctx, r.db.R, workspaceID, routeFamily)
		if cerr != nil {
			return nil, wrap(cerr)
		}
		chain = fullChain
	}

	// D4.5/D6.1: the declared base-scope set is always the WORKSPACE's own
	// raw settings — never effSettings, which (unlike
	// internal/mockplane's composeScenarioLayer) does NOT restore
	// basePath/basePathValues from the workspace when a scenario is
	// active (see [Repo.effectiveSettings]'s own doc comment). Reaching
	// for effSettings here would be the exact substitution P20b's own
	// test exists to fail on the reseed path — this is the identical trap
	// on the confirm path.
	bases := DeclaredBaseScopes(settings.BasePath, settings.BasePathValues)

	// chainPairs walks chain (empty for a top-level family, in which case
	// each base answers the single implicit route scope [""] with no
	// query at all) WITHIN each declared base value in turn (D6.1 — the
	// walk never crosses base scopes) and hands the live keys to
	// extendScopes — the family's own (base, route-scope) PAIR set, one
	// entry per live ancestor combination under each base. A parent (at
	// any level, under any base) with zero live entities right now is a
	// legal, ordinary state, and it must produce ZERO row sets under it,
	// which is exactly what an empty key list at that level does to
	// extendScopes' own fan-out. An empty `bases` (basePath carries a
	// parameter with an empty declared set, D6.4) produces zero pairs and
	// therefore zero bodies below — the write transaction is what refuses
	// that case (checkBaseScopeDeclared, inside fenceConfirmTx), not this
	// pre-transaction generation step.
	pairs, perr := chainPairs(ctx, r.db.R, chain, bases)
	if perr != nil {
		return nil, wrap(fmt.Errorf("compute scopes for %q: %w", routeFamily, perr))
	}

	// The row cap is checked on the COUNT before a single body is
	// generated: checkBatchCaps below measures bytes and needs the bodies,
	// but the count is known from the pairs alone, and populatePairs
	// materialises every body in RAM first — |declared bases| × |ancestor
	// tuples| × seedCount of them, a product an operator controls through
	// basePathValues and nested confirms and that must not be allowed to
	// reach the cap check from the wrong side.
	if int64(len(pairs))*int64(seedCount) > r.maxEntityRows {
		return nil, wrap(ErrEntityLimit)
	}
	bodies, baseScopeKeys, scopeKeys, err := populatePairs(generator, detailVariant, routeFamily, idParam, seedCount, pairs, sugg.IDField, sugg.Wrapper.IDType)
	if err != nil {
		return nil, wrap(err)
	}
	if err := r.checkBatchCaps(bodies); err != nil {
		return nil, wrap(err)
	}

	// D5.6: scope_params is the detail route's own outer parameter NAMES,
	// in order — []string{} for a top-level family, the value every row
	// already carries before P3e.
	scopeParams := outerParamNames(detailRoute.Path)
	scopeParamsJSON, err := jsonx.Marshal(scopeParams)
	if err != nil {
		return nil, wrap(fmt.Errorf("marshal scope params: %w", err))
	}

	wrapperJSON, err := jsonx.Marshal(sugg.Wrapper)
	if err != nil {
		return nil, wrap(fmt.Errorf("marshal wrapper: %w", err))
	}

	return &confirmPrep{
		core: core, basePath: settings.BasePath, basePathValues: settings.BasePathValues,
		sugg: sugg, effSettings: effSettings, seedCount: seedCount,
		writeForm: writeForm, bodies: bodies, baseScopeKeys: baseScopeKeys, scopeKeys: scopeKeys,
		scopeParams: scopeParams, scopeParamsJSON: string(scopeParamsJSON),
		bases: bases, pairs: pairs, wrapperJSON: string(wrapperJSON),
	}, nil
}

// fenceParentTx is D5.1/D5.6's own fence, run INSIDE Confirm's write
// transaction. Two checks, and only the first is single-hop by design
// (D5.1): the family's IMMEDIATE parent must have a CONFIRMED resources
// row (ErrParentNotConfirmed otherwise — the same question
// [Repo.prepareConfirm]'s own pre-check already asked, over the SAME
// relation; D5.2's induction proof is what makes asking it twice safe
// rather than redundant, because both checks answer the identical
// question). The second is the WIDENED half (D5.6, now D6.1 too): the
// family's whole (base, route-scope) PAIR set, recomputed here via
// [chainPairs] over the chain resolved INSIDE this transaction
// ([Repo.ancestorChainTx]) and bases (the declared set [Repo.prepareConfirm]
// already fenced staleness-free via [fenceConfirmTx]'s own basePathValues
// comparison), must still equal wantPairs element-wise (ErrStaleConfig
// otherwise) — the same window [Repo.prepareConfirm]'s own generation read
// leaves open, now over a set that can be up to three levels deep AND
// multiple base values. A no-op for a top-level family
// (router.ParentFamily(routeFamily) == "").
func (r *Repo) fenceParentTx(ctx context.Context, tx *sql.Tx, workspaceID int64, routeFamily string, bases []ScopeKey, wantPairs []basePair) error {
	parentFamily := router.ParentFamily(routeFamily)
	if parentFamily == "" {
		return nil
	}

	var parentID int64
	err := tx.QueryRowContext(ctx,
		"SELECT id FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, parentFamily,
	).Scan(&parentID)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("confirm %q for workspace %d: %w", routeFamily, workspaceID, ErrParentNotConfirmed)
	default:
		return fmt.Errorf("read parent resource %q for workspace %d: %w", parentFamily, workspaceID, err)
	}

	chain, cerr := r.ancestorChainTx(ctx, tx, workspaceID, routeFamily)
	if cerr != nil {
		return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, cerr)
	}
	livePairs, kerr := chainPairs(ctx, tx, chain, bases)
	if kerr != nil {
		return kerr
	}
	if !slices.Equal(livePairs, wantPairs) {
		return fmt.Errorf("resource confirm for workspace %d: %w", workspaceID, ErrStaleConfig)
	}
	return nil
}

// insertConfirmedResourceTx writes the decision row and the resources row,
// inside tx, and returns the new resource's id — the two INSERTs
// [Repo.Confirm]'s transaction needs before its own entity-insert loop.
func insertConfirmedResourceTx(ctx context.Context, tx *sql.Tx, workspaceID int64, routeFamily string, prep *confirmPrep) (int64, error) {
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO resource_decisions (workspace_id, route_family, state) VALUES (?, ?, 'confirmed')
		ON CONFLICT (workspace_id, route_family) DO UPDATE SET state = 'confirmed'`,
		workspaceID, routeFamily); err != nil {
		return 0, fmt.Errorf("write decision for %q: %w", routeFamily, err)
	}

	// parent_id stays NULL always (D9 — a declared divergence, not a
	// pending one). scope_params carries D5.6's outer parameter names,
	// "[]" for a top-level family, the value every row already held
	// before P3e. seq is the family-wide TOTAL just generated (D5.5 point
	// 4: P×L for a nested family), seed_count the PER-SCOPE count.
	res, err := tx.ExecContext(ctx, `
		INSERT INTO resources
			(workspace_id, route_family, name, id_field, id_strategy, parent_id, scope_params,
			 entity_schema, wrapper, filter_map, write_form, seq, seed_count)
		VALUES (?, ?, ?, ?, 'seq', NULL, ?, ?, ?, '{}', ?, ?, ?)`,
		workspaceID, routeFamily, prep.sugg.Name, prep.sugg.IDField, prep.scopeParamsJSON, prep.sugg.EntitySchema, prep.wrapperJSON,
		prep.writeForm, len(prep.bodies), prep.seedCount)
	if err != nil {
		return 0, fmt.Errorf("insert resource for %q: %w", routeFamily, err)
	}
	resourceID, err := res.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("resource id for %q: %w", routeFamily, err)
	}
	return resourceID, nil
}

// confirmEntityHook is a test-only seam (D13 clause 39: "the injected
// failure is at entity k of N, INSIDE the write transaction"). i is the
// 0-based index just about to be inserted, n is len(bodies). A test sets
// this to fail at a chosen i and restores it to confirmEntityHookNoop
// afterward; production never touches it.
var confirmEntityHook = confirmEntityHookNoop

func confirmEntityHookNoop(i, n int) error { return nil }

// confirmPreWriteHook is a second test-only seam, next to
// [confirmEntityHook]: called once, right after [Repo.prepareConfirm]'s
// generation half returns and right before Confirm's write transaction
// opens — the exact window D4/R36's fence exists to close. A test sets this
// to mutate the workspace's settings (or anything else [fenceConfirmTx]
// reads) in that window and restores it to confirmPreWriteHookNoop
// afterward, proving Confirm ITSELF returns ErrStaleConfig through its own
// real call to fenceConfirmTx — never fenceConfirmTx exercised in
// isolation, which cannot tell apart a Confirm that still calls it from one
// that silently stopped. Production never touches it.
var confirmPreWriteHook = confirmPreWriteHookNoop

func confirmPreWriteHookNoop() {}

// resourceExists reports whether workspaceID already has a resources row
// for routeFamily, over q — the reader pool for Confirm/Decline's
// pre-transaction check, or the write transaction itself for the
// authoritative, race-free one (the writer pool is a single connection, so
// nothing can write between this SELECT and this same transaction's own
// INSERT/DELETE).
func (r *Repo) resourceExists(ctx context.Context, q interface {
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}, workspaceID int64, routeFamily string) (bool, error) {
	var one int
	err := q.QueryRowContext(ctx, "SELECT 1 FROM resources WHERE workspace_id = ? AND route_family = ?", workspaceID, routeFamily).Scan(&one)
	switch {
	case err == nil:
		return true, nil
	case errors.Is(err, sql.ErrNoRows):
		return false, nil
	default:
		return false, fmt.Errorf("check existing resource for %q: %w", routeFamily, err)
	}
}

// findSuggestion looks up routeFamily among specID's derived suggestions
// (EnsureSuggestions — P3a's lazy backfill, so a spec imported before this
// slice existed still resolves). nil, nil when specID is nil (workspace has
// no spec attached) or when no suggestion names routeFamily — the caller
// (Confirm/Decline) already knows an existing resources row makes that
// case a non-issue (D9.4).
func (r *Repo) findSuggestion(ctx context.Context, specID *int64, routeFamily string) (*specs.ResourceSuggestion, error) {
	if specID == nil {
		return nil, nil
	}
	suggestions, err := r.specs.EnsureSuggestions(ctx, *specID)
	if err != nil {
		return nil, fmt.Errorf("load suggestions for spec %d: %w", *specID, err)
	}
	for _, s := range suggestions {
		if s.RouteFamily == routeFamily {
			return s, nil
		}
	}
	return nil, nil
}

// clampSeedCount is D5's "seed_count is the workspace's effective
// settings.listSize, clamped to [1, 200]".
func clampSeedCount(listSize int) int {
	switch {
	case listSize < 1:
		return 1
	case listSize > 200:
		return 200
	default:
		return listSize
	}
}

// --- write_form (D4/R12) ------------------------------------------------

// locateFamilyOperations mirrors internal/specs/derive.go's own family walk
// (router.Build, then router.ListFamily/router.DetailIDParam over each GET
// route) so the detail operation's row id and its id-parameter name are
// found the SAME way derivation found them, never a second, driftable
// lookup. postRoute is nil when the family declares no collection POST at
// all — write_form then stays nil without needing computeWriteForm at all.
func locateFamilyOperations(routes []router.Route, family string) (detail router.Route, idParam string, post *router.Route, err error) {
	table := router.Build(routes, "")
	found := false
	for i := range routes {
		rt := routes[i]
		if strings.ToUpper(rt.Method) != "GET" {
			continue
		}
		if router.ListFamily(table, &rt) != family {
			continue
		}
		detail = rt
		idParam = router.DetailIDParam(&rt)
		found = true
		break
	}
	if !found || idParam == "" {
		return router.Route{}, "", nil, fmt.Errorf("%w: locate detail route for family %q", ErrPopulationFailed, family)
	}
	for i := range routes {
		rt := routes[i]
		if strings.ToUpper(rt.Method) == "POST" && rt.CanonicalPath == family {
			p := rt
			post = &p
			break
		}
	}
	return detail, idParam, post, nil
}

// computeWriteForm is D4/R12's TWO-hop walk, written exactly as the design
// gives it: the first hop resolves a $ref of OpPointer+"/requestBody" (the
// only form that answers an anchor for BOTH an inline and a referenced
// body — handing ResolveNodePointer an already-chased value or a bare
// pointer STRING both give an empty anchor and kill this on every spec).
// nil on ANY error from either hop, on an empty/absent/non-object content
// map (checked WITHOUT calling specs.SelectMediaType, which panics on an
// empty map), and on a non-JSON selected media type — never an error
// itself: a POST with no request body is legal and common (D4).
func computeWriteForm(resolver *openapi.Resolver, postVariants []gen.ResponseVariant, entitySchemaPtr string) *string {
	if len(postVariants) == 0 {
		return nil
	}
	opPointer := postVariants[0].OpPointer
	if opPointer == "" {
		return nil
	}

	anchor, reqBodyNode, err := resolver.ResolveNodePointer(map[string]any{"$ref": opPointer + "/requestBody"})
	if err != nil {
		return nil
	}
	reqBodyMap, ok := reqBodyNode.(map[string]any)
	if !ok {
		return nil
	}
	content, ok := reqBodyMap["content"].(map[string]any)
	if !ok || len(content) == 0 {
		return nil
	}

	media := specs.SelectMediaType(content)
	if !httpx.IsJSONMediaType(media) {
		return nil
	}

	schemaPtr := anchor + "/content/" + openapi.EscapePointerToken(media) + "/schema"
	_, schemaNode, err := resolver.ResolveNodePointer(map[string]any{"$ref": schemaPtr})
	if err != nil {
		return nil
	}
	schemaMap, ok := schemaNode.(map[string]any)
	if !ok {
		return nil
	}

	itemNode, err := resolver.Resolve(entitySchemaPtr)
	if err != nil {
		return nil
	}
	itemMap, ok := itemNode.(map[string]any)
	if !ok {
		return nil
	}

	if reflect.DeepEqual(schemaMap, itemMap) {
		bare := "bare"
		return &bare
	}
	return nil
}

// defaultVariant200 mirrors internal/specs/derive.go's own defaultVariant,
// over [gen.ResponseVariant] instead of specs.Response — the IsDefault row,
// only when its HTTPStatus is 200 (R13: "the variant is the one
// chooseVariant would pick", and a suggestion is only ever derived when
// that row's status is 200, so a miss here means the spec changed
// underneath a fenced spec_id, which cannot happen).
func defaultVariant200(variants []gen.ResponseVariant) (gen.ResponseVariant, bool) {
	for _, v := range variants {
		if v.IsDefault && v.HTTPStatus == 200 {
			return v, true
		}
	}
	return gen.ResponseVariant{}, false
}
