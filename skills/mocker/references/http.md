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
