---
name: mocker
description: Drive a mocker instance — configure mock workspaces and API designs, prepare backend projects, or reconcile source-backed foundation or PostgreSQL/SQLite relational graphs with pinned database, comparison and evidence reads. Use for mocker, mock APIs for a frontend, backend project import, MOCKER_MCP_KEY, or `/__mocker/state`.
metadata:
  workflowId: "mocker-routing"
  workflowVersion: "2"
  requiredModelSchemaVersions: "[]"
  requiredCapabilities: "[]"
  guideSetId: "sha256:03dc78bcd9b4cb99bc1c121e9f7020803fbf90a97abaef416dc493fa69e64a87"
  manifestHash: "sha256:03dc78bcd9b4cb99bc1c121e9f7020803fbf90a97abaef416dc493fa69e64a87"
---

# mocker


mocker provides OpenAPI mocks, API design, saved sequences and backend projects.
Backend projects support metadata, immutable revisions and the first source-backed
foundation or PostgreSQL/SQLite relational source-facet import, explicit
same-provider snapshot reconciliation and pinned database/ER, structural
comparison and evidence reads. Typed NULL/NOT NULL and FK proposals use separate pinned preview/apply and unverified criteria;
source schema3 adds pinned endpoint flows and imported scoped data accesses;
source4 adds explicit field lineage and manual exact API associations through inspect13;
source5 adds evidence-backed event routes/jobs/service calls and contextual fields; source6 incremental synchronization uses sync2; static diff/impact uses durable pinned jobs with runtime unverified. For mocks, one OpenAPI
spec is imported once; every WORKSPACE
bound to it serves the spec's routes on its own host with deterministic
generated bodies, records what it served, and remembers what it is told to
remember. The agent talks to it through MCP (`POST /mcp`, bearer key) — the
same admin API the human panel uses, one tool per verb — spec import included
(`import_spec`, JSON or YAML) since A8. `tools/list` is a summary (one sentence and top-level argument names per tool): `describe_tool {name}` returns a tool's full description and argument schema — call it before the first use of a tool whose arguments are not obvious.

Source6 supports explicit multi-provider source synchronization through sync2. Full desired graph changes use change5; annotations use project2. Read the selected owner's exact contract before any write.

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

For backend imports intended to explain a system, use `mocker-backend-explain` and the Human-readable modeling contract in `backend-interactions` (selected inspect owner). Source capture plus readable, evidence-linked scenarios is the deliverable; a code-shaped Flow alone is incomplete.

Route the task before listing or changing resources. Keep ordinary mock workspace/API-design/sequence workflows below intact; routing2 has no backend capability requirements.

| Task | Select the complete pinned owner |
| --- | --- |
| Create/rename a backend project; source-object annotations | project2 / backend-overview, then backend-annotations |
| Existing source1–5 import, adjacent profile transition or revision comparison | import8 / backend-import |
| Composed source6 addition/reconcile/provider migration or authorized incremental sync | sync2 / backend-sync |
| Exact graph/proof/Flow/lineage/events/artifact inspection | inspect13 / backend-inspect |
| Relational source/full inspection or existing typed relational proposals | database7 / backend-database |
| Full desired graph proposal, qualified intended keys, history/restore | change5 / backend-change-proposals |
| Three-way rebase, durable diff/impact, exact impact report to ready | change5 / backend-change-rebase, backend-analysis-jobs |
| Saved Flow/Database presentation | inspect13/database7 with the matching v1/v2 view contract |

Prefer the installed independent leaf; root-only clients use its generated compatibility reference or get_guide entrypoint. Verify actual advertised workflowId/version/set/manifest/topic hashes and all requirements. On local mismatch use the complete compatible server procedure in the same selected immutable set. Unknown guide sets fail; tool presence alone is insufficient. Every shared topic keeps its canonical owner. No compatible procedure means supported reads only.

Inspection starts no source/metadata/proposal mutation. A requested source correction chooses import8 for its legacy source scope or sync2 for composed source scope, retaining capture/proof/audit rules. A human annotation is metadata; a full proposal is intent; neither repairs source evidence. READY staging has exact basic reads and cannot be used for specialized queries or saved presentations. Select change5/backend-analysis-jobs for durable diff/impact and exact report-to-ready; change5/backend-change-rebase for explicit three-way rebase. Imported code, SQL and jobs never execute.

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

Saved Flow/Database presentation tasks select inspect13/database7 respectively,
requiring backend-saved-views and saved-view-v1 in that same pinned set. Read the
saved version before model reads, preserve exact targets and both URL viewId/
viewVersion, and use the owner's complete saved-view example. Create/save are
explicit presentation writes with retained exact keys/requests and old-version
CAS. Failed saved reads have no head fallback. Proposal intent/unknown runtime
checks stay explicit; layouts/collapse infer no source behavior or field lineage.
Source import continues to select import8. Live agent acceptance remains deferred.

For authored sequence/state/rule/EventModel projections select inspect13 with
backend-editor-projections and backend-editor-artifacts-v1; load
`backend-editor-projections` before reads or authorized generic pin changes.
Keep authored completeness, origin verification, source evidence and runtime
completeness separate; saved copy content remains independent.

Source5 event/job/service-call questions select inspect13 with backend-events-query
and load backend-events from its verified owner in this set. Import8 owns
events-service-v1/schema5, exact five-profile initial imports and explicit4→5
extension. Database7 delegates column→event-field navigation to inspect13.
Keep exact route/full field context, explicit transport, unknown boundaries and
static source provenance; no scheduling or runtime delivery is inferred.

For static diagnostic jobs and separate finding review, negotiate **verify1** and read `backend-verify` / `backend-diagnostics` from the same immutable guide set. Inspection alone starts no job; review is not runtime proof.
