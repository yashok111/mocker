# Drift report (P4a) — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

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

