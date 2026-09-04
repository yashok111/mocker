# mocker MCP tools — the catalogue

Fifty-five tools on `POST /mcp` (admin host, `Authorization: Bearer <MOCKER_MCP_KEY>`).
Every tool is an adapter over the admin HTTP API: it calls the same handlers the
UI calls, under an MCP identity, and returns what the handler returned. This
file is the catalogue; `SKILL.md` says which of them to call in which order.

Legend: `*` = required. Types are wire types. `editVersion` is the per-row
compare-and-swap token (see "Compare-and-swap" at the end). `confirmSlug` is the
workspace's exact slug (see "confirmSlug").

## Orientation

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `get_guide` | Read this documentation from the server | `topic`: `overview` \| `tools` \| `shapes` \| `cookbook` \| `http` \| `design` (default `overview`) | `topic`, `topics[]`, `markdown` | Static text; calls no admin route. `overview` is the skill body; the others are its reference files. |
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

## Operations (the spec's routes) and overrides

| tool | purpose | input | output | gotchas |
|---|---|---|---|---|
| `find_operations` | Substring search over a workspace's operations | `workspaceId*`, `query`, `limit` (20, max 100) | `operations[]`{opKey, method, path, statuses[], overridden}, returned, total, truncated | `truncated` = the LIST was capped. Take `opKey` from here verbatim. |
| `get_operation` | One operation's merged view and override shape | `workspaceId*`, `opKey*` | `operation`{opKey, method, path, statuses[], override?{overrideOn, routeOff, activeStatus, delayMs, listSize, validateReq, responses{status → mode/mediaType/hasBody/recipeCount}, **editVersion**}} | Absent `override` = no override yet → write with `editVersion: 0`. Never returns pinned bodies. `opKey` arrives percent-encoded; pass it back as is, never re-escape. |
| `set_operation_response` | Force one operation's served status, keeping everything else | `workspaceId*`, `opKey*`, `status*`, `editVersion*` | opKey, activeStatus, changed[], editVersion, `conflict?` | The cheap way to "make this route answer 404". Forces overrideOn:true, routeOff:false. |
| `set_operation_variant` | Write the WHOLE override document for one operation | `workspaceId*`, `opKey*`, `overrideOn*`, `routeOff*`, `activeStatus?`, `responses`{status → Variant}, `listSize`{min,max}?, `delayMs?`, `validateReq?`, `editVersion*` | opKey, overrideOn, routeOff, responseCount, revision, editVersion, `conflict?` | FULL REPLACEMENT: every status not resent is dropped. Variant shape — `shapes.md`. `bodyRef` is exclusive with body/bodyEncoding/mediaType. |
| `reset_operation` | Delete the override, back to what the spec says | `workspaceId*`, `opKey*` | opKey, deleted, revision? | `deleted:false` when there was nothing, not an error. No editVersion guard. |
| `preview_operation` | Render what a DRAFT override would serve, saving nothing | `workspaceId*`, `opKey*`, `draft*` (same shape as `set_operation_variant` minus editVersion), `status` (string), `query`, `headers`, `body`, `pathParams` | status, statusSource, mediaType, body/encoding or noBody/routeOff/refused, schemaPatchApplied, recipesBound, delayMs, shadowedBy | Ignores live session directives. Refusals are named: `custom_endpoint_wins`, `invalid_draft`, `no_spec`, `operation_not_found`, `missing_path_param`, `resource_serves`. |
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
| `create_endpoint` | Create an endpoint at a (method, path) nothing serves | `workspaceId*`, `method*`, `path*`, `status` (200), `body`, `mediaType`, `bodyRef`, `kind` (`http` \| `sse` \| `ws`), `stream`, `schema` (inline JSON Schema the response is GENERATED from when no body is pinned; `$ref` into the bound spec allowed), `reqSchema`, `operation` ({summary, description, tags, operationId, deprecated, parameters}) | `endpoint` | 409 if an override or endpoint already sits at the same (method, path); 409 `operation_id_taken` if the operationId is the spec's or another row's. 400 `schema_ref_unresolved`: a `$ref` the bound spec lacks (ANY `$ref` with no spec bound) — never stored dangling. `sse`/`ws` require GET, no status/body/schema; `stream` document — `shapes.md`. |
| `update_endpoint` | Replace one endpoint's whole definition | `workspaceId*`, `endpointId*`, `method*`, `path*`, `overrideOn?`, `routeOff?`, `activeStatus*`, `responses`, `listSize?`, `delayMs?`, `kind`, `stream`, `reqSchema`, `operation`, `editVersion*` | `endpoint`, `conflict?` | FULL REPLACEMENT — `reqSchema`, `operation` and each `responses[status].schema` included: omitted means cleared. `activeStatus` is required (0 would be written literally). Omitted `kind` = `http`: resend `sse`/`ws` and `stream` or the write is refused. Same `$ref` and operationId refusals as `create_endpoint`. |
| `delete_endpoint` | Delete one endpoint | `workspaceId*`, `endpointId*` | endpointId, deleted | Custom endpoints ARE in checkpoints (the config snapshot); a rollback restores them. |
| `preview_endpoint` | Lay out the first ≤ 50 frames a stream DRAFT would send | `workspaceId*`, `method*`, `path*`, `kind*`, `stream*` | kind, frames[]{atMs, event?, data}, truncated, maxBytesPerSec | Validated exactly as `create_endpoint`. `maxBytesPerSec` is the amplifier estimate — read it before saving a loop. |

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
