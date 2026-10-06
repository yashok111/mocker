# Business event maps: explicit intent and implementation evidence

Require `backend-business-map`, admitted `business_map`, `backend-diagram-v1`
and `diagram-view-v1` from the same exact capability/guide set.

Use `create_backend_diagram {projectId,document,idempotencyKey}` with
`document:{format:"backend-diagram-v1",kind:"business_map",target,payload}`.
Only exact source `revisionId` or full `changeProposal` targets are admitted.
The closed payload is `{architecture?,elements:[],links:[]}`. There is no builder,
process inference, new migration, policy evaluator or broker execution.

Each element has `id,label,origin,refs,role,responsibility` and optionally
`architectureElementId`. Roles are actor, command, business_event, policy,
read_model and question. Each link has `id,label,origin,refs,from,to,relation`.
All arrays are nonnull. Origins are exact source assertions or authored reasons.
Keep an authored event with no message evidence and its implementation gap.
Business-event IDs are distinct from transport-message refs; two messages can
support one event. Several business concepts mapped to one message remain
explicitly distinct with a shared-message ambiguity gap.

Closed relation matrix:
- actor → command: initiates;
- command → business_event: produces;
- business_event → policy: reacts_to;
- policy → command: issues;
- business_event → read_model: updates;
- actor/command/policy → read_model: reads;
- question → any other element: questions.

Cycles express intent; they do not run jobs. Responsibility/card positions never
add causal links. A question is not a source fact or a diagnostic finding.

Use `get_backend_diagram`, `query_backend_diagram` sections elements/links/members/
gaps, and `compare_backend_diagrams` with exact pins. No C4 level/root is accepted
for business maps. Business rows are tagged business_element/business_link.
Optional C4 dependency must have the same targetHash; membership is exact and
immutable on save. Changed dependency or target requires `fork_backend_diagram`;
a new target needs its explicit C4 pin. Missing historical refs/members remain gaps.

`save_backend_diagram` uses expectedVersion and an idempotency key. Replay the
same request after an uncertain result; do not overwrite it with new edits.
`create_backend_diagram_view` / `save_backend_diagram_view` only change layout,
search and selection. Old view pins cannot advance: create a new view instead.
Raw historical receipts, provenance and selections survive restart.

Desktop: select or create a business map in Workbench, use the labelled card/link
forms or exact JSON to author it, and inspect all mappings through the list.
Open C4 at the selected element's exact level, inspect exact implementation refs,
and use Back to restore the map pin/selection. “Спроектировать изменение” hands
selected exact refs to explicit proposal command forms on a matching target.
The quick action fills a node rename locally; other ref kinds remain visible for
explicit typed commands. Preview/apply is separate. Saving a map never changes
proposal, API or scenario data. Imported code, SQL, jobs and brokers are not run.

B5.1 diagnostics and B5.2 portable transfer/materialization/SVG use separate
explicit workflows. Read `backend-portable` for semantic transfer. B5.3 replay,
B6 observations and ordinary/live-agent acceptance remain separate gates.
