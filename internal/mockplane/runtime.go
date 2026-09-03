// runtime.go builds the per-workspace runtime P1b's response generator needs
// to answer a matched request: the compiled route table (already built in
// P1a), a [gen.Generator] over the workspace's spec document, and the
// response-variant rows the generator needs per matched operation. Building
// one is the expensive step routes.go's cache exists to amortize — see
// runtimeFor's own doc comment for the full derivation and why it is safe to
// cache and share across concurrent readers.
package mockplane

import (
	"context"
	"fmt"
	"time"

	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/design"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// runtime is everything the response path (respond.go, a later phase) needs
// for one (workspace, revision): the compiled route table, a generator over
// the workspace's resolved spec document, the response variants a matched
// route's operation can answer with (keyed by router.Route.OpRowID, the same
// key a Match's Route carries), and the settings snapshot the runtime was
// built from.
//
// "The settings snapshot the runtime was built from" stays literally true
// under an active scenario: [composeScenarioLayer] (scenario.go) returns
// the EFFECTIVE settings — the scenario's seed/listSize/nullRate/identity/
// auth/delayMs/envelope, but the WORKSPACE's basePath, cors and
// notFoundBody, because those three are read outside this runtime and a
// value here that nothing reads would be a lie in the one place a reader
// trusts (A3a).
//
// A runtime is immutable after [Plane.runtimeFor] returns it: every field is
// written once, here, and never again, so — exactly like [router.Table]
// itself — any number of concurrent requests can read a shared *runtime with
// no lock. This is the property go test -race is run against: many
// goroutines serving requests against the SAME cached runtime while another
// goroutine may be building the NEXT one (a different, not-yet-published
// value) for a revision bump.
type runtime struct {
	table    *router.Table
	gen      *gen.Generator
	variants map[int64][]gen.ResponseVariant
	settings domain.Settings

	// overrides is the COMPOSED op_overrides map for this (workspace,
	// revision), keyed EXACTLY as [OverrideSource.ForWorkspace]'s own doc
	// comment promises its map already is — overrides.OpKey(method, path)
	// — so buildRuntime stores what it gets back verbatim instead of
	// rekeying it under a second, driftable derivation. With no scenario
	// active it IS that map; with one active it is
	// [composeScenarioLayer]'s result, where a key the snapshot carries
	// takes the scenario's row and every other key keeps the workspace's
	// (A1/A2). nil when the Plane has no OverrideSource
	// ([Plane.SetOverrides] was never called) AND no scenario layer was
	// composed. See [runtime.lookupOverride] (overrides.go) for how a
	// matched route resolves this at request time, and why the key is
	// route.Path and never route.CanonicalPath.
	overrides map[string]*overrides.Row

	// recipeSets is [buildRecipeSets]' (overrides.go) result: one compiled
	// *recipes.Set per (operation, status actually served), built once here
	// so DESIGN §18's "no per-request work" holds for recipe compilation
	// exactly as it already does for parsing and route-table construction.
	// nil exactly when overrides is nil — see [runtime.lookupRecipes].
	//
	// A4: it is compiled from the map above AFTER composition, never
	// before. Compiling the workspace's recipes while serving the
	// scenario's rows yields a runtime whose recipe sets belong to one
	// layer and whose rows belong to the other — a defect that compiles,
	// passes every test written before scenarios existed, and shows up only
	// as a body that is subtly wrong.
	recipeSets map[recipeSetKey]*recipes.Set

	// patchedSchemas is [buildPatchedSchemas]' (overrides.go) result: one
	// already-patched schema root per (OpRowID, Selector) — NEVER per
	// status, see patchedSchemaKey's own doc comment for why a status-keyed
	// map cannot express this — built once here so P2e's schemaPatch gets
	// the exact same "no per-request work" guarantee recipeSets already has
	// (A13). nil when overrides is nil, when the workspace has no spec
	// attached (a patch has nothing to apply to without one), or when no
	// composed row's variant qualified to have one built at all — see
	// [runtime.lookupPatchedSchema].
	patchedSchemas map[patchedSchemaKey]map[string]any

	// customInline is [buildCustomInline]'s (custom_schema.go) result: one
	// decoded, ref-sanitized schema plus compiled recipes per (custom row,
	// status) whose variant declares a schema and is not pinned (P7a,
	// DESIGN §34.3) — the custom endpoint's own recipeSets/patchedSchemas,
	// built once here for the same "no per-request work" reason. nil when
	// no row qualifies.
	customInline map[int64]map[string]inlineSource

	// custom is every custom_endpoints row this (workspace, revision) serves
	// (OverrideOn=true ones only — see buildRuntime), keyed by its OWN row
	// id, the same id router.Route.CustomRowID carries for a matched custom
	// route. Unlike overrides (keyed by op path, since a spec operation's
	// route.Path is stable across a rebuild), a custom endpoint's Route is
	// built FROM this same row every time, so there is no separate
	// "reattach a stale key" concern CustomRowID's own comment (router.go)
	// needs to guard against — an id lookup is simplest and exact. nil when
	// the Plane has no [CustomSource] ([Plane.SetCustomEndpoints] never
	// called). See [runtime.lookupCustom] (custom.go).
	custom map[int64]*customep.Row

	// routes is the combined spec-plus-custom slice buildRuntime already
	// holds the instant before it calls [router.Build] (step 9, below) —
	// retained rather than recomputed because [router.Table] exports only
	// Match, Len and HasCanonical: its own storage is private, so nothing
	// outside this package can walk it or look a route up by anything but a
	// live HTTP match. P2f (D4) needs exactly that second kind of lookup —
	// find the [router.Route] a preview's own opKey names, with no request
	// to match against at all — and this is the one place that slice still
	// exists to keep.
	routes []router.Route

	// resources is every CONFIRMED resource of this (workspace, revision),
	// keyed by RouteFamily — [ResourceSource.ForWorkspace]'s own result,
	// stored verbatim (D6 R17). Entities and seq are NEVER here: they are
	// read per request through [Plane.entities] instead — see
	// [resourceBranch] (resource.go). nil when the Plane has no
	// [ResourceSource] ([Plane.SetResources] never called), the identical
	// "no source wired, no change" contract [custom] already established.
	//
	// P3c: [Plane.resolveRef] (ref.go) is a SECOND reader of this field,
	// via the roster the "ref" recipe's closure closes over — a direct map
	// hit on the verbatim RouteFamily key, the same lookup resourceBranch
	// performs, which is what keeps a ref from ever resolving to another
	// workspace's rows (D6).
	resources map[string]*resources.Resource
}

// runtimeFor returns ws's runtime, building and caching it on a miss.
// serveRoute (routes.go) is still the only production caller, and — as of
// P1c2 — it calls this whenever ws has a spec attached OR the Plane has a
// [CustomSource] wired, never only the former: a workspace with no spec but
// a [CustomSource] and custom rows of its own still needs a table built
// (see buildRuntime's own doc comment for the spec-less path). A workspace
// with neither never reaches here at all — routes.go's own short-circuit
// keeps answering the plain 404 it always has, at zero extra cost.
//
// The numbered steps below are the ones each source contributes, and the
// numbers are STABLE — several comments in this package and its tests cite
// them by number. Their ORDER OF EXECUTION is not the numbering, and has
// not been since scenarios landed: step 7 (the override rows) and step 7b
// (the scenario composition) run FIRST, because gen.Options in step 4 must
// be filled from the EFFECTIVE settings, which under an active scenario are
// the composed ones rather than ws.Settings. Running the two halves the
// other way round would mean building the generator twice or building it
// from the wrong seed; neither half reads anything the other produces, so
// moving them costs nothing.
//
//  1. IF ws has a spec attached (p.specs != nil && ws.SpecID != nil):
//     [specs.Repo.Normalized] — the document bytes stored at import time,
//     already dialect-normalized, never the raw upload.
//  2. [openapi.Load] over those bytes. This is idempotent on
//     already-normalized input (specs' own TestRepo_Normalized_isIdempotentThroughLoad
//     pins exactly that), so re-running it here is a pure re-parse, not a
//     second, potentially-diverging pass of dialect normalization. Load's
//     own Report is discarded on purpose — the import that produced these
//     bytes already recorded it; recomputing and storing it again here would
//     just be a second, driftable copy.
//  3. [openapi.NewResolver] over the freshly loaded document.
//  4. [gen.New], with [gen.Options] filled from the EFFECTIVE settings
//     (Seed, ListSize, NullRate, Identity, Auth) and cfg.MaxResponse
//     (MaxBytes). "Effective" is ws.Settings with no scenario active and
//     step 7b's composed value with one — MaxDepth is
//     deliberately left at its zero value: nothing in config.Config or
//     domain.Settings supplies it, and gen.Options' own contract is that
//     zero there means "use the package default", never a budget of zero.
//     Identity and Auth are NEW in P1c: without them, every identity and jwt
//     recipe would silently produce nothing no matter what an operator
//     bound, because IdentityField/MintJWT would see a zero domain.Identity/
//     domain.AuthSettings instead of the workspace's own.
//  5. [specs.Repo.Variants] — every response variant for the spec, in one
//     query.
//  6. [specs.Repo.Routes] — the spec's own operations, in document order.
//     WITHOUT a spec attached, steps 1-6 are all skipped: runtime.gen stays
//     nil and runtime.variants stays nil. Nothing downstream dereferences
//     either for a route this build never puts in the table — a spec-less
//     table can only ever contain custom routes (step 8), and
//     [Plane.serveCustom] (custom.go) never touches rt.gen or rt.variants —
//     so "unreachable by construction" holds without a runtime nil check on
//     every call site that already assumes a spec.
//  7. [OverrideSource.ForWorkspace] — every op_overrides row for this
//     workspace, in one query — and [buildRecipeSets] (overrides.go), which
//     compiles each variant's recipes map exactly once. Both are skipped,
//     leaving runtime.overrides and runtime.recipeSets nil, when p.overrides
//     is nil AND no scenario layer was composed: HARD RULE 6 (no recipes
//     must mean no change) extends here to "no OverrideSource wired at all"
//     producing output byte-identical to a build from before P1c existed.
//     buildRecipeSets runs over the map step 7b produced, not the one this
//     step loaded — see runtime.recipeSets' own field comment (A4).
//     7b. NEW in P2b: IF ws.ScenarioID names an active scenario and a
//     [ScenarioSource] is wired, [Plane.scenarioSnapshot] (scenario.go)
//     loads and decodes its snapshot and [composeScenarioLayer] overlays it
//     on step 7's map and on ws.Settings. That function's doc comment is
//     where the whole layer is specified — which key wins, which settings
//     compose and which three come back from the workspace. A snapshot that
//     fails to load is logged and skipped: the workspace layer keeps
//     serving rather than the mock going down (scenarioSnapshot's own doc
//     comment).
//  8. NEW in P1c2: [CustomSource.ForWorkspace] — every custom_endpoints row
//     for this workspace, in one query. A row with OverrideOn=false is left
//     out of the merge entirely (customep.Row's own doc comment: it must be
//     as if the row did not exist), everything else becomes one more
//     router.Route with Custom=true and CustomRowID set. Skipped, leaving
//     runtime.custom nil, when p.custom is nil — the identical "no source
//     wired, no change" contract every other P1c/P1c2 source already
//     follows.
//  9. [router.Build] over the spec routes (step 6, nil when there was no
//     spec) PLUS the custom routes (step 8) in ONE slice — DESIGN §8's
//     "одна отсортированная таблица", not two tables consulted in some
//     fixed order: the comparator itself (router.compareRoutes) is what
//     decides a tie between a spec operation and a custom endpoint at equal
//     specificity, and it needs both kinds in the same sort to do that.
//     ws.Settings.BasePath is read here, fresh, every time the runtime is
//     (re)built, so a settings edit that changes BasePath without also
//     touching anything else still lands — it goes through
//     workspaces.Repo.Update, which bumps Revision on every edit, so it is
//     never a plain cache hit that would miss the change.
//
// The whole build runs OUTSIDE any lock: routeCache.getOrBuild's singleflight
// is what keeps concurrent cold requests for the same (workspace, revision)
// from each paying for it, not a mutex held across the database round trips
// and the 347 KB parse this can involve (DESIGN §18: the hot path has no
// room for per-request work — building is the one-time cost the cache exists
// to keep off of it).
func (p *Plane) runtimeFor(ctx context.Context, ws *workspaces.Workspace) (*runtime, error) {
	// runtimeBuildTimeout bounds one shared build; the 347 KB reference
	// spec builds in well under a second, so this is a backstop against a
	// stuck database, not a budget the build is expected to approach.
	const runtimeBuildTimeout = 30 * time.Second
	key := routeCacheKey{workspaceID: ws.ID, revision: ws.Revision}
	return p.routes.getOrBuild(key, func() (*runtime, error) {
		// The build runs under the LEADER's request context — whichever
		// concurrent cold request lost the singleflight race — and every
		// joiner receives the leader's result. Under the request context
		// as-is, one client giving up mid-build (a load generator with a
		// short timeout, a browser tab closed) cancelled the database
		// reads for everyone waiting, every joiner answered 500, nothing
		// was cached, and the next wave elected a new leader to repeat
		// it. The build is shared work, so it gets a context detached from
		// any one request's cancellation and bounded on its own.
		bctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), runtimeBuildTimeout)
		defer cancel()
		// nil draft: the cached, served runtime never carries an unsaved
		// splice — only [Plane.Preview] (preview.go) ever passes a non-nil
		// one, straight to buildRuntime, bypassing this cache entirely (D3).
		return p.buildRuntime(bctx, ws, nil)
	})
}

// buildRuntime does the actual work runtimeFor's doc comment describes,
// factored out so runtimeFor itself stays a thin cache-key/closure wrapper.
//
// draft is P2f's own addition (D1/D3): an unsaved op_overrides row, keyed
// exactly as [OverrideSource.ForWorkspace]'s map already is, that
// [Plane.Preview] splices in for the one operation it is asked to preview.
// runtimeFor's own call site always passes nil, and a nil draft leaves this
// function's output byte-identical to what it produced before this
// parameter existed — mergeDraftRows returns its base argument unchanged
// when there is nothing to merge, so nil in means the same *map value* out,
// not merely an equal one.
func (p *Plane) buildRuntime(ctx context.Context, ws *workspaces.Workspace, draft map[string]*overrides.Row) (*runtime, error) {
	// Steps 7, 7b and the recipe compilation run before the spec half:
	// gen.New below reads the EFFECTIVE settings, and an active scenario
	// replaces seven of their fields (see runtimeFor's own note on the
	// order of execution, and [composeScenarioLayer]).
	//
	// The overrides half is skipped entirely, leaving the map nil, when the
	// Plane was never given an OverrideSource — see [Plane.SetOverrides]'s
	// doc comment for why that is the correct, permanent state for every
	// P0/P1a/P1b test and call site this phase does not touch, not just a
	// startup transient.
	var overrideRows map[string]*overrides.Row
	if p.overrides != nil {
		loaded, oerr := p.overrides.ForWorkspace(ctx, ws.ID)
		if oerr != nil {
			return nil, fmt.Errorf("load overrides for workspace %d: %w", ws.ID, oerr)
		}
		overrideRows = loaded
	}

	// P2f (D1/D3): the draft splice runs IMMEDIATELY BEFORE the scenario
	// composition below, on purpose — an active scenario must still win by
	// key over a previewed draft exactly as it would over the same row once
	// saved (composeScenarioLayer's own key-wins-by-snapshot rule does not
	// change for this one extra source), so previewing a draft can never
	// make it observably outrank a scenario a real save would still shadow.
	overrideRows = mergeDraftRows(overrideRows, draft)

	settings := ws.Settings
	if snap, ok := p.scenarioSnapshot(ctx, ws); ok {
		settings, overrideRows = composeScenarioLayer(settings, overrideRows, snap)
	}

	// A4: compiled from the COMPOSED map. The condition is the map's own
	// nilness rather than `p.overrides != nil`, because a composed layer
	// produces rows even for a Plane with no OverrideSource wired — a
	// configuration cmd/mocker never builds, but one whose only defensible
	// behaviour is to serve the scenario's rows rather than compile
	// nothing for them.
	var recipeSets map[recipeSetKey]*recipes.Set
	if overrideRows != nil {
		recipeSets = buildRecipeSets(p.log, ws.Slug, overrideRows)
	}

	hasSpec := p.specs != nil && ws.SpecID != nil

	var (
		generator      *gen.Generator
		resolver       *openapi.Resolver
		variants       map[int64][]gen.ResponseVariant
		specRoutes     []router.Route
		patchedSchemas map[patchedSchemaKey]map[string]any
	)
	if !hasSpec {
		// P7a (decisions.md mocker-p7-api-design D5): the generator exists
		// WITHOUT a spec, over the same skeleton document the export
		// composes from (design.Skeleton — one definition, never two), so
		// a custom endpoint's inline schema generates on a workspace bound
		// to nothing — "a design from nothing" (DESIGN §34.4). variants,
		// specRoutes and patchedSchemas keep their spec gate: there is
		// nothing of theirs to build.
		doc, _, err := openapi.Load(design.Skeleton(ws.Name))
		if err != nil {
			return nil, fmt.Errorf("load the skeleton document: %w", err)
		}
		resolver = openapi.NewResolver(doc, openapi.DefaultRefBudget)
		generator = gen.New(resolver, gen.Options{
			Seed:     settings.Seed,
			ListSize: settings.ListSize,
			NullRate: settings.NullRate,
			MaxBytes: p.cfg.MaxResponse,
			Identity: settings.Identity,
			Auth:     settings.Auth,
		})
	}
	if hasSpec {
		specID := *ws.SpecID

		normalized, err := p.specs.Normalized(ctx, specID)
		if err != nil {
			return nil, fmt.Errorf("load normalized document for spec %d: %w", specID, err)
		}

		doc, _, err := openapi.Load(normalized)
		if err != nil {
			return nil, fmt.Errorf("re-load normalized document for spec %d: %w", specID, err)
		}
		resolver = openapi.NewResolver(doc, openapi.DefaultRefBudget)

		generator = gen.New(resolver, gen.Options{
			Seed:     settings.Seed,
			ListSize: settings.ListSize,
			NullRate: settings.NullRate,
			MaxBytes: p.cfg.MaxResponse,
			Identity: settings.Identity,
			Auth:     settings.Auth,
		})

		variants, err = p.specs.Variants(ctx, specID)
		if err != nil {
			return nil, fmt.Errorf("load response variants for spec %d: %w", specID, err)
		}

		specRoutes, err = p.specs.Routes(ctx, specID)
		if err != nil {
			return nil, fmt.Errorf("load routes for spec %d: %w", specID, err)
		}

		// D1/D2(6): the patch is parsed AND APPLIED here — the TAIL of this
		// block, after specRoutes is loaded and before the block closes —
		// because this is the only point where p.log, the COMPOSED
		// overrideRows, resolver, variants and specRoutes are all in scope
		// at once. resolver is declared with := above, inside this same
		// block, and does not outlive it: "after the spec block" is a place
		// where it does not exist. overrideRows is already the
		// post-composition map (an active scenario's rows, if any, were
		// already overlaid above), never the pre-composition one — the
		// identical A4 trap buildRecipeSets was written to avoid.
		patchedSchemas = buildPatchedSchemas(p.log, ws.Slug, resolver, overrideRows, variants, specRoutes)
	}

	// Custom endpoints (step 8): skipped entirely, leaving both customRoutes
	// and runtime.custom nil/empty, when the Plane has no CustomSource — the
	// same "no source wired, no change" contract as overrides above. A row
	// with OverrideOn=false is dropped right here rather than carried into
	// the table and filtered at match time: customep.Row's own doc comment
	// promises it is left out of the route table ENTIRELY, so a request
	// against its path either 404s or falls through to the spec operation it
	// would otherwise have shadowed (DESIGN §8 rule 3), exactly as if the
	// row were never created.
	var (
		customRoutes []router.Route
		customRows   map[int64]*customep.Row
	)
	if p.custom != nil {
		rows, cerr := p.custom.ForWorkspace(ctx, ws.ID)
		if cerr != nil {
			return nil, fmt.Errorf("load custom endpoints for workspace %d: %w", ws.ID, cerr)
		}
		customRoutes = make([]router.Route, 0, len(rows))
		customRows = make(map[int64]*customep.Row, len(rows))
		for _, row := range rows {
			if !row.OverrideOn {
				continue
			}
			customRoutes = append(customRoutes, router.Route{
				// OpRowID stays 0 (customep has no operations row); Custom
				// and CustomRowID are what let router's comparator and
				// respond.go's dispatch (routes.go) tell this apart from a
				// spec route, and what let traffic's matched_id (traffic.go)
				// record which custom row actually answered.
				Method:        row.Method,
				Path:          row.Path,
				CanonicalPath: row.CanonicalPath,
				Custom:        true,
				SourceOrder:   row.SourceOrder,
				CustomRowID:   row.ID,
			})
			customRows[row.ID] = row
		}
	}

	// Resources (P3a, D6 R17): every CONFIRMED resource of this workspace,
	// in one query, keyed by RouteFamily — the same "no source wired, no
	// change" contract custom endpoints (above) already follow. Unlike
	// custom endpoints this never touches allRoutes/table below: a resource
	// adds no route of its own — it takes over verbs on routes the spec
	// ALREADY declares — so this is purely a lookup table for
	// [resourceBranch] (resource.go), built from the workspace, never from
	// draft or scenario state, since D9 keeps resources out of every
	// snapshot this runtime otherwise composes.
	var resourcesByFamily map[string]*resources.Resource
	if p.resources != nil {
		rows, rerr := p.resources.ForWorkspace(ctx, ws.ID)
		if rerr != nil {
			return nil, fmt.Errorf("load resources for workspace %d: %w", ws.ID, rerr)
		}
		resourcesByFamily = make(map[string]*resources.Resource, len(rows))
		for _, res := range rows {
			resourcesByFamily[res.RouteFamily] = res
		}
	}

	// Step 9: ONE sorted table, spec routes and custom routes together —
	// see runtimeFor's own doc comment on step 9 for why that must be a
	// single router.Build call and not two tables merged after the fact.
	allRoutes := make([]router.Route, 0, len(specRoutes)+len(customRoutes))
	allRoutes = append(allRoutes, specRoutes...)
	allRoutes = append(allRoutes, customRoutes...)
	// ws.Settings.BasePath, not settings.BasePath — the two are equal by
	// construction (composeScenarioLayer restores the workspace's basePath
	// on purpose, A3a), and spelling the workspace out here keeps the route
	// table's dependence on the WORKSPACE's prefix visible at the one line
	// that decides it, instead of resting on a property of another file.
	table := router.Build(allRoutes, ws.Settings.BasePath)

	// P7a: the custom rows' inline schemas, compiled against the SAME
	// resolver the generator walks nested $refs through (the spec's, or
	// the skeleton's) — see buildCustomInline for what an unresolvable
	// $ref becomes.
	customInline := buildCustomInline(p.log, ws.Slug, customRows, resolver)

	return &runtime{
		table:          table,
		gen:            generator,
		variants:       variants,
		settings:       settings,
		overrides:      overrideRows,
		recipeSets:     recipeSets,
		patchedSchemas: patchedSchemas,
		custom:         customRows,
		customInline:   customInline,
		routes:         allRoutes,
		resources:      resourcesByFamily,
	}, nil
}

// mergeDraftRows overlays draft onto base by key — draft wins on a
// collision — without mutating either input: base may be the very map
// [OverrideSource.ForWorkspace] just handed back, which nothing here owns
// the right to write into. Returns base itself, unchanged, when draft is
// empty, so runtimeFor's own nil-draft call site gets back the identical
// *map value* buildRuntime would have produced before this function
// existed, not merely an equal one — the property runtimeFor's doc comment
// on draft promises.
func mergeDraftRows(base, draft map[string]*overrides.Row) map[string]*overrides.Row {
	if len(draft) == 0 {
		return base
	}
	merged := make(map[string]*overrides.Row, len(base)+len(draft))
	for k, v := range base {
		merged[k] = v
	}
	for k, v := range draft {
		merged[k] = v
	}
	return merged
}
