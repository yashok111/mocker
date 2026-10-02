# Source flow model and pinned reads

Canonical owner: `mocker-backend-inspect` workflow3. Select/verify this owner's
identity and contentHash in the same global guide set before using the topic.
Source schema3/4 and `runtime-flow-v1` extend relational source records. They do
not describe runtime observation or the effect of a database proposal.

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

Required on every variant: projectId, exact source schema3/4 revisionId, view.
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

Require inspect3, `backend-saved-views` and document `saved-view-v1`. Four tools:
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

## query_backend_lineage (inspect3)

Require source4, field-lineage-v1 and backend-field-lineage-query in the selected
inspect3 set. Existing flow/access reads remain source3/4; source1/2 refuse them.
Lineage refuses source1–3 and proposals with422, rather than an empty answer.

Input: projectId, exact revisionId, complete seed ValueRef, direction
forward/reverse; maxDepth1–32/default8, limit1–100/default50 and optional cursor.
ValueRefs are column {kind,nodeId,facetKey}, port
{kind,nodeId,collection,portKey}, api_field {kind,nodeId}. Unknown/mixed/proposal
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
