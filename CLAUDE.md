# mocker — context for the agent

A mock server on top of OpenAPI: imports a spec, serves each workspace on a host
(or on `/w/{slug}`), generates deterministic responses, records traffic, provides
an admin panel. Go 1.27.1, module `github.com/yashok111/mocker`, stdlib
`net/http` plus THREE libraries, each behind ONE importing package whose
`boundary_test.go` fails the build on a second importer —
`github.com/coder/websocket` behind `internal/wsmock` (`P6d`, admitted by
`DESIGN.md` §30.9 on a written measurement: zero transitive modules, no cgo,
no runtime executable memory, context-based reads and writes),
`go.yaml.in/yaml/v3` behind `internal/yamlx` (`A8`; a YAML document becomes
JSON bytes there and nowhere else) and `github.com/yuin/gopher-lua` behind
`internal/luafn` (`A18`). The pattern is the one `internal/jsonx` sets for
`encoding/json` and `internal/probe` for outgoing HTTP. The qualifier is
"one library, one importing package", not "a framework": there is no
framework and there will not be one.

The spec is `DESIGN.md` (v12, ~3200 lines, 50-odd headings; §34 is the API-design intent of 2026-09-03). **Do not read it
whole**, only by line range: it is the single document that outranks the
code. References of the form "§14" are its sections; to find one:
`grep -nE '^#{1,3} ' DESIGN.md`, then `sed -n 'A,Bp'`. **§23 is the state as
built**: what is shipped, what was built differently than designed, and what was
designed and not built. Sections 1–22 remain a document about the INTENT and were
edited only where the intent had silently diverged from the implementation.

**Four sibling documents are NOT auto-loaded and are read on demand.** This file
is the per-session context and holds only what every run needs; the rest lives
next to it, so open the one the task is actually about:

- `HISTORY.md` — how each slice arrived: the gate rounds, the fleet runs and their
  numbers, the lessons that were paid for rather than argued, and the ranked list of
  what is NEXT with the full argument for each item.
- `CARVE-OUTS.md` — every deferred or decided hole, per slice, with the measurement
  behind it. Read it before calling something missing a bug, and before a slice
  claims to close one.
- `README.md` — how to run it and what lives where; the essentials are duplicated
  here.
- `IDEAS.md` — what could come next that nobody promised, ranked by cost, with
  the argument for each; an item leaves it for `HISTORY.md` when it ships or
  for `CARVE-OUTS.md` when it is refused.

**The detailed context of each subsystem lives in `docs/agent/` and is read
on demand too.** This file was cut to what every session needs on
2026-09-05; every paragraph that left it is in one of these files, text
unchanged, in its original order, so a citation of CLAUDE.md "Architecture"
elsewhere resolves there. Open the one the task touches BEFORE editing that
subsystem: each records decisions the code cannot show, and several record
an approach that was tried and reverted.

| File | Read when the task touches… |
|---|---|
| `docs/agent/mcp.md` | `internal/mcp`, a new tool, `CallAsMCP`, the allowlist, the embedded guide (`A7`), `import_spec`/YAML (`A8`), `get_server_config` (`A9`) |
| `docs/agent/scenarios-checkpoints.md` | `internal/bundle`, `internal/scenarios`, `internal/checkpoints` (restore order, `Auto`, `data_snap`), export/import/fork (`P4b`) |
| `docs/agent/resources.md` | `internal/resources`: confirm/decline, `reset-data`, nested families, a parameterised `basePath`, `rederive`, the `ref` recipe, the entity read and write routes (`A4`, `A11`) |
| `docs/agent/drift.md` | `GET .../drift` (`P4a`), `resources.OrphanedIn` |
| `docs/agent/streaming.md` | `internal/stream`, SSE/WS custom endpoints, the connection routes, `MOCKER_STREAM_*`, the streaming screens (`P6a`–`P6e`, `A14`) |
| `docs/agent/assets.md` | `internal/assets`, `bodyRef`, `asset_url`, the «Файлы» tab (`A6`, `A10`) |
| `docs/agent/design.md` | a response `schema` or `operation` fields on a custom endpoint, `internal/design`, `GET .../openapi.json`, the «Контракт» tab (`P7a`, `P7b`) |
| `docs/agent/functions.md` | `internal/luafn`, `Variant.function`, `tick.lua`, `onFrame`, bundle v6 (`A18`) |
| `docs/agent/test-plugin.md` | `packages/mocker-test`, `livestate.Store.Delete` (`A12`, `A13`) |
| `docs/agent/audit-a15.md` | `internal/gen`'s cost model and `jsonSize`, the login limiter, and the other invariants `A15` fixed with a test each |
| `docs/agent/config.md` | any `MOCKER_*` variable: the full list with the rule each one enforces at startup |
| `docs/agent/ops.md` | CI, test speed, the smokes, `docker-compose.tls.yml` (`A5`), `mocker setup` (`A16`), publication and the npm release (`A17`), the `//nolint` census, goleak |
| `docs/agent/jsonx.md` | why `encoding/json` stays and what sonic broke |
| `docs/agent/contract-frontend.md` | `api/openapi.json`, `coverage.test.ts`, the `EXEMPT` roster, `web/` conventions |
| `docs/agent/where-we-are.md` | the full shipped list, what is NEXT with its argument, where each slice's decisions live |
| `docs/agent/ui-review-2026-09-05.md` | any UI change: the six-reader review after `A20` — the ranked bugs, gaps and usability items, the `VariantEditor` reading, the suggested `A21` order |

## Architecture

Two planes, and that is the whole point. `cmd/mocker/main.go` is the single entry
point: it assembles the config, the DB, both planes and wires the shared state
through setters. `internal/server` is the dispatcher: by `Host` (or by `/w/{slug}`
under `MOCKER_ROUTING=path`) it decides who gets the request, and does nothing else.

- **The mock plane** (`internal/mockplane`) — the workspace host, **with no
  authentication by design**: CORS to everyone, response generation, traffic
  recording, control routes under `MOCKER_RESERVED_PREFIX`.
- **The admin plane** (`internal/admin`) — only `MOCKER_ADMIN_HOST`: session,
  CSRF, CRUD, and it also serves the embedded UI (`internal/webui`) through a wrapper.

`internal/probe` — **the only outgoing HTTP client in the whole tree**
(the server half of the «Проверить» button, §14 screen 4). It is a separate package
exactly for that reason: the timeout, the redirect ban and the cap on the readable
body must sit in one place, the place people will look at when a second such client
appears. The target is not taken from the request — it is assembled from the
workspace record, a scheme from the whitelist, and a port that is required to be a
bare number. **The second such client appeared with `A5` (2026-09-02) and
landed exactly there**: `probe.Readiness(ctx, target, host)` is the same
client with a `Host` override, called by `mocker healthcheck`
(`cmd/mocker/healthcheck.go`) — the container's own `HEALTHCHECK`, because
the image is distroless and has no curl. It runs `config.Load` first (the
same environment the server started from, refused the same way), dials
`http://<loopback>:<MOCKER_ADDR port>/readyz` with `MOCKER_ADMIN_HOST` as
the `Host` (the dispatcher routes by it; a bare loopback authority reaches no
plane), and exits 0 only on a 200 whose body says `ok: true`. `/readyz` and
not `/healthz` on purpose: "healthy" is what `docker-compose.tls.yml`'s
Caddy waits on before taking a request, and a process whose database is not
open yet must not earn it.

`internal/mcp` — a Model Context Protocol server, `POST /mcp` on
`MOCKER_ADMIN_HOST`, mounted only when `MOCKER_MCP_KEY` is set: a thin
adapter over the admin plane, not a third plane (DESIGN §14.2, the threat
model §15). Three things about it cannot be guessed from the two planes'
code: it is mounted OUTSIDE the CSRF/session chain (a bearer key by
constant-time comparison, no cookies read), it is deliberately NOT in
`api/openapi.json` (nothing to type in JSON-RPC, and the contract tests'
perimeter must not include it), and every tool that touches the domain goes
through `admin.Server.CallAsMCP` — an in-process call into the same admin
route table under an MCP identity, never straight into a repository, so
there is one validation path. 63 tools; a tool with an empty `toolRoutes`
row (`get_guide`, `get_server_config`) calls no handler. The allowlist and
its one policy reversal (`POST /api/specs`, `A8`): `docs/agent/mcp.md`;
client config: `README.md` ("MCP").

A response is assembled by **four layers (§4), each on top of the previous one**: `Spec`
(the schema as in the spec, never mutated) → `Workspace` (operation overrides
`internal/overrides`, custom endpoints `internal/customep`, settings — all
in SQLite) → `Scenario` (a named snapshot of the workspace, P2b) → `Session`
(`internal/livestate`: forced status, `fail_next` — **RAM only and never
bumps `revision`**). Bodies are generated by `internal/gen` + `internal/recipes`,
deterministically from `settings.seed`.

Since A1 a custom endpoint has `PUT /api/workspaces/{id}/endpoints/{eid}`
— a full replacement of the definition, not a partial merge: an omitted `overrideOn`
reads as `true`, an omitted `routeOff` — as `false`, by the same rule
as on creation. The screen no longer sends the operator off to delete and re-create
the endpoint — it opens the same form, prefilled with the current
values.

Everything built on those four layers — the `Scenario` codec and
checkpoints, resources (a mock that REMEMBERS a write), streaming, assets,
API design, Lua functions — is described in `docs/agent/` (the index above),
one file per subsystem. Five rules recur across all of them and are the ones
a slice most often gets wrong on its first draft:

- **A new admin route** is: `Server.routes()` + `api/openapi.json` +
  `coverage.test.ts`'s count + an MCP tool + a screen or an `EXEMPT` entry.
  A mutating route that touches no configuration layer joins
  `autoCheckpointExcludedNeverTouchesLayer` (`internal/admin/route_table.go`);
  otherwise the auto-checkpoint wrapper snapshots before it, MCP calls
  included, since `CallAsMCP` dispatches through the same mux.
- **Inside a `db.Write` callback never call `workspaces.Repo.Update`** — the
  single writer connection deadlocks, a hang and not a red test. A package
  bumps `revision` with its own `bumpRevisionTx`; RAM-only state
  (`internal/livestate`) never bumps it.
- **`confirmSlug` guards verbs that destroy MANY workspace-created rows**
  (decline, `reset-data`, `restoreData: true`, asset delete), checked inside
  the same transaction as the delete; a verb that destroys one row or no data
  does not carry it.
- **`gen.Body` has two call sites and `assembleResponse` three callers**
  (`TestAssembleResponseIsTheOnlySeam` names them). A new response path is a
  sibling the test names, never a fourth caller and never a third `gen.Body`.
- **A family is addressed by `route_family`, an asset by name, never by row
  id**: an id does not survive the delete-and-recreate repair the product
  prescribes for a wrong object, and `EntityStore` is not workspace-scoped —
  a family name becomes a `resource_id` only through THIS request's roster.

## Bars

```bash
make test    # go test ./cmd/... ./internal/... -race -count=1 -p 2
make lint    # go vet + gofmt + golangci-lint v2 (.golangci.yml)
make ui-test # tsc --noEmit + vitest in web/
make ui-lint # oxlint + oxfmt --check
make smoke   # docker image + end-to-end curl checks
make smoke-tls # the HTTPS overlay end to end: TLS, the proxy contract, SSE and WS through Caddy
make plugin-test # @yashok111/mocker-test (packages/mocker-test): install, tsc, oxlint, vitest against ./bin/mocker
```

All seven run in CI (`.github/workflows/ci.yml`: six jobs, the non-docker
ones inside a memory- and CPU-capped systemd scope through
`scripts/ci-cap.sh` — pass `CAP=` to make there so the Makefile's own cap
does not nest). How the race suite was brought to ~1 minute, why the two
smokes are the critical path and how to read a slow one: `docs/agent/ops.md`.

The development cycle:

```bash
make run             # local binary; export .env.example first
make ui-dev          # Vite dev server, proxies /api to an already running :8080
make up / make down  # compose stack; `up` creates .env first if it is missing (scripts/init-env.sh)
make init            # .env from .env.example with a real hash; PASSWORD=… or one is generated and printed once
make up-tls / down-tls / tls-root  # the HTTPS overlay (docker-compose.tls.yml via scripts/compose-tls.sh)
make docker          # build the mocker:local image, UI inside
make release         # shippable binary: rebuilds the SPA (make build does not)
make hash-password   # argon2id for MOCKER_SHARED_PASSWORD_HASH
```

The compose stack as one command (`make up` creates `.env`; an existing one
is never rewritten), the HTTPS overlay (`docker-compose.tls.yml` is an
OVERLAY, never run bare, driven by `scripts/compose-tls.sh`; every curl at
it needs `--noproxy '*'` on this box), `mocker setup` (`A16`) and the npm
release recipe (`A17`) are in `docs/agent/ops.md`.

**Configuration — through the environment only: 36 `MOCKER_*` variables,
all with defaults in `internal/config`; the full list with comments —
`.env.example`, the rule each one enforces at startup —
`docs/agent/config.md`.** Two that bite first: `MOCKER_ADMIN_HOST` may not
sit under `MOCKER_BASE_DOMAIN` (startup refuses, and that is why the
browser cannot derive a workspace address from `window.location`), and
`MOCKER_DEFAULT_SPEC` is the numeric id of an already imported spec — set to
nothing that exists, a crash at startup.

**The scope of the Go bars is `./cmd ./internal`, never `./...` and never a bare `.`.**
`web/node_modules` sits inside the Go module; a single `.go` in any transitive
npm dependency would break `go build ./...`, `go vet ./...` and `gofmt -l .`
irreparably, and `gofmt -w .` would additionally rewrite someone else's sources. `gofmt`
gets two directories, not a list from `git ls-files` — that one does not see new
untracked files.

**The linter is golangci-lint v2**, `.golangci.yml` shared with another
backend of the owner's. Keep it at zero. Exceptions are only pinpoint
`//nolint:<linter> // reason` at the site of the trigger, never a wider
config: 39 today; the counting command, the census and the reason behind
each — `docs/agent/ops.md`.

**goleak is in every package with tests** (36 packages, three lines each,
the ignore list once in `internal/testleak`). A goroutine that outlives a
package's tests fails it. Do not extend the ignore list: it holds only what
the runtime parks for the whole process (`database/sql` opener/resetter,
sqlite VFS); everything else is a finding, usually a `Run`/`Close` protocol
returning before its own goroutines — exactly that once lost the last
traffic records at shutdown.

**A green `go test` ≠ the phase is done.** The project twice caught a fully green
suite with a feature dead in prod: the tests wire the dependency explicitly, and
`cmd/mocker/main.go` does not. The real check is `make smoke`: it builds the
image and pokes a live stack. Verify with commands and their output, not with agents'
reports.

## JSON — only through `internal/jsonx`

The backend is **`encoding/json`**, but **not a single production file
imports it directly** (`internal/jsonx/boundary_test.go` walks the AST and
fails the build on a violation). Ask `jsonx.Malformed(err)`, never
`errors.As` on a stdlib error type: a different backend returns different
types and the match silently stops (a malformed login body becomes a 500).
Tests stay on `encoding/json` directly, as a continuous cross-check of the
configured backend. bytedance/sonic was integrated in full and rejected by
measurement — the numbers and the two traps: `docs/agent/jsonx.md`.

## Style

**Comments explain "why", not "what", and there are many of them: 39.5% of the lines
of Go production code (19336 of 48905) are comments.** This is not decoration but the project's working format:
almost every non-obvious decision carries a measured reason next to it ("measured against a
live `net/http.ServeMux`…", "round-1 review finding 2…"), and what is deferred is
marked as deferred. A change in this tree that explains only "what it
does" falls out of the style.

**Language: everything an agent reads is ENGLISH — comments, `CLAUDE.md`,
`DESIGN.md`, `README.md`, commit messages.** These documents were Russian until
2026-08-25 and were converted for token economy: Cyrillic costs roughly twice as
many tokens per character, and the three files ran ~57k tokens against ~36k after
the switch, with `CLAUDE.md`'s share paid on every session because it is
auto-loaded. **The product's operator-facing strings stay RUSSIAN** — the admin UI,
its Russian copy and the Russian text asserted by tests. A Russian string quoted
from the running product is DATA: reproduce it verbatim, never translate it, and
make the English around it say that it is a Russian string in the product.

## The admin API contract — the main invariant

`api/openapi.json` (OpenAPI 3.1, 70 operations — method+path, not `paths`
keys) is **a build input, not documentation**: the frontend client is
generated from it by orval into `web/src/api/generated/` (gitignored,
edited only through `make ui-gen`), and
`internal/admin/openapi_contract_test.go` checks it against
`Server.routes()` in both directions and requires `csrfToken` on every
mutating route except login. That test checks routes and `csrfToken`,
never a schema or a description: schema drift has slipped through twice and
was caught only by a screen; a wrong description is caught by no bar.

**Added a handler — add the route to the contract, otherwise a red test.**
Routes live as one list in `Server.routes()`; `Handler()` only registers them.

**Every route is called from a reachable screen or is declared agent-only,
and that is a test, not a promise**: `web/src/api/coverage.test.ts`
enumerates the committed contract, pins the count (70) and scans `web/src`
for a caller; a screenless route earns an `EXEMPT` entry naming its MCP
tool as the only required caller (none today — the map emptied with `A20`); reachability itself is
`web/src/routes/routes.test.tsx` over the real route tree. **The agent is
PRIMARY: since `A4` (2026-09-01) a new route ships with its MCP tool and an
`EXEMPT` entry, never a screen**, on the owner's own words («UI вообще не
нужен делай только MCP», a Russian string quoted as data); the reverse cut —
a verb with a screen and no tool — has been forbidden since `P3a`. The owner
lifted the rule by name, in his own words each time, for `/guide` (`A7`),
the streaming screens (`P6e`), the «Файлы» tab (`A10`), the «Контракт»
tab (`P7b`) and, on 2026-09-05 (`A20`), everything that was left — first
the entity rows and the export/import/fork trio, then, on «добей последние
4 гэпа» (a Russian string quoted as data), drift, stream stats and the two
probes; the map is empty and the mechanism stays. An exemption and its
withdrawal are both decisions on the record, in the map's own comment.

The contract describes what the server **actually** does, not how it would
be pretty: `PATCH /api/workspaces/{id}` cannot unbind a spec, the directive
limit answers 400 not 413, `status`/`body` are optional on endpoint create,
`opKey` arrives already percent-encoded and is substituted as is. Do not
"fix" the contract toward expectations. The count's history, the full
`EXEMPT` roster and the A4 quotes in full: `docs/agent/contract-frontend.md`.

## Front end

`web/` — React 19 + **Mantine 9**, **TanStack Router** (file-based, code
generation) and **TanStack Query**, react-hook-form + arktype, orval,
oxlint/oxfmt, Sentry, dayjs. **There is no Tailwind.** The package manager
is **yarn 4 via corepack** (`corepack yarn …`; `cd web && …` in the
Makefile). Screens: `/`, `/specs`, `/guide` (calls no route) and
`/workspaces/$id` with ten tabs; `/w/$id` is gone. Generated and not in
git: `web/src/api/generated/`, `web/src/routeTree.gen.ts`. The only
hand-written file in `web/src/api/` is `client.ts`, the one `fetch` for the
whole application (CSRF header, `credentials: same-origin`, the error
envelope) — a screen that calls `fetch` directly silently loses the header.
`make build` does **not** rebuild the SPA, `make release` does; a tracked
`internal/webui/dist/.gitkeep` that `git status` shows deleted after a
build is restored by `make ui`. The remaining conventions (the session as a
route guard with `fetchQuery`, a root `data-testid` in all four states,
`{children}` only on success, `describeApiFailure` vs `…Detailed`):
`docs/agent/contract-frontend.md`.

## Hard rules

- **origin is GitHub** `git@github.com:yashok111/mocker.git`, branch `main`,
  PUBLIC since 2026-09-03 — the one repository of the owner's whose canon is
  not the private Gitea forge (that was origin until the same day; the
  module path moved with it). The history was restarted from one commit for
  publication: the pre-publication tree, 144 commits, is kept out of git as
  a bundle on the owner's machine.
- **`MOCKER_DEV=1` removes `Secure` from the cookie** — only for http on localhost.
  Never set it inside a corporate network.
- **The acceptance corpus is `internal/testspec/testdata/acceptance.json`**
  (~300 KB, 110 paths, 130 operations, 179 schemas): the structure-preserving,
  sanitised twin of the internal API document the project was built against,
  embedded since 2026-09-03 so `make test` is green on a fresh clone. It is
  what "the real corpus"/"the customer's spec" in older comments and in
  `HISTORY.md` refer to — the same shape, a neutral vocabulary. **Do not open
  it whole and do not `cat` it** — only `jq` by a concrete path.
- **A media type the browser executes is neither stored nor served — the rule is
  one and lives in `internal/httpx`** (`BrowserExecutableMediaType`), because
  both planes apply it: admin on write, mock when serving an already permitted
  type. There were two hand-written copies — and both let through, identically,
  `application/json,text/html`, which Go writes into the header as is, and the
  browser resolves to `text/html`. The rule is built on `mime.ParseMediaType`:
  an unparsable value is rejected, because there is no predicting what the
  browser will do with it. **Neither of the two guards asks about the variant's mode
  or about what produced the body** — both qualifiers were holes: `mode`
  changes with the next write, and a generated body does not need an override
  at all.
- **Everything that gets into a URL from a request goes through a whitelist.** The scheme is given by
  `httpx.ForwardedProto` (only `http`/`https`), the port by `httpx.RequestPort`
  (only a bare decimal number). `net.SplitHostPort` cuts at the last
  colon and returns anything at all **without an error**: `Host:
  mocker.local:9@evil.example` gave the port `9@evil.example`, and the assembled URL
  parsed with `evil.example` as the host. This is read-SSRF through the probe
  route, and a test holds it, not vigilance.
- Skills are not in git (`.agents/`, `.claude/skills/`), the repository holds only
  `skills-lock.json`; restore with `scripts/install-skills.sh`, not
  `skills experimental_install` (that one does not make the symlinks for Claude Code).
- **An agent does not edit `DESIGN.md`.** Every version from v4 to v10 was made by
  a human on an explicit request — **v10 (2026-09-01, §30, §31), v11 (§32) and
  v12 (2026-09-03, §34, API design — «внеси в правки Дизайн … что решили
  сейчас», a Russian string quoted as data) are what that rule
  looks like when it is SATISFIED, not an exception to it**: the owner asked for
  the non-goal to be lifted in his own words, chose the four behaviours, the
  authoring surface and the WebSocket dependency by answering direct questions,
  and the document records the decision as his. What stays forbidden is the other
  shape — an agent deciding a divergence is fine and editing the section it
  diverges from, in passing, alongside code. Note also that v10 shifted every line
  in `DESIGN.md` after §4 by **+35**, so a `DESIGN.md:NNN` citation written before
  2026-09-01 points 35 lines short; section numbers did not move, which is exactly
  why §30 was appended instead of inserted. the next divergence is closed the same way, never in
  passing along with the code. When the code and the document disagree, the code is
  wrong until a human says otherwise — and if the divergence is deliberate, record it
  in `CARVE-OUTS.md`, not by rewriting the section it diverges from. §23 is where the
  state as BUILT lives; §§1–22 are the INTENT and are the only record of why the code
  is the way it is.

## Where we are

Shipped, one slice per commit, `P0` (2026-08-16) through `A19` (2026-09-05,
`mock.generate` and the entity writers, on top of `A18`,
endpoint functions) — nearly fifty slices. The full list with one line
each, the ranked argument for what is NEXT and where each slice's
decisions live: `docs/agent/where-we-are.md` and `HISTORY.md`. Streaming
(§30), `P4` (drift, the bundle over HTTP, the fork) and §34 (API design)
are COMPLETE. `A18` has no DESIGN section: its authority is
`docs/A18-endpoint-functions.md`, the first gate document committed into
this repository — the seventeen earlier gate workspaces were outside it
and are gone, and anything that had to outlive them is in `HISTORY.md`.
Next, in order: `IDEAS.md`'s ranked backlog, then deeper nesting / `P2` UI
debt / `P5`, each with its cost written down.

## What is deliberately absent

Every hole in this tree is DEFERRED or DECIDED, never forgotten, and the full list —
per slice, with the measurement behind each — is `CARVE-OUTS.md`. Read it before
concluding that something missing here is a bug, and before a slice claims to close
one of them: several entries record an approach that was tried and REVERTED, with the
reason. The generator's one piece of open debt (`MaxBytes` is still not an absolute
ceiling) is at the end of that file.

## Environment

- The dev box the bars were tuned on is a 7.8 GB Linux VPS with swap already
  in use, and its kernel OOM killer has taken out the whole user session more
  than once. That is why `make test`, `make ui` and — since `P6c`, 2026-09-02
  — `make lint` go into a memory-capped systemd scope — see the `CAP` comment
  in the `Makefile`, it has the measured numbers and the explanation of why
  no cap is put on `docker`/`smoke`. `lint` joined last: an uncapped
  `golangci-lint` run right after a capped `make test` peaked at 5.8 GB and
  took the agent session with it, twice in one afternoon. Anything else
  heavy run by hand on such a box (`shellcheck` over `scripts/smoke.sh`, a
  second `go test`) goes through the same `systemd-run --user --scope -p
  MemoryMax=3G` wrapper, never bare. On macOS and in CI `CAP` degrades to
  nothing.
- Node `v24.17.0` via nvm; a non-interactive `ssh` session gets a PATH
  without the nvm bin, so an empty `which node` there is a PATH artifact,
  not a missing toolchain.
- The history before publication (144 commits, 2026-08-16 to 2026-09-03;
  `HANDOFF.md`, the per-slice predecessor of `HISTORY.md`, among the files
  deleted along the way) is NOT in this repository: it was restarted from
  one commit on 2026-09-03 and the old one survives as a git bundle on the
  owner's machine. Commit hashes cited in `HISTORY.md` and `CARVE-OUTS.md`
  belong to that history and resolve to nothing here.
