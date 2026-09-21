# Designing an API in mocker — drafts, review and publication

## Analyst editor: the recommended workflow

The **API designer** stores a complete OpenAPI document with immutable revisions.
It has two independent mock URLs: a working draft and a stable published version.
An agent edits the draft through MCP; an analyst reviews structural changes and
the Monaco line diff, then confirms publication in the browser. A save never
changes the published mock.

1. `list_api_designs`, then `get_api_design {designId}`. To start a project,
   `create_api_design {name, document}` imports JSON/YAML. Alternatively pass
   `workspaceId` to capture an existing workspace, or neither for an empty API.
   The source workspace is left unchanged. Creation is not idempotent: inspect the
   list after a lost response before creating again.
2. `create_api_design_change_set {designId, expectedVersion, title}` opens a named
   task. Use the design's current `version`; this is separate from a workspace's
   `revision` or an operation's `editVersion`.
3. Read the complete `draft.document`, edit it and preserve every unrelated field.
   `validate_api_design {designId, document}` checks proposed JSON/YAML without
   saving. `save_api_design_draft {designId, expectedVersion, document, summary,
   changeSetId}` atomically saves the full document, history and draft mock.
   This is FULL REPLACEMENT, not a merge. Local references, path parameters and
   operation identifiers must remain valid.
4. On `409 design_conflict`, read the current state and compare both edits before
   retrying. Never just substitute the new version number into an old document:
   that would overwrite the analyst's changes.
5. `get_api_design_diff {designId}` compares the latest publication (the initial
   import before the first publication) to the draft. For historical comparisons,
   supply `fromRevisionId` and `toRevisionId`. Results contain both exact documents
   and changes with JSON pointers. `impact: review` requires human analysis; it
   does not certify compatibility. Call the draft URL to inspect generated responses.
6. `close_api_design_change_set {designId, changeSetId, expectedVersion}` finishes
   the task. `request_api_design_review {designId, expectedVersion, summary}` freezes
   a candidate and returns `reviewUrl`. Give that link to the analyst. New saves
   supersede the candidate, so publication cannot include an unseen later edit.
7. **The analyst publishes in the UI.** The shared MCP key cannot confirm
   publication. `get_api_design` reports reviews/releases and the stable published
   mock URL. Retrying a successful publication returns the same release.
8. `get_api_design_revision {designId, revisionId}` reads an immutable document for
   handover. `restore_api_design_revision {designId, revisionId, expectedVersion,
   summary}` creates a new draft from history; it does not publish or erase history.

Managed draft/published workspaces are runtime projections. Legacy endpoint,
override, spec/settings, scenario, rollback and session-control writes cannot edit
them. Use the designer tools instead. Publication includes the HTTP contract and
generated mock, not entity data, assets, Lua functions or transient session state.
The author document preserves OpenAPI fields; runtime normalization is separate.
Names a client reports for an agent are not verified identities; history records
the server-derived UI/MCP source.

## Sequence canvas: edit, run, inspect, vary

A sequence-canvas scenario stores participants, ordered messages, contract
bindings and execution settings. It has its own immutable revisions and is
separate from the classic workspace snapshot called a scenario.

1. `list_design_scenarios` → `get_design_scenario {scenarioId}`. Read the full
   `draft.document`, `draft.formDrafts`, `scenario.version` and `draft.id`.
   `validate_design_scenario {scenarioId, document}` checks a proposed document
   without saving. Enabled HTTP requests need a linked, current API contract;
   finish form buffers and remove unsupported opt/loop fragments before a run.
2. To edit, use `apply_design_scenario_commands` with the exact `expectedVersion`
   and an ordered command batch, or `save_design_scenario_draft` with the complete
   document and all retained form buffers. Upserts replace complete objects.
   Re-read and reconcile a409; never blindly substitute the newer version.
   Message `execution` configures parameter/header/body templates (`{{name}}`),
   expected HTTP status, JSON-pointer assertions and extracted variables.
3. `run_design_scenario {scenarioId, revisionId, runId, name, variables}` starts
   the saved revision and immediately returns a report. Choose a unique run ID
   (`[A-Za-z0-9_-]{1,100}`) and a useful name for each experiment. `variables` is
   an optional map of string overrides; it does not save a new scenario revision.
4. While `status` is `running`, call `get_design_scenario_run {scenarioId, runId}`
   roughly once per second. **Starting successfully does not mean the scenario
   passed.** Read the terminal `passed`, `failed` or `cancelled` result. Every
   report includes the exact document snapshot and ordered steps with resolved
   requests, responses, assertion results and reasons. Missing JSON values and
   JSON null differ. `expectedJson` and `actualJson` contain serialized JSON so
   large integers remain exact; preserve them as strings when reporting them.
5. On failure, inspect the first failed step, its actual status/body and assertion
   reason. Try another variable set under a NEW run ID, or edit the scenario and
   run its new revision. Extraction happens only after the step's checks pass;
   failure stops later requests. Default expected status is any2xx.
6. If a start response is lost, GET the known run ID. Retrying the same ID and
   same input returns the existing run without repeating requests. A different
   payload with that ID is409. Old pruned report IDs are410 and do not re-execute.
   `list_design_scenario_runs` lists the latest50 reports from both transports.
   `cancel_design_scenario_run` stops an active run; completed effects remain.

The server runs the sequence even when the MCP call returns or its client
disconnects. Four runs may be active globally, one per scenario; runs have a120s
budget and individual requests30s. Runs use linked draft mocks in-process,
with the same contract checks as a single-step probe. External URLs, conditions
and loops are not supported; disabled/descriptive messages are skipped. Runs
use normal mock state and do not isolate or roll back effects.

The UI's execution panel follows new agent runs, shows progress and keeps saved
reports after reload. A manually selected historical report stays selected.
Closing the viewer does not cancel a run started by an agent. The run's `source`
is recorded by the transport (`mcp` or `ui`), not supplied by the caller.

## Classic workspace design (existing workflow)

This is the workflow DESIGN §34 describes: a frontend developer or systems
analyst designs an API here, sees it SERVING while they design it, and
hands the backend team one OpenAPI document. Every tool below already
exists; the only new one is `export_openapi`.

The whole idea in one line: **a design is a base plus a delta**, and
mocker's four layers already are exactly that. The base is a spec you
imported (or nothing at all). The delta is what you author on the
workspace: new operations, changed schemas, examples, removals. The
export merges the two into one document.

## The loop

1. **Take a base, or none.** `import_spec {name, document}` with an
   existing API's file (JSON or YAML), then `create_workspace {name, slug,
   specId}`. With nothing to start from, create the workspace with no
   `specId` at all — the export is then an empty OpenAPI 3.1 skeleton plus
   whatever you write, and generation still works.
2. **Add an operation.** `create_endpoint {workspaceId, method, path,
   status, schema, reqSchema, operation}`. `schema` is an inline JSON
   Schema and the response is GENERATED from it — under the workspace's
   seed, with recipes and `ref` — so the frontend can call the route the
   moment you save it. `operation` carries what a contract needs and a
   mock never did: `{summary, description, tags, operationId, deprecated,
   parameters}`.
3. **Reuse the base's types.** A `$ref` into the bound spec's components
   is allowed inside any of those schemas:
   `{"$ref": "#/components/schemas/User"}`. It must resolve when you write
   it — a pointer the spec does not have is refused
   (`400 schema_ref_unresolved`), and with no spec bound any `$ref` is
   refused, because there is nothing to resolve against.
4. **Change an existing operation.** `set_operation_variant` with a
   `schemaPatch` (add/remove/replace over the resolved response schema)
   changes the shape; a pinned body becomes the operation's example;
   `routeOff` proposes a removal. Do NOT send `schema` on a spec
   operation — it is refused by name (`400 schema_on_override`), because
   that operation already has a schema and `schemaPatch` is how it moves.
5. **Look at it.** `curl` the workspace `url` — that is the design
   running. `list_traffic` shows what it answered.
6. **Export.** `export_openapi {workspaceId}` → one OpenAPI 3.1 document.
   Hand it to the backend team, commit it, open it in any viewer.

## What the export does with each thing you did

| what you did | what the document says |
|---|---|
| custom endpoint at a NEW path | a new operation, with your schemas, parameters and operation fields |
| custom endpoint at a path the base already has (canonically) | that operation REPLACED — one entry, under YOUR spelling |
| `schemaPatch` on an override | the patched schema written INLINE on that response |
| pinned body (override or endpoint) | `examples` on that response |
| `routeOff` | `deprecated: true` — never a deletion |
| endpoint with `overrideOn: false` | nothing: a switched-off row is not a contract |
| `kind: "sse"` | a `GET` answering `text/event-stream` |
| `kind: "ws"` | a `GET` with `x-websocket: true` and a `101` |
| everything else in the workspace | nothing — scenarios, entity rows, assets and session directives are not contract |

`info.version` gets `-draft.<revision>`, so two exports of different
states are distinguishable and an earlier draft suffix is replaced, never
stacked.

## Accepting the design as the next base

The base is never edited in place. When the design is agreed:

1. `export_openapi` → the document.
2. `import_spec {name, document}` → a new spec id.
3. `update_workspace_settings {workspaceId, specId}` → the workspace now serves the
   design AS its base.
4. `get_workspace_drift` → it names every delta row that is now
   redundant: each custom endpoint shadows the operation it became, and an
   override whose operation the export re-spelled is reported orphaned.
5. **Delete those rows** (`delete_endpoint`, `reset_operation`).
   This step is not optional: a `schemaPatch` applied a SECOND time over a
   base that already carries the patched schema fails to apply, and that
   variant then serves unpatched — the design would silently stop matching
   the contract.

After that the workspace is a clean delta over the new base, and the next
round of design starts from step 2.

## Limits of the classic workspace workflow

- **No request validation.** `reqSchema` is exported as `requestBody` and
  is never enforced on an incoming request; the mock accepts what it is
  sent.
- **No schema editor.** Schemas are JSON documents you write (or an agent
  writes); the panel renders the contract read-only.
- **No shared components from your own rows.** A custom endpoint's schema
  is inline. Two rows with the same shape carry two copies.
- **No review or comments.** The export is a file: put it in git, review
  it there.
