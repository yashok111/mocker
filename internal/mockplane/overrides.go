// overrides.go is the other half of runtime.go's op_overrides wiring: that
// file loads every row for a workspace and compiles every bound recipe map
// AND applies every bound schemaPatch (P2e) ONCE per (workspace, revision)
// build; this file is what the serving path (respond.go, a later stage of
// this same phase) uses to find the right row, the right compiled
// *recipes.Set and the right already-patched schema root for a matched
// request in O(1), with a stale or malformed row going quietly inert
// instead of taking anything else down with it.
package mockplane

import (
	"log/slog"
	"strconv"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/jsonpatch"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/router"
)

// lookupOverride returns the op_overrides row bound to route, if any.
//
// The key is overrides.OpKey(route.Method, route.Path) — route.Path, and
// deliberately NEVER route.CanonicalPath. router.Route.Path's own doc
// comment says it is "relative — WITHOUT the base path — exactly as stored
// in operations.path", which is byte-identical to overrides.Row.Path (the
// op_overrides.path column's own comment says the same), precisely so a
// stored edit survives both a re-import and a basePath change. CanonicalPath
// instead collapses every path-parameter NAME to the same "{}" placeholder,
// so keying on it would let a stale row bound to a renamed parameter (say
// "/a/{name}") silently reattach to whatever operation happens to share that
// same canonical shape today (say "/a/{id}") — the exact wrong outcome the
// "an override row must survive a spec that no longer has that operation"
// requirement exists to prevent. router.go's own comment about
// CanonicalPath being "the override key P2 needs" is about custom-endpoint
// conflicts at MATCH time, a different lookup with a different key on
// purpose; it does not apply here.
//
// ok is false when rt.overrides is nil — no [OverrideSource] was ever wired
// via [Plane.SetOverrides], or the workspace simply has no rows; buildRuntime
// treats both identically — or route simply has no row of its own. The
// second case also covers a row left behind by a spec re-import that
// dropped the operation: nothing in the route table can ever produce that
// row's key again, so it is never consulted, never an error, and never
// drops anything else out of the map.
func (rt *runtime) lookupOverride(route *router.Route) (*overrides.Row, bool) {
	if rt.overrides == nil {
		return nil, false
	}
	row, ok := rt.overrides[overrides.OpKey(route.Method, route.Path)]
	return row, ok
}

// recipeSetKey addresses one compiled *recipes.Set: the operation
// (lookupOverride's own key) plus the STATUS ACTUALLY BEING SERVED. That is
// not necessarily chooseVariant's original pick — it is that status AFTER
// the row's ActiveStatus override, if any, has already been applied.
// Variant.Recipes lives per status inside op_overrides.responses, and
// respond.go only learns which status it landed on once ActiveStatus has
// run, so a map keyed by operation alone would be unusable at exactly the
// moment it is needed.
type recipeSetKey struct {
	op     string // overrides.OpKey(route.Method, route.Path)
	status string // decimal, e.g. "200" — a Row.Responses key, verbatim
}

// lookupRecipes returns the compiled *recipes.Set bound to route answering
// with status — a decimal string in the exact form a Row.Responses key
// uses, e.g. "200" — the status the caller has ALREADY resolved through any
// ActiveStatus override (see recipeSetKey's doc comment; this method does
// not apply that override itself, it only indexes by the result).
//
// ok is false whenever there is nothing usable to serve with: no override
// row for route, no Responses entry for status, an empty Recipes map (which
// [recipes.Compile] itself represents as a nil *Set, never stored here — see
// buildRecipeSets), or a Recipes map that failed to compile. That last case
// was already logged by buildRecipeSets at build time; the request that
// happens to land on it must still be served, with no recipes bound at all,
// rather than fail — a stored row must never be able to take a workspace's
// entire mock down.
func (rt *runtime) lookupRecipes(route *router.Route, status string) (*recipes.Set, bool) {
	if rt.recipeSets == nil {
		return nil, false
	}
	set, ok := rt.recipeSets[recipeSetKey{op: overrides.OpKey(route.Method, route.Path), status: status}]
	return set, ok
}

// buildRecipeSets compiles every Variant.Recipes map bound in rows, once, at
// runtime-build time — DESIGN §18 leaves no room for per-request work on the
// mock plane, and [recipes.Compile] both validates and precompiles a
// pattern set in one pass. rows is the POST-COMPOSITION map buildRuntime
// passes in — [OverrideSource.ForWorkspace]'s own result verbatim with no
// scenario active, [composeScenarioLayer]'s result with one (runtime.go's
// own A4 note on runtime.recipeSets: compiling from the PRE-composition map
// "is a defect that compiles, passes every test written before scenarios
// existed, and shows up only as a body that is subtly wrong"). Either way
// op is already overrides.OpKey(row.Method, row.Path) for every entry — the
// SAME key lookupOverride computes from a matched route, by construction.
//
// A single variant whose Recipes map fails Compile — a malformed value that
// slipped past Put/PutMany's own normalizeAndValidate some other way, a
// hand-run UPDATE, a future version writing a kind this build does not know
// — is logged and simply absent from the result: lookupRecipes then reports
// ok=false for that one (operation, status), and ONLY that one variant
// serves with no recipes. It never fails the build, never drops the row's
// other variants, and never touches any other operation in the workspace.
func buildRecipeSets(log *slog.Logger, workspaceSlug string, rows map[string]*overrides.Row) map[recipeSetKey]*recipes.Set {
	sets := make(map[recipeSetKey]*recipes.Set)
	for op, row := range rows {
		for status, variant := range row.Responses {
			set, err := recipes.Compile(variant.Recipes)
			if err != nil {
				log.Error("compile recipes: invalid stored recipe, serving without it",
					"workspace", workspaceSlug, "operation", op, "status", status, "err", err)
				continue
			}
			if set == nil {
				// Nothing bound at this status — recipes.Compile's own
				// "empty map -> nil Set" contract. Skipping the key here
				// (rather than storing the nil) keeps this map exactly as
				// large as what an operator actually wrote; lookupRecipes'
				// ok=false means the identical thing a stored nil would.
				continue
			}
			sets[recipeSetKey{op: op, status: status}] = set
		}
	}
	return sets
}

// patchedSchemaKey addresses one already-patched schema root: the operation
// (router.Route.OpRowID, the same key rt.variants is keyed by) plus the
// SELECTOR the spec variant it was built from carries — never the status.
// classifySelector maps both "default" and "2XX" to HTTPStatus 200
// (internal/specs/index.go), and the project's own P1b golden states the
// rule in capitals: "THE MAP KEY IS THE SELECTOR, NOT THE STATUS"
// (golden_p1b_test.go). An op_overrides.responses entry is keyed by status,
// so one stored patch can build MORE than one root here — one per spec
// variant that shares that HTTPStatus, each under its own selector — and a
// status-keyed map could not tell those roots apart (D3(2)/Risk 7).
type patchedSchemaKey struct {
	opRowID  int64
	selector string // "200" | "2XX" | "default", exactly as gen.ResponseVariant.Selector
}

// lookupPatchedSchema returns the already-patched schema root built once at
// runtime-build time for (opRowID, selector) — the variant respond.go has
// ALREADY chosen (chooseVariant on the ordinary path, variantForStatus on
// the forced-status one), never re-derived from the status alone. Looking
// it up by the variant already picked is what keeps the root and the
// schema pointer it came from from ever disagreeing (D3(3)).
func (rt *runtime) lookupPatchedSchema(opRowID int64, selector string) (map[string]any, bool) {
	if rt.patchedSchemas == nil {
		return nil, false
	}
	root, ok := rt.patchedSchemas[patchedSchemaKey{opRowID: opRowID, selector: selector}]
	return root, ok
}

// buildPatchedSchemas applies every composed override row's SchemaPatch,
// once, at runtime-build time (D1/D2(6)/A13 — never per request: no parse,
// no apply and no copy belongs anywhere near the request path this plane
// serves unauthenticated). resolver must be the SAME *openapi.Resolver
// gen.New below it is built from, and specRoutes/variants must both be the
// spec halves of THIS SAME build — the join from an override row's
// overrides.OpKey to a router.Route's OpRowID runs over specRoutes, exactly
// as [runtime.lookupOverride] and rt.variants are keyed, so a root this
// function stores can only ever be found under the OpRowID/Selector a real
// matched request will actually look up.
//
// overrideRows nil (no OverrideSource wired, or a composed layer that
// produced none) and resolver nil (called outside the hasSpec block, which
// buildRuntime never does — D1's own "must not reach a nil resolver") both
// return nil defensively; buildRuntime only ever calls this from inside
// that block, where resolver is never nil.
//
// A row whose operation the spec no longer declares is a LOOKUP MISS on
// overrideRows[op] against specRoutes, never an error — the identical
// orphaned-edit tolerance [runtime.lookupOverride] already has (D2(7)): an
// errored join here would fail the whole build and 500 every route in the
// workspace, the exact outcome this design exists to prevent.
//
// Per response variant (row.Responses[status]), a root is built ONLY when
// ALL of these hold (D1/D2(6)), and skipped SILENTLY — not logged, because
// none of it is an error — otherwise:
//   - the row is OverrideOn (checked by the caller loop, once per row);
//   - the variant's Mode is not "pinned" (pinnedBody never calls the
//     generator, so a patch there has nothing to apply to — D3(9));
//   - its SchemaPatch, run through jsonpatch.Parse, is non-empty — nil,
//     "[]" and "null" all parse to an empty Patch (jsonpatch.Patch.Empty's
//     own doc comment names all three spellings the wire has produced);
//   - the spec variant it names (same OpRowID, HTTPStatus == the response
//     key parsed as decimal) has a non-empty SchemaPtr that resolves to a
//     map[string]any — a boolable JSON Schema (2020-12's bare true/false)
//     resolves to a bool, not a map, and is skipped exactly like an unset
//     pointer.
//
// A malformed SchemaPatch (fails jsonpatch.Parse) or one that fails to
// [jsonpatch.Apply] against the resolved root (an add whose parent is
// absent, a remove/replace of an absent key, an out-of-range index, ...) IS
// logged once here — never per request — and that ONE variant serves
// unpatched (D2(6)/D5), exactly the behaviour a failed recipe compile
// already has in buildRecipeSets above.
//
// The stored root has its "examples" key deleted, root-only, AFTER Apply —
// D3(5): normalizeDialect already deletes every singular "example" at
// import time, so only the plural survives into the document this build
// loads, and leafValue reads only the plural; deleting it here (rather than
// gating the walk in internal/gen) is what keeps a patch on a LIST
// operation from silently suppressing every item's own example, since the
// deletion never touches anything but the top-level root jsonpatch.Apply
// already deep-copied.
func buildPatchedSchemas(
	log *slog.Logger,
	workspaceSlug string,
	resolver *openapi.Resolver,
	overrideRows map[string]*overrides.Row,
	variants map[int64][]gen.ResponseVariant,
	specRoutes []router.Route,
) map[patchedSchemaKey]map[string]any {
	if overrideRows == nil || resolver == nil {
		return nil
	}

	roots := make(map[patchedSchemaKey]map[string]any)
	for _, route := range specRoutes {
		op := overrides.OpKey(route.Method, route.Path)
		row, ok := overrideRows[op]
		if !ok || !row.OverrideOn {
			continue
		}

		for status, ov := range row.Responses {
			if ov.Mode == "pinned" || len(ov.SchemaPatch) == 0 {
				continue
			}
			patch, err := jsonpatch.Parse(ov.SchemaPatch)
			if err != nil {
				log.Error("schema patch: invalid stored patch, serving unpatched",
					"workspace", workspaceSlug, "operation", op, "status", status, "err", err)
				continue
			}
			if patch.Empty() {
				// nil/"[]"/"null" — DESIGN never picked one spelling for
				// "no patch" and the wire has produced all three; none of
				// them is an error (jsonpatch.Patch.Empty's own doc).
				continue
			}
			httpStatus, err := strconv.Atoi(status)
			if err != nil {
				// Row.Responses keys are already validated 3-digit decimal
				// strings by overrides.ValidateVariant at write time
				// (A12 forbids adding a NEW check there, not relying on
				// the one already in place) — unreached in practice, and
				// not an Apply failure, so no log: there is nothing here
				// that jsonpatch itself could have rejected.
				continue
			}

			for _, v := range variants[route.OpRowID] {
				if v.HTTPStatus != httpStatus || v.SchemaPtr == "" {
					continue
				}
				resolved, rerr := resolver.Resolve(v.SchemaPtr)
				if rerr != nil {
					// Not logged: D1 treats "has a non-empty SchemaPtr
					// that resolves to a map" as one precondition, and a
					// resolve failure here is the same "not usable" case
					// as an empty pointer — not something this patch did.
					continue
				}
				root, isMap := resolved.(map[string]any)
				if !isMap {
					// A boolean JSON Schema (asSchemaObject's own case in
					// internal/gen) — nothing to patch a property into.
					continue
				}

				patched, aerr := jsonpatch.Apply(patch, root)
				if aerr != nil {
					log.Error("schema patch: apply failed, serving unpatched",
						"workspace", workspaceSlug, "operation", op, "status", status,
						"selector", v.Selector, "err", aerr)
					continue
				}
				delete(patched, "examples")
				roots[patchedSchemaKey{opRowID: route.OpRowID, selector: v.Selector}] = patched
			}
		}
	}

	if len(roots) == 0 {
		return nil
	}
	return roots
}
