# Exact saved editor projections (inspect11)

Canonical owner: `mocker-backend-inspect` workflow11. Select its complete advertised
requirements in one guideSetId/manifestHash before using this topic. Require
`backend-editor-projections` and viewSchemaVersions `backend-editor-artifacts-v1`,
retaining `backend-api-artifact-pins`, `api-artifact-pins-v1`, saved views and
source schemas3/4/5. This is a tagged backend artifact context contract; source
schema5 is eligible and store is19. Source1–4/legacy contexts retain their supported
reads. Authored EventModel is saved design intent; it supplies no source event
proof, runtime execution or delivery guarantee. Source5/events-service-v1 and
job source semantics use the separate backend-events topic under inspect11.
Ordinary-agent acceptance/live evaluation is DEFERRED by the user.

## Source6, full drafts and READY candidate reads

Choose exactly one selector: revisionId; proposal:{proposalId,proposalRevisionId} for legacy relational drafts; changeProposal:{proposalId,proposalRevisionId} for full drafts; or importCandidate:{importId,importVersion,candidateHash} for current READY staging. Mixed/duplicate/null tags and implicit head fallback are invalid. Use canonical UUIDs and exact signed-int64 numeric tokens in raw MCP; browser clients must refuse values they cannot preserve exactly.

Graph/node/evidence/coverage accept the four appropriate basic read variants. get_backend_assertions accepts native source6, full proposals with source6 baseline, and composed READY candidates. Source5/legacy/full5 assertions return422. Keep every provider-qualified identity, losing claim, typed selection and own/dependency/field currentness visible. Full assertions and baselineEvidence do not confirm an edited intended value.

Preserve returned complete pins across pages/details: target/hash, view/structural versions, selected base revision/semantic hash, source vector/snapshot IDs and artifact context. Native source6 uses view tag6; full and staged views use proposal-graph-v1 and import-candidate-v1. Older source1–5 response shapes remain supported. Exact graph id cannot combine with cursor/kind/search/parent/from/to; omit node-only search/parentId entirely for edges. Exact evidenceId cannot combine with subjectId/cursor.

Artifact projections keep their artifact pins in pins and expose graph/effective context in effectivePins. Preserve both; artifact content/raw/authored hashes and graph semantic/candidate hashes are different contracts.

Database/Flow/lineage/events/artifact queries support their exact source/full targets. Legacy proposal specialized support is limited to Database; Flow, lineage, events and both API/editor artifact projections reject proposal with422. Source API-artifact reads technically accept schemas1–5, including an empty bindings result; a meaningful source owner/pin workflow uses source4–6. Do not turn that read admission into legacy-proposal artifact support. Staging supports basic graph/node/evidence/coverage/assertions only: specialized queries and SavedViews reject importCandidate. New batches, Preview versions, Commit or Abort can invalidate staged pins. A stale candidate409 never switches to source.

When original proof is historical_metadata, label it as historical metadata testimony and show its original source5 basis and semantic-support gap. Verify the original revision/project/schema/hash before opening that exact evidence ID. An unchanged selected provider claim can still have a stale dependent field; report both levels rather than promoting the entire record to current.

## Identity and frozen scope

Read `get_backend_project` for actual currentRevisionId/version. Historical reads
use the explicitly selected backend revision, its semanticHash and complete
sourceSnapshotIds vector. A pin is exact `(kind,id,revisionId,contentHash)`;
kind is api_design or design_scenario. Owner IDs/revisions/snapshot versions on
these generic surfaces are canonical positive decimal int64 strings, including
values above JavaScript's safe integer range. Never coerce them to Number or
replace a missing revision with latest. Backend project/revision/node IDs are
UUID strings. expectedVersion is the matching backend project's integer CAS.

One group is `(kind,id)`, with one owner revision shared by all its associations.
An editor binding identity is `(kind,id,complete canonical selector)`, including
embeddedContractId. Its sourceNodeIds is a set of actual pinned backend UUIDs;
several authored objects may share those nodes. Names, indices, labels, method,
path, workspace keys and apparent similarity do not establish cross-owner
identity. Owner selectors must exist uniquely in that exact saved owner.

Scenario states/rules use the saved embedded contract selected by
embeddedContractId. Their locator retains the scenario pin, full owner pointer,
contract ID/mode/documentHash and exact origin designId/revisionId/version when
present. Linked origins are verified lazily at their exact saved revision and
positive version using the API owner identity projection equality policy. Raw
API contentHash and embedded documentHash remain distinct. Verified origin may report
updateAvailable/currentDraftRevisionId without changing the pin. Missing,
mismatched or budget-limited origins keep qualified saved content and diagnostic
status; they do not acquire verified linked evidence. Copy and legacy-copy
contracts stay independent, even if their bytes equal an API design. Their
actual origin.version may be the string "0" and must remain "0"; it is distinct
from positive owner/snapshot/linked versions. No coercion, inferred association
or origin fetch can turn a copy into verified linked data.

## Read projections and raw snapshots

`query_backend_artifacts {projectId,revisionId,artifact:{kind,id},view,
embeddedContractId?,limit?,cursor?}` selects exactly one frozen group. Views are
sequence (scenario), states, response_rules and event_model (scenario). Scenario
states/response_rules require embeddedContractId; API versions forbid it.
EventModel includes owner event data and EventMap diagnostics/locators.

Every page returns exact revisionId/semanticHash/sourceSnapshotIds, the full
pins vector, selectedPin, hashPolicy, view/embeddedContractId and the sorted
COMPLETE selected-group apiBindings/editorBindings with bindingsComplete:true.
The roster is independent of item pagination, view and live owner availability;
it includes bindings whose objects are off-page or missing. Preserve/validate
the entire roster before an edit. Continue nextCursor with identical pin/context,
view and selectors; reset after any change. Page defaults50/max100. Zero items,
one page, or a readable unsupported object does not prove exhaustive success.

Projection items retain locator, exact authored data, objectHash, sourceNodeIds,
optional bindingSelector and diagnostics. Bind only when bindingSelector exists
and strict owner validation succeeds. Readable auxiliary locators include
fragments/branches, individual states, response edges, EventMap nodes/edges,
servers/schemas/contracts, copied HTTP operations and unsupported raw constructs.
Their full pointers/keys remain readable even outside mutation selector bounds.
Do not synthesize a selector for an auxiliary row. The nine binding variants are:

| kind | Required owner IDs | Scope |
|---|---|---|
| participant | participantId | scenario |
| sequence_message | messageId | scenario |
| event_channel | channelId | scenario |
| event_message | messageId | scenario |
| event_operation | contractId, operationId | scenario |
| state_diagram | diagramId | API, or scenario + embeddedContractId |
| state_transition | diagramId, transitionId | API, or scenario + embeddedContractId |
| response_rule | ruleId | API, or scenario + embeddedContractId |
| response_node | ruleId, nodeId | API, or scenario + embeddedContractId |

Unknown/duplicate/null/mixed selector fields are rejected. Sequence owner IDs
retain their full authored strings; event IDs retain owner admission. API
state/rule selectors do not accept embeddedContractId. The existing API-operation/
schema selector contract in backend-flow-reference remains separate and strict.

`get_design_scenario_artifact_snapshot {scenarioId,revisionId}` returns exact
stored documentJSON and formDraftsJSON, storedContentHash, documentHash,
hashPolicy:"design-scenario-envelope-v1", typedStatus and envelopeVerification.
contentHash is present ONLY for verified envelopes. Supported typed content has
verified envelope/hash; unsupported saved constructs keep stored metadata/raw
bytes with typedStatus:"unsupported", envelopeVerification:"unavailable" and no
contentHash. Invalid JSON/drafts/hash syntax or supported hash corruption is an
owner error. Strict owner snapshot checks remain strict. `get_api_artifact_snapshot
{artifactId,revisionId}` retains its existing exact raw API document contract.

Scenario contentHash verifies the owner's normalized document+formDrafts
REVISION ENVELOPE, including drafts; documentHash hashes raw documentJSON alone.
API pin contentHash uses api-design-raw-document-v1 (exact saved raw API bytes).
Embedded documentHash hashes its saved raw document separately. Selected authored
objectHash uses backend-editor-object-v1 over the lossless saved authored object,
including all message/transition/rule fields and exact numeric spellings. API
operation/schema object hashes retain their existing B24 policy. These hashes
have different inputs and cannot substitute for each other. Hashes are lowercase
SHA256 hex; guide identity/topic hashes separately use the sha256: prefix.

Report authored projection completeness, bindingsComplete, origin verification,
source coverage/evidence and runtime completeness separately. complete/coverage/
truncatedReasons and resolution diagnostics qualify the requested projection;
complete authored rows do not establish verified origin, complete source or
execution. Unsupported/resource-limited views retain exact pin/full roster and
raw snapshot navigation with diagnostics. Source navigation opens each backend
node/evidence at the same backend revision; imported evidence remains inert.

## Authorized full replacement, preview and Apply

Pin/association writes require explicit task authorization. Reads write no owner
or backend state. Fetch actual current project/head/version and the chosen group
roster. `preview_backend_artifact_pins {projectId,baseRevisionId,expectedVersion,
commands}` accepts set_artifact_pin or remove_artifact_pin. A set contains
artifact:{kind,id}, exact revisionId, complete editorBindings, reason, and for
API complete explicit apiBindings. Scenario forbids apiBindings. Each editor
input is ONLY {selector,sourceNodeIds}; the service freezes hashes/labels/manual
origin. Empty vectors allow a projection-only pin. Set REPLACES BOTH collections
for that group; omitted objects are intentionally removed. Untouched groups
remain frozen. Never construct replacement from visible items or one page.

Review every diff, diagnostics, source vector and pins; require canApply:true.
`apply_backend_artifact_pins` sends the EXACT preview inputs plus candidateHash
and a captured idempotencyKey. baseRevisionId must equal current backend head;
expectedVersion is the actual current project version. CAS/base/candidate
conflicts require explicit reconciliation and a new preview. Semantic hashes
use current source anchors + complete pins/associations (backend-editor-artifacts-
semantic-v1); label/reason changes do not infer source behavior. Candidate hashes
(backend-editor-artifacts-candidate-v1) also bind original ordered commands/reasons
and the full preview. Retain original body/key before sending.

Timeout/network/5xx leaves an unknown result: retry the original body/key through
the same transport before new work. Durable receipt replay returns exact prior
bytes even after owner/project disappearance and precedes new reservation.
A confirmed409 keeps intent/buffers; explicitly reload actual current head AND
version, then its complete chosen-group roster before deliberately navigating,
reconciling and repreviewing. A disappeared group or failed read is reported;
never silently convert an update to removal. A late receipt does not replace a
newer selected/current host context; its exact revision is available in history.

remove_artifact_pin requires artifact+reason and forbids revision/binding fields;
it explicitly removes the whole group and both collections. Review both rosters
first. Old preview/apply_backend_api_pins remain API-specific wrappers: in a v2
context an API set preserves editor bindings and shared revision consistency;
removal is blocked if editors exist. To unlink the last API association while
retaining editors, use generic set with apiBindings:[] and the COMPLETE retained
editorBindings. Legacy API-only v1 behavior remains unchanged. Source reconcile,
metadata/proposal/history reads carry frozen associations; a missing source node
stays an orphan with labels until an explicit replacement/removal.

## Independent admission and work budgets

Capabilities limits expose maxEditorArtifactContextBytes,
maxEditorEventConstructionBytes and maxEventMapBytes separately (each4194304).
Their scopes below remain distinct even though the numeric budgets agree.

Commands≤20; full vector pins≤20; total API+editor bindings≤200; each editor source
set1–100. Original REST body/MCP Arguments≤128KiB including whitespace. Generic
Preview/Apply additionally reserve canonical Apply arguments with projectId,
base/version/commands, hash64 and maximum128 printable ASCII key including worst
JSON escape cost (128 backslashes). Configured allowance is
min(128KiB,max(0,global MaxBody−126)); Arguments fit128KiB and Arguments plus126
bytes required numeric-int64 tools/call RPC framing fit global MaxBody. Required
framing omits optional _meta/inputResponses/requestState. Optional metadata,
string request IDs and padding remain subject to the actual unchanged global raw
cap outside this canonical guarantee. Early413 backend_artifact_apply_body_limit
reports allowedBytes/reservedApplyBytes and configured globalMaxBodyBytes/
rpcFramingBytes/reservedRPCBytes. Legacy API-specific/private wrappers stay under
their existing policy. Preview admission does not authorize any write.

NEW full tagged v2 context JSON≤4MiB per revision, including both binding
collections and frozen labels; exact UTF8/escape bytes are counted before
persistence. backend_artifact_context_limit413 reports allowedBytes and actual
size/lower bound. It neither retroactively constrains old v1 nor caps a whole
response page or process RSS. Diff bounds are1000 inclusive entries and2048
escaped-byte paths; true truncation blocks Apply. Reason/labels≤4096 UTF8 bytes.

A shared request cache allows20 distinct immutable owner snapshots across
old/new/top/nested reads; failed distinct attempts count, repeated identities
reuse the snapshot/tree. Selected-object origins are lazy; projection-only pins
need not fetch all origins. Read exhaustion qualifies origin as unverified;
required mutation resolution overflow returns backend_artifact_work_limit413.
Owner raw read admission follows configured MaxBody and remains separate.

EventModel preflight BEFORE AnalyzeEventMap admits at most4MiB of accounted
construction work (1024 per prospective row plus escaped scalar costs),5000nodes,
10000edges and2000diagnostics BEFORE compaction. construction_* diagnostics keep
exact pin/roster/raw access and mark incomplete. Owner final EventMap output has
its independent4MiB/5000nodes/10000edges/2000diagnostics bounds. Construction
accounting is not exact heap/RSS, final output, full context or a whole-page cap.
Other views/direct selectors keep their own owner bounds.

## Public synthetic linked and divergent-copy example

Assume a disposable public fixture: API design501 revision601 version4 contains
state diagram order-state; scenario9001 revision7001 embeds it as contract
linked-order, mode linked, source {designId:"501",revisionId:"601",version:"4"}.
Independent scenario9002 revision7002 embeds a divergent local copy as copy-order,
mode copy, with its actual legacy origin.version:"0". These are synthetic IDs;
obtain actual IDs/pins from your own authorized fixture, never infer them.
A source4 project has node UUID11111111-1111-4111-8111-111111111111. After explicit
authorization, preview these two complete projection/binding groups:

```json
{
  "projectId":"22222222-2222-4222-8222-222222222222",
  "baseRevisionId":"33333333-3333-4333-8333-333333333333",
  "expectedVersion":3,
  "commands":[
    {"type":"set_artifact_pin","artifact":{"kind":"design_scenario","id":"9001"},"revisionId":"7001","editorBindings":[{"selector":{"kind":"state_diagram","embeddedContractId":"linked-order","diagramId":"order-state"},"sourceNodeIds":["11111111-1111-4111-8111-111111111111"]}],"reason":"Inspect saved linked design"},
    {"type":"set_artifact_pin","artifact":{"kind":"design_scenario","id":"9002"},"revisionId":"7002","editorBindings":[{"selector":{"kind":"state_diagram","embeddedContractId":"copy-order","diagramId":"order-state"},"sourceNodeIds":["11111111-1111-4111-8111-111111111111"]}],"reason":"Inspect independent authored copy"}
  ]
}
```

Apply only that accepted preview with its returned candidateHash and captured
key. Save the returned backend revisionId/version/semanticHash/full pins vector.
For each scenario query states with that exact returned backend revision,
artifact:{kind:"design_scenario",id:"9001"|"9002"} and respectively
embeddedContractId:"linked-order"|"copy-order". Page all items while checking
unchanged complete rosters. Open raw scenario snapshots at7001/7002, then navigate
source node/evidence using get_backend_node/get_backend_evidence at the same
backend pin. The linked locator can verify its exact601 origin; the divergent
copy remains copy with its saved documentHash and string0 provenance.

If newer owner drafts/backend revisions appear, reopen the retained historical
backend revision and the same owner7001/7002 snapshots; compare_backend_revisions
can show editor_artifact/artifact_group context changes explicitly. Never replace
those pins with latest. Pinned panels preserve dirty current editor buffers,
string IDs and safe legacy navigation gates. Pin reads expose no Save, Restore,
Publish, ApplyRule or Execute action. Projection/snapshot/source/history reads
perform no owner writes, simulation or rule execution. The earlier authorized
backend association Apply does not edit the API/scenario owners. This example
claims saved authored projections and manual source associations only.
