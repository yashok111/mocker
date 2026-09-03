// respond.go is P1b's other half of the seam routes.go declares:
// [Plane.serveGenerated] is what actually puts a generated body on the wire
// for a matched route, replacing the 501 respond_stub.go answered while this
// file did not exist yet. Everything here is about ASSEMBLY (DESIGN §6/§9),
// never about what the bytes themselves look like — [gen.Generator] alone
// decides that; this file decides which variant to answer with, whether the
// client gets to see it at all (Accept negotiation), whether it gets a body
// on the wire (204/205/HEAD/degraded/no-variant), and the two things that
// only make sense at the serving layer: the artificial delay and the
// envelope wrap.
//
// P1c ADDS the op_overrides application, stage 2 of internal/overrides'
// three-stage build (compile at runtime-build time — overrides.go/runtime.go,
// frozen by this stage — apply at request time — here). Every override is
// gated on OverrideOn: a row that exists but is switched off must leave a
// request answering EXACTLY what it would with no [OverrideSource] wired at
// all (HARD RULE 6's "no recipes must mean no change" extended to "no ACTIVE
// override means no change" for every other override field too), which is
// why every new branch below reads `overrideActive` rather than `hasRow`.
package mockplane

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// serveGenerated is THE SEAM: routes.go calls this on every match, with the
// runtime the match was found against. Order, matching the digest's
// numbered requirements (P1c's additions interleaved where DESIGN §4/§6's
// own layering actually places them, not appended at the end):
//
//  0. route_off: an active override that switches the whole route off
//     answers the SAME 404 an unmatched route gets — DESIGN §8 wants a
//     disabled operation indistinguishable from one that was never
//     declared. It consumes NOTHING below it: no livestate.Apply, no delay,
//     no pause — an unmatched route never paid for either.
//  1. livestate.Apply(ws.ID, route.Method, route.Path) (NEW, P1c2) — ONCE
//     per request, and the ONLY place a fail counter is consumed. Skipped
//     entirely when the Plane has no LiveStateSource ([Plane.SetLiveState]
//     never called) — the identical nil-source contract op_overrides
//     already established for [Plane.SetOverrides].
//  2. resolvePause (NEW, P2a), THEN the effective delay (5a). A matched
//     pause parks the request BEFORE anything is written (DESIGN §14 rule
//     4 — a delay is paid once, AFTER a pause releases, never before the
//     park and never per wakeup); a canceled request context ends the park
//     writing nothing at all, and the wait never re-calls Apply — see
//     [awaitPause]'s own doc comment for why. The delay itself: SESSION
//     beats ROW beats WORKSPACE settings (DESIGN §4: Session is the
//     outermost of the four layers) — [livestate.Effect.DelayMs] when a
//     delay directive matched, else the row's own delay_ms when an active
//     override pins one, else settings.DelayMs ([effectiveDelayMs]) —
//     cancellable by r's context throughout.
//  3. THE STATUS, in this order and no other (DESIGN §4 puts Session above
//     Workspace):
//     a. the live [livestate.Effect.Status], when non-zero;
//     b. else, ONLY WHEN overrideActive, overrides.SelectWhen against the
//     row's own responses (NEW, P1c2) — the gate is not optional:
//     respond.go's own comment establishes that "every P1c branch below
//     gates on overrideActive (never merely hasRow) so an
//     OverrideOn=false row is inert end to end", and a when[] firing
//     from a switched-off row breaks that silently;
//     c. else row.ActiveStatus, when an active override pins one;
//     d. else chooseVariant(rt.variants[route.OpRowID]) — the document's
//     own choice, untouched.
//     Whichever of a/b/c fired routes through the SAME variantForStatus +
//     synthetic-no-body-variant fallback active_status has always used — a
//     pinned status with no matching declared variant answers that status
//     with no body rather than 500 (gen.Body's own SchemaPtr=="" branch
//     already does exactly that for a variant with no schema pointer, so no
//     special case is needed here).
//  4. a degraded operation short-circuits to an empty 200 (2), skipping
//     everything below — status, negotiation, headers, envelope all follow
//     from a variant DESIGN says not to trust;
//  5. 204/205 never generate or negotiate a body, but DO get declared
//     headers (4);
//  6. Accept negotiation, with a 406 on an explicit refusal (3) — against
//     the row's own pinned media type when mode is "pinned", falling back
//     to the document's declared type exactly as an unoverridden request
//     would use;
//  7. declared response headers, sanitized against CR/LF (5), plus the
//     row's own pinned headers on top for a "pinned" variant — also
//     sanitized, since a header value stored in an override row is exactly
//     as attacker-reachable as one declared in the spec;
//  8. the body itself: verbatim from the row for mode "pinned" (no
//     generation at all), otherwise generated with the row's compiled
//     recipe set threaded through gen.Request.Recipes and the row's own
//     list_size layered on top when it pins one — enveloped if configured
//     (6), and written with Content-Type/Content-Length set exactly once
//     (10). A body error (generation OR pinned-body decode) never reaches
//     the client as a 500 (9): it is logged and answered as the most
//     honest empty response the declared type allows.
//
// Everything from step 4 on keys off the FINAL status step 3 chose, exactly
// as before P1c2: row.Responses[status], lookupRecipes(route, status),
// pinned bodies, media type, headers all read variant.HTTPStatus, never
// chooseVariant's original, possibly-overridden pick.
//
// P2f (D1) splits the numbered contract above across three pieces instead of
// carrying it all in one function: [Plane.resolveVariant] does step 3 plus
// the two values step 6's gate reads (noBody, the resolved media type — up
// to and including the mediaType fallback), [Plane.assembleResponse] does
// steps 7-8 from the generator request through the envelope wrap, and this
// function stays THE SEAM — steps 0, 1, 2, the step-6 gate itself (it must
// run BETWEEN the two calls: see resolveVariant's own doc comment on why)
// and the four lines that actually put bytes on the wire. Preview
// (preview.go) is the second caller of resolveVariant/assembleResponse — the
// whole reason the split exists is that a preview route re-implementing this
// contract a second time is the exact defect CLAUDE.md already records twice
// for the media-type rule.
// base is the request's own base scope (D7.1/D7.2, P3h) — computed ONCE in
// serveRoute, next to the Match that produced it, and handed down here as a
// parameter, the same construct-and-receive split this package already
// keeps for the ref resolver (P3c D5). serveCustom does not receive one: a
// custom endpoint is never a resource (D7.2).
func (p *Plane) serveGenerated(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match, base resources.ScopeKey) {
	route := m.Route

	// row/overrideActive are read once, up front: every P1c branch below
	// gates on overrideActive (never merely hasRow) so an OverrideOn=false
	// row is inert end to end, not just for the one check that happens to
	// come first. resolveVariant recomputes the identical expression from
	// the same row/hasRow — it is a pure boolean, not a second source of
	// truth — because route_off must be checked here, before
	// livestate.Apply, and resolveVariant does not run until after it.
	row, hasRow := rt.lookupOverride(route)
	overrideActive := hasRow && row.OverrideOn

	if overrideActive && row.RouteOff {
		// The exact shape serveRoute already answers when nothing in the
		// table matches at all (routes.go's serveNoRoute) — never a new,
		// distinguishable "this route was disabled" body: a client that
		// could tell the two apart could enumerate which routes an operator
		// has turned off.
		p.serveNoRoute(w, r, ws, NormalizeSegments(r.URL.EscapedPath()))
		return
	}

	// Session sits above Workspace (DESIGN §4): the live-state layer gets
	// exactly one Apply per request, run here — after route_off has already
	// had its chance to short-circuit (a disabled route consumes nothing,
	// this file's own doc comment above), before the pause and the delay (a
	// live force still pays for a per-operation delay override exactly like
	// any other request would).
	var liveEffect livestate.Effect
	if p.livestate != nil {
		liveEffect = p.livestate.Apply(ws.ID, route.Method, route.Path)
	}

	// Pause sits BEFORE the delay (rule 4): a matched delay is paid ONCE,
	// AFTER a pause releases — never before the park, never per wakeup.
	if !resolvePause(r, liveEffect) {
		return // request context ended while parked: nothing written yet
	}

	// row is nil whenever there is no override row at all — row.DelayMs
	// would panic on every request to an operation without one, so
	// rowDelayMs stays nil except when overrideActive actually gates a real
	// row (effectiveDelayMs's own doc comment on why this is the caller's
	// job, not that function's).
	var rowDelayMs *int
	if overrideActive {
		rowDelayMs = row.DelayMs
	}
	delayMs := effectiveDelayMs(liveEffect.DelayMs, rowDelayMs, rt.settings.DelayMs)
	if !awaitDelay(r.Context(), delayMs) {
		return // context canceled/timed out mid-sleep: nothing left to write
	}

	rv, ok := p.resolveVariant(ws, rt, route, row, hasRow, overridesInputFor(r), nil, liveEffect)
	if !ok {
		// Either the operation declares no response variant at all, or the
		// resolved one is Degraded — both answer the same honest, empty 200
		// (resolveVariant's own doc comment on why they share one outcome).
		w.WriteHeader(http.StatusOK)
		return
	}

	if !rv.NoBody && rv.MediaType != "" && !acceptable(r.Header.Get("Accept"), rv.MediaType) {
		httpx.Err(w, http.StatusNotAcceptable, "not_acceptable", fmt.Sprintf(
			"%s %s only offers %q for this response; Accept %q excludes it",
			route.Method, route.CanonicalPath, rv.MediaType, r.Header.Get("Accept")))
		return
	}

	// P3a (D6 R19): the resource branch sits HERE — after route_off,
	// livestate.Apply, the pause, the delay and the 406 gate above (a POST
	// that would have answered 406 must never create a row first), and
	// BEFORE assembleResponse, which it never calls itself (seam_test.go
	// pins that function's caller set to exactly {serveGenerated,
	// Preview}). handled true means resourceBranch already wrote the
	// entire response (one of its three direct exits); rv may carry
	// PreBuilt either way, consumed by assembleResponse below exactly like
	// every other body source.
	rv, handled := p.resourceBranch(w, r, ws, rt, route, m, base, rv, liveEffect)
	if handled {
		return
	}

	// P3c (D5): the closure is CONSTRUCTED here, at the one call site that
	// has a real request — never inside assembleResponse, which Preview
	// shares and must never be handed a live resolver over real rows.
	ref := p.newRefResolver(r.Context(), rt.resources, p.entities, ws, r, base)
	// A6 (D6/D7): the asset lookup and the asset_url base are constructed
	// here too, from the same real request, and received by
	// assembleResponse — Preview passes nil and its own base.
	asm := p.assembleResponse(ws, rt, route, row, rv, m.Params, r.URL.Query(), delayMs, ref,
		p.newAssetLookup(r.Context(), r, ws), p.assetBase(r, ws))
	p.writeAssembled(w, ws, rv.NoBody, asm)
}

// writeAssembled is serveGenerated's write tail, shared with
// serveCustomGenerated (custom.go) since P7a: headers, then the status
// alone for a no-body response or an empty body, else the body under its
// type. Extracted rather than duplicated so the two callers of the one
// seam also write what it assembled the one way.
func (p *Plane) writeAssembled(w http.ResponseWriter, ws *workspaces.Workspace, noBody bool, asm domain.AssembledResponse) {
	for name, value := range asm.Headers {
		w.Header().Set(name, value)
	}

	if noBody {
		w.WriteHeader(asm.Status)
		return
	}

	if len(asm.Body) == 0 {
		if asm.WriteMediaType {
			// The declared type, so the client at least learns what shape
			// was intended, even though generation could not produce it.
			w.Header().Set("Content-Type", asm.MediaType)
		}
		w.WriteHeader(asm.Status)
		return
	}

	contentType := asm.MediaType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(asm.Body)))
	w.WriteHeader(asm.Status)
	// G705: body is a generated or operator-pinned response — serving it is
	// this plane's entire purpose. The media types that would turn it into an
	// XSS vector are refused at write time by dangerousMediaType, which
	// taint analysis has no way to see from here.
	if _, werr := w.Write(asm.Body); werr != nil { //nolint:gosec
		p.log.Debug("write generated response", "workspace", ws.Slug, "err", werr)
	}
}

// resolved is what [Plane.resolveVariant]'s status precedence produced, plus
// the two values the 406 gate needs — returning them is what lets that gate
// keep its exact position (between resolveVariant and assembleResponse) when
// the generator-request assembly moves out to the second half of the split.
// It never crosses the mockplane/admin boundary (unlike [domain.AssembledResponse]),
// so it stays unexported here rather than in internal/domain.
//
// P3a adds PreBuilt (see its own field comment): resolved is now ALSO the
// value [resourceBranch] (resource.go) mutates in place between the 406
// gate and assembleResponse, carrying what a confirmed resource takeover
// produced, when one fired.
type resolved struct {
	Variant gen.ResponseVariant
	// Override is nil when no override supplied this response — never
	// merely when hasRow is false, since a switched-off row leaves it nil
	// too (overrideActive gates the lookup, same as it always has).
	Override *overrides.Variant
	// OverrideActive gates recipes and the patched schema in assembleResponse.
	OverrideActive bool
	MediaType      string
	NoBody         bool
	// Empty200 is set for the two branches that answer an empty 200 and
	// stop: no response variant at all, and a Degraded variant. Both share
	// one outcome because DESIGN treats them identically — none of a
	// Degraded variant's own status/type can be trusted, override or not —
	// and a caller does not need to tell them apart to decide what to write.
	Empty200 bool
	// Status is the status the precedence chose. For the Empty200 branches
	// it is the literal 200 the plane actually answers with — DESIGN §7's
	// "an empty 200, regardless of whatever status/type this variant row
	// otherwise carries" — not variant.HTTPStatus, which the plane
	// deliberately does not honour there.
	Status       int
	StatusSource domain.StatusSource
	// PreBuilt is P3a's fourth body source (D6 R20): the bytes a confirmed
	// resource takeover already produced — a stored collection, a stored
	// entity, or the entity a DELETE just removed — set by [resourceBranch]
	// (resource.go) between resolveVariant and assembleResponse. It arrives
	// UNENVELOPED and ALREADY MEASURED against MOCKER_MAX_RESPONSE: R25's
	// caps and D7's own byte-cap arithmetic bound it before it ever reaches
	// here, so assembleResponse applies the envelope to it exactly like
	// every other body but performs NO MaxResponse re-check on it. nil
	// means no takeover fired for this request — the ordinary pinned/
	// generated switch below runs unchanged. Never set unless the chosen
	// variant is 2xx and its mode is not "pinned" (resourceBranch's own
	// precedence gate) — R20 is a mechanism, not a precedence claim; R21
	// rule 5 remains the only place precedence is stated.
	PreBuilt []byte
	// Inline is P7a's fifth body source (DESIGN §34.3): a custom
	// endpoint's own schema and recipes, compiled once at runtime build
	// (custom_schema.go), set ONLY by [Plane.serveCustomGenerated]. When
	// non-nil, assembleResponse walks Inline.Schema as the inline root and
	// binds Inline.Recipes, and the override-row block (recipes, patched
	// schema) is skipped: a custom row has no op_overrides row and no spec
	// pointer to patch. Override stays nil and OverrideActive false on that
	// path, so every `pinned` predicate reads false and the generated arm
	// runs.
	Inline *inlineSource
}

// resolveVariant is a METHOD so the receiver carries the logger and the
// config the branches below reach for; rt carries the variants, the
// override map and the composed settings; row/hasRow come from the caller's
// own rt.lookupOverride(route), which both callers already do before
// route_off (serveGenerated, above).
//
// requested is nil for every real request: it exists for Preview
// (preview.go), whose own optional Status field sits in the SAME precedence
// slot [livestate.Effect.Status] occupies for serving — D8 is why Preview
// never has a live effect of its own to put there instead, and the two
// inputs are mutually exclusive across the two call sites by construction
// (serving always passes a zero requested; Preview always passes a zero
// eff), so which one this function checks first can never change either
// caller's own answer.
func (p *Plane) resolveVariant(
	ws *workspaces.Workspace,
	rt *runtime,
	route *router.Route,
	row *overrides.Row, hasRow bool,
	in overrides.Input,
	requested *int,
	eff livestate.Effect,
) (resolved, bool) {
	overrideActive := hasRow && row.OverrideOn

	// THE STATUS, in the one order DESIGN §4/§12 fixes and serveGenerated's
	// own doc comment spells out: a live force (or, for Preview, its own
	// requested status in that same slot) beats a matching when[] (itself
	// only ever consulted when the row is actually switched on) beats
	// active_status beats the document's own choice.
	status := eff.Status
	statusSource := domain.StatusSourceDefault
	if status == 0 && requested != nil {
		status = *requested
		statusSource = domain.StatusSourceRequested
	}
	if status == 0 && overrideActive {
		if key, found := overrides.SelectWhen(row.Responses, in); found {
			// SelectWhen only ever returns a key that already parsed as a
			// decimal status (see its own doc comment) — nothing to guard
			// against here.
			status, _ = strconv.Atoi(key)
			statusSource = domain.StatusSourceWhen
		} else if row.ActiveStatus != nil {
			status = *row.ActiveStatus
			statusSource = domain.StatusSourceActive
		}
	}

	var variant gen.ResponseVariant
	var ok bool
	if status != 0 {
		if declared, found := variantForStatus(rt.variants[route.OpRowID], status); found {
			variant, ok = declared, true
		} else {
			// Not an error: something forced a status (live-state, a
			// matching when[], or active_status) the document itself never
			// declares a response for. A synthetic variant with no
			// SchemaPtr/MediaType flows through every branch below exactly
			// like a real one whose response object is empty —
			// gen.Body's own `v.SchemaPtr == ""` early return already
			// answers "no body", so nothing here needs to special-case it.
			p.log.Warn("a forced status has no declared response; answering with no body",
				"workspace", ws.Slug, "operation", route.OperationLabel, "method", route.Method,
				"path", route.CanonicalPath, "status", status)
			variant, ok = gen.ResponseVariant{OpRowID: route.OpRowID, HTTPStatus: status}, true
		}
	} else {
		variant, ok = chooseVariant(rt.variants[route.OpRowID])
	}
	if !ok {
		// The route exists — it matched — but the document declared no
		// response for its operation at all, and nothing forced a status
		// either. That is honest silence, not a server failure.
		return resolved{Empty200: true, Status: http.StatusOK, StatusSource: statusSource}, false
	}

	if variant.Degraded {
		// DESIGN §7: an operation that could not be parsed always answers
		// an empty 200, regardless of whatever status/type this variant row
		// otherwise carries — none of it can be trusted, override or not.
		return resolved{Variant: variant, Empty200: true, Status: http.StatusOK, StatusSource: statusSource}, false
	}

	// The row's own per-status override, if any, for the status ACTUALLY
	// being served — looked up AFTER live-state/when[]/active_status have
	// already resolved it, never chooseVariant's original pick (get this
	// backwards and mode "pinned"/recipes silently never fire on any
	// operation whose status was forced, with every other test still
	// green).
	var ov *overrides.Variant
	if overrideActive {
		if v, found := row.Responses[strconv.Itoa(variant.HTTPStatus)]; found {
			ov = &v
		}
	}
	pinned := ov != nil && ov.Mode == "pinned"

	noBody := variant.HTTPStatus == http.StatusNoContent || variant.HTTPStatus == http.StatusResetContent

	// The effective media type: the row's own pinned MediaType when mode is
	// "pinned" and it set one, falling back to the document's declared type
	// exactly as an unoverridden response would use. Generated mode never
	// overrides this — nothing in this slice lets an operator change what
	// TYPE a generated response claims to be, only its content.
	mediaType := variant.MediaType
	if pinned && ov.MediaType != "" {
		mediaType = ov.MediaType
	}

	return resolved{
		Variant:        variant,
		Override:       ov,
		OverrideActive: overrideActive,
		MediaType:      mediaType,
		NoBody:         noBody,
		Status:         variant.HTTPStatus,
		StatusSource:   statusSource,
	}, true
}

// assembleResponse is everything from the generator request through the
// bytes: the recipes/patched-schema assembly, the response headers, the
// browser-executable refusal, the pinned-vs-generated switch with its
// MaxResponse re-check, the generator call, the empty-body branch and the
// envelope wrap. It takes values, returns a value, and never sees a
// [http.ResponseWriter] — the writer half (serveGenerated) and Preview
// (preview.go) are its only two callers, and [seam_test.go] proves that
// structurally.
//
// It returns no error: D1's correction on an earlier draft. Every case that
// could have filled one now becomes rv.Refused or an empty Body instead —
// no case is left that could produce one, and a return nobody can produce
// is an invitation to invent one.
//
// D1's one permitted relocation lives here: the generator-request block
// (recipes, patched schema) ran BEFORE the 406 gate in the pre-split
// function; the gate now runs in serveGenerated, between the call to
// resolveVariant and this call, so the block effectively moves to after it.
// That is safe because every line of it is PURE — lookupRecipes and
// lookupPatchedSchema are map reads, and every log line about a compiled
// recipe set or a patched schema fires at BUILD time inside
// buildRecipeSets/buildPatchedSchemas (runtime.go/overrides.go), never here
// — so a request the gate refuses produces exactly the log output it always
// did, and moving the block costs nothing observable.
//
// effectiveDelay is the SAME value serveGenerated already computed to sleep
// on (effectiveDelayMs) — passed in rather than recomputed, so this function
// has one source for it and Preview (which never sleeps) still gets to
// report it via [domain.AssembledResponse.EffectiveDelay].
func (p *Plane) assembleResponse( //nolint:gocyclo // half of the pre-split serveGenerated's own 47: the recipes/patched-schema assembly, the browser-executable refusal, the pinned-vs-generated switch with its MaxResponse re-check, and the empty-body/envelope branches — D1's own split boundary, not further splittable without breaking the "one function per caller-visible outcome" shape D6's ordered table already fixes
	ws *workspaces.Workspace,
	rt *runtime,
	route *router.Route,
	row *overrides.Row,
	rv resolved,
	pathParams map[string]string,
	query url.Values,
	effectiveDelay int,
	ref recipes.Ref,
	lookup assetLookup,
	assetBase string,
) domain.AssembledResponse {
	pinned := rv.Override != nil && rv.Override.Mode == "pinned"
	// A6 (D5): a bodyRef is a pinned body of a different origin. The nil
	// guard is the one the pinned predicate above already has — Override
	// is nil for every request with no override row.
	bodyRef := rv.Override != nil && rv.Override.BodyRef != ""

	req := gen.Request{
		Method:        route.Method,
		CanonicalPath: route.CanonicalPath,
		PathParams:    pathParams,
		Query:         query,
		Status:        rv.Variant.HTTPStatus,
		ListFamily:    router.ListFamily(rt.table, route),
		IDParam:       router.DetailIDParam(route),
		// P3c (D5): assembleResponse RECEIVES the resolver, it never
		// builds one — serveGenerated constructs the live closure from
		// rt.resources/p.entities/r.Context() and passes it in; Preview
		// passes nil, the same way it already declines to reach
		// livestate.Apply, so a draft never touches real entity rows.
		Ref: ref,
		// A6 (D7): per request, off the Request — see gen.Request.AssetBase.
		AssetBase: assetBase,
	}

	var schemaPatchApplied bool
	var recipesBound int
	switch {
	case rv.Inline != nil:
		// P7a: the custom endpoint's own schema is the root and its own
		// recipes the set — the same two Request fields an override's
		// patched root and recipe set use, filled from the row instead.
		req.PatchedSchema = rv.Inline.Schema
		set := rv.Inline.Recipes
		if rv.Inline.ListSize != nil {
			set = withListSizeRecipe(set, rv.Inline.ListSize)
		}
		req.Recipes = set
		if set != nil {
			recipesBound = set.Len()
		}
	case rv.OverrideActive && !pinned:
		set, _ := rt.lookupRecipes(route, strconv.Itoa(rv.Variant.HTTPStatus))
		if row.ListSize != nil {
			set = withListSizeRecipe(set, row.ListSize)
		}
		req.Recipes = set
		recipesBound = set.Len()

		// P2e (D2(6)/D3(3)/D6): the SAME gate Recipes uses — a switched-off
		// row must be inert end to end, not just for whichever check
		// happens to run first. Looked up by the variant ALREADY CHOSEN
		// (chooseVariant, or variantForStatus/the synthetic fallback for a
		// forced status, both in resolveVariant), never re-derived from
		// variant.HTTPStatus alone: the root was built and stored keyed by
		// selector, not status (Risk 7), so looking it up any other way
		// could hand this response a root built for a different response of
		// the same operation. A miss (nil, false) leaves req.PatchedSchema
		// at its zero value, and gen.Body resolves v.SchemaPtr exactly as it
		// always has.
		if root, found := rt.lookupPatchedSchema(rv.Variant.OpRowID, rv.Variant.Selector); found {
			req.PatchedSchema = root
			schemaPatchApplied = true
		}
	}

	headers := make(map[string]string)
	for name, value := range rt.gen.Headers(rv.Variant, req) {
		addSafeHeader(headers, name, value)
	}
	if pinned {
		// A pinned header value is user input exactly as much as a
		// spec-declared one — same sanitizer, same reason (setSafeHeader's
		// own doc comment on response splitting).
		for name, value := range rv.Override.Headers {
			addSafeHeader(headers, name, value)
		}
	}

	resp := domain.AssembledResponse{
		Status:             rv.Status,
		Headers:            headers,
		EffectiveDelay:     effectiveDelay,
		SchemaPatchApplied: schemaPatchApplied,
		RecipesBound:       recipesBound,
	}

	if rv.NoBody {
		return resp
	}

	var body []byte
	var genErr error
	// assetType is the asset's stored media type when a bodyRef served it —
	// the ONE place that type reaches the wire (round-1 #4): the tail below
	// sets resp.MediaType from rv.MediaType unconditionally and the
	// envelope gate keys on it, so the arm cannot decide either on its own.
	var assetType string
	switch {
	case dangerousResolvedMediaType(rv.MediaType):
		// The write-time guard (admin/override_handlers.go's
		// dangerousMediaType) only ever inspects the OPERATOR's own
		// MediaType — leaving it blank falls through to rv.MediaType's
		// spec-declared value, resolved by resolveVariant, which can itself
		// be text/html (round-1 finding #9): bytes served under a
		// browser-executable Content-Type on the SAME origin DESIGN §16's
		// path-routing mode shares with the admin session. This is the one
		// place both the body and the EFFECTIVE (post-fallback) media type
		// are in scope, so it is the last gate: refuse the body exactly like
		// any other body error — honestly empty, no Content-Type to sniff.
		//
		// NOT qualified by `pinned`, and that is a fix, not an oversight. It
		// was, which made it the same shape of hole as the write gate's old
		// `Mode == "pinned" &&`: a GENERATED body needs no override row at
		// all, and an imported document that declares text/html on a response
		// whose example carries a <script> was served verbatim, status 200,
		// straight out of gen.finalize (which returns a text/* string raw).
		// The document is operator-supplied through POST /api/specs, so that
		// was an admin write path to a same-origin script.
		//
		// Refusing costs nothing measurable: the real customer document this
		// project targets declares 276 application/json responses, 3
		// text/plain, 2 multipart/form-data and 1 text/csv — no
		// browser-executable type anywhere. A spec that did declare one now
		// gets an empty body and the warn below, which is the same answer a
		// pinned one has always got, and the alternative is a script running
		// against a colleague's live admin session in a mode DESIGN itself
		// calls аварийный.
		p.log.Warn("pinned response resolved to a browser-executable media type; refusing to serve the body",
			"workspace", ws.Slug, "operation", route.OperationLabel, "method", route.Method,
			"path", route.CanonicalPath, "status", rv.Variant.HTTPStatus, "mediaType", rv.MediaType)
		resp.Refused = &domain.RefusedReason{Reason: "browser_executable_media_type", Detail: rv.MediaType}
	case rv.PreBuilt != nil:
		// P3a (D6 R20): a confirmed resource already produced this body —
		// [resourceBranch] measured and capped it BEFORE assembleResponse
		// ever ran, so there is nothing to generate and nothing to
		// MaxResponse-recheck here, unlike the pinned case right below.
		// Placed AFTER the browser-executable refusal on purpose: a
		// resource's stored bytes are always JSON (R7/R39 — nothing this
		// slice ever writes under a browser-executable type), but the
		// refusal above still runs first, exactly as D6 R20 specifies, so a
		// future caller can never bypass it by setting PreBuilt.
		body = rv.PreBuilt
	case bodyRef:
		// A6 (D5): AFTER the executable-type refusal (a spec-declared
		// text/html refuses the whole body before any reference is read)
		// and AFTER PreBuilt (which a pinned variant never coexists with —
		// resource takeover exits for pinned, resource.go — but the order
		// is stated so a later caller cannot bypass either), BEFORE
		// pinned. The lookup owns every reason it can decline and marks the
		// traffic itself; nil is Preview, handled exactly as refValue
		// handles a nil Ref — missing, no call. The bytes are NOT
		// re-checked against MaxResponse: an asset is bounded by its own
		// cap (§32.2), and the executable-type gate ran inside the lookup.
		name, wellFormed := assets.NameFromBodyRef(rv.Override.BodyRef)
		if !wellFormed || lookup == nil {
			// A malformed stored reference (a hand-run UPDATE) is missing,
			// never a query for ""; a nil lookup is Preview.
			resp.AssetMissing = true
		} else if meta, data, ok := lookup(name); ok {
			body, assetType = data, meta.MediaType
		} else {
			resp.AssetMissing = true
		}
	case pinned:
		body, genErr = pinnedBody(*rv.Override)
		if genErr == nil && int64(len(body)) > p.cfg.MaxResponse {
			// A pinned body is written ONCE (overrides.ValidateVariant
			// already rejects one over maxPinnedBodyBytes) but SERVED on
			// every unauthenticated request thereafter, with no per-request
			// cost gate of its own — unlike a generated body, which
			// gen.Options.MaxBytes bounds on every call (round-1 findings
			// #3/#8). Re-checking against the SAME ceiling the generator
			// itself is held to (cfg.MaxResponse, the LIVE config — which
			// can be lowered after a body was already stored under a larger
			// one) closes that gap.
			p.log.Warn("pinned response body exceeds MOCKER_MAX_RESPONSE; refusing to serve it",
				"workspace", ws.Slug, "operation", route.OperationLabel, "method", route.Method,
				"path", route.CanonicalPath, "status", rv.Variant.HTTPStatus,
				"bodyBytes", len(body), "maxResponse", p.cfg.MaxResponse)
			resp.Refused = &domain.RefusedReason{
				Reason: "pinned_body_too_large",
				Detail: fmt.Sprintf("%d bytes exceeds MOCKER_MAX_RESPONSE (%d)", len(body), p.cfg.MaxResponse),
			}
			body = nil
		} else if genErr != nil {
			p.log.Error("decode pinned response body", "workspace", ws.Slug, "operation", route.OperationLabel,
				"method", route.Method, "path", route.CanonicalPath, "status", rv.Variant.HTTPStatus, "err", genErr)
			resp.Refused = &domain.RefusedReason{Reason: "pinned_body_undecodable", Detail: genErr.Error()}
			body = nil
		}
	default:
		body, genErr = rt.gen.Body(rv.Variant, req)
		if genErr != nil {
			// Never a 500: the mock keeps answering something, honestly typed,
			// rather than failing a frontend booting against a schema quirk it
			// has no control over.
			p.log.Error("generate response body", "workspace", ws.Slug, "operation", route.OperationLabel,
				"method", route.Method, "path", route.CanonicalPath, "status", rv.Variant.HTTPStatus, "err", genErr)
			resp.Refused = &domain.RefusedReason{Reason: "generation_failed", Detail: genErr.Error()}
			body = nil
		}
	}

	if assetType != "" {
		// A6: the asset's type on the wire, verbatim bytes, no envelope —
		// §32.3's "serves the asset's bytes verbatim under the asset's own
		// media type" — and nosniff, the third half of §32.6's promise, on
		// this path as on the route. BEFORE the empty-body return: a
		// zero-byte upload is a legal asset and is not a missing one. The
		// variant's declared type was refused at write time
		// (overrides.ValidateVariant), so rv.MediaType here is the spec's
		// own, which has nothing to say about a file.
		resp.Headers["X-Content-Type-Options"] = "nosniff"
		resp.MediaType = assetType
		resp.Body = body
		// The writer sets Content-Type on an empty body only when told to
		// (its own WriteMediaType rule); a zero-byte asset is a body of its
		// own type, not a refusal.
		resp.WriteMediaType = true
		return resp
	}

	if len(body) == 0 {
		// WriteMediaType is true only for the two REFUSED cases that carry a
		// generation error (undecodable pinned body, generation failure):
		// the browser-executable refusal and the oversized-pinned refusal
		// both leave genErr nil, and the writer must not set a Content-Type
		// for either — matching the pre-split function's own
		// `genErr != nil && mediaType != ""` condition exactly.
		resp.MediaType = rv.MediaType
		resp.WriteMediaType = genErr != nil && rv.MediaType != ""
		return resp
	}

	if rt.settings.Envelope != nil && httpx.IsJSONMediaType(rv.MediaType) {
		if wrapped, werr := wrapEnvelope(*rt.settings.Envelope, body); werr != nil {
			// Belt-and-suspenders: a JSON-declared media type whose bytes
			// somehow are not valid JSON anyway (should not happen — gen.go's
			// own finalize marshals JSON media types through
			// encoding/json, and a pinned body's own BodyEncoding=="base64"
			// path is the one legitimate way to carry non-JSON bytes under a
			// JSON media type, which is exactly the case this guard exists
			// to leave alone — but wrapEnvelope's own validity check is
			// cheap insurance against trusting either source blindly). Leave
			// the body exactly as produced rather than risk corrupting it.
			p.log.Debug("skip envelope: body is not valid JSON", "workspace", ws.Slug, "operation", route.OperationLabel, "err", werr)
		} else {
			body = wrapped
		}
	}

	resp.MediaType = rv.MediaType
	resp.Body = body
	return resp
}
