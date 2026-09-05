# Endpoint functions (Lua)

An endpoint can PRODUCE its response by running code instead of having one
assembled from a schema, recipes and a pinned body. The language is Lua
(gopher-lua), the code is one function body you store as a string, and it runs
in a fresh sandboxed VM per request that is thrown away afterwards.

Reach for it when the answer depends on the request in a way a `when[]`
condition cannot express: check a password and branch, mint a token that
expires in an hour, compute a total, answer differently for the third page.
For anything a recipe or a `when[]` already does, use those — they are
deterministic and a function is not.

**It is always on.** There is no flag and no way to turn it off. Every
deployment of this build executes the Lua an operator or agent stores.

---

## 1. The contract

The stored string IS the function body. No `function(req)`, no `end`:

```lua
if req.body.password == "hunter2" then
  return 200, { token = mock.jwt({ sub = 42, role = "admin" }) }
end
return 401, { error = "bad credentials" }
```

**In** — one argument, `req`:

| field | type | note |
|---|---|---|
| `req.method` | string | `"GET"` |
| `req.path` | string | the canonical matched path, e.g. `/users/{}` |
| `req.pathParams` | table | `{ id = "7" }` — the route's own `{}` segments |
| `req.query` | table | always strings; a repeated key is an ARRAY |
| `req.headers` | table | keys LOWERCASED; a repeated header is joined with `", "` |
| `req.body` | table \| string | the decoded JSON when it parses, the raw string when it does not |

**Out** — `return status, body, headers`:

- `status` — a number, 100..599. Anything else fails the request.
- `body` — a table (JSON-encoded for you, served as `application/json`), a
  string (raw bytes, served as `text/plain; charset=utf-8` unless you set
  `Content-Type` yourself), or nil (empty body). A boolean or a number is a
  FAILURE, not a coercion. A table that contains itself, or nests deeper than
  64 levels, fails the request instead of encoding.
- `headers` — an optional table of string→string. Omit it entirely, or return
  nil for it. Every function response also carries
  `X-Content-Type-Options: nosniff`.

Returning nothing at all fails the request: a function must decide a status.

## 2. Helpers — the `mock` table

`mock` holds exactly four things and nothing else.

- **`mock.jwt(claims)` → token, or nil + err.** Signs with the WORKSPACE's own
  `settings.auth`, the same signer the `jwt` recipe uses. Answers
  `nil, "auth_not_configured"` when the workspace carries `alg: "none"` or no
  signing key — an unsigned token pretending to be signed is worse than an
  error. The signing key never enters the Lua state.
- **`mock.now(offsetSec?)` → unix seconds.** The REAL clock, plus the offset.
- **`mock.entities(family[, scopeArray])` → rows, or nil + err.** Reads a
  CONFIRMED resource family's rows as an array of tables.
  - `family` is the route family exactly as `list_workspace_resources` reports
    it, e.g. `"/subjects"`.
  - `scopeArray` is a NESTED family's ancestor tuple, raw values in order:
    `mock.entities("/orgs/{}/teams", {"7"})`. The host encodes it; do not
    escape anything yourself.
  - Omit `scopeArray` and the request's own outer path values are used, taken
    as a prefix at the family's depth. In a stream hook (`tick.lua`,
    `onFrame`) those are the CONNECTION's own URL values.
  - `nil, "unknown_family"` — never suggested, declined, or no spec bound.
  - `nil, "bad_scope"` — the tuple's length does not match the family's depth,
    or the request is too shallow to imply one.
  - **`mock.entities.create(family, data[, scopeArray])` → row, or nil + err.**
    The same write the mock plane's `POST X` performs: the family's own id
    field and strategy assign the id, the same row and byte caps apply
    (`nil, "entity_limit"`), and the stored row comes back with its id.
  - **`mock.entities.update(family, key, patch[, scopeArray])` → row, or
    nil + err.** A SHALLOW merge of `patch` over the stored row: keys in
    `patch` win, every other key stays. `key` is the row's id as a string or
    a whole number. `nil, "not_found"` when there is no such row; a key that
    is not the canonical form of the family's id type is `nil, "bad_key"`.
    A key cannot be removed this way — a Lua `nil` in a table is an absent
    key, not a value; delete and create the row to drop a field.
  - **`mock.entities.delete(family, key[, scopeArray])` → true | false.**
    `true` when a row went, `false` when there was none.
  - Every writer takes the family and scope exactly as the reader does —
    `nil, "bad_family"` for a family that is not a string, `nil, "bad_scope"`
    for a scope that is not a table of strings/numbers — and
    `nil, "bad_data"` when `data`/`patch` is not a table. `update` is atomic:
    the merge is read and written inside one transaction, so two concurrent
    updates of one row both land. A write does NOT check what the mock
    plane's own POST checks about the ROUTE: the family's `writeForm` and,
    for a nested family, that a live ancestor row anchors the scope — a
    function has said what it means, and a row under an unanchored scope is
    simply unreachable until an ancestor exists. Writes are data,
    not configuration: they bump no revision, appear in no checkpoint's
    config, and are reset by `reset-data` like any other row.
- **`mock.generate(schema)` → value, or nil + err.** A body generated the way
  a generated response is, handed back as a table to edit. Two forms:
  - a string that is a JSON pointer into the bound spec —
    `mock.generate("#/components/schemas/User")`;
  - a table that is an inline JSON Schema —
    `mock.generate({type = "object", properties = {n = {type = "integer"}}})`.
    `$ref`s inside it resolve into the bound spec.
  - `nil, "bad_schema"` for anything else (a bare word, a number, an array).
  - `nil, "unresolved_ref: <pointer>"` when a `$ref`, root or nested, names
    nothing in the bound spec, or no spec is bound. Never a silently empty
    object: a function asked a question and gets the answer.
  - The value is deterministic per (workspace seed, request) exactly as a
    generated response is; the schema is not in the seed, so two calls in one
    function draw from the same stream and two calls over the same schema
    return the same table. Deadline-shaped fields (`exp`, `*_expires_at`,
    `*_valid_until`) are anchored to the clock per call and are the one
    exception. A function that wants two different users asks for an array
    of two.
  - Cost: each call may produce up to `MOCKER_MAX_RESPONSE` bytes and is not
    bounded by the 2 s budget while it runs (a native call, like
    `string.rep`). Do not call it in a loop.

## 3. The sandbox

Open: `base`, `string`, `table`, `math`, `os` (only `time`, `clock`, `date`).
`os.date` is pinned to UTC, so output does not change with the machine.

Never opened: `io`, `package`, `debug`, `coroutine`.

Removed by name: `load`, `loadstring`, `loadfile`, `dofile`, `print`,
`module`, `require`, `newproxy`, `collectgarbage`, `getfenv`, `setfenv`,
`math.randomseed`, `string.dump`, `os.execute`, `os.exit`, `os.getenv`,
`os.remove`, `os.rename`, `os.setlocale`, `os.setenv`, `os.tmpname`.

`rawget`/`rawset`/`rawequal` stay: they bypass metatables and reach nothing
outside the VM.

`math.random` works and draws from a per-VM generator seeded from real
entropy. It is NOT reproducible and there is no way to seed it.

**Stateless by construction.** A fresh VM per invocation, closed when it
returns. No global, no closure, no variable survives between two calls. The
only thing a function can see that outlives the request is `mock.entities`.

## 4. Guards, and how each one is answered

| what happened | the client gets | traffic note |
|---|---|---|
| ran, returned a response | that status and body | `function` |
| still running after 2 s | `503 function_timeout` | `function_timeout` |
| Lua error, bad return shape, bad header, browser-executable Content-Type | `500 function_failed` | `function_failed` |
| body over `MOCKER_MAX_RESPONSE` | `500 function_too_large` | `function_too_large` |
| client disconnected mid-run | nothing | neither |

The 2 s budget is a wall clock and it is not adjustable. It bounds the VM
between instructions — one enormous native call (`string.rep("x", 1e9)`) can
allocate before any check fires, and memory is UNCAPPED. Do not write one.

Headers are REFUSED, never repaired: a CR or LF in a value, an empty or
non-token name, a value over 8 KiB, more than 64 headers or more than 64 KiB
of names and values together, or a non-string value fails the whole
response. `Content-Length` and `Transfer-Encoding` may not be set. A
`Content-Type` the browser would execute (`text/html`, `image/svg+xml`, an
unparseable value) fails the response; a string body with NO `Content-Type`
is `text/plain`, never sniffed. A `Content-Type` you declare stays on an
empty body too. ONE `Set-Cookie` is allowed and is the sign-in shape this
feature is for; two are not expressible.

The error's first line, capped at 200 bytes, reaches both the traffic note and
the 500's own message — so `curl` shows you what broke.

## 5. Where it attaches, and what it replaces

`function` is a field of ONE VARIANT — one status — not of the row. Per
variant it is exclusive with `body`, `bodyEncoding`, `bodyRef`, `recipes`,
`schemaPatch` and `mediaType`; a variant carrying two producers is refused by
name, so there is no precedence to remember. `when[]` is allowed: selection is
unchanged and the function runs only when its variant is selected.

Other statuses on the same row are untouched — a function-200 with a
pinned-401 beside it is the ordinary sign-in shape.

When it runs, it REPLACES response assembly for that variant: recipes,
`schemaPatch`, the envelope and `bodyRef` do not run. Compose from inside the
function instead.

**A function beats a confirmed resource** on the same 2xx operation. The
resource branch takes the operation only when no function variant applies.

The source is COMPILED when you store it, so a syntax error is a `400` that
carries the parser's own line and offending token — never a 500 on the first
request.

## 6. Where the branch sits, per plane

Two matrices, because a spec operation and a custom endpoint differ in one
place. Read top to bottom; the function runs at the row marked ★.

**Spec operation** (`PUT .../operations/{opKey}`):

| order | step |
|---|---|
| 1 | `routeOff` → the workspace 404 |
| 2 | session layer: a forced status answers, `fail_next` consumes, a pause parks, a delay delays |
| 3 | variant selection (`when[]`, `activeStatus`, the spec's default) |
| 4 | the 406 `Accept` gate, against the SPEC's declared media type |
| 5 ★ | **the function** — instead of assembly, and BEFORE the resource branch |
| 6 | a confirmed resource's takeover |
| 7 | assembly: recipes, `schemaPatch`, `ref`, the envelope, the byte cap |

**Custom endpoint** (`POST .../endpoints`):

| order | step |
|---|---|
| 1 | `routeOff` → the workspace 404 |
| 2 | session layer, exactly as above |
| 3 | status selection (`when[]`, `activeStatus`) |
| 4 | a stream row branches (`kind: "sse"` / `"ws"`), unless a status was forced |
| 5 ★ | **the function** — BEFORE the 406 gate |
| 6 | the 406 `Accept` gate, against a PINNED variant's media type |
| 7 | pinned body / inline schema / empty |

The difference at 5–6 is not an accident: a spec operation negotiates against
the type its DOCUMENT declares, which exists before anything runs, while a
custom endpoint's only declared type belongs to a pinned variant — and a
function variant is not pinned, so there is nothing to negotiate against until
the function has said what it produced.

## 7. Streams get two hooks instead

A variant-level `function` is REFUSED on `kind: "sse"` and `kind: "ws"`
(`400 function_on_stream`): a stream is not a request/response. Streams have
their own two, inside the stream document.

### `tick.lua` — the tick's producer (sse and ws)

Exclusive with `tick.schema` by name — exactly one of the two, and a document
carrying both is `400 tick_lua_and_schema`.

```jsonc
{ "tick": { "intervalMs": 1000, "event": "price", "lua": "return { p = 100 + ordinal }" } }
```

One argument, `ordinal` — the same number a generated body is seeded by.
Return a table (JSON-encoded), a string (the raw `data:` bytes), or nil to
SKIP that firing with the connection left open.

A body carrying a CR or LF, or one over `MOCKER_MAX_RESPONSE`, is a skipped
firing counted in the row's `frames_skipped` note — a raw newline would break
SSE framing. A `nil` return is counted as nothing at all: it is a decline, not
a refused frame.

The call runs on the connection's own writer goroutine, so a slow function
delays THAT connection's frames and pings, up to the 2 s timeout, and no
other. Unlike a generated tick, a Lua tick is **not** byte-identical at the
same ordinal across connections.

### `stream.onFrame` — one inbound frame (ws only)

Refused on `sse` (`400 on_frame_on_sse`). Present, it REPLACES `reactive` and
`echo` entirely — both must be absent (`400 on_frame_and_reactive`,
`400 on_frame_and_echo`).

```jsonc
{ "onFrame": "if frame.op == \"ping\" then return \"reply\", { op = \"pong\" } end\nreturn nil" }
```

One argument, `frame`: a TEXT frame that parses as a JSON OBJECT arrives as a
table, anything else as the raw string. Verb-first return:

- `return nil` — no reply.
- `return "reply", data` — one text frame (a table is JSON-encoded).
- `return "close", code, reason?` — the closing handshake; `code` is 1000 or
  4000..4999, `reason` at most 123 bytes (a close frame's payload). A longer
  reason is a hook error, not a truncated close.

A reply enters the same per-connection send budget a reactive reply does. A
Lua error or a return the contract does not have drops that reply, counts it
in the row's `on_frame_errors:K` note — never `replies_dropped`, which means
the budget was full — and the hook KEEPS being called for later frames. After
a `close`, the reader keeps draining the peer's half and the hook stops being
called.

The call runs on the reader goroutine, so reply order follows frame order and
a slow hook blocks only that connection's reads.

### Previewing a stream draft

`preview_endpoint` runs a `tick.lua` draft for real, on the honest clock, with
NO workspace behind it — `mock.jwt` and `mock.entities` answer `"no_host"`.
The whole lay-out shares a 10-second budget; past it a frame keeps its place
and comes back with `notRun: true` and no data. `maxBytesPerSec` comes back
with `nominalRate: true`, because with a Lua producer it is a sample of what
ran and not a bound.

## 8. What a function is NOT

- **Not deterministic.** One seed and one spec no longer imply byte-identical
  bodies on a function-bearing endpoint. Endpoints without one are unaffected.
- **Not schema-checked.** `export_openapi` writes the endpoint's declared
  response schema and nothing compares the function's output to it. The
  document may say one thing while the endpoint answers another.
- **Not rate-limited.** A Lua password check runs on the unauthenticated mock
  plane with no brute-force protection of any kind.
- **Not memory-bounded.** See the guards table.
- **Not exported.** The Lua source is mocker-only behaviour and never appears
  in the OpenAPI document `export_openapi` produces — OpenAPI cannot express
  it.
- **Not previewable on a custom endpoint.** `preview_operation` runs a spec
  operation's draft function; there is no HTTP-draft preview for a custom
  endpoint. Save it and call it.
