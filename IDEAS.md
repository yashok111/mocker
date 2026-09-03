# IDEAS — what could come next, ranked by cost

The backlog that is NOT a design debt: things nobody promised in
`DESIGN.md`, listed by an agent on 2026-09-02 after `A10` and kept here
because a ranked list in a chat transcript is gone by the next session.
Cost is for one slice in this repository (a gate where one is due, tests,
the four documents). Items move out of this file when they ship (to
`HISTORY.md`) or when they are refused (to `CARVE-OUTS.md`, with the
measurement). The five cheap items of the first ranking (`import_spec`,
YAML, `config.Limits`, the «Файлы» tab) shipped as `A8`–`A10` the same day;
the fifth was mis-priced and is item 1 below. Entity writes (once item 4)
shipped as `A11` the same evening; bundle export/import and fork (once
items 2 and 3) shipped together as `P4b` the same night, the test-suite
plugin (once item 6, then 4) as `A12` right after, and
`MOCKER_STREAM_TRAFFIC_FRAMES=all` (once item 5, then 3) as `A14`. On
2026-09-03 the owner refused fetch-by-URL, the drift screen and Swagger
2.0, and deferred the last two.

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

## Shipped 2026-09-03: API design on top of a workspace (`P7a`)

The section that stood here — the owner's brief, the four items priced
against the code — became DESIGN v12 §34 by the owner's hand and then
`P7a` (items 1, 2 and 4: the response schema on a custom endpoint, the
export as one OpenAPI document, the agent's tool and guide topic). How it
arrived is `HISTORY.md`, what it deliberately does not do is
`CARVE-OUTS.md`. Item 3 — the read-only «Контракт» tab, the A4 rule lifted
for it by the owner — shipped the same day as `P7b`, from the same gate
document (`mocker-p7-api-design`, D12). §34 is complete.

## Refused or deferred elsewhere

Deeper nesting (a fourth level), the Monaco/schema-tree editor and the
recipe editor are `CARVE-OUTS.md` entries with measurements, not ideas.
Fetch-by-URL, the drift screen and Swagger 2.0 were refused by the owner
on 2026-09-03 — `CARVE-OUTS.md`, "Ideas refused".

## Recommendation

§34 is closed (`P7a`, `P7b`). Record-proxy is next if the owner lifts the
deferral; its open questions are listed above.
