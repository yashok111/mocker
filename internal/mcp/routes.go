package mcp

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
)

// toolRoutes is the TOOL-TO-ROUTE EDGE: every tool this package publishes,
// mapped to the admin route TEMPLATES it calls. It is the second statement
// of the seam admin.mcpAllowedRoutes is the first statement of, and
// routes_test.go asserts the two describe the same set in both directions —
// a template here that the allowlist does not carry is a 404 that surfaces
// only in production (this package's own tests drive a scriptedCaller fake
// that never consults the allowlist), and an allowlist entry no tool names
// is reach nobody asked for.
//
// The value is a LIST because the mapping is not one-to-one, and five of the
// existing tools prove it: list_specs reaches /api/specs and then
// /api/specs/{id}/report to add the import-report counts; get_operation and
// set_operation_response both read before they act; list_traffic pages
// .../traffic and fetches one row through .../traffic/poll;
// set_session_directive POSTs a directive and DELETEs the whole set.
//
// The six tools D6 of the mocker-a-mcp gate document names carry
// "GET /api/workspaces/{id}" on top of their own route: that is
// confirmWorkspaceSlug (confirm.go) reading the slug their confirmation
// argument is checked against, not an accident of the table. Two more
// tools — decide_resource and reset_resource_data (D7 of
// mocker-p3b-resources) — carry the identical confirmSlug argument without
// this extra route: their own admin routes already check it against the
// live workspace, so eight tools require confirmSlug in total, six of them
// through this shared client-side read.
var toolRoutes = map[string][]string{
	// Reads.
	"list_workspaces":       {"GET /api/workspaces"},
	"get_workspace":         {"GET /api/workspaces/{id}"},
	"list_specs":            {"GET /api/specs", "GET /api/specs/{id}/report"},
	"get_spec":              {"GET /api/specs/{id}"},
	"list_spec_operations":  {"GET /api/specs/{id}/operations"},
	"find_operations":       {"GET /api/workspaces/{id}/operations"},
	"get_operation":         {"GET /api/workspaces/{id}/operations", "GET /api/workspaces/{id}/operations/{opKey}"},
	"get_auth_preset":       {"GET /api/workspaces/{id}/auth-preset"},
	"get_session_directive": {"GET /api/workspaces/{id}/session"},
	"list_traffic":          {"GET /api/workspaces/{id}/traffic", "GET /api/workspaces/{id}/traffic/poll"},
	"list_endpoints":        {"GET /api/workspaces/{id}/endpoints"},
	"list_scenarios":        {"GET /api/workspaces/{id}/scenarios"},
	"get_scenario":          {"GET /api/workspaces/{id}/scenarios/{sid}"},
	"list_checkpoints":      {"GET /api/workspaces/{id}/checkpoints"},

	// Workspace and operation edits.
	"create_workspace":          {"POST /api/workspaces"},
	"update_workspace_settings": {"PATCH /api/workspaces/{id}"},
	"apply_auth_preset":         {"POST /api/workspaces/{id}/auth-preset"},
	"set_operation_response":    {"GET /api/workspaces/{id}/operations/{opKey}", "PUT /api/workspaces/{id}/operations/{opKey}"},
	"set_operation_variant":     {"PUT /api/workspaces/{id}/operations/{opKey}"},
	"reset_operation":           {"DELETE /api/workspaces/{id}/operations/{opKey}"},
	"preview_operation":         {"POST /api/workspaces/{id}/preview"},
	"set_session_directive":     {"POST /api/workspaces/{id}/session", "DELETE /api/workspaces/{id}/session", "GET /api/workspaces/{id}/session"},

	// Custom endpoints and traffic conversions.
	"create_endpoint":       {"POST /api/workspaces/{id}/endpoints"},
	"update_endpoint":       {"PUT /api/workspaces/{id}/endpoints/{eid}"},
	"delete_endpoint":       {"DELETE /api/workspaces/{id}/endpoints/{eid}"},
	"override_from_traffic": {"POST /api/workspaces/{id}/traffic/{tid}/to-override"},
	"endpoint_from_traffic": {"POST /api/workspaces/{id}/traffic/{tid}/to-endpoint"},

	// Scenarios.
	"create_scenario":     {"POST /api/workspaces/{id}/scenarios"},
	"rename_scenario":     {"PUT /api/workspaces/{id}/scenarios/{sid}"},
	"activate_scenario":   {"POST /api/workspaces/{id}/scenarios/{sid}/activate"},
	"deactivate_scenario": {"POST /api/workspaces/{id}/scenarios/deactivate"},

	// History.
	"create_checkpoint": {"POST /api/workspaces/{id}/checkpoints"},

	// The six D6 tools: their own route, plus the read
	// confirmWorkspaceSlug does before any of them is allowed to proceed.
	"delete_workspace":   {"GET /api/workspaces/{id}", "DELETE /api/workspaces/{id}"},
	"clear_traffic":      {"GET /api/workspaces/{id}", "DELETE /api/workspaces/{id}/traffic"},
	"delete_scenario":    {"GET /api/workspaces/{id}", "DELETE /api/workspaces/{id}/scenarios/{sid}"},
	"delete_checkpoint":  {"GET /api/workspaces/{id}", "DELETE /api/workspaces/{id}/checkpoints/{cid}"},
	"rollback_workspace": {"GET /api/workspaces/{id}", "POST /api/workspaces/{id}/rollback/{cid}"},
	"reset_overrides":    {"GET /api/workspaces/{id}", "POST /api/workspaces/{id}/reset-overrides"},

	// P3b's resource group (decisions.md mocker-p3b-resources D7): three
	// backfills of routes P3a shipped without a tool, plus reset_resource_
	// data's own new route. Neither decide_resource nor reset_resource_data
	// carries the extra "GET /api/workspaces/{id}" the six D6 tools above
	// do — both admin routes they wrap already check confirmSlug against
	// the live workspace themselves, inside their own transaction, so
	// neither tool calls confirmWorkspaceSlug (tools_resources.go's own
	// package doc comment says why).
	"list_resource_suggestions": {"GET /api/specs/{id}/resource-suggestions"},
	"list_resources":            {"GET /api/workspaces/{id}/resources"},
	"decide_resource":           {"POST /api/workspaces/{id}/resource-decisions"},
	"reset_resource_data":       {"POST /api/workspaces/{id}/reset-data"},

	// A6 (decisions.md mocker-a6-assets D8): the three asset tools, each
	// over exactly its own route — delete_asset's confirmSlug travels in
	// the body and is checked by the route's own transaction.
	"upload_asset": {"PUT /api/workspaces/{id}/assets/{name}"},
	"list_assets":  {"GET /api/workspaces/{id}/assets"},
	"delete_asset": {"DELETE /api/workspaces/{id}/assets/{name}"},

	// P3f (decisions.md mocker-p3f-rederive, D8.3): the spec-scoped rederive
	// verb's own adapter. No confirmSlug and no extra "GET
	// /api/workspaces/{id}" — the route it wraps resolves no workspace at
	// all (D7.1), so there is nothing to confirm.
	"rederive_suggestions": {"POST /api/specs/{id}/rederive"},

	// P4a (decisions.md mocker-p4a-triage, D7): the slice's one route,
	// read-only, no confirmSlug and no extra "GET /api/workspaces/{id}" —
	// the route it wraps writes no workspace row.
	"get_workspace_drift": {"GET /api/workspaces/{id}/drift"},

	// A4 (decisions.md mocker-a4-mcp-reach): the slice's three wire changes
	// (D1). probe_workspace wraps an EXISTING route newly added to
	// mcpAllowedRoutes (D5, D9) — no confirmSlug, nothing destroyed.
	// list_traffic's own entry above is unchanged: D6 widens that tool's
	// behavior (an optional since, a returned lastId) without adding a
	// route or a tool. list_resource_entities wraps the one NEW route this
	// slice adds (D4) — read-only, no confirmSlug, agent-only by policy
	// exactly like get_workspace_drift above.
	"probe_workspace":        {"POST /api/workspaces/{id}/probe"},
	"list_resource_entities": {"GET /api/workspaces/{id}/resources/{family}/entities"},

	// P6a (decisions.md mocker-p6a-sse D16): the slice's one tool, over
	// its one agent-only route — read-only, process-wide, no confirmSlug.
	"get_stream_stats": {"GET /api/stream/stats"},

	// P6b (decisions.md mocker-p6b-sse-mock D13): the slice's one tool,
	// over its one route — a stream draft's first frames, nothing written.
	"preview_endpoint": {"POST /api/workspaces/{id}/endpoints/preview"},

	// P6c (decisions.md mocker-p6c-live-conns D1, D9): the slice's three
	// tools over its three routes — the live-connection surface of the
	// mock plane's SSE endpoints. No confirmSlug on any: a close destroys a
	// connection, never workspace data; a push destroys nothing.
	"list_stream_connections": {"GET /api/workspaces/{id}/connections"},
	"close_stream_connection": {"DELETE /api/workspaces/{id}/connections/{cid}"},
	"push_stream_frame":       {"POST /api/workspaces/{id}/connections/{cid}/frames"},
	// The guide (A7) calls nothing: an EMPTY row, so the population test
	// counts it and the allowlist test has nothing to check for it.
	"get_guide": {},
	// A8: the first admin route mocker-a4-mcp-reach D3 kept OUT of reach,
	// let in on the owner's own word (tools_specs.go).
	"import_spec": {"POST /api/specs"},
	// A9: the process's own limits, read from *config.Config — no route.
	"get_server_config": {},
	// A11: the entity read's two write siblings.
	"set_resource_entity":    {"PUT /api/workspaces/{id}/resources/{family}/entities/{key}"},
	"delete_resource_entity": {"DELETE /api/workspaces/{id}/resources/{family}/entities/{key}"},
	// P4b: export, import and fork.
	"export_workspace": {"GET /api/workspaces/{id}/export"},

	// P7a: the workspace as one OpenAPI document (DESIGN §34.4).
	"export_openapi":   {"GET /api/workspaces/{id}/openapi.json"},
	"import_workspace": {"POST /api/workspaces/import"},
	"fork_workspace":   {"POST /api/workspaces/{id}/fork"},
}

// toolCount is what toolRoutes is expected to hold: the nine tools that
// shipped with the MCP endpoint, plus the twenty-nine slice A2 adds, plus
// the four slice P3b adds (D7), plus the one slice P3f adds (D8.3), plus
// the one slice P4a adds (D7), plus the two slice A4 adds (D1, D9:
// probe_workspace and list_resource_entities — D6's list_traffic widening
// adds no tool of its own), plus the one slice P6a adds (D16:
// get_stream_stats), plus the one slice P6b adds (D13: preview_endpoint),
// plus the three slice P6c adds (D9: list_stream_connections,
// close_stream_connection, push_stream_frame), plus the three slice A6 adds
// (D8: upload_asset, list_assets, delete_asset), plus the one slice A7 adds
// (get_guide, the guide over internal/guide — no route at all), plus the
// one slice A8 adds (import_spec), plus the one slice A9 adds
// (get_server_config — no route at all), plus the two slice A11 adds
// (set_resource_entity, delete_resource_entity), plus the three slice P4b
// adds (export_workspace, import_workspace, fork_workspace), plus the one
// slice P7a adds (export_openapi)
// — 9 + 29 + 4 + 1 + 1 + 2 + 1 + 1 + 3 + 3 + 1 + 1 + 1 + 2 + 3 + 1 = 63. Pinned in routes_test.go so
// a tool added without an entry here is caught by a test rather than by a
// 404 in production.
const toolCount = 63

// toolPath resolves ONE call a tool makes into the (method, path) pair
// loopback.do/call take.
//
// It is keyed by the tool's OWN name and the FULL route template — not by
// the method alone, which does not identify a route: get_operation,
// list_specs and list_traffic each call two routes with the SAME method, so
// a (tool, method) key would be ambiguous for a quarter of the surface. The
// template at the call site is what makes this drift-proof in both
// directions: it is the same string toolRoutes carries and the same string
// admin.mcpAllowedRoutes carries, so a route a tool actually calls is
// spelled where a reader (and routes_test.go) can see it.
//
// params fill the template's {placeholders} LEFT TO RIGHT. Anything wrong —
// an unknown tool, a template that tool does not declare, the wrong number
// of params, a param of a type this cannot render — PANICS, and that is
// deliberate: every one of those is a fixed property of a call site, not of
// a client's arguments, so it is wrong on every call or on none. This
// package already takes that trade at startup for the same class of defect
// (see registerTools on sdk.AddTool's schema panic). The alternative —
// returning an empty path — is a 404 in production that no test catches.
//
// A query string is the CALLER's to append to the returned path
// (path+"?all=1"): the allowlist matches the mux pattern and mux matching
// ignores the query, so a query needs no template of its own.
func toolPath(tool, template string, params ...any) (method, path string) {
	routes, ok := toolRoutes[tool]
	if !ok {
		panic(fmt.Sprintf("mcp: toolPath: tool %q has no entry in toolRoutes", tool))
	}
	if !slices.Contains(routes, template) {
		panic(fmt.Sprintf("mcp: toolPath: tool %q does not declare route %q; it declares %v", tool, template, routes))
	}
	method, tmpl, ok := strings.Cut(template, " ")
	if !ok {
		panic(fmt.Sprintf("mcp: toolPath: route %q is not \"METHOD /path\"", template))
	}
	return method, fillTemplate(tool, template, tmpl, params)
}

// fillTemplate substitutes params into a route template's {placeholders}.
// Split out of toolPath so the substitution rule — and the opKey trap below
// — reads as one thing.
func fillTemplate(tool, template, tmpl string, params []any) string {
	var b strings.Builder
	rest := tmpl
	used := 0
	for {
		open := strings.IndexByte(rest, '{')
		if open < 0 {
			b.WriteString(rest)
			break
		}
		closing := strings.IndexByte(rest[open:], '}')
		if closing < 0 {
			panic(fmt.Sprintf("mcp: toolPath: unterminated placeholder in route %q", template))
		}
		b.WriteString(rest[:open])
		if used >= len(params) {
			panic(fmt.Sprintf("mcp: toolPath: tool %q route %q wants more path params than the %d given", tool, template, len(params)))
		}
		b.WriteString(renderParam(tool, template, params[used]))
		used++
		rest = rest[open+closing+1:]
	}
	if used != len(params) {
		panic(fmt.Sprintf("mcp: toolPath: tool %q route %q takes %d path param(s), got %d", tool, template, used, len(params)))
	}
	return b.String()
}

// renderParam turns one path param into its path segment.
//
// A string is substituted RAW — never url.PathEscape'd — and the reason is
// opKey. Every opKey this package hands around came out of the admin plane's
// mergedOperationView.OpKey, which is ALREADY percent-encoded
// (overrides.OpKey, internal/overrides/opkey.go:27) so that a "METHOD /path"
// fits in one path segment. url.PathEscape is not idempotent on input that
// already contains a literal "%": a second pass produces a string the admin
// handler's own url.PathEscape(r.PathValue("opKey")) round trip
// (override_handlers.go:62) no longer matches, yielding a 400 or a lookup
// against the wrong operation. Ids are rendered as decimal digits, which
// need no escaping either.
func renderParam(tool, template string, p any) string {
	switch v := p.(type) {
	case string:
		return v
	case int64:
		return strconv.FormatInt(v, 10)
	case int:
		return strconv.Itoa(v)
	default:
		panic(fmt.Sprintf("mcp: toolPath: tool %q route %q got a path param of type %T; want string, int or int64", tool, template, p))
	}
}
