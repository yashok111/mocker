// custom.go serves a matched CUSTOM ENDPOINT (DESIGN §8, P1c2): a route an
// operator defined from nothing, with no operations row and therefore no
// declared response variant, no schema, no document at all to fall back on.
// It is respond.go's other half — routes.go dispatches a matched
// router.Route into [Plane.serveGenerated] when Custom is false and into
// [Plane.serveCustom] here when it is true — and it deliberately reuses
// every wire-assembly helper respond.go already exports to this package
// (pinnedBody, setSafeHeader, acceptable, wrapEnvelope, awaitDelay,
// effectiveDelayMs, resolvePause, dangerousResolvedMediaType, ...) rather
// than growing a second copy of any of them: the two paths differ only in
// WHERE a response comes from — a document-driven [gen.Generator] there,
// [customep.Row.Responses] alone here — never in how a chosen status
// becomes bytes on the wire.
//
// Because there is no document, there is no [gen.chooseVariant] step and no
// generation step either: a custom endpoint's ONLY source of a body is a
// "pinned" entry in its own Responses map. Every other case — mode
// "generated" (the default an operator never had to set), or simply no
// entry at all for the status actually being served — has no schema to
// walk and answers with an empty body. That is DESIGN's own honest
// silence, not a degraded/error response; see [Plane.serveCustom]'s own
// doc comment for the full order this mirrors from respond.go.
package mockplane

import (
	"context"
	"fmt"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/livestate"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// CustomSource is *customep.Repo as the plane needs it: every custom
// endpoint of a workspace, in one query, exactly the shape
// [OverrideSource.ForWorkspace] and [SpecSource.Routes] already established
// for the same reason — this package never has to import the concrete
// customep.Repo type, and a test can fake the one method the runtime build
// actually needs with no database at all.
type CustomSource interface {
	ForWorkspace(ctx context.Context, workspaceID int64) ([]*customep.Row, error)
}

// SetCustomEndpoints wires src as the Plane's [CustomSource]. Call it once,
// during startup, right after [New] and before the Plane serves its first
// request — the identical startup-only calling contract [Plane.SetOverrides]
// establishes (see that method's own doc comment for why there is no lock:
// p.custom is written once and only ever read afterward, and calling this
// concurrently with ServeHTTP/ServeSlug is a data race go test -race
// correctly catches).
//
// A Plane whose SetCustomEndpoints is never called behaves exactly as one
// built before this file existed: buildRuntime (runtime.go) sees a nil
// CustomSource and loads no custom rows at all, routes.go's serveRoute keeps
// answering the plain pre-P1c2 404 for a spec-less workspace, and every
// custom_endpoints row of a workspace that DOES have a spec simply never
// joins the table — HARD RULE 6, the identical "no source wired, no change"
// contract every other P1c/P1c2 source already follows.
func (p *Plane) SetCustomEndpoints(src CustomSource) {
	p.custom = src
}

// lookupCustom returns the customep.Row a matched custom route's
// CustomRowID identifies. ok is false only if the row is somehow missing
// from what buildRuntime loaded — which, by construction, cannot happen for
// a route this SAME build put in the table (customRoutes and runtime.custom
// are populated from the identical loop, runtime.go) — but is checked
// anyway rather than trusted blindly, exactly like every other lookup this
// package does against data a stale cache entry or a future bug could
// desync.
func (rt *runtime) lookupCustom(id int64) (*customep.Row, bool) {
	if rt.custom == nil {
		return nil, false
	}
	row, ok := rt.custom[id]
	return row, ok
}

// serveCustom answers a matched custom route. The order mirrors
// [Plane.serveGenerated] (respond.go) exactly, per this slice's own digest,
// minus the steps that only make sense with a document behind them:
//
//  0. route_off: the SAME 404 [Plane.serveNoRoute] gives an unmatched route
//     (DESIGN §8) — no livestate.Apply, no delay, no pause, nothing else
//     consumed.
//  1. livestate.Apply(ws.ID, route.Method, route.Path), once, exactly like
//     a spec route.
//  2. resolvePause (respond.go, reused here), THEN the delay. A matched
//     pause parks the request BEFORE anything is written — the identical
//     rule-4 ordering a spec route follows, and a canceled request context
//     ends the park writing nothing. The delay itself: SESSION beats ROW
//     beats WORKSPACE settings, through the SAME [effectiveDelayMs] a spec
//     route uses. There is no "overrideActive" gate on the row here the way
//     op_overrides needs one, because a custom row that reached this
//     function already passed OverrideOn at BUILD time (runtime.go): every
//     row in rt.custom is, by construction, active, so row.DelayMs is
//     passed straight through.
//  3. the status: a live force beats a matching when[] beats the row's own
//     ActiveStatus — no fourth "document's own choice" step, because there
//     is no document. ActiveStatus is never the zero value here:
//     customep.Row's own contract defaults an unset one to 200 at write
//     time (customep.go's normalizeAndValidate), so this always resolves
//     to a real status.
//  4. the row's own Responses[status] entry, if any: mode "pinned" serves
//     its literal body (through the SAME pinnedBody/dangerousResolvedMediaType/
//     MaxResponse gates a spec route's pinned body goes through — see the
//     inline comments below for why the MaxResponse re-check cannot be
//     skipped just because this is a different call site); anything else —
//     mode "generated", or simply no entry for this status — has no schema
//     to generate from and answers with an empty body, which is honest
//     silence, not a failure.
//  5. 204/205 never carry a body, same as a spec route.
//  6. Accept negotiation against the row's own pinned MediaType (nothing to
//     negotiate against otherwise — acceptable("", "") always accepts).
//  7. the row's own pinned headers, sanitized through setSafeHeader exactly
//     like a spec route's pinned headers — there are no document-declared
//     headers to layer under them here, since there is no document.
//  8. the body, enveloped exactly like a spec route's generated one
//     (settings.Envelope applies uniformly — DESIGN never scopes it to
//     spec-only operations), Content-Type/Content-Length set once.
func (p *Plane) serveCustom(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match, base resources.ScopeKey) { //nolint:gocyclo // the custom-route serving path end to end: resolve, match, live state, variant, write
	route := m.Route

	row, ok := rt.lookupCustom(route.CustomRowID)
	if !ok {
		// See lookupCustom's own doc comment: this is not a case the current
		// build can actually produce, but a matched route with nothing to
		// serve it must still answer something rather than panic on a
		// nil *customep.Row below.
		p.log.Error("custom route matched but its row is missing from the runtime",
			"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath, "customRowID", route.CustomRowID)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}

	if row.RouteOff {
		// Identical to op_overrides' own route_off (respond.go): the exact
		// unmatched-route 404, never a distinguishable "disabled" shape.
		p.serveNoRoute(w, r, ws, NormalizeSegments(r.URL.EscapedPath()))
		return
	}

	var liveEffect livestate.Effect
	if p.livestate != nil {
		liveEffect = p.livestate.Apply(ws.ID, route.Method, route.Path)
	}

	// Pause sits BEFORE the delay (rule 4), exactly like a spec route —
	// reusing respond.go's own resolvePause rather than a second copy (this
	// file's own doc comment above).
	if !resolvePause(r, liveEffect) {
		return // request context ended while parked: nothing written yet
	}

	delayMs := effectiveDelayMs(liveEffect.DelayMs, row.DelayMs, rt.settings.DelayMs)
	if !awaitDelay(r.Context(), delayMs) {
		return // context canceled/timed out mid-sleep: nothing left to write
	}

	status := liveEffect.Status
	if status == 0 {
		if key, found := overrides.SelectWhen(row.Responses, overridesInputFor(r)); found {
			// SelectWhen only ever returns a key that already parsed as a
			// decimal status (its own doc comment) — nothing to guard here.
			status, _ = strconv.Atoi(key)
		} else {
			status = row.ActiveStatus
		}
	}

	// P6b (D7): a stream row branches here, AFTER the session layer above
	// and ONLY when no status was forced — a forced 503 falls through to
	// the ordinary path below and answers 503 with an empty body, which is
	// exactly "a forced status aborts the handshake" (§30.4). An sse row
	// carries no responses, so status is its activeStatus (200) whenever
	// nothing forced it.
	if row.Kind == customep.KindSSE && liveEffect.Status == 0 {
		p.serveStream(w, r, ws, rt, row)
		return
	}
	// P6d (D7): the WebSocket row branches at the same point, under the
	// same rule — a forced status falls through and aborts the handshake
	// with that status (§30.4), never a 101.
	if row.Kind == customep.KindWS && liveEffect.Status == 0 {
		p.serveWS(w, r, ws, rt, row)
		return
	}

	variant, found := row.Responses[strconv.Itoa(status)]
	pinned := found && variant.Mode == "pinned"

	// A18 (D7): the function branch, at the same logical position it takes on
	// a spec operation — after route_off, the session layer, the pause, the
	// delay and the stream branches — and BEFORE the 406 gate, which is the
	// one place the two matrices differ. A spec operation negotiates against
	// the type its DOCUMENT declares, which exists before anything runs; a
	// custom endpoint's only declared type belongs to a PINNED variant, and a
	// function variant is not pinned, so there is nothing here to negotiate
	// against until the function has run and said what it produced.
	//
	// No resource takeover to order against either: a custom endpoint is
	// never a resource (routes.go). base is still the request's own base
	// scope, because mock.entities reads rows that are partitioned by it.
	if source, ok := functionSource(variantPtr(row.Responses, strconv.Itoa(status))); ok {
		p.serveFunction(w, r, ws, rt, route, m, base, source)
		return
	}

	// P7a (DESIGN §34.3): a generated variant WITH a schema leaves this
	// function for the one seam every generated response goes through —
	// recipes, ref, envelope, byte budget all apply there. A pinned
	// variant, a variant with no schema and every stream row keep the
	// path below unchanged (the mode rule of §8: pinned outranks the
	// schema, which then only declares the export's shape).
	if found && !pinned {
		if src, ok := rt.lookupCustomInline(row.ID, strconv.Itoa(status)); ok {
			p.serveCustomGenerated(w, r, ws, rt, m, variant, src, status, delayMs)
			return
		}
	}

	noBody := status == http.StatusNoContent || status == http.StatusResetContent

	var mediaType string
	if pinned && variant.MediaType != "" {
		mediaType = variant.MediaType
	}

	if !noBody && mediaType != "" && !acceptable(r.Header.Get("Accept"), mediaType) {
		httpx.Err(w, http.StatusNotAcceptable, "not_acceptable", fmt.Sprintf(
			"%s %s only offers %q for this response; Accept %q excludes it",
			route.Method, route.CanonicalPath, mediaType, r.Header.Get("Accept")))
		return
	}

	if pinned {
		// No document-declared headers exist to layer these under — a
		// custom endpoint has no operation, so this is the entire header
		// set, sanitized exactly like a spec route's pinned headers
		// (setSafeHeader's own doc comment: a stored header is exactly as
		// attacker-reachable as one declared in a spec).
		for name, value := range variant.Headers {
			setSafeHeader(w, name, value)
		}
	}

	if noBody {
		w.WriteHeader(status)
		return
	}

	var body []byte
	var bodyErr error
	// assetType is the asset's stored media type when a bodyRef served it —
	// the one place that type reaches the wire, exactly as respond.go's own
	// assetType (A6 D5). A custom endpoint's variant is the same
	// overrides.Variant a spec operation's is, so §32.3's "an operation
	// override or a custom endpoint" is one arm written twice, not two
	// features.
	var assetType string
	switch {
	case dangerousResolvedMediaType(mediaType):
		// Same last-line gate a spec route's pinned body goes through
		// (respond.go): the write-time guard only ever inspected the
		// operator's own MediaType, never a fallback resolved this late —
		// except a custom endpoint has no spec-declared fallback to fall
		// back TO, so mediaType here is always the operator's own value,
		// re-checked anyway rather than assuming this call site cannot
		// reach a browser-executable type just because the fallback path
		// doesn't apply to it.
		//
		// Unqualified by `pinned`, in step with respond.go's own gate: this
		// switch is the last thing between a resolved media type and the
		// wire, and what produced the body underneath it does not change
		// whether a browser would execute it.
		p.log.Warn("pinned response resolved to a browser-executable media type; refusing to serve the body",
			"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath, "status", status, "mediaType", mediaType)
	case pinned && variant.BodyRef != "":
		// A6: the body IS an uploaded asset. The lookup owns every reason
		// it can decline and marks asset_missing itself; a malformed stored
		// reference (a hand-run UPDATE) is missing too, never a query for
		// "". Not re-checked against MaxResponse: an asset is bounded by
		// its own cap (§32.2), and the executable-type gate ran inside the
		// lookup.
		if name, ok := assets.NameFromBodyRef(variant.BodyRef); ok {
			if meta, data, found := p.newAssetLookup(r.Context(), r, ws)(name); found {
				body, assetType = data, meta.MediaType
			}
		} else {
			markAssetMissing(r)
		}
	case pinned:
		b, err := pinnedBody(variant)
		switch {
		case err == nil && int64(len(b)) > p.cfg.MaxResponse:
			// The SAME re-check respond.go's own comment insists on: a
			// pinned body is written ONCE but served on every
			// unauthenticated request thereafter, with no per-request cost
			// gate of its own — this closes that gap for a custom
			// endpoint's pinned body exactly like it does for op_overrides'.
			p.log.Warn("pinned response body exceeds MOCKER_MAX_RESPONSE; refusing to serve it",
				"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath,
				"status", status, "bodyBytes", len(b), "maxResponse", p.cfg.MaxResponse)
		case err != nil:
			bodyErr = err
			p.log.Error("decode pinned response body", "workspace", ws.Slug,
				"method", route.Method, "path", route.CanonicalPath, "status", status, "err", err)
		default:
			body = b
		}
	default:
		// Mode "generated" (the default), or simply no Responses entry for
		// this status: a custom endpoint has no schema to walk either way,
		// so this is DESIGN's own honest silence — an empty body at the
		// resolved status, never an error.
	}

	if assetType != "" {
		// A6: verbatim bytes under the asset's own type, no envelope, and
		// nosniff (§32.6) — BEFORE the empty-body return, because a
		// zero-byte upload is a legal asset and must not read as a missing
		// one.
		w.Header().Set("Content-Type", assetType)
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Content-Length", strconv.Itoa(len(body)))
		w.WriteHeader(status)
		if _, werr := w.Write(body); werr != nil {
			p.log.Debug("write custom asset response", "workspace", ws.Slug, "err", werr)
		}
		return
	}

	if len(body) == 0 {
		if bodyErr != nil && mediaType != "" {
			// The declared type, so the client at least learns what shape
			// was intended even though the pinned body could not be decoded.
			w.Header().Set("Content-Type", mediaType)
		}
		w.WriteHeader(status)
		return
	}

	if rt.settings.Envelope != nil && httpx.IsJSONMediaType(mediaType) {
		if wrapped, werr := wrapEnvelope(*rt.settings.Envelope, body); werr != nil {
			p.log.Debug("skip envelope: body is not valid JSON", "workspace", ws.Slug, "err", werr)
		} else {
			body = wrapped
		}
	}

	contentType := mediaType
	if contentType == "" {
		contentType = "application/json; charset=utf-8"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	if _, werr := w.Write(body); werr != nil {
		p.log.Debug("write custom response", "workspace", ws.Slug, "err", werr)
	}
}

// variantPtr addresses a row's variant so [functionSource] can ask its one
// question of a custom endpoint's variant and a spec operation's through the
// same signature — resolved.Override is already a pointer on that path, and a
// map index is not addressable here. It returns nil for a missing key, which
// functionSource reads as "no function", so the caller needs no second check.
func variantPtr(responses map[string]overrides.Variant, status string) *overrides.Variant {
	v, ok := responses[status]
	if !ok {
		return nil
	}
	return &v
}

// serveCustomGenerated is P7a's branch out of serveCustom: a custom
// endpoint's generated response, assembled through assembleResponse
// exactly as a spec operation's is. It builds the [resolved] the seam
// takes by hand — there is no spec variant to resolve, so the schema
// pointer is the sentinel customSchemaPtr and the root travels as
// rv.Inline — and writes the result through the same tail serveGenerated
// uses. The 406 gate runs here because serveCustom's own only knows a
// pinned variant's media type; a generated one declares application/json
// unless the variant says otherwise. No resource takeover: a custom
// endpoint is never a resource (routes.go), so the base scope the ref
// resolver takes is the empty one.
func (p *Plane) serveCustomGenerated(
	w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rt *runtime, m *router.Match,
	variant overrides.Variant, src inlineSource, status, delayMs int,
) {
	route := m.Route
	mediaType := variant.MediaType
	if mediaType == "" {
		mediaType = "application/json"
	}
	noBody := status == http.StatusNoContent || status == http.StatusResetContent
	if !noBody && !acceptable(r.Header.Get("Accept"), mediaType) {
		httpx.Err(w, http.StatusNotAcceptable, "not_acceptable", fmt.Sprintf(
			"%s %s only offers %q for this response; Accept %q excludes it",
			route.Method, route.CanonicalPath, mediaType, r.Header.Get("Accept")))
		return
	}
	rv := resolved{
		Variant:      gen.ResponseVariant{SchemaPtr: customSchemaPtr, HTTPStatus: status, MediaType: mediaType},
		MediaType:    mediaType,
		NoBody:       noBody,
		Status:       status,
		StatusSource: domain.StatusSourceActive,
		Inline:       &src,
	}
	ref := p.newRefResolver(r.Context(), rt.resources, p.entities, ws, r, "")
	asm := p.assembleResponse(ws, rt, route, nil, rv, m.Params, r.URL.Query(), delayMs, ref,
		p.newAssetLookup(r.Context(), r, ws), p.assetBase(r, ws))
	// The variant's own headers, exactly as serveCustom's pinned arm writes
	// them: assembleResponse layers override headers only under a pinned
	// variant, and this path has no override row, so without this a
	// generated custom variant would silently drop what the operator set.
	for name, value := range variant.Headers {
		setSafeHeader(w, name, value)
	}
	p.writeAssembled(w, ws, rv.NoBody, asm)
}
