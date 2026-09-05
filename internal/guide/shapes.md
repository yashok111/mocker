# mocker document shapes

The JSON an operator or agent WRITES. Every admin body is decoded with unknown
fields refused, so a misspelt key is a 400 naming it, never silently ignored.
Path templates below are the admin HTTP routes; the MCP tool that wraps each is
named in `tools.md`.

## 1. Operation override — `PUT /api/workspaces/{id}/operations/{opKey}` (`set_operation_variant`)

```jsonc
{
  "overrideOn": true,          // false = the row exists but is inert end to end
  "routeOff": false,           // true = the route answers the workspace's 404, as if unmatched
  "activeStatus": 409,         // optional; which status to serve when no when[] matches
  "responses": {               // key = 3-digit status string
    "409": {
      "mode": "pinned",        // "generated" (default) | "pinned"
      "when": [                // optional; ALL conditions must hold (AND)
        { "in": "query", "name": "verbose", "op": "exists" }
      ],
      "body": { "code": "conflict" },   // the literal body for mode "pinned" (any JSON)
      "bodyEncoding": "",               // "" | "base64" (then body is a JSON string)
      "bodyRef": "asset:photo.jpg",     // pinned only; exclusive with body, bodyEncoding AND mediaType
      "mediaType": "application/json",
      "headers": { "X-Trace": "abc" },  // added on top for pinned variants
      "schemaPatch": [                  // RFC 6902 add|remove|replace over the resolved schema
        { "op": "add", "path": "/properties/extra", "value": { "type": "string" } }
      ],
      "recipes": {                      // data-path pattern -> recipe (section 2)
        "items[*].status": { "kind": "enum", "value": ["new", "done"] }
      },
      "function": "return 200, { ok = true }"  // Lua that PRODUCES this response; see functions.md
    }
  },
  "listSize": { "min": 3, "max": 7 },  // optional; min == max for a fixed length
  "delayMs": 250,                      // optional, >= 0, <= 30000 on the wire
  "validateReq": null,                 // optional bool
  "editVersion": 12                    // REQUIRED; 0 = "I expect no row yet"
}
```

Rules:
- `mode` is `generated` (the body comes from the schema, seed and recipes) or `pinned` (the body is literal). A pinned body is at most 4 MiB decoded.
- `mediaType` that a browser executes (`text/html`, `image/svg+xml`, …) is refused on every write, whatever the mode.
- `bodyRef` = `asset:` + an asset name (`[A-Za-z0-9._-]{1,128}`). Existence is not checked at write time; a missing asset serves an empty body and marks the traffic row `asset_missing`.
- `recipes`: at most 1000 bindings per variant. `schemaPatch`: at most 64 ops, 64 KiB, root target refused.
- `function` is exclusive PER VARIANT with `body`, `bodyEncoding`, `bodyRef`, `recipes`, `schemaPatch` and `mediaType` — one producer per status, refused by name, so there is no precedence. `when[]` is allowed. Other statuses on the same row are untouched: a function-200 beside a pinned-401 is the sign-in shape. It is compiled at write time (a syntax error is a 400 with the parser's own line) and refused on a stream row. The whole contract, the `mock` helpers and the guards: `functions.md`.
- Status selection at serve time, in order: `routeOff` → session directive → `when[]` (candidates tried in ascending status, first full match wins; a variant with no `when[]` is never a candidate) → `activeStatus` → the spec's default (lowest 2xx, else lowest).
- The PUT answers the stored document plus `method`, `path`, `opKey`, `updatedAt`, `editVersion` and the workspace `revision`.

### `when[]` condition

```jsonc
{ "in": "query" | "header" | "body", "name": "verbose", "op": "equals" | "contains" | "exists", "value": "1" }
```

`value` is required for `equals`/`contains` and must be absent for `exists`.
`body` conditions see TOP-LEVEL keys of a JSON object body only (no dot paths).
Numbers render canonically (`1.0` → `"1"`). A header present but empty reads as absent.

### opKey

`url.PathEscape(METHOD + " " + relativePath)` — one URL segment, so
`GET /users/{id}` is `GET%20%2Fusers%2F%7Bid%7D`. The path is RELATIVE to the
workspace `basePath`. Take it from `find_operations`/`get_operation` and pass it
back verbatim: encoding it a second time is a 400.

## 2. Recipes

A recipe is a value source bound to a data path inside a GENERATED body. The
map key is the path pattern; the value is one recipe.

Path pattern grammar: dotted property names and bracketed indices; `[*]` is any
index; `""` is the response root. `items[*].status`, `user.profile.avatarUrl`,
`[*].id` (a top-level array). A literal index beats `[*]` at the same position.

```jsonc
{ "kind": "const",     "value": { "any": "json" } }
{ "kind": "enum",      "value": ["a", "b", "c"] }              // picked from the seed
{ "kind": "copy",      "field": "$.id" }                       // copy a sibling
{ "kind": "identity",  "field": "email" }                      // id|name|email|roles|org.id|org.name|org.type — from settings.identity
{ "kind": "jwt",       "claims": { "scope": "admin" }, "ttlSec": 900 }   // signed with settings.auth; 0 = workspace default TTL
{ "kind": "now",       "offset": "-7d", "format": "iso" }      // format: iso|epoch|epoch_ms; offset ±N with s|m|h|d
{ "kind": "null" }
{ "kind": "omit" }                                             // drop the property
{ "kind": "listSize",  "value": 5 }                            // or [lo, hi]; binds to the ARRAY node
{ "kind": "faker",     "field": "internet.email" }
{ "kind": "template",  "value": "user-{{index}}" }             // only {{index}} is substituted
{ "kind": "sequence",  "value": { "start": 100, "step": 5 } }
{ "kind": "ref",       "value": { "family": "/users", "property": "id", "policy": "generate" } }
{ "kind": "asset_url", "value": "avatar.png" }                 // or ["a.png", "b.png"]
```

- `faker.field` vocabulary (exact): `person.fullName`, `internet.email`, `phone.number`, `datetime.timestamp`, `datetime.date`, `lorem.title`, `lorem.description`, `status.value`, `code.value`, `color.hex`, `slug.value`, `string.uuid`.
- `ref` reads a value a CONFIRMED resource family really holds (section 6). `policy`: `generate` (default — fall back to a generated value and note `ref_unresolved` in traffic) or `set-null`. `restrict` is refused by name.
- `asset_url` writes the absolute URL of an uploaded asset (section 8).
- Recipes are the same map on an operation override and on a custom endpoint variant. A confirmed resource never evaluates recipes.

## 3. Custom endpoint — `POST /api/workspaces/{id}/endpoints` (`create_endpoint`)

```jsonc
{
  "method": "POST",
  "path": "/orders/{id}",        // relative to basePath, leading "/", no query
  "status": 201,                 // omitted = 200
  "body": { "ok": true },        // becomes responses["201"] with mode "pinned"
  "mediaType": "application/json",
  "bodyRef": "asset:sample.pdf", // optional, exclusive with body + mediaType
  "function": "…",               // Lua producing the response; exclusive with body/bodyRef/mediaType, refused on sse/ws
  "kind": "http",                // "http" (default) | "sse" | "ws"
  "stream": { ... },             // required for sse/ws, forbidden for http (section 4)
  "schema": {                    // P7a: inline JSON Schema; with no body the response is GENERATED from it
    "type": "object", "required": ["id"],
    "properties": { "id": { "type": "integer" }, "owner": { "$ref": "#/components/schemas/User" } }
  },
  "reqSchema": { "type": "object" },   // exported as requestBody; never enforced on a request
  "operation": {                       // the OpenAPI operation fields the contract needs (all optional)
    "summary": "Create an order", "description": "…", "tags": ["orders"],
    "operationId": "createOrder",      // [A-Za-z0-9_.-]{1,128}, unique across the workspace's rows AND the spec's operations
    "deprecated": false,
    "parameters": [ { "name": "dryRun", "in": "query", "required": false, "schema": { "type": "boolean" } } ]
  }
}
```

`schema`, `reqSchema` and every `parameters[].schema` are JSON objects of at
most 64 KiB; a `$ref` in any of them must be a local pointer (`#/…`) that
resolves against the bound spec at write time (400 `schema_ref_unresolved`;
with no spec bound every `$ref` is refused). A `parameters[]` entry has
`in` ∈ `query` \| `path` \| `header`; a `path` one must name a `{name}`
segment of the row's own `path`, and an undeclared `{name}` segment is
exported as a required string parameter. `schema` + a pinned `body`: the
body serves, the schema is only the exported shape (§8's mode rule). On a
spec operation's override `schema` is refused (400 `schema_on_override`) —
use `schemaPatch` there.

`PUT /api/workspaces/{id}/endpoints/{eid}` (`update_endpoint`) is a FULL
replacement: `method`, `path`, `overrideOn` (omitted = true), `routeOff`
(omitted = false), `activeStatus` (0 = 200), `responses` (status → the same
Variant as section 1, plus `schema`), `listSize`, `delayMs`, `kind`, `stream`,
`reqSchema`, `operation`, `editVersion` (required, 0 refused).

A custom endpoint wins over a spec operation at the same canonical shape, and
a 409 refuses creating one where an operation OVERRIDE already sits. A custom
endpoint is in checkpoints but NOT in scenarios.

## 4. Stream document (`kind: "sse"` or `"ws"`)

```jsonc
{
  "timeline": {                                   // scripted frames, in order
    "frames": [ { "delayMs": 250, "event": "tick", "data": { "n": 1 } } ],
    "loop": false
  },
  "tick": {                                       // generated frames on an interval
    "intervalMs": 1000, "event": "update",
    "schema": { "type": "object", "properties": { "price": { "type": "number" } } },
    "lua": "return { price = 100 + ordinal }"     // OR lua, never both (functions.md)
  },
  "closeWhenDone": true,                          // default true: close after the timeline (sse)
  "reactive": [                                   // ws only: reply to an inbound JSON object frame
    { "when": [ { "in": "body", "name": "cmd", "op": "equals", "value": "ping" } ],
      "data": { "pong": true },
      "close": { "code": 4000, "reason": "done" } }
  ],
  "echo": false,                                  // ws only: mirror unmatched frames
  "onFrame": "return \"reply\", { pong = true }"   // ws only: Lua per inbound frame; REPLACES reactive + echo
}
```

- `sse`/`ws` require method `GET`, no `responses`/`status`/`body`, and at least one of `timeline`/`tick` (`ws`: or `reactive`/`echo`/`onFrame`). A variant-level `function` is refused on both (`400 function_on_stream`) — a stream is not a request/response, and its Lua goes in `tick.lua` or `onFrame`.
- `tick.lua` and `tick.schema` are exclusive by name (`400 tick_lua_and_schema`); exactly one is required. `onFrame` is `ws` only (`400 on_frame_on_sse`) and refuses `reactive`/`echo` beside it (`400 on_frame_and_reactive`, `400 on_frame_and_echo`). Both are compiled at write time. `functions.md` has the argument, the return convention and what a bad frame costs.
- Limits: ≤ 500 frames; frame `delayMs` 0..30000; tick `intervalMs` ≥ 100; `event` ≤ 64 bytes, no newline; `data` required (`null` for empty) and ≤ `MOCKER_MAX_RESPONSE`; ≤ 100 reactive rules; `close.code` 1000 or 4000..4999; `tick.schema` an object with no `$ref`.
- Frames carry `id: <ordinal>`; `Last-Event-ID` is ignored — a connection copies its definition at the handshake and runs to completion on it. A SCHEMA tick body is deterministic per (seed, endpoint id, ordinal); a `lua` one is not, and a body it returns carrying a CR/LF or over `MOCKER_MAX_RESPONSE` is a skipped firing on an open connection.
- The session layer applies to the HANDSHAKE only: a forced status answers that status with no stream, a delay delays the first byte, a pause parks it.
- One traffic row per connection, written when it closes: `stream:sse,frames:N` / `stream:ws,frames_in:M,close:<code>`, plus `pushed:M`, `closed:admin`, `replies_dropped:K` and `on_frame_errors:K` when they apply.

## 5. Session directive — `POST /api/workspaces/{id}/session` (`set_session_directive`) and `POST {prefix}/state`

```jsonc
{
  "target": { "method": "POST", "path": "/auth/login" },   // or the string "*" for every operation
  "action": "status",     // "status" | "fail" | "delay" | "pause"
  "status": 503,          // status|fail only, 100..599
  "ms": 300,              // delay only, 1..30000
  "once": false,          // fail only
  "n": 3                  // fail only: fail the next n requests, then serve normally
}
```

- A field that does not belong to the action is REJECTED, not ignored.
- `pause` parks the request until the directive is cleared.
- `DELETE` on either route clears EVERY directive with no body, or ONE target's with a body `{ "target": "*" | {method, path}, "action"?: "..." }` (every action on it, or the one named; a parked pause on it is released). A body naming no target is refused, never read as "everything".
- `POST {prefix}/state` on the WORKSPACE host is the unauthenticated twin for tests, and it also takes `{ "scenario": "<name>" }` (`""` deactivates). `GET` lists, `DELETE` clears (all, or one target with the body above).
- Directives live in RAM, never bump `revision`, and are lost on restart.

## 6. Resources — `POST /api/workspaces/{id}/resource-decisions` (`decide_resource`)

```jsonc
{ "routeFamily": "/users", "state": "confirmed", "confirmSlug": "checkout" }
```

A route family is a canonical path `X` where the spec declares both `GET X`
and `GET X/{id}`. Confirming it makes the mock REMEMBER: the family is
populated with `listSize` generated rows per scope, `GET X` lists them, `GET
X/{id}` reads one, `POST X` (when the request body is the item schema) creates
one with the next key, and `DELETE X/{id}` (when the spec declares it) deletes.
Nothing else is taken over; `PUT`/`PATCH` still generate. A nested family
(`/orgs/{orgId}/teams`) is scoped per parent row and needs its parent confirmed
first; depth is at most three.

`PUT /api/workspaces/{id}/resources/{family}/entities/{key}` (`set_resource_entity`):
`{ "data": { … }, "scopeKey": "", "baseScopeKey": "" }` — create-or-replace one row;
`data[idField]` becomes the key. `DELETE` on the same path (`delete_resource_entity`)
takes the optional scope body. Both address the family by `routeFamily`
(`url.PathEscape`d in the path) and the row by its `entityKey`.

`POST /api/workspaces/{id}/reset-data` (`reset_resource_data`):
`{ "mode": "reseed" | "clear", "confirmSlug": "…" }` — `reseed` repopulates every
confirmed family from the CURRENT settings, `clear` deletes every row.

## 7. Workspace settings — `PATCH /api/workspaces/{id}` (`update_workspace_settings`)

```jsonc
{
  "name": "Checkout mock",          // optional
  "specId": 12,                     // optional; attaches, never detaches
  "settings": {                     // optional, but REPLACED WHOLE when present
    "seed": 1,                      // root of every generated value
    "basePath": "/api/v1",          // or "/tenants/{tenant}" with basePathValues
    "basePathValues": [],           // one element per declared base parameter tuple
    "listSize": 5,                  // default array length, 0..1000
    "nullRate": 0,                  // 0..1, share of nullable fields served as null
    "envelope": null,               // e.g. "data": wrap every generated body as {"data": …}
    "identity": { "id": 1, "name": "Test Testov", "email": "test@example.com",
                  "roles": ["user"], "org": { "id": 1, "name": "Test Org", "type": "school" } },
    "auth": { "jwtTtlSec": 3600, "alg": "HS256", "signingKey": "<hex>", "requireHeader": false },
    "cors": { "mode": "reflect", "credentials": true },   // mode: reflect | off
    "validateRequests": false,
    "delayMs": 0,
    "notFoundBody": null            // any JSON, replaces the default 404 body
  },
  "editVersion": 5                  // required
}
```

`POST /api/workspaces` (`create_workspace`): `{ "name": "…", "slug": "…", "specId": 12 }`.
The slug is 2..31 chars of `a-z0-9-`; reserved words (`admin`, `api`, `www`, …) are refused.

## 8. Assets — `PUT /api/workspaces/{id}/assets/{name}` (`upload_asset`)

The HTTP body IS the file and `Content-Type` is its type (raw body, not
multipart, not JSON). The MCP tool carries it as `dataBase64`. Name:
`[A-Za-z0-9._-]{1,128}`. Served at `GET <workspace url>{prefix}/assets/<name>`
with a strong `ETag` (sha256) and `nosniff`; not a mock, not recorded.

## 9. Mock-plane control routes (on the WORKSPACE host, no auth)

| route | answers |
|---|---|
| `GET {prefix}/health` | `{ "ok": true, "workspace": "<slug>", "revision": 42, "spec": 12 }` |
| `GET / POST / DELETE {prefix}/state` | section 5 |
| `GET {prefix}/assets/{name}` | section 8 |

`{prefix}` is `MOCKER_RESERVED_PREFIX`, default `/__mocker`. The workspace
host is `<slug>.<MOCKER_BASE_DOMAIN>` in host mode, or
`<MOCKER_ADMIN_HOST>/w/<slug>` in path mode; `get_workspace` reports the
exact `url`. `revision` in `/health` is the one signal an external test has
that the configuration changed.

## 10. Spec import — `POST /api/specs` (`import_spec`)

```jsonc
{ "name": "Billing API", "source": "upload", "document": "{\"openapi\":\"3.0.3\", …}" }
```

`document` is the whole OpenAPI document as ONE STRING — JSON or YAML text
(YAML is converted server-side, integer keys such as `200:` become `"200"`).
OpenAPI 3.0 and 3.1 are accepted; Swagger 2.0 and `source: "url"` are refused
by name. A byte-identical re-import answers 200 with `duplicate: true`.

## 11. Error envelope (admin API)

```jsonc
{ "error": { "code": "bad_request", "message": "human-readable text", "details": { } } }
```

Shared codes: `bad_request`, `unauthorized`, `forbidden`, `not_found`,
`conflict`, `too_large`, `internal`. Named ones you will meet: `edit_conflict`
(details = the current document, or `{"gone": true}`), `invalid_base_path`,
`invalid_base_path_values`, `custom_endpoint_wins`, `parent_not_confirmed`,
`child_confirmed`, `stale_config`, `unknown_family`, `connection_not_found`,
`inbox_full`, `push_timeout`, `asset_not_found`.
