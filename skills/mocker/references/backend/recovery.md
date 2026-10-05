# Import recovery and pinned reads


This topic is canonically import v8-owned. Database v7 readers explicitly select
its supported import owner in the same global set and verify the actual returned
owner tuple/contentHash. Use it from the selected set before commit
and whenever a response is lost, a session resumes, or CAS fails. Recheck selected
workflow identity after a server change without discarding original receipts.

## Composed and full-proposal recovery

Persist an exact pending request before transport, including Begin/Create before a server session/proposal ID exists. Keep its project, original source base and scope discoverable after restart even when the current head changes. A browser restore waits for explicit retry. Storage failure or invalid recovery must be visible; do not silently send from memory alone or replace an unknown request with a new key.

Composed Begin additionally preserves sourceScope, scopeStatus, syncPolicy, changeManifest and per-partition profileExtension. Decision batches preserve decision IDs, conflictHash/offered contender, batchId/hash and original import CAS. Full proposal Create/Apply/Restore have their own path IDs, selected draft, commands/command IDs, candidateHash and idempotency key. An exact successful receipt is historical output, not current head metadata.

Composed session scope/base/vector remains the captured context. If that context no longer applies, recover the original request first, then explicitly abort/start a new compatible session; do not mutate its saved base/CAS merely to clear409. Annotation/rename can stale project CAS while semantic head stays fixed. Reread metadata, then review a fresh preview and capture a new Commit only after the unknown old result is resolved.

get_backend_import_changes reads saved details by previewVersion even for NEEDS_RESOLUTION. Candidate reads require the current matching READY version/hash and become unavailable after invalidation/terminal state. Clear any old candidate view as soon as a newer session state is admitted, independently of whether a parallel project GET succeeds.

Import Preview itself advances the session version and has no idempotency key/receipt. After a lost Preview response, read durable session status and its saved preview; do not invent a receipt or replay a changed current version automatically. Full-proposal Preview is read-only and reserves no command IDs.

Full proposal accepted command IDs remain consumed after no-op, criteria-only or overwritten writes, restore and restart. A fresh request key with one of those IDs gets backend_change_command_conflict409 at valid current CAS. Exact old receipt replay still wins. Continue unchanged local IDs and explicitly copy as new edits are separate choices; no automatic regeneration. See change4 for the complete protocol and project2 for annotation cursor/CAS recovery.

## Save complete requests before sending

Keep projectId/importId, source manifest/inventory, original base, every command
and UUID mapping. Persist entire mutation inputs and their original keys:

| Operation | Fields that an exact replay preserves |
|---|---|
| Begin | expectedVersion, baseRevisionId, mode/profile including omission; legacy profileExtension/repositoryId/graphScope or composed sourceScope/scopeStatus/syncPolicy/changeManifest when applicable, manifest, inventory, idempotencyKey and path IDs. |
| Batch | batchId, expectedImportVersion, payloadHash, exact commands and path IDs. |
| Commit | expectedVersion, expectedImportVersion, candidateHash, idempotencyKey and path IDs. |
| Abort | expectedImportVersion, idempotencyKey and path IDs. |

The server resolves existing receipts before compatibility/profile checks and CAS.
Keep original schema1 receipt shapes and absent members absent; new profile fields
are not retroactively inserted into executed legacy requests. A lost response therefore
replays with the same complete input and same key, even after versions/head have
advanced. Batch retries keep the same batchId/hash/commands/original version.
An old key with a changed payload conflicts. Never compensate for timeout by
minting a new key or constructing an old request with fresh versions.

Returned receipts contain original state, not current state. After recovering
the receipt, reread project/session to continue from current versions. Preserve
large integer versions exactly. Import IDs are returned session `id`; MCP inputs
use importId, while commit's returned sessionId is a receipt field.

## Resume after interruption or restart

`list_backend_imports {projectId,limit?,cursor?}` locates durable sessions.
`get_backend_import {projectId,importId,limit?,cursor?}` returns session,
acceptedBatches, saved preview and committedRevisionId. Paginate acceptedBatches
using returned cursor. Batch summaries identify accepted batchId/payloadHash/
acceptedVersion; replay original batch inputs to recover missing identity mappings.
Continue from the current session version, not a stale receipt version.

Staging, acknowledged UUIDs, mapping reservations and receipts survive restart.
Abort does not erase the old session's receipts or acknowledged mappings. Read
persisted state; do not create a replacement import merely because an old response
is unavailable. committedRevisionId identifies that import's original revision
even if project head has since advanced. Replay the saved commit request to
recover its exact project/revision/session receipt.

To stop a candidate explicitly send
`abort_backend_import {projectId,importId,expectedImportVersion,idempotencyKey}`.
Save input first and retry identically on lost response. Abort leaves the current
model/history intact. A known terminal conflict calls for reread and explanation,
not retrying arbitrary versions.

## Distinguish timeout from a known conflict

A timeout leaves the outcome unknown: exact replay first. A definite409 means
the specific request was refused: read its code/details and relevant current
state, reconcile user intent, then issue an appropriately new request/key.
Do not retry a changed payload under an old idempotencyKey/batchId.

- Session changed: read acceptedBatches/current version, recover receipts, and
  repair only unaccepted work with a new batchId. New batches invalidate preview.
- Metadata changed with the same head: reread project, preserve concurrent fields,
  reconcile intent and use current project CAS with a new commit key.
- Legacy source head changed: read both exact revisions and compare them; under the selected legacy protocol explicitly preview
  against the chosen current base using current session version. For sourced
  bases use the same-provider reconciliation procedure. Mapping expectedId/aliases
  and deletion proof are revalidated; preserve acknowledged UUIDs. Inspect the
  new diagnostics/hash before saving a new commit input/key.
- Initial session's empty base became sourced through a competing commit: initial
  preview cannot silently become reconciliation. If new-base preview refuses
  reimport, preserve original IDs/receipts, explicitly abort or abandon that old
  candidate, then begin a new compatible reconcile session with the current sole
  repository/provider and current base. Analyze overlap and stage only intended
  assertions; never switch modes or carry old snapshot evidence into the new
  session. This is the available reconciliation procedure, not proposal rebase.
- Candidate changed or preview invalidated: read status and source/identity/
  deletion details where saved pins still apply, repair as needed, then explicitly
  preview. Never update candidateHash/versions inside an unresolved old request.
- Invalid decision: use explicit repair/remove in a new batch and preview again.
  remove changes staging; it cannot delete a committed base assertion.

## Fixed revision selectors and comparison

Read immutable revisions explicitly with `get_backend_revision` and
`get_backend_coverage`. `query_backend_graph` takes projectId, revisionId and
recordType nodes/edges. Node filters are kind/search/parentId; edge filters are
kind/from/to. Pages default 100/max 500; cursor belongs to project/revision/filter.
Selector changes require a fresh first page.

For one fixed UUID use
`query_backend_graph {projectId,revisionId,recordType:"nodes"|"edges",id}` or
`get_backend_evidence {projectId,revisionId,evidenceId}`. Direct selectors cannot
be combined with cursor or other filters. Evidence can otherwise filter subjectId.
`get_backend_node {projectId,revisionId,nodeId}` returns properties/evidence IDs;
query relationships separately. Paths/snippets are escaped source data; arbitrary
SQL, filesystem URLs and traversal are not supported by this profile.

`compare_backend_revisions {projectId,fromRevisionId,toRevisionId,recordType?,changeKind?,limit?,cursor?}`
requires both exact committed IDs in the same project. recordType is node/edge/
evidence/source/identity. changeKind is added/removed/modified/identity_mapped/
freshness_changed. Cursors pin both revisions and filters; never replace either
side with a later head. comparisonHash stays stable across pages/filters.

Matching uses UUIDs. Explicit key mapping is an identity facet; evidence refresh
and freshness changes are separate from structural changes. Open each before/
after reference at its returned revision, including removed subjects' old evidence.
Source refs identify snapshot/path; unknown partial absence is not removed source.
Report source consistency, stale counts and gaps on both sides using their pinned
coverage. A structural diff proves neither runtime behavior, impact safety,
proposal conformance nor B0 completion.

## Relational read pins and precision

`query_backend_database` is a read-only B1.1 operation. It requires exact
projectId/revisionId/datastoreId/facetKey/recordType. Tables may use search;
relationships may use tableId to select either endpoint. Default page 100/max500;
explicit limit0, mixed selectors, foreign pins/cursors or unknown fields fail.
A cursor binds project, revision, semanticHash, datastore, facet, record type and
filters. Save them all. A read timeout may retry the same pin/cursor safely.
Changing any selector restarts from a fresh first page; never use an old result
or layout to overwrite a new selection. No read implicitly resolves a new head.
Schema1/no relational descriptor returns backend_relational_unavailable (422),
not empty complete ER; missing IDs in that pin return 404.

Database query pages retain target IDs for off-page tables. Open their node,
children, constraints, outgoing references and evidence at the same revision;
page graph/evidence results fully as needed. Selected-facet absence is a concrete
projection limitation, not object deletion. Table counts and relationship
cardinalities retain their source/unknown/stale basis. A canvas page limit is not
schema coverage. See the verified database-owned backend-database-reference.

Preserve exact int64 versions, ordinal/order and counts through REST/MCP and
request storage. Do not parse through floating point. Browser clients reject any
unsafe JavaScript integer recursively and show precision failure; do not round
and then initiate extension/commit or present a rounded column ordinal. Restart
recovery preserves schema1 history and schema2/profile decisions, with historical
proof still read through its exact revision/snapshot.

## Existing legacy relational proposal recovery

Proposal create/apply uses a separate retained exact request and idempotencyKey.
Before a new mutation after restart/update select a compatible database7 workflow,
view proposal-relational-v1, exact set/hash and proposal/edit capabilities. For an
uncertain acknowledgement retry the identical request/key three times with
1/2/4-second backoff, then preserve request, key, proposal and base/draft pins as
a recovery checkpoint. The original receipt wins before stale CAS, command
history or later quota/compatibility checks. Never replace its pins or key.

A known CAS/preview conflict reports current proposal version/draft/hash. Reread
and explicitly reconcile commands, then preview and apply a new request/key.
Do not patch only expectedVersion or use project CAS. A new source head leaves
the proposal baseline intact and reports baseOutdated. Historical graph cursors
remain pinned; history-list continuation after a save must restart. Wrong ownership
or designed-object source evidence gives404. Legacy relational proposals do not support ready/rebase. For authorized static diff/impact select change4 and its `backend-analysis-jobs` topic in this same set, using the exact `proposal:{proposalId,proposalRevisionId}` target. Runtime behavior remains unverified. These rules do not alter import8 batch/commit recovery.

## Durable analysis, rebase and ready recovery

Select change4's `backend-analysis-jobs` and `backend-change-rebase` in this same set for exact operation schemas. Keep action-tagged pending Start/Retry/Cancel/Rebase/Ready records at the project level, with original path owners, body bytes, draft/base/source pins, report hashes, choices/reasons/repairs and keys. Persist/read back before transport; storage unavailable/corrupt/conflicting data must stay visible. Reload never sends automatically.

Unknown outcomes replay unchanged. Definitive409 retains the refused attempt and requires owner reread, explicit reconciliation and fresh preview before a changed request. Accepted cleanup failure remains accepted and must not be mistaken for unknown transport. Verify rebase receipts against the selected new base and ready receipts against the exact report/draft. Old successful receipts replay after restart or later status changes. Interrupted jobs remain readable; a deliberate new-key Retry creates a distinct job from saved immutable input. An old-key Start/Retry is receipt recovery, not a new execution.
