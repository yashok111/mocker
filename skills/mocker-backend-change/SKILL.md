---
name: mocker-backend-change
description: Prepare typed backend graph proposals, resolve three-way rebase conflicts, run durable package/conformance/endpoint review jobs and bind exact saved-draft reports to ready/implemented lifecycle. Use for desired backend changes, source comparison or analysis recovery; inspection alone starts no job.
metadata:
  workflowId: "mocker-backend-change"
  workflowVersion: "5"
  requiredModelSchemaVersions: "[\"5\",\"6\"]"
  requiredViewSchemaVersions: "[\"proposal-graph-v1\",\"backend-diagram-v1\",\"diagram-view-v1\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-revisions\",\"backend-graph-query\",\"backend-change-proposals\",\"backend-change-typed-edits\",\"backend-source-assertions\",\"backend-representations\",\"backend-analysis-jobs\",\"backend-analysis-diff\",\"backend-analysis-impact\",\"backend-change-rebase\",\"backend-change-ready\",\"backend-change-package\",\"backend-conformance\",\"backend-endpoint-review\",\"backend-change-implemented\",\"backend-change-archive\",\"backend-change-unarchive\",\"backend-diagrams\",\"backend-architecture\",\"backend-diagram-views\",\"backend-scenario-measurements\",\"backend-observed-impact\",\"backend-observed-sequences\"]"
  guideSetId: "sha256:d0c798afb3313b1dea1b46de2dfeef5d9287df962bc66acb0d6d25f5978d7c27"
  manifestHash: "sha256:d0c798afb3313b1dea1b46de2dfeef5d9287df962bc66acb0d6d25f5978d7c27"
---

# Prepare a full graph proposal

A full proposal keeps one immutable source baseline and an independent sequence of desired drafts. It does not alter source records, imported evidence, artifact owners or the source head. Desired edges and lineage describe intended structure; they do not prove executed behavior.

## Pin the procedure and baseline

Read `get_server_config`/`get_backend_capabilities`. Select advertised change5, proposal-graph-v1, all required capabilities and a compatible immutable guide set. Verify workflowId/version/guideSetId/manifestHash/contentHash; on mismatch use the complete pinned server `backend-change-proposals` entrypoint. Unknown sets fail. Shared source semantics use import8; exact read/lineage/proof procedures use inspect13; database facets use database7. Verify each actual owner in the same set.

Read the chosen source revision explicitly. Source5 is supported with source5 vocabulary; a full proposal's structural schema6 tag does not upgrade that baseline or grant representation commands. Source6 admits domain_entity, dto, api_schema and representation_field. Select source6 explicitly at creation when those kinds are needed.

Create using `create_backend_change_proposal {projectId,name,baseRevisionId,idempotencyKey}`. Save this exact input before sending, even before proposalId is known. List with `list_backend_change_proposals {projectId,baseRevisionId?,status:"draft"?,limit?,cursor?}`. `get_backend_change_proposal` may resolve a draft once when proposalRevisionId is omitted; downstream reads always use the returned immutable revision ID. Historical requests supply both proposalId and proposalRevisionId.

## Build ordered typed commands

Every command includes a new canonical commandId and a nonblank reason. Keep command order. References use exact UUIDs from the selected baseline/draft or explicit new client IDs. Desired attributes are complete typed groups; there is no arbitrary JSON patch, source owner edit or manufactured evidence.

| type | Typed payload besides commandId/reason |
| --- | --- |
| create_node | id, kind, name, parentId (explicit UUID or null), complete attributes |
| update_node | id, update:{kind,group:"attributes",attributes} or {kind,group:"parent",parentId} |
| rename | recordType:"node"|"edge", id, name |
| remove_node / remove_edge | id |
| upsert_edge | id, kind, from, to, complete attributes |
| alter_column | columnId, facetKey, change: native_type / nullable / default typed complete group |
| alter_constraint / alter_index | action and exact object/facet; create additionally name/tableId/definition, update definition, remove omits those groups. update writes the definition under facetKey and ADDS that facet when the object does not carry it yet (remove and alter_column refuse an absent facet), so read the object's facetKeys first |
| edit_flow_step | stepId, complete attributes |
| edit_branch | edgeId, kind next/branch/error/returns, from, to, complete attributes |
| set_field_mapping | mappingId, parentId, ordered sources, destination, transform, analysisStatus, gaps; optional typed transport/description |
| set_artifact_pin | typed artifact, exact revisionId, complete editorBindings; api_design also requires complete apiBindings |
| remove_artifact_pin | exact typed artifact |
| map_identity | exact source_identity, carried_source_identity or intent_identity target, expectedExternalKey, newExternalKey |
| set_criteria | complete criteria replacement array |

The canonical API has 16 families / 72 closed kind/action/artifact branches. Use that branch's complete attributes and definitions, preserving ordered FK pairs/index terms/ports/mapping inputs and explicit unknown reasons. Omitted, null, empty and false are different values. Source proof fields such as ownership, freshness, evidenceIds/evidenceKeys and sourceSnapshotId are not editable desired groups. Artifact owner documents remain immutable; pins refer to exact owner revisions.

Known graph IDs retain their kind. New object IDs cannot collide or reuse a previously introduced/tombstoned ID in this proposal. Deletion is intent in the proposal; source history remains readable. Repair dangling exact references in the ordered local list before applying.

## Choose a qualified intended identity

Read full-target graph identities at the selected draft. Every source identity is `{recordType,id,repositoryId,providerNamespace,externalKey,assertionHash}`; retain all providers, even if their key spellings match.

For an imported object:

```text
{
  type:"map_identity", commandId, reason,
  target:{kind:"source_identity",source:exactQualifiedIdentity},
  expectedExternalKey:currentIntendedKey,
  newExternalKey:newKey
}
```

`source` pins the original six-field baseline identity. expectedExternalKey compares its current intended key, initially the baseline key, and cannot be null. Choosing one provider changes no other provider's intended key or baseline identity. Never choose by array position, name or bare key.

For a live object created in this proposal:

```text
{
  type:"map_identity", commandId, reason,
  target:{kind:"intent_identity",recordType:"node"|"edge",id:createdId},
  expectedExternalKey:null,
  newExternalKey:firstKey
}
```

Use explicit null only for that first unassigned intent key. Later edits compare the exact assigned string. An imported ID cannot use intent_identity. A removed record cannot be edited through its reserved ID. Source5 derives its one qualified identity from the exact legacy owner/bootstrap hash without altering stored source bytes.

## Preview and apply

Retain `{projectId,proposalId,expectedVersion,proposalRevisionId,commands}` before `preview_backend_change_proposal_commands`. Preview is read-only and reserves no IDs. Inspect all diagnostics and proposed changes/criteria. candidateHash is null for an invalid final graph. The returned proposal/version/draft/base pins must match the captured input.

Changing any command, order, reason, unfinished form or selected draft invalidates approval. semanticHash identifies effective desired meaning; candidateHash additionally binds exact proposal/version/draft/base and ordered command IDs/reasons. They are not interchangeable.

After a matching successful preview, save `apply_backend_change_proposal_commands` with the same expectedVersion, proposalRevisionId, commands, candidateHash and a new stable idempotencyKey. Persist the complete body before transport. Use the returned immutable revision as the saved result; a replay can return an older receipt. Read current aggregate state separately before later edits.

Every accepted commandId stays consumed, including no-ops, criteria-only changes and earlier writes overwritten later in the batch. A new key does not permit reuse: the server returns 409 backend_change_command_conflict. This holds after later drafts, restore and restart. Exact original body/key still replays its original receipt. Only explicitly new edits get new command IDs; an unknown retry keeps the originals.

## Criteria and historical drafts

Criteria are authored statements about desired structure, not evidence of test execution. The 10 closed variants cover object_exists/object_absent for node or edge, edge_exists, field_equals with typed selector/present-value, artifact_object_matches for the two artifact namespaces, test_attachment with exact source/owner references, and runtime_check. Required flags, target UUIDs, attachment scope and unknown status remain explicit. Empty criteria means none declared, not passed runtime checks.

Page history with the selected immutable draft pinned. Source baseline/vector/artifact context belong to that historical revision, not newer aggregate metadata. Restore uses `restore_backend_change_proposal {projectId,proposalId,expectedVersion,proposalRevisionId,restoreRevisionId,idempotencyKey}` after explicit review. It appends a new draft on the same base; it neither replays old commands nor releases command/object IDs. Explicit rebase and ready follow the references below. Implemented/archive/unarchive follow the handoff procedure below. Local undo changes only unsaved commands.

## Read intent and baseline separately

Use `{changeProposal:{proposalId,proposalRevisionId}}` with graph/node/evidence/coverage tools and the supported Database/Flow/lineage/events/artifact query. Preserve full target/hash/base/vector/artifact pins and filters on every page. Native source6 reads use view tag `6`; full reads use `proposal-graph-v1`.

Full-target assertions/evidence describe the exact baseline. Edited properties carry intent command/reason origins and no invented supporting provider proof. A newly created desired field may have no provider evidence. Source6 losing claims remain inspectable. Assertions are unsupported for source5 and full proposals based on source5; use their exact baseline evidence instead. Historical metadata proof retains its original source5 basis and cannot confirm the intended semantic value.

Presentation saving belongs to inspect13/database7. For a full target, explicitly create/save SavedView-v2 with the exact target and returned view pins. The presentation does not update this proposal. Candidate staging cannot be saved as a view.

## Recover without changing an unknown request

Persist Create, Apply and Restore bodies/path IDs before sending. Storage failure must be visible; do not dispatch a request whose unknown result cannot be recovered. Find pending Create at the project level even if the project opens on a newer source base. Restore its original base context explicitly, without rebasing the saved body. Browser recovery never sends automatically.

For an unknown outcome, repeat exactly the saved request/key. For definitive 409, retain the refused attempt, read the current draft and explicitly reconcile intent, then preview a new request. Continuing with unchanged local IDs differs from consciously copying commands as new edits; do not silently regenerate IDs on resend. Keep separate original receipt state, selected historical draft and current aggregate head. No UI recovery step mutates source facts.

## Rebase and static analysis

Read `get_guide {topic:"backend-change-rebase",guideSetId:selected.guideSetId}` before moving a saved draft to an explicitly selected source base. B/O/N conflicts need exact selectors, choices and reasons; ordered repair commands remain real commands. Read `get_guide {topic:"backend-analysis-jobs",guideSetId:selected.guideSetId}` before starting durable diff/impact, cancelling/retrying a job or marking ready. Both topics belong to change5 in this exact set; verify their returned owner/contentHash.

Only in a standalone `mocker-backend-change` installation, the local files relative to that package's SKILL.md are `references/rebase.md` and `references/analysis-jobs.md`. Root-only compatibility and server-topic readers use the pinned get_guide calls above; those leaf-local paths are not relative to the generated compatibility document.

Ready is a static review association with an exact complete saved full-draft impact report. It is never runtime verification. Normal Apply, Restore and Rebase return to draft and clear ready association; older receipts and source/proposal bytes remain immutable. Source5 may retain snapshots with an empty contentHash and has a nonempty derived source vector. Preserve the returned legacy artifact context and exact pins instead of synthesizing source6 facts.

## Structural handoff and endpoint review

Read `get_guide {topic:"backend-change-handoff",guideSetId:selected.guideSetId}` for saved-only package, exact result-source conformance and implemented/archive/unarchive. Read `get_guide {topic:"backend-endpoint-review",guideSetId:selected.guideSetId}` for exact before/after endpoint IDs and explicit null removal. Both topics are change5-owned. Standalone local paths are `references/handoff.md` and `references/endpoint-review.md`; root-only installations use the pinned server topics. No step verifies runtime behavior.

## Architecture companion boundary

C4 mapping edits do not apply source/proposal commands. For a new exact proposal target, explicitly fork the mapping and retain historical evidence/provenance gaps. Read backend-architecture from the same guide set. Diagram compare/query is read-only and never starts an analysis or lifecycle operation.

Interaction edits use the shared diagram save/fork APIs, separately from proposal commands. Target and architecture dependency are immutable within an ID. A new-target dependent fork needs an exact architecture pin on the new target. Read `backend-interactions` under its inspect owner; B5 replay/export and B6 observations remain unavailable.

Entity lifecycle uses the shared diagram APIs and the `backend-lifecycle` inspect guide. Preserve exact source/artifact pins, distinguish authored rules from source claims, and retain partial coverage and opaque guards. Semantic saves/forks and layout-only views are separate; no simulation, B5 diagnostics or B6 runtime proof.

## Business event map

Read `backend-business-map` from the negotiated guide set. Explicitly author
actor/command/business_event/policy/read_model/question and closed role-pair
links. Keep event identities separate from transport messages and unresolved
implementation as gaps. Exact C4/implementation refs and historical pins survive
save/fork; layout-only views do not change semantics. Design change passes exact
refs to explicit proposal commands, never an implicit graph/API/scenario write.
No automatic process inference or policy execution; B5/B6 acceptance stays open.

For static diagnostic jobs and separate finding review, negotiate **verify1** and read `backend-verify` / `backend-diagnostics` from the same immutable guide set. Inspection alone starts no job; review is not runtime proof.

## Measured observations

Read pinned `backend-measurements` and `backend-benchmarks` from the same guide set.
Use exact saved source/observation/correlation/report pins; never enrich from latest.
Observed impact explicitly selects none or pinned. Before source evidence cannot
confirm desired after intent. Unknown builds and inferred paths remain qualified.
