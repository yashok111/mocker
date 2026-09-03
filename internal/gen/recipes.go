// recipes.go is P1c's plug of internal/recipes into internal/gen: the
// lookup/Env plumbing values.go's recipeValue calls into, the shared
// listSize-picking helper schema.go's arrayLength and list.go's page-level
// sizing both use, and the copy/omit post-pass gen.go's Body runs over the
// finished value before marshalling.
//
// Every entry point here is a documented no-op — no lookup, no walk, no
// allocation beyond a nil check — when req.Recipes is nil, which is what
// HARD RULE 6 requires: a workspace that has never written an op_overrides
// row must see byte-for-byte what P1b produced.
package gen

import (
	"fmt"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/recipes"
)

// --- the scalar seam: recipeValue's lookup + Env construction --------------

// lookupRecipe resolves the recipe bound to dataPath, adjusted for a
// restarted list-item subtree via recipePath (schema.go), declining
// outright — ok=false, nothing else computed — for the two cases that cover
// the overwhelming majority of calls in a recipe-less workspace: no *Set at
// all, or a Set that binds nothing at this exact path. A Deferred recipe
// (copy/omit/listSize) also declines here: none of the three can be
// answered from a Recipe and an Env alone (see recipes.Kind.Deferred's own
// doc), so leafValue must fall through to its ordinary chain for them too —
// copy/omit are applied by the whole-body post-pass below, listSize by
// arrayLength/listSizeOrDefault, neither ever through Recipe.Value.
func (w *walker) lookupRecipe(dataPath string) (r recipes.Recipe, path string, ok bool) {
	set := w.req.Recipes
	if set == nil || set.Len() == 0 {
		return recipes.Recipe{}, "", false
	}
	path = w.recipePath(dataPath)
	rec, found := set.Lookup(path)
	if !found || rec.Kind.Deferred() {
		return recipes.Recipe{}, "", false
	}
	return rec, path, true
}

// recipeEnv builds the recipes.Env a recipe bound at dataPath evaluates
// against. Identity/Auth come from Options — per-workspace, stable for the
// life of a Generator (Options' own doc comment), never per-request state.
// Now is w.now, the clock ALREADY frozen once per Body/Headers call (see
// Generator.newWalker) — DESIGN §9 "Время" is explicit that jwt/now derive
// from the real clock, but "the real clock" means the one instant this
// response was built under, never a second, independent time.Now() call
// mid-walk. Seed is SeedScalar keyed on the RAW, unprefixed dataPath — the
// same seed layer leafValue's enum/generated paths use for this exact
// field, so an enum recipe on a field and the generator's own enumValue
// (had no recipe been bound) would have picked from the identical
// distribution; recipePrefix (a lookup-only concern) never reaches this.
// Type/Format are read straight off the schema node leafValue was already
// called with, so a recipe answering a value Coerce cannot honestly fit to
// the declared type is told so and can decline.
func (w *walker) recipeEnv(schema map[string]any, dataPath string) recipes.Env {
	format, _ := schema["format"].(string)
	return recipes.Env{
		Identity: w.opts.Identity,
		Auth:     w.opts.Auth,
		Now:      w.now,
		Seed:     SeedScalar(w.seedList, dataPath),
		Type:     SchemaType(schema),
		Format:   format,
		// A6: off the REQUEST (w.req), never Options — see Request.AssetBase.
		AssetBase: w.req.AssetBase,
	}
}

// --- listSize: the shared "pick a length from a recipe" primitive ---------

// pinnedListSize evaluates a listSize recipe's [lo,hi] bounds (as returned
// by recipes.Set.ListSizeAt) into one concrete length: lo directly for a
// fixed size (lo==hi, the common case — a bare integer Value), otherwise a
// SEED-derived pick within the range so the same array at the same data
// path always lands on the same length across identical requests (DESIGN
// §9's determinism contract applies to a pinned range exactly as it does to
// any other generated value — "for a range the length is picked from the
// seed so it stays reproducible"). Keyed with a "\x00listSize" suffix that
// cannot collide with any real dataPath, the same technique walkSchema's
// own rollNull uses for its coin flip.
func pinnedListSize(lo, hi int, seedList uint64, dataPath string) int {
	if hi <= lo {
		return lo
	}
	seed := SeedScalar(seedList, dataPath+"\x00listSize")
	return lo + int(seed%uint64(hi-lo+1))
}

// --- the copy/omit post-pass ------------------------------------------------
//
// copy and omit are the two Deferred kinds Recipe.Value can never answer on
// its own: copy needs the sibling that lives in the SAME containing object,
// omit needs the parent to delete from. Both act on the FINISHED body value
// — data, never schema — so they run once, here, after walkSchema/listBody
// has built the whole tree and before gen.go's Body marshals it.
//
// HONEST CONSEQUENCE, worth stating at the seam it happens rather than only
// in DESIGN §9: omit on a REQUIRED property produces a body that no longer
// satisfies its own schema. That is allowed on purpose — DESIGN §9 lists
// omit as a recipe kind precisely so an operator can reproduce a backend
// bug, or trim a field a specific frontend build cannot yet parse — but a
// workspace that binds an omit recipe to a required property should expect
// exactly that body to fail schema-conformance checking, and this is the
// paper trail for why that is the recipe doing its job, not a defect here.

// maxPostPassNodes bounds the post-pass the same way schema.go's own
// maxWalkNodes bounds the generation walk: a hard ceiling independent of
// body shape, so a pathological body paired with a pathological pattern set
// cannot spin. Sharing the constant, not just the shape, keeps the two
// ceilings from silently drifting apart under a future tuning pass to one
// but not the other.
const maxPostPassNodes = maxWalkNodes

type postPassBudget struct{ nodes int }

// visit reports whether the post-pass may still process one more node;
// false means "stop transforming, return what's left untouched" — never a
// panic, never a partial/corrupt tree, just fewer of the pattern's matches
// actually applied once a body is large enough (or a pattern set hostile
// enough) to exhaust the ceiling.
func (b *postPassBudget) visit() bool {
	b.nodes++
	return b.nodes <= maxPostPassNodes
}

// applyRecipePostPass is the whole seam's entry point, called from gen.go's
// Body on both the schema-walk value and the list-body value. It is a
// documented no-op — no walk, no allocation — for the two cases that cover
// every request in a workspace that has never bound a copy/omit recipe: no
// *Set at all, and a Set that binds nothing but copy/omit's non-deferred
// siblings (const, enum, identity, jwt, now, null — every one of which
// recipeValue already resolved during the walk itself) OR listSize —
// itself technically a Deferred kind too, but one with its own dedicated
// seam (arrayLength/listSizeOrDefault, both applied DURING generation),
// never through this post-pass. bindingsHaveDeferred checks specifically
// for copy/omit, not "any Deferred kind", precisely so a Set that binds
// only listSize recipes also takes this fast path. Its one map copy
// (recipes.Set.Bindings is the only introspection the recipes package
// exposes) is paid at most ONCE per response, never once per node, and
// only when a *Set is actually bound to this variant — the rare path, not
// the hot one.
func (w *walker) applyRecipePostPass(val any) any {
	set := w.req.Recipes
	if set == nil || set.Len() == 0 || !bindingsHaveDeferred(set) {
		return val
	}
	budget := &postPassBudget{}
	out := applyDeferred(val, "", set, budget)

	// The post-pass runs on the FINISHED body, after the schema walk's own
	// MaxBytes budget has already had its say — a copy recipe can rebuild
	// the SAME already-bounded sibling under N different names, multiplying
	// an already-MaxBytes-bounded body N-fold with nothing downstream ever
	// re-checking the result (round-1 finding #6: 200 copy bindings turned
	// an 807KB body into 161MB). Re-measuring here, once, against the exact
	// ceiling the walk itself was held to is the second gate: decline the
	// WHOLE transform and serve what the walk actually produced rather than
	// something larger. (deepCloneValue's own node cost is charged to the
	// SAME budget applyDeferred spends from — see its doc comment — which is
	// the first gate, bounding the CPU/memory spent building `out` in the
	// first place; this is the belt to that suspenders, catching the case
	// where a handful of large-but-under-budget clones still add up to more
	// bytes than the ceiling allows.)
	if b, err := jsonx.Marshal(out); err == nil && int64(len(b)) > w.opts.effMaxBytes() {
		return val
	}
	return out
}

func bindingsHaveDeferred(set *recipes.Set) bool {
	for _, r := range set.Bindings() {
		if r.Kind == recipes.KindCopy || r.Kind == recipes.KindOmit {
			return true
		}
	}
	return false
}

// applyDeferred rebuilds val into a FRESH tree (never mutated in place):
// this is what keeps a copy immune to aliasing — Go map iteration order is
// randomized per process, and mutating the source object while both the
// original field and its copy alias the SAME underlying map/slice would
// make a later omit meant for one silently reach through and delete out
// from under the other. Every path fed to set.Lookup is the value tree's
// OWN absolute path (joinPath / "%s[%d]", exactly like schema.go's own
// dataPath construction) — recipePrefix plays no part here, since this
// walk starts fresh from the already-finished body's own root, not from a
// mid-generation, possibly-restarted dataPath.
func applyDeferred(val any, path string, set *recipes.Set, budget *postPassBudget) any {
	if !budget.visit() {
		return val
	}
	switch v := val.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for name, child := range v {
			childPath := joinPath(path, name)
			if r, ok := set.Lookup(childPath); ok {
				switch r.Kind {
				case recipes.KindOmit:
					continue // drop the property entirely
				case recipes.KindCopy:
					sibling := strings.TrimPrefix(r.Field, "$.")
					if sv, present := v[sibling]; present {
						// A deep clone, not the sibling's own reference: two
						// keys aliasing the same map/slice would let a
						// recipe bound under ONE of their paths reach
						// through and mutate what the OTHER one reads —
						// exactly the hazard this function's own doc
						// explains for why the whole tree is rebuilt fresh.
						if cloned, ok := deepCloneValue(sv, budget); ok {
							out[name] = applyDeferred(cloned, childPath, set, budget)
							continue
						}
						// Budget ran out mid-clone (round-1 finding #6: N
						// copy bindings must not get N un-budgeted clones of
						// an already-generated sibling): decline exactly
						// like a missing sibling, below — leave this
						// field's own generated value in place rather than
						// splice in a partial, alias-unsafe tree.
					}
					// Missing sibling: the recipe named a field this object
					// doesn't actually have — an operator mistake, not a
					// request failure on an open, unauthenticated endpoint.
					// The generated value is left alone rather than
					// invented or dropped.
				}
			}
			out[name] = applyDeferred(child, childPath, set, budget)
		}
		return out
	case []any:
		out := make([]any, 0, len(v))
		for i, child := range v {
			childPath := fmt.Sprintf("%s[%d]", path, i)
			if r, ok := set.Lookup(childPath); ok && r.Kind == recipes.KindOmit {
				continue // drop the element, closing the gap
			}
			out = append(out, applyDeferred(child, childPath, set, budget))
		}
		return out
	default:
		return val
	}
}

// deepCloneValue copies v's own composite structure (map[string]any,
// []any) so a copy recipe's target never shares a live reference with its
// source — see applyDeferred's own doc for why that matters. Scalars
// (string/bool/float64/int64/jsonx.Number/nil) are already immutable value
// types in Go and pass through unchanged.
//
// budget is the SAME postPassBudget applyDeferred's own recursion spends
// from, charged once per node visited here exactly as applyDeferred charges
// itself — round-1 finding #6 measured what cloning WITHOUT that charge
// costs: 200 copy bindings on one large sibling turned an 807KB body into
// 161MB (200x) with nothing bounding the CPU/memory spent building it,
// because applyDeferred's own budget check only ever gated its OWN
// recursion, never the unconditional clone it called out to. ok=false means
// the budget ran out partway through — the caller must discard whatever was
// built so far and decline the copy entirely (never splice in a partial
// tree: half a clone is exactly the aliasing hazard this function exists to
// avoid, not a smaller-but-safe one).
func deepCloneValue(v any, budget *postPassBudget) (any, bool) {
	if !budget.visit() {
		return nil, false
	}
	switch t := v.(type) {
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, vv := range t {
			cv, ok := deepCloneValue(vv, budget)
			if !ok {
				return nil, false
			}
			out[k] = cv
		}
		return out, true
	case []any:
		out := make([]any, len(t))
		for i, vv := range t {
			cv, ok := deepCloneValue(vv, budget)
			if !ok {
				return nil, false
			}
			out[i] = cv
		}
		return out, true
	default:
		return v, true
	}
}
