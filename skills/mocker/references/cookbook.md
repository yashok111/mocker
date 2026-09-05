# mocker cookbook — end-to-end recipes for an agent

Each recipe is the ordered tool calls, with the reads that must precede the
writes. Names are MCP tools (`tools.md`); the same steps map one-to-one onto
the HTTP routes (`http.md`).

## 0. Orient yourself (do this once per session)

1. `get_server_config` — the effective limits (body, response, asset, entities, stream caps); `list_workspaces` — ids, slugs, URLs. Note the `slug`: every destructive call wants it as `confirmSlug`.
2. `get_workspace` — `specId`, `basePath`, `seed`, `listSize`, `scenarioId`, `editVersion`.
3. `list_specs` — is the spec you need imported? If not, `import_spec {name, document}` with the file's text (JSON or YAML) — a repeat of the same bytes answers `duplicate: true`, never a second spec.
4. `find_operations` with an empty query — the first 20 routes; raise `limit` or narrow `query`.

## 1. Stand up a workspace for a frontend

1. `list_specs` → the spec id, or `import_spec` first.
2. `create_workspace {name, slug, specId}` → `url`.
3. `probe_workspace` → `kind: "ok"` proves the host really resolves to this workspace. `wrong-workspace` means DNS or `Host` points elsewhere.
4. Hand the frontend `url` as its API base. The workspace's `basePath` was seeded from the spec's `servers[]`; change it with `update_workspace_settings` if the client prepends its own prefix.

## 2. Make login work against the mock

1. `get_auth_preset` — proposed bindings for the spec's login/refresh routes (`bindings[]`, `editVersions`).
2. Review; narrow `bindings` if you want fewer routes touched.
3. `apply_auth_preset {bindings, editVersions}`.
4. The token fields now carry a real JWT signed with `settings.auth.signingKey`, and `settings.identity` is who it says logged in. Set `settings.auth.requireHeader: true` (via `update_workspace_settings`, resending the WHOLE settings object) to get 401 on missing `Authorization`.

## 2a. Login that actually CHECKS the password (Lua)

The preset above makes login answer a real token to everyone. When a test
needs the wrong password to fail, put the branch in the endpoint itself:

1. `get_operation {opKey}` for the login route — take its `editVersion`.
2. `set_operation_variant` with the whole document and ONE function variant:

```jsonc
{
  "overrideOn": true, "routeOff": false, "activeStatus": 200,
  "responses": {
    "200": {
      "function": "if req.body.password == \"hunter2\" then\n  return 200, { token = mock.jwt({ sub = 42, role = \"admin\" }) }\nend\nreturn 401, { error = \"bad credentials\" }"
    }
  },
  "editVersion": 3
}
```

3. `preview_operation {opKey, draft, body: {"password": "hunter2"}}` before
   saving if you want to see it run — a failing draft comes back with
   `notes: function_failed`, not an error.
4. Verify on the mock plane and read `list_traffic`: the row's note says
   `function` when it ran, `function_failed` with the error's first line when
   it did not.

`mock.jwt` signs with the workspace's own `settings.auth` and answers
`nil, "auth_not_configured"` when the workspace has `alg: "none"` or no key.
The whole contract, the sandbox and the guards: `functions.md`.

## 3. Force an error state for one test

Cheapest, RAM-only, no config change:

- `set_session_directive {workspaceId, method: "POST", path: "/orders", action: "status", status: 503}` — until cleared.
- `... action: "fail", status: 500, n: 2` — the next two requests fail, then normal.
- `... action: "delay", ms: 1500` — latency.
- `... action: "pause"` — hold the request until cleared (loading-state screenshots).
- `set_session_directive {workspaceId, clearAll: true}` when done — or `{clear: true, method, path}` to drop one target and keep the rest.

From the test suite itself, without MCP: `POST <workspace url>/__mocker/state` with the same body (`http.md`).

Persistent instead: `get_operation` → `set_operation_response {status: 503, editVersion}`; undo with `reset_operation`.

## 4. Shape one response (pinned body, recipes, conditions)

1. `get_operation {opKey}` → the current document and `editVersion` (absent override → 0).
2. Optional: `preview_operation {draft, pathParams, query, headers, body}` — see what the draft would serve, nothing saved. Iterate here.
3. `set_operation_variant {overrideOn: true, routeOff: false, responses: {...}, editVersion}` — the WHOLE document; a status you do not resend is dropped.

Patterns for `responses` (shapes in `shapes.md`):

- Literal body: `{"200": {"mode": "pinned", "body": {...}}}`.
- Generated but realistic: `{"200": {"recipes": {"items[*].email": {"kind": "faker", "field": "internet.email"}, "items[*].createdAt": {"kind": "now", "offset": "-3d", "format": "iso"}}}}`.
- Conditional: `{"422": {"mode": "pinned", "when": [{"in": "body", "name": "email", "op": "equals", "value": "taken@x.io"}], "body": {...}}, "201": {...}}` — `when[]` candidates are tried in ascending status; `activeStatus` is the fallback.
- Empty list: `"listSize": {"min": 0, "max": 0}` on the document, or a `listSize` recipe on the array path.
- Bigger page: `"listSize": {"min": 50, "max": 50}`.

## 5. Add a route the spec does not have

1. `list_traffic` — a 404 row is the fastest source: `endpoint_from_traffic {trafficId}` creates a pinned 200 at that method+path.
2. Or `create_endpoint {method, path, status, body, mediaType}`.
3. Edit later with `list_endpoints` → `update_endpoint` (full replacement, `activeStatus` required, `editVersion`).

A custom endpoint wins over a spec operation at the same shape; `get_workspace_drift` lists such shadowing.

## 6. A mock that remembers (CRUD that persists)

1. `list_resources` — families the spec suggests (`GET X` + `GET X/{id}`), their decision and `writeForm`.
2. `decide_resource {routeFamily: "/users", state: "confirmed", confirmSlug}` — populates `listSize` rows.
3. Now `POST /users` on the mock creates a row `GET /users/{id}` returns; `DELETE /users/{id}` removes it (when the spec declares it). `list_resource_entities` shows the rows; `set_resource_entity {routeFamily: "/users", entityKey: "42", data: {…}}` places or rewrites one ("user 42 is blocked"), `delete_resource_entity` removes one.
4. Fresh state between tests: `create_checkpoint` first, then `reset_resource_data {mode: "reseed", confirmSlug}`; or `rollback_workspace {checkpointId, restoreData: true, confirmSlug}` to a known snapshot.
5. Cross-family consistency in GENERATED bodies: a `ref` recipe (`{"kind": "ref", "value": {"family": "/users", "property": "id"}}`) on e.g. `items[*].authorId`.

Nested: confirm `/orgs` before `/orgs/{orgId}/teams`. Declining a confirmed family deletes its rows — there is no editor, decline and reconfirm is the repair.

## 7. Scenarios: one named state per test suite

1. Bring the workspace to the wanted state with recipes 3–6.
2. `create_scenario {name: "checkout-empty"}` (refused while another scenario is active — `deactivate_scenario` first).
3. Repeat for each state; `create_scenario {name, from: <id>}` clones one.
4. In the test suite: `POST <workspace url>/__mocker/state {"scenario": "checkout-empty"}` before, `{"scenario": ""}` after. From MCP: `activate_scenario` / `deactivate_scenario`. In a JS/TS suite the same calls are `@yashok111/mocker-test` (`packages/mocker-test`): `mock.scenario("checkout-empty")`, `mock.fail("POST /orders", 503, {times: 2})`, `mock.reset()`, plus a Playwright fixture and Cypress commands — point the frontend's tests at it instead of hand-written fetches.

A scenario carries overrides and settings, not custom endpoints, not `basePath`/CORS/`notFoundBody`, not entity rows. Renaming one breaks tests that switch by the old name.

## 8. Undo

- `list_checkpoints` — `auto` rows are written before mutating admin calls (debounced), `pre-destructive` rows before rollback/reset, `manual` by `create_checkpoint`.
- `rollback_workspace {checkpointId, confirmSlug}` restores overrides, endpoints and settings wholesale; add `restoreData: true` for entity rows (needs `hasData`).
- `reset_overrides {confirmSlug}` — back to the bare spec.
- Not covered by any checkpoint: traffic, scenarios, assets, the session layer.

## 8a. Hand a workspace to a teammate, or keep it in git

- Same installation: `fork_workspace {workspaceId, name}` — a full copy with its own URL (assets, scenarios and entity rows included; `includeData: false` for the configuration alone). The source is not touched.
- Another installation, or a file next to the tests: `export_workspace {workspaceId, includeData: true, includeSpec: true}` → save `document` as `<slug>.mocker.json` (keys are sorted; diffs read). There: `import_workspace {bundle: <the document>, name}`. The spec is found by hash if already imported, else imported from `spec.inline`; without either, 409 `spec_not_found` — `import_spec` first or pass `specId`.
- An export never carries assets: re-upload them (`upload_asset`) after an import, or the traffic shows `asset_missing` on the first request.
- Both create a NEW workspace with its own slug and URL; nothing overwrites an existing one. To replace a configuration in place, use `rollback_workspace` on a checkpoint, not an import.

## 9. Streams: SSE and WebSocket

1. Draft the `stream` document (`shapes.md` §4); `preview_endpoint {method: "GET", path, kind, stream}` lays out the first frames and `maxBytesPerSec`.
2. `create_endpoint {method: "GET", path: "/events", kind: "sse", stream}` (or `kind: "ws"` with `reactive`/`echo`).
3. While a client is connected: `list_stream_connections` → `push_stream_frame {connectionId, data}` to inject one event, `close_stream_connection` to drop it.
4. The traffic row (one per connection, written at close) says `stream:sse,frames:N`.

For frames a schema cannot express, swap `tick.schema` for `tick.lua`
(`return { price = 100 + ordinal }`) — exclusive with it by name. For a
WebSocket that must branch on what the client sent, use `onFrame` instead of
`reactive`/`echo`: `return "reply", data`, `return "close", code`, or `nil`
for silence. `functions.md` §7 has both.

## 10. After a spec changed

1. A human re-imports (a new spec row) and rebinds, or `rederive_suggestions` refreshes the families of the SAME spec.
2. `get_workspace_drift` — overrides, families and endpoints the bound spec no longer answers for.
3. Repair by deletion only: `reset_operation`, `decide_resource(declined)`, `delete_endpoint` — read each row first, `create_checkpoint` before.

## 11. Debug "why did the mock answer that"

1. `list_traffic {limit: 20}` → find the row; `list_traffic {trafficId}` for headers and bodies.
2. `matchedKind`/`matchedId` say which layer answered (operation, custom endpoint, resource, no route). `notes[]` name the reasons: `ref_unresolved`, `asset_missing`, `pause_refused`, `stream:*`.
3. `get_session_directive` — a forgotten `fail`/`status` directive is the usual culprit; `clearAll`.
4. `get_workspace` — `scenarioId` set means a scenario shadows the workspace's own overrides.
5. `preview_operation` with the same query/headers/body reproduces the selection without traffic.

## 12. Pictures, PDFs, downloads

1. `upload_asset {name: "avatar.png", mediaType: "image/png", dataBase64}` (≤ ~7 MB; bigger via curl, `http.md`).
2. Serve it: `bodyRef: "asset:avatar.png"` on a pinned variant (`GET /users/{id}/avatar`), or `{"kind": "asset_url", "value": ["a.png", "b.png"]}` on `items[*].avatarUrl` to get real URLs in generated bodies.
3. `list_assets` — names, sizes, `url`, caps. `delete_asset` needs `confirmSlug`.
