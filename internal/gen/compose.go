package gen

import (
	"errors"
	"fmt"
	"maps"
	"sort"
	"strings"
	"sync"

	"github.com/yashok111/mocker/internal/jsonx"
)

// composeValue implements DESIGN §9 "Композиция схем": allOf is an
// intersection of constraints, oneOf/anyOf pick one branch deterministically
// by hashing the data path, and discriminator picks a branch through
// mapping while always stamping the discriminator property itself.
//
// allOf is resolved FIRST and unconditionally: once its branches are merged
// into one plain schema, generation continues through w.walkSchema on that
// merged schema rather than through any of the helpers below — walkSchema's
// own hasComposition check re-enters composeValue automatically if (and
// only if) a branch carried its own nested oneOf/anyOf/discriminator, so
// this function never needs to juggle "allOf combined with oneOf" as a
// special case: it falls out of the normal recursive structure.
func composeValue(w *walker, schema map[string]any, dataPath string, depth int) (any, error) {
	if raw, ok := schema["allOf"].([]any); ok && len(raw) > 0 {
		merged, err := w.mergeAllOf(schema, raw)
		if err != nil {
			return nil, err
		}
		return w.walkSchema(merged, dataPath, depth)
	}
	if disc, ok := schema["discriminator"].(map[string]any); ok {
		return w.composeDiscriminator(schema, disc, dataPath, depth)
	}
	if raw, ok := schema["oneOf"].([]any); ok && len(raw) > 0 {
		return w.composeOneOf(schema, raw, dataPath, depth, true)
	}
	if raw, ok := schema["anyOf"].([]any); ok && len(raw) > 0 {
		return w.composeOneOf(schema, raw, dataPath, depth, false)
	}
	// hasComposition was true (schema.go's gate) but none of the keywords
	// above turned out to carry anything usable — an empty "allOf": [],
	// or a "discriminator" that isn't an object in a malformed spec. Fall
	// back to the schema's own base shape, same as the pre-composition
	// stub did for every node, rather than erroring on a spec quirk that
	// isn't actually meaningful composition.
	return w.structural(schema, dataPath, depth)
}

// --- allOf: intersection of constraints -------------------------------

// maxAllOfBranches bounds the flattening queue below so a pathological
// chain of allOf-of-allOf branches cannot spin the merge loop forever. The
// resolver's own $ref cycle/budget detection already guards any ONE $ref
// chain (schema.go/openapi already provably terminate there); this guards
// the separate case of a long chain of textually distinct allOf branches.
const maxAllOfBranches = 256

// maxMergeDepth bounds mergeSchemaPair's own recursion, which happens only
// when two DIFFERENT allOf/oneOf-sibling branches redeclare the SAME
// property key and their subschemas must be intersected too. Real schemas
// never come close to this; it exists purely so a pathological or
// self-referencing property collision degrades (by simply keeping the
// first branch's subschema, unnarrowed) instead of recursing without bound.
const maxMergeDepth = 32

// mergeAllOf intersects schema's own sibling keywords with every branch in
// raw — DESIGN §9: "required объединяются, properties сливаются, числовые
// и строковые границы сужаются... additionalProperties: false в любой
// ветке закрывает результат". Branches are resolved lazily, one $ref hop
// at a time, via the walker's resolver; a branch that itself carries a
// nested "allOf" is flattened into the same queue so multi-level allOf
// composes correctly, while any oneOf/anyOf/discriminator a branch carries
// survives the flattening (composeValue's caller re-dispatches on those
// once merging is done). The required+additionalProperties:false
// unsatisfiable case (DESIGN's second bullet) is checked once, after the
// full accumulation — checking it mid-merge would false-positive whenever
// one branch requires a property that only a LATER branch declares.
func (w *walker) mergeAllOf(schema map[string]any, raw []any) (map[string]any, error) {
	if key, ok := allOfCacheKey(schema); ok {
		if entry, found := w.loadMergeCache(key); found {
			return entry.result, entry.err
		}
		result, err := w.mergeAllOfUncached(schema, raw)
		w.storeMergeCache(key, result, err)
		return result, err
	}
	return w.mergeAllOfUncached(schema, raw)
}

// mergeAllOfUncached is mergeAllOf's actual computation, split out so the
// cache wrapper above can memoize it. See mergeAllOf's own doc comment for
// why the split is worth it, and the Generator/walker's mergeCache field
// for where the memo actually lives.
func (w *walker) mergeAllOfUncached(schema map[string]any, raw []any) (map[string]any, error) {
	acc := siblingSchema(schema)
	queue := append([]any{}, raw...)

	for visited := 0; len(queue) > 0; visited++ {
		if visited >= maxAllOfBranches {
			return nil, fmt.Errorf("%w: allOf branch limit exceeded", ErrUnsatisfiable)
		}
		node := queue[0]
		queue = queue[1:]

		resolved, err := w.res.ResolveNode(node)
		if err != nil {
			return nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
		}
		branch, err := asSchemaObject(resolved)
		if err != nil {
			return nil, err
		}
		if nested, ok := branch["allOf"].([]any); ok && len(nested) > 0 {
			queue = append(queue, nested...)
			branch = withoutKeys(branch, "allOf")
		}
		merged, err := w.mergeSchemaPair(acc, branch, 0)
		if err != nil {
			return nil, err
		}
		acc = merged
	}
	if err := validateRequiredAgainstClosed(acc); err != nil {
		return nil, err
	}
	return acc, nil
}

// mergeCache is the memo mergeAllOf reads/writes through — one per
// Generator (see gen.go), shared by every walker/request built over it, and
// keyed by allOfCacheKey (the composition node's own JSON content, not its
// dataPath or anything else request-scoped). mergeAllOf's result depends
// ONLY on the immutable document, never on the request being answered
// (confirmed: neither it nor anything it calls reads dataPath or
// seedList), so recomputing the full intersection (branch resolution, map
// allocation, bound narrowing) on every single request to an allOf-bearing
// operation is pure waste — the P1b round-1 review's finding 3, on the same
// hot path DESIGN §18 requires to survive a load test. sync.Map (not a
// plain map+mutex) matches how heavily this can be read concurrently
// across many simultaneous requests to the same operation, with at most
// occasional writes.
type mergeCache struct {
	m sync.Map // string (allOfCacheKey) -> mergeCacheEntry
}

type mergeCacheEntry struct {
	result map[string]any
	err    error
}

// allOfCacheKey is the composition node's own canonical JSON — sorted keys,
// since encoding/json always sorts map[string]any keys on Marshal — rather
// than the schema map's pointer/address identity. Content-addressing is
// slightly more work per call (one Marshal of the composition node itself,
// NOT the whole document) but is unconditionally SAFE: two schema.go call
// sites can and do hand mergeAllOf a schema map that is a document node
// reached two different ways (a shared $ref, or the SAME node reached
// twice in one walk), and content-addressing treats those identically
// without needing to reason about Go map/GC address-reuse guarantees the
// way a pointer-identity key would have to. A schema that fails to marshal
// (never happens for anything decoded from JSON in the first place) simply
// isn't cached — ok=false, mergeAllOf computes it directly every time,
// which is correct, just not sped up.
func allOfCacheKey(schema map[string]any) (string, bool) {
	b, err := jsonx.Marshal(schema)
	if err != nil {
		return "", false
	}
	return string(b), true
}

func (w *walker) loadMergeCache(key string) (mergeCacheEntry, bool) {
	if w.mergeCache == nil {
		return mergeCacheEntry{}, false
	}
	v, ok := w.mergeCache.m.Load(key)
	if !ok {
		return mergeCacheEntry{}, false
	}
	return v.(mergeCacheEntry), true
}

func (w *walker) storeMergeCache(key string, result map[string]any, err error) {
	if w.mergeCache == nil {
		return
	}
	w.mergeCache.m.Store(key, mergeCacheEntry{result: result, err: err})
}

// mergeHandledKeys are the keywords mergeSchemaPair gives dedicated
// narrowing/union logic to; every other key is copied through generically
// (a's value wins when both sides declare it, b fills gaps otherwise) by
// the trailing loop in mergeSchemaPair. Composition keywords (allOf/oneOf/
// anyOf/discriminator) are deliberately NOT in this set: mergeAllOf strips
// "allOf" itself before ever calling mergeSchemaPair, and a branch's own
// oneOf/anyOf/discriminator must pass through untouched so the caller's
// hasComposition check finds it afterward.
var mergeHandledKeys = map[string]bool{
	"required": true, "properties": true, "additionalProperties": true,
	"type":             true,
	"minimum":          true,
	"maximum":          true,
	"exclusiveMinimum": true,
	"exclusiveMaximum": true,
	"minLength":        true,
	"maxLength":        true,
	"minItems":         true,
	"maxItems":         true,
	"minProperties":    true,
	"maxProperties":    true,
	"enum":             true,
}

// mergeSchemaPair intersects two already-resolved schema objects — the
// single primitive every allOf/oneOf/discriminator merge in this file
// reduces to. depth bounds RECURSIVE merging that only happens when a
// property key collides between a and b (mergeProperties below); it is
// unrelated to the schema walk's own depth/dataPath.
//
// The w.budget.visitNode() check below bounds the TOTAL number of pairwise
// merges this call (and everything it recurses into) may perform, sharing
// the same counter walkSchema's own node-visit ceiling uses. maxMergeDepth
// alone only bounds RECURSION DEPTH, not the WORK done at each level — a
// self-referencing schema under allOf, with as few as two colliding
// property keys per level, fans out multiplicatively (2^maxMergeDepth
// mergeSchemaPair calls, each allocating its own map) entirely within that
// depth bound, hanging the request rather than erroring it (the P1b
// round-1 review's finding 8, reproduced end-to-end through gen.Body: no
// response, no error, for as long as the process keeps running). Tripping
// this ceiling degrades to ErrUnsatisfiable, exactly like every other
// budget/depth ceiling in this package — a slow, bounded "no" beats an
// unbounded hang.
func (w *walker) mergeSchemaPair(a, b map[string]any, depth int) (map[string]any, error) {
	if depth > maxMergeDepth {
		return a, nil
	}
	if !w.budget.visitNode() {
		return nil, fmt.Errorf("%w: allOf merge work limit exceeded", ErrUnsatisfiable)
	}
	out := make(map[string]any, len(a)+len(b))
	maps.Copy(out, a)

	if req := unionRequired(a, b); req != nil {
		out["required"] = req
	} else {
		delete(out, "required")
	}

	props, err := w.mergeProperties(a["properties"], b["properties"], depth+1)
	if err != nil {
		return nil, err
	}
	if props != nil {
		out["properties"] = props
	}

	if ap := mergeAdditionalProperties(a, b); ap != nil {
		out["additionalProperties"] = ap
	}

	t, err := mergeType(a["type"], b["type"])
	if err != nil {
		return nil, err
	}
	if t != nil {
		out["type"] = t
	} else {
		delete(out, "type")
	}

	narrowLower(out, a, b, "minimum")
	narrowUpper(out, a, b, "maximum")
	narrowLower(out, a, b, "exclusiveMinimum")
	narrowUpper(out, a, b, "exclusiveMaximum")
	narrowLower(out, a, b, "minLength")
	narrowUpper(out, a, b, "maxLength")
	narrowLower(out, a, b, "minItems")
	narrowUpper(out, a, b, "maxItems")
	narrowLower(out, a, b, "minProperties")
	narrowUpper(out, a, b, "maxProperties")

	en, err := mergeEnum(a["enum"], b["enum"])
	if err != nil {
		return nil, err
	}
	if en != nil {
		out["enum"] = en
	}

	for k, v := range b {
		if mergeHandledKeys[k] {
			continue
		}
		if _, present := out[k]; !present {
			out[k] = v
		}
	}

	if err := validateBounds(out); err != nil {
		return nil, err
	}
	return out, nil
}

// mergeProperties unions a's and b's property-name sets; a key declared by
// only one side passes through unchanged, a key declared by BOTH is
// intersected too (mergePropertyPair) — DESIGN's "properties сливаются" is
// not just a key-set union when two branches redeclare the same field.
func (w *walker) mergeProperties(ap, bp any, depth int) (map[string]any, error) {
	am, _ := ap.(map[string]any)
	bm, _ := bp.(map[string]any)
	if len(am) == 0 && len(bm) == 0 {
		return nil, nil
	}
	out := make(map[string]any, len(am)+len(bm))
	maps.Copy(out, am)
	for k, bv := range bm {
		av, exists := out[k]
		if !exists {
			out[k] = bv
			continue
		}
		merged, err := w.mergePropertyPair(av, bv, depth)
		if err != nil {
			return nil, err
		}
		out[k] = merged
	}
	return out, nil
}

// mergePropertyPair intersects two property subschemas declared for the
// SAME key by two different branches. Each side is resolved through the
// walker's resolver first (one hop, on demand) since a redeclared property
// is commonly a bare $ref on one or both sides — this is the only place in
// composition merging that needs to look INSIDE a possibly-$ref'd node
// before generation time, because narrowing bounds requires reading both
// sides' actual keywords, not just picking one blindly.
func (w *walker) mergePropertyPair(av, bv any, depth int) (any, error) {
	aResolved, err := w.res.ResolveNode(av)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
	}
	bResolved, err := w.res.ResolveNode(bv)
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
	}
	aSchema, aErr := asSchemaObject(aResolved)
	bSchema, bErr := asSchemaObject(bResolved)
	if aErr != nil || bErr != nil {
		if errors.Is(aErr, ErrUnsatisfiable) || errors.Is(bErr, ErrUnsatisfiable) {
			return nil, ErrUnsatisfiable
		}
		// Not a schema object on one side (malformed spec): fall back to
		// the later branch's raw value rather than failing the whole
		// composition over one odd property.
		return bv, nil
	}
	return w.mergeSchemaPair(aSchema, bSchema, depth)
}

// unionRequired is DESIGN's "required объединяются": the union of a's and
// b's required-property-name sets, deduplicated and sorted for a
// deterministic (if incidental — "required" is a validation keyword, never
// itself serialized into a generated body) merge result. Returns nil when
// neither side declares any required properties, so the caller can tell
// "no required list" apart from "empty required list" and skip the key
// entirely rather than writing required: [].
func unionRequired(a, b map[string]any) []any {
	set := stringSet(a["required"])
	for k := range stringSet(b["required"]) {
		set[k] = true
	}
	if len(set) == 0 {
		return nil
	}
	names := make([]string, 0, len(set))
	for k := range set {
		names = append(names, k)
	}
	sort.Strings(names)
	out := make([]any, len(names))
	for i, n := range names {
		out[i] = n
	}
	return out
}

// mergeAdditionalProperties is DESIGN's "additionalProperties: false в
// любой ветке закрывает результат": false on EITHER side wins outright,
// regardless of what the other side says. Absent on both sides returns nil
// (caller must not write the key at all — an explicit `nil` would be
// indistinguishable from an explicit `"additionalProperties": null`).
func mergeAdditionalProperties(a, b map[string]any) any {
	av, aHas := a["additionalProperties"]
	bv, bHas := b["additionalProperties"]
	if isClosedAP(av) || isClosedAP(bv) {
		return false
	}
	if aHas {
		return av
	}
	if bHas {
		return bv
	}
	return nil
}

func isClosedAP(v any) bool {
	b, ok := v.(bool)
	return ok && !b
}

// mergeType intersects two "type" declarations (each in either the OAS 3.0
// single-string or OAS 3.1/JSON-Schema-2020-12 union-array form, via
// schema.go's own typeList). "null" is treated specially: it survives the
// intersection only when BOTH sides allow it — a value can be null only if
// every allOf branch tolerates null, exactly like any other type. An empty
// intersection of the non-null types (e.g. "string" allOf "integer") is
// the classic unsatisfiable allOf and returns ErrUnsatisfiable.
//
// A side whose type is EXACTLY "null" (aNon/bNon empty because aNull/bNull
// is true — as opposed to no type declared at all, already handled above by
// the len(at)==0/len(bt)==0 returns) admits no non-null type at all: the
// non-null intersection block below only ever runs when BOTH sides have a
// non-null type to contribute, so a null-only side correctly contributes
// nothing there rather than letting the OTHER side's full non-null type
// list pass through unconstrained (the P1b round-1 review's finding 1,
// case 1: normalizeNullable's own ["null"] allOf ["string"] output used to
// come back as bare "string", silently dropping the null-only branch's
// constraint entirely). The aNull&&bNull check runs regardless of whether
// the non-null block ran, so two nullable-but-type-disjoint branches (e.g.
// ["string","null"] allOf ["integer","null"]) correctly narrow to "null" —
// the only type both sides actually admit — instead of hitting the
// non-null intersection's own empty-result ErrUnsatisfiable BEFORE null
// ever gets a chance to save the merge (finding 1, case 2).
func mergeType(av, bv any) (any, error) {
	at := typeList(map[string]any{"type": av})
	bt := typeList(map[string]any{"type": bv})
	if len(at) == 0 {
		return bv, nil
	}
	if len(bt) == 0 {
		return av, nil
	}

	aNull, aNon := splitNullType(at)
	bNull, bNon := splitNullType(bt)

	var inter []string
	if len(aNon) > 0 && len(bNon) > 0 {
		bset := make(map[string]bool, len(bNon))
		for _, t := range bNon {
			bset[t] = true
		}
		for _, t := range aNon {
			if bset[t] {
				inter = append(inter, t)
			}
		}
	}
	if aNull && bNull {
		inter = append(inter, "null")
	}
	if len(inter) == 0 {
		return nil, ErrUnsatisfiable
	}

	if len(inter) == 1 {
		return inter[0], nil
	}
	out := make([]any, len(inter))
	for i, t := range inter {
		out[i] = t
	}
	return out, nil
}

func splitNullType(types []string) (hasNull bool, rest []string) {
	rest = make([]string, 0, len(types))
	for _, t := range types {
		if t == "null" {
			hasNull = true
		} else {
			rest = append(rest, t)
		}
	}
	return hasNull, rest
}

// narrowLower sets out[key] to the MORE RESTRICTIVE (larger) of a[key] and
// b[key] — DESIGN's "minimum = max из веток". Used for minimum,
// exclusiveMinimum, minLength, minItems, minProperties: every one of these
// keywords means "at least this much", so intersecting two branches keeps
// whichever demands more.
func narrowLower(out, a, b map[string]any, key string) {
	av, aok := numFromSchema(a, key)
	bv, bok := numFromSchema(b, key)
	switch {
	case aok && bok:
		if bv > av {
			out[key] = bv
		} else {
			out[key] = av
		}
	case bok:
		out[key] = bv
	case aok:
		out[key] = av
	}
}

// narrowUpper sets out[key] to the MORE RESTRICTIVE (smaller) of a[key] and
// b[key] — DESIGN's "maximum = min из веток". Used for maximum,
// exclusiveMaximum, maxLength, maxItems, maxProperties: an "at most this
// much" keyword narrows to whichever branch demands less.
func narrowUpper(out, a, b map[string]any, key string) {
	av, aok := numFromSchema(a, key)
	bv, bok := numFromSchema(b, key)
	switch {
	case aok && bok:
		if bv < av {
			out[key] = bv
		} else {
			out[key] = av
		}
	case bok:
		out[key] = bv
	case aok:
		out[key] = av
	}
}

// mergeEnum intersects two enum lists (a value must be a member of BOTH to
// survive an allOf) and returns ErrUnsatisfiable when that intersection is
// empty. A side that declares no enum at all imposes no constraint, so the
// other side's list (or nil, if neither declares one) passes through.
func mergeEnum(a, b any) ([]any, error) {
	ae, aok := a.([]any)
	be, bok := b.([]any)
	switch {
	case aok && bok:
		var inter []any
		for _, v := range ae {
			if enumContains(be, v) {
				inter = append(inter, v)
			}
		}
		if len(inter) == 0 {
			return nil, ErrUnsatisfiable
		}
		return inter, nil
	case aok:
		return ae, nil
	case bok:
		return be, nil
	default:
		return nil, nil
	}
}

// validateBounds is DESIGN's explicit unsatisfiable-composition example
// made concrete: "minimum: 10" + "maximum: 5" (or the same shape for
// length/item/property-count bounds) must not quietly generate a value
// anyway. Checked once per pairwise merge — narrowing is monotonic, so
// catching the violation at the exact pair that causes it is equivalent to
// (but earlier than) checking only once at the very end.
func validateBounds(schema map[string]any) error {
	pairs := [...][2]string{
		{"minimum", "maximum"},
		{"minLength", "maxLength"},
		{"minItems", "maxItems"},
		{"minProperties", "maxProperties"},
	}
	for _, p := range pairs {
		lo, hasLo := numFromSchema(schema, p[0])
		hi, hasHi := numFromSchema(schema, p[1])
		if hasLo && hasHi && lo > hi {
			return ErrUnsatisfiable
		}
	}
	return nil
}

// validateRequiredAgainstClosed is DESIGN/the task's second unsatisfiable
// example: a required property that additionalProperties:false also
// forbids (because no branch ever declared it under "properties") can
// never be satisfied — closing the object and demanding a property it
// doesn't allow is a contradiction, not a value this package should
// silently paper over.
func validateRequiredAgainstClosed(schema map[string]any) error {
	closed, ok := schema["additionalProperties"].(bool)
	if !ok || closed {
		return nil
	}
	props, _ := schema["properties"].(map[string]any)
	for name := range stringSet(schema["required"]) {
		if _, declared := props[name]; !declared {
			return ErrUnsatisfiable
		}
	}
	return nil
}

// --- oneOf / anyOf / discriminator: branch selection --------------------

// composeOneOf is DESIGN's oneOf/anyOf handling: the branch is picked
// deterministically by hashing dataPath (branchOrder), generated through
// the ordinary walk pipeline merged with the composition node's own
// sibling keywords (generateBranch), then checked against the branch it
// came from and — for oneOf, which demands satisfying EXACTLY one — every
// OTHER branch too. A miss advances to the next branch in hash order
// (bounded: at most len(raw) attempts, never an unbounded loop); if none
// ever satisfies the check, the LAST value generated is returned anyway —
// DESIGN calls for a bounded retry, then giving up honestly, not silently
// dropping the response. The retry loop itself is selectBranch's, shared
// with composeDiscriminator below; oneOf/anyOf needs nothing stamped onto
// an attempt, so it passes a nil stamp.
func (w *walker) composeOneOf(schema map[string]any, raw []any, dataPath string, depth int, exclusive bool) (any, error) {
	sibling := siblingSchema(schema)
	return w.selectBranch(sibling, raw, dataPath, depth, exclusive, nil)
}

// composeDiscriminator is DESIGN's discriminator handling: the branch is
// still picked from the oneOf/anyOf list, but by mapping (falling back to
// the referenced schema's own component name when mapping has no entry for
// it) rather than purely by hash — and, unconditionally, the discriminator
// property is stamped onto the result with the value that selected the
// branch. §9: "без него zod/io-ts отвергает всё тело" — a discriminated
// body missing its own discriminator is treated as a correctness bug here,
// not a cosmetic one, so it is set even when the exactness check below
// never manages to confirm exactly-one-branch conformance: selectBranch's
// stamp callback runs on EVERY attempt, not just the one finally returned.
func (w *walker) composeDiscriminator(schema, disc map[string]any, dataPath string, depth int) (any, error) {
	propName, _ := disc["propertyName"].(string)
	raw, exclusive := oneOfOrAnyOf(schema)
	if len(raw) == 0 {
		// A discriminator with nothing to branch over: nothing was
		// actually selected, so there is no value to stamp it with either.
		// Behave like a plain (non-discriminated) node.
		return w.structural(withoutKeys(schema, "discriminator"), dataPath, depth)
	}
	mapping, _ := disc["mapping"].(map[string]any)
	sibling := siblingSchema(schema)
	stamp := func(val any, idx int) {
		setDiscriminatorProp(val, propName, discriminatorValueFor(refOf(raw[idx]), mapping))
	}
	return w.selectBranch(sibling, raw, dataPath, depth, exclusive, stamp)
}

// selectBranch is the deterministic, bounded-retry branch-selection loop
// composeOneOf and composeDiscriminator both need and used to each
// implement by hand (the P1b round-1 review's finding 4: the two copies
// differed only in whether a discriminator property got stamped, an
// avoidable duplication of DESIGN's own retry/fallback contract). stamp,
// when non-nil, is called on EVERY branch attempted, before either
// conformance check — composeDiscriminator's only reason to need its own
// copy of this loop in the first place.
//
// Once the shared byte budget is ALREADY exhausted, the exclusivity check
// (matchesAnyOtherBranch) is skipped: a full generateBranch attempt per
// candidate is real, non-trivial work (finding 7), and for a realistic,
// non-disjoint oneOf — several branches sharing most of their required
// fields, only the MOST RESTRICTIVE one ever passing matchesAnyOtherBranch
// (the real corpus's TenantCohort.item: Cohort's generated value
// always ALSO structurally matches SpoGroup/AdministrativeGroup, since
// neither declares additionalProperties:false) — paying for every branch
// in order just to end up honest-but-broke is exactly the "keep
// generating past a depleted budget" finding 7 describes, just reached
// through composition instead of a plain object/array. A branch that at
// least satisfies its OWN schema is accepted immediately once there is
// nothing left to spend on finding a cleaner disjoint match.
func (w *walker) selectBranch(sibling map[string]any, raw []any, dataPath string, depth int, exclusive bool, stamp func(val any, idx int)) (any, error) {
	order := branchOrder(w.seedList, dataPath, len(raw))

	var lastVal any
	var lastErr error
	for _, idx := range order {
		val, branchSchema, err := w.generateBranch(sibling, raw[idx], dataPath, depth)
		if err != nil {
			lastErr = err
			continue
		}
		if stamp != nil {
			stamp(val, idx)
		}
		lastVal, lastErr = val, nil

		if !conformsSchema(w, val, branchSchema, 0) {
			continue
		}
		tightBudget := w.budget != nil && w.budget.remaining <= 0
		if exclusive && !tightBudget && matchesAnyOtherBranch(w, val, raw, idx) {
			continue
		}
		return val, nil
	}
	if lastVal == nil && lastErr != nil {
		return nil, lastErr
	}
	return lastVal, nil
}

// generateBranch resolves one oneOf/anyOf branch, merges it with the
// composition node's own sibling keywords (properties declared ALONGSIDE
// oneOf apply no matter which branch is chosen — standard JSON Schema
// semantics), and walks the merged result through w.walkSchema — the SAME
// path every other node in this package takes, so a chosen branch's own
// nested composition, budgets, depth, and nullability all go through
// identical machinery rather than a side door around any of them.
func (w *walker) generateBranch(sibling map[string]any, branchNode any, dataPath string, depth int) (any, map[string]any, error) {
	resolved, err := w.res.ResolveNode(branchNode)
	if err != nil {
		return nil, nil, fmt.Errorf("%w: %w", ErrNoSchema, err)
	}
	branchSchema, err := asSchemaObject(resolved)
	if err != nil {
		return nil, nil, err
	}
	combined, err := w.mergeSchemaPair(sibling, branchSchema, 0)
	if err != nil {
		return nil, nil, err
	}
	if err := validateRequiredAgainstClosed(combined); err != nil {
		return nil, nil, err
	}
	// The merged branch is walked at the SAME depth (a oneOf hop is not a
	// level of the value), which is correct for output but means a branch
	// that refers back to its own composition node (A: {oneOf: [{$ref:
	// A}]}) recurses with nothing but maxWalkNodes to stop it — about
	// 10^5 frames deep, each with fresh merged maps, before the node
	// ceiling trips. compHops counts composition hops along the current
	// path; past maxCompositionHops the branch degrades to the same
	// ceiling value the depth limit gives, without touching depth.
	if w.compHops >= maxCompositionHops {
		return depthLimitValue(w, combined, dataPath), branchSchema, nil
	}
	w.compHops++
	val, err := w.walkSchema(combined, dataPath, depth)
	w.compHops--
	if err != nil {
		return nil, nil, err
	}
	return val, branchSchema, nil
}

// maxCompositionHops bounds nested oneOf/anyOf/discriminator branch hops
// along one path (see generateBranch); allOf is bounded separately by
// maxAllOfBranches. 32 is far beyond any real document's nesting and far
// below the stack a cycle would otherwise grow.
const maxCompositionHops = 32

// branchOrder is the deterministic, bounded-retry visiting order over n
// branches: hash dataPath (suffixed so it can never collide with a real
// data path, same trick schema.go's rollNull uses for its own coin flip)
// to pick a starting branch, then walk the rest in a fixed rotation so
// every branch is tried at most once.
func branchOrder(seedList uint64, dataPath string, n int) []int {
	if n <= 0 {
		return nil
	}
	start := int(SeedScalar(seedList, dataPath+"\x00branch") % uint64(n))
	order := make([]int, n)
	for i := range order {
		order[i] = (start + i) % n
	}
	return order
}

// matchesAnyOtherBranch reports whether val also structurally conforms to
// any branch OTHER than chosenIdx — the check oneOf's "exactly one" needs
// on top of "conforms to the chosen branch". A branch that fails to
// resolve is skipped rather than treated as a match: an unreachable branch
// cannot be the thing making the selection ambiguous.
func matchesAnyOtherBranch(w *walker, val any, raw []any, chosenIdx int) bool {
	for i, node := range raw {
		if i == chosenIdx {
			continue
		}
		resolved, err := w.res.ResolveNode(node)
		if err != nil {
			continue
		}
		schema, err := asSchemaObject(resolved)
		if err != nil {
			continue
		}
		if conformsSchema(w, val, schema, 0) {
			return true
		}
	}
	return false
}

// oneOfOrAnyOf returns whichever of the two a schema declares (oneOf takes
// priority — a schema legitimately declaring both is unusual, and oneOf's
// stricter exactly-one semantics is the safer one to honor), plus whether
// the caller must enforce EXCLUSIVE (oneOf) selection.
func oneOfOrAnyOf(schema map[string]any) (raw []any, exclusive bool) {
	if arr, ok := schema["oneOf"].([]any); ok && len(arr) > 0 {
		return arr, true
	}
	if arr, ok := schema["anyOf"].([]any); ok && len(arr) > 0 {
		return arr, false
	}
	return nil, false
}

// discriminatorValueFor is DESIGN's "ветка по mapping": look up ref among
// mapping's entries (sorted for deterministic tie-breaking, though a
// well-formed mapping never maps two names to the same ref) and return the
// mapping KEY that points at it; when mapping has no entry for ref at all
// (legal per OAS — mapping may cover only SOME branches), fall back to the
// referenced schema's own component name.
func discriminatorValueFor(ref string, mapping map[string]any) string {
	if len(mapping) > 0 {
		names := make([]string, 0, len(mapping))
		for k := range mapping {
			names = append(names, k)
		}
		sort.Strings(names)
		for _, k := range names {
			if v, ok := mapping[k].(string); ok && v == ref {
				return k
			}
		}
	}
	return schemaNameFromRef(ref)
}

// schemaNameFromRef takes the last path segment of a JSON pointer $ref
// ("#/components/schemas/Cat" -> "Cat"), the fallback DESIGN calls for
// when mapping is absent or doesn't cover this branch. A ref that isn't
// pointer-shaped (or an inline, non-$ref branch, ref == "") is returned
// as-is — still a stable, deterministic string, just not a component name.
func schemaNameFromRef(ref string) string {
	idx := strings.LastIndex(ref, "/")
	if idx < 0 || idx == len(ref)-1 {
		return ref
	}
	return ref[idx+1:]
}

// refOf extracts branchNode's own "$ref" string, or "" when it isn't a
// $ref (an inline branch schema).
func refOf(branchNode any) string {
	m, ok := branchNode.(map[string]any)
	if !ok {
		return ""
	}
	ref, _ := m["$ref"].(string)
	return ref
}

// setDiscriminatorProp stamps the discriminator's own property onto val —
// unconditionally, per DESIGN, whenever val is an object and propName is
// known. Anything else (val generated as a non-object, or a spec that
// left propertyName blank) is left alone rather than corrupting val into
// a different shape than its schema describes.
func setDiscriminatorProp(val any, propName, value string) {
	if propName == "" {
		return
	}
	if obj, ok := val.(map[string]any); ok {
		obj[propName] = value
	}
}

// --- lightweight structural conformance ---------------------------------

// conformsSchema is a deliberately LIGHTWEIGHT (not a full JSON Schema
// validator — out of scope for a leaf generator package) structural check:
// type, required-properties-present, additionalProperties:false's
// extra-key rule, enum/const membership, and array length bounds. It is
// enough to distinguish the disjoint-union-shaped oneOf/anyOf schemas real
// APIs actually write (DESIGN's own oneOf count is 1 in the acceptance
// document) without attempting full recursive validation — a branch with
// its own nested composition is treated as conforming (returns true)
// rather than guessed at, since re-validating composition with a
// non-validator is exactly the kind of half-correct guess that is worse
// than admitting the limit.
func conformsSchema(w *walker, value any, schema map[string]any, depth int) bool { //nolint:gocyclo // one arm per JSON Schema keyword
	if depth > 16 {
		return true
	}
	if value == nil {
		forced, allowed := nullableInfo(schema)
		return forced || allowed || len(typeList(schema)) == 0
	}
	if hasComposition(schema) {
		return true
	}
	if enumRaw, ok := schema["enum"].([]any); ok {
		return enumContains(enumRaw, value)
	}
	if c, ok := schema["const"]; ok {
		return valuesEqual(c, value)
	}

	want := SchemaType(schema)
	if !typeMatches(want, value) {
		return false
	}
	switch want {
	case "object":
		obj := value.(map[string]any)
		for name := range stringSet(schema["required"]) {
			if _, present := obj[name]; !present {
				return false
			}
		}
		if closed, ok := schema["additionalProperties"].(bool); ok && !closed {
			props, _ := schema["properties"].(map[string]any)
			for k := range obj {
				if _, declared := props[k]; !declared {
					return false
				}
			}
		}
	case "array":
		arr := value.([]any)
		if n, has := numFromSchema(schema, "minItems"); has && float64(len(arr)) < n {
			return false
		}
		if n, has := numFromSchema(schema, "maxItems"); has && float64(len(arr)) > n {
			return false
		}
	}
	return true
}

func typeMatches(want string, value any) bool {
	switch want {
	case "object":
		_, ok := value.(map[string]any)
		return ok
	case "array":
		_, ok := value.([]any)
		return ok
	case "string":
		_, ok := value.(string)
		return ok
	case "boolean":
		_, ok := value.(bool)
		return ok
	case "integer":
		_, ok := value.(int64)
		return ok
	case "number":
		switch value.(type) {
		case int64, float64:
			return true
		}
		return false
	default:
		return true
	}
}

func enumContains(enum []any, v any) bool {
	for _, e := range enum {
		if valuesEqual(e, v) {
			return true
		}
	}
	return false
}

// valuesEqual compares two decoded-JSON values for equality by marshaling
// both back to JSON and comparing bytes — sidesteps the cross-type numeric
// noise (jsonx.Number vs float64 vs int64 all marshal to the same decimal
// text for an integral value) that a naive `==` or type-switch comparison
// would have to handle by hand.
func valuesEqual(a, b any) bool {
	// Strings, bools and null can only equal a value of the same kind, and
	// those are what an enum usually holds — compared directly, so an
	// enum membership check (once per member, per attempt, per item) does
	// not marshal both operands each time. Numbers keep the marshal: the
	// equality this function defines for them is textual (1 and 1.0 are
	// NOT equal), and only the encoder knows the text.
	switch x := a.(type) {
	case string:
		if y, ok := b.(string); ok {
			return x == y
		}
	case bool:
		if y, ok := b.(bool); ok {
			return x == y
		}
	case nil:
		return b == nil
	}
	ab, aerr := jsonx.Marshal(a)
	bb, berr := jsonx.Marshal(b)
	return aerr == nil && berr == nil && string(ab) == string(bb)
}

// --- shared schema-map helpers -------------------------------------------

// withoutKeys returns a shallow copy of schema with keys omitted.
func withoutKeys(schema map[string]any, keys ...string) map[string]any {
	skip := make(map[string]bool, len(keys))
	for _, k := range keys {
		skip[k] = true
	}
	out := make(map[string]any, len(schema))
	for k, v := range schema {
		if skip[k] {
			continue
		}
		out[k] = v
	}
	return out
}

// siblingSchema strips all four composition keywords, leaving whatever
// ELSE a composition node declared alongside them (properties, required,
// description, ...) — the "outer" schema that combines with whichever
// branch mergeAllOf/composeOneOf/composeDiscriminator picks.
func siblingSchema(schema map[string]any) map[string]any {
	return withoutKeys(schema, "allOf", "oneOf", "anyOf", "discriminator")
}
