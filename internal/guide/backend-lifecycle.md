# Entity lifecycle: pinned claims and desired rules

Require `backend-lifecycle`, admitted `lifecycle`, `backend-diagram-v1` and
`diagram-view-v1` from the selected inspect guide set. Existing States artifacts,
source schemas1–6, Store22 and Flow/Database views retain their original formats.
Business maps, B5 diagnostics/export/replay and B6 observation acceptance remain open.

Select an exact source `{revisionId}` or full
`{changeProposal:{proposalId,proposalRevisionId}}` target. Read its entity and
actual state fields. Current source entity kinds are table/view/domain_entity/dto/
api_schema; fields are column/representation_field/api_field/event_field and must
belong to the selected entity. Unsupported projection kinds return422; a label is
never a field reference. Multiple fields need `compoundMappingReason`.

`build_backend_lifecycle {projectId,target,stateDiagram:{locator,rowId},entity,
stateFields,compoundMappingReason?}` (POST `/api/backend-projects/{id}/diagrams/lifecycle/build`)
reads an exact `states` projection row of kind state_diagram. Copy locator and rowId
from `query_backend_artifacts`; never invent JSON pointers or use the current API
head. It returns `{document,targetHash,gaps}` without writing any domain rows.
The owner snapshot is bounded and source resolution caps inspected objects at250000.

The candidate is partial authored intent, reason "pinned state diagram", with exact
artifact row refs. UUID identities include owner/embedded contract/diagram/member
identity and exclude revision/hash. Explicit string state values become JSON string
encodings; omitted values remain omitted. Guards are serialized opaque text, never
evaluated. Only explicit source-operation bindings become triggers; unknown bindings
produce gaps. No writes, events or transitions are fabricated from names/enums.

Explicitly accept using `create_backend_diagram {projectId,document,idempotencyKey}`.
The accepted document and targetHash must match the candidate. Alternatively author
an explicit payload containing entity, stateFields, states, transitions, rules,
coverage and coverageOrigin. Required arrays are nonnull and objects are closed.
State `value:{json:"..."}` holds exactly one lossless JSON scalar, including null;
numeric strings must not be parsed through a floating-point representation.

Transitions have exact state endpoints, triggers/writes/events refs and a guard
`{kind:"none"}` or `{kind:"opaque",text}`. Rules have id/from/to/trigger,
verdict allowed/forbidden and authored origin/reason. Duplicate or conflicting
from/to/trigger rule triples reject atomically. A terminal state with an outgoing
transition retains that transition and exposes a gap. Partial coverage never proves
an absent transition forbidden, impossible or unreachable. Desired rules do not
create source claims or verified runtime violations.

`query_backend_diagram` uses the exact pin and omits C4 level/root. Elements returns
states/transitions/rules; links returns transitions; members requires subjectId;
gaps exposes current unknowns. Page with unchanged pin and filters. Canvas bounds
200states/600transitions do not restrict accessible list/search inspection.

Semantic `save_backend_diagram` uses expectedVersion/key. Preserve local edits on409;
unknown outcome retries the identical request. Target changes require explicit fork;
retained missing refs/evidence become historical gaps, never current proof. Compare
uses stable row IDs and also reports entity, field mapping and coverage changes.

View create/save is separate from semantic save: layout positions/collapse/filter/
selection cannot change the pinned diagram. Create a new view for a newer semantic
version. C4/detail/Back and old bookmarks retain exact target/pin/selection through
restart. Opening or moving a lifecycle never applies States or runs a simulation.
