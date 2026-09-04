# A18 — endpoint functions (Lua) — gate decisions

Date: 2026-09-04. Status: decided; reviewed in TWO rounds (both recorded near
the end of this file); the acceptance section §A is written and measured;
awaiting the owner's word to cut the slice.
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

The owner's own phrasing in D1 — "issue a token that expires in an hour and
remember it" — is satisfied by the TOKEN, not by state: a JWT carries its own
expiry and verifies without anything on the server remembering it. No session
store was promised and none is built. Said here because the two sentences sit
one section apart and a reader six months from now would otherwise wonder
which one lost (round 1 NOTE).

**Sandbox — built ALLOWLIST-ONLY, not by stripping** (round 1 RED: `OpenBase`
also registers `loadstring`, `module`, `newproxy`, `require`, `raw*` —
baselib.go:35–55; a strip-list of four names is not airtight). Construction:

1. `lua.NewState(lua.Options{SkipOpenLibs: true})` — NOTHING is preloaded.
2. Open exactly: `OpenBase`, `OpenString`, `OpenTable`, `OpenMath`, `OpenOs`.
3. Remove from `_G` BY NAME after opening: `load`, `loadstring`, `loadfile`,
   `dofile`, `print`, `module`, `require`, `newproxy`, `collectgarbage`
   (gopher-lua's implementation calls process-wide `runtime.GC()` — one
   function must not impose GC latency on unrelated work).
3a. `raw*` — `rawget`, `rawset`, `rawequal`, `rawlen` — STAY, deliberately,
   and this sentence exists because round 1's RED named five things
   `OpenBase` registers and the list above closes four. They bypass
   METATABLES and reach nothing outside the VM: a global this construction
   removed is gone from the table, not hidden behind a metatable, so
   `rawget(_G, "io")` returns the same nil an ordinary index does — which is
   round 1's own GREEN finding ("metatables cannot resurrect removed globals
   without a retained reference") read from the other side. The frozen-`_G`
   test of step 6 pins their PRESENCE, so this decision is observed rather
   than assumed, and a later slice that wants them gone changes one literal.
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

**Two header edges, decided rather than left silent** (round 1 MINOR, both
directions). INBOUND: `req.headers` lowercases the key and joins a repeated
header's values with `", "`, the form `net/http` itself round-trips — it does
NOT become an array, because unlike a query string a repeated header is
already defined by RFC 9110 as equivalent to that join, and one shape per
field is worth more here than symmetry with `req.query`. OUTBOUND: a
`string→string` table cannot express two `Set-Cookie` lines, and that is a
STATED limitation of this slice rather than an oversight — the sign-in shape
D1 describes needs one cookie or a token in the body. A function that needs
two same-named response headers is out of scope; the escape is a pinned
variant on another status, which carries a header map of its own.

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
  The family is looked up in THIS workspace's runtime `route_family`-keyed
  roster — the same lookup `ref` makes (`internal/mockplane/ref.go`), and the
  reason is the same one: `EntityStore.List` is not workspace-scoped, so
  anything else turning a family name into a `resource_id` would be one
  forgotten check away from serving another workspace's rows on a plane that
  is unauthenticated by design. Rows are read under the SERVING request's own
  base scope, scope values arrive RAW and are encoded by the HOST (never
  double-escaped by the function); an unconfirmed or unknown family → nil,
  "unknown_family". Read-only: there is no `mock.write` — the anonymous mock
  plane's own POST/DELETE verbs remain the only writers, and the `mock` table
  holds exactly `jwt`, `now`, `entities` and nothing else, pinned the way
  step 6 pins `_G`.
  **`scopeArray` is NOT the `ref` recipe's behaviour and the word "verbatim"
  is withdrawn** (round 1 MAJOR). `ref` passes the EMPTY ROUTE scope to
  `EntityStore.List` for every family — `CARVE-OUTS.md:574`, with the `P3c`
  entry at `:440`; `P3h` closed the BASE half only — so a `ref` at a nested
  family finds nothing, deliberately. Inheriting that would make `scopeArray`
  decorative: a nested family would return an empty array, not an error,
  forever. The owner's call, 2026-09-04: **implement real filtering.**
  `mock.entities(family, {"7", "5"})` reads the rows under that ancestor
  tuple through `resources.Repo.ListFiltered`, the tuple encoded by
  `resources.EncodeScope` — the ONE owner of that join, never a second
  `strings.Join` at this call site — and an arity that does not match the
  family's depth is `nil, "bad_scope"`. Omitting the argument keeps the
  request's own scope. This is NEW WORK, not a reuse, and it is named as such
  so the estimate carries it; it also leaves `ref`'s own carve-out exactly
  where it is, since nothing here changes what a recipe does.

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
- **Bundle v6 reads v5 AND NOTHING OLDER: `CurrentVersion = 6`,
  `minVersion = 5`** (`internal/bundle/bundle.go:50,60`, today 5 and 4). The
  field is additive, so a v5 document without functions imports unchanged; a
  v4 document is REFUSED BY NAME, and an OLD (v5) binary meeting a v6
  document refuses it by name too — the P6b precedent, no silent
  field-dropping in either direction. **This was round 1's open question and
  it is the owner's call, taken 2026-09-04**, against this document's own
  recommendation, which was to leave the floor at 4. What it costs is named
  rather than hidden: P7a set `minVersion = 4` deliberately because A16 had
  shipped the install wizard the day before, so a colleague's v4 checkpoint
  or export was plausible; after this slice such a document no longer
  imports, and the operator's route is to re-export from a build that still
  reads it. The invariant bought in exchange is the simple one — each version
  reads exactly the version before it — and it is what stops `minVersion`
  being re-argued at every bump.
  **The version literal lives in SIX SHAPES across the tree, and no single
  pattern reaches them.** This paragraph has been rewritten three times and
  each rewrite was short for a different reason, which is the reusable half:
  the first ran a correct command over a GUESSED SCOPE
  (`internal/bundle/*_test.go`) and used `NR` where a multi-file glob needs
  `FNR`; the second widened the scope to `cmd internal` and kept a PATTERN
  (`"mockerBundle": N`) that only matches the JSON-text form, missing a Go
  comparison, three prose strings, a JSON contract and two markdown guides. A
  command is an enumeration only when its scope AND its pattern cover the
  population. The population is what this returns, reviewed hit by hit:

  ```
  grep -rniI mockerbundle --exclude-dir=node_modules --exclude-dir=.git \
    --exclude-dir=generated .
  ```

  Run 2026-09-04, the six shapes and what each needs:

  1. **JSON text in a Go string** — `internal/bundle/bundle_test.go:187`
     (`TestDecode_rejectsBasePathDisagreement`), `:245` and `:272`
     (`TestValidate_acceptsResourcesAndStillRejectsNonNullEntities`), `:352`
     (`TestDecode_documentWithNoDecisionsKey`),
     `internal/mcp/tools_transfer_test.go:18`, `:33`, `:46`, `:69`. v4 as a
     convenient fixture; each literal moves to 5.
  2. **The promise test** — `internal/bundle/version_compat_test.go:17`
     (`TestDecode_readsV4`, whose `func` line is `:15`). **INVERTED, not
     renumbered**: it is P7a's own promise test, its doc comment argues the
     opposite of this decision ("A refusal here would strand that history with
     no migration path"), and bumping its literal to 5 leaves a green suite
     that has silently stopped testing anything about v4. It becomes a refusal
     test, keeping its fixture bytes and its comment rewritten to say which
     decision retired the promise.
  3. **A Go field comparison, which no JSON-text pattern reaches** —
     `internal/bundle/bundle_test.go:360-361` (`b.MockerBundle != 4` and its
     message) and `internal/admin/transfer_handlers_test.go:83-84`
     (`plain.MockerBundle != 5`, which moves to 6, not to 5 like the fixtures).
  4. **The "future version" test, which is not a v4 site at all and still
     breaks** — `internal/bundle/bundle_test.go:205`,
     `TestDecode_rejectsUnknownVersion`, feeds **6** as the version a build
     must refuse, and this slice makes 6 current. Its own doc comment records
     that P7a re-aimed it once for exactly this reason. It re-aims to **7**.
  5. **Prose that names the version to a reader** —
     `internal/mcp/tools_transfer.go:23` and `:37` (two tool descriptions) and
     `:106` (a `jsonschema` tag), all saying "mockerBundle v4"; `api/openapi.json`
     `:4030`, `:7827`, `:7969` (three descriptions saying "v4") and `:4043`
     (`WorkspaceExportDocument.mockerBundle`, `"const": 4`);
     `internal/guide/tools.md:42` and its source
     `skills/mocker/references/tools.md:42` ("mockerBundle v5; v4 still
     imports"). **Six of these are ALREADY STALE today, before this slice
     touches anything**: P7a moved `CurrentVersion` to 5 and left every "v4" in
     `tools_transfer.go` and `api/openapi.json` behind, and the `const: 4` is
     wrong against the current build. Nothing caught it — the contract test
     reads routes and `csrfToken`, never a schema; there is no runtime schema
     validator; and no test asserts on an MCP description string. This slice
     repairs them on the way past.
  6. **A golden fixture** — `internal/bundle/testdata/golden_bundle.json:1`,
     currently 5, moves to 6 with `CurrentVersion`.

  **One site is version-INSENSITIVE and is listed only so the grep comes back
  clean**: `internal/checkpoints/repo_test.go:1151` is
  `TestSnapshot_refusesABlobThatIsNotGzip`, which writes a raw non-gzip blob to
  assert `ErrCorruptSnapshot`; the failure fires at `gzip.NewReader`'s header
  check before any version is read, so the number is incidental. Its literal
  moves to 5 to satisfy the sweep and for no other reason.

  **Two sites need nothing**: `internal/admin/transfer_handlers_test.go:191`
  and `internal/bundle/bundle_test.go:644` feed v3, already refused and staying
  refused. And every mention in `CARVE-OUTS.md`, `DESIGN.md` and the comments
  at `internal/bundle/bundle.go:36` and `bundle_test.go:345` is a HISTORICAL
  statement about v3 and must NOT move — a sweep that "fixes" those has
  rewritten the record of what earlier slices decided.

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
- **The host helpers run under the SAME deadline, and it is threaded, not
  assumed** (round 1 MAJOR): `mock.entities` reads through the store and can
  therefore queue behind a checkpoint restore or a `reset-data` holding the
  single writer connection, and `mock.jwt` signs. Both take the invocation's
  `context.Context` — the one `SetContext` already carries — and a helper
  that returns because that context expired is the `503 function_timeout`
  path, not `500 function_failed`. Without this the 2 s budget bounds Lua
  bytecode and nothing else, which is the half `string.rep` above already
  says the check cannot reach.
- **No rate limit on a Lua auth check, accepted and recorded** (round 1
  MAJOR). The feature's own motivating example is a password check, it runs
  on the unauthenticated mock plane, and D2 makes it always-on on a shared
  contour as well as a laptop — while the ADMIN plane runs a two-bucket login
  limiter (A15) precisely because one shared password is guessable. A mock's
  Lua sign-in has no host-side brute-force protection of any kind, and adding
  one would be a policy this product does not have. It follows from the
  owner's overruling at the head of this document, and it goes into
  `CARVE-OUTS.md` in these words, beside the memory residual, rather than
  being left to be discovered.
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

## D8b — The three orderings `internal/customep`'s validator does not have

Round 1's external lens read the tree and found that three refusals D5 and
D10 require cannot fire where the validator checks things today. Each was
verified by reading the file. They are written here because an implementer
following D5/D10 alone would produce a build that fails this document's own
acceptance clauses while looking correct.

1. **`function_on_stream` needs its check BEFORE the response-map refusal.**
   `internal/customep/stream.go:336` (sse) and `:351` (ws) refuse
   `len(row.Responses) != 0` with "kind takes no responses" before anything
   inspects a variant — and a `function` lives inside a variant, inside
   `Responses`. As it stands D5's `400 function_on_stream` can never be the
   answer. The function-bearing-variant check runs FIRST; the generic
   response-map refusal keeps its wording for every other case.
2. **`tick_lua_and_schema` needs exclusivity checked BEFORE `schema` is
   required.** `internal/customep/stream.go:253` — inside `validateTick`,
   which begins at `:246` and runs the interval floor and the event-name
   check first — is an unconditional `if len(t.Schema) == 0` →
   "stream.tick.schema is required", so a Lua-only tick is refused as
   schema-missing and a `lua`+`schema` tick never reaches D10's own refusal.
   Order: exclusivity first, then require and validate `schema` only when
   `lua` is absent. The two checks that precede `:253` are unaffected and
   keep their place.
3. **`onFrame` has no site at all.** `internal/customep/stream.go:152-163`
   refuses `Reactive` and `Echo` on a non-ws kind and knows nothing of a
   third inbound-side field, so D10's `on_frame_on_sse`,
   `on_frame_and_reactive` and `on_frame_and_echo` need both a site and an
   order: the kind check first (`on_frame_on_sse`), then the two conflicts,
   beside the existing Reactive/Echo refusals rather than after them.

## D9 — Documentation at slice end

`docs/USER-GUIDE.md` (the operator's copy, Russian), `skills/mocker/` all
four references + `internal/guide` resync (`make guide-sync`), the `design`
guide topic untouched, `CLAUDE.md` Architecture + `HISTORY.md` — the standing
slice-end set.

**`CARVE-OUTS.md` carries TWELVE named items.** The count moved three times —
six, then eight, then nine, then twelve — and the shape of the moves is the
finding rather than the number: each fix closed the case it was handed and not
the question behind it, which is round 3's own lesson stated about itself. The
class is: **every decision in this document that gives something up — a
guarantee withdrawn, a capability refused, a compatibility broken, a residual
accepted — is an entry.**

Each item below carries the literal tag `[GIVES-UP]` and names the section
that DECIDES it, so the list is countable by command and each entry is one
pointer to walk rather than a sweep to redo. The command is ANCHORED to the
numbered lines below — `grep -cE '^[0-9]+\. .\[GIVES-UP\]'` — because the
unanchored form was written first and returned 13 against a list of twelve: it
counted this very sentence. A guard that counts its own prose is the shape this
document has now produced twice, and it was found by running the command rather
than by reading it.

1. `[GIVES-UP]` **Determinism (D4).** One seed and one spec no longer imply
   byte-identical bodies on a function-bearing endpoint.
2. `[GIVES-UP]` **Memory uncapped (D6).** The context check fires between VM
   instructions; a single native call allocates before any check.
3. `[GIVES-UP]` **No rate limit on a Lua auth check (D6).** On an
   unauthenticated plane, always on, while the admin plane runs a two-bucket
   limiter.
4. `[GIVES-UP]` **The `coroutine` library is never opened (D3).** A thread
   could launder an infinite loop past a single `SetContext`.
5. `[GIVES-UP]` **The timeout is a fixed 2 s const (D6).** No env knob; a
   deployment that needs another number has none.
6. `[GIVES-UP]` **`os.date` is pinned to UTC (D3).** The process timezone
   cannot reach a function's output.
7. `[GIVES-UP]` **The RNG is per-VM host entropy (D3).** `math.randomseed` is
   removed and nothing substitutes the workspace seed.
8. `[GIVES-UP]` **`mock.entities` has no write-time check (D3).** A family
   name is a runtime Lua string, so it can never be checked the way P7a checks
   a `$ref` ("never STORED dangling"), and a function broken by a later
   decline, a spec rebind or a config-only rollback has no host-side signal.
9. `[GIVES-UP]` **The v4 refusal (D5).** `minVersion` moves to 5, so a bundle
   a colleague exported or a checkpoint they took before this slice no longer
   imports; their route is to re-export from a build that still reads it. Not
   a new KIND of entry — this file already carries one per bundle bump,
   `CARVE-OUTS.md:919` (P6b) and `:1402` (P7a), each stating its own cost.
10. `[GIVES-UP]` **No opt-out (D2).** No `MOCKER_FUNCTIONS`, no gate: every
    deployment executes operator-authored Lua, a shared contour as much as a
    laptop. An operator who wants a mocker that runs no Lua has no switch and
    must not deploy this build.
11. `[GIVES-UP]` **No two same-named response headers (D3).** `headers` is a
    `string→string` table, so two `Set-Cookie` lines cannot be emitted — in
    the sign-in shape D1's own example describes. The escape is a pinned
    variant on another status.
12. `[GIVES-UP]` **A function's output is never validated against its own
    declared schema (D8).** The schema is exported and the drift report stays
    shape-only, so a body that has drifted from the contract it publishes is
    reported by nothing. This is the price of D5's "the function REPLACES
    response assembly", and it is invisible from the contract side.

**The class was re-derived INDEPENDENTLY in round 4** — a reviewer walked
D1–D10, D8b and §A for withdrawn guarantees, refused capabilities, broken
compatibilities and accepted residuals WITHOUT reading this list first, and
converged on exactly these twelve, excluding four borderline candidates by
name (the `io`/`package`/`debug` refusal, folded into the allowlist mechanism
rather than argued separately; `mock.entities` being read-only, which is the
intended surface rather than a cost; the inbound repeated-header join, which
D3 explicitly does not call a limitation; and D9's own closing paragraph on
the Lua contract's missing version marker, which is an obligation on future
slices). That derivation is the evidence the count is complete, and it is
recorded because a thirteenth can only ever be found by another one.

**The two matrices D7 promises are a DELIVERABLE of this section**, not a
sentence in passing (round 1 MINOR): spec operation versus custom endpoint,
where the function branch sits against the 406 gate and against media
negotiation, written into `skills/mocker/` and into `docs/USER-GUIDE.md`.

**The Lua CONTRACT changes by the same refuse-by-name discipline the bundle
uses** (round 1 NOTE). The `req`/`out` shape and D3's allowlist carry no
version marker of their own, and a function travels byte-for-byte through
export, import, fork and every checkpoint — so a later tightening of the
allowlist breaks a persisted function silently at runtime, because an
undefined global reads `nil` in Lua and D8's write-time compile check cannot
see it. Any future slice that narrows either must refuse by name at some
door, the way a bundle version does; this one records the obligation.

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
stateless-by-construction. **What "forces" means is named, because a hedge
with no number is a decision nobody can take** (round 1 MINOR): the benchmark
reports per-firing overhead against the 100 ms floor, there is no automatic
gate, and the OWNER reads the raw number and decides. A slice that pools VMs
on its own reading of a benchmark has changed D3's statelessness guarantee
without anybody saying so. **Preview runs a draft's tick Lua for the ≤ 50
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
   the recorded 27 564 281 bytes is recorded. *Fails if the build needs cgo*,
   and *fails if the delta is not taken at all* — the same defeat clause 5
   states for the `-race` number, for the same reason: an unrecorded
   measurement leaves D1's §30.9 admission resting on a figure from a
   throwaway module.
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
    per-workspace and this plane is unauthenticated by design — and *fails if
    the rows differ from that endpoint's own output in count, order or field
    values*, which is the headline claim and had no defeat of its own until
    round 3. And on a NESTED
    family of depth 2, `mock.entities(family, {"7", "5"})` returns the rows under
    that ancestor tuple and not under the serving request's own, while a tuple of
    the wrong arity returns `nil, "bad_scope"`. *Fails if the two-argument call
    returns an empty array rather than rows* — which is what inheriting `ref`'s
    empty-route-scope behaviour (`CARVE-OUTS.md:574`) produces, silently, and it
    is the reason D3 withdrew the word "verbatim". And *fails if a wrong-arity
    tuple returns rows, or is silently padded or truncated to the family's
    depth, instead of `nil, "bad_scope"`* — a refusal that degrades into a
    best-effort match is how a scope becomes decorative a second time.
12. The `mock` table's key set is exactly `jwt`, `now`, `entities` — pinned
    against a frozen literal, the way clause 6 pins the surviving `_G` set, not
    sampled and not grepped for one name. *Fails if any other key is reachable* —
    a writer named `mock.upsert`, or a write folded into `mock.entities`, passes
    a grep for a `mock.write`-shaped name and is still the wrong implementation.
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
    asserts both statuses serve — and *fails if the pair is ACCEPTED*, the
    defeat clause 18 already states for the stream case: a variant that stores
    both and then silently runs one of them is the failure this refusal exists
    to prevent.
17. `when[]` on a function variant is accepted and selects the variant; the
    function runs only when its variant is selected. *Fails if a function-bearing
    row runs its function for a request that selected another variant.*
18. A variant-level `function` on a row of `kind: "sse"` or `kind: "ws"` is
    refused `400 function_on_stream` — that refusal and not another. *Fails if it
    is accepted and then silently never fires*, and *fails if the answer is
    `kind %q takes no responses`*, which is what
    `internal/customep/stream.go:336` gives today because it refuses a non-empty
    `Responses` map before anything inspects a variant: D8b(1) states the order
    this clause observes.
19. The `function` string survives byte-identical through each round trip its
    carrier already makes: checkpoint capture → `rollback`, `export_workspace` →
    `import_workspace`, `fork_workspace`, and a CAS conflict document. One test
    per trip. *Fails if any trip drops or re-encodes it.*
20. A scenario snapshot carries a function set on a SPEC OPERATION's override and
    does NOT carry one set on a custom endpoint. *Fails if the asymmetry is
    "fixed" here* — it is DESIGN §12's, inherited (`capture.go:127`,
    `bundle.go:143`, both read and confirmed 2026-09-04), and changing it would
    be a scenario-layer decision this slice never took.
21. `bundle.CurrentVersion` is 6 and `minVersion` is 5. A v5 document imports
    unchanged; a **v4 document is refused BY NAME**, with the version in the
    message; a v6 document presented to a binary built at `a6ae6ee` is refused by
    name rather than field-dropped. *Fails if an old binary silently ignores the
    field*, and *fails if a v4 document still imports* — the owner took that call
    on 2026-09-04 against this document's own recommendation (D5), so an
    implementation that quietly leaves the floor at 4 passes a clause written the
    other way and ships the opposite decision — and *fails if a v5 document is
    refused, or if any field of it is altered on import*, which is the half of
    the clause that says the bump is additive and had no defeat of its own.
    The v4 fixture is the one P7a's own v4 test already uses; D5 enumerates
    every site that feeds one.

### A.5 — Execution guards (D6)

22. A function whose body is `while true do end` answers `503 function_timeout`
    within the 2 s budget plus measurement noise, and the traffic row carries the
    note `function_timeout`. *Fails if the request outlives the budget*, and
    *fails if the status is 500* — the two classes are separate on purpose.
23. A function returning `Content-Type: text/html` answers `500 function_failed`
    and no `text/html` byte reaches the wire; so does one returning
    `image/svg+xml`, and so does one returning a value `mime.ParseMediaType`
    cannot parse. Three cases, because the table at
    `internal/httpx/mediatype.go:20-24` holds more than one entry and an
    unparseable value is refused by a different branch. *Fails if a body reaches
    the client under any media type `httpx.BrowserExecutableMediaType` refuses*,
    and the three cases are what stops an implementation that special-cases the
    literal `text/html` from passing — nothing in the tree pins that function to
    a single call site, unlike an isolated library.
24. A function returning a header value containing CR or LF, an empty header
    name, a value past the sane-length bound D6 names, or a NON-STRING value (a
    Lua number or table) answers `500 function_failed` and writes no header —
    four cases for the three properties D6 states plus the empty name. *Fails if
    the value is written after being sanitized* — the plane refuses such headers,
    it does not repair them — and *fails if a non-string value is coerced*, which
    is the same silent-coercion refusal D3's `Out` block already makes for the
    status and the body.
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
    `onFrame` on `kind: "sse"` → `400 on_frame_on_sse`. And one more, which is
    the ordering rather than the refusal: a tick carrying `lua` and NO `schema`
    is ACCEPTED. *Fails if one refusal is clamped or silently ignored*, and
    *fails if a Lua-only tick is refused as `stream.tick.schema is required`* —
    which is what `internal/customep/stream.go:253` gives today, because it
    requires `schema` before it could ever check exclusivity; D8b(2) and D8b(3)
    state the orders this clause observes.
40. A Lua tick returning a string containing CR or LF, or a body over
    `MOCKER_MAX_RESPONSE`, SKIPS that firing, leaves the connection open, and the
    skip is counted in the existing `frames_skipped:M` note — the size half
    observed with `MOCKER_MAX_RESPONSE` at a NON-DEFAULT value, exactly as clause
    25 observes its own twin. *Fails if the frame is written and breaks SSE
    framing*, *fails if the connection closes*, *fails if the observation uses
    the default* — a hard-coded 4 MiB and a config-reading implementation emit
    identical bytes there — and *fails if the skip is not counted, or is
    counted twice*: an implementation that checks the CR/LF and the size
    conditions independently, with no early return, increments
    `frames_skipped` twice for one frame. (Clause 41 defeats a different thing
    — a nil return counted as a skip AND an error — and this clause used to
    cite it as though the two were the same claim.)
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
44. A benchmark records the per-firing cost of `NewState` + sandbox open + one
    call against the 100 ms tick floor. The number is RECORDED, not thresholded,
    and D10 names who reads it: the owner, with no automatic gate. *Fails if no
    number is taken* — D10 makes fresh-VM-per-firing conditional on it, and a
    missing measurement leaves the pooling question unanswerable rather than
    answered — and *fails if the slice pools VMs on its own reading of the
    number*, which changes D3's statelessness guarantee with nobody saying so.
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
    `docs/USER-GUIDE.md`, all four `skills/mocker/` references, `CLAUDE.md` and
    `HISTORY.md` carry the slice. *Fails if the guide's embedded copy and its
    source disagree.*
47a. `CARVE-OUTS.md` carries all TWELVE items D9 enumerates — the determinism
    carve-out, the memory residual, the absent rate limit on a Lua auth check,
    the `coroutine` refusal, the const timeout, the UTC pin, the RNG override,
    the `mock.entities`-versus-`$ref` asymmetry, the v4 refusal, the absent
    opt-out, the single-valued response headers, and the unvalidated function
    output — each as its own entry naming what it gives up, and each MATCHED BY
    NAME rather than counted. *Fails if any one of the twelve is absent*, which
    is a different check from the one this clause first carried: "fewer than
    eight entries" is a floor, and a floor passes over twelve entries of which
    one is the wrong twelve. *And fails if
    `grep -cE '^[0-9]+\. .\[GIVES-UP\]'` over this document does not return the
    same number as the count of items D9 lists and the count of matching entries
    in `CARVE-OUTS.md`* — three numbers, one
    command each, which is what round 4 replaced the previous wording with: that
    wording asked a reader to re-derive the class, and a defeat a reader must
    re-derive is a judgement, not an observation, which is the one thing §D of
    the gate's own checklist refuses. The class question itself — is there a
    thirteenth — is irreducible to a command, and what settles it is a fresh
    independent derivation; round 4 ran one and it converged, which is recorded
    in D9 rather than asserted here.
47b. The two matrices D7 promises — spec operation versus custom endpoint, where
    the function branch sits against the 406 gate and against media negotiation —
    are present in `skills/mocker/` and in `docs/USER-GUIDE.md`. *Fails if the
    word "matrix" appears in this document's D7 and in no shipped guide*, which
    is where round 1 found it.
48. The run reports every SKIP its suites print. *Fails if a skip is printed and
    not read* — one shipped in this tree already, a relative path that did not
    resolve from the test's own working directory.

### A.10 — What round 1 found uncovered

Ten clauses, numbered from 49 so that no number above moves. The citations that
would break run in two directions and neither is a D-section — a grep for
`clause N` and `#A.n(m)` across D1–D10 and D8b returns nothing: §A's own clauses
18 and 39 cite `D8b(1)`–`D8b(3)`, and the clauses below cite clauses 6, 22, 24,
25 and 34 by number. Renumbering to keep each group in ascending order would
silently re-aim every one of those. The group a clause belongs to is named in
the clause instead.

49. **(D3, the `req` block.)** A function on a route with a `{}` segment,
    called with a repeated query key, a mixed-case request header, the SAME
    header name sent TWICE with different values, and a body that is not JSON,
    sees: `req.pathParams` holding the segment's value,
    `req.query` holding an ARRAY for the repeated key, `req.headers` keyed in
    lower case with a repeated header's values joined by `", "`, and `req.body`
    as the RAW STRING. The same function called with a JSON body sees a table.
    *Fails if any of the five is shaped otherwise* — nothing downstream refuses a
    runner that hands Lua a differently shaped table, and until round 1 no clause
    observed the input contract at all.
50. **(D3, the `Out` block.)** Three returns, three answers: a non-number status
    → `500 function_failed`; a boolean body → `500 function_failed`, not coerced
    to `"true"`; `return 200, nil` → a 200 with an EMPTY body. *Fails if any is
    silently coerced* — D3 says "not a silent coercion" and A.5 tested only the
    guard paths.
51. **(D3, stateless by construction.)** A function that sets a global runs; a
    second, unrelated invocation of the same function reads that global and gets
    `nil`. And the runner creates a fresh `*lua.LState` per invocation rather
    than checking one out of a pool. *Fails if the global survives*, and *fails
    if the second half is not observed* — D10 refuses VM pooling on this
    guarantee's account, so an unobserved guarantee is load-bearing twice.
52. **(D3, `mock.now`.)** `mock.now()` is within a second of the host clock and
    `mock.now(60)` is exactly sixty greater than `mock.now()` taken in the same
    invocation. *Fails if the offset is ignored*, and *fails if the clock is the
    workspace seed's* — D4 puts the real clock out of the determinism guarantee
    on purpose.
53. **(D6, the host helpers' deadline.)** A `mock.entities` call made while a
    checkpoint restore holds the single writer connection ends the invocation
    with `503 function_timeout` at the 2 s budget, not later and not as
    `500 function_failed`. *Fails if the request outlives the budget* — clause 22
    times out pure Lua bytecode only, and D6's own `string.rep` note says the
    context check does not reach a single native call.
54. **(D6, a header on the success path.)** A function returning
    `{["X-Mock-Case"] = "signed-in"}` alongside a 200 puts that header on the
    wire, with that value. *Fails if function-set headers are dropped* — clause
    24 observes only the refusals, so an implementation that writes no
    function-set header at all passes every header clause.
55. **(D8, D10 — the MCP input schemas.)** The published input schema of
    `set_operation_variant` declares `function`, and the stream writer's declares
    `tick.lua` and `onFrame`. *Fails if a field reaches the Go types and not the
    tool schema* — clause 34 pins the tool COUNT at 63, which does not move when
    a schema silently lacks a field, and D8 makes an agent through MCP the
    PRIMARY authoring path in the owner's own words, so this is the one gap that
    blocks the intended user while every other clause passes.
56. **(D8, the drift report.)** A workspace whose only distinguishing feature is
    a function-bearing operation reports `hasDrift: false` through
    `GET /api/workspaces/{id}/drift`. *Fails if a function is reported as drift*
    — D8 keeps the report shape-only, and nothing else observes that.
57. **(D10, the A14 recorder.)** A Lua tick connection under
    `MOCKER_STREAM_TRAFFIC_FRAMES=all` produces the same `frames_recorded`
    accounting and the same truncation behaviour a generated-tick connection
    produces under the same budgets. *Fails if Lua frames bypass the recorder*,
    and *fails if they are recorded under a different token* — D10 claims the
    semantics are "unchanged", and an unobserved claim of sameness is where a
    second code path hides.
59. **(D5, the v4 refusal's blast radius.)** After the change,
    `TestDecode_readsV4` asserts a REFUSAL naming version 4 and its doc comment
    says which decision retired P7a's promise; `TestDecode_rejectsUnknownVersion`
    feeds 7; `internal/admin/transfer_handlers_test.go:83` expects 6; the
    fixture sites of D5 shape 1 carry 5; `internal/bundle/testdata/`
    `golden_bundle.json` carries 6; and the prose sites of D5 shape 5 name the
    current version rather than v4. The observation is the OUTPUT of
    `grep -rniI mockerbundle --exclude-dir=node_modules --exclude-dir=.git
    --exclude-dir=generated .`, read hit by hit against D5's six shapes — not
    an empty result, because the population is not empty and never will be.
    *Fails if `TestDecode_readsV4` merely has its fixture literal bumped* — the
    suite is then green and the one regression check about v4 tests nothing.
    *Fails if any hit outside D5's historical set still names a version below
    the current one.* *And fails if the check is run as a pattern over one
    shape*, which is how this slice's own enumeration came back short twice: a
    Go field comparison, three prose strings, a JSON `const` and two markdown
    guides all live in this population and none of them matches
    `"mockerBundle": N`.
58. **(D3, the sandbox's deliberate residue.)** `rawget`, `rawset`, `rawequal`
    and `rawlen` are PRESENT in the frozen `_G` allowlist of clause 6. *Fails if
    the allowlist literal is silent about them* — round 1's RED named five
    things `OpenBase` registers and four were removed, so their presence has to
    be a recorded decision (D3 step 3a) rather than a leftover. And
    `rawget(_G, "load")` from inside a function returns `nil`. *Fails if it
    returns a function* — `load` is a name `OpenBase` DID register and step 3
    removed, which is the only case that discriminates: `io` is never opened at
    all, so a `rawget` for it returns nil under every implementation, correct or
    not.

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
design facts the FROZEN-`_G` ALLOWLIST TEST of D3 step 6 will pin, observed by
clauses 6 and 58 — not the boundary test, which is
`internal/luafn/boundary_test.go` and checks single-importer isolation and
nothing else (round 1 finding 23, half-closed there and finished in round 2).

## Review round 2 — the acceptance section's first read, 2026-09-04

Workspace `mocker-a18-functions` (`wf`), round 1 of that workspace and the
document's second: four lenses against one frozen snapshot — `criterion`
(sonnet, §A only), `closure` (sonnet, whether round 1's REDs are really
closed), `other` (sonnet, deliberately unaimed) and an external
`gpt-5.6-luna` at effort `high` reading the document against the repository.
**34 findings, 3 blockers, 8 of them NOTE rows asserting no failure.**

What each lens was for, and what it actually bought:

- The EXTERNAL lens found what only a tree-reader could, and all three were
  verified by hand before adoption: the three orderings in
  `internal/customep/stream.go` that make D5's and D10's refusals
  unreachable where the validator checks things today. They are now D8b, and
  they were invisible to three reviewers of round 1, who read the design.
- `criterion` found the two holes nothing downstream catches: D3's request
  table and D3's statelessness had no clause at all, and the second is
  load-bearing twice because D10 refuses VM pooling on its account.
- `closure` found that round 1's own quoted RED named FIVE things `OpenBase`
  registers and this document closed four, `raw*` disappearing in silence —
  now D3 step 3a, a decision rather than a leftover.
- `other`, whose brief named no target, found that "resolution is the `ref`
  recipe's, verbatim" makes `scopeArray` inert, because `ref` passes the
  empty ROUTE scope for every family (`CARVE-OUTS.md:574`).

Two questions went to the owner because no reviewer owns them, and both are
recorded above as his: bundle `minVersion` moves to 5 (v4 refused by name,
against this document's own recommendation), and `mock.entities` gets REAL
scope filtering rather than `ref`'s behaviour.

## Estimate

The slice: `internal/luafn` (runner + allowlist sandbox + boundary test +
frozen-`_G` test), the variant field through overrides/customep/admin/MCP,
the serving branch in `internal/mockplane` with the function-vs-resource
precedence tests, the two stream hooks of D10, bundle v6 with round-trip
tests, the safety-tail tests (browser-executable media, CR/LF, caps,
classification), acceptance-style sign-in e2e (wrong password → 401, right →
JWT verifiable with the workspace key), a ws onFrame reply/close check, the
VM-cost benchmark, smoke check, the D9 set. A focused two days.

**What round 2 added to that estimate**, named rather than absorbed: real
`scopeArray` filtering in `mock.entities` (`resources.Repo.ListFiltered` plus
an arity refusal — the owner's call, and NOT a reuse of `ref`); threading the
invocation context through both host helpers; the three validator reorderings
of D8b; and ten more acceptance clauses (§A.10), of which 49, 51 and 55 carry
their own fixtures. Half a day on top of the two, most of it the scope
filtering.
