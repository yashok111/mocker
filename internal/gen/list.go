package gen

import (
	"fmt"
	"maps"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/yashok111/mocker/internal/jsonx"
)

// list.go implements DESIGN §9's list contract — the seam declared (as a
// stub) in stub_list.go: pagination (limit/offset/page/cursor, hard-clamped),
// a stable `total`, global-index item identity, list-row == detail-card
// equivalence, and a spec-declared-query-parameter postfilter with top-up.
//
// listBody is tried BEFORE the ordinary example/schema priority chain
// (gen.go's Body) because "is this even a list, and if so which route of the
// pair is this" is a STRUCTURAL/shape decision, not a value source — it
// needs Request.ListFamily (router-family knowledge this leaf package does
// not derive itself) and the response schema's own shape, neither of which
// leafValue's priority chain is equipped to reason about.
//
// Two independent paths:
//
//   - req.ListFamily != "" -> this IS a detail route for some list family
//     (rule 5): generate from SeedItemByID(family seed, the id from the
//     path), unconditionally — regardless of what the detail schema's own
//     shape happens to look like.
//   - otherwise -> detect whether schema is itself a list (top-level array,
//     or an object with exactly one array-typed property, DESIGN's own
//     phrasing) and, if so, generate a page: limit/offset from the query
//     (hard-clamped against a hostile ?limit=1000000 on an open mock plane),
//     total from Options.ListSize, each item's identity and fields from
//     IDForIndex/SeedItemByID at its GLOBAL index, and a postfilter over
//     spec-declared query parameters that match an item property, topped up
//     to the requested length.
func listBody(w *walker, v ResponseVariant, req Request, schema map[string]any) (any, bool, error) {
	if req.ListFamily != "" {
		return w.detailBody(schema, req)
	}
	return w.listPageBody(v, req, schema)
}

// --- detail routes: rule 5 ("list row == detail card") --------------------

// detailBody generates the object for a detail route (Request.ListFamily set
// by the caller): the SAME object a list row at the requested id would
// generate, by computing the FAMILY's own seedList (same method, ListFamily
// as the canonical path, the id path parameter removed, status 200 — DESIGN:
// "того же семейства маршрутов") and then SeedItemByID(familySeedList, the
// id from the path) — never req's own seedList, which would hash in the
// EXTRA id path segment and land on a different value than the list route
// ever produces for that id.
//
// ok is false (not an error) when the id path parameter cannot be
// identified at all (req.ListFamily set but PathParams empty — a shape this
// phase's fixtures never produce, but not this package's job to treat as
// fatal): the caller falls through to ordinary, non-identity-aware
// generation rather than failing the request.
func (w *walker) detailBody(schema map[string]any, req Request) (any, bool, error) {
	idName, idRaw, ok := identifyIDParam(req)
	if !ok {
		return nil, false, nil
	}

	familyReq := Request{
		Method:        req.Method,
		CanonicalPath: req.ListFamily,
		PathParams:    familyPathParams(req.PathParams, idName),
		Status:        200,
	}
	familySeedList := SeedList(w.opts, familyReq)
	itemSeed := SeedItemByID(familySeedList, idRaw)

	iw := *w // shares w.budget's pointer; only the seed root changes
	iw.seedList = itemSeed
	val, err := iw.walkSchema(schema, "", 0)
	if err != nil {
		return nil, false, err
	}

	// The identity write-back (DESIGN's launch decision, restated in the
	// task brief): the id property takes the PATH PARAMETER's value,
	// coerced to the id property's OWN schema type — never the raw URL
	// string, or an {type: integer} id schema emits {"id":"42"} (a
	// string) and fails its own schema while also disagreeing with the
	// list row on the one field that identifies them.
	if obj, isObj := val.(map[string]any); isObj {
		if props, hasProps := schema["properties"].(map[string]any); hasProps {
			if idNode, hasID := props["id"]; hasID {
				var idSchema map[string]any
				if resolved, rerr := w.res.ResolveNode(idNode); rerr == nil {
					idSchema, _ = resolved.(map[string]any)
				}
				obj["id"] = coerceID(idRaw, idSchema)
			}
		}
	}
	return val, true, nil
}

// identifyIDParam picks, out of req.PathParams, the one parameter that is
// THIS detail route's own id (as opposed to a path parameter belonging to
// the list family it shares, e.g. an outer /orgs/{orgId}/bulletins/{id}
// nesting).
//
// req.IDParam, when set, decides this OUTRIGHT: the caller (mockplane)
// computed it structurally, off the router's real ORDERED path pattern,
// which is the only reliable source for a route with two or more
// "id-shaped" parameter names — see Request.IDParam's own doc comment for
// why a name-based heuristic over the unordered PathParams map cannot be
// trusted to disambiguate those.
//
// req.IDParam == "" falls back to a heuristic, kept only for callers that
// build a Request by hand without router knowledge (tests, mainly).
// Request.CanonicalPath anonymizes every {param} segment to a bare "{}"
// (router.CanonicalPath), so there is no positional name<->segment mapping
// left to recover from the request alone in this fallback path.
//
// The overwhelmingly common case (this phase's own fixture included: GET
// /api/v1/bulletins/{bulletinId}) has exactly one path parameter, which
// makes the choice unambiguous regardless. For a route sitting two path
// segments deep, the parameter whose name itself looks like an id (exact
// "id", or an "Id"/"_id"-suffixed name) is preferred; if that is still
// ambiguous (none or more than one match), the lexicographically LAST name
// is used so the fallback is at least deterministic rather than a
// process-randomized map-iteration pick — NOT guaranteed to pick the right
// parameter when more than one looks id-shaped, which is exactly why a real
// request never relies on this branch.
func identifyIDParam(req Request) (name, value string, ok bool) {
	if req.IDParam != "" {
		if v, present := req.PathParams[req.IDParam]; present {
			return req.IDParam, v, true
		}
	}
	if len(req.PathParams) == 0 {
		return "", "", false
	}
	if len(req.PathParams) == 1 {
		for k, v := range req.PathParams {
			return k, v, true
		}
	}

	var idLike []string
	for k := range req.PathParams {
		if looksLikeIDParam(k) {
			idLike = append(idLike, k)
		}
	}
	if len(idLike) == 1 {
		return idLike[0], req.PathParams[idLike[0]], true
	}

	names := make([]string, 0, len(req.PathParams))
	for k := range req.PathParams {
		names = append(names, k)
	}
	sort.Strings(names)
	last := names[len(names)-1]
	return last, req.PathParams[last], true
}

// looksLikeIDParam reports whether name is conventionally an identifier
// parameter: exactly "id" (any case), or ending in "Id"/"ID" (camelCase, the
// acceptance fixture's own "bulletinId") or "_id" (snake_case).
func looksLikeIDParam(name string) bool {
	if strings.EqualFold(name, "id") {
		return true
	}
	if strings.HasSuffix(name, "Id") || strings.HasSuffix(name, "ID") {
		return true
	}
	return strings.HasSuffix(strings.ToLower(name), "_id")
}

// familyPathParams is req's own path parameters minus the id one — exactly
// what DESIGN's "the id path parameter removed" asks the family seedList to
// be computed over.
func familyPathParams(all map[string]string, idName string) map[string]string {
	out := make(map[string]string, len(all))
	for k, v := range all {
		if k == idName {
			continue
		}
		out[k] = v
	}
	return out
}

// coerceID shapes raw (a URL path-parameter string) into idSchema's own
// declared type — the same typed form IDForIndex/typedIDFromHash produces
// for a list row, so a detail card's id is never left as a bare string when
// the schema says otherwise. A thin wrapper over [CoerceIDValue]: this is
// where the schema gets resolved into a type and a format, the pure
// coercion itself lives in the exported function so resource population
// (internal/resources) can call it without a resolver at serve time.
func coerceID(raw string, idSchema map[string]any) any {
	t := ""
	format := ""
	if idSchema != nil {
		t = PrimaryIDType(idSchema)
		format, _ = idSchema["format"].(string)
	}
	return CoerceIDValue(raw, t, format)
}

// CoerceIDValue shapes raw (a URL path-parameter string, or — from P3a — a
// decimal internal/store sequence number) into idType's declared shape.
// When raw doesn't actually parse as that type (a mismatched request, or
// idType "" for missing/untyped), it falls back to a deterministic hash of
// raw shaped into the same type — never the raw string verbatim for a
// non-string type, and never an error: callers are documented to never
// fail a request/write over a schema mismatch like this.
//
// Exported (P3a) so that a resource's serving path — which stores idType
// once at derivation ([PrimaryIDType] of the resolved id property) and has
// no resolver at serve time — can coerce a freshly allocated seq into the
// same shape [coerceID] would give a detail-route path parameter, without
// a second, driftable implementation of the four branches below. format is
// carried because the integer branch's fallback ([idInteger]) depends on
// it; every P3a caller passes format "" — unreachable there in practice,
// since every raw a resource hands this is a decimal seq, which always
// parses as both integer and number.
func CoerceIDValue(raw, idType, format string) any {
	switch idType {
	case "integer":
		if v, err := strconv.ParseInt(raw, 10, 64); err == nil {
			return v
		}
		return idInteger(hash64([]byte("coerce-id"), []byte(raw)), format)
	case "number":
		if v, err := strconv.ParseFloat(raw, 64); err == nil {
			return v
		}
		h := hash64([]byte("coerce-id"), []byte(raw))
		return float64(h%1_000_000) / 100
	case "boolean":
		if v, err := strconv.ParseBool(raw); err == nil {
			return v
		}
		return hash64([]byte("coerce-id"), []byte(raw))%2 == 0
	default:
		// "string", or no usable schema at all: the raw path value already
		// IS the correct shape (including a uuid-formatted id — the URL
		// string is the canonical form JSON Schema "format": "uuid" wants).
		return raw
	}
}

// --- list routes: rules 1-4, 6, 7 -------------------------------------

// listShape is what detectListShape found: which schema node is the array,
// and under what wrapper-object property name (empty for a bare top-level
// array). generateItems always walks each item at depth 0 (see there) —
// the SAME starting depth detailBody uses for the identical item schema —
// so this shape carries no depth information of its own; DESIGN's dataPath
// restart at the item root is the parallel, unconditional rule for
// dataPath, applied by always walking each item with dataPath "" too.
type listShape struct {
	arraySchema     map[string]any
	arrName         string
	isTopLevelArray bool
}

// listPreferredArrayNames is DESIGN's own tie-break list for an object
// carrying more than one array-typed property: "предпочитая свойство с
// именем items/data/results/list/content/rows при нескольких кандидатах".
var listPreferredArrayNames = []string{"items", "data", "results", "list", "content", "rows"}

// detectListShape is rule 1: a top-level array is a list; an object with
// array-typed properties is a list when there is exactly one such property,
// or — with more than one — when one of them carries a canonical
// collection-ish name. An object with several array properties and NONE of
// them canonically named is genuinely ambiguous which one is "the page", so
// this declines (ok=false) rather than guessing: DESIGN's own words, "не
// навязывать пагинацию объекту, который не является страницей."
func detectListShape(w *walker, schema map[string]any) (listShape, bool) {
	if SchemaType(schema) == "array" {
		return listShape{arraySchema: schema, isTopLevelArray: true}, true
	}

	props, _ := schema["properties"].(map[string]any)
	if len(props) == 0 {
		return listShape{}, false
	}

	names := make([]string, 0, len(props))
	for name := range props {
		names = append(names, name)
	}
	sort.Strings(names)

	type candidate struct {
		name   string
		schema map[string]any
	}
	var candidates []candidate
	for _, name := range names {
		resolved, err := w.res.ResolveNode(props[name])
		if err != nil {
			continue
		}
		propSchema, ok := resolved.(map[string]any)
		if !ok {
			continue
		}
		if SchemaType(propSchema) == "array" {
			candidates = append(candidates, candidate{name, propSchema})
		}
	}

	switch len(candidates) {
	case 0:
		return listShape{}, false
	case 1:
		return listShape{arraySchema: candidates[0].schema, arrName: candidates[0].name}, true
	}

	for _, preferred := range listPreferredArrayNames {
		for _, c := range candidates {
			if c.name == preferred {
				return listShape{arraySchema: c.schema, arrName: c.name}, true
			}
		}
	}
	return listShape{}, false
}

// listPageBody is rules 2-4, 6, 7 for a genuine list route (req.ListFamily
// == ""): detect the shape, compute limit/offset/total, generate each
// item's identity+fields from its GLOBAL index, apply the spec-declared
// postfilter with top-up, and — for the wrapper-object shape — splice the
// page into the object alongside normally-generated sibling fields (with
// total/limit/offset/page overridden to the pagination bookkeeping values
// DESIGN calls for, rather than whatever the ordinary scalar generator would
// have produced for them).
func (w *walker) listPageBody(v ResponseVariant, req Request, schema map[string]any) (any, bool, error) {
	shape, ok := detectListShape(w, schema)
	if !ok {
		return nil, false, nil
	}

	limit := w.pageLimit(req, shape.arraySchema, shape.arrName)
	offset := pageOffset(req, limit)
	total := w.listSizeOrDefault(shape.arrName)

	itemsNode, hasItems := shape.arraySchema["items"]
	var itemSchema map[string]any
	if hasItems {
		if resolved, err := w.res.ResolveNode(itemsNode); err == nil {
			itemSchema, _ = resolved.(map[string]any)
		}
	}
	idSchema, hasIDProp := itemIdentitySchema(w, itemSchema)
	filters := w.listFilters(v, req, itemSchema)

	// A top-level array response has no wrapper object at all — items IS
	// the whole body, so there is nothing to sequence against.
	if shape.isTopLevelArray {
		items, err := w.generateItems(itemsNode, hasItems, idSchema, hasIDProp, limit, offset, total, filters, shape.arrName)
		if err != nil {
			return nil, false, err
		}
		return items, true, nil
	}

	// The wrapper's OWN fields (total/limit/offset and any other sibling
	// of the array property) are walked BEFORE generateItems runs, not
	// after (P1b round-2 review, finding 1): generateItems has no
	// visibility at all into what the wrapper still owes (it neither
	// receives the wrapper schema nor was ever called after it), so
	// running it FIRST let it legitimately spend the entire shared budget
	// on items alone, leaving the wrapper's own REQUIRED properties —
	// which forceSpend regardless of how negative that leaves things —
	// to blow the total over Options.MaxBytes once their turn came.
	// Walking the wrapper first instead means generateItems naturally
	// inherits WHATEVER real budget remains afterward and, being governed
	// by spend() (never forceSpend, list items are never schema-required
	// beyond the page itself), cannot re-introduce the same overshoot.
	// An estimate-based reservation was tried and rejected here: it
	// worked for a wrapper whose fields all land on their SMALLEST legal
	// value, but an ordinary unbounded {"type":"integer"} property's REAL
	// generated value (before walkWrapperFields's own pagination
	// override replaces it) can run to many more digits than the
	// estimate's bare `0` assumed, silently under-reserving. Generating
	// for real first has no such gap: there is nothing left to estimate.
	wrapperVal, err := w.walkWrapperFields(schema, shape.arrName)
	if err != nil {
		return nil, false, err
	}
	obj, ok := wrapperVal.(map[string]any)
	if wrapperVal == nil || !ok {
		// Nullable wrapper rolled null, or schema unexpectedly didn't hand
		// back a map (walkWrapperFields's own doc explains why that
		// guard exists) — either way there is no wrapper to splice a page
		// into, so generating items would only spend budget on a value
		// nothing downstream will ever use.
		return wrapperVal, true, nil
	}

	items, err := w.generateItems(itemsNode, hasItems, idSchema, hasIDProp, limit, offset, total, filters, shape.arrName)
	if err != nil {
		return nil, false, err
	}

	body := w.spliceWrapperPage(obj, schema, shape.arrName, items, limit, offset, total)
	return body, true, nil
}

// itemIdentitySchema resolves the item schema's own "id" property (the
// identity field the whole seed contract, and this fixture's row/card
// equivalence, is written against), reporting whether the property exists
// at all separately from whether its schema could be resolved — a property
// that exists but has an unresolvable (broken $ref) schema still gets an
// id written into it, just an untyped-fallback one (IDForIndex's own nil-
// schema branch), rather than being silently skipped.
func itemIdentitySchema(w *walker, itemSchema map[string]any) (idSchema map[string]any, hasIDProp bool) {
	if itemSchema == nil {
		return nil, false
	}
	props, ok := itemSchema["properties"].(map[string]any)
	if !ok {
		return nil, false
	}
	idNode, ok := props["id"]
	if !ok {
		return nil, false
	}
	resolved, err := w.res.ResolveNode(idNode)
	if err != nil {
		return nil, true
	}
	m, _ := resolved.(map[string]any)
	return m, true
}

// listFilters is rule 6: query parameters DECLARED IN THE SPEC (resolved
// off v.OpPointer's own "parameters" — the operation object, not the
// response) whose name also matches a property of the item schema. The
// recognized pagination keys are excluded even if a spec coincidentally
// declares a same-named item property, so pagination bookkeeping never
// doubles as an accidental filter.
func (w *walker) listFilters(v ResponseVariant, req Request, itemSchema map[string]any) map[string][]string {
	filters := map[string][]string{}
	if itemSchema == nil || len(req.Query) == 0 {
		return filters
	}
	props, ok := itemSchema["properties"].(map[string]any)
	if !ok {
		return filters
	}
	declared := w.declaredQueryParams(v)
	for key, vals := range req.Query {
		if isPaginationKey(key) {
			continue
		}
		if !declared[key] {
			continue
		}
		if _, isItemProp := props[key]; !isItemProp {
			continue
		}
		filters[key] = vals
	}
	return filters
}

// declaredQueryParams resolves v.OpPointer (the OPERATION object itself,
// e.g. "#/paths/~1api~1v1~1x/get" — not a response) and reads its own
// "parameters" array, collecting the names of every "in": "query" entry.
// Each entry is resolved through ResolveNode since OpenAPI commonly $refs
// shared parameters (a "limit"/"offset" pair reused across every list
// operation, say) rather than inlining them. Unresolvable, absent, or
// malformed parameters are simply not counted as declared — never an
// error, matching every other soft-decline in this package's query-time
// introspection (gen.go's Headers has the identical shape of tolerance).
func (w *walker) declaredQueryParams(v ResponseVariant) map[string]bool {
	out := map[string]bool{}
	if v.OpPointer == "" {
		return out
	}
	resolved, err := w.res.Resolve(v.OpPointer)
	if err != nil {
		return out
	}
	opObj, ok := resolved.(map[string]any)
	if !ok {
		return out
	}
	paramsRaw, ok := opObj["parameters"].([]any)
	if !ok {
		return out
	}
	for _, p := range paramsRaw {
		resolvedP, perr := w.res.ResolveNode(p)
		if perr != nil {
			continue
		}
		pm, ok := resolvedP.(map[string]any)
		if !ok {
			continue
		}
		if in, _ := pm["in"].(string); in != "query" {
			continue
		}
		if name, _ := pm["name"].(string); name != "" {
			out[name] = true
		}
	}
	return out
}

// paginationQueryKeys are the query-parameter names rule 2 reads pagination
// off of; listFilters excludes them from postfilter consideration
// regardless of whether they happen to also be declared and match an item
// property, so a spec that (unusually) names an item field "offset" cannot
// make pagination bookkeeping double as a filter.
var paginationQueryKeys = map[string]bool{
	"limit": true, "offset": true, "page": true, "cursor": true,
	"per_page": true, "size": true,
}

func isPaginationKey(key string) bool {
	return paginationQueryKeys[strings.ToLower(key)]
}

// --- pagination: limit/offset, hard-clamped ----------------------------

const (
	// maxListPageLimit is the hard ceiling on how many items ONE page
	// request can ask for, independent of anything the query string says.
	// The mock plane is, by DESIGN's own threat model (§15), an OPEN
	// endpoint: an unauthenticated `?limit=1000000` must not make the
	// generator walk a million items before Options.MaxBytes ever gets a
	// chance to say no. 500 is generous for any real pagination UI (which
	// virtually never renders more than a few hundred rows at once) while
	// keeping one page's worst-case item count firmly bounded.
	maxListPageLimit = 500

	// maxListPageOffset bounds a hostile `?offset=99999999999999`. Offset
	// arithmetic itself is O(1) and cannot runaway-allocate on its own —
	// the real protection is generateItems' own `idx >= total` cutoff —
	// but clamping keeps an absurd value from being echoed back verbatim
	// into a response's own "offset" field either.
	maxListPageOffset = 1_000_000

	// maxListPageIndex bounds a 1-based `?page=N` BEFORE it is multiplied
	// by limit to derive an offset, for the same reason maxListPageOffset
	// exists — an unclamped multiply on a hostile page number is exactly
	// the kind of arithmetic this boundary is supposed to make impossible
	// to weaponize.
	maxListPageIndex = 100_000
)

// pageLimit is rule 2's page length: `limit` / `per_page` / `size`, in that
// priority order, defaulting to listSizeOrDefault (P1c: Options.ListSize,
// or a listSize recipe's pinned length when one is bound — see there) when
// none parse, then hard-clamped to maxListPageLimit, then narrowed into the
// array schema's own [minItems, maxItems] when it declares either (maxItems
// can only SHRINK the ceiling further; minItems widening is re-clamped
// against the hard cap immediately after, so a pathological `minItems` in
// the spec itself cannot be used to defeat the safety clamp).
//
// recipePath is the array's own path (shape.arrName — "" for a top-level
// array, both call sites) for listSizeOrDefault's own recipe lookup. The
// client always wins when it asked: an explicit limit/per_page/size is
// checked FIRST, above, so a listSize recipe is only ever consulted as the
// fallback default, never as an override of a real request.
func (w *walker) pageLimit(req Request, arrSchema map[string]any, recipePath string) int {
	n, ok := firstIntQuery(req.Query, "limit", "per_page", "size")
	if !ok {
		n = w.listSizeOrDefault(recipePath)
	}
	if n < 0 {
		n = 0
	}
	if n > maxListPageLimit {
		n = maxListPageLimit
	}
	if v, has := numFromSchema(arrSchema, "maxItems"); has && int(v) < n {
		n = int(v)
	}
	if v, has := numFromSchema(arrSchema, "minItems"); has && int(v) > n {
		n = min(int(v), maxListPageLimit)
	}
	if n < 0 {
		n = 0
	}
	return n
}

// listSizeOrDefault is Options.ListSize (pageLimit's and listPageBody's
// own `total` computation's shared fallback), overridden by a listSize
// recipe bound to the array's own path (recipePath) when the Set carries
// one — DESIGN §9's "the recipe pins the length... overrides
// Options.ListSize for that array." With no *Set at all (HARD RULE 6's
// common case) this returns exactly w.opts.effListSize(), unchanged —
// the SAME expression pageLimit's fallback and listPageBody's `total`
// assignment used before this recipe seam existed, just factored so both
// call sites see the recipe override identically.
func (w *walker) listSizeOrDefault(recipePath string) int {
	n := w.opts.effListSize()
	set := w.req.Recipes
	if set == nil {
		return n
	}
	if lo, hi, ok := set.ListSizeAt(w.recipePath(recipePath)); ok {
		return pinnedListSize(lo, hi, w.seedList, recipePath)
	}
	return n
}

// pageOffset is rule 2's offset: `offset` directly, else `page` (1-based,
// multiplied by the already-clamped limit), else `cursor` treated as an
// opaque-but-integer-shaped token — "accept an integer-ish cursor, ignore
// what you cannot parse" (firstIntQuery already does exactly that: a
// present-but-unparseable value is skipped, not treated as absent-with-a-
// hard-stop). No match at all defaults to 0 — the first page.
func pageOffset(req Request, limit int) int {
	if n, ok := firstIntQuery(req.Query, "offset"); ok {
		return clampOffset(n)
	}
	if n, ok := firstIntQuery(req.Query, "page"); ok {
		if n < 1 {
			n = 1
		}
		if n > maxListPageIndex {
			n = maxListPageIndex
		}
		return clampOffset((n - 1) * limit)
	}
	if n, ok := firstIntQuery(req.Query, "cursor"); ok {
		return clampOffset(n)
	}
	return 0
}

func clampOffset(n int) int {
	if n < 0 {
		return 0
	}
	if n > maxListPageOffset {
		return maxListPageOffset
	}
	return n
}

// firstIntQuery tries keys in order, returning the first one that is both
// PRESENT and parses as a plain base-10 integer; a key that is present but
// garbage (an opaque non-numeric cursor, say) is skipped rather than
// treated as a hard failure, so a later key in the list still gets a
// chance.
func firstIntQuery(q map[string][]string, keys ...string) (int, bool) {
	for _, k := range keys {
		vals, present := q[k]
		if !present || len(vals) == 0 {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(vals[0]))
		if err != nil {
			continue
		}
		return n, true
	}
	return 0, false
}

// --- item generation: rules 4, 6, 7 -------------------------------------

const (
	// postfilterScanMultiplier and postfilterScanCap bound rule 6's "top
	// up to the requested length" scan: a filter that matches almost
	// nothing must still terminate in bounded work rather than scanning
	// forever looking for matches that do not exist. In practice
	// generateItems' own `idx >= total` cutoff (total is a workspace
	// SETTING, Options.ListSize, never query-controlled) already bounds
	// this tightly for any sane configuration; these two exist as a second,
	// independent ceiling in case ListSize itself is configured very large.
	postfilterScanMultiplier = 20
	postfilterScanCap        = 4000
)

// generateItems is rules 4, 6, 7: for each candidate global index starting
// at offset, derive its id (IDForIndex) and per-item seed (SeedItemByID),
// walk the item schema under THAT seed with BOTH dataPath and depth reset
// — dataPath to "" (the restart DESIGN's item-root rule demands — see the
// package doc's SEED CONTRACT) and depth to 0, exactly like detailBody
// (list.go) starts the SAME item schema for a detail-route request.
// Depth used to reset to the wrapper's own itemDepth (1 for a bare
// top-level array, 2 for the common wrapper-object shape) instead of 0 —
// the P1b round-1 review's finding 6: the identical item schema then got a
// DIFFERENT depth budget depending on whether it was reached as a list row
// or a detail card, so a field sitting within itemDepth levels of
// Options.MaxDepth would clear the ceiling on one side and hit
// schema.go's depth-limit stub on the other, breaking list-row ==
// detail-card for any item shape close to the ceiling even though both
// sides used the identical item seed. Resetting depth to 0 here, exactly
// like detailBody, is what makes the two paths symmetric again. Stamp the
// id property with its typed value, apply the postfilter, and keep
// scanning until `limit` items have matched, the index reaches `total`,
// or the scan-cap safety net trips. Nested arrays inside an item are
// untouched by any of this — they still go through schema.go's own
// walkArray, sharing the SAME w.budget pointer, which is exactly what
// keeps rule 7's "25x25 nested blowup" bounded without this function
// needing to know anything about it.
//
// arrName (P1c) is the array's own property name — "" for a top-level
// array response, the wrapper property name otherwise (shape.arrName, both
// call sites) — used ONLY to set each item's recipePrefix ("items[3]") to
// the item's own ABSOLUTE path before its (dataPath-restarted) walk begins.
// Without it, a recipe bound to "items[*].status" would have no absolute
// path to match against once dataPath restarts at the item root, exactly
// the gap schema.go's recipePrefix field exists to close (see its own doc).
func (w *walker) generateItems(itemsNode any, hasItems bool, idSchema map[string]any, hasIDProp bool, limit, offset, total int, filters map[string][]string, arrName string) ([]any, error) {
	result := make([]any, 0, limit)
	if limit <= 0 {
		return result, nil
	}

	scanCap := max(min(limit*postfilterScanMultiplier, postfilterScanCap), limit)

	for scanned := 0; len(result) < limit && scanned < scanCap; scanned++ {
		idx := offset + scanned
		if idx >= total {
			break
		}
		if w.budget != nil && w.budget.remaining <= 0 {
			break
		}

		id, idStr := IDForIndex(w.seedList, idx, idSchema)
		itemSeed := SeedItemByID(w.seedList, idStr)
		iw := *w
		iw.seedList = itemSeed
		// Only computed when a *Set is actually bound (HARD RULE 6): the
		// fmt.Sprintf below would otherwise run once per item on every list
		// request in every workspace for a prefix nothing ever looks up.
		if w.req.Recipes != nil {
			iw.recipePrefix = w.recipePath(fmt.Sprintf("%s[%d]", arrName, idx))
		}

		var val any
		if hasItems {
			v, err := iw.walkNode(itemsNode, "", 0)
			if err != nil {
				return nil, err
			}
			val = v
		}
		if hasIDProp {
			if obj, ok := val.(map[string]any); ok {
				obj["id"] = id
			}
		}

		if !matchesFilters(val, filters) {
			continue
		}

		size := estimateJSONSize(val) + 1
		if !w.budget.spend(size) {
			break
		}
		result = append(result, val)
	}
	return result, nil
}

// matchesFilters reports whether item satisfies every filter — an item that
// isn't even an object (a malformed or scalar item schema) can't be
// filtered by property match, so it passes through rather than being
// silently dropped over a shape mismatch this function has no way to
// interpret. Multiple values for the SAME query key (a repeated
// ?status=a&status=b) match as "any of"; multiple DIFFERENT keys are AND-
// ed together.
func matchesFilters(item any, filters map[string][]string) bool {
	if len(filters) == 0 {
		return true
	}
	obj, ok := item.(map[string]any)
	if !ok {
		return true
	}
	for key, vals := range filters {
		fv, present := obj[key]
		if !present || !anyValueMatches(fv, vals) {
			return false
		}
	}
	return true
}

func anyValueMatches(fv any, vals []string) bool {
	s := stringifyForFilter(fv)
	return slices.Contains(vals, s)
}

// stringifyForFilter renders a generated field value the same way it would
// appear as a URL query string value, so it can be compared against the
// raw string the client actually sent.
func stringifyForFilter(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case bool:
		return strconv.FormatBool(t)
	case int64:
		return strconv.FormatInt(t, 10)
	case float64:
		if t == math.Trunc(t) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	default:
		b, err := jsonx.Marshal(v)
		if err != nil {
			return ""
		}
		return string(b)
	}
}

// --- the wrapper object: rule 3 -----------------------------------------

// walkWrapperFields generates the wrapper object's OWN fields (schema minus
// the array property, which the ordinary walk would otherwise regenerate
// from scratch — wastefully, since it is about to be discarded, and worse,
// AGAINST THE SHARED BYTE BUDGET before the real, identity-correct items
// slice ever gets a chance to spend it). Split out from the old buildWrapper
// (P1b round-2 review, finding 1) specifically so listPageBody can run this
// BEFORE generateItems: the wrapper's own required properties need to
// forceSpend whatever they really cost while the budget still reflects the
// truth, not after generateItems has already spent it down on an estimate's
// say-so. The result may be nil (a legitimately null wrapper — nullable
// object, nullRate roll) or a non-map value (guarded rather than
// index-panicked on, though detectListShape only ever reaches this for an
// object-shaped schema, so walkSchema handing back a map is the only case
// spliceWrapperPage below is ever asked to handle in practice).
func (w *walker) walkWrapperFields(schema map[string]any, arrName string) (any, error) {
	stripped := schemaWithoutProperty(schema, arrName)
	return w.walkSchema(stripped, "", 0)
}

// spliceWrapperPage splices the real, identity-correct items slice into obj
// (the map walkWrapperFields already generated and charged against the
// budget) plus DESIGN's pagination-bookkeeping overrides: any property named
// total/count/total_count/totalCount is Options.ListSize (never the page
// length); any named limit/offset/page echoes the request's own resolved
// values, rather than whatever walkWrapperFields's ordinary per-property
// scalar generator invented for them. Purely a value substitution — no
// budget accounting here, since walkWrapperFields already paid for
// placeholder values at these same keys and this only overwrites them.
func (w *walker) spliceWrapperPage(obj map[string]any, schema map[string]any, arrName string, items []any, limit, offset, total int) any {
	obj[arrName] = items

	props, _ := schema["properties"].(map[string]any)
	pageNum := 1
	if limit > 0 {
		pageNum = offset/limit + 1
	}
	for name, node := range props {
		if name == arrName {
			continue
		}
		switch lname := strings.ToLower(name); {
		case IsTotalFieldName(lname):
			obj[name] = w.coerceCountLike(node, total)
		case lname == "limit":
			obj[name] = w.coerceCountLike(node, limit)
		case lname == "offset":
			obj[name] = w.coerceCountLike(node, offset)
		case lname == "page":
			obj[name] = w.coerceCountLike(node, pageNum)
		}
	}
	return obj
}

// IsTotalFieldName reports whether lname — a property name already
// lower-cased by the caller — is one of the four spellings DESIGN accepts
// for a wrapper's row-count field. The parameter is named lname, not name,
// deliberately: the comparison is against lower-case literals only, and a
// caller that forgets to lower-case first gets a silent false for a
// property that really does declare a count.
//
// Exported (P3a): resource derivation (internal/specs) picks a wrapper's
// countKey with this exact predicate at import time, so the property
// [spliceWrapperPage] treats as the count at serve time and the property
// derivation stored as countKey are decided by the same rule, not two.
func IsTotalFieldName(lname string) bool {
	switch lname {
	case "total", "count", "total_count", "totalcount":
		return true
	}
	return false
}

// coerceCountLike shapes the pagination integer n according to node's own
// resolved schema type — most of these fields are declared as plain
// integers, but a spec that declares one as a string (a stringified count)
// still gets a schema-conformant value rather than a bare JSON number.
// Unresolvable/absent schema falls back to the ordinary integer form. A
// thin wrapper over [CountValue]: this is where the schema gets resolved
// into a type, the pure shaping itself lives in the exported function.
func (w *walker) coerceCountLike(node any, n int) any {
	countType := ""
	if resolved, err := w.res.ResolveNode(node); err == nil {
		if m, ok := resolved.(map[string]any); ok {
			countType = SchemaType(m)
		}
	}
	return CountValue(n, countType)
}

// CountValue shapes the integer n — a wrapper's row count, or (P3a) a
// resource's stored row count at serve time — according to countType, the
// SAME string [SchemaType] would return for the count property's resolved
// schema. The switch is verbatim what [coerceCountLike] computed inline
// before this slice: "string" gives a decimal string, "number" gives a
// JSON float, and everything else — INCLUDING countType "" (an unresolved
// or untyped count property, [SchemaType] is never called to produce that
// case; see the two callers below) — gives a plain JSON integer.
//
// Exported (P3a) so that a resource's serving path, which stores countType
// once at derivation and has no resolver at serve time, can shape a stored
// row count into the exact wire type [coerceCountLike] would have produced
// for the same property, without a second, driftable copy of this switch.
func CountValue(n int, countType string) any {
	switch countType {
	case "string":
		return strconv.Itoa(n)
	case "number":
		return float64(n)
	default:
		return int64(n)
	}
}

// schemaWithoutProperty is a shallow copy of schema with ONE property
// (propName) removed from its "properties" map — everything else (type,
// required, description, ...) passes through untouched. "required" is
// deliberately left as-is even if it lists propName: walkObject only ever
// iterates schema["properties"]'s own keys, so a required-but-absent-from-
// properties entry is simply never visited, not an error.
func schemaWithoutProperty(schema map[string]any, propName string) map[string]any {
	out := make(map[string]any, len(schema))
	maps.Copy(out, schema)
	props, ok := schema["properties"].(map[string]any)
	if !ok {
		return out
	}
	newProps := make(map[string]any, len(props))
	for k, v := range props {
		if k == propName {
			continue
		}
		newProps[k] = v
	}
	out["properties"] = newProps
	return out
}
