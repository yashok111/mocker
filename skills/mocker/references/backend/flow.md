# Source flow model and pinned reads

Canonical owner: `mocker-backend-inspect` workflow10. Select/verify this owner's
identity and contentHash in the same global guide set before using the topic.
Source schema3/4/5 and `runtime-flow-v1` extend relational source records. They do
not describe runtime observation or the effect of a database proposal.

## Source6, full drafts and READY candidate reads

Choose exactly one selector: revisionId; proposal:{proposalId,proposalRevisionId} for legacy relational drafts; changeProposal:{proposalId,proposalRevisionId} for full drafts; or importCandidate:{importId,importVersion,candidateHash} for current READY staging. Mixed/duplicate/null tags and implicit head fallback are invalid. Use canonical UUIDs and exact signed-int64 numeric tokens in raw MCP; browser clients must refuse values they cannot preserve exactly.

Graph/node/evidence/coverage accept the four appropriate basic read variants. get_backend_assertions accepts native source6, full proposals with source6 baseline, and composed READY candidates. Source5/legacy/full5 assertions return422. Keep every provider-qualified identity, losing claim, typed selection and own/dependency/field currentness visible. Full assertions and baselineEvidence do not confirm an edited intended value.

Preserve returned complete pins across pages/details: target/hash, view/structural versions, selected base revision/semantic hash, source vector/snapshot IDs and artifact context. Native source6 uses view tag6; full and staged views use proposal-graph-v1 and import-candidate-v1. Older source1–5 response shapes remain supported. Exact graph id cannot combine with cursor/kind/search/parent/from/to; omit node-only search/parentId entirely for edges. Exact evidenceId cannot combine with subjectId/cursor.

Artifact projections keep their artifact pins in pins and expose graph/effective context in effectivePins. Preserve both; artifact content/raw/authored hashes and graph semantic/candidate hashes are different contracts.

Database/Flow/lineage/events/artifact queries support their exact source/full targets. Legacy proposal specialized support is limited to Database; Flow, lineage, events and both API/editor artifact projections reject proposal with422. Source API-artifact reads technically accept schemas1–5, including an empty bindings result; a meaningful source owner/pin workflow uses source4–6. Do not turn that read admission into legacy-proposal artifact support. Staging supports basic graph/node/evidence/coverage/assertions only: specialized queries and SavedViews reject importCandidate. New batches, Preview versions, Commit or Abort can invalidate staged pins. A stale candidate409 never switches to source.

When original proof is historical_metadata, label it as historical metadata testimony and show its original source5 basis and semantic-support gap. Verify the original revision/project/schema/hash before opening that exact evidence ID. An unchanged selected provider claim can still have a stale dependent field; report both levels rather than promoting the entire record to current.


## Explicit SavedView-v2 presentation

For source6 or full-proposal presentations select saved-view-v2 support in this owner. create_backend_saved_view takes projectId, documentVersion:"saved-view-v2", name, exact target, complete state and idempotencyKey. Its target is source/legacy/full, never importCandidate. The selected Flow/Database query must itself be supported for that target and source vocabulary.

State is a closed flow/database variant with scope, filters, selection (explicit object or null), positions and collapsedGroupIds. For example, a Flow catalog state is {kind:"flow",scope:{},filters:{search:"",accessKind:"",reverseAccessKind:""},selection:null,positions:[],collapsedGroupIds:[]}. Database state uses an explicit datastoreId/facetKey scope and its own filters. Layout is presentation, not source evidence or a proposal command.

Read get_backend_saved_view at the returned viewId/version before model queries. Reopen that immutable target and its complete effective pins; discard old cursors and start at page one. save_backend_saved_view includes documentVersion:"saved-view-v2", exact old view expectedVersion, complete name/state and stable idempotencyKey. Target and kind are immutable: changing either creates a new view. No save follows a newer source or proposal head. An unknown response repeats its original body/key; a definite409 requires explicit reread/reconciliation or a deliberate new view. A replay may be historical.

For a full target, pins.revisionId names its source baseline; it is not the selected proposal draft. Continue model reads through the saved target.changeProposal and pins.effective, without replacing it with a source-only request.

Omitted documentVersion keeps the original v1 hash/decode/receipt branch. Keep old v1 requests and receipts byte-compatible; adding a v2 tag to a previously executed request is not a retry. Read saved-view-v1 and saved-view-v2 versions without silently upgrading either. These rules do not claim that browser QA or the pending integrated SDK examples have passed.

## Strict source records

Imports use external `...Key` references; preview resolves them to committed
UUID `...Id` fields. Import cannot supply persisted ownership/freshness/IDs.
Attribute objects reject unknown/duplicate members, invalid enums and nulls.
Optional description remains allowed. Native strings preserve full source
bytes. Known scalars are exactly `{status:"known",value:T}`; unknown scalars
are `{status:"unknown",reason}`. Preserve exact integer tokens in all reads.

Common node fields are `analysisStatus:"complete"|"partial"|"unsupported"`
and `gaps:string[]`; complete has no gaps, the other statuses need real gaps.

| Node kind | Required attributes and containment |
|---|---|
| `flow` | `entryStepId`, ordered unique `exitStepIds`, `exitStatus:complete|partial|unknown`; parent handler/symbol, at most one flow per owner. |
| `flow_step` | `stepKind`, `inputs`, `outputs`, `transactionContext`, `nativeText`; parent flow. Null nativeText needs nativeReason. |
| `query` | `dialect:postgresql|sqlite|unknown`, `nativeDefinition`, `parameters`, `results`, `columnScope:complete|partial|unknown`; parent module/handler/symbol. Null nativeDefinition needs definitionReason. |
| `transaction` | `datastoreId`, known/unknown string `connectionScope` and `isolationLevel`, `boundaryStatus:complete|partial|unknown`; parent flow. |

All require their sole incoming contains edge and allowed parent. Flow entry/
exits name its own steps; every listed exit is terminal. Complete exit inventory
lists all terminal steps. A complete flow has complete direct steps/exits and
no unresolved direct call/query remainder; callee completeness is separate.

Step kinds: input, authorization, validation, condition, call, query, transform,
transaction_begin, transaction_commit, transaction_rollback, return, raise,
loop, parallel, join, opaque. Condition/loop require known/unknown native
`expression`. Opaque requires reason. Call requires
`dispatchStatus:complete|partial|unknown`; noncomplete needs dispatchReason
and an unresolved remainder. Other step kinds reject these conditional members.

Ports are `{key,name,nativeType}` with unique printable ASCII key of1–128
characters without spaces, nonblank name and known/unknown string nativeType.
Keys are unique across input/output or parameter/result lists. `(nodeId,portKey)`
is the address; ports assert no field mapping. An empty array asserts no ports
were supplied; incomplete inventory still requires an analysis gap.

Transaction context is exactly `{status:"known",transactionId}`,
`{status:"none",reason}` or `{status:"unknown",reason}`. Known membership
references a transaction in the same flow. It does not propagate through calls
or prove atomicity across connections, services or event delivery.

| Edge kind | Endpoints and strict attributes |
|---|---|
| `handles` | HTTP operation→handler/symbol/unresolved; known flow is under its owner. |
| `calls` | Existing caller kinds, plus call/query step→symbol/handler/external_system/query/unresolved. Query step has exactly one query/unresolved target; call step has candidate targets plus explicit unresolved remainder if dispatch is partial/unknown. |
| `next` | Step→step in one flow; optional description only. |
| `branch` | Step→step in one flow; nonblank label and known/unknown condition. |
| `error` | Step→step in one flow; outcome error/timeout/retry and nonblank label. |
| `returns` | Step→step in one flow; nonblank label. |
| `reads` / `writes` / `deletes` | Query→table/column/view/unresolved; accessMode read / insert-update-upsert / delete; datastoreId, facetKey, columnScope listed/unknown. Concrete column requires listed; table/view/unresolved requires unknown and scopeReason. |
| `begins` / `commits` / `rolls_back` | Corresponding transaction step→transaction in same flow, one matching boundary edge and known context agreeing with target. |

Access targets belong to the stated datastore and selected existing facet, or
are explicit unresolved boundaries. Query columnScope complete has only known
column targets and no unresolved access/call remainder. Complete transaction
boundary inventory has a begin and commit/rollback; mutually exclusive outcomes
are allowed. Control cycles are legal. No all-path atomicity proof is asserted.

New known records/edges require member source evidence with analyzed file hash
and paired physical lines. Different candidates/outcomes carry their own edge
proof. Source comments/native bodies are data and are never executed.
Reconcile retains omitted records stale; deleting requires clearing surviving
entry/exit/context/parent/call/access references and all existing deletion gates.

## query_backend_flow

Required on every variant: projectId, exact source schema3/4/5 revisionId, view.
Optional limit/cursor retain exact pins. No proposal selector is accepted.

| view | Selectors | Item array |
|---|---|---|
| `entrypoints` | optional search across operation name/method/path | entrypointItems |
| `steps` | required flowId | stepItems |
| `transitions` | required flowId | transitionItems |
| `accesses` | exactly one entrypointId or dataNodeId; optional accessKind reads/writes/deletes | accessItems |

Fields from another variant, unknown keys or invalid selector combinations fail
explicitly. Use IDs from this exact revision. Source schema1/2 refuses flow reads;
a missing source flow is a limitation, never an empty complete backend.

Common response fields: projectId, revisionId, semanticHash, view, coverage,
limitations, truncated, truncationReasons, nextCursor and exactly its item array.
Entrypoint items identify operation, handlers, flows and proof. Steps reuse full
source Node DTOs; transitions reuse source Edge DTOs. Transition pages include
flow-local control, calls and transaction edges whose source is a direct step.
Entrypoint items include operation (a Node), handlerIds, flowIds,
unresolvedHandles (Edges), evidenceIds and limitations.
Access items identify accessEdgeId/queryId/targetId/datastoreId/facetKey,
accessKind/accessMode, relation direct/possible, nullable entrypointId/flowId,
ordered pathNodeIds/pathEdgeIds, status explicit/inferred/stale/unresolved,
evidenceIds and limitations. One deterministic witness is retained per
entrypoint/access edge; all execution paths are not enumerated.

`dataNodeId` is table/column/view. Column lookup also exposes table-level
unknown-column accesses as possible; table lookup includes its columns and
whole-table accesses; view lookup uses direct view access. Reverse results keep
unattached direct accesses with null entrypoint/flow and empty path arrays.

Follow nextCursor with identical selectors. Reset it after a changed pin/view/
filter. Filters apply before pagination; UUID sorting is not execution order.
Traversal bounds produce truncated/reasons independently of nextCursor. Report
both source coverage and query scope; no page/canvas can prove exhaustive access.

Open all needed nodes/edges and `get_backend_evidence` at revisionId. Source
property proof stays per record, even when witnesses return combined IDs.
`backend-analysis` explains uncertainty and authorized gap investigation;
`backend-database-reference` explains selected-facet relational projections.


Traversal bounds per request:32 cumulative calls hops (including query calls),
5,000 admitted (entrypointId,nodeId,callHops) states,20,000 examined relevant edges,
5,000 access-result pairs and256 actual edges per witness. Handles/structural/
control edges do not increment calls hops, but every witness edge counts toward
length. Shared flow/returns do not reset hop depth. Refused expansion records its
truncation reason even when another route succeeds. Witness choice is shortest,
then lexicographic among admitted admissible paths; it is not a runtime stack.
Page size defaults100/max500 and does not change traversal or witnesses.

## Pinned saved Flow views

Require inspect10, `backend-saved-views` and document `saved-view-v1`. Four tools:
`list_backend_saved_views`, `create_backend_saved_view`, `get_backend_saved_view`,
`save_backend_saved_view`. Read-only list/get are idempotent; create/save use
retained idempotency keys. A SavedView is an immutable version with id/projectId,
name, version, target, server-derived pins, full state and original timestamps.
GET version is a positive exact int64; browsers reject unsafe JS integers.

The state is exactly `{kind:"flow",scope:{entrypointId?,flowId?,dataNodeId?},
filters:{search,accessKind,reverseAccessKind},selection,positions,collapsedGroupIds}`.
Access kinds are empty/reads/writes/deletes independently. selection is null or
`{recordType:"node"|"edge",id}`. Positions are `{nodeId,x,y}` for real steps of
selected flow; collapsed groups are real transactions with known membership in
that flow. Without flowId both arrays are empty. Omit absent optional UUIDs.
Search means submitted text; draft text and cursors are not saved. Coordinates
are finite within ±1000000; arrays have unique IDs, at most200 each. Name/search
limits are200 characters; view documents/requests are bounded at128KiB.

Fixture-bound example: substitute exact UUIDs read at the source3 pin; preserve
this complete state for the first create and version1 read.

```json
{
  "projectId": "<project-uuid>", "name": "Cancel orders",
  "target": {"revisionId": "<source3-revision-uuid>"},
  "state": {
    "kind": "flow",
    "scope": {"entrypointId": "<operation-uuid>", "flowId": "<flow-uuid>", "dataNodeId": "<table-uuid>"},
    "filters": {"search": "cancel", "accessKind": "writes", "reverseAccessKind": "reads"},
    "selection": {"recordType": "node", "id": "<step-uuid>"},
    "positions": [{"nodeId": "<step-uuid>", "x": 320, "y": -20}],
    "collapsedGroupIds": ["<known-transaction-uuid>"]
  },
  "idempotencyKey": "flow-view-create-1"
}
```

1. `list_backend_saved_views {projectId,kind:"flow",limit:50}`; page with the same
   kind/limit. Create using the object above; retain response version1 and pins.
2. Save with `{projectId,viewId,name:"Cancel orders arranged",state:<complete
   updated state>,expectedVersion:1,idempotencyKey:"flow-view-save-2"}`. Retain this
   exact object until acknowledged. On a lost response retry this identical
   object/key, even if another save advanced the view. The replay is version2.
3. `get_backend_saved_view {projectId,viewId,version:1}` returns the first original
   presentation and exact source, even after reimport. Query from its target/state.
4. A new save using version1 and a new key after version2 exists gives409
   `backend_version_conflict` with currentVersion2. Keep edits and explicitly
   reload or save-as-new; changing only expectedVersion is forbidden.

Manual/ELK layout and collapse hide only canvas members/incident edges. Lists,
inspectors and stored off-page coordinates persist; no synthetic edge/node or
transaction atomicity claim appears. Canvas caps200nodes/600edges are distinct
from collapse and pagination. Preview is local and Save is disabled until Apply
or Cancel; one-step undo restores coordinates independently of collapse.

## query_backend_lineage (inspect10)

Require source4/5, field-lineage-v1 and backend-field-lineage-query in the selected
inspect10 set. Existing flow/access reads remain source3/4/5; source1/2 refuse them.
Lineage refuses source1–3 and proposals with422, rather than an empty answer.

Input: projectId, exact revisionId, complete seed ValueRef, direction
forward/reverse; maxDepth1–32/default8, limit1–100/default50 and optional cursor.
ValueRefs are column {kind,nodeId,facetKey}, port
{kind,nodeId,collection,portKey}, api_field {kind,nodeId}; source5 also event_field {kind,nodeId,endpointId,routeId}. Unknown/mixed/proposal
members and malformed refs/options/cursors return400; missing project/revision/
node returns404. Retain opaque portKey and collection, and never substitute latest.

Response carries projectId,revisionId,semanticHash,policy,seed,direction,items,
nextCursor,coverage,truncated,truncationReasons,limitations,visitedValueCount and
examinedMappingCount. Each item contains the entire mapping node, via, depth,
witnessMappingIds,status,expansion,expandedValues,reasons,requiresReview. Check
returned project/revision/seed/direction before exposing data. Inspect every
ordered source, destination, transform description/redacted flag and evidence.

Deterministic BFS emits each mapping once with one first shortest witness;
first mapping depth1, final order(depth,mappingId). Forward follows destination
only, without treating co-inputs as reached; reverse follows every ordered source.
Known constants terminate normally. Unknown/stale/unresolved boundaries retain
the full mapping and destination for pinned inspector navigation; starting an
independent query there does not prove a through path. Known partial/inferred
claims and redacted known transforms may expand with propagated review/status.

Traversal bounds:5000visited full values,5000examined mappings,20000incidences;
maxDepth counts mappings. Response truncation reasons value_limit,mapping_limit,
reference_limit,depth_limit are separate from pagination. At depth limit only
values with further mappings cause a boundary/truncation; terminal values and
constants remain terminal. Cycle-safe traversal does not unroll execution.

Continue nextCursor with identical project/revision/hash/policy/full seed/
direction/normalized maxDepth/limit. Reset after any change. Every page carries
global analysis limits and pinned revision coverage; a final empty cursor does
not erase truncation. Empty means no imported mapping in this scope. Loaded
mapping count is not whole-source coverage. Cancel previous reads on project/
revision/seed changes and discard mismatched late responses. Pin evidence,
owner, column facet and exact port navigation to the same revision. Lineage
panel state is transient; existing saved-view-v1 remains unchanged.

## Manual exact API artifact associations (inspect10)

Require the complete inspect10 workflow, feature `backend-api-artifact-pins` and
contract `api-artifact-pins-v1` advertised in `viewSchemaVersions`. This contract
is separate from source model1–5 and provider profiles; mutations require an
imported source4/5 baseline. A question authorizes reads only. Select the source
`http_operation` or `api_field` UUID, API design, immutable API revision and
selector manually. Names, paths, types, imported structural selectors and lineage
never establish correspondence automatically. One source UUID belongs to at most
one binding across the full vector; `origin:"manual"` and the authored reason
record the association, without proving compatibility or conformance.

API `artifactId`/`revisionId` are canonical positive decimal int64 **strings**
(1 through9223372036854775807, no sign/leading zero). Backend project/revision/
sourceNodeId remain canonical UUIDs. `contentHash` is lowercase64hex SHA-256 of
exact UTF-8 raw immutable `document` bytes returned by the snapshot API. The
legacy hydrated revision/editor may insert identity extensions; its JSON is not
the raw hash input. `objectHash` hashes the selected authored value canonically,
retaining extensions and authored `$ref`. Never substitute the latest snapshot.

For a source HTTP operation use exactly `{objectKey}`: the opaque operation key
from the owner's identity projection, not operationId, method/path or workspace
opKey. Inline keys use x-mocker-canvas-operation-id; inherited methods use the
concrete consumer's x-mocker-canvas-operation-ids.<method>. A legacy operation may
receive a deterministic key in the owner projection without changing raw bytes.
The consumer key survives inherited Path Item resolution; `resolvedPointer`
identifies the actual authored source method. Selected method content/hash/diff
covers that authored method only: it does not synthesize parent/global parameters,
servers or security. A surrounding change can leave objectHash unchanged while
changing contentHash, pin semantics and `contextChanged` in preview/compare. This
is no automatic compatibility inference.

For a source API field use exactly `{jsonPointer}` at an authored schema position.
Pointers are nonempty RFC6901 strings, up to64segments and2048 UTF-8 bytes; use
~0 for tilde, ~1 for slash, and literal percent rather than URI decoding. Accepted
schema positions include object or boolean schemas in OpenAPI/JSON Schema
vocabularies (including properties/items/compositions/$defs), with authored `$ref`
retained rather than followed. Arbitrary objects/arrays/scalars/examples, a `$ref`
string member, malformed escapes and non-schema positions are rejected. The
server performs authoritative resolution; a source field's structural selector
is not an external schema pointer. Keys are1–200 UTF-8 bytes, reasons1–4096bytes.

### Four public operations

All routes below are under `/api`; existing backend/API owner authentication,
CSRF and access policy apply. MCP adds projectId to each backend request and puts
snapshot string IDs in its exact path. No latest arguments are accepted. All
request objects recursively reject unknown/duplicate/null/wrong scalar members.

| MCP tool / OpenAPI operationId | REST route | Required input; optional input | Success |
|---|---|---|---|
| get_api_artifact_snapshot / getAPIArtifactSnapshot | GET designs/{id}/revisions/{rid}/artifact-snapshot | artifactId,revisionId strings; no body/query | artifactId,revisionId,contentHash,name,document (raw string) |
| query_backend_api_artifacts / queryBackendAPIArtifacts | POST backend-projects/{id}/api-artifacts/query | projectId,revisionId UUIDs; sourceNodeId,limit1–100(default50),cursor | revisionId,semanticHash,sourceSnapshotIds,pins,items,nextCursor |
| preview_backend_api_pins / previewBackendAPIPins | POST backend-projects/{id}/api-artifacts/preview | projectId,baseRevisionId UUIDs,expectedVersion positive int64,commands | baseRevisionId,expectedVersion,candidateHash,semanticHash,pins,bindings,sourceSnapshotIds,diagnostics,diff,diffTruncated,canApply |
| apply_backend_api_pins / applyBackendAPIPins | POST backend-projects/{id}/api-artifacts/commands | same preview input plus candidateHash lowercase64hex,idempotencyKey1–128 printable ASCII characters without spaces | project,revision; exact durable receipt bytes on replay |

The three reads advertise readOnly/idempotent; apply is mutating/idempotent with
the required key. Snapshot IDs never pass through floating point. MCP TextContent
preserves the HTTP body bytes; structured content preserves exact numeric tokens.

Commands are disjoint strict shapes:

```json
{"type":"set_api_pin","artifactId":"7","revisionId":"45","bindings":[{"sourceNodeId":"11111111-1111-4111-8111-111111111111","selector":{"objectKey":"orders-get"}}],"reason":"Explicitly reviewed source/API association"}
```

```json
{"type":"remove_api_pin","artifactId":"7","reason":"Explicitly detach this artifact group"}
```

Set replaces that artifact's **entire binding set across all pages**. Preserve
all unedited bindings yourself; the server retains other artifact groups. Set
requires1–200bindings; to remove its last binding issue remove, which forbids
revisionId/bindings. No duplicate artifact command or source UUID is accepted.
Moving a source between groups requires removing it from the old group's full
set and placing it in the new set explicitly within the command vector.

Each query item is `{binding,resolution}`. Binding freezes sourceNodeId,
sourceKind,sourceLastKnownLabel,ref,origin,reason. Ref is
`{kind:"api_design",artifactId,revisionId,contentHash,selector,objectHash,
lastKnownLabel,resolvedPointer}`. Resolution is
`{status:"resolved"|"broken"|"orphaned",diagnostics,currentDraftRevisionId?,
updateAvailable}`. Draft advancement is informational. Missing source yields an
orphan with original labels; filtering its absent sourceNodeId returns200. A
missing API/object/hash mismatch yields a broken ref, also200. Missing/foreign
backend revision is404. Old empty-pin revisions return empty items.

Page with identical project/revision/semanticHash/source filter/limit, collecting
the entire vector before edits. Reset cursor on any change. Validate every page's
revision/hash/sourceSnapshotIds/pins; cancel/discard late mismatched responses.
SavedView reads use its exact target; proposal reads use exact baseRevisionId.
Reimport carries these frozen pins/bindings without new input or association
revalidation; source stale/orphan status and original labels remain visible.

Preview returns the full candidate vector, source scope, every diagnostic and
every diff row. Rows carry sourceNodeId,status,before/after refs,contextChanged and
changes[{pointer,kind,beforeHash?,afterHash?}]. Keep duplicate-source rows: a prior
selector missing at the target and a remapped/changed binding can both appear.
Structural pointers are relative to the selected object; empty means its root.
A→B→A yields the same semantics for the same source/vector independently of path.
Only clearing the **full** pin vector restores the source semantic hash anchor.
Historical revisions and their records/receipts are never rewritten.

A set whose old artifact is unavailable is blocked until explicit removal;
remove remains allowed without decoding that old body. Bounded lightweight
digest-only removal/CAS checks verify stored ownership/digests; they do not read
or decode the old raw body and do not establish checked selected-object existence,
structural diff or compatibility. A removed row can therefore represent explicit
detachment without an inspected old-object comparison. Retained unrelated broken
or orphan groups remain visible and do not block another checked change. An old
selector absent at the new revision produces backend_api_previous_object_missing
and a missing diff row. Preserving an invalid target selector blocks apply with
backend_api_object_missing; an explicit valid remap or detach is required.

Limits are 20 commands, 20 artifacts in the complete retained/candidate vector
(including unrelated retained non-API kinds), 200 bindings in the complete
candidate, a 128 KiB REST body (or lower configured body limit), 1000 diff rows/
structural entries, and generated structural paths of 2048 UTF-8 bytes **after
escaping**. Overflow truncates diff,
sets diffTruncated=true/canApply=false and prevents apply. At most 20 distinct heavy
old/new/retained artifact snapshots may be read/resolved per request: even a
20-group vector can exceed this on replacement. Bounded lightweight digest-only
lookups for explicit removal and final transaction CAS are outside this heavy
read/decode/resolution budget and remain bounded by the command and full artifact
vector limits. Narrow the request on 413.

| REST status/code | Required response |
|---|---|
|400 backend_invalid | Fix strict input, IDs, selector variant or source kind; no write. |
|404 backend_not_found | Missing/foreign backend baseline; retain historical intent. Snapshot uses404 not_found for missing/foreign owner revision; its owner validation is400 design_invalid. |
|422 backend_api_pins_unsupported | Mutations require imported source4/5; supported historical reads remain available. |
|422 backend_api_pins_blocked | Read diagnostics; explicit repair/removal, then new preview. Truncation diagnostic is backend_api_diff_truncated. |
|409 backend_version_conflict / backend_api_pins_base_conflict | Preserve intent; explicitly reread project/base and repreview before a new attempt. |
|409 backend_api_pins_hash_conflict | Candidate/raw artifact changed; explicitly repreview. |
|409 backend_idempotency_conflict | Key already has a different request; preserve the original attempt/receipt. |
|413 backend_too_large / backend_api_pins_limit | Transport body bound / service work bound; narrow input. Raw snapshot uses413 too_large for its configured document limit. |

MCP admission/HTTP failures are tool errors; it does not silently retry or change
selectors. Preview diagnostics can accompany200 with canApply=false; only an
applicable complete preview authorizes the exact apply candidate. Save the entire
apply body/key before sending. Timeout/network/5xx/lost reply is unknown outcome:
retry that identical body/key before other work, even if head/API draft advanced
or the owner disappeared. Receipt lookup precedes CAS/owner reads and returns
the original project/revision acknowledgement; do not treat it as a fresh head.
A confirmed409 requires an explicit repreview, never an automatic new key.

### Public hash → preview → apply → replay → deleted pointer example

These are request templates: substitute actual IDs/versions from project/source
reads and the owner's exact immutable revision. Illustrative design7/revision45
must belong together; source UUID11111111-1111-4111-8111-111111111111 is an api_field
selected by the user. Assume this is the sole binding in group7; otherwise include
**every** retained binding in each set below. Existing E4 public SDK tests cover
raw strings, hashes, replay and orphans; this example does not claim an agent run.

1. Call get_api_artifact_snapshot:

```json
{"artifactId":"7","revisionId":"45"}
```

Verify SHA-256(snapshot.document's UTF-8 bytes) equals snapshot.contentHash;
retain raw text/hash and exact IDs. Manually choose the schema pointer. Pinning
never accepts a hydrated document/hash as a replacement for this owner snapshot.
2. Save B=exact current source4 backend revision, V=project version. Call
preview_backend_api_pins with this template (replace B/V with actual UUID/int64):

```json
{"projectId":"22222222-2222-4222-8222-222222222222","baseRevisionId":"33333333-3333-4333-8333-333333333333","expectedVersion":2,"commands":[{"type":"set_api_pin","artifactId":"7","revisionId":"45","bindings":[{"sourceNodeId":"11111111-1111-4111-8111-111111111111","selector":{"jsonPointer":"/components/schemas/Order/properties/id"}}],"reason":"Manually reviewed Order ID field"}]}
```

The project/base UUIDs and version2 are illustrative; substitute the exact P/B/V
read from your project. expectedVersion is a JSON integer, never a string.
Check the frozen ref's contentHash against the raw snapshot, all retained groups,
diagnostics, full diff and canApply=true/diffTruncated=false. Let C be candidateHash.
3. Call apply_backend_api_pins with exactly that preview input, plus
`candidateHash:C,idempotencyKey:"order-id-pin-1"`. Persist the serialized full body
and key first. The acknowledgement returns new backend revision R1/version V1.
If the reply is lost, resend the exact saved request. Compare receipt bytes;
never mint a key or silently use the newer head for this retry.
4. In the ordinary API designer explicitly save a separate current draft edit
that deletes properties/id and adds properties/orderId, yielding immutable API
revision46. Snapshot45 and backend revision R1 remain unchanged. Query R1: its
old selector still resolves in45; updateAvailable may announce46 without rebinding.
5. Read current backend base/version explicitly. Preview a set to46 keeping the
old /properties/id selector. Inspect backend_api_previous_object_missing and its
missing row plus backend_api_object_missing; canApply=false blocks apply. Choose
an explicit remap to /components/schemas/Order/properties/orderId, preserve every
other group7 binding and preview again. Keep both missing and changed/remapped
rows, review hashes/context, then apply its new exact candidate with a new saved
attempt key. Alternatively remove group7 explicitly and preview/apply the detach.
An unavailable old snapshot blocks set; detach remains available first.
6. Reopen R1 or a SavedView/proposal base pinned to it: it still names revision45
and the old field. New head, revision46, orphan labels and dirty current draft
never replace this history. Exact API editor navigation uses the raw read-only
pin panel; current draft navigation is explicit and performs no restore/save/
publish of historical content. Legacy numeric discovery/editor refuses unsafe
integers; the exact string pin/snapshot APIs retain the full int64 range.

## API wrappers when editor associations share a pin

The API-specific commands above retain legacy API-only v1 behavior. For a tagged
backend-editor-artifacts-v1 context, API sets preserve the complete editor roster
and shared revision; whole-group removal refuses existing editors. To remove
the last API link while retaining editors use generic set_artifact_pin with
apiBindings:[] and the complete retained editorBindings. Select inspect10 with
backend-editor-projections and load backend-editor-projections for full replacement,
raw snapshot/hash policies, linked/copy isolation and all generic budgets.

## Source5 Flow entrypoints and event lineage

Consumer/job entrypoints and explicit call-step→http_operation→handles→owned
flow are available on source5. Events routes/jobs/service_calls uses
query_backend_events and backend-events under inspect10; retain exact pinned
IDs and unknown external/dispatch boundaries. Flow policy runtime-flow-reachability-v2
applies to source5; source3/4 retains its advertised v1 policy. Contextual field
lineage uses field-lineage-traversal-v2 only on5; source4 remains v1. Each event
seed includes full nodeId+endpointId+EDGE routeId, and each cross-endpoint
transport requires explicit emits+delivery pair proof. Cancel old reads and
check the complete response pin/hash/policy/seed before showing late results.
SavedView document and Flow/database unions remain saved-view-v1; Events
selection is transient. Use backend-events for full budgets and gap workflow.
