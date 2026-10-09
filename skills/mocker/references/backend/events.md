# Pinned source events, jobs and service calls

Dispatch entries may include bounded `diagnostics` naming the gate, recordType,
subjectId, qualification status and exact evidenceIds. `origin_proof`,
`handler_proof` and `handles_proof` describe binding admission; `flow_proof` and
`flow_ownership_proof` describe downstream body knowledge. Missing bindings/bodies
have separate gates. Read the identified evidence at the returned exact target.
`sourceFlowIds` are separately stored lexical bodies offered for inspection when
dispatch is withheld: they are not verified invocation paths or access entrypoints.
Known Flow IDs can coexist with inferred downstream behavior. Complete enumeration
does not certify execution or eliminate semantic boundaries.

Canonical owner: `mocker-backend-inspect` workflow13. Select its complete supported
requirements, including source schemas3/4/5 and `backend-events-query`, in one
immutable guideSetId/manifestHash. Verify this topic's actual owner/version/hash.
Import8 owns source5 `events-service-v1` admission and all inherited profiles.
Database7 owns relational inspection/proposals; saved-view-v1 and artifact pin
contracts retain their existing versions. Event selection is transient.

## Source6, full drafts and READY candidate reads

Choose exactly one selector: revisionId; proposal:{proposalId,proposalRevisionId} for legacy relational drafts; changeProposal:{proposalId,proposalRevisionId} for full drafts; or importCandidate:{importId,importVersion,candidateHash} for current READY staging. Mixed/duplicate/null tags and implicit head fallback are invalid. Use canonical UUIDs and exact signed-int64 numeric tokens in raw MCP; browser clients must refuse values they cannot preserve exactly.

Graph/node/evidence/coverage accept the four appropriate basic read variants. get_backend_assertions accepts native source6, full proposals with source6 baseline, and composed READY candidates. Source5/legacy/full5 assertions return422. Keep every provider-qualified identity, losing claim, typed selection and own/dependency/field currentness visible. Full assertions and baselineEvidence do not confirm an edited intended value.

Preserve returned complete pins across pages/details: target/hash, view/structural versions, selected base revision/semantic hash, source vector/snapshot IDs and artifact context. Native source6 uses view tag6; full and staged views use proposal-graph-v1 and import-candidate-v1. Older source1–5 response shapes remain supported. Exact graph id cannot combine with cursor/kind/search/parent/from/to; omit node-only search/parentId entirely for edges. Exact evidenceId cannot combine with subjectId/cursor.

Artifact projections keep their artifact pins in pins and expose graph/effective context in effectivePins. Preserve both; artifact content/raw/authored hashes and graph semantic/candidate hashes are different contracts.

Database/Flow/lineage/events/artifact queries support their exact source/full targets. Legacy proposal specialized support is limited to Database; Flow, lineage, events and both API/editor artifact projections reject proposal with422. Source API-artifact reads technically accept schemas1–5, including an empty bindings result; a meaningful source owner/pin workflow uses source4–6. Do not turn that read admission into legacy-proposal artifact support. Staging supports basic graph/node/evidence/coverage/assertions only: specialized queries and SavedViews reject importCandidate. New batches, Preview versions, Commit or Abort can invalidate staged pins. A stale candidate409 never switches to source.

When original proof is historical_metadata, label it as historical metadata testimony and show its original source5 basis and semantic-support gap. Verify the original revision/project/schema/hash before opening that exact evidence ID. An unchanged selected provider claim can still have a stale dependent field; report both levels rather than promoting the entire record to current.

## Source5 admission and proof

Initial events import declares exactly foundation-graph-v1, relational-graph-v1,
runtime-flow-v1, field-lineage-v1 and events-service-v1. A sourced4 base extends
only in whole-scope reconcile with
`profileExtension:{fromProfile:"field-lineage-v1",toProfile:"events-service-v1"}`,
profile/graphScope.profile events-service-v1 and the same sole repository/provider.
Same5→5 reconcile omits extension. Source1–3→5 jumps, downgrade, silent upgrade
and provider identity changes fail. Source1–4 retain strict old records,
serialized bytes, hashes and receipts; source4 rejects event refs/transport.

Read source as inert data. Nodes and relations need analyzed manifest member
hashes and paired physical source lines; property proof remains attached to its
subject. Never execute broker, job, package, application, SQL or migration code.
The channel's configured protocol/address/scope and group are known scalars or
unknown reasons. Names, addresses and matching schemas create no graph identity.
An emits relation declares source emission; delivered_to declares a configured
subscription. Neither establishes broker acceptance, deployment or delivery.
Retries/dead_letters describe configured alternatives; no retry loop is unfolded.
A job trigger is static cron(expression/timezone), interval(duration), manual or
unknown(reason). No scheduler or run history is provided.

Messages declare fieldInventory complete/partial/unknown. Each event_field is
identified by message+section(payload/headers/key)+structural property/items path;
maximum500 fields per message. Consumer/job dispatch is complete/partial/unknown;
partial/unknown preserves an unresolved handler remainder, alongside any known
handlers. Maximum50 known handlers plus one unresolved remainder. A handler owns
its explicit flow. Missing external repository/source stays a boundary.

## Query and preserve exact selectors

Use `query_backend_events` (REST POST `/api/backend-projects/{id}/events/query`,
operation queryBackendEvents). Every request has projectId and exact revisionId
of source5; REST projectId is in the path. Inputs are strict named variants:

| view | Optional selector | Meaning |
|---|---|---|
| routes | seedNodeId | Exact emit-step, message, channel or consumer UUID; omit for project scope. |
| jobs | serviceId | Exact owning service UUID; omit for project scope. |
| service_calls | serviceId | Exact calling service UUID; omit for project scope. |

All permit limit1–100(default50) and cursor. Routes forbids serviceId;
jobs/service_calls forbid seedNodeId. Proposal, null, mixed, unknown and wrong
variant members fail.400 invalid input/cursor;404 missing/foreign pin;
422 unsupported source profile;413 body/admission budgets on relevant surfaces.
An oversized event scan itself returns the pinned diagnostic page below.

1. Resolve head once if requested; save revisionId, semanticHash and coverage.
   Never substitute latest for a missing historical revision.
2. Page the selected view and filter. Routes joins exact emits+delivery edge
   identities through the same message/channel UUIDs. One edge pair remains one
   tuple even with unknown dispatch. Equal names create no pairs.
3. Preserve the discriminated item kind route/job/service_call/boundary and its
   corresponding payload. Read references, dispatch, related routes, emitContext
   and witness rather than flattening a boundary into a successful route.
   References retain producer/message/channel/consumer/emitsEdge/deliveryEdge IDs;
   dispatch retains exact handlesEdge/handler/flow or unresolvedTarget IDs.
4. Open returned producer/consumer/handler/flow IDs and get_backend_node/graph/
   evidence at this same pin. Reverse selection by consumer shows orphan
   subscriptions without a discovered producer. This means absent in analyzed
   scope; it cannot establish globally absent producers. Partial dispatch retains
   known flow plus unknown remainder.
5. Jobs shows configured trigger/dispatch; service_calls shows explicit
   call-step→operation→handles→owned flow and targetServiceId. A calls edge is
   required; matching URL/method/name invents no target. External/unresolved
   operations stop navigation, and absent request/response mappings stay absent.
6. Emit context retains local flow/transaction IDs/status/reason and available
   control witness. An emit before local commit shows a static possible ordering;
   it establishes no transaction/delivery atomicity across services or connections.

Check each response projectId/revisionId/semanticHash/policy/view and exact
seedNodeId/serviceId against the request pin before presenting or caching it.
Policy depends on the target: source-events-projection-v1 for a source5
revision, events-source6-query-v1 for a source6 revision, and
effective-events-v1 for a full changeProposal (whatever its baseline). Expect
the one the request's target implies. Continue nextCursor with identical
project/revision/hash/policy/view/seed/service/effective limit. Reset cursors when
any member changes. Cancel obsolete requests and discard mismatched late replies.
UUID ordering is deterministic pagination order, not execution order.

## Bounded enumeration and diagnostic pages

Limits: examine100000 total revision edges, construct5000 items, admit20000
auxiliary records, and retain256 graph IDs and256 evidence IDs per witness;
page50default/100max. Dispatch, related routes and control witness lists also
report witness_limit. These are work budgets, not a whole-response byte/RSS bound.

`scanPolicy:"complete-scan-admission"` is conservative: a revision with more
than100000 total edges returns empty items, truthful totalEdgeCount,
examinedEdgeCount0, complete:false, truncated:true and edge_limit. It performs no
partial adjacency/tuple discovery. At admitted size each distinct edge is
scanned once; reserve tuple/auxiliary slots before allocation. Ownership tracing
has depth256; ownership_depth exposes an incomplete boundary when exceeded.
Item/auxiliary/witness exhaustion exposes item_limit/auxiliary_limit/witness_limit
and propagated limitations. Retain every truncationReason, counts and coverage.

`complete:true` means the selected bounded projection enumeration completed.
It proves no source completeness, execution or successful delivery. Ordinary
pagination may have nextCursor while complete:true/truncated:false. A final empty
cursor does not erase truncation. An empty diagnostic/truncated page establishes
no absent dependency; do not relabel a union of smaller queries exhaustive.
Witness provenance is source and worst status is explicit/inferred/stale/unresolved.
Authored EventModel projections have distinct editor provenance and never create
source routes. Stale/unknown/unresolved route/handler proof remains a boundary.

## Contextual field lineage

A source5 event ValueRef is exactly
`{kind:"event_field",nodeId,endpointId,routeId}`. nodeId identifies the message
field; endpointId the exact emit step or consumer; routeId the emits or
delivered_to EDGE. Import uses nodeKey/endpointKey/routeKey and edge resolution.
Full equality, cache keys, selected values and cursor seeds include all three
UUIDs. One field at one consumer on two delivery edges is two distinct values.

Serialization maps producer ports to producer-scoped fields under the emit step.
Deserialization maps consumer-scoped fields to local ports in an explicitly
handled flow. Missing handlers acquire no fabricated destination port.
Transport is a separate explicit field_mapping under the consumer with
`transport:{emitsEdgeId,deliveryEdgeId}` (Keys on import). Its ordered producer
sources and consumer destination reference the exact same declared
producer/message/channel/consumer tuple; routeIds equal its transport edge IDs.
Matching field shape/name/channel never creates transport. Cross-channel or
unrelated delivery pairs fail validation. Multi-input mappings preserve all
ordered sources and prove no simultaneous execution.

Call query_backend_lineage with exact revisionId, full seed, direction
forward/reverse, maxDepth1–32(default8), limit1–100(default50), cursor. Source5 uses
field-lineage-traversal-v2; source4 retains field-lineage-traversal-v1. Inherited
column/port/api_field addresses remain strict. Budgets retain64 sources/mapping,
5000 visited full values,5000 mappings,20000 incidences and depth32; on source5
the mapping/incidence limits apply while the index is built, before traversal,
so a larger revision answers truncated:true (mapping_limit/reference_limit)
whatever the seed (flow.md). Unknown
transform, unknown delivery, stale/unresolved tuple or proof stops expansion;
retain mapping/ref/evidence and requiresReview. An independent query beyond a
boundary cannot be stitched into a proven path. Preserve full pin/policy/seed/
direction/maxDepth/limit and cancel/discard obsolete or mismatched responses.

## Export one blocking gap and check its outcome

Capture project/revision/semanticHash, selected view/filter, exact node/edge/route/
ValueRefs, bounded fragment, evidence IDs and source repository/snapshot/path/
hash/physical lines, inspected/search scope, question and completion criterion.
Export prepares input to external investigation; it schedules or spawns nothing.
Pure questions authorize reads. Evidence publication follows the explicitly
authorized full compatible import8 procedure, not a gap-only/incremental API.

Supported example: in the inert orders fixture, consumer.fraud is registered by
plugin key fraud-v1 but baseline has no plugin source. Criterion: exact fraud-v1
registration and concrete handler body with analyzed member proof, a handles
edge and owned flow on the SAME consumer/subscription. Capture baseline pin and
wiring.ts.txt lines17–18. Once fraud-plugin.ts.txt lines2–6 are supplied, hash and
analyze the five-member source scope and reconcile the SAME project/repository/
provider5→5. Preserve exact existing route UUIDs. Replace consumer.fraud dispatch
with complete only on justified proof; add handler.fraud/flow.fraud and concrete
handles.fraud; explicitly delete old unknown.fraud/handles.unknown.fraud/contains
assertions with pinned expected IDs only after full verified deletion gates.
Static files/endpoints/datastores inventory may be complete only for these
supplied members; schema details/external dependencies stay unknown. Stage
whole-scope bundles/receipts, ready preview, independent full source audit bound
to candidate/base/project CAS, then first commit. Replay uncertain writes exactly.
Query events and Flow at commit.result.revision.id and semanticHash, verify the
criterion against concrete handler/flow/proof, and retain the original pin.
A separate initial resolved project is not an evidence reimport outcome.

Unresolved example: consumer.legacy refers to ../private-ledger/cancel, outside
all supplied analyzed members. Inspect wiring.ts.txt lines21–22, record the
five-member inspected scope and missing module, retain expectedKind handler,
searchScope and concrete missing-source reason. Requery the acknowledged pin:
legacy remains a boundary, including after fraud's supported reimport. A missing
module establishes no absent dependency and licenses no guessed handler or empty
progress commit. Dynamic audit dispatch likewise keeps its known flow and unknown
remainder. Both outcomes are exercised by the durable real public SDK example
TestBackendEventsRealSDKGuideFixtureExample; this is product interface QA.
Ordinary/live-agent A21/S19 acceptance remains deferred.
