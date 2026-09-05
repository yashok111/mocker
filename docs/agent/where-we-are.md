# Where we are: every shipped slice, what is next, where the decisions live — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

## Where we are

**Shipped, one slice per commit.** `P0` (authentication, workspaces, both planes,
admin API) → `P1a` (spec import, lazy `$ref` resolver, route table) → `P1b` (body
generator) → `P1c-1` (recipes, `op_overrides`, the auth preset) → `P1c-2` (`when[]`,
live state in RAM, traffic, custom endpoints) → `P1d-1` (the SPA inside the binary) →
`P1d-2` (the admin API contract and the test that does not let it lie) → `P1e` (the six
screens of §14) → `delay`/`pause`, Go 1.27, `/mcp` with nine tools → `P2b` (the
`Scenario` layer) → `P2c` (checkpoints and rollback) → `P2d` (scenario clone and
rename, debounce, checkpoint deletion) → `P2e` (`schema_patch` and three recipes) →
`P2f` (`POST .../preview`) → `A1` (`PUT .../endpoints/{eid}`) → `A2` (MCP surface
9 → 38) → `A3` (per-row compare-and-swap on write) → `P3a` (a mock REMEMBERS a write:
resources) → `P3b` (`config_snap` carries resources UPSERT-only; `reset-data`) →
`P3c` (the `ref` recipe) → `P3d` (`checkpoints.data_snap` becomes real bytes) →
`P3e` (one level of nested families) → `P3g` (nesting to a ceiling of three) →
`P3h` (a parameterised `basePath` as a second addressing axis) → `P3f`
(`rederive`: a new `resource_suggestions` generation over an already-imported
spec, without a re-import) → `P4a` (the read-only drift report: an orphaned
override, an orphaned confirmed resource family, a shadowing custom
endpoint — `GET /api/workspaces/{id}/drift`, one MCP tool, no repair verb,
no screen) → `A4` (the agent's reach: entity ROWS through a filtered, cursored
read, a tool over the existing probe route, and the traffic cursor
`list_traffic` had underneath and never exposed) → `P6a` (the traffic feed over
SSE: `internal/stream`, `GET /api/workspaces/{id}/traffic/stream` beside the
poll it never replaces, `GET /api/stream/stats` and its tool, the nudge out of
the recorder, the shutdown order on both exit paths, migration `0004`, and the
screen's live/fallback badge) → `P6b` (SSE mock endpoints: a custom endpoint of
`kind: "sse"` with a scripted timeline and a generated tick, served on the mock
plane through a second, per-workspace-capped registry; migration `0005`,
bundle v4, one traffic row per connection, three mock-plane variables, a
draft preview route and tool — MCP-only, no screen) → `P6c` (the
live-connection surface: list, close and push a frame over the mock plane's
registry, three routes under `/api/workspaces/{id}/connections`, three MCP
tools, a per-connection RAM inbox answering §30.16's "where a pushed frame
lives", two new traffic-note tokens — no variable, no migration, no screen)
→ `P6d` (WebSocket: `kind: "ws"` over `github.com/coder/websocket` behind
`internal/wsmock`, reactive rules and echo over the inbound frame, a
byte-bounded reply queue with a gap marker, three variables, the CSRF
predicate widened for an upgrade `GET`, the CSP naming `ws:`/`wss:`, the
first inbound frame recorded redacted — no route, no tool, no migration, no
screen) → `A5` (the stack as one command and the proxy contract as a test:
`make up` on a bare clone creates `.env` with a real hash, `mocker
healthcheck` is the distroless image's `HEALTHCHECK` through
`probe.Readiness`, `docker-compose.tls.yml` puts Caddy with a local CA in
front with `MOCKER_DEV=0`, Caddy's one static address trusted and no plain port, and
`scripts/smoke-tls.sh` checks TLS, the `Secure` cookie, the `https`
workspace URL, the wildcard, the forwarded client in the traffic row, SSE
and `wss://` through the proxy — no Go route, no tool, no migration, no
screen, one Go subcommand) → `A6` (assets: uploaded files a mock can
serve — `internal/assets` over a new `assets` table (migration `0006`),
three admin routes with three MCP tools (contract 61 → 64, tools 51 → 54,
`EXEMPT` 9 → 12), the mock plane's third control route
`GET {prefix}/assets/{name}`, `bodyRef` on a pinned variant, the
`asset_url` recipe, `httpx.WorkspaceURL` as the one builder of a
workspace's public URL, two variables (32 → 34), DESIGN v11 §32 — no
screen) → `A7` (documentation: `docs/USER-GUIDE.md` rendered at `/guide`,
the `skills/mocker/` skill with four references, `internal/guide` embedding
them for `initialize`'s `instructions` and the `get_guide` tool — tools
54 → 55, one screen on the owner's request, no route, no variable, no
migration) → `P6e` (the streaming screens: stream authoring on the
custom-endpoints screen, the browser-side test client, the «Соединения»
tab; two contract drifts repaired, one server struct widened, `EXEMPT`
12 → 8 — no route, no tool, no variable, no migration) → `A8`
(`import_spec`, tool 55 → 56, `POST /api/specs` let into the MCP allowlist;
YAML input through `internal/yamlx`, the second isolated library — no
route, no migration, no variable) → `A9` (`config.Limits` on
`ServerConfigView` and `get_server_config`, tool 56 → 57; the caps strip
shows effective numbers — no route, no migration, no variable) → `A10` (the
«Файлы» tab over the asset routes, `customFetch` keeping a `Blob` body's
own type, `EXEMPT` 8 → 5 — no route, no tool, no migration, no variable)
→ `A11` (entity writes: `PUT`/`DELETE .../resources/{family}/entities/{key}`,
contract 64 → 66, `set_resource_entity`/`delete_resource_entity`, tools
57 → 59, `EXEMPT` 5 → 7 — no screen, no migration, no variable) → `P4b`
(the bundle over HTTP and the fork: `GET .../export`,
`POST /api/workspaces/import`, `POST .../fork` over `checkpoints.Repo`'s
own capture and apply in one transaction each, `bundle.Export` with
`data` and `spec.inline`, `workspaces.CreateTx`, spec resolved
`specId` → hash → inline → none; contract 66 → 69, tools 59 → 62,
`EXEMPT` 7 → 10 — no screen, no migration, no variable) → `A12`
(`@yashok111/mocker-test`: `packages/mocker-test/`, a client, a Playwright fixture
and Cypress commands over the mock plane's control routes, tested
against the real binary — no Go change at all) → `A15` (the audit:
five files split by pure moves, one process-killing race and a dozen
correctness holes closed with a test each, the generator 37% faster on
the corpus benchmark, migration `0007` — no route, no tool, no variable,
no screen) → `A16` (`mocker setup`: the install wizard, three OS, one
binary, `make dist`) → `P7a` (API design on top of a workspace, the
server and the agent — DESIGN §34: a response `schema` and the
`operation` fields on a custom endpoint, served through `assembleResponse`
and exported by `internal/design` as one OpenAPI document,
`GET /api/workspaces/{id}/openapi.json`, `export_openapi`, a `design`
guide topic, migration `0008`, bundle v5 reading v4; contract 69 → 70,
tools 62 → 63, `EXEMPT` 10 → 11 — no screen, no variable) → `P7b` (the
«Контракт» tab, DESIGN §34.5's screen half: a read-only tree over
`GET .../openapi.json` with base/added/changed/removed badges computed
client-side from the two editor routes, «Открыть в редакторе» links into
those editors through search params, «Скачать» from the fetched document;
`hasSchemaPatch` on the operations list's per-status summary is the one
server change; `EXEMPT` 11 → 10 — no route, no tool, no migration, no
variable) → `A17` (publication: origin GitHub and one commit, the
acceptance corpus embedded, `.github/workflows/ci.yml` with the capped
scope, `@yashok111/mocker-test` 0.1.0 by hand and 0.1.1 by tag through
Trusted Publishing — no product change) → `A19` (`mock.generate` and the
entity writers, 2026-09-05; the same day's review of A18 is seven `fix(a18)`
commits, recorded in `docs/agent/functions.md`) ← `A18` (endpoint functions:
Lua that PRODUCES a response, `internal/luafn` as the third isolated
library behind one importing package, a serving branch on both planes
that beats a confirmed resource and replaces `assembleResponse` without
calling it, `mock.jwt`/`mock.now`/`mock.entities` with real `scopeArray`
filtering, bundle v6 reading v5 and nothing older, and D10's two stream
hooks `tick.lua` and `stream.onFrame`; a seventh `get_guide` topic,
fourteen carve-outs, `//nolint` 36 → 39 — no route, no tool, no
migration, no variable, no screen) → `A20` (the screens the A4 rule had
deferred, 2026-09-05, on the owner's pick: «Записи» under a confirmed
family over the three entity routes, «Скачать бандл»/«Копировать» on the
overview and «Импорт из файла» on the workspaces list over the three P4b
routes; the stream form learned `tick.lua`/`onFrame` and the endpoint form
the `function` field, both of which an edit from the screen had silently
dropped; then, the same day on «добей последние 4 гэпа», «Проверить спеку»
on the overview, the stats strip on «Соединения» and the header's server
status over the two probes — `EXEMPT` 10 → 0, no route, no tool, no
migration, no variable).
What each of them SHIPPED is in "Architecture"
above; HOW each arrived — gates, fleet runs, the lessons paid for — is
`HISTORY.md`.

**What is NEXT, ranked.** The full argument for each is in `HISTORY.md`; this is the
order and the reason in one line.

**Streaming is complete**: `P6a`–`P6d` shipped 2026-09-02 and `P6e` — the
screens — the same evening, after the owner lifted the A4 rule for it in
his own words (see "Architecture", the paragraphs after `internal/stream`
and the `P6e` paragraph). Nothing of §30 is left.

**`P4` is complete too**: `P4a` shipped its read half, `P4b` (2026-09-02)
the bundle over HTTP and the fork, and `migrate-workspaces` was turned
down on `P4a`'s own evidence (`CARVE-OUTS.md`). §2's decision table's
compensation for "git review of the config is lost" is now built.

**§34 is complete too**: `P7a` (the server and the agent) and `P7b` (the
«Контракт» tab, the A4 rule lifted for it by the owner — «снимаю
ограничения для 3 пункта», a Russian string quoted as data) both shipped
2026-09-03 from one gate document.

**`A18` (endpoint functions) is complete and has no DESIGN section**: the
owner asked for it directly and the authority is
`docs/A18-endpoint-functions.md` in this repository, not a DESIGN heading —
the first gate document committed here rather than left in a workspace
outside it. What it leaves open is written down and not planned: the Lua
contract carries no version marker, so a future slice that NARROWS the
sandbox must refuse by name at some door the way a bundle version does (an
undefined global reads `nil` in Lua and the write-time compile check
cannot see it). That is an obligation on a later slice, recorded in
`CARVE-OUTS.md`, not an item on this list.

1. **`IDEAS.md`'s ranked backlog** — fetch-by-URL through the allowlist
   (needs a gate: outbound HTTP on the authenticated plane), a drift
   screen, a record-proxy, Swagger 2.0. Nothing there is a design
   debt; each entry carries its cost and its argument.
2. **Deeper nesting, `P2` UI debt, `P5`.** A fourth nesting level is refused on
   measured grounds (`CARVE-OUTS.md`); `P2`'s Monaco/schema tree and the recipe
   editor are UI debt; `P5` (real isolation — the admin plane is one shared password)
   is a policy call about the network, not a gap in the code.

`P4`, part one — spec re-import triage (§5) — is no longer on this list: `P4a`
shipped its READ half (`GET /api/workspaces/{id}/drift`, "Architecture" above),
and its REPAIR half needed no new verb at all — `DELETE .../operations/{opKey}`,
`DELETE .../endpoints/{eid}` and `POST .../resource-decisions` with
`state: "declined"` already existed, so the drift report simply points at them
by naming what an operator or agent must delete. What §5 also asked for and
`P4a` deliberately did NOT build — both schema-diff halves, an automatic
reattachment heuristic, the two PRESERVING remedies (turn an orphaned override
into a custom endpoint, turn a shadowing endpoint into an override), a fourth
signal for a declined family with no confirmed row, a static-capture signal, a
screen — is recorded in `CARVE-OUTS.md`, not on this ranked list: none of it
was argued as a future slice, each is a line item `P4a`'s own decisions turned
down on its own evidence.

**Where the decisions live.** Every slice from `A1` to `P7b` was designed in its own gate
workspace outside this repository — SEVENTEEN of them (`mocker-a-mcp`, `mocker-a3-cas`,
and one each for `P3a`, `P3b`, `P3c`, `P3d`, `P3e`, `P3g`, `P3h`, `P3f`, `P4a`, `A4`, `P6a`, `P6b`, `P6c`, `P6d`, and `mocker-p7-api-design` for `P7a` AND `P7b`, one document for both) — and each holds
the decisions document that is the authority on WHY the slice is the way it is.
They are all OUTSIDE this repository and survive only while those directories do,
so anything that must outlive them is written here or in `HISTORY.md`.

**`A18` broke that pattern and the pattern should stay broken.**
`docs/A18-endpoint-functions.md` is IN the repository: D1–D10 plus D8b, a
§A acceptance section of 59 clauses measured against a recorded baseline,
the fourteen `[GIVES-UP]` items, and the four-round gate record. It cost
nothing to commit and it is the only one of the eighteen that a reader six
months from now can still open.

