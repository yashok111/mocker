# A18 — endpoint functions (Lua) — gate decisions

Date: 2026-09-04. Status: decided, reviewed (round 1 below), awaiting the
owner's word to cut the slice.
The owner asked for the feature and then answered four direct questions; his
answers are quoted verbatim below as data, in the language they were given in.

**The ask.** Today an endpoint's behaviour is assembled from four layers and
fourteen recipe kinds, but there is no way to attach LOGIC to an endpoint —
"check the password and branch", "issue a token that expires in an hour and
remember it". The owner wants exactly that for local use:

> «хочу такую фичу для локального инструмента на все эти угрозы пофигу»

The threat-model objection (an unauthenticated mock plane executing
operator-supplied code an anonymous caller can trigger) is hereby OVERRULEN BY
THE OWNER for this product. It is recorded as accepted, not argued away.

---

## D1 — Runtime: gopher-lua, always

> «это все равно будет писать агент через mcp так что без разницы на каком
> языке. пусть lua будет чтобы быстро было»

`github.com/yuin/gopher-lua` v1.1.2 (MIT) becomes the tree's THIRD library,
behind ONE importing package — `internal/luafn` — with a boundary test that
fails the build on a second importer, the identical shape `internal/wsmock`
(coder/websocket) and `internal/yamlx` (yaml) already have. The admission
follows the §30.9 precedent: a written measurement, made 2026-09-04 in a
throwaway module (the repository untouched):

- **cgo: zero.** `CGO_ENABLED=0` builds and runs; the library is pure Go.
- **Runtime executable memory: none.** gopher-lua is an INTERPRETER over its
  own opcode array — it generates no machine code at runtime, ships no JIT;
  the §30.9 property holds by construction.
- **Transitive modules.** The raw `go get` graph shows 4 (`chzyer/logex`,
  `chzyer/readline`, `chzyer/test`, an old `golang.org/x/sys`) — all of them
  belong to the REPL subpackage this tree will never import, and NONE is
  LINKED into a binary importing only the root `lua` package (verified:
  `go list -deps` shows gopher-lua's own `ast`, `parse`, `pm` only). The
  in-tree admission runs `go mod tidy` FIRST and records the tidied
  `go list -m all`: expected to satisfy §30.9's "zero transitive modules"
  literally; if tidy cannot prune them, the divergence is recorded with the
  linked-not-linked evidence, never silently.
- **Binary size: +1.55 MiB** (baseline hello-world 2.43 MB → 3.99 MB with the
  VM linked).
- **Interruptibility: confirmed.** `L.SetContext(ctx)` on
  `while true do end` returns `context deadline exceeded` at the deadline
  (measured: interrupted 101.2 ms into a 100 ms budget). This is what makes
  the wall-clock timeout of D6 real rather than aspirational. NOTE (round 1):
  the check fires BETWEEN VM instructions — it cannot interrupt a single
  native call; see D6's memory wording.
- **`-race` suite delta: to measure in-tree during the slice** (the number is
  only meaningful over the real test tree; §30.9 recorded the same way).

## D2 — Enablement: always on, no flag

> «Всегда вкл»

No `MOCKER_FUNCTIONS` variable, no gate. Recorded plainly: EVERY deployment —
a colleague's laptop and a shared contour alike — executes endpoint Lua. The
sandbox of D3 is the whole of the containment there will be.

## D3 — The function's contract

One Lua source string per operation (D5). Per invocation mocker creates a
fresh VM, opens the sandbox, calls the function with the request, takes the
response, closes the VM. **Stateless by construction**: no global, no closure,
no variable survives between two calls; the only persistence a function can
see is `mock.entities` (read-only).

**Sandbox — built ALLOWLIST-ONLY, not by stripping** (round 1 RED: `OpenBase`
also registers `loadstring`, `module`, `newproxy`, `require`, `raw*` —
baselib.go:35–55; a strip-list of four names is not airtight). Construction:

1. `lua.NewState(lua.Options{SkipOpenLibs: true})` — NOTHING is preloaded.
2. Open exactly: `OpenBase`, `OpenString`, `OpenTable`, `OpenMath`, `OpenOs`.
3. Remove from `_G` BY NAME after opening: `load`, `loadstring`, `loadfile`,
   `dofile`, `print`, `module`, `require`, `newproxy`, `collectgarbage`
   (gopher-lua's implementation calls process-wide `runtime.GC()` — one
   function must not impose GC latency on unrelated work).
4. From `os` keep ONLY `time`, `clock`, `date` — remove `execute`, `exit`,
   `getenv`, `remove`, `rename`, `setlocale`, `tmpname`.
5. NEVER OPEN: `io`, `package`, `debug`, `coroutine` (a coroutine/thread
   could launder an infinite loop and an attacker-controlled scheduling past
   a single `SetContext`; round 1 verified child threads WOULD inherit the
   context — refusing the lib entirely is the cheap airtight cut).
6. A test pins the SURVIVING `_G` key set against a frozen allowlist — a
   gopher-lua upgrade that adds a global fails the build, not a customer.

gopher-lua compiles SOURCE only (`L.Load` parses; `string.dump` is
unimplemented — round 1 verified): there is no bytecode ingress to close.

**RNG — overridden, not seeded** (round 1 RED: gopher-lua's `math.random`
calls Go's PACKAGE-GLOBAL `math/rand` and `math.randomseed` the global
`rand.Seed` — mathlib.go:186–205; "seed per VM" is unimplementable as
written). The runner REPLACES `math.random` with a host closure over a
per-VM `*rand.Rand` seeded from `crypto/rand`, and REMOVES
`math.randomseed`. Honest entropy (D4) survives; nothing shared remains.
`os.date` is pinned to UTC (the process-local timezone must not change a
function's output between machines).

**In**: the function receives ONE argument, a table `req`:

```
req.method      string
req.path        string          the canonical matched path
req.pathParams  table           {id = "7"} — the route's own {} segments
req.query       table           always strings; repeated key → array
req.headers     table           lowercased keys
req.body        table|string    decoded JSON when parseable, raw string when not
```

**Out**: `return status, body, headers` — status a number (validated
100..599 as `overrides.ValidHTTPStatus` already does), body a table (encoded
as JSON by mocker — `internal/jsonx`, the function never sees an encoder) or
a string (served as raw bytes), headers an optional table of string→string.
A nil body is an empty body. Anything else the function returns — a
non-number status, a boolean body — is a `function_failed` 500 (D6), not a
silent coercion.

**Helpers** — the full set, the owner's explicit pick:

> «Полный набор (Рекомендую)»

- `mock.jwt(claims)` → string | nil, err. Signs EXACTLY as the `jwt` recipe
  does (`internal/recipes/jwt.go` — the same claims converter, which is
  already bounded and cycle-rejecting; no second conversion rule is invented)
  with the workspace's OWN `settings.auth`. REFUSES (nil, "auth_not
  configured") when the workspace auth carries `alg: none` or no key: an
  unsigned token pretending to be signed is worse than an error (round 1
  RED). The alg is visible in the token header by JWS construction; the
  SIGNING KEY never enters the Lua state.
- `mock.now(offsetSec?)` → number, unix seconds, REAL clock.
- `mock.entities(family[, scopeArray])` → array of row tables | nil, err.
  Resolution is the `ref` recipe's, verbatim (`internal/mockplane/ref.go`):
  the family is looked up in THIS workspace's runtime `route_family`-keyed
  roster, rows are read through the filtered list read under the SERVING
  request's own base scope, scope values arrive RAW and are encoded by the
  HOST (never double-escaped by the function); an unconfirmed or unknown
  family → nil, "unknown_family". Read-only: there is no `mock.write` — the
  anonymous mock plane's own POST/DELETE verbs remain the only writers.

**Determinism**: honest and OUT of the guarantee —

> «Честный вне гарантий»

Per-VM RNG seeded from real entropy, `os.time` the real clock, and NOTHING
substitutes the workspace seed. One seed + one spec no longer implies
byte-identical bodies ON ENDPOINTS THAT CARRY A FUNCTION; endpoints without
one keep the guarantee untouched. The golden corpus never exercises functions
and does not start. Recorded in `CARVE-OUTS.md`, with a negative test pinning
that boundary (round 1 GREEN made mandatory).

## D5 — Where a function attaches: a VARIANT field, both writers

- `overrides.Variant` gains `Function string`; `custom_endpoints`'
  definition gains the same field. **No migration** — both are JSON columns
  already.
- **`Function` is a field of ONE VARIANT (one status), not of the row**
  (round 1 RED: the first draft's `400 function_and_responses` was
  ill-defined — `Responses` IS the row's status→Variant map, and refusing it
  would break mixed rows). Exclusivity is PER VARIANT: a variant carrying
  `function` refuses `body`, `bodyRef`, `recipes` and `schemaPatch`
  (`400 function_and_body`) — one producer per variant, no precedence to
  document. OTHER statuses' variants on the same row are untouched:
  a function-200 with a pinned-401 sibling is legal and useful (the sign-in
  shape). `when[]` on a function variant is ALLOWED — selection logic is
  unchanged; the function runs only when the variant is selected. The auth
  preset binding recipes onto a function-carrying variant refuses exactly as
  any other recipe-on-pinned refusal does today.
- When `function` is set and the row is on, the function REPLACES response
  assembly for that variant — recipes, `schemaPatch`, the envelope,
  `assetType` do not run. `mock.jwt` composes with it from inside the
  function instead.
- `kind: "sse"`/`"ws"` custom endpoints refuse the variant-level `function`
  by name (`400 function_on_stream`) — a stream is not a request/response.
  Streams get their OWN two hooks instead: D10.
- **Persistence: `Function` rides every path its carrier already rides** —
  checkpoint `config_snap`, CAS conflict documents, `export_workspace` /
  `import_workspace`, `fork_workspace` — the same bytes, no special casing;
  each round trip gets a test. One asymmetry is INHERITED, not introduced: a
  scenario snapshot carries overrides but NEVER custom endpoints (DESIGN
  §12, `capture.go:127`) — so a scenario switches a function on a spec
  operation's override and cannot touch a custom endpoint's. Stated, not
  changed.
- **Bundle v6 reads v5** (and the decode chain already accepts older): the
  field is additive; a v5 document without functions imports unchanged. An
  OLD (v5) binary meeting a v6 document REFUSES it by name — the P6b
  precedent, no silent field-dropping.

## D6 — Execution guards

- **Wall-clock timeout: a fixed 2 s const** (`functionTimeout`), no env
  variable — `SetContext`, measured in D1. On expiry: `503 function_timeout`,
  traffic note `function_timeout`. A knob is a carve-out away if a real load
  wants one.
- **The 2 s timeout is NOT a memory guard and does not pretend to be**
  (round 1 YELLOW, adopted): the context check fires between VM instructions
  only; a single native call (`string.rep("x", 1e9)`) allocates ~1 GB before
  any check, and several concurrent calls can pressure a 7.8 GB box inside
  the budget. Memory stays UNCAPPED because the owner accepted it (D2); the
  carve-out says so in these words.
- **The output passes the shared safety tail before the wire** (round 1 RED:
  a function returning `Content-Type: text/html` would otherwise serve
  browser-executable content the rest of the plane refuses on write). The
  function's `Content-Type` runs through `httpx.BrowserExecutableMediaType`
  (refuse → `500 function_failed`), header VALUES are validated (strings, no
  CR/LF, sane length), and the body is FULLY materialized and capped BEFORE
  `WriteHeader` — over `MOCKER_MAX_RESPONSE` → `500 function_too_large`,
  never a partial response, never a committed-then-refused one.
- **Errors are classified INSIDE the runner** (round 1 YELLOW): a recovered
  panic or Lua error → `500 function_failed`; the context deadline →
  `503 function_timeout`; REQUEST cancellation is not classified as either —
  the client is gone, nothing is answered. The traffic note carries the
  error's first line CAPPED (≤ 200 bytes) — Lua error text can embed request
  data (tokens, headers) and notes are not a disclosure channel. No raw
  error objects, no Go stacks.
- **Memory: uncapped, accepted** — see above; the residual is recorded in
  `CARVE-OUTS.md`, not hidden.

## D7 — Serving order: the resources' mirror

The branch sits exactly where `resourceBranch` sits — AFTER `route_off`, the
session layer (a forced status answers without running the function;
`fail_next` consumes and answers before it; a pause parks the request BEFORE
the VM is created; a delay delays the run), after the 406 gate — and INSTEAD
of `assembleResponse`. `serveCustom` branches at the same logical position;
the two matrices (spec operation vs custom endpoint: where the branch sits
against the 406 gate and media negotiation) are written out in the guide
(round 1 YELLOW: "identical" was doing unspecified work; "forced" always
means the live-state status). The auth-check request traffic recording
suppression stays suppressed. Traffic records the row normally with the note
token `function`.

**Function vs confirmed resource on the same 2xx operation** (round 1 RED —
the first draft was silent): **the function WINS**. It is the Workspace
layer's explicit operator statement; the resource is generated convenience.
The resource branch takes the operation only when no function variant
applies. A test pins the precedence both ways.

**Preview** (round 1 YELLOW — three surfaces, named): operation preview runs
a draft's `function` through the same runner with the request the preview
carries; a failure lands in `PreviewResult.Notes`, never as a status.
Custom-endpoint preview: same. Stream preview: D10. The P7a carve-out on
custom schema preview is not reopened. `TestAssembleResponseIsTheOnlySeam`
is untouched: the function branch is a SIBLING of that seam (it produces the
bytes `assembleResponse` would have), not a fourth caller of it — the test's
own comment gains the sentence.

## D8 — Surface: no new routes, no new tools, contract grows in place

`set_operation_variant`, `create_endpoint`, `update_endpoint` and the preview
tools gain `function`; the stream shapes gain D10's two fields. The writers
VALIDATE by compiling: gopher-lua's parser runs at write time, `400
bad_function` carries the parser's own words (the same shape
`ValidateStreamFor`'s refusals already have).

**The contract work is real and enumerated** (round 1 RED: "no new routes"
is not "no contract work"): `api/openapi.json` gains `function` in the
create/update requests, the shared Variant view, the conflict payload, and
the endpoint conflict details, plus `tick.lua`/`onFrame` in the stream
shapes; the MCP input schemas regenerate from the Go types; `make ui-gen`
regenerates orval. The operations count STAYS 70; `coverage.test.ts` learns
nothing; no screen (the A4 rule — the owner said so himself: «это все равно
будет писать агент через mcp»).

**P7a export interplay** (round 1 RED — the first draft never mentioned it):
`export_openapi` exports the custom endpoint's operation metadata and its
DECLARED response schema normally; the Lua source is mocker-only behaviour
and is OMITTED from the OpenAPI document — OpenAPI cannot express it. A
function's dynamic output is not schema-validated (there is no schema to
validate against beyond the declared one), and the drift report stays
shape-only: a function-bearing row is not drift by itself.

## D9 — Documentation at slice end

`docs/USER-GUIDE.md` (the operator's copy, Russian), `skills/mocker/` all
four references + `internal/guide` resync (`make guide-sync`), the `design`
guide topic untouched, `CLAUDE.md` Architecture + `HISTORY.md` +
`CARVE-OUTS.md` (determinism carve-out, memory residual, `coroutine`
refusal, the const timeout, the UTC pin, the RNG override) — the standing
slice-end set.

## D10 — Streams: two Lua hooks (added on the owner's word)

> «добвляй 1 и 2 в гейт»

The variant-level `function` stays refused on streams (D5). Streams get two
hooks of their own, both through the SAME `internal/luafn` runner, the same
sandbox (D3), the same honest non-determinism (D4), the same compile-at-write
`400 bad_function` (D8).

**1. `tick.lua` — Lua as the tick's producer (sse and ws).** The tick
document `{intervalMs, event, schema}` gains `lua`; `lua` and `schema` are
mutually exclusive by name (`400 tick_lua_and_schema`) — one producer per
tick, the identical exclusivity D5 keeps on a variant. Per firing the runner
calls `function(ordinal)` — the same ordinal the generated body is seeded by
today — and takes `return data`: a table → JSON-encoded by the host, a
string → the raw bytes of the `data:` line; `event` stays declarative in the
tick config. A nil return skips that firing (the connection stays open, the
frame is simply not sent). **A Lua tick's output passes the SAME frame checks
a timeline frame does** (round 1 RED: an arbitrary string can break SSE
framing): the body must fit an SSE line — no raw CR/LF — and
`MOCKER_MAX_RESPONSE`; a violation is a SKIPPED firing counted in the
existing `frames_skipped:M`, exactly an oversize generated frame's twin.
CONSEQUENCE, recorded: P6b's guarantee "the same body at the same ordinal on
every connection" does NOT hold for a Lua tick — D4's carve-out extends
here. The call runs ON the connection's own loop goroutine (§30.8: the
handler loop is the only writer), so a slow function delays that
connection's frames and pings up to the 2 s timeout — per connection, never
process-wide. **VM cost is measured in-slice**: a benchmark at the 100 ms
floor (10 Hz × connections) of fresh `NewState`+open per firing; fresh VMs
stay unless the number forces a carve-out — pooling would violate
stateless-by-construction. **Preview runs a draft's tick Lua for the ≤ 50
laid-out frames on the honest clock, under an AGGREGATE 10 s budget**
(round 1 YELLOW: 50 × 2 s is 100 s otherwise) — past the budget the
remaining timeline is laid out labelled "not run". `maxBytesPerSec` is
labelled NOMINAL when `tick.lua` is present (a sample over what ran, not a
bound). Every successfully written Lua frame enters A14's existing outbound
recorder exactly as a generated frame — `off|first|all`, `frames_recorded`,
truncation semantics unchanged.

**2. `stream.onFrame` — Lua per inbound frame (ws only).** Present, it
REPLACES `reactive` and `echo` entirely — both must be absent (`400
on_frame_and_reactive`, `400 on_frame_and_echo`); refused on `sse` (`400
on_frame_on_sse`). Every inbound frame reaches `function(frame)` while the
connection is LIVE: a TEXT frame that parses as a JSON OBJECT arrives as a
table (the D3 body-decode rule, one shape everywhere), anything else as the
raw string. Return convention, verb-first multiple return: `return nil` —
no reply; `return "reply", data` — one text frame (table → JSON by the
host); `return "close", code, reason?` — the closing handshake, `code`
validated 1000 or 4000–4999 exactly as a reactive rule's `close` is.
**Close mechanics are the reactive rule's, unchanged** (round 1 RED: "the
reader returns close" must not read as a reader-side write): the reader
ENQUEUES a terminal close item outside the SEND_BUDGET; the WRITER loop
performs the handshake; after it the reader keeps DRAINING the peer's half
but STOPS calling `onFrame` — the P6d post-close discipline verbatim. A Lua
error or malformed return: the reply is dropped, counted in a NEW
`on_frame_errors:K` note token (`replies_dropped:K` stays budget-drops-only
— round 1 YELLOW: overloading it hides broken code behind a full budget),
and `onFrame` KEEPS being called for later frames. A valid reply enters the
SAME per-connection SEND_BUDGET byte bound a reactive reply does (§30.11's
`{"$gap": N}` marker unchanged). The call runs on the READER goroutine, so
reply ORDER follows frame order and a slow function blocks only that
connection's reads, up to the 2 s timeout.

Both hooks ride the stream document inside `custom_endpoints`' JSON column —
no migration; bundle v6 (D5) already covers the stream's shape. The MCP
writers and `preview_endpoint`'s stream schemas grow the two fields in place;
the contract count stays 70.

## DESIGN.md — on the owner's word only

v13 would add §35 «Функции эндпоинтов» recording D1–D10 as the owner's
decisions with his answers quoted. Per the hard rule the agent does not open
`DESIGN.md` until the owner says the word («внеси в правки Дизайн…» or
equivalent). Until then THIS document is the authority on why.

## Review round 1 — three Codex reviewers (luna, high), 2026-09-04

`review-1-sandbox.md`, `review-2-architecture.md`, `review-3-streams.md`
beside this file; prompts alongside. Verdicts: all three "do not cut as
written" — every RED is folded into the decisions above, every YELLOW that
changed wording is folded too. Findings verified against source before
adoption, three load-bearing ones by reading the library and the tree:

- gopher-lua `mathlib.go:186–205`: `math.random` → package-global
  `math/rand`, `math.randomseed` → global `rand.Seed` (D3 RNG override).
- gopher-lua `baselib.go:35–55`: `loadstring`/`module`/`newproxy` registered
  by `OpenBase` (D3 allowlist-only construction).
- `internal/checkpoints/capture.go:127` + `internal/bundle/bundle.go:143`:
  a scenario NEVER carries custom endpoints (D5's inherited asymmetry is
  real, not introduced).

Not adopted as-is: review-2's "4 graph deps violate §30.9" — resolved by the
tidy-first remeasure (expected to meet the bar literally; divergence
recorded if not). Review-1's GREEN findings (no bytecode ingress; metatables
cannot resurrect removed globals without a retained reference) are kept as
design facts the boundary test will pin.

## Estimate

The slice: `internal/luafn` (runner + allowlist sandbox + boundary test +
frozen-`_G` test), the variant field through overrides/customep/admin/MCP,
the serving branch in `internal/mockplane` with the function-vs-resource
precedence tests, the two stream hooks of D10, bundle v6 with round-trip
tests, the safety-tail tests (browser-executable media, CR/LF, caps,
classification), acceptance-style sign-in e2e (wrong password → 401, right →
JWT verifiable with the workspace key), a ws onFrame reply/close check, the
VM-cost benchmark, smoke check, the D9 set. A focused two days.
