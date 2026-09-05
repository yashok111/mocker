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

`mock` holds exactly three helpers and a test pins the key set the way
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

