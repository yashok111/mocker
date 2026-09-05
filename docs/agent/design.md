# API design on top of a workspace (P7a, P7b) — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

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

