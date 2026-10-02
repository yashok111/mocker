# Relational facets and pinned database reads


Load as `backend-database-reference` from the selected global guideSetId. Its
canonical owner is `mocker-backend-database` v5. Import v5 explicitly selects
that supported owner in the same set and verifies the actual returned owner
workflow/version/set/manifestHash/contentHash against the owner's manifest.
Do not expect this topic to return import identity. For shared UUID/provenance/
recovery semantics, model/recovery topics keep import v5 ownership.

## Declared source model

Schema2/relational-graph-v1 stores a combined foundation/relational graph for one
repository and provider. SQL, ORM and migration facets can share a stable subject
only when the collector supplies a supported identity match. Names do not merge
subjects, and facets are not independently registered providers. PostgreSQL and
SQLite are distinct explicit dialects; SQLite `main` is an explicit schema.
No live environment/time is claimed. Native types, definitions and expressions
are retained verbatim as UTF-8 data; no parser, DDL, migration or stored body runs.

The exact containment hierarchy is datastore → db_schema/migration,
db_schema → table/view/databaseRoutine symbol, table → column/constraint/index/
trigger symbol, view → column. Foundation containers may contain datastore and
foundation nodes but may not bypass schema/table/view parents. parentKey and
sole incoming contains must agree and be acyclic. Native DDL text creates no
implicit containment or relationship.

## Facet fields

Every facet has sourceKind sql/orm/migration, dialect postgresql/sqlite,
analysisStatus complete/partial/unsupported, gaps (required nonempty for incomplete
analysis) and nonempty evidenceKeys. Facet keys are stable printable map keys,
1–200 characters. New kinds use attributes.facets; datastore uses
attributes.relational.facets, and symbol uses attributes.databaseRoutine.facets.
Committed keys resolve to evidenceIds and the server adds freshness and
sourceSnapshotId; do not supply those computed fields in an import.

Required scalar uncertainty is represented by exactly one known value or unknown
reason. Known null means an explicitly absent nullable value; unknown means it
was not established. For example:

```json
{"status":"known","value":null}
{"status":"unknown","reason":"Default is generated dynamically"}
```

These are separate illustrative fixture-only JSON values. A known-null default
means no DEFAULT; it is not an unknown default. Omission is not a third state.

| Facet kind/descriptor | Required properties beyond common metadata |
|---|---|
| datastore.relational | databaseName, qualifiedName; nullable nativeDefinition. Existing technology/description remain allowed. |
| db_schema | qualifiedName; nullable nativeDefinition. |
| table | qualifiedName; nullable nativeDefinition; constraintsStatus and columnsStatus complete/partial/unsupported with gaps for incomplete lists. |
| column | Wrapped nativeType string, typeFamily enum, nullable boolean, nullable-string defaultExpression/generatedExpression/identity, positive-int64 ordinal. |
| constraint | constraintKind primary_key/unique/check/foreign_key; ordered columnKeys (empty only for CHECK); wrapped nullable expression, deferrable and initiallyDeferred; nullable nativeDefinition. CHECK requires an expression or explicit unknown with definition/gap. |
| index | Ordered terms, each exactly columnKey or expression plus direction asc/desc/unknown and nulls first/last/unknown; wrapped unique boolean, nullable predicate/method; nullable nativeDefinition. |
| view | qualifiedName; materialized boolean; definition; dependencyKeys; dependenciesStatus complete/partial/unsupported and gaps when incomplete. |
| migration | Wrapped nullable nonnegative-int64 order; parentKeys; definition; changes; derivationStatus complete/partial/unsupported. |
| symbol.databaseRoutine | routineKind procedure/function/trigger; qualifiedName; definition; dependencyKeys; bodyStatus complete/partial/unsupported and gaps when incomplete. Existing language/qualifiedName/description remain allowed. |

Only column fields, constraint expression/deferrability, index unique/predicate/
method and migration order use the scalar wrapper. Other listed values are direct.
The typeFamily enum is boolean/integer/decimal/float/string/binary/date/time/
timestamp/json/uuid/array/other. Native type remains independent of an unknown
family. No PK/FK is required for a valid table; incomplete lists never prove
absence. A raw view/routine definition with unknown dependencies remains valid.

Keys resolve within the candidate to committed UUID names: columnKeys→columnIds,
dependencyKeys→dependencyIds, parentKeys→parentIds, index term.columnKey→columnId
and migration candidate target.objectKey→objectId. Forward refs are allowed;
wrong kind/parent, missing and foreign refs block preview. Every facet proof
belongs to its subject and appears in top-level evidenceIds. Property evidence
uses committed field names; JSON Pointer escapes slash/tilde in facet keys.
Ordered arrays retain their order, facet maps sort for canonical JSON, unknown
members/wrong types/duplicate JSON keys fail. Names unique within a schema/facet
are validated rather than automatically merged. Dialect is consistent within
one datastore/facetKey.

## Foreign keys and distinct physical targets

`references` goes from a foreign_key constraint to a table or explained
unresolved_target. Its attributes.facets use common metadata and ordered
columnPairs [{fromColumnKey,toColumnKey}], wrapped updateAction/deleteAction
no_action/restrict/cascade/set_null/set_default or unknown, and wrapped matchType
simple/full/partial or unknown. Committed pairs use fromColumnId/toColumnId.
Pairs are nonempty for a physical table target, unique on each side and at most
64; source columns belong to the constraint's parent table, target columns to
the destination table. The selected constraint's ordered column list equals
that selected edge facet's source pair list. Self-FKs and cycles are valid.

Physical FKs require explicit source evidence. Naming suggestions remain inferred
and cannot establish confirmed cardinality. Unknown table/target columns use
unresolved_target expectedKind table, empty pairs and nonblank targetReason,
with unknown coverage/bounds. No same-name table is invented to close a gap.

An edge has one shared from/to pair across its facets. Retargeting a SQL facet
while omitted ORM proof remains blocks preview with backend_facet_endpoint_conflict.
Keep differing physical targets on distinct stable edges from the same constraint,
for example fk:orders/customer:sql and fk:orders/customer:orm. The inspector groups
by constraintId and reports the target disagreement. Reassert every edge facet
with current proof to retarget a single stable edge explicitly; or add a new edge
and retain stale/prove deletion of the old one. Pair/parent checks still apply.

## Logical and historical migration targets

Each change has target, operation create/alter/drop/unknown and description.
Target is exactly one of these fixture-only illustrative forms:

```json
{"kind":"candidate","objectKey":"column:orders/state"}
{"kind":"historical","revisionId":"<same-project-ancestor-uuid>","objectId":"<old-column-uuid>"}
{"kind":"source_only","externalKey":"column:orders/old_status","expectedKind":"column","qualifiedName":"orders.old_status","reason":"Created and dropped before the first import"}
```

Candidate refs resolve to surviving active graph objects. Historical pins resolve
to immutable base/ancestor revisions in the same project/repository; they can
record any operation, including an old CREATE after a later DROP, without keeping
the object active. Before deleting an object, reassert any retained migration's
candidate target as historical where supported; otherwise that active ref still
blocks deletion. Foreign/unavailable history fails validation.

source_only retains a logical source declaration absent from candidate and
available model history, including first-import create→drop. It needs a relational
expectedKind, nonblank qualifiedName/reason and migration facet proof. It allocates
no UUID, appears as source-only history and asserts no stable identity match to
an ER object. Known ordered handled changes and supporting migration proof are
needed for complete provider-derived facets; null/unknown order, unsupported
changes or incomplete dependencies retain gaps. The server never replays DDL.

## Stale retention, drift and proof

An upsert replaces supplied facetKeys and retains omitted facets as stale with
their original source vector and proof. Retain the needed top-level evidence
union. Reusing an evidence UUID needed by retained facets with changed
source/property/body blocks preview with backend_facet_evidence_conflict. Repair
with a new proof externalKey or reassert every dependent facet; unchanged complete
proof may be shared. Two snapshot proofs cannot occupy one UUID. Omitted sources,
objects or facets are unknown/stale, never inferred deletes or facet retraction.
Whole-subject deletion additionally checks nested FK/index/view/routine/migration
refs, and old history/proof remains immutable and readable.
Omitting an entire optional relational/databaseRoutine descriptor in an
ordinary-metadata upsert also retains all committed facets and their old proof
as stale. New metadata and its current proof can coexist with that old proof;
the server restores required retained evidence IDs. Verify the final union and
never restage the old proof with the new snapshot. Explicit null or malformed
descriptors are invalid, not a descriptor-removal command.

Drift compares explicit known properties on the same stable subject at one pin.
Read-only facetComparison has status consistent/different/unknown and pairs with
leftFacetKey, rightFacetKey, status, changedPaths and definitionDifferent. Facet
keys/pairs have lexical order; changedPaths are sorted facet-relative JSON
Pointers. Node/reference comparisons include ordered keys/pairs/actions/MATCH.
A known comparable contradiction makes the comparison different even with
unrelated unknowns. Without one, uncertain/incomplete facts can yield unknown;
incomplete lists cannot prove absence. Source/proof/freshness/completeness
metadata is not a semantic changedPath. Raw native text differences use the
separate flag and assert no SQL equivalence. Distinct target edges are visible
in pinned constraint outgoing reference reads. The comparison is computed for
reads, refused as import data, unstored and excluded from semanticHash.

## Pinned query and evidence procedure

The MCP operation `query_backend_database` and REST
`POST /api/backend-projects/{id}/database/query` are read-only. The operationId
is queryBackendDatabase. Required MCP input is projectId plus exactly one target (revisionId or
proposal:{proposalId,proposalRevisionId}), datastoreId/
facetKey/recordType; recordType is tables or relationships. search is optional
only for tables; tableId is optional only for relationships and selects either
FK endpoint. Omit optional limit for default100; max500. limit0, wrong selector
combinations, foreign pins/cursors, repeated query values and unknown members
fail. Schema1/no relational descriptor gives backend_relational_unavailable (422),
not an empty complete projection; missing IDs at the pin give 404.
Required selectors are nonempty strings; null/wrong types and unknown fields
fail. Optional search/tableId presence matters: tableId is forbidden on tables
and search on relationships even when empty. Omit tableId unless using a valid
UUID. Explicit limit must be an integer token 1–500, never null, zero, fractional
or exponent notation. An existing wrong-kind/outside-datastore tableId returns
backend_invalid (400, details.field tableId); an absent pinned ID returns404.

Response pins projectId/revisionId/semanticHash/datastoreId/facetKey/recordType
and includes coverage, facetStatus current/stale/unknown, limitations, tableItems,
relationshipItems and nextCursor. Both item arrays are always present; only the selected one is populated.
nextCursor is always a string.
Each cursor pins project/revision/hash/datastore/facet/type/search/tableId. Changing
selector or filter starts a fresh page. Head changes cannot alter these pages.
Counts/ordinal/order/version values are int64; never round them. A browser must
refuse any unsafe JS integer instead of silently displaying or using it.

Table summary has tableId/schemaId/qualifiedName/columnCount/facetKeys/driftStatus.
Tables appear only when they carry the selected facetKey, and columnCount counts
child columns with that selected facet (stale included explicitly). A column
known only in another facet does not become selected-facet presence. Load full
columns/constraints/indexes with pinned node/graph pages and proof with evidence
reads; table summaries intentionally omit full large arrays. TableItem has no
freshness field: describe stale proof using actual node/facet reads and page
facetStatus/limitations.

Relationship summary has edgeId/constraintId/sourceTableId, nullable targetTableId,
ordered columnPairs/evidenceIds, sourceCardinality/targetCardinality, status
explicit/inferred/unresolved/stale and nullable targetReason. It requires selected
edge and constraint facets. Missing selected table/column/target proof is a
projection limitation and contributes unknown facetStatus/bounds; retain known
graph target UUIDs even if that facet is unavailable. Off-page tables remain
navigable through the same revision; a page or canvas is not coverage. Provider
columnsStatus/constraintsStatus cannot override missing referenced proof.

Read pinned coverage and all pages needed for the question. Open evidence at its
exact revision to report repository/snapshot/file/hash/line/propertyPath and
sanitized definition snippets. Fresh source-only does not mean runtime truth.

## Cardinality means declared participation

sourceCardinality is source rows per target row; targetCardinality is target rows
per source row. Bounds have min 0/1/null, max "1"/"many"/null and textual basis.
Each min/max is proved independently using current explicit selected-facet proof.
Target max is one when ordered target columns match a complete explicit PK/UNIQUE
or an unconditional unique column-only index. Expression/conditional indices
cannot prove that global key; an unknown predicate leaves it uncertain. Unknown
MATCH/enforcement/deferrability may prevent target min1 without erasing a proved
max1. Target min1 needs known NOT NULL source columns and the established declared
enforcement/MATCH basis. Under MATCH SIMPLE one known nullable source can prove
min0 even if another nullability is unknown. Source max is one when the source
columns are globally unique, otherwise many when complete explicit selected
constraints establish no matching global key. Declared source min stays zero.
Missing/stale/inferred/incomplete proof leaves the affected bound unknown with a
concrete basis reason. None of these bounds checks live data or enforcement.

A nondeferrable orders.user_id NOT NULL→users.id PK declaration may yield
targetCardinality {min:1,max:"1"} and sourceCardinality {min:0,max:"many"} when
source constraints are complete and nonunique. It does not require every user to
have an order. A composite FK preserves pair order; uncertainty in its basis
stays visible. A join table remains itself plus its two FKs, not a synthetic
physical N:M edge. ER arrows do not identify readers, joins, transactions or
parameters; those are B2 work.

## Limits and later actions

Use advertised limits: 16 facets/subject, 64 ordered constraint/FK columns,
64 index terms, 64KiB UTF-8 per native type/definition/expression and
500 dependency/change refs per view/routine/migration, within existing batch/
staging/revision limits and smaller global maxBodyBytes. Limits reject atomically;
never truncate. They are validation bounds, not measured performance claims.
Canvas visibility is at most 200 tables/600 FKs with explicit scope counts; the
paginated list, relationship table and inspector preserve full read access.

Source schema3/4 supports imported scoped endpoint accesses through inspect5 and
query_backend_flow. Select that owner in this same set, then page accesses at
the exact source revision with dataNodeId; table-level unknown-column access is
possible, never a confirmed column reader/writer. Open query/edge/witness proof.
Source2 flow inspection is unavailable. DB proposals keep their exact source base
for flow navigation; no proposal selector is accepted by the flow query.
Imported accesses do not verify all writers, data/backfill or proposed behavior.
Live collection, measured data/writer checks, ready/rebase,
impact and provider migration remain unavailable. Saved views persist presentation and exact pins only.

## Proposal documents and property provenance

`proposal-relational-v1` is a desired view/document, separate from source schemas2/3/4.
Create pins project/repository/base revision and hash/datastore/facet. A proposal
has its own positive int64 version and immutable draft history. Only draft status
is supported. Imported graph rows, provider identities, source receipts and the
project source pointer remain intact. There is no live database mutation.

Read responses add viewSchemaVersion/proposalPins/proposalProjection. Ordinary
nodes/edges remain source records. A projected node/edge pairs sourceRecord with
an effectiveFacet; new designed constraint/reference IDs have sourceRecord:null.
Values retain typed known/unknown semantics. Each propertyOrigin is source with
baseline/property/evidence pins, or intent with commandId/reason and no invented
source evidence. Inherited uniqueness remains source basis; changing nullability
or FK intent does not refresh source facts or stale evidence.

All proposal ER relationships use status:proposed and runtimeStatus:unverified.
Bounds describe desired participation under the stated baseline assumptions.
They do not prove that runtime enforcement or existing rows match the proposal.
Partial, stale, inferred, expression or conditional uniqueness remains limited.
Off-page targets retain UUIDs and can be read at the exact proposal revision.
Evidence/coverage resolves the pinned source baseline with explicit proposal pins;
a new designed subject evidence read returns404. Explain its intent provenance.

Strict commands (every command requires commandId and nonblank reason):

- `{type:"alter_column",columnId,nullable:<boolean>}`; no other column property.
- `{type:"alter_constraint",action:"create",name,tableId,targetTableId,
  columnPairs:[{fromColumnId,toColumnId}],updateAction,deleteAction,matchType,
  deferrable,initiallyDeferred}`; update replaces name/tableId with constraintId.
  Source and target tables/columns must belong to the pinned selection. Pair order
  is meaningful. Restrict dialect features to actual declared PostgreSQL/SQLite
  support; preview validates the final batch, including SET NULL/nullability.
- `{type:"set_criteria",criteria:[{key,kind,targetIds,description}]}` replaces
  authored criteria only. Allowed authored kinds are existing_data, writers,
  referential_integrity, target_uniqueness and migration_plan. Required criteria
  cannot be removed; all statuses stay unverified.

Preview returns proposal/base/draft pins, expectedVersion, candidateHash and
candidateGraphHash (nullable for invalid desired state), changes, criteria,
diagnostics and limitations. Apply adds the candidateHash and idempotencyKey to
that exact input and returns proposal/revision/changes/criteria/candidateGraphHash.
On get, revision is the selected immutable revision; proposal contains current
CAS pins. History is paginated and get may include lastApplyReceipt/baseOutdated.

Limits add 100 commands/1MiB per proposal batch, 100 authored criteria, and
4096 UTF-8 bytes per reason/description. Existing 64-pair/reference, graph,
semantic payload and shared 512MiB project transient limits still apply. No
truncation, extra open-proposal cap or performance claim is implied.

## Pinned saved Database views and executable SDK example

Require database5, `backend-saved-views` and `saved-view-v1`, preserving source
schemas2/3 and proposal-relational-v1 requirements. List/get/create/save use the
four `*_backend_saved_view[s]` tools; no delete or rebind exists. GET resolves an
exact immutable saved version before reading its source/proposal model. Share
both viewId and viewVersion. A missing/foreign/network-failed pin is an error,
never a reason to use current source/draft. Source/proposal heads and project
versions are unaffected. Returned server-derived pins retain the exact source
hash and all proposal baseline/effective hashes.

Fixture-bound source example (UUIDs come from the imported PostgreSQL or SQLite
orders fixture, never names):

```json
{
  "projectId": "<project-uuid>", "name": "Orders ER",
  "target": {"revisionId": "<source-revision-uuid>"},
  "state": {
    "kind": "database",
    "scope": {"datastoreId": "<datastore-uuid>", "facetKey": "sql"},
    "filters": {"search": "orders", "relationshipTableId": "<orders-table-uuid>"},
    "selection": {"recordType": "node", "id": "<orders-table-uuid>"},
    "positions": [{"nodeId": "<orders-table-uuid>", "x": 320, "y": -20}],
    "collapsedGroupIds": ["<schema-uuid>"]
  },
  "idempotencyKey": "database-view-create-1"
}
```

1. `list_backend_saved_views {projectId,kind:"database",limit:50}`, then
   `create_backend_saved_view` with this exact complete object. Retain version1.
2. Change the selected presentation and call `save_backend_saved_view` with
   `{projectId,viewId,name:"Orders ER arranged",state:<complete updated state>,
   expectedVersion:1,idempotencyKey:"database-view-save-2"}`. No target on save.
3. Simulate a lost response by discarding its acknowledgement; retry the exact
   original request/key. It returns byte-for-byte version2, even after later saves.
4. `get_backend_saved_view {projectId,viewId,version:1}` returns original filters,
   selection/coordinates/collapse and target. Use target with
   query_backend_database and reset cursors. Reimport cannot move this source pin.
5. Save from version1 with a new key after version2 exists:409
   backend_version_conflict/currentVersion2. Preserve edits; explicitly reload and
   reconcile or create a new view. Never silently overwrite or replace only CAS.

For a proposal use target `{proposal:{proposalId,proposalRevisionId}}`, omit
revisionId, and select designed FK with `{recordType:"edge",id:<designed-edge>}`.
Its effective IDs are valid and have no invented source evidence. The saved
scope must remain creation-bound; source DB scopes can change when validated.
Old versions remain pinned after source/draft advancement. Intent, checks and
base-outdated warnings continue to describe the exact old proposal.

Positions name selected-facet tables, including off-page tables; collapsed IDs
name real db_schema memberships. Collapse hides canvas cards and incident FKs,
keeps lists/inspectors/coordinates, and creates no synthetic relations.200cards/
600edges and query pagination are separate limits. Manual graph coordinates and
local ELK preview/apply/cancel/one-step undo carry no semantic changes; Save is
disabled during preview. Name/search limits200characters; finite x/y within
±1000000, unique nonnull arrays at most200, request128KiB. Never save unfinished
search drafts, cursors, graph bodies, source text or credentials. Ordinary-agent
acceptance and live agent evaluation remain deferred.

## Source4 column lineage belongs to inspect5

Database5 reads source2/3/4 and existing proposal-relational-v1/saved-view-v1.
For source4 lineage select inspect5 in the same guideSetId/manifestHash, verify
its schema3/4 and field-lineage-v1 support, all required capabilities including
backend-field-lineage-query, and actual flow-reference/analysis topic hashes.
Use exact {kind:"column",nodeId,facetKey}, revisionId and reverse for origins or
forward for downstream. Preserve facet identity, ordered co-inputs, unknown
boundary destination/actions, coverage and truncation. Do not issue lineage for
source2/3 or proposals; an exact source-base navigation does not turn designed
intent into source lineage. No SQL/application/migration execution or latest-pin
fallback. Query procedure and evidence interpretation belong to inspect5.

## Exact API links from a database value

Select the complete inspect5 owner in this same guide set, including
backend-api-artifact-pins and api-artifact-pins-v1 in viewSchemaVersions. Follow
backend-flow-reference for a manually chosen API-field association reached through
column lineage; column names/types create no automatic correspondence. Query a
proposal's exact baseRevisionId and a SavedView's exact source target. Orphaned
source UUIDs remain queryable/removable with their frozen labels. New head or API
draft observations never replace these pins. Database5 and its proposal contract
remain unchanged; source1–3 keep their supported reads.
