# Scenarios, checkpoints, export/import/fork — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

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


**A stored snapshot this build cannot decode is `409 snapshot_unreadable`**
(2026-09-05, the A18 review): on GET/activate of a scenario and GET/rollback
of a checkpoint, `bundle.ErrInvalid` is answered by name with the codec's own
message (`mockerBundle 4, this build reads versions 5..6`), and
`scenarios.SetActive` decodes the snapshot (`decodeSnapshot`, shared with
the scan) before it activates — a scenario no read path can decode must not
become the active one, because the mock plane's `scenarioSnapshot` fallback
would then serve the workspace layer under a switch that looked taken.
