# mocker over plain HTTP — curl, scripts, CI

Two planes, two hosts. The ADMIN plane (`MOCKER_ADMIN_HOST`, e.g.
`mocker.local`) is where configuration lives: session cookie, CSRF, the JSON
API of `api/openapi.json`, the UI, `/mcp`. The MOCK plane is every workspace
host (`<slug>.<MOCKER_BASE_DOMAIN>`, or `<admin host>/w/<slug>` in path mode):
no authentication, CORS to everyone, the spec's routes plus three control
routes under `MOCKER_RESERVED_PREFIX` (default `/__mocker`).

Without DNS the dispatcher still works: it routes on the `Host` header, so
`curl -H 'Host: alex.mock.local' http://localhost:8080/...` reaches workspace
`alex`. If the box exports `HTTPS_PROXY`, add `--noproxy '*'`.

## Admin plane: login, CSRF, one write

```bash
ADMIN=http://localhost:8080; H='Host: mocker.local'; O='Origin: http://mocker.local'
JAR=$(mktemp)

# 1. login: the cookie lands in the jar, the CSRF token in the body
CSRF=$(curl -s -c "$JAR" -H "$H" -H 'Content-Type: application/json' \
  -d '{"password":"'"$MOCKER_PASSWORD"'","name":"ci"}' "$ADMIN/api/auth/login" | jq -r .csrfToken)

# 2. every state-changing request: cookie + Origin + X-CSRF-Token + JSON content type
curl -s -b "$JAR" -H "$H" -H "$O" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
  -d '{"name":"alex"}' "$ADMIN/api/workspaces"
# -> 201 {"id":1,"slug":"alex","name":"alex","url":"http://alex.mock.local", ...}

# 3. reads need only the cookie
curl -s -b "$JAR" -H "$H" "$ADMIN/api/workspaces" | jq .
```

`GET /api/me` returns the same `{user, csrfToken, config}` as login; a 401
means the session is gone (logout, restart with a wiped volume, another tab
logged out). On a write the hostname of `Origin` (or, absent that, of
`Referer` — never neither) must equal `MOCKER_ADMIN_HOST`, else 403.

## Export a saved sequence or API contract

Authenticated read-only routes:

- `GET /api/design-scenarios/{id}/revisions/{rid}/export-options`
- `GET /api/design-scenarios/{id}/revisions/{rid}/exports/{format}?contractId=...`
- `POST /api/design-scenarios/{id}/revisions/{rid}/archive`

Formats: `plantuml`, `mermaid`, `openapi-json`, `openapi-yaml`, `asyncapi-json`,
`asyncapi-yaml`, `postman`, `curl`, `markdown`, `html`.
Supply `contractId` for OpenAPI or AsyncAPI, selecting a contract of the matching
family. The response is JSON with `content` as a string, plus
`filename`, `mediaType`, `scenarioId`, `revisionId`, `sourceHash` and diagnostics.
Write the content exactly; parsing and reserializing the contract in JavaScript
can round large numbers. Example using the login variables above:

```bash
curl -fsS -b "$JAR" -H "$H" \
  "$ADMIN/api/design-scenarios/7/revisions/11/exports/mermaid" \
  | jq -j '.content' > scenario.mmd
```

These endpoints read immutable snapshots and do not create mocks or change
history. A revision from another scenario is 404. Unknown format is 400;
missing contract is 404; blocked output is 422 with `error.details.diagnostics`;
results exceeding the configured body-size limit are 413. OpenAPI JSON preserves
the saved contract bytes; AsyncAPI is generated from saved event definitions.
JSON and YAML retain schema values and numeric precision.
Images are exported locally from the browser. Markdown and self-contained HTML
include the saved diagram, scenario metadata, participants, steps, HTTP API
details and complete JSON contracts. Execution inputs are excluded. Pending API
forms and invalid contracts are warnings; invalid diagrams block documentation.
PDF is browser printing of HTML (A4 landscape, up to 200 overlapping diagram
sheets), not a server format.

`asyncapi-json` and `asyncapi-yaml` describe one Kafka application using AsyncAPI
3.0.0 and Kafka bindings 0.5.0. Document v3 stores shared definitions in
`eventModel` and event-step references in `eventBindings`. Create/save/validate
use the existing scenario routes; commands accept `set_event_model`. Schemas
and examples are JSON text strings to preserve numbers. Missing contractId or
a contract from the wrong family is 400; an unknown contract ID is 404.
Export does not connect to a broker, execute Kafka messages or contact a schema
registry. Inspect readiness diagnostics before downloading a contract.

`postman` returns a Collection v2.1 JSON file; `curl` returns a POSIX shell file.
Both use enabled HTTP requests and execution inputs in the selected revision.
Fill missing base URL variables before running the downloaded artifact. Inspect
`export-options` diagnostics first: required inputs and unsupported execution
semantics can block these formats while diagram exports remain available.

For ZIP, POST `{"items":[{"format":"mermaid"},{"format":"openapi-json",
"contractId":"api"}]}` to the archive route. It accepts 1–32 unique selections
of the server formats above. Include the session cookie, `Origin`,
`X-CSRF-Token` and `Content-Type: application/json`; the usual CSRF checks apply
even though this operation is read-only. Decode the response's `contentBase64`
into binary ZIP bytes and save them under `filename` (`application/zip`). The
response includes `scenarioId`, `revisionId`, `sourceHash` and `manifest`.
Inside the ZIP, `manifest.json` v1 lists the exact exported files, byte lengths,
SHA-256 and diagnostics in selection order. The bundle cannot restore a project
(`restorable: false`). Invalid or repeated selections return 400; a failure in
any selected export cancels the entire archive. Raw contents, ZIP bytes and the
base64 JSON response are subject to the server size limit (413). SVG/PNG are
browser ZIP options; PDF remains separate HTML printing.

## Import a spec (from a shell; the MCP tool is `import_spec`)

```bash
jq -cn --rawfile doc openapi.json '{name:"Billing API", source:"upload", document:$doc}' \
| curl -s -b "$JAR" -H "$H" -H "$O" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' \
    -d @- "$ADMIN/api/specs"
# -> 201 {"spec":{"id":12,...},"duplicate":false,"report":{"operations":57,"degraded":0,"warnings":[...]}}
```

OpenAPI 3.0/3.1, JSON or YAML (YAML is converted server-side; Swagger 2.0 is refused). Bind it: `PATCH /api/workspaces/{id}` with
`{"specId":12,"editVersion":<from GET /api/workspaces/{id}>}`.

## Upload an asset (raw body, not JSON)

```bash
curl -s -b "$JAR" -H "$H" -H "$O" -H "X-CSRF-Token: $CSRF" -H 'Content-Type: image/jpeg' \
  -X PUT --data-binary @photo.jpg "$ADMIN/api/workspaces/1/assets/photo.jpg"
# -> 201 {"name":"photo.jpg","mediaType":"image/jpeg","sizeBytes":...,"url":"http://alex.mock.local/__mocker/assets/photo.jpg"}
```

This is the path for files over ~7 MB, which the base64 MCP tool cannot carry.

## Export and import a workspace (the MCP tools are `export_workspace`, `import_workspace`, `fork_workspace`)

```bash
# the document: keys sorted, entity rows under data, the spec inlined
curl -sb "$JAR" "$ADMIN/api/workspaces/$WS/export?includeData=true&includeSpec=true" -o alex.mocker.json

# a NEW workspace from it (here or on another installation); slug uniquified from name when omitted
jq -n --slurpfile b alex.mocker.json '{bundle: $b[0], name: "alex-copy"}' |
  curl -s -X POST "$ADMIN/api/workspaces/import" -b "$JAR" -H "Origin: $ADMIN" \
    -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d @-

# a copy inside the same installation, assets and scenarios included
curl -s -X POST "$ADMIN/api/workspaces/$WS/fork" -b "$JAR" -H "Origin: $ADMIN" \
  -H "X-CSRF-Token: $CSRF" -H 'Content-Type: application/json' -d '{"name":"alex (копия)"}'
```

## Mock plane: control routes a test suite uses

A JS/TS suite should use `@yashok111/mocker-test` (`packages/mocker-test` in the repository: `mocker(url).scenario(…)`, `.fail(…)`, `.status(…)`, `.delay(…)`, `.pause(…)`, `.reset()`, `.waitForRevision(n)`; a Playwright fixture, Cypress commands) — the calls below are what it does.

```bash
W='Host: alex.mock.local'
curl -s -H "$W" $ADMIN/__mocker/health
# -> {"ok":true,"workspace":"alex","revision":7,"spec":12}

# force POST /auth/login to answer 503 until cleared
curl -s -X POST -H "$W" $ADMIN/__mocker/state \
  -d '{"target":{"method":"POST","path":"/auth/login"},"action":"status","status":503}'
# fail the next 2 requests to any route with 500, then serve normally
curl -s -X POST -H "$W" $ADMIN/__mocker/state -d '{"target":"*","action":"fail","status":500,"n":2}'
# add 800 ms to everything
curl -s -X POST -H "$W" $ADMIN/__mocker/state -d '{"target":"*","action":"delay","ms":800}'
# switch a scenario on by name, then off
curl -s -X POST -H "$W" $ADMIN/__mocker/state -d '{"scenario":"checkout-empty"}'
curl -s -X POST -H "$W" $ADMIN/__mocker/state -d '{"scenario":""}'
# list, clear
curl -s -H "$W" $ADMIN/__mocker/state
curl -s -X DELETE -H "$W" $ADMIN/__mocker/state
```

These need no login: they are meant to be called from a Playwright/Cypress
`beforeEach`. Directives are RAM-only and never bump `revision`; a scenario
switch does.

## Traffic feed for a script

```bash
curl -s -b "$JAR" -H "$H" "$ADMIN/api/workspaces/1/traffic?limit=50" | jq '.rows[] | {id,method,path,status,notes}'
curl -s -b "$JAR" -H "$H" "$ADMIN/api/workspaces/1/traffic/poll?since=$LAST_ID"
curl -s -N -b "$JAR" -H "$H" "$ADMIN/api/workspaces/1/traffic/stream"   # SSE, 15 min max
```

## MCP from curl (to check a key or list tools)

```bash
curl -s -X POST -H "$H" -H "Authorization: Bearer $MOCKER_MCP_KEY" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":1,"method":"tools/list","params":{}}' "$ADMIN/mcp" | jq '.result.tools[].name'
curl -s -X POST -H "$H" -H "Authorization: Bearer $MOCKER_MCP_KEY" \
  -H 'Content-Type: application/json' -H 'Accept: application/json, text/event-stream' \
  -d '{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"get_guide","arguments":{"topic":"overview"}}}' "$ADMIN/mcp"
```

Both `Accept` values are mandatory; without them the SDK answers 400 before
reading the body. `/mcp` reads no cookie and is a 404 when `MOCKER_MCP_KEY` is
unset.

## Client configuration for an MCP host (Claude Code, Cursor, …)

```json
{
  "mcpServers": {
    "mocker": {
      "type": "http",
      "url": "https://mocker.corp.internal/mcp",
      "headers": { "Authorization": "Bearer <MOCKER_MCP_KEY>" }
    }
  }
}
```

Claude Code: `claude mcp add --transport http mocker https://mocker.corp.internal/mcp --header "Authorization: Bearer <key>"`.

## Install the procedure into the agent's repository

Choose root-only for mock configuration, API design and backend project routing;
import-only for foundation repository import/reconciliation and pinned revision
comparison; or both packages:

```bash
npx -y -p skills skills add /path/to/mocker --skill mocker -a claude-code
npx -y -p skills skills add /path/to/mocker --skill mocker-backend-import -a claude-code
```

Run both commands for the bundle. For any host, copying the selected
`skills/mocker/` and/or `skills/mocker-backend-import/` directory into its recognized
skill directory is also sufficient. Import-only needs just its SKILL.md; it loads
backend-model/import-protocol/recovery/examples with get_guide and the selected
guideSetId, with no neighboring root files. Root-only includes the generated import
compatibility copy. All three choices select the same compatible import procedure.

Keep the MCP connection above: installing local instructions does not install
server capabilities or configure credentials. Without local skills, initialize
points to get_guide for the same procedure. Backend writes first discover
get_backend_capabilities, select exact workflow/version/set/hash and read related
topics from that set. Legacy mock-response tasks use workspace tools directly;
they do not require loading backend import references.
