// derive.go implements P3a's resource derivation (DESIGN §11, decisions.md
// §D3): the pure predicate that decides, from an already-Indexed OpenAPI
// document, which route families are eligible for the "confirm this as a
// resource" screen at all. It never touches a database and never fails —
// a family that does not qualify is simply absent from the returned slice,
// the same "degrade, don't abort" contract [Index] itself keeps.
package specs

import (
	"cmp"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/router"
)

// Suggestion is one derived candidate — everything [Repo.Import] needs to
// write a resource_suggestions row, minus spec_id/gen (the caller's own
// bookkeeping, not a fact derivation computes) and confidence (always the
// constant 1.0 per decisions.md §D3, so there is nothing here to carry).
type Suggestion struct {
	// RouteFamily is the collection route's canonical path, relative —
	// resource_suggestions.route_family and, unchanged, resources.route_family
	// once confirmed.
	RouteFamily string
	// Name is the family's last path segment, display only — never a
	// lookup key (paths change across reimports).
	Name string
	// IDField is the DATA property name a stored entity's identity lives
	// under — reconciled from the detail route's path-parameter name by
	// [R4] in decisions.md §D3, which is NOT always the same string.
	IDField string
	// EntitySchema is the resolved JSON pointer to the ITEM schema — the
	// wrapper -> array -> item walk's final anchor, re-anchored at every
	// $ref hop (R5).
	EntitySchema string
	// Wrapper is never nil for a Suggestion this package returns: the two
	// TYPES it carries (R39) have no other column to live in.
	Wrapper Wrapper
}

// Wrapper is resource_suggestions.wrapper / resources.wrapper's decoded
// shape (R6, R39): exactly the four keys the serving path reads at write
// time so it never needs a resolver of its own. ArrayKey and CountKey are
// pointers because "there is no such property" (a bare-array collection, or
// a wrapper that declares no accepted count name) is a real, distinct value
// from "the property is named \"\"" — nil marshals to JSON null, which is
// what decisions.md §D3 asks for verbatim ("a bare-array 200 is expressed
// by \"arrayKey\": null and \"countKey\": null, not by a NULL column").
type Wrapper struct {
	// ArrayKey is the wrapper object's single array-typed property name, or
	// nil for a top-level (bare) array 200.
	ArrayKey *string `json:"arrayKey"`
	// CountKey is the wrapper's single [gen.IsTotalFieldName]-accepted
	// property, or nil when none is declared (including: always nil for a
	// bare array, which has no wrapper object to carry one).
	CountKey *string `json:"countKey"`
	// CountType is [gen.SchemaType] of CountKey's resolved schema, or "" —
	// exactly the value [gen.CountValue] must be given later to reproduce
	// the SAME wire shape the generator already serves that property as.
	CountType string `json:"countType"`
	// IDType is [gen.PrimaryIDType] of the DETAIL variant's id property,
	// resolved once here so the serving path ([gen.CoerceIDValue]) never
	// needs a resolver of its own.
	IDType string `json:"idType"`
}

// maxNestingDepth is the ceiling on a family's "{}" segment count, enforced
// in exactly ONE place — this loop — and nowhere else (decisions.md §D3.1,
// R1): a family deeper than this is never suggested, so it is never
// confirmed, so nothing downstream ever sees one. Three is the smallest
// ceiling at which the pass loop below is a loop rather than a single
// hard-coded special case run once (§D3.1's own argument for "why three and
// not two").
const maxNestingDepth = 3

// deriveSuggestions is the whole of P3a's derivation predicate (R2-R7):
// pure over ops/resp — the exact pair [Index] returns — and res, the same
// resolver Index was indexed with. It is called from two places, and only
// two: [Repo.Import] (between Index and the write transaction) and the
// lazy backfill (over inputs it rebuilds from a spec already on disk) —
// see [Repo.EnsureSuggestions]. There is deliberately only one copy of
// this logic (R33): a second, driftable implementation living in
// internal/resources would need to import internal/specs while Import
// calls back into internal/resources — an import cycle decisions.md §D3
// names explicitly as the reason this stays here.
//
// Every route family recognized here separately satisfies the family
// predicate [router.ListFamily] already gives DESIGN §11's mock plane
// (R2's first half); this function's own addition is R2's second half, and
// since P3g (D4.1) that addition NARROWS rather than excludes outright: a
// family is derived when its "{}" segment count k sits in [0,
// maxNestingDepth] and — for k > 0 — the family [router.ParentFamily]
// returns for it was itself derived at depth k-1, i.e. by THIS SAME PASS's
// predecessor rather than by any earlier pass (D4.1: the stricter
// "derived by pass k-1 specifically" and the looser "derived by some
// earlier pass" agree in practice, because ParentFamily always removes
// exactly one "{}" segment, but the stricter form is what makes each pass's
// own invariant — "this pass's output is exactly the depth-k families whose
// whole chain derived" — checkable on its own). A family whose depth exceeds
// maxNestingDepth is accepted by no pass and so stays excluded (D3.1), and so
// does a depth-k family whose apparent parent is not itself a derivable
// family at depth k-1 — a scope layer keyed on the family alone could not
// tell two different parents' rows apart if the parent itself were never
// confirmable as a resource.
//
// This needs a BOUNDED LOOP over the same ops, not recursion: pass 0 derives
// every family with no "{}" segment, and pass k (1..maxNestingDepth) derives
// the depth-k families whose parent pass k-1 derived. No recursion is needed
// and none is written, because each pass strictly increases the "{}" count
// it accepts while ParentFamily strictly shortens its argument by exactly
// one segment — so the fixed point is reached in exactly maxNestingDepth+1
// passes, by construction rather than by convergence (D4.2).
func deriveSuggestions(res *openapi.Resolver, ops []*Operation, resp map[int][]*Response) []Suggestion {
	routes := make([]router.Route, len(ops))
	for i, op := range ops {
		routes[i] = router.Route{
			Method:        strings.ToUpper(op.Method),
			Path:          op.Path,
			CanonicalPath: op.CanonicalPath,
			SourceOrder:   op.SourceOrder,
		}
	}
	// basePath "" (decisions.md §D3, R3): a family key is the RELATIVE
	// canonical path, so the table is built the same way regardless of
	// where the spec's own base path (or a workspace's, later) mounts it.
	table := router.Build(routes, "")

	// collectionByFamily maps a GET route's own canonical path to its index
	// into ops, so the detail-route loop below can find R2's "GET X" half
	// by the exact key ListFamily already computed for it, in O(1) rather
	// than a nested scan.
	collectionByFamily := make(map[string]int, len(ops))
	for i, op := range ops {
		if strings.ToUpper(op.Method) == "GET" {
			collectionByFamily[op.CanonicalPath] = i
		}
	}

	var out []Suggestion
	seen := make(map[string]bool)    // defensive: two operations sharing one canonical GET path never happens in a real document, but nothing upstream guarantees it.
	derived := make(map[string]bool) // accumulates across passes — pass k's parent check reads exactly pass k-1's own additions (D4.1).

	// pass 0: accept a family with NO "{}" segment.
	// pass k, for k = 1..maxNestingDepth: accept a family with EXACTLY k
	// "{}" segments whose router.ParentFamily(family) was derived by pass
	// k-1. derived is read, never written, until the pass finishes — so at
	// the moment pass k's accept closure runs, derived holds exactly passes
	// 0..k-1's own output, and a parent lookup against it can only ever
	// match a depth-(k-1) family, i.e. one pass k-1 (and no other pass)
	// could have added (D4.1).
	for k := 0; k <= maxNestingDepth; k++ {
		derivedThisPass := make(map[string]bool)
		accept := acceptAtDepth(k, derived)
		for i, op := range ops {
			sugg, ok := deriveOneFamily(i, op, routes, table, collectionByFamily, seen, accept, res, resp)
			if !ok {
				continue
			}
			out = append(out, sugg)
			derivedThisPass[sugg.RouteFamily] = true
		}
		for family := range derivedThisPass {
			derived[family] = true
		}
	}

	// Deterministic emission order: ops itself is walked in the document's
	// own (already-sorted, see [Index]'s doc comment) order, but that is
	// GET-operation order, not family order — sorting here is what lets a
	// caller assert an exact, reproducible slice.
	slices.SortFunc(out, func(a, b Suggestion) int { return cmp.Compare(a.RouteFamily, b.RouteFamily) })
	return out
}

// acceptAtDepth builds pass k's accept predicate (D4.1): a family with
// exactly k "{}" segments, and — for k > 0 only — whose
// [router.ParentFamily] is a key of derived, the caller's accumulated
// output of every earlier pass. For k == 0 the parent check is skipped
// outright (a depth-0 family has no parent to check), which is also what
// makes pass 0 the identical "top-level" case P3a always derived.
func acceptAtDepth(k int, derived map[string]bool) func(family string) bool {
	return func(family string) bool {
		if strings.Count(family, "{}") != k {
			return false
		}
		if k == 0 {
			return true
		}
		return derived[router.ParentFamily(family)]
	}
}

// deriveOneFamily is [deriveSuggestions]'s per-GET-operation body, factored
// out of the bounded pass loop so no single pass carries the whole loop's
// cyclomatic cost: the parts every pass shares (skip non-GET, resolve the
// family, dedupe by seen, find the matching collection route, call
// deriveFamily) live once. accept is the one thing that differs between
// passes — [acceptAtDepth]'s per-k predicate (D4.1) — so the shared
// plumbing does not need to know which pass it is running.
func deriveOneFamily(
	i int,
	op *Operation,
	routes []router.Route,
	table *router.Table,
	collectionByFamily map[string]int,
	seen map[string]bool,
	accept func(family string) bool,
	res *openapi.Resolver,
	resp map[int][]*Response,
) (Suggestion, bool) {
	if strings.ToUpper(op.Method) != "GET" {
		return Suggestion{}, false
	}
	route := routes[i]
	family := router.ListFamily(table, &route)
	if family == "" || family == "/" || seen[family] || !accept(family) {
		return Suggestion{}, false
	}
	j, ok := collectionByFamily[family]
	if !ok {
		return Suggestion{}, false
	}
	seen[family] = true
	return deriveFamily(res, family, &route, resp[i], resp[j])
}

// deriveFamily is [deriveSuggestions]'s per-family body: R4 through R7 for
// exactly one already-confirmed (family, detail route, collection
// responses) triple. detailRoute is the detail GET's own [router.Route],
// needed only for [router.DetailIDParam] — the trailing path-parameter
// NAME, read off the route's real ordered pattern rather than guessed.
func deriveFamily(res *openapi.Resolver, family string, detailRoute *router.Route, detailResp, collResp []*Response) (Suggestion, bool) {
	idParam := router.DetailIDParam(detailRoute)
	if idParam == "" {
		return Suggestion{}, false
	}

	// R13/D5: the variant is the one chooseVariant would actually pick at
	// serve time — the IsDefault row — and ONLY when that row's own status
	// is 200. A detail (or collection) GET declaring 201 beside "default"
	// has an HTTP-200 row that nothing ever serves; deriving a suggestion
	// from it would populate under a variant the plane never chooses.
	detailVariant := defaultVariant(detailResp)
	if detailVariant == nil || detailVariant.HTTPStatus != 200 {
		return Suggestion{}, false
	}
	collVariant := defaultVariant(collResp)
	if collVariant == nil || collVariant.HTTPStatus != 200 {
		return Suggestion{}, false
	}

	// R7: the detail 200's media type must be JSON AND non-empty.
	// [httpx.IsJSONMediaType] answers true for "" (right for a no-body
	// variant, wrong here — decisions.md §D3 spells this out), so the
	// non-empty half is asked separately.
	if detailVariant.MediaType == nil || *detailVariant.MediaType == "" || !httpx.IsJSONMediaType(*detailVariant.MediaType) {
		return Suggestion{}, false
	}
	if detailVariant.SchemaPtr == nil || collVariant.SchemaPtr == nil {
		return Suggestion{}, false
	}

	arrayKey, countKey, countType, itemAnchor, itemSchema, ok := deriveCollectionShape(res, *collVariant.SchemaPtr)
	if !ok {
		return Suggestion{}, false
	}

	// R4: id_field is reconciled over the COLLECTION's item schema — see
	// reconcileIDField's own doc comment for the ordered pick.
	idField, ok := reconcileIDField(itemSchema, idParam)
	if !ok {
		return Suggestion{}, false
	}

	idType, ok := deriveDetailIDType(res, *detailVariant.SchemaPtr, idField)
	if !ok {
		return Suggestion{}, false
	}

	name := family
	if idx := strings.LastIndex(family, "/"); idx >= 0 {
		name = family[idx+1:]
	}

	return Suggestion{
		RouteFamily:  family,
		Name:         name,
		IDField:      idField,
		EntitySchema: itemAnchor,
		Wrapper: Wrapper{
			ArrayKey:  arrayKey,
			CountKey:  countKey,
			CountType: countType,
			IDType:    idType,
		},
	}, true
}

// deriveCollectionShape is R5+R6 together: the wrapper -> array -> item
// walk (re-anchored at every $ref hop) and, for a wrapper object (not a
// bare array), the single accepted count property alongside it — split out
// of [deriveFamily] purely to keep that function's own branch count at "one
// per R7 eligibility reason", not because this half is reusable elsewhere.
func deriveCollectionShape(res *openapi.Resolver, collSchemaPtr string) (arrayKey, countKey *string, countType, itemAnchor string, itemSchema map[string]any, ok bool) {
	collAnchor, collSchema, ok := resolveSchemaNode(res, refNode(collSchemaPtr), collSchemaPtr)
	if !ok {
		return nil, nil, "", "", nil, false
	}
	arrayKey, arrayAnchor, arraySchema, ok := findArrayShape(res, collAnchor, collSchema)
	if !ok {
		return nil, nil, "", "", nil, false
	}
	itemsRaw, hasItems := arraySchema["items"]
	if !hasItems {
		return nil, nil, "", "", nil, false
	}
	itemAnchor, itemSchema, ok = resolveSchemaNode(res, itemsRaw, arrayAnchor+"/items")
	if !ok {
		return nil, nil, "", "", nil, false
	}

	// R6: the wrapper's count property, over the WRAPPER's own properties
	// (collSchema), not the item schema — a bare array has no wrapper
	// object at all, so it is skipped outright and both fields stay
	// nil/"" (D3: "For a bare array ... countType is \"\"").
	if arrayKey != nil {
		countKey, countType, ok = findCountShape(res, collSchema, *arrayKey)
		if !ok {
			return nil, nil, "", "", nil, false
		}
	}
	return arrayKey, countKey, countType, itemAnchor, itemSchema, true
}

// deriveDetailIDType is R7's tail: the DETAIL schema (never the collection
// item schema — R12 says the two need not agree) must independently
// declare idField, as a resolvable property whose [gen.PrimaryIDType] is
// "", "string" or "integer" — the one bucket [gen.CoerceIDValue] treats
// uniformly. A boolean id collapses entity_key to two values; a number id
// gives float-formatted keys; neither is a usable identity, so both refuse
// here rather than at population time.
func deriveDetailIDType(res *openapi.Resolver, detailSchemaPtr, idField string) (idType string, ok bool) {
	detailAnchor, detailSchema, ok := resolveSchemaNode(res, refNode(detailSchemaPtr), detailSchemaPtr)
	if !ok {
		return "", false
	}
	detailProps, _ := detailSchema["properties"].(map[string]any)
	idNode, hasID := detailProps[idField]
	if !hasID {
		return "", false
	}
	_, idSchema, ok := resolveSchemaNode(res, idNode, detailAnchor+"/properties/"+openapi.EscapePointerToken(idField))
	if !ok {
		// "An id property that does not RESOLVE emits no suggestion at
		// all" (D3) — population would hit the same unresolvable $ref and
		// abort every confirm this suggestion could ever complete.
		return "", false
	}
	idType = gen.PrimaryIDType(idSchema)
	switch idType {
	case "", "string", "integer":
		return idType, true
	default:
		return "", false
	}
}

// defaultVariant returns rs' IsDefault row, or nil if rs is empty (never
// happens for a real operation — [indexResponses] always synthesizes a
// fallback row — but resp[i] is a map lookup that can legitimately miss
// for an operation Index never populated a key for).
func defaultVariant(rs []*Response) *Response {
	for _, r := range rs {
		if r.IsDefault {
			return r
		}
	}
	return nil
}

// refNode wraps ptr the same way [openapi.Resolver.Resolve] does
// internally (decisions.md §D3: "the call that takes a POINTER is Resolve
// ... Where this slice wants both the value and the anchor it passes
// map[string]any{\"$ref\": ptr}, exactly as Resolve does internally").
func refNode(ptr string) any {
	return map[string]any{"$ref": ptr}
}

// resolveSchemaNode chases node's $ref chain (if any) via
// [openapi.Resolver.ResolveNodePointer] and reports the schema it lands
// on, re-anchored at the LAST hop actually taken — own is the anchor to
// fall back to when node was never a $ref at all (ResolveNodePointer's
// lastPointer is then ""), i.e. an inline schema whose location is
// wherever the caller already knows it to be (a property path built by
// hand, since there is no $ref hop to report it from).
//
// This is the one place every hop of R5's wrapper -> array -> item walk
// goes through, which is what keeps the walk from ever appending a child
// segment (\"/items\", \"/properties/x\") onto a STILL-$ref'd location —
// decisions.md §D3's own warning about [SchemaPtr] stopping at the schema
// node rather than the thing it points to.
func resolveSchemaNode(res *openapi.Resolver, node any, own string) (anchor string, schema map[string]any, ok bool) {
	lastPointer, resolved, err := res.ResolveNodePointer(node)
	if err != nil {
		return "", nil, false
	}
	anchor = own
	if lastPointer != "" {
		anchor = lastPointer
	}
	m, ok := resolved.(map[string]any)
	if !ok {
		return "", nil, false
	}
	return anchor, m, true
}

// findArrayShape is R6's array half: schema is either itself an array (a
// bare-array 200 — arrayKey nil) or an object with exactly ONE array-typed
// property (arrayKey non-nil) — zero or two-or-more candidates are both
// refused, because either shape would make arrayKey a guess (D3: no
// preferred-name tie-break here, unlike [gen]'s own generation-time
// heuristic — this is the derivation-time DECISION whether a family
// exists at all, not a best-effort pick of which property to render).
func findArrayShape(res *openapi.Resolver, anchor string, schema map[string]any) (arrayKey *string, arrayAnchor string, arraySchema map[string]any, ok bool) {
	if gen.SchemaType(schema) == "array" {
		return nil, anchor, schema, true
	}

	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return nil, "", nil, false
	}
	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	slices.Sort(names) // map range order is randomized; candidate discovery must be deterministic.

	type candidate struct {
		name   string
		anchor string
		schema map[string]any
	}
	var candidates []candidate
	for _, name := range names {
		propAnchor, propSchema, ok := resolveSchemaNode(res, props[name], anchor+"/properties/"+openapi.EscapePointerToken(name))
		if !ok {
			continue
		}
		if gen.SchemaType(propSchema) == "array" {
			candidates = append(candidates, candidate{name, propAnchor, propSchema})
		}
	}
	if len(candidates) != 1 {
		return nil, "", nil, false
	}
	c := candidates[0]
	return &c.name, c.anchor, c.schema, true
}

// findCountShape is R6's count half, run over the WRAPPER's own properties
// (not the array property itself, which is excluded by name): zero
// [gen.IsTotalFieldName]-accepted properties is legal (nil, ""); exactly
// one wins; two or more is refused for the same reason two array
// candidates are.
func findCountShape(res *openapi.Resolver, wrapperSchema map[string]any, arrayKey string) (countKey *string, countType string, ok bool) {
	props, _ := wrapperSchema["properties"].(map[string]any)
	names := make([]string, 0, len(props))
	for name := range props {
		if name == arrayKey {
			continue
		}
		names = append(names, name)
	}
	slices.Sort(names)

	var matches []string
	for _, name := range names {
		if gen.IsTotalFieldName(strings.ToLower(name)) {
			matches = append(matches, name)
		}
	}
	switch len(matches) {
	case 0:
		return nil, "", true
	case 1:
		// countType is read from the RESOLVED node, second-line first-line
		// rule: unresolved or resolved-to-non-object both store "" (D3),
		// never falling back to [gen.SchemaType]'s "string" default for
		// those two cases specifically — only a resolved MAP schema
		// (including a genuinely untyped {}) reaches SchemaType at all.
		name := matches[0]
		ct := ""
		if resolved, err := res.ResolveNode(props[name]); err == nil {
			if m, ok := resolved.(map[string]any); ok {
				ct = gen.SchemaType(m)
			}
		}
		return &name, ct, true
	default:
		return nil, "", false
	}
}

// reconcileIDField is R4's ordered pick, over itemSchema's OWN properties
// (the collection's item schema — the detail schema's agreement is R7's
// separate, later check): a property named EXACTLY idParam wins outright;
// failing that, a property literally named "id"; failing that, exactly ONE
// property equal to idParam case-insensitively. Two or more
// case-insensitive matches — like two array candidates — would make the
// pick depend on Go's randomized map range order, so that case is refused
// rather than guessed.
func reconcileIDField(itemSchema map[string]any, idParam string) (string, bool) {
	props, _ := itemSchema["properties"].(map[string]any)
	if len(props) == 0 {
		return "", false
	}
	if _, ok := props[idParam]; ok {
		return idParam, true
	}
	if _, ok := props["id"]; ok {
		return "id", true
	}

	var matches []string
	for name := range props {
		if strings.EqualFold(name, idParam) {
			matches = append(matches, name)
		}
	}
	if len(matches) != 1 {
		return "", false
	}
	return matches[0], true
}
