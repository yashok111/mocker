---
name: mocker
description: Drive a mocker instance — configure mock workspaces and API designs, prepare backend projects, or reconcile source-backed foundation or PostgreSQL/SQLite relational graphs with pinned database, comparison and evidence reads. Use for mocker, mock APIs for a frontend, backend project import, MOCKER_MCP_KEY, or `/__mocker/state`.
metadata:
  workflowId: "mocker-routing"
  workflowVersion: "1"
  requiredModelSchemaVersions: "[]"
  requiredCapabilities: "[]"
  guideSetId: "sha256:54ad47e9cec57296780d3ef881fa90d192c4bb467fcdcde124bd7df8692776de"
  manifestHash: "sha256:54ad47e9cec57296780d3ef881fa90d192c4bb467fcdcde124bd7df8692776de"
---

# mocker


mocker provides OpenAPI mocks, API design, saved sequences and backend projects.
Backend projects support metadata, immutable revisions and the first source-backed
foundation or PostgreSQL/SQLite relational source-facet import, explicit
same-provider snapshot reconciliation and pinned database/ER, structural
comparison and evidence reads. Typed NULL/NOT NULL and FK proposals use separate pinned preview/apply and unverified criteria;
endpoint-flow/lineage, incremental import and impact remain future work. For mocks, one OpenAPI
spec is imported once; every WORKSPACE
bound to it serves the spec's routes on its own host with deterministic
generated bodies, records what it served, and remembers what it is told to
remember. The agent talks to it through MCP (`POST /mcp`, bearer key) — the
same admin API the human panel uses, one tool per verb — spec import included
(`import_spec`, JSON or YAML) since A8.

## Mental model

**Two planes.** The ADMIN plane (one host, login, CSRF, the API, the UI,
`/mcp`) is where you configure. The MOCK plane (one host per workspace, no
auth, CORS to everyone) is what the frontend or test suite calls. A workspace
has a public `url` — `get_workspace` reports it — and three control routes
under `/__mocker`: `health`, `state`, `assets/{name}`.

**Four layers, each on top of the previous, decide a response:**

1. `Spec` — the schema, never mutated.
2. `Workspace` — per-operation overrides (`set_operation_variant`), custom
   endpoints (`create_endpoint`), settings (`update_workspace_settings`).
   Persisted, versioned, checkpointed.
3. `Scenario` — a named snapshot of layer 2, switched on and off by name.
4. `Session` — RAM-only directives: force a status, fail N times, delay,
   pause (`set_session_directive`, or `POST {url}/__mocker/state` from a
   test with no auth). Lost on restart, never versioned.

Plus two things outside the layers. A confirmed RESOURCE family
(`decide_resource`) makes `GET/POST/DELETE` on `/things` and `/things/{id}`
read and write real rows instead of generating; `set_resource_entity` and
`delete_resource_entity` write those rows from your side. And an endpoint
FUNCTION — Lua on one variant of layer 2 — PRODUCES the response by running
code instead of having one assembled, which is how "check the password and
branch" or "mint a token that expires in an hour" is expressed
(`references/functions.md`). A function beats a confirmed resource on the
same operation.

**Determinism.** Same spec + same `settings.seed` + same request =
the same body, every run — except fields anchored to the clock (deadlines,
`now`/`jwt` recipes), a confirmed resource's rows, and any endpoint carrying
a Lua function, which is out of the guarantee entirely. Change the seed to get different data; change `listSize` for
longer lists; recipes (`faker`, `enum`, `now`, `jwt`, `ref`, …) make single
fields realistic without pinning the whole body.

## Classify the request first

Before `list_workspaces`, classify the task as mock response configuration,
API design, saved sequence, backend project preparation, source snapshot
import/reimport, pinned database/schema inspection, or typed database proposal design. Mock configuration
uses the workspace procedure below; API design and saved sequences begin with
`references/design.md`. Backend project preparation begins with
`references/backend/overview.md` or `get_guide {topic:"backend-overview"}` and
its before-write compatibility handshake. The root routing workflow has no
backend capability requirements, so legacy mock tasks remain usable. Backend
project preparation must select a compatible pinned guide set before writes.
Source graph import/reimport or pinned structural comparison selects the installed
`mocker-backend-import` leaf when available. Root-only installations use the
generated compatibility copy `references/backend/import.md`, or discover
`get_guide {topic:"backend-import"}`. Select that complete pinned workflow before
any import write; its model/protocol/recovery/examples details come from the same
selected server guide set. A relational import requires schema2/profile/capability
support; a foundation-only compatible fallback cannot write a database task.
Database/ER inspection and proposal design select the independent `mocker-backend-database` leaf,
the generated root alias `references/backend/database-workflow.md`, or advertised
`backend-database` entrypoint. Select its complete compatible pinned procedure,
then load database-reference/model/recovery only as needed. Shared topics have
one canonical workflow owner; explicitly select/verify that owner's supported
identity in this same global set and check its returned tuple/contentHash.
Before any proposal write select database workflow2, proposal-relational-v1 view,
exact guide set/hash and proposal/edit capabilities. Workflow1 is inspection-only;
a complete compatible server fallback is required for mismatched local metadata.
No compatible set means no proposal writes. Source import stays on import3.
Mock response changes keep the workspace procedure and
do not need source import or a full backend topic load.

## Mock workspace workflow

1. `get_server_config` once — the limits behind every 413 and refused draft. Then `list_workspaces` → `get_workspace` (slug, url, specId, editVersion).
2. For workspace response changes, `find_operations {query}` → `opKey`s;
   `get_operation {opKey}` before a write. For a saved sequence, read
   `references/design.md` (or `get_guide {topic:"design"}`): its `operationKey`
   comes from `x-mocker-canvas-operation-id` in the returned API `draft.document`.
   Keep that returned document when creating the scenario contract.
3. No spec yet? `import_spec {name, document}` with the file's text (JSON or
   YAML); then `create_workspace {specId}`.
4. Do the task from the cookbook (`references/cookbook.md`): stand up a
   workspace, make login work, force an error, shape a body, add a route,
   confirm a resource, save a scenario, undo, stream, assets, drift, debug.
5. Verify on the MOCK plane, not by re-reading config: `probe_workspace`, or
   curl `{url}/…`, then `list_traffic` to see what was served and why.

## Rules that bite

- **Whole-object writes replace.** `set_operation_variant`, `update_endpoint`
  and `update_workspace_settings.settings` drop everything you do not resend.
  Read, edit, resend the whole document.
- **`editVersion` is compare-and-swap.** Take it from the matching read
  (`get_operation`, `get_workspace`, `list_endpoints`, `get_scenario`); a
  stale one comes back as a `conflict` field carrying the current document,
  not as an error. Re-read, reapply, resend. `0` is legal only where "no row
  yet" is possible (operation overrides).
- **`confirmSlug` is the workspace's exact slug**, read live, on the nine
  destructive tools (`delete_*`, `rollback_workspace`, `reset_*`,
  `clear_traffic`, `decide_resource`). Never guess it; never pass another
  workspace's.
- **`opKey` is already percent-encoded.** Pass it back as it came; encoding
  it again is a 400.
- **Session directives never bump `revision`**; a stuck `fail`/`status`
  directive is the first thing to check when "the mock is broken".
  `set_session_directive {clearAll: true}`.
- **A scenario does not carry** custom endpoints, `basePath`, CORS,
  `notFoundBody` or entity rows; `create_scenario` is refused while another
  scenario is active. Renaming one breaks tests that switch by name.
- **Undo exists only for layer 2** (`list_checkpoints` → `rollback_workspace`);
  traffic, scenarios, assets and directives are not in any checkpoint.
  `reset_resource_data` writes no pre-destructive checkpoint: `create_checkpoint` first.
- **Not idempotent:** `create_workspace`, `create_checkpoint`. List before retrying after a timeout. `import_spec` IS safe to retry: the same bytes answer `duplicate: true`.
- **A Lua function is compiled when you store it** — a syntax error is a 400
  with the parser's own line, never a 500 on the first request — and it
  REPLACES assembly for its variant: recipes, `schemaPatch`, the envelope and
  `bodyRef` do not run beside it, and the variant refuses to carry them.
- **Errors carry the server's own words** (`admin API returned 400: …`). Read them; they name the field.

## When to open which reference

- `references/tools.md` — tool inputs, outputs, gotchas, `editVersion` and `confirmSlug` rules.
- `references/shapes.md` — override document, `when[]`, recipes (14 kinds), custom endpoint, stream document, session directive, settings, resources, assets, error envelope.
- `references/cookbook.md` — twelve ordered recipes.
- `references/http.md` — the same thing with curl: login/CSRF, spec import, raw asset upload, the `/__mocker/state` calls a test suite makes, MCP client config.
- `references/design.md` — API designer revisions and publication, sequence-canvas scenarios and complete runs, plus the classic workspace design/export workflow.
- `references/functions.md` — endpoint functions: the `req`/return contract, the `mock` helpers, the sandbox, the guards, where the branch sits on each plane, and the two stream hooks (`tick.lua`, `stream.onFrame`).

The running server serves these same texts: `get_guide {topic: "overview" | "tools" | "shapes" | "cookbook" | "http" | "design" | "functions"}`.


For sequence branch coverage gaps, use `suggest_design_scenario_tests` to obtain
initial-variable variants. Run selected cases with `run_design_scenario`, a
fresh runId and the returned revisionId/name/variables, then verify the actual
controlFlow and refresh coverage. Generation is read-only; unresolved paths
include reasons. See `references/design.md` for the workflow and limits.
