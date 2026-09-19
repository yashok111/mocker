// The admin route table: every route as one row, the mux built from it and
// the auto-checkpoint wrapper that sits on the mux because the table is the
// one place with data for every route. Split out of server.go 2026-09-03;
// the text is unchanged.
//
// Since 2026-09-07 the row carries the two POLICIES that used to be
// declared beside it in four other places — the MCP allowlist (loopback.go
// retyped fifty-odd "METHOD /path" strings verbatim and a comment called
// that list "the single highest-risk object" of its slice) and the
// auto-checkpoint decision (one label map here plus four exclusion maps in
// autocheckpoint_test.go). Every one of those was keyed by the SAME pattern
// string this table already spells, so each was one copy free to drift from
// this one; both are now DERIVED from these rows (see [mcpAllowedRoutes] in
// loopback.go and [Server.routeMux] below) and the drift is not expressible.
// What the tests still guard is what a derivation cannot: that the set of
// routes deliberately kept OUT of MCP reach is exactly the documented one,
// and that no mutating route ships with the zero-value — undecided —
// checkpoint policy.
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
// pattern ("METHOD /path"), the handler it dispatches to, and the two
// policies every other reader of this table used to declare for itself.
//
// Both policy fields are DECIDED per row, never defaulted: the zero value
// of each means "nobody has said", which is a red test rather than a quiet
// default. That is the whole point of moving them here — an added route
// that says nothing about MCP reach or about undo fails a bar, where before
// it silently landed outside a copy nobody thought to update.
type route struct {
	pattern string
	handler http.HandlerFunc
	// mcp is whether [CallAsMCP] may dispatch to this row, and when it may
	// not, WHY — see [mcpPolicy].
	mcp mcpPolicy
	// checkpoint is this row's decision about the debounced ("auto")
	// checkpoint — see [checkpointPolicy].
	checkpoint checkpointPolicy
}

// mcpPolicy is one route's decision about MCP reach: [mcpAllow] to put the
// pattern in the allowlist [CallAsMCP] enforces, [mcpDeny] to keep it out
// with the reason written down at the row.
//
// The zero value is neither — not allowed, and with no reason given — so a
// route added without a deliberate answer fails
// TestRouteMCPPolicyIsDecided (loopback_test.go) instead of landing on the
// safe side by accident. Safe-by-accident is not the property this file
// wants: the reasons below are the whole argument for why the surface a
// bearer key reaches is smaller than the surface a session reaches, and an
// unreasoned exclusion cannot be reviewed.
type mcpPolicy struct {
	allowed bool
	// reason is why this route is NOT reachable over MCP. Non-empty
	// exactly when allowed is false.
	reason string
}

// mcpAllow marks a row as reachable by [CallAsMCP].
//
// What may carry it is not a judgement made at a call site: the MCP slice's
// gate document enumerates both the tools and the routes each one wraps, and
// a tool that needs a route this table does not allow is that document's
// decision to make. CallAsMCP bypasses attachSession, enforceCSRF and
// rateLimitLogin by construction, so the set of rows carrying this value is
// the whole of what stands between a bearer key and the admin plane.
var mcpAllow = mcpPolicy{allowed: true}

// mcpDeny marks a row as OUT of MCP reach, with the reason it is out.
func mcpDeny(reason string) mcpPolicy { return mcpPolicy{reason: reason} }

// checkpointGroup names the decision a route makes about P2d's debounced
// ("auto") checkpoint — DESIGN §12:770-772's third trigger, «по дебаунсу».
//
// The zero value, cpGroupUndecided, is deliberately not a decision: a
// mutating route that carries it fails
// TestAutoCheckpointPolicy_pinsEveryMutatingRoute (autocheckpoint_test.go).
// The five real answers below are the four exclusion groups §4 of the P2d
// context document names plus the labelled case itself; cpGroupRead is the
// sixth and is the answer a GET gives — it is excluded by construction, and
// saying so explicitly is what keeps "a read" distinguishable from "nobody
// decided".
type checkpointGroup uint8

const (
	cpGroupUndecided checkpointGroup = iota
	cpGroupRead
	cpGroupLabelled
	cpGroupRevisionOnly
	cpGroupNoLayerYet
	cpGroupNeverTouchesLayer
	cpGroupAnotherLayer
)

// String is §4's own wording for each group, kept as the one place those
// phrases live: the test that counts group membership prints them, and a
// reader comparing this table against that document should find the same
// sentence on both sides.
func (g checkpointGroup) String() string {
	switch g {
	case cpGroupRead:
		return "a read: writes nothing, excluded by construction"
	case cpGroupLabelled:
		return "labelled"
	case cpGroupRevisionOnly:
		return "bumps revision only, changes nothing a snapshot holds"
	case cpGroupNoLayerYet:
		return "no workspace layer to snapshot yet or anymore"
	case cpGroupNeverTouchesLayer:
		return "never touches a workspace layer at all"
	case cpGroupAnotherLayer:
		return "writes a row of another layer"
	default:
		return "UNDECIDED"
	}
}

// checkpointPolicy is one route's auto-checkpoint decision: a group, plus
// the Russian label the checkpoint carries when the group is
// cpGroupLabelled.
//
// The label strings on the rows below are copied VERBATIM from §4 of the
// P2d slice's context document, the one place both this table and
// scripts/smoke.sh's own acceptance observation read them from — neither
// side invents or translates one, because a string invented here would go
// red against otherwise-correct code the moment the acceptance check
// compares it. The ninth, for A1's PUT editor, is that document's own
// addition (D4 item 4): editing a custom endpoint mutates the Workspace
// layer exactly as creating and deleting one already do, so it takes an
// undo point on the same terms.
type checkpointPolicy struct {
	group checkpointGroup
	// label is the checkpoint's own text; non-empty exactly when group is
	// cpGroupLabelled. [Server.routeMux] is the only production reader.
	label string
}

// The five decided answers a route can give without a label. Each carries
// §4's group name; the WARRANT for a particular route belonging to one of
// them is written at that route's own row, because that is where a reader
// asking "why does this verb take no undo point" looks.
var (
	// cpRead is what a GET says: it writes nothing, so there is nothing to
	// snapshot in front of it. Stated rather than left at the zero value so
	// "a read" and "nobody decided" are different facts.
	cpRead = checkpointPolicy{group: cpGroupRead}
	// cpRevisionOnly: bumps workspaces.revision but changes nothing a
	// snapshot HOLDS. Two of its members (rollback, reset-overrides)
	// already write their own pre-destructive row for the same instant;
	// a second, auto snapshot of the identical state would be noise in the
	// screen an operator reads to find a state worth returning to.
	cpRevisionOnly = checkpointPolicy{group: cpGroupRevisionOnly}
	// cpNoLayerYet: no workspace layer exists yet (create — the request IS
	// what creates it) or none is left (delete — nothing survives to
	// restore into).
	cpNoLayerYet = checkpointPolicy{group: cpGroupNoLayerYet}
	// cpNeverTouchesLayer: never touches a workspace layer at all.
	cpNeverTouchesLayer = checkpointPolicy{group: cpGroupNeverTouchesLayer}
	// cpAnotherLayer: writes a row of a DIFFERENT layer — a scenario row
	// (including the clone) or a checkpoint row itself — not the workspace
	// layer this trigger exists to snapshot.
	cpAnotherLayer = checkpointPolicy{group: cpGroupAnotherLayer}
)

// cpLabelled is the answer a route gives when it DOES take an undo point:
// the label that checkpoint carries. [Server.routeMux] wraps exactly these
// rows with [Server.withAutoCheckpoint], and only when cfg.CheckpointDebounce
// is configured on.
func cpLabelled(label string) checkpointPolicy {
	return checkpointPolicy{group: cpGroupLabelled, label: label}
}

// routes is the admin plane's COMPLETE route table for every client the
// OpenAPI contract describes — and the single place that table is written
// down. [Server.Handler] registers exactly this list on its mux,
// openapi_contract_test.go compares exactly this list against
// api/openapi.json — so a handler added here without a matching entry in the
// contract (or the reverse) is a red test, not a client that silently 404s —
// and, since 2026-09-07, [mcpAllowedRoutes] and the auto-checkpoint wrapper
// are derived from the two policy columns rather than retyped elsewhere.
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
// a cookie-free bearer-only route the rule has no business describing. It is
// out of MCP reach for the same structural reason and needs no mcpDeny row:
// CallAsMCP dispatches against this table, and /mcp is not in it. See
// [Server.Handler] for where /mcp actually mounts, and §A4 of the MCP
// slice's context document for the full reasoning.
func (s *Server) routes() []route {
	return []route{
		{"GET /api/designs", s.handleListAPIDesigns, mcpAllow, cpRead},
		{"POST /api/designs", s.handleCreateAPIDesign, mcpAllow, cpAnotherLayer},
		{"GET /api/designs/{id}", s.handleGetAPIDesign, mcpAllow, cpRead},
		{"PUT /api/designs/{id}/draft", s.handleSaveAPIDesignDraft, mcpAllow, cpAnotherLayer},
		{"GET /api/designs/{id}/revisions/{rid}", s.handleGetAPIDesignRevision, mcpAllow, cpRead},
		{"GET /api/designs/{id}/diff", s.handleGetAPIDesignDiff, mcpAllow, cpRead},
		{"POST /api/designs/{id}/validate", s.handleValidateAPIDesign, mcpAllow, cpNeverTouchesLayer},
		{"POST /api/designs/{id}/change-sets", s.handleCreateAPIDesignChangeSet, mcpAllow, cpAnotherLayer},
		{"PUT /api/designs/{id}/change-sets/{cid}", s.handleCloseAPIDesignChangeSet, mcpAllow, cpAnotherLayer},
		{"POST /api/designs/{id}/reviews", s.handleRequestAPIDesignReview, mcpAllow, cpAnotherLayer},
		{"POST /api/designs/{id}/reviews/{rid}/publish", s.handlePublishAPIDesignReview, mcpDeny("publication requires human confirmation through a UI session"), cpAnotherLayer},
		{"POST /api/designs/{id}/restore", s.handleRestoreAPIDesignRevision, mcpAllow, cpAnotherLayer},
		// The two infrastructure probes: not part of the admin surface a
		// tool composes (mocker-a-mcp D12), and reads, so no checkpoint.
		{"GET /healthz", s.handleHealthz, mcpDeny("an infrastructure probe, not part of the admin surface a tool composes"), cpRead},
		{"GET /readyz", s.handleReadyz, mcpDeny("an infrastructure probe, not part of the admin surface a tool composes"), cpRead},

		// mocker-a-mcp D12's first three exclusions. Login writes no
		// workspace layer either, which is why it is a member of the
		// never-touches-a-layer group below rather than labelled.
		{"POST " + loginPath, s.handleLogin, mcpDeny("allowlisting it would hand an unthrottled credential oracle to anything holding the bearer key"), cpNeverTouchesLayer},
		{"POST /api/auth/logout", s.handleLogout, mcpDeny("this endpoint has no session and a fixed identity"), cpNeverTouchesLayer},
		{"GET /api/me", s.handleMe, mcpDeny("this endpoint has no session and a fixed identity"), cpRead},

		{"GET /api/workspaces", s.handleListWorkspaces, mcpAllow, cpRead},
		{"POST /api/workspaces", s.handleCreateWorkspace, mcpAllow, cpNoLayerYet},
		{"GET /api/workspaces/{id}", s.handleGetWorkspace, mcpAllow, cpRead},
		{"PATCH /api/workspaces/{id}", s.handlePatchWorkspace, mcpAllow, cpLabelled("правка настроек воркспейса")},
		{"DELETE /api/workspaces/{id}", s.handleDeleteWorkspace, mcpAllow, cpNoLayerYet},
		// P4b (2026-09-02): export, import and fork — DESIGN §14's three
		// transfer routes over checkpoints.Repo's capture and apply. An
		// import creates a NEW workspace and a fork writes only the copy —
		// neither touches a layer of the workspace the route names (import
		// names none), and both write the new row's baseline checkpoint
		// themselves, inside the creating transaction.
		{"GET /api/workspaces/{id}/export", s.handleExportWorkspace, mcpAllow, cpRead},
		// P7a (DESIGN §34.4): the workspace as one OpenAPI document — the
		// deliverable a backend team reads and `import_spec` accepts back.
		{"GET /api/workspaces/{id}/openapi.json", s.handleExportOpenAPI, mcpAllow, cpRead},
		{"POST /api/workspaces/import", s.handleImportWorkspace, mcpAllow, cpNeverTouchesLayer},
		{"POST /api/workspaces/{id}/fork", s.handleForkWorkspace, mcpAllow, cpNeverTouchesLayer},

		// A6 (DESIGN §32.5): three asset routes, agent-primary, no screen.
		// An asset's bytes are not configuration — config_snap carries no
		// asset (DESIGN §32.4) — so a checkpoint before an upload or a
		// delete would capture nothing the verb changes (D3). Both mutating
		// ones bump revision on their own (D11).
		{"PUT /api/workspaces/{id}/assets/{name}", s.handlePutAsset, mcpAllow, cpNeverTouchesLayer},
		{"GET /api/workspaces/{id}/assets", s.handleListAssets, mcpAllow, cpRead},
		{"DELETE /api/workspaces/{id}/assets/{name}", s.handleDeleteAsset, mcpAllow, cpNeverTouchesLayer},

		// A8 (2026-09-02): POST /api/specs is reachable over MCP —
		// mocker-a4-mcp-reach D3 kept it out because the /specs screen
		// "already works and stays the only way in"; the owner reversed
		// that so an agent holding the spec file in its own repository
		// needs no human to paste it. DELETE /api/specs/{id} stays out, and
		// that reason is on its own row below.
		{"POST /api/specs", s.handleImportSpec, mcpAllow, cpNeverTouchesLayer},
		{"GET /api/specs", s.handleListSpecs, mcpAllow, cpRead},
		{"GET /api/specs/{id}", s.handleGetSpec, mcpAllow, cpRead},
		{"GET /api/specs/{id}/report", s.handleSpecReport, mcpAllow, cpRead},
		{"GET /api/specs/{id}/operations", s.handleSpecOperations, mcpAllow, cpRead},
		{"DELETE /api/specs/{id}", s.handleDeleteSpec, mcpDeny("it cascades across every bound workspace"), cpNeverTouchesLayer},

		// P3a (D10): derived route-family suggestions and their per-workspace
		// decision state. resource_handlers.go owns all three; the decision
		// route is the only mutating one of the trio and takes
		// cpRevisionOnly rather than a label (D10/D13 clause 41; P3b,
		// decisions.md mocker-p3b-resources D3 R25, rewrites the WARRANT
		// without moving the route). Since P3b, config_snap DOES carry this
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
		{"GET /api/specs/{id}/resource-suggestions", s.handleListResourceSuggestions, mcpAllow, cpRead},
		{"GET /api/workspaces/{id}/resources", s.handleListWorkspaceResources, mcpAllow, cpRead},
		{"POST /api/workspaces/{id}/resource-decisions", s.handleDecideResource, mcpAllow, cpRevisionOnly},

		// P4a (mocker-p4a-triage decisions.md D4): the three silent-drift
		// signals named against the workspace's CURRENTLY bound spec — an
		// orphaned operation override, an orphaned confirmed resource family,
		// a shadowing custom endpoint. A GET, so it is excluded from the
		// mutating population by construction (D6.1); drift_handlers.go
		// owns it and writes nothing but [specs.Repo.EnsureSuggestions]'s own
		// lazy backfill of resource_suggestions (D4.4). Agent-only from P4a
		// to A20 (2026-09-05), when «Проверить спеку» on the overview
		// (DriftPanel.tsx) started calling it on the button.
		{"GET /api/workspaces/{id}/drift", s.handleGetWorkspaceDrift, mcpAllow, cpRead},

		// A4 (decisions.md mocker-a4-mcp-reach D4): the read corner of
		// DESIGN.md:936's entity CRUD, addressed by route_family, never by
		// resources.id — GET only, no /:key. A declared DIVERGENCE from
		// that design line (recorded in CARVE-OUTS.md, an amendment to
		// P3a's own entry): the write half stays refused by the same rule
		// that makes a confirmed resource uneditable. Agent-only by policy
		// from A4 to A20 (2026-09-05, the resources screen's «Записи»
		// reads it now; the EXEMPT entry withdrawn). A GET (D10): it
		// bumps no revision, takes no checkpoint, writes no row.
		{"GET /api/workspaces/{id}/resources/{family}/entities", s.handleListResourceEntities, mcpAllow, cpRead},
		// A11: the read's two write siblings — create-or-replace one row by
		// key, delete one row by key. Agent-only like the read until A20
		// (the same screen edits and deletes a row). They change ONLY
		// entities, so they take cpNeverTouchesLayer beside reset-data, not
		// a label: config_snap does not carry rows, a label would promise
		// an undo a config rollback cannot perform, and the real undo is a
		// checkpoint restored with restoreData: true.
		{"PUT /api/workspaces/{id}/resources/{family}/entities/{key}", s.handleSetResourceEntity, mcpAllow, cpNeverTouchesLayer},
		{"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}", s.handleDeleteResourceEntity, mcpAllow, cpNeverTouchesLayer},

		// P3f (decisions.md D4.1): re-runs family derivation over an
		// already-imported spec and mints a new resource_suggestions
		// generation if it differs from the newest one. Spec-scoped, never
		// workspace-scoped — derivation is a function of a spec's own
		// document, and one call changes what every workspace bound to it
		// sees. Touches no workspace table at all (D7.1), so it takes
		// cpNeverTouchesLayer, not a label: a checkpoint is per-workspace
		// and this route names none, and config_snap does not capture
		// resource_suggestions in the first place.
		{"POST /api/specs/{id}/rederive", s.handleRederiveSuggestions, mcpAllow, cpNeverTouchesLayer},

		// DESIGN §14 lines 828-862 name the four /operations routes verbatim.
		// GET/POST .../auth-preset are this slice's own addition — the design
		// text doesn't name them, but §10 requires the identity/token mapping be
		// SHOWN and EDITED before anything is written, which needs a preview call
		// (GET, writes nothing) and a separate apply call (POST, writes exactly
		// the operator-approved list) rather than one route that does both.
		{"GET /api/workspaces/{id}/operations", s.handleListOperations, mcpAllow, cpRead},
		{"GET /api/workspaces/{id}/operations/{opKey}", s.handleGetOperation, mcpAllow, cpRead},
		{"PUT /api/workspaces/{id}/operations/{opKey}", s.handlePutOperation, mcpAllow, cpLabelled("правка операции")},
		{"DELETE /api/workspaces/{id}/operations/{opKey}", s.handleDeleteOperation, mcpAllow, cpLabelled("сброс правки операции")},
		{"GET /api/workspaces/{id}/auth-preset", s.handleGetAuthPreset, mcpAllow, cpRead},
		{"POST /api/workspaces/{id}/auth-preset", s.handleApplyAuthPreset, mcpAllow, cpLabelled("применение auth-пресета")},

		// P2f: DESIGN §14's "POST .../preview" — what the draft override in
		// the request body WOULD produce once saved, rendered through the
		// SAME assembly the mock plane serves with (preview_handlers.go's
		// own doc comment). Writes nothing, bumps no revision, takes no
		// checkpoint — it reads a workspace layer (to build its own
		// throwaway runtime) but never writes one, which is why it takes
		// cpNeverTouchesLayer and not a label. The OTHER half of this note
		// has been reversed: A2 (mocker-a-mcp gate document, D7) put the
		// route INTO MCP reach as preview_operation, because an agent
		// editing an override cannot see a screen and would otherwise have
		// to issue a real request — writing traffic and bumping the
		// revision — just to learn what body its edit produces.
		{"POST /api/workspaces/{id}/preview", s.handlePreviewOperation, mcpAllow, cpNeverTouchesLayer},

		// P1c2: the admin half of live state (DESIGN §14 lines 852-861, plus the
		// GET/DELETE addition logged in livestate_handlers.go's own header
		// comment) and of traffic (screen 4). Both surfaces route one-to-one
		// onto the SAME *livestate.Store / *traffic.Recorder the mock plane
		// itself reads and writes — see SetLiveState/SetTraffic for why they
		// arrive as setters rather than New parameters. Session directives
		// take cpNeverTouchesLayer categorically, under all circumstances —
		// DESIGN.md:1098 forbids them from touching SQLite, so they can never
		// gain a label later without also violating that rule.
		{"GET /api/workspaces/{id}/session", s.handleGetSession, mcpAllow, cpRead},
		{"POST /api/workspaces/{id}/session", s.handlePostSession, mcpAllow, cpNeverTouchesLayer},
		{"DELETE /api/workspaces/{id}/session", s.handleDeleteSession, mcpAllow, cpNeverTouchesLayer},

		// DESIGN §14 screen 4's «Проверить», server half: mocker dialling
		// the workspace's own external URL itself, so a browser that can't
		// reach it can be told WHY (probe_handlers.go, internal/probe — the
		// first outgoing HTTP client in this tree). It was among
		// mocker-a-mcp D12's exclusions and is not any more
		// (mocker-a4-mcp-reach D5): a probe destroys nothing and reaches
		// exactly the hosts an operator's own «Проверить» click can reach,
		// so the reason the original D12 exclusions exist does not apply.
		{"POST /api/workspaces/{id}/probe", s.handleProbeWorkspace, mcpAllow, cpNeverTouchesLayer},

		{"GET /api/workspaces/{id}/traffic", s.handleListTraffic, mcpAllow, cpRead},
		{"GET /api/workspaces/{id}/traffic/poll", s.handlePollTraffic, mcpAllow, cpRead},
		// A destructive verb over a table the workspace layer does not
		// hold — the precedent every later member of cpNeverTouchesLayer
		// (reset-data among them) is argued from.
		{"DELETE /api/workspaces/{id}/traffic", s.handleDeleteTraffic, mcpAllow, cpNeverTouchesLayer},

		// P6a (decisions.md mocker-p6a-sse D3, D15): the traffic feed over
		// SSE beside the poll it never replaces, and the process-wide
		// streaming health an agent reads (stream_handlers.go). Both GET,
		// so neither joins the CSRF or the auto-checkpoint sets. The stream
		// itself is the one read of the two kept out of MCP reach (D9,
		// D16): an in-process loopback response cannot take a write
		// deadline, and a stream is not a call an MCP tool returns from.
		{"GET /api/workspaces/{id}/traffic/stream", s.handleStreamTraffic, mcpDeny("an in-process loopback response cannot take a write deadline, and a stream is not a call an MCP tool returns from"), cpRead},
		{"GET /api/stream/stats", s.handleStreamStats, mcpAllow, cpRead},

		// P6c (decisions.md mocker-p6c-live-conns D1, D9): the live-connection
		// surface over the MOCK plane's registry (connection_handlers.go) —
		// list, close, push a frame. The two mutating ones join the CSRF
		// set by method and cpNeverTouchesLayer by argument: a close cancels
		// a connection's context and a push queues a frame into its RAM
		// inbox — neither writes a row, bumps revision or touches a layer.
		{"GET /api/workspaces/{id}/connections", s.handleListConnections, mcpAllow, cpRead},
		{"DELETE /api/workspaces/{id}/connections/{cid}", s.handleCloseConnection, mcpAllow, cpNeverTouchesLayer},
		{"POST /api/workspaces/{id}/connections/{cid}/frames", s.handlePushFrame, mcpAllow, cpNeverTouchesLayer},

		// P1c2: custom endpoints (screen 6's CRUD) and the two
		// traffic-to-{override,endpoint} conversions (screen 8). The PUT
		// editor below is A1 (mocker-a-mcp gate document, D4), not P1c2 —
		// endpoint_handlers.go and from_traffic.go own everything below.
		{"GET /api/workspaces/{id}/endpoints", s.handleListEndpoints, mcpAllow, cpRead},
		// P6b (decisions.md mocker-p6b-sse-mock D13): a stream draft's first
		// frames, no row written (endpoint_preview_handlers.go) — the same
		// reasoning as POST .../preview above, one table over. Registered
		// BEFORE the {eid} routes below only for reading order — the mux
		// prefers the literal segment "preview" over a {eid} wildcard on
		// its own.
		{"POST /api/workspaces/{id}/endpoints/preview", s.handlePreviewEndpoint, mcpAllow, cpNeverTouchesLayer},
		{"POST /api/workspaces/{id}/endpoints", s.handleCreateEndpoint, mcpAllow, cpLabelled("новый кастомный endpoint")},
		// A1 (mocker-a-mcp gate document, D4): the PUT editor DESIGN §14 and
		// this file's OWN package doc comment used to defer as "P2" —
		// endpoint_handlers.go's handleUpdateEndpoint owns it, a full
		// replacement of the row's definition, not a partial merge.
		{"PUT /api/workspaces/{id}/endpoints/{eid}", s.handleUpdateEndpoint, mcpAllow, cpLabelled("правка кастомного endpoint'а")},
		{"DELETE /api/workspaces/{id}/endpoints/{eid}", s.handleDeleteEndpoint, mcpAllow, cpLabelled("удаление кастомного endpoint'а")},

		{"POST /api/workspaces/{id}/traffic/{tid}/to-override", s.handleToOverride, mcpAllow, cpLabelled("правка из трафика")},
		{"POST /api/workspaces/{id}/traffic/{tid}/to-endpoint", s.handleToEndpoint, mcpAllow, cpLabelled("endpoint из трафика")},

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
		//
		// Create and rename write a SCENARIO row, not the workspace layer,
		// hence cpAnotherLayer; delete and the activate/deactivate pair
		// bump workspaces.revision and change nothing a snapshot holds,
		// hence cpRevisionOnly.
		{"GET /api/workspaces/{id}/scenarios", s.handleListScenarios, mcpAllow, cpRead},
		{"POST /api/workspaces/{id}/scenarios", s.handleCreateScenario, mcpAllow, cpAnotherLayer},
		{"GET /api/workspaces/{id}/scenarios/{sid}", s.handleGetScenario, mcpAllow, cpRead},
		{"PUT /api/workspaces/{id}/scenarios/{sid}", s.handleRenameScenario, mcpAllow, cpAnotherLayer},
		{"DELETE /api/workspaces/{id}/scenarios/{sid}", s.handleDeleteScenario, mcpAllow, cpRevisionOnly},
		{"POST /api/workspaces/{id}/scenarios/{sid}/activate", s.handleActivateScenario, mcpAllow, cpRevisionOnly},
		{"POST /api/workspaces/{id}/scenarios/deactivate", s.handleDeactivateScenario, mcpAllow, cpRevisionOnly},

		// P2c: history and undo for the workspace layer (DESIGN §12:759-776,
		// §14:838-842 minus reset-data — §0 of the P2c context document).
		// checkpoint_handlers.go owns everything below; [checkpoints.Repo]
		// owns every write these routes cause. DELETE below is P2d's own
		// addition — DESIGN §14:838 names only GET/POST for this
		// collection, so a manual checkpoint could be created and never
		// removed until this slice. The two checkpoint writes take
		// cpAnotherLayer (a checkpoint row is not the workspace layer);
		// rollback and reset-overrides take cpRevisionOnly, and both write
		// their own pre-destructive row for the same instant anyway.
		{"GET /api/workspaces/{id}/checkpoints", s.handleListCheckpoints, mcpAllow, cpRead},
		{"POST /api/workspaces/{id}/checkpoints", s.handleCreateCheckpoint, mcpAllow, cpAnotherLayer},
		{"DELETE /api/workspaces/{id}/checkpoints/{cid}", s.handleDeleteCheckpoint, mcpAllow, cpAnotherLayer},
		{"POST /api/workspaces/{id}/rollback/{cid}", s.handleRollbackWorkspace, mcpAllow, cpRevisionOnly},
		{"POST /api/workspaces/{id}/reset-overrides", s.handleResetOverrides, mcpAllow, cpRevisionOnly},

		// P3b (D3): the RESET half of P3's carve-out list — reset-data
		// lives entirely inside internal/resources (resource_handlers.go's
		// own doc comment on handleResetData says why) and takes
		// cpNeverTouchesLayer, not a label: it changes ONLY entities, a
		// layer config_snap does not carry, so a label here would promise
		// an undo this build cannot perform — the identical reasoning
		// resource-decisions' own comment above already gives. The exact
		// precedent is DELETE /api/workspaces/{id}/traffic: a destructive
		// verb over a table the workspace layer does not hold. resources
		// and resource_decisions ARE configuration and reset-data touches
		// neither (D3 R12).
		{"POST /api/workspaces/{id}/reset-data", s.handleResetData, mcpAllow, cpNeverTouchesLayer},
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
// (P2d §4): per-route data (rt.checkpoint.label) only exists in this loop,
// and building the mux here rather than inline in [Server.Handler] is
// exactly what makes the wrap apply to [Server.CallAsMCP] too — both share
// this SAME mux, so an MCP-origin PUT .../operations/{opKey} leaves an auto
// checkpoint behind exactly like a browser-origin one does.
// cfg.CheckpointDebounce is read once, here, rather than per request inside
// the wrapper: a window of 0 means the wrapper is not installed AT ALL, not
// installed-and-always-a-no-op — the distinction
// TestAutoCheckpointWrapper_disabledWhenWindowIsZero (autocheckpoint_test.go)
// exists to pin.
//
// The label comes off the ROW now, not out of a map keyed by the row's own
// pattern: same lookup, one fewer copy of the route table to keep in step.
func (s *Server) routeMux() *http.ServeMux {
	s.routeMuxOnce.Do(func() {
		mux := http.NewServeMux()
		for _, rt := range s.routes() {
			handler := rt.handler
			if rt.checkpoint.label != "" && s.cfg.CheckpointDebounce > 0 {
				handler = s.withAutoCheckpoint(rt.checkpoint.label, handler)
			}
			handler = s.withManagedWorkspaceGuard(rt.pattern, handler)
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
