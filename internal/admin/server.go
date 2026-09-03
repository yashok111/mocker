// Package admin implements mocker's admin-plane HTTP API (DESIGN §14): login
// and session lookup, and workspace CRUD, guarded by the CSRF, same-origin
// and rate-limiting rules DESIGN §15 requires. The mock plane (unauthenticated
// by design) lives entirely outside this package; nothing here ever serves a
// workspace subdomain.
package admin

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"slices"
	"strconv"
	"sync"
	"time"

	"github.com/yashok111/mocker/internal/jsonx"

	"github.com/yashok111/mocker/internal/assets"
	"github.com/yashok111/mocker/internal/auth"
	"github.com/yashok111/mocker/internal/checkpoints"
	"github.com/yashok111/mocker/internal/config"
	"github.com/yashok111/mocker/internal/customep"
	"github.com/yashok111/mocker/internal/httpx"
	"github.com/yashok111/mocker/internal/overrides"
	"github.com/yashok111/mocker/internal/resources"
	"github.com/yashok111/mocker/internal/scenarios"
	"github.com/yashok111/mocker/internal/specs"
	"github.com/yashok111/mocker/internal/store"
	"github.com/yashok111/mocker/internal/stream"
	"github.com/yashok111/mocker/internal/traffic"
	"github.com/yashok111/mocker/internal/webui"
	"github.com/yashok111/mocker/internal/workspaces"
)

// loginPath is the one route both the CSRF middleware and the login rate
// limiter special-case: exempt from the CSRF token check (there is no session
// yet) but subject to the attempt cap neither of them apply anywhere else.
const loginPath = "/api/auth/login"

// mcpPath is where the MCP (Model Context Protocol) endpoint is mounted —
// OUTSIDE [Server.routes] entirely. See that method's own doc comment and
// [Server.Handler] below for why, and §A3/§A4 of the MCP slice's context
// document for the full argument.
const mcpPath = "/mcp"

// handlerBypass lists every path [Server.Handler] serves OUTSIDE the route
// table in [Server.routes] — i.e. everything a generated client will never
// see, because it is neither in api/openapi.json nor reachable through the
// mux openapi_contract_test.go checks against. It is a package-level table
// rather than a string literal buried in Handler()'s closure so
// mcp_mount_test.go can pin its CONTENT — length and membership — as a unit:
// nothing in the code enumerates "paths served outside the mux" on its own,
// so a behavioral test could only ever probe paths it already knew about and
// would stay green the day a second bypass (say, /metrics) is added without
// updating this list. Keeping the branch's decision data HERE, and reading
// it from the table rather than hardcoding mcpPath again in Handler(), is
// what makes a second bypass a red test instead of a silent discovery.
var handlerBypass = []string{mcpPath}

// readyzTimeout bounds how long GET /readyz waits on the database before
// answering 503 — a slow database should make readiness probes fail fast,
// not hang the healthcheck that is supposed to detect exactly that.
const readyzTimeout = 2 * time.Second

// Server holds the admin plane's dependencies and builds its HTTP handler.
type Server struct {
	cfg      *config.Config
	sessions *auth.Manager
	ws       *workspaces.Repo
	db       *store.DB
	log      *slog.Logger

	// specsRepo is constructed internally from db and cfg rather than taken
	// as a New parameter: New's signature is shared with cmd/mocker/main.go,
	// which this package does not own, so adding a parameter here would break
	// a build outside this package's remit.
	specsRepo *specs.Repo

	// overridesRepo is constructed internally for the same reason specsRepo
	// is: New's signature is shared with cmd/mocker/main.go (which wires its
	// OWN overrides.Repo into the mock plane via mockplane.Plane.SetOverrides
	// — a separate instance over the same table, not this one; the two never
	// share state and don't need to).
	overridesRepo *overrides.Repo

	// trafficRepo is built internally for the same reason specsRepo and
	// overridesRepo are (see overridesRepo's own comment): it is a stateless
	// reader over the traffic table, not shared state with the mock plane,
	// so there is nothing for it to receive through a setter.
	trafficRepo *traffic.Repo

	// customepRepo is built internally for the same reason specsRepo,
	// overridesRepo and trafficRepo are: it is a stateless data-access layer
	// over custom_endpoints, not shared RAM state with the mock plane (the
	// mock plane's own route table reads the SAME table through its own
	// customep.Repo instance, built independently — the two never need to
	// share state, only agree on workspaces.revision as the cache-invalidation
	// signal, which [customep.Repo.Create]/Delete already bump correctly).
	customepRepo *customep.Repo

	// scenariosRepo is constructed internally for the same reason
	// specsRepo/overridesRepo/trafficRepo/customepRepo are (see
	// overridesRepo's own comment): a scenario snapshot lives in SQLite,
	// not shared RAM, so there is nothing here for a setter to receive
	// after New returns — unlike liveState/traffic below, which the mock
	// plane also holds a reference to. It is built over THIS Server's own
	// overridesRepo instance (constructed once, in New, and handed to
	// both) rather than a second overrides.Repo allocated just for
	// [scenarios.Repo.CreateFromCurrentState]'s own ForWorkspace read:
	// overrides.Repo carries no RAM state of its own to disagree about, so
	// one instance per data-access layer per plane is the existing pattern
	// here (customepRepo and trafficRepo are each their own instance too),
	// not a corner cut invented for this slice.
	scenariosRepo *scenarios.Repo

	// checkpointsRepo is constructed internally for the same reason
	// scenariosRepo is (see its own field comment): a checkpoint's snapshot
	// lives in SQLite, not shared RAM, so there is nothing here for a
	// setter to receive after New returns. It is built over THIS Server's
	// own overridesRepo AND customepRepo instances (both constructed once,
	// in New, and shared here) rather than allocating a second instance of
	// either just for [checkpoints.Repo]'s own ForWorkspace reads — the
	// same "one instance per data-access layer per plane" rule
	// scenariosRepo's comment already states, which is why customepRepo
	// below is hoisted to a local exactly like overridesRepo already was,
	// instead of staying inline in the struct literal.
	checkpointsRepo *checkpoints.Repo

	// resourcesRepo is P3a's data-access layer for resources/entities/
	// resource_decisions (internal/resources) — constructed internally for
	// the same reason specsRepo/overridesRepo/etc. are (see specsRepo's own
	// comment): it is stateless SQLite access, not RAM shared with the mock
	// plane (which holds its OWN resources.Repo behind the
	// mockplane.ResourceSource interface, wired through SetResources in
	// cmd/mocker/main.go — a separate instance over the same tables, the
	// same relationship overridesRepo already has with the mock plane's own
	// overrides.Repo). Built over this Server's own specsRepo, so
	// [resources.Repo.Confirm]/[Decline]'s suggestion lookups reuse the
	// EnsureSuggestions backfill this package already exposes through GET
	// /api/specs/{id}/resource-suggestions, rather than a second
	// specs.Repo pointed at the same database.
	resourcesRepo *resources.Repo
	// assetsRepo is A6's uploaded-file store (DESIGN §32); its two caps
	// come from cfg so this instance and cmd/mocker's own (which the mock
	// plane reads through) enforce the same numbers.
	assetsRepo *assets.Repo

	loginLimiter *rateLimiter

	// liveState and traffic ARE shared state with the mock plane — the SAME
	// *livestate.Store and *traffic.Recorder instances it reads and writes
	// through, not independent copies — so, unlike specsRepo/overridesRepo/
	// trafficRepo above, they cannot be constructed here: they arrive via
	// [Server.SetLiveState] and [Server.SetTraffic] after New returns. See
	// either setter's own doc comment for why a setter and not a New
	// parameter: New's signature (cfg, sessions, ws, db, log) is shared with
	// cmd/mocker/main.go's five call sites, and adding a second
	// session-shaped dependency next to the existing *auth.Manager
	// "sessions" parameter is exactly the adjacent-same-name swap this
	// project's gate has caught before. Both are nil until wired, and every
	// route that needs one checks for that rather than assuming a caller
	// always wires it (livestate_handlers.go, traffic_handlers.go).
	liveState LiveStateSource
	traffic   TrafficControl

	// streamReg and streamOpts are P6a's (stream_handlers.go): the
	// process's one connection registry and D12's three timings, wired by
	// SetStream from cmd/mocker/main.go for the same reason liveState and
	// traffic arrive through setters — the registry is ALSO handed to the
	// traffic recorder as its Notifier, so it must be one instance built
	// outside this package.
	streamReg   *stream.Registry
	mockStreams *stream.Registry
	streamOpts  StreamOptions

	// previewer is P2f's own dependency, wired the identical way liveState
	// and traffic are (see either field's own comment): a setter, not a
	// New parameter, because New's signature is shared across
	// cmd/mocker/main.go's five call sites. Unlike liveState/traffic,
	// which are RAM state literally shared with the mock plane, previewer
	// is the plane's [Previewer] interface — this package never names
	// *mockplane.Plane itself (A9) — but the wiring shape (nil until
	// SetPreviewer runs, every route checking for that rather than
	// assuming a caller always wires it) is identical, and preview_handlers.go
	// is where the check and the 503 it answers with both live.
	previewer Previewer

	// routeMuxOnce/routeMuxVal back [Server.routeMux]: the ONE dispatch mux
	// [Server.Handler] and [Server.CallAsMCP] share, built at most once no
	// matter which of the two asks for it first, or whether Handler() is
	// ever called at all (CallAsMCP must work against a Server it never
	// was — an in-process caller has no listener to force that ordering).
	routeMuxOnce sync.Once
	routeMuxVal  *http.ServeMux

	// mcpUserMu/mcpUser cache the MCP identity [Server.CallAsMCP] resolves
	// through auth.Manager.EnsureUser. See [Server.mcpIdentity]'s own
	// comment (loopback.go) for why only a SUCCESSFUL resolution is cached.
	mcpUserMu sync.Mutex
	mcpUser   *auth.User

	// mcp is the /mcp handler [Server.SetMCP] installs; nil until then, and
	// nil forever in a deployment that never sets MOCKER_MCP_KEY. Handler()
	// reads this field PER REQUEST, inside its returned closure — see
	// Handler()'s own comment for why a build-time nil check here would be
	// wrong.
	mcp http.Handler
}

// New builds a Server. cfg, sessions, ws and db are held, not copied: a later
// config reload or session purge is visible to every request through the
// same pointers.
func New(cfg *config.Config, sessions *auth.Manager, ws *workspaces.Repo, db *store.DB, log *slog.Logger) *Server {
	// Built as locals rather than inline in the struct literal below so
	// scenariosRepo, checkpointsRepo and resourcesRepo can be handed the
	// SAME instances overridesRepo/customepRepo/specsRepo are, rather than
	// each allocating its own independent (but functionally identical)
	// repo — see scenariosRepo's own field comment, and checkpointsRepo's
	// for why customepRepo joins overridesRepo as a local here (it used to
	// sit inline in this struct literal; P2c is checkpointsRepo's own
	// second reader of it). specsRepo joins them for P3a: resourcesRepo's
	// own SpecSource reads through THIS instance, not a second
	// specs.Repo(db, cfg) built just for it, which would be indistinguishable
	// state pointed at the same tables for no reason.
	overridesRepo := overrides.NewRepo(db)
	customepRepo := customep.NewRepo(db)
	specsRepo := specs.NewRepo(db, cfg)
	return &Server{
		cfg:           cfg,
		sessions:      sessions,
		ws:            ws,
		db:            db,
		log:           log,
		specsRepo:     specsRepo,
		overridesRepo: overridesRepo,
		trafficRepo:   traffic.NewRepo(db),
		customepRepo:  customepRepo,
		scenariosRepo: scenarios.NewRepo(db, overridesRepo),
		// P2c (§B): cfg.CheckpointRetention is a REQUIRED constructor
		// parameter, not normalised away here or inside checkpoints.NewRepo
		// — MOCKER_CHECKPOINT_RETENTION=0 means "prune nothing" (C7), a
		// deliberate departure from traffic.NewRecorder's own <=0 default
		// substitution that a copy-paste of that pattern would get wrong.
		checkpointsRepo: checkpoints.NewRepo(db, overridesRepo, customepRepo, cfg.CheckpointRetention),
		// cfg.MaxResponse/cfg.TrafficMaxBody/cfg.MaxEntities are the three
		// numbers [resources.Repo]'s own doc comment names as the entity
		// caps' sources (R25, D11) — this instance is what a confirm made
		// through the ADMIN API enforces its row cap against, so wiring
		// cfg.MaxEntities here too (not just cmd/mocker/main.go's own
		// *resources.Repo) is what keeps a workspace's ceiling from
		// depending on which door the request came through (D11).
		resourcesRepo: resources.NewRepo(db, specsRepo, cfg.MaxResponse, cfg.TrafficMaxBody, int64(cfg.MaxEntities)),
		assetsRepo:    assets.NewRepo(db, cfg.MaxAsset, cfg.MaxAssetsTotal),
		// 10 attempts/minute (DESIGN §15) — not configurable in P0, so it is
		// not threaded through config.Config.
		loginLimiter: newRateLimiter(10, time.Minute),
	}
}

func (s *Server) Handler() http.Handler {
	mux := s.routeMux()

	// webui.Handler wraps mux rather than being registered ON it as another
	// pattern. Measured on this exact route set: mux.Handle("/", ui) beside
	// the routes above turns PUT /api/workspaces from "405 Allow: GET, HEAD,
	// POST" into a 404 and answers POST /healthz with the SPA's HTML and a
	// 200 — a second pattern rooted at "/" resolves ahead of a real route's
	// own method mismatch, so mounting the UI as a pattern is how the admin
	// API's 405s get stolen. Wrapping keeps mux the single source of truth
	// for its own routes; webui.Handler only ever intercepts a GET/HEAD
	// outside /api, /healthz and /readyz, and passes everything else through
	// untouched — see [webui.Handler]'s own doc comment for the full
	// argument.
	//
	// Chain's first argument runs first (outermost). Order matters:
	// rateLimitLogin runs first here so a hammered /api/auth/login is turned
	// away before it costs a database round trip. attachSession must run
	// before enforceCSRF: CSRF needs the session's csrf_token, which only
	// exists in the request context after session lookup. securityHeaders no
	// longer sits in this chain — see below for why it moved out one level.
	inner := httpx.Chain(webui.Handler(webui.Dist, mux, s.cfg, s.log), s.rateLimitLogin, s.attachSession, s.enforceCSRF)

	// /mcp is mounted OUTSIDE inner entirely — see handlerBypass's own
	// comment and §A3 of the MCP slice's context document for the security
	// argument. It must not pass through rateLimitLogin (that limiter guards
	// exactly one route, login, by design — DESIGN §18), attachSession
	// (there is no cookie here to look up — the MCP endpoint is bearer-only,
	// §A2) or enforceCSRF (which would 403 every legitimate non-browser MCP
	// client for lacking an Origin/Referer naming this admin host). Nor may
	// it reach webui.Handler inside inner: isAdminPath does not know /mcp,
	// so a GET there would get the SPA shell and a 200 instead of the MCP
	// transport's own 405.
	//
	// Two rules about this branch's shape, both of which an obvious first
	// draft gets wrong:
	//
	//   - s.mcp is read PER REQUEST, inside this closure — never once here
	//     at Handler() build time. main calls Handler() BEFORE SetMCP
	//     (cmd/mocker/main.go), exactly as it already does for
	//     SetLiveState/SetTraffic; a `if s.mcp == nil { return inner }`
	//     guard evaluated right here would capture "unset" forever and ship
	//     a permanently dead feature behind a fully green test suite — this
	//     project has already shipped exactly that failure twice.
	//   - which path bypasses inner comes from the package-level
	//     handlerBypass table, not a second mcpPath literal typed into this
	//     closure, so mcp_mount_test.go can pin the table's membership
	//     directly: a second bypass added later without updating the table
	//     is then a red test, not a silent discovery.
	withMCP := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if s.mcp != nil && slices.Contains(handlerBypass, r.URL.Path) {
			s.mcp.ServeHTTP(w, r)
			return
		}
		inner.ServeHTTP(w, r)
	})

	// securityHeaders wraps EVERYTHING — withMCP included — because every
	// admin-plane response carries these headers, success or rejection
	// alike; "every response except /mcp" would be a silent narrowing of
	// that guarantee. httpx.Chain(h, s.securityHeaders) is the right call
	// shape for a single middleware: securityHeaders is a method value of
	// type func(http.Handler) http.Handler, which is assignable to
	// httpx.Middleware.
	return httpx.Chain(withMCP, s.securityHeaders)
}

// SetMCP installs the MCP endpoint's handler, served at /mcp OUTSIDE this
// plane's CSRF/session middleware — see Handler() for why, and §A3 of the
// MCP slice's context document for the security argument. Nil (the default)
// means MOCKER_MCP_KEY was unset and /mcp is not a route at all.
//
// Like SetLiveState and SetTraffic, it may be called AFTER Handler() has
// already been built: the branch reads this field per request.
func (s *Server) SetMCP(h http.Handler) {
	s.mcp = h
}

// healthResponse is the body of a healthy /healthz or /readyz.
type healthResponse struct {
	OK bool `json:"ok"`
}

// handleHealthz answers liveness: mocker's own process is up. It never
// touches the database, so it stays fast and accurate even while /readyz is
// failing.
func (s *Server) handleHealthz(w http.ResponseWriter, r *http.Request) {
	httpx.JSON(w, http.StatusOK, healthResponse{OK: true})
}

// handleReadyz answers readiness: the database actually answers a query.
//
// It pings the reader pool directly rather than running any real query
// through workspaces.Repo. GET /readyz carries no auth and no rate limit
// (DESIGN §16 wants it probeable by any container orchestrator), so its cost
// per call must be CONSTANT regardless of how much data is in the database —
// round-1 review finding 2: s.ws.List(ctx, nil) used to decode every
// workspace's settings JSON on every call, letting one workspace with an
// inflated settings blob (finding 3, capped in [workspaces.Repo] since) turn
// an anonymous, unauthenticated GET /readyz into a meaningful CPU/memory
// cost.
func (s *Server) handleReadyz(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), readyzTimeout)
	defer cancel()

	if err := s.db.R.PingContext(ctx); err != nil {
		s.log.Warn("readyz: database not answering", "err", err)
		httpx.Err(w, http.StatusServiceUnavailable, httpx.CodeInternal, "database not ready")
		return
	}
	httpx.JSON(w, http.StatusOK, healthResponse{OK: true})
}

// decodeJSON decodes r's body into v, rejecting unknown fields and trailing
// data. An admin API that silently ignores fields it does not recognize
// hides a client's typo instead of rejecting it; DisallowUnknownFields turns
// that into a 400 the caller can act on.
//
// The caller is expected to already sit behind a body-size limit — main's
// MaxBody middleware wraps the whole dispatcher (see [Server.Handler]) — so
// nothing here adds a second one.
func decodeJSON(r *http.Request, v any) error {
	if r.Body == nil {
		return io.EOF
	}
	return decodeStrict(r.Body, v)
}

// decodeStrict is decodeJSON's body over any reader: unknown fields and
// trailing data refused. The two places that decode a body they already
// hold as bytes (a DELETE .../session body, a `stream` sub-document) go
// through it too, so a misspelled key there is a 400 and not a silently
// widened or dropped field — the one shape of strictness every admin body
// has.
func decodeStrict(r io.Reader, v any) error {
	dec := jsonx.NewDecoder(r)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return err
	}
	if dec.More() {
		return errors.New("admin: unexpected trailing data after JSON body")
	}
	return nil
}

// cookieSecure decides the session cookie's Secure attribute for r: secure
// when the client actually reached this request over TLS — directly or via a
// trusted proxy's X-Forwarded-Proto — or when the deployment is not in dev
// mode. Reading the scheme per-request (rather than trusting cfg alone) is
// what makes the cookie work correctly behind a TLS-terminating proxy.
func (s *Server) cookieSecure(r *http.Request) bool {
	return httpx.ForwardedProto(r, s.cfg.TrustProxy) == "https" || s.cfg.CookieSecure()
}

// parseWorkspaceID extracts and validates the {id} path value, answering 400
// and reporting failure on anything that is not a positive integer.
func parseWorkspaceID(w http.ResponseWriter, r *http.Request) (int64, bool) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Err(w, http.StatusBadRequest, httpx.CodeBadRequest, "invalid workspace id")
		return 0, false
	}
	return id, true
}
