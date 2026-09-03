// Package router builds and matches the single sorted route table DESIGN §8
// describes: one table per runtime holding both spec operations and (from P2)
// custom endpoints, ordered so the most specific match — and, at equal
// specificity, a custom override — always wins.
//
// The package is a leaf: it imports nothing else of ours. In particular it
// never imports internal/mockplane, even though mockplane is the only real
// caller of Match — that direction would be an import cycle, since mockplane
// imports router to serve requests. Match takes pre-split, pre-decoded path
// segments instead of a raw path string precisely so the caller (mockplane,
// via NormalizeSegments) can own request-path normalization and this package
// never needs to.
package router

import (
	"slices"
	"strings"
)

// Route is one entry in the table: either a spec operation (OpRowID > 0,
// Custom false) or, from P2, a custom endpoint (OpRowID 0, Custom true).
type Route struct {
	// OpRowID is specs.Operation.ID, the database row id. It is 0 for a
	// custom endpoint, which has no row in operations.
	OpRowID int64
	// OperationLabel is the OpenAPI operationId, a human label for display
	// only. It may be "" and must NEVER be used as a lookup key — paths
	// change across reimports, but nothing in this package or its callers
	// may key off this field.
	OperationLabel string
	// Method is upper case. HEAD is never stored as its own Route: Match
	// resolves a HEAD request against the GET route instead (DESIGN §8).
	Method string
	// Path is relative — WITHOUT the base path — exactly as stored in
	// operations.path. Build is the only place it gets glued to a base path,
	// so a route's identity never depends on where the workspace happens to
	// mount its API today.
	Path string
	// CanonicalPath is Path with every {param} segment replaced by {},
	// computed once by [CanonicalPath] (by the indexer, for spec operations)
	// and stored rather than recomputed, since it is also the override/
	// conflict key P2 needs at lookup time, not just match time.
	CanonicalPath string
	// Custom marks a P2 custom endpoint. Custom routes win ties against a
	// spec operation at equal specificity (DESIGN §8 rule 3) — the
	// documented way to override a spec route without editing the spec.
	// Nothing sets this true until P2; the comparator branch exists now so
	// ordering behavior does not change out from under an already-built
	// table the day custom endpoints arrive.
	Custom bool
	// SourceOrder is the final tie-break, ascending. SQLite row order is not
	// guaranteed stable across scans, so anything that must reproduce
	// document/insertion order — including two routes with genuinely
	// indistinguishable specificity — has to carry that order explicitly.
	SourceOrder int64
	// CustomRowID is custom_endpoints.id for a custom endpoint and 0 for a
	// spec operation. OpRowID is the mirror image (0 for a custom route), and
	// respond.go keys the declared response variants by OpRowID — so without
	// a separate id every custom route in a workspace would collide on key 0
	// and traffic's matched_id would have nothing to record.
	CustomRowID int64
}

// Match is one successful [Table.Match] result: the Route that matched and
// the path parameters it captured, keyed by parameter name.
type Match struct {
	Route  *Route
	Params map[string]string
}

// segKind distinguishes a literal path segment from a {param} one.
type segKind uint8

const (
	segStatic segKind = iota
	segParam
)

// patternSeg is one segment of a route's glued, split match pattern.
type patternSeg struct {
	kind segKind
	lit  string // the literal text, when kind is segStatic
	name string // the parameter name with braces stripped, when kind is segParam
}

// compiledRoute pairs a Route with the pattern Build derived from it, so
// Match never re-parses a path per request.
type compiledRoute struct {
	route       Route
	segs        []patternSeg
	staticCount int
}

// Table is the immutable, pre-sorted route table a runtime matches requests
// against. It holds no mutex: every field is written once, in Build, and
// never again, so concurrent [Table.Match] calls need no synchronization —
// the same guarantee a plain Go map or slice gives any number of concurrent
// readers as long as nothing is writing.
type Table struct {
	// byMethod groups routes by upper-case HTTP method, each slice already
	// sorted per DESIGN §8's four rules. There is never a "HEAD" key: Match
	// resolves HEAD to the "GET" bucket instead of the table carrying a
	// second, always-identical copy of every GET route.
	byMethod map[string][]compiledRoute
	n        int
}

// Build compiles routes into a Table matched against basePath+Path.
//
// This is THE ONE PLACE the base path is glued to a route's relative path
// (DESIGN §7 step 3: "склейка происходит ровно в одном месте"). Route.Path
// itself stays relative, so a workspace can change its base path, rebuild,
// and every override still applies — its key was never absolute to begin
// with. Build the same routes against two different base paths and both
// tables match correctly; nothing about a Route needs to change.
//
// Build copies every route it is given (Route holds only value fields, so a
// plain copy already fully decouples the table from the caller's slice) and
// keeps no reference to routes itself, so a caller mutating or reusing that
// slice after Build returns cannot reach into the table.
func Build(routes []Route, basePath string) *Table {
	basePath = strings.TrimRight(basePath, "/")

	byMethod := make(map[string][]compiledRoute)
	for _, r := range routes {
		full := basePath + r.Path
		segs, static := compilePattern(full)

		cr := compiledRoute{route: r, segs: segs, staticCount: static}
		cr.route.Method = strings.ToUpper(r.Method)
		byMethod[cr.route.Method] = append(byMethod[cr.route.Method], cr)
	}

	for method, list := range byMethod {
		slices.SortStableFunc(list, compareRoutes)
		byMethod[method] = list
	}

	return &Table{byMethod: byMethod, n: len(routes)}
}

// compareRoutes orders two compiled routes per DESIGN §8, most specific
// first, in exactly the priority Build's doc comment on Table.byMethod
// promises: more static segments, then leftmost static-over-param, then
// custom-over-spec, then ascending SourceOrder.
//
// Two routes that could ever both match the same request necessarily have
// the same total segment count (a literal must match exactly and a {param}
// matches exactly one segment, never zero or two), so comparing staticCount
// and per-position kind is only ever decisive between real competitors.
// Routes of different length may tie all the way through and fall back to
// SourceOrder; that is harmless; since Match filters by segment count before
// ever consulting pattern content, such routes can never be mismatched
// against each other's requests regardless of table order.
func compareRoutes(a, b compiledRoute) int {
	// Rule 1: more static segments wins.
	if a.staticCount != b.staticCount {
		if a.staticCount > b.staticCount {
			return -1
		}
		return 1
	}

	// Rule 2: at equal static-segment count, the leftmost position where one
	// route is static and the other is a param — static wins.
	for i := 0; i < len(a.segs) && i < len(b.segs); i++ {
		aStatic := a.segs[i].kind == segStatic
		bStatic := b.segs[i].kind == segStatic
		if aStatic != bStatic {
			if aStatic {
				return -1
			}
			return 1
		}
	}

	// Rule 3: at equal specificity, a custom endpoint outranks a spec
	// operation — the documented override (DESIGN §8), not a conflict.
	if a.route.Custom != b.route.Custom {
		if a.route.Custom {
			return -1
		}
		return 1
	}

	// Rule 4: SQLite row order is not guaranteed stable, so the explicit
	// source_order is the only thing that can reproduce document order.
	if a.route.SourceOrder != b.route.SourceOrder {
		if a.route.SourceOrder < b.route.SourceOrder {
			return -1
		}
		return 1
	}

	return 0
}

// compilePattern splits a glued path into match segments and reports how
// many of them are static, for [compareRoutes]'s rule 1. It reuses
// [isParamSegment] — the same predicate [CanonicalPath] uses — so a segment
// is a route parameter here if and only if canonicalizing the same path
// would have turned it into "{}"; the two can never drift apart into
// disagreeing about what counts as a parameter.
func compilePattern(full string) ([]patternSeg, int) {
	raw := strings.Split(full, "/")
	segs := make([]patternSeg, 0, len(raw))
	static := 0
	for _, s := range raw {
		if s == "" {
			continue // collapses "//" and drops a leading/trailing "/"
		}
		if isParamSegment(s) {
			segs = append(segs, patternSeg{kind: segParam, name: s[1 : len(s)-1]})
			continue
		}
		segs = append(segs, patternSeg{kind: segStatic, lit: s})
		static++
	}
	return segs, static
}

// Match finds the highest-priority route matching method and segments.
// segments must already be normalized and percent-decoded — the caller's
// job (mockplane.NormalizeSegments), never this package's, so router stays a
// leaf. A HEAD request is matched against the table's GET routes with an
// otherwise identical result (DESIGN §8: "HEAD матчится как GET"); the
// caller is still responsible for suppressing the response body, same as
// mockplane's headWriter already does for the reserved endpoints.
//
// Because byMethod's slices were sorted once in Build, Match only has to
// return the first candidate whose segment count and literal segments agree
// with the request — that candidate is, by construction, the most specific
// one, or the custom override, or whichever SourceOrder rule 4 picked.
func (t *Table) Match(method string, segments []string) (*Match, bool) {
	method = strings.ToUpper(method)
	if method == "HEAD" {
		method = "GET"
	}

	list, ok := t.byMethod[method]
	if !ok {
		return nil, false
	}

	for i := range list {
		cr := &list[i]
		if len(cr.segs) != len(segments) {
			continue // a {param} matches exactly one segment, never across "/"
		}
		params, matched := matchSegments(cr.segs, segments)
		if !matched {
			continue
		}
		route := cr.route // copy: never hand out a pointer into the table's own storage
		return &Match{Route: &route, Params: params}, true
	}
	return nil, false
}

// matchSegments compares a compiled pattern against request segments of the
// same length, collecting {param} values as it goes. It returns ok=false as
// soon as a static segment disagrees, without allocating a Params map for a
// route that was never going to match.
func matchSegments(pattern []patternSeg, segments []string) (map[string]string, bool) {
	var params map[string]string
	for i, ps := range pattern {
		switch ps.kind {
		case segStatic:
			if ps.lit != segments[i] {
				return nil, false
			}
		case segParam:
			if params == nil {
				params = make(map[string]string, len(pattern))
			}
			params[ps.name] = segments[i]
		}
	}
	if params == nil {
		params = map[string]string{}
	}
	return params, true
}

// Len reports how many routes the table holds.
func (t *Table) Len() int { return t.n }

// HasCanonical reports whether the table holds a route for method whose
// CanonicalPath equals canonicalPath. canonicalPath is compared in the same
// RELATIVE form Route.CanonicalPath itself is stored in — the base path
// Build glues on only ever touches a compiled route's match pattern, never
// route.CanonicalPath, so this needs no basePath argument and a caller never
// has to strip one back off before calling it.
//
// P1b's response generator (gen.Request.ListFamily) uses this to recognize a
// detail route's sibling LIST route: strip a detail route's trailing {}
// segment to get a candidate canonical path, then ask the table whether that
// candidate is itself a real route. That knowledge — which canonical paths
// exist in THIS workspace's table — belongs to router, the one place that
// already has it; duplicating it in mockplane or gen would give route
// families a second, driftable definition.
//
// A HEAD request resolves to the table's GET bucket, exactly like Match,
// since HEAD is never stored as its own route (DESIGN §8).
func (t *Table) HasCanonical(method, canonicalPath string) bool {
	method = strings.ToUpper(method)
	if method == "HEAD" {
		method = "GET"
	}
	for _, cr := range t.byMethod[method] {
		if cr.route.CanonicalPath == canonicalPath {
			return true
		}
	}
	return false
}

// CanonicalPath is THE one implementation of DESIGN §8's canonical path:
// every whole-segment {param} becomes {}, static segments are untouched.
// The specs indexer calls this to fill operations.canonical_path; nothing
// else may reimplement the rule, because canonical_path is also the P2
// override/conflict key and two implementations would eventually drift on
// an edge case neither side noticed.
func CanonicalPath(path string) string {
	segs := strings.Split(path, "/")
	for i, seg := range segs {
		if isParamSegment(seg) {
			segs[i] = "{}"
		}
	}
	return strings.Join(segs, "/")
}

// ListFamily computes [gen.Request.ListFamily]: route is a detail route of
// some list family exactly when its canonical path ends in the {} segment
// CanonicalPath leaves behind for a path parameter, AND the prefix left
// after stripping that segment is itself a route in table, on the same
// method. table is the one place that already knows which canonical paths
// exist in this workspace (HasCanonical's own doc comment); duplicating
// that knowledge here would give route families a second, driftable
// definition.
//
// Exported (P3a) so that resource derivation, which runs at spec import
// where no [Table] built from live workspace state exists yet, can call the
// same rule over a table it builds itself from the spec's own operations
// (router.Build(routes, "")) instead of maintaining a second copy —
// [internal/mockplane] becomes a caller instead of the sole owner.
func ListFamily(table *Table, route *Route) string {
	const paramSuffix = "/{}"
	if !strings.HasSuffix(route.CanonicalPath, paramSuffix) {
		return ""
	}
	prefix := strings.TrimSuffix(route.CanonicalPath, paramSuffix)
	if prefix == "" || !table.HasCanonical(route.Method, prefix) {
		return ""
	}
	return prefix
}

// DetailIDParam computes [gen.Request.IDParam]: the path-parameter NAME of
// route's own trailing {name} segment, read off route.Path's real, ordered
// pattern — never guessed from the unordered PathParams map gen.Request
// also carries. That distinction matters for a route with two or more
// path parameters that both LOOK like an id by name: e.g.
// GET /tenants/{tenantId}/cohorts/{cohortId}, where a name-only
// heuristic ("ends in Id") cannot tell "cohortId" (this route's own
// resource) apart from "tenantId" (the outer, shared, list-family
// parameter) — but the ORDER can, trivially: the id is always whichever
// segment is last, which is exactly the segment CanonicalPath's trailing
// "{}" stands for (ListFamily's own premise). "" when route's last path
// segment is not a {param} at all (route isn't a detail route to begin
// with, or ListFamily already declined it).
//
// Exported (P3a) alongside ListFamily, for the same reason: resource
// derivation needs it before a live [Table] exists.
func DetailIDParam(route *Route) string {
	segs := strings.Split(route.Path, "/")
	if len(segs) == 0 {
		return ""
	}
	last := segs[len(segs)-1]
	if len(last) < 2 || last[0] != '{' || last[len(last)-1] != '}' {
		return ""
	}
	return last[1 : len(last)-1]
}

// ParentFamily computes D4.1's derivation rule and D3's scope resolution: the
// prefix of family ending immediately before family's LAST "{}" segment, or
// "" when family carries none. /orgs/{}/users → /orgs.
// /orgs/{}/teams/{}/users → /orgs/{}/teams. /items → "". Unlike ListFamily,
// this takes no *Table and proves nothing about what routes actually exist —
// it is a pure string operation over an already-computed family, because its
// two callers each already hold that guarantee a different way: derivation's
// second pass checks the result against the FIRST pass's own derived set (a
// family this pass already derived is, by construction, a real route table
// entry), and a nested request's scope resolution checks the parent's
// EntityStore directly (D6.3) rather than trusting the string.
//
// Exported (P3e) alongside ListFamily and DetailIDParam, for the reason
// those two already state: this package is the one place canonical-path
// knowledge is owned, and the string is needed at three separate times —
// derivation, confirm and every nested request — with no place to persist
// it (D4.2: a stored parent_family would be a second source of truth for a
// value that is a pure function of the row's own route_family).
func ParentFamily(family string) string {
	segs := strings.Split(family, "/")
	last := -1
	for i, seg := range segs {
		if seg == "{}" {
			last = i
		}
	}
	if last < 0 {
		return ""
	}
	return strings.Join(segs[:last], "/")
}

// BaseParamIndexes returns the indexes, within basePath's own segments, of
// its {name} parameter segments, their names in the same order, and whether
// the base path's brace shape is VALID at all — false for an unbalanced
// brace, a brace that does not span a whole segment, or an empty name. It is
// the ONE owner of "which segments of a base path are parameters": the base
// scope reader, stripBasePath, authCheckPath and the Settings validator
// (D4.3) all read it, and the third return is what keeps that validator from
// scanning braces itself. On an invalid shape the first two returns are
// meaningless and no caller may use them. (D4.3, D7.1, D12)
//
// A segment counts as a parameter by the IDENTICAL predicate compilePattern
// already applies, [isParamSegment] — not a fresh scan of the same braces,
// which is exactly the second reader the one-owner rule exists to prevent.
// Segments are split the same way compilePattern splits a glued pattern —
// on "/", with empty segments dropped, so "/orgs/{orgId}/" and
// "//orgs/{orgId}" index identically to what Build actually compiles; a
// disagreement here would put a base value at the wrong offset for every
// caller downstream of Build.
func BaseParamIndexes(basePath string) (idx []int, names []string, valid bool) {
	for i, seg := range splitBaseSegments(basePath) {
		if isParamSegment(seg) {
			name := seg[1 : len(seg)-1]
			if name == "" {
				return nil, nil, false // "{}" — a brace pair with an empty name
			}
			idx = append(idx, i)
			names = append(names, name)
			continue
		}
		if strings.ContainsAny(seg, "{}") {
			// A brace appears but isParamSegment declined it: either the pair
			// does not span the whole segment ("v{n}") or it has no match at
			// all ("{orgId", "orgId}"). Either way the shape is invalid, not
			// a literal segment that happens to contain a brace character.
			return nil, nil, false
		}
	}
	return idx, names, true
}

// BaseValues returns the base tuple of a request whose matched segments are
// segments, positionally: basePath's segments are the leading segments of
// every pattern Build compiles, so value i is segments[idx[i]] for the idx
// [BaseParamIndexes] returns. It returns false when len(segments) is shorter
// than basePath's own segment count — a state a successful [Table.Match]
// makes impossible, checked here because the alternative is an index panic
// on a plane that is unauthenticated by design.
func BaseValues(basePath string, segments []string) ([]string, bool) {
	idx, _, valid := BaseParamIndexes(basePath)
	if !valid {
		return nil, false
	}
	baseSegCount := len(splitBaseSegments(basePath))
	if len(segments) < baseSegCount {
		return nil, false
	}
	values := make([]string, len(idx))
	for i, at := range idx {
		values[i] = segments[at]
	}
	return values, true
}

// splitBaseSegments splits basePath the same way compilePattern splits a
// glued full pattern: on "/", dropping every empty segment. That single
// definition is what keeps a trailing slash or a doubled "//" in basePath
// from shifting a parameter's index relative to what Build actually
// compiles — see [BaseParamIndexes]'s own doc comment.
func splitBaseSegments(basePath string) []string {
	raw := strings.Split(basePath, "/")
	segs := make([]string, 0, len(raw))
	for _, s := range raw {
		if s == "" {
			continue
		}
		segs = append(segs, s)
	}
	return segs
}

// isParamSegment reports whether seg is a whole-segment path parameter such
// as "{id}" — never "prefix{id}" or "{id}suffix". DESIGN §8 says a parameter
// matches exactly one segment; that only means something if the brace pair
// spans the entire segment, so a brace pair that does not is treated as
// literal text instead of silently matching more than the spec describes.
func isParamSegment(seg string) bool {
	return len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}'
}

// Conflicts reports every canonical-path collision between two CUSTOM
// routes (DESIGN §8: "два кастомных endpoint'а с одинаковым canonical_path —
// конфликт, второй создать нельзя"). A custom route that canonically equals
// a spec operation is deliberately excluded — that pairing is the documented
// override, not a conflict — so Conflicts only ever groups routes with
// Custom set, never comparing a custom route against a spec one.
//
// Grouping is per (Method, CanonicalPath): two custom endpoints on different
// methods at the same path are two independent endpoints, not a clash.
// Results are sorted for a deterministic, reproducible report.
func Conflicts(routes []Route) []string {
	type key struct{ method, canonical string }
	groups := make(map[key][]Route)
	for _, r := range routes {
		if !r.Custom {
			continue
		}
		k := key{strings.ToUpper(r.Method), r.CanonicalPath}
		groups[k] = append(groups[k], r)
	}

	keys := make([]key, 0, len(groups))
	for k, rs := range groups {
		if len(rs) > 1 {
			keys = append(keys, k)
		}
	}
	slices.SortFunc(keys, func(a, b key) int {
		if c := strings.Compare(a.method, b.method); c != 0 {
			return c
		}
		return strings.Compare(a.canonical, b.canonical)
	})

	out := make([]string, 0, len(keys))
	for _, k := range keys {
		out = append(out, k.method+" "+k.canonical)
	}
	return out
}
