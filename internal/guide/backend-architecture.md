# Exact C4 architecture mappings

Negotiate inspect13 and `backend-diagrams`, `backend-architecture`,
`backend-diagram-views`, `backend-diagram-v1` and `diagram-view-v1`. `architecture` and `interactions` are admitted. See `backend-interactions` for
static behavior; lifecycle and business_map are admitted companion views. Source schemas1–6 and legacy saved-view-v1/v2 are unchanged.

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

When `diagramSupport.membershipFormats` includes `exact-node-members-v1`, a
responsibility element may add `membership:{format:"exact-node-members-v1",
nodeIds:[...exactNodeUUIDs]}`. This is an immutable exact set in that diagram
version, not inferred descendants. Its1–5000 node IDs must be unique and disjoint
from the element's inline node refs. At most20000 membership IDs fit one document;
the existing1MiB document limit still applies. Inline refs remain bounded at100.
Membership participates in ownership, relationship projection, evidence resolution,
portable identity remapping and paginated `section:"members"` reads. Old versions
keep their original sets. Use the actual responsibility boundary; do not invent
extra applications merely to fit refs.

Before saving, call `preview_backend_architecture {projectId,document,level,
rootId,section:"elements",origin:"all",search:"",limit:100}`. Repeat for links,
members (subjectId required) and gaps, following every cursor. The preview returns
documentHash and targetHash, counts, bounded gap summary and paginated rows; it
creates no diagram or saved view and exposes no saved pin. Keep the same document,
level/root and filters across pages. Changing membership or any document field
invalidates the cursor. Inspect ambiguity, missing proof and unresolved neighbors
before create. Preview is subject to the same read admission/cache budgets.

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

When `diagramSupport.gapScopeFormats` advertises `exact-node-scope-v1`, compact
query/preview may include `gapScope:{format:"exact-node-scope-v1",nodeIds:[...]}`
with1–20000 unique source-node IDs present at the exact target. This is the
explicit intended scope, independent of current membership. It binds the cache
and all cursors. Gap details gain scope and summaries gain byScope: in_scope
(both endpoints), boundary (one endpoint), unrelated (neither), collapsed_internal
(inspectable internal aggregates), or unqualified (no exact edge to classify).
All gaps and evidence remain present. Without explicit scope no inventory is
automatically called unrelated. Keep the same ordered scope list across pages.

When `diagramSupport.navigationFormats` advertises `architecture-navigation-v1`,
an element may carry up to eight authored `navigation` entries. A diagram entry
is `{format:"architecture-navigation-v1",kind:"diagram",label,diagram:exactPin,
level,rootId,focusId?}`; a Flow entry is `{format:"architecture-navigation-v1",
kind:"flow",label,flowId,target:exactReadTarget}` on the document's exact target. At most128 distinct
destination pins fit one document. Validate actual destination roles and ownership;
never derive a correspondence from labels or C4 parentage. Diagram dependencies
travel with portable closure, and source/element IDs are remapped explicitly.
Inherited historical destinations remain visibly gapped on fork; their pins never
advance. The explorer follows a sole explicit destination on entry, offers
multiple labelled choices in the inspector, and retains browser back navigation.
Source inspection reveals preserved native code and individual facet definitions
behind keyboard-accessible disclosure controls, alongside file/line evidence.

When capabilities advertise `diagramSupport.responseModes:["compact-v1"]`, set
`responseMode:"compact-v1"` on ordinary and `section:"gaps"` queries. The response
then carries `gaps:[]` and `gapSummary:{total,byCode}` for the entire pinned
projection; the empty array does not mean complete coverage. Read all gap records
as paginated rows of `section:"gaps"`. Keep the same response mode, exact pin,
level/root and filters for every cursor. Omitted mode keeps the legacy envelope.

Architecture cache misses admit one graph/projection builder per server Repo.
Excess concurrent misses return retryable `backend_projection_busy` (HTTP503,
Retry-After1s) before graph allocation. Retry the same exact pinned read. The
server retains at most one projection whose serialized data fits24MiB; native
graphs and snippets are not cached. Pins include project, document/version/hash,
target/provenance hashes, level/root and policy. Cache hits still check target
visibility. These bounds are admission/cache budgets, not a measured process-RSS
or latency SLO. Unmapped gaps remain unresolved unless explicit evidence supports
a different classification; a focused view does not automatically excuse them.

Unknown mutation outcome: replay the identical operation, resource ID, key and
body. Replay precedes CAS/quota/ownership changes; a changed body with the same
key returns backend_idempotency_conflict. Definitive version conflict requires
reread/reconciliation or a new fork. Never overwrite a saved request to retry.

Caps: semantic document1MiB,1000elements/3000links,1000documents/project,
1000versions/document,256MiB logical/project; refs100 and evidence20 per element,
labels256UTF8 bytes, reason4096. Views128KiB,1000views/project,
1000versions/view,64MiB logical/project. Query default100/max500; canvas200/600
is separate from whole-projection search/paging and250000 traversal budget.

B5.2 portable transfer and exact SVG are separately available: read
`backend-portable` and the tool reference. B5.3 replay and B6 observations
remain outside these diagram APIs. Do not
execute inspected applications, SQL, jobs or brokers. Ordinary/live-agent
acceptance beyond this static workflow remains deferred.

Compare includes payload edits and canonical Context aggregate membership at each
version's primarySystemId, including collapsed internal groups. A changed members
field or removed aggregate ID is inspected with the before pin, level=context,
rootId=before.document.payload.primarySystemId and architecture-v1; never resolve
it against the newer mapping. Target/evidence pin churn alone is not a membership
change. Projected identities are checked against full keys and semantic IDs.
