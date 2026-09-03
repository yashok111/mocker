// The admin route table: every route as one row, the mux built from it and
// the auto-checkpoint wrapper that sits on the mux because the table is the
// one place with data for every route. Split out of server.go 2026-09-03;
// the text is unchanged.
package admin

import (
	"errors"
	"net/http"
	"strconv"

	"github.com/yashok111/mocker/internal/checkpoints"
)

// Handler builds the admin plane's HTTP handler: the route set below, the
// built admin UI in front of it (P1d, internal/webui), wrapped in exactly the
// middleware this plane needs on top of what main provides.
//
// main wraps the returned handler with httpx.Recover, httpx.RequestLog and
// httpx.MaxBody ONCE around the whole dispatcher (mock plane included);
// adding them again here would double every log line and enforce the body
// limit twice for no benefit. Handler therefore adds only what is specific to
// this plane: security headers, the login rate limit, session lookup and CSRF
// enforcement (DESIGN §15).
// route is one entry of the admin plane's route table: a Go 1.22 ServeMux
// pattern ("METHOD /path") and the handler it dispatches to.
type route struct {
	pattern string
	handler http.HandlerFunc
}

// autoCheckpointLabels maps a route pattern (exactly as it appears in
// [Server.routes]) to the Russian label its debounced ("auto") checkpoint
// carries — P2d's third of DESIGN §12:770-772's three triggers, «по
// дебаунсу». A pattern absent from this map takes no auto checkpoint at
// all; [Server.routeMux] is the only reader, and only when
// cfg.CheckpointDebounce is configured on. See that method's own comment
// for the wrap site and [Server.withAutoCheckpoint] for what the wrapper
// does and deliberately does not do.
//
// The first eight strings are copied VERBATIM from §4 of the P2d slice's
// context document, the one place both this map and
// scripts/smoke.sh's own acceptance observation read them from — neither
// side invents or translates one, because a string invented here would go
// red against otherwise-correct code the moment the acceptance check
// compares it. The ninth, for A1's PUT editor, is this document's own
// addition (D4 item 4) — editing a custom endpoint mutates the Workspace
// layer exactly as creating and deleting one already do, so it takes an
// undo point on the same terms. TestAutoCheckpointLabels_pinsEveryMutatingRoute
// (autocheckpoint_test.go) is what keeps this map from silently losing a
// route that routes() gains later: it derives every mutating pattern from
// routes() itself and requires each to resolve here OR be a member of a
// named exclusion list, so an unlabelled addition is a red test rather
// than a route that quietly never gets an undo point.
var autoCheckpointLabels = map[string]string{
	"PATCH /api/workspaces/{id}":                          "правка настроек воркспейса",
	"PUT /api/workspaces/{id}/operations/{opKey}":         "правка операции",
	"DELETE /api/workspaces/{id}/operations/{opKey}":      "сброс правки операции",
	"POST /api/workspaces/{id}/auth-preset":               "применение auth-пресета",
	"POST /api/workspaces/{id}/endpoints":                 "новый кастомный endpoint",
	"PUT /api/workspaces/{id}/endpoints/{eid}":            "правка кастомного endpoint'а",
	"DELETE /api/workspaces/{id}/endpoints/{eid}":         "удаление кастомного endpoint'а",
	"POST /api/workspaces/{id}/traffic/{tid}/to-override": "правка из трафика",
	"POST /api/workspaces/{id}/traffic/{tid}/to-endpoint": "endpoint из трафика",
}

// routes is the admin plane's COMPLETE route table for every client the
// OpenAPI contract describes — and the single place that table is written
// down. [Server.Handler] registers exactly this list on its mux, and
// openapi_contract_test.go compares exactly this list against
// api/openapi.json — so a handler added here without a matching entry in the
// contract (or the reverse) is a red test, not a client that silently 404s.
//
// It is a method rather than a package-level var because every handler is a
// method value bound to s; the receiver is never called during table
// construction, so the contract test builds the table from a zero Server.
//
// mcpPath is deliberately NOT in this list. /mcp is JSON-RPC, not a REST
// route a generated client could ever call — describing it in
// api/openapi.json would hand orval a typed client function for a route the
// UI can never reach, and would force api/openapi.json's csrfToken rule
// (openapi_contract_test.go) to grow a second carve-out, next to login, for
// a cookie-free bearer-only route the rule has no business describing. See
// [Server.Handler] for where /mcp actually mounts, and §A4 of the MCP
// slice's context document for the full reasoning.
func (s *Server) routes() []route {
	return []route{
		{"GET /healthz", s.handleHealthz},
		{"GET /readyz", s.handleReadyz},

		{"POST " + loginPath, s.handleLogin},
		{"POST /api/auth/logout", s.handleLogout},
		{"GET /api/me", s.handleMe},

		{"GET /api/workspaces", s.handleListWorkspaces},
		{"POST /api/workspaces", s.handleCreateWorkspace},
		{"GET /api/workspaces/{id}", s.handleGetWorkspace},
		{"PATCH /api/workspaces/{id}", s.handlePatchWorkspace},
		{"DELETE /api/workspaces/{id}", s.handleDeleteWorkspace},
		// P4b (2026-09-02): export, import and fork — DESIGN §14's three
		// transfer routes over checkpoints.Repo's capture and apply.
		{"GET /api/workspaces/{id}/export", s.handleExportWorkspace},
		// P7a (DESIGN §34.4): the workspace as one OpenAPI document — the
		// deliverable a backend team reads and `import_spec` accepts back.
		{"GET /api/workspaces/{id}/openapi.json", s.handleExportOpenAPI},
		{"POST /api/workspaces/import", s.handleImportWorkspace},
		{"POST /api/workspaces/{id}/fork", s.handleForkWorkspace},

		// A6 (DESIGN §32.5): three asset routes, agent-primary, no screen.
		{"PUT /api/workspaces/{id}/assets/{name}", s.handlePutAsset},
		{"GET /api/workspaces/{id}/assets", s.handleListAssets},
		{"DELETE /api/workspaces/{id}/assets/{name}", s.handleDeleteAsset},

		{"POST /api/specs", s.handleImportSpec},
		{"GET /api/specs", s.handleListSpecs},
		{"GET /api/specs/{id}", s.handleGetSpec},
		{"GET /api/specs/{id}/report", s.handleSpecReport},
		{"GET /api/specs/{id}/operations", s.handleSpecOperations},
		{"DELETE /api/specs/{id}", s.handleDeleteSpec},

		// P3a (D10): derived route-family suggestions and their per-workspace
		// decision state. resource_handlers.go owns all three; the decision
		// route is the only mutating one of the trio and joins
		// autoCheckpointExcludedRevisionOnly below rather than
		// autoCheckpointLabels (D10/D13 clause 41; P3b, decisions.md
		// mocker-p3b-resources D3 R25, rewrites the WARRANT below without
		// moving the route). Since P3b, config_snap DOES carry this
		// route's two tables (resources, resource_decisions) — the
		// exclusion no longer rests on "nothing to snapshot" but on two
		// narrower facts: the auto-checkpoint wrapper snapshots BEFORE the
		// handler runs, so the row a CONFIRM leaves holds the PRE-confirm
		// state, and P3b's restore is UPSERT-only (it never deletes a
		// resources row), so rolling back to that row cannot undo the
		// confirm it preceded — a label here would promise exactly the
		// undo this shape cannot perform. The one case where a label would
		// genuinely help, a DECLINE, is the case MOCKER_CHECKPOINT_DEBOUNCE
		// already eats in the common sequence (confirm then decline inside
		// one window suppresses the decline's own row).
		{"GET /api/specs/{id}/resource-suggestions", s.handleListResourceSuggestions},
		{"GET /api/workspaces/{id}/resources", s.handleListWorkspaceResources},
		{"POST /api/workspaces/{id}/resource-decisions", s.handleDecideResource},

		// P4a (mocker-p4a-triage decisions.md D4): the three silent-drift
		// signals named against the workspace's CURRENTLY bound spec — an
		// orphaned operation override, an orphaned confirmed resource family,
		// a shadowing custom endpoint. A GET, so it is excluded from the
		// mutating population by construction (D6.1) and joins neither
		// autoCheckpointLabels nor either exclusion group below; drift_handlers.go
		// owns it and writes nothing but [specs.Repo.EnsureSuggestions]'s own
		// lazy backfill of resource_suggestions (D4.4).
		{"GET /api/workspaces/{id}/drift", s.handleGetWorkspaceDrift},

		// A4 (decisions.md mocker-a4-mcp-reach D4): the read corner of
		// DESIGN.md:936's entity CRUD, addressed by route_family, never by
		// resources.id — GET only, no /:key. A declared DIVERGENCE from
		// that design line (recorded in CARVE-OUTS.md, an amendment to
		// P3a's own entry): the write half stays refused by the same rule
		// that makes a confirmed resource uneditable. Agent-only by policy
		// exactly like GET .../drift above — coverage.test.ts's EXEMPT map
		// carries the second entry. A GET, so it joins neither
		// autoCheckpointLabels nor either exclusion group below (D10): it
		// bumps no revision, takes no checkpoint, writes no row.
		{"GET /api/workspaces/{id}/resources/{family}/entities", s.handleListResourceEntities},
		// A11: the read's two write siblings — create-or-replace one row by
		// key, delete one row by key. Agent-only like the read (EXEMPT
		// carries both). They change ONLY entities, so they join
		// autoCheckpointExcludedNeverTouchesLayer beside reset-data, not
		// autoCheckpointLabels: config_snap does not carry rows, a label
		// would promise an undo a config rollback cannot perform, and the
		// real undo is a checkpoint restored with restoreData: true.
		{"PUT /api/workspaces/{id}/resources/{family}/entities/{key}", s.handleSetResourceEntity},
		{"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}", s.handleDeleteResourceEntity},

		// P3f (decisions.md D4.1): re-runs family derivation over an
		// already-imported spec and mints a new resource_suggestions
		// generation if it differs from the newest one. Spec-scoped, never
		// workspace-scoped — derivation is a function of a spec's own
		// document, and one call changes what every workspace bound to it
		// sees. Touches no workspace table at all (D7.1), so it joins
		// autoCheckpointExcludedNeverTouchesLayer below, not
		// autoCheckpointLabels: a checkpoint is per-workspace and this
		// route names none, and config_snap does not capture
		// resource_suggestions in the first place.
		{"POST /api/specs/{id}/rederive", s.handleRederiveSuggestions},

		// DESIGN §14 lines 828-862 name the four /operations routes verbatim.
		// GET/POST .../auth-preset are this slice's own addition — the design
		// text doesn't name them, but §10 requires the identity/token mapping be
		// SHOWN and EDITED before anything is written, which needs a preview call
		// (GET, writes nothing) and a separate apply call (POST, writes exactly
		// the operator-approved list) rather than one route that does both.
		{"GET /api/workspaces/{id}/operations", s.handleListOperations},
		{"GET /api/workspaces/{id}/operations/{opKey}", s.handleGetOperation},
		{"PUT /api/workspaces/{id}/operations/{opKey}", s.handlePutOperation},
		{"DELETE /api/workspaces/{id}/operations/{opKey}", s.handleDeleteOperation},
		{"GET /api/workspaces/{id}/auth-preset", s.handleGetAuthPreset},
		{"POST /api/workspaces/{id}/auth-preset", s.handleApplyAuthPreset},

		// P2f: DESIGN §14's "POST .../preview" — what the draft override in
		// the request body WOULD produce once saved, rendered through the
		// SAME assembly the mock plane serves with (preview_handlers.go's
		// own doc comment). Writes nothing, bumps no revision, takes no
		// checkpoint — so it is absent from autoCheckpointLabels on
		// purpose, and stays absent for that reason. The OTHER half of this
		// note has been reversed: A2 (mocker-a-mcp gate document, D7) put
		// the route INTO mcpAllowedRoutes as preview_operation, because an
		// agent editing an override cannot see a screen and would otherwise
		// have to issue a real request — writing traffic and bumping the
		// revision — just to learn what body its edit produces.
		{"POST /api/workspaces/{id}/preview", s.handlePreviewOperation},

		// P1c2: the admin half of live state (DESIGN §14 lines 852-861, plus the
		// GET/DELETE addition logged in livestate_handlers.go's own header
		// comment) and of traffic (screen 4). Both surfaces route one-to-one
		// onto the SAME *livestate.Store / *traffic.Recorder the mock plane
		// itself reads and writes — see SetLiveState/SetTraffic for why they
		// arrive as setters rather than New parameters.
		{"GET /api/workspaces/{id}/session", s.handleGetSession},
		{"POST /api/workspaces/{id}/session", s.handlePostSession},
		{"DELETE /api/workspaces/{id}/session", s.handleDeleteSession},

		// DESIGN §14 screen 4's «Проверить», server half: mocker dialling
		// the workspace's own external URL itself, so a browser that can't
		// reach it can be told WHY (probe_handlers.go, internal/probe — the
		// first outgoing HTTP client in this tree).
		{"POST /api/workspaces/{id}/probe", s.handleProbeWorkspace},

		{"GET /api/workspaces/{id}/traffic", s.handleListTraffic},
		{"GET /api/workspaces/{id}/traffic/poll", s.handlePollTraffic},
		{"DELETE /api/workspaces/{id}/traffic", s.handleDeleteTraffic},

		// P6a (decisions.md mocker-p6a-sse D3, D15): the traffic feed over
		// SSE beside the poll it never replaces, and the process-wide
		// streaming health an agent reads (stream_handlers.go). Both GET,
		// so neither joins the CSRF or the auto-checkpoint sets.
		{"GET /api/workspaces/{id}/traffic/stream", s.handleStreamTraffic},
		{"GET /api/stream/stats", s.handleStreamStats},

		// P6c (decisions.md mocker-p6c-live-conns D1): the live-connection
		// surface over the MOCK plane's registry (connection_handlers.go) —
		// list, close, push a frame. The two mutating ones join the CSRF
		// set by method and the "never touches a layer" auto-checkpoint
		// group by argument: a connection and a pushed frame are RAM.
		{"GET /api/workspaces/{id}/connections", s.handleListConnections},
		{"DELETE /api/workspaces/{id}/connections/{cid}", s.handleCloseConnection},
		{"POST /api/workspaces/{id}/connections/{cid}/frames", s.handlePushFrame},

		// P1c2: custom endpoints (screen 6's CRUD) and the two
		// traffic-to-{override,endpoint} conversions (screen 8). The PUT
		// editor below is A1 (mocker-a-mcp gate document, D4), not P1c2 —
		// endpoint_handlers.go and from_traffic.go own everything below.
		{"GET /api/workspaces/{id}/endpoints", s.handleListEndpoints},
		// P6b (decisions.md mocker-p6b-sse-mock D13): a stream draft's first
		// frames, no row written (endpoint_preview_handlers.go). Registered
		// BEFORE the {eid} routes below only for reading order — the mux
		// prefers the literal segment "preview" over a {eid} wildcard on
		// its own.
		{"POST /api/workspaces/{id}/endpoints/preview", s.handlePreviewEndpoint},
		{"POST /api/workspaces/{id}/endpoints", s.handleCreateEndpoint},
		// A1 (mocker-a-mcp gate document, D4): the PUT editor DESIGN §14 and
		// this file's OWN package doc comment used to defer as "P2" —
		// endpoint_handlers.go's handleUpdateEndpoint owns it, a full
		// replacement of the row's definition, not a partial merge.
		{"PUT /api/workspaces/{id}/endpoints/{eid}", s.handleUpdateEndpoint},
		{"DELETE /api/workspaces/{id}/endpoints/{eid}", s.handleDeleteEndpoint},

		{"POST /api/workspaces/{id}/traffic/{tid}/to-override", s.handleToOverride},
		{"POST /api/workspaces/{id}/traffic/{tid}/to-endpoint", s.handleToEndpoint},

		// P2b/P2d: DESIGN §4's Scenario layer. DESIGN §14:840-841's [/:sid]
		// block names list/create/get/delete/rename verbatim, and every one
		// of those five is registered below now — P2b shipped all but
		// rename, deferring it because a name collision
		// (UNIQUE(workspace_id, name)) needs a 409 path and a form neither
		// that slice's own §0 wanted to open; delete-and-recreate covered
		// the gap until P2d's PUT below closed it (see
		// [Server.handleRenameScenario]'s own comment for why that route
		// bumps no revision). The activate/deactivate pair is P2b's OWN
		// addition, for the same reason the auth-preset pair above is:
		// DESIGN predates the discovery that PATCH cannot express "no
		// scenario" — .../workspaces/{id}'s scenarioId is a bare *int64,
		// which collapses `null` and "field absent" (A6, the same trap
		// CLAUDE.md already records for specId) — so deactivation gets its
		// own explicit route rather than riding inside PATCH.
		{"GET /api/workspaces/{id}/scenarios", s.handleListScenarios},
		{"POST /api/workspaces/{id}/scenarios", s.handleCreateScenario},
		{"GET /api/workspaces/{id}/scenarios/{sid}", s.handleGetScenario},
		{"PUT /api/workspaces/{id}/scenarios/{sid}", s.handleRenameScenario},
		{"DELETE /api/workspaces/{id}/scenarios/{sid}", s.handleDeleteScenario},
		{"POST /api/workspaces/{id}/scenarios/{sid}/activate", s.handleActivateScenario},
		{"POST /api/workspaces/{id}/scenarios/deactivate", s.handleDeactivateScenario},

		// P2c: history and undo for the workspace layer (DESIGN §12:759-776,
		// §14:838-842 minus reset-data — §0 of the P2c context document).
		// checkpoint_handlers.go owns everything below; [checkpoints.Repo]
		// owns every write these routes cause. DELETE below is P2d's own
		// addition — DESIGN §14:838 names only GET/POST for this
		// collection, so a manual checkpoint could be created and never
		// removed until this slice.
		{"GET /api/workspaces/{id}/checkpoints", s.handleListCheckpoints},
		{"POST /api/workspaces/{id}/checkpoints", s.handleCreateCheckpoint},
		{"DELETE /api/workspaces/{id}/checkpoints/{cid}", s.handleDeleteCheckpoint},
		{"POST /api/workspaces/{id}/rollback/{cid}", s.handleRollbackWorkspace},
		{"POST /api/workspaces/{id}/reset-overrides", s.handleResetOverrides},

		// P3b (D3): the RESET half of P3's carve-out list — reset-data
		// lives entirely inside internal/resources (resource_handlers.go's
		// own doc comment on handleResetData says why) and joins
		// autoCheckpointExcludedNeverTouchesLayer below, not
		// autoCheckpointLabels: it changes ONLY entities, a layer
		// config_snap does not carry, so a label here would promise an
		// undo this build cannot perform — the identical reasoning
		// resource-decisions' own comment above already gives.
		{"POST /api/workspaces/{id}/reset-data", s.handleResetData},
	}
}

// routeMux returns the Server's route dispatch mux — the actual registered
// handlers for every entry [Server.routes] lists — built exactly once no
// matter how many times, or in which order, [Server.Handler] and
// [Server.CallAsMCP] ask for it.
//
// Handler() used to build this mux inline, which was fine while Handler()
// was its only caller (main invokes it exactly once). CallAsMCP needs the
// SAME mux — not a second, independently built copy that starts identical
// today and is free to drift the moment routes() changes — and it must work
// on a Server whose Handler() was NEVER called at all: an in-process caller
// has no HTTP listener to force that ordering the way a real deployment
// does. sync.Once is safe here, with no fallback or retry path, because
// routes() is entirely static — nothing in it depends on request-time
// state, so there is no failure mode worth handling.
//
// This is also the ONLY place [Server.withAutoCheckpoint] gets installed
// (P2d §4): per-route data (rt.pattern, looked up in
// [autoCheckpointLabels]) only exists in this loop, and building the mux
// here rather than inline in [Server.Handler] is exactly what makes the
// wrap apply to [Server.CallAsMCP] too — both share this SAME mux, so an
// MCP-origin PUT .../operations/{opKey} leaves an auto checkpoint behind
// exactly like a browser-origin one does. cfg.CheckpointDebounce is read
// once, here, rather than per request inside the wrapper: a window of 0
// means the wrapper is not installed AT ALL, not installed-and-always-a-
// no-op — the distinction TestAutoCheckpointWrapper_disabledWhenWindowIsZero
// (autocheckpoint_test.go) exists to pin.
func (s *Server) routeMux() *http.ServeMux {
	s.routeMuxOnce.Do(func() {
		mux := http.NewServeMux()
		for _, rt := range s.routes() {
			handler := rt.handler
			if label, ok := autoCheckpointLabels[rt.pattern]; ok && s.cfg.CheckpointDebounce > 0 {
				handler = s.withAutoCheckpoint(label, handler)
			}
			mux.HandleFunc(rt.pattern, handler)
		}
		s.routeMuxVal = mux
	})
	return s.routeMuxVal
}

// withAutoCheckpoint wraps handler so [Server.autoCheckpoint] runs to
// completion — window check, capture, fence, insert, all of it — BEFORE
// handler ever runs. Capture-before-and-insert-after (not
// capture-before-and-run-after-and-insert-after) is not on offer here:
// [checkpoints.Repo.Auto] reuses [checkpoints.Repo.Create]'s own fenceTx,
// which exists precisely to refuse an insert whose captured state moved
// between capture and insert, so running the whole write up front is what
// keeps this wrapper outside that race rather than a second source of it.
// A row written in front of a request that goes on to answer 400 or 404 is
// still a correct undo point: it captured the state at time T, and a
// failed request changes nothing, so the next successful mutation departs
// from exactly that state.
func (s *Server) withAutoCheckpoint(label string, handler http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		s.autoCheckpoint(r, label)
		handler(w, r)
	}
}

// autoCheckpoint is [Server.withAutoCheckpoint]'s body, split out so each
// rule below reads as its own guard clause rather than nested inside the
// closure that method returns.
//
// Two cases are silent no-ops, deliberately not logged:
//
//   - No authenticated user ([UserFrom] fails). checkpoints.created_by is a
//     foreign key into users(id), and an anonymous request has no caller to
//     attribute a row to — [Server.requireUser] answers its own 401 from
//     inside handler regardless, so the request is not silently accepted,
//     only this row is skipped. createdBy is deliberately the user, never
//     the session: [Server.CallAsMCP]'s loopback path attaches a nil
//     session on purpose (loopback.go), and a hook that read the session
//     half would nil-dereference on every MCP-origin mutation.
//   - The {id} path value does not parse, or names no workspace
//     ([checkpoints.ErrWorkspaceNotFound]). Both are already
//     handler-visible failures (the handler answers its own 400 or 404
//     right after this returns) — this wrapper has nothing of its own to
//     report about either one.
//
// Any OTHER error [checkpoints.Repo.Auto] returns IS logged, and the
// request proceeds regardless either way: a failed auto checkpoint must
// never fail the request it rides in front of.
func (s *Server) autoCheckpoint(r *http.Request, label string) {
	user, ok := UserFrom(r.Context())
	if !ok {
		return
	}
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil || id <= 0 {
		return
	}
	if _, err := s.checkpointsRepo.Auto(r.Context(), id, label, user.ID, s.cfg.CheckpointDebounce); err != nil {
		if errors.Is(err, checkpoints.ErrWorkspaceNotFound) {
			return
		}
		s.log.Error("auto checkpoint", "workspace_id", id, "label", label, "err", err)
	}
}
