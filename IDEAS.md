# IDEAS — remaining work

Updated 2026-09-30. Shipped decisions and verification history live in
`HISTORY.md`; deliberately limited behavior lives in `CARVE-OUTS.md`.

## Baseline in origin/main

`origin/main` includes visual response rule execution (`15f1474`), the
extended visual API workflows and shared graph layouts (`3597d58`), and
Postman response data bindings (`6144220`). Lua endpoint functions and
entity helpers shipped as A18/A19. They are available features, not pending
backlog decisions.

## Completed local slice

`feat/postman-precision-entity-rules` adds exact Postman JSON assertions and
variable extraction, plus entity read/create/update blocks in visual response
rules. Both slices passed regression, Newman, browser and real HTTP checks,
including managed draft/publication isolation. Full Go race checks, UI tests,
typecheck, lint and production builds passed. The slice is merged into local
`main`; remote publication is separate. Specs, plans and reports are under `docs/postman-precision-*`
and `docs/response-rule-entities-*`.

`feat/entity-state-execution` connects applied state diagrams to real entity
HTTP transitions. State lives in a selected business field; selection, exact
guards and data/state updates are atomic. Saved authoring and applied copies
stay separate, with explicit reapply and independent published data. Reports
and QA evidence live under `docs/entity-state-execution-*`.

`feat/entity-result-conditions` adds equals/not-equals and presence conditions
on previous entity results, nested JSON Pointers, exact numeric equality and
ordering, comparison with another result, bounded AND/OR groups and recursive
comparison traces. Named simulation examples persist with authored rules and
can be run by exampleId through REST/MCP. Saved and applied copies remain
separate. The slice is merged into local `main`; remote publication is separate.
Specs, plans and verification artifacts remain local under
`docs/entity-result-conditions-*` and `docs/entity-result-followups-*`.

## Remaining directions

| Area | Remaining work |
|---|---|
| Scenario export | Postman branches/loops and cURL response data bindings |
| Data flow | Using results beyond a conditional branch or loop |
| Response rules | Further entity operations (next set not selected) |
| Event map | Delivery simulation, retries and DLQ |
| Impact analysis | Broader schema compatibility and dependencies across APIs |

## Recommendation

Next, support branches and loops in Postman exports using the runner's control
flow. Data flow beyond branch/loop boundaries needs explicit result visibility
rules before the runner and exports can share that behavior. Event delivery
simulation remains the next separate runtime feature.

## Deferred (the owner's call, 2026-09-03: «отложим на потом»)

Record-proxy was resumed by the owner on 2026-10-02. Isolation remains deferred.

1. **Record-proxy** — implemented on `feat/record-proxy` (2026-10-02):
   HTTP passthrough, record/replay, per-operation policy, UI/REST/MCP and
   optional capture into confirmed entities. Request-keyed recordings are
   separate from pinned overrides, so concrete IDs/query/body/header variants
   do not overwrite one another and recording does not invalidate runtime
   caches. Remaining deliberate boundaries are listed in `CARVE-OUTS.md`.
2. **Isolation** (`P5`) — users, roles, workspace ownership. A policy
   decision about the network before it is code; touches `internal/auth`,
   every handler's identity check and the MCP identity.
