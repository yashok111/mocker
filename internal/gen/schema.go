package gen

import (
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"
)

// walker carries everything ONE Body/Headers call's schema walk needs that
// is NOT part of the schema node itself: the resolver (for lazy $ref
// chasing — DESIGN §2/§7: never a fully dereferenced tree), the seed and
// clock for this one request, and a byte/node budget SHARED by pointer
// across every recursive call so a deeply or widely nested generation run
// can stop growing before it exceeds Options.MaxBytes. A walker is built
// fresh per call and never escapes it (see Generator.newWalker) — no field
// here is shared across requests or goroutines, matching DESIGN §9's "fully
// synchronous, no shared mutable generator state" rule. mergeCache is the
// one deliberate exception: it is a *pointer* shared with every other
// walker built over the same Generator (compose.go's mergeAllOf memo,
// safe precisely because it is keyed by the composition node's own
// content, never by anything request-scoped — see mergeCache's own doc).
type walker struct {
	// compHops is the count of oneOf/anyOf/discriminator branch hops along
	// the current path; see generateBranch (compose.go).
	compHops   int
	res        resolver
	opts       Options
	req        Request
	seedList   uint64
	now        time.Time // Options.Now(), frozen once per call — see Generator.newWalker
	budget     *walkBudget
	mergeCache *mergeCache

	// recipePrefix is "" almost everywhere: the body root (gen.go's Body,
	// via newWalker) and detailBody's own single-item walk (list.go) both
	// restart dataPath at "" too, but BOTH of those restarts already ARE
	// the absolute root — nothing prefixes them. The one place this is
	// non-empty is list.go's generateItems, which restarts dataPath at ""
	// PARTWAY through a walk whose absolute root was already elsewhere
	// ("items", or the array's own wrapper property name): there,
	// recipePrefix holds that missing absolute prefix ("items[3]") so
	// recipePath (below) can glue it back on for a lookup. recipePath is
	// the ONLY consumer — it must never reach SeedScalar, a filter, a
	// field-name classification, or anything else dataPath itself already
	// drives, or a recipe binding would silently reshuffle the very seed
	// contract HARD RULE 6 exists to keep untouched.
	recipePrefix string
}

// resolver is the subset of *openapi.Resolver the walk needs. Declared here
// (rather than depending on the concrete type directly at every call site)
// only to keep gen_test.go able to exercise the walk without constructing a
// full Document — Generator itself is built over a real *openapi.Resolver,
// which satisfies this trivially.
type resolver interface {
	Resolve(pointer string) (any, error)
	ResolveNode(node any) (any, error)
}

// walkBudget bounds BOTH the serialized size of the result (DESIGN §9
// "Границы": "Большой listSize на вложенной схеме умножается на каждом
// уровне — режем по размеру результата") and, as a hard safety ceiling
// independent of that byte accounting, the total number of schema nodes
// visited in one walk — the second line of defense that makes termination
// provable on a self-referencing schema regardless of how the byte math
// plays out on any particular shape.
type walkBudget struct {
	remaining int64
	nodes     int
}

// maxWalkNodes is an absolute ceiling on how many schema nodes one Body
// call's walk will visit, independent of Options.MaxBytes/MaxDepth. It
// exists so "provably terminating" does not rest on a subtle argument about
// how fast the byte budget depletes on every possible schema shape: once
// this many nodes have been visited, every further node is treated as if
// it were past MaxDepth (DESIGN §9 boundaries: recursion depth, array
// length, response size all cut generation short, never let it run away).
const maxWalkNodes = 200_000

// spend reserves n bytes from the remaining budget, returning false (and
// reserving nothing) if that would go negative. Called for array items
// once minItems is already satisfied — the array is allowed to stop
// growing there without breaking the schema.
func (b *walkBudget) spend(n int) bool {
	if b == nil {
		return true
	}
	if int64(n) > b.remaining {
		return false
	}
	b.remaining -= int64(n)
	return true
}

// forceSpend always reserves n bytes, even past zero. Used for content a
// schema REQUIRES (a required property, an item within minItems) — a hard
// schema constraint outranks the soft "cut for size" heuristic, but going
// negative here correctly makes every LATER spend() call fail fast,
// self-limiting the rest of the walk.
func (b *walkBudget) forceSpend(n int) {
	if b == nil {
		return
	}
	b.remaining -= int64(n)
}

// visitNode counts one more node visited; false once maxWalkNodes is
// exceeded, telling the caller to stop recursing regardless of depth/bytes.
func (b *walkBudget) visitNode() bool {
	if b == nil {
		return true
	}
	b.nodes++
	return b.nodes <= maxWalkNodes
}

// snapshot reads remaining without spending anything — walkObject/walkArray
// compare a before/after snapshot around a recursive walkNode call to tell
// whether that call spent anything internally, which is how they decide
// whether the value it returned still needs charging at THIS level (see
// their own doc comments; the P1b round-1 review's finding 2). A nil budget
// reads as a fixed 0 on both sides of any such comparison, so the
// "unchanged means unaccounted, charge it" branch still triggers correctly
// even when there is no budget in play at all (forceSpend is a no-op then
// anyway).
func (b *walkBudget) snapshot() int64 {
	if b == nil {
		return 0
	}
	return b.remaining
}

// capShare temporarily narrows b.remaining to a 1/n share of its CURRENT
// (positive) value, returning a restore func that adds back whatever of
// the hidden (n-1)/n portion was NOT spent by the work done under the cap.
// walkObject uses this to give each of several optional sibling
// properties on the SAME object a FAIR slice of whatever budget remains,
// instead of the first one processed getting to spend the WHOLE thing
// before its siblings ever feel the squeeze — P1b round-1 review, finding
// 7: several independently rich-but-optional properties (the real
// corpus's Tenant.cohorts and Tenant.roles, each chaining
// several levels into further optional sub-structure) otherwise EACH
// individually stayed within a byte budget taken alone, yet still
// compounded to several times over it once reached as siblings on the
// same object, none of them able to see how much of the budget an EARLIER
// sibling already used up.
//
// The arithmetic is exact, not approximate: whatever the capped work
// actually spends comes out of the REAL remaining exactly once — replacing
// the hidden portion afterward means the running total after N siblings
// each get their own capShare/restore pair equals remaining minus the SUM
// of what each one really spent, identical to never capping at all, just
// reached with an earlier stop inside each individual share.
func (b *walkBudget) capShare(n int) func() {
	if b == nil || n <= 1 || b.remaining <= 0 {
		return func() {}
	}
	share := b.remaining / int64(n)
	hidden := b.remaining - share
	b.remaining = share
	return func() {
		b.remaining += hidden
	}
}

// walkNode resolves node (chasing any $ref, one hop at a time, via the
// Resolver — DESIGN §2/§7 forbid building a dereferenced tree) and
// dispatches on what it finds. A JSON Schema boolean subschema (legal per
// 2020-12: `true` matches anything, `false` matches nothing) is handled
// directly since it never has properties/type of its own to walk.
func (w *walker) walkNode(node any, dataPath string, depth int) (any, error) {
	resolved, err := w.res.ResolveNode(node)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
	}
	switch v := resolved.(type) {
	case map[string]any:
		return w.walkSchema(v, dataPath, depth)
	case bool:
		if !v {
			return nil, ErrUnsatisfiable
		}
		return nil, nil
	case nil:
		return nil, nil
	default:
		return nil, fmt.Errorf("%w: schema node is not an object", ErrNoSchema)
	}
}

// walkSchema is walkNode's implementation once node has been resolved to a
// concrete (non-$ref) schema object. The order here is deliberate:
//
//  1. depth/node budgets — a schema that is ABOUT to be cut short must be
//     cut short before anything else looks at it.
//  2. forced nullability (type exactly ["null"]) — the ONE case nothing
//     could ever override: no coercion, no recipe, satisfies a type that
//     excludes everything but null.
//  3. composition (allOf/oneOf/anyOf/discriminator) and the value-source
//     priority chain (recipe/example/const/default/enum/format/field name)
//     — both seams the Values agent implements; a concrete pinned/composed
//     value always wins over generation, and DESIGN §9 line 339 ranks a
//     recipe above even a schema-level example/const/default. An explicit
//     "default": null falls out of this chain naturally — leafValue's own
//     default check (values.go) reads schema["default"] via comma-ok, so a
//     nil default flows through as a legitimate (if unexciting) pinned
//     value exactly like a non-nil one, AFTER recipe/example/const have had
//     their turn, never before.
//  4. the PROBABILISTIC nullRate roll — deliberately last among the "make
//     it null" checks, so a schema-pinned example/default is never thrown
//     away in favor of a random null.
//  5. structural generation (object/array) or Kernel's own bare scalar
//     fallback, when nothing above produced a value.
func (w *walker) walkSchema(schema map[string]any, dataPath string, depth int) (any, error) {
	if depth > w.opts.effMaxDepth() || !w.budget.visitNode() {
		return depthLimitValue(w, schema, dataPath), nil
	}

	forced, allowed := nullableInfo(schema)
	if forced {
		return nil, nil
	}

	if hasComposition(schema) {
		return composeValue(w, schema, dataPath, depth)
	}
	if v, ok := leafValue(w, schema, dataPath); ok {
		return v, nil
	}

	if allowed && w.rollNull(dataPath) {
		return nil, nil
	}

	return w.structural(schema, dataPath, depth)
}

// structural is the plain object/array/scalar walk — DESIGN §9's
// "генерация по схеме" once no pinned/composed value source applied. It is
// also what the composition stub falls back to (see stub_compose.go): a
// composed schema Values hasn't landed support for yet is generated from
// its own bare type/properties, same as any other node.
func (w *walker) structural(schema map[string]any, dataPath string, depth int) (any, error) {
	switch SchemaType(schema) {
	case "object":
		return w.walkObject(schema, dataPath, depth)
	case "array":
		return w.walkArray(schema, dataPath, depth)
	default:
		return w.genScalar(schema, dataPath)
	}
}

// walkObject generates one JSON object from schema's properties.
// writeOnly properties are never included in a response body — even when
// they are also listed in required, which DESIGN calls out as a spec bug:
// omitting is the honest reading, not a crash. Property iteration is
// sorted purely for predictable behavior under test; it does not affect
// the output bytes, since encoding/json always sorts map[string]any keys
// on Marshal.
func (w *walker) walkObject(schema map[string]any, dataPath string, depth int) (any, error) { //nolint:gocyclo // one branch per budget state a property can be in (required/optional × depth-ceiling/doomed-reserve/individually-priced/self-accounted) — DESIGN §9's own boundary rules, not incidental branching; see the function's own doc and the hard-ceiling comments inline
	// The object's own `{`/`}` wrapper (P1b round-2 review, finding 1):
	// every per-property charge below accounts for that property's own
	// key/value/comma, but nothing ever charged the two brace bytes the
	// object itself contributes when spliced into ITS OWN parent — the
	// mirror image of the LAST property's charge always assuming a
	// trailing comma that a real marshal never emits for the final key.
	// Charging both fixed costs here (rather than tracking which property
	// ends up literally last, fragile once optional ones can be rolled
	// back below) nets out to the same total either way: two bytes owed,
	// one byte over-charged already, so what was a systematic ~1-byte
	// UNDER-count per nested object — invisible on any one object, but
	// compounding across every item of a long array of small objects
	// (exactly this corpus's roles->positions->attributes->cohorts->
	// children shape) into real, measurable MaxBytes overshoot — becomes a
	// harmless ~1-byte over-charge instead.
	w.budget.forceSpend(2)

	props, _ := schema["properties"].(map[string]any)
	required := stringSet(schema["required"])
	result := make(map[string]any, len(props))

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	// optionalLeft is a (slightly over-)approximate count of optional
	// properties still to come, walked down as each one is reached — used
	// below only to decide whether an OPTIONAL property gets skipped once
	// the budget is exhausted. siblingsLeft is the same running count over
	// EVERY property, required or not, and is what capShare actually
	// divides by (P1b round-2 review, finding 1): dividing only among
	// optional siblings left every REQUIRED property free to recurse with
	// the WHOLE remaining budget for its own nested optional content —
	// the real corpus's Participants has two required array properties
	// (tenant_members, non_tenant_members), and the first one
	// processed could spend the entire budget on its own array before the
	// second was even reached, since forceSpend for a required property
	// was never capped against required siblings, only optional ones.
	// capShare does not change what a required property is FORCED to
	// spend on its own required content either way (forceSpend ignores
	// the cap, same as it ignores remaining going negative) — it only
	// narrows what OPTIONAL content nested inside that property's own
	// subtree can see as available before its siblings get a turn.
	// writeOnly properties are counted in both even though they are
	// always skipped — a harmless over-count, not an under-count, and
	// cheap to compute exactly since walkObject would need to resolve
	// every property either way.
	optionalLeft := 0
	siblingsLeft := 0
	for _, name := range names {
		siblingsLeft++
		if !required[name] {
			optionalLeft++
		}
	}

	// floorFrom[i] answers one question UP FRONT, before generating
	// names[i]: summed across every REQUIRED property at position i or
	// later (names[i:]), what is the least this object could possibly
	// still owe from here on, at each such property's own smallest legal
	// size? floorFrom[len(names)] is 0 (nothing left); floorFrom[i] is
	// that plus names[i]'s own minCost when names[i] is required, or just
	// floorFrom[i+1] unchanged when it is optional. This is a RESERVE, not
	// a one-shot "is the whole object doomed" flag: checked fresh at every
	// property against the LIVE budget (`before`, which already reflects
	// everything actually spent so far — always >= what floorFrom assumed,
	// since minCost is a lower bound and real generation is rarely
	// cheaper), so it tightens automatically as required siblings turn out
	// to cost more than their own floor, not just once at the start.
	//
	// Two places consult it (both below): an OPTIONAL property is skipped
	// once `before` would not leave enough for the REQUIRED content still
	// to come (`before <= floorFrom[idx]`, which — since floorFrom[idx] is
	// 0 once no required siblings remain — subsumes the old plain
	// "remaining exhausted" check as its own special case); a REQUIRED
	// property goes straight to its OWN minimal form, skipping the real
	// walk entirely, once even the reserve for itself AND every required
	// sibling still ahead already exceeds what remains.
	//
	// Without this, an optional property reached EARLY (alphabetically,
	// before enough required siblings have run to reveal how tight the
	// budget really is) can look perfectly affordable at the moment it is
	// generated — the per-property checks below are honest about what
	// they know AT THAT TIME — and survive, only for the REQUIRED
	// properties reached afterward to still unconditionally spend their
	// own floor on top of it regardless (forceSpend/minCost never shrink
	// required content further once it is already at its own smallest
	// legal form). Found verifying this exact fix against the real
	// corpus: Bulletin's required floor alone is 136 bytes, already over
	// a 128-byte MaxBytes — yet the OPTIONAL `description` property,
	// generated first (alphabetically ahead of the required properties
	// that would have revealed the deficit), stayed at its own
	// fully-generated size because ITS OWN post-generation check still
	// found the budget non-negative at the time it ran, inflating the
	// body to 210 bytes for no reason the schema demands: the required
	// floor alone was always going to exceed 128 bytes regardless of what
	// `description` did. A single whole-object "doomed or not" flag,
	// computed once up front, fixes that exact case but not the milder
	// version of the same pattern where the TOTAL required floor fits
	// comfortably yet several independently-affordable-looking optional
	// properties still compound past MaxBytes before any ONE of them
	// individually trips the plain remaining<=0 check — the per-property,
	// live-checked reserve here catches that too, since it shrinks
	// starting from wherever `before` REALLY is at each property, not
	// from the object's starting budget.
	floorFrom := make([]int64, len(names)+1)
	// floorVal/floorSize keep what this pass priced so the per-property
	// loop below reuses it instead of running hardCeilingValue — a
	// recursion over the whole required subtree — a SECOND time for the
	// identical (schema, path, depth); floorPriced marks the entries that
	// were actually computed here.
	floorVal := make([]any, len(names))
	floorSize := make([]int, len(names))
	floorPriced := make([]bool, len(names))
	if w.budget != nil {
		for i := len(names) - 1; i >= 0; i-- {
			floorFrom[i] = floorFrom[i+1]
			name := names[i]
			if !required[name] {
				continue
			}
			resolved, err := w.res.ResolveNode(props[name])
			if err != nil {
				continue
			}
			reqSchema, _ := resolved.(map[string]any)
			if isWriteOnly(reqSchema) {
				continue
			}
			// depth+1: this property's own value would sit one level below
			// THIS object, the exact depth walkNode would receive for it
			// below (childDepth) — hardCeilingValue's own depth parameter
			// is always the depth of the schema being priced, never the
			// depth of its container.
			mv := hardCeilingValue(w, reqSchema, joinPath(dataPath, name), depth+1)
			floorVal[i], floorSize[i], floorPriced[i] = mv, estimateJSONSize(mv), true
			floorFrom[i] += int64(floorSize[i] + len(name) + 4)
		}
	}

	for idx, name := range names {
		propNode := props[name]
		resolvedProp, err := w.res.ResolveNode(propNode)
		if err != nil {
			return nil, fmt.Errorf("%w: property %q: %w", ErrNoSchema, name, err)
		}
		propSchema, _ := resolvedProp.(map[string]any)
		isRequired := required[name]
		if !isRequired {
			optionalLeft--
		}
		siblingsLeft--
		if isWriteOnly(propSchema) {
			continue
		}

		childPath := joinPath(dataPath, name)
		childDepth := depth + 1

		// DESIGN §9 boundaries: at the depth ceiling, an optional property
		// is omitted outright (never even attempted) and a required one
		// gets the smallest legal value for its type — computed directly,
		// without recursing into it (recursing is exactly what the ceiling
		// exists to stop).
		if childDepth > w.opts.effMaxDepth() {
			if !isRequired {
				continue
			}
			val := depthLimitValue(w, propSchema, childPath)
			result[name] = val
			// Same len(name)+4 wrapper charge as the ordinary (non-ceiling)
			// leaf branch below, and for the same reason (P1b round-2
			// review, finding 1): depthLimitValue's stub is charged only
			// its own bare value size here otherwise, missing the
			// `"name":`+comma bytes every OTHER charge site accounts for.
			// A schema several levels below the ceiling with a long chain
			// of required properties (this corpus's roles->positions
			// ->attributes->cohorts->children shape) hits this branch on
			// EVERY required leaf at the ceiling, so an 8-17 byte miss per
			// property compounds fast across the many array items such a
			// chain produces.
			w.budget.forceSpend(estimateJSONSize(val) + len(name) + 4)
			continue
		}

		// before is captured BEFORE capShare narrows the visible budget —
		// capShare's restore func always adds the hidden portion back, so
		// comparing against a snapshot taken AFTER capping would show a
		// "change" even when walkNode spent nothing at all, breaking the
		// self-accounted detection below for every property. Snapshotting
		// before/restoring after instead keeps the delta exactly "what
		// walkNode really spent", cap or no cap — capShare's own doc
		// comment works out why that arithmetic is exact.
		before := w.budget.snapshot()
		wrapperCost := len(name) + 4 // `"` + name + `":` + `,`

		// THE HARD CEILING for a required property (Body's own doc comment,
		// "cost a required subtree before committing to it, or emit its
		// depth-limit form when it will not fit"): before ever recursing
		// into the real generation below, price the schema's own SMALLEST
		// legal representation of this property — the same non-recursive
		// depthLimitValue the depth ceiling already uses — and compare that
		// price to what is actually left. Two outcomes:
		//
		//   - the minimal form itself does not fit in `before`: nothing
		//     richer will fit either, so there is no point recursing at
		//     all — emit the minimal form directly and move on. This is
		//     the case that stays a DOCUMENTED, honest overshoot: the
		//     schema's own floor for this property genuinely exceeds what
		//     remains (Body's doc comment's badges example, MaxBytes
		//     below its 174-byte floor).
		//   - the minimal form WOULD fit: there is room for something
		//     richer, so the real walk below is worth attempting — but
		//     its EVENTUAL cost is not knowable without running it (a
		//     recipe, example, or nested optional structure sized by its
		//     own capShare share all affect it), so the symmetric check
		//     after generation (below) is what actually enforces the
		//     ceiling in that case, falling back to this same minVal if
		//     the real walk overshot `before` after all.
		//
		// Previously this branch fired only on `remaining <= 0` — a check
		// made from the TOTAL shared budget, not this property's own cost,
		// so a required property whose real generation cost far more than
		// what was left (measured: badges' 601-byte body at
		// MaxBytes=512, a schema whose own floor is 174 bytes) was always
		// committed in full anyway once `remaining` was merely positive,
		// however small. Comparing against minCost instead catches that
		// case too, since minCost is always >= 1.
		var minVal any
		var minCost int64
		if isRequired {
			if floorPriced[idx] {
				minVal, minCost = floorVal[idx], int64(floorSize[idx]+wrapperCost)
			} else {
				minVal = hardCeilingValue(w, propSchema, childPath, childDepth)
				minCost = int64(estimateJSONSize(minVal) + wrapperCost)
			}
			// floorFrom[idx] (this function's own doc comment above) already
			// includes THIS property's own minCost, summed with every
			// required sibling still ahead — so comparing it against
			// `before` in one shot subsumes the plain `before < minCost`
			// check (that is just the special case where idx is the LAST
			// required property, floorFrom[idx] == minCost) while also
			// catching what that check alone cannot: this property's own
			// floor fits fine on its own, but not once every required
			// sibling still to come is reserved for too. Going straight to
			// the floor here, not just once some LATER property discovers
			// the deficit, is what makes the object land exactly at its own
			// required floor rather than "somewhere over it".
			if w.budget != nil && floorFrom[idx] > before {
				result[name] = minVal
				w.budget.forceSpend(int(minCost))
				continue
			}
		}

		// The shared byte budget is the OTHER, purely-optional ceiling: once
		// it is exhausted, an OPTIONAL property is skipped outright — DESIGN
		// §9's own "режем по размеру результата" applies to object
		// properties, not only array items. A required property never gets
		// skipped this way; the minCost check above already decided whether
		// it is even worth attempting the real walk. floorFrom[idx] (this
		// function's own doc comment above) extends the same skip to a
		// budget that ISN'T literally exhausted yet but provably will be
		// once the object's OWN required siblings still ahead are through
		// with it — without it, an optional property reached before enough
		// required ones have run stays fully generated on the strength of a
		// budget that looks fine only because the real, unavoidable cost has
		// not been charged against it yet. floorFrom[idx] is 0 once no
		// required siblings remain, so this already covers the plain
		// "budget exhausted" case too; there is nothing left to OR it with.
		if !isRequired && w.budget != nil && before <= floorFrom[idx] {
			continue
		}

		// Reserved BEFORE generating the value, not charged only after
		// (P1b round-2 review, finding 1): charging it afterward let a
		// recursive object/array value legitimately spend the ENTIRE
		// visible share on its own required content and leave nothing for
		// the very key+comma bytes that wrap it — exactly what happened to
		// the real corpus's lone required `enabled_featureflags` array
		// property, which filled its whole capped share cleanly (ending
		// AT zero, not negative) and only then, back in the parent, was
		// charged a further 25 bytes it never reserved room for. Reserving
		// it first means the child recursion sees a budget that already
		// excludes its own wrapper, so it cannot spend into those bytes.
		w.budget.forceSpend(wrapperCost)
		// siblingsLeft+1 (this one), not optionalLeft+1: see siblingsLeft's
		// own doc comment above (P1b round-2 review, finding 1) for why a
		// required property needs the same fair share too.
		restoreShare := w.budget.capShare(siblingsLeft + 1) // +1: this one
		val, err := w.walkNode(propNode, childPath, childDepth)
		restoreShare()
		if err != nil {
			return nil, err
		}
		result[name] = val

		// Charging estimateJSONSize(val) here is correct — and the ONLY
		// place that ever will — for a value this call produced ITSELF
		// without spending anything internally beyond the wrapper reserved
		// above: a scalar from genScalar/leafValue, a pinned
		// example/const/default value, or a depth/node-ceiling stub
		// (schema.go's depthLimitValue, reached through walkSchema's OWN
		// ceiling check — which returns before ever touching w.budget). A
		// val that came from a RECURSIVE walkObject/walkArray call instead
		// (through walkNode->walkSchema->structural above) already
		// forceSpend/spent for every one of ITS OWN leaves against this
		// SAME shared w.budget — charging its whole marshaled size AGAIN
		// here would double-spend those bytes once per level of nesting
		// (the P1b round-1 review's finding 2).
		//
		// The two cases are told apart by whether the budget's own
		// remaining count moved BELOW what the wrapper reservation alone
		// left it at, not by val's Go type: an early version of this fix
		// skipped the charge for every map/array value uniformly, which
		// correctly avoided double-counting genuine recursive content but
		// ALSO silently let every depth/node-ceiling stub through
		// completely uncharged — once maxWalkNodes trips (a schema.go
		// ceiling shared globally across the whole request), every FURTHER
		// node anywhere in the walk becomes exactly such a stub, and an
		// object built entirely out of free stubs never depletes the
		// budget at all, regressing right back to the unbounded-body
		// failure mode finding 2 was fixing in the first place.
		if w.budget.snapshot() == before-int64(wrapperCost) {
			w.budget.forceSpend(estimateJSONSize(val))
		}

		if !isRequired {
			// The property was OPTIONAL — the object is already
			// schema-legal without it — but its generated value (a rich
			// nested chain of its OWN required content, most often) still
			// forceSpent enough to push the real budget negative. Same
			// rollback walkArray's optional-item path performs and for the
			// identical reason (P1b round-2 review, finding 1): nothing
			// requires keeping a value the schema never demanded once it
			// turns out to be what pushed the response over MaxBytes, so
			// it is dropped and generation moves on to the next sibling
			// with the budget restored to what it was before this
			// property was even attempted.
			if w.budget.remaining < 0 {
				delete(result, name)
				w.budget.remaining = before
			}
			continue
		}

		// isRequired: the property cannot be dropped, but — the other half
		// of the hard-ceiling fix — it does not have to stay at whatever
		// size the real walk actually produced either. minCost above
		// already proved the schema's own minimal legal form fits in
		// `before`; if the REAL generation overshot `before` anyway (richer
		// than strictly required, e.g. an optional grandchild that itself
		// only fit because ITS OWN budget check ran before this property's
		// overshoot was known), replace it with that already-known-to-fit
		// minimal form instead of keeping the oversized one — exactly the
		// badges case Body's doc comment measures (a 174-byte floor,
		// yet 601 bytes emitted at MaxBytes=512, because the full walk was
		// always kept no matter how far past `before` it landed). Resetting
		// `remaining` to before-minCost (not to `before`) keeps the ledger
		// honest: minCost bytes really were spent on this property, they
		// are just minVal's bytes instead of val's.
		if w.budget != nil && w.budget.remaining < 0 {
			result[name] = minVal
			w.budget.remaining = before - minCost
		}
	}
	return result, nil
}

// walkArray generates a JSON array. Its length starts from
// Options.ListSize (clamped into [minItems, maxItems] when the schema
// declares them, and hard-capped at maxGenArrayLength regardless — see
// arrayLength) and is cut short once the shared byte budget runs out —
// DESIGN §9: "Большой listSize на вложенной схеме умножается на каждом
// уровне — режем по размеру результата". minItems is always honored in
// full (forceSpend, even past zero remaining budget) since it is a hard
// schema constraint; anything beyond it is the soft, size-driven part.
func (w *walker) walkArray(schema map[string]any, dataPath string, depth int) (any, error) { //nolint:gocyclo // one branch per budget state an item can be in (required-within-minItems/optional × individually-priced/self-accounted), mirroring walkObject's own required-property accounting for the identical reason
	// The array's own `[`/`]` wrapper — same reasoning, same fix, as
	// walkObject's identical brace charge above (P1b round-2 review,
	// finding 1): every per-item charge below accounts for that item's own
	// value+comma, never the two bracket bytes the array itself
	// contributes, while ALSO always assuming a trailing comma after the
	// last item that a real marshal never emits. The two fixed costs net
	// out the same way: what was an ~1-byte-per-array under-count becomes
	// a harmless ~1-byte over-charge.
	w.budget.forceSpend(2)

	itemsNode, hasItems := schema["items"]
	target := w.arrayLength(schema, dataPath)
	minItems := 0
	if v, ok := numFromSchema(schema, "minItems"); ok && v > 0 {
		minItems = int(v)
	}

	// itemSchema is resolved once, up front, and shared by both
	// minOptionalItemCost below and the required-item (i<minItems) hard-
	// ceiling check inside the loop — both need the SAME non-recursive
	// "what is this item schema's own smallest legal shape" answer
	// depthLimitValue/minimalLegalValue already compute, so there is no
	// reason to re-resolve itemsNode on every iteration.
	var itemSchema map[string]any
	if hasItems {
		if resolvedItems, err := w.res.ResolveNode(itemsNode); err == nil {
			itemSchema, _ = resolvedItems.(map[string]any)
		}
	}

	// minOptionalItemCost is a cheap LOWER-BOUND estimate — the item
	// schema's own smallest legal representation, the same minimalLegalValue
	// the depth ceiling already uses, never a full recursive walk — of what
	// one more OPTIONAL (i>=minItems) item would cost at minimum. Beyond
	// minItems an array is already schema-legal without the item at all, so
	// attempting one only to discover AFTERWARD that its own required
	// content pushed the budget over (P1b round-2 review, finding 1) is
	// avoidable: if not even the item's cheapest legal shape fits, a
	// richer/realistic one certainly will not either, and skipping it
	// outright costs nothing the schema demands.
	minOptionalItemCost := int64(1) // just the comma, if items' own schema can't be resolved
	if itemSchema != nil {
		minOptionalItemCost = int64(estimateJSONSize(minimalLegalValue(w, itemSchema)) + 1)
	}

	result := make([]any, 0, target)
	for i := range target {
		if i >= minItems && w.budget != nil && w.budget.remaining < minOptionalItemCost {
			break // not even the item's own smallest legal shape would fit
		}

		itemPath := fmt.Sprintf("%s[%d]", dataPath, i)
		before := w.budget.snapshot()

		if i < minItems {
			// Required position (within minItems): THE HARD CEILING, same
			// pattern and reason as walkObject's required-property fix
			// (Body's own doc comment, "cost a required subtree before
			// committing to it, or emit its depth-limit form when it will
			// not fit"). hardCeilingValue is walkObject's own required-
			// property helper, shared here — it prices the item schema's
			// own smallest LEGAL representation (recursing into the item's
			// own required properties, depth permitting, not just one bare
			// level), so pricing it costs nothing extra it wasn't already
			// paying for elsewhere. depth+1 matches the depth the item
			// itself is walked at below (w.walkNode(itemsNode, itemPath,
			// depth+1)) — hardCeilingValue's depth parameter is always the
			// depth of the schema being priced, never its container's.
			//
			// Without this, a required array property nested inside a
			// required OBJECT property could recurse fully here regardless
			// of how little budget was left, pushing that enclosing
			// object's own remaining deeply negative — which, one level up,
			// walkObject's OWN hard-ceiling fallback would then have no
			// choice but to discard the WHOLE enclosing object (its only
			// alternative representation) rather than just this one
			// oversized item. Bounding it HERE, at the item's own level,
			// keeps an overshoot localized to the smallest subtree that
			// actually caused it.
			minVal := hardCeilingValue(w, itemSchema, itemPath, depth+1)
			minCost := int64(estimateJSONSize(minVal) + 1) // +1: this item's comma
			if w.budget != nil && before < minCost {
				result = append(result, minVal)
				w.budget.forceSpend(int(minCost))
				continue
			}

			// Comma reserved before generating, same reorder/reason as the
			// optional path below and as walkObject's wrapperCost.
			w.budget.forceSpend(1)
			var val any
			if hasItems {
				v, err := w.walkNode(itemsNode, itemPath, depth+1)
				if err != nil {
					return nil, err
				}
				val = v
			}
			// See walkObject's identical before/after-snapshot guard for
			// why this comparison (against before-1, since the comma above
			// already moved it by exactly one) is what tells a value that
			// spent nothing itself apart from a scalar/pinned/ceiling-stub
			// leaf from one that already forceSpent internally through its
			// own recursive walk.
			if w.budget.snapshot() == before-1 {
				w.budget.forceSpend(estimateJSONSize(val))
			}
			// The other half of the fix: minCost already proved the item's
			// own minimal legal form fits in `before` — if the REAL,
			// richer generation overshot `before` anyway, replace it with
			// that already-known-to-fit minimal form instead of keeping
			// the oversized one, exactly like walkObject's required-
			// property fallback. before-minCost (not `before`) keeps the
			// ledger honest: minCost bytes really were spent on this item.
			if w.budget != nil && w.budget.remaining < 0 {
				val = minVal
				w.budget.remaining = before - minCost
			}
			result = append(result, val)
			continue
		}

		// This item's own trailing separator, reserved BEFORE generating
		// its value, not charged only after (P1b round-2 review, finding
		// 1) — same reorder, same reason, as walkObject's wrapperCost:
		// charged afterward, an item that recurses into an object/array
		// could spend its ENTIRE (possibly capped) share on its own
		// required content and leave nothing for the comma byte charged
		// afterward. Reserving it first means the recursion below sees a
		// budget that already excludes it.
		w.budget.forceSpend(1)
		// An array's own eventual length is data-dependent — unlike
		// walkObject's KNOWN count of sibling properties, walkArray cannot
		// divide the remaining budget by "how many items are left" because
		// that is exactly what is being decided one item at a time. Halving
		// the visible share for each OPTIONAL (i>=minItems) item instead
		// (capShare(2), the same mechanism, applied per item rather than
		// per sibling) is the array-shaped equivalent of walkObject's fair
		// split: a single deeply-nested chain of optional arrays-of-
		// optional-arrays (P1b round-1 review, finding 7 — the real
		// corpus's roles→positions→attributes→cohorts→children shape) sees
		// its visible allowance shrink geometrically at every level it
		// descends through, instead of every level along the chain getting
		// an unrestricted "at least try one item" shot at whatever the
		// FULL shared remaining still happened to be.
		restoreShare := w.budget.capShare(2)
		var val any
		if hasItems {
			v, err := w.walkNode(itemsNode, itemPath, depth+1)
			restoreShare()
			if err != nil {
				return nil, err
			}
			val = v
		} else {
			restoreShare()
		}

		// See walkObject's identical before/after-snapshot guard (P1b
		// round-1 review, finding 2 — and its own follow-up regression,
		// where a naive "map/array means already charged" heuristic let
		// every depth/node-ceiling stub through completely uncharged; see
		// walkObject's doc comment for the full story). selfAccounted
		// tells apart a val this item's own recursive walkObject/walkArray
		// call already forceSpend/spent for internally (charging it AGAIN
		// here would double-spend it) from one that spent nothing at all —
		// a scalar, a pinned value, or a ceiling stub — which this is the
		// only place that will ever charge. The comparison is against
		// before-1, not before, since the comma above already moved it by
		// exactly one regardless of what val turns out to be.
		selfAccounted := w.budget.snapshot() != before-1

		// Optional position (i >= minItems): the array is already
		// schema-legal without this item, so a value that turns out not to
		// fit is rolled back to `before` (undoing the reserved comma too)
		// rather than kept (P1b round-2 review, finding 1) — the avoidable
		// overshoot minOptionalItemCost's pre-check above tries to catch in
		// advance but, being only a shallow lower-bound estimate, cannot
		// always catch: a required property nested inside a required
		// property is not accounted for at all by minimalLegalValue's own
		// single-level pass, and a still-positive capped share can let the
		// REAL walk generate richer content than the bare minimal one ever
		// would. Nothing requires keeping a value the schema never
		// demanded once it is what finally broke MaxBytes.
		if !selfAccounted {
			if !w.budget.spend(estimateJSONSize(val)) {
				w.budget.remaining = before
				break
			}
		} else if w.budget.remaining < 0 {
			w.budget.remaining = before
			break
		}
		result = append(result, val)
	}
	return result, nil
}

// maxGenArrayLength hard-caps arrayLength's result regardless of what a
// hostile or malformed document's own minItems declares — list.go's
// maxListPageLimit only protects the query-controlled ?limit path on a
// detected list route; an ordinary NESTED array (not list-shaped, or
// reached below a list's own items) has no other ceiling at all before
// this one. Without it, {"type":"array","minItems":5000000,"items":...}
// makes walkArray's own `make([]any, 0, target)` reserve millions of slice
// slots — measured (P1b round-1 review, finding 9) at 431 MB allocated
// and a 10 MB response body for one unauthenticated request at
// minItems=5_000_000, and an outright `makeslice: cap out of range` panic
// (500 on every request to that route, permanently) at minItems=1e15.
// 500 mirrors maxListPageLimit's own reasoning: generous for anything a
// real client renders, firmly bounded for anything a hostile spec asks
// for.
const maxGenArrayLength = 500

// arrayLength is Options.ListSize widened to at least minItems and
// narrowed to at most maxItems, when the schema declares either — then
// hard-capped at maxGenArrayLength regardless of how high minItems pushed
// it. A minItems above the cap means the generated array will fall short
// of the schema's own minItems; that is deliberate (see maxGenArrayLength's
// own doc) — the byte/node budgets that are supposed to cut runaway
// generation short never get a chance to if arrayLength itself already
// handed walkArray a length in the millions.
//
// A listSize recipe bound at dataPath (the array's OWN path, not an
// element's) overrides the Options.ListSize starting point exactly the way
// DESIGN §9 describes: pinned to a fixed size, or picked reproducibly from
// the seed for a [lo,hi] range. It is applied BEFORE the minItems/maxItems
// clamps below, never after — a pinned value that would violate the
// schema's own declared bounds is clamped like anything else, never
// exempted, matching the digest's "it NEVER overrides the schema's own
// minItems/maxItems... clamp, and only clamp." With no *Set at all (HARD
// RULE 6's common case) this function computes exactly what it did before
// this recipe seam existed.
func (w *walker) arrayLength(schema map[string]any, dataPath string) int {
	n := w.opts.effListSize()
	if v, ok := numFromSchema(schema, "minItems"); ok && int(v) > n {
		n = int(v)
	}
	if v, ok := numFromSchema(schema, "maxItems"); ok && int(v) < n {
		n = int(v)
	}

	if set := w.req.Recipes; set != nil {
		if lo, hi, ok := set.ListSizeAt(w.recipePath(dataPath)); ok {
			n = pinnedListSize(lo, hi, w.seedList, dataPath)
			if v, ok := numFromSchema(schema, "maxItems"); ok && int(v) < n {
				n = int(v)
			}
			if v, ok := numFromSchema(schema, "minItems"); ok && int(v) > n {
				n = int(v)
			}
		}
	}

	if n < 0 {
		n = 0
	}
	if n > maxGenArrayLength {
		n = maxGenArrayLength
	}
	return n
}

// rollNull is the ONE place a package-level RNG would have gone, and
// deliberately isn't: the "should this be null" coin flip is itself a
// SeedScalar-derived value, keyed with a suffix that cannot collide with
// any real dataPath ("\x00" cannot appear in a JSON property/array-index
// path), so it is exactly as deterministic and replay-stable as every
// other generated value.
func (w *walker) rollNull(dataPath string) bool {
	rate := w.opts.NullRate
	if rate <= 0 {
		return false
	}
	if rate > 1 {
		rate = 1
	}
	h := SeedScalar(w.seedList, dataPath+"\x00null")
	frac := float64(h%1_000_000) / 1_000_000
	return frac < rate
}

// genScalar is Kernel's OWN bare, type-correct scalar generator — the
// final fallback once composition, examples/const/default/enum, and
// format/field-name realism (all Values-agent territory via the leafValue
// seam) have all declined. It ignores enum/example/const/default on
// purpose: if any of those applied, leafValue would already have returned
// ok=true and genScalar would never run. What it DOES respect are the
// schema's own numeric/length bounds, since "a value satisfying the
// schema" is squarely the walk's job, not a realism concern.
func (w *walker) genScalar(schema map[string]any, dataPath string) (any, error) {
	seed := SeedScalar(w.seedList, dataPath)
	switch SchemaType(schema) {
	case "boolean":
		return seed%2 == 0, nil
	case "integer":
		return genInteger(schema, seed)
	case "number":
		return genNumber(schema, seed)
	default: // "string" and untyped leaves
		// Once the shared byte budget is already exhausted, the bare
		// fallback generator's own realism-flavored default length (16
		// chars, genString's OWN choice — not a schema constraint) stops
		// being free: this scalar may well be a REQUIRED field whose
		// forceSpend cannot be skipped no matter how negative remaining
		// already is (schema.go's "hard schema constraint outranks the
		// soft cut-for-size heuristic" rule). Once there is nothing left
		// to spend, the least-bad length to pick is the schema's own
		// MINIMUM legal one rather than a realism-sized filler — P1b
		// round-1 review, finding 7: gen.go's own doc comment promises
		// Body never exceeds Options.MaxBytes, and every uncapped 16-byte
		// filler on a required field the budget can no longer say no to
		// was quietly breaking that promise across a wide/deep schema.
		tight := w.budget != nil && w.budget.remaining <= 0
		return genString(schema, seed, tight)
	}
}

func genInteger(schema map[string]any, seed uint64) (any, error) {
	lo, hi, hasLo, hasHi := integerBounds(schema)
	switch {
	case hasLo && hasHi:
	case hasLo && !hasHi:
		hi = lo + 2000
	case !hasLo && hasHi:
		lo = hi - 2000
	default:
		lo, hi = -1000, 1000
	}
	if lo > hi {
		return nil, ErrUnsatisfiable
	}
	return lo + int64(seed%integerSpan(lo, hi)), nil
}

// integerSpan is the number of int64 values in [lo, hi] inclusive, computed
// WIDENED throughout — cast to uint64 BEFORE subtracting, never after (see
// below for why the order matters). schema.go's genInteger and values.go's
// generatedInteger both reduce to this same "pick seed%span, add lo" shape,
// and both crashed on it identically (the P1b round-1 review's finding 12,
// reproduced end-to-end through the real serving path): a hostile or
// malformed document can push minimum to exactly math.MinInt64 and
// exclusiveMaximum to exactly 2^63 (one past math.MaxInt64 — the float64
// value IS exactly representable, only the int64 conversion of it isn't),
// landing hi at math.MaxInt64 and lo at math.MinInt64. The OLD computation
// — `uint64(hi-lo) + 1`, subtracting FIRST in int64 arithmetic, THEN
// casting — overflows int64 itself (the true difference, 2^64-1, does not
// fit in int64) and silently wraps to a span of exactly 0, and `seed %
// span` on a zero span is a live divide-by-zero panic, 500ing every
// request to that route forever. Casting to uint64 BEFORE subtracting
// avoids that: for any lo<=hi, `uint64(hi) - uint64(lo)` computed with
// uint64's OWN wraparound arithmetic equals the true mathematical
// difference exactly (the two's-complement bit patterns make the wrap
// self-correcting), and that true difference is always <= 2^64-1, which
// DOES fit in a uint64. The one value it still cannot hold is the span
// itself when lo/hi are the two extremes: 2^64-1 (the difference) plus 1
// is 2^64, one more than the largest uint64 — so that one case is
// special-cased directly rather than left to wrap back to 0.
func integerSpan(lo, hi int64) uint64 {
	if lo == math.MinInt64 && hi == math.MaxInt64 {
		return math.MaxUint64
	}
	return uint64(hi) - uint64(lo) + 1
}

func integerBounds(schema map[string]any) (lo, hi int64, hasLo, hasHi bool) {
	if v, ok := numFromSchema(schema, "minimum"); ok {
		lo, hasLo = int64(math.Ceil(v)), true
	}
	if v, ok := numFromSchema(schema, "exclusiveMinimum"); ok {
		c := int64(math.Floor(v)) + 1
		if !hasLo || c > lo {
			lo, hasLo = c, true
		}
	}
	if v, ok := numFromSchema(schema, "maximum"); ok {
		hi, hasHi = int64(math.Floor(v)), true
	}
	if v, ok := numFromSchema(schema, "exclusiveMaximum"); ok {
		c := int64(math.Ceil(v)) - 1
		if !hasHi || c < hi {
			hi, hasHi = c, true
		}
	}
	return
}

func genNumber(schema map[string]any, seed uint64) (any, error) {
	lo, hasLo := numFromSchema(schema, "minimum")
	hi, hasHi := numFromSchema(schema, "maximum")
	if v, ok := numFromSchema(schema, "exclusiveMinimum"); ok {
		c := v + 1e-6
		if !hasLo || c > lo {
			lo, hasLo = c, true
		}
	}
	if v, ok := numFromSchema(schema, "exclusiveMaximum"); ok {
		c := v - 1e-6
		if !hasHi || c < hi {
			hi, hasHi = c, true
		}
	}
	switch {
	case hasLo && hasHi:
	case hasLo && !hasHi:
		hi = lo + 1000
	case !hasLo && hasHi:
		lo = hi - 1000
	default:
		lo, hi = 0, 1000
	}
	if lo > hi {
		return nil, ErrUnsatisfiable
	}
	frac := float64(seed%1_000_000) / 1_000_000
	val := lo + frac*(hi-lo)
	return math.Round(val*100) / 100, nil
}

// tight, when true, collapses hi down to the schema's own minimum (lo)
// instead of picking within [lo, hi] — see genScalar's own call site for
// why (P1b round-1 review, finding 7).
func genString(schema map[string]any, seed uint64, tight bool) (any, error) {
	lo, hi, err := stringLengthBounds(schema)
	if err != nil {
		return nil, err
	}
	if tight {
		hi = lo
	}
	length := lo
	if hi > lo {
		length = lo + int(seed%uint64(hi-lo+1))
	}
	const alphabet = "abcdefghijklmnopqrstuvwxyz0123456789"
	b := make([]byte, length)
	h := seed
	for i := range b {
		h = fnv1a(h, []byte{byte(i)})
		b[i] = alphabet[h%uint64(len(alphabet))]
	}
	return string(b), nil
}

// maxGenStringLength hard-caps any generated string's length, regardless of
// what a hostile or malformed document's own minLength/maxLength declare —
// both here (the bare fallback generator's own length choice) and in
// values.go's declaredStringLength (the format/field-name-realism path's
// equivalent). Without it, one field declaring {"minLength": 10000000} —
// let alone twenty such fields in one object — generates tens or hundreds
// of megabytes for a single unauthenticated request (measured, P1b
// round-1 review finding 10: 200,000,181 bytes / 1.4 GB allocated from
// ~1 KB of schema JSON), and a large enough minLength is an outright
// process-killing OOM. 65536 (64 KiB) is generous for any realistic single
// field while keeping the worst case firmly bounded.
const maxGenStringLength = 65536

// clampStringLength floors a schema-declared minLength/maxLength at 0 — a
// negative bound is nonsensical and, left alone, flows straight into a
// negative `make([]byte, length)` panic in genString (P1b round-1 review,
// finding 11: reproduced end-to-end through the real serving pipeline,
// permanently 500ing whichever seeds land the computed length below zero)
// — and ceilings it at maxGenStringLength (finding 10).
func clampStringLength(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxGenStringLength {
		return maxGenStringLength
	}
	return n
}

func stringLengthBounds(schema map[string]any) (lo, hi int, err error) {
	lo = 0
	hi = 16
	if v, ok := numFromSchema(schema, "minLength"); ok {
		lo = clampStringLength(int(v))
	}
	if v, ok := numFromSchema(schema, "maxLength"); ok {
		hi = clampStringLength(int(v))
	} else if hi < lo {
		hi = lo
	}
	if hi < lo {
		return 0, 0, ErrUnsatisfiable
	}
	if hi-lo > 200 {
		hi = lo + 200 // sane cap for the bare fallback generator
	}
	return lo, hi, nil
}

// depthLimitValue is used ONLY at the depth/node-visit ceiling, where
// recursing further is exactly what must not happen. It still prefers a
// static, non-recursive source (default, const, first enum value) over
// synthesizing one, since none of those require walking anything. w is used
// to resolve one $ref hop for a REQUIRED property's own schema inside
// minimalLegalValue's object case (see there), and to pick a branch when
// schema is itself a composition node (see unwrapCompositionAtCeiling);
// every call site already has a *walker in scope, so threading it through
// costs nothing. dataPath seeds that branch pick so two different ceiling-
// hit oneOf fields don't all collapse onto the identical branch.
func depthLimitValue(w *walker, schema map[string]any, dataPath string) any {
	if schema == nil {
		return nil
	}
	// cloneDocValue: see leafValue — a document map must never reach a
	// writer by reference.
	if def, ok := schema["default"]; ok {
		return cloneDocValue(def)
	}
	if c, ok := schema["const"]; ok {
		return cloneDocValue(c)
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return cloneDocValue(enum[0])
	}
	schema = unwrapCompositionAtCeiling(w, schema, dataPath)
	return minimalLegalValue(w, schema)
}

// hardCeilingValue is walkObject/walkArray's own "smallest legal form"
// helper for the pre-generation/post-generation MaxBytes hard-ceiling
// checks (Body's own doc comment: "cost a required subtree before
// committing to it, or emit its depth-limit form when it will not fit") —
// depthLimitValue's twin, with TWO differences, both because the hard-
// ceiling checks here run on perfectly ordinary, shallow properties —
// nowhere near the depth/node ceiling — so neither of depthLimitValue's own
// shortcuts, justified THERE by "recursing further is exactly what must not
// happen", carries over:
//
//  1. allOf resolves through a full mergeAllOf rather than
//     unwrapCompositionAtCeiling's one-branch shortcut: an allOf's SECOND
//     branch can require properties the first branch never mentions (found
//     verifying this fix against the real corpus: Badge is
//     BadgePreview allOf'd with a second branch requiring
//     isBadgeGrantable/isSystem, neither of which BadgePreview's
//     own required list carries), and picking only the first branch would
//     silently drop them. mergeAllOf is the SAME merge composeValue itself
//     uses for the real, full walk (and shares its mergeCache memoization),
//     so this is not a second, diverging notion of what the schema
//     requires — just the same one, computed before recursing instead of
//     during it.
//  2. a required OBJECT property recurses into ITS OWN required
//     properties in turn — depth permitting — rather than bottoming out
//     after one shallow pass. minimalLegalValue's single-level
//     minimalRequiredObject is correct at the actual depth ceiling (a
//     schema deep enough to hit it is already a degraded, best-effort
//     case, and recursing further there would just reintroduce the
//     unbounded recursion that ceiling exists to stop) but wrong here: a
//     required property nested inside a required property is completely
//     ordinary, and stopping after one level silently drops THAT nested
//     object's own required fields — measured on the acceptance corpus:
//     GET /api/v1/invites/{inviteId} at MaxBytes=256 lost creator.id and
//     tenant.id/name, both required two levels down from the
//     response root, because the one-level floor materialized creator and
//     tenant as bare {}. depth/effMaxDepth bounds the recursion the
//     exact same way walkSchema's own ceiling check does (line ~217): once
//     a nested required property's OWN depth would exceed effMaxDepth,
//     this falls back to depthLimitValue for that property instead of
//     recursing further, so the result is never any MORE degraded than
//     what the real walk would already produce there — there is no new
//     unbounded case beyond what the real walk's depth ceiling already
//     handles safely, only a deeper (still terminating) walk on the
//     ordinary side of it.
//
// oneOf/anyOf need no such fix beyond the recursion itself: exactly one
// branch, in full, already satisfies either keyword, so
// unwrapCompositionAtCeiling's per-branch pick (shared with
// depthLimitValue) is already schema-correct here, not merely a degraded
// approximation — and it is applied at EVERY level of the recursion below,
// not just the top, so a nested required property that is itself a
// oneOf/anyOf gets the same treatment its parent did.
//
// depth is the depth at which schema's OWN value will sit once generated —
// the same childDepth a real walkNode call for this same schema would
// receive — so every caller passes exactly what it would pass to walkNode
// for this schema, never the depth of the OBJECT that contains it.
func hardCeilingValue(w *walker, schema map[string]any, dataPath string, depth int) any {
	if schema == nil {
		return nil
	}
	if w != nil && depth > w.opts.effMaxDepth() {
		// Same ceiling walkSchema itself enforces before ever looking at
		// the schema (line ~217): beyond it, the real walk never recurses
		// either, so this stops here too rather than materializing a
		// "required" recursion the real generation would never attempt at
		// this depth. depthLimitValue is exactly what the real walk would
		// emit in this spot.
		return depthLimitValue(w, schema, dataPath)
	}
	// cloneDocValue: see leafValue — a document map must never reach a
	// writer by reference.
	if def, ok := schema["default"]; ok {
		return cloneDocValue(def)
	}
	if c, ok := schema["const"]; ok {
		return cloneDocValue(c)
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return cloneDocValue(enum[0])
	}
	if raw, ok := schema["allOf"].([]any); ok && len(raw) > 0 && w != nil {
		if merged, err := w.mergeAllOf(schema, raw); err == nil {
			schema = merged
		}
		// Unsatisfiable or unresolvable allOf: fall through and treat
		// schema as-is, the same one-branch approximation the ceiling uses
		// rather than fail the whole property outright — a degraded-but-
		// present value beats none for a hard-ceiling fallback, and
		// ErrUnsatisfiable is still reachable through the ordinary walk for
		// the caller that actually wants to know the schema has no legal
		// value at all.
	}
	schema = unwrapCompositionAtCeiling(w, schema, dataPath)
	if SchemaType(schema) != "object" || w == nil {
		// w == nil: no resolver to chase $refs with and no Options to bound
		// recursion depth against (effMaxDepth lives on w.opts) — the same
		// single-level minimalRequiredObject the depth ceiling itself uses
		// is the safe fallback here, exactly as it always was before this
		// function recursed at all. Every real call site (walkObject,
		// walkArray) always has a live walker; only a hypothetical direct
		// unit-test call could ever hit this.
		return minimalLegalValue(w, schema)
	}

	// Object case: unlike minimalLegalValue's own one-level
	// minimalRequiredObject, recurse into every required property's own
	// required content, one depth deeper each time (see this function's
	// own doc comment, point 2). writeOnly is skipped here for the same
	// reason walkObject's real per-property loop skips it even when the
	// property is required (DESIGN calls writeOnly+required a spec bug;
	// omitting is the honest reading) — this fallback must never emit
	// something the real, unconstrained walk would not have emitted either.
	// This recursion is charged to the SAME node budget the real walk
	// spends (visitNode): it prices every required subtree, once per
	// required property of every object visited, and an all-required
	// self-typed schema made that a k^depth blow-up that maxWalkNodes
	// never saw, because only walkSchema counted nodes. Over the budget it
	// degrades to the one-level minimum, exactly as the real walk would.
	if w.budget != nil && !w.budget.visitNode() {
		return minimalLegalValue(w, schema)
	}
	return hardCeilingObject(w, schema, dataPath, depth)
}

// hardCeilingObject is hardCeilingValue's object case: every required,
// readable property priced one level deeper through hardCeilingValue.
func hardCeilingObject(w *walker, schema map[string]any, dataPath string, depth int) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for name := range stringSet(schema["required"]) {
		propNode, ok := props[name]
		if !ok {
			continue
		}
		var propSchema map[string]any
		if resolved, err := w.res.ResolveNode(propNode); err == nil {
			propSchema, _ = resolved.(map[string]any)
		}
		if isWriteOnly(propSchema) {
			continue
		}
		out[name] = hardCeilingValue(w, propSchema, joinPath(dataPath, name), depth+1)
	}
	return out
}

// unwrapCompositionAtCeiling resolves ONE branch of an allOf/oneOf/anyOf
// node caught at the depth/node ceiling — allOf's own first branch (a full
// intersection merge is exactly the recursive work the ceiling exists to
// avoid; one branch's own shape is close enough for a value this degraded
// already), oneOf/anyOf's branch picked the SAME deterministic,
// dataPath-hashed way branchOrder (compose.go) does for a full walk — so
// minimalLegalValue's SchemaType dispatch afterward sees the branch's OWN
// type/required list instead of the bare composition node's. Without
// this, SchemaType(schema) on a raw {"oneOf":[...]} node (no "type", no
// "properties", no "items" of its own) falls through to its untyped-leaf
// default, "string" — so ANY oneOf/allOf/anyOf-typed field unlucky enough
// to sit exactly at the ceiling generated a plain string filler instead
// of a value even loosely shaped like its schema, failing schema
// validation outright (found verifying finding 5/6 against the real
// corpus: TestAcceptance_SchemaConformance's remaining failures, all
// "'oneOf' failed... got string, want object" on
// TenantCohort.item, several hops below MaxDepth). A schema with no
// composition keyword, or one whose branch fails to resolve, is returned
// unchanged.
func unwrapCompositionAtCeiling(w *walker, schema map[string]any, dataPath string) map[string]any {
	if w == nil {
		return schema
	}
	if raw, ok := schema["allOf"].([]any); ok && len(raw) > 0 {
		if resolved, err := w.res.ResolveNode(raw[0]); err == nil {
			if m, ok := resolved.(map[string]any); ok {
				return m
			}
		}
		return schema
	}
	raw, _ := oneOfOrAnyOf(schema)
	if len(raw) == 0 {
		return schema
	}
	idx := int(SeedScalar(w.seedList, dataPath+"\x00ceilingbranch") % uint64(len(raw)))
	if resolved, err := w.res.ResolveNode(raw[idx]); err == nil {
		if m, ok := resolved.(map[string]any); ok {
			return m
		}
	}
	return schema
}

// minimalLegalValue produces the smallest value that satisfies schema's own
// type/bounds WITHOUT recursing into any nested schema BEYOND its own
// immediate required properties. Array nesting still bottoms out at []:
// minItems is a bare count, nothing to populate. Object nesting, before the
// P1b round-1 review's finding 5, bottomed out at bare {} the same way —
// but a REQUIRED object several hops below the depth ceiling (reached, say,
// via an array item's own walkSchema ceiling check, which — unlike
// walkObject's per-property ceiling branch — had no per-required-property
// handling of its own) then failed its OWN schema validation outright:
// {} does not satisfy `"required":["code","name"]`. minimalRequiredObject
// below fixes that with exactly one shallow, non-recursive pass over
// schema's own "required" list — deliberately NOT walking further into any
// of THOSE properties' own required lists in turn, which would just
// reintroduce the unbounded recursion this ceiling exists to stop.
func minimalLegalValue(w *walker, schema map[string]any) any {
	switch SchemaType(schema) {
	case "object":
		return minimalRequiredObject(w, schema)
	case "array":
		return []any{}
	default:
		return minimalScalarValue(schema)
	}
}

// minimalRequiredObject is minimalLegalValue's "object" case: a shallow
// object carrying just schema's own required properties, each resolved one
// $ref hop (mirroring every other property lookup in this package) and
// given its own minimal scalar/empty-composite value via minimalScalarValue
// — never recursing into THAT property's own required list in turn, which
// is exactly the bound that keeps this a single shallow pass rather than
// an unbounded one. w may be nil (some direct unit-test callers construct
// a schema with no walker at hand); an unresolvable or missing property
// schema falls back to minimalScalarValue's own nil-schema case.
func minimalRequiredObject(w *walker, schema map[string]any) map[string]any {
	out := map[string]any{}
	props, _ := schema["properties"].(map[string]any)
	for name := range stringSet(schema["required"]) {
		propNode, ok := props[name]
		if !ok {
			continue
		}
		var propSchema map[string]any
		if w != nil {
			if resolved, err := w.res.ResolveNode(propNode); err == nil {
				propSchema, _ = resolved.(map[string]any)
			}
		} else if m, ok := propNode.(map[string]any); ok {
			propSchema = m
		}
		out[name] = minimalScalarValue(propSchema)
	}
	return out
}

// minimalScalarValue is minimalLegalValue's own type/bounds logic for a
// schema found ONE level below the depth ceiling (a required object's own
// required property) — object/array cases here bottom out at {}/[] with no
// further nesting at all, unlike minimalLegalValue's own "object" case one
// level up, which gets exactly one such pass and no more. const/default/
// enum are checked FIRST, mirroring depthLimitValue's own priority: an
// enum-constrained required property (e.g. TenantCohort branches'
// shared "source": {"enum":[...]}, discovered verifying this exact fix
// against the real corpus) has no legal value in the type-based fallback
// below at all — "" or 0 satisfy the bare TYPE but not the enum, so a
// required field like this reaching the ceiling without this check went
// on to fail its own schema validation regardless of type-correctness.
func minimalScalarValue(schema map[string]any) any {
	if schema == nil {
		return ""
	}
	// cloneDocValue: see leafValue — a document map must never reach a
	// writer by reference.
	if c, ok := schema["const"]; ok {
		return cloneDocValue(c)
	}
	if def, ok := schema["default"]; ok {
		return cloneDocValue(def)
	}
	if enum, ok := schema["enum"].([]any); ok && len(enum) > 0 {
		return cloneDocValue(enum[0])
	}
	switch SchemaType(schema) {
	case "object":
		return map[string]any{}
	case "array":
		return []any{}
	case "boolean":
		return false
	case "integer":
		if v, ok := numFromSchema(schema, "minimum"); ok {
			return int64(math.Ceil(v))
		}
		return int64(0)
	case "number":
		if v, ok := numFromSchema(schema, "minimum"); ok {
			return v
		}
		return float64(0)
	default: // "string" and untyped
		lo, _, err := stringLengthBounds(schema)
		if err != nil || lo <= 0 {
			return ""
		}
		return strings.Repeat("x", lo)
	}
}

// --- schema-node introspection helpers --------------------------------

func numFromSchema(schema map[string]any, key string) (float64, bool) {
	v, ok := schema[key]
	if !ok {
		return 0, false
	}
	return toFloat64(v)
}

func toFloat64(v any) (float64, bool) {
	var f float64
	switch n := v.(type) {
	case jsonx.Number:
		var err error
		if f, err = n.Float64(); err != nil {
			return 0, false
		}
	case float64:
		f = n
	case int:
		f = float64(n)
	case int64:
		f = float64(n)
	default:
		return 0, false
	}
	// Clamped to the integers a float64 represents exactly: a schema
	// number is fed to int/int64/uint64 conversions all over this package
	// (bounds, lengths, item counts, multipleOf steps), and a float outside
	// the target's range converts to an implementation-defined value —
	// MinInt64 on amd64, a saturation on arm64 — so `maximum: 1e308` used
	// to give a span of +Inf and, on arm64, a modulo by zero. NaN and the
	// infinities are refused outright.
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	const exact = float64(1 << 53)
	return max(min(f, exact), -exact), true
}

// typeList reads schema's "type" in either its OAS 3.0 (single string) or
// OAS 3.1 / JSON Schema 2020-12 (union array) form.
func typeList(schema map[string]any) []string {
	switch t := schema["type"].(type) {
	case string:
		return []string{t}
	case []any:
		out := make([]string, 0, len(t))
		for _, v := range t {
			if s, ok := v.(string); ok {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// SchemaType returns schema's primary (non-"null") type, falling back to a
// structural guess from "properties"/"items"/"additionalProperties" when
// "type" itself is absent (common in loosely-specified real-world specs),
// and finally to "string" for a truly untyped leaf.
//
// Exported (P3a): resource derivation (internal/specs) reads a wrapper's
// count-property type at import time with this exact function, so that the
// type [CountValue] later shapes a stored total into at serve time is
// always the type this function would compute right now.
func SchemaType(schema map[string]any) string {
	for _, t := range typeList(schema) {
		if t != "null" {
			return t
		}
	}
	if _, ok := schema["properties"]; ok {
		return "object"
	}
	if _, ok := schema["items"]; ok {
		return "array"
	}
	if ap, ok := schema["additionalProperties"]; ok {
		if _, isMap := ap.(map[string]any); isMap {
			return "object"
		}
	}
	return "string"
}

// nullableInfo reports whether schema is FORCED null (its type is exactly
// ["null"] — nothing else could possibly satisfy it) and whether null is
// ALLOWED at all (type includes "null" alongside another type — DESIGN §9:
// "x-nullable — null только если type ровно [\"null\"] ... или в доле
// nullRate". A union like ["string","null"] is not forced null; it is
// eligible for the nullRate roll in walkSchema, same as any other
// generation choice).
func nullableInfo(schema map[string]any) (forced, allowed bool) {
	types := typeList(schema)
	if len(types) == 0 {
		return false, false
	}
	hasNull := false
	nonNull := 0
	for _, t := range types {
		if t == "null" {
			hasNull = true
		} else {
			nonNull++
		}
	}
	return hasNull && nonNull == 0, hasNull
}

func isWriteOnly(schema map[string]any) bool {
	if schema == nil {
		return false
	}
	v, ok := schema["writeOnly"].(bool)
	return ok && v
}

// hasComposition reports whether schema carries any of the composition
// keywords DESIGN §9 assigns to the Values agent's composeValue seam.
func hasComposition(schema map[string]any) bool {
	for _, k := range [...]string{"allOf", "oneOf", "anyOf", "discriminator"} {
		if v, ok := schema[k]; ok && v != nil {
			return true
		}
	}
	return false
}

func stringSet(v any) map[string]bool {
	arr, _ := v.([]any)
	out := make(map[string]bool, len(arr))
	for _, e := range arr {
		if s, ok := e.(string); ok {
			out[s] = true
		}
	}
	return out
}

// joinPath appends name to a dataPath, dot-separated — "user.profile" +
// "avatar_url" -> "user.profile.avatar_url". base == "" (the item/body
// root) yields the bare name, which is exactly the restart DESIGN §9
// requires for a list item's own field paths.
func joinPath(base, name string) string {
	if base == "" {
		return name
	}
	return base + "." + name
}

// recipePath is dataPath adjusted for recipe LOOKUP ONLY: outside a list
// item (recipePrefix == "", the overwhelming common case) it is dataPath
// unchanged, at zero cost. Inside a restarted list-item subtree it is
// recipePrefix+"."+dataPath — the item's own absolute path glued back onto
// its item-relative one — except at the item's own root (dataPath == ""),
// where gluing on a trailing "." would produce "items[3]." instead of the
// bare "items[3]" a recipe bound to the WHOLE item needs to match. Nothing
// but a recipe lookup may ever call this — see recipePrefix's own doc
// comment for why.
func (w *walker) recipePath(dataPath string) string {
	if w.recipePrefix == "" {
		return dataPath
	}
	if dataPath == "" {
		return w.recipePrefix
	}
	return w.recipePrefix + "." + dataPath
}

func estimateJSONSize(v any) int {
	if n, ok := jsonSize(v); ok {
		return n
	}
	b, err := jsonx.Marshal(v)
	if err != nil {
		return 0
	}
	return len(b)
}
