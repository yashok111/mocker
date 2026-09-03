// Package mockplane serves HTTP requests on workspace hosts: it resolves the
// workspace addressed by the request (from the Host header, or — under
// MOCKER_ROUTING=path — from a slug the dispatcher already pulled out of the
// URL), applies that workspace's CORS policy, and answers the reserved
// control endpoints under MOCKER_RESERVED_PREFIX. From P1 on it also serves
// the generated mock routes; P0 only gets as far as the reserved prefix.
//
// The order all of this happens in is deliberate, and is itself the point of
// P0 (DESIGN §6): resolve the workspace first, set its CORS headers before
// any branch that might 404 or panic, then short-circuit a preflight, then
// the reserved prefix, then everything else. Getting this order wrong is
// invisible in a happy-path demo: a 404 or a panic-500 with no CORS headers
// reads to browser JavaScript as an opaque CORS failure, hiding the real
// status code that would have explained what actually went wrong.
package mockplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/domain"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/workspaces"
)

// Source looks up a workspace by slug. [*workspaces.Repo] satisfies it;
// tests substitute a fake so the plane needs no database.
type Source interface {
	BySlug(ctx context.Context, slug string) (*workspaces.Workspace, error)
}

// Plane serves the mock plane for whichever workspace a request resolves to.
type Plane struct {
	cfg   *config.Config
	src   Source
	specs SpecSource
	log   *slog.Logger

	// overrides is nil until [Plane.SetOverrides] is called — see that
	// method's doc comment for the calling contract (startup only) and for
	// what "never called" means for every request this Plane serves.
	overrides OverrideSource

	// livestate is nil until [Plane.SetLiveState] is called — see that
	// method's doc comment (livestate.go) for the identical startup-only
	// calling contract SetOverrides already documents.
	livestate LiveStateSource

	// streams and streamOpts are P6b's (stream.go): the mock plane's own
	// per-workspace-capped connection registry and the loop's timings,
	// nil/zero until [Plane.SetStreams] — every stream handshake answers
	// 503 until then.
	streams    *stream.Registry
	streamOpts StreamOptions

	// traffic is nil until [Plane.SetTraffic] is called — see that method's
	// doc comment (traffic.go) for the identical startup-only calling
	// contract SetOverrides already documents.
	traffic TrafficSink

	// custom is nil until [Plane.SetCustomEndpoints] is called — see that
	// method's doc comment (custom.go) for the identical startup-only
	// calling contract SetOverrides already documents. A nil custom means
	// exactly what a nil OverrideSource means: buildRuntime (runtime.go)
	// loads no custom rows at all, and routes.go's serveRoute keeps its
	// pre-P1c2 short-circuit for a spec-less workspace.
	custom CustomSource

	// scenarios is nil until [Plane.SetScenarios] is called — see that
	// method's doc comment below for the identical startup-only calling
	// contract SetOverrides establishes, and [ScenarioSource]
	// (scenario.go) for why this one source carries a WRITE where every
	// other source on this struct is read-only. A nil scenarios means
	// buildRuntime composes no scenario layer at all (a workspace whose
	// scenario_id is set still serves its own layer) and the
	// {prefix}/state scenario switch answers 503, exactly as an unwired
	// LiveStateSource already makes the rest of that endpoint answer.
	scenarios ScenarioSource

	// resources is nil until [Plane.SetResources] is called — see that
	// method's doc comment for the identical startup-only calling contract
	// SetOverrides already documents. A nil resources means buildRuntime
	// (runtime.go) composes no resource layer at all, so [resourceBranch]
	// (resource.go) always sees an empty rt.resources and every request
	// falls through to the generator exactly as it did before P3a.
	resources ResourceSource

	// entities is nil until [Plane.SetEntities] is called — see that
	// method's doc comment for the same contract. Unlike resources, this
	// source is read PER REQUEST, never cached in a runtime (D6 R17): a nil
	// entities makes [resourceBranch] fall through just as a nil resources
	// does, on the same "no source wired, no change" reasoning.
	entities EntityStore
	// assets is the per-request AssetStore (A6, asset.go); read per request
	// like entities, never cached in a runtime.
	assets AssetStore

	// recover is built once in New rather than per-request: httpx.Recover(log)
	// only needs the logger, and the request-scoped part (which slug to serve)
	// is threaded through as a closure argument at call time instead.
	recover httpx.Middleware

	// routes is the runtime's cache of compiled route tables (routes.go). It
	// is built even when specs is nil — it just never gets used in that case,
	// which is simpler than threading a second nil-check through routeTable.
	routes *routeCache
}

// New builds a Plane. cfg and src are read on every request, never copied, so
// a config reload or a workspace edit is visible to the next request without
// restarting the plane. specs may be nil, meaning "no spec support": every
// request then gets the plain 404 Step 5 has always given.
func New(cfg *config.Config, src Source, specs SpecSource, log *slog.Logger) *Plane {
	return &Plane{
		cfg:     cfg,
		src:     src,
		specs:   specs,
		log:     log,
		recover: httpx.Recover(log),
		routes:  newRouteCache(cfg.RuntimeCache),
	}
}

// SetOverrides wires src as the Plane's [OverrideSource]. Call it once,
// during startup, right after [New] and BEFORE the Plane serves its first
// request — cmd/mocker's own wiring is the only production caller (a later
// stage of this same phase). This is a setter rather than a New parameter
// purely so New's six existing call sites — five files outside this task,
// one of them inside internal/gen — keep compiling exactly as they are; it
// is not an invitation to call this from a request handler or a background
// goroutine racing real traffic. Plane has no lock around this field for the
// same reason src and specs have none: all three are meant to be written
// once, before the first request, and only ever read afterward — calling
// this a second time, or calling it concurrently with ServeHTTP/ServeSlug,
// IS a data race on p.overrides, the same as reassigning p.specs after
// startup would be, and go test -race would (correctly) catch it.
//
// A Plane whose SetOverrides is never called behaves exactly as it did
// before this phase: buildRuntime (runtime.go) sees a nil OverrideSource and
// every matched request answers precisely what P1b would have (HARD RULE
// 6) — which is also why every other New call site, and every P0/P1a/P1b
// test that never calls this method, keeps passing unmodified.
//
// One consequence worth stating rather than leaving for someone to
// "discover" later: a runtime already sitting in routes.go's LRU keeps
// serving with whatever overrides (or lack of them) it was built with until
// its own workspace's revision changes — the cache key is
// (workspace, revision), and every write through overrides.Repo
// (Put/PutMany/Delete) bumps that revision, so a stale-looking cache entry
// right after an edit is an ordinary cache-miss-on-next-request, not a bug
// to "fix" by clearing the cache from inside this setter.
func (p *Plane) SetOverrides(src OverrideSource) {
	p.overrides = src
}

// SetScenarios wires src as the Plane's [ScenarioSource] (scenario.go).
// Call it once, during startup, right after [New] and BEFORE the Plane
// serves its first request — cmd/mocker's own wiring is the only
// production caller. The calling contract is [Plane.SetOverrides]' word
// for word, including why there is no lock: p.scenarios is written once
// and only ever read afterward, and calling this concurrently with
// ServeHTTP/ServeSlug IS a data race go test -race correctly catches.
//
// It sits here, beside SetOverrides, rather than in scenario.go with the
// interface it takes, because this is where a reader already looks for
// "which sources does a Plane have" — the same reason cmd/mocker wires
// every setter in one block instead of near the code that consumes it.
//
// A Plane whose SetScenarios is never called behaves exactly as one built
// before this feature existed: buildRuntime (runtime.go) composes no
// scenario layer even for a workspace whose scenario_id column is set, so
// every request answers from the workspace layer alone, and the
// {prefix}/state scenario switch answers 503 "service_unavailable" rather
// than a nil-pointer panic. Every test in this package that never calls
// this method keeps passing unmodified.
//
// The consequence worth stating out loud, because this source is the first
// on this struct that WRITES: an activation changes workspaces.scenario_id
// and bumps revision, and the runtime cache keys on (workspace, revision)
// — so the switch becomes visible as an ordinary cache miss on the next
// request. Nothing here clears a cache, and nothing should start to.
func (p *Plane) SetScenarios(src ScenarioSource) {
	p.scenarios = src
}

// SetResources wires src as the Plane's [ResourceSource] (resource.go). Call
// it once, during startup, right after [New] and BEFORE the Plane serves its
// first request — cmd/mocker's own wiring is the only production caller,
// next to [Plane.SetEntities] and [Plane.SetCustomEndpoints]. The calling
// contract is [Plane.SetOverrides]' word for word, including why there is
// no lock: p.resources is written once and only ever read afterward.
//
// A Plane whose SetResources is never called behaves exactly as one built
// before P3a existed: buildRuntime (runtime.go) loads no resource rows at
// all, rt.resources stays nil, and [Plane.resourceBranch] falls through on
// its very first check for every request — the identical "no source wired,
// no change" contract every other P1c/P1c2/P3a source already follows.
func (p *Plane) SetResources(src ResourceSource) {
	p.resources = src
}

// SetEntities wires store as the Plane's [EntityStore] (resource.go). Call
// it once, during startup, right next to [Plane.SetResources] — cmd/mocker
// wires both together, and D13 clause 3's own smoke walk is what actually
// PROVES both reached main.go, not a second, narrower test that could pass
// with one of them silently missing.
//
// Unlike every other setter on this struct, what this one gates is never
// cached in a runtime: EntityStore is read once PER REQUEST (D6 R17), so a
// Plane whose SetEntities is never called answers exactly like one with no
// ResourceSource either — resourceBranch checks both before doing anything
// else.
func (p *Plane) SetEntities(store EntityStore) {
	p.entities = store
}

// ServeHTTP resolves the workspace from the request's Host header and hands
// off to ServeSlug — the only place that knows what happens once a workspace
// is (or is not) known.
func (p *Plane) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	slug, ok := p.cfg.IsWorkspaceHost(r.Host)
	if !ok {
		p.recovered(func(w http.ResponseWriter, r *http.Request) {
			p.unresolved(w, r, fmt.Sprintf(
				"host %q does not address a workspace; expected <slug>.%s", r.Host, p.cfg.BaseDomain))
		}).ServeHTTP(w, r)
		return
	}
	p.ServeSlug(w, r, slug)
}

// ServeSlug serves a request for a workspace whose slug is already known.
// This is the entry point MOCKER_ROUTING=path needs: in that mode there is no
// workspace host to parse (wildcard DNS is exactly what the mode is for
// avoiding), so the dispatcher pulls the slug out of the path itself
// (/w/{slug}/...) and calls this directly instead of going through
// ServeHTTP.
func (p *Plane) ServeSlug(w http.ResponseWriter, r *http.Request, slug string) {
	p.recovered(func(w http.ResponseWriter, r *http.Request) {
		p.serveResolved(w, r, slug)
	}).ServeHTTP(w, r)
}

// recovered wraps fn with the shared httpx.Recover middleware so a panic
// anywhere below — health, the reserved prefix, the route table from P1 —
// turns into a 500 that still carries whatever CORS headers had already been
// set on w, instead of a dropped connection.
func (p *Plane) recovered(fn func(http.ResponseWriter, *http.Request)) http.Handler {
	return p.recover(http.HandlerFunc(fn))
}

// serveResolved is the single implementation of "what happens once we know
// which workspace a request is for" (DESIGN §6): both ServeHTTP and
// ServeSlug end up here, and it is the only place that runs the five-step
// order the phase exists to get right.
func (p *Plane) serveResolved(w http.ResponseWriter, r *http.Request, slug string) {
	// Step 1: resolve the workspace before anything else — CORS, preflight
	// and the reserved prefix all need to know which workspace they're for.
	ws, err := p.src.BySlug(r.Context(), slug)
	if err != nil {
		if errors.Is(err, workspaces.ErrNotFound) {
			p.unresolved(w, r, fmt.Sprintf("workspace %q does not exist", slug))
			return
		}
		p.log.Error("resolve workspace", "slug", slug, "err", err)
		httpx.Err(w, http.StatusInternalServerError, httpx.CodeInternal, "internal error")
		return
	}

	// Step 2: CORS headers go on the response now, before any branch that
	// could 404 or panic, so those responses still carry them.
	setCORS(w, r, ws.Settings.CORS)

	// HEAD matches GET with an empty body (DESIGN §8) — enforced once, here,
	// so every handler below (health today, the route table from P1) gets it
	// for free instead of remembering it on its own.
	if r.Method == http.MethodHead {
		w = &headWriter{ResponseWriter: w}
	}

	// Step 3: preflight short-circuits everything below it.
	if isPreflight(r) {
		writePreflight(w, r, ws.Settings.CORS)
		return
	}

	// Segments, not a joined string: cutReservedPrefix and (from P1) the
	// route table must compare segment-by-segment, never against a rejoined
	// "/"-delimited string — see [NormalizeSegments].
	segments := NormalizeSegments(r.URL.EscapedPath())

	// Step 4: the reserved control prefix.
	if rest, ok := cutReservedPrefix(segments, p.cfg.ReservedPrefix); ok {
		p.serveReserved(w, r, ws, rest)
		return
	}

	// Step 5: capture the body ONCE (reqbody.go), then hand off to
	// captureTraffic (traffic.go), which times and (when a TrafficSink is
	// wired) tees the whole call before matching against the workspace's
	// compiled route table (routes.go). Capture runs here, not inside
	// serveRoute/serveGenerated: every consumer this phase adds —
	// respond.go's when[] evaluation, the traffic recorder — needs it
	// regardless of whether the request even matches a route (traffic logs a
	// 404 exactly like a 200), and the reserved prefix above deliberately
	// never reaches this line at all — {prefix}/state reads its OWN body
	// directly (livestate.go) and is never recorded in traffic either.
	cb, err := captureRequestBody(w, r, requestBodyCap(p.cfg.TrafficMaxBody))
	if err != nil {
		// httpx.MaxBody's http.MaxBytesReader tripped MOCKER_MAX_BODY on
		// OUR read — vanishingly rare, since requestBodyCap's own cap sits
		// far under MaxBody's default, but a live config can bring them
		// arbitrarily close. Before this phase, nothing on the mock plane
		// ever read the body at all, so a request over the limit simply
		// succeeded; capturing must not change that — log it and serve on
		// with "no body" rather than let a fresh read failure turn into a
		// response this request would not have gotten a moment ago.
		p.log.Warn("capture request body", "workspace", ws.Slug, "err", err)
		cb = &capturedBody{}
	}
	p.captureTraffic(w, attachCapturedBody(r, cb), ws, segments)
}

// unresolved answers a request whose workspace could not be pinned down at
// all — either the host never had workspace shape, or it did but no
// workspace with that slug exists. Both are the same "нет такого" branch in
// DESIGN §6's flowchart, and both still answer a CORS preflight, using the
// default policy since there is no real workspace to read one from: a
// browser probing a workspace before it exists must not see a bare CORS
// failure that hides the 404 underneath.
func (p *Plane) unresolved(w http.ResponseWriter, r *http.Request, msg string) {
	if isPreflight(r) {
		cors := domain.DefaultSettings().CORS
		setCORS(w, r, cors)
		writePreflight(w, r, cors)
		return
	}
	httpx.Err(w, http.StatusNotFound, httpx.CodeNotFound, msg)
}

// serveReserved answers the MOCKER_RESERVED_PREFIX control endpoints.
// rest is what followed the prefix, as segments: nil for the bare prefix,
// otherwise the remaining path segments in order.
func (p *Plane) serveReserved(w http.ResponseWriter, r *http.Request, ws *workspaces.Workspace, rest []string) {
	if len(rest) == 1 && rest[0] == "health" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		p.serveHealth(w, ws)
		return
	}
	// GET/POST/DELETE /state (livestate.go): the one HTTP surface DESIGN
	// §12/§14 gives for "переключение из тестов". Filtered to exactly these
	// three methods here, same as health above is filtered to GET/HEAD —
	// anything else under "state" (or under the prefix at all) falls to the
	// generic 404 below, unchanged.
	if len(rest) == 1 && rest[0] == "state" &&
		(r.Method == http.MethodGet || r.Method == http.MethodPost || r.Method == http.MethodDelete) {
		p.serveLiveState(w, r, ws)
		return
	}
	// A6 (DESIGN §32.3, decisions.md mocker-a6-assets D4): GET|HEAD
	// {prefix}/assets/{name}, the third control route — asset.go. Exactly
	// two segments: a name is one segment by construction (assets.ValidName
	// admits no slash), so anything deeper is the generic 404 below.
	if len(rest) == 2 && rest[0] == "assets" && (r.Method == http.MethodGet || r.Method == http.MethodHead) {
		p.serveAsset(w, r, ws, rest[1])
		return
	}
	// Anything else under the prefix lands in a later phase. Rejoining rest
	// for the message is display-only, same caveat as joinForDisplay.
	full := p.cfg.ReservedPrefix
	if len(rest) > 0 {
		full += "/" + strings.Join(rest, "/")
	}
	httpx.Err(w, http.StatusNotFound, "not_implemented_yet", fmt.Sprintf(
		"%s %s is not implemented yet — it arrives in a later phase", r.Method, full))
}

// health is the exact response shape DESIGN §14 specifies for
// GET/HEAD {prefix}/health.
type health struct {
	OK        bool   `json:"ok"`
	Workspace string `json:"workspace"`
	Revision  int64  `json:"revision"`
	Spec      *int64 `json:"spec"`
}

func (p *Plane) serveHealth(w http.ResponseWriter, ws *workspaces.Workspace) {
	httpx.JSON(w, http.StatusOK, health{
		OK:        true,
		Workspace: ws.Slug,
		Revision:  ws.Revision,
		Spec:      ws.SpecID,
	})
}

// headWriter drops the body of every Write while passing headers and the
// status code straight through, giving "HEAD matches GET with an empty body"
// (DESIGN §8) without every handler needing to special-case the method.
type headWriter struct {
	http.ResponseWriter
}

func (h *headWriter) Write(b []byte) (int, error) { return len(b), nil }

// Unwrap lets http.ResponseController reach the real writer underneath, the
// same contract [httpx.StatusRecorder] follows.
func (h *headWriter) Unwrap() http.ResponseWriter { return h.ResponseWriter }

// cutReservedPrefix reports whether segments falls under prefix — the
// MOCKER_RESERVED_PREFIX, already validated by config to start with "/" and
// never end with one — and if so returns what follows as its own segment
// slice: nil for the bare prefix, otherwise the remaining segments.
//
// Matching is segment-by-segment against segments, never against a rejoined
// string: joining decoded segments back into one "/"-delimited string before
// comparing would make the single encoded segment "__mocker%2Fhealth" — one
// segment whose decoded content happens to contain a slash — byte-identical
// to the two real segments "__mocker", "health", letting a request that was
// never under the reserved prefix reach it anyway (round-1 review finding 1).
// Comparing element-by-element keeps that distinction no matter what either
// side decodes to.
func cutReservedPrefix(segments []string, prefix string) (rest []string, ok bool) {
	prefixSegs := NormalizeSegments(prefix)
	if len(segments) < len(prefixSegs) {
		return nil, false
	}
	if !slices.Equal(segments[:len(prefixSegs)], prefixSegs) {
		return nil, false
	}
	return segments[len(prefixSegs):], true
}

// NormalizeSegments prepares a raw request path for route matching (DESIGN
// §8): strip the query string, collapse repeated "/", drop empty
// leading/trailing segments, and percent-decode each segment on its own —
// returned as a slice, deliberately never rejoined into one string.
//
// The splitting decision is made BEFORE decoding, on the still-encoded
// string: an encoded "/" (%2F) is three literal characters at that point, not
// a separator, so it can never manufacture a segment boundary — or get
// silently swallowed by the "//" collapse — that was never in the request.
// Only after segments are fixed does each one get percent-decoded on its own.
//
// Callers that match against the result (e.g. [cutReservedPrefix], and from
// P1 the route table) MUST compare it segment-by-segment. Rejoining it into a
// single string first — even just for a nicer log line — reintroduces
// exactly the ambiguity splitting-before-decoding was meant to prevent: a
// decoded "/" that lands inside one segment becomes indistinguishable from a
// real boundary between two segments. [NormalizePath] exists as the
// deliberately-lossy, display-only escape hatch for callers that only want
// text to put in an error message or a log line.
//
// Callers pass r.URL.EscapedPath(), not r.URL.Path: net/url's Path field is
// already decoded by the time a handler sees it, which would have destroyed
// exactly the distinction this function exists to preserve.
func NormalizeSegments(p string) []string {
	if i := strings.IndexByte(p, '?'); i >= 0 {
		p = p[:i]
	}

	raw := strings.Split(p, "/")
	segments := make([]string, 0, len(raw))
	for _, seg := range raw {
		if seg == "" {
			continue // collapses "//" and drops a leading/trailing "/"
		}
		if decoded, err := url.PathUnescape(seg); err == nil {
			seg = decoded
		}
		segments = append(segments, seg)
	}
	return segments
}

// NormalizePath renders [NormalizeSegments] back into one "/"-prefixed
// string, for DISPLAY ONLY — log lines and error-message bodies. It must
// never be used for route matching: see the warning on NormalizeSegments for
// exactly why rejoining is unsafe there.
func NormalizePath(p string) string {
	return joinForDisplay(NormalizeSegments(p))
}

// joinForDisplay is [NormalizePath]'s formatting half, factored out so a
// caller that already computed segments (like serveResolved, for its 404
// message) does not have to decode the same path a second time just to get
// the display string.
func joinForDisplay(segments []string) string {
	if len(segments) == 0 {
		return "/"
	}
	return "/" + strings.Join(segments, "/")
}
