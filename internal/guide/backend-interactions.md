# Static dynamic interactions

Require `backend-interactions`, admitted `interactions`, `backend-diagram-v1`
and `diagram-view-v1` from the selected immutable guide set. Source schemas1–6,
legacy Flow/Database saved views and existing Sequence artifacts are unchanged.
Business_map, B5 export/check/replay and B6 observed-run acceptance remain
open. This view never applies or runs a scenario.

## Read, then explicitly accept

1. Read an exact runtime/event entrypoint on `{revisionId}` or
   `{changeProposal:{proposalId,proposalRevisionId}}`. Legacy DB proposals and
   import candidates are unsupported. Never substitute a current head.
2. `build_backend_interactions {projectId,target,entrypointId,maxSteps:200}`
   (REST POST `/api/backend-projects/{id}/diagrams/interactions/build`) returns
   `{document,targetHash,gaps,visitedObjects,truncated}` without writing a project,
   diagram or analysis job. Optional `architecture:{id,version,contentHash}` must
   share the exact target. Bounds are maxSteps1–1000 and250000 visited objects;
   document limits include participants/steps/branches together. Inspect frontier
   and gaps before accepting. A source assertion is a static claim, not execution.
3. Supply authored declarations when business meaning cannot be established from
   source. A participant has id/label/origin/refs and optional architectureElementId.
   A step has id/label/origin/refs/from, optional to/replyTo, kind
   request/response/send/receive/error/boundary and required branchPath.
   Branches have id, optional parentId, groupId, label, kind
   alternative/parallel/loop, guardText and origin. Order links have stable id,
   from/to step IDs and origin. Payload arrays scopeRefs/participants/steps/order/
   branches are required and nonnull. Missing to is only legal for send/boundary.
4. Explicit `create_backend_diagram {projectId,document,idempotencyKey}` accepts
   the candidate through the normal validator. Its normalized document and
   targetHash must equal the accepted candidate. Never save implicitly on query.
5. `query_backend_diagram {projectId,pin,search:"",origin:"all",section:"elements",
   limit:100}` returns participants/steps/branches. `section:"links"` returns order
   IDs; members requires subjectId; gaps returns boundaries. Omit architecture
   level/root. Page all results using the same pin/filters; cursor mismatch409
   requires restarting the exact query. Canvas limits do not limit search.

## Semantics and history

B/C alternatives never imply B→C. Parallel peers stay unordered even when display
sorts IDs. Missing delivery does not create a receiver, response or retry.
Order is an acyclic partial order; duplicate endpoint pairs and cross-kind ID
collisions reject save. replyTo must address the corresponding request.
Branch paths follow the full parent chain. Unsafe sequence fragments use an
explained accessible list; the static canvas has no execution controls.

`save_backend_diagram` requires expectedVersion and a new idempotencyKey. Adding
or removing an order edge while steps remain unchanged appears in compare by
that edge's ID. Changing its origin keeps the ID. Reordering arrays is a no-op.
`compare_backend_diagrams {projectId,before,after,limit:100}` retains before-side
inspection for removed messages/order/evidence. After a version conflict, retain
local edits and explicitly reread or fork. Unknown result: replay the identical
saved request/key, never overwrite it with another body.

`create_backend_diagram_view {projectId,name,state:{diagram:pin,search:"",
origin:"all",selection:{type:"link",id:orderId},positions:[],collapsedIds:[]},
idempotencyKey}` creates an exact bookmark. Layout-only save uses
`save_backend_diagram_view` with the view's expectedVersion; it cannot change its
semantic pin. A newer diagram needs a new view. Historical selection remains
readable after edge removal, including after restart.

`fork_backend_diagram` takes source pin, explicit target, reason and key. If a
dependent document moves targets, supply architecture on that target. Evidence
from the former target remains historical, with gaps, never upgraded to current
proof by name. C4 → interactions → exact Flow/API/event evidence → Back retains
pins and selection. Inspect `backend-architecture` for C4 projection semantics.
