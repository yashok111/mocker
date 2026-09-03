# mocker — context for the agent

A mock server on top of OpenAPI: imports a spec, serves each workspace on a host
(or on `/w/{slug}`), generates deterministic responses, records traffic, provides
an admin panel. Go 1.27.1, module `github.com/yashok111/mocker`, stdlib
`net/http` plus TWO libraries, each behind ONE importing package —
`github.com/coder/websocket` behind `internal/wsmock` and, since `A8`
(2026-09-02), `go.yaml.in/yaml/v3` behind `internal/yamlx` (a YAML document
becomes JSON bytes there and nowhere else; zero transitive modules, no cgo,
`boundary_test.go` fails on a second importer) — there is no framework and
there will not be one.
`DESIGN.md` §30.9 (v10) admitted the library for slice `P6d` (shipped
2026-09-02) on a written measurement: zero transitive modules, no cgo, no
runtime executable memory, context-based reads and writes. It is isolated
behind `internal/wsmock` the way `internal/jsonx` isolates `encoding/json`
and `internal/probe` isolates outgoing HTTP, and
`internal/wsmock/boundary_test.go` fails the build on a second importer.
The qualifier is "one library, one importing package", not "a framework".

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
`MOCKER_ADMIN_HOST`, mounted only when `MOCKER_MCP_KEY` is set. Not a
third plane, but a thin adapter over the admin plane. **Since v4 it is described in
DESIGN.md — §14.2, and the threat model around it in §15**; before that it was not
there at all. Three things about it cannot be
guessed from the code of the two planes above: it is mounted **outside the
CSRF/session chain** of the admin plane (`Server.Handler()` gives it a
separate branch before `webui.Handler`; authorization is a bearer key by constant-time
comparison, `/mcp` does not read cookies at all); it is **deliberately not part** of
`api/openapi.json` (that stays at 61 routes — there is nothing to type in JSON-RPC,
and `coverage.test.ts` and the CSRF contract test both exist precisely
for the routes FROM that contract, `/mcp` is not in their perimeter and must
not be); and fifty-four of its fifty-five tools reach the domain only through
`admin.Server.CallAsMCP` — an in-process call into the very same admin route table
under an MCP identity, never straight into `overrides.Repo`/`workspaces.Repo`
and so on, so as not to create a second validation path bypassing the handlers.
The fifty-fifth, `get_guide` (`A7`), reaches nothing: it returns the embedded
usage guide (`internal/guide`, below), and its `toolRoutes` row is empty.
The fifty-seventh, `get_server_config` (`A9`), reads `*config.Config`
directly — `config.Limits`, the same projection `ServerConfigView.limits`
carries to the panel through login and `GET /api/me`, so the two readers
cannot disagree — and its `toolRoutes` row is empty like `get_guide`'s.
The fifty-sixth, `import_spec` (`A8`), is the reversal of the one exclusion
`mcpAllowedRoutes` kept on a policy rather than a hazard — `POST /api/specs`,
which mocker-a4-mcp-reach D3 left to the `/specs` screen; the owner let it
in so an agent holding the spec file in its own repository needs no human
to paste it. `DELETE /api/specs/{id}` stays out: it cascades across every
bound workspace.
Tool details — `internal/mcp`, client config — `README.md`
("MCP").

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

The `Scenario` layer is `internal/bundle` (the codec of the v3 format §17: a snapshot
stores ROWS, not merge semantics — P2b overlays them by key, and P2c restores them
with the same bytes by the inverse rule, which is why the format has no `omitempty`
erasing the difference between "the row is not in the snapshot" and "the row is there,
switched off") and `internal/scenarios` (a repository over the `scenarios` table).
Composition is a PURE function called from `buildRuntime`
(`internal/mockplane/runtime.go`), not a branch inside it: the map merged by
key goes both into serving and into compiling the recipe sets, otherwise the
workspace and the scenario would diverge between what is served and what is
compiled. Activation is ONE column (`workspaces.scenario_id`) plus a
`revision` bump in one record; `routeCache` is already keyed by
`(workspace_id, revision)`, so the bump is enough to make the switch
visible — deactivation is technically the same operation, only with the value
`NULL`, not a separate route. Three settings do NOT enter the composition, and that
is exactly what cannot be guessed from the code: `basePath` (a scenario taken under a
different prefix would silently relocate every route — §12 ("a fork as a cheap undo") keeps the fork for
such a case deliberately inconvenient), the CORS policy and `notFoundBody` (both
are read past the assembled runtime — preflight and `serveNoRoute` take them
straight from `ws.Settings`, not from `rt.settings`, which is what the scenario would
substitute) — all three a scenario always carries as the workspace's own, even when active.

Since P2d the layer has two new verbs, and both deliberately GO AROUND what was
just described above. `POST .../scenarios` with a `from` field clones someone else's
SNAPSHOT with a single `INSERT … SELECT snapshot FROM scenarios WHERE id = ? AND
workspace_id = ?`: the clone does not re-read the `Workspace` layer at all, so
the A10 refusal ("a scenario cannot be created while another one is active") does not
apply to it — the snapshot is taken from an already existing row, not from live
state, so the question "which of the two snapshots would be lying" never arises.
`PUT .../scenarios/{sid}` renames and deliberately does NOT bump `revision`:
the `routeCache` key is `(workspace_id, revision)`, it does not hold on to the name, and
switching a scenario on the mock plane (`POST {prefix}/state
{"scenario":"<name>"}`) resolves the name LIVE through `ByName` on every request,
so a rename is visible there without a single bump — and for exactly that reason it
silently breaks any external test that hardcoded the old name into that
directive (details — `CARVE-OUTS.md`).

`internal/checkpoints` (P2c, P2d) — history and undo for the `Workspace` layer: list,
manual checkpoint, rollback, `reset-overrides`, retention, and since P2d — debounce and
deletion of a single row. Restore is
DELETE-then-UPSERT by the natural key (`settings` + `internal/overrides` +
`internal/customep`), not truncate-and-reinsert: the latter would strip a custom
endpoint of its id across a rollback (`GET /endpoints` holds it), and the order
DELETE-before-UPSERT within one operation is itself load-bearing —
`custom_endpoints` has a second unique index on `(workspace_id, method,
canonical_path)` (`0001_init.sql:211`), and an upsert before the delete fails the insert
on a snapshot like `GET /a/{id}` over a live `GET /a/{x}`. **`resources` and
`resource_decisions` — the two tables P3b adds to `config_snap` — are the deliberate
exception to that rule: their restore is UPSERT-ONLY, never DELETE.**
`entities.resource_id` is `ON DELETE CASCADE`, so a DELETE-then-UPSERT restore of
`resources` would cascade away entity rows a P3b-era checkpoint neither captured nor
restored — P3b itself never wrote `entities` at all, and `checkpoints.data_snap`
stayed an explicit NULL through P3b and P3c. **Since P3d it does not**: every
checkpoint capture now also snapshots the workspace's confirmed entity rows into
`data_snap` (`internal/checkpoints`' own `captureEntitiesTx`, below), and
`POST .../rollback/{cid} {restoreData: true, confirmSlug}` restores them — a
DELETE-then-INSERT of `entities` scoped per resolved `resource_id` — one family at
a time, never at the `resources` grain, so the DELETE cannot cascade past the
level it is scoped to — which is true of that statement's `WHERE`, not of the
schema in general, and stays true only while `entities.parent_entity_id` is NULL
on every row, which P3e (D9) decided and P3g (D9, D10) re-argues on the same
evidence at a chain up to three families deep: a live column would let this
same DELETE cascade into a family the call never named — a DESCENDANT one,
not necessarily a sibling, because at depth the family it reaches can sit two
levels below the one being restored, and that blast radius grows with the
chain rather than staying one family wide.
Entity rows carry no id an external table holds onto the
way `custom_endpoints`' id does, so unlike that table there is nothing here for
a DELETE-then-reinsert to strip.
The natural-key rule above still governs `settings`/`internal/overrides`/
`internal/customep`, and `resources`/`resource_decisions` stay UPSERT-only exactly
as P3b made them. Two consequences follow from THAT half and an operator still
hits both, now each conditioned on `restoreData`: a family CONFIRMED after a
checkpoint is not undone by a `restoreData: false` rollback to it (the confirm's
`resources` row has no matching row in the older snapshot to delete, and
UPSERT-only never deletes what it does not find) — `restoreData` does not change
this, since it governs `entities`, not `resources`; and a family DECLINED after a
checkpoint comes back CONFIGURED and EMPTY on a `restoreData: false` rollback
(the older snapshot's `resources` row is restored, but the entity rows the
decline deleted stay gone — a rollback restores the switch, never the data
behind it, on this path) while a `restoreData: true` rollback to the SAME
checkpoint restores both the switch and the rows the decline deleted, decoded
straight from that checkpoint's own `data_snap`.

Restore runs as **one transaction for the whole restore** — a declared
exception from `DESIGN.md` §18 ("large operations are cut into transactions of
N rows"), not an oversight: its price is a QUEUE on the single writer
connection (`store.go:74`), not data corruption; the expensive half (read,
encode, gzip) is already moved OUT of the transaction; and chunking would
trade the atomic undo — the reason the whole package exists — for a
ceiling that real load does not reach. **Before P3a** this queue was invisible
to the mock plane: every mock-plane read went through the reader pool, which
WAL lets run in parallel with the writer, so a long restore never blocked
serving. **P3a makes that false for two ordinary mock-plane routes**: `POST X`
and `DELETE X/{}` on a confirmed resource write through `EntityStore.Create`/
`Delete` — the same single writer connection a restore holds for its whole
duration — so those two routes now queue behind a restore in progress exactly
as an admin write would; `GET X` and `GET X/{}` still read through the reader
pool and are unaffected. If this ever
reads as a bug — read this, do not "fix" it. **P3b's `POST .../reset-data` is a
second hold of the same shape**: like restore, it is one transaction with no row
chunking (the `reset-data` chunking carve-out in `CARVE-OUTS.md` says why), so it now sits on the
writer connection exactly as a restore does, and the two ordinary mock-plane routes
above queue behind a `reset-data` in progress on the identical terms they queue
behind a restore.

`checkpoints.config_snap` stores the same v3 `internal/bundle` document as
`scenarios.snapshot`, but NOT the same bytes: a checkpoint is gzip, a scenario is raw
JSON. `DESIGN.md` §13 (the comment above `checkpoints`) itself puts the load
at "fifty gzips of the whole layer", so compressing checkpoints is following
the spec, not improvisation;
recompressing already shipped P2b scenarios into the same format is a migration this
slice does not take on. The cap on the decoded document, `maxSnapshotBytes`
(a package `var`, not a `const`, 8 MiB — nothing in the tree caps a
workspace AS A WHOLE, only individual fields), stands at BOTH ends: on write a
checkpoint whose snapshot exceeds it is rejected BEFORE the INSERT (otherwise a workspace whose
history once crosses the cap loses both rollback and reset at once —
both first take a pre-destructive snapshot of the current state); on read the
gunzip reads no more than `maxSnapshotBytes+1` bytes (`io.LimitReader`) and explicitly
refuses on excess, rather than silently truncating a tail which may otherwise
remain valid JSON.

`internal/checkpoints.Repo.Auto` is the third of the three triggers that
`DESIGN.md` §13 asks for verbatim ("by debounce, always before a destructive
action, and by the button"): a `kind = "auto"` row before nine marked
admin routes, no more than once per `MOCKER_CHECKPOINT_DEBOUNCE` seconds per
workspace (`0` — disabled, not a single row). The window is checked TWICE — by a reading
query before `captureSnapshot` (so that a suppressed call costs one index
SELECT, not a snapshot and a gzip) and by the same condition again inside the writing
transaction right before the insert itself, because two concurrent mutating
requests that passed the first check separately would otherwise each write a row.
The wrapper sits on `routeMux()` (`internal/admin/route_table.go`) —
the only place where there is data for every route — and not inside the
handlers, and exactly for that reason `CallAsMCP`, which dispatches through THE SAME mux,
passes through it too: two MCP tools (operation override, override from traffic)
have left an auto checkpoint behind since that slice as well, even though not a single file under
`internal/mcp` changed (details — `CARVE-OUTS.md`). A failed
auto checkpoint does not fail the request — only a log; there is nothing to cancel it with, because
`fenceTx` refuses an insert whose snapshot is already stale, rather than replaying
the capture.

`DELETE /api/workspaces/{id}/checkpoints/{cid}` is the inverse verb: it deletes
one row by id, of any `kind`, including the newest and the last remaining one —
an empty history is legal, every workspace starts with one. It does not take its own
pre-destructive snapshot before itself and does not bump `revision`: the mere fact of
deleting a history row changes none of the layers the snapshot describes.

`internal/resources` (P3a) — the `Session`-layer sibling nothing else in this tree
has: the first state a mock REMEMBERS. Four tables that have existed unused since
P0 finally carry rows — `resource_suggestions` (derived once at import by
`internal/specs/derive.go`, from the predicate `internal/router.ListFamily`
already computes: a family is a canonical `X` where `GET X` and `GET X/{}` both
exist and `X` itself has no `{}` segment), `resource_decisions` (the per-workspace
confirm/decline, one row per family), `resources` (the materialized switch) and
`entities` (the rows themselves). **There is NO migration: all four tables are
P0's**, which is why a slice this size adds no `0003_`.

The lifecycle is ONE route, `POST /api/workspaces/{id}/resource-decisions`, and
each transition is one transaction. A confirm generates every body FIRST — the
same `gen.Body` path that serves a detail route today, so the population is the
generator's own output with the id forced — and only then opens the writer, which
is why the transaction FENCES what generation read: workspace identity
(`created_at` + `slug`, the pair `internal/checkpoints` fences on), `spec_id`,
`scenario_id`, `settings.seed` and the effective `listSize`. A mismatch answers
`409 stale_config` and writes nothing. A bare `revision` bump is deliberately NOT
a refusal: `revision` moves on an anonymous `POST {prefix}/state`, so fencing the
counter would let any client make an operator's confirm fail on demand.

Serving is a BRANCH between `resolveVariant` and `assembleResponse` (never a third
variant mode — the carve-out in `CARVE-OUTS.md` says why), so `route_off`, the session
directive, the pause, the delay and the 406 gate are all inherited unchanged and
the branch sits AFTER them. Which verbs it takes over is decided PER METHOD, not
per family: both `GET`s always, `POST X` only when `write_form = "bare"`, `DELETE
X/{}` only when the spec declares that route. `internal/mockplane` holds
`internal/resources` as two interfaces plus two setters (`SetResources` for the
runtime-cached configuration, `SetEntities` for the per-request store) — the same
shape `SetCustomEndpoints` already had, and for the same reason: that package
imports `internal/store` in not one production file.

**Nothing about a confirmed resource's CONFIGURATION is editable** (its
rows are, since `A11` — see below). No `PUT` on the resource, no `repopulate`, no
fifth `edit_version` table, no `409 edit_conflict` on this surface. A resource
that is wrong is declined and confirmed again — and a decline that destroys rows
requires `confirmSlug`, checked inside the same transaction as the delete, because
entity data is state an anonymous caller can create and, since P3d, the only
recovery is a checkpoint taken before the decline: `POST .../rollback/{cid}
{restoreData: true}` (its own `confirmSlug`, see `internal/checkpoints` below)
restores the rows a checkpoint's `data_snap` carried, nothing a decline's own
transaction offers on its own.

`POST /api/workspaces/{id}/reset-data` (P3b) is the operator's other way to touch
entity rows, and it is workspace-wide, not per-family: `reseed` repopulates every
confirmed family from the workspace's CURRENT configuration (never from what a
`resources` row was confirmed against — there is no baseline column to read one
back from), deleting and reinserting the rows of each family it manages to
repopulate and leaving a family it SKIPS untouched (`stranded` — no suggestion
answers its route anymore; `over_caps`; `population_failed`; and since P3e,
`group_skipped` — four closed reasons on the wire, the fourth is a nested
family's own, see below); `clear` deletes every entity row of the workspace and
mints nothing back. Both require `confirmSlug` and neither is undoable BY ITSELF, same
as a decline — and since P3d, the same recovery applies: a checkpoint taken
before either ran, restored with `POST .../rollback/{cid} {restoreData: true}`
(its own `confirmSlug`, see `internal/checkpoints` below), brings the rows back
from that checkpoint's own `data_snap`; neither verb's own transaction offers
that on its own. A partially skipped `reseed` still
reports `changed: true` when it deleted or inserted at least one row; a `reseed`
that skips every family, or a `clear` over a workspace with no entity row, reports
`changed: false` on the same 200 a successful call answers. `clear`'s DELETE is
scoped to the whole workspace's `resources` roster read live inside the write
transaction, not to a roster read earlier, so a family confirmed between the two
cannot keep its rows behind the mode's own promise. Like a checkpoint restore,
`reset-data` runs as one transaction with no row chunking, so it now holds the
single writer connection the same way a restore does — see `internal/checkpoints`
above for what that costs `POST X`/`DELETE X/{}` on a confirmed resource while
either is in progress.

Since P3e a route family can be NESTED, and since P3g the depth is not fixed at
one: a family's depth is the count of `{}` segments in its canonical path, and
`maxNestingDepth = 3` (`internal/specs`, enforced in exactly ONE place —
derivation — and nowhere else: a family deeper than the ceiling is never
suggested, so it is never confirmed, so nothing downstream ever sees one) is
the smallest ceiling at which derivation is a LOOP rather than a hard-coded
special case run once — `/orgs/{}/teams/{}/users/{}/badges` runs the same
derivation pass, confirm walk, anchor walk and reseed group a one-level family
does, and a family one level past the ceiling still derives nothing, the
identical negative control P3a and P3e both already pinned one level shallower.
The parent link stays `router.ParentFamily` (`internal/router`, the one owner
of the parent string, a pure function of a family's own key, never stored,
because a stored copy would be a second source of truth `internal/specs`,
`internal/resources` and `internal/mockplane` would each have to keep in sync
by hand); `router.ParentFamily` returns the prefix before the LAST `{}`
segment, so an ANCESTOR CHAIN — the ordered families obtained by applying it
repeatedly, root first — needs no new string function. Derivation is a BOUNDED
LOOP of `maxNestingDepth + 1` passes over the same operations — pass 0 accepts
a family with no `{}` segment, pass k accepts a family with exactly k `{}`
segments whose `router.ParentFamily` was derived by pass k−1 specifically (not
merely by some earlier pass — the stricter form is what makes each pass's own
invariant checkable) — so `/orgs/{orgId}/departments` still derives nothing
when `/orgs` is absent from the spec, and a chain broken one level ABOVE the
immediate parent (`/regions/{}/cities/{}/streets` with `/regions` absent, its
parent `/regions/{}/cities` otherwise a legal shape) derives nothing either —
the control that separates a loop checking the WHOLE chain from one checking
only the immediate parent.
**A scope is the ordered tuple of a nested family's outer parameter VALUES**,
encoded as `resources.ScopeKey` (`internal/resources`, a defined string type so
`EntityStore`'s four methods cannot swap it silently with an adjacent
`string`): each value `url.PathEscape`d and joined with `/` (`EncodeScope`, the
one owner of that join, called by confirm, reseed and every request alike — a
site that inlined its own `strings.Join` would be a second encoding a UNIQUE
index could disagree with, and this stays true at any depth — no second
encoder and no inverse: every consumer builds a scope from raw values it
already holds, never by decoding a stored `scope_key`). A top-level family's
scope is the empty tuple, which encodes to `""` — exactly the literal every
row already carried, so no row in the database moves; a depth-2 family's scope
is a two-value tuple, the storage format does not move either. Serving
computes the scope from the ROUTE that MATCHED, positionally off `route.Path`'s
own ordered pattern, the identical discipline `router.DetailIDParam` already
uses — never a by-name lookup through the confirm-time `resources.scope_params`
column, which would silently serve a family HALF the moment its collection and
detail routes spell an outer parameter differently. **Confirming a nested
family requires its IMMEDIATE parent to be CONFIRMED already**
(`409 parent_not_confirmed`, read inside the same transaction `Confirm` already
fences workspace identity in) **and declining a parent with a confirmed child
is refused** (`409 child_confirmed`) — both checks stay SINGLE-HOP at every
depth, on purpose: the two together prove by induction that a confirmed family
at any depth has a confirmed family at every level of its ancestor chain (a
depth-0 family's chain is empty; a depth-k family confirms only while its
depth-(k−1) parent is confirmed, and a confirmed parent cannot lose its
confirmation under a confirmed child), so a THIRD check walking the whole
chain at confirm time would be a second enforcement of the same invariant and
a second place for it to disagree with itself — there is no repair path
because there is nothing to reconcile. Population is per scope TUPLE — one row
set per LIVE ancestor tuple, not per parent row alone: a top-down walk starting
from the empty root tuple reads each level's live entity keys under the
tuple built so far, extends it, and repeats to the family's own depth, so a
depth-2 family gets one row set per (root key, middle key) pair rather than
one per middle row — generated before the transaction opens exactly as a
top-level family's is, with `entity_key` continuing ONE counter across EVERY
scope of the family, at every level (never restarting per scope, and never per
level either) because `resources.seq`, a checkpoint restore's MAX rule and
`EntityStore.Create`'s next-key allocation all assume one counter per family —
a `POST` into a freshly created parent row's scope answers an empty collection,
permanently, until something writes into it; nothing populates a scope lazily.
Serving a nested family's four verbs (`GET`, `GET/{}`, `POST`, `DELETE/{}`)
first resolves the request's scope and, before touching storage, WALKS the
whole ancestor chain top down — not the immediate parent alone, which is the
wrong implementation a reader is most likely to write at depth: a live
immediate-parent row says nothing about whether the GRANDparent row that
scopes it still exists — and refuses at the FIRST ancestor whose key does not
anchor a live row, naming that OUTERMOST missing id (with `/orgs/7` deleted,
`GET /orgs/7/teams/5/users` says entity `"7"` is missing, not `"5"` — the true
cause, not the one closest to the request) with the family's own
`404 entity_not_found`, the same the parent's own detail route gives, on all
four verbs, not just reads. `POST /api/workspaces/{id}/reset-data`'s `reseed`
treats a whole confirmed SUBTREE — a root family and every confirmed family
below it, transitively, not merely a parent and its direct children — as ONE
GROUP: repopulated together or skipped together (`group_skipped`, the fourth
skip reason) — a grandparent and parent reseeded while a grandchild is skipped
for its OWN reason (`over_caps` is likelier for it: population multiplies
`parents × listSize` once PER LEVEL) would stand under OLD scopes, anchoring to
a DIFFERENT ancestor row of the same key the moment new ones are confirmed
again; each level's re-scoping consumes the level above's own freshly minted
keys, never a live re-read of rows the reseed is about to delete.

**`entities.parent_entity_id` and `resources.parent_id` stay NULL — a declared
DIVERGENCE from `DESIGN.md:505-508`'s physical `ON DELETE CASCADE` nesting,
argued on evidence rather than taste since P3e and RE-ARGUED, the same way, at
the full three-level depth by P3g.** The design's own warrant is two failures
joined by "and": records stay orphaned AND resurrect when a row of the same id
is created again. The second half is closed on every LIVE write path already —
`entity_key` never reissues through the counter, `POST` cannot mint a key a
checkpoint restore has not already raised past — except ONE `restoreData: true`
rollback sequence that re-inserts an ancestor's old key VERBATIM and can make
an orphaned descendant scope reachable again against a different ancestor row
of that id; the cascade WOULD close that one sequence, but only by reopening
the larger guarantee P3d shipped
(`TestRollback_dataLeavesALiveFamilyAbsentFromTheDocumentUntouched`): a live
`parent_entity_id` on `entities` would let `restoreEntitiesTx`'s own
`DELETE FROM entities WHERE resource_id = ?` cascade into a family's rows that
call never named and step 4 never recreates. At depth this trade is stronger,
not weaker: the blast radius a live cascade would open grows with the chain (a
config rollback naming one root would cascade through every level below it),
while the anchor walk above closes the orphan half OBSERVABLY at every level
of the chain, not just one. The orphan half is closed observably instead of by
a delete — the rows persist, unreachable except through that one rollback
sequence, until a decline or a `reset-data` removes them; `entityCount` on the
wire counts them regardless, so the admin screen (never a mock-plane client)
can tell an orphan from a live row. And `resources.parent_id` would
additionally be the wrong SHAPE regardless of the cascade question: a raw
`resources.id` does not survive the decline-then-reconfirm repair this project
prescribes for a wrong resource, the identical argument `ref`'s own addressing
(below) already made.

Since P3h a `basePath` may itself carry a `{param}` — a declared DIVERGENCE
from `DESIGN.md` §7, whose own example (`"basePath": "/api/v1"`) is a
literal prefix throughout, never a parameterised one; `settings.basePath =
"/orgs/{orgId}"` — and `settings.BasePathValues` (`internal/domain`, a field
`DESIGN.md` does not describe) DECLARES which values that parameter may
take: each element is the parameter's raw
values joined with `/` in declared order (one component per base-path
parameter), the same shape `resources.EncodeScope` already imposes on a
nested family's own scope, because a declared element becomes a stored
`entities.base_scope_key` by that identical split. `router.BaseParamIndexes`
is the ONE place "which segments of a base path are parameters" is decided
(`internal/router`, a leaf package `internal/domain` imports rather than
re-scanning braces itself) — `domain.ValidateBasePath`/`ValidateBasePathValues`
read it to refuse an unbalanced brace, a base parameter colliding with a
route parameter of the bound spec, or a `basePathValues` element of the wrong
arity, on BOTH writers (`PATCH /api/workspaces/{id}` and MCP's
`update_workspace_settings`) before a bad shape ever reaches a confirm;
`internal/mockplane`'s base-scope reader, `internal/admin`'s `stripBasePath`
and `internal/mockplane`'s `authCheckPath` all read the SAME function rather
than each parsing the brace on their own — a second local scanner anywhere
is the defect the one-owner rule exists to prevent, and it is why
`stripBasePath` (building an endpoint from recorded traffic) and
`authCheckPath` (recognising the auth-check request traffic recording
suppresses) went from a literal/segment-by-segment prefix comparison, silently
wrong the moment `basePath` carried a parameter, to a segment-wise one where a
base parameter segment matches any single segment. `entities.base_scope_key`
(migration `0003_base_scope.sql`, the first since `0002`) rides beside the
existing `scope_key`, is read POSITIONALLY off the request's own path
segments exactly as a nested family's own scope is (never out of
`router.Match.Params` by name — the identical discipline, applied one layer
outward), and partitions every one of `EntityStore`'s four methods: a
confirm populates one row set per (declared base value × live ancestor
tuple) rather than one set total, with the ancestor walk for a nested family
itself reading rows WITHIN the base value it is populating, never across
the declared set; serving resolves the request's base scope before touching
storage and refuses a value `BasePathValues` does not declare with the
family's own `404 entity_not_found` on all four verbs, the identical refusal
an unanchored nested scope already gives, positioned in `resourceBranch`
right after the pinned-response/session-force exits it must never preempt,
never ahead of them; an empty `basePathValues` against a parameterised
`basePath` refuses the CONFIRM itself (`409 base_scope_undeclared`) rather
than producing a family with zero rows. A confirm's own fence
(`fenceConfirmTx`) widens to compare `basePath`/`basePathValues` against
what generation read, UNCONDITIONALLY — the same branch `seed`/`listSize`
already fence in, beside `!scenarioID.Valid` — so a declared-set edit racing
a confirm is caught inside the transaction, not only by the pre-transaction
read a scenario-active fixture would otherwise let slip past. Composition
carries `BasePathValues` as the FOURTH field `composeScenarioLayer` restores
from the workspace, beside `basePath`, CORS and `notFoundBody` — an active
scenario never changes which values are declared, the identical exemption
those three already had — and `POST .../reset-data`'s `reseed` reads the
WORKSPACE's own declared set, never a scenario's, the same distinction its
`seed`/`listSize` arithmetic already draws one field over. A `ref`
(`internal/mockplane/ref.go`) resolves within the SERVING request's own base
scope, not the empty one P3c and P3e both left it at — the identical
per-family exemption for scope filtering the `ref` carve-out in `CARVE-OUTS.md` already
states, now closed for the base half alone. A checkpoint capture/restore
carries `base_scope_key` at `DataVersion = 2` (`internal/bundle/data.go`);
a `DataVersion = 1` document — every checkpoint taken before this slice —
still restores, every row landing in the empty base scope, exactly where it
already was. `MOCKER_MAX_ENTITIES` (`internal/config`) is now the value
`resources.Repo`'s population cap actually reads, through BOTH of its two
production constructions (`cmd/mocker/main.go`'s and
`internal/admin/server.go`'s separate `*resources.Repo`, over the one
`*store.DB` — main.go's own comment already says why they are two) — the
constant it replaced was `1000`, so the config default drops from the
`10000` it advertised (and nothing read) to the `1000` the code has always
enforced, closing a silent tenfold rise for anyone who wired the variable
without also raising the number. `resources.parent_id` and
`entities.parent_entity_id` stay NULL exactly as the nesting paragraph above
decided: a base scope is not a parent row, and this slice adds no cascade
and gives that question no new evidence.

Since P3f `resource_suggestions.gen` — in the schema since P0, empty of
meaning until now — carries more than one row set per spec: `POST
/api/specs/{id}/rederive` (`internal/specs.Repo.Rederive`) re-runs the exact
`deriveSuggestions` walk `Import` and the lazy backfill already run, over
the spec's stored NORMALIZED document, and writes the result as a NEW
generation only when it differs from the current newest one — the
comparison is over the WHOLE row tuple (name, id field, entity schema,
wrapper, confidence, and the sentinel row an empty derivation writes), never
the family name alone, so a rule change that only renames a family or only
widens its schema still mints a generation. The read side gained exactly
ONE new predicate for it, written in exactly one place —
`listSuggestions`'s own `gen = (SELECT MAX(gen) ...)` — and inherited by
`internal/resources`' `findSuggestion` with no code of its own, because a
second hand-written `MAX(gen)` anywhere else is precisely the divergent copy
that one-place rule exists to prevent. `added`/`removed` on the wire are the
diff of two GENERATIONS, sentinel excluded, never a statement about what any
workspace has confirmed or declined: a family `removed` from the newest
generation may still be confirmed somewhere, and the next `reset-data`
reseed reports it `stranded`, the same fourth skip reason P3e's nested
families already gave that word.

The verb is spec-scoped and stays that way on purpose: derivation is a pure
function of a spec's own document (`deriveSuggestions`'s own signature — a
resolver, the operations and the responses, nothing a workspace owns), so
one call changes what EVERY workspace bound to the spec sees, and the
handler resolves no workspace at all — it touches no `resources`,
`resource_decisions`, `entities`, `checkpoints`, `scenarios`,
`op_overrides`, `custom_endpoints` or `traffic` row, bumps no workspace's
`revision`, and takes no auto checkpoint (the route joins
`autoCheckpointExcludedNeverTouchesLayer`, not the labelled set — a
checkpoint is per-workspace and this route names none). Triage of what a
new generation means for an already-confirmed family — an `orphaned[]`
field, a migration verb — is deliberately not this slice's: that is `P4`
part one, ranked next in "Where we are" below, and what an operator has
until then is the `stranded` reseed classification and the `added`/`removed`
counts themselves.

Derivation runs OUTSIDE the write transaction — loading and walking a
document under the single writer connection would hold it for the whole
parse, the same reasoning `Import` and the lazy backfill already follow —
so the newest generation is read once over the reader pool BEFORE
derivation, and re-read a second time INSIDE the write transaction; a
mismatch between the two answers `409 stale_generation`
(`specs.ErrStaleGeneration`) and writes nothing, the same fencing shape
`internal/resources`' own `fenceConfirmTx` already keeps — chosen over
letting the UNIQUE `(spec_id, gen, route_family)` index fail on its own,
because a concurrent call minting a HIGHER generation than either read saw
is a case that constraint cannot see at all. `MOCKER_SUGGESTION_RETENTION`
(`internal/config`, default 3, "0 means keep every generation" — the same
shape `MOCKER_CHECKPOINT_RETENTION`'s own 0 already uses) prunes the oldest
generations inside the SAME transaction as the insert, and only on a call
that actually wrote one: a `changed: false` call has nothing new to justify
pruning history for.

`rederive_suggestions` is MCP tool 43 (`internal/mcp`): no `confirmSlug`,
unlike the two destructive resource tools beside it — the route it wraps
resolves no workspace and destroys no workspace-created data, so there is
nothing to confirm. The screen is a per-row button on `/specs`
(«Пересобрать ресурсы»), behind a confirm modal that names the
cross-workspace blast radius before the call goes out, and a success
notification that reads the response's own `generation`/`added`/`removed`
rather than a fixed string («Изменений нет» on `changed: false`); because
the roster it changes is rendered per-workspace and `/specs` holds no
workspace id to build a `getListWorkspaceResourcesQueryKey` from,
invalidation runs by PREDICATE over every open `.../resources` query key
rather than by one built id, so a rederive triggered from `/specs` does not
leave a second open workspace tab's roster stale.

`internal/recipes`'s tenth kind (P3c), `ref`, is the point where a GENERATED body
(never a confirmed resource's own — D8 below) can carry a value a confirmed
resource really holds: a `/quizzes` field whose `subjectId` is an id
`internal/resources` actually stores for `/subjects`, instead of a plausible
integer that matches nothing. The seam is a second typed func value beside
`Faker` (`recipes.Ref`), for the identical reason: `internal/recipes` is a LEAF
and may import neither `internal/gen` nor, one layer further down,
`internal/resources` (which itself imports `internal/gen`), so the resolver is
handed in, never imported. The resolver itself — `internal/mockplane/ref.go`,
`Plane.newRefResolver`/`Plane.resolveRef` — is CONSTRUCTED by `serveGenerated`,
from `rt.resources`, `p.entities` and the request's own context, and RECEIVED by
`assembleResponse` as a parameter; `Preview` passes `nil` rather than build a
second, live one — the identical construct/receive split `internal/mockplane`
already keeps for why it never reaches `livestate.Apply`. A request-scoped
closure MEMOIZES one `EntityStore.List` per family, shared by both `Generator.Body`
and `Generator.Headers` (the same `gen.Request`), so a ref bound at every
position of a 200-row list costs one query, not two hundred; the family lookup is
a direct hit on the SAME `route_family`-keyed roster map `resourceBranch` already
reads, which is what keeps a reference inside its own workspace — `EntityStore.List`
itself is not workspace-scoped, so anything OTHER than this request's own roster
turning a family name into a `resource_id` would be one forgotten check away from
serving another workspace's rows on a plane that is unauthenticated by design.
Every reason a reference fails to resolve — no store wired, the family absent from
the roster, no rows, a non-scalar property, a coercion failure, a store error —
answers to ONE policy, read off the recipe itself: `generate` (the default) falls
through to the ordinary generated value and marks the response `ref_unresolved` in
the traffic (`internal/mockplane/traffic.go`, joined with `pause_refused` rather
than overwriting it — the first note this plane ever had to combine with another);
`set-null` emits JSON `null` and marks nothing, because it did exactly what the
operator asked. `restrict` — DESIGN §9's third policy, "refuse the response" — is
declared out of scope: a refusal would have to leave `internal/gen` through
`assembleResponse`'s one seam and decide what status code an unresolvable
reference deserves on a plane whose whole contract is that it always answers, so
`Validate` refuses the token by name instead, at write time, rather than silently
downgrading it to `generate`. **A confirmed resource never evaluates a `ref` at
all — not at confirm, not on a `reset-data` reseed, and not when SERVING its own
route**: `internal/resources/populate.go` builds its `gen.Request` with no
`Recipes` field, and `internal/mockplane/resource.go` never calls `gen.Body` in
the first place (D8) — a `ref` is therefore useful precisely on the operations a
resource does NOT serve, never inside a confirmed family's own schema.

`checkpoints.data_snap` (P3d) is the column that has existed since `0001_init.sql`
and held no byte through P3a, P3b and P3c: this slice makes it real, as one seam of
three parts that ship together — capture, restore, screen. **Capture** is
`captureEntitiesTx` (`internal/checkpoints/capture.go`), called from all FOUR of the
package's write paths (`Create`, `Auto`, `Rollback`, `Reset`) after `fenceTx` and
before the checkpoint row itself is inserted: it reads every confirmed family's
entity rows, encodes them through the sibling of `internal/bundle`'s existing
codec — `DataBundle`/`FamilyEntry`/`EntityRow`, `internal/bundle/data.go`,
`DataVersion = 1` — gzips the result the same way `config_snap` already is, and on
either of two distinct overflows (the probe over `maxDataProbeBytes`, or the
codec-side refusal `compressSnapshot` already carried for `config_snap`) writes
`data_snap` NULL rather than failing the checkpoint outright: a workspace's
history staying available matters more than one row's entity data being
recoverable, and the checkpoint's `config_snap` half is unaffected either way.
**Restore** is `restoreEntitiesTx`, reached only on `POST .../rollback/{cid}
{"restoreData": true, "confirmSlug": "<slug>"}` — `ConfirmSlug` is required
exactly when `RestoreData` is true, the same shape `decideResourceRequest` and
`resetDataRequest` already require one for, because a `restoreData: true` rollback
DELETEs a resolved family's current entity rows before reinserting the checkpoint's
own, the same irreversible-without-a-checkpoint shape a decline already has. A
family is resolved by `route_family`, never by `resources.id` — the identical
reason `ref` addresses a family that way (below): an id does not survive
decline-then-reconfirm, and the natural key has to be resolved through THIS
workspace's own roster to become one at all. `restoreData` false or absent (the
default, and every pre-P3d caller) changes no entity row — the whole surface
`POST .../rollback/{cid}` already had stays exactly as it was. **The screen**
renders the checkbox and the `confirmSlug` control DESIGN §14 always drew and this
build never had: `HistoryPage.tsx`'s rollback modal disables the checkbox on a row
whose `hasData` is false (`checkpointSummaryView.HasData`, projected from
`checkpoints.Summary.HasData`, itself `data_snap IS NOT NULL`), refuses to submit a
checked box with an empty or mismatched slug, and sends both `restoreData` and
`confirmSlug` in the same POST body the two resource verbs already send theirs in.

`GET /api/workspaces/{id}/drift` (P4a, `internal/admin/drift_handlers.go`) is a
READ-ONLY report over the workspace's CURRENTLY bound spec and nothing else —
there is no baseline column recording what a workspace was configured
against, so a re-bind (`PATCH /api/workspaces/{id}`) and a `rederive` that
drops a family from the same spec's newest generation are deliberately not
distinguished; both leave a stored row the current spec no longer answers
for. Three signals, one predicate each: an `op_overrides` row whose
`(method, path)` no operation of the bound spec produces — compared
LITERALLY, never through `router.CanonicalPath`, the identical rule
`lookupOverride` already applies on the serve path, so a report using a
wider key would name rows the serve path does not in fact treat as
stranded; a confirmed `resources` row whose `route_family` no suggestion of
the bound spec's newest generation names — `resources.OrphanedIn` is now the
ONE function that predicate is written in, and it is PURE: a suggestion list,
a roster, a map, no context and no error. It has two doors and which one a
reader takes is decided by whether it already holds the list.
`resources.Repo.OrphanedFamilies` is the fetching door — one `specID`, the
whole roster, one round trip — and it delegates rather than reimplementing;
the drift handler and `reset-data`'s reseed loop take it, and the latter lost
its private copy of the same check. `buildFamiliesView` takes the PURE door,
because its primary loop emits one row per SUGGESTION and therefore fetched
the list already: routing it through the fetching door read
`resource_suggestions` twice per request, the second read a LATER snapshot
than the first, so a `rederive` landing between them could show one family as
both suggested and orphaned inside one response. That is not hypothetical —
the slice's own first fleet run shipped it, and the split is what the gate's
post-run round replaced it with. And a `custom_endpoints` row whose
`(method, canonical_path)` a spec operation ALSO declares, carrying
`precededSpec` (`custom_endpoints.created_at < specs.created_at` for the
bound spec, STRICTLY — equality reads as `false`) as a hint beside the row,
never a filter: rule 3 of `router.compareRoutes` makes a custom endpoint's
priority at equal canonical shape documented behaviour, and an operator who
built one on purpose still has it reported, only with the hint saying so. A
static capture (a custom route that wins by MORE static segments, rule 1,
never rule 3) is deliberately NOT reported — that would mean running
`router.Build` over the merged table and reading off winners, a second and
much larger predicate whose true positives are mostly deliberate. The
response carries three typed arrays and `HasDrift`, DERIVED from their
emptiness on the way out and never from a separate query, and no remedy
field at all: every repair this slice's three signals name already has its
own verb (`DELETE .../operations/{opKey}`, `DELETE .../endpoints/{eid}`,
`POST .../resource-decisions` with `state: "declined"`), each one a
DELETION — the two PRESERVING remedies §5 also names (turn an orphaned
override into a custom endpoint, turn a shadowing endpoint into an
override) are not built by this slice, and neither is a schema-diff signal,
an automatic reattachment heuristic, or a fourth signal for a declined
family with no confirmed row (see `CARVE-OUTS.md`). A workspace with no
spec bound answers `200` with `hasDrift: false` and three empty arrays —
there is nothing to be out of step WITH. Like its two siblings
(`buildFamiliesView`, `GET /api/specs/{id}/resource-suggestions`), the read
may DERIVE: on a spec whose suggestions have never been computed it runs
`specs.Repo.EnsureSuggestions`'s lazy backfill and writes
`resource_suggestions`, the only row this GET can ever write. No screen —
`get_workspace_drift` (`internal/mcp`, tool 44) is the ONLY caller the
coverage invariant requires, and `web/src/api/coverage.test.ts`'s `EXEMPT`
map's third entry says so; its own description names all three repair
verbs PAIRED with what each one destroys (a pinned override's body and
recipes; a custom endpoint's authored body; a confirmed family's entity
rows), because a caller that sees only the verb and not the cost would
delete configuration on a report's say-so.

`GET /api/workspaces/{id}/resources/{family}/entities` (A4,
`internal/admin/resource_handlers.go`) is the first STRUCTURED read of what a
confirmed family actually holds, and the word structured is load-bearing: the
rows were never invisible. `internal/mockplane/traffic.go:222` records
`ev.RespBody` for a `resourceBranch` response like any other, so an agent has
been able to read a family's row bodies through `list_traffic` since `P3a` —
unpaginated, unfiltered by scope, only for a `GET` somebody already made, and
interleaved with every other request the workspace served. What did not exist is
asking what a family holds NOW. **The route is a declared DIVERGENCE from
`DESIGN.md:936`**, which designs full CRUD addressed by `:rid` with a per-key
accessor; this is the read corner only, addressed by `route_family` and with no
`/:key` — recorded in `CARVE-OUTS.md`, which is also where the `P3a` entry's "no
entity routes at all" was amended rather than left standing.

Two decisions about it cost more thought than the handler. **The `{family}`
segment carries `url.PathEscape` on the CLIENT and the handler reads
`r.PathValue("family")` directly** — Go's `ServeMux` splits the raw path on
literal slashes and unescapes each segment on its own, so `PathValue` arrives
decoded. Copying `internal/admin/override_handlers.go`'s `opKeyFromPath` here
RE-escapes an already-decoded value before `resourceByFamily`, which matches the
raw `route_family`, and 404s on every real family; that function re-escapes
because `overrides.ParseOpKey` unescapes again for a different caller, and the
precedent it sets is for the CLIENT-side escape, never for a handler-side
unescape. **And the `after` cursor keys on `entities.id`, never on
`entity_key`**: that column is `TEXT` holding an unpadded decimal from
`strconv.FormatInt`, SQLite compares `TEXT` with BINARY collation, so `'10' <
'9'` and a cursor on it silently skips and reorders rows past the ninth — in a
family holding up to `MOCKER_MAX_ENTITIES`. `entities.id` is an `INTEGER PRIMARY
KEY` that `Repo.List` already orders by, and all three write paths (`Confirm`,
reseed, checkpoint restore, the last capturing `ORDER BY CAST(e.entity_key AS
INTEGER)`) insert in ascending key order, so the two orders never disagree.

`internal/resources.Repo.ListFiltered` is the read that shape needs and did not
exist: `Repo.List` requires an EXACT base and scope pair with no wildcard, no
cursor and no limit, and `selectEntity` did not even SELECT `base_scope_key`, so
a filtered row could not name the scope it came from — `Entity.BaseScopeKey`
closes that half. The filtering, the limit and the cursor live in the SQL; a
widened signature with a Go loop inside it satisfies the signature and nothing
else, which is why the gate split it out as its own decision with its own
criterion. The route answers **`404 unknown_family` whenever `resourceByFamily`
returns nil, for every reason** — the spec never suggested it, it was declined,
no spec is bound — because telling those apart costs a second query per request
for a caller whose repair (`POST .../resource-decisions`) is the same in all
three.

Beside it A4 makes two smaller reaches. `probe_workspace` wraps the existing
`POST /api/workspaces/{id}/probe` and carries **no `confirmSlug`**: that field
guards verbs destroying workspace-created data, and a probe destroys nothing —
adding it where nothing is destroyed makes it decoration and weakens it where it
is real. And `list_traffic` finally exposes the `since`/`lastId` cursor its own
route has always had (`internal/admin/traffic_handlers.go:131-158`), with **no
new tool**: on the no-`since` path it calls `GET .../traffic`, whose
`trafficListView` has no `LastID` field at all, so `lastId` is derived from the
newest-first rows rather than by switching that path onto `/traffic/poll`, which
would reorder them and drop `rate1m`.

`internal/stream` (`P6a`, 2026-09-02) is the first line of streaming code in
this tree and the admin plane's traffic feed over Server-Sent Events —
`GET /api/workspaces/{id}/traffic/stream`, with `GET .../traffic` (the tail)
and `GET .../traffic/poll` (the cursor) left exactly as they were: the three
are three halves of one feed, and the poll is what a client falls back to
whenever the stream refuses or dies. The package owns the CONNECTION
REGISTRY (a process-wide cap of `maxStreamConns = 64`, a constant and not a
variable — file descriptors and memory are properties of the process, and a
per-workspace cap would bound the process by nothing since a workspace costs
one row), the SSE wire (`id: <lastId>`, `event: traffic`, `data:` the SAME
`trafficPollView` JSON the poll route answers with, at most 200 rows per
frame — the screen's own `POLL_LIMIT`, not the poll route's default or
ceiling), the write-deadline exemption through `http.ResponseController`,
the per-frame deadline, the ping ticker, the lifetime and the refusal path —
and knows nothing about sessions, workspaces or repositories: `internal/admin`'s
`stream_handlers.go` resolves those and hands the package two callbacks. The
transport is SSE and not WebSocket permanently for this route (`DESIGN.md`
§30.10): the CSRF guard runs only on state-changing methods, a WebSocket
handshake is a `GET` that CORS does not cover, and a WebSocket feed would be
readable, session-authenticated, by any page in the contour; `EventSource` is
an ordinary `GET` and the admin plane emits no cross-origin allowance, so the
browser blocks the read.

Six things about it cannot be guessed from the code. **Delivery is a NUDGE
plus a read, never a push of rows**: `traffic.Recorder.SetNotifier` hands the
registry to the recorder as a `Notifier`, which is told the distinct workspace
ids of a batch AFTER `writeBatch` committed (a row's identity is the SQLite
id the INSERT assigns, so a row pushed before the batch landed could not be
merged with what the poll and the tail already produced); `internal/traffic`
does not import `internal/stream`. A connection wakes on a nudge for its own
workspace, reads `Repo.Since(cursor)`, and reads AGAIN while the page it just
wrote came back FULL, every iteration passing through the same `select` the
outer wait does — a bare inner loop would hold a connection past its lifetime
and its recheck under steady traffic. The wakeup is a ONE-SLOT channel per
connection, drained BEFORE the read (drained after, a nudge landing between
the read's snapshot and the wait is lost with its row unseen), sent into
without blocking and DROPPED AND COUNTED (`coalescedNudges`) when the slot is
full — that count is the whole of §30's "drop-and-count" here, because no row
is lost by a coalesced nudge, and the channel is never CLOSED, only left to
be collected, because closing it races a `Notify` in flight and panics the
recorder's goroutine. A failed `Repo.Since` does NOT re-arm the slot: the
connection waits for the next nudge, the lifetime being the backstop.
**The cursor is `Last-Event-ID` when present and positive, `?since=`
otherwise** — the browser's own reconnect replays the URL and adds the
header; honouring only one of the two would re-deliver, skip, or leave
`curl` unable to resume. **`maxStreamLifetime` (900 s) is a package `var`,
never a literal in the loop**, so the package's own test can shorten it — and
that test does not run in parallel and restores the value only after the
connection goroutine is JOINED, because a mutable package var read by a
goroutine belonging to another test is exactly what `-race` would only
probably catch. **The session AND the workspace are re-validated on
`MOCKER_STREAM_SESSION_RECHECK`**, the session by a store read the SPA's own
route guard already makes, the workspace by `(created_at, slug)` and never by
id — `workspaces.id` is a reusable rowid, deleting a workspace cascades its
traffic away, and an id-only recheck would keep serving a DELETED
workspace's connection with whatever workspace a later `POST /api/workspaces`
was handed that id; the pair is an identity only to within a second, the
identical residual `internal/checkpoints` has fenced on since `P2c`, accepted
rather than hidden. And the identity check ALSO runs before every read, not
only on the timer: the package's own reissued-id test delivered the
impostor's first batch on the timer-only draft, because a nudge for the new
workspace arrives inside the recheck interval — a read that refuses answers
`stream.ErrRefused` and closes the connection. **The 501 and the 503 are
different statuses on purpose**: `501 streaming_unsupported` is a response
writer that reports `http.ErrNotSupported` on `SetWriteDeadline` (a test
recorder, an in-process loopback, a proxy that unwraps into something that
cannot flush), refused BEFORE a single frame and never a buffered
fallthrough; `503 service_unavailable` is the cap, or the registry closing
for shutdown, or no registry wired — two checks sharing one sentinel both
pass when the other feature is deleted. **The shutdown order is
`liveState.Close()` → `registry.Close()` → the drain → `recorderCancel()`, on
BOTH exit paths of `cmd/mocker/main.go`**, and the listener-error path was
REORDERED to say so — before `P6a` it cancelled the recorder first and
released the live state after, the reverse of the signal path. Live state
first because a handler parked on a pause must be released before its
connection is cancelled; registry before the drain because `Shutdown` waits
for a live SSE connection for the whole drain window; recorder last because
a mock request answered during the drain leaves an event in the recorder's
QUEUE that a cancel before the drain ends would discard — the admin stream's
own request is recorded by nothing, so what the recorder must outlive is
mock traffic, never this slice's connections. `Close()` itself is the
three-step shape §30.7 prescribes — closed flag first (a handshake racing
shutdown is refused, not registered), cancel every connection, WAIT for
their handlers to return — and `internal/testleak`'s ignore list did not
grow for it.

`GET /api/stream/stats` (`{open, cap, refusedCap, refusedUnsupported,
coalescedNudges, byWorkspace}`) is process-wide because the cap is; it is
agent-only (`get_stream_stats`, tool 47) and `coverage.test.ts`'s THIRD
non-probe `EXEMPT` entry. The stream route is NOT exempt: the screen calls
it through the browser's own `EventSource` (`web/src/api/stream.ts`), and
the scanner learned that fifth consumption shape by turning each `{param}`
of the contract's template into a one-segment wildcard, so the natural
template literal matches without the screen being written around the guard.
The screen (`TrafficPage.tsx`) opens the stream from the tail's cursor,
goes live on the FIRST frame (badge «живой поток» — a Russian UI string),
and on ANY `EventSource` error — before or after that frame, including the
server's own 900-second expiry — closes the stream, enters the poll at its
2 s interval (badge «опрос каждые 2 с», with the server's own envelope message
beside it where there is one), retries a replacement stream every 60 s and
leaves the fallback only when THAT stream delivers a frame. The reason
beside the badge comes from ONE raw `fetch` of the same URL that reads the
status and aborts on a success — deliberately not the orval mutator, whose
`res.text()` would await a stream that never ends and hold one of the 64
slots for fifteen minutes on every fallback; a `401` there goes where every
other dead session goes (`notifyUnauthorized`, the same handler `client.ts`
already fires). Clearing the traffic (`DELETE .../traffic`) reopens the
stream with no cursor and the server learns nothing about it — but a cursor
had to SURVIVE a clear for every other client, so `traffic.id` is now
`INTEGER PRIMARY KEY AUTOINCREMENT` (migration `0004_traffic_autoincrement.sql`,
the tree's first REBUILD migration: create, `INSERT … SELECT` every row with
its id, drop, rename, recreate `traffic_ws`; `sqlite_sequence` then sits at
the highest id carried across). Without it SQLite reissues a deleted
highest id, and a second tab, a `curl`, or the browser's own reconnect racing
the clear sits on a connection that looks alive and is permanently empty.
`scripts/migration-check.sh` is the observation `make smoke` cannot make —
it starts from an empty volume — a parent-commit binary's data directory
opened by the current one, every row still there with its id.

**`P6b` (2026-09-02) is the first mock-plane stream: a custom endpoint of
`kind: "sse"`.** A stream is a custom endpoint and nothing else (§30.2) —
never derived from a spec, no fifth response layer — so it inherits the
edit-version compare-and-swap, the auto checkpoint, `routeOff`/`overrideOn`,
the bundle's `endpoints[]` and the checkpoint restore unchanged. Migration
`0005_custom_endpoints_stream.sql` adds `kind` (`'http'` by default, so no
row moves) and `stream` (the whole definition as one JSON document, `NULL`
exactly when `kind = 'http'`, a SQL `CHECK` because that is the one invariant
a hand-run `UPDATE` or a restore could break silently); it is the tree's
second REBUILD migration, and neither UNIQUE index admits `kind` — one path
cannot be both an ordinary `GET` and a stream. The document
(`internal/customep/stream.go`) is `{timeline: {frames: [{delayMs, event,
data}], loop}, tick: {intervalMs, event, schema}, closeWhenDone}`, at least
one of timeline/tick, validated in ONE place both writers and the preview
route reach (`customep.ValidateDraft` is the same function `Create` and
`UpdateExpecting` run) and refused BY NAME, never clamped: the tick floor
is 100 ms, a timeline holds at most 500 frames, a frame waits at most
30 000 ms (the same ceiling a delay directive has), a payload is at most
`MOCKER_MAX_RESPONSE` (`customep.Repo.MaxFrameBytes`, set from config by
both constructors), an `event:` fits an SSE line, and an inline schema
carries no `$ref` because there is no document to resolve one against. An
`sse` row is STRICT — `GET`, no `responses`, `activeStatus` 200 — because a
response map on a stream would quietly never fire, the shape §30.3 forbids
for `when[]`; `kind: "ws"` is refused by name until `P6d`; and a `PUT` that
resends everything but the kind is refused for carrying a stream on an
http row, never silently downgraded.

Serving is a BRANCH in `serveCustom` (`internal/mockplane/stream.go`),
after the session layer and only when no status was forced — a forced 503
answers 503 with no stream, a pause parks the handshake, a delay delays the
handshake and never each frame (§30.4), all of which the existing custom
path already did before the branch. The loop is ONE `select` on the
handler's own goroutine (§30.8: a timeline and a tick start nothing): the
timeline's timer re-armed per frame with that frame's own delay, the tick's
ticker, the ping (`MOCKER_STREAM_PING`, shared with the admin feed), the
lifetime (`MOCKER_STREAM_MAX_LIFETIME`, the mock plane's own), the request
context and the registry's cancel; frames carry `id: <ordinal>` and
`Last-Event-ID` is ignored — a connection copies its definition out at the
handshake and has no server-side state (§30.5), so an edit bumps `revision`
and takes effect on the NEXT connection while an open one runs to
completion on what it opened with. **A tick body is `gen.Body` over the
inline schema**, seeded by the workspace seed, the endpoint id and the tick
ordinal folded through `PathParams` (no new `gen.Request` field) — the same
body at the same ordinal on every connection, and the SECOND `gen.Body`
site in the tree beside `assembleResponse`: `TestAssembleResponseIsTheOnlySeam`
names it, because the seam it guards is a RESPONSE's assembly (recipes,
schemaPatch, pinned-versus-generated, `ref`) and a tick frame has none of it
to duplicate. A frame over `MOCKER_MAX_RESPONSE` is skipped and counted.
The registry is `stream.NewWorkspaceRegistry(cfg.StreamMaxConns)` — a
SECOND instance, capped per workspace, closed by `main.go` in the same step
as the admin feed's, because an unauthenticated plane must not be able to
exhaust the authenticated one's feed by sharing a counter; `0` refuses every
handshake outright; `GET /api/stream/stats` reports it as `mock`. **The
traffic recorder writes ONE row per connection**, when it closes: status
200 (or the forced/refused one), `durationMs` the connection's whole life,
notes `stream:sse,frames:N` (`frames_skipped:M` when any), and `respBody`
NULL under `MOCKER_STREAM_TRAFFIC_FRAMES=off`, the first frame's wire
bytes under `first`, or every frame's under `all` (`A14`, below) — never
the writer's own capture, which would be pings and whatever fit in 8 KiB. The bundle is **v4** (`kind` always, `stream`
`null` for http) and **v3 is refused**, on the owner's own decision that no
deployment exists: a database created before this slice holds checkpoints
and scenarios nothing can decode, and `migration-check.sh` does not exercise
a rollback across that boundary. `POST /api/workspaces/{id}/endpoints/preview`
(`preview_endpoint`, tool 48, the fourth non-probe `EXEMPT` entry) lays the
first ≤ 50 frames of a DRAFT out on one time axis with tick bodies seeded by
endpoint id 0, writes nothing, and carries `maxBytesPerSec`, the estimate of
the amplifier §30.12 wants shown. No screen: the A4 rule ("UI вообще не
нужен делай только MCP", a Russian string quoted as data) applied to §30.14,
recorded in `CARVE-OUTS.md`.

**`P6c` (2026-09-02) is the live-connection surface: three routes under
`/api/workspaces/{id}/connections` — list, close one, push one frame — over
the MOCK plane's registry only, with three MCP tools
(`list_stream_connections`, `close_stream_connection`, `push_stream_frame`,
tools 49–51) and no screen.** Contract 58 → 61, `EXEMPT` six → nine, no
variable, no migration, no bundle change. Five things about it cannot be
guessed from the code. **A connection has an identity now, minted by its
registry**: `stream.Conn.id` is an `int64` counter from 1 per registry,
never reused while the process runs and restarting at 1 after a restart —
an id held across a restart can name a NEW connection, recorded rather than
hidden behind a token; `Conn.SetInfo` (endpoint id, path, kind, the peer as
`httpx.ResolvePeer` renders it) is called by the mock plane between `Open`
and `Handshake`, the admin feed never calls it and is never listed, and
`Registry.Snapshot`/`Lookup` are workspace-scoped and SKIP a connection whose
context is already done. **Where a pushed frame lives is §30.16's first open
question, answered: in the connection, in RAM, nowhere else** — a bounded
channel on the `Conn` (`inboxDepth = 16`, a constant: 16 × 4 MiB is the
worst case and only an authenticated operator can fill it), never
`internal/livestate` (keyed by operation; a frame's target is a connection),
never SQLite, a checkpoint or a bundle, never replayed to a later connection.
**Push is delivered by the connection's OWN loop and the caller waits for the
ordinal**: `Conn.Push` enqueues without blocking (`ErrInboxFull`), the loop
gains one `select` case that writes the frame under the next `id:` with the
same per-frame deadline every other frame has (§30.8: the handler goroutine
stays the only writer), and the pusher waits on a ONE-SLOT buffered reply
with a second non-blocking look before it returns `ErrPushTimeout` (the
frame STAYS queued) or `ErrConnClosed` — `deregister` cancels the connection
context on every exit so no pusher parks behind a returned loop. The admin
handler waits `2 × FrameTimeout` and answers `200 {connectionId, frameId}`,
`409 inbox_full`, `409 connection_closed` or `504 push_timeout`; the MCP tool
uses `lb.do` for exactly that 504 because `toolErr` drops every 5xx message
and this one says "do not resend blindly". The frame body is a timeline
frame's `{event?, data}`, validated by `customep.ValidatePushFrame` — the
same private checks the timeline validator calls, the same words after the
field name. **Close is a compare-and-swap cancel and nothing more, and it is ONE
registry operation** (`Registry.CloseByAdmin(workspaceID, id)` finds, checks
liveness and flips the flag under the registry's own lock — never a
`Lookup` followed by a close, which the diff review showed could answer 204
for a connection that deregistered in between): no final frame (SSE has
none; the client's `EventSource` reconnects as a NEW id), the loser of two
racing DELETEs answers `404 connection_not_found` like a DELETE after
deregistration, no `confirmSlug` on any of the three (a connection is not
workspace data). **The traffic row learns two conditional tokens**:
`pushed:M` (M > 0) and `closed:admin`, beside `stream:sse,frames:N` —
`frames:N` counts pushed frames, since they carry ordinals like every other.
The list envelope is `{open, cap, connections[]}` — the ceiling beside who
holds it, `cap` the same number `stats.mock.cap` reports — with an optional
`?endpointId=` filter that never changes `open`. Both mutating routes joined
`autoCheckpointExcludedNeverTouchesLayer` (12 → 14): a cancel and a RAM
inbox write no row and bump no `revision`.

**`P6d` (2026-09-02) is WebSocket: a custom endpoint of `kind: "ws"`, served
by `github.com/coder/websocket` behind `internal/wsmock` (the one importer,
held by a boundary test), with all four behaviours of §30.3 — timeline and
tick reused from `P6b`, reactive and echo new — three variables (29 → 32:
`MOCKER_STREAM_MAX_FRAME`, `_SEND_BUDGET`, `_ORIGINS`), the CSRF predicate
of §30.10, the CSP sources of §30.14, and the first inbound data this plane
records.** No route, no tool, no migration, no bundle change: contract 61,
tools 51, `EXEMPT` 9. Seven things about it cannot be guessed from the code.
**The document grows two fields, refused by name on `sse`**:
`stream.reactive: [{when, data?, close?: {code, reason?}}]` (at most 100
rules; `when` is `overrides.Condition[]` through the SAME
`ValidateConditions`/`MatchAll` — the inbound frame is the `body`, the
handshake's query and headers are captured once and constant; only a TEXT
frame carrying a JSON OBJECT can match; first match wins; `close.code` is
1000 or 4000–4999, the owner's own addition against the recommendation)
and `stream.echo` (the fallback for an unmatched frame, mirroring the
opcode, never a parallel producer). `customep.ValidateStreamFor(kind, …)` is
the one door; a `ws` row is strict exactly like `sse`. **One extra goroutine
per connection, the reader, and the handler loop stays the ONLY writer**
(§30.8): the reader reads, counts `framesIn`, matches and hands replies to
a per-connection queue bounded in BYTES by `SEND_BUDGET`; a reply over it
is dropped and counted and the loop writes one `{"$gap": N}` text frame
before its next write from that queue (§30.11); a rule's `close` is a
terminal item outside the budget, after which the reader keeps DRAINING
(the peer's half of the closing handshake arrives on that same read) but
stops matching. The reader's context is its OWN, cancelled only after the
closing handshake — the library tears the socket down the instant a Read's
context expires, which would defeat the 1001 close frame an operator's
close or a shutdown promises — while every Write/Ping/Close runs under the
connection context plus one frame timeout, so `Registry.Close` aborts a
blocked write within that. The exit order is: loop returns → `Close(code,
reason)` (peer half read by the reader) → reader context cancelled → reader
JOINED → row → `Release`; goleak's ignore list did not grow. **Close codes
are D8's**: 1000 `done`/`lifetime`, 1001 `shutting down`/`closed by
operator`/`no pong`, the rule's own, 1009 from the library's read limit
(recognised by its error text inside `wsmock.CloseStatus`, the one place
the library's wording is depended on), the peer's mirrored. **Origins are
checked BEFORE the upgrade and before the cap** (`403 origin_refused`; a
missing `Origin` is always allowed; the library's own origin check is
disabled by name at the call site because this one is the owner), and a
writer that cannot be hijacked answers `501 streaming_unsupported` before
the library touches it — `wsmock.CanHijack` walks `Unwrap` exactly as the
library and `http.ResponseController` do, and every wrapper on the mock
path (`httpx.StatusRecorder`, the traffic tee, `headWriter`) implements it.
**The traffic row learns the inbound half**: `stream:ws`, `frames_in:M`,
`replies_dropped:K` (K > 0), `close:<code>` always, `first_in:binary|text`
when the first inbound frame was kept as nothing; under
`MOCKER_STREAM_TRAFFIC_FRAMES=first` the row's `reqBody` is the first
inbound TEXT frame that is a JSON object, run through
`traffic.RedactJSONBody` directly (there is no content type to dispatch on,
§30.13's first collision, answered by dispatching on the frame's opcode and
shape). **The CSRF predicate takes the request**: a `GET` with `Connection:
upgrade` and `Upgrade: websocket` is state-changing, so on the admin host
it meets the chain's first check and is refused `415` (a handshake carries
no JSON content type) — the door is locked before any admin route upgrades.
**The P6c surface works unchanged**: `kind` `ws` and `framesIn` on the row,
a push writes `data` as one text frame and refuses a non-empty `event` by
name, a close ends the connection with 1001. The smoke's WebSocket client is
`scripts/wsclient.py`, python3 standard library only, because curl's CLI can
only receive frames and the runtime image is distroless; "Requires:" at the
top of `scripts/smoke.sh` says so.

**`A6` (2026-09-02) is assets: uploaded files a mock can serve — DESIGN
§32, the second version (v11) to add intent before code, inserted by the
agent at the owner's explicit instruction from four answered questions.**
`internal/assets` is a leaf repository over ONE new table (migration
`0006_assets.sql`, the first table since P0): one row per file, the bytes
as a BLOB, the natural key `(workspace_id, name)` — a `bodyRef` and an
`asset_url` recipe address an asset by NAME and never by id, because a
name survives the delete-and-reupload repair this product prescribes for a
wrong object everywhere else. `assets.ValidName` is the ONE owner of what
a name may be (`[A-Za-z0-9._-]{1,128}`, no dot-segment); `internal/recipes`
carries its own copy because it is a leaf that may not import
`internal/store`, and `internal/assets`' corpus test feeds both. Seven
things about it cannot be guessed from the code. **The upload is a raw-body
`PUT`** (`PUT /api/workspaces/{id}/assets/{name}`, `Content-Type` = the
file's type): §8's "multipart we do not touch" stands, and the CSRF chain's
content-type check (`enforceCSRF`) is swapped for "parseable and not
browser-executable" on exactly ONE predicate, `rawBodyRoute` — the origin
check and `X-CSRF-Token` run unchanged, the header keeps the request
non-simple and preflighted, and the handler repeats the media-type refusal
because the MCP loopback bypasses the chain by construction. The read is
`http.MaxBytesReader` at `MOCKER_MAX_ASSET + 1`, never a bare `io.ReadAll`
under the dispatcher's 10 MB `MaxBody`. **The `revision` bump is the
package's own `bumpRevisionTx`**, HARD RULE 5's sixth copy —
`workspaces.Repo.Update` inside a `db.Write` callback deadlocks the single
writer connection, a hang and not a red test, which the first draft had
backwards until a reader caught it. **The mock route is dispatch step 4**
(`serveReserved`, beside `/health` and `/state`): CORS already set at step
2, preflight answered at step 3, the session layer (step 5) never reached —
no forced status, delay or pause applies to a picture — and not recorded in
traffic; `Meta` first and the BLOB only on a miss, so `If-None-Match`
answers 304 without moving bytes through the reader pool; a strong `ETag`
(the sha256), `nosniff`, and NO `Cache-Control` (§32.3: nothing beyond the
tag — with no `Last-Modified` either a browser has no heuristic freshness to
apply). **The executable-type refusal runs TWICE** — at the upload and
again on the stored type at serve, on the route and inside the `bodyRef`
lookup — because a row written by an older build or a hand-run `UPDATE`
must not serve script under the admin origin in path mode (§32.6).
**`bodyRef` on a pinned variant is exclusive with `body`, `bodyEncoding` AND
`mediaType`** — refused, where §32.3 said "must agree", a declared narrowing
(`CARVE-OUTS.md`): agreement is unknowable at write time, so the asset's
stored type is the only type such a variant has, and it reaches the wire
through ONE place — a local `assetType` in `assembleResponse` that
overrides `resp.MediaType` after the switch and skips the envelope wrap,
because the tail sets the type from `rv.MediaType` unconditionally and a
switch arm alone could not. The arm sits AFTER the executable-type refusal
and AFTER `PreBuilt` (a pinned variant already exits resource takeover, so
the two never coexist; the order is stated so a later caller cannot bypass
either) and BEFORE `pinned`; the lookup it resolves through is the `ref`
shape twice over — `assetLookup`, CONSTRUCTED by `serveGenerated` from the
real request (it is what MARKS `asset_missing` in the traffic, because
`assembleResponse` holds no `*http.Request`), RECEIVED by
`assembleResponse`, `nil` from Preview and handled like a nil `Ref`. A
missing asset answers the variant's status with an EMPTY body and the note;
Preview answers `noBody` with `PreviewResult.Notes`. **The `asset_url`
recipe writes `Env.AssetBase + PathEscape(name)`** and DECLINES on an empty
base (population, a tick frame, a `Request` built by hand) rather than emit
a relative URL; the base is per REQUEST — `gen.Request.AssetBase`, copied
into `recipes.Env` from `w.req`, never `Options`, the precedent being `Ref`
and the reason being the runtime cache under `(workspace_id, revision)` —
and it is built by `httpx.WorkspaceURL(r, cfg, slug) + ReservedPrefix +
"/assets/"` at both call sites (the mock request in `serveGenerated`, the
admin request in the preview handler, carried in on
`domain.PreviewRequest.AssetBase`). **`httpx.WorkspaceURL` is the ONE
construction of a workspace's public URL**: `admin.workspaceURL` delegates
to it, `httpx.WorkspacePathPrefix` replaced the two identical unexported
consts in `internal/server` and `internal/admin`, and the two guarded reads
(scheme through `ForwardedProto`, port through `RequestPort` — the SSRF
pivot `TestP1c2WorkspaceView_urlRefusesAnInjectedPort` pins) moved with it.
**`upload_asset` reaches the admin plane through `CallAsMCPRaw`**, a second
method on `admin.Server` with a content type, asserted by `internal/mcp`
through its own one-method `rawCaller` interface only where it is needed —
`mcp.Caller` is the seam all fifty-one earlier tools and every test double
dispatch through and is not widened; the tool's description names the REAL
ceiling (about 7 MB at the default `MOCKER_MAX_BODY`, the base64 travels
under it), and `list_assets`/`delete_asset` (`confirmSlug`, checked inside
the delete's transaction) complete the three. No checkpoint, scenario or
bundle carries asset bytes (bundle stays v4: `bundle.Decode` is a plain
`jsonx.Unmarshal` and the deep gate re-enters `ValidateVariant`, so a
`bodyRef` round-trips and a v4 document without one decodes byte for
byte); a rollback restores a `bodyRef` whose asset may be gone, and
`asset_missing` is the whole of the answer. `PUT`/`DELETE` bump `revision`
for §32.5's reason (`{prefix}/health`'s `revision` is the one signal an
external test has that something changed) at a named cost — `routeCache`
is keyed by it, so every upload discards the compiled runtime — and both
joined `autoCheckpointExcludedNeverTouchesLayer` (14 → 16), because bytes
are not configuration.

**`A7` (2026-09-02) is the documentation slice: how to use the product,
for a human and for an agent, with the agent's copy served by the server
itself.** Three artefacts and one owner each. `docs/USER-GUIDE.md` is the
operator's manual in Russian (the product's language, like every other
operator-facing string), rendered inside the panel at `/guide` by
`GuidePage.tsx` through `marked` (the SPA's first markdown renderer and
its one new dependency, zero transitive) from a `?raw` import — a screen
added AFTER the A4 "no new screens" rule, on the owner's explicit request
(«для людей прям отдельную страницу можно заверстать в ui», a Russian
string quoted as data), and admissible under it because the screen calls
NO admin route: the contract stays at 64, `coverage.test.ts` learns
nothing, and `routes.test.tsx` asserts the mount makes no call past the
guard's own `/api/me`. `skills/mocker/` is the agent's guide as an
installable skill — `SKILL.md` (the mental model, the order of calls, the
rules that bite) plus four references (`tools.md`, every tool with inputs,
outputs and gotchas; `shapes.md`, every document an agent writes;
`cookbook.md`, twelve ordered recipes; `http.md`, the same over curl and
the MCP client config) — and it is the ONE OWNER of that text.
`internal/guide` embeds byte copies of those five files (go:embed cannot
reach above its package, and a skill is discovered by the path
`skills/<name>/SKILL.md`, so neither can move), `make guide-sync`
refreshes them, and `internal/guide`'s own test fails on any drift. Two
things reach an agent that has no repository: `initialize` now returns
`instructions` (`internal/guide/instructions.md`, a few paragraphs, the
one file with no skill counterpart, passed as `sdk.ServerOptions` — a
FIELD of the initialize result, not a capability, so DESIGN §14.2's "only
`tools`, without `resources` and `prompts`" still holds and no divergence
is recorded), and `get_guide {topic}` (tool 55, `internal/mcp/tools_guide.go`)
returns one of the five files, `overview` with SKILL.md's frontmatter
stripped. It is the first tool whose `toolRoutes` row is EMPTY — it calls
no handler — which `TestToolRoutesPopulation` counts and
`TestToolRoutesAgreeWithAdminAllowlist` has nothing to check for. No
migration, no variable, no contract change.

**`P6e` (2026-09-02) is the last streaming slice and the one whose whole
deliverable is a screen — DESIGN §30.14, built after the owner said
«сделай P6e» (a Russian string quoted as data), which is the lifting of
the A4 rule for exactly this slice and nothing else.** Three pieces, all
under `web/src/components/`. **Authoring** on the custom-endpoints screen:
a «Тип» selector (`http` \| `sse` \| `ws`) that pins the method to GET and
swaps the body editor for `StreamEditor.tsx` — the four behaviours phrased
as TASKS («Отправлять кадры по расписанию», «Генерировать кадр по
интервалу», «Отвечать на входящие сообщения», «Возвращать входящие
сообщения как есть»), because §14 forbids "recipe"/"JSON patch"/"matcher"
in the interface and §30.14 puts "timeline"/"reactive"/"tick" under the
same rule; a test asserts none of the six words renders. The reply rules
reuse `OperationEditor.tsx`'s `when[]` row and labels verbatim — one
condition language. `draftToDefinition` mirrors the server's refusals a
form can answer before a round trip (floor, ceilings, JSON, close codes)
and never clamps; `StreamCapsStrip` is the read-only strip — the caps as
constants (`STREAM_CAPS`, a copy of `internal/customep/stream.go`'s, the
one deliberate duplication) plus «Рассчитать кадры», which is
`POST .../endpoints/preview` and shows `maxBytesPerSec`, the amplifier
§30.12 wants seen before a loop is saved. A stream row edits through
`EditStreamForm` (PUT resends `kind` and `stream`, `activeStatus` 200).
**"Try it"** is `StreamTestClient.tsx`, a BROWSER-side client and never a
server-side probe (§30.14's own argument: `internal/probe` stays the tree's
only outgoing client, and the proxy §16 warns about sits between THIS
browser and the mock host): «Проверить» on a stream row opens
`EventSource` or `WebSocket` to `${workspace.url}${path}` (the ws URL is
the http one with the scheme swapped, never rebuilt), listens for the
named events the definition declares plus `message`, logs frames, sends a
text frame on ws, and reports what the browser will not say — an
EventSource `error` carries no status, so the wording says "the browser
does not report the reason" rather than guessing, and the source is
CLOSED on the first error (one attempt, one verdict) instead of left to
its silent reconnect loop. **The connections panel** is an eighth tab,
«Соединения» (`/workspaces/$id/connections`, `StreamConnectionsPage.tsx`),
polling `GET .../connections` every 2 s (the registry has no feed of its
own, §30.16), with «Закрыть» and an inline «Отправить кадр» form whose
error alert carries the server's own sentence — the 504 `push_timeout`
one says the frame stays queued. Two contract repairs rode along, both
drifts the screens exposed: `StreamPreviewRequest.kind` said `sse` alone
while the handler accepted `ws` since `P6d`; and `EndpointConflictDetails`
lacked `kind`/`stream`, so a stream row's 409 could not seed the editor
with the document the other writer saved — the ONE server change of the
slice (`endpointConflictDetails` in `internal/admin/endpoint_handlers.go`).
Contract stays 64 operations; `EXEMPT` 12 → 8 (preview and the three
connection operations are called by screens now); no tool, no variable,
no migration.

**`A8` (2026-09-02) is spec import for the agent, and YAML.** Two things.
`import_spec` (tool 56, `internal/mcp/tools_specs.go`) wraps `POST
/api/specs` — the document as one string inside the arguments, exactly the
shape the screen sends, deduplicated by byte hash so a retry answers
`duplicate: true` — and `POST /api/specs` joins `mcpAllowedRoutes`, the
reversal described under `/mcp` above. And `internal/yamlx` (`ToJSON`) is
the tree's second isolated library: `internal/openapi`'s `decodeDocument`
tries JSON first (JSON is valid YAML, so the other order would route every
document through the converter), and only an input whose first CONTENT
line — after blank lines, `#` comments and a `---` marker — names
`openapi:`/`swagger:` goes through the converter and back into the SAME
`decodeJSON`; so the pipeline keeps one root type, one number handling
(`json.Number`) and one error set, and a YAML parse failure is
`ErrNotADocument` with the decoder's words beside it, never
`ErrUnsupportedFormat` (that is for a document we recognise and decline —
Swagger 2.0 still is). Integer mapping keys (`200:` under `responses:`)
become the string keys JSON needs; a sequence key or a multi-document
stream is refused by name. The `/specs` screen dropped its client-side
`.yaml` refusal. No route, no migration, no variable; the stored hash is
over the bytes as uploaded, so a YAML and a JSON rendering of the same spec
are two specs, as two JSON serialisations already were.

**`A9` (2026-09-02) is the limits, readable.** `config.Limits`
(`internal/config`) is the one projection of every ceiling a caller can
hit — bytes as bytes, seconds as seconds — and two readers share it:
`ServerConfigView.limits` (login and `GET /api/me`, a schema change on an
existing route, so no contract count, no `EXEMPT` entry) feeds the stream
caps strip through the endpoints route's session context, replacing the
`MOCKER_*` variable NAMES it showed under `P6e`; and `get_server_config`
(tool 57) hands the same struct to an agent from `mcp.New`'s own `cfg`,
calling no route. The validator's constants (`STREAM_CAPS`: frames,
delays, rules) stay constants on the strip because they are not
configuration. No route, no migration, no variable.

**`A10` (2026-09-02) is the assets screen: the «Файлы» tab
(`AssetsPage.tsx`, the ninth) over `A6`'s three routes, on the owner's
word like `P6e`.** List with the workspace's usage against its two caps, a
dropzone upload whose name is pre-repaired to `assets.ValidName`'s
alphabet and editable, and a delete behind the typed workspace slug. One
thing in it is not a screen: `api/client.ts`'s `customFetch` used to set
`Content-Type: application/json` on EVERY non-GET, which on the raw-body
`PUT .../assets/{name}` would have stored a JPEG under the JSON type (the
server takes the asset's type from that header); a `Blob` body now keeps
its own type, orval's `*/*` placeholder overridden the same way, and the
CSRF chain's `rawBodyRoute` predicate admits it. `EXEMPT` 8 → 5 (the
three asset operations withdrawn); no route, no tool, no migration, no
variable.

**`A11` (2026-09-02) is the entity read's two write siblings:
`PUT`/`DELETE /api/workspaces/{id}/resources/{family}/entities/{key}`
(contract 64 → 66, `EXEMPT` 5 → 7) with `set_resource_entity` and
`delete_resource_entity` (tools 57 → 59).** Until now the only writers of
entity rows were the mock plane's anonymous `POST X` (which mints the NEXT
key, never a chosen one) and `DELETE X/{}`, plus the wholesale verbs;
"give user 42 status blocked" had no verb. `resources.Repo.Set`
(`internal/resources/entity_write.go`) is create-or-replace by the natural
address `(base, scope, key)` in one write transaction, and three rules keep
its rows indistinguishable from the ones `Create` mints and a restore
brings back: `data[idField]` is OVERWRITTEN with the key coerced to the
family's id type (the key IS the identity, exactly as `Create` forces the
allocated seq); `resources.seq` is raised to `MAX(seq, key)` for a decimal
key — the identical rule `restoreEntitiesTx` applies — so the mock plane's
next `POST` can never collide on the unique tuple; and both caps apply,
the byte total computed against the row being REPLACED. Deliberately NOT
done: schema validation (the mock plane's own `POST` does not validate,
R23), the ancestor walk the serve path does (a row under an unanchored
scope is stored, unreachable, counted — the observable-orphan rule the
nesting paragraph already states), a revision bump, an auto checkpoint
(both routes join `autoCheckpointExcludedNeverTouchesLayer` beside
`reset-data`: `config_snap` carries no rows, the undo is a checkpoint
restored with `restoreData: true`), and `confirmSlug` on the delete (one
row, the same thing the anonymous mock-plane `DELETE` does; the field
guards verbs that destroy MANY rows). The key is a URL segment
(`[A-Za-z0-9._~-]{1,128}`, never `.`/`..`), refused `400
invalid_entity_key`; the family arrives `url.PathEscape`d exactly as A4's
read takes it. No screen, no migration, no variable.

**`P4b` (2026-09-02) is the bundle over HTTP and the fork — DESIGN §17's
third job for the one format and §19's `P4` "team scenarios":
`GET /api/workspaces/{id}/export`, `POST /api/workspaces/import`,
`POST /api/workspaces/{id}/fork` (contract 66 → 69, `EXEMPT` 7 → 10) with
`export_workspace`, `import_workspace`, `fork_workspace` (tools 59 → 62);
no screen, no migration, no variable.** Six things about it cannot be
guessed from the code. **The three live in `internal/checkpoints`
(`transfer.go`), not in a package of their own**: an export is
`captureSnapshot`'s read half without the gzip (`readBundle`, split out
for it, as `readDataBundleTx` was from `captureEntitiesTx`), an import is
`rollbackTx`'s apply half into a workspace created in the same
transaction (`applyBundleTx`, the rollback's own order, because
`restoreEntitiesTx` resolves a family only after `upsertResourcesTx`
wrote it), and exporting the eight private apply steps to a sibling
package would create the second caller every one of their comments
forbids. **`workspaces.CreateTx` is the one addition to that package**:
the row and its layers land in ONE transaction, so no client can list,
edit or serve a half-filled workspace; `CreateInput.ForkedFrom` writes
the `forked_from` column P0 declared and nothing wrote until now — a raw
id with no `ON DELETE` clause, dangling on purpose once the source is
deleted. **The export document is `bundle.Export`**: the v4 `Bundle`
EMBEDDED (a file lifted out of a checkpoint or written by hand for a
scenario imports unchanged; a config-only export IS a v4 bundle) plus
`data`, the same `DataBundle` a checkpoint's `data_snap` holds, present
only with `includeData=true` and only when a family is confirmed;
`entities` stays `null` and `Validate` keeps refusing anything else
(P3d D3). Assets never travel (§32.4); `includeSpec=true` inlines the
spec's bytes AS UPLOADED as one JSON string, because `Import` hashes
those bytes and only those re-import to the same hash
(`specs.Repo.Raw`, `ByHash`). The data half keeps the checkpoint
capture's probe budget with the OPPOSITE policy: a checkpoint degrades to
NULL, an export refuses `413 export_too_large` — a copy that silently
serves empty lists is worse than no copy. **Spec resolution on import is
the handler's, in a fixed order**: `specId` (404 if unknown) → the
document's `spec.hash` already here → `spec.inline` imported now through
the same `specs.Repo.Import` (dedup by hash, `source: "bundle"`) → none
when the document names none; a named hash resolving to nothing with no
inline copy is `409 spec_not_found` with `{hash, name}` in details, and
`checkpoints.Repo.Import` refuses `ErrSpecMissing` on its own if a
caller passes nil for a document that names one. `basePath` and
`basePathValues` pass the same two `domain.Validate*` checks `PATCH`
runs. **A fork is export-then-import inside one installation plus what
an export leaves behind**, by `INSERT … SELECT` in the same transaction:
assets (bytes included — a `bodyRef` must resolve on the first request),
scenarios row by row with `edit_version` from the COPY's sequence and
the active one re-pointed by name, and — unless `includeData: false` —
entity rows joined through `route_family` (never `resources.id`), each
family's `seq` raised to its highest copied key afterwards (R18: the
bundle's `seq` was read on the reader pool and an anonymous `POST X`
moves it with no revision bump). The source is read on the reader pool
and FENCED by `fenceTx` inside the write (retried like a rollback) and
is never written: no revision bump, no checkpoint. **Both new
workspaces start with a `manual` checkpoint** («импорт», «копия
воркспейса <slug>» — Russian labels, the history tab's language),
`config_snap` the document just applied and `data_snap` captured from
the rows just written; both routes joined
`autoCheckpointExcludedNeverTouchesLayer` (18 → 20), because neither
touches a layer of the workspace the route names. Name defaults: an
import takes the document's `workspace.name`, a fork appends « (копия)»;
the slug is uniquified from the name as on create, so neither verb is
idempotent.

**`A12` (2026-09-02) is `@yashok111/mocker-test` — `packages/mocker-test/`, the
mock as a fixture a test suite owns: a zero-dependency npm package over
`{prefix}/health` and `{prefix}/state`, with a Playwright fixture factory
and a Cypress command registration that import neither framework.** Not a
Go change: no route, no tool, no migration, no variable. Its own yarn 4
install (same TypeScript/vitest/oxlint/oxfmt versions as `web/`, its own
`.oxlintrc.json` without the react plugin — that plugin reads Playwright's
`use` as a hook), its suite spawns `./bin/mocker` in PATH routing on a
loopback port (`MOCKER_ADMIN_HOST=localhost`; Node's `fetch` cannot set
`Host`), and `make plugin-test` depends on `build`. Since 2026-09-03 it
is built by **tsdown** (`tsdown.config.ts`: three entries, ESM only with
declarations, target node24, `engines.node >= 24` — the owner's call,
Node 24 `require()`s ESM natively so a CJS twin would only double the
files) and distributed as a
tarball — `make plugin-pack` runs `npm pack`, whose `prepack` script IS
the build, so a tarball never ships a stale `dist/`; `package.json`'s
`exports` name the tsdown file names, not tsc's. `reset()` is the only
clear and clears everything; `fail` collapses the server's `once`/`n` to
one `times`.

**`A14` (2026-09-02) is `MOCKER_STREAM_TRAFFIC_FRAMES=all` — §30.13's
"own retention budget", built as a budget per ROW and not as more rows:**
a connection is still ONE traffic row, so frames can never evict ordinary
rows, and `MOCKER_STREAM_TRAFFIC_MAX_FRAMES` (200) and
`MOCKER_STREAM_TRAFFIC_MAX_BYTES` (`64kb`), each way, bound what that row
holds. `internal/mockplane/traffic.go`'s `frameLog` is the one type
behind all three modes: nil under `off`; one frame CUT at
`MOCKER_TRAFFIC_MAX_BODY` under `first` (the pre-A14 bytes, and a later
frame is neither kept nor a truncation); whole frames only under `all`,
the first frame that does not fit — by count or by size — marking the log
truncated and closing it, because half a JSON object in an NDJSON body is
worse than a flag. SSE frames concatenate on the wire's own blank line;
WebSocket frames go one per line each way, the inbound side still only a
TEXT frame holding a JSON object, redacted per frame (`reqContentType`
`application/x-ndjson` under `all`). The row's `truncated` now also means
"a frame budget stopped the log", and the notes gain
`frames_recorded:N` (`frames_in_recorded:M` for WebSocket) under `all`
beside the unchanged `frames:N`/`frames_in:M` counts. `config.Limits`
carries both budgets (a schema change on `ServerConfigView.limits`, and
the enum gains `all`); the two P6b/P6d carve-outs that refused `all` are
closed. No route, no tool, no migration, no screen.

**`A13` (2026-09-02) is the per-target clear the plugin exposed the lack
of**: `livestate.Store.Delete(workspaceID, target, action)` — `Clear`
narrowed by key (every action on the target, or one), the same broadcast,
so a request parked on the deleted pause is released and one parked on
another target stays — reached by an OPTIONAL body on BOTH planes'
existing DELETE (`DELETE {prefix}/state` and `DELETE /api/workspaces/{id}/session`,
`{target, action?}`; no body is the pre-A13 clear-all; a body naming no
target is refused, never read as "everything"), by `clear: true` on
`set_session_directive` (which DELETEs then GETs, so `directives[]` is
what remains — the tool's `toolRoutes` row gained the GET), and by
`mock.clear(target, action?)` in `@yashok111/mocker-test`. Contract: a schema change
on an existing route (an optional `SessionClearRequest`), no count
change; no migration, no variable, no screen.

**`A15` (2026-09-03) is the audit: five monolith files split by pure
text moves, then a five-reviewer pass (hot path, concurrency, SQL,
admin/MCP/config, generator) whose findings were fixed with a test each.**
The splits changed no text: `internal/resources/repo.go` →
`scope.go`/`confirm.go`/`decline.go`/`entity_read.go`/`entity_write.go`/
`workspace_tx.go`, `internal/checkpoints/repo.go` →
`capture.go`/`apply.go`/`write_tx.go`/`rollback.go`/`auto.go`,
`internal/specs/repo.go` → `suggestions.go`/`read.go`/`write.go`,
`internal/mockplane/respond.go` → `negotiate.go`/`delay.go`/`variant.go`,
`internal/admin/server.go` → `route_table.go`. Eight things the fixes
established that cannot be guessed from the code. **A document value
leaves the spec through ONE door, `gen.cloneDocValue`**: `leafValue`,
`depthLimitValue`, `hardCeilingValue` and `minimalScalarValue` used to
return a schema-level `example`/`const`/`default`/enum member BY
REFERENCE, and the list walker wrote the row's id INTO it — every row of
a page was the same document map, and two concurrent anonymous requests
wrote it together, which is a fatal `concurrent map writes` that kills
the process, not a panic the middleware recovers. **`gen.jsonSize` is the
budget's sizer and must stay byte-exact**: `estimateJSONSize` was a
reflective `Marshal` per scalar, per item and per required floor, the
single largest cost of a generated body; the sizer reproduces
`encoding/json`'s escaping and float rules and `jsonsize_test.go` holds
it to the byte, because the byte-budget tests and the 419-body golden
both move on a one-byte disagreement (measured, `BenchmarkBody_fullCorpus`:
1.34 s → 0.85 s, 3.9 M → 1.6 M allocations, with the floor pass reused
in `walkObject`, the resolver memo on a `sync.Map` and `valuesEqual`'s
scalar fast path). **The generator's cost model now counts what it
prices**: `hardCeilingValue`'s required-subtree recursion charges
`visitNode`, a oneOf/anyOf branch hop is bounded by `maxCompositionHops`
(a self-referencing `oneOf` recursed ~10^5 frames at the SAME depth
before `maxWalkNodes` tripped), and `toFloat64` clamps a schema number to
±2^53 (a `maximum: 1e308` gave a span of `+Inf`, a modulo by zero on
arm64). **The login limiter has two buckets**: the `(address, name)` one
that keeps a flood under one name from locking a colleague out, and a
per-address one at `addrLimitMultiplier` × the limit — without it a fresh
name per attempt was a fresh 10/minute budget against the ONE shared
password; the map is capped at `maxLoginBuckets` and the name is keyed at
64 runes. **A stored status is 100..599** (`overrides.ValidHTTPStatus`,
shared by `internal/customep`): `WriteHeader` panics outside it, and a
pinned `activeStatus: 999` was a 500 with a stack trace on every request
to that operation. **`entities`' UNIQUE does not span `base_scope_key`**
(0003's premise, broken by A11's operator-chosen key): `resources.Repo.Set`
answers `ErrEntityKeyConflict` (409) for the same key under another base
value, and `ErrEntityKeyNotCanonical` (400) for a key that does not
round-trip through the family's id type — `gen.CoerceIDValue` HASHES an
unparsable key rather than failing, so `PUT .../entities/abc` on an
integer family stored a row whose key and id disagreed. **`forked_from`
is `NO ACTION`, not dangling**: `workspaces.Repo.Delete` detaches every
copy before the delete (`CARVE-OUTS.md`'s P4b entry is corrected in
place). **The admin feed's lifetime is a one-shot timer**: `drain`'s
non-blocking select used to consume it and return nil, so under exactly
the steady traffic that drains, D10's 900 s was lost; `errLifetimeExpired`
carries the fact out. Beside those: the import (`POST
/api/workspaces/import`) refuses a browser-executable media type and
runs the bound-spec base-path check like `PATCH` does (which now runs it
on a `specId`-only body too); `bundle.Validate` checks
`resources[]`/`decisions[]` and `ValidateData` requires an object body;
`config.Load` refuses a reserved prefix that trims to empty, a host with
a port, a body size under 1kb and a size that overflows, and `main`
refuses an argon2 hash the library would panic on; `internal/yamlx`
walks `yaml.Node` so `1.0` stays `1.0` and an unquoted date stays a
string; the recorder caps stored headers at the body cap
(`truncated:headers`), copies a cut body, counts a failed batch as
dropped, writes under a detached context and keeps writing while a full
batch waits (one batch per tick capped it at 128 rows/s); `http.Server`
gets `MaxHeaderBytes` 64 KiB; the runtime build under singleflight runs
under `context.WithoutCancel` (one client aborting failed every joiner
with 500); `routeCache.get` takes a read lock on the hot path; the
envelope and a collection body are built with `jsonx.Compact`, never a
reflective marshal; a rollback decodes `data_snap` before the write
transaction; `CAST(entity_key AS INTEGER)` is guarded against a
19-digit key saturating `seq`; migration `0007_fk_indexes.sql` indexes the
seven foreign-key columns that had none (a `DELETE FROM resources`
scanned three tables whole). Refused on policy and left for the owner:
treating `+xml` media types as browser-executable (it would break XML
mocking) and the probe route's residual port choice.

**`A16` (2026-09-03) is `mocker setup`, the install wizard for a
colleague's own machine — the owner's choice after `A15`: every colleague
runs their own mocker for local development, all three OS, the image
built from the checkout, the wizard a subcommand of the one binary.**
`cmd/mocker/setup.go` (+ `setup_env.go`, `setup_compose.go`,
`setup_platform.go`) does what README's "HTTPS" section did by hand:
docker/compose ≥ 2.30 checked; `.env` rendered from `.env.example` with
`auth.HashPassword` in-process (no `docker run` to mint the hash),
`MOCKER_ROUTING=path` fixed (one machine has no wildcard DNS), an
optional `MOCKER_MCP_KEY`, and an existing `.env` never touched
(`scripts/init-env.sh`'s rule); `scripts/compose-tls.sh` re-implemented
as `composeEnv` (the same subnet arithmetic, the same bare-host check,
`COMPOSE_ENV_FILES` pointed at the null device) because bash is not a
given on macOS or Windows; `docker compose up -d --build` through the
overlay pair; the CA root exported with a retry while Caddy starts;
readiness through `probe.ReadinessTLS` — the probe package's THIRD
caller, the same client with one trusted root and the admin host as TLS
ServerName, because the wizard dials loopback; the hosts line and the
trust-store install as per-OS PLANS (`trustPlan`, `hostsCommand`:
Debian/RHEL anchors, `security add-trusted-cert`, `certutil -addstore`)
that are tested on every OS from this one and degrade to a printed
manual line when sudo/admin is missing. The process `chdir`s into the
checkout so no `-dir` value reaches a file operation or an argv. Three
verbs (`up`, `down`, `status`), `make dist` cross-compiles the six
targets (CGO_ENABLED=0; modernc sqlite is pure Go). Found on its first
live runs, TWO ways `make up` at HEAD had been red with nothing green
noticing: `web/src/test/fixtures.ts` lacked A14's two `limits` fields
(the local generated client was stale in the opposite direction, so
`make ui-test` passed — `make ui-gen` first is what would have caught
it), and the Dockerfile's webbuilder stage never copied
`docs/USER-GUIDE.md`, which `GuidePage.tsx` imports `?raw` since A7 and
which `.dockerignore`'s `*.md` kept out of the context (one named `COPY`
and one `!` exception now; `make smoke` would have said so the same
evening). No route, no tool, no variable, no migration; nolint 29 → 32.

**`P7a` (2026-09-03) is API design on top of a workspace — DESIGN §34
(v12), the server and the agent; `P7b`, the screen, is the same gate
document's second slice.** A design is a base plus a delta and §4's four
layers already are that; what the Workspace layer lacked was a RESPONSE
SCHEMA. Eight things about it cannot be guessed from the code. **`schema`
lives on `overrides.Variant`** — one response type for the contract, the
bundle and the MCP inputs — and is REFUSED BY NAME on an op_overrides
row (`400 schema_on_override`: a spec operation already has a schema,
`schemaPatch` is how it changes); `overrides.ValidateSchemaShape` is the
document-free half (an object, at most `jsonpatch.MaxPatchBytes` —
exported for it rather than copied), `customep.ValidateRefs` the half
that needs the bound document. **A `$ref` is never STORED dangling
(D6)**: every writer that could break that resolves every `$ref` of the
rows it writes against the document the workspace will hold AFTER the
write — the two endpoint writers (`400 schema_ref_unresolved` naming the
pointer; with no spec bound ANY `$ref` is refused), `PATCH
/api/workspaces/{id}` with a `specId`, `POST /api/workspaces/import` and
`POST .../rollback/{cid}` (`409 endpoint_ref_unresolved`, one detail row
per endpoint, nothing written) — and the SERVE path tolerates what slips
past (a hand-run `UPDATE`): `buildCustomInline` logs once per build and
generates `{}` at that node, the identical tolerance a failed
`schemaPatch` already has. The `refResolverOf` guard in
`internal/admin/design_handlers.go` exists because a nil
`*openapi.Resolver` stored in the `RefResolver` interface is not a nil
interface — the first draft panicked on the first `$ref` of a no-spec
workspace, and a test caught it. **Serving enters the seam, never a third
`gen.Body` site**: `serveCustom` branches to `serveCustomGenerated` for a
non-pinned variant with a schema, which builds a `resolved` with
`Inline` (the decoded schema plus the row's compiled recipes, built once
per runtime in `custom_schema.go`) and calls `assembleResponse` —
`TestAssembleResponseIsTheOnlySeam` names it as the third caller. The
mode rule of §8 stands: schema + pinned body → the body serves, the
schema is only the export's declared shape. **The generator exists
WITHOUT a spec** (`buildRuntime` builds it over `design.Skeleton`, the ONE
skeleton the export composes from too), which is what makes "a design
from nothing" serve; `variants`, `specRoutes` and `patchedSchemas` keep
their spec gate. **The operation fields are ONE JSON column**
(`custom_endpoints.operation`, migration `0008`, ADD-only, NULL for
every earlier row); `operationId` is UNIQUE across the workspace's
custom rows AND the bound spec's operations (`409 operation_id_taken`
naming the holder, checked inside the write transaction through
`json_extract` — the one query the column ever answers), on the two
endpoint writers only: a restore or an import never re-checks it, so a
rebind CAN produce a collision the export then writes twice
(`CARVE-OUTS.md`). `reqSchema` stops being preserved-only: validated as
a schema on write, exported as `requestBody`, never enforced on a
request. **The export is `internal/design.Compose`**, a LEAF over the
normalized base and decoded rows (no store), whose output is run
through `openapi.Load` and returned as `Normalized()` bytes — so the
document is a FIXED POINT of `Load` and re-imports to the same bytes,
which is what D8's round trip (`scripts/smoke.sh`, P7a observation 7)
observes: export → `import_spec` → `PATCH specId` → the drift names every
delta row → delete them → export again → equal after `info.version`.
The accept step is three existing verbs and not an `accept_design` tool
on purpose: a `schemaPatch` applied a SECOND time over a base that
already carries it fails and that variant serves unpatched, so the delta
MUST be deleted after accepting, and the guide says so. **Bundle v5
READS v4** — the one departure from `P6b`'s refuse-the-old precedent,
because `A16` shipped an installer the day before and a colleague's v4
checkpoint is now plausible. **The export is a `GET` and joined no
auto-checkpoint exclusion map**: that map holds MUTATING routes only,
which the decisions document (D13, "20 → 21") had wrong.

**`P7b` (2026-09-03) is the «Контракт» tab — DESIGN §34.5's screen half,
the tenth tab, the same gate document's D12, built the same day as `P7a`
on the owner's word («сделай p7b»).** `ContractPage.tsx` reads three
routes — `GET .../openapi.json` (the export, `P7a`'s `EXEMPT` entry
withdrawn), `GET .../operations` and `GET .../endpoints` — and renders the
document read-only: paths → operations → «Запрос» / «Ответы» → the schema
tree (`SchemaTree.tsx`, hand-rolled, no dependency: the owner's call over
`swagger-ui-react`). Four things about it cannot be guessed from the code.
**The badges are computed on the CLIENT from the two editor routes, never
from a marker in the document** (`contractBadges.ts`, pure, keyed by the
operation's literal `METHOD path`): a custom row with `overrideOn` whose
canonical shape no spec operation has is «добавлено», at a spec shape
«изменено» (it replaced that operation, rule 3), `routeOff` on either
«удалено»; a spec operation whose override is on and carries a patched
schema or a pinned response is «изменено», a RECIPES-ONLY override stays
«база» (values move, the shape does not); a switched-off row or override
is nothing. The list view's per-status summary gained `hasSchemaPatch`
for exactly that predicate — a schema change on an existing route, the
one server change of the slice — because the patch itself stays behind
`GET .../operations/{opKey}`. **A `$ref` renders as the component's NAME,
collapsed, and expands from `components` on click**, one level per click,
so a self-referencing schema is recursion-safe by construction and no
depth limit exists. **The editors accept a selection through search
params**: `/workspaces/$id/endpoints?endpointId=N` opens that row's edit
form, `/workspaces/$id/operations?opKey=…` selects that operation once the
list has loaded (a `useRef` guard, once, keyed on the query's own data —
oxlint's exhaustive-deps refuses a per-render array), and «Открыть в
редакторе» on every non-«база» row navigates there; the two route files
gained `validateSearch` (arktype) for it. **«Скачать» hands the FETCHED
document to the browser** — one `createObjectURL`, one click, revoked —
never a second fetch, and the test counts both. §14's word rule is a
test: none of "patch", "recipe", "matcher", "schemaPatch" renders. The
smoke's path-mode block checks the deep link reloads (`B7`); no route, no
tool, no migration, no variable.

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

**`A5` (2026-09-02) made the compose stack one command and the reverse-proxy
contract a test.** Three things about it are not visible from the files.
`up` depends on the FILE `.env`, so a fresh clone's first `make up` runs
`scripts/init-env.sh` (copy the example, build, mint the hash with the
image's own `hash-password`, print the generated password once) and an
existing `.env` is never rewritten by any target — `make init` on one says so
and exits 0. `docker-compose.tls.yml` is an OVERLAY, never run bare: it needs
`MOCKER_BASE_DOMAIN` and `MOCKER_ADMIN_HOST` exported for `${…}`
interpolation, and `.env` is loaded `format: raw` (not interpolated) as
mocker's env_file only — `scripts/compose-tls.sh` reads the two names out of
the file (always the file, never the caller's shell, so Caddy's site blocks
cannot disagree with the names mocker started with) and refuses to give
Caddy the whole `.env`, which would hand the password hash to a second
container. The overlay forces `MOCKER_DEV=0`, gives Caddy a STATIC address on a
FIXED /24 (`MOCKER_TLS_SUBNET`, 172.30.10.0/24; gateway .1, Caddy .254 —
mocker starts first and takes .2 dynamically — both derived in
`compose-tls.sh` and nowhere else) and sets `MOCKER_TRUST_PROXY`
to that ONE address — never the subnet: an address or CIDR is the only
shape that variable takes, a service name is not one, and the subnet's .1
is the docker HOST, from which mocker's unpublished :8080 is still
reachable on the bridge, so trusting the subnet would let any local
process forge `X-Forwarded-For` into the traffic log and rotate the login
limiter's key per attempt (the security review's one major finding) — and
empties mocker's published ports with YAML `!reset` (compose ≥ 2.24; an
overlay otherwise APPENDS, leaving a plain-http door with a Secure cookie
nobody could log in over). `deploy/Caddyfile` pins `X-Forwarded-For`/
`-Proto` with `header_up` rather than relying on Caddy's default, and
carries no HSTS on purpose (`CARVE-OUTS.md`, `A5`). `deploy/Caddyfile` replaced `Caddyfile.example` — the old file said
"ILLUSTRATIVE ONLY — not exercised" and it was; the new one is what
`make smoke-tls` runs, `tls internal` issuing the wildcard from Caddy's own CA
at startup. Two facts the first run paid for: the dev box exports
`HTTPS_PROXY` (a local egress proxy), and a `curl --resolve` to
loopback still sends a CONNECT for `mocker.local:8443` through it — every
curl in `smoke-tls.sh` carries `--noproxy '*'`, and any new https check
must too; and `docker compose port` answers `invalid IP:0` with exit 0 for
an UNPUBLISHED port (compose v5.5), so "is :8080 still published" is asked
of `docker port <cid>`, which prints nothing.

**Configuration — through the environment only: 36 `MOCKER_*` variables, all with
defaults in `internal/config`, the full list with comments — `.env.example`.**
`MOCKER_MAX_ASSET` (`8mb`, one uploaded file; floor `1kb`; may not exceed
`MOCKER_MAX_BODY`, whose limit would otherwise refuse the upload first with
a 413 naming the wrong knob) and `MOCKER_MAX_ASSETS_TOTAL` (`64mb`, a
workspace's stored sum; may not be below the per-file cap) are `A6`'s two,
read by `assets.Repo` inside the upload's own transaction — `loadAssets`
in `internal/config`, the same sub-function shape `loadStreamWS` has.
`MOCKER_STREAM_MAX_CONNS` (200, per WORKSPACE, `0` refuses every stream
handshake — the one `MOCKER_STREAM_*` zero that means something),
`MOCKER_STREAM_MAX_LIFETIME` (900, ≥ 1; the mock plane's, the admin feed keeps
its constant) and `MOCKER_STREAM_TRAFFIC_FRAMES` (`off` \| `first` \| `all`
since `A14`) are `P6b`'s three, the mock plane's;
`MOCKER_STREAM_TRAFFIC_MAX_FRAMES` (200, ≥ 1) and
`MOCKER_STREAM_TRAFFIC_MAX_BYTES` (`64kb`, ≥ 1kb) are `A14`'s two, `all`'s
per-row budget each way, read nowhere but under `all`.
`MOCKER_STREAM_MAX_FRAME` (`64kb`, the INBOUND WebSocket frame cap, a
bigger frame closes with 1009), `MOCKER_STREAM_SEND_BUDGET` (`256kb`, the
per-connection byte bound of the reactive/echo reply queue — over it a
reply is dropped and counted, never blocks) and `MOCKER_STREAM_ORIGINS`
(empty = any; a list of `scheme://host[:port]`, parsed like
`MOCKER_URL_IMPORT_ALLOWLIST`, each element validated at startup; a request
with no `Origin` is always allowed) are `P6d`'s three; both sizes are
floored at `1kb`.
`MOCKER_STREAM_PING` (15), `MOCKER_STREAM_FRAME_TIMEOUT` (5) and
`MOCKER_STREAM_SESSION_RECHECK` (60) — `P6a`'s three, integer SECONDS read by
the same `count()` as `MOCKER_CHECKPOINT_DEBOUNCE` and converted to a
`time.Duration` in `cmd/mocker/main.go`, never inside `internal/stream`; unlike
every other `count()` value, **`0` fails startup for all three** — a zero ping
or recheck interval is a ticker that fires continuously and a zero frame
deadline expires every write before it is attempted, and none of them has a
"disabled" reading.
`MOCKER_MCP_KEY` — the bearer key of the MCP endpoint (see "Architecture" and `README.md`);
empty (the default) — `/mcp` is not mounted at all, a non-empty value shorter than 32
bytes fails startup, as with every other variable here.
`MOCKER_DEFAULT_SPEC` — **the numeric id of an already imported spec**, not a name (not
unique) and not a path (nothing at startup reads a document from disk or from the network).
Set — a user's first login with zero workspaces creates one for them from that
spec (§14 screen 2); pointing at nothing — **a crash at startup**, not silent
inaction.
Four decide routing: `MOCKER_ROUTING` (`host`/`path`),
`MOCKER_ADMIN_HOST`, `MOCKER_BASE_DOMAIN` and `MOCKER_RESERVED_PREFIX`.
The admin host is **forbidden** to sit under the base domain — the config crashes at startup
with `MOCKER_ADMIN_HOST (…) must not sit under MOCKER_BASE_DOMAIN (…)`, and that is exactly
why the browser cannot derive a workspace address from `window.location`.

**The scope of the Go bars is `./cmd ./internal`, never `./...` and never a bare `.`.**
`web/node_modules` sits inside the Go module; a single `.go` in any transitive
npm dependency would break `go build ./...`, `go vet ./...` and `gofmt -l .`
irreparably, and `gofmt -w .` would additionally rewrite someone else's sources. `gofmt`
gets two directories, not a list from `git ls-files` — that one does not see new
untracked files.

**The linter is golangci-lint v2**, `.golangci.yml` carried over from another backend of the owner's,
so that the set is one across two repositories. Keep it at zero. Exceptions are only
pinpoint `//nolint:<linter> // reason` at the site of the trigger, not widening the
config. There are **34** of them now (`rg -c '//nolint' cmd internal`, summed), and each
carries the reason right in the line: twelve `gocyclo` on functions where the branching is the
specification (a branch per schema keyword, per recipe kind, per refusal
reason, plus P3d's own `rollbackTx` — a branch per D7 refusal); eleven `gosec` — cookie `Secure`
being a parameter for the sake of `MOCKER_DEV`, serving
a body by the mock plane (its media types are already filtered out on write), reading a fixture in
a test helper, an intentionally oversized test write, P3c's `ref` recipe's modulo-by-length
entity pick, P3f's `dumpTableRows` test helper interpolating a table name that comes from
`sqlite_master` itself and never from a request, and A6's asset route writing an operator's
own upload (its type refused at upload and again at serve), and A16's three in
`cmd/mocker/setup.go` — two G204 on the wizard's docker/sudo exec (running
those IS the job; every variable argument is validated first) and one G703
on writing `.env` (the path is a constant, the taint is the content); one `errorlint` — `recover()` gives `any`, not an error chain; one `contextcheck` — P6d's `wsLoop.run` takes `conn.Context()`, Registry.Open's child of the request context, because the registry's cancel is what must end a WebSocket loop and the linter cannot see the derivation (P6d's two `gocyclo` are `wsLoop.run` and `wsLoop.loop`, one select case per producer);
two `errcheck`, one of which (the oversized test write above) shares its line with a `gosec` tag
rather than adding a nineteenth line, the other `defer tx.Rollback()`; two `nilnil` — P7a's `resolverForSpec` ("no spec is bound" IS the answer, and every caller reads a nil resolver as exactly that) and `operationFromJSON` (a snapshot that declares no operation). Two relaxations in the config, both along the
way: `gocyclo` does not count `_test.go` (table-driven acceptance reaches 74 and stays
readable) and G115 does not count `internal/{gen,recipes,auth}` — there a uint64 truncation
of a deterministic PRNG is the algorithm, and the 419-body golden guards it.

**goleak is in every package with tests** (35 of them since `P7a` added `internal/design`, after `A8` added `internal/yamlx`, after `A7` added `internal/guide`, after `A6` added `internal/assets`, after `A5` gave `cmd/mocker` its first test file beside `P6d`'s `internal/wsmock`, `internal/*/main_test.go` and `cmd/mocker/main_test.go`
— three lines per package, the whole ignore list with reasons once in
`internal/testleak`; `internal/store` — `store.go` since P0, but without a single test
until A3, whose `AllocateEditVersion` gave it both its first file and its first harness
at once). A goroutine that outlives a package's tests fails it. Do not extend the
ignore list: it holds only what the runtime parks for the whole process
(`database/sql` opener/resetter, sqlite VFS). Everything else is a finding, and
usually it is a `Run`/`Close` protocol that returns earlier than its own
goroutines; exactly that already lost the last traffic records at shutdown.

**A green `go test` ≠ the phase is done.** The project twice caught a fully green
suite with a feature dead in prod: the tests wire the dependency explicitly, and
`cmd/mocker/main.go` does not. The real check is `make smoke`: it builds the
image and pokes a live stack. Verify with commands and their output, not with agents'
reports.

## JSON — only through `internal/jsonx`

The backend is **`encoding/json`**, but **not a single production file imports it
directly** (`internal/jsonx/boundary_test.go` checks by walking the AST and fails the
build on a violation). The wrapper is a seam that makes the backend choice a decision
rather than a rewrite: one `var api` line changes, and the tests next to it are what
makes such a swap safe.

**bytedance/sonic was integrated in full and rejected by measurement, not by taste:**

```
marshal, microbenchmark     4771 → 2692 ns/op (29 → 8 allocations)
unmarshal, microbenchmark  10261 → 4320 ns/op
generating all 419 bodies   0.524 → 0.452 s   (6 runs each, ~14%)
```

The one-and-a-half-fold microbenchmark win collapses to 14% through the real
path: generation is dominated by schema traversal, the PRNG and validation, not marshaling.
Against it — five extra modules, including a JIT that writes executable memory at
runtime (a strange addition to a product whose entire delivery story is one
static CGO-free binary into a closed network), `-race` on `internal/gen`
28 s → 86 s, and a hard dependency on whether sonic keeps up with Go releases.

**Two traps that attempt exposed** — because of them the wrapper is worth its own
file even today:

1. **Output stability.** Default sonic does not sort map keys and does not
   escape HTML (only `sonic.ConfigStd` matches stdlib). Go
   randomizes map traversal on every run, and the whole point of the project is that one
   seed and one spec give byte-identical bodies. A backend taken for speed
   without checking that property would break the golden **every other time** — the worst
   possible form of failure. `TestMarshal_isStableAcrossRuns` holds the line.
2. **Error taxonomy.** sonic returns `*decoder.MismatchTypeError` where
   stdlib returns `*json.UnmarshalTypeError`: an `errors.As` on the standard type
   keeps **compiling** and silently stops matching — a malformed login
   body starts getting 500 instead of 400. Therefore one must ask
   `jsonx.Malformed(err)`, and a new backend is obliged to extend it. The requirement is
   held not by a comment but by a test: it decodes through `jsonx.NewDecoder` and
   checks through `jsonx.Malformed`, i.e. it goes red on any backend.

The types `RawMessage`, `Number`, `Marshaler`, `Unmarshaler` are aliases to stdlib.
**The tests are deliberately left on `encoding/json` directly**: a test that builds
the expectation with the standard library and compares it with `jsonx`'s output is a continuous
cross-check of whichever backend is configured right now.

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
screens call them now; an exemption is a
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
does not measure itself under jsdom and the tests would assert nothing).

Screens: `/` (workspaces), `/specs`, `/guide` (the operator's manual,
`docs/USER-GUIDE.md` compiled in through a `?raw` import and rendered with
`marked` — no API call, so no contract entry; `A7`), and `/workspaces/$id` — a layout with ten
tabs (overview, endpoints, custom, traffic, scenarios, history, resources,
since `P6e` connections, since `A10` files, and since `P7b` contract). **`/w/$id`
is gone**: in path mode `/w/{slug}` belongs to the mock plane, and one
URL layout working in both modes is better than two that depend on a server
setting.

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
variable).
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

**Where the decisions live.** Every slice since `A1` was designed in its own gate
workspace outside this repository — SEVENTEEN of them (`mocker-a-mcp`, `mocker-a3-cas`,
and one each for `P3a`, `P3b`, `P3c`, `P3d`, `P3e`, `P3g`, `P3h`, `P3f`, `P4a`, `A4`, `P6a`, `P6b`, `P6c`, `P6d`, and `mocker-p7-api-design` for `P7a` AND `P7b`, one document for both) — and each holds
the decisions document that is the authority on WHY the slice is the way it is.
They are all OUTSIDE this repository and survive only while those directories do,
so anything that must outlive them is written here or in `HISTORY.md`.

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
