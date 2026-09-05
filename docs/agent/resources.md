# Resources: confirmed families, entity rows, nesting, basePath, rederive, ref — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

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

