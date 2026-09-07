# The refactoring scan of 2026-09-07 — `A22`'s record

**Not auto-loaded.** Read this before a refactor, or before asking why something
is duplicated: every item below is either shipped in `A22` (marked ✔ with the
commit), left for a named reason, or refused with the argument. Numbers and
`file:line`s are as measured on the morning of 2026-09-07, BEFORE the builders
ran — a citation below points into the pre-`A22` tree, and the moved code is
found by name.

**How it was produced.** Ten Sonnet read-only agents, one vector each
(duplication, oversized functions, error envelope, SQLite repos, generator and
schema, MCP and route tables, concurrency and lifecycle, React components, FE
data layer, tests/tooling/doc drift), plus three vcodex `gpt-5.6-sol`/high runs
of which two died on the Codex usage limit before a verdict and the front-end
one finished. ★ marks an item two or more vectors found independently. Then
eleven builder commits in two waves of parallel git worktrees on disjoint file
sets (a worktree branches from the session's START commit, not from the moving
head — the FE split had to be rebased onto the helpers it was told were already
there), one cherry-pick each onto `main`, the four bars once at the end.

## What shipped (✔) and what did not

| # | Item | Outcome |
|---|---|---|
| 1, 1b, 2, 3, 5, 6, 13, 44 | admin boilerplate, the 413 bug, `write_busy` const, stale comments | ✔ `refactor(admin): collapse decode/404/conflict boilerplate, fix 413`; `write_busy` in `refactor(mockplane)` |
| 4, 7, 17, 25, 27 | `store.RowScanner`, `compareConfirmSlug` in decline, `store.BumpRevisionTx`/`IsUniqueViolation`/`BoolToInt`, `store.MarshalNullable`, `DB.Read` | ✔ `refactor(store): consolidate six repos' revision/scan helpers into store` — reverses the recorded "own copy" decision on the owner's word |
| 8, 10, 11, 12, 23, 44b | `openapi.EscapePointerToken`, `walkBudget` for the post-pass, the size gate on `jsonSize`, `openapi.SelectMediaType`, `openapi.WalkRefNodes` | ✔ `refactor(openapi): one home for the escape, the picker and the ref walk` |
| 9, 15, 20, 21, 24 | `gen.OptionsFrom`/`OverDocument`, `streamLoop.startClocks`, `buildRuntime` phases, `writeCustomBody`, the resources refusal-code table | ✔ `refactor(mockplane): one Options, one clock, one refusal table` |
| 14, 18 | `confirmSlug` tag test, ONE `route{}` table with `mcp` and `checkpoint` | ✔ `refactor(admin): one route table — MCP reach and undo policy on the row` |
| 16, 19, 29 | `run()` as phases + `Ready()` + wiring test, `janitor` type, the TLS-subnet guard test | ✔ `refactor(cmd): run() as named phases, plus a wiring-completeness gate` |
| 30–39, 42 | `QueryState`, `cachePolicy`, `conflictOf`, `format.ts`, `validation/json.ts`, the page-bundle splits, `EndpointFormFields`, `HeadersKeyValueList`/`WhenConditionsList`, `dialog-cancel` | ✔ `refactor(web): five shared helpers…` and `refactor(web): split the page bundles, one QueryState for the ladder` |
| 46, 47, 48 | AST coverage scanner, `TrafficPage` → model/hook/table, `cachePolicy` | ✔ `test(web): find route callers by AST, not by substring`; the rest in the splits commit |
| 41, 43 | `internal/testkit` (+ `adminkit`), the three biggest test files split | ✔ `test: dedupe the DB/admin-server bootstrap into testkit, split the three biggest test files` |
| 22, errors-#5 | `mergeAuthPresetBindings` named and unit-tested; the mock plane's decode error no longer echoes the raw Go error | ✔ the last small commit of the pass |
| 45 | doc drift: DESIGN headings 82, the comment ratio, goleak 37 | ✔ `docs(a22)` |
| 26 | `store.CollectRows` over 29 `rows.Next` loops | LEFT: every site wraps its own error context, which the project's style values more than the 80 lines |
| 28 | a per-workspace index in `stream.Registry` | LEFT: bounded by `MOCKER_STREAM_MAX_CONNS`; worth it only when the cap grows |
| 40 | `handleConflictReload` ×5 | LEFT by design: same name, different side effects per route; a shared hook would hide that |
| 49 | `WorkspacesPage`/`SpecsPage` splits | LEFT: function boundaries already good; reviewability only |

## Refuted hypotheses — do not re-propose without a new measurement

- A generic keyed-map-with-lock: five caches with five different eviction policies, each race-tested; nothing error-prone to remove.
- One `lifecycle` helper over every Run/Close: three shapes exist by design (single goroutine + done, `wg.Wait` over N connections, per-connection loops); the WS reader goroutine has no SSE analog.
- Splitting `walkObject`/`walkArray`/`assembleResponse`/`prepareConfirm`/`resourceBranch`/`rollbackTx`: each carries a `nolint:gocyclo` with a design citation; the walk state is one tightly coupled pass — the A15 bug class.
- Merging `workspaceCore` ×3 or `overrides` with `customep`: the divergences are load-bearing.
- Exporting admin's view types to kill the 49 MCP wire structs: a public-boundary change, a later slice. Generating OpenAPI schemas from the view structs would close the "schema drift twice" hole but is a project, not a refactor.
- MCP tool schemas are already reflected from struct tags; `toolCount = 63` and coverage's `70` are deliberate trip-wires, never to be derived.

## The scan itself, as delivered

## Tier 1 — cheap, mechanical, zero recorded decision against them

| # | Finding | Sites | Home for the shared form | LOC | Effort |
|---|---|---|---|---|---|
| 1 ★ | `decodeJSON` → `httpx.Err(400, "invalid request body")` boilerplate | 18 byte-identical sites in `internal/admin/*_handlers.go` (+6 variants) | `decodeBody[T](w, r) (T, bool)` next to `decodeJSON` in `internal/admin/server.go` | −50 | 30 min |
| 1b | **Bug riding on #1**: `http.MaxBytesError` from `MOCKER_MAX_BODY` is answered as a generic 400, not 413 — only `asset_handlers.go:111` handles it | every decode site except assets | the same helper branches on `errors.As(err, &mbe)` | +4 | in #1, plus one test |
| 2 ★ | "workspace not found" 404 re-spelled after a write race, outside `loadWorkspace` | 20 sites (`workspace_handlers.go:242/378/463/517`, `override_handlers.go:70/634/682`, `endpoint_handlers.go:351/632/666`, …) | `answerWorkspaceGone(w)` | −20 | 20 min |
| 3 | Four near-identical `answer*EditConflict` | `override_handlers.go:357`, `workspace_handlers.go:272`, `endpoint_handlers.go:502`, `scenario_handlers.go:188` | generic `answerEditConflict[T]` in `internal/admin` | −35 | 1 h |
| 4 ★ | `rowScanner` interface retyped 5× + inlined 3× | `traffic/repo.go:124`, `customep/repo.go:972`, `workspaces/repo.go:532`, `specs/read.go:480`, `overrides/repo.go:790`; inline `assets/repo.go:131`, `resources/repo.go:224`, `resources/entity_read.go:73` | `store.RowScanner` | −21 | 15 min |
| 5 | `parseResourceEntitiesLimit` = `parseTrafficLimit` (own comments cross-cite each other) | `resource_handlers.go:932`, `traffic_handlers.go:204` | `parseClampedLimit(r, def, max)` | −16 | 15 min |
| 6 | `parseWorkspaceID`/`parseSpecID` duplicate `parsePathInt64` | `server.go:420`, `spec_handlers.go:405` vs `endpoint_handlers.go:679` | delegate | −6 | 10 min |
| 7 | `decline.go` inlines the confirmSlug switch its sibling `compareConfirmSlug` exists to prevent | `resources/decline.go:110-113` vs `reset.go:156` | call the helper | −4 | 5 min |
| 8 | `escapePointerToken` ×3; gen's copy contradicts its own comment (gen already imports `internal/openapi`) | `openapi/resolver.go:220`, `gen/gen.go:659`, `specs/index.go:364` | `openapi.EscapePointerToken` | −12 | 10 min |
| 9 | `gen.Options{Seed, ListSize, NullRate, MaxBytes, Identity, Auth}` built by hand 5× | `mockplane/runtime.go:346,369`, `mockplane/stream.go:246,596`, `resources/populate.go:45` | `gen.OptionsFrom(settings, maxBytes)`; 3 sites also repeat Load→NewResolver→gen.New | −27 | 30 min |
| 10 | `postPassBudget` duplicates `walkBudget.visitNode` (constants already tied "so they don't drift") | `gen/recipes.go:113-131` vs `schema.go:71-120` | use `&walkBudget{}` | −10 | 10 min |
| 11 | **Perf bug**: `applyRecipePostPass` size gate calls `jsonx.Marshal(out)` to measure — allocates the very 161 MB body the comment cites; `jsonSize` exists for exactly this | `gen/recipes.go:171` | `jsonSize(out)` with Marshal fallback on `!ok` | +4 | 20 min |
| 12 | `SelectMediaType` ×2 with a leaf-vs-store excuse that `internal/openapi` resolves | `specs/index.go:348`, `design/design.go:350` (design's defaults to `application/json` on empty map — preserve) | `openapi.SelectMediaType` | −18 | 30 min |
| 13 | `write_busy` is the one error code left as a bare literal | `admin/entity_write_handlers.go:138,202`, `mockplane/resource.go:650,700` | `const codeWriteBusy` | 0 | 5 min |
| 14 | `confirmSlug` jsonschema tag text copied into 8 struct tags, comment says "verbatim", nothing checks | `mcp/confirm.go:18` vs `tools_endpoints.go:727`, `tools_history.go:215/263/338/380/411`, `tools_assets.go:177`, `tools_resources.go:278/333` | reflection test asserting each equals `confirmSlugDoc` | +30 test | 30 min |
| 15 | SSE vs WS timer scaffolding (`wsLoop` embeds `*streamLoop` yet re-derives lifetime/ping/tick timers) | `mockplane/stream.go:481-511` vs `ws.go:336-352` | `streamLoop.startClocks()` | −5 net | 2 h |
| 16 | `MOCKER_TLS_SUBNET` default hardcoded in bash and Go (documented, but nothing catches drift) | `scripts/compose-tls.sh:59`, `cmd/mocker/setup_compose.go:23,60` | a guard test that greps the script | +10 | 20 min |

## Tier 2 — real value, needs a decision or care

| # | Finding | Why it needs a decision |
|---|---|---|
| 17 ★ | `bumpRevisionTx` ×6 byte-identical (`assets/repo.go:257`, `customep/repo.go:536`, `resources/workspace_tx.go:43`, `checkpoints/write_tx.go:219`, `scenarios/repo.go:930`, `overrides/repo.go:565`), `workspaceRevisionTx` ×2, `isUniqueViolation` ×4, `boolToInt` ×2 | Every copy carries a comment blessing the copy ("no package imports another for a four-line helper") and CLAUDE.md codifies "own `bumpRevisionTx`". Three agents note the stated reason is about sibling imports, and all six already import `internal/store` — so `store.BumpRevisionTx` breaks no rule. Reverses a recorded decision; owner's call. −45 LOC. |
| 18 ★ | `mcpAllowedRoutes` retypes 56 pattern strings from `Server.routes()` (`admin/loopback.go:82-198`); plus 5 checkpoint-policy maps keyed by the same strings (`route_table.go:55` + 4 in `autocheckpoint_test.go:373-471`) | One `route{}` with `mcp`/`checkpointPolicy` fields, allowlist derived by filter. `loopback.go` calls itself "the single highest-risk object" (bearer gate) — keep `mcpPathAllowed` untouched, change only its source list; keep one test pinning the excluded set. Medium effort, medium risk. |
| 19 ★ | `cmd/mocker/main.go:147-509` `run()` is 363 lines with 14 post-construction setters and no wiring-completeness check — the exact "green suite, dead feature" failure CLAUDE.md records twice | Split into `app` struct + `wireMockPlane`/`wireStreaming`/`wireMCP`/`startAndDrain`; add `Plane.Ready() []string` naming nil required sources, asserted by a cheap unit test. Constructor-with-options does NOT fix it (nil interface fields). ~1.5 days. |
| 20 | `buildRuntime` 217 lines (`mockplane/runtime.go:280-496`) — five clear phases | Extract `loadComposedOverrides`, `buildGeneratorForWorkspace`, `loadCustomRoutes`, `loadResourcesByFamily`. No new `gen.Body`/`assembleResponse` caller. Medium. |
| 21 | `serveCustom` tail (`mockplane/custom.go:256-370`) — write phase extractable; do NOT unify with respond.go (headers differ by design, comment 246-248) | `writeCustomBody(...)`. Medium. |
| 22 | `handleApplyAuthPreset` (`admin/preset_handlers.go:241-396`): promote the `merge` closure (324-372) to a named pure function, testable without HTTP/DB | Low-medium. |
| 23 | Three `$ref` tree-walkers over `map[string]any` (`customep/stream.go:379`, `customep/operation.go:206`, `mockplane/custom_schema.go:135`) with bool/collect/mutate semantics | `openapi.WalkRefNodes(node, visit)`; −40 LOC; three tests must pass verbatim. Medium. |
| 24 | `storeErr` (`mockplane/function.go:473`) is a THIRD hand mapping of the 5 `resources.Err*` sentinels (HTTP in `resource.go`, admin in `entity_write_handlers.go`) | Shared `{sentinel, shortCode}` table like `refusal_codes.go`, plus a sync test. Medium. |
| 25 | Nullable-JSON column codec pairs identical modulo type between `overrides/repo.go:751,762,838-849` and `customep/repo.go:924,935,1033-1046` | `jsonx.MarshalNullable[T]`/`UnmarshalNullable[T]` (error prefix as arg). −35. On the decode path HARD RULE 4 guards. |
| 26 | `rows.Next()/Err()` collect loop ×29 in 9 packages | `store.CollectRows[T]` — but each site wraps a specific error string; the project's style will likely push back. Only if one wrap point per site is acceptable. |
| 27 | No `store.Read` mirror of `store.Write`; `checkpoints/transfer.go:70` hand-rolls a read-only tx | Additive; low urgency. |
| 28 | `stream.Registry` scans every conn per workspace lookup (`registry.go:116-127,182-209,245-258`) | Secondary `map[int64]map[*Conn]` — only if `MOCKER_STREAM_MAX_CONNS` grows. |
| 29 | `runJanitor` inlined in `main.go:399-403,525-549`, no isolated test unlike `traffic.Recorder` | Extract a tiny typed `Run(ctx)`. Small. |

## Front end (`web/src`)

| # | Finding | Sites | Fix | Risk |
|---|---|---|---|---|
| 30 ★ | Four-state query ladder (`isPending ? Loader+«Загрузка…» : isError ? Alert+«Повторить» : …`) copy-pasted | 15+ screens (`OperationsPage.tsx:252-289`, `WorkspaceOverview.tsx:31-50`, `ScenariosPage.tsx:~94`, `CustomEndpointsPage.tsx:339`, …; `IconAlertTriangle` in 23 files) | `<QueryState query testIdPrefix onRetry>` + a multi-query variant | **Highest FE risk**: `routes.test.tsx` and every page test assert the exact root/`-error`/`-retry` testids in all four states; prefix must be a parameter. −250/+60. |
| 31 ★ | `getGetWorkspaceQueryKey(id)` invalidated ad hoc | 18 sites in 10 files (`CustomEndpointsPage.tsx` ×6 alone: 442/669/911/965/1135/1182) | `useInvalidateWorkspace(id)` hook | Low-medium, mechanical. |
| 32 | `edit_conflict` extraction expression ×7 | `AuthPresetPanel.tsx:349`, `OperationEditor.tsx:575`, `SettingsPanel.tsx:350`, `CustomEndpointsPage.tsx:985,1191`, `ScenariosPage.tsx:694` | `conflictOf(mutation)` in `api/errors.ts` next to `isGoneTombstone` | Low. −21. |
| 33 | One-file page bundles: `CustomEndpointsPage.tsx` 1274 (4 components), `OperationEditor.tsx` 1007, `TrafficPage.tsx` 923, `StreamEditor.tsx` 891, `HistoryPage.tsx` 815 (5 components), `ScenariosPage.tsx` 813 (5) | — | Split by move into `custom-endpoints/*`, `history/*`, `scenarios/*`; `StreamEditor.tsx:1-409` is pure draft logic with no JSX → `streamDraft.ts`, `StreamCapsStrip` (770-891) is unrelated | Pure moves; the 700-800-line test files import by name, add `export`. |
| 34 | `CreateEndpointForm`/`EditEndpointForm` near-identical JSX (`CustomEndpointsPage.tsx:393-635` vs `841-1106`) | — | `EndpointFormFields` presentational only; keep the two state bodies apart (their divergent invariants are commented on purpose) | −150. Medium. |
| 35 | `VariantEditor.tsx` (574, one render): hand-rolled headers editor (202-221) and `when[]` list (222-241) | — | `HeadersKeyValueList`, `WhenConditionsList`; keep the producer-exclusivity state machine unified | Three test files reach through `testId()`/`whenTestId()` — ids must survive. |
| 36 | `formatBytes` **disagrees** between `AssetsPage.tsx:59` (`toFixed(1)`) and `StreamEditor.tsx:760` (`toFixed(0)` on exact multiples): "1.0 МБ" vs "1 МБ" | — | `web/src/format.ts`, one rounding rule | Both tests likely assert exact strings. |
| 37 | `formatTimestamp` ×5 — `HistoryPage.tsx:158` explicitly argued a 4th copy is cheaper; an undocumented 5th appeared at `AssetsPage.tsx:334` | `SpecsPage.tsx:77`, `CustomEndpointsPage.tsx:277`, `ScenariosPage.tsx:156`, `HistoryPage.tsx:160`, `AssetsPage.tsx:334` | same `format.ts` | Reverses a recorded decision — flag, do not do silently. |
| 38 | `jsonLocation` exported once (`VariantEditor.tsx:81`), re-implemented at `CustomEndpointsPage.tsx:144`, ignored by 5 hand-rolled `JSON невалиден (…)` sites | `StreamConnectionsPage.tsx:160`, `StreamEditor.tsx:207`, `SettingsPanel.tsx:67`, `ResourceEntities.tsx:195,440` | `validation/json.ts` + a shared arktype JSON-body validator | Changes visible Russian copy → tests update. |
| 39 | Two arktype status-code validators differing in the empty-string branch (`CustomEndpointsPage.tsx:127`, `:220`) | — | `statusCodeField(required)` | Trivial. |
| 40 | `handleConflictReload` ×5 same name, genuinely different side effects | — | Do NOT extract; document the pattern once in `contract-frontend.md` | — |

## Tests, tooling, docs

| # | Finding | Fix |
|---|---|---|
| 41 ★ | `newTestDB` byte-identical in 9 `_test.go` (assets, checkpoints, customep, overrides, scenarios, workspaces, store, + path-parametrised resources, specs); admin-server bootstrap duplicated in `admin/admin_test.go:96` and `mcp/mcp_resources_test.go:111` | `internal/testkit.NewDB(t)` / `NewAdminServer(t, cfg)`. Do NOT touch per-package `insertWorkspace` fixtures (deliberate, `mcp_resources_test.go:143`). −130. |
| 42 | 7 test files assert `getByText("Отмена")`; ≥5 assert the literal «Загрузка…» where CLAUDE.md prescribes the root testid | `data-testid="dialog-cancel"` + `closeDialog()` helper; loading assertions on the testid. |
| 43 | `checkpoints/repo_test.go` 3477 lines, `mockplane/respond_test.go` 3056, `resources/repo_test.go` 2134 (23 files > 800) | Split by concern, mechanical. |
| 44 | Stale comments: `admin/workspace_handlers.go:402` "the ten existing gocyclo pinpoints" (15 today); `overrides/overrides.go` `ValidateSchemaShape` cites `customep.ValidateSchemaDoc`, real name `ValidateRefs` (`operation.go:161`) | One-line fixes. |
| 45 | Doc drift, measured: 70 ops ✅, 63 tools ✅, 36 vars ✅, 36 goleak ✅, 39 nolint ✅ (38 real + 1 narrative mention makes the count coincidental); **DESIGN.md "50-odd headings" → 82**; **comment ratio "19336/48905" → 24834 comment / 36610 code (tokei)** | Refresh CLAUDE.md numbers on its next touch. |

## Refuted hypotheses (agents checked and advise against)

- Generic keyed-map-with-lock: five caches, five different eviction policies, all race-tested — unifying removes no error-prone code.
- One `lifecycle` abstraction over Run/Close: three shapes exist by design (Recorder, Registry wg.Wait, per-conn loops); WS's reader goroutine has no SSE analog.
- `walkObject`/`walkArray`/`assembleResponse`/`prepareConfirm`/`resourceBranch`/`rollbackTx` splits: every one carries a `nolint:gocyclo` with a design citation; walk state is one tightly coupled pass (the A15 bug class).
- `workspaceCore` ×3, `overrides` vs `customep` merge: divergences are load-bearing.
- 49 MCP "wire" mirror structs → exported admin views: high risk, touches the public boundary — a later slice, not a refactor. Generated OpenAPI schemas from view structs would close the "schema drift twice" hole but is a project (~400 LOC generator + orval/CI).
- MCP tool schemas are already reflected from struct tags (`sdk.AddTool`), nothing to derive; `toolCount = 63` and coverage's `70` are deliberate trip-wires.
- `time.Now` in `checkpoints` not injected — tests already age rows in SQL; theoretical.
- Memoising `OperationsPage` filter/group — unmeasured; only with a profile.
- `fetch(` in `connect/probe.ts:110` is a documented cross-origin exception, not a violation. Session guard is already one `beforeLoad`.

## vcodex `gpt-5.6-sol`/high — front end (the only run that reached a verdict; the Go and cross-cutting runs died on the Codex usage limit, retry after 11:59)

Converges with #30–#35 and #38 above; adds three things Sonnet did not rank:

| # | level | Finding | Fix |
|---|---|---|---|
| 46 | high | `web/src/api/coverage.test.ts:84-217` is a substring scan over concatenated source: a comment, a dead function, or a `get…QueryKey(` call that performs no request produces a false green; an alias a false red. **Verified stale title**: line 166 says "exactly 64 routes" while line 194 asserts 70. | AST-based caller scanner over the TypeScript already in devDeps; resolve imports, ignore comments, count executed hooks only; explicit manifest for native `EventSource`/raw-fetch transports; fixtures for the five false cases. +80-120 LOC, 8-12 h. |
| 47 | high | `TrafficPage.tsx:76-923` couples the three-source feed state machine, generation fencing, cursor/cache keys, SSE lifecycle, polling, filters, mutations and the table; clear-vs-in-flight logic sits inside JSX. | `traffic/model.ts` (token/merge/filter, pure), `useTrafficFeed.ts` (tail/poll/SSE/generation), `TrafficTable.tsx`; page stays the orchestration boundary. Page −450 LOC, 10-14 h. Pins: `TrafficPage.test.tsx:146-191, 357-645, 696-755`. |
| 48 | high | The cache dependency graph is re-derived at every mutation (`CustomEndpointsPage.tsx:428-442`, `OperationEditor.tsx:318-327`, `TrafficPage.tsx:409-530`, `SpecsPage.tsx:320-354`) — the structural version of #31. | `api/cachePolicy.ts` with named policies (`invalidateEndpointChange`, `invalidateOperationChange`, `invalidateScenarioActivation`, `invalidateWorkspaceResources`), unit-tested for exact/prefix/predicate. −40-70 LOC, 8-12 h. |
| 49 | low | `WorkspacesPage.tsx` (623) and `SpecsPage.tsx` (641): orchestration + forms + modals + file parsing in one file; function boundaries already good. | Reviewability split only. |
