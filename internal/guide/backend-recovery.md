# Import recovery and pinned reads


This topic is canonically import v6-owned. Database v6 readers explicitly select
its supported import owner in the same global set and verify the actual returned
owner tuple/contentHash. Use it from the selected set before commit
and whenever a response is lost, a session resumes, or CAS fails. Recheck selected
workflow identity after a server change without discarding original receipts.

## Save complete requests before sending

Keep projectId/importId, source manifest/inventory, original base, every command
and UUID mapping. Persist entire mutation inputs and their original keys:

| Operation | Fields that an exact replay preserves |
|---|---|
| Begin | expectedVersion, baseRevisionId, mode/profile including omission, profileExtension/repositoryId/graphScope when used, manifest, inventory, idempotencyKey and path IDs. |
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

## Proposal recovery

Proposal create/apply uses a separate retained exact request and idempotencyKey.
Before a new mutation after restart/update select a compatible database6 workflow,
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
or designed-object source evidence gives404. Unsupported ready/rebase/impact
remains a later boundary. These rules do not alter import6 batch/commit recovery.
