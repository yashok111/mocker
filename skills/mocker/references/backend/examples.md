# Import and pinned database examples

This topic is import5-owned. Verify its actual import workflow identity/contentHash
in the selected global guide set. Database5 inspection may load it through that
owner without starting import writes. The foundation procedure below remains
schema1; the relational captures afterward use schema2. SQL/ORM/migration input
is source data and is never executed.

These excerpts use the same two-version Go foundation fixture as the B0.3
protocol checks. They run through the unchanged MCP contract. `tool(name,input)`
means a call over your configured connection, after compatible guide selection
and reading backend-model/import-protocol/recovery from that selected set.
Persist each named input before sending; variables below show what to retain.
Actual UUIDs, versions and hashes always come from responses, never these names.
Fixture strings are source data; no source application, SQL or package script runs.

## Shared fixture and helpers

The first snapshot has main.go and independent.go. The second renames ListOrders,
removes its call and removes independent.go. Hash the exact source bytes.

```javascript
const { createHash } = require('node:crypto');
const sha = text => createHash('sha256').update(text).digest('hex');
const sorted = v => Array.isArray(v) ? v.map(sorted) : v && typeof v === 'object'
  ? Object.fromEntries(Object.keys(v).sort().map(k => [k, sorted(v[k])])) : v;
const provider = { name:'local-agent', version:'1', namespace:'qa-foundation',
  method:'ast', profiles:['foundation-graph-v1'], limitations:['Fixture, not runtime proof'] };
const oldText = 'func ListOrders() { independentCall() }';
const newText = 'func ListOrdersV2() { return }';
const otherText = 'func independentCall() {}';
const oldHash = sha(oldText), newHash = sha(newText), otherHash = sha(otherText);
const file = (path, contentHash) => ({path, contentHash, fileType:'go', analysisStatus:'analyzed'});
function inventory(count, partial=false) {
  return ['files','endpoints','datastores','migrations','producers','consumers','jobs','contracts','tests']
    .map(category => ({category, status:['files','endpoints','datastores'].includes(category)
      ? (partial && category !== 'datastores' ? 'partial' : 'complete') : 'unsupported',
      knownCount:category === 'files' ? count : category === 'endpoints' && !partial ? 1 : 0,
      denominator:category === 'files' && !partial ? count : category === 'endpoints' && !partial
        ? 1 : category === 'datastores' ? 0 : null, discoverySource:'foundation-fixture',
      gaps:partial && ['files','endpoints'].includes(category) ? ['main.go unavailable'] : [],
      reason:['files','endpoints','datastores'].includes(category) ? '' : 'Not analyzed by fixture'}));
}
function beginInput(key, files, mode='initial', repositoryId, partial=false) {
  return {projectId:project.id, expectedVersion:project.version, baseRevisionId:project.currentRevisionId,
    idempotencyKey:key, mode, ...(mode === 'reconcile' ? {repositoryId,
      graphScope:{profile:'foundation-graph-v1',status:partial?'partial':'complete',gaps:partial?['main.go unavailable']:[]}} : {}),
    manifest:{repositoryName:'orders-demo',provider,snapshot:{dirty:false,consistency:'verified',
      capturedAt:new Date().toISOString(),files}}, inventory:inventory(files.length,partial)};
}
const n = (key, kind, name, ev) => ({op:'upsert_node',node:{externalKey:key,kind,name,
  attributes:kind === 'http_operation' ? {method:'GET',path:'/orders'} : {},evidenceKeys:[ev]}});
const e = (key, kind, fromKey, toKey, ev) => ({op:'upsert_edge',edge:{externalKey:key,kind,
  fromKey,toKey,attributes:{},evidenceKeys:[ev]}});
const ev = (s, key, type, subject, file, contentHash, explanation) => ({op:'upsert_evidence',
  evidence:{externalKey:key,subjectType:type,subjectKey:subject,method:'ast',status:'explicit',
    source:{repositoryId:s.repositoryId,snapshotId:s.snapshotId,file,contentHash,startLine:1,endLine:1},explanation}});
const batchInput = (s, batchId, version, commands) => ({projectId:project.id,importId:s.id,
  batchId,expectedImportVersion:version,payloadHash:sha(JSON.stringify(sorted(commands))),commands});
const commitInput = (s, p, key) => ({projectId:project.id,importId:s.id,expectedVersion:project.version,
  expectedImportVersion:p.version,candidateHash:p.candidateHash,idempotencyKey:key});
```

Complete zero datastores is justified only for this fully known tiny fixture.
Unknown migrations/producers/consumers/jobs/contracts/tests are unsupported with
null denominators, keeping overall inventory coverage partial.

## First import: empty revision to five nodes and two edges

Start with a project created through the pinned backend-overview workflow and its
empty revision. Save the returned project as `project`; do not create a duplicate
if it already exists. A fresh isolated fixture may use:

```javascript
let project = await tool('create_backend_project', {name:'Orders fixture',idempotencyKey:'fixture-project'});
const begin1 = beginInput('fixture-begin-1',[file('main.go',oldHash),file('independent.go',otherHash)]);
const s1 = await tool('begin_backend_import',begin1);
const subjects = [n('service:orders','service','Orders service','ev-service'),
  n('module:orders','module','Orders module','ev-module'),
  n('operation:orders','http_operation','GET /orders','ev-operation'),
  n('handler:orders','handler','ListOrders','ev-handler'),
  n('handler:independent','handler','IndependentHandler','ev-independent'),
  e('handles:orders','handles','operation:orders','handler:orders','ev-handles'),
  e('calls:independent','calls','handler:orders','handler:independent','ev-calls')];
const evidence1 = [['ev-service','node','service:orders'],['ev-module','node','module:orders'],
  ['ev-operation','node','operation:orders'],['ev-handler','node','handler:orders'],
  ['ev-handles','edge','handles:orders'],['ev-calls','edge','calls:independent']]
  .map(([key,type,subject]) => ev(s1,key,type,subject,'main.go',oldHash,'Original source assertion'));
evidence1.push(ev(s1,'ev-independent','node','handler:independent','independent.go',otherHash,'Independent declaration'));
const batch1 = batchInput(s1,'fixture-batch-1',s1.version,[...subjects,...evidence1]);
const receipt1 = await tool('put_backend_import_batch',batch1);
const p1 = await tool('preview_backend_import',{projectId:project.id,importId:s1.id,
  expectedImportVersion:receipt1.acceptedVersion,baseRevisionId:project.currentRevisionId});
// Require p1.state === 'ready' and nonnull candidateHash before commit.
const commit1 = commitInput(s1,p1,'fixture-commit-1');
const r1 = await tool('commit_backend_import',commit1); project = r1.project;
const g1 = await tool('query_backend_graph',{projectId:project.id,revisionId:r1.revision.id,recordType:'nodes'});
const edges1 = await tool('query_backend_graph',{projectId:project.id,revisionId:r1.revision.id,recordType:'edges'});
const ids = Object.fromEntries([...g1.nodes,...edges1.edges].map(x => [x.externalKey,x.id]));
```

Expected: five nodes/two edges/seven evidence records, fixed revision r1.revision.id,
no stale records, partial inventory for unsupported categories. Read coverage.
Failure: evidence with a wrong file hash is backend_import_invalid; no batch is
accepted. Correct source/hash and send a new batchId at the unchanged session
version. Unresolved references yield needs_resolution/null preview hash; add or
repair their bundles in a new batch and preview again, never commit that null hash.

## Lost response: replay the whole request

Starting from the accepted first batch/commit, suppose the response was lost.
These exact requests recover their original receipts even after version advancement:

```javascript
const recoveredBatch = await tool('put_backend_import_batch',batch1);
const recoveredCommit = await tool('commit_backend_import',commit1);
const status1 = await tool('get_backend_import',{projectId:project.id,importId:s1.id});
```

Expected: original identities/acceptedVersion and original r1 receipt;
status1.committedRevisionId identifies r1 even after a later head. Altering commands
under batch1.batchId returns backend_import_batch_conflict; altering versions/hash
under commit1.idempotencyKey returns backend_idempotency_conflict. Recover by
replaying the saved original input, then reread current state. Resume after restart
with list_backend_imports/get_backend_import and acceptedBatches pagination.

## Explicit rename and proven deletion

Start from r1 and the same provider/repository. Capture verified new main.go and
complete file inventory proving independent.go absent. Omitted initial mode on this
sourced base returns backend_reimport_unsupported; recovery is an explicit new
reconcile begin request/key, not replacing versions inside the old request.

```javascript
const begin2 = beginInput('fixture-begin-2',[file('main.go',newHash)],'reconcile',s1.repositoryId);
const s2 = await tool('begin_backend_import',begin2);
const map2 = batchInput(s2,'fixture-map-2',s2.version,[{op:'map_identity',identity:{recordType:'node',
  fromExternalKey:'handler:orders',toExternalKey:'handler:orders:v2',expectedId:ids['handler:orders'],
  reason:'Same handler renamed',evidenceKeys:['ev-handler-v2']}}]);
const mapped = await tool('put_backend_import_batch',map2);
const deletes = ['calls:independent','handler:independent'].map(externalKey => ({op:'delete_assertion',
  deletion:{recordType:externalKey.startsWith('calls:')?'edge':'node',externalKey,
    expectedId:ids[externalKey],reason:'Removed from verified complete fixture'}}));
const current = [n('service:orders','service','Orders service','ev-service'),
  n('module:orders','module','Orders module','ev-module'),
  n('operation:orders','http_operation','GET /orders','ev-operation'),
  n('handler:orders:v2','handler','ListOrdersV2','ev-handler-v2'),
  e('handles:orders','handles','operation:orders','handler:orders:v2','ev-handles')];
const evidence2 = [['ev-service','node','service:orders'],['ev-module','node','module:orders'],
  ['ev-operation','node','operation:orders'],['ev-handler-v2','node','handler:orders:v2'],
  ['ev-handles','edge','handles:orders']].map(([key,type,subject]) =>
    ev(s2,key,type,subject,'main.go',newHash,'Current source assertion'));
const batch2 = batchInput(s2,'fixture-batch-2',mapped.acceptedVersion,[...current,...deletes,...evidence2]);
const receipt2 = await tool('put_backend_import_batch',batch2);
const p2 = await tool('preview_backend_import',{projectId:project.id,importId:s2.id,
  expectedImportVersion:receipt2.acceptedVersion,baseRevisionId:r1.revision.id});
// Inspect ready diagnostics and saved source/identity/deletion pages at p2.version.
const commit2 = commitInput(s2,p2,'fixture-commit-2');
const r2 = await tool('commit_backend_import',commit2); project = r2.project;
const comparison = await tool('compare_backend_revisions',{projectId:project.id,
  fromRevisionId:r1.revision.id,toRevisionId:r2.revision.id,limit:1});
```

Expected: renamed handler keeps ids['handler:orders']; other retained UUIDs stay
stable. Four nodes/one edge remain; comparison nodes={added:0,removed:1,modified:1},
edges={added:0,removed:1,modified:0}, identityMappings=1 and sourceChanges=2.
Inventory still discloses unsupported categories. Open removed subjects/evidence
at r1; comparison pages retain both pins and comparisonHash.

## Partial source: retain stale assertions

Start from r2. main.go is unavailable; only a metadata.go service assertion is
reobserved. Use partial graphScope and partial files/endpoints inventory. Never
stage delete_assertion for that missing file. Partial deletion proof instead yields
needs_resolution/null hash; remove the unsafe staged decision and preview again.

```javascript
const metadataHash = sha('service metadata');
const begin3 = beginInput('fixture-begin-3',[file('metadata.go',metadataHash)],'reconcile',s1.repositoryId,true);
const s3 = await tool('begin_backend_import',begin3);
const batch3 = batchInput(s3,'fixture-batch-3',s3.version,
  [n('service:orders','service','Orders service','ev-service'),
   ev(s3,'ev-service','node','service:orders','metadata.go',metadataHash,'Only service reobserved')]);
const receipt3 = await tool('put_backend_import_batch',batch3);
const p3 = await tool('preview_backend_import',{projectId:project.id,importId:s3.id,
  expectedImportVersion:receipt3.acceptedVersion,baseRevisionId:r2.revision.id});
const commit3 = commitInput(s3,p3,'fixture-commit-3');
const r3 = await tool('commit_backend_import',commit3); project = r3.project;
const coverage3 = await tool('get_backend_coverage',{projectId:project.id,revisionId:r3.revision.id});
```

Expected: same four node/one edge UUIDs; three stale nodes, one stale edge and four
stale evidence records. coverage3.coverage.status is partial; snapshots include
primary and retained_provenance. Old handler evidence stays pinned to r2's source.
Missing main.go is unknown source absence, not deletion. Final answer names these
gaps and consistency instead of claiming executed behavior or full reconstruction.

## PostgreSQL and SQLite: captured source imports

The static fixture sources are under
`internal/backendmodel/testdata/relational/orders/{postgresql,sqlite}/{v1,v2}/`:
`schema.sql`, `models.go` and ordered `migrations/*.sql`. They are declarations,
including unsupported bodies and a create→drop source_only target, not SQL to run.
The complete authored command bundles are the adjacent `commands.json` inputs;
substitute the returned session/history tokens as the executable example does in
`internal/mcp/tools_backend_relational_example_test.go`. Do not use expected.json
as import data. Provider/repository/manifest hashes come from those exact inputs.
Use the ordered import protocol, not a partial request excerpt as a begin input.

The following are actual Task4 MCP SDK capture excerpts from isolated fixture
stores, after cold begin→batch→ready preview→commit. IDs/hashes identify those
fixture runs only; for another server use its returned IDs and source hashes.
No guideSetId is inferred from these data revision pins: select/verify the current
advertised guide set first and pin every related guide read to that same set.
Selected response fields are shown; omitted coverage/limitations are still part
of the full response and must accompany an answer.

### Postgresql fixture: exact read pins

Ready preview: schema 2, 48 nodes, 50 edges and 141 evidence. The commit and identical lost-response replay returned revision `01a0f4e1-7969-770e-bb3f-5e7f461bb988`. Four tables and three physical FK relationships were read at that revision; unsupported migration/body coverage remains disclosed.

`query_backend_database` input:

```json
{
  "projectId": "01a0f4e1-72d0-79fb-840a-8472b0ef5ef9",
  "revisionId": "01a0f4e1-7969-770e-bb3f-5e7f461bb988",
  "datastoreId": "01a0f4e1-786b-788b-98b8-4368f1508d17",
  "facetKey": "sql",
  "recordType": "tables"
}
```

Selected table-page fields and its first complete TableItem:

```json
{
  "projectId": "01a0f4e1-72d0-79fb-840a-8472b0ef5ef9",
  "revisionId": "01a0f4e1-7969-770e-bb3f-5e7f461bb988",
  "semanticHash": "95951ebad2e8557668e0e439ac02599642ff5257800ced4f1f161d283466594a",
  "datastoreId": "01a0f4e1-786b-788b-98b8-4368f1508d17",
  "facetKey": "sql",
  "recordType": "tables",
  "facetStatus": "current",
  "nextCursor": "",
  "tableItems": [
    {
      "tableId": "01a0f4e1-786b-7ab4-a240-d57949761c40",
      "schemaId": "01a0f4e1-786b-794b-bfb0-07625bf77f59",
      "qualifiedName": "public.users",
      "columnCount": 4,
      "facetKeys": [
        "orm",
        "sql"
      ],
      "driftStatus": "unknown"
    }
  ],
  "relationshipItems": []
}
```

This excerpt shows one item, not the complete table inventory. The actual page has four; tableItems is populated and relationshipItems empty. Keep this revision for node/graph/evidence reads, and fully page any larger result.

### Sqlite fixture: exact read pins

Ready preview: schema 2, 47 nodes, 49 edges and 139 evidence. The commit and identical lost-response replay returned revision `01a0f4e1-88ac-7c51-9228-98e8e929145d`. Four tables and three physical FK relationships were read at that revision; unsupported migration/body coverage remains disclosed.

`query_backend_database` input:

```json
{
  "facetKey": "sql",
  "recordType": "tables",
  "projectId": "01a0f4e1-82d2-768f-aebf-d90b8bda7df6",
  "revisionId": "01a0f4e1-88ac-7c51-9228-98e8e929145d",
  "datastoreId": "01a0f4e1-87ad-780c-9000-7cec0b796898"
}
```

Selected table-page fields and its first complete TableItem:

```json
{
  "projectId": "01a0f4e1-82d2-768f-aebf-d90b8bda7df6",
  "revisionId": "01a0f4e1-88ac-7c51-9228-98e8e929145d",
  "semanticHash": "92280b25f86b4e2851186715d5bd624da362f6ae49d609015529c77059db2592",
  "datastoreId": "01a0f4e1-87ad-780c-9000-7cec0b796898",
  "facetKey": "sql",
  "recordType": "tables",
  "facetStatus": "current",
  "nextCursor": "",
  "tableItems": [
    {
      "tableId": "01a0f4e1-87ad-7a4d-af57-37a7a778920e",
      "schemaId": "01a0f4e1-87ad-78d0-a596-13518f821961",
      "qualifiedName": "main.users",
      "columnCount": 4,
      "facetKeys": [
        "orm",
        "sql"
      ],
      "driftStatus": "unknown"
    }
  ],
  "relationshipItems": []
}
```

This excerpt shows one item, not the complete table inventory. The actual page has four; tableItems is populated and relationshipItems empty. Keep this revision for node/graph/evidence reads, and fully page any larger result.

### Ordered composite FK and participation

The PostgreSQL SQL orders→users relationship below is a complete captured RelationshipItem. Pair order is tenant_id→tenant_id, user_id→id. sourceCardinality means source rows per target; targetCardinality means target rows per source. Its explicit source min0 does not require every user to have an order.

```json
{
  "edgeId": "01a0f4e1-786f-752f-b96a-2fc746765406",
  "constraintId": "01a0f4e1-786e-76c8-9d50-0edebce9c147",
  "sourceTableId": "01a0f4e1-786c-725e-9695-9a88bd4cef9f",
  "targetTableId": "01a0f4e1-786b-7ab4-a240-d57949761c40",
  "columnPairs": [
    {
      "fromColumnId": "01a0f4e1-786c-73ef-9bde-d2bbd9ecd896",
      "toColumnId": "01a0f4e1-786b-7c5a-9d7b-75f947a6aaa8"
    },
    {
      "fromColumnId": "01a0f4e1-786c-7770-9ac5-0c4a1349d50b",
      "toColumnId": "01a0f4e1-786b-7ddb-a873-748b2d3ad824"
    }
  ],
  "evidenceIds": [
    "01a0f4e1-786a-7c35-bd52-ef0ff60c4725"
  ],
  "sourceCardinality": {
    "min": 0,
    "max": "many",
    "basis": [
      "Declared relationship 01a0f4e1-786f-752f-b96a-2fc746765406 does not require a source row for every target",
      "Current explicit nonmatching global key 01a0f4e1-786e-753f-9765-ed3ecb77e1ad with evidence 01a0f4e1-7869-796c-ab6d-073cb6adfc71",
      "Complete selected constraint inventory for 01a0f4e1-786c-725e-9695-9a88bd4cef9f has no matching global unique key"
    ]
  },
  "targetCardinality": {
    "min": 1,
    "max": "1",
    "basis": [
      "Current explicit complete key 01a0f4e1-786e-7208-89f4-3be9120109fa with evidence 01a0f4e1-7869-759d-8412-f35b225c4843",
      "Current explicit nonmatching global key 01a0f4e1-786e-737c-a5e6-b30f34fbbfe6 with evidence 01a0f4e1-7869-7799-9188-f76926d19a0d",
      "Selected source column 01a0f4e1-786c-73ef-9bde-d2bbd9ecd896 nullable known false with evidence 01a0f4e1-7866-7947-933f-59b3e829d82e",
      "Selected source column 01a0f4e1-786c-7770-9ac5-0c4a1349d50b nullable known false with evidence 01a0f4e1-7866-7d2f-b46d-898b7f02ac33",
      "Current explicit nondeferrable MATCH SIMPLE 01a0f4e1-786e-76c8-9d50-0edebce9c147 with selected source nullability"
    ]
  },
  "status": "explicit",
  "targetReason": null
}
```

A join table stays a physical table plus its FK edges; no synthetic N:M relationship is added. Matching complete current target uniqueness can prove max1 independently of the minimum. Conditional/expression indices do not establish a global column-only key.

### Unknown minimum with an independently proved maximum

These additional ORM examples were captured through real public MCP HTTP calls in the Task4 preliminary run, separately from the SDK captures above. They are source-only inspection examples, not final frozen-binary acceptance. SQL-only datastore/schema descriptors still have ORM facets on descendants; discover those facets through the whole pinned hierarchy. Each captured ORM page reports facetStatus unknown, but a complete current explicit target key proves max1 while MATCH/deferrability leaves min unknown.

postgresql — exact request and selected response fields:

```json
{
  "projectId": "01a0f4f3-d32a-7b43-bd5a-bb44100b3095",
  "revisionId": "01a0f4f3-d467-7dae-9184-56f26af8eafa",
  "datastoreId": "01a0f4f3-d41f-7a6e-81e7-562682ba1df2",
  "facetKey": "orm",
  "recordType": "relationships",
  "limit": 500
}
```

```json
{
  "revisionId": "01a0f4f3-d467-7dae-9184-56f26af8eafa",
  "semanticHash": "f8a4f96d5180f9c36eb2492dceabc54f49979e68fc24443884ddd2b0d07d3496",
  "facetKey": "orm",
  "facetStatus": "unknown",
  "relationshipItems": [
    {
      "edgeId": "01a0f4f3-d424-71df-8ef5-f2fcc49bbf05",
      "targetCardinality": {
        "min": null,
        "max": "1",
        "basis": [
          "Current explicit complete key 01a0f4f3-d422-7c04-bb16-c09f19a91427 with evidence 01a0f4f3-d41d-74c8-b304-ee0ee826f061",
          "Current explicit nonmatching global key 01a0f4f3-d422-7d95-8f19-4c7cbdb13926 with evidence 01a0f4f3-d41d-76c0-a8a1-c52e436b7d74",
          "Missing selected key proof 01a0f4f3-d424-7570-83c6-caeb646ec067",
          "Selected source column 01a0f4f3-d420-786e-8b54-f5be7a170285 nullable known false with evidence 01a0f4f3-d41a-7f3f-ae93-0c7f9f210885",
          "Selected source column 01a0f4f3-d420-7c62-acac-4b2ddfc67e05 nullable known true with evidence 01a0f4f3-d41b-72ed-9f8b-71f78cb1df4f",
          "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for 01a0f4f3-d424-71df-8ef5-f2fcc49bbf05"
        ]
      }
    }
  ]
}
```

The full page also reports missing selected facet/key proofs and incomplete ORM analysis. Keep each basis proof/limitation; an unknown minimum does not erase the separately established maximum.

sqlite — exact request and selected response fields:

```json
{
  "projectId": "01a0f4f3-d629-7a56-9b13-941945aae81e",
  "revisionId": "01a0f4f3-d74b-78f9-8250-6da0d87e2e1e",
  "datastoreId": "01a0f4f3-d707-7e4d-9dde-97d7ba2d1b90",
  "facetKey": "orm",
  "recordType": "relationships",
  "limit": 500
}
```

```json
{
  "revisionId": "01a0f4f3-d74b-78f9-8250-6da0d87e2e1e",
  "semanticHash": "a3796ce3496a1c801ea90e63b3e7d74008660c82f8740b23ba1c9fb773a8625c",
  "facetKey": "orm",
  "facetStatus": "unknown",
  "relationshipItems": [
    {
      "edgeId": "01a0f4f3-d70c-7056-a2ad-571210113e3b",
      "targetCardinality": {
        "min": null,
        "max": "1",
        "basis": [
          "Current explicit complete key 01a0f4f3-d70a-7c87-bf23-9fa80456aa89 with evidence 01a0f4f3-d705-7b1a-a192-a5ce1cb0f920",
          "Current explicit nonmatching global key 01a0f4f3-d70a-7e14-aec2-089ba7bde04e with evidence 01a0f4f3-d705-7d02-94c2-81b0092b0edf",
          "Missing selected key proof 01a0f4f3-d70c-738d-84cf-cbf7f211a9c8",
          "Selected source column 01a0f4f3-d708-7bbe-a26b-f59a98c58fbc nullable known false with evidence 01a0f4f3-d703-753f-abb4-c1dc179d8ca4",
          "Selected source column 01a0f4f3-d708-7f12-ae40-b898983c31be nullable known true with evidence 01a0f4f3-d703-7bd2-a983-8f2fc3282701",
          "Minimum unknown: selected nullability, nondeferrable MATCH SIMPLE enforcement or target key is not established for 01a0f4f3-d70c-7056-a2ad-571210113e3b"
        ]
      }
    }
  ]
}
```

The full page also reports missing selected facet/key proofs and incomplete ORM analysis. Keep each basis proof/limitation; an unknown minimum does not erase the separately established maximum.

### SQL/ORM drift with pinned evidence

At PostgreSQL revision `01a0f4e1-7969-770e-bb3f-5e7f461bb988`, column `01a0f4e1-786c-7a9f-a061-dfdfa09fa46e` (`column:orders:total`) has SQL nativeType `numeric(18,2)` and ORM `numeric(12,2)`. SQLite retains its own `NUMERIC(18,2)` versus ORM `DECIMAL(12,2)` native strings. The captured PostgreSQL comparison and SQL evidence excerpt are:

```json
{
  "id": "01a0f4e1-786c-7a9f-a061-dfdfa09fa46e",
  "facetComparison": {
    "status": "different",
    "pairs": [
      {
        "leftFacetKey": "orm",
        "rightFacetKey": "sql",
        "status": "different",
        "changedPaths": [
          "/nativeType"
        ],
        "definitionDifferent": false
      }
    ]
  }
}
```

```json
{
  "id": "01a0f4e1-7867-72c4-96f2-f4211d90fad7",
  "subjectId": "01a0f4e1-786c-7a9f-a061-dfdfa09fa46e",
  "propertyPath": "/attributes/facets/sql",
  "status": "explicit",
  "source": {
    "repositoryId": "01a0f4e1-732e-7ff7-ad8a-6cc87bab3a8c",
    "snapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
    "file": "postgresql/v1/schema.sql",
    "contentHash": "902c33120041178dc4bcca6cbbd512d696e6ee3c8c346f12eb390c028e4adc88",
    "startLine": 20,
    "endLine": 20
  }
}
```

Pair fields are leftFacetKey/rightFacetKey; changedPaths sort as facet-relative JSON Pointers. A known contradiction remains different despite unrelated unknowns. definitionDifferent is separate from semantic property paths. Do not use source/completeness/freshness metadata as changed properties or interpret text inequality as SQL-equivalence analysis.

### Partial omission retains old facet snapshots

The SDK partial snapshot refreshed only table:orders SQL, then committed revision `01a0f4e1-7dca-7acc-83cf-67e3b534a3f0`. The omitted status column `01a0f4e1-786c-78e1-8704-d1553270b80c` retained both SQL and ORM claims, including different known DEFAULT values, at the original source snapshot:

```json
{
  "id": "01a0f4e1-786c-78e1-8704-d1553270b80c",
  "externalKey": "column:orders:status",
  "freshness": {
    "status": "stale",
    "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
    "reasons": [
      "not_reobserved"
    ]
  },
  "attributes": {
    "facets": {
      "sql": {
        "sourceSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
        "freshness": {
          "status": "stale",
          "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
          "reasons": [
            "not_reobserved"
          ]
        },
        "defaultExpression": {
          "value": "'draft'",
          "status": "known"
        }
      },
      "orm": {
        "sourceSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
        "freshness": {
          "status": "stale",
          "confirmedSnapshotId": "01a0f4e1-732f-7106-852c-cef37fc4e4f9",
          "reasons": [
            "not_reobserved"
          ]
        },
        "defaultExpression": {
          "status": "known",
          "value": "'pending'"
        }
      }
    }
  }
}
```

This is a selected-field excerpt of a retained node. Partial scope/stale evidence keeps coverage partial; omission never removes a facet or subject. A metadata-only datastore/routine upsert omitting its entire optional descriptor likewise retains committed facets and old proof stale alongside new metadata. Explicit null/malformed descriptors fail.

For a shared-proof collision, save the unresolved diagnostics and null candidateHash; do not commit. In a new addressed batch send `op:"remove"` with `remove:{recordType:"evidence",externalKey:oldProofKey}` to remove the conflicting staged evidence, then upsert the subject/facet using a distinct current proof key and upsert that current proof. The server retains old proof needed by omitted facets and restores the union. Repreview at the accepted version; commit only ready. Alternatively explicitly reassert every facet that needs the proof. This repair is a protocol recipe, not a fabricated captured response.

For endpoint conflict, an FK edge cannot retarget omitted facets. Reassert all edge facets with valid current pairs/proof, or use distinct stable edge keys for differing physical targets and explicitly retain/delete the old one under the deletion gates. No name-based retarget or inferred cascade.

### Explicit rename before target allocation

The captured v2 map batch changed `column:orders:status` to `column:orders:state`, preserving `01a0f4e1-786c-78e1-8704-d1553270b80c`. It ran before the target upsert. Complete actual mapping command and response:

```json
{
  "identity": {
    "recordType": "node",
    "fromExternalKey": "column:orders:status",
    "toExternalKey": "column:orders:state",
    "expectedId": "01a0f4e1-786c-78e1-8704-d1553270b80c",
    "reason": "Explicit source rename",
    "evidenceKeys": [
      "proof:v2:column:orders:state:sql"
    ]
  },
  "op": "map_identity"
}
```

```json
{
  "batchId": "mapping",
  "payloadHash": "0e64a3c320286ed0ad4634493a11103fac89c01668e74b4ed25bff761378ba45",
  "acceptedVersion": 2,
  "identities": [
    {
      "recordType": "node",
      "externalKey": "column:orders:state",
      "id": "01a0f4e1-786c-78e1-8704-d1553270b80c"
    }
  ]
}
```

V2 committed revision `01a0f4e1-80e0-7f78-8d1b-b96d88fca034`. Its later read at the original v1 pin still returned the original semanticHash and four-table page. The complete v2 bundle proves legacy_note/index deletion, repairs the view/index dependencies, and pins retained migration CREATE targets to their old immutable revision. source_only create→drop creates no active UUID. Never delete from a partial inventory or unavailable prior proof.

### Inspection-only design request

For typed schema designs use the separately negotiated database5 procedure.
Source import5 examples above keep their original protocol and CAS.

## Actual SDK proposal examples: PostgreSQL and SQLite

These are actual responses from the deterministic SDK fixture import, create,
preview, apply, pinned reads and restart receipt replay. The SQL/Go fixture files
were read as bytes and never executed. IDs below belong to those example runs;
use IDs returned by your own baseline reads. Both examples leave source records
and project/source pointers unchanged. Every data/writer/migration check remains
unverified. Root, either leaf alone, bundle and server-only load the same pinned
procedures with the actual topic owners.

### postgresql: nullable legacy_note to desired NOT NULL

`create_backend_proposal` input after choosing the exact source baseline:

```json
{
  "idempotencyKey": "proposal-create",
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "name": "Require users",
  "baseRevisionId": "01a0f7ed-a479-78a3-b4c2-e0318bea27bc",
  "repositoryId": "01a0f7ed-5c70-7f06-9514-f0bbeeaf8056",
  "datastoreId": "01a0f7ed-9725-73eb-8e9b-ad26a5547bf5",
  "facetKey": "sql"
}
```

`preview_backend_proposal_commands` input (no mutation yet):

```json
{
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "nullable": false
    }
  ],
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da"
}
```

Actual preview response; its hash is bound to this exact draft and command:

```json
{
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "baseRevisionId": "01a0f7ed-a479-78a3-b4c2-e0318bea27bc",
  "baseSemanticHash": "7203cee5fbfd06cd79d572228fb40a4133be452fd8c323283ac781377fba7194",
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "draftHash": "c9a24d4f029fb4673243b77c49681288ab0adc9771c1c6155b402c410b5327bd",
  "expectedVersion": 1,
  "candidateHash": "9c9f0ee16aea27c153e9a67388f31d5aa4729fced64baa5ab4386e455e5d8a59",
  "candidateGraphHash": "ef6fad3daace09ccc6c334a9ca817e52e0fa5f147165d3ef23a6bc6c0bb66233",
  "changes": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "subjectId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "before": {
        "nullable": {
          "status": "known",
          "value": true
        }
      },
      "after": {
        "nullable": {
          "status": "known",
          "value": false
        }
      },
      "generatedIds": {}
    }
  ],
  "criteria": [
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
      "kind": "existing_data",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Check existing nulls and backfill before enforcing NOT NULL",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
      "kind": "writers",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
      "kind": "migration_plan",
      "targetIds": [
        "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
      ],
      "description": "Review migration, rollout and rollback against the pinned baseline",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    }
  ],
  "diagnostics": [],
  "limitations": [
    "Existing data, writer inventory and migration feasibility are unverified; runtime enforcement and impact analysis are unavailable"
  ]
}
```

`apply_backend_proposal_commands` input using that preview and its own retained key:

```json
{
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf",
      "nullable": false
    }
  ],
  "idempotencyKey": "save-require-user",
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ed-ad02-7343-9470-bc1b7c6fa149",
  "candidateHash": "9c9f0ee16aea27c153e9a67388f31d5aa4729fced64baa5ab4386e455e5d8a59"
}
```

Read the acknowledged revision with `get_backend_node`:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposal": {
    "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
    "proposalRevisionId": "01a0f7ed-bb74-7f6c-b27d-b39e67327548"
  },
  "nodeId": "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
}
```

Exact value at `proposalProjection.sourceRecord.attributes.facets.sql.nullable`:

```json
{
  "status": "known",
  "value": true
}
```

Exact value at `proposalProjection.effectiveFacet.values.nullable`:

```json
{
  "status": "known",
  "value": false
}
```

Exact value at `proposalProjection.effectiveFacet.propertyOrigins["/nullable"]`:

```json
{
  "kind": "intent",
  "commandId": "require-user",
  "reason": "Require legacy notes after backfill",
  "evidenceIds": []
}
```

### postgresql: complete ordered FK and rollout criterion

The next preview uses the acknowledged proposal version/draft. It creates a
separate desired membership FK with ordered tenant/user pairs:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 2,
  "draftRevisionId": "01a0f7ed-bb74-7f6c-b27d-b39e67327548",
  "commands": [
    {
      "type": "alter_constraint",
      "commandId": "membership",
      "reason": "Retain user membership",
      "action": "create",
      "name": "designed_membership_fk",
      "tableId": "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "targetTableId": "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1",
      "columnPairs": [
        {
          "fromColumnId": "01a0f7ed-973c-707e-b482-a7557003b1e2",
          "toColumnId": "01a0f7ed-972c-7533-800b-06b4ee402966"
        },
        {
          "fromColumnId": "01a0f7ed-9742-786a-970b-9ec021e04bb7",
          "toColumnId": "01a0f7ed-972f-7581-8d4e-41f29c0f2277"
        }
      ],
      "updateAction": "no_action",
      "deleteAction": "restrict",
      "matchType": "simple",
      "deferrable": false,
      "initiallyDeferred": false
    }
  ]
}
```

Actual preview change, including stable designed constraint/reference IDs:

```json
{
  "type": "alter_constraint",
  "commandId": "membership",
  "subjectId": "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
  "before": {},
  "after": {
    "columnPairs": [
      {
        "fromColumnId": "01a0f7ed-973c-707e-b482-a7557003b1e2",
        "toColumnId": "01a0f7ed-972c-7533-800b-06b4ee402966"
      },
      {
        "fromColumnId": "01a0f7ed-9742-786a-970b-9ec021e04bb7",
        "toColumnId": "01a0f7ed-972f-7581-8d4e-41f29c0f2277"
      }
    ],
    "deleteAction": {
      "status": "known",
      "value": "restrict"
    },
    "matchType": {
      "status": "known",
      "value": "simple"
    },
    "updateAction": {
      "status": "known",
      "value": "no_action"
    }
  },
  "generatedIds": {
    "constraintId": "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
    "edgeId": "66b018f0-ed90-5c84-8387-0178158ea413"
  }
}
```

The pinned ER read after apply returns status:proposed and
runtimeStatus:unverified. Bounds retain inherited uniqueness and any limitations.
Expression, conditional, stale or incomplete uniqueness cannot establish a global
key. Proposed FK intent does not prove orphan-free data or action safety.

The following authored migration-plan criterion supplements required checks:

```json
{
  "projectId": "01a0f7ed-5926-74f9-84a9-4703b7b4ce1b",
  "proposalId": "01a0f7ed-ad02-7333-aa4f-b967371c52da",
  "expectedVersion": 3,
  "draftRevisionId": "01a0f7ed-da03-7753-a3f8-ad7124c384e3",
  "commands": [
    {
      "commandId": "rollout",
      "reason": "Capture rollout planning",
      "criteria": [
        {
          "key": "rollback",
          "kind": "migration_plan",
          "targetIds": [
            "01a0f7ed-9738-7e5e-bced-821f9e32b74a"
          ],
          "description": "Review rollout and rollback before implementation"
        }
      ],
      "type": "set_criteria"
    }
  ]
}
```

Actual saved criteria are all unverified:

```json
[
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
    "kind": "existing_data",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Check existing nulls and backfill before enforcing NOT NULL",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
    "kind": "writers",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ed-974c-75a9-86b1-31fd4aa8fcbf"
    ],
    "description": "Review migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:referential_integrity",
    "kind": "referential_integrity",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Check existing orphan rows and ordered pair values before enforcing the FK",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:target_uniqueness",
    "kind": "target_uniqueness",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Verify actual target uniqueness and FK enforcement against the selected baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:writers",
    "kind": "writers",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Identify every source/target writer and verify update/delete actions; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "a7d5d0cd-2ae0-59c4-8973-9e70591b551b",
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a",
      "01a0f7ed-9729-74f9-8808-9c4ec5c5d4f1"
    ],
    "description": "Review FK migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "rollback",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ed-9738-7e5e-bced-821f9e32b74a"
    ],
    "description": "Review rollout and rollback before implementation",
    "origin": "authored",
    "status": "unverified",
    "commandId": "rollout"
  }
]
```

After those later saves and a database restart, replaying the original
NOT NULL apply request/key returned byte-for-byte the original receipt.
Creation replay also returned its original acknowledgement. Reading the old
proposal revision still returned its immutable original draft; current CAS
version was 4. A later source head is reported as baseOutdated, without rebasing
the proposal. Ready/rebase belongs to B4; measured checks and impact are later.

### sqlite: nullable legacy_note to desired NOT NULL

`create_backend_proposal` input after choosing the exact source baseline:

```json
{
  "idempotencyKey": "proposal-create",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "name": "Require users",
  "baseRevisionId": "01a0f7ee-d8f9-743d-b97a-a73461e7c9b3",
  "repositoryId": "01a0f7ee-9003-7697-90f6-bfa0f849ca79",
  "datastoreId": "01a0f7ee-cb9f-7e9b-87bb-75c730f459d4",
  "facetKey": "sql"
}
```

`preview_backend_proposal_commands` input (no mutation yet):

```json
{
  "expectedVersion": 1,
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "nullable": false
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9"
}
```

Actual preview response; its hash is bound to this exact draft and command:

```json
{
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "baseRevisionId": "01a0f7ee-d8f9-743d-b97a-a73461e7c9b3",
  "baseSemanticHash": "3d08a0a78244e72d3c50cb0bb4c3644f3359e1b10a4cc6e403a56eadb7bdbfc3",
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "draftHash": "7bdaa877a41c6783236b8e015282d1a63566fdfb5ea69fd1741727f9f8f77405",
  "expectedVersion": 1,
  "candidateHash": "73e95b6fe3ba818d175bd330c03d89b6f737bcf74d9b6a43f79db0b754aa5cf8",
  "candidateGraphHash": "e728151dd3a79bff4b7e3ee41ca20426445b53c49f0149a3d049a2ad86d2eb32",
  "changes": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "subjectId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "before": {
        "nullable": {
          "status": "known",
          "value": true
        }
      },
      "after": {
        "nullable": {
          "status": "known",
          "value": false
        }
      },
      "generatedIds": {}
    }
  ],
  "criteria": [
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
      "kind": "existing_data",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Check existing nulls and backfill before enforcing NOT NULL",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
      "kind": "writers",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    },
    {
      "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
      "kind": "migration_plan",
      "targetIds": [
        "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
      ],
      "description": "Review migration, rollout and rollback against the pinned baseline",
      "origin": "required",
      "status": "unverified",
      "commandId": "require-user"
    }
  ],
  "diagnostics": [],
  "limitations": [
    "Existing data, writer inventory and migration feasibility are unverified; runtime enforcement and impact analysis are unavailable"
  ]
}
```

`apply_backend_proposal_commands` input using that preview and its own retained key:

```json
{
  "draftRevisionId": "01a0f7ee-e173-71ba-a1f6-07ea0d5c353e",
  "candidateHash": "73e95b6fe3ba818d175bd330c03d89b6f737bcf74d9b6a43f79db0b754aa5cf8",
  "commands": [
    {
      "type": "alter_column",
      "commandId": "require-user",
      "reason": "Require legacy notes after backfill",
      "columnId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
      "nullable": false
    }
  ],
  "idempotencyKey": "save-require-user",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 1
}
```

Read the acknowledged revision with `get_backend_node`:

```json
{
  "proposal": {
    "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
    "proposalRevisionId": "01a0f7ee-f020-7a8f-b8af-d9ed84d86a62"
  },
  "nodeId": "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b",
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17"
}
```

Exact value at `proposalProjection.sourceRecord.attributes.facets.sql.nullable`:

```json
{
  "status": "known",
  "value": true
}
```

Exact value at `proposalProjection.effectiveFacet.values.nullable`:

```json
{
  "status": "known",
  "value": false
}
```

Exact value at `proposalProjection.effectiveFacet.propertyOrigins["/nullable"]`:

```json
{
  "kind": "intent",
  "commandId": "require-user",
  "reason": "Require legacy notes after backfill",
  "evidenceIds": []
}
```

### sqlite: complete ordered FK and rollout criterion

The next preview uses the acknowledged proposal version/draft. It creates a
separate desired membership FK with ordered tenant/user pairs:

```json
{
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 2,
  "draftRevisionId": "01a0f7ee-f020-7a8f-b8af-d9ed84d86a62",
  "commands": [
    {
      "type": "alter_constraint",
      "commandId": "membership",
      "reason": "Retain user membership",
      "action": "create",
      "name": "designed_membership_fk",
      "tableId": "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "targetTableId": "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4",
      "columnPairs": [
        {
          "fromColumnId": "01a0f7ee-cbb6-703d-8ba4-51dda0eb6cf4",
          "toColumnId": "01a0f7ee-cba6-7cc8-a8eb-87ff6155732e"
        },
        {
          "fromColumnId": "01a0f7ee-cbbc-7191-9aa5-ab8bb009a720",
          "toColumnId": "01a0f7ee-cba9-7d91-8502-ba686f6043e7"
        }
      ],
      "updateAction": "no_action",
      "deleteAction": "restrict",
      "matchType": "simple",
      "deferrable": false,
      "initiallyDeferred": false
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17"
}
```

Actual preview change, including stable designed constraint/reference IDs:

```json
{
  "type": "alter_constraint",
  "commandId": "membership",
  "subjectId": "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
  "before": {},
  "after": {
    "columnPairs": [
      {
        "fromColumnId": "01a0f7ee-cbb6-703d-8ba4-51dda0eb6cf4",
        "toColumnId": "01a0f7ee-cba6-7cc8-a8eb-87ff6155732e"
      },
      {
        "fromColumnId": "01a0f7ee-cbbc-7191-9aa5-ab8bb009a720",
        "toColumnId": "01a0f7ee-cba9-7d91-8502-ba686f6043e7"
      }
    ],
    "deleteAction": {
      "status": "known",
      "value": "restrict"
    },
    "matchType": {
      "status": "known",
      "value": "simple"
    },
    "updateAction": {
      "status": "known",
      "value": "no_action"
    }
  },
  "generatedIds": {
    "constraintId": "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
    "edgeId": "c9fb4dab-8b05-5fb4-b1de-b6bc228e0687"
  }
}
```

The pinned ER read after apply returns status:proposed and
runtimeStatus:unverified. Bounds retain inherited uniqueness and any limitations.
Expression, conditional, stale or incomplete uniqueness cannot establish a global
key. Proposed FK intent does not prove orphan-free data or action safety.

The following authored migration-plan criterion supplements required checks:

```json
{
  "commands": [
    {
      "type": "set_criteria",
      "commandId": "rollout",
      "reason": "Capture rollout planning",
      "criteria": [
        {
          "key": "rollback",
          "kind": "migration_plan",
          "targetIds": [
            "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d"
          ],
          "description": "Review rollout and rollback before implementation"
        }
      ]
    }
  ],
  "projectId": "01a0f7ee-8c95-76e9-8a92-8ab9cac36b17",
  "proposalId": "01a0f7ee-e173-71b2-aa04-e1bf1c0ec9e9",
  "expectedVersion": 3,
  "draftRevisionId": "01a0f7ef-0f67-78d9-881b-ec2e008aceec"
}
```

Actual saved criteria are all unverified:

```json
[
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:existing_data",
    "kind": "existing_data",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Check existing nulls and backfill before enforcing NOT NULL",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:writers",
    "kind": "writers",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Identify all writers and verify omission/null handling; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:3e07f95cfe0c4b3b6f50b78e8f63497600cee07252606e135d466e1b63ff9042:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ee-cbc5-77e3-b97c-7dfaf87d468b"
    ],
    "description": "Review migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "require-user"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:referential_integrity",
    "kind": "referential_integrity",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Check existing orphan rows and ordered pair values before enforcing the FK",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:target_uniqueness",
    "kind": "target_uniqueness",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Verify actual target uniqueness and FK enforcement against the selected baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:writers",
    "kind": "writers",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Identify every source/target writer and verify update/delete actions; writer coverage is unknown",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "required:bf5cf59e356652253268c604cbf8df8cfdb03a4a0d32b27ad158e581709c80e4:migration_plan",
    "kind": "migration_plan",
    "targetIds": [
      "3c6ca83e-d403-5ebb-a351-3b7852681ad9",
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d",
      "01a0f7ee-cba3-7d26-a56f-6c4506a52bd4"
    ],
    "description": "Review FK migration, rollout and rollback against the pinned baseline",
    "origin": "required",
    "status": "unverified",
    "commandId": "membership"
  },
  {
    "key": "rollback",
    "kind": "migration_plan",
    "targetIds": [
      "01a0f7ee-cbb2-7eb8-b266-a6b56e679a0d"
    ],
    "description": "Review rollout and rollback before implementation",
    "origin": "authored",
    "status": "unverified",
    "commandId": "rollout"
  }
]
```

After those later saves and a database restart, replaying the original
NOT NULL apply request/key returned byte-for-byte the original receipt.
Creation replay also returned its original acknowledgement. Reading the old
proposal revision still returned its immutable original draft; current CAS
version was 4. A later source head is reported as baseOutdated, without rebasing
the proposal. Ready/rebase belongs to B4; measured checks and impact are later.

## Public MCP source4 import → lineage query fixture

Select import5 and inspect4 with complete requirements in one verified guide
set; load backend-model, backend-import-protocol, backend-recovery and the
inspect4 flow/analysis references. This example is exercised by
`internal/mcp/tools_backend_lineage_example_test.go`,
`TestBackendLineageRealSDKGuideFixtureExample` through actual SDK tool calls.
The independent bundle is `internal/backendmodel/testdata/lineage/orders/`:
source.go.txt, commands.json and expected.json. Read source bytes as inert data;
never run source, SQL or migrations. expected.json is an oracle, not import data.
Use an isolated fixture project and retain every full write input/key/receipt.

With the earlier sha/sorted helpers and configured tool connection, Node.js
can load this fixture using the following ordered procedure. Begin input is
complete; actual IDs/hashes/versions always come from responses.

```javascript
const fs = require('node:fs');
const root = 'internal/backendmodel/testdata/lineage/orders/';
let lineageProject = await tool('create_backend_project',
  {name:'Lineage fixture',idempotencyKey:'lineage-example-create'});
const sourceHash = sha(fs.readFileSync(root+'source.go.txt'));
const lineageInventory = ['files','endpoints','datastores','migrations',
  'producers','consumers','jobs','contracts','tests'].map(category => {
    const captured = ['files','endpoints','datastores'].includes(category);
    return {category,status:captured?'complete':'unsupported',knownCount:captured?1:0,
      denominator:captured?1:null,discoverySource:'source fixture',gaps:[],
      reason:captured?'':'Outside captured fixture'};
  });
const lineageBegin = {projectId:lineageProject.id,expectedVersion:lineageProject.version,
  baseRevisionId:lineageProject.currentRevisionId,idempotencyKey:'lineage-example-begin',
  mode:'initial',profile:'field-lineage-v1',inventory:lineageInventory,
  manifest:{repositoryName:'orders',provider:{name:'orders-fixture',version:'1',
    namespace:'lineage-fixture',method:'agent',profiles:['foundation-graph-v1',
      'relational-graph-v1','runtime-flow-v1','field-lineage-v1'],
    limitations:['Static bounded fixture']},snapshot:{dirty:false,consistency:'verified',
      capturedAt:'2026-10-01T12:00:00Z',files:[{path:'source.go.txt',contentHash:sourceHash,
        fileType:'go',analysisStatus:'analyzed'}]}}};
const lineageSession = await tool('begin_backend_import',lineageBegin);
const lineageCommands = JSON.parse(fs.readFileSync(root+'commands.json','utf8')
  .replaceAll('@repositoryId@',lineageSession.repositoryId)
  .replaceAll('@snapshotId@',lineageSession.snapshotId));
const lineageBatch = {projectId:lineageProject.id,importId:lineageSession.id,
  batchId:'lineage-example',expectedImportVersion:lineageSession.version,
  payloadHash:sha(JSON.stringify(sorted(lineageCommands))),commands:lineageCommands};
const lineageReceipt = await tool('put_backend_import_batch',lineageBatch);
const lineageIds = Object.fromEntries(lineageReceipt.identities
  .filter(x=>x.recordType==='node').map(x=>[x.externalKey,x.id]));
const lineagePreview = await tool('preview_backend_import',{projectId:lineageProject.id,
  importId:lineageSession.id,expectedImportVersion:lineageReceipt.acceptedVersion,
  baseRevisionId:lineageProject.currentRevisionId});
if (lineagePreview.state !== 'ready' || !lineagePreview.candidateHash)
  throw Error('Inspect and repair preview; no commit');
// Independently audit every source assertion/proof at this ready tuple first.
const lineageCommit = {projectId:lineageProject.id,importId:lineageSession.id,
  expectedVersion:lineageProject.version,expectedImportVersion:lineagePreview.version,
  candidateHash:lineagePreview.candidateHash,idempotencyKey:'lineage-example-commit'};
const lineageResult = await tool('commit_backend_import',lineageCommit);
const lineagePin = lineageResult.revision.id;
const requestSeed = {kind:'api_field',nodeId:lineageIds.request};
const amountSeed = {kind:'column',nodeId:lineageIds['column:orders:amount'],facetKey:'sql'};
const responseSeed = {kind:'api_field',nodeId:lineageIds.response};
async function lineagePages(seed,direction) {
  let cursor='', items=[];
  do {
    const input = {projectId:lineageProject.id,revisionId:lineagePin,seed,direction,
      maxDepth:8,limit:1,...(cursor?{cursor}:{})};
    const page = await tool('query_backend_lineage',input);
    // Verify project/revision/semanticHash/complete seed/direction before display.
    // Retain page.coverage, truncated, truncationReasons and limitations.
    items.push(...page.items); cursor=page.nextCursor;
  } while(cursor);
  return items;
}
const requestMappings = await lineagePages(requestSeed,'forward');
const amountMappings = await lineagePages(amountSeed,'forward');
const responseOrigins = await lineagePages(responseSeed,'reverse');
const unknownBoundary = await lineagePages(
  {kind:'api_field',nodeId:lineageIds.token},'reverse');
```

Expected request→column witness: m01-request,m02-parameter,m03-write;
amount column→response: m04-amount,m06-total; reverse tax witness:
m06-total,m05-tax (use receipt UUIDs). m06-total retains both ordered query
results amount then tax; forward arrival through amount does not reach tax as a
co-input. Reverse expands both. m10-unknown remains a boundary with its complete
destination/inputs visible. The constant is m08-constant; unknown with zero
inputs is still unknown, never relabeled constant. Static fixture includes cycle
and alternative mappings, not runtime execution or every possible path.

On uncertain commit replay lineageCommit unchanged and require original receipt;
never replace CAS/key with today's head. Evidence/owner/value inspector reads use
lineagePin and complete facet/collection/opaque portKey. Redacted shape/explanation
must omit sensitive constants and samples; source-local API field identity does
not resolve external API pins; their separate inspect4 contract is in backend-flow-reference. Old foundation/relational examples above keep
their source1/2 formats and ordinary workflow; source3 flow examples stay source3.
