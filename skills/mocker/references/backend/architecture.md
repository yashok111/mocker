# Exact C4 architecture mappings

Negotiate inspect9 and `backend-diagrams`, `backend-architecture`,
`backend-diagram-views`, `backend-diagram-v1` and `diagram-view-v1`. Only
`architecture` is admitted. Interactions/lifecycle/business_map are not delivered
capabilities. Source schemas1–6 and legacy saved-view-v1/v2 are unchanged.

A diagram is an immutable companion to `{revisionId}` or
`{changeProposal:{proposalId,proposalRevisionId}}`. Legacy DB proposals and
import candidates cannot own it. No mapping means incomplete architecture, not
permission to invent boundaries from folder/repository names. A pure inspection
uses read/query/compare only; creating, saving or forking requires explicit intent.

Each element has a stable UUID, label, role, responsibility, technology, origin
and nonnull refs. Roles are person/software_system (no parent), application or
data_store (system parent), component (application parent). Authored boundaries
use `{kind:"authored",reason}`. Source assertions use
`{kind:"source_assertion",evidence:[{revisionId,evidenceId,subjectId}]}` with
actual admitted evidence at that exact target. Record refs are
`{kind:"record",recordType:"node"|"edge",id}`; artifact refs use the existing
exact ArtifactProjectionLocator plus its returned rowId. Never manufacture a
pointer or substitute an artifact head. Authorship comes from authentication.

## Public sequence

1. Read the exact source nodes/dependencies and their evidence. Keep their IDs;
   identical names do not establish correspondence. For Orders, explicitly map
   Customer, Orders and Payments systems, Orders API/worker/DB, and
   OrderService/PaymentAdapter components. Attach the two payment-call source
   endpoint mappings. Preserve the unresolved delivery as a gap.
2. `create_backend_diagram {projectId,document:{format:"backend-diagram-v1",
   kind:"architecture",target,payload:{primarySystemId,elements,links}},
   idempotencyKey}`. Persist the exact request before sending. Capture returned
   pin `{id,version,contentHash}`, targetHash and server-owned provenanceHash.
3. `query_backend_diagram {projectId,pin,level:"context",rootId:systemId,
   search:"",origin:"all",section:"elements",limit:100}`. Repeat for links,
   then containers with the system root and components with the application root.
   Use the returned projection policy/level/root for every selection.
4. For the payment aggregate, query `section:"members",subjectId:aggregateId`
   with the same pin/root/level. Page until nextCursor is empty. Two source
   dependencies must yield exactly two distinct refs even if canvas limits hide
   them. Ambiguous membership is a gap, never duplicated or guessed ownership.
   Source and authored links remain different groups. A source assertion is
   inspectable static proof, not executed runtime truth.
5. `create_backend_diagram_view {projectId,name:"Orders Context",state:{diagram:pin,
   level:"context",rootId:systemId,search:"",origin:"all",selection:null,
   positions:[],collapsedIds:[]},idempotencyKey}`. Record view ID/version.
6. `save_backend_diagram {projectId,diagramId:pin.id,expectedVersion:pin.version,
   document:editedDocument,idempotencyKey}` appends a semantic version. A no-op
   retains the old version and creates its own exact receipt. Save cannot change
   target/kind. `get_backend_diagram_view {projectId,viewId,version}` still returns
   the ORIGINAL diagram pin. A new semantic pin needs create-new-view; a layout
   save uses `save_backend_diagram_view` and the view's expectedVersion.
7. `get_backend_diagram {projectId,diagramId,version,hash}` reads an exact version.
   `compare_backend_diagrams {projectId,before,after,limit:100}` returns stable-ID
   added/removed/changed fields. Inspect removals using the before pin.
   `fork_backend_diagram {projectId,source:pin,target:newTarget,reason,idempotencyKey}`
   creates a new ID/v1 and retains introduction/edit/fork provenance. Historical
   refs/evidence stay historical and gapped, never promoted by a same-name match.

`list_backend_diagrams` and `list_backend_diagram_views` accept kind, limit and
cursor. Their catalog cursors are separate from each other and from legacy
saved-view cursors. Changed catalogs or projection filters reject stale cursors;
restart listing explicitly. Query/compare POSTs are read-only.

## Recovery and bounds

Unknown mutation outcome: replay the identical operation, resource ID, key and
body. Replay precedes CAS/quota/ownership changes; a changed body with the same
key returns backend_idempotency_conflict. Definitive version conflict requires
reread/reconciliation or a new fork. Never overwrite a saved request to retry.

Caps: semantic document1MiB,1000elements/3000links,1000documents/project,
1000versions/document,256MiB logical/project; refs100 and evidence20 per element,
labels256UTF8 bytes, reason4096. Views128KiB,1000views/project,
1000versions/view,64MiB logical/project. Query default100/max500; canvas200/600
is separate from whole-projection search/paging and250000 traversal budget.

Current delivery is A25 architecture foundation. Scoped B5 diagnostics/replay,
portable/SVG and B6 observations are not available through these APIs. Do not
execute inspected applications, SQL, jobs or brokers. Ordinary/live-agent
acceptance beyond this static workflow remains deferred.

Compare includes payload edits and canonical Context aggregate membership at each
version's primarySystemId, including collapsed internal groups. A changed members
field or removed aggregate ID is inspected with the before pin, level=context,
rootId=before.document.payload.primarySystemId and architecture-v1; never resolve
it against the newer mapping. Target/evidence pin churn alone is not a membership
change. Projected identities are checked against full keys and semantic IDs.
