# Foundation import examples

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
