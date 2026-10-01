# Source flow model and pinned reads

Canonical owner: `mocker-backend-inspect` workflow1. Select/verify this owner's
identity and contentHash in the same global guide set before using the topic.
Source schema3 and `runtime-flow-v1` extend relational source records. They do
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

Required on every variant: projectId, exact source schema3 revisionId, view.
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
