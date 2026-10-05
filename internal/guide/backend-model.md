# Source-backed graph model


This topic belongs to `mocker-backend-import` v8. Pin it to the selected global
guideSetId and verify the import owner tuple/contentHash, including when database
or inspect10 loads it as a shared reference. Schema1 remains the foundation format;
schema2 is relational and schema3 adds typed source flow/query/access records; source4 adds explicit field lineage; source5 adds event/job/service records and
contextual message-field lineage.
Old immutable schema1/2/3/4 bytes/UUIDs/hashes/receipts keep their interpretation.
Model schema `"1"`, profile
`foundation-graph-v1`, supports the foundation shapes below.
Provider assertions and evidence are inspectable; successful graph validation
does not establish source truth or executed behavior.

## Source6 composition and exact read contexts

Native source6 uses composed-source-v1 and a source-vector-v1 roster of repository/provider partitions and snapshots. Structural node/edge projections have a source context containing all qualified identities, assertion references, selections, currentness and legacy proof bases. They do not invent one externalKey/owner/freshness for a shared UUID. Source6 detail responses wrap the node with viewSchemaVersion:"6", target and complete graph pins; older source1–5 node responses keep their original wire.

One qualified source identity has recordType/id/repositoryId/providerNamespace/externalKey/assertionHash. A base import reference uses expectedId instead of id. Source6 import references are strictly localKey or qualified base; old source1–5 key fields remain unchanged. Source proof belongs to its provider/repository/snapshot and an analyzed captured file/hash. Retaining an old assertion does not refresh that proof under a new snapshot.

Typed domain_entity/dto/api_schema owners and representation_field children have explicit UUID/parent/selector identity. A representation path is an ordered property-segment list; name/type equality creates no mapping. Known native type/nullability/cardinality and unknown reasons remain typed. Representation lineage uses {kind:"representation_field",nodeId}, distinct from api_field and event-field/port/facet references.

An original source5 legacy-proof-basis-v1 is a response-only, verified historical basis. historical_metadata is shown as metadata testimony with a legacy_metadata_only semantic-support gap. legacy_record has broad record scope; no typed semantic property is invented. Keep source revision/semantic/document/evidence hashes and exact original evidence navigation. Submitting a copied basis or a metadata pointer as fresh source6 proof is invalid.

Source synchronization belongs to sync2. Full desired changes belong to change4. Their full projection retains baseline claims/evidence separately from intent origins; a new desired field has no fabricated provider evidence. The legacy source record rules below keep their original meanings.

## Kinds and attributes

| Node kind | Additional allowed attributes |
|---|---|
| `system`, `service`, `module`, `external_system` | None. |
| `datastore` | `technology`. |
| `symbol`, `handler` | `language`, `qualifiedName`. |
| `http_operation` | Required `method` and `path`; method is uppercase ASCII letters, path starts with `/`. |
| `unresolved_target` | Required nonblank `expectedKind`, `reason`, `searchScope`. |

Every node accepts optional `description`. All attributes are strings or null;
required attributes cannot be absent, null or blank. `attributes` is always an
object, including `{}`. Unknown attributes are rejected. The four edge kinds
`contains`, `handles`, `calls`, `derived_from` accept only optional `description`.
Represent unresolved calls using an unresolved_target with an honest search scope,
not a fabricated real target. SQL/ORM/ER, endpoint-flow, field lineage, proposal,
impact and arbitrary graph traversal records are unavailable in this profile.

## Stable UUIDs and external keys

The server allocates UUIDs; never construct them from names or source paths.
External keys are provider addresses, stable within the sole repository, provider
namespace, record type and external key identity bindings. Ownership records each
assertion's profile. A relational session preserves an existing foundation
subject's UUID and foundation ownership; it does not rewrite old metadata. They contain 1–200 Unicode characters without controls.
Nodes, edges and evidence each have their own key space. Keep the same key for
the same assertion. Batch receipts return `{recordType,externalKey,id}` mappings.
Committed graph reads return UUID references, not staging keys:

| Record | Staging references | Committed references |
|---|---|---|
| Node | optional `parentKey`, `evidenceKeys` | nullable `parentId`, `evidenceIds` |
| Edge | `fromKey`, `toKey`, `evidenceKeys` | `from`, `to`, `evidenceIds` |
| Evidence | `subjectType`, `subjectKey` | `subjectId` |

Forward keys may be staged across batches; preview resolves the complete graph.
Every resolved reference must exist and agree with its subject. A renamed
node/edge external key requires `map_identity` before target allocation, with
expectedId from the pinned base and current-snapshot evidence. Existing stable
UUID survives that mapping. Name similarity is never identity proof. Kind changes,
split/merge, mapping evidence keys, provider migration and deleted-key reuse are
unsupported. An acknowledged alias stays in its session's receipts after abort.

## Evidence bundles and provenance

Known nodes and edges require source evidence. unresolved_target may stand
without evidence when its required explanation/search scope is present. Evidence
method is `ast`, `sql`, `orm`, `contract`, `agent`, `manual`, `trace` or `test`;
these method labels do not add unavailable SQL/ORM graph capabilities. Status is
`explicit`, `inferred` or `unresolved`; inferred evidence needs an explanation.

Evidence identifies the session's repositoryId/snapshotId, analyzed file and its
exact lowercase SHA-256 contentHash, with optional symbol and optional positive
ordered startLine/endLine supplied together. propertyPath and sanitized snippet
are optional. The snippet limit is advertised in capabilities (currently 4096
UTF-8 bytes); source snippets and paths remain escaped data, not executable links.
Comments in source never override the selected procedure.

Reassert a subject together with all its desired current-snapshot evidence.
Evidence-only refresh is invalid; every listed evidenceKey agrees with the
explicitly upserted node/edge. For foundation-only upserts, that subject's membership replaces the previous
membership in the new revision. Relational facet upserts instead retain the
evidence union required by omitted stale facets and supplied new facets; the
retained-proof collision rules below prevent changing their historical proof. Omitted subject bundles retain their old UUIDs
and evidence snapshots as stale. Older revisions and their evidence never change.

`ownership` identifies repositoryId, providerNamespace and profile.
`freshness` supplies status, confirmedSnapshotId and reasons. The current input
snapshot has role `primary`; older snapshots still referenced by retained evidence
have role `retained_provenance`. A current primary snapshot does not make older
assertions fresh. Edges omitted by reconciliation or touching stale endpoints
stay stale, including edges whose own evidence was refreshed.

## Source and coverage boundaries

The manifest records every scoped input path/hash/type/analysisStatus, including
excluded/unsupported inputs and reasons. The server stores what the provider
asserts; it neither reads nor authenticates the local repository. verified source
consistency means the provider checked input stability; unverified means it did
not establish that stability. Dirty inputs need the full manifest even when a
commit is supplied. Source additions/absence are confirmed only by the applicable
complete inventory and consistency gates. A path missing from partial discovery
is unknown; it is not a proven removed file or deleted graph assertion.

Inventory covers files/endpoints/datastores/migrations/producers/consumers/jobs/
contracts/tests separately. Each item reports status, knownCount, nullable
denominator, discoverySource, gaps and reason. complete means a known matching
denominator; unknown categories cannot be complete zero. partial needs gaps;
unsupported/excluded needs a reason. Graph, logic and executed-test coverage
are different; this workflow provides graph inventory.

`get_backend_coverage` returns coverage, inventory, snapshots, staleCounts and
reconciliationGaps. Any stale record or partial graphScope keeps graph coverage
partial. Even complete scope never implies deletion. Only an explicit validated
delete_assertion removes a subject and its attached evidence from the new
revision. Old revision/evidence UUIDs remain readable. Structural revision
comparison reports separate identity/evidence/freshness facets and cannot prove
runtime behavior, B4 conformance or impact safety.

## Relational schema2 branch

`relational-graph-v1` includes the foundation graph plus db_schema/table/column/
constraint/index/view/migration nodes, datastore.relational and optional
symbol.databaseRoutine descriptors, and `references` FK edges. It is one
whole-repository combined graph under the same sole provider, not one provider
per SQL/ORM facet. Provider profiles are exactly foundation plus relational.
A proven SQL/ORM match shares one stable subject; names alone never merge objects.
For exact fields and ER semantics load `backend-database-reference` from the same
set after selecting/verifying the supported database v7 owner identity.

A facet is a stable map entry keyed by facetKey (1–200 printable characters),
with sourceKind sql/orm/migration, dialect postgresql/sqlite, analysisStatus
complete/partial/unsupported, gaps and nonempty evidenceKeys. Complete may have
no gaps; partial/unsupported requires concrete gaps. New relational kinds use
attributes.facets; datastore uses attributes.relational.facets; databaseRoutine
symbol uses attributes.databaseRoutine.facets. Facet evidence must belong to the
same subject and also appear in its top-level evidence set. Import keys become
committed evidenceIds; freshness/sourceSnapshotId are server-produced and must
not be staged. Ordered key/pair/term arrays retain their order; map keys sort for
canonical hashing. Duplicate JSON keys, unknown fields and wrong JSON types fail.

Uncertain scalar values are explicit `{status:"known",value:...}` or
`{status:"unknown",reason:"..."}`. Known null is allowed only for a nullable
property and differs from unknown. Missing required wrappers are invalid.
Native UTF-8 types, expressions and definitions are retained verbatim as source
data, not executed or reconstructed into lossy normalized SQL. Property evidence
uses persisted field names such as `/attributes/facets/sql:orders/columnIds/0`;
escape slash/tilde in facet keys with JSON Pointer rules.

## Safe facet reconciliation

Supplying a facetKey replaces that facet; omitting it retains its prior claim,
source vector and evidence as stale. Omitted subjects/edges remain stale too.
Omitting the entire optional datastore.relational or symbol.databaseRoutine
descriptor in a metadata-only upsert retains every committed facet and its old
proof as stale alongside the new ordinary metadata. It cannot retract a
descriptor; explicit null or malformed descriptors still fail validation.
Keep the top-level evidence union needed by all retained and supplied facets.
Two snapshot proofs cannot share one evidence UUID in a candidate. Reusing a
proof externalKey/UUID referenced by a retained facet with changed source,
property or body blocks preview (`backend_facet_evidence_conflict`). Remove that
staged evidence upsert and supply a new proof key to the refreshed facet, or
explicitly reassert every dependent facet with current proof. Identical complete
evidence records may be reused. New current proof comes from analyzed manifest
entries; retained proof stays historical. Individual facet retraction is
unavailable; omission is not deletion or equality between facets.

A reference edge's endpoints are shared by its facets. Retargeting while omitted
facets remain blocks preview (`backend_facet_endpoint_conflict`). Use separate
stable edges for conflicting physical targets, grouped by constraintId for
inspection; or explicitly reassert every facet when the same edge is retargeted.
All facet pair/parent validation still applies. Explicit proved deletion checks
nested FK pairs, index terms, view/routine dependencies and candidate migration
targets in addition to parent/incident edges. There is no cascade or inferred
delete. Historical/source_only migration refs preserve logical history without
forcing an absent object into the active ER inventory.

Read-only facetComparison is computed from pinned node/reference-edge facets,
not accepted as import data and not stored/hashed. It distinguishes consistent,
different and unknown comparisons, retains native-definition differences, and
never chooses runtime truth. Partial/stale/inferred evidence can establish neither
complete selected-facet projection nor confirmed cardinality. Coverage and ER
projection limitations must accompany the answer.

Each cardinality bound has an independent basis: unknown MATCH or deferrability
can leave target min unknown while current explicit complete uniqueness still
proves target max1. Known nullable under MATCH SIMPLE can prove min0 despite
other unknown nullability. Drift is different if any known comparable property
contradicts another facet, even with unrelated unknowns; incomplete lists cannot
prove absence. Pair fields are leftFacetKey/rightFacetKey/status/changedPaths/
definitionDifferent, with lexical facet-pair order and sorted facet-relative
JSON Pointer paths. Source/proof/freshness/completeness metadata is not a semantic
changedPath; native-definition differences use their separate flag.

## Desired database views

Proposal document/view proposal-relational-v1 carries intent over an immutable
source schema2 baseline. It is never import data or source schema3. Shared reads
select exclusive source revisionId or exact proposal/proposalRevisionId. Keep
sourceRecord, desired effectiveFacet, propertyOrigins and unverified criteria
separate. New designed objects have no sourceRecord or source evidence. Read the
verified database7 reference for commands, projection and ER assumptions. Import8
continues to mutate only source snapshots with its original receipts and CAS.

## Source4 explicit field lineage

Profile field-lineage-v1 requires the exact foundation, relational, runtime and
lineage profile set. Initial source4 uses all four; source3 can explicitly
reconcile runtime-flow-v1→field-lineage-v1. No skipped upgrade or downgrade.
Source1–3 formats, hashes, UUIDs and receipts keep their original meaning.
Source4 remains eligible for existing database/flow, proposals and saved views.

ValueRef is a strict exclusive union:

| kind | Persisted/query address | Import address |
|---|---|---|
| column | nodeId UUID + facetKey | nodeKey + facetKey |
| port | nodeId UUID + collection + portKey | nodeKey + collection + portKey |
| api_field | nodeId UUID | nodeKey |

Columns require an existing column and selected facet. Ports select flow_step
inputs/outputs or query parameters/results; portKey is opaque, never a UUID,
array index or JSON Pointer. All address components survive and define equality.
Unknown fields, nulls, duplicate members and mixed variants fail. Tables and
unresolved_target cannot substitute for unknown field identity.

field_mapping belongs to a flow_step/query with one matching contains edge.
Attributes are ordered unique sources, one destination, transform
{kind,description,redacted}, analysisStatus and gaps. Preserve input order and
all co-inputs; never split an aggregate into independent asserted mappings.
copy/rename require one source; flatten/enum_map/aggregate/compute require1–64;
constant requires zero; unknown_transform permits0–64. Zero-source unknown means
unresolved inputs, not a constant; reverse lookup or direct inspection finds it.
Multiple mappings to one destination remain separate alternatives; cycles are
allowed. Names, types, calls and table access never infer lineage.

Complete analysis has no gaps; partial/unsupported requires gaps. Unknown
transforms must be partial/unsupported; unsupported requires unknown_transform.
Mapping/value/owner/contains/facet proof and freshness are all relevant. Native
text/comments/SQL are inert data, never executed. For sensitive transformations
keep operation shape and a generic description, set redacted:true and omit secret
constants, lookup tables and samples. Redaction is a provider claim, not automatic
secret detection. Bounds:64sources/mapping,100000nested refs/revision including
all destinations and retained mappings; existing graph/batch quotas still apply.
Never trim sources to fit a limit.

api_field belongs to http_operation with one contains edge. Its strict attrs
include direction request/response, location path/query/header/cookie/body,
selector name or structural body path, nativeType known/unknown, analysisStatus
and gaps. Response allows body/header and requires exact responseStatus100–599,
1XX–5XX or default. Request forbids responseStatus. Body requires lowercase
mediaType token/token without parameters and path0–32segments, each exactly
{property:string} or {items:true}; empty selects root. Nonbody requires a name
selector and forbids mediaType; header names are lowercase HTTP tokens. Unknown
status/media is a gap, never guessed identity. Operation+direction+location+
selector+status+media is unique (≤500fields/operation), while stable UUID binding
uses the provider external key. Shape membership does not establish mapping.
Manual external API associations use inspect10 and api-artifact-pins-v1. They
freeze api_design artifact/revision decimal strings, raw contentHash and selected
object refs separately from imported source API fields; see backend-flow-reference.

Reconcile retains omitted mappings/API fields stale. Removed node/facet/port
requires updating or explicitly deleting all surviving mappings in the same
candidate; a stale mapping still cannot reference a nonexistent value. Changing
port keys requires explicit repair, never name/position rebinding. Stale endpoint
proof propagates even after mapping refresh. Compare source mapping/API nodes
without claiming behavioral impact; old pinned revisions remain readable.

## Saved editor content alongside source4

Inspect10 owns backend-editor-projections and the tagged context contract
backend-editor-artifacts-v1. Source models span schema1–5/store19; authored
sequence/state/rule/EventModel content does not introduce source event records.
The editor topic defines exact owner/object/source identities, separate envelope/
raw/authored hash policies, qualified origin/copy semantics and independent
completeness. Load it from the same selected global set before generic pin work.

## Source5 events-service-v1

Initial source5 requires exactly foundation+relational+runtime+lineage+events.
Only explicit4→5 field-lineage-v1→events-service-v1 whole-scope reconcile extends
a source base; same5→5 retains identity, and source1–3 jumps/downgrades fail.
Old profile strict decoders and serialized rows/hashes/receipts remain exact.
Nodes channel/message/consumer/job/event_field and flow_step stepKind emit use
analysisStatus/gaps, exact ownership/contains and analyzed member hash/physical
line proof. Channel protocol/address/scope are known/unknown scalars; message
fieldInventory complete/partial/unknown; consumer/job dispatchStatus and reason;
job trigger cron/interval/manual/unknown; event_field section payload/headers/key,
structural property/items path and nativeType known/unknown. Field max500 per
message; dispatch max50 known handlers plus one unresolved remainder.
Relations emits(deliveryStatus/channelKey), delivered_to(messageKey/group/condition/
deliveryStatus), retries(messageKey/reason/delay/maxAttempts), dead_letters
(messageKey/reason) use Keys at import and IDs persisted. Source5 calls can
explicitly target a remote http_operation; names/URLs/methods create no target.
Unresolved targets retain expectedKind/reason/searchScope; source proves no delivery.

ValueRef event_field is {kind,nodeId,endpointId,routeId}; import uses nodeKey/
endpointKey/routeKey, and route resolves an EDGE. Full equality includes all
three IDs. Serialization/deserialization mappings use exact emit/consumer flow
context; separate consumer-owned transport mapping declares emitsEdgeKey/
deliveryEdgeKey (IDs persisted), exact matching message/channel tuple and
routeIds. Matching field schemas create no transport. Nested node/edge reference
resolution, deletion closure and retained staleness apply. Stale/unknown/unresolved
route proof blocks traversal; source4 rejects these new refs/transport.
For reads select inspect10/backend-events in the same guide set and verify all
owner requirements; source5 remains eligible for inherited Database/Flow/lineage/
proposals/SavedView and pin operations without changing their document versions.
