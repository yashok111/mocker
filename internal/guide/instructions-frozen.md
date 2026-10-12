mocker supports OpenAPI mocks, API design and saved sequences. A WORKSPACE serves mocks and records traffic.

`tools/list` is a summary; call `describe_tool {name}` for a tool's full description and argument schema before first using one with non-obvious arguments.

Read `get_guide {topic:"overview"}`. Mocks use `get_server_config` → `list_workspaces` → `get_workspace` → `find_operations`; API design/sequences read topic design.

Response layers: Spec (never mutated) → Workspace (overrides `set_operation_variant`, custom endpoints `create_endpoint`, settings — persisted, checkpointed) → Scenario (a named snapshot) → Session (`set_session_directive`: force a status, fail N times, delay, pause — RAM only, never versioned). A confirmed resource family (`decide_resource`) makes GET/POST/DELETE on `/things` and `/things/{id}` read and write real rows; `set_resource_entity` writes one from your side.

Rules: whole-object writes REPLACE (resend everything); `editVersion` is compare-and-swap from the matching read; stale writes return `conflict`; `confirmSlug` is the workspace's exact slug, read live, on every destructive tool; `opKey` is already percent-encoded, pass it back verbatim; session directives never bump `revision`, so a forgotten one is the usual "the mock is broken". Verify on the mock plane (`probe_workspace`, then `list_traffic`), not by re-reading configuration. No spec yet: `import_spec {name, document}` takes the file's text, JSON or YAML. Workspaces travel as one document: `export_workspace` → `import_workspace` (spec found by hash or inlined with `includeSpec`), or `fork_workspace` to copy within one installation. Classic API design uses `export_openapi`.

For analyst API authoring use `list_api_designs` → `get_api_design` → `save_api_design_draft` with the exact `expectedVersion`. `request_api_design_review` returns a human review link; only the analyst UI can publish. Managed workspaces reject legacy mutation tools.

Sequences: `list_design_scenarios` → `get_design_scenario`. `run_design_scenario {scenarioId,revisionId,runId,variables?,name?}` starts a complete saved sequence asynchronously; poll `get_design_scenario_run` to a terminal status and inspect failed steps. Start is not success; variables do not edit the document. Use fresh runId per experiment, but after a lost response read that ID before retrying. `list_design_scenario_runs`/`cancel_design_scenario_run` manage persisted results.
