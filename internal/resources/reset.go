// reset.go is D3's whole reset-data verb (decisions.md, mocker-p3b-resources):
// POST /api/workspaces/{id}/reset-data's two modes, "reseed" and "clear",
// entirely inside this package (D3 R5) — it opens its own [store.DB.Write],
// declares its own fence ([fenceResetTx], deliberately NOT [fenceConfirmTx]),
// and needs nothing from internal/checkpoints: there is nothing to
// snapshot, since the verb changes no configuration and P3b captures no
// entity data at all.
//
// The two modes share almost everything and differ in exactly three
// places: whether a population is prepared at all (reseed only), what the
// DELETE is scoped to (reseed: the non-skipped families it just prepared a
// population for; clear: the whole workspace, so a family confirmed after
// the roster read still loses its rows — D8), and whether anything is
// INSERTed back. Splitting them into two functions would duplicate the
// fence, the slug check and the no-op count; one function with a mode
// argument is D3's own shape.
package resources

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"maps"
	"slices"
	"strconv"
	"time"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/router"
)

// ResetMode is reset-data's `mode` field, typed so the HANDLER — the only
// place that decodes the raw wire value — can tell an ABSENT field apart
// from an EMPTY string before either collapses into this type (D3, clause
// 6): `ResetMode("")` is indistinguishable from `ResetMode` unset once a
// Go value exists, so the absent/empty distinction has to be made before
// one is constructed, not after.
type ResetMode string

const (
	// ResetModeReseed repopulates every confirmed family from the
	// workspace's CURRENT configuration (D3: "not the population the
	// confirm produced").
	ResetModeReseed ResetMode = "reseed"
	// ResetModeClear deletes every entity row and mints nothing back.
	ResetModeClear ResetMode = "clear"
)

// The FOUR reasons [Repo.ResetData] can SKIP a family during a reseed
// (D3 R8, D8.2/D8.3) — never a whole-call refusal, because one bad family
// must not block the workspace's only data-reset verb forever.
const (
	skipReasonStranded         = "stranded"
	skipReasonOverCaps         = "over_caps"
	skipReasonPopulationFailed = "population_failed"
	// skipReasonGroupSkipped is D8.2 rule 2's fourth reason: a GROUP (a
	// parent and its confirmed nested children) is repopulated atomically
	// or skipped atomically. When one member fails for any of the three
	// reasons above, every OTHER member of the same group — including a
	// parent that generated successfully on its own — is skipped too,
	// reporting this reason; the member that actually failed still
	// reports its own. Without it, a child's over_caps (far likelier than
	// the parent's, D5.4's own arithmetic) would leave the parent
	// repopulated with NEW keys beside a child still scoped to the OLD
	// ones — DESIGN.md's "orphaned records resurrect", arriving via
	// reseed instead of a live decline (D9.1 names the shape).
	skipReasonGroupSkipped = "group_skipped"
)

// SkippedFamily is one entry of [ResetOutcome.Skipped]: a confirmed family
// a reseed left standing rather than repopulating, and why.
type SkippedFamily struct {
	RouteFamily string
	Reason      string // "stranded" | "over_caps" | "population_failed" | "group_skipped"
}

// ResetOutcome is [Repo.ResetData]'s whole answer (D8). Changed is true
// when rows were deleted OR inserted — not "the call did something": a
// call over an empty roster, or a reseed in which every family was
// skipped, reports Changed=false even though it ran to completion.
type ResetOutcome struct {
	Changed bool
	Deleted int64
	Skipped []SkippedFamily
}

// preparedFamily is one confirmed family's already-generated population,
// built OUTSIDE any transaction (mirroring [confirmPrep]'s own reason: a
// generator built over a 347 KB document must not hold the single writer
// connection for the walk). Only [ResetModeReseed] ever populates this
// slice — [ResetModeClear] needs no spec at all.
type preparedFamily struct {
	resourceID  int64
	routeFamily string
	bodies      [][]byte
	// baseScopeKeys/scopeKeys are parallel to bodies (D5.5/D8.2/D6.1's
	// shared shape, see [populatePairs]): baseScopeKeys is all "" (and
	// scopeKeys all "") for a top-level family under an unparameterised
	// basePath, or one entry per (declared base value, ancestor tuple)
	// PAIR otherwise — base outer order, route scope inner order (D6.1).
	baseScopeKeys []ScopeKey
	scopeKeys     []ScopeKey
	// keys is D8.3's own obligation on this type: the flat, family-wide
	// entity-key strings ("1".."N"), one per body, in the SAME order as
	// bodies/baseScopeKeys/scopeKeys. It is what makes a level's own
	// PREPARED keys available to the level below it without re-deriving
	// them: the next level's keysByParent is built by chunking this
	// slice's PER-BASE restriction ([preparedFamily.keysForBase]) into
	// PER-SCOPE groups (D8.3's positional chunking, [chunkKeys]), never
	// by reading this family's LIVE rows — those are what the DELETE
	// step is about to remove.
	keys []string
}

// keysForBase returns p's own PREPARED key list restricted to base, in the
// SAME relative order as p.bodies/p.keys (D6.1's base-outer, scope-inner
// pair order is preserved by a stable filter). This is what
// [Repo.prepareGroupPopulation] needs to scope a NESTED child positionally
// under its parent's rows of THAT base alone — the walk stays inside one
// base value at a time, exactly like [chainScopes] does for the live key
// source (D6.1) — never a raw decode of an encoded [ScopeKey], which D3.2
// bans by name; base is compared as the exact key [populatePairs] already
// tagged each body with, never re-derived.
func (p preparedFamily) keysForBase(base ScopeKey) []string {
	var keys []string
	for i, b := range p.baseScopeKeys {
		if b == base {
			keys = append(keys, p.keys[i])
		}
	}
	return keys
}

// resetPreWriteHook is [confirmPreWriteHook]'s sibling for this verb: called
// once, right after the pre-transaction preparation returns and right
// before the write transaction opens — the exact window [fenceResetTx] and
// the roster fence exist to close. A test sets this to mutate the
// workspace (or its resources) in that window and restores it to
// resetPreWriteHookNoop afterward, proving ResetData ITSELF answers
// ErrStaleConfig through its own real calls rather than those functions
// exercised in isolation. Production never touches it.
var resetPreWriteHook = resetPreWriteHookNoop

func resetPreWriteHookNoop() {}

// compareConfirmSlug is D3 R9's two-way refusal, shared by both the
// pre-transaction vacuous check and the authoritative in-transaction one:
// empty is ErrConfirmSlugRequired, present-but-wrong is
// ErrConfirmSlugMismatch — the same split [Repo.Decline] already makes
// inline for its own confirmSlug (repo.go's Decline), reproduced here as
// its own function because ResetData compares it TWICE (D3's ordered
// listing, step 3, plus the pre-check outside the transaction) and a
// helper is cheaper than two hand-copies drifting apart.
func compareConfirmSlug(confirmSlug, actualSlug string) error {
	switch {
	case confirmSlug == "":
		return ErrConfirmSlugRequired
	case confirmSlug != actualSlug:
		return ErrConfirmSlugMismatch
	default:
		return nil
	}
}

// fenceResetTx is D3/D14.1's own fence, run INSIDE ResetData's write
// transaction — NOT [fenceConfirmTx], and R9 is explicit about why: that
// function also compares `slug`, and if this one did too, a concurrent
// RENAME would answer 409 stale_config here before the in-transaction slug
// comparison (D3's own step 3) ever ran, making that step dead code and
// clause 7's rename half either vacuous or wrong. Slug proves the operator
// meant THIS workspace; created_at (SECOND-resolution, unlike a
// monotonically-bumped revision) proves the row itself was not deleted and
// recreated underneath the call.
//
// wantSeed/wantListSize are read only for [ResetModeReseed]; a
// [ResetModeClear] caller passes zero values and this function ignores
// them entirely — clause 10's other half ("with mode: clear the same edit
// succeeds") is exactly this early return.
//
// wantBasePath/wantBasePathValues join the same UNCONDITIONAL-within-reseed
// comparison [fenceConfirmTx] gives them under D6.5, for the identical
// reason: [Repo.prepareReset] (P20b) reads the declared set from the
// WORKSPACE's own raw settings, never from a scenario overlay, so there is
// no active-scenario branch to be conditional on here either — a settings
// edit landing in [resetPreWriteHook]'s window must be caught whether or
// not a scenario happens to be active, exactly like Seed/ListSize's own
// neighbour check below is NOT exactly like (that one genuinely does
// depend on scenario state, because Seed/ListSize themselves come from the
// scenario when one is active).
func fenceResetTx(ctx context.Context, tx *sql.Tx, workspaceID int64, before workspaceCore, mode ResetMode, wantSeed int64, wantListSize int, wantBasePath string, wantBasePathValues []string) error {
	var (
		createdAt          int64
		settingsJSON       string
		specID, scenarioID sql.NullInt64
	)
	err := tx.QueryRowContext(ctx,
		"SELECT created_at, spec_id, scenario_id, settings FROM workspaces WHERE id = ?", workspaceID,
	).Scan(&createdAt, &specID, &scenarioID, &settingsJSON)
	switch {
	case err == nil:
	case errors.Is(err, sql.ErrNoRows):
		return fmt.Errorf("workspace %d: %w", workspaceID, ErrWorkspaceNotFound)
	default:
		return fmt.Errorf("read workspace %d inside transaction: %w", workspaceID, err)
	}

	if createdAt != before.createdAt {
		return fmt.Errorf("reset data for workspace %d: %w", workspaceID, ErrStaleConfig)
	}
	if mode != ResetModeReseed {
		return nil
	}

	if !nullIntEqual(specID, before.specID) || !nullIntEqual(scenarioID, before.scenarioID) {
		return fmt.Errorf("reset data for workspace %d: %w", workspaceID, ErrStaleConfig)
	}

	// Parsed unconditionally — not inside the `!scenarioID.Valid` branch
	// below — because basePath/basePathValues are never taken from a
	// scenario (D4.5) and the population fenceResetTx is guarding was
	// already prepared against `before`'s declared set regardless of
	// scenario state.
	settings, perr := domain.ParseSettings([]byte(settingsJSON))
	if perr != nil {
		return fmt.Errorf("reset data: parse workspace %d settings: %w", workspaceID, perr)
	}
	if settings.BasePath != wantBasePath || !slices.Equal(settings.BasePathValues, wantBasePathValues) {
		return fmt.Errorf("reset data for workspace %d: %w", workspaceID, ErrStaleConfig)
	}

	if !scenarioID.Valid {
		if settings.Seed != wantSeed || settings.ListSize != wantListSize {
			return fmt.Errorf("reset data for workspace %d: %w", workspaceID, ErrStaleConfig)
		}
	}
	return nil
}

// rosterMatchesTx is D3 R10's roster fence, [ResetModeReseed] only: re-read
// which (resources.id, route_family) pairs the workspace currently has
// confirmed and compare that set to want — the EXACT set
// [Repo.prepareReset]'s own roster read produced. Any difference (a family
// confirmed OR declined in the window between that read and this
// transaction) answers ErrStaleConfig and writes nothing: a family
// confirmed in that window had no population prepared for it, and a
// family declined in it no longer has a resources row for the DELETE
// below to target correctly.
func rosterMatchesTx(ctx context.Context, tx *sql.Tx, workspaceID int64, want map[int64]string) error {
	rows, err := tx.QueryContext(ctx, "SELECT id, route_family FROM resources WHERE workspace_id = ?", workspaceID)
	if err != nil {
		return fmt.Errorf("re-read resource roster for workspace %d: %w", workspaceID, err)
	}
	defer func() { _ = rows.Close() }()

	got := make(map[int64]string, len(want))
	for rows.Next() {
		var id int64
		var family string
		if err := rows.Scan(&id, &family); err != nil {
			return fmt.Errorf("scan resource roster row for workspace %d: %w", workspaceID, err)
		}
		got[id] = family
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate resource roster for workspace %d: %w", workspaceID, err)
	}

	if !maps.Equal(got, want) {
		return fmt.Errorf("reset data for workspace %d: %w", workspaceID, ErrStaleConfig)
	}
	return nil
}

// familyGroups partitions roster into D8.2's atomic reseed GROUPS: a
// ROOT — a confirmed family whose router.ParentFamily names no CONFIRMED
// family in THIS roster — together with every confirmed family below it,
// TRANSITIVELY (D3.3's subtree, not just direct children: the depth ceiling
// is three, D3.1, so a subtree can be up to three levels deep). group[0]
// is always the root, and every member after it is ordered TOP DOWN — a
// breadth-first walk, level by level — because D8.3's own positional
// chunking needs a family's parent already resolved (prepared or skipped)
// by the time this function's caller reaches it, which only a top-down
// order guarantees.
//
// A family whose parent is a non-empty string absent from THIS roster
// (declined, or never confirmed) is treated as its OWN root: D5.2's
// invariant (a confirmed nested family always has a confirmed parent)
// makes that case unreachable in a consistent database, so this is a
// defensive fallback — the same one [Repo.prepareGroupPopulation] takes
// for a root that turns out to carry outer path parameters of its own —
// rather than silently dropping the row from consideration.
func familyGroups(roster []*Resource) [][]*Resource {
	inRoster := make(map[string]bool, len(roster))
	for _, res := range roster {
		inRoster[res.RouteFamily] = true
	}

	childrenOf := make(map[string][]*Resource, len(roster))
	var roots []*Resource
	for _, res := range roster {
		parent := router.ParentFamily(res.RouteFamily)
		if parent == "" || !inRoster[parent] {
			roots = append(roots, res)
			continue
		}
		childrenOf[parent] = append(childrenOf[parent], res)
	}

	groups := make([][]*Resource, 0, len(roots))
	for _, root := range roots {
		group := []*Resource{root}
		queue := []*Resource{root}
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			children := childrenOf[cur.RouteFamily]
			group = append(group, children...)
			queue = append(queue, children...)
		}
		groups = append(groups, group)
	}
	return groups
}

// chunkKeys is D8.3's positional chunking: the first width keys belong to
// the first parent scope, the next width to the second, and so on — no
// lookup, no match on scope_key, no map. width is the PARENT's own
// seed_count (its per-scope list size), and keys is that parent's own
// FLAT, family-wide key list ([preparedFamily.keys]) — never its live
// rows. The order is fixed entirely by the caller's own construction: keys
// is already in scope order (D5.5's one counter across scopes), so
// chunking it positionally reproduces exactly the per-scope grouping the
// parent's own population used.
func chunkKeys(keys []string, width int) [][]string {
	if width <= 0 {
		return nil
	}
	chunks := make([][]string, 0, (len(keys)+width-1)/width)
	for i := 0; i < len(keys); i += width {
		end := min(i+width, len(keys))
		chunks = append(chunks, keys[i:end])
	}
	return chunks
}

// prepareFamilyPopulation is prepareReset's per-family generation step,
// shared by every member of a D8.2 group regardless of depth: the caller
// ([Repo.prepareGroupPopulation]) has already decided pairs — {"", ""} for
// the group's own root under one declared base value, or the level
// above's own PREPARED tuples extended by its PREPARED keys FOR THAT SAME
// BASE for anything below it (D8.2 rule 1/D6.1 — never the parent's live
// rows, which the DELETE step is about to remove). This function itself
// makes no scope decision at all. Returns a zero preparedFamily and one of
// the three ordinary skip reasons on an ordinary failure; err is reserved
// for an infrastructure failure that must abort the whole reset-data call
// rather than skip one family.
func (r *Repo) prepareFamilyPopulation(generator *gen.Generator, routes []router.Route, variants map[int64][]gen.ResponseVariant, res *Resource, pairs []basePair) (preparedFamily, string, error) {
	// The three nil returns below are deliberate, not swallowed errors
	// (this function's own contract, stated in its doc comment): the
	// underlying cause is CLASSIFIED into the returned reason string —
	// exactly the discrimination [Repo.prepareReset]'s own doc comment
	// already describes for [Repo.OrphanedFamilies]'s miss — and err stays
	// reserved for an infrastructure failure the caller must abort on.
	detailRoute, idParam, _, lerr := locateFamilyOperations(routes, res.RouteFamily)
	if lerr != nil {
		return preparedFamily{}, skipReasonPopulationFailed, nil //nolint:nilerr // classified, see comment above
	}
	detailVariant, ok := defaultVariant200(variants[detailRoute.OpRowID])
	if !ok {
		return preparedFamily{}, skipReasonPopulationFailed, nil
	}
	// Same count-before-bodies guard as prepareConfirm: over_caps is
	// decided from the pair count alone before anything is generated.
	if int64(len(pairs))*res.SeedCount > r.maxEntityRows {
		return preparedFamily{}, skipReasonOverCaps, nil
	}
	bodies, baseKeys, scopeKeys, perr := populatePairs(generator, detailVariant, res.RouteFamily, idParam, int(res.SeedCount), pairs, res.IDField, res.Wrapper.IDType)
	if perr != nil {
		return preparedFamily{}, skipReasonPopulationFailed, nil //nolint:nilerr // classified, see comment above
	}
	if cerr := r.checkBatchCaps(bodies); cerr != nil {
		return preparedFamily{}, skipReasonOverCaps, nil //nolint:nilerr // classified, see comment above
	}
	keys := make([]string, len(bodies))
	for i := range keys {
		keys[i] = strconv.Itoa(i + 1)
	}
	return preparedFamily{resourceID: res.ID, routeFamily: res.RouteFamily, bodies: bodies, baseScopeKeys: baseKeys, scopeKeys: scopeKeys, keys: keys}, "", nil
}

// prepareGroupPopulation processes one D8.2 SUBTREE group top down —
// group's own order, fixed by [familyGroups]' breadth-first walk, which is
// what guarantees a family's parent is already resolved (succeeded,
// failed, or blocked) by the time this reaches it. Every family populates
// once per declared base value (D6.1 — bases is the CURRENT declared set
// [Repo.prepareReset] computed from the workspace's own settings, D4.5/
// P20b): a family with no parent IN THIS GROUP (group[0], the root)
// populates the single top-level route scope [""] under each base —
// D5.2's invariant makes a genuinely nested, group-external-parent root
// unreachable, the same defensive posture [familyGroups]' own fallback
// already takes. Every other family re-scopes to its OWN parent's
// PREPARED tuples and keys FOR THAT SAME BASE ([preparedFamily.keysForBase]),
// positionally chunked by the parent's own seed_count ([chunkKeys]) and
// extended through the identical [extendScopes] arithmetic [chainScopes]
// calls with the LIVE key source (D5.3 rule 3/D6.1 — one arithmetic, two
// key sources) — never the parent's live rows, which the DELETE step is
// about to remove.
//
// If ANY member fails for ANY reason (D8.2 rule 2), the WHOLE group is
// skipped: every other member — including one that had already
// succeeded, and including one BLOCKED because its own parent failed
// rather than failing on its own — reports group_skipped, while the
// member that actually failed (or was stranded) keeps its own reason.
func (r *Repo) prepareGroupPopulation(generator *gen.Generator, routes []router.Route, variants map[int64][]gen.ResponseVariant, group []*Resource, stranded map[string]bool, bases []ScopeKey) ([]preparedFamily, []SkippedFamily, error) {
	resourceByFamilyName := make(map[string]*Resource, len(group))
	for _, res := range group {
		resourceByFamilyName[res.RouteFamily] = res
	}

	succeeded := make(map[string]preparedFamily, len(group))
	// tuplesByFamily is FAMILY-LEVEL presence (D8.2's own "no prepared key
	// set to scope against" check, unmoved): a family absent from this map
	// failed, was stranded, or was blocked — never distinguished from "the
	// family succeeded with zero live rows under every base," which the
	// per-base map value being empty at every key already expresses
	// without an extra nil check.
	tuplesByFamily := make(map[string]map[ScopeKey][][]string, len(group))
	ownReason := make(map[string]string, len(group)) // family -> its OWN failure reason, when it has one
	groupFailed := false

	for _, res := range group {
		if stranded[res.RouteFamily] {
			ownReason[res.RouteFamily] = skipReasonStranded
			groupFailed = true
			continue
		}

		parentFamily := router.ParentFamily(res.RouteFamily)
		if parentFamily != "" && tuplesByFamily[parentFamily] == nil {
			// The parent already failed, was stranded, or was itself
			// blocked — there is no prepared key set to scope against,
			// so this family is BLOCKED rather than attempted. It gets
			// no own reason here; the final pass below reports every
			// family with no own reason as group_skipped.
			groupFailed = true
			continue
		}

		var pairs []basePair
		tuplesForFamily := make(map[ScopeKey][][]string, len(bases))
		for _, base := range bases {
			var scopes []ScopeKey
			var tuples [][]string
			if parentFamily == "" {
				tuples = [][]string{{}}
				scopes = []ScopeKey{""}
			} else {
				parentRes := resourceByFamilyName[parentFamily]
				keysByParent := chunkKeys(succeeded[parentFamily].keysForBase(base), int(parentRes.SeedCount))
				tuples, scopes = extendScopes(tuplesByFamily[parentFamily][base], keysByParent)
			}
			tuplesForFamily[base] = tuples
			for _, s := range scopes {
				pairs = append(pairs, basePair{Base: base, Scope: s})
			}
		}

		prep, reason, gerr := r.prepareFamilyPopulation(generator, routes, variants, res, pairs)
		if gerr != nil {
			return nil, nil, gerr
		}
		if reason != "" {
			ownReason[res.RouteFamily] = reason
			groupFailed = true
			continue
		}
		succeeded[res.RouteFamily] = prep
		tuplesByFamily[res.RouteFamily] = tuplesForFamily
	}

	if !groupFailed {
		prepared := make([]preparedFamily, 0, len(group))
		for _, res := range group {
			prepared = append(prepared, succeeded[res.RouteFamily])
		}
		return prepared, nil, nil
	}

	var skipped []SkippedFamily
	for _, res := range group {
		if reason, ok := ownReason[res.RouteFamily]; ok {
			skipped = append(skipped, SkippedFamily{RouteFamily: res.RouteFamily, Reason: reason})
			continue
		}
		skipped = append(skipped, SkippedFamily{RouteFamily: res.RouteFamily, Reason: skipReasonGroupSkipped})
	}
	return nil, skipped, nil
}

// prepareReset is D3's whole generation half, run OUTSIDE any transaction
// exactly like [Repo.prepareConfirm] (R11's identical reasoning): building
// a generator and walking the spec must not hold the single writer
// connection. Returns nil, nil, effSettings for [ResetModeClear] — that
// mode needs no spec and has none of reseed's four skip cases (D3: "clear
// has none of these cases").
//
// Unlike [Repo.prepareConfirm], each family's generation inputs (id field,
// id type, seed_count) come from the `resources` ROW roster itself
// (D3 R7) — NOT from a suggestion, which this function only consults to
// decide STRANDEDNESS (R8, mocker-p4a-triage D5/R1): [Repo.OrphanedFamilies]
// decides `stranded` for the WHOLE roster, in one round trip, BEFORE the
// spec is ever walked, because [locateFamilyOperations]
// wraps its own miss in the identical ErrPopulationFailed sentinel a
// generation failure returns, and discriminating on that sentinel alone
// would report every stranded family as population_failed instead.
//
// D8.2's group rule is layered on top of that per-family logic: stranded
// is knowable per family with no dependency on anything else, so it is
// computed for the WHOLE roster up front, and [Repo.prepareGroupPopulation]
// applies D8.2 rule 2 (a group repopulates or is skipped atomically) to
// each SUBTREE [familyGroups] partitions the roster into, at whatever
// depth it reaches (D3.1's ceiling is three).
// The two extra return values, wantBasePath/wantBasePathValues, are
// [fenceResetTx]'s own D6.5 obligation: the WORKSPACE's own raw
// basePath/basePathValues, read here from the same `settings` parse the
// population below is built against — never from effSettings, for the
// identical P20b reason the declared-set read three lines down already
// documents — so that [Repo.ResetData] has something to fence the write
// transaction against without re-parsing core.settingsJSON a second time
// and risking the two reads disagreeing about what "current" meant.
func (r *Repo) prepareReset(ctx context.Context, workspaceID int64, mode ResetMode, core workspaceCore, roster []*Resource) ([]preparedFamily, []SkippedFamily, domain.Settings, string, []string, error) {
	if mode != ResetModeReseed {
		return nil, nil, domain.Settings{}, "", nil, nil
	}

	settings, err := domain.ParseSettings([]byte(core.settingsJSON))
	if err != nil {
		return nil, nil, domain.Settings{}, "", nil, fmt.Errorf("reset data: parse workspace %d settings: %w", workspaceID, err)
	}
	effSettings := r.effectiveSettings(ctx, workspaceID, settings, core.scenarioID)

	// P20b: the declared base-scope set is read off `settings` — the
	// WORKSPACE's own raw parse, two lines up — never `effSettings`, which
	// [Repo.effectiveSettings] hands back as the ACTIVE SCENARIO's own
	// captured settings when one is active (that function's own doc
	// comment: unlike composeScenarioLayer it does not restore
	// basePath/basePathValues from the workspace). Reaching for effSettings
	// here — the natural mistake, one field past where seedCount already
	// reads it below — would repopulate the SCENARIO's declared base
	// scopes while a scenario is active, exactly the substitution D4.5
	// forbids and P20b's own test exists to catch.
	bases := DeclaredBaseScopes(settings.BasePath, settings.BasePathValues)
	if len(bases) == 0 {
		// D6.4's own condition (basePath carries a parameter with an
		// empty declared set) reached from the reset path: D8 assigns no
		// fifth skip reason to it, reusing population_failed — every
		// family in every group is skipped on that ground, uniformly,
		// without ever building a generator.
		var skipped []SkippedFamily
		for _, res := range roster {
			skipped = append(skipped, SkippedFamily{RouteFamily: res.RouteFamily, Reason: skipReasonPopulationFailed})
		}
		return nil, skipped, effSettings, settings.BasePath, settings.BasePathValues, nil
	}

	families := make([]string, len(roster))
	for i, res := range roster {
		families[i] = res.RouteFamily
	}
	stranded, serr := r.OrphanedFamilies(ctx, core.specID, families)
	if serr != nil {
		return nil, nil, domain.Settings{}, "", nil, fmt.Errorf("reset data for workspace %d: %w", workspaceID, serr)
	}
	anyNonStranded := false
	for _, family := range families {
		if !stranded[family] {
			anyNonStranded = true
			break
		}
	}

	groups := familyGroups(roster)

	// Nothing in the whole roster has a live suggestion: every group is
	// entirely stranded, and there is nothing to generate for any of
	// them — skip building the generator at all, the same optimisation
	// P3b's own single-level code already made (there building a
	// 347 KB-document generator only to discover the one group's root
	// is stranded would be wasted work).
	if !anyNonStranded {
		var skipped []SkippedFamily
		for _, group := range groups {
			for _, res := range group {
				skipped = append(skipped, SkippedFamily{RouteFamily: res.RouteFamily, Reason: skipReasonStranded})
			}
		}
		return nil, skipped, effSettings, settings.BasePath, settings.BasePathValues, nil
	}

	// core.specID != nil here, and the reason is the ROSTER rather than the
	// predicate. A confirmed resource can only exist for a workspace with a
	// bound spec — Confirm refuses otherwise — so a nil core.specID comes
	// with an empty roster, the loop above runs zero times, anyNonStranded
	// stays false and this line is unreachable. Note what does NOT hold:
	// [Repo.OrphanedFamilies] on a nil specID returns an EMPTY map, so every
	// family reads as NOT orphaned through a map miss, which is the opposite
	// of "every family orphaned" — an earlier version of this comment claimed
	// the latter and would have sent a reader looking for safety in the wrong
	// place. If a future slice ever lets a spec be unbound while confirmed
	// resources remain, this dereference needs a real guard and the invariant
	// above is where it broke.
	generator, _, err := r.buildGenerator(ctx, *core.specID, effSettings)
	if err != nil {
		return nil, nil, domain.Settings{}, "", nil, fmt.Errorf("reset data for workspace %d: %w", workspaceID, err)
	}
	routes, err := r.specs.Routes(ctx, *core.specID)
	if err != nil {
		return nil, nil, domain.Settings{}, "", nil, fmt.Errorf("reset data: load routes for workspace %d: %w", workspaceID, err)
	}
	variants, err := r.specs.Variants(ctx, *core.specID)
	if err != nil {
		return nil, nil, domain.Settings{}, "", nil, fmt.Errorf("reset data: load variants for workspace %d: %w", workspaceID, err)
	}

	var prepared []preparedFamily
	var skipped []SkippedFamily
	for _, group := range groups {
		gp, gs, gerr := r.prepareGroupPopulation(generator, routes, variants, group, stranded, bases)
		if gerr != nil {
			return nil, nil, domain.Settings{}, "", nil, gerr
		}
		prepared = append(prepared, gp...)
		skipped = append(skipped, gs...)
	}
	return prepared, skipped, effSettings, settings.BasePath, settings.BasePathValues, nil
}

// ResetData is D3's whole verb. Outside any transaction: read the
// workspace's identity, refuse a wrong confirmSlug against it (the
// vacuous pre-check — the ONLY check an empty-roster call ever reaches),
// read the confirmed-family roster and answer 200 changed:false when it
// is empty (clause 19a: neither mode reads the spec on this path), and —
// for reseed only — prepare every family's population. Then one
// [store.DB.Write]: fence, re-fence the roster (reseed), re-compare the
// slug (both, authoritative this time), decide the no-op case from a
// COUNT taken inside this same transaction (clause 14: never from the
// pre-transaction read), delete, and — for reseed — insert and reset each
// repopulated family's seq counter.
func (r *Repo) ResetData(ctx context.Context, workspaceID int64, mode ResetMode, confirmSlug string) (ResetOutcome, error) {
	core, err := r.readWorkspaceCore(ctx, workspaceID)
	if err != nil {
		return ResetOutcome{}, err
	}
	if serr := compareConfirmSlug(confirmSlug, core.slug); serr != nil {
		return ResetOutcome{}, serr
	}

	roster, err := r.ForWorkspace(ctx, workspaceID)
	if err != nil {
		return ResetOutcome{}, fmt.Errorf("reset data for workspace %d: %w", workspaceID, err)
	}
	if len(roster) == 0 {
		return ResetOutcome{}, nil
	}

	prepared, skipped, effSettings, wantBasePath, wantBasePathValues, err := r.prepareReset(ctx, workspaceID, mode, core, roster)
	if err != nil {
		return ResetOutcome{}, err
	}
	resetPreWriteHook()

	rosterWant := make(map[int64]string, len(roster))
	for _, res := range roster {
		rosterWant[res.ID] = res.RouteFamily
	}

	var out ResetOutcome
	writeErr := r.db.Write(ctx, func(tx *sql.Tx) error {
		o, terr := r.resetTx(ctx, tx, workspaceID, core, mode, confirmSlug, rosterWant, prepared, skipped, effSettings, wantBasePath, wantBasePathValues)
		if terr != nil {
			return terr
		}
		out = o
		return nil
	})
	if writeErr != nil {
		return ResetOutcome{}, writeErr
	}
	return out, nil
}

// resetTx is [Repo.ResetData]'s write-transaction body — D3's ordered
// listing, steps 1 through 7, split out of ResetData itself purely to keep
// that method's own branching (mode dispatch, the early empty-roster
// return) from folding together with this one's (the fence chain, the
// no-op count, the delete/insert/seq-write shape) into a single function
// whose cyclomatic complexity the style note on prepareConfirm/confirmPrep
// already explains the same split for.
func (r *Repo) resetTx(ctx context.Context, tx *sql.Tx, workspaceID int64, core workspaceCore, mode ResetMode, confirmSlug string, rosterWant map[int64]string, prepared []preparedFamily, skipped []SkippedFamily, effSettings domain.Settings, wantBasePath string, wantBasePathValues []string) (ResetOutcome, error) {
	// Step 1: fenceResetTx — created_at (both modes), spec_id/scenario_id,
	// basePath/basePathValues (reseed only, unconditionally — D6.5) and
	// (no scenario) seed/listSize (reseed only). Deliberately not slug —
	// see fenceResetTx's own doc comment.
	if ferr := fenceResetTx(ctx, tx, workspaceID, core, mode, effSettings.Seed, effSettings.ListSize, wantBasePath, wantBasePathValues); ferr != nil {
		return ResetOutcome{}, ferr
	}

	// Step 2: the roster fence, reseed only (D8: clear's DELETE is
	// workspace-scoped and needs no roster at all).
	if mode == ResetModeReseed {
		if rerr := rosterMatchesTx(ctx, tx, workspaceID, rosterWant); rerr != nil {
			return ResetOutcome{}, rerr
		}
	}

	// Step 3: the AUTHORITATIVE slug comparison, re-read inside this
	// transaction — the pre-check in ResetData is vacuous against a
	// concurrent rename, this one is not.
	var liveSlug string
	if err := tx.QueryRowContext(ctx, "SELECT slug FROM workspaces WHERE id = ?", workspaceID).Scan(&liveSlug); err != nil {
		return ResetOutcome{}, fmt.Errorf("read workspace %d slug inside transaction: %w", workspaceID, err)
	}
	if serr := compareConfirmSlug(confirmSlug, liveSlug); serr != nil {
		return ResetOutcome{}, serr
	}

	// Step 4: the no-op count, taken HERE — inside the transaction, over
	// live state — never from the pre-transaction roster/preparation
	// read (clause 14's own mutation target).
	entityCount, cerr := countEntitiesTx(ctx, tx, workspaceID)
	if cerr != nil {
		return ResetOutcome{}, cerr
	}
	if entityCount == 0 && (mode == ResetModeClear || len(prepared) == 0) {
		return ResetOutcome{Changed: false, Skipped: skipped}, nil
	}

	// Step 5: DELETE — workspace-scoped for clear (D8: a family confirmed
	// after the roster read still loses its rows), scoped to the
	// non-skipped families' resource ids for reseed.
	var deleted int64
	var derr error
	if mode == ResetModeClear {
		deleted, derr = deleteEntitiesWorkspaceTx(ctx, tx, workspaceID)
	} else {
		ids := make([]int64, len(prepared))
		for i, p := range prepared {
			ids[i] = p.resourceID
		}
		deleted, derr = deleteEntitiesForResourcesTx(ctx, tx, ids)
	}
	if derr != nil {
		return ResetOutcome{}, derr
	}

	// Steps 6 and 7: INSERT the prepared rows and reset each repopulated
	// family's seq — reseed only. clear leaves seq exactly where the
	// client's own POSTs left it (D3: "this tree never reuses an id").
	var inserted int64
	if mode == ResetModeReseed {
		now := time.Now().UTC()
		for _, p := range prepared {
			for i, body := range p.bodies {
				entityKey := p.keys[i]
				base := p.baseScopeKeys[i]
				scope := p.scopeKeys[i]
				if _, err := tx.ExecContext(ctx, `
					INSERT INTO entities (resource_id, base_scope_key, scope_key, entity_key, data, created_at, updated_at)
					VALUES (?, ?, ?, ?, ?, ?, ?)`,
					p.resourceID, string(base), string(scope), entityKey, string(body), now.Unix(), now.Unix()); err != nil {
					return ResetOutcome{}, fmt.Errorf("insert entity %d of %d for %q: %w", i+1, len(p.bodies), p.routeFamily, err)
				}
				inserted++
			}
			// D8.2 rule 3 (the SECOND writer of this column, beside
			// insertConfirmedResourceTx): seq is the family's TOTAL row
			// count just inserted, across every scope — never the
			// PER-SCOPE count, which len(p.bodies) would be if a nested
			// family's preparation were keyed per scope instead of
			// concatenated (it is not: populateScoped already returns one
			// flat slice). Writing the per-scope count here mints a
			// duplicate id on the very next POST X (P8g).
			if _, err := tx.ExecContext(ctx, "UPDATE resources SET seq = ? WHERE id = ?", int64(len(p.bodies)), p.resourceID); err != nil {
				return ResetOutcome{}, fmt.Errorf("reset seq for resource %d (%q): %w", p.resourceID, p.routeFamily, err)
			}
		}
	}

	// No bumpRevisionTx call, anywhere in this function (D3 R11):
	// entities are never cached in the runtime, routeCache is keyed on
	// configuration alone, and a bump would invalidate every runtime for a
	// change no runtime holds.
	return ResetOutcome{Changed: deleted > 0 || inserted > 0, Deleted: deleted, Skipped: skipped}, nil
}

// countEntitiesTx counts every entity row of workspaceID's resources, live,
// inside tx — [Repo.resetTx]'s step 4, and clause 14's own subject: moving
// this count to the pre-transaction read is the exact mutation that clause
// is written to catch.
func countEntitiesTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (int64, error) {
	var n int64
	err := tx.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM entities WHERE resource_id IN (SELECT id FROM resources WHERE workspace_id = ?)", workspaceID,
	).Scan(&n)
	if err != nil {
		return 0, fmt.Errorf("count entities for workspace %d: %w", workspaceID, err)
	}
	return n, nil
}

// deleteEntitiesWorkspaceTx is [ResetModeClear]'s DELETE (D8): every entity
// row of every resource of workspaceID, in one statement, scoped by a live
// subquery rather than a pre-read id list — so a family confirmed between
// the roster read and this transaction loses its rows too, exactly as D3
// promises the mode does.
func deleteEntitiesWorkspaceTx(ctx context.Context, tx *sql.Tx, workspaceID int64) (int64, error) {
	res, err := tx.ExecContext(ctx,
		"DELETE FROM entities WHERE resource_id IN (SELECT id FROM resources WHERE workspace_id = ?)", workspaceID)
	if err != nil {
		return 0, fmt.Errorf("clear entities for workspace %d: %w", workspaceID, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return 0, fmt.Errorf("rows affected clearing workspace %d: %w", workspaceID, err)
	}
	return n, nil
}

// deleteEntitiesForResourcesTx is [ResetModeReseed]'s DELETE: one statement
// per resource id, scoped `WHERE resource_id = ?`, rather than a single
// query built around an IN-list assembled from a slice length —
// [internal/checkpoints]' own liveResourceFamiliesTx names the identical
// choice and the identical reason (gosec G202: this tree fixes those
// rather than annotating them). resourceIDs is small (bounded by however
// many families a workspace has confirmed), so N round trips inside an
// already-open transaction on the single writer connection costs nothing
// this verb's own numbers ever reach.
func deleteEntitiesForResourcesTx(ctx context.Context, tx *sql.Tx, resourceIDs []int64) (int64, error) {
	var total int64
	for _, id := range resourceIDs {
		res, err := tx.ExecContext(ctx, "DELETE FROM entities WHERE resource_id = ?", id)
		if err != nil {
			return 0, fmt.Errorf("delete entities for resource %d: %w", id, err)
		}
		n, err := res.RowsAffected()
		if err != nil {
			return 0, fmt.Errorf("rows affected deleting resource %d: %w", id, err)
		}
		total += n
	}
	return total, nil
}
