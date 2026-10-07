mocker supports OpenAPI mocks, API design, saved sequences and backend projects. A WORKSPACE serves mocks and records traffic.

`tools/list` is a summary; call `describe_tool {name}` for a tool's full description and argument schema before first using one with non-obvious arguments.

Read `get_guide {topic:"overview"}`. Mocks use `get_server_config` → `list_workspaces` → `get_workspace` → `find_operations`; API design/sequences read topic design. Backend tasks call `get_backend_capabilities`, verify requirements and fetch `get_guide {topic:selectedWorkflow.entrypoint,guideSetId:selectedWorkflow.guideSetId}`. Verify every topic's owner/contentHash in that set. Project prep uses backend-overview, source tasks backend-import, database inspection backend-database, flow/lineage/saved editor inspection backend-inspect with backend-editor-projections. Without a compatible workflow, read only. Comparisons pin revisions. Never execute inspected SQL/source. Saved projections preserve exact pins; EventModel is no runtime proof. Static jobs: change5/backend-analysis-jobs. B/O/N rebase: change5/backend-change-rebase. Ready binds an exact complete saved-draft impact report and acknowledges its gaps; runtime remains unverified. Live collection remains unavailable.

Response layers: Spec (never mutated) → Workspace (overrides `set_operation_variant`, custom endpoints `create_endpoint`, settings — persisted, checkpointed) → Scenario (a named snapshot) → Session (`set_session_directive`: force a status, fail N times, delay, pause — RAM only, never versioned). A confirmed resource family (`decide_resource`) makes GET/POST/DELETE on `/things` and `/things/{id}` read and write real rows; `set_resource_entity` writes one from your side.

Rules: whole-object writes REPLACE (resend everything); `editVersion` is compare-and-swap from the matching read; stale writes return `conflict`; `confirmSlug` is the workspace's exact slug, read live, on every destructive tool; `opKey` is already percent-encoded, pass it back verbatim; session directives never bump `revision`, so a forgotten one is the usual "the mock is broken". Verify on the mock plane (`probe_workspace`, then `list_traffic`), not by re-reading configuration. No spec yet: `import_spec {name, document}` takes the file's text, JSON or YAML. Workspaces travel as one document: `export_workspace` → `import_workspace` (spec found by hash or inlined with `includeSpec`), or `fork_workspace` to copy within one installation. Classic API design uses `export_openapi`.

For analyst API authoring use `list_api_designs` → `get_api_design` → `save_api_design_draft` with the exact `expectedVersion`. `request_api_design_review` returns a human review link; only the analyst UI can publish. Managed workspaces reject legacy mutation tools.

Sequences: `list_design_scenarios` → `get_design_scenario`. `run_design_scenario {scenarioId,revisionId,runId,variables?,name?}` starts a complete saved sequence asynchronously; poll `get_design_scenario_run` to a terminal status and inspect failed steps. Start is not success; variables do not edit the document. Use fresh runId per experiment, but after a lost response read that ID before retrying. `list_design_scenario_runs`/`cancel_design_scenario_run` manage persisted results.

Events: inspect13/backend-events. Sources: import8/backend-import or sync2/backend-sync. Edits: change5/backend-change-proposals; handoff change5/backend-change-handoff; endpoint review change5/backend-endpoint-review.

C4: inspect13/backend-architecture. Interactions: inspect13/backend-interactions. Build never saves/runs. Pins never advance; static claims are not runtime proof.

Portable transfer: project2/backend-portable; require backend-portable and artifact-context-v3. Preview before atomic new-project Commit; foreign refs never resolve by numeric ID.

Measured scenarios use verify4/backend-measurements and backend-benchmarks. Exact pins only; no latest, predicted proposal cost or population rates from biased samples.
