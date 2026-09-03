// preview.go is P2f's one exported addition to this package: [Plane.Preview]
// answers "what would the mock plane serve for this operation once this
// draft override is saved" without saving anything, bumping [workspaces.Workspace.Revision]
// or touching [Plane.routes] (the route-table cache real traffic reads).
//
// It exists so [internal/admin]'s POST .../preview handler never
// re-implements [resolveVariant]/[Plane.assembleResponse] a second time —
// D1's whole argument, recorded in respond.go's own doc comment on
// serveGenerated, applies here word for word: two copies of "which variant,
// which body, which headers" drift, and CLAUDE.md already records that
// exact failure shape twice for the media-type rule alone.
package mockplane

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// previewDraftFields mirrors internal/admin's overrideMutableFields JSON
// shape byte-for-byte (internal/admin/override_handlers.go's own
// overrideMutableFields — the wire shape PUT .../operations/{opKey}
// decodes into): eight tagged fields, nothing more. It exists so this
// package can decode [domain.PreviewRequest.Draft] without importing
// internal/admin (A9) and without decoding straight into [overrides.Row]
// itself — that type carries six exported but UNTAGGED fields
// (ID/WorkspaceID/Method/Path/OperationID/UpdatedAt) that encoding/json
// would match case-insensitively against stray wire keys PUT would have
// rejected, which is exactly the drift D2 records as an earlier draft's
// mistake. By the time Draft reaches here it is already the RE-MARSHALLED
// output of that same eight-field struct on the admin side (D2), so a
// decode failure here means a bug in that contract, never a hand-crafted
// wire body — see the one call site below for how that failure is
// answered.
type previewDraftFields struct {
	OverrideOn    bool                         `json:"overrideOn"`
	RouteOff      bool                         `json:"routeOff"`
	ActiveStatus  *int                         `json:"activeStatus,omitempty"`
	Responses     map[string]overrides.Variant `json:"responses"`
	ListSize      *overrides.ListSize          `json:"listSize,omitempty"`
	DelayMs       *int                         `json:"delayMs,omitempty"`
	FailDirective jsonx.RawMessage             `json:"failDirective,omitempty"`
	ValidateReq   *bool                        `json:"validateReq,omitempty"`
}

// Preview renders the response the draft override WOULD produce for req.OpKey
// once saved. It writes nothing, bumps no revision and never touches the route
// cache.
//
// Order (D6/D12's ordered taxonomy — row 1, draft validation, is answered
// entirely inside internal/admin before this method is ever called, per
// D7):
//
//  1. buildRuntime, called directly — never p.routes.getOrBuild, never put —
//     with req.Draft spliced in as the ONE extra row buildRuntime's own
//     draft parameter exists for (D3). The runtime this produces is
//     discarded the moment Preview returns; nothing about it is cached.
//  2. row 2: an enabled custom endpoint at the same (method, canonical
//     path) — or the located route IS one — outranks the spec operation.
//  3. row 3: the workspace has no spec attached at all.
//  4. row 4: opKey names no operation in the (attached) spec.
//  5. [resolveVariant] itself runs next — hoisted, since P3a, above BOTH
//     of the checks that used to precede it (D12): row 5a needs the chosen
//     variant to exist at all, and row 5 is simply carried along with it,
//     previewInput and rt.lookupOverride in tow. The hoist is
//     behaviour-neutral on the wire — resolveVariant reads no path
//     parameter, and its one impure line (a log warn for a forced status
//     naming no declared response) now fires, for a draft that ALSO names
//     a missing path parameter, where the old order returned before it
//     ever ran; accepted, not a defect.
//  6. row 5a (D12): the located route belongs to a CONFIRMED resource and
//     the method being drafted is one P3a takes over at serving time — see
//     [domain.ErrPreviewResourceServes]'s own doc comment for the exact
//     condition and its one exclusion (a pinned or non-2xx chosen variant
//     previews normally, D11).
//  7. row 5: the located route declares a path parameter req.PathParams
//     does not carry, or carries empty.
//
// THE ORDER IS LOAD-BEARING (D6/R2/D12): the first row whose condition
// holds decides and no later row is consulted. Rows 2-4 still precede
// everything below them for the reason they always did: a parametrised
// operation shadowed by a custom endpoint, or one the spec never declared,
// must never reach resolveVariant, let alone the resource or
// missing-path-parameter checks that follow it. Row 5a sits BEFORE row 5,
// not after, because D12 gives the resource answer priority: an operator
// drafting DELETE /items/{id} against a confirmed "items" resource, with no
// id in req.PathParams, is told the route is resource-served — the more
// actionable fact, since nothing about that draft will ever reach the
// plane's own DELETE handler regardless of how the parameter ends up
// filled in — not that a parameter happens to be missing. Rows 2 and 3 are
// still ordered as they are (2 before 3) because a spec-less workspace CAN
// still carry custom endpoints (buildRuntime builds a table from them
// alone) — custom_endpoint_wins is both the more specific answer and the
// one an operator can act on.
//
// Rows 2-4 are reported as a returned error (one of the three
// domain.ErrPreview* sentinels decided before a variant exists) for the
// reason D6 always gave: none of them names a RENDERED response, so no
// domain.PreviewResult field could carry it. Rows 5a and 5 are ALSO
// reported as a returned error — [domain.ErrPreviewResourceServes],
// [domain.ErrPreviewMissingPathParam] wrapped with the parameter's own
// name — but that is no longer because they precede resolveVariant: since
// the P3a hoist, both are decided AFTER it, from the very rv this method
// goes on to use for every row below. They stay errors because what they
// name is still not a RENDERED response — a route the plane will never let
// this draft answer for at all, not a response this draft renders
// differently than expected — the same shape [domain.PreviewResult]'s own
// presence rule already draws between "no body because nothing was
// resolved" (an error) and "no body because what resolved has none" (the
// NoBody field, still below).
//
// Once past row 5, Preview shares [resolveVariant] and
// [Plane.assembleResponse] with the real serving path verbatim (rows 6-11,
// owned by those two functions and carried in domain.PreviewResult's own
// doc comment) with exactly one substitution: a ZERO [livestate.Effect] in
// resolveVariant's eff parameter, and req.Status (an operator-picked status
// tab, D10) in its requested parameter — the same slot a live force would
// otherwise occupy. Preview NEVER calls [livestate.Apply]: that one call
// reads the forced status and consumes fail_next in the SAME invocation
// (respond.go's own doc comment on serveGenerated, step 1), so calling it
// here would spend a real session's fail counter on a request that renders
// no traffic and answers no client. A session force is therefore invisible
// to preview, by decision (D8) — it is a WORKSPACE-layer statement, not a
// session-layer one.
func (p *Plane) Preview(ctx context.Context, ws *workspaces.Workspace, req domain.PreviewRequest) (domain.PreviewResult, error) {
	method, path, err := overrides.ParseOpKey(req.OpKey)
	if err != nil {
		return domain.PreviewResult{}, err
	}

	var fields previewDraftFields
	if err := jsonx.Unmarshal(req.Draft, &fields); err != nil {
		// Per this type's own doc comment: F re-marshals an already-validated
		// overrideMutableFields into these bytes, so reaching here means the
		// admin/mockplane contract itself broke, never a malformed request a
		// human typed. Reported plainly rather than folded into one of the
		// D6 sentinels, which all mean something a real workspace state can
		// produce — this does not.
		return domain.PreviewResult{}, fmt.Errorf("mockplane: preview: decode draft: %w", err)
	}
	draftRow := &overrides.Row{
		Method:        method,
		Path:          path,
		OverrideOn:    fields.OverrideOn,
		RouteOff:      fields.RouteOff,
		ActiveStatus:  fields.ActiveStatus,
		Responses:     fields.Responses,
		ListSize:      fields.ListSize,
		DelayMs:       fields.DelayMs,
		FailDirective: fields.FailDirective,
		ValidateReq:   fields.ValidateReq,
	}

	// D3: buildRuntime directly, never the cache. The runtime this builds —
	// draft spliced in, scenario composed over it if one is active — is
	// thrown away the moment this method returns; nothing published to
	// p.routes ever carries an unsaved draft.
	rt, err := p.buildRuntime(ctx, ws, map[string]*overrides.Row{overrides.OpKey(method, path): draftRow})
	if err != nil {
		return domain.PreviewResult{}, err
	}

	// Rows 2-4: locateRoute owns the three checks that decide WHETHER an
	// operation exists to preview at all — pulled out of this method so its
	// own branching (a custom-endpoint scan, the no-spec check, a spec-route
	// scan) does not pile onto this function's own gocyclo count on top of
	// rows 5-11's, still below it, below.
	route, err := p.locatePreviewRoute(rt, method, path, ws.SpecID)
	if err != nil {
		return domain.PreviewResult{}, err
	}

	// D10: query/headers/body all feed overrides.Input — Condition.In is
	// "query" | "header" | "body", and a preview that only ever offered
	// query would render a DIFFERENT variant than the plane serves for any
	// header- or body-gated when[], with no indication anything was
	// skipped. Malformed query bytes degrade to whatever url.ParseQuery
	// still managed to parse (mirroring (*url.URL).Query()'s own silent
	// tolerance for a request's raw query string, respond.go's r.URL.Query()
	// call); an undecodable body degrades to BodyOK=false, mirroring
	// reqbody.go's tryParseBody — DESIGN §8 is explicit that a body the mock
	// plane cannot parse is never surfaced as a client-visible error, and a
	// preview of that same body should not behave differently from what a
	// real request with it would.
	//
	// Hoisted here, above row 5a and row 5 (D12/P3a): resolveVariant is the
	// one call both new rows below need — row 5a to know whether the
	// chosen variant is pinned or non-2xx (D12's own exclusion), and this
	// package's own [domain.ErrPreviewResourceServes] doc comment for why
	// that matters. previewInput and rt.lookupOverride move up with it
	// purely because resolveVariant needs their results; neither reads a
	// path parameter, so nothing about THIS pair's own behaviour changes.
	query, _ := url.ParseQuery(req.Query)
	in := previewInput(method, query, req.Headers, req.Body)

	// From here on: the SAME two functions the real serving path calls,
	// with a zero livestate.Effect (D8) and req.Status in the slot a live
	// force would otherwise occupy (resolveVariant's own doc comment on why
	// the two inputs are mutually exclusive by construction across the two
	// call sites).
	row, hasRow := rt.lookupOverride(route)
	rv, ok := p.resolveVariant(ws, rt, route, row, hasRow, in, req.Status, livestate.Effect{})

	// Row 5a (D12): the located route is served from a confirmed resource
	// for the method being drafted — see
	// [domain.ErrPreviewResourceServes]'s own doc comment for the exact
	// condition (GET always, DELETE X/{} when the spec declares it, POST X
	// when write_form is "bare") and its one exclusion (a pinned or
	// non-2xx rv previews normally, D11). Checked here, immediately after
	// resolveVariant produced rv and BEFORE row 5 below, because that
	// exclusion is UNCONSTRUCTIBLE without rv: there is no chosen variant
	// to ask "pinned? 2xx?" about until this call has run.
	if err := previewResourceServes(rt, route, method, rv); err != nil {
		return domain.PreviewResult{}, err
	}

	// Row 5: [R1] the declared set is the {param} segments of
	// ws.Settings.BasePath + route.Path, never route.Path alone — a
	// workspace whose own basePath carries a parameter (e.g.
	// "/orgs/{orgId}") has a real parameter segment in EVERY route it
	// serves, because router.Build glues the two exactly this way before
	// ever compiling a match pattern. ws.Settings.BasePath, not
	// rt.settings.BasePath: the two are equal by construction
	// (composeScenarioLayer always restores the workspace's own basePath,
	// runtime.go's own A3a) and spelling the workspace out here matches
	// runtime.go's own router.Build call site.
	//
	// Checked AFTER row 5a, not before: D12 gives the resource answer
	// priority over a missing parameter on the identical route (this
	// function's own top comment explains why), so a request that would
	// trip both never reaches this check at all.
	if missing, ok := firstMissingPathParam(ws.Settings.BasePath, route.Path, req.PathParams); ok {
		return domain.PreviewResult{}, fmt.Errorf("%w: %s", domain.ErrPreviewMissingPathParam, missing)
	}

	// D3/D10: named regardless of which of rows 6-11 the request lands on —
	// even a routeOff or noBody answer is still "the row that governs this
	// key", and an operator who edits under an active scenario and sees no
	// change deserves to be told why, not to discover it by guessing.
	shadowedBy := p.shadowedByScenario(ctx, ws, method, path)

	// Row 6: routeOff. Checked AFTER resolveVariant, not before — unlike
	// serveGenerated's own route_off short-circuit (which runs first
	// because a disabled route must consume nothing below it, respond.go's
	// own doc comment), D5's presence rule requires status/statusSource to
	// be reported EVEN for a disabled route: "a disabled route still has a
	// status the plane would have used had it been enabled". Consuming
	// nothing has no meaning for a preview that sleeps nothing and spends
	// no fail counter to begin with.
	overrideActive := hasRow && row.OverrideOn
	if overrideActive && row.RouteOff {
		return domain.PreviewResult{
			Status:       rv.Status,
			StatusSource: rv.StatusSource,
			RouteOff:     true,
			ShadowedBy:   shadowedBy,
		}, nil
	}

	// Rows 7 and 9: no declared variant at all, or a Degraded one —
	// resolveVariant's own doc comment on why the two share one outcome
	// (DESIGN treats them identically; neither's status/type can be
	// trusted). Both leave ok false and rv.Status already set to the
	// literal 200 the plane actually answers with.
	if !ok {
		return domain.PreviewResult{
			Status:       rv.Status,
			StatusSource: rv.StatusSource,
			NoBody:       true,
			ShadowedBy:   shadowedBy,
		}, nil
	}

	// D8: the delay is COMPUTED and REPORTED, never slept — Preview calls
	// neither awaitDelay nor time.Sleep anywhere. rowDelayMs mirrors
	// serveGenerated's own guard exactly: row is nil whenever there is no
	// row at all, so it stays nil unless overrideActive actually gates a
	// real one (effectiveDelayMs's own doc comment on why that is the
	// caller's job).
	var rowDelayMs *int
	if overrideActive {
		rowDelayMs = row.DelayMs
	}
	delayMs := effectiveDelayMs(0, rowDelayMs, rt.settings.DelayMs)

	// Rows 8, 10, 11: assembleResponse is the ONE place recipes are
	// assembled, the patched schema is looked up, headers are computed and
	// the body is generated or pinned — sharing it with serveGenerated is
	// the entire reason this split exists (D1). effectiveDelay is passed
	// through so AssembledResponse.EffectiveDelay carries the SAME number
	// serveGenerated would have slept on, computed once, reported here,
	// slept nowhere.
	// P3c (D5): Preview passes nil — it already declines to reach live
	// state (it never calls livestate.Apply, so as not to build a second
	// implementation of Apply's precedence), and a live ref resolver over
	// real entity rows would be the identical mistake in a new place.
	// A6 (D6/D7): no asset lookup — a draft never reads real asset rows,
	// the same refusal it makes for live state and entity rows — and the
	// asset_url base built from the ADMIN request through the same one
	// function the mock plane uses.
	asm := p.assembleResponse(ws, rt, route, row, rv, req.PathParams, query, delayMs, nil,
		nil, req.AssetBase)

	result := domain.PreviewResult{
		Status:             rv.Status,
		StatusSource:       rv.StatusSource,
		MediaType:          asm.MediaType,
		Headers:            asm.Headers,
		NoBody:             rv.NoBody || asm.AssetMissing, // A6: an unservable bodyRef is a body-less answer, with Notes saying why
		Refused:            asm.Refused,
		SchemaPatchApplied: asm.SchemaPatchApplied,
		RecipesBound:       asm.RecipesBound,
		DelayMs:            asm.EffectiveDelay,
		ShadowedBy:         shadowedBy,
	}
	if asm.AssetMissing {
		result.Notes = noteAssetMissing
	}
	// D5's presence rule: Body (and Encoding, present exactly when Body is)
	// absent whenever NoBody, RouteOff or Refused is set — RouteOff already
	// returned above, so only the other two remain to gate here.
	if !rv.NoBody && asm.Refused == nil {
		result.Body = asm.Body
		if utf8.Valid(asm.Body) {
			result.Encoding = "utf8"
		} else {
			result.Encoding = "base64"
		}
	}
	return result, nil
}

// locatePreviewRoute answers D6's rows 2-4 — everything that decides
// WHETHER an operation exists to preview at all, before row 5 (a
// parameter of THAT route) can even be asked. Pulled out of Preview itself
// purely to keep gocyclo's count on that method from combining this
// function's three checks with rows 5-11's own branching (N6).
//
// Row 2: [R1] compares CANONICAL paths (D4), not the literal Path string
// opKey addresses — router.compareRoutes' own rule 3 ("custom outranks
// spec at equal specificity") fires whenever two routes share a canonical
// SHAPE, e.g. a custom endpoint at "/users/{userId}" against a spec
// operation at "/users/{id}": different literal parameter names, identical
// canonical path "/users/{}", and the custom one already wins every real
// request to that shape today. A literal-Path comparison would miss
// exactly that case. Any custom route surviving into rt.routes is already
// enabled — buildRuntime drops an OverrideOn=false custom_endpoints row
// before it ever becomes a router.Route (runtime.go's own step 8 comment)
// — so presence here already means "enabled".
//
// Row 3: the same hasSpec predicate buildRuntime itself gates steps 1-6 on
// (runtime.go's own doc comment) — checked here by the identical
// condition, rather than inferred from some field of rt going nil, so this
// stays readable as "no spec" rather than "whatever buildRuntime happened
// to leave unset".
//
// Row 4: locate the SPEC route by exact (Method, Path) — real parameter
// names, never CanonicalPath (D4's own emphasis: ParseOpKey returns
// "/users/{id}", CanonicalPath collapses that to "/users/{}", which
// matches nothing for any parametrised operation). Row 2 has already ruled
// out a custom route winning this exact spot, so any match found here is
// unambiguously the operation being previewed.
func (p *Plane) locatePreviewRoute(rt *runtime, method, path string, specID *int64) (*router.Route, error) {
	canon := router.CanonicalPath(path)
	for _, rr := range rt.routes {
		if rr.Custom && rr.Method == method && rr.CanonicalPath == canon {
			return nil, domain.ErrPreviewCustomEndpointWins
		}
	}

	if p.specs == nil || specID == nil {
		return nil, domain.ErrPreviewNoSpec
	}

	for i := range rt.routes {
		rr := &rt.routes[i]
		if !rr.Custom && rr.Method == method && rr.Path == path {
			return rr, nil
		}
	}
	return nil, domain.ErrPreviewOperationNotFound
}

// previewResourceServes is row 5a (D12): does route belong to a confirmed
// resource for the method named by method, given the variant resolveVariant
// already chose (rv)?
//
// Deliberately NOT [resourceBranch] (resource.go) reused or called: that
// function answers the PER-REQUEST question — it also gates on the
// variant's own media type, on [isSyntheticSelector] and on a live force,
// none of which this row consults, because D12 asks this row to answer
// ELIGIBILITY, not the per-request outcome. Calling resourceBranch here
// would silently narrow this row to resourceBranch's own precision and
// stop over-approximating, the opposite of D12's design.
//
// [lookupResource] alone decides family membership and collection/detail
// shape; this function adds only the per-method eligibility table and D12's
// pinned/non-2xx exclusion — checked FIRST, since resource-family
// membership must never even be asked about a variant D12 says to leave
// alone.
func previewResourceServes(rt *runtime, route *router.Route, method string, rv resolved) error {
	res, isDetail := lookupResource(rt, route)
	if res == nil {
		return nil
	}

	// D12's own exclusion, mirrored from [resourceBranch]'s identical
	// "rule 4" guard: a pinned or non-2xx chosen variant previews normally
	// — the one draft D11 keeps the endpoints editor open for.
	pinned := rv.Override != nil && rv.Override.Mode == "pinned"
	if pinned || rv.Status < 200 || rv.Status >= 300 {
		return nil
	}

	switch {
	case method == http.MethodGet:
		// GET X and GET X/{} always — over-approximating past route_off, a
		// non-JSON/absent media type and a degraded operation, none of
		// which this row checks (D12).
		return domain.ErrPreviewResourceServes
	case isDetail && method == http.MethodDelete:
		// DELETE X/{} "when the spec declares it": route was located by
		// [p.locatePreviewRoute] via an exact (Method, Path) match against
		// the spec's OWN route table (row 4, above), so reaching here with
		// method == DELETE already proves the spec declares this
		// operation — there is no separate declared/undeclared case left
		// to test. Over-approximates past the media type of the 2xx this
		// draft actually resolved to (D13 clause 25's text/plain case).
		return domain.ErrPreviewResourceServes
	case !isDetail && method == http.MethodPost && res.WriteForm != nil && *res.WriteForm == "bare":
		// POST X, only when write_form is "bare" — R14's own per-method
		// table. A nil write_form (never taken over on POST) falls through
		// to the default below and previews normally.
		return domain.ErrPreviewResourceServes
	default:
		return nil
	}
}

// firstMissingPathParam reports the first (left-to-right) name among
// declaredPathParams(basePath, relativePath) that pathParams either omits
// or maps to "" — D10's own requirement that a missing parameter is a
// hard 400, never defaulted to the empty string the way an earlier draft
// of that decision tried: NormalizeSegments drops an empty path segment
// entirely, so a synthesised "" would match a DIFFERENT route (one
// segment short), not the one being previewed.
func firstMissingPathParam(basePath, relativePath string, pathParams map[string]string) (string, bool) {
	for _, name := range declaredPathParams(basePath, relativePath) {
		if v, ok := pathParams[name]; !ok || v == "" {
			return name, true
		}
	}
	return "", false
}

// previewInput builds the [overrides.Input] resolveVariant's when[]
// evaluation reads, from the wire's raw method/headers/body — the same
// three-dimension shape overridesInputFor (reqbody.go) builds from a real
// *http.Request, D10's own reason for the wire request carrying all three
// (a preview that only ever offered query would render a DIFFERENT variant
// than the plane serves for any header- or body-gated when[], with no sign
// anything was skipped).
//
// BodyOK is computed by mirroring captureRequestBody+tryParseBody
// (reqbody.go) EXACTLY, not by "does body decode as JSON": the real serving
// path never even attempts to read a body for GET/HEAD/DELETE or for a
// multipart Content-Type, and for every other method it only tries a JSON
// decode when Content-Type is empty, "application/json" or "text/plain"
// (tryParseBody's own switch, an EXACT type/subtype match via
// splitMediaType). A previewInput that ignored method and Content-Type and
// decoded any JSON-shaped bytes regardless would let a body-gated when[]
// match in Preview and silently NOT match on the real plane for the
// byte-identical request (or vice versa) whenever the operation's method is
// GET/HEAD/DELETE, the client sends multipart, or the client's Content-Type
// is anything other than exactly those three values — precisely the class
// of divergence D6/D10 exist to prevent. A decode failure (or a
// method/Content-Type this function refuses to even attempt) degrades to
// BodyOK=false, never a client-visible error — DESIGN §8's own rule, applied
// here exactly as reqbody.go applies it to a real request.
func previewInput(method string, query url.Values, headers map[string]string, body jsonx.RawMessage) overrides.Input {
	header := make(http.Header, len(headers))
	for name, value := range headers {
		header.Set(name, value)
	}

	var bodyVal any
	var bodyOK bool
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodDelete:
		// captureRequestBody never reads a body for these methods at all
		// (reqbody.go:125-128); BodyOK stays false regardless of what bytes
		// req.Body carries.
	default:
		contentType := header.Get("Content-Type")
		if !isMultipart(contentType) {
			bodyVal, bodyOK = tryParseBody(contentType, body)
		}
	}
	return overrides.Input{Query: query, Header: header, Body: bodyVal, BodyOK: bodyOK}
}

// declaredPathParams returns the parameter NAMES of every whole-segment
// {param} in basePath+relativePath, in left-to-right order, under the
// IDENTICAL rule compilePattern and router.DetailIDParam (both
// internal/router/router.go) already apply: a brace pair counts only when it
// spans the entire segment ("{id}", never "prefix{id}" or "{id}suffix"). It
// is not a call to either of those — compilePattern returns a match-time
// pattern this package has no reason to build twice per preview, and
// router.DetailIDParam only ever wants the LAST segment's name — so this is
// a third, narrower reader of the same
// rule, kept in step with router.Build's own glue (`full := basePath +
// r.Path`, router.go's own Build) by mirroring it exactly rather than
// calling router.NormalizeBasePath first, which router.Build does not
// either.
func declaredPathParams(basePath, relativePath string) []string {
	full := strings.TrimRight(basePath, "/") + relativePath
	var names []string
	for _, seg := range strings.Split(full, "/") {
		if len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			names = append(names, seg[1:len(seg)-1])
		}
	}
	return names
}

// shadowedByScenario reports the name of ws's active scenario when its
// snapshot carries a row for (method, path) — composeScenarioLayer's own
// "MEMBERSHIP decides the layer" rule (scenario.go): a key present in the
// snapshot always wins over the workspace's own row, and — because D3's
// splice runs BEFORE the scenario merge — over a previewed draft too. ""
// when there is no active scenario, when its snapshot fails to load (the
// same degrade-not-500 contract [Plane.scenarioSnapshot] already
// documents; not re-logged here, since scenarioSnapshot's own call inside
// buildRuntime, moments earlier in this same request, already logged the
// identical failure once), or when the snapshot simply carries no row for
// this key.
//
// This is a second call to p.scenarios.Get, not a reuse of buildRuntime's
// own scenarioSnapshot call: that method returns only the decoded
// bundle.Bundle, discarding the scenario's own Name — the one field this
// function needs and buildRuntime never did.
func (p *Plane) shadowedByScenario(ctx context.Context, ws *workspaces.Workspace, method, path string) string {
	if p.scenarios == nil || ws.ScenarioID == nil {
		return ""
	}
	sc, err := p.scenarios.Get(ctx, ws.ID, *ws.ScenarioID)
	if err != nil {
		return ""
	}
	key := overrides.OpKey(method, path)
	for _, e := range sc.Bundle.Overrides {
		if overrides.OpKey(e.Method, e.Path) == key {
			return sc.Name
		}
	}
	return ""
}
