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

## Remaining directions

| Area | Remaining work |
|---|---|
| Scenario export | Postman branches/loops and cURL response data bindings |
| Data flow | Using results beyond a conditional branch or loop |
| Response rules | Conditions on entity fields and further entity operations |
| State diagrams | Execution in the HTTP mock with state per entity |
| Event map | Delivery simulation, retries and DLQ |
| Impact analysis | Broader schema compatibility and dependencies across APIs |

## Recommendation

Next, connect state diagrams to real HTTP execution and entity state. The
entity blocks supply the underlying persistence operations. Postman control
flow and data flow beyond branch/loop boundaries require explicit execution
and visibility rules before export can share the runner's semantics.

## Deferred (the owner's call, 2026-09-03: «отложим на потом»)

Both are real, neither is next. Each is written with what it would take
so the next session prices it from here, not from scratch.

1. **Record-proxy** — a workspace mode that reverse-proxies to the real
   API and writes what came back as pinned overrides (and, for families,
   as entity rows). A mock of a live backend in a minute. Estimated 2026-09-03
   at two to three days WITH a gate: the client and its policy (~1 day —
   what to forward and strip of the headers each way, a body limit,
   timeouts, TLS and a corporate CA, no redirects; the upstream comes only
   from workspace settings under a host allowlist, never from the request,
   because the mock plane is unauthenticated and would otherwise be an
   SSRF pivot into the customer's network — §15 gains a section), the
   branch in `serveGenerated` and the recording (~1 day — a per-workspace
   mode `record` | `replay` | `passthrough`; an operation writes a pinned
   override through `overrides.Repo.Put`, a family writes rows through
   `resources.Repo.Set`, every body through the traffic log's redaction,
   auth routes never; first-wins or last-wins is a decision; a revision
   bump per recorded response discards the runtime cache per request in
   `record`), the gate, tests and the four documents (~1 day). What
   exists: `internal/probe`'s discipline (381 lines, not a proxy — only
   the rules carry over), the pinned variant's `body`/`mediaType`/
   `headers`, A11's entity write, the redaction. MCP-only, no screen. The
   questions to answer BEFORE the gate: whose CA, which hosts, what
   happens to the customer's cookies inside recorded bodies.
2. **Isolation** (`P5`) — users, roles, workspace ownership. A policy
   decision about the network before it is code; touches `internal/auth`,
   every handler's identity check and the MCP identity.
