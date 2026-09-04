# mocker

[![ci](https://github.com/yashok111/mocker/actions/workflows/ci.yml/badge.svg)](https://github.com/yashok111/mocker/actions/workflows/ci.yml)

mocker is a self-hosted mock backend: load an OpenAPI/Swagger document, get a
working HTTP server that answers per the spec without a line of code. Each
workspace lives on its own host (`<slug>.mock.corp.internal`), state is kept in
SQLite and edited through the admin panel, not by files on disk. Built for a
closed corporate network: one Go binary, one SQLite file, no npm registry and no
external dependencies at runtime.

Full specification — [`DESIGN.md`](DESIGN.md). This README does not duplicate
it, it only gives a quick way in.

## Documentation

| who | where |
|---|---|
| an operator at the admin panel | [`docs/USER-GUIDE.md`](docs/USER-GUIDE.md) (Russian, the product's language) — also rendered inside the panel at `/guide` |
| an agent driving mocker over MCP | [`skills/mocker/`](skills/mocker/SKILL.md) — a skill: the mental model, the order of calls, the rules that bite, and five references (every tool, every document shape, a cookbook, the same over curl). The running server serves the identical text: `initialize` returns a short orientation in `instructions`, and the `get_guide` tool returns the skill by topic |
| a script or CI job | [`skills/mocker/references/http.md`](skills/mocker/references/http.md) — login, CSRF, spec import, asset upload, the `/__mocker/state` calls a test suite makes |
| someone changing mocker itself | [`CLAUDE.md`](CLAUDE.md), [`HISTORY.md`](HISTORY.md), [`CARVE-OUTS.md`](CARVE-OUTS.md), and `DESIGN.md` above |

Install the skill into a frontend project so its agent knows mocker before the
first call: `npx -y -p skills skills add <this repo> --skill mocker -a claude-code`
(or copy `skills/mocker/` into `.claude/skills/`). [`docs/README.md`](docs/README.md)
is the index.

## What P0 does and what it does not yet

P0 is the skeleton everything else is written for: HTTP is in place, host
routing resolves the workspace before anything else, SQLite and migrations
apply, login works. P1a added spec import and the route table; P1b, the
response generator: a route matched against the spec now answers with
generated data instead of `404`/`501`. P1c (slice 1) added the recipe engine
and per-operation overrides on top of the generator, plus the auth preset: a
mocked login answers with a JWT-like token the frontend can decode and
schedule a refresh from (DESIGN §19, criterion "the frontend logs in"). Slice
2 made `when` conditions executable, added the RAM "right now" layer (forced
status and "fail N times"), traffic recording with polling, and two
conversions from an observed request — into an override and into a custom
endpoint. The UI and everything stateful are still ahead, in later phases.

**Present:**
- Docker image, `docker compose up` brings the service up.
- Dispatcher by the `Host` header: `<slug>.<MOCKER_BASE_DOMAIN>` goes to the
  mock plane, `MOCKER_ADMIN_HOST` to the admin plane.
- SQLite with the whole schema (`internal/store/migrations`) and two
  connection pools (one writer, one reader).
- Shared-password login (`MOCKER_AUTH_MODE=shared-password`), sessions,
  CSRF protection of the admin API.
- Workspace CRUD (`/api/workspaces`) with atomic slug reservation.
- `GET /__mocker/health` on the workspace host — `{ok, workspace, revision,
  spec}` — and `GET /healthz`, `GET /readyz` on the admin host.
- Spec import (OpenAPI 3.0/3.1, JSON — `POST /api/specs`) and binding to a
  workspace; the route table is built from indexed operations.
- Response generator: a route matched against the spec answers with
  deterministic data per the schema declared in the spec — body,
  `Content-Type` and status come from the spec, a list gets a predictable set
  of items and a total counter, and a detail shares fields with its row in the
  list. An unmatched path on the workspace host still answers `404`, and
  `/__mocker/*` other than `health` and `state` — `404` with code `not_implemented_yet`.
- Recipe engine (`internal/recipes`): `const`/`enum`/`copy`/`identity`/
  `jwt`/`now`/`null`/`omit`/`listSize`/`faker`/`template`/`sequence`, bound
  by data path (`user.id`, `items[*].status`), overriding the generated
  value. `jwt` is a compact JWS (`crypto/hmac`+`crypto/sha256`, HS256),
  no third-party library. `faker` takes a value from a published
  dictionary of twelve tokens (`person.fullName`, `internet.email` and
  so on — deterministic by seed); `template` substitutes
  `{{index}}` (the item's position in the array) into a string; `sequence`
  computes `start + index*step` from the same position.
- Per-operation overrides on `op_overrides` (`GET /api/workspaces/{id}/operations`,
  `GET`/`PUT`/`DELETE .../operations/{opKey}`): `routeOff` (the route answers
  `404`), `activeStatus`, pinned/generated body per status, recipes by data
  path — applied in the mock plane without an extra DB trip per request.
  `schemaPatch` is a subset of RFC 6902 (`add`/`remove`/`replace`) over the
  operation schema, applied once when the runtime is built, before body
  generation; it acts only on variants of spec operations — a custom
  endpoint's body is literal, there is nothing to patch there, and a patch on
  its variant stays stored but unapplied. `PUT` carries a mandatory
  `editVersion` field — the value from the preceding `GET` (or `0` if the row
  does not exist yet); a stale value answers `409 edit_conflict` with the
  current document in `details` instead of silently overwriting someone else's override.
- Auth preset (`GET`/`POST /api/workspaces/{id}/auth-preset`): `GET` proposes
  bindings by parsing the spec (`jwt` on token fields of auth paths, `now`/`const`
  on expiry fields, `identity.*` on profile fields), writes nothing and
  carries `editVersions` — a map `opKey -> editVersion` for every operation
  from the proposed bindings; `POST` applies exactly the list passed in
  (possibly edited) in one transaction and requires the same map back
  (for every affected `opKey`) — a mismatch on even one row
  answers `409 edit_conflict` with `staleVersions`, not a partial application.

- `when` conditions on a response variant: a list of simple predicates over
  query, header or body field (`equals`/`contains`/`exists`). The first
  matching variant wins, otherwise `activeStatus`, otherwise the spec's own
  choice. Iteration order is by ascending status, not by map order, otherwise
  the answer would be non-deterministic with two matching variants.
- The "right now" layer (`internal/livestate`, memory only, its own TTL):
  `POST`/`GET`/`DELETE /__mocker/state` on the workspace host — without
  authorization, so it works from tests — and the same through
  `GET`/`POST`/`DELETE /api/workspaces/{id}/session`. Actions: `status`
  (answer with this status until cleared), `fail` (fail N times or
  once), `delay` (hold the response N milliseconds) and `pause` (hold the
  request until the directive is cleared, but no longer than 10 s). The `fail`
  counter lives in RAM only and **never moves `revision`**.

  ```bash
  # make GET /widgets answer 503 until the directive is cleared
  # (the route must exist — see spec import in "Quick start"):
  curl -s -X POST -H 'Host: alex.mock.local' http://localhost:8080/__mocker/state \
    -d '{"target":{"method":"GET","path":"/widgets"},"action":"status","status":503}'
  # -> 200 {"workspace":"alex","directives":[{"target":{"method":"GET","path":"/widgets"},
  #          "action":"status","status":503,"once":false,"n":0,"setAt":"..."}]}

  curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: alex.mock.local' http://localhost:8080/widgets
  # -> 503

  curl -s -X DELETE -H 'Host: alex.mock.local' http://localhost:8080/__mocker/state
  # -> 200 {"workspace":"alex","cleared":1}

  curl -s -o /dev/null -w '%{http_code}\n' -H 'Host: alex.mock.local' http://localhost:8080/widgets
  # -> 200, generated response again
  ```
- Traffic: every mock-plane response is written in batches inside one
  transaction, not with an INSERT per request. `GET /api/workspaces/{id}/traffic`,
  `GET .../traffic/poll?since=<id>` (the cursor is a row id, not a time),
  `GET .../traffic/stream?since=<id>` (P6a: the same rows over Server-Sent
  Events — one `event: traffic` frame per committed batch, its `data:` the
  poll's own JSON and its `id:` the cursor, so a reconnecting `EventSource`
  resumes on `Last-Event-ID` and misses nothing; `:ping` comments every
  `MOCKER_STREAM_PING` seconds, a 900-second lifetime, at most 64 connections
  per process (`503` past that, `501 streaming_unsupported` behind a proxy
  that cannot stream) — the poll stays beside it as the fallback, and
  `GET /api/stream/stats` reports the process's open/refused/coalesced
  counters), `DELETE .../traffic` (after which ids are never reissued —
  `traffic.id` is `AUTOINCREMENT` since migration `0004`). Secrets are
  stripped **before** they reach the buffer:
  `authorization`, `cookie`, `x-api-key`, `password`/`token`/`*_key` fields at
  any depth, and bodies of auth paths are not stored at all (DESIGN §15).
- Custom endpoints (`GET`/`POST`/`PUT`/`DELETE /api/workspaces/{id}/endpoints`):
  they live in the same sorted route table and, at equal specificity,
  beat a spec operation. A workspace without a spec serves them too.
  `PUT .../endpoints/{eid}` is a full replacement of the definition and, like `PUT
  .../operations/{opKey}`, carries a mandatory `editVersion`. Since P6b a
  custom endpoint has a `kind`: `http` (every row that existed before) or
  `sse` — a Server-Sent Events stream an unauthenticated client opens with
  `GET` and receives a scripted `timeline` of frames and/or a generated
  `tick` from (`stream: {timeline: {frames: [{delayMs, event, data}], loop},
  tick: {intervalMs, event, schema}, closeWhenDone}`; tick bodies come from
  the generator over the inline schema, deterministic per seed, endpoint and
  tick). The tick floor is 100 ms, a timeline is at most 500 frames, both
  refused by name at write time on the admin route and the MCP tool alike;
  the session layer (forced status, pause, delay) applies at the handshake
  and never per frame; at most `MOCKER_STREAM_MAX_CONNS` streams per
  workspace, each closed at `MOCKER_STREAM_MAX_LIFETIME`; one traffic row per
  connection. `POST .../endpoints/preview` (`preview_endpoint`) lays a draft's
  first frames out on a time axis without saving it. Authoring is MCP-only —
  `create_endpoint`/`update_endpoint` take `kind` and `stream`; the screen
  shows a stream row as an endpoint with an empty response map.
- The live connections of those streams (P6c): `GET .../connections`
  (`list_stream_connections`) lists a workspace's open SSE connections with
  their ids, endpoint, peer and live frame counters beside the per-workspace
  cap; `DELETE .../connections/{cid}` (`close_stream_connection`) cancels one
  (no final frame; a browser `EventSource` reconnects as a new id);
  `POST .../connections/{cid}/frames` (`push_stream_frame`) pushes one
  `{event, data}` frame into one connection and answers with the `id:` it
  went out under — the frame lives in RAM on that connection and nowhere
  else. Agent-only; no screen.
- WebSocket endpoints (P6d): `kind: "ws"` on a custom endpoint, served by
  `github.com/coder/websocket` behind `internal/wsmock` — the tree's one
  library, one importing package. The same stream document as `sse`
  (timeline, tick) plus two inbound behaviours: `reactive: [{when, data?,
  close?: {code, reason?}}]` (the frame is the `body` of the same `when[]`
  language overrides use; `query`/`header` are the handshake's; first match
  wins; `close.code` is 1000 or 4000–4999) and `echo: true` (an unmatched
  frame comes back as-is). `MOCKER_STREAM_MAX_FRAME` caps an inbound frame
  (over it: close 1009), `MOCKER_STREAM_SEND_BUDGET` bounds the reply queue
  in bytes (over it: replies dropped and counted, one `{"$gap": N}` frame
  when writing resumes), `MOCKER_STREAM_ORIGINS` narrows who may connect (a
  missing `Origin` is always allowed). The traffic row records the close
  code, inbound frame count and, under `MOCKER_STREAM_TRAFFIC_FRAMES=first` (or `all`, A14),
  the first inbound JSON frame redacted by field name. The P6c list/close/
  push routes work on a `ws` connection unchanged (`framesIn` on the row;
  a push carries `data` only). Agent-only; no screen.
- Two conversions from traffic: `POST .../traffic/{tid}/to-override` turns an
  observed response into a pinned override (the key is the operation's
  **template** path, the only one the router produces), `POST .../traffic/{tid}/to-endpoint`
  makes a custom endpoint out of an observed request (the path is concrete, the
  base path is stripped; an observed `404` becomes `200` with the same body).
- `settings.notFoundBody` is finally applied: if set, an unmatched path
  answers with it instead of the standard error body.

**Not yet (later phases, see `DESIGN.md` §19):**
- The UI as a whole: the «Рецепты», «Трафик», «Подключить» screens (the
  product's UI is Russian), the editor with a schema tree and live preview.
- WebSocket mocks, the reactive and echo behaviours, the live-connection
  surface and the browser-side test client (`DESIGN.md` §30, v10: `P6c`
  through `P6e`). SSE mock endpoints have LEFT this list — `P6b` shipped
  them, see "Custom endpoints" above — and so has the admin traffic feed
  over SSE (`P6a`, "Traffic" above); `HISTORY.md` "Streaming — WS/SSE" sorts
  the streaming questions from each other. Stateful resources have LEFT this
  list too — P3a through P3h shipped them, see "Present" above. Scenarios
  (snapshot and switching) already exist — see "Scenarios" below; so do
  checkpoints and rollback, shipped in P2c.
- Applying `fail_directive` from an override (stored, not executed).
  `schema_patch` has left this list — it is applied, see "Present" above.
- Swagger 2.0 conversion, URL import (YAML input decodes since `A8`),
  request validation (`settings.validateRequests`), multi-file/ZIP specs.
- Multi-user isolation: the name at login is confirmed by nothing, this is a
  trusted network for synthetic data, not access control (DESIGN §15).

## Quick start

Needs Docker and Docker Compose **2.30+** (why — below). One command:

```bash
make up
```

On a tree with no `.env` that runs `scripts/init-env.sh` first: copies
`.env.example`, builds the image, mints an argon2id hash with the image's own
`hash-password` subcommand and writes it in, prints the generated admin
password ONCE, then brings the stack up on `http://127.0.0.1:8080`. To pick
the password yourself, run the step by hand first:

```bash
make init PASSWORD=changeme   # or: make init — a password is generated and printed
make up
```

An existing `.env` is never rewritten by any target: `make init` on one says
so and exits 0. To change the password later, `make hash-password
PASSWORD=…` and paste the hash into `.env` — verbatim, with every `$` sign,
escaping nothing.

About the compose version and the `$` signs: an argon2id hash looks like
`$argon2id$v=19$m=65536,t=3,p=4$<salt>$<hash>`, and compose by default
interpolates variables inside values from `env_file` — what reaches the
container from such a hash is the stump `=19=65536,t=3,p=4`, and login silently
stops working. So `docker-compose.yml` reads `.env` with `format: raw`, and that
requires Compose 2.30+. On older compose the key is rejected while parsing the
file (loudly, not silently); then either upgrade compose, or run the
image with `docker run --env-file .env` — it does not interpolate at all.

Bringing it up directly with `docker compose up -d --build` works the
same, but compose additionally reads `.env` a second way — as the
project env file — and prints one warning per `$` in the hash
(`The "argon2id" variable is not set`). That no longer affects the value in the
container, the text is merely misleading; `make up` silences it with
`COMPOSE_ENV_FILES=/dev/null`.

The container has a real healthcheck: `docker-compose.yml` runs `/mocker
healthcheck`, the binary's own subcommand, because the image is distroless
and has no curl to run one with. It reads the same `MOCKER_*` environment the
server did and dials its own `/readyz` with `MOCKER_ADMIN_HOST` as the
`Host` header, so `docker compose ps` says `healthy` only once the database
answers — and the HTTPS stack below waits for exactly that before Caddy
takes a request.

No DNS needed — the dispatcher decides by the `Host` header, so every check
below goes through `curl -H 'Host: ...'` against `localhost`.

The check is three requests with different `Host` values, exactly what the P0
readiness criterion is verified with (DESIGN §19), without DNS or TLS:

```bash
# a known workspace: it must first be created through the admin API —
# see `make smoke`, which does that automatically and checks all three
# cases. By hand, after logging in and creating workspace "alex":
curl -s -H 'Host: alex.mock.local' http://localhost:8080/__mocker/health
# -> 200, {"ok":true,"workspace":"alex","revision":1,"spec":null}

curl -s -o /dev/null -w '%{http_code}\n' \
  -H 'Host: nope.mock.local' http://localhost:8080/__mocker/health
# -> 404 (the host arrived, no such workspace)

curl -s -o /dev/null -w '%{http_code}\n' \
  -H 'Host: mocker.local' http://localhost:8080/healthz
# -> 200
```

The fastest way to see all three working at once is `make smoke`: it brings the
stack up, logs in, creates workspace `alex` and runs exactly these checks
itself.

Stop and remove the data volume: `docker compose down -v`.

## Setup wizard (a colleague's own machine)

`mocker setup` is the whole of the HTTPS section below as one command, for
a colleague who has the repository and Docker and nothing else — on Linux,
macOS or Windows. Build the binaries once (`make dist` puts
`bin/mocker-<os>-<arch>[.exe]` next to each other), hand the checkout over
with the right one, and from the repository root:

```sh
bin/mocker-linux-amd64 setup            # asks three questions; -yes takes the defaults
```

What it does, in order, and re-runs safely: checks docker and compose
(>= 2.30); writes `.env` from `.env.example` with a real argon2id hash, path
routing (one host, no wildcard DNS) and optionally an MCP key — an existing
`.env` is never touched; builds the image and starts the stack behind
Caddy's local CA (`docker-compose.tls.yml`); exports the CA root to
`mocker-root.crt`; waits for `/readyz` through TLS; adds `127.0.0.1
<admin host>` to the hosts file and installs the root into the system
trust store (both ask for your password — `-no-hosts` / `-no-trust` print
the manual line instead); prints the panel URL, the generated password
once, and the MCP header when a key was minted. Firefox needs the root
imported by hand (its own store); the summary says how.

Flags: `-password` (or `$MOCKER_PASSWORD`), `-admin-host` (default
`mocker.local`), `-port` (8443), `-subnet` (change on a "Pool overlaps"
error), `-mcp`, `-yes`, `-no-build`, `-dir`. Verbs: `setup down` stops the
stack keeping data and CA, `setup status` checks it. The panel is then
`https://mocker.local:8443` and every workspace
`https://mocker.local:8443/w/<slug>`.

## HTTPS

```bash
make up-tls     # the same stack behind Caddy, https://127.0.0.1:8443
make tls-root   # copy the stable local CA root as ./mocker-root.crt
make down-tls   # stop it (the CA root lives on the host and survives anything)
make smoke-tls  # the end-to-end check of everything below
```

`make up` and `make up-tls` are the same compose project and never run side
by side: each begins with `docker compose down --remove-orphans` (the data
volume stays), because the plain stack's network was created with docker's
own subnet and the overlay's `MOCKER_TRUST_PROXY` names an address on the
fixed one — switching without that teardown would leave Caddy on a network
mocker does not trust.

`docker-compose.tls.yml` is an overlay on `docker-compose.yml`, applied
through `scripts/compose-tls.sh` (the Makefile targets above all go through
it — the overlay needs `MOCKER_BASE_DOMAIN` and `MOCKER_ADMIN_HOST` read out
of `.env` and exported for interpolation, which raw `env_file` loading cannot
do). It adds a Caddy 2.11 sidecar with `deploy/Caddyfile`: two site blocks,
`*.<MOCKER_BASE_DOMAIN>` and `<MOCKER_ADMIN_HOST>`, both `tls internal` —
Caddy's own local CA issues the wildcard and the admin certificate at
startup, no ACME, nothing on port 80. The overlay also does the three things
DESIGN §15/§16 require of a real deployment and the plain stack cannot:
mocker's `:8080` is no longer published (Caddy is the only door),
`MOCKER_DEV` is forced to `0` (the session cookie is `Secure`), and
`MOCKER_TRUST_PROXY` is set to Caddy's own static address (the subnet's
`.254`; never the whole subnet, whose `.1` is the docker host) so mocker
believes Caddy's `X-Forwarded-Proto` / `X-Forwarded-For` and nobody
else's — the workspace URL the admin API reports becomes
`https://<slug>.mock.local:8443`, and the traffic log records the real
client, not the proxy.

To use it from a browser: `make tls-root`, import `mocker-root.crt` into the
BROWSER's own trust store (a profile-level import, not the OS store: the
root has no name constraints, so trusting it OS-wide trusts whoever holds
its key with every site the machine visits), and point `mocker.local` and `*.mock.local` (the
names in `.env`) at `127.0.0.1` in `/etc/hosts` — a wildcard is not an
`/etc/hosts` feature, so list each workspace host you use. From `curl`:

```bash
curl --cacert mocker-root.crt --resolve mocker.local:8443:127.0.0.1 https://mocker.local:8443/healthz
```

Two knobs, both taken from the shell: `MOCKER_TLS_PORT` (8443) and
`MOCKER_TLS_SUBNET` (`172.30.10.0/24`, an IPv4 `/24` ending in `.0` —
Caddy's static `.254` is what `MOCKER_TRUST_PROXY` is set to, so the subnet
has to be fixed; change it if compose reports `Pool overlaps` with a
network already on the box). The CA root does NOT live in a volume:
`scripts/tls-ca.sh` (`make tls-init`, run automatically by `up-tls`) creates
`./.tls-ca/root.crt` + `root.key` on the host once — gitignored, ten years —
and the Caddyfile's `pki` block provisions it as the CA root; only the
intermediate and the leaves stay Caddy-managed in `caddy-data`, and
re-issuing those is invisible to any client. `down -v`, `docker volume
prune`, a lost checkout copy — none of it rotates the root a trust store
already holds; deleting `.tls-ca/` is the one way, and every client then
re-trusts by hand. `mocker setup` creates the same pair on Go
(`ensureLocalCA`, `cmd/mocker/setup_ca.go`) because the wizard cannot
assume bash, and writes the same `mocker-root.crt` from it.

For a real contour, keep the shape and swap the certificate source in
`deploy/Caddyfile` (the contour's wildcard certificate, or TLS terminated at
the balancer in front) — the comment at the top of that file says what else
must change. `make smoke-tls` is what proves the contract end to end: TLS
verified against the root, the `Secure` cookie, the `https` workspace URL,
the wildcard certificate on a workspace host, the forwarded client address
in the traffic row, no plain-http port, an SSE tick stream and a WebSocket
`wss://` upgrade both through Caddy, and the healthcheck compose waited on.

## Environment variables

The full list is in [`.env.example`](.env.example), with a comment on each.
Nothing is read from a config file, only `MOCKER_*` (DESIGN §16):

| variable | default | what |
|---|---|---|
| `MOCKER_ADDR` | `:8080` | listen address |
| `MOCKER_LOG_LEVEL` | `info` | `debug` \| `info` \| `warn` \| `error` |
| `MOCKER_DEV` | off | drops `Secure` from the cookie for http on localhost. **Never set it inside the corporate network** |
| `MOCKER_BASE_DOMAIN` | — | e.g. `mock.corp.internal` |
| `MOCKER_ADMIN_HOST` | — | e.g. `mocker.corp.internal` |
| `MOCKER_ROUTING` | `host` | `host` \| `path` (fallback mode without wildcard DNS) |
| `MOCKER_RESERVED_PREFIX` | `/__mocker` | reserved on the workspace host |
| `MOCKER_AUTH_MODE` | `shared-password` | login mode |
| `MOCKER_SHARED_PASSWORD_HASH` | — | argon2id hash, see below |
| `MOCKER_DEFAULT_SPEC` | — | spec for workspace auto-creation (P1) |
| `MOCKER_DATA_DIR` | `/data` | volume with SQLite |
| `MOCKER_MAX_BODY` | `10mb` | request body limit |
| `MOCKER_MAX_RESPONSE` | `4mb` | cap on the generated response |
| `MOCKER_MAX_ENTITIES` | `1000` | cap on entities per resource |
| `MOCKER_TRAFFIC_MAX_BODY` | `8kb` | how much body reaches the traffic log |
| `MOCKER_TRAFFIC_RETENTION` | `1000` | traffic records per workspace |
| `MOCKER_CHECKPOINT_RETENTION` | `20` | machine checkpoints per workspace — both `pre-destructive` and (since P2d) debounced `auto`; manual ones are never deleted, `0` — do not delete machine ones either |
| `MOCKER_SUGGESTION_RETENTION` | `3` | `resource_suggestions` generations kept per spec (P3f `rederive`), oldest pruned first; `0` — keep every generation |
| `MOCKER_CHECKPOINT_DEBOUNCE` | `300` | seconds between debounced (`auto`) checkpoints taken before workspace mutations; `0` — do not write them at all |
| `MOCKER_RUNTIME_CACHE` | `32` | runtimes in the cache, LRU eviction |
| `MOCKER_STREAM_PING` | `15` | seconds between `:ping` comment frames on an idle SSE traffic stream (P6a); must be ≥ 1 |
| `MOCKER_STREAM_FRAME_TIMEOUT` | `5` | seconds a single SSE frame write may take before a stalled client is disconnected; the connection's own deadline stays cleared; must be ≥ 1 |
| `MOCKER_STREAM_SESSION_RECHECK` | `60` | seconds between a stream's re-validation of its session and its workspace (by identity, not id); must be ≥ 1 |
| `MOCKER_STREAM_MAX_CONNS` | `200` | live SSE mock connections per WORKSPACE (P6b); `0` refuses every stream handshake |
| `MOCKER_STREAM_MAX_LIFETIME` | `900` | seconds a mock stream may live before the server closes it; must be ≥ 1 (the admin feed keeps its fixed 900) |
| `MOCKER_STREAM_TRAFFIC_FRAMES` | `off` | `off` \| `first` \| `all` — what of a stream connection the one traffic row carries as its body: nothing, the first frame each way, or every frame each way (whole frames, one per line for WebSocket) under the two budgets below; the row is marked truncated when a budget stops the log |
| `MOCKER_STREAM_TRAFFIC_MAX_FRAMES` | `200` | `all`'s per-row budget: frames each way (≥ 1) |
| `MOCKER_STREAM_TRAFFIC_MAX_BYTES` | `64kb` | `all`'s per-row budget: bytes each way (≥ 1kb) |
| `MOCKER_MAX_ASSET` | `8mb` | cap on one uploaded asset (A6, DESIGN §32); must not exceed `MOCKER_MAX_BODY`; floor 1kb |
| `MOCKER_MAX_ASSETS_TOTAL` | `64mb` | cap on a workspace's stored assets; must not be below `MOCKER_MAX_ASSET` |
| `MOCKER_TRUST_PROXY` | `off` | `off` \| a list of proxy CIDRs/addresses, optionally with one hop count (a hop count alone is refused); the HTTPS overlay sets it to Caddy's one static address (`MOCKER_TLS_SUBNET`'s `.254`) |
| `MOCKER_URL_IMPORT_ALLOWLIST` | empty | domains and CIDRs for spec import by URL |
| `MOCKER_MCP_KEY` | empty | bearer key for `POST /mcp` (see "MCP" below); empty — the route does not exist at all |

`MOCKER_SESSION_SECRET` is deliberately absent: a session is a random token in
the DB, there is nothing to sign.

## Password hash

Login works by shared password: the server stores only the argon2id hash
(`MOCKER_SHARED_PASSWORD_HASH`), not the password itself.

```bash
make hash-password PASSWORD=your-password
# or without an argument — the password is read from stdin:
make hash-password
```

Prints the hash to stdout — paste it into `.env`. The command works without any
configured environment (`config.Load()` is not called in this mode).

## Repository layout

```
cmd/mocker/           entry point: config, migrations, service wiring, graceful shutdown
internal/config/      MOCKER_* -> Config, validated in one pass
internal/store/       SQLite: two pools (writer/reader), migrations by user_version
internal/domain/      workspace settings, slug (validation + transliteration)
internal/httpx/       error format, middleware (recover/log/body limit), peer resolution
internal/auth/        password (argon2id), sessions, CSRF token, Provider for future OIDC
internal/workspaces/  workspace repository: create/update/list, atomic slug
internal/mockplane/   dispatcher on the workspace host: resolve, CORS, /__mocker/health
internal/admin/       admin API: login, workspace CRUD, security headers
internal/server/      both planes assembled into one http.Handler by Host (or /w/{slug} in path mode)

internal/jsonx/        the single JSON boundary (backend — encoding/json); nobody else imports it directly
internal/webui/        embed.FS with the built UI (internal/webui/dist/) and the handler serving it
web/                   admin panel sources (React + Mantine + TanStack Router) — built by `make ui`, never reaches the runtime
api/openapi.json       admin API contract: the whole frontend client is generated from it, and the Go route table is checked against it

DESIGN.md              the specification in full (all phases)
CLAUDE.md              agent context: bars, invariants, what is deliberately absent
docker-compose.yml, Dockerfile, .env.example    local run (see "Quick start"); the compose file's healthcheck is `mocker healthcheck`
docker-compose.tls.yml, deploy/Caddyfile        the HTTPS overlay: Caddy with a local CA in front (see "HTTPS")
scripts/init-env.sh                             .env from .env.example with a real hash — what a fresh clone's `make up` runs first
scripts/compose-tls.sh                          `docker compose` with the overlay applied and the two host names exported from .env
scripts/smoke.sh                                end-to-end check of the P0 readiness criterion and every slice since
scripts/smoke-tls.sh                            end-to-end check of the HTTPS stack: TLS, the proxy contract, SSE and WebSocket through Caddy
```

## UI

The admin panel (`web/`) is built separately from Go and embedded into the
binary via `go:embed` (`internal/webui`) — the runtime needs no node at all,
only the build stage does.

Stack: **React 19 + Mantine 9** (components, forms, modals, notifications,
Tabler icons), **TanStack Router** (file-based routing with code generation) and
**TanStack Query**, **react-hook-form + arktype** (validation), **orval**
(API client generation), **Vite + vitest**, **oxlint/oxfmt**, package
manager — **yarn 4** via corepack. Styling — PostCSS with
`postcss-preset-mantine`, no Tailwind.

| command | what it does |
|---|---|
| `make ui` | `yarn install --immutable` + `yarn build` in `web/` (build = `yarn gen` → `tsc --noEmit` → `vite build`), the result lands straight in `internal/webui/dist/` — `go:embed` picks it up from there on the next Go build (`make build`/`make docker`) |
| `make ui-gen` | regenerate the client from `api/openapi.json` (orval) and the route tree (`tsr generate`). Needed after editing the contract or adding a file under `web/src/routes/` |
| `make ui-dev` | Vite dev server with hot reload; proxies `/api`, `/healthz`, `/readyz` to an already running `mocker` (`127.0.0.1:8080`) |
| `make ui-test` | `tsc --noEmit` + vitest |
| `make ui-lint` | `oxlint` + `oxfmt --check` |

`make ui-dev` rewrites both `Host` and `Origin` on proxied requests —
separately, not with a single `changeOrigin`. The mocker dispatcher decides which
plane serves a request by the `Host` header; on top of that the admin plane
checks `Origin` against `MOCKER_ADMIN_HOST` on every state-changing
request (CSRF protection, DESIGN §15) — Vite sets neither header
correctly on its own, and `changeOrigin` fixes only `Host`. The value
comes from `VITE_ADMIN_HOST` (default `mocker.local`, as in
`.env.example`).

A binary built without `make ui` (only `.gitkeep` in `internal/webui/dist/`)
starts and works as usual — any request to the UI simply
answers `503` with a text saying the panel is not built, instead of pretending
to be a working page.

Under `MOCKER_ROUTING=path` the UI is not served at all yet: on `/` the server
still answers a diagnostic `404` (`internal/server`). That is a deliberate
cut of this very slice — path mode for the UI is the first thing the next one
does (P1d-2) — not a forgotten case.

## Scenarios

A scenario is a named snapshot of a workspace: its current per-operation
overrides (`op_overrides`) and settings (`settings`) in full, stored under a
name and switched with one call. This is a **layer on top of the workspace, not
a restore**: an active scenario touches no row of the workspace —
it is substituted while the response is assembled, and deactivation returns the
previous state precisely because nothing changed underneath the scenario.
Overrides made while a scenario is active are still written to the workspace
(the overrides screen shows this explicitly) — they are simply invisible while
the scenario shadows them.

Save (the snapshot is taken from the workspace **as it is right now**; saving
again while another scenario is active is forbidden — `409`, deactivate
first):

```
POST /api/workspaces/{id}/scenarios {"name":"demo-empty-list"}
```

Switch and switch back — from the admin panel (the «Сценарии» tab; the
product's UI is Russian) or directly:

```
POST /api/workspaces/{id}/scenarios/{sid}/activate
POST /api/workspaces/{id}/scenarios/deactivate
```

Switch from a test suite — through the same unauthorized
`/__mocker/state` that sets the "right now" directives above, but by the
`scenario` key instead of `action`. An empty string means "deactivate", not
"switch to the scenario with an empty name" (a scenario name is never empty):

```bash
# turn a scenario on by name before the test scenario
curl -s -X POST -H 'Host: alex.mock.local' http://localhost:8080/__mocker/state \
  -d '{"scenario":"demo-empty-list"}'
# -> 200 {"workspace":"alex","scenario":"demo-empty-list","revision":8}

# ... run the tests ...

# return the workspace to its own state
curl -s -X POST -H 'Host: alex.mock.local' http://localhost:8080/__mocker/state \
  -d '{"scenario":""}'
# -> 200 {"workspace":"alex","scenario":null,"revision":9}
```

Activating an already active scenario is a no-op (writes nothing, does not bump
`revision`, does not rebuild the runner): this route is unauthorized by design
(so it can be poked from tests), and idempotency is what keeps an
accidental repeat from starting a rebuild loop.

What a scenario does **not** carry, and why:
- `basePath`, CORS settings and the 404 body — they are wired into the frontend
  config and into unmatched-path diagnostics; a scenario merged in from
  elsewhere would silently move the workspace to someone else's URL.
- Custom endpoints — they live by DB row id, not inside the snapshot, so
  changing one inside a scenario is still delete-and-create-again. Renaming a
  SCENARIO is a different thing and it shipped in P2d:
  `PUT .../scenarios/{sid}`. A scenario CONTENT editor is still absent by
  design — content is obtained by bringing the workspace to the wanted state
  and snapshotting it.
- Checkpoints, rollback, retention — a scenario is non-destructive by itself
  (deactivation erases nothing), there is nothing to roll back here.

## Preview

`POST /api/workspaces/{id}/preview` takes a DRAFT per-operation override —
the same shape `PUT .../operations/{opKey}` accepts, but without `editVersion`:
a preview stores nothing, so there is nothing to check here — and answers with
the body the mock plane would serve for this operation had the draft been
saved.
Writes nothing, does not bump `revision`, does not touch the runner cache: the
workspace is rebuilt on every call and thrown away right after.

```
POST /api/workspaces/{id}/preview
{
  "opKey": "GET%20%2Fwidgets%2F%7Bid%7D",
  "draft": { ... },              // same shape as PUT .../operations/{opKey}
  "status": "404",               // optional — explicit status-tab choice
  "query": "page=2",             // optional
  "headers": {"X-Tenant": "acme"},
  "body": { ... },               // optional — request body for when[] over body
  "pathParams": {"id": "42"}     // EVERY {param} of the operation and basePath required
}
```

The response carries `status`/`statusSource` (`requested`/`when`/`active`/`default` —
what exactly picked the status), `body`/`mediaType`/`encoding` (`utf8` or
`base64`), or `noBody`/`routeOff`/`refused` instead of a body, and separately
`schemaPatchApplied`, `recipesBound`, `delayMs` (computed, not slept) and
`shadowedBy` (the name of the scenario whose snapshot would really serve the
row, if an active scenario shadows the draft by key).

Refusals are not a 500 but a named code: `409 custom_endpoint_wins` (for this
`(method, path)` a custom endpoint wins, not a spec operation),
`400 no_spec` (the workspace has no spec), `404 operation_not_found` (the spec
exists, `opKey` is not in it), `400 missing_path_param` (the draft sent no value
for at least one declared `{param}`) and `400 invalid_draft` (the draft itself
fails the same three gates as `PUT`). In the admin panel — the
«Показать пример» button (Russian UI) and a result panel on the operation tab.

What the preview deliberately does NOT see and does NOT do — in CLAUDE.md,
the "What is deliberately absent" section.

## Assets

Since `A6` (DESIGN §32) a workspace holds uploaded FILES a mock can serve —
a JPEG, a WebP, a PDF, anything a browser does not execute — addressed by
name and stored as SQLite BLOBs beside everything else in the one volume.
The agent is primary (`upload_asset`, `list_assets`, `delete_asset`);
since `A10` the workspace's «Файлы» tab lists, uploads (a dropzone) and
deletes them too; and from a shell the upload is one `curl`:

```bash
# the body IS the file; Content-Type is its media type; X-CSRF-Token as on every write
curl -s -b jar -H 'Host: mocker.local' -H 'Origin: http://mocker.local' \
  -H "X-CSRF-Token: $CSRF" -H 'Content-Type: image/jpeg' \
  -X PUT --data-binary @photo.jpg http://localhost:8080/api/workspaces/1/assets/photo.jpg
# -> 201 {"name":"photo.jpg","mediaType":"image/jpeg",...,"url":"http://alex.mock.local/__mocker/assets/photo.jpg"}
```

Three ways the file reaches a response:

- **The url itself** — `GET <workspace host>/__mocker/assets/<name>`, the
  third control route beside `/health` and `/state`: the bytes under the
  stored type, a strong `ETag` (the sha256, `304` on `If-None-Match`),
  `nosniff`, no cache header beyond the tag; not a mock (no forced status,
  delay or pause), not recorded in traffic.
- **`bodyRef` on a pinned variant** — `{"mode": "pinned", "bodyRef":
  "asset:photo.jpg"}` on an operation override or a custom endpoint: the
  variant serves the file verbatim under the asset's own type (`mediaType`,
  `body` and `bodyEncoding` are refused beside it). A missing asset answers
  the variant's status with an empty body and `asset_missing` in the traffic
  row.
- **The `asset_url` recipe** — `{"kind": "asset_url", "value": "photo.jpg"}`
  (or a list of names, one picked per element from the seed) writes the
  absolute url into a generated field: `items[*].avatarUrl` pointing at a
  real picture, the "valid but meaningless" hole §9 names.

Two caps, `MOCKER_MAX_ASSET` per file and `MOCKER_MAX_ASSETS_TOTAL` per
workspace (see the table above); browser-executable media types are refused
at upload and again at serve. No checkpoint, scenario or bundle carries the
bytes — a deleted asset comes back only by a new upload under the same name,
and a `bodyRef` or recipe that names it is neither refused nor changed
meanwhile. `DELETE .../assets/{name}` requires `confirmSlug`.

## MCP

`POST /mcp` on the admin host is a Model Context Protocol server (streamable
HTTP, stateless): sixty-three domain-named tools over the admin API, so an
LLM agent creates workspaces, reads and overrides operations, sets and clears
session directives and reads traffic through the same path as the UI — without
inventing a second way to talk to mocker that bypasses admin-handler validation.

Mounted only if `MOCKER_MCP_KEY` is set. Client configuration:

```
URL:     https://<admin-host>/mcp
Header:  Authorization: Bearer <MOCKER_MCP_KEY>
Header:  Accept: application/json, text/event-stream
```

```bash
curl -s -X POST https://<admin-host>/mcp \
  -H "Authorization: Bearer ${MOCKER_MCP_KEY}" \
  -H 'Content-Type: application/json' \
  -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}'
```

- `Accept` is mandatory and must carry **both** values separated by a comma —
  without it the MCP SDK answers `400` before it even looks at the request body.
- `MOCKER_MCP_KEY` shorter than 32 bytes fails server startup (`config.Load`) —
  the same "bad configuration dies at startup" rule as for any other
  `MOCKER_*` variable.
- An empty `MOCKER_MCP_KEY` (the default in `.env.example`) — `/mcp` does not
  exist as a route: `404`, not `401` and not a panel. An unset key means
  "there is no attack surface", not "a surface locked with an empty lock".
- `/mcp` does not accept a session cookie at all — only the `Authorization`
  header, compared in constant time.
- An agent orients itself without the repository: `initialize` answers with a
  short `instructions` text, and `get_guide {topic}` returns the usage guide
  by topic (`overview`, `tools`, `shapes`, `cookbook`, `http`, `design`) — the same
  files as [`skills/mocker/`](skills/mocker/SKILL.md), embedded through
  `internal/guide` and held equal by a test. Since `A8` spec import has a
  tool too (`import_spec`, JSON or YAML); `DELETE /api/specs/{id}` is the
  one spec verb still without one.

## @yashok111/mocker-test — the mock as a fixture a test suite owns

`packages/mocker-test/` is a zero-dependency npm package over the mock
plane's control routes (`{prefix}/health`, `{prefix}/state`): a typed
client, a Playwright fixture and Cypress commands, so a test switches a
scenario before a block, forces a status for one case and resets after —
without an operator and without the admin plane.

```sh
npm i -D @yashok111/mocker-test
```

```ts
import { mocker } from "@yashok111/mocker-test";
const mock = mocker("http://alex.mock.local");
beforeEach(() => mock.reset());
await mock.scenario("checkout-empty");
await mock.fail("POST /orders", 503, { times: 2 });
await mock.pause("GET /cart"); // … reset() releases it
```

`make plugin-test` runs its suite against `./bin/mocker` (a real server on
a loopback port, path routing); `make plugin-build` emits `dist/`. The
package README has the full table of calls and both integrations.

## Tests

```bash
make test    # go test ./cmd/... ./internal/... -race -count=1 (+ goleak in every package)
make lint    # go vet + gofmt -l ./cmd ./internal + golangci-lint run
make fmt     # gofmt -w ./cmd ./internal
make smoke   # docker compose up + every end-to-end check; needs curl, jq and python3 (>= 3.10,
             # standard library only — scripts/wsclient.py is the WebSocket client since P6d)
make smoke-tls  # the HTTPS overlay end to end (see "HTTPS"); same requirements
make plugin-test # @yashok111/mocker-test against ./bin/mocker (packages/mocker-test: install, tsc, oxlint, vitest)
```

The scope is `./cmd` and `./internal`, not `./...`: `web/node_modules` now sits
inside this Go module, and a single `.go` file of a transitive npm dependency
would break build/vet/gofmt for everyone — unfixably, since it is not a project file.

The acceptance tests (every `*acceptance_test.go`, the P1b golden over 419
generated bodies in `internal/gen/golden_p1b_test.go`, and a dozen more)
run against `internal/testspec/testdata/acceptance.json`, a 110-path,
130-operation OpenAPI 3.0.3 corpus embedded into the test binaries — the
structure-preserving, sanitised twin of the internal API document the
project was built against. A fresh clone runs `make test` green with no
extra file and no environment variable.

`internal/admin/openapi_contract_test.go` checks `api/openapi.json` against the
real route table (`Server.routes`) in both directions: a handler with no
contract entry and an entry with no handler fail `make test` alike. It also
checks that every state-changing route (except login) declares the
`csrfToken` requirement — otherwise the generated client would not learn the
header is needed and would get a `403` with no explanation.

`make smoke` runs the same thing as "Quick start" above, but through
Docker and with response codes checked automatically — handy before a commit or
in CI.

## License

MIT — see [`LICENSE`](LICENSE).
