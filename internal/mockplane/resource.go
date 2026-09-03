// resource.go is P3a's whole per-request switch (DESIGN §19, D6/D7/D8 of the
// mocker-p3a-resources decisions): whether a matched route belongs to a
// CONFIRMED resource family, and if so, whether the verb being served is one
// of the (up to) four this slice takes over — GET X, GET X/{}, POST X,
// DELETE X/{}.
//
// [ResourceSource] and [EntityStore] are the two database-facing seams
// [Plane] holds as interfaces, never a concrete *resources.Repo — the same
// "database dependency arrives as an interface plus a setter" contract
// [CustomSource]/[Plane.SetCustomEndpoints] already established. The split
// mirrors the two different places D6 says the two kinds of data live:
// ResourceSource is read ONCE PER RUNTIME BUILD, under the existing
// (workspace_id, revision) cache key buildRuntime already keys everything
// else on (runtime.go); EntityStore is read PER REQUEST, through the reader
// pool for List/Get and the single writer connection for Create/Delete —
// entities and the resource's own seq counter are NEVER in the runtime
// (D6 R17), because a runtime this cheap to rebuild-and-cache must not also
// carry a snapshot of rows a concurrent write elsewhere could make stale
// the instant it is taken.
package mockplane

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// ResourceSource is *resources.Repo as the plane needs it: every CONFIRMED
// resource of a workspace, in one query, exactly the shape
// [CustomSource.ForWorkspace] already established — this package never has
// to import the concrete resources.Repo type, and a test can fake the one
// method buildRuntime actually needs with no database at all.
//
// buildRuntime (runtime.go) keys the result by RouteFamily, under the
// SAME (workspace_id, revision) cache key every other source already
// shares — D6 R18: the decision route (confirm/decline) bumps revision on
// both transitions, so a stale-looking runtime after an operator's decision
// is an ordinary cache miss on the next request, never a bug to "fix" from
// inside this file.
type ResourceSource interface {
	ForWorkspace(ctx context.Context, workspaceID int64) ([]*resources.Resource, error)
}

// EntityStore is *resources.Repo's OTHER four methods — the ones a request
// reads and writes through directly, never via the cached runtime (D6 R34).
// The types are the other package's, matching every other source this
// package already holds behind an interface (CustomSource's *customep.Row,
// OverrideSource's *overrides.Row, ...).
// Every method takes base AHEAD of scope (D18.2's own order): base is P3h's
// SECOND axis, the request's own base-path parameter values (D3), computed
// once in serveRoute and threaded through resourceBranch — never a fourth
// level of the nesting scope route already has (D3.2 names four reasons the
// two axes stay independent).
type EntityStore interface {
	List(ctx context.Context, resourceID int64, base, scope resources.ScopeKey) ([]resources.Entity, error)
	Get(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (resources.Entity, bool, error)
	Create(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, idField, idType string, data map[string]any) (resources.Entity, error)
	Delete(ctx context.Context, resourceID int64, base, scope resources.ScopeKey, entityKey string) (bool, error)
}

// The real store: *resources.Repo has to satisfy BOTH seams above, since
// [ResourceSource] and [EntityStore] together are the whole of what this
// package needs from that package (this file's own doc comment). These two
// lines are the wiring check neither cmd/mocker/main.go's assembly nor any
// test that only exercises a hand-rolled fake would otherwise ever catch —
// the package compiled green under exactly that gap once already: nothing
// short of a build-time assertion notices that *resources.Repo grew the
// four EntityStore methods but never a ForWorkspace of its own, because
// nothing in this tree assigns the concrete type to either interface-typed
// variable.
var (
	_ ResourceSource = (*resources.Repo)(nil)
	_ EntityStore    = (*resources.Repo)(nil)
)

// lookupResource reports whether route belongs to a confirmed resource, and
// whether route is the family's DETAIL route (GET/DELETE X/{}) or its
// COLLECTION route (GET/POST X).
//
// The collection case is checked FIRST, by a direct key lookup on the
// route's own canonical path: a resource's RouteFamily IS the collection's
// canonical path (D2), so a route matching it exactly needs no further
// computation at all.
//
// The detail case deliberately does NOT call [router.ListFamily]: that
// helper's own contract requires a collection route on the SAME METHOD as
// the route it is asked about (table.HasCanonical(route.Method, prefix)) —
// exactly right for the GET-only walk resource derivation performs
// (locateFamilyOperations, internal/resources/repo.go, filters to GET
// before ever calling it), and exactly wrong for DELETE X/{}: a spec
// declares DELETE only on the DETAIL route, never on the collection, so
// table.HasCanonical("DELETE", prefix) is false for every real family and
// ListFamily would silently return "" for the one verb D7 says is NOT
// gated on write_form. What actually decides "is this route part of a
// confirmed family" is membership in rt.resources itself — a resource
// exists there only because a live GET collection route was present at
// DERIVATION time (D6's own premise) — so re-verifying the table's current
// method shape here would be redundant for GET and wrong for DELETE. The
// suffix trim below is the one piece of [router.ListFamily]'s rule that
// still applies regardless of method: CanonicalPath's trailing "{}" is
// exactly what [router.CanonicalPath] leaves behind for a path parameter
// (R2 guarantees exactly one), the same fact [router.DetailIDParam]'s own
// doc comment leans on.
func lookupResource(rt *runtime, route *router.Route) (res *resources.Resource, isDetail bool) {
	if r, ok := rt.resources[route.CanonicalPath]; ok {
		return r, false
	}
	const paramSuffix = "/{}"
	if !strings.HasSuffix(route.CanonicalPath, paramSuffix) {
		return nil, false
	}
	family := strings.TrimSuffix(route.CanonicalPath, paramSuffix)
	if r, ok := rt.resources[family]; ok {
		return r, true
	}
	return nil, false
}

// scopeOf computes the request's scope from the pattern of the route that
// MATCHED — never by name out of res.ScopeParams (D5.6). route.Path's own
// templated pattern names every whole-segment {param} IN ORDER, positionally,
// the same read [router.DetailIDParam] already makes off the same field; when
// isDetail is true the LAST of those names is the entity key, not a scope
// segment, and is dropped. Reading by NAME instead — matching res.ScopeParams
// against m.Params — would miss whenever the collection route and the detail
// route spell the same outer parameter differently: router.CanonicalPath
// erases the name, so the two routes are one family, but scope_params stores
// only the detail route's spelling (D5.6). A by-name miss yields an empty
// string that still passes the arity check below and serves the family HALF,
// silently — acceptance property P15 exists to observe exactly that.
//
// It returns the encoded [resources.ScopeKey], the same values RAW and in
// order (unescaped — D6.3's anchor check compares a parent's entity_key
// against the raw string its own detail route stored it as, never against
// the escaped form EncodeScope produces for the child's own key), and ok,
// which is false ONLY on the arity cross-check: len(outer) != len(scope
// params the family was CONFIRMED with). In production that is a family
// confirmed against one spec and served under another whose route shape
// changed — a re-bind — and the caller declines to the generator rather than
// serving one scope's rows under another scope's request.
//
// For a family with no outer parameter at all, outer is empty, the arity
// check passes trivially (0 == len(res.ScopeParams) for every row this build
// writes before P3e), and the returned scope is "" — every existing
// behaviour stays bit-identical (D6.1).
func scopeOf(res *resources.Resource, route *router.Route, m *router.Match, isDetail bool) (resources.ScopeKey, []string, bool) {
	segs := strings.Split(route.Path, "/")
	names := make([]string, 0, len(segs))
	for _, seg := range segs {
		if len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			names = append(names, seg[1:len(seg)-1])
		}
	}
	if isDetail && len(names) > 0 {
		names = names[:len(names)-1]
	}
	if len(names) != len(res.ScopeParams) {
		// `names`, not nil: the only caller reads len(outer) for its Warn line
		// on this path and never touches the values, and a nil here made that
		// line report routeParams=0 for every mismatch regardless of the
		// route's real outer-parameter count — the one number an operator
		// diagnosing a re-bind needs from it.
		return "", names, false
	}
	outer := make([]string, len(names))
	for i, name := range names {
		outer[i] = m.Params[name]
	}
	return resources.EncodeScope(outer), outer, true
}

// ancestorFamilies returns family's ancestor chain, ordered TOP DOWN: index
// 0 is the depth-0 root, index len-1 is the immediate parent (D3.3's own
// vocabulary) — the order the anchor walk needs, because ancestor i's own
// scope is the PREFIX outer[:i] (D6.2), and that indexing only lines up when
// index 0 is the root.
//
// [router.ParentFamily] strips exactly one "{}" segment per call, walking
// TOWARD the root — the first call from a depth-d family yields the
// immediate parent (depth d-1), the next the grandparent (depth d-2), and so
// on down to the root at the d-th call. depth is len(outer) (already proven
// equal to len(res.ScopeParams) by scopeOf's own arity check before this is
// ever called), so filling the result BACKWARDS while walking forwards
// reverses that bottom-up walk into the top-down order this function
// promises, with no second pass and no separate reverse step.
func ancestorFamilies(family string, depth int) []string {
	chain := make([]string, depth)
	cur := family
	for i := depth - 1; i >= 0; i-- {
		cur = router.ParentFamily(cur)
		chain[i] = cur
	}
	return chain
}

// isSyntheticSelector reports whether selector is NOT a genuine numeric
// selector of a spec-declared response — either one of
// [gen.ResponseVariant]'s two non-literal forms, "2XX" or "default" (the
// two D6 rule 5's write-verb numeric conjunct names explicitly:
// classifySelector maps both to HTTP 200, markDefault picks "default" when
// no numeric 2xx exists, and in a real spec that response is almost always
// an ERROR schema, so the media-type test alone would let a DELETE answer
// the deleted entity under an error contract), or the EMPTY string.
//
// The empty case is what makes this catch resolveVariant's OWN third kind
// of non-literal variant, not just the two named ones: its synthetic
// fallback for a forced status the document never declares a response for
// (respond.go:373, `gen.ResponseVariant{OpRowID: route.OpRowID, HTTPStatus:
// status}`) leaves every field but OpRowID/HTTPStatus at its zero value,
// so Selector there is "" — never a value a real declared row can carry.
// specs.Repo.Variants (internal/specs/repo.go, the Variants query) scans
// operation_responses.selector into a plain (non-nullable) Go string, and
// the column itself is NOT NULL, so every row this tree's own derivation
// ever produces names an actual selector: "200", "2XX", "default", or any
// other literal status the spec declares — never "". A write forced (by a
// live effect OR by active_status/when[]) to a status the operation never
// declares must fail this test the same way "2XX"/"default" already do, or
// D6 rule 5's write-verb conjuncts silently stop applying to exactly the
// case they exist for.
func isSyntheticSelector(selector string) bool {
	return selector == "" || selector == "2XX" || selector == "default"
}

// writeEntityNotFound is R22's own 404 — the branch's OWN answer, never
// [Plane.serveNoRoute] (whose body lies "no route for GET /quizzes/99" when
// the route plainly exists) and never settings.NotFoundBody (that setting
// is the operator's answer for an UNROUTED path, not a missing row under a
// route that matched).
func writeEntityNotFound(w http.ResponseWriter, route *router.Route, key string) {
	httpx.Err(w, http.StatusNotFound, "entity_not_found", fmt.Sprintf(
		"%s %s has no entity %q", route.Method, route.CanonicalPath, key))
}

// envelopeOverhead is D7's own arithmetic, not a second copy of
// wrapEnvelope's rule: the exact number of bytes [wrapEnvelope] would ADD to
// a collection body if this call let it reach assembleResponse —
// len(jsonx.Marshal(key)) (the marshalled key already carries its own
// quotes AND stdlib's HTML escaping of <, >, & at six bytes each) plus 3 for
// the wrapping object's own `{`, `:` and `}`. Zero when no envelope is
// configured, since assembleResponse then does nothing to the body at all.
func envelopeOverhead(rt *runtime) (int64, error) {
	if rt.settings.Envelope == nil {
		return 0, nil
	}
	keyBytes, err := jsonx.Marshal(*rt.settings.Envelope)
	if err != nil {
		return 0, err
	}
	return int64(len(keyBytes)) + 3, nil
}

// marshalCollection is R6's wrapper rule, applied to entities already
// fetched: a nil ArrayKey means a bare array 200 (nothing else); a non-nil
// one means {<arrayKey>: rows} plus {<countKey>: count} when CountKey is
// also set, AND NOTHING ELSE — every other property the generator used to
// echo (limit, offset, page) disappears at confirm, D14's own carve-out.
// The count value is [gen.CountValue], never a bare Go int (R39/clause 49):
// a collection whose count property declares no type at all serves a
// STRINGIFIED total, exactly as the generator served it before confirm.
func marshalCollection(res *resources.Resource, entities []resources.Entity) ([]byte, error) {
	// Rows are appended through Compact rather than marshaled as a
	// []RawMessage: the encoder compacts each RawMessage anyway, so the
	// bytes are identical, but it did so through reflection over a slice
	// of up to MOCKER_MAX_ENTITIES rows on every GET of the collection.
	var buf bytes.Buffer
	total := 2
	for _, e := range entities {
		total += len(e.Data) + 1
	}
	buf.Grow(total + 64)
	if res.Wrapper.ArrayKey != nil {
		keyBytes, err := jsonx.Marshal(*res.Wrapper.ArrayKey)
		if err != nil {
			return nil, err
		}
		buf.WriteByte('{')
		buf.Write(keyBytes)
		buf.WriteByte(':')
	}
	buf.WriteByte('[')
	for i, e := range entities {
		if i > 0 {
			buf.WriteByte(',')
		}
		if err := jsonx.Compact(&buf, e.Data); err != nil {
			return nil, fmt.Errorf("mockplane: entity %q of %s is not valid JSON: %w", e.EntityKey, res.RouteFamily, err)
		}
	}
	buf.WriteByte(']')
	if res.Wrapper.ArrayKey == nil {
		return buf.Bytes(), nil
	}
	if res.Wrapper.CountKey != nil {
		keyBytes, err := jsonx.Marshal(*res.Wrapper.CountKey)
		if err != nil {
			return nil, err
		}
		countBytes, err := jsonx.Marshal(gen.CountValue(len(entities), res.Wrapper.CountType))
		if err != nil {
			return nil, err
		}
		buf.WriteByte(',')
		buf.Write(keyBytes)
		buf.WriteByte(':')
		buf.Write(countBytes)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// parsePostBody re-decodes the ONE capture [captureRequestBody] already
// performed (reqbody.go), read back through [capturedBodyFromContext] —
// never a second read of r.Body, which that file forbids.
//
// cb.parseOK gates content type AND truncation for free: [tryParseBody] is
// only ever attempted when the capture did NOT truncate, and only for
// application/json, text/plain or no Content-Type at all — exactly R23's
// list of "writes nothing, no 400" cases (malformed, oversized, a
// Content-Type the capture does not parse). What THIS function adds on top
// is R38: it never uses cb.parsed, which [tryParseBody] built through
// encoding/json's own `any` decode and which turns every number into a
// float64, losing precision past 2^53 — jsonx.NewDecoder(...).UseNumber()
// over the SAME bytes keeps the literal. ok is false for anything that is
// not a JSON OBJECT once decoded (a scalar, an array, null) — R14's own
// "the request body parsed as a JSON object" condition.
func parsePostBody(r *http.Request) (map[string]any, bool) {
	cb := capturedBodyFromContext(r)
	if cb == nil || !cb.parseOK {
		return nil, false
	}
	dec := jsonx.NewDecoder(bytes.NewReader(cb.bytes))
	dec.UseNumber()
	var v any
	if err := dec.Decode(&v); err != nil {
		return nil, false
	}
	obj, ok := v.(map[string]any)
	return obj, ok
}

// resourceBranch is D6's whole per-request switch. It sits between
// resolveVariant/the 406 Accept gate and assembleResponse (D6 R19) — every
// caller passes the SAME rv the 406 gate already cleared — so route_off,
// livestate.Apply, the pause, the delay and the 406 gate itself are all
// inherited unchanged, and a POST that would have answered 406 never
// reaches here to create a row.
//
// It NEVER calls assembleResponse itself: [seam_test.go]'s
// TestAssembleResponseIsTheOnlySeam pins that function's caller set to
// exactly {serveGenerated, Preview}, and this is a HELPER of the first one,
// not a third caller. handled reports which of R19's five exits fired:
// true means this function already wrote the ENTIRE response (the 404, the
// entity_limit/write_busy refusal, or collection_too_large — all three
// written BEFORE any body could reach assembleResponse, and none of them
// through [domain.RefusedReason], whose own doc comment calls it a CLOSED
// vocabulary the admin route and the UI panel switch on by exact string,
// and whose consumer answers the variant's status with a NIL body — on a
// collection route that reads as "no rows", exactly the wrong lie); false
// means the caller must still call assembleResponse with the returned rv,
// which for a successful takeover carries rv.PreBuilt (R20's fourth body
// source) and for every other case — no resource, wrong verb, precedence
// lost to rule 4's "not 2xx" or "pinned" — is exactly what it was handed,
// the fifth exit, a fall-through that changes nothing.
func (p *Plane) resourceBranch( //nolint:gocyclo // D6's ordered per-request switch: the precedence guards, the D6.1 scope/arity computation, the D6.2 anchor check and the four-way verb dispatch are each one clause of the specification, not an accidental branch to split away
	w http.ResponseWriter,
	r *http.Request,
	ws *workspaces.Workspace,
	rt *runtime,
	route *router.Route,
	m *router.Match,
	base resources.ScopeKey,
	rv resolved,
	liveEffect livestate.Effect,
) (resolved, bool) {
	if p.resources == nil || p.entities == nil || len(rt.resources) == 0 {
		return rv, false
	}

	res, isDetail := lookupResource(rt, route)
	if res == nil {
		return rv, false
	}

	// D6 rule 4: a variant that is not 2xx, or one the row PINS, is never
	// consulted at all — nothing is written, and the pinned body or the
	// generator answers exactly as if no resource had ever been confirmed
	// for this route.
	pinned := rv.Override != nil && rv.Override.Mode == "pinned"
	if pinned || rv.Status < 200 || rv.Status >= 300 {
		return rv, false
	}

	// D6 rule 5's media-type test: [httpx.IsJSONMediaType] admits the empty
	// media type unconditionally (its own doc comment: "empty — the
	// generator's own fallback"), which is right for gen.Body but wrong
	// here — D7 admits the empty type ONLY when the variant is also NoBody.
	// A 2xx that is neither JSON-shaped nor NoBody-with-no-declared-type
	// takes no takeover at all: no body, and (on a write verb) no write.
	if !httpx.IsJSONMediaType(rv.MediaType) || (rv.MediaType == "" && !rv.NoBody) {
		return rv, false
	}

	// D7.3: the base-scope MEMBERSHIP check sits here, right after rule 5
	// and right before the route-scope computation below — never ahead of
	// rule 4/5, which would let an undeclared base value's 404 override a
	// pinned response or a session-forced non-2xx status the branch was
	// never meant to reach for (P3a's own invariant for this branch: "a
	// forced 200 on GET X/{99} still answers this branch's own 404" cuts
	// the other way too — a genuinely pinned or non-2xx variant is never
	// consulted at all). base was computed ONCE in serveRoute, positionally
	// off the request's own segments (D7.1) — never re-derived here, and
	// never read out of m.Params by name (D4.4). rt.settings, not
	// ws.Settings: D4.5 makes BasePathValues the FOURTH field
	// composeScenarioLayer restores from the workspace (beside basePath,
	// CORS and notFoundBody), so rt.settings.BasePathValues already IS the
	// workspace's own declared set whether or not a scenario is active —
	// reading it here is what makes that restore line load-bearing rather
	// than a write nothing downstream observes (P22).
	declared := resources.DeclaredBaseScopes(rt.settings.BasePath, rt.settings.BasePathValues)
	if !slices.Contains(declared, base) {
		writeEntityNotFound(w, route, string(base))
		return rv, true
	}

	// D6.1: the scope is computed ONCE, from the route that matched, never
	// re-derived per verb — scopeOf's own doc comment carries the positional
	// rule (D5.6). ok is false only on the arity cross-check: a family
	// confirmed against one spec and served under a re-bound one whose route
	// shape changed. The generator answers, as every other declined case
	// does, rather than the plane serving one scope's rows under another
	// scope's request.
	scope, outer, ok := scopeOf(res, route, m, isDetail)
	if !ok {
		p.log.Warn("resource scope arity mismatch", "workspace", ws.Slug, "routeFamily", res.RouteFamily,
			"routeParams", len(outer), "confirmedParams", len(res.ScopeParams))
		return rv, false
	}

	// D6.2: a scope no LIVE ancestor row anchors answers 404, before
	// anything is read or written — on all four verbs, so a POST/DELETE
	// cannot mint or remove a row in a scope whose ancestor a concurrent
	// request already deleted. Armed on len(outer) > 0 (what THIS REQUEST
	// carried), never on len(res.ScopeParams) > 0 (what the stored row
	// claims): the two agree on every request that passes the arity check
	// above and disagree only where it fails, which is exactly the case
	// the arity check already declined to above — so this must read the
	// request's own outer values, not the resource's configuration, or
	// the arity check above would be unobservable (D6.1).
	//
	// The walk goes TOP DOWN over the whole ancestor chain — not the
	// immediate parent alone (P3e's own shape, and the wrong implementation
	// a reader is most likely to write at depth): a live immediate-parent
	// row says nothing about whether the GRANDparent row that scopes IT
	// still exists, so checking only the last hop lets a scope orphaned
	// above the parent keep serving — the resurrection
	// DESIGN.md:508-511#parent_entity_id warns about, arriving through the
	// depth this slice adds. ancestorFamilies gives the chain in that
	// order; ancestor i's own scope is the PREFIX outer[:i] (D3.3), which
	// is arithmetic on values this request already carries, never a read
	// of a stored scope_key. The walk refuses at the FIRST miss and names
	// the OUTERMOST missing id — the true cause: with /orgs/7 deleted,
	// GET /orgs/7/teams/5/users says entity "7" is missing, not "5" — a
	// message naming the innermost key would send an operator looking for
	// a team that may well exist.
	if len(outer) > 0 {
		for i, ancestorFamily := range ancestorFamilies(res.RouteFamily, len(outer)) {
			ancestor, ancestorOK := rt.resources[ancestorFamily]
			if !ancestorOK {
				// D5.2: every ancestor of a confirmed family is confirmed,
				// so a roster miss here is an invariant violation, not a
				// case this branch serves an answer for — logged and
				// declined, the same shape R37 already has for "the
				// resource this route belongs to is gone".
				p.log.Warn("nested resource ancestor missing from roster", "workspace", ws.Slug,
					"routeFamily", res.RouteFamily, "ancestorFamily", ancestorFamily)
				return rv, false
			}
			// outer[:i] and outer[i], both raw and unescaped — the same
			// values scopeOf already extracted positionally, compared
			// against entity_key as the string the ancestor's own detail
			// route stored it as. Only the prefix is ever passed through
			// EncodeScope, to look the ancestor's OWN scope up — never
			// the key itself, which is compared as scopeOf returned it.
			ancestorScope := resources.EncodeScope(outer[:i])
			ancestorKey := outer[i]
			_, anchored, gerr := p.entities.Get(r.Context(), ancestor.ID, base, ancestorScope, ancestorKey)
			if gerr != nil {
				if errors.Is(gerr, resources.ErrResourceGone) {
					return rv, false // R37: an ancestor declined out from under a request in flight
				}
				p.log.Warn("get ancestor entity for anchor check", "workspace", ws.Slug,
					"routeFamily", res.RouteFamily, "ancestorFamily", ancestorFamily, "err", gerr)
				return rv, false
			}
			if !anchored {
				writeEntityNotFound(w, route, ancestorKey)
				return rv, true
			}
		}
	}

	switch {
	case !isDetail && route.Method == http.MethodGet:
		return p.resourceServeCollection(w, r, ws, rt, route, res, base, scope, rv)
	case isDetail && route.Method == http.MethodGet:
		return p.resourceServeDetail(w, r, ws, route, m, res, base, scope, rv)
	case !isDetail && route.Method == http.MethodPost:
		return p.resourceServePost(w, r, ws, route, res, base, scope, rv, liveEffect)
	case isDetail && route.Method == http.MethodDelete:
		return p.resourceServeDelete(w, r, ws, route, m, res, base, scope, rv, liveEffect)
	default:
		// R14's table names exactly four verb/route-shape combinations; any
		// other method matched at either shape (PUT X, PATCH X/{}, ...) is
		// not one of them and is served exactly as before this slice.
		return rv, false
	}
}

// resourceServeCollection is GET X (R14): every row, ORDER BY entities.id
// (already [EntityStore.List]'s own order), wrapped per R6. The byte cap
// (R25/D7) is checked and refused HERE, before [resolved.PreBuilt] is ever
// set — never inside assembleResponse's pinned-body channel, which answers
// through the status with a nil body and would read as "no rows" on a
// collection route.
func (p *Plane) resourceServeCollection(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime,
	route *router.Route, res *resources.Resource, base, scope resources.ScopeKey, rv resolved,
) (resolved, bool) {
	entities, err := p.entities.List(r.Context(), res.ID, base, scope)
	if err != nil {
		if errors.Is(err, resources.ErrResourceGone) {
			return rv, false // R37: declined out from under a parked request
		}
		p.log.Warn("list resource entities", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", err)
		return rv, false
	}

	body, merr := marshalCollection(res, entities)
	if merr != nil {
		p.log.Warn("marshal resource collection", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", merr)
		return rv, false
	}

	overhead, oerr := envelopeOverhead(rt)
	if oerr != nil {
		p.log.Warn("compute envelope overhead", "workspace", ws.Slug, "err", oerr)
		overhead = 0
	}
	if int64(len(body))+overhead > p.cfg.MaxResponse {
		httpx.Err(w, http.StatusConflict, "collection_too_large", fmt.Sprintf(
			"%s %s: %d stored bytes exceeds MOCKER_MAX_RESPONSE (%d)",
			route.Method, route.CanonicalPath, len(body), p.cfg.MaxResponse))
		return rv, true
	}

	rv.PreBuilt = body
	return rv, false
}

// resourceServeDetail is GET X/{} (R14): one row by entity_key, compared as
// a string. key is m.Params[router.DetailIDParam(route)] — BY NAME,
// recomputed from route's own ordered pattern, never by the literal name
// "id" and never by VALUE out of the match's parameters (router.Build glues
// basePath+Path before compiling, and a basePath carrying its own {param}
// makes Match.Params hold TWO entries — a value-pick would be a Go map
// range, random 404s on rows that exist).
func (p *Plane) resourceServeDetail(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace,
	route *router.Route, m *router.Match, res *resources.Resource, base, scope resources.ScopeKey, rv resolved,
) (resolved, bool) {
	key := m.Params[router.DetailIDParam(route)]
	entity, found, err := p.entities.Get(r.Context(), res.ID, base, scope, key)
	if err != nil {
		if errors.Is(err, resources.ErrResourceGone) {
			return rv, false // R37
		}
		p.log.Warn("get resource entity", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", err)
		return rv, false
	}
	if !found {
		writeEntityNotFound(w, route, key)
		return rv, true
	}
	rv.PreBuilt = []byte(entity.Data)
	return rv, false
}

// resourceServePost is POST X (R14/R23): taken over only when the family's
// write_form is "bare" AND the request body parsed as a JSON object.
//
// The two write-verb-only conjuncts of R21 rule 5 are checked here, not in
// [Plane.resourceBranch], because they gate POST and DELETE identically but
// read differently at each call site: a NUMERIC selector — never "2XX" or
// "default" (isSyntheticSelector) — and NO live force. That carrier is
// liveEffect.Status, taken from serveGenerated where the branch already
// sits, explicitly NOT rv.StatusSource: StatusSource is
// domain.StatusSourceDefault for a live force too (resolveVariant sets it
// before the eff.Status branch and never revisits it), so an implementer
// reaching for that enum instead writes a rule that never fires, and a
// session-forced 204 on POST X would keep writing its row in silence — 204
// is 2xx and NoBody, so the ordinary bodyless-DELETE carve-out would
// otherwise reach it too.
func (p *Plane) resourceServePost(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace,
	route *router.Route, res *resources.Resource, base, scope resources.ScopeKey, rv resolved, liveEffect livestate.Effect,
) (resolved, bool) {
	if res.WriteForm == nil || *res.WriteForm != "bare" {
		return rv, false
	}
	if isSyntheticSelector(rv.Variant.Selector) || liveEffect.Status != 0 {
		return rv, false
	}

	data, ok := parsePostBody(r)
	if !ok {
		return rv, false // R23: no takeover, no write, no 400
	}

	entity, err := p.entities.Create(r.Context(), res.ID, base, scope, res.IDField, res.Wrapper.IDType, data)
	if err != nil {
		switch {
		case errors.Is(err, resources.ErrResourceGone):
			return rv, false // R37: declined mid-request — answer from the generator
		case errors.Is(err, resources.ErrEntityLimit):
			httpx.Err(w, http.StatusConflict, "entity_limit", fmt.Sprintf(
				"%s %s: resource is at its entity limit", route.Method, route.CanonicalPath))
			return rv, true
		case errors.Is(err, resources.ErrWriteBusy):
			httpx.Err(w, http.StatusServiceUnavailable, "write_busy", fmt.Sprintf(
				"%s %s: writer busy, try again", route.Method, route.CanonicalPath))
			return rv, true
		default:
			p.log.Warn("create resource entity", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", err)
			return rv, false
		}
	}

	// R24: the status is whatever R21 rule 4 already selected (normally the
	// spec's declared 201) — this branch supplies only the body, the stored
	// entity, already carrying the id overwrite Create performed.
	rv.PreBuilt = []byte(entity.Data)
	return rv, false
}

// resourceServeDelete is DELETE X/{} (R14): NOT gated on write_form, which
// describes a POST body and says nothing about deletion. Every SUCCESSFUL
// delete still goes through assembleResponse (handled=false) so the
// variant's declared headers are carried: a declared 204 sets no PreBuilt
// at all and relies on rv.NoBody exactly as R19 says, and a declared 200 or
// 202 hands back the entity that was just deleted — the only body this
// branch has that is not an invention, captured from Get BEFORE the delete
// so it is still available afterward.
func (p *Plane) resourceServeDelete(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace,
	route *router.Route, m *router.Match, res *resources.Resource, base, scope resources.ScopeKey, rv resolved, liveEffect livestate.Effect,
) (resolved, bool) {
	if isSyntheticSelector(rv.Variant.Selector) || liveEffect.Status != 0 {
		return rv, false
	}

	key := m.Params[router.DetailIDParam(route)]
	entity, found, err := p.entities.Get(r.Context(), res.ID, base, scope, key)
	if err != nil {
		if errors.Is(err, resources.ErrResourceGone) {
			return rv, false // R37
		}
		p.log.Warn("get resource entity before delete", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", err)
		return rv, false
	}
	if !found {
		writeEntityNotFound(w, route, key)
		return rv, true
	}

	deleted, err := p.entities.Delete(r.Context(), res.ID, base, scope, key)
	if err != nil {
		switch {
		case errors.Is(err, resources.ErrWriteBusy):
			httpx.Err(w, http.StatusServiceUnavailable, "write_busy", fmt.Sprintf(
				"%s %s: writer busy, try again", route.Method, route.CanonicalPath))
			return rv, true
		case errors.Is(err, resources.ErrResourceGone):
			return rv, false // R37
		default:
			p.log.Warn("delete resource entity", "workspace", ws.Slug, "routeFamily", res.RouteFamily, "err", err)
			return rv, false
		}
	}
	if !deleted {
		// A race between this Get and the Delete below (a concurrent
		// duplicate DELETE won it first) — the tree's ordinary 404, not a
		// 500, and not a silent success over a row that is already gone.
		writeEntityNotFound(w, route, key)
		return rv, true
	}

	if !rv.NoBody {
		rv.PreBuilt = []byte(entity.Data)
	}
	return rv, false
}
