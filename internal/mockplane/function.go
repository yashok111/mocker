// function.go is A18's serving branch: an operation whose selected variant
// carries Lua source produces its response BY RUNNING that source, instead of
// having one assembled for it.
//
// The branch is a SIBLING of [Plane.assembleResponse], never a fourth caller
// of it (D7): it produces the bytes that function would have produced and
// writes them through its own tail. seam_test.go's wantCallers literal is
// unchanged for exactly that reason, and its comment says so.
//
// Everything a function returns crosses this file's safety tail before a byte
// reaches the wire (D6). That tail is not a copy of what the rest of the plane
// does to a generated header — it is STRICTER in one direction and LOOSER in
// another, and both differences are deliberate:
//
//   - STRICTER: a bad header or a browser-executable Content-Type REFUSES the
//     whole response with 500 function_failed, where a spec-declared header the
//     same predicate rejects is merely DROPPED (negotiate.go's headerIsSafe).
//     A generated header comes from a document an operator uploaded and may not
//     have read; a function's header is a line of code its author wrote this
//     minute, and answering it with silence teaches the wrong contract — the
//     same argument D3 already makes for refusing a boolean body rather than
//     coercing it.
//   - LOOSER: unsafeResponseHeaderName is NOT applied. That map exists because
//     a header value can come out of an uploaded OpenAPI document, which §15
//     treats as attacker-controllable; a function's source is operator-authored
//     code at the same trust level as a pinned body, and D3 names one
//     Set-Cookie as the sign-in shape this feature is FOR. What stays refused
//     here is only what would corrupt framing — Content-Length and
//     Transfer-Encoding, which the writer computes itself.
package mockplane

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/gen"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/jsonx"
	"github.com/yashok111/mocker/internal/luafn"
	"github.com/yashok111/mocker/internal/openapi"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/recipes"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/router"
	"github.com/yashok111/mocker/internal/workspaces"
)

// maxFunctionHeaderValue is D6's "sane length" for one header value a function
// returned, made a number here rather than left to the reader. 8 KiB is well
// under the 64 KiB http.Server.MaxHeaderBytes this tree sets for INBOUND
// headers (A15) and far above anything the sign-in shape needs; a function that
// wants to move more than that has a body to put it in.
const maxFunctionHeaderValue = 8 << 10

// maxFunctionHeaders and maxFunctionHeaderBytes bound the header SET where
// maxFunctionHeaderValue bounds one value: a Lua loop can return a table of a
// hundred thousand 8 KiB headers, and with no cap on the set the response
// header block is the one output MOCKER_MAX_RESPONSE does not reach. Sixty-four
// fields and 64 KiB of names plus values — the same figure as the inbound
// MaxHeaderBytes — is more than any response a mock has a reason to build.
// Review finding 13.
const (
	maxFunctionHeaders     = 64
	maxFunctionHeaderBytes = 64 << 10
)

// functionDefaultTextType is the Content-Type an untyped STRING body is served
// under. Before it existed the branch wrote such a body with no Content-Type
// at all, and net/http then sniffs the first 512 bytes: `return 200,
// "<script>…"` went to the wire as `text/html; charset=utf-8` — the one
// browser-executable rule both planes enforce, bypassed by leaving the field
// empty, on the unauthenticated plane. The pinned path never had the hole
// because custom.go defaults an empty type to application/json; a function's
// string return is the author's own bytes, so text/plain is the honest
// default and an author who means JSON returns a table or says so in a
// header. httptest.ResponseRecorder does NOT reproduce the sniff after an
// explicit WriteHeader, which is why the test for this runs a real server.
// Review finding 2.
const functionDefaultTextType = "text/plain; charset=utf-8"

// functionFramingHeader is the whole of what a function may not set: the two
// fields the writer computes for itself, where a second value is not a policy
// disagreement but a corrupted response. Content-Type is absent on purpose —
// it is CONSUMED by the branch below as the function's declared type rather
// than refused.
var functionFramingHeader = map[string]bool{
	"content-length":    true,
	"transfer-encoding": true,
}

// functionSource reports the Lua a variant carries, if any. It is a function
// and not an inline field read so both call sites (respond.go's spec-operation
// branch and custom.go's) ask the question the same way, and so the "is this
// variant a function variant" predicate has one owner the day a second field
// can carry one.
func functionSource(ov *overrides.Variant) (string, bool) {
	if ov == nil || ov.Function == "" {
		return "", false
	}
	return ov.Function, true
}

// serveFunction runs source against r and writes the whole response.
//
// base is the request's own base scope and outer the ordered values of every
// {} segment the matched route carries — both computed by the caller from the
// match it already has, never re-parsed here, the same construct-and-receive
// split this package keeps for the ref resolver and the asset lookup.
func (p *Plane) serveFunction(
	w http.ResponseWriter, r *http.Request,
	ws *workspaces.Workspace, rt *runtime,
	route *router.Route, m *router.Match,
	base resources.ScopeKey, source string,
) {
	// The body is the one Step 5 already captured (reqbody.go), never a
	// second read: r.Body was restored to a stream of the same bytes, but
	// reading it here would race every other consumer of that stream and
	// would ALSO read past the cap when[] matching is bounded by. A function
	// therefore sees exactly what a when[] condition sees — up to
	// requestBodyCap bytes — and a body past that is truncated rather than
	// refused, which is the behaviour every other body consumer on this
	// plane already has.
	var body []byte
	if cb := capturedBodyFromContext(r); cb != nil {
		body = cb.bytes
	}

	req := luafn.Request{
		Method:     route.Method,
		Path:       route.CanonicalPath,
		PathParams: m.Params,
		Query:      r.URL.Query(),
		Headers:    r.Header,
		Body:       body,
	}
	host := p.newLuaHost(rt, ws, base, routeOuterValues(route, m), genRequestFor(route.Method, route.CanonicalPath, m.Params))

	resp, err := luafn.Run(r.Context(), source, req, host)
	if err != nil {
		p.answerFunctionError(w, r, ws, route, err)
		return
	}

	p.writeFunctionResponse(w, r, ws, route, resp)
}

// answerFunctionError is D6's classification, and the three outcomes are three
// because the plane answers them differently.
func (p *Plane) answerFunctionError(
	w http.ResponseWriter, r *http.Request,
	ws *workspaces.Workspace, route *router.Route, err error,
) {
	switch {
	case errors.Is(err, luafn.ErrCanceled):
		// The client is gone. Nothing is written and nothing is classified:
		// a request nobody is waiting for is not a server error, and
		// recording it as one would make an operator chase a fault that
		// belongs to a closed connection (D6).
		return
	case errors.Is(err, luafn.ErrTimeout):
		markFunctionNote(r, noteFunctionTimeout)
		p.log.Warn("endpoint function exceeded its time budget",
			"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath)
		httpx.Err(w, http.StatusServiceUnavailable, "function_timeout",
			"the endpoint function exceeded its time budget")
	default:
		// The capped first line reaches the CLIENT as well as the traffic
		// note. An author debugging their own function through curl is the
		// primary loop this feature is for, and sending them to a second
		// surface for the one sentence that says what broke would make the
		// 500 useless where it is read most. It is the same 200 bytes
		// luafn.Note already trims to for the note, for the same reason:
		// Lua error text can echo request data.
		note := luafn.Note(err)
		markFunctionNote(r, noteFunctionFailed)
		p.log.Warn("endpoint function failed", "workspace", ws.Slug,
			"method", route.Method, "path", route.CanonicalPath, "err", note)
		httpx.Err(w, http.StatusInternalServerError, "function_failed", note)
	}
}

// writeFunctionResponse is the safety tail and the write, in the one order D6
// fixes: every refusal is decided BEFORE WriteHeader, so a refused response is
// never a committed status with a body the plane then declined to finish.
func (p *Plane) writeFunctionResponse(
	w http.ResponseWriter, r *http.Request,
	ws *workspaces.Workspace, route *router.Route, resp luafn.Response,
) {
	headers, mediaType, err := functionHeaders(resp)
	if err != nil {
		markFunctionNote(r, noteFunctionFailed)
		p.log.Warn("endpoint function returned a header the plane refuses",
			"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath, "err", err)
		httpx.Err(w, http.StatusInternalServerError, "function_failed", err.Error())
		return
	}

	if int64(len(resp.Body)) > p.cfg.MaxResponse {
		markFunctionNote(r, noteFunctionTooLarge)
		p.log.Warn("endpoint function body exceeds MOCKER_MAX_RESPONSE; refusing to serve it",
			"workspace", ws.Slug, "method", route.Method, "path", route.CanonicalPath,
			"bodyBytes", len(resp.Body), "maxResponse", p.cfg.MaxResponse)
		httpx.Err(w, http.StatusInternalServerError, "function_too_large", fmt.Sprintf(
			"the endpoint function returned %d bytes, over MOCKER_MAX_RESPONSE", len(resp.Body)))
		return
	}

	markFunctionNote(r, noteFunction)
	for name, value := range headers {
		w.Header().Set(name, value)
	}
	// nosniff on every function response, typed or not: the type was checked
	// against the browser-executable rule a moment ago, and the header tells
	// the browser to believe it rather than the bytes. The asset path in
	// custom.go sets the same header for the same reason.
	w.Header().Set("X-Content-Type-Options", "nosniff")
	// The type goes on BEFORE the empty-body return: a function that answers
	// `204, nil, {["Content-Type"] = …}` declared it, and dropping it because
	// there is no body to describe was review finding 14.
	if mediaType != "" {
		w.Header().Set("Content-Type", mediaType)
	}
	if len(resp.Body) == 0 {
		w.WriteHeader(resp.Status)
		return
	}
	w.Header().Set("Content-Length", strconv.Itoa(len(resp.Body)))
	w.WriteHeader(resp.Status)
	// G705: the body is the operator's own code's output, which is this
	// branch's entire purpose. The media types that would turn it into an XSS
	// vector were refused three statements above, in functionHeaders.
	if _, werr := w.Write(resp.Body); werr != nil { //nolint:gosec
		p.log.Debug("write function response", "workspace", ws.Slug, "err", werr)
	}
}

// functionHeaders validates what a function returned and separates the
// Content-Type out of it: the returned map is what goes on the wire verbatim,
// mediaType is the type the body is served under ("" when the function
// declared none and returned a string — D3's own reading of an untyped body).
//
// Every refusal is BY NAME and refuses the WHOLE response. Sanitizing instead
// would mean answering an author's mistake with a response that looks right and
// is not, which is the coercion D3 already refuses for the status and the body.
func functionHeaders(resp luafn.Response) (map[string]string, string, error) {
	if len(resp.Headers) > maxFunctionHeaders {
		return nil, "", fmt.Errorf("the function returned %d headers, over the %d-field limit",
			len(resp.Headers), maxFunctionHeaders)
	}
	out := make(map[string]string, len(resp.Headers))
	mediaType := resp.MediaType
	total := 0
	for name, value := range resp.Headers {
		total += len(name) + len(value)
		if total > maxFunctionHeaderBytes {
			return nil, "", fmt.Errorf("the function's headers exceed the %d-byte limit together", maxFunctionHeaderBytes)
		}
		if !validHeaderName(name) {
			return nil, "", fmt.Errorf("header name %q is not a valid HTTP field name", name)
		}
		if strings.ContainsAny(value, "\r\n") {
			return nil, "", fmt.Errorf("header %q carries a CR or LF in its value", name)
		}
		if len(value) > maxFunctionHeaderValue {
			return nil, "", fmt.Errorf("header %q is %d bytes, over the %d-byte limit",
				name, len(value), maxFunctionHeaderValue)
		}
		lower := strings.ToLower(name)
		if functionFramingHeader[lower] {
			return nil, "", fmt.Errorf("header %q is computed by the server and may not be set", name)
		}
		if lower == "content-type" {
			// The function's own type WINS over the one luafn inferred from
			// the return shape: a table returned under an explicit
			// text/csv is the author saying what they meant.
			mediaType = value
			continue
		}
		out[name] = value
	}
	if mediaType == "" && len(resp.Body) > 0 {
		// Only a STRING body gets here: a table return arrives typed as
		// application/json from luafn, and an empty body needs no type.
		mediaType = functionDefaultTextType
	}
	if httpx.BrowserExecutableMediaType(mediaType) {
		// The one rule both planes share, applied here at the last line
		// before the wire exactly as respond.go and custom.go apply it to a
		// pinned body — and refusing rather than blanking, because unlike a
		// stored media type this one was never checked at write time: a
		// function computes it per request.
		return nil, "", fmt.Errorf("Content-Type %q is browser-executable and is refused", mediaType)
	}
	return out, mediaType, nil
}

// validHeaderName is RFC 9110's tchar set. net/http would write a name with a
// space or a colon in it straight into the response, which is a splitting
// vector the CR/LF check on the VALUE does not cover — negotiate.go's own
// headerIsSafe checks neither, because a spec-declared header name comes out
// of a document whose keys a JSON parser already constrained, and a function's
// does not.
func validHeaderName(name string) bool {
	if name == "" {
		return false
	}
	for i := range len(name) {
		c := name[i]
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case strings.IndexByte("!#$%&'*+-.^_`|~", c) >= 0:
		default:
			return false
		}
	}
	return true
}

// routeOuterValues is the ordered value of every whole-segment {param} the
// MATCHED route carries, read positionally off route.Path's own pattern — the
// identical discipline scopeOf (resource.go) and router.DetailIDParam already
// use, and never a by-name read out of res.ScopeParams, which would silently
// mis-scope whenever a collection route and a detail route spell an outer
// parameter differently.
//
// It is the request's OWN tuple, not any family's: newLuaHost takes the prefix
// of it a family's depth asks for, the same prefix rule resourceBranch's own
// anchor walk already applies with outer[:i].
func routeOuterValues(route *router.Route, m *router.Match) []string {
	segs := strings.Split(route.Path, "/")
	out := make([]string, 0, len(segs))
	for _, seg := range segs {
		if len(seg) >= 2 && seg[0] == '{' && seg[len(seg)-1] == '}' {
			out = append(out, m.Params[seg[1:len(seg)-1]])
		}
	}
	return out
}

// luaHost is the product half of luafn.Host: it holds exactly what the two
// helpers need and nothing that would let a function reach past its own
// workspace.
type luaHost struct {
	p     *Plane
	rt    *runtime
	ws    *workspaces.Workspace
	base  resources.ScopeKey
	outer []string
	// req is what mock.generate seeds a body by — the method, canonical
	// path and path params of the request (or, on a stream, of the
	// connection's row), the same tuple a generated variant's walker is
	// seeded by, so `mock.generate` on GET /users/{id} draws what
	// GET /users/{id}'s generated 200 would. Query is left out on purpose:
	// a function reads req.query itself and decides what varies.
	req gen.Request
}

// newLuaHost returns nil when there is nothing behind the helpers to reach —
// which luafn reads as "no host" and answers from inside Lua, so a function
// written against a live workspace still runs and reports why (mock.go's own
// nil-Host contract).
func (p *Plane) newLuaHost(rt *runtime, ws *workspaces.Workspace, base resources.ScopeKey, outer []string, req gen.Request) luafn.Host {
	return &luaHost{p: p, rt: rt, ws: ws, base: base, outer: outer, req: req}
}

// genRequestFor is the seed tuple newLuaHost takes, built from a matched
// route; the stream handlers build theirs from the row.
func genRequestFor(method, canonicalPath string, params map[string]string) gen.Request {
	return gen.Request{Method: method, CanonicalPath: canonicalPath, PathParams: params, Status: http.StatusOK}
}

// JWT signs with the workspace's own settings.auth, through the SAME
// recipes.MintJWT the "jwt" recipe calls — not a second signer, so the claim
// order, the epoch unit and the iat/exp stamping are one implementation.
//
// The context is accepted and not used: MintJWT is pure CPU over a map that is
// already bounded, with no store read and no network. Taking it anyway is what
// makes the seam honest — the day the signer grows a key lookup, the deadline
// is already threaded to it rather than needing to be discovered.
func (h *luaHost) JWT(_ context.Context, claims map[string]any) (string, error) {
	auth := h.ws.Settings.Auth
	if auth.Alg == "none" || auth.SigningKey == "" {
		// D3: an unsigned token pretending to be signed is worse than an
		// error. The alg is visible in the token header by JWS construction,
		// so a caller that got one back would have no reason to look.
		return "", errors.New("auth_not_configured")
	}
	token, err := recipes.MintJWT(auth, h.ws.Settings.Identity, claims, 0, time.Now())
	if err != nil {
		// The signer's own words are not passed on: they name internals a
		// function's author cannot act on, and this error becomes a Lua
		// string the function may put in a response.
		return "", errors.New("jwt_failed")
	}
	return token, nil
}

// Entities reads one confirmed family's rows under an explicit or an implied
// scope.
//
// The family is resolved through THIS request's own runtime roster and nothing
// else — the same lookup ref makes, for the same reason: EntityStore.List is
// not workspace-scoped, so any other way of turning a family name into a
// resource id would be one forgotten check away from serving another
// workspace's rows on a plane that is unauthenticated by design (D3).
// resolveFamily is the one reading of (family, scope) every entity helper
// shares — the read and, since A19, the three writers — so the roster rule
// (THIS request's runtime, never the store) and the scope rule cannot drift
// between them.
func (h *luaHost) resolveFamily(family string, scope []string) (*resources.Resource, resources.ScopeKey, error) {
	if h.p.entities == nil || h.rt.resources == nil {
		return nil, "", errors.New("unknown_family")
	}
	res, ok := h.rt.resources[family]
	if !ok {
		// One answer for "never suggested", "declined" and "no such family":
		// telling them apart costs a query per call and the repair is the
		// same in all three, which is the rule A4's own route already
		// settled for this question.
		return nil, "", errors.New("unknown_family")
	}

	depth := len(res.ScopeParams)
	values := scope
	if scope == nil {
		// D3's "omitting the argument keeps the request's own scope", made
		// precise: a scope IS an ancestor tuple read top down, and this
		// request's own outer values are that tuple for every family on its
		// own path — the same prefix reading resourceBranch's anchor walk
		// applies with outer[:i]. A request that carries fewer outer values
		// than the family has levels cannot name one, and says so rather
		// than reading the empty scope's rows.
		if len(h.outer) < depth {
			return nil, "", errors.New("bad_scope")
		}
		values = h.outer[:depth]
	}
	if len(values) != depth {
		return nil, "", errors.New("bad_scope")
	}
	// EncodeScope, never a strings.Join here: it is the ONE owner of that
	// join, and a second encoding at this call site is one a UNIQUE index
	// could disagree with (D3).
	return res, resources.EncodeScope(values), nil
}

// storeErr maps a store failure to the word the function reads. The set is
// the union of what the mock plane's own POST/DELETE and the admin entity
// route already answer by name. ErrResourceGone is `unknown_family`: the
// family was declined between the runtime build and this call, and from the
// function's side that is indistinguishable from never having been confirmed.
func storeErr(err error) error {
	switch {
	case errors.Is(err, resources.ErrResourceGone):
		return errors.New("unknown_family")
	case errors.Is(err, resources.ErrEntityLimit):
		return errors.New("entity_limit")
	case errors.Is(err, resources.ErrEntityKeyConflict):
		return errors.New("key_conflict")
	case errors.Is(err, resources.ErrEntityKeyNotCanonical):
		return errors.New("bad_key")
	case errors.Is(err, resources.ErrWriteBusy):
		return errors.New("write_busy")
	default:
		return errors.New("store_failed")
	}
}

// entityObject decodes a stored row's data as the object the function sees.
func entityObject(row resources.Entity) (map[string]any, error) {
	var obj map[string]any
	if err := jsonx.Unmarshal(row.Data, &obj); err != nil {
		return nil, errors.New("row_undecodable")
	}
	return obj, nil
}

func (h *luaHost) Entities(ctx context.Context, family string, scope []string) ([]map[string]any, error) {
	res, scopeKey, err := h.resolveFamily(family, scope)
	if err != nil {
		return nil, err
	}
	// List and not ListFiltered: a full ancestor tuple is an EXACT key, and
	// List takes exactly that pair. ListFiltered's wildcards, its cursor and
	// its limit are the three things this call does not want.
	rows, err := h.p.entities.List(ctx, res.ID, h.base, scopeKey)
	if err != nil {
		return nil, storeErr(err)
	}

	out := make([]map[string]any, 0, len(rows))
	for _, row := range rows {
		var obj map[string]any
		if err := jsonx.Unmarshal(row.Data, &obj); err != nil {
			// A row that will not decode is skipped, not fatal: the rest of
			// the family is still the honest answer, and a stored row is
			// only ever unparseable through a hand-run UPDATE.
			h.p.log.Warn("skip an entity row that does not decode as an object",
				"workspace", h.ws.Slug, "family", family, "entityKey", row.EntityKey)
			continue
		}
		out = append(out, obj)
	}
	return out, nil
}

// Generate is mock.generate's host half: the generator this runtime already
// holds, over schema as an inline document (PatchedSchema is how an inline
// custom-endpoint schema reaches the same walker, and how a `$ref` inside it
// resolves into the bound spec), seeded by the request tuple the host was
// built with. This is the tree's THIRD gen.Body call site and the seam test
// names it: it is not a fourth assembleResponse caller because a function
// asks for a BODY and not a response — no envelope, no recipes, no
// negotiation, no byte-cap refusal of its own (the function's whole return
// meets MOCKER_MAX_RESPONSE at the writer) — so assembleResponse would be the
// wrong seam, and going through it would hand the function a wrapped,
// recipe-applied document it did not ask for.
func (h *luaHost) Generate(_ context.Context, schema map[string]any) (any, error) {
	if h.rt.gen == nil {
		return nil, errors.New("no_generator")
	}
	// The generator takes PatchedSchema as the ROOT verbatim and resolves
	// only nested $refs, so a root `{"$ref": …}` — mock.generate's string
	// form — is chased here, the way buildCustomInline chases a stored
	// inline schema's root once at build. Unlike that path, which must keep
	// SERVING a row whose $ref stopped resolving and so empties the node
	// with a warning, a function is being asked a question and gets the
	// answer: an unresolvable $ref, root or nested, is a refusal naming the
	// pointer, never a silently empty object in the middle of the body.
	root, err := h.resolveSchema(schema)
	if err != nil {
		return nil, err
	}
	req := h.req
	req.PatchedSchema = root
	body, err := h.rt.gen.Body(gen.ResponseVariant{
		SchemaPtr: customSchemaPtr, HTTPStatus: http.StatusOK, MediaType: "application/json",
	}, req)
	if err != nil {
		// The generator's own words, capped by luafn.Note at the caller: an
		// unresolvable $ref names the pointer it could not follow.
		return nil, fmt.Errorf("generate_failed: %s", err.Error())
	}
	if len(body) == 0 {
		return nil, nil
	}
	var value any
	if err := jsonx.Unmarshal(body, &value); err != nil {
		return nil, errors.New("generate_failed: the generated body is not JSON")
	}
	return value, nil
}

// resolveSchema chases a root $ref and checks every nested one against the
// runtime's resolver; see Generate for why a miss is a refusal here.
func (h *luaHost) resolveSchema(schema map[string]any) (map[string]any, error) {
	root := schema
	if raw, ok := schema["$ref"]; ok {
		ptr, isString := raw.(string)
		if !isString {
			return nil, errors.New("bad_schema")
		}
		if h.rt.resolver == nil {
			return nil, errors.New("unresolved_ref: no spec is bound, " + ptr + " names nothing")
		}
		resolved, err := h.rt.resolver.Resolve(ptr)
		if err != nil {
			return nil, errors.New("unresolved_ref: " + ptr)
		}
		obj, isObj := resolved.(map[string]any)
		if !isObj {
			return nil, errors.New("bad_schema: " + ptr + " is not a schema object")
		}
		root = obj
	}
	if err := checkRefs(root, h.rt.resolver); err != nil {
		return nil, err
	}
	return root, nil
}

// checkRefs is sanitizeRefs's refusing sibling: the first nested $ref the
// resolver cannot follow is the error, and nothing is rewritten.
func checkRefs(node any, resolver *openapi.Resolver) error {
	switch n := node.(type) {
	case map[string]any:
		if raw, ok := n["$ref"]; ok {
			ptr, isString := raw.(string)
			if !isString {
				return errors.New("bad_schema: $ref is not a string")
			}
			if resolver == nil {
				return errors.New("unresolved_ref: no spec is bound, " + ptr + " names nothing")
			}
			if _, err := resolver.Resolve(ptr); err != nil {
				return errors.New("unresolved_ref: " + ptr)
			}
			// A resolvable $ref is left to the generator, which follows it
			// under its own budget; walking into it here would re-check the
			// spec's own document, which the resolver already validated.
			return nil
		}
		for _, child := range n {
			if err := checkRefs(child, resolver); err != nil {
				return err
			}
		}
	case []any:
		for _, child := range n {
			if err := checkRefs(child, resolver); err != nil {
				return err
			}
		}
	}
	return nil
}

// EntityCreate is the mock plane's anonymous POST through the same store:
// the family's own id field and strategy, the same caps, the same refusals
// by name. WriteForm is NOT consulted — that field says whether a POST on the
// collection route takes over, and a function calling create has said what
// it means.
func (h *luaHost) EntityCreate(ctx context.Context, family string, scope []string, data map[string]any) (map[string]any, error) {
	res, scopeKey, err := h.resolveFamily(family, scope)
	if err != nil {
		return nil, err
	}
	row, err := h.p.entities.Create(ctx, res.ID, h.base, scopeKey, res.IDField, res.Wrapper.IDType, data)
	if err != nil {
		return nil, storeErr(err)
	}
	return entityObject(row)
}

// EntityUpdate is Get → shallow merge → Set: patch's keys win, every other
// key of the stored row stays, and the id field is the row's own whatever
// patch says (Set keys the body by entityKey). `not_found` when there is no
// row — a function that means "create if absent" calls create on that.
func (h *luaHost) EntityUpdate(ctx context.Context, family string, scope []string, key string, patch map[string]any) (map[string]any, error) {
	res, scopeKey, err := h.resolveFamily(family, scope)
	if err != nil {
		return nil, err
	}
	current, found, err := h.p.entities.Get(ctx, res.ID, h.base, scopeKey, key)
	if err != nil {
		return nil, storeErr(err)
	}
	if !found {
		return nil, errors.New("not_found")
	}
	merged, err := entityObject(current)
	if err != nil {
		return nil, err
	}
	for k, v := range patch {
		merged[k] = v
	}
	row, _, err := h.p.entities.Set(ctx, res.ID, h.base, scopeKey, key, res.IDField, res.Wrapper.IDType, merged)
	if err != nil {
		return nil, storeErr(err)
	}
	return entityObject(row)
}

// EntityDelete is the mock plane's anonymous DELETE through the same store.
func (h *luaHost) EntityDelete(ctx context.Context, family string, scope []string, key string) (bool, error) {
	res, scopeKey, err := h.resolveFamily(family, scope)
	if err != nil {
		return false, err
	}
	deleted, err := h.p.entities.Delete(ctx, res.ID, h.base, scopeKey, key)
	if err != nil {
		return false, storeErr(err)
	}
	return deleted, nil
}

// previewFunction is D7's preview half: the same runner, the same safety
// tail, and a failure that lands in [domain.PreviewResult.Notes] rather than
// in a status.
//
// "Rather than in a status" is the whole rule, and it is the asset_missing
// precedent one file over: a preview whose draft function failed answers the
// status the variant resolved to, with no body and the note saying what
// happened — never a 500 dressed up as the mock's own answer, which would
// teach the author that their endpoint returns 500 when what actually failed
// was the preview's attempt to show it.
//
// The context is the ADMIN request's, so an operator who navigates away stops
// the VM exactly as a mock client disconnecting does; luafn's own ErrCanceled
// is folded into the failure note here rather than answered by silence,
// because unlike a mock request there IS somebody to answer — the preview
// route, which must return a document either way.
func (p *Plane) previewFunction(
	ctx context.Context, req domain.PreviewRequest, rv resolved,
	delayMs int, shadowedBy, source string, route *router.Route,
) domain.PreviewResult {
	result := domain.PreviewResult{
		Status:       rv.Status,
		StatusSource: rv.StatusSource,
		DelayMs:      delayMs,
		ShadowedBy:   shadowedBy,
	}

	query, err := url.ParseQuery(strings.TrimPrefix(req.Query, "?"))
	if err != nil {
		// The same tolerance the serving path has by construction: a query
		// net/url cannot parse becomes no query rather than a refusal, since
		// a preview is a drafting aid and the author is looking at the body.
		query = url.Values{}
	}
	headers := make(map[string][]string, len(req.Headers))
	for name, value := range req.Headers {
		headers[name] = []string{value}
	}

	resp, runErr := luafn.Run(ctx, source, luafn.Request{
		Method:     route.Method,
		Path:       route.CanonicalPath,
		PathParams: req.PathParams,
		Query:      query,
		Headers:    headers,
		Body:       req.Body,
	}, nil)
	if runErr != nil {
		result.NoBody = true
		if errors.Is(runErr, luafn.ErrTimeout) {
			result.Notes = noteFunctionTimeout
		} else {
			result.Notes = noteFunctionFailed
		}
		return result
	}

	headersOut, mediaType, err := functionHeaders(resp)
	if err != nil {
		result.NoBody = true
		result.Notes = noteFunctionFailed
		return result
	}
	if int64(len(resp.Body)) > p.cfg.MaxResponse {
		result.NoBody = true
		result.Notes = noteFunctionTooLarge
		return result
	}

	result.Status = resp.Status
	result.StatusSource = domain.StatusSourceActive
	result.MediaType = mediaType
	result.Headers = headersOut
	result.Notes = noteFunction
	if len(resp.Body) == 0 {
		result.NoBody = true
		return result
	}
	result.Body = resp.Body
	// D5's presence rule, the same two words preview.go's own tail uses:
	// Encoding is present exactly when Body is.
	if utf8.Valid(resp.Body) {
		result.Encoding = "utf8"
	} else {
		result.Encoding = "base64"
	}
	return result
}
