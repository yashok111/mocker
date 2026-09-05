# Endpoint functions: Lua (A18) — agent context

Moved out of `CLAUDE.md` on 2026-09-05, text unchanged and in its original
order; `CLAUDE.md` holds the index of these files and what stayed. Other
documents and code comments cite this text as CLAUDE.md "Architecture" (or
the section named below). "Above"/"below" inside refer to that original
order: the paragraph named is either in this file or in the sibling the index
points to.

**`A18` (2026-09-04/05) is endpoint functions: Lua that PRODUCES a
response instead of having one assembled — DESIGN has no section for it,
and its authority is `docs/A18-endpoint-functions.md`, a gate document
committed INTO this repository (the seventeen earlier gate workspaces
were outside it and are gone).** The owner asked for it and overruled the
threat model in his own words — «хочу такую фичу для локального
инструмента на все эти угрозы пофигу», a Russian string quoted as data —
so an unauthenticated plane executing operator-supplied code is the
decision, not an oversight.

`internal/luafn` is the tree's THIRD isolated library
(`github.com/yuin/gopher-lua` v1.1.2, `boundary_test.go` fails the build
on a second importer, the shape `internal/wsmock` and `internal/yamlx`
already have). Measured in-tree rather than in the throwaway module D1
used: `go list -m all` is 56 (up 4) while `go.mod` gained exactly ONE
line and zero `chzyer` packages link into `cmd/mocker` — §30.9's "zero
transitive modules" holds for what is LINKED and not for the graph
`go list -m all` walks, and that divergence is written down rather than
glossed; the binary grows **+1 007 664 bytes (+0.96 MiB)**, measured
worktree-to-worktree because a worktree embeds no SPA and a main-tree
number cannot be subtracted from it; `-race` shows no regression, since
an interpreter over its own opcode array gives the race runtime no
C-translated code to instrument.

**The sandbox is built ALLOWLIST-ONLY**: `lua.NewState(Options{SkipOpenLibs:
true})`, then exactly `base`/`string`/`table`/`math`/`os` opened by name,
then twenty names removed from `_G` and `os`, and a test pins the
surviving `_G` key set against a FROZEN literal — a gopher-lua upgrade
that adds a global fails the build, not a customer. `io`, `package`,
`debug` and `coroutine` are never opened (a thread could launder an
infinite loop past a single `SetContext`). `math.random` is REPLACED by a
host closure over a per-VM `*rand.Rand` from `crypto/rand` and
`math.randomseed` removed — gopher-lua's own `math.random` calls Go's
PACKAGE-GLOBAL `math/rand`, so "seed per VM" was unimplementable as first
written, and `rand.Seed` is a no-op on Go 1.27 besides. `os.date` is
pinned to UTC by wrapping the library's own `osDate` with a forced `!`,
never by reimplementing strftime. A fresh VM per invocation, closed when
it returns: no global survives two calls, and a test observes it.

Five things about the SERVING branch cannot be guessed from the code.
**`Function` is a field of ONE VARIANT, not of the row** — exclusivity is
per variant against `body`, `bodyEncoding`, `bodyRef`, `recipes`,
`schemaPatch` and `mediaType`, so a function-200 beside a pinned-401 is
legal and is the sign-in shape; `when[]` is allowed, because selection is
unchanged. **The branch is a SIBLING of `assembleResponse`, never a
fourth caller**: `TestAssembleResponseIsTheOnlySeam`'s `wantCallers`
literal is unchanged and its comment says why, and it opens no `gen.Body`
site because it generates from no schema. **A function BEATS a confirmed
resource** on the same 2xx operation and the branch therefore sits before
`resourceBranch` (after route_off, the session layer, the pause, the
delay and the 406 gate); a test asserts both directions, because the
first half alone passes against an implementation that disabled the
resource branch. **On a custom endpoint the branch sits BEFORE the 406
gate** — the one place the two matrices differ, and the reason is that a
custom endpoint's only declared media type belongs to a PINNED variant,
so a function variant has nothing to negotiate against until it has run;
both matrices are written out in `skills/mocker/references/functions.md`
and in `docs/USER-GUIDE.md` §4. **The safety tail is stricter in one
direction and looser in another**: a bad header or a browser-executable
`Content-Type` REFUSES the whole response with `500 function_failed`
where the same predicate merely DROPS a spec-declared header, and
`unsafeResponseHeaderName` is NOT applied, because D3 names one
`Set-Cookie` as the sign-in shape this feature is for — what stays
refused is `Content-Length` and `Transfer-Encoding`, where a second value
is corruption and not disagreement. Every refusal is decided BEFORE
`WriteHeader`. Four traffic tokens on ONE field (`function`,
`function_timeout`, `function_failed`, `function_too_large`), mutually
exclusive by construction; a client that walked away is deliberately
unclassified.

`mock` held exactly three helpers through A18 (four since A19, below) and a test pins the key set the way
`_G`'s is pinned. `mock.jwt` signs through `recipes.MintJWT` — the same
signer the `jwt` recipe uses — and refuses `auth_not_configured` on
`alg: "none"` or no key. `mock.now` is the real clock. **`mock.entities`
does REAL `scopeArray` filtering**, the owner's own call against this
document's recommendation to inherit `ref`'s behaviour (which passes the
EMPTY scope for every family, so a nested one would find nothing
forever): an explicit tuple is encoded through `resources.EncodeScope`,
an omitted one is the PREFIX of the request's own outer values at the
family's depth — the same prefix rule `resourceBranch`'s anchor walk
applies with `outer[:i]` — and any arity that cannot name a scope is
`bad_scope`. The family resolves through THIS request's runtime roster
and nothing else, `ref`'s own reason: `EntityStore.List` is not
workspace-scoped. Both helpers take the INVOCATION's context, threaded
from `Run` through `installMock`, because otherwise the two-second budget
would bound Lua bytecode and not the store read behind `mock.entities`.

**Bundle v6 reads v5 and NOTHING older** (`CurrentVersion = 6`,
`minVersion = 5`) — the owner's call, REVERSING P7a's own `minVersion = 4`
one slice earlier, and the cost is named rather than hidden: a colleague's
v4 checkpoint or export no longer imports and their route is to re-export
from a build that still reads it. `TestDecode_readsV4` was INVERTED rather
than renumbered — its fixture bytes are untouched and it is now the
refusal test, because bumping its literal would have left a green suite
that had stopped testing anything about v4.

**Streams get two hooks of their own and the variant-level `function` is
refused on them by name** (`400 function_on_stream`; a stream is not a
request/response). `tick.lua` produces a frame's body, exclusive with
`tick.schema`; `stream.onFrame` answers one inbound frame on `ws` and
REPLACES `reactive` and `echo` entirely. Both needed an ORDER as much as
a check, which is why `D8b` is its own section: the tick's `schema is
required` refusal was UNCONDITIONAL, so a Lua-only tick was refused as
schema-missing and a `lua`+`schema` tick never met its own refusal; and
`onFrame` had no site at all. A Lua tick's body passes the frame checks a
generated one passes by construction — no raw CR/LF, `MOCKER_MAX_RESPONSE`
— as one SKIPPED firing counted ONCE, while `return nil` is a third
outcome counted as nothing. `onFrame` runs on the READER goroutine (reply
order follows frame order) and writes nothing: a reply is enqueued under
the same send budget, a close is a terminal item the WRITER loop performs,
after which the reader keeps draining and the hook stops being called. A
Lua error or a verb the contract lacks drops that reply into a NEW
`on_frame_errors:K` token — never `replies_dropped`, which means the
budget was full — and the hook keeps being called. Stream preview runs a
draft's Lua for real with a NIL host under an AGGREGATE 10 s budget (fifty
firings at the per-call timeout is a hundred seconds): past it a frame
keeps its PLACE and comes back `notRun`, and `maxBytesPerSec` comes back
`nominalRate`, because with a Lua producer it is a sample and not a bound.

**The per-firing cost was MEASURED and is recorded, not thresholded**
(D10.1 makes fresh-VM-per-firing conditional on it, there is no automatic
gate, and the owner reads the number): `BenchmarkLuaTickFiring` is
**142 127 ns/op, 185 351 B/op, 612 allocs/op** — 0.14% of one connection's
100 ms tick budget, about 28% of one core for 200 connections all at the
floor. Fresh VMs stay; a slice that pools them on its own reading has
changed D3's statelessness with nobody saying so.

No new route and no new tool: contract stays at 70 operations, tools at
63, migrations at 8, `EXEMPT` at 10. What DID move: `Variant.function` on
the contract (inherited by the update request, the endpoint view and both
conflict payloads, which all `$ref` it) plus its own flat field on
`CreateEndpointRequest`; `StreamTick.lua` with `schema` out of `required`;
`StreamDefinition.onFrame`; the preview's `notRun`/`nominalRate`; and a
SEVENTH `get_guide` topic, `functions`, with its own
`skills/mocker/references/functions.md`. `//nolint` 36 → 39. The fourteen
`[GIVES-UP]` items are in `CARVE-OUTS.md` — two of them found by BUILDING
rather than by a gate round: a custom endpoint's HTTP draft cannot be
previewed at all (the route refuses `kind: "http"` and answers a type with
no `Notes` field), and `mock.entities` reads through `EntityStore.List`
rather than the `ListFiltered` D3 names, which satisfies every one of D3's
own criteria because a full tuple is an exact key.


**The review after the gate (2026-09-05) found fourteen things the four
rounds had not, and each is fixed with a test.** Three readers — the
session's own pass, `/code-review` at high effort, `vcodex` (`gpt-5.6-luna`)
— over `64f591f..39f4fc9`; the fixes are the six `fix(a18)` commits after
`7617c4b`, one finding group each. What a later slice must know, because the
code alone does not say why:

- **The converter is guarded by the tables on the CURRENT PATH, not a visited
  set, and by depth 64** (`internal/luafn/convert.go`). `t.self = t` used to
  recurse in Go until `fatal error: stack overflow` — unrecoverable, outside
  the Lua deadline and every `recover`, the whole process gone. A path set
  and not a visited set because `{a = u, b = u}` is a legal shape that
  encodes as two copies. `LTable.Append` is a no-op for `LNil` in gopher-lua,
  so arrays are built with `RawSetInt` and a JSON `null` keeps its index.
- **An untyped string body is `text/plain; charset=utf-8` and every function
  response carries `nosniff`** (`function.go`). Written with no type, a
  string body was SNIFFED by `net/http` — `return 200, "<script>…"` went to
  the wire as `text/html` — and the clause-23 test was green because
  `httptest.ResponseRecorder` does not sniff after an explicit `WriteHeader`
  while a real server does. The test for it runs `httptest.NewServer`; any
  future test of a Content-Type on the wire must too. The header SET is
  capped (64 fields, 64 KiB together) beside the 8 KiB per value, and a
  declared type stays on an empty body.
- **`MaxCloseReasonBytes` lives in `luafn` and customep re-exports it**: a
  hook's `close` reason over 123 bytes made `coder/websocket` refuse to write
  the frame and the peer saw a 1006 while traffic recorded the hook's code.
  `luafn.ValidateHook` is what customep's two hook sites call now; it existed
  unused.
- **`Tick.Schema` carries `omitempty` and `validateTick` reads a literal
  `null` beside `lua` as absent.** A Lua-only tick was stored as
  `"schema":null`, decoded as a four-byte `RawMessage`, and every
  re-validation of the STORED row (`Update`, a rollback, a scenario apply, a
  bundle import through `ReplaceAllTx`) refused it as two producers. The
  null-as-absent rule stays for rows and checkpoints the A18 build wrote.
- **Stream hosts carry the connection's route tuple** (`serveStream` and
  `serveWS` take `outer`, from `routeOuterValues` on the matched row). With
  nil, `mock.entities` on a nested family from a hook was `bad_scope` with no
  way around it — a hook has no request table. A failing `tick.lua` firing in
  the PREVIEW is a skipped frame, as it is on a live connection, not the
  route's 500.
- **`ResponseShape` carries `function` verbatim** — the one variant field the
  MCP projection returns in full. §C's compaction rule keeps a pinned body
  out because a body is data of any size; a function is the contract the
  agent itself authored, and the guide's read-then-write-whole-document flow
  DELETED it when the read hid it. `create_endpoint`'s wire body carries
  `function` too; it was declared on the input and dropped.
- **A stored snapshot this build cannot read is `409 snapshot_unreadable`**
  on GET, activate and rollback, with the codec's own words — and
  `scenarios.SetActive` decodes before it activates (`decodeSnapshot`, shared
  with the scan). D5's "refused BY NAME" held for import only: a v4 scenario
  activated with 200 and the mock plane served the workspace layer under it.
- **The seven named refusal codes exist** (`internal/admin/refusal_codes.go`):
  `bad_function`, `function_and_body`, `function_on_stream`,
  `tick_lua_and_schema`, `on_frame_on_sse`, `on_frame_and_reactive`,
  `on_frame_and_echo`. Each refusal wraps its sentinel ALONGSIDE
  `ErrInvalidRow` (two in `overrides`, five in `customep`; a hook that does
  not compile is `overrides.ErrBadFunction`), so every existing
  `errors.Is(ErrInvalidRow)` keeps its 400 and `refusalCode` reads the name
  at the five answering sites. The documents had promised the codes since the
  gate; the server answered `bad_request`, and the smoke could only grep for
  words. It asserts the code now.

**`A19` (2026-09-05) is the fourth helper and the writer: `mock.generate` and
`mock.entities.create/update/delete`.** The owner chose both from a ranked
assessment («давай сделаем 1 и 2», a Russian string quoted as data) and
answered two design questions: `generate` takes a `#/` pointer OR an inline
table, and the writers hang off `mock.entities` as a CALLABLE table rather
than as new top-level keys — `mock.entities(family)` still reads through
`__call`, and `entitiesTableKeys` pins the three sub-keys the way
`mockTableKeys` pins the four. What a later slice must know:

- **`Generate` is the tree's THIRD `gen.Body` call site and the seam test
  names it** (`wantBodySites`). It is not a fourth `assembleResponse` caller
  on purpose: a function asks for a BODY, not a response — no envelope, no
  recipes, no negotiation, no byte-cap refusal of its own (the function's
  whole return meets `MOCKER_MAX_RESPONSE` at the writer). `gen.Body` takes
  `Request.PatchedSchema` as the root VERBATIM, so the host chases a root
  `$ref` and checks every nested one against the runtime's resolver (now a
  field on `runtime`) BEFORE the call — `resolveSchema`/`checkRefs`, the
  refusing sibling of `buildCustomInline`'s `chaseRootRef`/`sanitizeRefs`:
  a stored inline schema must keep SERVING when its `$ref` stops resolving
  and so empties the node with a warning; a function is being asked a
  question and gets `unresolved_ref: <pointer>`.
- **The seed tuple is the request's plus a per-call ordinal** (`luaHost.req`,
  a `gen.Request` of method, canonical path and path params; on a stream,
  the row's; `__generate: n` mixed into a COPY of the params on each call,
  the way `newTickGenerator` mixes `__tick`). The ordinal is the review's
  one real bug: the host is built once per stream connection, so a
  `tick.lua` calling `generate` emitted the same frame for the life of the
  connection. The n-th call of one request or connection is deterministic;
  consecutive calls draw consecutive values. It is a body seeded like a
  generated response, not a copy of the detail route's own 200 (no
  `ListFamily`/`IDParam`, so no id write-back from the URL — the function
  does that). A root that rolls `null` is `nil, "null"`, so the
  `if not t then` guard sees a reason and not an empty error. Query is
  left out: a function reads `req.query` itself.
- **The writers are the mock plane's own POST/DELETE through the same
  store and caps**, plus `Repo.Patch` for update — `EntityStore` gained
  `Patch` (its fifth method; the mock plane's HTTP verbs do not call it, a
  mock has no PATCH on an entity). `Patch` reads, merges and writes inside
  ONE write transaction: the first draft was Get → merge → `Set` at the
  host, and the review (two of three readers) named it a read-modify-write
  outside the writer — a lost update between two concurrent requests, and a
  row deleted between the Get and the Set RESURRECTED by Set's upsert.
  `not_found` when there is none, nothing written; a non-canonical key is
  `bad_key` at the host BEFORE the store (the store would say not found).
  `WriteForm` is NOT consulted and the D6.2 ancestor walk is NOT run — both
  recorded in `CARVE-OUTS.md` (the admin entity route's own precedent). The
  family/scope resolution is one function (`resolveFamily`) shared by the
  read and the three writers; the store's refusals map to words in one
  place (`storeErr`); every argument refusal is a returned word
  (`bad_family`, `bad_scope`, `bad_data`, `bad_key`), never a raised Lua
  error — `l.CheckString` raised, which was 500 where its siblings were
  nil-plus-reason.
- **A19 reverses A18 D3's "no writer"** and the guide's "there is no
  `mock.write`" paragraph is gone; the entry in `CARVE-OUTS.md` records
  what is still absent (a field cannot be removed through update; no
  validation against `entity_schema`, as the plane's own POST has none; no
  traffic token for a Lua write; `generate` is deterministic per schema
  within a request). `goToLua` learned Go `int`/`int64` because a host
  built in Go may hand it one; decoded JSON never does.
