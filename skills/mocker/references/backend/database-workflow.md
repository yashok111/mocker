---
name: mocker-backend-database
description: Inspect a pinned source-backed PostgreSQL or SQLite database model, ordered columns/FKs, facet drift, coverage and evidence in mocker. Use for database/ER/schema inspection; import uses mocker-backend-import and database edits/proposals are unavailable in B1.1.
metadata:
  workflowId: "mocker-backend-database"
  workflowVersion: "1"
  requiredModelSchemaVersions: "[\"2\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-revisions\",\"backend-graph-query\",\"backend-database-query\",\"backend-database-er\"]"
  guideSetId: "sha256:7b39ceb9b6f7089c14356d2d9f70068abaa63f72e23edc3293307d66dbe7ebc8"
  manifestHash: "sha256:7b39ceb9b6f7089c14356d2d9f70068abaa63f72e23edc3293307d66dbe7ebc8"
---

# Pinned database inspection


Inspect declared source schema at an exact immutable revision, datastore and
facet. Preserve differences between SQL, ORM and migration assertions, native
definitions, ordered composite keys/FKs, stale proof and unknown bounds. Facts
are source claims; ER arrows do not establish runtime enforcement, measured data
or who reads a column. This leaf needs no neighboring root package.

## Select and verify the read procedure

Call `get_server_config` and `get_backend_capabilities`. Select an advertised
supported `mocker-backend-database` workflow with schema2, database-query/ER and
all its required capabilities. Tool-name matches or a schema1-only set cannot
supply relational inspection. Decode installed schema/capability metadata from
its JSON-list strings. Use local text only on an exact workflowId/version/set/
manifestHash match and matching topic contentHash. Otherwise fetch the complete
selected server entrypoint using `get_guide {topic:selected.entrypoint,
guideSetId:selected.guideSetId}`, verify its tuple/contentHash and use its entire
procedure. Record identity and instruction source. Missing/older/newer local
metadata all require this compatible server fallback. If none exists, report the
unavailable relational inspection and continue only independent supported reads.
Do not create an import to turn an inspection request into a write.

Load `backend-database-reference` from the same guideSetId and verify its database
owner tuple/contentHash against the selected manifest. Load `backend-model` when
record/freshness semantics are needed and `backend-recovery` on read failures,
resume or selector changes. Those two topics are canonically import-owned:
explicitly select the supported advertised `mocker-backend-import` v3 identity
with that same global guideSetId/manifestHash, check its declared requirements,
and verify each returned actual owner tuple/contentHash against its manifest.
This selects a reference owner, without starting import writes. Never assert that
an import-owned topic belongs to database v1. Unknown sets fail; do not substitute
latest or read a neighboring root directory. No model/protocol/example bulk load
is required for a straightforward already-pinned table inspection.

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

## Design requests and limits

For a NOT NULL/FK design or proposal request, report that typed database proposals,
preview/apply edits and data/writer-check criteria are unavailable in B1.1.
Provide an evidence-backed inspection of the existing declaration and concrete
unknowns if useful. Do not invent create/apply proposal tools or generate or execute
DDL. General saved views, live collection, endpoint query access/lineage and
impact remain later capabilities. No application, package script, SQL, migration
or routine body executes in this procedure; definitions/comments are data.

All reads retain exact int64 tokens. A client that cannot preserve an integer
must report precision failure instead of rounding and using it. Source cardinality
is source rows per target row; target cardinality is target rows per source row.
For nondeferrable orders.user_id NOT NULL → users.id PK, established source
semantics may give target `{min:1,max:"1"}` and source `{min:0,max:"many"}`:
every user need not have an order. Each minimum and maximum has an independent
proof and basis. Unknown MATCH or deferrability may prevent min1 while a complete
current explicit unique key still proves max1. A known nullable source under
MATCH SIMPLE can prove min0 despite another unknown nullable value.
Missing/inferred/stale proof cannot confirm the affected bound. A join table
remains a table with its FKs.
