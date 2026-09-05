# The admin API contract and the front end, in full — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

## The admin API contract — the main invariant

`api/openapi.json` (OpenAPI 3.1, 70 routes) is **a build input, not documentation**
(and it has drifted from the code in SCHEMAS twice without a bar noticing,
both caught by `P6e`'s screens: the preview `kind` enum and the endpoint
conflict payload — the contract test checks routes and `csrfToken`, never
a schema):
the document here has already diverged from the code once, silently: P2f added
`POST .../preview`, the contract and `coverage.test.ts` moved to 47, and this place
stayed at 46 unnoticed — A1's `PUT .../endpoints/{eid}` brought the number to
48 and with it the occasion to fix the fact of the drift, not just the digit.
P3a's three resource routes (`GET /api/specs/{id}/resource-suggestions`,
`GET /api/workspaces/{id}/resources`, `POST .../resource-decisions`) brought it
to 51, P3b's `POST /api/workspaces/{id}/reset-data` to 52, P3f's
`POST /api/specs/{id}/rederive` to 53, and P4a's
`GET /api/workspaces/{id}/drift` to 54, `A4`'s
`GET /api/workspaces/{id}/resources/{family}/entities` to 55, and `P6a`'s
`GET /api/workspaces/{id}/traffic/stream` and `GET /api/stream/stats` to 57,
and `P6b`'s `POST /api/workspaces/{id}/endpoints/preview` to 58, and `P6c`'s
three `/api/workspaces/{id}/connections` operations (list, close, push) to 61, and
`A6`'s three asset operations (`PUT`/`DELETE /api/workspaces/{id}/assets/{name}`,
`GET /api/workspaces/{id}/assets`) to 64, and `A11`'s
`PUT`/`DELETE /api/workspaces/{id}/resources/{family}/entities/{key}` to 66,
and `P4b`'s `GET /api/workspaces/{id}/export`, `POST /api/workspaces/import`
and `POST /api/workspaces/{id}/fork` to 69, and `P7a`'s
`GET /api/workspaces/{id}/openapi.json` to 70 —
the population `coverage.test.ts` pins is the count of method+path
**operations**, not of `paths` keys, so a route sharing a path with an
existing one still adds one to the count.

- the entire frontend client is generated from it via orval into `web/src/api/generated/`;
  that directory is in `.gitignore` and is **edited only through `make ui-gen`**;
- `internal/admin/openapi_contract_test.go` checks the document against
  `Server.routes()` in both directions and requires every mutating route
  (except login) to declare `csrfToken`.

**Added a handler — add the route to the contract, otherwise a red test.** Routes
live as one list in `Server.routes()`; `Handler()` only registers them.

**Every route is called from a reachable screen or is declared agent-only, and that
is a test, not a promise.**
`web/src/api/coverage.test.ts` enumerates the routes from the **committed**
contract (not from the generated client — that one is in `.gitignore`, and on a tree without
`make ui-gen` an empty iteration would pass vacuously), pins the population at 70,
accepts any of the four orval symbols with a mandatory `(` and scans
`web/src` **minus tests, minus `src/test`, minus the generated code, minus itself**.
**The agent is PRIMARY and a screen is OPTIONAL — decided 2026-08-31, and it
changes what this test means.** A slice may ship a verb with its MCP tool and no
screen at all; the reverse cut is still forbidden, and that half is older — a
verb with a screen and no tool has been unacceptable since `P3a` shipped the
whole resource layer with zero MCP tools and left an agent unable to confirm,
decline or read a resource. The reason is that the second subject of this
product is an agent holding `MOCKER_MCP_KEY`, not only the human at the admin
screen, and `DESIGN.md` §14.2/§15 already treat the two as equals.

**`A4`, 2026-09-01, escalates that rule: a new admin route ships with NO
screen, superseding "optional" above.** "Optional" said a screen was not
required; it did not say one was refused. The owner said so directly, in his
own words, the same day: «тот ui что есть сейчас не трогай ... так что спеки
человек продолжит импортировать», and earlier in the same conversation «UI
вообще не нужен делай только MCP» — a Russian string reproduced verbatim as
the evidence for this attribution, not translated. From `A4` on, a new route
ships with its MCP tool and an `EXEMPT` entry here, never a React component,
a route file, or a `data-testid` — the 2026-08-31 paragraph above still
describes the invariant this test checks, but no future slice may read
"optional" as license to add a screen instead of an `EXEMPT` entry. A screen
already shipped is untouched by this and is repaired when the contract
breaks it — spec import (`/specs`) stays the one thing the existing UI still
owns. **Two later exceptions, both the owner's own words, neither a
relaxation of the rule:** `A7`'s `/guide` (calls no route) and `P6e`'s
stream authoring, browser test client and «Соединения» tab («сделай P6e»,
2026-09-02) — the one slice §30.15 always priced as a screen, and the rule's
own text said it waits on the owner lifting it for exactly that.

So the invariant now reads "every route is called from a screen **or is
declared agent-only**", and a screenless route earns an `EXEMPT` entry naming the policy
— not a silent gap, and not a probe: the map's first two entries were both
infrastructure probes until this rule widened it, and its own comment says an
exemption is a decision on the record. `/healthz` and `/readyz` are those two;
`GET /api/workspaces/{id}/drift` (P4a) was the first entry that is NOT a
probe, `GET /api/workspaces/{id}/resources/{family}/entities` (`A4`) is
the second, `GET /api/stream/stats` (`P6a`) the third, and
`POST /api/workspaces/{id}/endpoints/preview` (`P6b`) the fourth, and
`P6c`'s three `/api/workspaces/{id}/connections` operations the fifth to
seventh, and `A6`'s three asset operations the eighth to tenth, and `P7a`'s
`GET /api/workspaces/{id}/openapi.json` (`export_openapi`) the eleventh,
which `P7b` WITHDREW the same day (the «Контракт» tab renders exactly that
route; `EXEMPT` back to 10) — each names
its own MCP tool (`get_workspace_drift`, `list_resource_entities`,
`push_stream_frame`, `upload_asset`, …) as the only caller the coverage
invariant requires. `P6e` WITHDREW four of them — the preview and the three
connection operations — and `A10` the three asset operations, because
screens call them now; `A20` (2026-09-05) withdrew six more in one slice —
the entity read and its two `A11` write siblings (`ResourceEntities.tsx`,
«Записи» under a confirmed family) and `P4b`'s export, import and fork
(`TransferPanel.tsx` on the overview, the import modal on the workspaces
list) — on the owner's pick from a list that also offered drift and stream
stats, which he did not take that morning — and took the same day («добей
последние 4 гэпа», a Russian string quoted as data): `DriftPanel.tsx` on
the overview, the stats strip on `StreamConnectionsPage.tsx`, and the
header's server status (`AppShell.tsx`) over `/readyz` and `/healthz`, so
`EXEMPT` is EMPTY and the mechanism stays for the next agent-only route.
An exemption is a
decision on the record and so is its withdrawal, in the map's own comment. Reachability is not visible to the
static guard, it is checked by `web/src/routes/routes.test.tsx`, which mounts
the real route tree; screens are found by a root `data-testid` that is
rendered **outside** the four-state switch — a marker only on success
would make the check depend on whether the test also stubs the screen's
requests. Added a route — raise the number and give it either a caller or an
`EXEMPT` entry naming why no screen calls it.

The contract describes what the server **actually** does, not how it would be
pretty. Already caught: `PATCH /api/workspaces/{id}` cannot unbind a spec
(`*int64` collapses `null` and an absent field), on `POST .../session`
the directive limit answers 400, not 413, `status` and `body` are optional when creating
an endpoint, and `opKey` in `MergedOperationView` arrives **already
percent-encoded** (`url.PathEscape`) and is substituted into the path as is — orval does not
encode path parameters, and encoding again gives a 400. All of this is written down in
the field descriptions — do not "fix" it by bringing the contract in line with expectations. And remember that
the contract test checks routes and `csrfToken`, but **not descriptions**: an incorrect
description is caught by no bar, only by a human.

## Front end

`web/` — React 19 + **Mantine 9** (core/hooks/form/modals/notifications/dates/
dropzone, Tabler icons), **TanStack Router** (file-based routing, code generation)
and **TanStack Query**, **react-hook-form + arktype**, **orval**, **oxlint/oxfmt**,
Sentry, dayjs. Styling is PostCSS with `postcss-preset-mantine`. **There is no Tailwind.**
The stack is taken from another project of the owner's so that the toolchain is one across two repositories — but VTable and
the three `ajv` packages from there are **removed**: not a single file imported them (validation
went to arktype, and the traffic feed is a plain Mantine table, because VTable
does not measure itself under a DOM emulation — jsdom then, happy-dom now —
and the tests would assert nothing).

Screens: `/` (workspaces), `/specs`, `/guide` (the operator's manual,
`docs/USER-GUIDE.md` compiled in through a `?raw` import and rendered with
`marked` — no API call, so no contract entry; `A7`), and `/workspaces/$id` — a layout with ten
tabs (overview, endpoints, custom, traffic, scenarios, history, resources,
since `P6e` connections, since `A10` files, and since `P7b` contract). **`/w/$id`
is gone**: in path mode `/w/{slug}` belongs to the mock plane, and one
URL layout working in both modes is better than two that depend on a server
setting.

- **The test runner is vitest 5 on happy-dom** (2026-09-05; vitest 4 on
  jsdom before — the vitest 5 migration was uneventful for this tree: no
  `vi.mock` outside module scope, no `bench`, no unawaited `.resolves`,
  and `clearMocks` defaulting to true changed nothing). happy-dom differs
  from jsdom in two ways the tests met at once: it NAVIGATES on an anchor
  click and loads what a page links (shut off in `vite.config.ts`'s
  `environmentOptions.happyDOM.settings`, and `TransferPanel.test.tsx` spies
  `URL.createObjectURL` instead of replacing the `URL` class the navigation
  needs), and it ships a REAL `fetch` that resolves a relative URL against
  `http://localhost:3000` and connects — a React Query poll firing once more
  after a test's `vi.unstubAllGlobals()` reached the network as an
  unhandled `ECONNREFUSED`. `src/test/setup.ts` installs a no-network
  `fetch` baseline that rejects with a sentence; a test that wants a fetch
  installs one with `route()` from `src/test/http.ts`, and that baseline is
  what `unstubAllGlobals` restores to.
- **Bulk text goes into a field with `fill()` from `src/test/user.ts`, not
  `userEvent.type()`.** `type()` sends one event per character and React
  re-renders the screen after each; on the big editors that was the whole
  cost of a test — 25 characters into the operation editor's preview form
  took 1.4 s, a 201-rune label 0.6 s, 65 emoji into the login form 0.7 s
  (measured 2026-09-05). `fill()` is a click and one paste: those three
  came down to 0.65 s, 0.1 s and 0.1 s, `OperationEditor.test.tsx` from 9 s
  to 6 s and `CustomEndpointsPage.test.tsx` from 8 s to 5 s. `type()` stays
  where the test is about what happens per keystroke (a search filter, a
  `{enter}`, a validator that runs as you type) and in the small forms,
  where it costs nothing. The suite runs in worker threads (`pool:
  "threads"` — 44 s against 48 s with forked processes on the 4-core box;
  `isolate` stays on, because Testing Library's auto-cleanup registers its
  `afterEach` on first import and a shared module graph runs it for one
  file only, measured as 219 «Found multiple elements» failures).
- **The package manager is yarn 4 via corepack**, not npm. `corepack yarn …`
  (yarn 4 does not know `--prefix`/`--cwd`, in the Makefile it is `cd web && …`).
- Generated and not in git: `web/src/api/generated/`, `web/src/routeTree.gen.ts`.
  `yarn build` calls `yarn gen` itself, so a fresh clone builds.
- **The session is a route guard.** `beforeLoad` in `src/routes/_authed.tsx` decides
  "who is logged in" before the screen mounts. `ensureSession` calls `fetchQuery`, not
  `ensureQueryData`: the latter returns the cache regardless of `staleTime`, and a session killed in
  another tab would pass the guard with a stale CSRF token for as long as you
  like.
- **`internal/webui/dist/.gitkeep` is tracked.** `//go:embed dist` over a
  directory holding one dotfile does not compile, `all:dist` does.
  Vite's `emptyOutDir` erases the file on every build, `make ui`
  restores it — if after a frontend build `git status` shows a deletion,
  that is it.
- `make build` does **not** rebuild the SPA (the inner loop does not want that), whereas
  `make release` depends on `ui`. An old `dist` is now a UI built against
  **the old contract**, and nothing on the screen will say so.
- **A screen renders a root `data-testid` in all four states**, outside the
  switch. By it `routes.test.tsx` finds the screen after mounting the real
  route tree; a marker only in the success branch would make the reachability check
  depend on whether the screen's requests are stubbed in the test.
- **The workspace layout renders `{children}` only in the success branch** of its
  `useGetWorkspace`. Otherwise one failed request draws two alerts and two
  «Повторить» buttons — its own and the child screen's.
- **`describeApiFailure` deliberately does not show `err.message`** (it is written
  for mocker's logs), while `describeApiFailureDetailed` does. The second is
  needed where the server's message is exactly what the operator can act
  on: which spec format is unsupported, which status was rejected.
- The only hand-written file in `web/src/api/` is `client.ts`, the orval mutator:
  one `fetch` call for the whole application, the CSRF header, `credentials:
  same-origin`, parsing of the error envelope. A screen that calls `fetch` directly is
  a way to silently lose the header.

