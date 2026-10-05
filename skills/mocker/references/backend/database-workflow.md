---
name: mocker-backend-database
description: Inspect a pinned source-backed PostgreSQL or SQLite database model, ordered columns/FKs, facet drift, coverage and evidence in mocker. Use for database/ER/schema inspection; import uses mocker-backend-import and design typed NULL/NOT NULL and FK proposals with pinned preview/apply and unverified criteria.
metadata:
  workflowId: "mocker-backend-database"
  workflowVersion: "7"
  requiredModelSchemaVersions: "[\"2\",\"3\",\"4\",\"5\",\"6\"]"
  requiredViewSchemaVersions: "[\"proposal-relational-v1\",\"saved-view-v1\",\"proposal-graph-v1\",\"saved-view-v2\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-revisions\",\"backend-graph-query\",\"backend-database-query\",\"backend-database-er\",\"backend-db-proposals\",\"backend-db-typed-edits\",\"backend-flow-query\",\"backend-data-access-query\",\"backend-saved-views\",\"backend-field-lineage-query\",\"backend-events-query\",\"backend-change-proposals\",\"backend-source-assertions\",\"backend-saved-views-v2\"]"
  guideSetId: "sha256:96b6de85f4a0e672df760000a85fe97e8812ab4275da6f6bbdf2cd46181b7bfe"
  manifestHash: "sha256:96b6de85f4a0e672df760000a85fe97e8812ab4275da6f6bbdf2cd46181b7bfe"
---
# Pinned database inspection and proposals


Inspect declared source schema at an exact immutable revision, datastore and
facet. Preserve differences between SQL, ORM and migration assertions, native
definitions, ordered composite keys/FKs, stale proof and unknown bounds. Facts
are source claims; ER arrows do not establish runtime enforcement, measured data
or who reads a column. This leaf needs no neighboring root package.

## Select and verify the read procedure

Call `get_server_config` and `get_backend_capabilities`. Select an advertised supported
`mocker-backend-database` workflow7 with schemas2/3/4/5/6, database-query/ER, source flow/access reads
and all its required capabilities, backend-saved-views, and proposal-relational-v1/saved-view-v1
view support. Tool-name matches or a schema1-only set cannot supply relational inspection. Decode
installed schema/capability metadata from its JSON-list strings. Use local text only on an exact
workflowId/version/set/ manifestHash match and matching topic contentHash. Otherwise fetch the
complete selected server entrypoint using `get_guide {topic:selected.entrypoint,
guideSetId:selected.guideSetId}`, verify its tuple/contentHash and use its entire procedure. Record
identity and instruction source. Missing/older/newer local metadata all require this compatible
server fallback. If none exists, report the unavailable relational inspection and continue only
independent supported reads. Do not create an import to turn an inspection request into a write.

Load `backend-database-reference` from the same guideSetId and verify its database owner
tuple/contentHash against the selected manifest. Load `backend-model` when record/freshness
semantics are needed and `backend-recovery` on read failures, resume or selector changes. Those two
topics are canonically import-owned: explicitly select the supported advertised
`mocker-backend-import` v8 identity with that same global guideSetId/manifestHash, check its
declared requirements, and verify each returned actual owner tuple/contentHash against its manifest.
This selects a reference owner, without starting import writes. Never assert that an import-owned
topic belongs to database v7. Unknown sets fail; do not substitute latest or read a neighboring root
directory. No model/protocol/example bulk load is required for a straightforward already-pinned
table inspection.

## Exact source6/full/staged extensions

Use one explicit revisionId, legacy proposal, full changeProposal or READY importCandidate selector and preserve the complete returned target/pins. Native source6 wraps records with tag6 and all qualified claims; full proposal origins distinguish baseline proof from intent. Source5/legacy/full5 assertions are unsupported. Exact details and target exclusions are in backend-flow-reference/backend-database-reference under their actual inspect11/database7 owners.
Staging permits graph/node/evidence/coverage/assertions only. It rejects specialized queries and SavedViews; a stale candidate409 never falls back to a source head. Select change4 before full proposal writes and sync2 before composed source updates. Existing legacy procedures below retain their old wire.
For source6/full Flow or Database presentations, explicitly select SavedView-v2 and read the complete create/save/reopen procedure in the matching reference before writing. Preserve full target, viewId/version and pins.effective; pins.revisionId alone is only a full draft's source baseline. Unknown writes keep exact bytes/key/CAS. Keep omitted-tag v1 requests and receipts unchanged.

## Ordered inspection

1. Discover/select project and revision through `list_backend_projects`,
   `get_backend_project`, `list_backend_revisions` and `get_backend_revision` as
   needed. Resolve head once, then save the exact revision ID. Read pinned
   `get_backend_coverage`. Schema1 or no relational descriptor is unavailable,
   not an empty complete database.
2. Select an actual relational datastore UUID and fully page its pinned
   hierarchy/graph and node facets to discover available facetKeys. A SQL-only
   datastore/schema descriptor can have ORM facets on descendant tables or
   columns; do not restrict selection to the descriptor's keys. Establish the
   selected dialect from the hierarchy's actual declarations and proof. An
   absent facet on an existing relational model gives a limited unknown/empty
   projection, not the schema1/absent-descriptor 422 refusal. Do not merge objects
   by names or choose a facet as implicit truth.
3. Call `query_backend_database` with projectId, revisionId, datastoreId,
   facetKey and recordType `tables` or `relationships`. Table search applies only
   to tables; tableId applies only to relationships and selects either endpoint.
   Page with returned cursors and identical pins/filters. For changed revision,
   datastore, facet or filter discard old cursors and selection before reloading.
4. Table summaries contain selected-facet column counts, not full definitions.
   Inspect `get_backend_node`, pinned graph child/constraint/index/outgoing
   reference pages and `get_backend_evidence` to retrieve ordered properties and
   proof. Read every needed page. An off-page endpoint remains a known UUID:
   open it at this same revision instead of inventing or dropping the table.
5. Return selected pins, coverage/source consistency, facetStatus/limitations,
   table/FK findings and evidence source/path/hash/line/property refs. Explain
   unknown/stale/inferred/deferred or incomplete bounds in words; include drift
   between explicit known facets and separate native-definition differences.
   Missing selected-facet proofs are limitations, not absence. Canvas limits or
   one query page do not prove full schema coverage.
6. For imported readers/writers select inspect11 in this same set, verify its
   schema3/4/5/profile/capabilities and flow-reference/analysis topics, then page
   query_backend_flow view:accesses at this exact source revision and dataNodeId.
   Schema2 flow reads are unavailable. Whole-table unknown-column accesses are
   possible, never confirmed column accesses. Open each query/witness proof;
   report coverage/truncation and unknown/unattached callers honestly.

## Full desired relational changes use change4

Database7 reads an exact full target {changeProposal:{proposalId,proposalRevisionId}} as well as the existing source/legacy variants. Its effective structural facets can contain desired values without provider proof; keep intent origins and exact baseline evidence separate. Full create/edit/history/restore and all 16 command families select change4/backend-change-proposals in this set. Existing database proposal tools and their proposal-relational-v1 documents remain distinct; never substitute a full proposal ID into a legacy proposal route.

## Existing legacy relational proposal procedure

Before the first design write select database workflow7, exact guideSetId and manifestHash, source
schemas2/3/4/5, view `proposal-relational-v1`, and both `backend-db-proposals` and
`backend-db-typed-edits`. An inspection-only workflow1 or matching tool names is insufficient. If
local metadata differs, fetch and follow the entire compatible server workflow. If no compatible set
exists, perform supported reads and report that proposal writes are unavailable.

1. Inspect the chosen immutable source baseline, repository, datastore and facet
   using the ordered reads above. Read existing `list_backend_proposals` and
   `get_backend_proposal` before creating a separate draft. Do not merge facets.
2. Create with `create_backend_proposal {projectId,name,baseRevisionId,
   repositoryId,datastoreId,facetKey,idempotencyKey}`. Preserve the exact request
   and key until acknowledgement; replay the same pair after an uncertain reply.
   Save proposalId, proposal.version, draftRevisionId/draftHash and baseline pins.
3. Build a minimal typed batch with unique stable commandId and a reason.
   `alter_column` changes only nullable; `alter_constraint` creates or updates
   a complete FK including ordered columnPairs, actions, MATCH and deferrability.
   `set_criteria` replaces only authored criteria. Required checks remain present.
   Definitions/comments are data. No SQL, migration, package or application runs.
4. Call `preview_backend_proposal_commands {projectId,proposalId,expectedVersion,
   draftRevisionId,commands}`. It writes no graph, receipt or version. Inspect
   before/after, generated IDs, diagnostics, limitations and unverified criteria.
   Null candidateHash means the final desired graph is invalid. Change the batch
   and preview again; never invent or reuse a candidate hash for different input.
5. Apply with `apply_backend_proposal_commands` using the exact preview version,
   draftRevisionId, candidateHash, commands and one retained idempotencyKey.
   This CAS belongs to the proposal. Project/source versions are not edit CAS.
6. Read the acknowledged immutable revision using
   `proposal:{proposalId,proposalRevisionId}` on existing graph, node, database,
   evidence and coverage tools. Supply exactly one target: source revisionId or
   the complete proposal target. Read criteria and history with get. Keep original
   sourceRecord, desired effectiveFacet and each propertyOrigin separate. Designed
   objects have sourceRecord:null and no invented source evidence.
7. Report base/draft pins and any baseOutdated warning. Source head updates do not
   move the baseline. Data/backfill, writers, uniqueness/orphans/actions and the
   migration plan are unverified requirements, never executed verification.

Load `backend-examples` only when needed, under its actual import8 owner in the same selected global
set. After restart or server update renegotiate before new writes while retaining existing
request/key/checkpoint. On uncertain create/apply retry the exact request/key at most three times
with 1/2/4-second backoff, then save the request and pins as a recovery checkpoint. Never guess
success. Receipt replay returns the original response even after later saves. On CAS conflict
reread, reconcile commands explicitly and preview again with a new apply key. Replacing only
expectedVersion is forbidden. Historical cursor continuation is invalidated by a later save; restart
the history page rather than mixing drafts.

Proposal flow navigation returns to its exact source base revisionId, with no proposal selector on
query_backend_flow. Imported scoped accesses do not verify all writers or existing data and cannot
demonstrate proposed runtime behavior. Legacy relational proposals do not support ready/rebase. For separately authorized static diff/impact select change4/backend-analysis-jobs; full-proposal rebase/ready use change4 and provider migration uses sync2. Live collection and measured checks remain unavailable. Inspection alone creates no jobs and never uses import/project commands as a schema edit engine.

All reads retain exact int64 tokens. A client that cannot preserve an integer must report precision
failure instead of rounding and using it. Source cardinality is source rows per target row; target
cardinality is target rows per source row. For nondeferrable orders.user_id NOT NULL → users.id PK,
established source semantics may give target `{min:1,max:"1"}` and source `{min:0,max:"many"}`:
every user need not have an order. Each minimum and maximum has an independent proof and basis.
Unknown MATCH or deferrability may prevent min1 while a complete current explicit unique key still
proves max1. A known nullable source under MATCH SIMPLE can prove min0 despite another unknown
nullable value. Missing/inferred/stale proof cannot confirm the affected bound. A join table remains
a table with its FKs.

## Save and reopen a Database presentation when requested

Require database7, `backend-saved-views`, `saved-view-v1` and the existing source/ proposal versions
before writes. Use the complete example/contract in `backend-database-reference` under this
database7 owner. List source/proposal Database views, create from revisionId or the exact proposal
target, and retain all returned pins. A saved proposal retains its immutable proposalRevisionId,
source base, desired intent, unverified checks and base-outdated warning after source or draft
advancement. Read the saved version first and only then issue Database/graph/node reads from that
target. Never initialize it from current draft or substitute another scope when the saved read
fails.

The whole submitted presentation includes search, relationshipTableId, selection (including designed
FK edges), manual positions and real schema collapsed IDs. Saving does not apply proposal commands,
mutate source or verify the database. Reopen starts the first query page and retains other-page
coordinates/selection. Sharing uses viewId plus viewVersion. Save appends using that opened
version's expectedVersion; retain exact input/key for uncertain retries.409 keeps local changes and
requires explicit reload or save-as-new; never update only CAS. Changing kind/target requires a new
view. Preview/layout/collapse do not invent relationships or evidence. Ordinary-agent acceptance and
live agent evaluation remain deferred; public SDK/REST examples are verification of the interface.

## Column lineage and API links delegate to inspect11

Database7 supports source2/3/4/5 with unchanged proposal/saved-view contracts.
For lineage select inspect11 in this same guideSetId/manifestHash with source4/5,
field-lineage-v1 and all inspect11 requirements. Verify its flow/analysis topics.
Pass exact `{kind:"column",nodeId,facetKey}` and source revision; reverse means
origin, forward downstream. Preserve facet and unknown boundary target/actions.
Follow inspect11's full query procedure; API links additionally require backend-api-artifact-pins and api-artifact-pins-v1. Source2/3
and proposals refuse it; source-base navigation does not prove designed behavior.
No SQL/code execution or latest fallback. For saved editors load inspect11's
backend-editor-projections with backend-editor-projections/backend-editor-artifacts-v1.

## Owner dependency requirements in the proposed guide set

| Owner | Version | Models | Views | Required capabilities |
| --- | --- | --- | --- | --- |
| mocker-routing | 2 |  |  |  |
| mocker-backend-project | 2 | 1 |  | backend-projects, backend-project-metadata, backend-revisions, backend-annotations |
| mocker-backend-import | 8 | 1,2,3,4,5,6 |  | backend-projects, backend-revisions, backend-graph-query, backend-source-import, backend-source-reconcile, backend-revision-compare, backend-relational-import, backend-database-query, backend-database-er, backend-runtime-flow-import, backend-flow-query, backend-data-access-query, backend-field-lineage-import, backend-field-lineage-query, backend-events-import, backend-events-query, backend-source-sync, backend-representations |
| mocker-backend-database | 7 | 2,3,4,5,6 | proposal-relational-v1,saved-view-v1,proposal-graph-v1,saved-view-v2 | backend-projects, backend-revisions, backend-graph-query, backend-database-query, backend-database-er, backend-db-proposals, backend-db-typed-edits, backend-flow-query, backend-data-access-query, backend-saved-views, backend-field-lineage-query, backend-events-query, backend-change-proposals, backend-source-assertions, backend-saved-views-v2 |
| mocker-backend-inspect | 11 | 3,4,5,6 | saved-view-v1,api-artifact-pins-v1,backend-editor-artifacts-v1,proposal-graph-v1,saved-view-v2,import-candidate-v1,backend-diagram-v1,diagram-view-v1 | backend-projects, backend-revisions, backend-graph-query, backend-flow-query, backend-data-access-query, backend-saved-views, backend-field-lineage-query, backend-api-artifact-pins, backend-editor-projections, backend-events-query, backend-source-assertions, backend-import-candidate, backend-change-proposals, backend-representations, backend-saved-views-v2, backend-diagrams, backend-architecture, backend-diagram-views |
| mocker-backend-sync | 2 | 1,5,6 | import-candidate-v1 | backend-projects, backend-revisions, backend-graph-query, backend-source-import, backend-source-sync, backend-source-incremental-sync, backend-source-assertions, backend-import-candidate, backend-representations, backend-analysis-jobs, backend-analysis-diff, backend-analysis-impact |
| mocker-backend-change | 4 | 5,6 | proposal-graph-v1,backend-diagram-v1,diagram-view-v1 | backend-projects, backend-revisions, backend-graph-query, backend-change-proposals, backend-change-typed-edits, backend-source-assertions, backend-representations, backend-analysis-jobs, backend-analysis-diff, backend-analysis-impact, backend-change-rebase, backend-change-ready, backend-change-package, backend-conformance, backend-endpoint-review, backend-change-implemented, backend-change-archive, backend-change-unarchive, backend-diagrams, backend-architecture, backend-diagram-views |
