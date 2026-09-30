# Import recovery and pinned reads

Use this topic from the selected `mocker-backend-import` guide set before commit
and whenever a response is lost, a session resumes, or CAS fails. Recheck selected
workflow identity after a server change without discarding original receipts.

## Save complete requests before sending

Keep projectId/importId, source manifest/inventory, original base, every command
and UUID mapping. Persist entire mutation inputs and their original keys:

| Operation | Fields that an exact replay preserves |
|---|---|
| Begin | expectedVersion, baseRevisionId, mode, repositoryId/graphScope when used, manifest, inventory, idempotencyKey. |
| Batch | batchId, expectedImportVersion, payloadHash, exact commands and path IDs. |
| Commit | expectedVersion, expectedImportVersion, candidateHash, idempotencyKey and path IDs. |
| Abort | expectedImportVersion, idempotencyKey and path IDs. |

The server resolves existing receipts before CAS. A lost response therefore
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
- Head changed: read both exact revisions and compare them; explicitly preview
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
