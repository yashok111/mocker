# mocker MCP tools — the catalogue

Tools on `POST /mcp` (admin host, `Authorization: Bearer <MOCKER_MCP_KEY>`).
Every tool is an adapter over the admin HTTP API: it calls the same handlers the
UI calls, under an MCP identity, and returns what the handler returned. This
file is the catalogue; `SKILL.md` says which of them to call in which order.

Legend: `*` = required. Types are wire types. `editVersion` is the per-row
compare-and-swap token (see "Compare-and-swap" at the end). `confirmSlug` is the
workspace's exact slug (see "confirmSlug").

## Operation identifiers

Workspace `opKey` from `find_operations` addresses a mock route. Sequence
`operationKey` is the value of `x-mocker-canvas-operation-id` in the pinned
`contracts[].document`. For a new linked contract, parse the `draft.document`
returned by `create_api_design` or `get_api_design`. See the operation-binding
recipe in `design.md`. `bind_operation` reuses an existing operation;
`create_operation` creates a new method/path.

## Orientation

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `get_guide` | Read this documentation from the server | `topic`: `overview` \| `tools` \| `shapes` \| `cookbook` \| `http` \| `design` \| `functions` (default `overview`) | `topic`, `topics[]`, `markdown` | Static text; calls no admin route. `overview` is the skill body; the others are its reference files. |
| `get_server_config` | This server's routing facts and effective limits | — | adminHost, baseDomain, routing, reservedPrefix, limits{maxBodyBytes, maxResponseBytes, maxAssetBytes, maxAssetsTotalBytes, maxEntities, trafficMaxBodyBytes, trafficRetention, checkpointRetention, checkpointDebounceSec, streamMaxConns, streamMaxLifetimeSec, streamMaxFrameBytes, streamSendBudgetBytes, streamPingSec, streamFrameTimeoutSec, streamTrafficFrames} | Read from the process's own config; calls no route. Read once per session before sizing a document, a frame or a family — the answer to "why 413". |

## Workspaces and specs

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_workspaces` | Every workspace with its public URL | — | `workspaces[]`: id, slug, name, url, basePath, specId?, revision | Read `slug` here for `confirmSlug`. |
| `get_workspace` | One workspace: identity, URL, shaping settings | `workspaceId*` | `workspace`{id, slug, name, url, basePath, specId?, scenarioId?, revision, seed, listSize, nullRate, envelope?, validateRequests, delayMs, **editVersion**} | The read that must precede `update_workspace_settings`. Omits identity/auth/cors/notFoundBody. |
| `create_workspace` | Create a workspace, optionally bound to a spec | `name*`, `slug`, `specId?` | id, slug, url | Not idempotent: a retry after a timeout makes a SECOND workspace. `list_workspaces` first. The slug is uniquified silently. |
| `update_workspace_settings` | Rename, attach a spec, replace the whole settings object | `workspaceId*`, `name`, `specId?`, `settings`{seed*, basePath*, basePathValues*, listSize*, nullRate*, envelope*, identity*, auth*, cors*, validateRequests*, delayMs*, notFoundBody}, `editVersion*` | id, slug, name, specId?, settings, revision, editVersion, `conflict?` | `settings` REPLACES wholesale: an omitted subfield (including `auth.signingKey`) is wiped. Read `get_workspace`, edit, resend everything. `specId` attaches, never detaches. |
| `delete_workspace` | Delete a workspace and everything under it | `workspaceId*`, `confirmSlug*` | workspaceId, deleted | Cascades: overrides, endpoints, scenarios, traffic, checkpoints, assets. No undo — `export_workspace` first if in doubt. |
| `list_specs` | Imported specs, optionally with one spec's import report | `specId?` | `specs[]`: id, name, version, format, basePath (+ operations/degraded/warnings on the asked spec) | Report counts attach only to the row matching `specId`. |
| `get_spec` | One spec's metadata | `specId*` | `spec`{id, name, version, format, source, sourceRef?, basePath, hash, createdAt} | Never the document body. |
| `list_spec_operations` | Page a spec's declared operations, workspace-independent | `specId*`, `limit` (100, max 500), `offset` | `operations[]`{id, method, path, canonicalPath, operationId?, summary?, tag?, parseError?}, hasMore | No total: page until `hasMore` is false. For the merged state use `find_operations`. |
| `probe_workspace` | Dial the workspace's own `{prefix}/health` from the server | `workspaceId*` | kind (`ok` \| `wrong-workspace` \| `http-error` \| `timeout` \| `network-error`), status?, workspace?, revision?, message? | A target failure is inside the 200 body via `kind`, never a tool error. |

| `import_spec` | Import an OpenAPI document (3.0/3.1, JSON or YAML text) as a new spec | `name*`, `document*` (the whole file as one string) | spec{id, name, version, format, basePath, hash}, duplicate, report{operations, degraded, warnings[]} | Deduplicated by byte hash: the same bytes again answer the existing spec with `duplicate: true` — safe to retry. Swagger 2.0 refused by name. Ceiling `MOCKER_MAX_BODY`. Binds to no workspace by itself. |

`DELETE /api/specs/{id}` stays without a tool: it cascades across every
workspace bound to the spec.

## Export, import, fork (a workspace as one portable document)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `export_workspace` | The whole configuration layer as one key-sorted JSON document (mockerBundle v6; v5 still imports, v4 is refused by name) | `workspaceId*`, `includeData` (entity rows under `data`), `includeSpec` (the spec's bytes under `spec.inline`) | `document` — the bundle | Carries settings, overrides, custom endpoints, resources, decisions; never assets, scenarios, checkpoints, traffic. 413 `export_too_large` when the rows exceed the snapshot budget: export without `includeData`. Save it in git next to the tests. |
| `import_workspace` | A NEW workspace from a document | `bundle*` (the document as an object), `name`, `slug`, `specId?` | workspace{id, slug, url, …}, specId?, specCreated, entitiesRestored | Spec resolved in order: `specId`; the document's `spec.hash` already imported here; `spec.inline` imported now (dedup by hash); none. 409 `spec_not_found` (details `{hash, name}`) when a hash resolves to nothing and there is no inline copy — `import_spec` it or pass `specId`. 400 `invalid_bundle` names the entry and field. Not idempotent (`list_workspaces` before a retry). Starts with a `manual` checkpoint «импорт». |
| `fork_workspace` | A copy inside this installation | `workspaceId*`, `name`, `slug`, `includeData` (default true) | workspace{id, slug, url, …} | Copies configuration, scenarios (the active one stays active), assets and — unless `includeData:false` — entity rows. Not checkpoints, not traffic. The source is untouched (no revision bump). `forkedFrom` on the copy. Not idempotent. |
| `export_openapi` | The workspace as ONE OpenAPI 3.1 document — the design's deliverable (DESIGN §34.4) | `workspaceId*` | `document` — the OpenAPI document as ONE JSON STRING, exactly the bytes served (pass it to `import_spec` unchanged) | Base = the bound spec (an empty 3.1 skeleton when none); delta = custom endpoints (a new operation, or the base operation REPLACED at an equal canonical shape), `schemaPatch` written inline, pinned bodies as `examples`, `routeOff` as `deprecated: true` (never deleted), `overrideOn: false` rows omitted, `sse`/`ws` rows as GET operations. `info.version` ends in `-draft.<revision>`. Re-imports through `import_spec`; the accept step and its non-optional cleanup — `design.md`. |

## HTTP proxy and recorded responses

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `get_workspace_proxy` | Read local proxy controls and allowed origins | `workspaceId`* | `config`, `allowedOrigins[]` | `config.version` is independent of workspace `editVersion`. |
| `set_workspace_proxy` | Replace local proxy controls | `workspaceId`*, `config`* | `config`, `allowedOrigins[]` | Echo version. Full config: `mode`, `upstream`, `timeoutSeconds`, `forwardAuth`, `forwardCookies`, `overwrite`, `captureEntities`, `operations`. |
| `list_proxy_recordings` | Inspect recorded redacted JSON responses | `workspaceId`* | `items[]` with id/method/path/status/contentType/bodyText/redacted/timestamps | `bodyText` preserves exact JSON numbers. Up to 500 responses / 32 MiB; no request credentials, query values or bodies. |
| `delete_proxy_recording` | Delete one response | `workspaceId`*, `recordingId`*, `version`* | `ok` | Advances config version; reload before another mutation. |
| `clear_proxy_recordings` | Clear all recorded responses | `workspaceId`*, `version`*, `confirmSlug`* | `ok` | Does not delete captured entity rows. |

Modes: `off`, `passthrough`, `record`, `replay`; replay never calls upstream.
`operations` maps `METHOD /relative/path/{parameter}` to `default`, `mock` or
`proxy`. A proxy rule in off mode enables passthrough; a mock rule always stays
local. `overwrite` is `first` or `last`; timeout is 1..120 seconds. Forwarding
credentials/cookies and entity capture are explicit boolean options. Mocker's
own session and CSRF credentials are always stripped. Only exact origins in
`MOCKER_PROXY_ALLOWLIST` can receive network traffic. Saved responses match the
request path, query, body and end-to-end headers, including application credentials.
Authentication and non-JSON responses are not recorded. Replay includes status,
Content-Type and the redacted body. Entity capture upserts successful GETs into
confirmed families and can stop at existing storage limits; check the mock
response's `X-Mocker-Entities-Imported` and `X-Mocker-Entities-Result` headers.
These live controls and recordings do not travel in workspace exports/forks or
scenario snapshots and are not used by immutable scenario execution.

## Operations (the spec's routes) and overrides

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `find_operations` | Substring search over a workspace's operations | `workspaceId*`, `query`, `limit` (20, max 100) | `operations[]`{opKey, method, path, statuses[], overridden}, returned, total, truncated | `truncated` = the LIST was capped. Take `opKey` from here verbatim. |
| `get_operation` | One operation's merged view and override shape | `workspaceId*`, `opKey*` | `operation`{opKey, method, path, statuses[], override?{overrideOn, routeOff, activeStatus, delayMs, listSize, validateReq, responses{status → mode/mediaType/hasBody/recipeCount/function}, **editVersion**}} | Absent `override` = no override yet → write with `editVersion: 0`. Never returns pinned bodies. `opKey` arrives percent-encoded; pass it back as is, never re-escape. |
| `set_operation_response` | Force one operation's served status, keeping everything else | `workspaceId*`, `opKey*`, `status*`, `editVersion*` | opKey, activeStatus, changed[], editVersion, `conflict?` | The cheap way to "make this route answer 404". Forces overrideOn:true, routeOff:false. |
| `set_operation_variant` | Write the WHOLE override document for one operation | `workspaceId*`, `opKey*`, `overrideOn*`, `routeOff*`, `activeStatus?`, `responses`{status → Variant}, `listSize`{min,max}?, `delayMs?`, `validateReq?`, `editVersion*` | opKey, overrideOn, routeOff, responseCount, revision, editVersion, `conflict?` | FULL REPLACEMENT: every status not resent is dropped. Variant shape — `shapes.md`. `bodyRef` is exclusive with body/bodyEncoding/mediaType, and so is `function` (Lua producing the response — `functions.md`), which additionally excludes recipes and schemaPatch and is compiled at write time. |
| `reset_operation` | Delete the override, back to what the spec says | `workspaceId*`, `opKey*` | opKey, deleted, revision? | `deleted:false` when there was nothing, not an error. No editVersion guard. |
| `preview_operation` | Render what a DRAFT override would serve, saving nothing | `workspaceId*`, `opKey*`, `draft*` (same shape as `set_operation_variant` minus editVersion), `status` (string), `query`, `headers`, `body`, `pathParams` | status, statusSource, mediaType, body/encoding or noBody/routeOff/refused, schemaPatchApplied, recipesBound, delayMs, shadowedBy | Ignores live session directives. Refusals are named: `custom_endpoint_wins`, `invalid_draft`, `no_spec`, `operation_not_found`, `missing_path_param`, `resource_serves`. A draft's `function` RUNS, with no workspace behind it (`mock.*` answers `no_host`); a failure comes back as `notes: function_failed`/`function_timeout` with `noBody`, never as a non-200 on the route. |
| `get_auth_preset` | Propose auth recipe bindings for the spec's login/refresh routes | `workspaceId*` | bindings[]{method, path, status, dataPath, recipe, reason}, schemes[], authPaths[], notes[], sampleJwt, **editVersions**{opKey → n} | Writes nothing. Forward `editVersions` (unedited or narrowed) into `apply_auth_preset`. |
| `apply_auth_preset` | Write exactly the given auth bindings | `workspaceId*`, `bindings*`, `editVersions*` | applied, revision, editVersions, `conflict?`{staleVersions} | All-or-nothing. Every opKey the bindings touch needs an entry in `editVersions`. |

## Session layer (RAM, never persisted, never bumps `revision`)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `get_session_directive` | Directives in force right now | `workspaceId*` | `directives[]`{target{all/method/path}, action, status, ms, once, n, setAt} | Lost on restart. |
| `set_session_directive` | Force a status, fail N times, delay, pause; or clear all, or clear one target | `workspaceId*`, `clearAll`, `clear`, `all`, `method`, `path`, `action`, `status`, `ms`, `once`, `n` | directives[], cleared? | Target = `all:true` or `method`+`path`. `clearAll` drops EVERY directive and ignores the other fields. `clear: true` with a target drops that target's directives (every action, or only `action` when given) and releases a pause parked on it. Actions and limits — `shapes.md`. The same thing an anonymous test can do with `POST`/`DELETE {prefix}/state`. |

## Custom endpoints (routes the spec does not have, including SSE and WebSocket)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_endpoints` | Every custom endpoint and its response shapes | `workspaceId*` | `endpoints[]`{id, method, path, canonicalPath, overrideOn, routeOff, activeStatus, responses{shape}, kind, stream?, **editVersion**} | The read preceding `update_endpoint`. |
| `create_endpoint` | Create an endpoint at a (method, path) nothing serves | `workspaceId*`, `method*`, `path*`, `status` (200), `body`, `mediaType`, `bodyRef`, `function` (Lua producing the response — `functions.md`; exclusive with body/bodyRef/mediaType, refused on `sse`/`ws`), `kind` (`http` \| `sse` \| `ws`), `stream`, `schema` (inline JSON Schema the response is GENERATED from when no body is pinned; `$ref` into the bound spec allowed), `reqSchema`, `operation` ({summary, description, tags, operationId, deprecated, parameters}) | `endpoint` | 409 if an override or endpoint already sits at the same (method, path); 409 `operation_id_taken` if the operationId is the spec's or another row's. 400 `schema_ref_unresolved`: a `$ref` the bound spec lacks (ANY `$ref` with no spec bound) — never stored dangling. `sse`/`ws` require GET, no status/body/schema/function; `stream` document — `shapes.md`, whose `tick.lua` and `onFrame` hooks are compiled here too. |
| `update_endpoint` | Replace one endpoint's whole definition | `workspaceId*`, `endpointId*`, `method*`, `path*`, `overrideOn?`, `routeOff?`, `activeStatus*`, `responses`, `listSize?`, `delayMs?`, `kind`, `stream`, `reqSchema`, `operation`, `editVersion*` | `endpoint`, `conflict?` | FULL REPLACEMENT — `reqSchema`, `operation` and each `responses[status].schema` included: omitted means cleared. `activeStatus` is required (0 would be written literally). Omitted `kind` = `http`: resend `sse`/`ws` and `stream` or the write is refused. Same `$ref` and operationId refusals as `create_endpoint`; a variant's `function` and a stream's `tick.lua`/`onFrame` are compiled here too. |
| `delete_endpoint` | Delete one endpoint | `workspaceId*`, `endpointId*` | endpointId, deleted | Custom endpoints ARE in checkpoints (the config snapshot); a rollback restores them. |
| `preview_endpoint` | Lay out the first ≤ 50 frames a stream DRAFT would send | `workspaceId*`, `method*`, `path*`, `kind*`, `stream*` | kind, frames[]{atMs, event?, data, notRun?}, truncated, maxBytesPerSec, nominalRate, rules, echo | Validated exactly as `create_endpoint`. `maxBytesPerSec` is the amplifier estimate — read it before saving a loop. A `tick.lua` draft really RUNS here (with no workspace behind it, so `mock.*` answers `no_host`) under one 10 s budget for the whole lay-out: past it a frame comes back `notRun: true` with no data, keeping its place, and `nominalRate` is true because the rate is then a sample and not a bound. |

## Traffic (what the mock actually served)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_traffic` | Recent requests newest first; one row with bodies | `workspaceId*`, `limit` (100, max 500), `trafficId?`, `since?` | rows[]{id, method, path, status, durationMs, matchedKind, matchedId?, truncated, notes[]}, hasMore, lastId | Bodies ONLY with `trafficId`. `since: lastId` pages forward (oldest first). Row `truncated` = that body was cut. `notes[]` carry `ref_unresolved`, `asset_missing`, `stream:sse,frames:N`, … |
| `override_from_traffic` | Pin one observed response onto the operation it matched | `workspaceId*`, `trafficId*` | opKey, status, revision | Refuses truncated, redacted or unmatched rows. |
| `endpoint_from_traffic` | Turn one observed request (typically a 404) into a custom endpoint | `workspaceId*`, `trafficId*` | id, method, path, revision | A 404 becomes a pinned 200 with `{}`; other statuses are kept. |
| `clear_traffic` | Delete every recorded row | `workspaceId*`, `confirmSlug*` | deleted | No checkpoint covers traffic. |
| `get_stream_stats` | Process-wide health of the admin traffic feed | — | open, cap, refusedCap, refusedUnsupported, coalescedNudges, byWorkspace[] | Not workspace-scoped. The HTTP route also reports `mock` (the mock plane's registry); the tool's declared output does not carry it — `list_stream_connections` has that plane's `open`/`cap`. |

## Scenarios (named snapshots of the workspace layer)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_scenarios` | Saved scenarios and which one is active | `workspaceId*` | scenarios[]{id, name, createdAt, isActive} | No editVersion here — `get_scenario` has it. |
| `get_scenario` | One snapshot's settings and override list | `workspaceId*`, `scenarioId*` | scenario{…settings…, overrides[] (≤ 50), overridesTruncated, **editVersion**} | Shapes only, never pinned bodies. |
| `create_scenario` | Snapshot the workspace NOW under a name, or clone another | `workspaceId*`, `name*`, `from?` | scenario | Without `from`: 409 while another scenario is active — deactivate first. Custom endpoints are NOT in a scenario. |
| `rename_scenario` | Rename | `workspaceId*`, `scenarioId*`, `name*`, `editVersion*` | scenario, conflict? | Breaks an external test that switches by the old name through `POST {prefix}/state`. |
| `activate_scenario` / `deactivate_scenario` | Switch the layer on or off | `workspaceId*`, `scenarioId*` / `workspaceId*` | revision | basePath, CORS, notFoundBody and basePathValues stay the workspace's own. Re-activating the active one is a no-op. |
| `delete_scenario` | Delete one | `workspaceId*`, `scenarioId*`, `confirmSlug*` | deleted | No undo. |

## Sequence-canvas scenarios and execution

These are persisted design documents, separate from workspace snapshot scenarios
above. `get_guide {topic: "design"}` explains editing and the run/poll workflow.

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_design_scenarios` / `get_design_scenario` | Find a sequence and read its current document | — / `scenarioId*` | list / scenario, draft, revisions, diagnostics | Read `scenario.version` before editing and `draft.id` before running. |
| `get_design_scenario_export_options` | Check result readiness per format and contract | `scenarioId*`, `revisionId*` | revision/hash and options with targeted diagnostics | Read-only. Descriptive diagrams need no API. |
| `export_design_scenario` | Export a saved diagram, contract, HTTP requests or documentation | `scenarioId*`, `revisionId*`, `format*`, `contractId?` | content string, filename, mediaType, revision/hash, diagnostics | Formats: plantuml, mermaid, openapi-json, openapi-yaml, asyncapi-json, asyncapi-yaml, postman, curl, markdown, html. OpenAPI/AsyncAPI accept and require contractId. AsyncAPI 3.0.0 describes one Kafka application; Kafka runtime is not supported. Save content unchanged; 422 means blocked, 413 means too large. SVG/PNG and PDF printing of HTML are browser-only. Documentation includes saved contracts without execution settings; incomplete API details produce warnings. |
| `export_design_scenario_archive` | Export selected artifacts as ZIP | `scenarioId*`, `revisionId*`, `items*` | contentBase64, filename, mediaType, revision/hash, manifest v1 | Read-only. Select 1–32 unique `{format, contractId?}` items using the server formats above. Decode base64 into ZIP bytes. Exact files, SHA-256 and diagnostics are listed in manifest.json. Any failure cancels the archive. Not a restorable project; SVG/PNG/PDF remain browser features. |
| `run_design_scenario` | Run every enabled HTTP step, assertions and extraction | `scenarioId*`, `revisionId*`, `runId*`, `variables?` (string map), `name?` | full run report, initially `status: running` | Async: poll `get_design_scenario_run` until terminal. Overrides apply only to this run. Same ID and input returns the existing run; different input with same ID is409. Pruned report IDs return410 and never redispatch. |
| `get_design_scenario_run` | Read progress or the complete result | `scenarioId*`, `runId*` | report with document snapshot, resolved requests, responses, assertions, final variables and reasons | Inspect `status` and each step. A successful tool call can contain a failed run. Assertion `expectedJson`/`actualJson` are exact JSON strings; absent actual is distinct from `"null"`. |
| `list_design_scenario_runs` | Find recent runs from UI and MCP | `scenarioId*` | `runs[]` with ID, name, source, revision, status and times | Latest50, newest first; reports survive page reload and server restart. |
| `cancel_design_scenario_run` | Stop an active run | `scenarioId*`, `runId*` | current full report | Idempotent; poll until terminal if still running. Completed steps are not rolled back. |
| `get_design_scenario_data_flow` | Read binding analysis for a saved revision | `scenarioId*`, `revisionId` | bounded response/request fields, bindings and diagnostics | Read-only; defaults to current draft. |
| `analyze_design_scenario_data_flow` | Analyze unsaved bindings | `scenarioId*`, `document*` | field catalogs, bindings and diagnostics | Read-only; saves nothing and executes no requests. |
| `upsert_design_scenario_data_binding` | Create or replace a binding by ID | `scenarioId*`, `expectedVersion*`, `messageId*`, `binding*`, `summary` | updated scenario and revision | Atomic version-fenced command; reconcile conflicts. |
| `remove_design_scenario_data_binding` | Remove a binding | `scenarioId*`, `expectedVersion*`, `messageId*`, `id*`, `summary` | updated scenario and revision | Use the binding ID and the version from your latest read. |
| `execute_design_scenario_step` | Probe one resolved HTTP request | `scenarioId*`, `revisionId*`, `messageId*`, `pathParams*`, `query*`, `headers*`, `body*` | method, path, status, headers, body, duration and revision identities | Refuses steps containing data bindings. Does not run assertions or extraction. Can change mock state; do not retry a lost response blindly. Use `run_design_scenario` for a complete sequence. |

## Checkpoints (history and undo of the workspace layer)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_checkpoints` | History newest first | `workspaceId*` | checkpoints[]{id, kind (`manual` \| `auto` \| `pre-destructive`), label, createdAt, createdBy?} | `pre-destructive` rows are what rollback/reset write before acting — roll back to one to undo them. Whether a row carries entity data is not on the wire here: a `restoreData: true` rollback to one without it answers 409 `no_data_snapshot`. |
| `create_checkpoint` | Labelled snapshot now (config + entity rows) | `workspaceId*`, `label*` | checkpoint | Not idempotent. |
| `rollback_workspace` | Restore the layer to a checkpoint | `workspaceId*`, `checkpointId*`, `confirmSlug*`, `restoreData` | revision, scenarioActive, dataRestored | Overrides/endpoints/settings are restored wholesale; resource CONFIG is upsert-only (a family confirmed after the checkpoint stays). `restoreData:true` also restores entity rows (409 `no_data_snapshot` when the row has none). Writes its own `pre-destructive` checkpoint first. |
| `reset_overrides` | Delete every override and custom endpoint | `workspaceId*`, `confirmSlug*` | revision, scenarioActive, changed | Settings, resources, assets untouched. |
| `delete_checkpoint` | Delete one history row | `workspaceId*`, `checkpointId*`, `confirmSlug*` | deleted | |

## Resources (a mock that REMEMBERS writes)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_resource_suggestions` | Families the spec's shape suggests (`GET X` + `GET X/{id}`) | `specId*` | suggestions[]{routeFamily, name, idField, confidence} | Spec-scoped. |
| `list_resources` | Families with decision state and row counts | `workspaceId*` | families[]{routeFamily, name, decision (null \| confirmed \| declined), resourceId?, idField?, writeForm?, entityCount?, byBaseScope[]} | Orphans (confirmed under an earlier spec) are included. |
| `decide_resource` | Confirm or decline one family | `workspaceId*`, `routeFamily*`, `state*` (`confirmed` \| `declined`), `confirmSlug*` | family | Confirm populates `listSize` rows per scope from the generator. Declining a CONFIRMED family deletes all its rows. A nested family needs its parent confirmed first (409 `parent_not_confirmed`); a parent with a confirmed child cannot be declined (409 `child_confirmed`). There is no editor: decline and re-confirm. |
| `list_resource_entities` | Page a confirmed family's rows | `workspaceId*`, `routeFamily*`, `limit` (100, max 500), `after`, `scopeKey?`, `baseScopeKey?` | rows[]{id, entityKey, scopeKey, baseScopeKey, data}, lastId | 404 `unknown_family` for suggested-but-unconfirmed, declined and unbound alike. |
| `set_resource_entity` | Create or replace ONE row by key | `workspaceId*`, `routeFamily*`, `entityKey*`, `data*` (the whole row), `scopeKey`, `baseScopeKey` | row{…}, created | `data[idField]` is overwritten with the key; a decimal key raises the family's counter so the mock's next `POST` cannot collide. Not validated against the schema (the mock's own `POST` is not either). 409 `entity_limit` over the caps. No revision bump, no auto checkpoint: `create_checkpoint` first, `rollback_workspace {restoreData: true}` to undo. |
| `delete_resource_entity` | Delete ONE row by key | `workspaceId*`, `routeFamily*`, `entityKey*`, `scopeKey`, `baseScopeKey` | entityKey, deleted | 404 `entity_not_found`. No `confirmSlug`: one row, the same thing the mock's anonymous `DELETE X/{key}` does. |
| `reset_resource_data` | Reseed every confirmed family, or clear all rows | `workspaceId*`, `mode*` (`reseed` \| `clear`), `confirmSlug*` | changed, deleted, skipped[]{routeFamily, reason} | No pre-destructive checkpoint — `create_checkpoint` first. `skipped` reasons: `stranded`, `over_caps`, `population_failed`, `group_skipped`. |
| `rederive_suggestions` | Re-run family derivation over the stored spec | `specId*` | changed, generation, added[], removed[] | Spec-scoped: every bound workspace sees the new generation. |
| `get_workspace_drift` | Overrides, families and endpoints the bound spec no longer answers for | `workspaceId*` | hasDrift, orphanedOverrides[], orphanedResources[], shadowedEndpoints[]{…, precededSpec} | Report only. The repairs are `reset_operation`, `delete_endpoint`, `decide_resource(declined)` — each one destroys. |

## Live stream connections (SSE and WebSocket on the mock plane)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_stream_connections` | Live connections of a workspace | `workspaceId*`, `endpointId?` | open, cap, connections[]{id, endpointId, path, kind, remoteAddr, openedAt, frames, pushed, skipped, framesIn} | Ids restart at 1 on a process restart: list right before you close or push. |
| `push_stream_frame` | Write one frame into one live connection | `workspaceId*`, `connectionId*`, `event?`, `data*` | connectionId, frameId | 409 `inbox_full` (not queued), 409 `connection_closed`, 504 `push_timeout` — the frame STAYS queued, do not resend blindly. `event` must be empty on `ws`. Never stored, never replayed. |
| `close_stream_connection` | Close one connection | `workspaceId*`, `connectionId*` | closed | SSE: no final frame, the browser reconnects as a NEW id. WS: close code 1001. |

## Assets (uploaded files a mock can serve)

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `upload_asset` | Store a file under a name | `workspaceId*`, `name*` (`[A-Za-z0-9._-]{1,128}`), `mediaType*`, `dataBase64*` | asset{name, mediaType, sizeBytes, sha256, url}, created | Same name REPLACES. Browser-executable types refused. Ceiling ≈ 7 MB through this tool (base64 under `MOCKER_MAX_BODY`); bigger files go by `curl -T` (`http.md`). Bumps `revision`. |
| `list_assets` | Names, sizes, URLs, caps | `workspaceId*` | assets[], totalBytes, maxAssetBytes, maxTotalBytes | Never the bytes: GET the `url`. |
| `delete_asset` | Delete one | `workspaceId*`, `name*`, `confirmSlug*` | deleted | A `bodyRef`/`asset_url` naming it keeps working and serves empty, noted `asset_missing`. |

### API change impact

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `analyze_api_design_impact` | Read-only comparison with dependency evidence | `designId*`, `fromRevisionId*`, exactly one of `document` or `toRevisionId` | designId, version, exact revision IDs and hashes, changes[], affected[], evidence[], diagnostics[], coverage, complete | JSON text stays exact; null targets and unknown fields are rejected. Current scenario drafts are read separately and carry their own revision IDs. `complete` is dependency coverage, not compatibility. No contract refresh, binding/assertion validation, save, checkpoint, publication or execution. |

See `design.md` for both input variants, compatibility rules and limits.

### API resource map

These ten tools use the same API design draft and version fence as the rest of
the designer. See `design.md` for a preview and relationship example.

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `get_api_resource_map` | Read resources, operations, relations and saved scenario usages | `designId*` | designId, version, revisionId, model, scenarioUsages[], usagesTruncated | Local Path Item operations have distinct per-path keys and optional sourcePointer. Usage source revision may be older than the API draft; scan is bounded. |
| `preview_api_resource_map` | Project changes without saving | `designId*`, `document?`, `commands` (0–100) | document, model, valid, diagnostics | `document` is a complete OpenAPI string; returned document includes generated stable operation keys. |
| `apply_api_resource_map_commands` | Write ordered atomic batch | `designId*`, `expectedVersion*`, `commands*` (1–100) | API detail | A 409 requires rereading and reconciling. |
| `upsert_api_resource` | Replace a resource annotation | `designId*`, `expectedVersion*`, `resource*` | API detail | Complete resource `{id,name,service,description,operationKeys,x,y}`. |
| `remove_api_resource` | Remove annotation and its explicit relations | `designId*`, `expectedVersion*`, `resourceId*` | API detail | Operations return to automatic grouping. |
| `assign_api_resource_operation` | Assign or restore automatic grouping | `designId*`, `expectedVersion*`, `operationKey*`, `resourceId*` | API detail | Empty resourceId restores automatic grouping. |
| `upsert_api_resource_relation` | Replace a directed relation | `designId*`, `expectedVersion*`, `relation*` | API detail | Complete relation `{id,fromResourceId,toResourceId,label}`; no self link or duplicate endpoint pair. |
| `remove_api_resource_relation` | Remove an explicit relation | `designId*`, `expectedVersion*`, `relationId*` | API detail | Does not remove either resource. |
| `move_api_resource` | Position a resource card | `designId*`, `expectedVersion*`, `resourceId*`, `x*`, `y*` | API detail | Coordinates are finite and in [-100000,100000]. |
| `auto_layout_api_resources` | Arrange all resource cards from their relationships | `designId*`, `expectedVersion*` | API detail | Saves deterministic positions; preserves resource metadata, assignments and relations. Preview first with `commands:[{kind:"auto_layout"}]`. The command takes no additional parameters. |

Copy operation keys from the map. Inline keys live in
`x-mocker-canvas-operation-id`; inherited keys live in the consumer Path Item's
`x-mocker-canvas-operation-ids` map by HTTP method. An inherited operation's
`sourcePointer` identifies its shared authored definition using a decoded JSON
Pointer. Editing that definition affects its other consumers; the UI opens source
with this warning. Map support does not add inherited-operation support to mock
runtime indexing or sequence bindings, which still enumerate literal operations.

### Visual response rules

These eight tools edit `x-mocker-response-rules` in the API draft and simulate
decision graphs. Saving or publishing this metadata does not activate it in a
live mock. See `design.md` for a complete recipe and predicate semantics.

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `list_response_rules` | List complete graphs | `designId*` | designId, version, revisionId, rules[] | Uses the API draft's version. |
| `get_response_rule` | Read one graph | `designId*`, `ruleId*` | designId, version, revisionId, rule | Read before replacing or editing. |
| `create_response_rule` | Add a graph | `designId*`, `expectedVersion*`, `rule*` | API detail | Unique stable ID; incomplete graphs may be saved. |
| `save_response_rule` | Replace a complete graph | `designId*`, `ruleId*`, `expectedVersion*`, `rule*` | API detail | Retain every node/edge you need. rule.id must match ruleId. |
| `delete_response_rule` | Remove a graph | `designId*`, `ruleId*`, `expectedVersion*` | API detail | Revision history is retained. |
| `apply_response_rule_commands` | Ordered atomic graph edits | `designId*`, `ruleId*`, `expectedVersion*`, `commands*` (≤200) | API detail | set_rule, add/update/remove_node, add/update/remove_edge, move_nodes. Failed batches save nothing. |
| `validate_response_rule` | Check selected graph and binding | `designId*`, `ruleId*`, `document?` | valid, diagnostics[], diagnosticsTruncated, source | Exact proposed document or saved draft. No writes. |
| `simulate_response_rule` | Evaluate an explicit sample request | `designId*`, `ruleId*`, `document?`, `request*` | validation, source, inputHash, outcome, trace[], terminalNodeId?, totalDelayMs?, response?, results?, entities? | Ordered query/header/path rows, optional exact bodyJSON and isolated entity fixtures. Results are exact JSON text; final fixture rows are returned. Every run starts fresh without sleeping, HTTP or workspace/session effects. Fallback has no concrete response. |

Every write fences the whole API version; reread and reconcile a 409. Evaluation
allows an unsaved rule ID when `document` contains that rule. Explicit null or
empty document is invalid. Request data and traces are not persisted.

### Executable response copies

| Tool | Purpose | Required parameters | Returns |
| --- | --- | --- | --- |
| `get_response_rule_execution` | List applied copies and current/outdated/missing authoring status | `designId` | designId, version, revisionId, rules[] |
| `apply_response_rule` | Copy saved valid graph into draft HTTP execution | `designId`, `ruleId`, `expectedVersion` | API detail |
| `unapply_response_rule` | Remove execution copy, retaining authoring and published revision | `designId`, `ruleId`, `expectedVersion` | API detail |

### State diagram execution

| Tool | Action | Inputs | Result |
|---|---|---|---|
| `get_state_diagram_execution` | List frozen applied copies and current/outdated/missing status | `designId*` | designId, version, revisionId, diagrams[] with entity and operationCount |
| `apply_state_diagram` | Copy a valid saved diagram into draft HTTP execution | `designId*`, `diagramId*`, `expectedVersion*` | API detail |
| `unapply_state_diagram` | Remove a copy, including an orphan, retaining stored entities | `designId*`, `diagramId*`, `expectedVersion*` | API detail |

Use `list_state_diagrams` and `get_state_diagram` before editing. Create/save
requires a complete diagram and whole-API `expectedVersion`.
`apply_state_diagram_commands` accepts settings with `entity` or
`clearEntity:true`, plus the existing state/transition commands. State `value`
is the persisted business string; omission uses its graph ID. Authoring saves
do not replace applied copies. `validate_state_diagram` and
`simulate_state_diagram` remain pure. Reapply explicitly after source edits;
review/publication updates the published HTTP mock. See `design` for binding,
admission, exact guards and data isolation rules.

These actions use whole-API CAS. Save source edits first; reapply to update the
executable copy. Removing an authoring graph does not remove its execution copy.
Draft behavior changes immediately; published behavior changes through existing
UI review/publication. Read `design.md` for routing/layer priorities, live input
capture differences, real delays and immutable copy/rollback semantics.

## confirmSlug

Nine destructive tools take `confirmSlug`: `delete_workspace`, `clear_traffic`,
`delete_checkpoint`, `delete_scenario`, `rollback_workspace`, `reset_overrides`,
`decide_resource`, `reset_resource_data`, `delete_asset`. The value is the exact,
case-sensitive slug the workspace has RIGHT NOW (`get_workspace` /
`list_workspaces`); a mismatch refuses the call and changes nothing, and the
refusal does not reveal the expected slug. It exists because on a flat tool
surface a wrong `workspaceId` is a SUCCESSFUL call against the wrong workspace.

## Compare-and-swap (`editVersion`)

Five whole-object writes carry `editVersion`, the row's version as the matching
read returned it; the server refuses a stale one with 409 `edit_conflict`, which
the tool projects into a `conflict` field (`{gone, document}`, the document being
the current row) instead of a tool error, so the caller re-reads, re-applies its
intent and resends:

| write | read it from | 0 legal? |
|---|---|---|
| `set_operation_response` | `get_operation` | yes — 0 means "no override row yet" |
| `set_operation_variant` | `get_operation` | yes — same |
| `update_workspace_settings` | `get_workspace` | no |
| `update_endpoint` | `list_endpoints` | no |
| `rename_scenario` | `get_scenario` | no |

`apply_auth_preset` takes the plural `editVersions` (opKey → n) from
`get_auth_preset`, all-or-nothing.

## How errors reach you

A 4xx from the admin plane becomes a tool error `admin API returned <status>:
<message>` with the handler's own message verbatim — read it, it names the
field or the rule. A 5xx is `admin API returned <status>` with nothing more.
Exceptions: 409 `edit_conflict` → the `conflict` field above; `push_stream_frame`'s
504 keeps its message.


### Sequence branch test suggestions

| Tool | Purpose | Parameters |
| --- | --- | --- |
| `suggest_design_scenario_tests` | Read-only initial-variable proposals for unobserved alt/opt/loop paths; returns cases, expected targets, unresolved reasons and search limits | `scenarioId*`, `revisionId` (default: current draft) |

Run a returned case through `run_design_scenario` with its revisionId, name,
variables and a fresh unique runId. Poll and compare actual controlFlow with
targets; reread coverage. The preview does not execute HTTP or change coverage.
See `design.md` for limits and the complete workflow.

## Backend saved views

Select inspect12 for source Flow or database7 for source/proposal Database in one
verified guideSetId/manifestHash. Require backend-saved-views and saved-view-v1;
import8 remains the source import owner. Saving presentation requires an explicit
request and does not import/apply/execute source, SQL or migrations.

| Tool | Purpose | Input |
|---|---|---|
| `list_backend_saved_views` | Current kind/name/version summaries; stable ID pagination | `projectId*`, `kind` flow/database, `limit`1–100, `cursor` |
| `create_backend_saved_view` | New immutable target/kind binding and version1 | `projectId*`, `name*`, `target*`, full `state*`, retained `idempotencyKey*` |
| `get_backend_saved_view` | Exact immutable saved version, or resolve latest once | `projectId*`, `viewId*`, positive exact `version` |
| `save_backend_saved_view` | Append presentation with CAS; exact retry after uncertain reply | `projectId*`, `viewId*`, `name*`, full `state*`, `expectedVersion*`, retained `idempotencyKey*` |

Reopen resolves saved target/state before model reads. Share viewId/viewVersion;
failed saved pins never fall back to heads.409 preserves changes and needs
explicit reload/reconcile or save-as-new. See backend-flow-reference under
inspect12 and backend-database-reference under database7 for complete examples.

## Backend source4/5 field lineage

Import8 owns source4 field-lineage-v1 import; inspect12 owns pinned lineage reads;
database7 routes column actions to inspect12. Select compatible complete owners
in one verified guideSetId/manifestHash. Existing source1–3 operations, DB
proposals and saved-view-v1 retain their established contracts.

| Tool | Purpose | Input |
|---|---|---|
| query_backend_lineage | Read-only/idempotent explicit static dependencies | projectId*, revisionId* (source4/5), seed* (full ValueRef), direction* forward/reverse, maxDepth1–32/default8, limit1–100/default50, cursor |

Column seed needs nodeId+facetKey; port needs nodeId+collection+opaque portKey;
api_field needs nodeId. Import refs instead use nodeKey. No proposal or mixed
variant is accepted. Continue cursors only with identical request/pins. Entire
mapping cards preserve all ordered sources, destination, transform/redaction,
evidence and explicit boundaries. Zero-input unknown is not constant. Empty
results never prove no dependency. Pagination differs from global traversal
truncation and source coverage. No source code/SQL execution, latest fallback or
implicit external API association is provided; load inspect12 flow/analysis topics.

## Exact backend API artifact associations (inspect12)

Require backend-api-artifact-pins and api-artifact-pins-v1 in viewSchemaVersions.
Read backend-flow-reference under its complete inspect12 owner for all four tools:
query_backend_api_artifacts, preview_backend_api_pins, apply_backend_api_pins and
get_api_artifact_snapshot. Associations are manual, use exact decimal string API
IDs and immutable raw hashes, and never select latest implicitly.

## Saved editor projections and generic artifact pins

Select inspect12 and verify backend-editor-projections/backend-editor-artifacts-v1;
load `backend-editor-projections` from its advertised immutable set.

| Tool | Input and behavior |
|---|---|
| query_backend_artifacts | projectId, exact revisionId, artifact:{kind,id}, view sequence/states/response_rules/event_model, embeddedContractId when scenario states/rules, limit/cursor; returns frozen full selected-group rosters on every page. Read only. |
| preview_backend_artifact_pins | projectId, current baseRevisionId/expectedVersion, complete ordered commands; returns candidateHash/diff/canApply with body/context/work admission. Read only. |
| apply_backend_artifact_pins | Exact preview body plus candidateHash/idempotencyKey; full group replacement/removal with backend CAS and exact durable retry. Edits backend associations only. |
| get_design_scenario_artifact_snapshot | Exact scenarioId/revisionId strings; returns raw document/drafts, storedContentHash/documentHash and qualified status, contentHash only when verified. Read only. |

The old API-specific query/preview/apply/snapshot tools remain available. Generic
set preserves editors when the last API link is removed; explicit remove clears
both collections. Unsupported saved content remains readable without becoming
bindable. No projection/snapshot read writes owners or executes authored rules.

## Backend source5 events (inspect12)

Select complete inspect12/source5/events-service-v1 with backend-events-query;
import8 owns initial/whole-scope source import and exact4→5 extension. Load
backend-events before route/job/service-call or contextual field navigation.

| Tool | Required pins | Views/selectors | Output |
|---|---|---|---|
| query_backend_events | projectId, revisionId | routes with optional seedNodeId; jobs/service_calls with optional serviceId; limit1–100/default50,cursor | Exact hash/policy/selectors, typed route/job/service_call/boundary, source witnesses, coverage, limits/counts/truncation. Read only/idempotent. |

REST POST /api/backend-projects/{id}/events/query is queryBackendEvents. Preserve
full cursor identity and cancel/discard obsolete replies. >20000edge admission
returns pinned empty diagnostic; complete means enumeration, never delivery.
Event lineage seeds carry nodeId+endpointId+edge routeId on source5; explicit
transport mappings alone join producer/consumer fields. No job executes.

## Source synchronization, desired changes and annotations

Select sync2 and load `backend-sync` for composed source6 scopes, exact provider claims, incremental affected writes and READY candidate reads. Select change4 and load `backend-change-proposals` for full desired graph commands, preview/apply, immutable history and restore. Project2 owns `backend-annotations`: annotations are project metadata with exact text, CAS, cursor conflicts and orphan tracking. Inspect12/database7 own supported source/full inspection and explicit SavedView-v2; importCandidate supports basic graph/node/evidence/coverage/assertions only and cannot be saved. Verify each owner's complete requirements and topic hashes in the same selected guide set before following its procedure.

### Backend static analysis, rebase and ready

Select change4 and read `backend-analysis-jobs` / `backend-change-rebase` from the negotiated set. Source-to-source entry points also use sync2 for source preparation; `backend-analysis` stays inspect12-owned.

| Tool | Inputs beyond projectId | Result / constraint |
| --- | --- | --- |
| start_backend_analysis | kind diff/impact, fromRevisionId, exact target, scope, limits, observationMode none, idempotencyKey | Durable job (REST202); result is not ready yet. |
| list_backend_analysis | status?, kind?, limit?, cursor? | Stable filtered job page. |
| get_backend_analysis | jobId | Mutable job plus immutable input context/pins. |
| get_backend_analysis_results | jobId, resultVersion, section, cursor?/limit?/filters | Frozen manifest and section page; no latest fallback. |
| cancel_backend_analysis | jobId, idempotencyKey | Persisted cancellation; terminal new-key cancel409. |
| retry_backend_analysis | jobId, idempotencyKey | New job from terminal immutable input (REST202); old key replays. |
| preview_backend_change_proposal_rebase | proposalId, expectedVersion, proposalRevisionId, newBaseRevisionId, identityResolutions[], resolutions[], repairCommands[] | Read-only B/O/N conflicts, diagnostics and candidateHash. |
| apply_backend_change_proposal_rebase | Exact preview input, candidateHash, idempotencyKey | New saved draft on selected base, status draft; immutable receipt. |
| apply_backend_change_proposal_lifecycle | proposalId, expectedVersion, proposalRevisionId, action ready, report, acknowledgedGapIds[], idempotencyKey | Exact completed saved-draft impact association; runtime unverified. |

REST paths are `/api/backend-projects/{projectId}/analyses` (POST/GET), `/{jobId}` (GET), `/{jobId}/results` (GET), `/{jobId}/cancel` and `/{jobId}/retry` (POST). Proposal POST paths are `/api/backend-projects/{projectId}/change-proposals/{proposalId}/rebase-preview`, `/rebase`, `/lifecycle`. Browser REST mutations retain existing session/CSRF requirements; MCP uses its bearer-authenticated adapter to the same handlers. Preserve exact integer versions and original request bytes.
