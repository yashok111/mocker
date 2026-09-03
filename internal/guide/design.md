# Designing an API in mocker — from a brief to a contract

This is the workflow DESIGN §34 describes: a frontend developer or systems
analyst designs an API here, sees it SERVING while they design it, and
hands the backend team one OpenAPI document. Every tool below already
exists; the only new one is `export_openapi`.

The whole idea in one line: **a design is a base plus a delta**, and
mocker's four layers already are exactly that. The base is a spec you
imported (or nothing at all). The delta is what you author on the
workspace: new operations, changed schemas, examples, removals. The
export merges the two into one document.

## The loop

1. **Take a base, or none.** `import_spec {name, document}` with an
   existing API's file (JSON or YAML), then `create_workspace {name, slug,
   specId}`. With nothing to start from, create the workspace with no
   `specId` at all — the export is then an empty OpenAPI 3.1 skeleton plus
   whatever you write, and generation still works.
2. **Add an operation.** `create_endpoint {workspaceId, method, path,
   status, schema, reqSchema, operation}`. `schema` is an inline JSON
   Schema and the response is GENERATED from it — under the workspace's
   seed, with recipes and `ref` — so the frontend can call the route the
   moment you save it. `operation` carries what a contract needs and a
   mock never did: `{summary, description, tags, operationId, deprecated,
   parameters}`.
3. **Reuse the base's types.** A `$ref` into the bound spec's components
   is allowed inside any of those schemas:
   `{"$ref": "#/components/schemas/User"}`. It must resolve when you write
   it — a pointer the spec does not have is refused
   (`400 schema_ref_unresolved`), and with no spec bound any `$ref` is
   refused, because there is nothing to resolve against.
4. **Change an existing operation.** `set_operation_variant` with a
   `schemaPatch` (add/remove/replace over the resolved response schema)
   changes the shape; a pinned body becomes the operation's example;
   `routeOff` proposes a removal. Do NOT send `schema` on a spec
   operation — it is refused by name (`400 schema_on_override`), because
   that operation already has a schema and `schemaPatch` is how it moves.
5. **Look at it.** `curl` the workspace `url` — that is the design
   running. `list_traffic` shows what it answered.
6. **Export.** `export_openapi {workspaceId}` → one OpenAPI 3.1 document.
   Hand it to the backend team, commit it, open it in any viewer.

## What the export does with each thing you did

| what you did | what the document says |
|---|---|
| custom endpoint at a NEW path | a new operation, with your schemas, parameters and operation fields |
| custom endpoint at a path the base already has (canonically) | that operation REPLACED — one entry, under YOUR spelling |
| `schemaPatch` on an override | the patched schema written INLINE on that response |
| pinned body (override or endpoint) | `examples` on that response |
| `routeOff` | `deprecated: true` — never a deletion |
| endpoint with `overrideOn: false` | nothing: a switched-off row is not a contract |
| `kind: "sse"` | a `GET` answering `text/event-stream` |
| `kind: "ws"` | a `GET` with `x-websocket: true` and a `101` |
| everything else in the workspace | nothing — scenarios, entity rows, assets and session directives are not contract |

`info.version` gets `-draft.<revision>`, so two exports of different
states are distinguishable and an earlier draft suffix is replaced, never
stacked.

## Accepting the design as the next base

The base is never edited in place. When the design is agreed:

1. `export_openapi` → the document.
2. `import_spec {name, document}` → a new spec id.
3. `update_workspace_settings {workspaceId, specId}` → the workspace now serves the
   design AS its base.
4. `get_workspace_drift` → it names every delta row that is now
   redundant: each custom endpoint shadows the operation it became, and an
   override whose operation the export re-spelled is reported orphaned.
5. **Delete those rows** (`delete_endpoint`, `reset_operation`).
   This step is not optional: a `schemaPatch` applied a SECOND time over a
   base that already carries the patched schema fails to apply, and that
   variant then serves unpatched — the design would silently stop matching
   the contract.

After that the workspace is a clean delta over the new base, and the next
round of design starts from step 2.

## What this does not do

- **No request validation.** `reqSchema` is exported as `requestBody` and
  is never enforced on an incoming request; the mock accepts what it is
  sent.
- **No schema editor.** Schemas are JSON documents you write (or an agent
  writes); the panel renders the contract read-only.
- **No shared components from your own rows.** A custom endpoint's schema
  is inline. Two rows with the same shape carry two copies.
- **No review or comments.** The export is a file: put it in git, review
  it there.
