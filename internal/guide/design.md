# Designing an API in mocker — drafts, review and publication

## Analyze the impact of API changes

Open **Влияние** in the API designer and click **Проанализировать**. The default
comparison uses the editor's exact current JSON text and the saved revision on
which those edits began. Apply or cancel unfinished forms first. **Ревизии**
compares two explicit saved revisions independently of local edits. Neither mode
saves a draft, creates a checkpoint, publishes, refreshes contracts or runs tests.

Agents call the same analysis with either input:

```json
{"designId":7,"fromRevisionId":45,"document":"{\"openapi\":\"3.1.0\",\"info\":{\"title\":\"Orders\",\"version\":\"2\"},\"paths\":{}}"}
```

```json
{"designId":7,"fromRevisionId":37,"toRevisionId":45}
```

Pass these to `analyze_api_design_impact`. IDs must be positive and belong to
the same API; exactly one target is required. Preserve `document` as a string,
including large numeric literals. JSON syntax errors are rejected; unresolved
references produce a partial report with diagnostics. External references are
never fetched.

Read `changes`, then follow each change's `evidence` to `affected` entities.
Evidence records its `before`/`after` side, request/response direction and ordered
reference sites. It can connect shared schemas and contract nodes to operations,
resources, state transitions and scenario messages. Deleted definitions keep
their old consumers. User-drawn resource relations do not propagate impact.
`beforeJSON`/`afterJSON` are strings of exact JSON; `"null"` differs from a missing
side. Values over 4 KiB are omitted with a corresponding truncation flag.

Compatibility is deliberately limited: disappearance of an effective HTTP
method/path or an unambiguous new required input can be `breaking`. Complex
schema, constraint and response changes are `review`. A moved editor key can
require checking bindings while the old HTTP address still exists. Compatible
presentation metadata is hidden by default; identity warnings remain visible.

`complete` describes dependency coverage within this feature's scope. It does
not certify compatibility. Read `diagnostics` and `coverage.truncatedReasons`
before concluding that no consumers were found. Limits are 500 changes, 10,000
reference sites per document, 200,000 traversal visits, 5,000 entities, 10,000
evidence records and 4 MiB of output. Scenario reads stop at 500 current drafts,
1,000 usages or 64 MiB. The graph shows at most 100 nodes for the selected change;
the list retains the returned evidence.

Scenario usages refer to **current saved scenario drafts**, read separately
from the API snapshots. Every locator includes the actual scenario revision,
contract ID, pinned API revision and `copy`/`linked` mode. A copied contract may
have been edited even when its pinned revision matches the comparison base.
Linked snapshots are pinned too; runtime separately checks stale API revisions.
The report identifies operation usage. It does not validate individual bindings,
assertions, extracts or status expectations against a replaced contract.

Use source/diff navigation to inspect the reported side. Historical and deleted
elements open in a read-only comparison. Opening a scenario shows its current
version, which may be newer than the revision named in the report.

## Choosing an operation for a sequence step

A sequence step uses the stable **operationKey** stored in the authored OpenAPI
operation's **x-mocker-canvas-operation-id** extension. Copy the value exactly;
it is an opaque string and is not required to be a UUID.

| Identifier | Read it from | Use it for |
|---|---|---|
| `opKey`, e.g. `POST%20%2Forders` | `find_operations` | Workspace tools such as `get_operation` and response overrides |
| `operationKey` | `x-mocker-canvas-operation-id` in the pinned contract | `message.operation` and the `bind_operation` command |
| `operationId`, e.g. `createOrder` | Standard OpenAPI operation | OpenAPI naming; it does not identify a sequence binding |

For a new sequence using an API project:

1. Call `create_api_design` or `get_api_design`. Parse the **returned**
   `draft.document` JSON text; it contains the stable keys assigned by the server.
   Your original input document may not contain them.
2. Select the operation by method/path and copy its extension value:

   ```js
   const authored = JSON.parse(api.draft.document);
   const operationKey = authored.paths["/orders"].post["x-mocker-canvas-operation-id"];
   const contract = {
     id: "orders-api", name: "Orders API", mode: "linked",
     source: {designId: api.design.id, revisionId: api.draft.id, version: api.design.version},
     document: authored,
   };
   const operation = {contractId: contract.id, operationKey};
   ```

3. Put `contract` into `document.contracts` and `operation` into the request
   message when calling `create_design_scenario`. For an existing message, use
   `apply_design_scenario_commands` with
   `{type:"bind_operation", messageId, contractId, operationKey}` and the current
   scenario `expectedVersion`.
4. Validate the scenario and its data flow before running it.

For an existing scenario, get the key from its **pinned** `contracts[].document`
returned by `get_design_scenario` or `get_design_scenario_revision`. If you need a
newer API revision, use `refresh_contract` first and read the updated scenario.
A stable key follows method/path renames when preserved in the authored API.

If validation says the operation is missing, check the matching contract and
extension value. Do not decode a workspace opKey or substitute OpenAPI
operationId. Use `create_operation` only when creating a new method/path;
it refuses an operation that already exists. Use `bind_operation` to reuse one.

## State diagrams: visual authoring and simulation

Open an API project and select **Состояния**. Create an empty diagram or use
**Пример заказа**. Move states on the canvas, connect their ports, or use the
labelled state/transition lists and **Добавить переход**. The inspector edits
names, initial/terminal flags, positions and transition endpoints. Every graph
edit also has a keyboard-accessible form. **Сохранить черновик** on this tab
saves the diagrams together with the API draft.

A transition optionally binds to a method/path in this API. Its guard compares
an entity-data JSON Pointer to `equalsJSON` (missing differs from JSON null).
`patchJSON` is a JSON object shallow-merged after an accepted transition;
`responseStatus` is the simulated response, not a network response. Removing or
renaming a bound API operation requires repairing the binding. An unbound
transition remains usable as a descriptive business action.

**Проверить диаграмму** reports missing initial states, dangling transitions,
outgoing transitions from terminal states, missing API operations and
unreachable states. **Начать заново** starts from the initial state and the
entered JSON object. Clicking actions replays a path and shows accepted or
blocked steps, current state and exact resulting data. The first blocked action
stops the trace without applying its patch. Editing a diagram or seed resets the
local run; late responses from an older proposal are discarded.

Simulation performs no HTTP mock calls, writes no entity data and saves no
revision. State diagrams describe and simulate lifecycle behavior only; they do
not install executable state machines in HTTP mocks.

The authored OpenAPI extension `x-mocker-state-diagrams` stores
`{formatVersion:1, diagrams:[...]}`. Existing draft history, diff, restore and
contract export preserve it. The complete-document API save validates the
extension structure too. Limits: 20 diagrams/API, 100 states and 300
transitions/diagram, 100 simulated actions and 64 KiB per JSON value/data object.

### First-class state diagram MCP tools

- `list_state_diagrams {designId}` and `get_state_diagram {designId,diagramId}`
  return the current API `version` and `revisionId` with the diagram data.
- `create_state_diagram {designId,expectedVersion,diagram}` creates one model.
  A blank diagram needs `id`, `name`, `initialStateId:""`, `states:[]` and
  `transitions:[]`. Choose a stable URL-safe ID. After a lost response read before
  retrying; create is not idempotent.
- `save_state_diagram {designId,diagramId,expectedVersion,diagram}` replaces
  only that complete diagram. `delete_state_diagram` takes the same identity and
  expected version. Other contract fields and diagrams are preserved.
- `apply_state_diagram_commands {designId,diagramId,expectedVersion,commands}`
  applies an atomic batch. Kinds: `upsert_state` with a complete `state`,
  `remove_state` with `id`, `upsert_transition` with a complete `transition`,
  `remove_transition` with `id`, and `settings` with `name` and/or
  `initialStateId`. Removing a state removes its incident transitions and clears
  its initial-state selection. Upserts replace complete objects.
- `validate_state_diagram {designId,diagramId}` checks the saved model.
- `simulate_state_diagram {designId,diagramId,dataJSON,transitionIds}` replays
  from the initial state; an empty action array inspects the initial run.
  Both evaluate tools accept optional `diagram` and `document` to evaluate an
  unsaved proposal. `document` is the full proposed OpenAPI text for bindings;
  omission resolves bindings against the current saved draft. Both tools are
  read-only despite their POST transport. UI uses the same handlers/evaluator.

All writes require the API design version, not a diagram-local version. A 409
means re-read and reconcile both API and diagram changes; never simply replace
`expectedVersion` in a stale request. Restoring a diagram's old snapshot uses the
existing `get_api_design_revision` and `restore_api_design_revision` tools and
restores that entire API revision as a new draft. Publication stays separate.

Example atomic construction after creating an empty `order` diagram:

```json
{
  "designId": 7,
  "diagramId": "order",
  "expectedVersion": 2,
  "commands": [
    {"kind":"upsert_state","state":{"id":"created","name":"Created","x":60,"y":80,"terminal":false}},
    {"kind":"upsert_state","state":{"id":"paid","name":"Paid","x":360,"y":80,"terminal":true}},
    {"kind":"settings","initialStateId":"created"},
    {"kind":"upsert_transition","transition":{"id":"pay","name":"Pay","from":"created","to":"paid","guard":{"pointer":"/allowed","equalsJSON":"true"},"patchJSON":"{\"status\":\"paid\"}","responseStatus":200}}
  ]
}
```

Then simulate with `dataJSON:"{\"allowed\":true}"` and
`transitionIds:["pay"]`. Preserve returned `dataJSON` as text when handling
numbers outside JavaScript's exact integer range.


## Branches and nested blocks

In a block inspector, choose **alt** for alternatives and edit each branch's
condition. Split a branch to add an alternative, or remove it to merge its steps
into an adjacent branch. Conditions are descriptive text. Choose a parent block
and, for an alt parent, its branch to nest a block. Removing a block also removes
its child frames; messages stay in place. Boundary messages cannot be removed
until the block or branch boundaries are adjusted. Moves that change branch
membership are rejected.

REST and MCP accept `formatVersion: 1 | 2`. Version 2 fragments retain
`fromMessageId`/`toMessageId` and add optional `parentFragmentId`,
`parentBranchId` and `branches: [{id, label, fromMessageId, toMessageId}]`.
An alt has 2–100 contiguous nonempty branches covering its range. Children must
fit their parent range or branch; sibling blocks cannot overlap. Limits are
500 blocks, 1,000 branches total and depth 16. Version 1 rejects the new fields.
Editing blocks in the canvas upgrades a copy of the draft; saved revision hashes
stay intact. MCP clients must first call `save_design_scenario_draft` with the
complete document converted to `formatVersion: 2`, including explicit parent
links, before using `upsert_fragment` commands with alt or nested blocks on an
existing v1 scenario. The server does not migrate the document automatically.
Text diagram exports use branch labels for `alt`/`else` conditions. Running a
scenario or exporting Postman/cURL remains blocked while any blocks are present.

## Start with a sequence, then get a result

In **Scenarios**, create a blank sequence or choose Login, Checkout or Error.
Add participants, insert messages before/after a selected message, add replies,
and edit labels directly on the canvas. A descriptive sequence is enough to
export a diagram; API details can be added as the design becomes concrete.

For an HTTP request, choose **Создать API для вызова**, set its method and
path, or reuse an existing operation. Add parameters, body, response schemas and
examples in the inspector. When converting requests into contracts, mocker makes
one API per receiving service. Two services may both expose `GET /status`.
Review the conversion and apply the whole batch atomically. Unknown response
status remains `default` until the analyst specifies it.

Open **Получить результат** after saving:

- PlantUML and Mermaid preserve participants, ordered messages, replies, notes,
  self-calls and nested alt/else, opt and loop blocks. Crossing blocks need correction for text
  export. Canvas colors are available in SVG/PNG.
- SVG and PNG use the full saved diagram, independently of zoom, selection and
  execution overlays. PNG uses scale 2, at most 16,384 pixels on either side and
  32 million pixels total; choose SVG for larger diagrams. Images are generated
  locally in the browser and have a 15-second rendering timeout.
- OpenAPI JSON/YAML exports the **entire selected API contract**, including
  operations outside this sequence, shared schemas, security and extensions.
  Descriptive messages are listed as omitted. Invalid bindings or unfinished API
  forms block the affected contract; they do not block diagram export. JSON is
  the saved contract text; YAML preserves numbers and scalar types. The original
  OpenAPI version is retained.
- Postman exports a Collection v2.1 with enabled HTTP requests in scenario order,
  including saved execution parameters, headers, bodies and variables. Review
  per-service base URLs before running it. Supported explicit status/JSON Pointer
  assertions and response extractions become Postman scripts. Failed checks
  stop the collection before extracting values or sending the next request.
- cURL exports a POSIX shell file with saved input values and separate base URL
  variables. It checks the expected status (2xx by default), but does not run
  JSON body assertions or extract response values;
  diagnostics explain these omissions. A later request that needs an extracted
  value blocks cURL export. Use Postman for such request chains.
- Missing required inputs, unresolved bindings and unfinished forms block HTTP
  exports. alt/opt/loop execution is not supported and blocks these formats.
- Create a mock through contract preparation, then inspect it in the execution
  panel. Export itself does not execute requests or publish an API.

The result window pins a saved revision. Polling never silently switches the
preview or download to a newer one. Resolve save conflicts and unfinished saves
before opening results, or explicitly refresh an open result to a newer saved
revision. Diagnostics point back to messages, blocks or contracts for correction.
Local legacy canvases must first use the existing server-save migration.

Agents use `get_design_scenario_export_options {scenarioId, revisionId}` followed
by `export_design_scenario {scenarioId, revisionId, format, contractId?}`.
Formats are `plantuml`, `mermaid`, `openapi-json`, `openapi-yaml`, `asyncapi-json`,
`asyncapi-yaml`, `postman`, `curl`, `markdown` and `html`. OpenAPI and AsyncAPI
accept and require `contractId`. Save the returned `content` string unchanged using `filename` and
`mediaType`. `sourceHash` identifies the immutable snapshot. SVG/PNG and PDF printing are browser
features. Markdown and HTML document participants, ordered steps, nested blocks,
HTTP bindings and complete saved JSON contracts without execution settings.
HTML includes an offline SVG and overlapping A4 landscape print sheets (up to
200). PDF uses the HTML print view and the browser Save as PDF dialog; there is
no server `pdf` format. Incomplete or invalid API details produce warnings in
documentation; an invalid diagram still blocks export. Export reads neither runtime mock state nor newer linked API revisions
and creates no resources. Oversized results return 413; blocked results return
422 with diagnostics. Importing arbitrary diagram formats remains future work.

### Kafka event contracts

Document version 3 adds `eventModel` with five arrays: `servers`, `channels`,
`messages`, `schemas` and `contracts`. Each event contract belongs to one
application participant and contains explicit `send` or `receive` operations.
Shared channels/messages/schemas are reused across producers and consumers.
Each event step may use `eventBindings: [{contractId, operationId}]`; the sender
owns a send operation and the receiver owns a receive operation. Descriptive
events may remain unbound. Existing version 1/2 revisions keep their original
content and hashes.

Use `apply_design_scenario_commands` with `set_event_model {eventModel}` to
replace the complete event model, followed by `upsert_message` commands in the
same atomic batch to bind steps. Read the current `expectedVersion` first.
The command upgrades the working document to v3. The ordinary complete-document
save also accepts v3. Store schemas in `schemaJSON` and examples in `payloadJSON`
and optional `headersJSON` strings, preserving exact JSON numbers. Schema dialect
is Draft 07; external references and other schema formats are not supported.

Export `asyncapi-json` or `asyncapi-yaml` with the event `contractId`. The result
is AsyncAPI 3.0.0 with Kafka bindings 0.5.0 for that application: topic addresses,
schemas, message keys, servers, SASL descriptions and consumer groups when supplied.
Readiness diagnostics identify incomplete or invalid dependencies. Incomplete
event definitions do not prevent diagram export. Event definitions appear in
Markdown/HTML documentation, and each ready contract can be selected in ZIP.

Kafka support covers contract authoring and export. Mocker does not connect to
Kafka, publish/consume records, create topics or contact Schema Registry.
HTTP runs and Postman/cURL export skip event steps explicitly; their success
describes only the HTTP checks. AsyncAPI import, Avro/Protobuf and Kafka runtime
are separate future capabilities.

For a ZIP bundle use `export_design_scenario_archive {scenarioId, revisionId,
items:[{format, contractId?}]}`. Select 1–32 unique server exports; OpenAPI/AsyncAPI need
one item per contract and format. Decode `contentBase64` explicitly and save it
using `filename` (`application/zip`). The read-only REST equivalent is
`POST /api/design-scenarios/{id}/revisions/{rid}/archive` with `{items}` and the
normal authentication and CSRF headers. The ZIP stores the original file bytes
plus `manifest.json` (schemaVersion 1, kind `mocker-scenario-artifacts`,
restorable false). Manifest files retain selection order and describe paths,
formats, media types, byte counts, SHA-256 and diagnostics. Any selected export
failure cancels the entire archive; raw files, ZIP and base64 JSON response all
have size limits. In the browser, ZIP also supports SVG/PNG from the same pinned
revision. PDF remains separate HTML printing. ZIP is an artifact bundle and
cannot restore a Mocker project.

## Analyst editor: the recommended workflow

The **API designer** stores a complete OpenAPI document with immutable revisions.
It has two independent mock URLs: a working draft and a stable published version.
An agent edits the draft through MCP; an analyst reviews structural changes and
the Monaco line diff, then confirms publication in the browser. A save never
changes the published mock.

1. `list_api_designs`, then `get_api_design {designId}`. To start a project,
   `create_api_design {name, document}` imports JSON/YAML. Alternatively pass
   `workspaceId` to capture an existing workspace, or neither for an empty API.
   The source workspace is left unchanged. Creation is not idempotent: inspect the
   list after a lost response before creating again.
2. `create_api_design_change_set {designId, expectedVersion, title}` opens a named
   task. Use the design's current `version`; this is separate from a workspace's
   `revision` or an operation's `editVersion`.
3. Read the complete `draft.document`, edit it and preserve every unrelated field.
   `validate_api_design {designId, document}` checks proposed JSON/YAML without
   saving. `save_api_design_draft {designId, expectedVersion, document, summary,
   changeSetId}` atomically saves the full document, history and draft mock.
   This is FULL REPLACEMENT, not a merge. Local references, path parameters and
   operation identifiers must remain valid.
4. On `409 design_conflict`, read the current state and compare both edits before
   retrying. Never just substitute the new version number into an old document:
   that would overwrite the analyst's changes.
5. `get_api_design_diff {designId}` compares the latest publication (the initial
   import before the first publication) to the draft. For historical comparisons,
   supply `fromRevisionId` and `toRevisionId`. Results contain both exact documents
   and changes with JSON pointers. `impact: review` requires human analysis; it
   does not certify compatibility. Call the draft URL to inspect generated responses.
6. `close_api_design_change_set {designId, changeSetId, expectedVersion}` finishes
   the task. `request_api_design_review {designId, expectedVersion, summary}` freezes
   a candidate and returns `reviewUrl`. Give that link to the analyst. New saves
   supersede the candidate, so publication cannot include an unseen later edit.
7. **The analyst publishes in the UI.** The shared MCP key cannot confirm
   publication. `get_api_design` reports reviews/releases and the stable published
   mock URL. Retrying a successful publication returns the same release.
8. `get_api_design_revision {designId, revisionId}` reads an immutable document for
   handover. `restore_api_design_revision {designId, revisionId, expectedVersion,
   summary}` creates a new draft from history; it does not publish or erase history.

Managed draft/published workspaces are runtime projections. Legacy endpoint,
override, spec/settings, scenario, rollback and session-control writes cannot edit
them. Use the designer tools instead. Publication includes the HTTP contract and
generated mock, not entity data, assets, Lua functions or transient session state.
The author document preserves OpenAPI fields; runtime normalization is separate.
Names a client reports for an agent are not verified identities; history records
the server-derived UI/MCP source.

## Schema model: components, fields and references

`get_schema_model {designId}` returns the current version, component cards,
field `schemaJSON`, reference sites and API operations using each component,
including transitive usage. The source is `components.schemas`; positions live
in `x-mocker-schema-layout`. External references are displayed without fetching.

Start with `create_api_design {name:"Orders"}` and retain its id and version.
Preview this batch with `preview_schema_model_changes {designId, commands}`:

```json
[
  {"kind":"create_schema","schemaName":"User","schemaJSON":"{\"type\":\"object\"}"},
  {"kind":"create_schema","schemaName":"OrderItem","schemaJSON":"{\"type\":\"object\"}"},
  {"kind":"create_schema","schemaName":"Order","schemaJSON":"{\"type\":\"object\"}"},
  {"kind":"upsert_property","schemaName":"Order","propertyName":"id","schemaJSON":"{\"type\":\"integer\",\"minimum\":1,\"example\":9007199254740993}","required":true},
  {"kind":"upsert_property","schemaName":"Order","propertyName":"user","schemaJSON":"{}","required":true},
  {"kind":"upsert_property","schemaName":"Order","propertyName":"items","schemaJSON":"{\"minItems\":1}"},
  {"kind":"set_reference","schemaName":"Order","propertyName":"user","targetSchema":"User"},
  {"kind":"set_reference","schemaName":"Order","propertyName":"items","targetSchema":"OrderItem","array":true},
  {"kind":"move_schema","schemaName":"Order","x":0,"y":0},
  {"kind":"move_schema","schemaName":"User","x":450,"y":0},
  {"kind":"move_schema","schemaName":"OrderItem","x":450,"y":350}
]
```

Preview returns `document`, `model`, `valid` and `diagnostics` and creates no
revision. Supply optional `document` to preview against an unsaved full OpenAPI
buffer; omit commands to inspect it. When valid, use
`apply_schema_model_commands {designId, expectedVersion, commands}` with the
same batch against the stored draft. It returns updated API detail. For an
unsaved full-document buffer, use the existing `save_api_design_draft` instead.
On 409 reread the draft and reconcile the edit before retrying.

The same actions have individual tools, each requiring `designId` and the
current `expectedVersion`:

| Tool | Additional arguments |
|---|---|
| `create_api_schema`, `replace_api_schema` | `schemaName`, complete `schemaJSON` |
| `rename_api_schema` | `schemaName`, `newName` |
| `delete_api_schema` | `schemaName` |
| `upsert_api_schema_property` | `schemaName`, `propertyName`, complete `schemaJSON`, optional `required` |
| `rename_api_schema_property` | `schemaName`, `propertyName`, `newName` |
| `delete_api_schema_property` | `schemaName`, `propertyName` |
| `set_api_schema_reference` | `schemaName`, `propertyName`, `targetSchema`, optional `array` |
| `move_api_schema` | `schemaName`, `x`, `y` |

Next, `rename_api_schema {designId, expectedVersion, schemaName:"User",
newName:"Customer"}` updates local references and layout. Read
`get_schema_model` to inspect referrers and operation usage. Existing diff,
revision, restore and analyst publication tools apply to these edits too.
A referenced schema or property cannot be deleted until its consumers are fixed;
errors name their JSON pointers. Unknown names are errors.

`schemaJSON` accepts an object or boolean and preserves advanced keywords and
exact numbers. A field upsert replaces that whole field; read its current JSON
before editing. Omitted `required` retains membership, while `false` removes it.
Property actions need an object-compatible component (type object or absent,
without root `$ref`). Explicit direct binding sets `$ref` and removes the field's
old `type` and `items`. Array binding sets `type:array`, removes the field's `$ref`,
and sets `items.$ref` after removing the element's old `type` and `items`. Other
field and element keywords are retained. Binding across a scope with `$id` or
removing nested resources is refused. Schema/property deletion may also be
refused for anchor references or resources with `$id`; inspect their consumers
and use explicit raw JSON edits when the change needs manual reference handling.

Batches are atomic, capped at 100 commands, 200 schemas and 200 direct properties per schema;
each `schemaJSON` is at most 64 KiB. Coordinates must be finite within ±100000.
The visual workbench uses preview to edit its buffer; Save creates a revision.

## API resource map: operations, ownership and relationships

`get_api_resource_map {designId}` groups the current API draft's HTTP operations
into resources. It returns `designId`, `version`, `revisionId`, `model`,
`scenarioUsages` and `usagesTruncated`. Use each returned operation `key` verbatim
when assigning it. Inline operations store the key in
`x-mocker-canvas-operation-id`; inherited Path Item operations use the per-path
identity described below. A scenario usage refers to that scenario's saved draft and its pinned
contract revision, with `mode` equal to `copy` or `linked`. It does not mean the
scenario uses the latest API draft. At most 500 scenario drafts are scanned and
1000 usages returned; a true `usagesTruncated` means the list is incomplete.

Preview an ordered batch before saving:

```json
{"designId":7,"commands":[
  {"kind":"upsert_resource","resource":{"id":"orders","name":"Orders","service":"billing","description":"Order lifecycle","operationKeys":[],"x":40,"y":80}},
  {"kind":"assign_operation","operationKey":"order-list","resourceId":"orders"},
  {"kind":"upsert_relation","relation":{"id":"orders-payments","fromResourceId":"orders","toResourceId":"payments","label":"charges"}},
  {"kind":"move_resource","resourceId":"orders","x":120,"y":160}
]}
```

Pass that object to `preview_api_resource_map`. Its optional `document` is a
complete unsaved OpenAPI string; preview returns the normalized document,
projected model, validity and diagnostics without writing. Omit `commands` to
inspect a document. If the preview used the saved draft, pass the same commands
to `apply_api_resource_map_commands {designId, expectedVersion, commands}`.
It writes one revision atomically and returns API detail. For an unsaved full
document, use `save_api_design_draft` with the preview document. Reread and
reconcile after a 409 version conflict.

Individual tools use the same command route and require `designId` and
`expectedVersion`: `upsert_api_resource {resource}`, `remove_api_resource
{resourceId}`, `assign_api_resource_operation {operationKey, resourceId}`,
`upsert_api_resource_relation {relation}`, `remove_api_resource_relation
{relationId}`, `move_api_resource {resourceId, x, y}` and
`auto_layout_api_resources {designId, expectedVersion}`. Empty `resourceId`
on assignment restores automatic grouping. Removing an annotation also returns
its operations to automatic grouping; it never deletes HTTP operations. Unknown
operation keys and missing relation endpoints remain visible as diagnostics so
they can be repaired. All commands preserve unrelated OpenAPI fields and exact
JSON numbers. A batch holds 1–100 commands; preview permits zero.
Maps support 200 resources, 1000 operations and 500 relationships. Coordinates
must be finite within ±100000.
The visual workbench uses preview to edit its buffer; Save creates a revision.

### Arrange resource cards

Preview the parameter-free `auto_layout` command:

```json
{"designId":7,"commands":[{"kind":"auto_layout"}]}
```

The server arranges all resource cards from their directed relationships, with
deterministic positions for cycles and disconnected groups. Names, ownership,
descriptions, operation assignments and relationships are preserved. Inferred
resources receive stored positions while retaining automatic operation grouping.
One command handles all 200 supported resources. No arrangement runs implicitly.

After inspecting the proposal, apply the command with the current
`expectedVersion`, or call `auto_layout_api_resources {designId, expectedVersion}`
to save the arrangement directly. Both use the same atomic command handler.
The visual **Расставить ресурсы** action displays a proposal before changing the
API buffer. **Применить расположение** applies it to the buffer; **Отменить**
restores the previous presentation. Save persists an applied arrangement.

### Operations inherited from a local Path Item

The map includes operations inherited through local JSON Pointer `$ref` chains,
including their schema dependencies and path-level parameter schemas. A reusable
Path Item referenced by two paths produces distinct operation keys. The server
stores inherited identities on each consumer Path Item, for example:

```json
{
  "$ref": "#/components/pathItems/Orders",
  "x-mocker-canvas-operation-ids": {"get":"orders-list-instance"}
}
```

Preserve this metadata when moving the consumer path. Read generated keys from
the returned document or map; changing a method without preserving its key
creates a new identity. Resource commands preserve the reference and shared
definition instead of flattening them.

An inherited operation's optional `sourcePointer` is a decoded JSON Pointer to
its authored definition, such as `/components/pathItems/Orders/get`. Its `path`
still names the concrete API path. The workbench opens inherited definitions in
the source editor and explains that editing the shared definition affects its
other consumers.

The nearest authored sibling method takes precedence in the map. Conflicting
methods, cycles, missing or invalid targets, excessive reference depth and
unsupported external references produce diagnostics. No external document is
fetched. Resolve diagnostics before saving a valid API draft.

This reference support covers map projection, assignment and schema analysis.
Mock runtime indexing and sequence operation bindings currently read literal
operations under `paths`; inherited map keys do not enable those flows.

## Sequence canvas: edit, run, inspect, vary

A sequence-canvas scenario stores participants, ordered messages, contract
bindings and execution settings. It has its own immutable revisions and is
separate from the classic workspace snapshot called a scenario.

1. `list_design_scenarios` → `get_design_scenario {scenarioId}`. Read the full
   `draft.document`, `draft.formDrafts`, `scenario.version` and `draft.id`.
   `validate_design_scenario {scenarioId, document}` checks a proposed document
   without saving. Enabled HTTP requests need a linked, current API contract;
   finish form buffers and configure every alt/opt/loop guard before a run. Labels are descriptive and never evaluated.
2. To edit, use `apply_design_scenario_commands` with the exact `expectedVersion`
   and an ordered command batch, or `save_design_scenario_draft` with the complete
   document and all retained form buffers. Upserts replace complete objects.
   Re-read and reconcile a409; never blindly substitute the newer version.
   `set_design_scenario_fragment_execution {scenarioId, expectedVersion, fragmentId, execution}` and
   `set_design_scenario_branch_execution {scenarioId, expectedVersion, fragmentId, branchId, execution}`
   are focused version-fenced edits; `execution: null` clears a setting. The same
   changes work in command batches as `set_fragment_execution {id, fragmentExecution}`
   and `set_branch_execution {id, branchId, branchExecution}`. An opt needs a
   condition, a loop needs 1–100 maximum iterations and may have a condition,
   and each alt branch needs a condition or one final `otherwise: true`. Conditions
   compare exact string variables using `equals`, `not_equals`, `exists` or
   `not_exists`; equality requires `value`, including an empty string.
   Message `execution` configures parameter/header/body templates (`{{name}}`),
   expected HTTP status, JSON-pointer assertions and extracted variables.
3. `run_design_scenario {scenarioId, revisionId, runId, name, variables}` starts
   the saved revision and immediately returns a report. Choose a unique run ID
   (`[A-Za-z0-9_-]{1,100}`) and a useful name for each experiment. `variables` is
   an optional map of string overrides; it does not save a new scenario revision.
4. While `status` is `running`, call `get_design_scenario_run {scenarioId, runId}`
   roughly once per second. **Starting successfully does not mean the scenario
   passed.** Read the terminal `passed`, `failed` or `cancelled` result. Every
   report includes `controlFlow` decisions and the exact document snapshot and ordered steps with resolved
   requests, responses, assertion results and reasons. Repeated loop steps have
   `occurrence` and `iterations`; skipped branches are decisions, not HTTP calls.
   Missing JSON values and
   JSON null differ. `expectedJson` and `actualJson` contain serialized JSON so
   large integers remain exact; preserve them as strings when reporting them.
5. On failure, inspect the first failed step, its actual status/body and assertion
   reason. Try another variable set under a NEW run ID, or edit the scenario and
   run its new revision. Extraction happens only after the step's checks pass;
   failure stops later requests. Default expected status is any2xx.
6. If a start response is lost, GET the known run ID. Retrying the same ID and
   same input returns the existing run without repeating requests. A different
   payload with that ID is409. Old pruned report IDs are410 and do not re-execute.
   `list_design_scenario_runs` lists the latest50 reports from both transports.
   `cancel_design_scenario_run` stops an active run; completed effects remain.
   `get_design_scenario_coverage {scenarioId, revisionId?}` returns observed
   message and control-flow path counts for one immutable revision, sampled from
   at most 50 retained terminal reports; omitted revision selects the draft.
   Zero hits mean unobserved, not impossible.

The server runs the sequence even when the MCP call returns or its client
disconnects. Four runs may be active globally, one per scenario; runs have a120s
budget and individual requests30s. Runs use linked draft mocks in-process,
with the same contract checks as a single-step probe. External URLs are never dispatched. The server evaluates configured conditions
and bounded loops against run variables; incomplete fragment settings stop a full
run before dispatch. Direct `execute_design_scenario_step` still refuses any
fragmented revision. Disabled/descriptive messages are skipped. Runs
use normal mock state and do not isolate or roll back effects.

The UI's execution panel follows new agent runs, shows progress and keeps saved
reports after reload. A manually selected historical report stays selected.
Closing the viewer does not cancel a run started by an agent. The run's `source`
is recorded by the transport (`mcp` or `ui`), not supplied by the caller.

## Classic workspace design (existing workflow)

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

## Limits of the classic workspace workflow

- **No request validation.** `reqSchema` is exported as `requestBody` and
  is never enforced on an incoming request; the mock accepts what it is
  sent.
- **No schema editor.** Schemas are JSON documents you write (or an agent
  writes); the panel renders the contract read-only.
- **No shared components from your own rows.** A custom endpoint's schema
  is inline. Two rows with the same shape carry two copies.
- **No review or comments.** The export is a file: put it in git, review
  it there.


### Typed response-to-request data bindings

Use `get_design_scenario_data_flow {scenarioId, revisionId?}` to inspect fields
from saved OpenAPI snapshots. Use `analyze_design_scenario_data_flow
{scenarioId, document}` to analyze unsaved changes without saving or executing.
Both tools are read-only. Unknown or unsupported schema types produce warnings;
incompatible known types and unavailable sources are errors that block runs.
Catalogs have a depth limit of 20 and 2000 fields per message, with explicit
warnings when truncated. External schema references are never fetched.

A binding is stored in `message.execution.bindings` (at most 100 per step):

```json
{"id":"order-id","sourceMessageId":"create-order","sourcePointer":"/id","target":{"kind":"path","name":"id"}}
```

For a typed mutation, first read `get_design_scenario`, then call:

```json
{"scenarioId":1,"expectedVersion":1,"messageId":"get-order","binding":{"id":"order-id","sourceMessageId":"create-order","sourcePointer":"/id","target":{"kind":"path","name":"id"}}}
```

Pass this object to `upsert_design_scenario_data_binding`. Reusing `binding.id`
replaces that binding. `remove_design_scenario_data_binding` takes `scenarioId`,
`expectedVersion`, `messageId` and `id`. Generic `apply_design_scenario_commands`
accepts `upsert_data_binding` (`messageId`, `binding`) and `remove_data_binding`
(`messageId`, `id`) in an atomic batch. Reconcile stale versions; never blind-retry.

Targets are `path`, `query` or `header` with `name`, or `body` with `pointer`.
The body pointer `""` replaces the whole JSON body. `prefix` is literal text,
for example `"Bearer "`, and is allowed only for string destinations. JSON body
values retain their original types and number precision. Existing templates and
extractions remain supported; bindings override their occupied destinations.

The source must be an earlier enabled HTTP request with an operation. Every
source opt/loop/alt-branch must also enclose the target. Values cannot escape a
branch or loop or cross branches. A loop uses only successful source occurrences
from the same iteration; a missing field or incompatible value fails before
sending the target request. Structurally valid broken references remain editable
and are reported by analysis; they are never removed silently.

Run the whole scenario with `run_design_scenario`: direct
`execute_design_scenario_step` refuses bindings because it lacks source history.
Read `get_design_scenario_run` and inspect each step's optional `bindingResults`:
`bindingId`, `sourceMessageId`, `sourcePointer`, `sourceOccurrence`, optional
`sourceIterations`, `target`, and `valueJson` show the exact successful source
and original typed value. The final prefixed value is visible in the request.
Use the report's document snapshot when interpreting historical runs.
Standalone HTTP, cURL and Postman exports reject steps with bindings explicitly.


### Generate tests for unobserved sequence branches

Call `suggest_design_scenario_tests {scenarioId, revisionId?}` after saving the
scenario. It reads the same revision-scoped coverage as
`get_design_scenario_coverage` (up to 50 retained terminal reports) and returns:

- `cases[]`: `id`, `name`, initial `variables` overrides and expected `targets`
  (`fragmentId`, optional `branchId`, `outcome`).
- `unresolved[]`: paths for which no input was found, with `code` and `reason`.
- `truncated` and `checkedCandidates`: bounded-search status.

For each selected case call `run_design_scenario` with the returned `revisionId`,
case `variables`, case `name`, and a **new unique runId**. The case `id` is only a
preview label; do not reuse it as runId. Poll `get_design_scenario_run` to a
terminal report, compare its `controlFlow` with the case targets, then refresh
coverage. A passed run alone does not prove that every proposed target was hit.

Generation does not save or execute requests. Existing HTTP assertions and data
bindings remain active. Initial values are combined with saved defaults, exactly
like normal runs; overrides cannot remove a saved default variable. Conditions
whose values come from HTTP responses may need mock configuration. `unresolved`
means no input was found, not that the path is impossible. The search checks up to
256 input sets and returns at most 20 cases / 1 MiB of case JSON. Cases are
proposals, and coverage increases only through actual observed run decisions.
Executed cases persist as named runs with their inputVariables; there is no
separate saved test-suite entity in this version.
