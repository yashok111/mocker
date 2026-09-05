# mocker — what is deliberately absent

**Not auto-loaded.** Every entry here is a hole that is DEFERRED or DECIDED, never
forgotten. Read this file before concluding that something missing in the tree is a
bug, and before a slice claims to close one of these — several entries record an
approach that was tried and reverted, with the measurement.

`CLAUDE.md` is the per-session context; `HISTORY.md` is how each slice arrived;
`DESIGN.md` §23 is the state as built.

Not "forgotten" but deferred — so that a hole does not read as an oversight:

## P2 UI debt, scenarios, checkpoints, `schema_patch`

- Monaco and the schema tree with live preview — together with the editor that
  requires them (§19 gives them to P2). `POST .../preview` already exists — see P2f below —
  but that is one button and one panel on top of the already existing editor, not
  Monaco with a schema tree.
- A recipe editor. Recipes are **shown** (kind and count in the status — that is what
  explains why a body looks the way it does), but not edited: P2.
- stateful resources — shipped, `P3a`–`P3h`. WS/SSE in traffic — still unbuilt,
  but no longer merely "P2": `DESIGN.md` v10 §30 designs it as `P6a`. Scenarios (a snapshot of a
  workspace under a name and switching to it) — P2b, and checkpoints and rollback
  (the history of the `Workspace` layer, retention, `reset-overrides`) — P2c; cloning
  and renaming a scenario, plus the debounce trigger and checkpoint deletion —
  P2d; applying `schema_patch` to the generated body and three recipes —
  `faker`, `template`, `sequence` — P2e; all four slices exist already, and their
  own carve-outs are these:
- **Scenarios: there is no editor.** The contents of a scenario are obtained only by
  bringing the workspace into the desired state and taking a snapshot off it — a separate
  editing screen will not exist.
- **Scenarios: custom endpoints do not live inside the snapshot.** DESIGN §12 —
  "`settings` + selected overrides"; `runtime.custom` is keyed by the DB row id,
  which a record inside a BLOB snapshot does not have.
- **Scenarios: a rename is a breaking change for anyone who addresses a
  scenario by name on the mock plane.** The mechanics ("why it is visible immediately
  without a `revision` bump") are in CLAUDE.md "Architecture"; the consequence is recorded here:
  `PUT` checks only for a conflict INSIDE the workspace (`UNIQUE (workspace_id,
  name)` → 409), not whether anything OUTSIDE references the old name — another team's
  test suite that hardcoded `{"scenario":"old-name"}` into its
  `POST {prefix}/state` finds out only from the mock plane's not-found.
- **Checkpoints: `data_snap` and screen 10's checkbox «вернуть и данные
  ресурсов» shipped in P3d** — its own carve-out list is below, next to P3a's,
  P3b's and P3c's.
- **Checkpoints: there is no media-type guard in the codec** — that belongs to the P4 importer (C16).
- **Checkpoints: the label of an auto row is a constant per route PATTERN, not per
  concrete operation.** DESIGN §13's label example in the `checkpoints` schema
  («закреплён ответ
  GET /quizzes») names a concrete endpoint; `autoCheckpointLabels`
  (`internal/admin/server.go`) gives one string for the whole pattern — «правка
  операции» for ANY `PUT .../operations/{opKey}`, regardless of
  which `opKey` is in the path. Distinguishing a concrete operation would mean reading the path
  or the body inside the shared wrapper — and the wrapper sits in `routeMux()`, before
  dispatch, where there is nothing but the pattern for that yet.
- **`schema_patch`: no deep pointers and no cross-variant rule.** A pointer
  that goes inside a nested `$ref` falls off at the moment of APPLICATION, and the whole
  patch is discarded entirely rather than applied partially — making it
  work would mean eager `$ref` resolution inside the patcher, and that is
  a separate and far larger change, not this one. Patching the 200 of
  `GET /users/{id}` and not patching the 200 of `GET /users` is a deliberately acceptable
  asymmetry (a detail card may get a field that a list row does not
  have): a patch lives on a variant, not on an operation, and a screen tying two
  variants together is a separate follow-up, not this slice.
- **`schema_patch`: a patched variant stops serving the whole-body
  `example`.** This is a visible change, not a side effect: if the body as a
  whole had an example, the applied patch displaces it. A LIST operation whose
  `example` is declared on the ITEM's schema rather than on the response body keeps its example
  — a patch aimed inside the item is invisible to it.
- **The `ref` recipe shipped in P3c** — its own carve-out list is below, next to
  P3a's and P3b's.
- There is no screen for applying `schema_patch` nor for the three new recipes;
  this slice does not touch `internal/mcp`. `DESIGN.md` was not edited — the document already
  names both capabilities as part of the designed product, there is nothing to edit
  in it.
- **DESIGN.md lagged behind the code in five places; v4 closed all five, and this
  item stays here as a record of WHAT exactly diverged.** `POST /mcp` was missing
  entirely (now §14.2); the threat model did not know about the second subject — an
  agent with a bearer key that got the same power as the operator at the screen
  (now §15); `DELETE .../checkpoints/{cid}` did not make it into the route table
  (now it is there); `MOCKER_CHECKPOINT_DEBOUNCE` was named nowhere, the debounce was
  described in words only, and on top of that "in minutes" instead of seconds (now §13 and §16);
  and compare-and-swap on write was not described at all — neither `edit_version`, nor
  the mandatory expectation, nor `409 edit_conflict` (now §14.1). The state as
  built is §23, the list of v4 edits is §24. **The rule did not change: the agent does not edit
  DESIGN.md.** v4 was made on the owner's explicit request; the next divergence
  is closed the same way, not in passing along with the code.
## Bundle, import, the session directive, unread handles

- **Bundle: export/import over HTTP is P4** (`DESIGN.md` §19, the P4 line), and
  `scenario_id` in a snapshot is never restored — there is no
  `activeScenario` field in the bundle, and this slice does not introduce one.
- **Bundle: the version stayed at `mockerBundle: 3`.** An earlier plan (A16)
  expected P2c to bump it to v4; instead the `endpoints` emptiness rule moved
  into `scanScenario` — see the `CurrentVersion` comment in
  `internal/bundle/bundle.go`.
- **Workspace: there is no incarnation column.** Deleting a workspace and creating
  a new one with the same id, the same slug, in the same second and with the same revision,
  during an in-flight operation, is a residual window, accepted rather than closed;
  instead of an incarnation, restore checks the triple
  revision/created_at/slug inside the transaction (`fenceTx`): id is the row selector, identical in
  both reads, it cannot diverge and therefore proves nothing, and
  revision alone does not prove the workspace's identity, so slug and created_at
  are checked next to it (details — `internal/checkpoints`).
- Re-importing a spec with orphaned overrides (§5): `POST .../migrate-workspaces`
  does not exist, it is P4.
- A Swagger 2.0 converter, YAML input, spec import by URL. Import refuses them with
  an explicit message, not with a silent zero operations. **YAML closed by
  `A8` (2026-09-02)**: `internal/yamlx` renders it to JSON before the parser;
  Swagger 2.0 and URL import stand.
- The `scenario` field in the session directive: since P2b this is no longer "not implemented". On
  the admin plane (`POST .../session`) it answers **400** rather than switching the
  scenario — DESIGN §14's session body (lines 857-861) never described this field,
  it reached the decoder only because the shape is shared with the
  mock plane (`internal/livestate.Directive`), and scenarios have their own
  dedicated pair of routes (`POST .../scenarios/{sid}/activate`,
  `POST .../scenarios/deactivate`, see the `Scenario` layer in CLAUDE.md "Architecture").
  On the mock plane the same key is, on the contrary, a working switch:
  `POST /__mocker/state {"scenario":"<name>"}` activates by name,
  `{"scenario":""}` deactivates (an empty string, not `null` — `null` means
  "the key was not there at all"). The session directive's `delay` and `pause` actions
  are still alive alongside `status` and `fail`.
- The `validateRequests` handle (in settings) and `validateReq`/`failDirective` (in an
  override): declared in the models, **not read by the mock plane**.
  They are persisted on write, but there is no toggle for them — it would be a lie
  about what the server does.
## MCP

- **MCP: the stdio transport** (a subcommand like `mocker mcp`) — a dozen lines
  on top of the already finished tool layer, but not in this slice.
- **MCP: OAuth 2.1 / dynamic client registration** — disproportionate for
  synthetic data in a trusted network; the bearer key in
  `MOCKER_MCP_KEY`, compared in constant time, closes the same
  question more simply.
- **MCP: resources (`resources`) and prompts** — the first version has `tools` only.
- **MCP: tools that destroy PERSISTED state — since A2 they exist, except
  one.** `delete_workspace`, `clear_traffic`, `delete_checkpoint`,
  `delete_scenario`, `rollback_workspace`, `reset_overrides`, and since P3b
  `decide_resource` (declining a confirmed family) and `reset_resource_data` —
  eight of them, by the owner's explicit decision — require a `confirmSlug` argument: the exact slug of the
  workspace, checked against what `GET /api/workspaces/{id}` returns right now,
  not against what the caller named; a mismatch
  is a refusal that changes nothing, not a silent no-op. The former argument that
  kept them aside ("there is nothing to undo them with yet, checkpoints are only in P2c") was
  wrong and would not have applied to workspace deletion in any case:
  `checkpoints.workspace_id` cascades via `ON DELETE CASCADE`
  (`0001_init.sql:218`), so `delete_workspace` takes its own
  checkpoint history with it — it is irreversible by construction, and the argument
  exists exactly for that reason, not because a rollback once covered it.
  Five of the eight (`delete_workspace`, `clear_traffic`, `delete_checkpoint`,
  `decide_resource`, `reset_resource_data`) do not
  take a pre-destructive snapshot at all — there is nothing to snapshot or nothing to
  restore with; `delete_scenario` is next to them, a scenario's snapshot lives in
  its own table, not in `config_snap`. The remaining two (`rollback_workspace`,
  `reset_overrides`) are in fact undoable — each writes a pre-destructive
  checkpoint inside its own transaction — and carry the same argument for a different
  reason: on a flat tool surface it is easy to miss and hit
  a neighbour, and a miss is a SUCCESSFUL call of the wrong tool, which no
  schema will reject.
  `set_session_directive`'s `clearAll` is still not counted in this list:
  session directives live in RAM only, are never written to SQLite and are set again
  by the same tool in one call — clearing them is not the same
  as erasing a row from the DB with no right to a rollback.
- **MCP: deleting a spec is still not a tool.** The only spec
  that can be deleted at all is the one no
  workspace references: `specs.Repo.Delete` refuses with `ErrAttached` from inside its own
  transaction on any reference, and the handler answers `409` with a list of
  the attached slugs. The tool would answer `409` on any plausible
  call, and in the single remaining one it would irreversibly erase a document
  that would have to be imported again by hand, through the same route
  `POST /api/specs`, which an MCP client cannot reach anyway (see
  `import_spec` below). A tool whose successful case is a refusal, and whose
  unsuccessful one cannot be fixed through MCP, is not worth publishing.
- **MCP: two tools now also leave an auto checkpoint behind, even though
  P2d, like P2c before it, touches not a single file under `internal/mcp`.**
  The operation override and the override from traffic are both in `mcpAllowedRoutes`
  (`internal/admin/loopback.go`) and both among the nine routes marked
  for the debounce trigger (see CLAUDE.md "Architecture"). The trigger sits on
  `routeMux()`, which `CallAsMCP` shares with the ordinary HTTP path, not inside
  the handlers, so a call through MCP passes it on the same terms —
  back then, at P2d, the number of tools did not grow and not one of their descriptions
  changed: only two already existing ones acquired a side effect shared with the admin
  panel.
- **MCP: `scenario` in the session directive** — since P2b the route that
  `set_session_directive` wraps answers this field with **400**, not
  `501` (see about the admin plane above). The tool still does not publish
  such a field: adding it would mean letting the client name a scenario on a
  knowingly wrong route and get a refusal — no better than a field that used to
  be able only to 501. Switching lives on its own admin routes
  (`.../scenarios/{sid}/activate`, `.../scenarios/deactivate`); since A2 they are
  wrapped by the dedicated tools `activate_scenario` and
  `deactivate_scenario` — not `set_session_directive`, which still does not have this field
  and will not.
- **MCP: there is no `import_spec` tool.** `POST /api/specs` accepts
  the **whole** spec document as a JSON string in the body — the CSRF gate strictly requires
  `Content-Type: application/json`, and that decision (§8) was not relaxed for the sake of
  one upload route. There is no server-side import from disk, import by URL
  is rejected with a `400` (`source: "url"`). So the only way for an
  MCP client to reach that route is to drag a document of hundreds of
  kilobytes through the model's context, and that hits this slice's own
  compaction rule harder than any other tool. The role that
  `import_spec` could have closed is closed instead by `list_specs`: without
  it `create_workspace` has nowhere to learn `specId` from.
## Preview (P2f)

- **Preview: the session force is invisible.** `POST .../preview` (P2f) never
  calls `livestate.Apply` — the forced status, `fail_next` and the pause belong to whoever
  set them, not to the operator who is editing a draft right now. Reading them with a
  read-only accessor would mean creating a second implementation of `Apply`'s
  precedence rather than using the existing one. The price is paid honestly:
  `delayMs` in the response is a computed but not slept delay that the
  operation would have paid for real.
- **Preview: a custom endpoint that shadows an operation is a refusal, not a
  substitution.** If a custom endpoint wins by `(method, path)` (it has
  its own literal body form, not an override draft), the route answers
  `409 custom_endpoint_wins` rather than rendering the body of the spec operation, which
  the plane would never serve anyway.
- **Preview: the 406 gate is not performed.** A draft has no `Accept` header —
  preview always answers with a JSON document regardless of the generated body's
  `Content-Type`; this is a deliberate divergence from serving, not a forgotten
  branch.
- **Preview: there is no `recipesApplied` field.** There is `recipesBound` — the
  number of recipes actually passed to the generator (`Set.Len()`, already including
  the synthetic `listSize` binding), but not how many of them FIRED:
  such a field would mean instrumenting `internal/gen`/`internal/recipes`
  for one display field, and those packages are guarded by the 419-body golden.
- **Preview: the zero-import invariant (`internal/admin` ↔ `internal/mockplane`)
  is not tested by anything.** The shared types (`domain.PreviewRequest`,
  `domain.PreviewResult`) are moved into `internal/domain`, which both planes
  already import — but review holds this, not a test.
- **Preview: every call builds a full runtime anew.** `Plane.Preview`
  calls `buildRuntime` directly, never reads and never writes
  `routeCache` — a draft cannot poison what real traffic sees, but
  preview also reuses no runtime between calls.
## A3 — compare-and-swap

- **`A3`: there is no idempotency.** A write repeated after a lost response is
  still a SECOND write, not the same action twice: it allocates a
  fresh `edit_version` like any other. `A2`'s `D16` already recorded this
  limit for the MCP tools, and this slice neither widens nor narrows it.
- **`A3`: there is no conditional `DELETE`.** A deletion racing with someone else's override is not
  made safe by this slice — a token on `DELETE` will be needed by the slice that
  has a caller genuinely asking for it, not by this one.
- **`A3`: a refused write still leaves an auto checkpoint behind on
  FOUR of the five routes.** `autoCheckpointLabels`
  (`internal/admin/server.go`) wraps `PATCH /api/workspaces/{id}`,
  `PUT .../operations/{opKey}`, `POST .../auth-preset` and `PUT
  .../endpoints/{eid}` — the wrapper commits its snapshot BEFORE the handler and spends
  the debounce window regardless of whether the CAS check inside passed; only `PUT
  .../scenarios/{sid}` is outside it. Accepted deliberately, not as an oversight:
  a refused write changes NOTHING in the four tokened tables (the
  A3 invariant itself), so a snapshot taken on a refusal is byte-identical to the one
  that a successful retry suppressed by the debounce AFTER it would have taken. Moving
  the wrapper inside the handler so that it fires only on success is a change in
  `routeMux()`, not in this slice.
- **`A3`: traffic, checkpoints and live state carry no token.** Not an oversight but
  a consequence of what those three layers already are: traffic rows are
  append-only (there is nothing to overwrite), checkpoints are immutable after the write
  (the same), and live state lives in RAM only (`internal/livestate`) and
  never claims to be the document read before an edit.
- **`A3`: the bundle format does not change.** No PER-ROW version was added to
  the snapshot — `mockerBundle: 3` (`internal/bundle/bundle.go:50#CurrentVersion`)
  stays as is. This follows from the `edit_version` allocator: restore
  does not restore a row's old version but always allocates a fresh one
  (`internal/store.AllocateEditVersion`), so the snapshot simply has nothing
  to carry — there would be nothing to restore from what was carried into it.
## P3a — resources

- **`P3a`: the wrapper's array/count property is found by ARITY, not by name —
  a declared exception from `DESIGN.md` §11:511, not an oversight.** The
  design names a fixed vocabulary (`items`/`data`/`results`) for the array
  property; the code instead walks the wrapper's own properties and picks
  the one array-typed one, because a name list silently misses a wrapper
  called `records` and silently picks the wrong one when a schema declares
  two. The arity rule replaces the name list everywhere a wrapper is read at
  confirm or served afterward.
- **`P3a`: resource serving is a BRANCH ahead of `assembleResponse`, not a
  third variant mode — a declared exception from `DESIGN.md` §6:168, not an
  oversight.** The design draws GENERATED/PINNED/RESOURCE as three sibling
  modes a variant can be in. The code keeps `resolved.Mode` at two values and
  adds a `PreBuilt []byte` field instead: `serveGenerated` asks the resource
  branch BEFORE calling `assembleResponse`, and `assembleResponse` itself
  just gained a fourth body source ahead of `pinned`/`default`, still the
  sole caller `TestAssembleResponseIsTheOnlySeam` pins it to (`{serveGenerated,
  Preview}`). A third mode would have meant widening that switch and
  everything downstream that already exhaustively matches two.
- **`P3a`: the three resource routes are a DECLARED divergence from
  `DESIGN.md` §14's own route table, not an oversight.** The design names
  `GET /api/specs/:id/suggestions`, a `GET/POST` pair on `.../resources` with
  `PUT`/`DELETE .../resources/:rid`, `POST .../resources/rederive`, and a full
  `GET/POST/PUT/DELETE .../resources/:rid/entities[/:key]` CRUD surface. This
  slice ships `GET /api/specs/{id}/resource-suggestions` (the noun alone
  reads ambiguous next to `.../specs/{id}/operations`), `GET
  /api/workspaces/{id}/resources`, and ONE `POST .../resource-decisions`
  instead of the `PUT`/`DELETE` pair — a `rid` does not exist until a
  suggestion is confirmed, so `DELETE .../resources/{rid}` cannot decline
  one — and, until `A4`, no entity routes at all: the stored rows were read
  through `GET X` on the mock plane itself. `A4` adds the read corner of
  the design's entity CRUD — `GET /api/workspaces/{id}/resources/{family}/
  entities`, see its own carve-out entry below; the write half (`POST`,
  `PUT`, `DELETE .../entities[/:key]`) is still absent, for the same reason a
  confirmed resource has no editor at all (a few entries below, "a confirmed
  resource has no editor").
- **`P3a`: chunking is deliberately deleted from the confirm transaction — a
  declared exception from `DESIGN.md` §11:527-528 ("large operations are cut
  into transactions of N rows"), not an oversight.** `seed_count` is clamped
  to `[1, 200]`, generation runs before the transaction opens, and the
  transaction itself is N inserts of already-built rows — cutting it into
  chunks would trade the atomic confirm (a resource whose entity set is
  partial is indistinguishable from a complete one) for a ceiling this
  slice's own numbers never reach.
- **`P3b`: `reset-data`'s transaction is unchunked for the same reason, and it
  is named separately because the P3a argument above does NOT cover it.** That
  one rests on ONE family of at most 200 rows; `reseed` prepares EVERY confirmed
  family and `clear` issues a workspace-scoped DELETE, so neither inherits the
  ceiling. What carries them is the other half of the same reasoning: generation
  happens entirely before the transaction opens, so the transaction is N inserts
  of already-built rows or one DELETE, and chunking would trade the atomic reset
  — a workspace whose entity set is half-cleared is indistinguishable from one
  an operator meant to half-clear — for a bound this slice's numbers never reach
  either. The price is a longer hold on the single writer connection, and
  `reset-data` is named beside restore in CLAUDE.md's `internal/checkpoints`
  paragraph for exactly that.
- **`P3a`: nested families derive nothing. Discharged by P3e for one level, and
  by P3g up to three** (see CLAUDE.md "Architecture" and both slices' own
  carve-out lists, next to P3b's, P3c's and P3d's) — `/orgs/{}/teams/{}/users`
  now derives; a fourth level (`/orgs/{}/teams/{}/users/{}/badges`) still does
  not, `maxNestingDepth` unclaimed by any slice letter above `P3g`'s own
  ceiling.
- **`P3a`: filters, sorting and pagination are read by nothing.**
  `filter_map` stays `{}`, and a declared `?limit=`/`?offset=` on a
  confirmed collection is visibly ignored: `GET X?limit=1` still answers the
  whole collection.
- **`P3a`: a confirmed resource has no editor.** No `PATCH`/`PUT` of a single
  entity, no editing of a resource's own configuration — with them go a
  fifth CAS table, a migration `0003`, `409 edit_conflict` on this surface
  and a `repopulate` verb. A resource that is wrong is declined and
  confirmed again (D4).
- **`P3a`: `POST X` only ever takes a BARE creation shape.** A wrapped or
  adapted body (`{user: {...}}`, JSON Patch) is out of scope — `write_form`
  is either `"bare"` or `NULL`, never a third shape.
- **`P3a`: the `ref` recipe is still absent. Discharged by P3c** — see its own
  carve-out list below, next to this one.
- **`P3a`: `data_snap` and screen 10's «вернуть и данные ресурсов» checkbox.
  Discharged by P3d** — see its own carve-out list below, next to P3a's, P3b's
  and P3c's.
- **`P3a`: `entities` is absent from the bundle.** `mockerBundle` stays at `3`; a
  scenario taken over a workspace with a confirmed resource leaves its entities
  untouched by both capture and restore, and a bundle import/export round trip
  (P4) does not carry them. `resources`/`resource_decisions` are no longer part of
  this hole — P3b puts both into `checkpoints.config_snap`, restored UPSERT-only.
- **`P3a`: `op_overrides.resource_id` and `custom_endpoints.resource_id` stay
  `NULL` and unread.** Both columns exist since P0; nothing in this slice
  writes or reads either.
- **`P3a`: a re-bind can strand a confirmed resource, and re-importing a
  spec with a NEW generation (`gen > 1`) triggers no re-derivation.** A
  resource confirmed against one spec becomes unreachable the moment the
  workspace is re-bound to a spec that does not declare its family — the
  rows persist and serve again if the original spec comes back, because the
  branch only runs after a route resolves (D8). Orphaned-suggestion triage
  on re-import is the same shape as an orphaned override and is closed by
  the same P4 work.
- **`P3a`: a confirmed collection serves only its array and its count —
  every other wrapper property disappears at confirm.** The generator today
  echoes `limit`/`offset`/`page` from the request and every other declared
  wrapper property; a confirmed resource serves `{arrayKey, countKey}` and
  nothing else. Accepted rather than fixed: with no pagination in this
  slice an echoed `limit` would be a lie, and re-emitting a stored skeleton
  would freeze request-derived values into a constant.
- **`P3a`: the endpoints screen carries no per-operation "resource-served"
  badge.** An operator learns a route is resource-served from the
  «Ресурсы» tab and from preview's `resource_serves` refusal, not from a
  marker on the operation row — the cut keeps `internal/mcp` and
  `MergedOperationView` untouched; the only change under `internal/mcp` is
  one comment.
- **`P3a`: a session force changes the STATUS on `POST X`/`DELETE X/{}`,
  never the body — and the branch's own direct exits still apply through a
  force.** A forced 200 on `GET X/{99}` still answers this branch's own
  `404 entity_not_found` rather than a generated 200, and a forced 204 on an
  over-ceiling `GET X` still answers `409 collection_too_large` — both
  follow from rules already written elsewhere, named here because neither
  is obvious from the force alone.
- **`P3a`: a family whose success is declared `2XX` or `default` serves its
  reads and silently drops its writes.** Derivation gates on a literal `200`,
  which both selectors resolve to at request time, while the write takeover
  requires a NUMERIC selector — so `GET X`/`GET X/{}` answer from storage
  while `POST X` answers a generated response, and the operator sees
  "created it, did NOT see it in the list" with a confirmed badge and an
  `entityCount` on screen and no message. Accepted for this slice, named
  here rather than left to be discovered as a bug report.
- **`P3a`: a parameterised `basePath` shares one entity set across its
  values. Discharged by `P3h`** (see CLAUDE.md "Architecture") —
  `settings.basePathValues` now declares which values `basePath`'s
  `{param}` may take, and `/orgs/7/quizzes` and `/orgs/8/quizzes` serve
  disjoint rows.
- **`P3a`: a confirmed resource on a degraded operation serves nothing.**
  `resolveVariant` answers not-ok for an operation that failed to parse and
  `serveGenerated` returns an empty 200 before the resource branch is ever
  reached — named here so it is not mistaken for a bug.
- **`P3a`: a family whose detail 200 is JSON and whose collection 200 is not
  is served HALF.** `GET X/{}` serves stored rows, `GET X` stays on the
  generator (the takeover requires a JSON-shaped 2xx) — accepted, named here
  rather than discovered.
- **`P3a`: `confidence` is a written constant (`1.0`), not a computed
  score.** The column is `NOT NULL` with no default; the suggestions route
  carries it on the wire and nothing branches on it.
- **`P3a`: no MCP tool. Discharged by P3b** — the tool inventory is now 42:
  `internal/mcp` gained `list_resource_suggestions`, `list_resources`,
  `decide_resource` and `reset_resource_data`, so confirming, declining, reading
  and resetting resources are no longer operator-only.
- **`P3a`: no idempotency.** A `POST` repeated after a lost response creates
  a SECOND entity — the first place in the tree where `A3`'s idempotency
  hole (see above) costs a visible duplicate row rather than a redundant
  overwrite; this slice does not close it.
## P3c — the `ref` recipe

- **`P3c`: `ref` addresses a resource family by its `route_family` string, not by
  `resources.id` — a DECLARED divergence from `DESIGN.md` §9:416, not an
  oversight.** §9 gives the kind its example (`resource:42.id`) and states in bold
  that `ref` "addresses a resource by `resource_id`, not by name" because names are
  not unique. `route_family` IS unique (`UNIQUE (workspace_id, route_family)`), so
  the ambiguity §9 names cannot arise through it, and two facts unavailable when §9
  was written decide the rest: a `resources.id` does not survive the repair this
  project prescribes (decline deletes the row, a re-confirm mints a new one, so an
  id-addressed reference would silently degrade to a decline the moment an operator
  fixes a wrong resource), and `EntityStore.List` is not itself workspace-scoped —
  an id handed straight to the store would be one forgotten roster lookup away from
  serving another workspace's rows on a plane that is unauthenticated by design. A
  family string has to be resolved through THIS request's own roster to become an
  id at all, so the workspace boundary is structural, not merely un-exploited.
- **`P3c`: `restrict` is not implemented** — a DECLARED divergence from
  `DESIGN.md` §9:419. `restrict` means the response must be REFUSED, and a refusal
  cannot leave `internal/gen`'s value-source chain (`recipeValue` swallows
  `Recipe.Value`'s error channel outright — "an open mock endpoint must never turn
  that into a 500"). Shipping it would mean a refusal path out through
  `assembleResponse`'s one seam and an answer to what status code an unresolvable
  reference deserves on a plane whose whole contract is that it always answers —
  larger than this whole slice. `Validate` refuses the token BY NAME, at write
  time, with a message saying it is not implemented, rather than silently
  downgrading it to `generate`.
- **`P3c`: scope filtering is not implemented — P3e made scopes real and did
  not implement it, and P3g widens the set of families this is false for
  without implementing it either, both on evidence rather than deferral.**
  `ref`'s
  `resolveRef` (`internal/mockplane/ref.go`) passes the EMPTY scope to
  `EntityStore.List` no matter which family it addresses, so a `ref` bound at
  a nested family finds no rows under that scope and falls to the recipe's own
  declared policy (`generate`/`set-null`) — the refusal IS the mechanism, not
  a check standing in for one. Threading the SERVING request's own scope
  through has no value to thread in the commonest case a `ref` fires at all —
  a generated body most often means `lookupResource` found no confirmed family
  for the route being served, so there is no `*resources.Resource` and no
  scope to take it from — and the family a `ref` addresses is in any case a
  DIFFERENT family from the one being served, so the serving request's own
  scope means nothing there.
- **`P3c`: auto-suggestion is not implemented.** `DESIGN.md` names one — a
  `<singular>_id` field auto-suggesting a `ref` when the resource `<plural>`
  exists — and it needs a screen to offer it on; this slice renders nothing.
- **`P3c`: two `ref` fields on one object resolve to two DIFFERENT entities, by
  design, not a bug.** The per-field seed layer (`Env.Seed`, keyed on the data
  path) is what makes a list of many rows reference many different entities
  instead of one — the same mechanism means `{userId, userName}` on one object
  picks one row's id and a DIFFERENT row's name. `DESIGN.md`'s own linked-select
  motivation for the recipe assumes the opposite; closing it would need a
  per-OBJECT binding the wire shape (a per-property recipe map) has no room for.
- **`P3c`: a shadow family is addressable, and closing it was tried and cut.**
  Route matching collapses `//` while the stored `route_family` string does not, so
  `/a/b` and `/a//b` can both be confirmed as two different resources and a `ref`
  can name the one that never actually serves a route. A normalisation rule was
  tried and reverted: the only reachable place to apply it also percent-decodes,
  which creates a NEW collision on exactly the characters a spec path is legally
  allowed to contain, and with both spellings confirmed the rule would have to pick
  between two equally matching roster keys by map iteration order — trading a
  deterministic hole for a nondeterministic one. No spec this project has ever
  imported produces the case; closing it for real belongs to whatever slice makes
  `route_family` and the compiled route table agree by construction.
- **`P3c`: no self- or sibling-reference inside a confirmed family's own schema.**
  A confirmed resource never calls `gen.Body` at all — not at confirm, not on a
  `reset-data` reseed, not when serving its own route (D8 above) — so a `ref`
  bound inside `/subjects`' own schema, referencing `/subjects` or any other
  family, is compiled but never evaluated. Closing it would mean compiling
  overrides at confirm time and giving `internal/resources` a resolver over
  families confirmed earlier — a confirm ORDERING constraint across families this
  slice does not take on.
- **`P3c`: the resolver decodes one entity row per evaluation** — a narrow,
  declared exception to `internal/resources`' own read-path rule ("raw bytes, never
  round-tripped through a second decode"), because a `ref` needs exactly one
  property of exactly one row and the collection/detail assembly still embeds the
  rest of a family's rows verbatim, unchanged.
- **`P3c`: no recipe editor, no contract change, no migration, and
  `op_overrides.resource_id`/`custom_endpoints.resource_id` stay `NULL` and
  unread.** Recipes are shown, never edited (P2's own carve-out, unmoved) — a
  `ref` is written through the same `op_overrides` path every other kind is, and
  through MCP's `set_operation_variant`, whose `RecipeInput` mirrors
  `recipes.Recipe` with a bare `Kind string` and no enum, so no MCP change was
  needed for a tenth kind and none was made. `api/openapi.json`'s `Recipe.kind` is
  likewise a bare string, so the contract stays at 52 operations. `0002_edit_version.sql`
  remains the newest migration. The two `resource_id` columns keep waiting for the
  slice that has exactly one resource per override to record — a `ref`'s addressing
  lives inside the recipe itself (the divergence above), and one column cannot
  carry the N references one override's N recipes may hold.
- **`P3c`: a `ref` survives a rollback and a decline-and-reconfirm, and does NOT
  survive a spec re-bind that drops the family.** The family path (`route_family`)
  is stable across both; what is not is whether the family still exists at all —
  that case is an ordinary decline under the recipe's own policy, not a new
  failure mode, and it is the identical hole P3a's own re-bind carve-out already
  names from the resource's side.
## P3d — `data_snap`

- **`P3d`: `POST .../reset-data` and a decline stay irreversible.** Neither takes a
  pre-destructive checkpoint and neither is in `autoCheckpointLabels` — a
  deliberate re-decision, not an inherited one: `autocheckpoint_test.go`'s comment
  that used to justify the omission on the ground that a rollback "cannot bring
  back the entities a subsequent decline's cascade destroyed" is exactly what
  `restoreData: true` now does, so that warrant is retired and the comment records
  it as retired rather than leaving it to be found stale a third time. Both verbs'
  own screen copy keeps its CONCLUSION (a decline or a `reset-data` is still not
  undoable by itself) and changes its ARGUMENT (the undo now available is a
  checkpoint taken before either ran, restored with `restoreData: true` — not "there
  is nothing that can bring the rows back at all").
- **`P3d`: `MOCKER_MAX_ENTITIES` is not wired up. Discharged by `P3h`** (see
  CLAUDE.md "Architecture") — three documents (this one included) used to
  advertise a cap no code read; the config default now matches the `1000`
  the code has always enforced, and the variable reaches both of
  `resources.Repo`'s two production constructions.
- **`P3d`: `entities` in a SCENARIO snapshot, and bundle export/import, are still
  out of scope.** `bundle.Bundle.Entities` still stays refused by `Validate` —
  this slice's new codec is `internal/bundle/data.go`'s own `DataBundle`, a
  SEPARATE type for the CHECKPOINT half, and does not touch the scenario-snapshot
  `Bundle` type at all; P4 still owns export/import over HTTP.
- **`P3d`: `op_overrides.resource_id`/`custom_endpoints.resource_id` stay `NULL`
  and unread**, as since P0 — unmoved by this slice, same as by P3a/P3b/P3c.
- **`P3d`: no migration.** `data_snap` has existed since `0001_init.sql:222`;
  `0002_edit_version.sql` remains the newest migration — this slice only rewrites
  that column's comment (see `internal/store/migrations/0001_init.sql`).
- **`P3d`: no new route, and no `edit_version`/CAS on rollback.** Rollback's
  concurrency control stays its own bounded fence mapped to 409
  (`internal/admin/checkpoint_handlers.go`'s `ErrConcurrentEdit` case) — the same
  fence P2c already had, unwidened.
- **`P3d`: the wire does not say WHY a checkpoint has no `data_snap`.**
  `checkpointSummaryView.HasData` is a bare boolean; a row with `hasData: false`
  could be the probe's own refusal (over `maxDataProbeBytes`) or `compressSnapshot`'s
  (the encoded document too large), and the wire distinguishes neither — a
  workspace confirming NOTHING still gets `hasData: true` (capture still runs and
  writes a zero-family document, not NULL), so `hasData: false` on this build
  always means one of the two degrade bands, never "nothing to capture", and
  CLAUDE.md's `internal/checkpoints` paragraph names both bands but
  neither reaches the client as a separate signal.
## P3e / P3g — nesting

- **`P3e`: more than one level of nesting. Discharged by `P3g` up to
  `maxNestingDepth = 3`** (see CLAUDE.md "Architecture") — `/orgs/{}/teams/{}/users`
  now derives; a fourth level still does not, on the identical per-request-cost
  and cap-multiplication reasons P3e gave for stopping at one, restated at
  three in `HISTORY.md`'s "What is NEXT".
- **`entities.parent_entity_id` and `resources.parent_id` stay NULL — a
  DECISION, not a deferral, made by `P3e` and RE-TAKEN identically by `P3g`
  at the full three-level chain.** See CLAUDE.md "Architecture" for the argument
  in full; the short form is that the cascade `DESIGN.md:505-508` asks for
  would close the one live sequence that still lets an orphaned descendant
  scope become reachable again (a `restoreData: true` rollback re-inserting an
  ancestor's deleted key verbatim) only by reopening the larger guarantee P3d
  shipped — a live `parent_entity_id` would let a config rollback's own
  `DELETE FROM entities WHERE resource_id = ?` cascade into a family's rows
  the call never named and never recreates. At depth the trade is stronger,
  not weaker: a live cascade's blast radius grows with the chain, while the
  anchor walk closes the orphan half observably at every level, not just one.
  A later slice that wants the physical cascade must first rewrite
  `restoreEntitiesTx` steps 3 and 4, add a sixth `bundle.ValidateData` rule,
  put a parent reference on `EntityRow`, order families topologically and
  decide a new `DataVersion` — the codec rewrite neither slice takes on.
- **`P3e`: a parameterised `settings.basePath` sharing one entity set across
  its values. Discharged by `P3h`** — see CLAUDE.md "Architecture" and `P3a`'s
  own carve-out entry, unmoved by `P3g` in between but closed by `P3h`.
- **Scope filtering in the `ref` recipe is not implemented for the ROUTE
  scope — `P3c` shipped that absence, `P3e` re-decided it on evidence, `P3g`
  re-decides it identically over a larger set of families, and `P3h` closes
  the BASE half alone, leaving the route half exactly as it was.** See
  `P3c`'s own carve-out entry above: a `ref` at a nested family resolves
  against the empty scope and
  therefore against nothing, which is the refusal itself, not a check standing
  in for one — what `P3g` changes is only how many `ref` targets that is now
  false for, not the mechanism.
- **`P3e`: `rederive` (`gen > 1`). Discharged by `P3f`** (see CLAUDE.md
  "Architecture" and `P3f`'s own carve-out list below) — `listSuggestions`
  now carries the `gen = (SELECT MAX(gen) ...)` predicate, and
  `POST /api/specs/{id}/rederive` is the one path that writes a generation
  above 1.
- **`resource_suggestions.parent_family` and `bundle.ResourceEntry.ParentFamily`
  stay unwritten and always null — a rule `P3e` wrote and `P3g` restates
  rather than revisits.** The parent link is COMPUTED (`router.ParentFamily`,
  a pure function of a family's own `route_family`), never stored, in any
  table or wire view, at any depth — a stored copy would be a second source of
  truth `internal/specs`, `internal/resources` and `internal/mockplane` would
  each have to keep in sync by hand, the identical one-owner argument `ref`'s
  own addressing already made for `resources.id` (see CLAUDE.md "Architecture").
- **`P3e`: `op_overrides.resource_id`/`custom_endpoints.resource_id` stay
  `NULL` and unread**, as since P0 — unmoved by this slice or by P3g, same as
  by P3a/P3b/P3c/P3d.
- **`P3e`: `filter_map`, `?limit=`/`?offset=`, an entity editor, idempotency.**
  Unmoved from P3a's own carve-out list, and by P3g too.
- **`P3e`: `MOCKER_MAX_ENTITIES` stays unwired. Discharged by `P3h`** on the
  same terms as `P3d`'s own entry above — this slice's confirm-time
  arithmetic (`parents × listSize`) was the first path in the tree that
  could reach the cap from ordinary settings, and `P3g`'s own `L^(d+1)`
  arithmetic reaches it too, but wiring the variable itself waited for
  `P3h`, which also had to decide the default's tenfold drop alongside it.
- **`P3e`: no migration.** `0002_edit_version.sql` remained the newest
  through `P3g`; `P3h` adds `0003_base_scope.sql` (see CLAUDE.md "Architecture").
- **`P3e`: `entities` in a SCENARIO snapshot, and bundle export/import, are
  still out of scope.** Unmoved from P3d — still P4.
- **A screen for the nesting relationship, beyond what `P3g` changed on it.**
  `DESIGN.md` §11 draws no tree component, no depth badge and no parent
  column; what exists is the collection-URL pointer's dimmed Russian line
  saying the `{}` segment takes a parent id, not a component — `P3e` gave it
  in the SINGULAR, and `P3g` made it plural where a family carries more than
  one `{}` and pinned the list order so an ancestor always precedes its
  descendants (D12.2), which is the whole of what a nesting screen gets in
  either slice.
- **A `restoreData: true` rollback can resurrect an orphaned descendant
  scope — the trade `entities.parent_entity_id` staying NULL makes, accepted
  by `P3e` at one level and RE-ACCEPTED by `P3g` at three, on the same
  terms.** Named above as the trade, not as an independent hole — repeated
  here because it is the one place neither slice fully closes what
  `DESIGN.md:505-508` asks for; the KIND of sequence is unchanged between the
  two slices, only its SURFACE grows with the chain (D9.1).
- **`P3e`: `reset-data`'s `clear` is not scope-aware and does not need to
  be.** It deletes every entity row of the workspace, at every level alike, so
  it cannot leave a descendant anchored to an ancestor that no longer exists —
  unchanged by `P3g`.

## P3f — rederive

- **Triage of what a new generation means for an already-confirmed family —
  CLOSED by `P4a`.** `GET /api/workspaces/{id}/drift` (`internal/admin/
  drift_handlers.go`) now names an orphaned confirmed family through the same
  `resources.Repo.OrphanedFamilies` predicate `reset-data`'s `stranded`
  classification already used, alongside the two other signals §5 named.
  What `P4a` deliberately did NOT build is its own, separate entry below.
- **A generation number, and a `?gen=` selector, do not reach the suggestion
  listing.** `GET /api/specs/{id}/resource-suggestions` keeps its four
  fields — `routeFamily`, `name`, `idField`, `confidence` — unchanged: a
  client that lists suggestions is told what the spec currently offers, not
  which generation produced it. The new verb's own response
  (`changed`/`generation`/`added`/`removed`) is the one place a generation
  number is visible on the wire, and only because it is the value an
  operator just changed. A caller that needs to read an OLDER generation —
  for audit, or to build the `P4` triage screen above — has no route to ask
  for one; every read this slice ships answers only the newest.
- **No automatic trigger rederives on its own.** No read path notices that
  the derivation rules have widened since a spec's suggestions were last
  produced and rederives silently; `POST .../rederive` is the only door. An
  automatic rederive would change what a spec offers without an operator
  asking for it, and the `409 stale_generation` refusal on a race between
  two writers already makes an UNEXPECTED generation change observable — an
  automatic trigger would make that the common case rather than the rare
  one.
- **`resource_suggestions.parent_family` still stays NULL and unwritten.**
  Unmoved from `P3e`/`P3g`'s own decision (see their carve-out entry above):
  the parent link stays `router.ParentFamily`, computed, never stored, and a
  rederive writes the same shape every other insert path already does.
- **A rederive's response does not diff the two generations' full ROWS, only
  the family names.** `added`/`removed` name which `route_family` values
  entered or left the newest generation; they say nothing about a family
  that survived the call but changed ONE column — a suggestion's `name`, its
  `idField`, its schema. The comparison that decides whether to write a new
  generation at all already reads the whole row tuple (so a rule change that
  only renames a family still mints one), but the wire response does not
  expose what changed inside a surviving family — that is a screen this
  slice does not build, and the per-column drift it would show is currently
  visible only by comparing two generations' raw rows directly in the
  database.
- **`make smoke`'s rederive block never exercises the `changed: true` path,
  and cannot, through HTTP alone.** Every assertion in the P3f smoke block —
  200, `changed:false`, `generation:1`, empty `added`/`removed`, an unchanged
  listing read back, 404 on an unknown id — is also exactly what a handler
  hardcoded to return that canned response after a real 404 check would
  produce. Found by the slice's own acceptance run, not by a later reader.
  It cannot be closed with the harness smoke.sh has: forcing a real
  generation change on the live image means making generation 1 differ from
  what derivation produces NOW, and over HTTP a spec's derivation output can
  never widen — `Import` dedupes by sha256 and mints a NEW `spec_id` for
  different bytes, which is the same fact that makes a re-import unable to
  produce `gen > 1` in the first place. The only remaining route is writing a
  deliberately narrower generation-1 row set straight into the container's
  SQLite volume, and `scripts/smoke.sh` has never touched the database
  directly (it is HTTP-only by construction, and the image carries no
  `sqlite3`). The path is covered where the coverage is cheap:
  `internal/admin/resource_handlers_test.go`'s
  `TestHandler_rederive_addedRemovedBothDirections` seeds a real
  narrower/wider generation-1 fixture and asserts `changed:true` with
  populated `added`/`removed` THROUGH the real HTTP handler. What stays
  uncovered is the live-image half — the class of failure `make smoke` exists
  for, and which this project's own `CLAUDE.md` says a green `go test` has
  twice hidden.
- **Entity data in a scenario snapshot, and bundle export/import, are still
  out of scope.** Unmoved from `P3d`'s own entry above — still `P4`.

## P4a — the drift report

Every item below is `decisions.md` (mocker-p4a-triage) §D9's own "out of
scope" list, stated so each reads as a decision rather than a gap.

- **No repair verb.** `GET /api/workspaces/{id}/drift` reports KEYS, never a
  remedy: the three deletions §5 asks for already exist —
  `DELETE /api/workspaces/{id}/operations/{opKey}`,
  `DELETE /api/workspaces/{id}/endpoints/{eid}`, and
  `POST /api/workspaces/{id}/resource-decisions` with `state: "declined"` —
  and a façade over those three would be a second path into the same
  handlers, the exact shape `CallAsMCP` exists to avoid.
- **§5's two PRESERVING remedies are narrowed to deletion.** §5 offers "turn
  into a custom endpoint" for an orphaned override and "turn it into an
  override" for a shadowing endpoint; `P4a` offers neither, so the only
  repair the report points at destroys configuration instead of moving it.
  An operator who wants the preserving path today reads the row through the
  existing GET, re-creates it as the other kind through the existing POST,
  and deletes the original — three calls that all ship, in an order no verb
  enforces. This is the largest thing `P4a` takes away from §5.
- **Both schema-diff halves of §5 are still out of scope.** An override
  referencing a field the response schema no longer has, and a resource
  whose `entity_schema` diverged. No schema comparison exists anywhere in
  this tree, and `resources.EntitySchema` is a JSON POINTER rather than a
  body, so comparing two detects the item schema MOVING inside the document,
  not a property added, removed or retyped where it stands — a second slice
  hiding inside §5, unmoved from `P3f`'s own entry above.
- **No automatic reattachment heuristic.** §22's first open question, still
  open: the report names the orphan and never guesses which new operation is
  the old one.
- **No screen.** The agent is primary and a screen is optional
  (`CLAUDE.md`'s coverage invariant) — `get_workspace_drift` is the only
  caller the invariant requires, and `web/src/api/coverage.test.ts`'s
  `EXEMPT` map's third entry names the policy. An operator working the admin
  UI with no MCP-connected agent observes NO change from this slice: all
  three failures stay exactly as silent for them as before it. The signals
  become readable for the agent holding `MOCKER_MCP_KEY` and for nobody
  else — the coverage invariant's own trade, accepted here rather than
  overlooked.
- **A fourth signal for a `resource_decisions` row with no `resources` row
  behind it.** A DECLINED family with no confirmed row serves nothing and
  stores nothing, so nothing about it has silently stopped working — an
  orphaned decision row is litter, not drift, and reporting it would put a
  fourth population in a report whose whole subject is §5's three.
- **A static-capture signal.** A custom `GET /gadgets/new` beating a spec
  `GET /gadgets/{id}` for the request `/gadgets/new` wins by rule 1 (more
  static segments), not rule 3, and their canonical paths differ. Reporting
  it would mean running `router.Build` over the merged table and reading off
  winners — a second, larger predicate whose true positives are mostly
  deliberate.
- **Refusing a shadowing endpoint at creation.** `handleCreateEndpoint` does
  not refuse a path canonically equal to a spec operation, and `P4a` does
  not make it: that is the documented override rule 3 of
  `router.compareRoutes` already makes, held by
  `router_test.go:TestMatch_CustomBeatsSpecAtEqualSpecificity` and
  `mockplane/custom_test.go:TestServeCustom_OverridesSpecOperationAtEqualCanonicalPath`.
- **Naming which workspaces a `rederive` affected.**
  `specs.Repo.AttachedWorkspaces` exists and returns slugs, and nothing puts
  it on the wire. An agent sweeping after a rederive lists workspaces
  through `GET /api/workspaces` and reads `drift` per workspace instead —
  adding an addressee to `rederive`'s own response would give that verb a
  second subject, which `P3f` deliberately kept spec-scoped.
- **`GET .../operations` (`handleListOperations`) still answers only the
  bound spec's operations, never the UNION of the spec and the stored
  rows.** An earlier plan had it walk that union to surface an orphaned
  override; `P4a`'s own decisions REVERSE that plan (§D6.2) — that handler's
  answer is "the operations of the currently bound spec, with your edits
  merged in", the Endpoints screen and its tests read exactly that shape,
  and widening it would put the drift predicate in a second place while
  changing an existing wire contract. The orphaned override surfaces in
  `drift` and nowhere else. (`CLAUDE.md`'s own "Where we are" carried the
  UNION-walk plan as a stale sentence through `P3f`'s HISTORY entry; `P4a`
  is the slice that reverses it, not just deletes the sentence.)

## A4 — entity read route, and the wider MCP surface over it

- **`A4`: `GET /api/workspaces/{id}/resources/{family}/entities` is a
  DECLARED divergence from `DESIGN.md:936`, not an oversight — it discharges
  half of `P3a`'s "no entity routes at all" carve-out above (and since `A11`
  a PUT and a DELETE by key beside it — still by `route_family` and
  `entity_key`, still not DESIGN.md:936's CRUD by `:rid`), and stays a
  carve-out of its own because only the read corner is built.** The design
  names `GET/POST/PUT/DELETE /api/workspaces/:id/resources/:rid/entities[/:key]`
  — full CRUD, addressed by `resources.id`, with a per-key accessor. This
  slice ships GET only, addressed by `route_family` rather than `resources.id`
  (the same reason `ref` and `restoreEntitiesTx` already address a family
  that way: an id does not survive decline-then-reconfirm, the repair path
  this project prescribes for a wrong resource), and no `/:key` accessor at
  all. The write half — `POST`, `PUT`, `DELETE .../entities[/:key]` — is
  refused by the identical rule that already makes a confirmed resource
  uneditable (`P3a`'s "a confirmed resource has no editor" entry above): a
  resource that is wrong is declined and confirmed again, not patched row by
  row. This route brings the admin API contract's population from 54 to 55
  (`api/openapi.json`), and it is `web/src/api/coverage.test.ts`'s SECOND
  non-probe `EXEMPT` entry (the first is `GET .../drift`, `P4a`): no screen
  calls it, its only caller is the `list_resource_entities` MCP tool
  (`internal/mcp/tools_entities.go`), by the same "agent is primary, a screen
  is optional" policy `drift` already established.
- **`A4`: the route's error taxonomy collapses three causes into one 404.**
  `unknown_family` answers a route family the bound spec never suggested, one
  that was declined, and a workspace with no spec bound alike — distinguishing
  them costs a second query for a caller whose repair
  (`POST .../resource-decisions`) is identical in every case, the same
  reasoning `resourceByFamily` already applies on the confirm/decline path.
- **`A4`: pagination is a cursor on `entities.id`, never on `entity_key`.**
  `entity_key` is `TEXT`, holding an unpadded decimal, and SQLite compares
  `TEXT` with BINARY collation — `'10' < '9'` — so a cursor built on it would
  skip and reorder rows past the ninth of any family. `entities.id` is an
  `INTEGER PRIMARY KEY` and needs no such caveat; `limit` defaults to 100,
  clamps silently (never a 400) to a hard ceiling of 500, matching
  `resourceEntitiesDefaultLimit`/`resourceEntitiesMaxLimit`
  (`internal/admin/resource_handlers.go`), copied from — not shared with —
  `trafficDefaultLimit`/`trafficMaxLimit`'s own numbers, so an import
  accident on one side cannot silently move the other's ceiling.
- **`A4`: no filter on `data`, only on the two scope axes.** `scopeKey` and
  `baseScopeKey` are both optional, both exact-match filters (the empty
  string is itself an addressable scope, not "unset" — distinguished by
  `url.Values.Has`, not `.Get`); nothing on the wire lets a caller filter,
  search or sort by a property inside a row's own `data`, the identical gap
  `P3a`'s "filters, sorting and pagination are read by nothing" entry already
  states for the mock-plane serving side.

## P6a — the traffic feed over SSE

- **`P6a`: no proof in the acceptance that the three variables are READ.**
  `make smoke` writes `MOCKER_STREAM_SESSION_RECHECK=7`, `MOCKER_STREAM_PING=3`
  and `MOCKER_STREAM_FRAME_TIMEOUT=4` into the stack's `.env` and observes
  behaviour consistent with those values (pings in a ten-second window, a
  logged-out stream closing within 15 s, a stalled peer cut within 15 s), but
  consistent is not caused: a build hard-coding three numbers close enough
  passes every clause. The check that would have closed it — a second stack
  recreation with different values under which those observations invert —
  was written, reviewed over three gate rounds, and CUT on the owner's
  decision of 2026-09-01: it cost more rounds than any other clause and added
  one to two minutes to every smoke run. What stands in its place costs
  nothing and catches only the naive case: 7, 3 and 4 are numbers no plausible
  hard-coded implementation carries. `internal/config`'s own test proves the
  values reach `Config`; `cmd/mocker/main.go` is where they become durations
  and it has no test. A run that wants the real proof recreates the stack.
- **`P6a`: no mock-plane streaming.** No `text/event-stream` custom endpoint,
  no WebSocket, none of `DESIGN.md` §30.11's six `MOCKER_STREAM_*` caps — this
  slice's three variables are not among them. `P6b` and `P6d`.
- **`P6a`: no frame recording.** §30.13's `MOCKER_STREAM_TRAFFIC_FRAMES` is
  mock-plane and is not introduced. The admin stream's own request is recorded
  by nothing: the traffic recorder wraps the mock plane's `serveRoute` only,
  and this slice does not change that.
- **`P6a`: no `retry:` field.** The stream sends no SSE `retry:` directive; the
  browser's own default reconnect delay stands. A server-chosen backoff is a
  knob nothing has asked for.
- **`P6a`: no per-workspace cap.** `maxStreamConns = 64` is one number for the
  process. A workspace that opens 64 connections starves the others, and the
  repair is the operator's, not the server's.
- **`P6a`: no stats screen.** `GET /api/stream/stats` is agent-only by policy
  (`get_stream_stats`; `coverage.test.ts`'s third non-probe `EXEMPT` entry).
- **`P6a`: the screen's 60-second retry is a client constant**, not
  configuration. A proxy that cuts streams at a shorter interval than that
  leaves the screen a minute in fallback after every cut; the poll runs the
  whole time, so nothing is missed, only the badge reads «опрос каждые 2 с» (a
  Russian UI string) longer than it might.
- **`P6a`: a contract delta §30.15 did not anticipate.** `DESIGN.md` §30.15
  prices `P6a` at `54 → 55` — one route, against a base that was 54 when §30
  was written. `A4` has since taken the base to 55, and `GET /api/stream/stats`
  is a SECOND route, so the contract goes from 55 to 57. Recorded here rather
  than repaired in §30: the v10 permission covers §23 only, and §30 is intent.
- **`P6a`: a reason in §30.7 that this slice does not honour.** `DESIGN.md`
  §30.7 justifies registry-close-before-recorder-cancel by "each closing
  stream writes its own event". `P6a`'s streams write none — the recorder wraps
  the mock plane only — so the slice keeps §30.7's ORDER and replaces its
  reason with the queue argument (CLAUDE.md, `internal/stream`). Recorded here
  because §30 is intent and the permission covers §23 alone.
- **`P6a`: no repair of the recorder's own drain bound.** The signal path's
  fifteen-second drain can end while a `delay`-ed mock request (up to thirty)
  is still live, and the listener-error path does not drain at all, so an
  event queued after `recorderCancel()` is lost. Both predate this slice, both
  are the recorder's rather than the stream's, and `P6a` leaves them exactly
  as it found them — what it adds is a registry whose own `Close()` joins its
  own goroutines on both paths.
- **`P6a`: no REPLAY of cleared rows.** After `DELETE .../traffic` the rows a
  cursor pointed past are gone and no client gets them back. What a cursor no
  longer does is go permanently deaf: migration `0004` makes ids
  non-reusable, so a client holding an old cursor receives every row recorded
  after the clear, and only the deleted ones are missing.
- **`P6a`: `byWorkspace` in `GET /api/stream/stats` has no cap of its own.** A
  process with 64 connections over 64 workspaces returns 64 entries; that is
  bounded by the connection cap and therefore small, but the bound is
  incidental rather than stated.
- **`P6a`: the workspace identity residual is one second wide.** The stream's
  recheck compares `(created_at, slug)`, and `created_at` is Unix seconds
  (`internal/workspaces/repo.go`), so a workspace deleted and recreated with
  the SAME slug inside the same second still matches — the identical residual
  `internal/checkpoints` has fenced on since `P2c`, needing the same slug and
  an already-authenticated operator. Closing it would mean a new identity
  column, a schema change this slice has no other reason to make.

## P6b — SSE mock endpoints

- **`P6b`: no authoring screen — a DECLARED divergence from `DESIGN.md`
  §30.14.** The design puts a type selector, the four behaviours phrased as
  tasks and a caps strip on the existing custom-endpoints screen; the A4 rule
  of 2026-09-01 (a new route ships with its MCP tool and no screen; «UI
  вообще не нужен делай только MCP», the owner's words quoted as data)
  applies, so `kind` and `stream` reach the contract and
  `create_endpoint`/`update_endpoint`/`preview_endpoint` only.
  `CustomEndpointsPage.tsx` is untouched: it never sends `kind`, so it never
  authors a stream, and it renders a stream row as an endpoint with an empty
  response map — which is what the row is. **Closed by `P6e` (2026-09-02)**:
  the type selector, `StreamEditor.tsx` and the caps strip are on that
  screen now, after the owner lifted the rule for the slice.
- **`P6b`: no inbound frames** — no reactive rules, no echo, no WebSocket,
  no `MOCKER_STREAM_MAX_FRAME`, `SEND_BUDGET` or `ORIGINS` (`P6d`); no
  live-connection list/close/push (`P6c`).
- **`P6b`: no `Last-Event-ID` resume and no `retry:`.** Frames carry
  `id: <ordinal>`; a reconnect starts the timeline from frame 1. A connection
  copies its definition out at the handshake and holds no server-side state
  (§30.5) — resuming mid-script would be state the design refuses.
- **`P6b`: `MOCKER_STREAM_TRAFFIC_FRAMES=all` is refused by name**, not
  built: §30.13 gives `all` its own retention budget so frames cannot evict
  ordinary rows, and that budget does not exist yet. **Closed by `A14`
  (2026-09-02)**: the budget is per ROW (`MOCKER_STREAM_TRAFFIC_MAX_FRAMES`,
  `_MAX_BYTES`, each way) and a connection stays one row, so the eviction
  §30.13 feared cannot happen by construction.
- **`P6b`: bundle v3 is NOT read.** `bundle.CurrentVersion` is 4 and
  `Validate` refuses 3 exactly as it refuses everything else — the owner's
  decision against the recommendation ("read 3 and 4, write 4"), on his own
  statement that no deployment of mocker exists. A database created before
  this slice holds checkpoints and scenarios no rollback or activate can
  decode; the repair is to delete them or the database, and
  `scripts/migration-check.sh` does not exercise a rollback across the
  boundary.
- **`P6b`: the tick schema is inline only.** No `$ref` (refused by name —
  there is no document to resolve it against), no derivation from a spec's
  `text/event-stream` response (§30.2 turned that down).
- **`P6b`: `POST .../preview` (the override preview) is unchanged.** A stream
  has its own route; `previewRequestWire` is keyed by `opKey` and a custom
  endpoint has none.
- **`P6b`: the contract delta differs from §30.15.** The table prices `P6b`
  at `55 → 56`; the slice went `57 → 58` because `A4` and `P6a` moved the base
  by three. Recorded here, not repaired in §30.
- **`P6b`: a second `gen.Body` site.** `TestAssembleResponseIsTheOnlySeam`
  (P2f's C8) now names `newTickGenerator` beside `assembleResponse`: the
  guard protects a RESPONSE's one assembly, which a tick frame does not
  have; a third site anywhere still goes red.
- **`P6b`: `maxBytesPerSec` is an estimate.** The larger of the timeline's
  bytes over its own duration (at least one second) and the tick's first
  body over its interval — not a measurement of a live connection, which
  `P6c` would be the place to make.
- **`P6b`: a frame over `MOCKER_MAX_RESPONSE` is skipped, not refused at
  write time**, for the tick alone: a generated body's size is not known
  until it is generated. Timeline payloads ARE refused at write time.
  `frames_skipped:N` in the row's notes is where it shows.
- **`P6b`: a recorded first frame is not redacted.** Under
  `MOCKER_STREAM_TRAFFIC_FRAMES=first` the traffic row's body is the first
  frame's wire bytes (`id:`/`event:`/`data:` lines), which the recorder's
  content-type dispatch does not recognise as JSON, so §15's field-name
  redaction does not run over the `data:` payload. Every frame this slice
  can record is operator-authored (a timeline) or generated from an inline
  schema with no recipes (a tick) — nothing a client sent. The day a frame
  can carry inbound data (`P6d`) this becomes §30.13's redaction problem in
  full, and that slice owns it.

## P6c — the live-connection surface

- **`P6c`: no broadcast.** A push addresses ONE connection by id
  (`POST .../connections/{cid}/frames`); an agent that wants every
  connection of an endpoint loops over `list_stream_connections`
  (`?endpointId=` narrows the rows). Decided in the interview (Q3): §30.15
  counts three routes, and a broadcast needs a partial-delivery answer
  (who got it, whose inbox was full) this slice did not want to design.
- **`P6c`: the admin feed's own connections (`P6a`) are neither listed nor
  addressable.** They stay in `GET /api/stream/stats` as counts. Their
  `stream.Conn` has no inbox (`Push` answers `ErrConnClosed` at once) and no
  `Info`.
- **`P6c`: no close frame, no `retry:` hint.** A close is a cancel; a browser
  `EventSource` reconnects on its own and appears as a NEW connection with a
  NEW id. Closing does not stop a client from coming back — edit or delete
  the endpoint for that.
- **`P6c`: a pushed frame is not persisted, not replayed, not recorded beyond
  the ordinary `first` body rule.** It lives in the connection's RAM inbox
  and dies with the connection. Under `MOCKER_STREAM_TRAFFIC_FRAMES=first` a
  pushed frame that happens to be the first frame is the recorded body, as
  any first frame is — and `P6b`'s "a recorded first frame is not redacted"
  now applies to an operator-pushed payload too.
- **`P6c`: the inbox is bounded in FRAMES (`inboxDepth = 16`, a constant),
  not in bytes.** `MOCKER_STREAM_SEND_BUDGET` (§30.11, bytes) stays `P6d`'s:
  it is the WebSocket outbound queue, and building it here for one
  operator-driven queue would have introduced a variable one slice early.
- **`P6c`: a queued frame is not cancelled on `push_timeout`.** `504
  push_timeout` means the loop did not write the frame within
  `2 × MOCKER_STREAM_FRAME_TIMEOUT`; the frame STAYS queued and may still be
  written. Un-queueing it would race the loop's own read of the channel;
  the message says so and the tool's description says "do not resend
  blindly".
- **`P6c`: connection ids do not survive a restart.** The counter is per
  registry per process and restarts at 1; an id held from before a restart
  can name a different connection. The list it came from was emptied by the
  same restart.
- **`P6c`: `inbox_full` and `push_timeout` are not observed in `make
  smoke`.** Both need a peer that never drains; they are observed in
  `internal/stream`'s own tests over a connection that was `Open`ed and never
  served (`push_test.go`).
- **`P6c`: the contract delta differs from §30.15.** The table prices `P6c`
  at `56 → 59`; the slice went `58 → 61` because `A4`, `P6a` and `P6b` moved
  the base. Recorded here, like `P6b`'s, not repaired in §30.
- **`P6c`: no screen.** The A4 rule; `P6e` remains the slice that would draw
  a connections panel, and it waits on the owner lifting that rule for it.
  **Closed by `P6e`**: the «Соединения» tab (`StreamConnectionsPage.tsx`).
- **`P6c`: `DESIGN.md` §30.16's first open question is answered in code and
  in `HISTORY.md`, not in §30.16 itself** — an agent does not edit
  `DESIGN.md`; the owner appends it to §23 the way `P6a`'s and `P6b`'s
  as-built sections were appended.

## P6d — WebSocket mock endpoints

- **`P6d`: no templating in a rule's `data`.** A reply is a literal JSON
  value; nothing from the matched frame is echoed into it. Echo is the
  whole of "send back what came in".
- **`P6d`: matching reads a TEXT frame carrying a JSON OBJECT and nothing
  else.** A binary frame, a non-JSON text frame, a JSON array or scalar
  never matches a rule (echo still returns them); `when[]` has no `path`
  field (the `overrides.Condition` type has none), and no condition on a
  frame's size or opcode.
- **`P6d`: no subprotocol negotiation, no permessage-deflate.** The library
  is called with compression disabled and no subprotocol list; a client
  that offers one gets none back.
- **`P6d`: `MOCKER_STREAM_TRAFFIC_FRAMES=all` stays refused; the seventh
  frame's token (§30.13) stays unrecorded.** `first` records one frame each
  way — the first inbound only when it is a text frame holding a JSON
  object, redacted by field name — and nothing else. A binary or non-JSON
  first inbound frame is not stored; `first_in:binary|text` in the notes
  says which. **Amended by `A14`**: under `all` every inbound frame that is
  a text frame holding a JSON object is stored, each redacted by field
  name, one per line; a binary or non-JSON frame is still never stored
  (§30.13's redaction has no rule for it), only counted in `frames_in`.
  The seventh frame's token is therefore recorded REDACTED — the same
  guarantee an ordinary request body has — and a token in a non-JSON
  frame is not recorded at all.
- **`P6d`: the session layer applies at the handshake only.** `fail_next`
  is spent by the handshake, a forced status aborts it, a delay delays it;
  no directive reaches a frame (§30.4).
- **`P6d`: no WebSocket on the admin plane.** The CSRF predicate treats a
  `GET` with `Connection: upgrade` / `Upgrade: websocket` as state-changing
  and the chain refuses it — with **415**, not 403: the first check a
  handshake fails is the JSON content type it does not carry. The document
  said 403; the build recorded what the chain actually answers and the
  smoke accepts either. No admin route upgrades; the traffic feed stays
  SSE (§30.10).
- **`P6d`: the `{"$gap": N}` marker is a data frame.** A client that treats
  every frame as application data sees it as one; the key is chosen so a
  JSON-object protocol can filter it by name. It consumes an ordinal like
  any data frame.
- **`P6d`: `SEND_BUDGET` bounds reactive/echo replies only.** The admin push
  inbox keeps P6c's 16 frames and its 409; a rule's terminal close travels
  outside the budget (a single slot, never dropped).
- **`P6d`: a single reply larger than the whole `SEND_BUDGET` is never
  sent.** The budget is the queue's byte sum, so a reply that alone exceeds
  it is dropped every time — correctly, and invisibly until the row is
  written (a gap marker announces drops only when a write FOLLOWS them;
  drops after the last write reach `replies_dropped` at close and nothing
  else). The first draft of A3(f) sent 3000-byte echoes under a 1kb budget
  and observed exactly that: no frame, no marker, a connection idling to
  its lifetime. An operator who sets the budget below the replies an
  endpoint sends has muted it.
- **`P6d`: after a rule's `close` the reader keeps DRAINING, it stops
  MATCHING.** The document said "stops reading"; the peer's half of the
  closing handshake arrives on that same read and nobody else reads it, so
  a reader that stopped would leave every rule-close waiting out the
  library's handshake timeout. Frames read after the terminal are counted
  in `frames_in` and answered by nothing.
- **`P6d`: a dead peer holds the closing handshake for ONE frame timeout,
  not the library's five seconds.** `Close(code, reason)` waits for the
  peer's close frame under the library's own internal deadline, which a
  registry shutdown cannot cancel; the loop therefore runs the handshake on
  a helper goroutine it JOINS, and closes the socket outright once
  `MOCKER_STREAM_FRAME_TIMEOUT` has passed (the external diff read's
  finding). What a peer that never answers costs a shutdown is one frame
  timeout per such connection, in parallel — and that connection's row
  records the code the server SENT, since the peer never acknowledged it.
- **`P6d`: rule close codes are `1000` and `4000..4999` only.** `1xxx` is
  the library's and the protocol's, `3xxx` the IANA registry's; both are
  refused by name.
- **`P6d`: the 1009 close is recognised by the library's error TEXT.** The
  read limit is the one close coder/websocket performs without returning a
  `CloseError` to the reader; `wsmock.CloseStatus` matches "read limited
  at" so the row records the code the peer saw. One string, one place,
  inside the seam that exists for it.
- **`P6d`: no `ws` client anywhere but the smoke's python.** `internal/probe`
  stays the tree's only outgoing HTTP client (§30.14: "Try it" is a
  browser-side client, P6e's); `wsmock.Dial` exists for tests only.
- **`P6d`: no screen.** P6e remains the slice that draws the test client
  and the connections panel, waiting on the A4 rule. **Closed by `P6e`**:
  `StreamTestClient.tsx` is the browser-side client §30.14 describes.
- **`P6d`: the dependency is pinned by version in `go.mod`, not vendored.**
  `go.sum` gains lines for exactly one module path; the boundary test keeps
  the importer count at one.
- **`P6d`: `DESIGN.md` is not edited.** The library's arrival, the
  predicate's widening and the CSP sources are §30.9/§30.10/§30.14 as
  designed; the as-built section is the owner's to append to §23.

## P6e — the streaming screens

- **`P6e`: the caps strip shows the server's limits as CONSTANTS**
  (`STREAM_CAPS` in `StreamEditor.tsx`, a copy of
  `internal/customep/stream.go`'s), not effective values: no route reports
  `MOCKER_MAX_RESPONSE`, `MOCKER_STREAM_MAX_CONNS` or
  `MOCKER_STREAM_MAX_LIFETIME`, and a route for five integers is a route.
  The strip names the variables instead. The per-workspace cap is live on
  the «Соединения» tab (`cap` from `GET .../connections`) and
  `maxBytesPerSec` is live from the preview. **Closed by `A9`** (the same
  evening): `config.Limits` rides inside `ServerConfigView`, which the
  session already carries, so the strip shows the effective numbers with no
  new route; the validator's constants (frames, delays, rules) stay
  constants, because those are not configuration.
- **`P6e`: no endpoint filter on the connections tab.** `GET
  .../connections?endpointId=` exists (P6c) and `list_stream_connections`
  exposes it; the tab lists every connection of the workspace, which at
  `MOCKER_STREAM_MAX_CONNS` = 200 is one screen.
- **`P6e`: the browser test client decodes no binary frame** («[бинарный
  кадр]» in the log), listens only for the SSE event names the definition
  declares plus `message` (EventSource cannot enumerate unknown names), and
  keeps the last 200 log lines.
- **`P6e`: `DESIGN.md` is not edited.** §30.14 is built as written; the two
  contract schema drifts it exposed (`StreamPreviewRequest.kind`,
  `EndpointConflictDetails`) are repaired in `api/openapi.json`, and the
  as-built section is the owner's to append to §23.

## A5 — one command up, the HTTPS overlay, the healthcheck

- **`A5`: the HTTPS overlay's certificate source is Caddy's local CA and
  nothing else.** No ACME, no `tls` with a mounted certificate pair, no
  DNS challenge: a dev stack on loopback has nothing a public CA could
  validate, and a corporate contour has a wildcard of its own to mount —
  `deploy/Caddyfile`'s top comment says which one line to swap. The root
  has to be trusted by hand (`make tls-root`); no target installs it into
  an OS or browser store, because that is the one step this repository
  should never do to a machine on its own.
- **`A5`: nothing listens on port 80.** `auto_https disable_redirects` and
  no published `:80` — an http→https redirect would need the plain port
  open, and the point of the overlay is that Caddy on 443 is the only
  door. A browser typing `http://mocker.local:8443` gets a TLS error,
  not a redirect.
- **`A5`: the subnet is a fixed /24 knob, not discovered, and the trust
  is ONE address on it.** `MOCKER_TLS_SUBNET` defaults to `172.30.10.0/24`;
  `compose-tls.sh` derives the gateway (`.1`) and Caddy's static address
  (`.254`) from it and refuses any other shape, and `MOCKER_TRUST_PROXY` is
  set to the `.254` alone — the review found the subnet form trusted the
  host's own gateway address. Reading a subnet back out of a created
  network and re-running would be two `up`s. Compose says `Pool overlaps`
  on a collision; the fix is the knob.
- **`A5`: no HSTS on the admin site, on purpose.** HSTS is keyed by host
  name, not port: a browser that once saw
  `Strict-Transport-Security` on `mocker.local:8443` would refuse the
  plain-http quick start on `mocker.local:8080` for the header's whole
  `max-age`, and `make up` and `make up-tls` share the names in `.env`.
  The security review asked for it as a second control beside the `Secure`
  cookie against `originAllowed`'s hostname-only comparison; a contour
  with real names and no plain stack can add the one line to
  `deploy/Caddyfile`.
- **`A5`: the wildcard is not resolvable from the host without help.**
  `/etc/hosts` has no wildcards; `make tls-root` prints the two names to
  add, and each workspace host is added by hand. A dnsmasq sidecar or a
  `*.localhost` scheme was not built — the smoke uses `--resolve` and needs
  neither.
- **`A5`: Caddy has no healthcheck of its own.** `caddy:2.11-alpine` has
  no curl either; mocker's `service_healthy` gates Caddy's START, and
  `smoke-tls.sh` observes Caddy by talking to it. A `caddy` subcommand
  probe (`caddy version` proves nothing about :443) was not added.
- **`A5`: HTTP/3 is not published, and the SSE check covers h1 and h2
  only.** Caddy enables an h3 listener on `:443/udp` by itself; the
  overlay publishes the TCP port alone, so a browser negotiates h2. Check
  7 runs once per TCP protocol; nothing measures QUIC. A `443/udp`
  mapping is one line when someone needs it.
- **`A5`: `smoke-tls.sh` observes one handshake per name, not renewal,
  and one immediate WebSocket exchange, not idle survival.** The local
  CA's leaves are Caddy's to renew and the smoke's stack lives minutes;
  Caddy's `reverse_proxy` stream timeout is unlimited by default, and the
  lifetime/idle behaviour of a `ws` connection is `make smoke`'s P6d block
  (direct, no proxy). Neither is measured through Caddy.
- **`A5`: Caddy does no active upstream health checking.** The
  `service_healthy` gate runs ONCE, at Caddy's start; a mocker that
  restarts later (`restart: unless-stopped`) is not re-gated, and Caddy
  answers 502 until it is back — which is the honest answer. Caddy's own
  `health_uri /readyz` would add a second prober beside compose's.
- **`A5`: `smoke-tls.sh` runs host routing only.** `MOCKER_ROUTING=path`
  under the overlay is the same Caddyfile with one site block doing all
  the work; nothing suggests it differs, and nothing measured it. The admin
  plane's own SSE feed (`P6a`) through Caddy is likewise unmeasured — check
  7 is the MOCK plane's tick stream through the identical `reverse_proxy`.
- **`A5`: `init-env.sh` prints a generated password to stdout, once.**
  Writing it to a file would leave a plaintext copy beside the hash; not
  printing it would make the generated password useless. The line is
  labelled "shown once" and the hash is the only thing that persists.
- **`A5`: `mocker healthcheck` reads the full configuration.** A probe that
  read only `MOCKER_ADDR` and `MOCKER_ADMIN_HOST` would be cheaper and would
  answer for a container whose environment the server itself refused; the
  chosen shape fails the probe exactly where the server would fail to
  start. The cost is `config.Load`'s parse every 10 s, measured as nothing.
- **`A5`: no CI target runs the smokes.** Gitea's act_runner exists; the
  two scripts need a docker socket and a spare subnet on the runner, and
  wiring that is a runner question, not this slice's.

## A6 — assets

- **`A6`: `mediaType` on a `bodyRef` variant is REFUSED, where DESIGN §32.3
  says "must agree or the write is refused".** A declared narrowing:
  agreement is unknowable at write time — the asset may be uploaded later,
  or replaced under the same name with another type — so the asset's
  stored type is the only type such a variant has, and the
  `asset_type_mismatch` note the first draft invented went with the field.
- **`A6`: no multipart, no chunked or resumable upload, no `POST` form.**
  The body is the file; `curl -T` is the client; §8's "multipart we do not
  touch" stands.
- **`A6`: no asset in `config_snap`, `data_snap`, a scenario snapshot or
  the bundle.** Bundle stays v4. A rollback restores a `bodyRef` whose
  asset may be gone and the answer is `asset_missing`; whether assets one
  day enter `data_snap` is §32.7's first open question, decided by the
  measured size of real workspaces' assets.
- **`A6`: no sniffing of the bytes.** The declared type is the type; a
  sniffer is a second parser that has to agree with the browser's own, and
  `BrowserExecutableMediaType` exists because a second parser did not.
- **`A6`: no range requests, no `Content-Disposition`, no per-asset
  headers, no `Cache-Control` beyond the ETag.** §32.7.
- **`A6`: no `get_asset` MCP tool and no URL-fetch upload.** An agent that
  needs the bytes GETs the mock route; a fetch-by-URL is a second outgoing
  HTTP client, and `internal/probe` is not growing a general fetcher.
  `upload_asset` carries about 7 MB at the default `MOCKER_MAX_BODY` (the
  base64 travels under it), not the 8 MB file cap; the description says so.
- **`A6`: deleting a referenced asset neither refuses nor cascades.** A
  `bodyRef` or `asset_url` naming it answers `asset_missing` / a 404 URL
  until a new upload under the same name; the repair IS the re-upload.
- **`A6`: no per-asset access control.** As public as the mock (§32.6).
- **`A6`: the runtime caches no bytes.** A hot asset is one reader-pool
  query per request; a `bodyRef` costs one `Get`, a 304 costs one `Meta`.
  If that is ever measured to matter, an LRU keyed by sha256 is the
  answer, not the runtime cache.
- **`A6`: the mock route is not recorded in traffic and the session layer
  does not apply to it** — by dispatch order (step 4), stated in §32.3 and
  pinned by construction rather than by a test that forces a 503 and reads
  the picture anyway.
- **`A6`: no screen.** `A4`'s rule; three `EXEMPT` entries. **Closed by
  `A10` (2026-09-02)**: the «Файлы» tab (`AssetsPage.tsx`), the three
  entries withdrawn.
- **`A6`: four acceptance clauses are pinned by the smoke, by construction
  or by a neighbouring test rather than by their own unit test** — the
  handler-side media-type refusal on the MCP loopback path (the chain's
  refusal and the handler's are the same `dangerousMediaType`, and the
  handler test reaches it through the chain), a CORS preflight against
  `{prefix}/assets/{name}` (step 3 precedes step 4 by construction), the
  overrides `scan` path refusing a malformed stored `bodyRef` (the decode
  re-enters the same `ValidateVariant` the write test pins), and a pinned
  `bodyRef` on a confirmed family's route (pinned already exits takeover;
  no fixture builds both). Both post-build readers named them; recorded
  rather than argued away.

## P4b — export, import and fork

- **No screen.** `GET .../export`, `POST /api/workspaces/import` and
  `POST .../fork` ship with `export_workspace`, `import_workspace` and
  `fork_workspace` and three `EXEMPT` entries, under the A4 rule. A
  «Скачать»/«Импорт» pair on the workspaces list and a «Копировать» on the
  overview are the obvious screens; they wait on the owner's word, like the
  drift screen (`IDEAS.md`).
- **Assets are not in the export**, by DESIGN §32.4's own decision ("the
  bundle does not carry assets in v11"). An imported workspace has its
  `bodyRef`s and `asset_url`s intact and its assets absent until
  re-uploaded; `asset_missing` in the traffic says so on the first request.
  A fork copies them (same installation, one `INSERT … SELECT`), which is
  why the two verbs differ here on purpose.
- **Scenarios are not in the export.** A scenario is a second snapshot of
  the same layer; an export is ONE state. An installation that needs the
  scenarios too exports once per scenario (activate, export, deactivate),
  or forks. A fork copies every scenario row and re-points the active one.
- **An import always creates; nothing imports over an existing workspace.**
  Replacing a live configuration in place is a rollback's job (the
  pre-destructive checkpoint, the fence, the retry are all there), and an
  import that could target an existing row would be a second restore path
  with none of them. To "update" a workspace from a file: import as a new
  one, or open the file's rows through the existing verbs.
- **`entities` stays `null` in the export; rows travel under `data`.**
  DESIGN §17 draws `entities` as the data half; P3d (D3) moved entity rows
  to a separate document (`DataBundle`) with its own version, and
  `bundle.Validate` refuses a non-null `entities` so no capture site can
  put rows through a field that has no restore rule. The export keeps that
  refusal and adds `data` beside the bundle rather than reopening it.
- **The export's data half has the checkpoint's probe budget** (6 MiB
  estimated, 8 MiB encoded) and refuses rather than degrades. A workspace
  over it exports without `includeData` and ships its rows some other way
  — or is forked, where the rows never leave SQLite and no budget applies.
- **`spec.inline` is the bytes as uploaded, as one JSON string** — a YAML
  spec inlines as a YAML string inside JSON. Prettier would be the
  normalized document as an object; it would hash differently and import
  as a second spec. The hash is the identity, so the bytes are the
  payload.
- **The export carries `settings` whole, `auth.signingKey` included** —
  exactly what a checkpoint's `config_snap` and a scenario's snapshot
  already hold. It is a mock's key for a mock login, and carrying it is
  what keeps a frontend's stored token valid against the imported copy;
  a document committed to git therefore holds it, as the checkpoint
  table already does. Redacting it would make an import mint a new key
  and silently log every stored session out — a different product
  decision, not taken here.
- **A fork's `forkedFrom` is a raw id with no `ON DELETE` clause** (P0's
  column, written for the first time here). Delete the source and the copy
  points at nothing; the view renders the number. A slug would survive the
  delete but not a rename; neither is a stable identity, and the column is
  a provenance note, not a reference the code follows. **Corrected
  2026-09-03 (audit):** "points at nothing" was not what the schema did —
  `REFERENCES workspaces(id)` with no clause is `NO ACTION`, and with
  `foreign_keys=ON` on every connection that REFUSED the delete of a fork
  source ("FOREIGN KEY constraint failed", a 500). `workspaces.Repo.Delete`
  now detaches every copy (`forked_from = NULL`) in the same transaction
  before the delete, which is what the sentence above always meant; the
  column stays a provenance note and no cascade was added.
- **A fork does not copy checkpoints or traffic.** History is the source's
  own; the copy starts with one baseline row. Traffic is what the SOURCE
  served, under the source's URL.
- **`POST .../migrate-workspaces`** (DESIGN §14's table, §5) stays unbuilt:
  `P4a`'s decisions turned it down on their own evidence (its own
  `CARVE-OUTS.md` entry), and this slice adds nothing to that question.

## P7a — API design on top of a workspace

Decided in `mocker-p7-api-design` (decisions.md §4) and
confirmed by the build; each is a hole an operator or an agent can hit.

- **No request validation against `reqSchema`.** It is validated as a
  schema on write and exported as `requestBody`; an incoming request is
  never checked against it (P2, unchanged). A design tool makes this more
  wanted, not less, and it stays P2's.
- **No schema editor.** `P7b`'s «Контракт» tab is read-only (the same
  gate document, D12); the ONLY author of `schema`, `reqSchema` and
  `operation` is the agent (or a direct call): the existing
  custom-endpoint form has no field for them and passes a row's own
  values back untouched on every edit, so an edit from the screen does
  not clear what the agent wrote. Widening that form is UI debt beside
  Monaco/schema-tree priced above.
- **The export's schemaPatch apply is a SECOND site of the same
  primitive.** D7 asked for "the SAME buildPatchedSchemas … never a second
  apply path"; that function lives in `internal/mockplane`, which imports
  `internal/design` for `Skeleton`, so sharing it is an import cycle. The
  export restates the runtime's rule instead — `jsonpatch.Apply` over the
  resolved response schema, the schema root's own `examples` deleted after
  the patch, the media-type object picked by `specs.SelectMediaType`'s rule
  (restated for the same reason: `internal/specs` is a store package),
  `2XX`/`default` answering for a 2xx status only — and
  `compose_test.go` pins each half. A divergence between the two is a
  bug in this list's sense, not a decision.
- **A generated custom variant now has a 406 gate.** `serveCustom` only
  ever negotiated a PINNED variant's media type; the P7a branch runs the
  same `acceptable` check a spec operation gets, so an `Accept` that
  excludes `application/json` answers 406 where the pre-P7a empty body
  did not. Deliberate: the branch enters the seam a spec operation uses.
- **`POST .../preview` does not know a custom row's schema.** Preview
  answers a custom route as it always did (an empty body at the status);
  the schema-bearing branch is the serve path's only. A preview that
  mirrors it is `P7b`'s if the screen wants one.
- **A rebind's `$ref` check is not inside the rebind's transaction.**
  `PATCH specId` reads the endpoint rows on the reader pool and refuses
  before `UpdateExpecting` opens; a `POST .../endpoints` landing between
  the two stores a row dangling against the new spec (the same window an
  import has). The serve path tolerates it (a log line, `{}` at that
  node), which is why the window is recorded rather than closed.
- **A rollback decodes the checkpoint twice.** The D6 check reads and
  gunzips `config_snap` through `checkpoints.Repo.Get` before `Rollback`
  reads it again for the write — one extra decode of at most 8 MiB per
  rollback, on the reader pool, accepted over threading a decoded bundle
  through `Rollback`'s signature.
- **The export's `examples` is an ARRAY**, the normalizer's own shape for
  a singular `example`, where OpenAPI's Media Type Object wants a MAP of
  Example Objects. It is what a re-import round-trips byte for byte
  (D14, forced by `normalizeDialect`), and a strict validator or code
  generator may reject it; a viewer renders it. Recorded, not fixed: the
  fix belongs in the normalizer and would move every stored spec.
- **A dangling `$ref` is not a drift signal.** Writers refuse it (the two
  endpoint writers, `PATCH specId`, import, rollback), so there is nothing
  for `GET .../drift` to report; a row made dangling BEHIND the writers (a
  hand-run `UPDATE`, a spec row deleted from the file) is one log line at
  runtime build and `{}` at that node, never a 500.
- **`operationId` uniqueness is enforced on the two endpoint writers
  only.** A checkpoint restore, an import and a rebind never re-check it:
  a rebind onto a spec whose operation reuses an id a custom row already
  holds produces a document with two operations under one id, which a
  code generator refuses and the mock does not care about. A rebind-time
  check would be the same shape as the `$ref` one and is a later slice
  if it is ever hit.
- **No `accept_design` composite tool.** The accept step is `import_spec`
  → `update_workspace_settings {specId}` → `get_workspace_drift` → delete
  the named delta rows, and the last step is not optional: a `schemaPatch`
  applied a second time over a base that already carries it fails and
  that variant serves unpatched (D8; the guide's step list says so).
- **No `x-mocker-*` extension in the export, no per-operation origin
  marker.** The base/added/changed/removed badges `P7b` draws are computed
  client-side from `.../operations` and `.../endpoints`; the document a
  backend team receives is plain OpenAPI. `x-websocket: true` on a `ws`
  row is the one extension, because there is no standard way to write a
  WebSocket upgrade as an operation.
- **No `components` written from custom rows.** A custom endpoint's
  schema is inline; two rows with the same shape carry two copies.
  Deduplicating them into a shared component is a later idea.
- **`sse`/`ws` operations carry no frame schema.** They export as a `GET`
  with `text/event-stream` and a `GET` with a `101`; a re-import of them
  yields ordinary base operations the custom row still outranks (rule 3).
- **A `$ref` inside an override's `schemaPatch` is not checked** — unchanged
  since P2e; the patch applies to a resolved schema and a bad pointer in
  its value lands in the export as written.
- **Bundle v5 reads v4 but not v3.** The precedent (`P6b`) refused the
  old version on the owner's word that no deployment existed; `A16`'s
  installer made a colleague's v4 database plausible, so v4 decodes with a
  nil operation and no schema. v3 stays refused: nothing older than P6b's
  own refusal can exist on a machine that ran P6b.
- **The export is read on the reader pool and fenced by nothing.** Two
  reads (overrides, endpoints) can straddle a concurrent write, and the
  document then mixes two revisions; `info.version`'s `-draft.<revision>`
  is read once with the workspace and names the earlier one. A
  transaction-scoped read would hold no writer lock (the reader pool is
  WAL) and is cheap to add if a diff between two exports of a quiet
  workspace ever shows it — none has.
- Multi-designer editing, comments and review on a design: `P5`'s
  question and git's, respectively (DESIGN §34.6).

## P7b — the «Контракт» tab

- **A recipes-only override is «база».** The badge asks whether the SHAPE
  moved (a patched schema, a pinned response, a removal, a new or
  replacing row); recipes change values inside an unchanged schema and
  the document does not show them. An operator who wants to see them has
  «Endpoint'ы».
- **Badges are keyed by the document's literal spelling.** A custom row
  at `/users/{userId}` over the base's `/users/{id}` is «изменено» under
  ITS spelling because the export writes it there and drops the base key
  (rule 3); nothing in the tab says which base key it replaced.
- **No diff between two exports** and no "what changed since the last
  accept" view — the badges are against the CURRENT base, which is what
  the drift report is against too. Git over the downloaded file is the
  diff.
- **No preview of a generated body on the tab.** The schema is what the
  tab shows; the body is one request away on the mock host, or
  `POST .../preview` for a spec operation (a custom row's schema is not
  in the preview — the P7a entry above).
- **`?opKey=` selects only an operation the list holds.** A link to an
  operation the bound spec no longer has (an orphaned override) opens
  «Endpoint'ы» with nothing selected; the drift report is where that row
  is named.
- **The download's file name is the tab's own** (`workspace-<id>-<version>.json`),
  not the route's `?download=1` name (`<slug>-draft-<rev>.json`): the tab
  has the document and the workspace id, not the slug, and fetching the
  route a second time only to inherit its header would be the second
  fetch the test forbids.

## A18 — endpoint functions (Lua)

The gate document (`docs/A18-endpoint-functions.md`) tags every item of this
class with the literal `[GIVES-UP]` and the list is countable by command:
`grep -cE '^[0-9]+\. .\[GIVES-UP\]' docs/A18-endpoint-functions.md` returns
fourteen, and so does the count of entries below. The class GREW at every
look — six, then eight, then nine, then twelve across four gate rounds, then
fourteen while the code was being written — which is why the two documents
match BY NAME rather than by a number either of them asserts.

The head of that document records the whole feature's own premise, in the
owner's words and not paraphrased: «хочу такую фичу для локального
инструмента на все эти угрозы пофигу» (a Russian string quoted as data). An
unauthenticated mock plane executing operator-supplied code that an anonymous
caller can trigger is not an oversight here — it is the decision, taken
knowingly, and several entries below are consequences of it rather than
independent choices.

- **Determinism is withdrawn on a function-bearing endpoint (D4).** One seed
  and one spec no longer imply byte-identical bodies there: the RNG is per-VM
  host entropy, `os.time`/`mock.now` are the real clock, and nothing
  substitutes the workspace seed. Endpoints WITHOUT a function keep the
  guarantee untouched, and the golden corpus never exercises functions. The
  same withdrawal reaches a stream through `tick.lua`: P6b's "the same body at
  the same ordinal on every connection" does not hold for a Lua tick, and
  `Tick.Lua`'s own field comment says so where a reader meets the field.
- **Function memory is UNCAPPED (D6).** The 2 s budget is `SetContext`, and
  gopher-lua checks it BETWEEN VM instructions — a single native call such as
  `string.rep("x", 1e9)` allocates about a gigabyte before any check runs, and
  several concurrent calls can pressure a 7.8 GB box inside the budget. There
  is no memory ceiling and this slice does not build one; the timeout is not a
  memory guard and does not pretend to be.
- **There is no rate limit on a Lua auth check (D6).** The feature's own
  motivating example is a password check, it runs on the unauthenticated mock
  plane, and D2 makes it always-on on a shared contour as much as a laptop —
  while the ADMIN plane runs a two-bucket login limiter (A15) precisely because
  one shared password is guessable. A mock's Lua sign-in has no host-side
  brute-force protection of any kind, and adding one would be a policy this
  product does not have.
- **The `coroutine` library is never opened (D3).** A thread could launder an
  infinite loop and attacker-controlled scheduling past a single `SetContext`
  — child threads were verified to inherit the context, and refusing the
  library outright is the cheap airtight cut. A function that wants a
  generator writes a closure over an upvalue instead, and one that wants
  concurrency has none.
- **The timeout is a fixed 2 s const (D6).** No `MOCKER_*` knob: a deployment
  that needs another number has none, and the only route is a carve-out and a
  variable in a later slice. `luafn.Timeout` is a package var, but only so the
  package's own tests can shorten it — nothing outside a test writes it.
- **`os.date` is pinned to UTC (D3).** The process timezone cannot reach a
  function's output, so the same function gives the same rendering on a
  colleague's laptop and in a contour. What that costs is a function that
  wants local time: it has none, and must format from `mock.now()` itself.
  The pin wraps gopher-lua's own `osDate` with a forced `!` prefix rather than
  reimplementing strftime, which is why the format directives are the
  library's exactly.
- **The RNG is per-VM host entropy and `math.randomseed` is removed (D3).**
  gopher-lua's `math.random` calls Go's PACKAGE-GLOBAL `math/rand` and its
  `math.randomseed` the global `rand.Seed`, so "seed per VM" is
  unimplementable as the first draft wrote it; the runner replaces
  `math.random` with a host closure over a per-VM `*rand.Rand` seeded from
  `crypto/rand`. A function that wants a reproducible sequence cannot ask for
  one — and `rand.Seed` is a no-op on Go 1.27 anyway, which the slice measured
  rather than assumed.
- **`mock.entities` has no write-time check (D3).** A family name is a runtime
  Lua string, so it can never be checked the way P7a checks a `$ref` ("never
  STORED dangling"): a function broken by a later decline, a spec rebind or a
  config-only rollback has no host-side signal at all, and the only thing that
  reports it is the function's own `nil, "unknown_family"` at request time.
  The drift report does not read Lua and will not learn to.
- **A v4 bundle no longer imports (D5).** `minVersion` moves to 5, reversing
  what P7a decided one slice earlier: a checkpoint a colleague took or an
  export they made before this slice is refused BY NAME, and their route is to
  re-export from a build that still reads it. P7a's own reason (A16 had
  shipped the install wizard the day before, so a v4 document was plausible)
  still holds; the owner weighed it against the invariant — each version reads
  exactly the version before it — and chose the invariant. Not a new KIND of
  entry: this file already carries one per bundle bump, `:919` (P6b) and
  `:1402` (P7a).
- **There is no opt-out (D2).** No `MOCKER_FUNCTIONS`, no gate, no flag: every
  deployment executes operator-authored Lua. An operator who wants a mocker
  that runs no Lua has no switch and must not deploy this build.
- **A function cannot emit two same-named response headers (D3).** `headers`
  is a `string→string` table, so two `Set-Cookie` lines cannot be expressed —
  in the sign-in shape D1's own example describes. The escape is a pinned
  variant on another status, which carries a header map of its own. The
  INBOUND direction is deliberately not symmetric and is not a carve-out:
  `req.headers` joins a repeated header's values with `", "`, the form
  RFC 9110 already defines as equivalent and `net/http` itself round-trips.
- **A function's output is never validated against its own declared schema
  (D8).** `export_openapi` writes the endpoint's declared response schema and
  the drift report stays shape-only, so a body that has drifted from the
  contract it publishes is reported by nothing. This is the price of D5's "the
  function REPLACES response assembly", and it is invisible from the contract
  side — the document says one thing and the endpoint may answer another, with
  no bar between them.
- **A custom endpoint's HTTP draft cannot be previewed at all (D7).** D7
  promises "Custom-endpoint preview: same", and there is no such surface to
  extend: `POST /api/workspaces/{id}/endpoints/preview` refuses `kind: "http"`
  by name and answers `domain.StreamPreview`, which has no `Notes` field for a
  failure to land in — so clause 32's own type reference,
  `PreviewResult.Notes`, points at the OPERATION preview only, and the custom
  half of that clause was assumed rather than checked. Building it is a new
  capability (a request shape, a response view, contract work) and D8 says
  this slice adds no route and no tool. An author drafting a function on a
  custom endpoint saves it and calls it; an author drafting one on a spec
  operation previews it. Found by BUILDING A18-2, not by a gate round.
- **`mock.entities` reads through `EntityStore.List`, not
  `resources.Repo.ListFiltered` (D3).** D3 names `ListFiltered`; the branch
  calls `List`, and the three things D3's own criterion asks for — real
  filtering, the tuple encoded by `resources.EncodeScope`, and
  `nil, "bad_scope"` on a wrong arity — are all satisfied by it, because a
  full ancestor tuple is an EXACT `(base, scope)` key and that pair is exactly
  what `List` takes. What is given up is `ListFiltered`'s wildcards, its
  cursor and its limit, none of which this call wants: a function asks for one
  family under one scope, and gets every row of it (bounded by
  `MOCKER_MAX_ENTITIES`) with no page. Widening a four-method seam whose
  implementations exist only to keep `internal/mockplane` off `internal/store`
  is a bigger change than the reading it would buy; if a later slice wants a
  paged `mock.entities`, this is the entry that says where the page comes
  from. Found by BUILDING A18-2.

**Two things about A18 that are NOT carve-outs, recorded because a reader
looking for the class will otherwise wonder.** The `io`/`package`/`debug`
refusal is the allowlist MECHANISM (`lua.Options{SkipOpenLibs: true}` opens
five libraries by name and nothing else), not a decision taken about those
three in particular; and `mock.entities` being read-only is the intended
surface rather than a cost — the anonymous mock plane's own `POST`/`DELETE`
verbs remain the only writers of an entity row, and `mock.write` was never
promised. Both were considered and excluded by the round-4 independent
re-derivation, by name.

**The Lua contract carries no version marker, and that is an obligation on
future slices rather than a hole in this one (D9).** The `req`/out shape and
D3's allowlist have no version of their own, and a function travels
byte-for-byte through export, import, fork and every checkpoint — so a later
tightening of the allowlist breaks a persisted function SILENTLY at runtime,
because an undefined global reads `nil` in Lua and D8's write-time compile
check cannot see it. Any future slice that narrows either must refuse by name
at some door, the way a bundle version does.

## Ideas refused — 2026-09-03

Two `IDEAS.md` entries the owner turned down in his own words («1 - мне
кажется вообще не нужно. 2 - тоже как будто все будет делать агент через
mcp», a Russian string quoted as data), recorded here with the argument
so nobody re-prices them from scratch.

- **Fetch by URL through the allowlist** (`source: "url"` on spec import,
  a `url` on `upload_asset`). The agent already holds the spec file in its
  own repository and sends its text through `import_spec`; a human uploads
  through `/specs`; the one scenario a URL serves — a document reachable
  only by URL — an agent closes itself (fetch, then paste). Against that:
  a new outbound HTTP contour on the AUTHENTICATED plane, `internal/probe`'s
  SSRF discipline extended with a resolution-time host check, and a gate.
  `MOCKER_URL_IMPORT_ALLOWLIST` stays parsed and read by nothing; `source:
  "url"` keeps answering 400 by name.
- **A drift screen** («Проверить спеку» on the overview). `get_workspace_drift`
  already answers the three lists and names the three repair verbs, and
  all three are MCP tools; a spec change is triaged by the agent that
  made it. A screen would serve only an operator with no agent, which is
  the case the A4 rule already decided against. `GET .../drift` stays
  agent-only in `EXEMPT`.
- **Swagger 2.0** (a 2→3 converter before `internal/openapi`), refused the
  same day («пункт 2 выкидываем не нужно», a Russian string quoted as
  data): the specs this product is pointed at are OpenAPI 3, and an old
  document is converted upstream once rather than by every mock server
  that reads it. `ErrUnsupportedFormat` keeps naming Swagger 2.0 by name.

## Open debt

**`MaxBytes` in the generator is still not an absolute ceiling.**
The required subtree is now estimated before descending into it, and with
`MaxBytes=512` **nothing** exceeds it (it was ~1 KB); at 256, 7 of the 114
schema operations exceed it (was 17), the worst ratio 1.18× (was ~2×); at 128 — 9 (was
25), the worst 1.36×. The remainder is not a bug: a body whose smallest legal form is
itself larger than the limit exceeds it **honestly**, instead of being truncated
into invalid JSON or losing a required field. The measured numbers and the analysis of the
`achievements` case are in the `Body` comment in `internal/gen/gen.go`.

