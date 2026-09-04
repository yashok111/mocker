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

## D4 — Determinism: honest, out of the guarantee

Written as a section 2026-09-04: the decision was always here, as a paragraph
inside D3, while D10 and the review notes cite it as `(D4)` — a citation that
resolved to nothing. The text below is unchanged; only its heading is new.

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

## A — Acceptance

Written 2026-09-04, after D1–D10 and before a line of code. Two rules govern
this section and nothing else does.

**Every number in A.0 was produced by RUNNING the command beside it**, on the
untouched tree at `a6ae6ee`, at the moment this section was written. A clause
below compares against the recorded number and never against the word "green":
what a codebase does today is not what its own documentation says it does, and a
clause resting on a documented figure can go red against a correct
implementation.

**Every clause states an OBSERVATION and names its defeat as `Fails if …`** —
the implementation it exists to reject, written as a condition somebody can go
and observe. A clause with no named defeat has no floor. Each was also read the
other way, against a CORRECT implementation: a criterion that goes red on
correct code stalls the run at the one step where changing production code is
forbidden, which is the worse direction.

### A.0 — Baseline on the untouched tree (`a6ae6ee`, 2026-09-04)

| bar | command | recorded |
|---|---|---|
| `make test` | `make test` | 35 packages ok, 0 FAIL, 3 with no test files, 0 SKIP; 77.6 s wall, maxrss 2.04 GiB |
| `make lint` | `make lint` | 0 issues, 60.5 s wall, maxrss 1.89 GiB |
| `make ui-test` | `make ui-test` | 32 test files, 378 tests, all passed |
| `make ui-lint` | `make ui-lint` | clean over 100 files |
| `make smoke` | `make smoke` | 358 PASS, 0 FAIL, 0 SKIP, 213.4 s wall |
| contract operations | `jq '[.paths[] \| keys[] \| select(IN("get","post","put","patch","delete","head","options"))] \| length' api/openapi.json` | 70 |
| contract paths | `jq '.paths \| length' api/openapi.json` | 51 |
| coverage population | `web/src/api/coverage.test.ts:208` | `toHaveLength(70)` |
| MCP tools | `internal/mcp/mcp_resources_test.go:344` | 63 |
| `MOCKER_*` variables | `grep -rhoE '"MOCKER_[A-Z_]+"' internal/config/*.go \| sort -u \| wc -l` | 36 |
| migrations | `ls internal/store/migrations/` | 8, newest `0008_custom_endpoints_operation.sql` |
| bundle version | `internal/bundle/bundle.go:50,60` | `CurrentVersion = 5`, `minVersion = 4` |
| `//nolint` | `grep -rho '//nolint' cmd internal \| wc -l` | 36 |
| goleak harnesses | `grep -rl testleak.VerifyTestMain --include=main_test.go cmd internal \| wc -l` | 35 |
| module graph | `go list -m all \| wc -l` | 52 (8 direct) |
| binary size | `CGO_ENABLED=0 go build -o /tmp/m ./cmd/mocker && stat -c%s /tmp/m` | 27 564 281 bytes (26.29 MiB) |

Every cell above was produced by running its own command on this box on
2026-09-04, at `a6ae6ee` plus this section. No cell is derived from a document,
and none carries a placeholder: a clause resting on a placeholder has no
baseline, which is the defect this table exists to prevent.

### A.1 — The library is admitted on the §30.9 terms (D1)

1. `go mod tidy` runs FIRST, then `go list -m all` is re-taken; the delta over
   the recorded 52 is written into `HISTORY.md` verbatim, whatever it is.
   *Fails if the tidied graph carries a module beyond `github.com/yuin/gopher-lua`
   and the divergence is not written down beside the `go list -deps` evidence
   that the REPL subpackage is unlinked.*
2. `CGO_ENABLED=0 go build ./cmd/...` exits 0 and the binary-size delta over
   the recorded 27 564 281 bytes is recorded. *Fails if the build needs cgo.*
3. `internal/luafn/boundary_test.go` fails the build on a second importer,
   OBSERVED by adding an import of `github.com/yuin/gopher-lua` to a file outside
   `internal/luafn`, watching `go test ./internal/luafn/` go red, and reverting.
   *Fails if that mutation leaves the test green* — which is what a boundary test
   that walks only its own package does.
4. `internal/luafn/main_test.go` exists and the goleak count is 36 (was 35).
   *Fails if a VM's goroutine outlives its package's tests and nothing says so.*
5. `make test` is no worse than the recorded 35/0/3/0 and its wall time under
   `-race` is recorded beside the baseline. *Fails if the number is not taken at
   all* — D1 defers the `-race` delta to the slice on purpose, so not taking it
   leaves D1 unclosed.

### A.2 — The sandbox is what D3 says it is (D2, D3)

6. A test pins the SURVIVING `_G` key set against a frozen literal allowlist,
   and the same test pins `os`'s surviving key set (`time`, `clock`, `date`
   only). *Fails if the assertion is over a SUBSET — "these names are absent" —
   rather than over the whole set*: a gopher-lua upgrade that adds a global must
   go red here, and an absence list cannot see a name nobody thought of.
7. From inside a function, each of `load`, `loadstring`, `loadfile`, `dofile`,
   `print`, `module`, `require`, `newproxy`, `collectgarbage`, `io`, `package`,
   `debug`, `coroutine`, `string.dump`, `math.randomseed`, `os.execute`,
   `os.exit`, `os.getenv`, `os.remove`, `os.rename`, `os.setlocale`,
   `os.tmpname` evaluates to `nil`. The list in the test is the list in this
   clause — enumerated, not sampled. *Fails if one name is reachable*, and the
   defeat it exists to catch is `OpenBase` re-registering `loadstring`/`module`/
   `newproxy` (gopher-lua baselib.go:35–55), which a strip-list of four names
   misses.
8. A Lua call to `math.random` does NOT advance Go's package-global
   `math/rand`: seed the global to a fixed value, read `rand.Int63()`, run a
   function that draws from `math.random`, read `rand.Int63()` again, and compare
   against the same pair taken with no function run. *Fails if the two differ* —
   which is exactly what gopher-lua's own `math.random` does (mathlib.go:186–205),
   and it is the observation a "seed per VM" implementation cannot pass.
9. `os.date("%Y-%m-%dT%H:%M:%S")` inside a function returns UTC while the
   process runs under `TZ=Asia/Tokyo`. *Fails if the value follows the process
   timezone* — the test sets a NON-DEFAULT `TZ`, because under this box's own UTC
   a pinned implementation and a process-following one emit identical bytes.
10. `mock.jwt` on a workspace whose `settings.auth` carries `alg: none`, or no
    key, returns `nil, "auth_not_configured"`. On a configured workspace the
    returned token VERIFIES with the workspace's own key — decoded and checked,
    never measured for length. *Fails if an unsigned token is returned in either
    case*, and *fails if the signing key is reachable from Lua*: a function that
    walks `mock`'s own keys returns a set that does not contain it.
11. `mock.entities(family)` returns exactly the rows
    `GET /api/workspaces/{id}/resources/{family}/entities` returns for the same
    family under the serving request's own base scope; an unconfirmed or unknown
    family returns `nil, "unknown_family"`. Scope values are passed RAW and the
    host encodes them. *Fails if a scope value is escaped twice*, and *fails if a
    family belonging to another workspace resolves* — the roster lookup is
    per-workspace and this plane is unauthenticated by design.
12. There is no writer: a grep over `internal/luafn` for a `mock.write`-shaped
    name prints nothing, and the grep is named in the finding. *Fails if a
    function can create or delete an entity row.*
13. `MOCKER_FUNCTIONS` appears nowhere in `internal/config` or `cmd`, and the
    `MOCKER_*` count stays at 36. *Fails if the feature ships behind a flag* —
    D2 is «Всегда вкл», and a flag would make every clause here conditional on a
    setting no clause names.

### A.3 — Determinism, honestly bounded (D4)

14. `TestGoldenP1bBodyHashes` and `TestP1cAcceptance_NoRecipesMatchesGolden` are
    green and unchanged, and the golden corpus contains no function-bearing
    endpoint. *Fails if a function enters the corpus* — the corpus IS the
    seed-plus-spec guarantee, and a non-deterministic member destroys it
    silently rather than loudly.
15. A negative test pins the boundary from both sides: two requests to a
    function-bearing endpoint under one seed MAY differ, and two requests to a
    function-FREE endpoint under the same seed are byte-identical. *Fails if the
    second half is not asserted* — without it the clause passes on an
    implementation that made the whole plane non-deterministic.

### A.4 — The variant field and everything it rides (D5)

16. A variant carrying `function` together with any of `body`, `bodyRef`,
    `recipes`, `schemaPatch` is refused `400 function_and_body` on BOTH writers
    (the operation override and the custom endpoint), and the refusal names the
    conflicting field. *Fails if the refusal is at the ROW grain* — a
    function-200 with a pinned-401 sibling on one row is legal, and the test
    asserts both statuses serve.
17. `when[]` on a function variant is accepted and selects the variant; the
    function runs only when its variant is selected. *Fails if a function-bearing
    row runs its function for a request that selected another variant.*
18. A variant-level `function` on a row of `kind: "sse"` or `kind: "ws"` is
    refused `400 function_on_stream`. *Fails if it is accepted and then silently
    never fires.*
19. The `function` string survives byte-identical through each round trip its
    carrier already makes: checkpoint capture → `rollback`, `export_workspace` →
    `import_workspace`, `fork_workspace`, and a CAS conflict document. One test
    per trip. *Fails if any trip drops or re-encodes it.*
20. A scenario snapshot carries a function set on a SPEC OPERATION's override and
    does NOT carry one set on a custom endpoint. *Fails if the asymmetry is
    "fixed" here* — it is DESIGN §12's, inherited (`capture.go:127`,
    `bundle.go:143`, both read and confirmed 2026-09-04), and changing it would
    be a scenario-layer decision this slice never took.
21. `bundle.CurrentVersion` is 6; a v5 document imports unchanged; a v6 document
    presented to a binary built at `a6ae6ee` is refused BY NAME rather than
    field-dropped. *Fails if an old binary silently ignores the field.*
    **The `minVersion` half is OPEN — see (i) below.**

### A.5 — Execution guards (D6)

22. A function whose body is `while true do end` answers `503 function_timeout`
    within the 2 s budget plus measurement noise, and the traffic row carries the
    note `function_timeout`. *Fails if the request outlives the budget*, and
    *fails if the status is 500* — the two classes are separate on purpose.
23. A function returning `Content-Type: text/html` answers `500 function_failed`
    and no `text/html` byte reaches the wire. *Fails if a body reaches the client
    under any media type `httpx.BrowserExecutableMediaType` refuses* — the same
    rule both planes already apply on write.
24. A function returning a header value containing CR or LF, or an empty header
    name, answers `500 function_failed` and writes no header. *Fails if the value
    is written after being sanitized* — the plane refuses such headers, it does
    not repair them.
25. A function returning a body over `MOCKER_MAX_RESPONSE` answers
    `500 function_too_large` with NO partial body, observed with
    `MOCKER_MAX_RESPONSE` set to a NON-DEFAULT value (the default is `4mb`).
    *Fails if the status line was already committed when the cap was found*, and
    *fails if the observation uses the default* — a hard-coded 4 MiB and a
    config-reading implementation emit identical bytes there.
26. A Lua runtime error answers `500 function_failed`; the traffic note carries
    at most 200 bytes of the error's FIRST line and no Go stack. Observed with an
    error message deliberately longer than 200 bytes that contains a token-shaped
    string. *Fails if the note carries the whole message*, and *fails if the
    observation uses a message shorter than the cap* — then the cap is never
    exercised.
27. A client that disconnects mid-function produces neither a `function_failed`
    nor a `function_timeout` classification. *Fails if a cancelled request is
    recorded as a server error.*
28. `CARVE-OUTS.md` carries, in its own words, that function memory is UNCAPPED
    and why: the context check fires between VM instructions, so a single native
    call such as `string.rep("x", 1e9)` allocates before any check. *Fails if the
    residual lives only in this gate document* — a gate document stops existing
    when its directory does, which is exactly why this one was committed.

### A.6 — Serving order (D7)

29. Each of the four session-layer directives is observed against a
    function-bearing operation, one assertion each: a forced status answers
    without the function running (the traffic note lacks `function`); `fail_next`
    consumes and answers before it; a pause parks the request BEFORE the VM is
    created; a delay delays the run. *Fails if any one of the four runs the
    function first* — the branch sits where `resourceBranch` sits
    (`internal/mockplane/respond.go:207`), after all four and after the 406
    gate.
30. On one 2xx operation carrying BOTH a function variant and a confirmed
    resource family, the FUNCTION serves; with the function variant removed and
    nothing else changed, the RESOURCE serves. Both directions asserted. *Fails
    if only the first direction is asserted* — that half passes on an
    implementation that disabled the resource branch outright.
31. `TestAssembleResponseIsTheOnlySeam` (`internal/mockplane/seam_test.go:37`) is
    green with its `wantCallers` literal unchanged: the function branch is a
    SIBLING that produces the bytes `assembleResponse` would have, not a fourth
    caller of it. *Fails if that literal grows.*
32. Operation preview and custom-endpoint preview run a DRAFT's function through
    the same runner, and a failure lands in `PreviewResult.Notes` rather than in a
    status. *Fails if a failing draft function makes the preview route answer
    non-200.*
33. Traffic records a function-served request normally, with the note token
    `function`; the auth-check request's recording stays suppressed. *Fails if a
    function-served request writes no traffic row.*

### A.7 — Surface (D8)

34. Contract operations stay at 70 and `coverage.test.ts:208` stays
    `toHaveLength(70)`; MCP tools stay at 63; migrations stay at 8. *Fails if any
    of the three moves* — D8 is "no new routes, no new tools", and a moved count
    is the cheapest possible signal that something else was built.
35. `api/openapi.json` carries `function` in the create and update requests, the
    shared Variant view, the conflict payload and the endpoint conflict details,
    and `tick.lua`/`onFrame` in the stream shapes; the contract test
    (`internal/admin/openapi_contract_test.go`) is green. *Fails if the field
    reaches the wire without reaching the contract* — the contract has drifted in
    SCHEMAS twice before with no bar noticing, and both times a screen found it.
36. `make ui-gen`, then `make ui-test` and `make ui-lint`, are no worse than the
    recorded 32/378 and clean-over-100. *Fails if the regenerated client does not
    compile* — the local generated client has been stale in both directions
    before.
37. Unparseable Lua at write time is refused `400 bad_function` carrying the
    parser's own words. *Fails if the source is stored and the parse deferred to
    the first request* — this plane always answers, so a deferred parse is a 500
    nobody asked for.
38. The document `export_openapi` returns for a workspace whose endpoint carries
    a function contains no byte of that function's source, checked by grepping the
    exported document for a distinctive literal placed inside the Lua; the finding
    names that grep. *Fails if the source leaks into the OpenAPI document.*

### A.8 — Streams (D10)

39. Four refusals, four assertions, each BY NAME: `tick.lua` with `tick.schema`
    → `400 tick_lua_and_schema`; `onFrame` with `reactive` →
    `400 on_frame_and_reactive`; `onFrame` with `echo` → `400 on_frame_and_echo`;
    `onFrame` on `kind: "sse"` → `400 on_frame_on_sse`. *Fails if one is clamped
    or silently ignored.*
40. A Lua tick returning a string containing CR or LF, or a body over
    `MOCKER_MAX_RESPONSE`, SKIPS that firing, leaves the connection open, and the
    skip is counted in the existing `frames_skipped:M` note. *Fails if the frame
    is written and breaks SSE framing*, and *fails if the connection closes.*
41. A Lua tick returning `nil` skips the firing with the connection open and
    nothing counted as an error. *Fails if a nil return is counted as both a skip
    and an error* — they are different outcomes.
42. `onFrame` returning `"reply", data` produces exactly one text frame;
    returning `"close", code` performs the closing handshake FROM THE WRITER
    LOOP, after which the reader keeps draining the peer's half while `onFrame`
    is no longer called. *Fails if the reader writes the close itself* — P6d's
    discipline, verbatim.
43. A Lua error inside `onFrame` drops that reply, counts it in a NEW
    `on_frame_errors:K` note token, leaves `replies_dropped:K` unchanged, and
    `onFrame` is still called for the NEXT inbound frame. *Fails if the error is
    counted in `replies_dropped`* — that token means budget drops, and
    overloading it hides broken code behind a full budget.
44. A benchmark records the cost of `NewState` + sandbox open + one call at the
    100 ms tick floor. The number is RECORDED, not thresholded. *Fails if no
    number is taken* — D10 makes fresh-VM-per-firing conditional on it, and a
    missing measurement leaves the pooling question unanswerable rather than
    answered.
45. Stream preview runs a draft's tick Lua under an AGGREGATE 10 s budget; past
    it the remaining frames are laid out and LABELLED "not run", and
    `maxBytesPerSec` is labelled NOMINAL whenever `tick.lua` is present. *Fails
    if the preview can run 50 × 2 s*, and *fails if the label is absent* — an
    unlabelled nominal number is read as a bound.

### A.9 — Through the deployed artifact, and the documents

46. `make smoke` is no worse than the recorded 358 PASS / 0 FAIL / 0 SKIP and
    gains a sign-in section
    through the CONTAINER: wrong password → 401, right password → a JWT that
    verifies with the workspace's key; plus one timeout observation and one
    `function_on_stream` refusal. *Fails if the feature is proven only by the test
    runner* — "a green `go test` ≠ the phase is done" has caught a dead feature in
    this tree twice, and both times the wiring nobody tested was
    `cmd/mocker/main.go`.
47. `make guide-sync` leaves no drift and `internal/guide`'s own test is green;
    `docs/USER-GUIDE.md`, all four `skills/mocker/` references, `CLAUDE.md`,
    `HISTORY.md` and `CARVE-OUTS.md` carry the slice. *Fails if the guide's
    embedded copy and its source disagree.*
48. The run reports every SKIP its suites print. *Fails if a skip is printed and
    not read* — one shipped in this tree already, a relative path that did not
    resolve from the test's own working directory.

**(i) OPEN, for the owner — the one question A.4(21) defers.** D5 says "bundle
v6 reads v5" and, in the same bullet, "the decode chain already accepts older".
Today `minVersion = 4` (`bundle.go:60`), and P7a put it there deliberately: A16
shipped an installer the day before, so a colleague's v4 checkpoint is plausible
and must keep importing. Moving `minVersion` to 5 breaks exactly that, and the
field is additive, so nothing forces the move. **Recommendation:
`CurrentVersion = 6` and `minVersion` STAYS 4** — v6 reads v5 and v4 alike. Not
decided here.

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
