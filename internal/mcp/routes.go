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
// noRoute is the value of a toolRoutes row for a tool that calls NO admin
// route at all — get_guide (A7, tools_guide.go), get_server_config (A9,
// tools_config.go) and describe_tool (review 2026-10-06, F23,
// tools_describe.go), and nothing else today.
//
// It is one named sentinel because the fact it states was previously
// documented in three places and written in none: two bare `{}` literals in
// the table below, plus a paragraph in each of those two files explaining
// that an empty row means "reaches no handler, so there is nothing for
// admin's allowlist to carry and nothing for CallAsMCP to refuse". A bare
// `{}` reads equally well as "nobody filled this in yet", which is the row
// TestToolRoutesAgreeWithAdminAllowlist cannot tell apart from a deliberate
// one. Named, it can be searched for, and the two files now point here
// instead of restating it.
//
// Empty and not nil so the distinction stays visible to a reader; both
// behave identically in every loop over a row, and nothing appends to a row.
var noRoute = []string{}

var toolRoutes = map[string][]string{
	"list_backend_import_summaries":       {"GET /api/backend-projects/{id}/import-summaries"},
	"query_backend_explore":               {"POST /api/backend-projects/{id}/explore/query"},
	"list_backend_materializations":       {"GET /api/backend-projects/{id}/materializations"},
	"get_backend_materialization":         {"GET /api/backend-projects/{id}/materializations/{mid}"},
	"import_backend_observations":         {"POST /api/backend-projects/{id}/observations"},
	"list_backend_observations":           {"GET /api/backend-projects/{id}/observations"},
	"get_backend_observation_version":     {"GET /api/backend-projects/{id}/observations/{sid}/versions/{v}"},
	"get_backend_observation_records":     {"GET /api/backend-projects/{id}/observations/{sid}/versions/{v}/records"},
	"adapt_backend_observations":          {"POST /api/backend-projects/{id}/observations/adapt"},
	"correlate_backend_observations":      {"POST /api/backend-projects/{id}/observations/{sid}/correlations"},
	"get_backend_observation_correlation": {"GET /api/backend-projects/{id}/observations/{sid}/correlations/{v}"},

	"resolve_backend_diagram_scope":            {"POST /api/backend-projects/{id}/diagrams/resolve-scope"},
	"list_backend_replay_targets":              {"GET /api/backend-projects/{id}/replay/targets"},
	"get_backend_replay_template":              {"GET /api/backend-projects/{id}/replay/template"},
	"connect_backend_replay_profile":           {"POST /api/backend-projects/{id}/replay/profiles"},
	"list_backend_replay_profiles":             {"GET /api/backend-projects/{id}/replay/profiles"},
	"revoke_backend_replay_authorization":      {"POST /api/backend-projects/{id}/replay/revoke"},
	"save_backend_replay_package":              {"POST /api/backend-projects/{id}/replay/packages"},
	"list_backend_replay_packages":             {"GET /api/backend-projects/{id}/replay/packages"},
	"get_backend_replay_profile":               {"GET /api/backend-projects/{id}/replay/profiles/{item}/versions/{v}"},
	"get_backend_replay_package":               {"GET /api/backend-projects/{id}/replay/packages/{item}/versions/{v}"},
	"start_backend_replay":                     {"POST /api/backend-projects/{id}/replay/runs"},
	"list_backend_replay_runs":                 {"GET /api/backend-projects/{id}/replay/runs"},
	"get_backend_replay_run":                   {"GET /api/backend-projects/{id}/replay/runs/{rid}"},
	"cancel_backend_replay_run":                {"POST /api/backend-projects/{id}/replay/runs/{rid}/cancel"},
	"reconcile_backend_replay_run":             {"POST /api/backend-projects/{id}/replay/runs/{rid}/reconcile"},
	"compare_backend_replay_runs":              {"POST /api/backend-projects/{id}/replay/compare"},
	"start_backend_analysis":                   {"POST /api/backend-projects/{id}/analyses"},
	"list_backend_analysis":                    {"GET /api/backend-projects/{id}/analyses"},
	"get_backend_analysis":                     {"GET /api/backend-projects/{id}/analyses/{aid}"},
	"cancel_backend_analysis":                  {"POST /api/backend-projects/{id}/analyses/{aid}/cancel"},
	"retry_backend_analysis":                   {"POST /api/backend-projects/{id}/analyses/{aid}/retry"},
	"get_backend_analysis_results":             {"GET /api/backend-projects/{id}/analyses/{aid}/results"},
	"preview_backend_change_proposal_rebase":   {"POST /api/backend-projects/{id}/change-proposals/{pid}/rebase-preview"},
	"apply_backend_change_proposal_rebase":     {"POST /api/backend-projects/{id}/change-proposals/{pid}/rebase"},
	"apply_backend_change_proposal_lifecycle":  {"POST /api/backend-projects/{id}/change-proposals/{pid}/lifecycle"},
	"list_backend_change_proposals":            {"GET /api/backend-projects/{id}/change-proposals"},
	"create_backend_change_proposal":           {"POST /api/backend-projects/{id}/change-proposals"},
	"get_backend_change_proposal":              {"GET /api/backend-projects/{id}/change-proposals/{pid}"},
	"preview_backend_change_proposal_commands": {"POST /api/backend-projects/{id}/change-proposals/{pid}/preview"},
	"apply_backend_change_proposal_commands":   {"POST /api/backend-projects/{id}/change-proposals/{pid}/commands"},
	"restore_backend_change_proposal":          {"POST /api/backend-projects/{id}/change-proposals/{pid}/restore"},
	"get_backend_assertions":                   {"GET /api/backend-projects/{id}/revisions/{rid}/assertions", "GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/assertions", "GET /api/backend-projects/{id}/imports/{iid}/candidate/assertions"},
	"get_workspace_proxy":                      {"GET /api/workspaces/{id}/proxy"},
	"set_workspace_proxy":                      {"PUT /api/workspaces/{id}/proxy"},
	"list_proxy_recordings":                    {"GET /api/workspaces/{id}/proxy/recordings"},
	"delete_proxy_recording":                   {"DELETE /api/workspaces/{id}/proxy/recordings/{rid}"},
	"clear_proxy_recordings":                   {"POST /api/workspaces/{id}/proxy/recordings/clear"},
	"query_backend_api_artifacts":              {"POST /api/backend-projects/{id}/api-artifacts/query"},
	"preview_backend_api_pins":                 {"POST /api/backend-projects/{id}/api-artifacts/preview"},
	"apply_backend_api_pins":                   {"POST /api/backend-projects/{id}/api-artifacts/commands"},
	"get_api_artifact_snapshot":                {"GET /api/designs/{id}/revisions/{rid}/artifact-snapshot"},
	"build_backend_interactions":               {"POST /api/backend-projects/{id}/diagrams/interactions/build"},
	"preview_backend_architecture":             {"POST /api/backend-projects/{id}/diagrams/architecture/preview"},
	"build_backend_lifecycle":                  {"POST /api/backend-projects/{id}/diagrams/lifecycle/build"},
	"create_backend_diagram":                   {"POST /api/backend-projects/{id}/diagrams"},
	"save_backend_diagram":                     {"POST /api/backend-projects/{id}/diagrams/{did}/save"},
	"fork_backend_diagram":                     {"POST /api/backend-projects/{id}/diagrams/fork"},
	"query_backend_namespaced_artifact":        {"POST /api/backend-projects/{id}/artifacts/namespaced/query"},
	"resolve_backend_portable_selection":       {"POST /api/backend-projects/{id}/portable/selection"},
	"export_backend_project":                   {"POST /api/backend-projects/{id}/portable/export"},
	"get_backend_export_chunk":                 {"GET /api/backend-projects/portable/exports/{sid}/chunks/{index}"},
	"begin_backend_portable_import":            {"POST /api/backend-projects/portable/imports"},
	"put_backend_portable_import_chunk":        {"POST /api/backend-projects/portable/imports/{sid}/chunks"},
	"preview_backend_portable_import":          {"POST /api/backend-projects/portable/imports/{sid}/preview"},
	"commit_backend_portable_import":           {"POST /api/backend-projects/portable/imports/{sid}/commit"},
	"abort_backend_portable_import":            {"POST /api/backend-projects/portable/imports/{sid}/abort"},
	"export_backend_view_svg":                  {"GET /api/backend-projects/{id}/diagram-views/{vid}/versions/{v}/svg"},
	"preview_backend_materialization":          {"POST /api/backend-projects/{id}/materializations/preview"},
	"apply_backend_materialization":            {"POST /api/backend-projects/{id}/materializations/apply"},
	"list_backend_findings":                    {"GET /api/backend-projects/{id}/findings"},
	"review_backend_finding":                   {"PUT /api/backend-projects/{id}/findings/{fingerprint}/review"},
	"list_backend_diagrams":                    {"GET /api/backend-projects/{id}/diagrams"},
	"get_backend_diagram":                      {"GET /api/backend-projects/{id}/diagrams/{did}/versions/{v}"},
	"query_backend_diagram":                    {"POST /api/backend-projects/{id}/diagrams/query"},
	"compare_backend_diagrams":                 {"POST /api/backend-projects/{id}/diagrams/compare"},
	"create_backend_diagram_view":              {"POST /api/backend-projects/{id}/diagram-views"},
	"save_backend_diagram_view":                {"POST /api/backend-projects/{id}/diagram-views/{vid}/save"},
	"list_backend_diagram_views":               {"GET /api/backend-projects/{id}/diagram-views"},
	"get_backend_diagram_view":                 {"GET /api/backend-projects/{id}/diagram-views/{vid}/versions/{v}"},
	"list_backend_saved_views":                 {"GET /api/backend-projects/{id}/saved-views"},
	"create_backend_saved_view":                {"POST /api/backend-projects/{id}/saved-views"},
	"get_backend_saved_view":                   {"GET /api/backend-projects/{id}/saved-views/{vid}"},
	"save_backend_saved_view":                  {"POST /api/backend-projects/{id}/saved-views/{vid}/save"},
	"compare_backend_revisions":                {"POST /api/backend-projects/{id}/revisions/compare"},
	"get_backend_import_changes":               {"GET /api/backend-projects/{id}/imports/{iid}/changes"},
	"begin_backend_import":                     {"POST /api/backend-projects/{id}/imports"},
	"list_backend_imports":                     {"GET /api/backend-projects/{id}/imports"},
	"get_backend_import":                       {"GET /api/backend-projects/{id}/imports/{iid}"},
	"put_backend_import_batch":                 {"PUT /api/backend-projects/{id}/imports/{iid}/batches/{bid}"},
	"preview_backend_import":                   {"POST /api/backend-projects/{id}/imports/{iid}/preview"},
	"plan_backend_import":                      {"POST /api/backend-projects/{id}/imports/plan"},
	"get_backend_storage_usage":                {"GET /api/backend-projects/{id}/storage/usage"},
	"get_backend_import_schema":                {"GET /api/backend-projects/import-schema"},
	"query_backend_coverage":                   {"POST /api/backend-projects/{id}/coverage/query"},
	"validate_backend_import_batch":            {"POST /api/backend-projects/{id}/imports/{iid}/validate"},
	"commit_backend_import":                    {"POST /api/backend-projects/{id}/imports/{iid}/commit"},
	"abort_backend_import":                     {"POST /api/backend-projects/{id}/imports/{iid}/abort"},
	"query_backend_database":                   {"POST /api/backend-projects/{id}/database/query"},
	"query_backend_events":                     {"POST /api/backend-projects/{id}/events/query"},
	"query_backend_lineage":                    {"POST /api/backend-projects/{id}/lineage/query"},
	"query_backend_flow":                       {"POST /api/backend-projects/{id}/flow/query"},
	"query_backend_graph":                      {"POST /api/backend-projects/{id}/graph/query"},
	"get_backend_node":                         {"GET /api/backend-projects/{id}/revisions/{rid}/nodes/{nid}", "GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/nodes/{nid}", "GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/nodes/{nid}", "GET /api/backend-projects/{id}/imports/{iid}/candidate/nodes/{nid}"},
	"get_backend_evidence":                     {"GET /api/backend-projects/{id}/revisions/{rid}/evidence", "GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/evidence", "GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/evidence", "GET /api/backend-projects/{id}/imports/{iid}/candidate/evidence"},
	"get_backend_coverage":                     {"GET /api/backend-projects/{id}/revisions/{rid}/coverage", "GET /api/backend-projects/{id}/proposals/{pid}/revisions/{prid}/coverage", "GET /api/backend-projects/{id}/change-proposals/{pid}/revisions/{prid}/coverage", "GET /api/backend-projects/{id}/imports/{iid}/candidate/coverage"},
	"list_backend_proposals":                   {"GET /api/backend-projects/{id}/proposals"},
	"create_backend_proposal":                  {"POST /api/backend-projects/{id}/proposals"},
	"get_backend_proposal":                     {"GET /api/backend-projects/{id}/proposals/{pid}"},
	"preview_backend_proposal_commands":        {"POST /api/backend-projects/{id}/proposals/{pid}/preview"},
	"apply_backend_proposal_commands":          {"POST /api/backend-projects/{id}/proposals/{pid}/commands"},

	"query_backend_artifacts":               {"POST /api/backend-projects/{id}/artifacts/query"},
	"preview_backend_artifact_pins":         {"POST /api/backend-projects/{id}/artifacts/preview"},
	"apply_backend_artifact_pins":           {"POST /api/backend-projects/{id}/artifacts/commands"},
	"get_design_scenario_artifact_snapshot": {"GET /api/design-scenarios/{id}/revisions/{rid}/artifact-snapshot"},

	"get_backend_capabilities":        {"GET /api/backend-projects/capabilities"},
	"list_backend_projects":           {"GET /api/backend-projects"},
	"create_backend_project":          {"POST /api/backend-projects"},
	"get_backend_project":             {"GET /api/backend-projects/{id}"},
	"apply_backend_project_commands":  {"POST /api/backend-projects/{id}/commands"},
	"list_backend_annotations":        {"GET /api/backend-projects/{id}/annotations"},
	"list_backend_revisions":          {"GET /api/backend-projects/{id}/revisions"},
	"get_backend_revision":            {"GET /api/backend-projects/{id}/revisions/{rid}"},
	"list_response_rules":             {"GET /api/designs/{id}/response-rules"},
	"get_response_rule":               {"GET /api/designs/{id}/response-rules/{rid}"},
	"create_response_rule":            {"POST /api/designs/{id}/response-rules"},
	"save_response_rule":              {"PUT /api/designs/{id}/response-rules/{rid}"},
	"delete_response_rule":            {"DELETE /api/designs/{id}/response-rules/{rid}"},
	"apply_response_rule_commands":    {"POST /api/designs/{id}/response-rules/{rid}/commands"},
	"validate_response_rule":          {"POST /api/designs/{id}/response-rules/{rid}/validate"},
	"simulate_response_rule":          {"POST /api/designs/{id}/response-rules/{rid}/simulate"},
	"get_state_diagram_execution":     {"GET /api/designs/{id}/state-diagram-execution"},
	"apply_state_diagram":             {"PUT /api/designs/{id}/state-diagrams/{did}/execution"},
	"unapply_state_diagram":           {"DELETE /api/designs/{id}/state-diagrams/{did}/execution"},
	"get_response_rule_execution":     {"GET /api/designs/{id}/response-rule-execution"},
	"apply_response_rule":             {"PUT /api/designs/{id}/response-rules/{rid}/execution"},
	"unapply_response_rule":           {"DELETE /api/designs/{id}/response-rules/{rid}/execution"},
	"get_api_resource_map":            {"GET /api/designs/{id}/resource-map"},
	"preview_api_resource_map":        {"POST /api/designs/{id}/resource-map/preview"},
	"apply_api_resource_map_commands": {"POST /api/designs/{id}/resource-map/commands"},
	"upsert_api_resource":             {"POST /api/designs/{id}/resource-map/commands"},
	"remove_api_resource":             {"POST /api/designs/{id}/resource-map/commands"},
	"assign_api_resource_operation":   {"POST /api/designs/{id}/resource-map/commands"},
	"upsert_api_resource_relation":    {"POST /api/designs/{id}/resource-map/commands"},
	"remove_api_resource_relation":    {"POST /api/designs/{id}/resource-map/commands"},
	"move_api_resource":               {"POST /api/designs/{id}/resource-map/commands"},
	"auto_layout_api_resources":       {"POST /api/designs/{id}/resource-map/commands"},
	// Schema model authoring shares one version-fenced command route.
	"get_schema_model":             {"GET /api/designs/{id}/schema-model"},
	"preview_schema_model_changes": {"POST /api/designs/{id}/schema-model/preview"},
	"apply_schema_model_commands":  {"POST /api/designs/{id}/schema-model/commands"},
	"create_api_schema":            {"POST /api/designs/{id}/schema-model/commands"},
	"replace_api_schema":           {"POST /api/designs/{id}/schema-model/commands"},
	"rename_api_schema":            {"POST /api/designs/{id}/schema-model/commands"},
	"delete_api_schema":            {"POST /api/designs/{id}/schema-model/commands"},
	"upsert_api_schema_property":   {"POST /api/designs/{id}/schema-model/commands"},
	"rename_api_schema_property":   {"POST /api/designs/{id}/schema-model/commands"},
	"delete_api_schema_property":   {"POST /api/designs/{id}/schema-model/commands"},
	"set_api_schema_reference":     {"POST /api/designs/{id}/schema-model/commands"},
	"move_api_schema":              {"POST /api/designs/{id}/schema-model/commands"},

	// Persisted sequence-design scenarios. All mutations use the same admin
	// handlers as the UI; validation is a write-shaped read with no side effects.
	"list_design_scenarios":                    {"GET /api/design-scenarios"},
	"create_design_scenario":                   {"POST /api/design-scenarios"},
	"get_design_scenario":                      {"GET /api/design-scenarios/{id}"},
	"save_design_scenario_draft":               {"PUT /api/design-scenarios/{id}/draft"},
	"apply_design_scenario_commands":           {"POST /api/design-scenarios/{id}/commands"},
	"get_design_scenario_revision":             {"GET /api/design-scenarios/{id}/revisions/{rid}"},
	"export_design_scenarios_file":             {"POST /api/design-scenarios/transfer-export"},
	"import_design_scenarios_file":             {"POST /api/design-scenarios/transfer-import"},
	"get_design_scenario_export_options":       {"GET /api/design-scenarios/{id}/revisions/{rid}/export-options"},
	"export_design_scenario":                   {"GET /api/design-scenarios/{id}/revisions/{rid}/exports/{format}"},
	"export_design_scenario_archive":           {"POST /api/design-scenarios/{id}/revisions/{rid}/archive"},
	"get_design_scenario_diff":                 {"GET /api/design-scenarios/{id}/diff"},
	"restore_design_scenario_revision":         {"POST /api/design-scenarios/{id}/restore"},
	"validate_design_scenario":                 {"POST /api/design-scenarios/{id}/validate"},
	"execute_design_scenario_step":             {"POST /api/design-scenarios/{id}/execute-step"},
	"run_design_scenario":                      {"POST /api/design-scenarios/{id}/runs"},
	"list_design_scenario_runs":                {"GET /api/design-scenarios/{id}/runs"},
	"get_design_scenario_data_flow":            {"GET /api/design-scenarios/{id}/data-flow"},
	"analyze_design_scenario_data_flow":        {"POST /api/design-scenarios/{id}/data-flow"},
	"get_design_scenario_event_map":            {"GET /api/design-scenarios/{id}/event-map"},
	"analyze_design_scenario_event_map":        {"POST /api/design-scenarios/{id}/event-map"},
	"apply_design_scenario_event_map_commands": {"POST /api/design-scenarios/{id}/commands"},
	"upsert_design_scenario_data_binding":      {"POST /api/design-scenarios/{id}/commands"},
	"remove_design_scenario_data_binding":      {"POST /api/design-scenarios/{id}/commands"},
	"suggest_design_scenario_tests":            {"GET /api/design-scenarios/{id}/test-suggestions"},
	"get_design_scenario_coverage":             {"GET /api/design-scenarios/{id}/coverage"},
	"set_design_scenario_fragment_execution":   {"POST /api/design-scenarios/{id}/commands"},
	"set_design_scenario_branch_execution":     {"POST /api/design-scenarios/{id}/commands"},
	"get_design_scenario_run":                  {"GET /api/design-scenarios/{id}/runs/{runId}"},
	"cancel_design_scenario_run":               {"POST /api/design-scenarios/{id}/runs/{runId}/cancel"},

	// Versioned API authoring. Human publication is deliberately not MCP-accessible.
	"list_state_diagrams":          {"GET /api/designs/{id}/state-diagrams"},
	"get_state_diagram":            {"GET /api/designs/{id}/state-diagrams/{did}"},
	"create_state_diagram":         {"POST /api/designs/{id}/state-diagrams"},
	"save_state_diagram":           {"PUT /api/designs/{id}/state-diagrams/{did}"},
	"delete_state_diagram":         {"DELETE /api/designs/{id}/state-diagrams/{did}"},
	"apply_state_diagram_commands": {"POST /api/designs/{id}/state-diagrams/{did}/commands"},
	"validate_state_diagram":       {"POST /api/designs/{id}/state-diagrams/{did}/validate"},
	"simulate_state_diagram":       {"POST /api/designs/{id}/state-diagrams/{did}/simulate"},
	"list_api_designs":             {"GET /api/designs"},
	"create_api_design":            {"POST /api/designs"},
	"get_api_design":               {"GET /api/designs/{id}"},
	"save_api_design_draft":        {"PUT /api/designs/{id}/draft"},
	"get_api_design_revision":      {"GET /api/designs/{id}/revisions/{rid}"},
	"get_api_design_diff":          {"GET /api/designs/{id}/diff"},
	"validate_api_design":          {"POST /api/designs/{id}/validate"},
	"analyze_api_design_impact":    {"POST /api/designs/{id}/impact"},
	"create_api_design_change_set": {"POST /api/designs/{id}/change-sets"},
	"close_api_design_change_set":  {"PUT /api/designs/{id}/change-sets/{cid}"},
	"request_api_design_review":    {"POST /api/designs/{id}/reviews"},
	"restore_api_design_revision":  {"POST /api/designs/{id}/restore"},
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
	// The guide (A7) calls nothing: [noRoute], so the population test
	// counts it and the allowlist test has nothing to check for it.
	"get_guide": noRoute,
	// A8: the first admin route mocker-a4-mcp-reach D3 kept OUT of reach,
	// let in on the owner's own word (tools_specs.go).
	"import_spec": {"POST /api/specs"},
	// A9: the process's own limits, read from *config.Config — [noRoute].
	"get_server_config": noRoute,
	// F23: one tool's full description and schemas, read from the server's
	// own registry (tools_describe.go) — [noRoute].
	"describe_tool": noRoute,
	// Validates the registry's import wire schemas; never dispatches a command.
	"diagnose_backend_import_request": noRoute,
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
