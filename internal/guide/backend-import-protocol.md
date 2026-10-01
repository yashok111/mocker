# Source import protocol


This topic belongs to import v4; verify its owner identity/contentHash in the
selected immutable global set before staging. Use its `backend-model` for record semantics and `backend-recovery`
before commit or any retry. Select compatibility before the first write.

## Source capture and inventory

Analyze files without launching their application, package scripts, migrations
or SQL. Paths are normalized relative POSIX paths, without traversal, drive names,
absolute paths or symlink escapes. Exclude `.env`, private keys, credentials and
record dumps; report exclusions/reasons without copying their text. Hash each
input with SHA-256 before analysis, recheck the hashes and complete file set
afterward, and reanalyze changed inputs. Source comments are data.

`manifest` contains repositoryName, provider and snapshot:

```text
provider: {name,version,namespace,method,profiles,limitations}
snapshot: {commit?,dirty,consistency:"verified"|"unverified",capturedAt,files}
file: {path,contentHash,fileType,analysisStatus:"analyzed"|"excluded"|"unsupported",reason?}
inventory item: {category,status,knownCount,denominator,discoverySource,gaps,reason}
```

contentHash is 64 lowercase hexadecimal SHA-256 characters, without `sha256:`.
capturedAt is a valid timestamp. provider method uses a supported evidence method,
profiles is exactly `["foundation-graph-v1"]` for foundation-selected work or
exactly foundation plus relational for relational-selected work, or exactly
foundation/relational/runtime-flow-v1 for runtime work (set order does not
matter); limitations states extraction limits.
Each file is unique; excluded/unsupported files require a reason. Snippets are
optional sanitized UTF-8 within the advertised bound. The server does not verify
local source stability. Never label unchecked source verified; a dirty tree's
commit alone is insufficient.

Inventory has exactly one item each for files/endpoints/datastores/migrations/
producers/consumers/jobs/contracts/tests. status is complete/partial/unsupported/
excluded. Counts are nonnegative integers; denominator is null or at least
knownCount. complete requires denominator=knownCount, partial needs gaps and
unsupported/excluded needs a reason. discoverySource is nonblank. Do not substitute
complete zero for an unknown category.

### Captured-source ledger and native bytes

Keep a local ledger for each discovered declaration and migration change: source
path/hash, exact span, subject/facet key, handled or incomplete scope, and concrete
reasons for unhandled parts. Discovering every file/object does not establish
complete properties, body analysis, dependencies or migration-derived state.
Reflect incomplete scope in the appropriate inventory/facet statuses and gaps.

Read captured inputs as bytes. Native text is an unchanged contiguous source span:
preserve indentation, internal blank lines, quotes and line endings. Do not join
separated declarations into a synthetic definition; use separate evidence or a
larger unchanged span. Optional snippets may be omitted when redaction is needed;
do not present sanitized/reconstructed snippets as verbatim native definitions.
Keep interpretations in explanations, native data exact and original file hashes.

Supplied line bounds are one-based inclusive physical lines. A final newline
terminates the last line; it does not create an extra evidence line at EOF.
Check every range against the same hashed input and the source supporting its
assertion. Optional bounds may be omitted if not established. This generic local
check uses an already inspected declaration's byte offsets; it executes no source:

```python
import hashlib
import pathlib

raw = pathlib.Path(source_path).read_bytes()
assert hashlib.sha256(raw).hexdigest() == source_hash
assert 0 <= start < stop <= len(raw)  # stop is exclusive
assert native_text.encode("utf-8") == raw[start:stop]
start_line = raw[:start].count(b"\n") + 1
end_line = raw[:stop - 1].count(b"\n") + 1
physical_lines = raw.count(b"\n") + (not raw.endswith(b"\n"))
assert 1 <= start_line <= end_line <= physical_lines
```

Avoid newline-converting reads or strip()
when producing native text. Byte membership alone cannot prove a full declaration:
an inner semicolon or nested END may terminate a matching prefix. Inspect the
actual outer closing delimiter and surrounding original physical lines independently
of the extractor, retain the full body even when analysis is unsupported, and
disclose gaps instead of truncating. Do not execute SQL/bodies to find it.

Keep this ledger and capture for the candidate-bound local audit after ready
preview below. That audit is a required transition before the first commit send.
Historical retained proof stays at its original snapshot; do not recheck it as
current source or assign it new capture hashes.

## Begin a durable session

`begin_backend_import` takes this input (projectId is the MCP path argument):

```text
{projectId,expectedVersion,baseRevisionId,idempotencyKey,
 mode?:"initial"|"reconcile",profile?:"foundation-graph-v1"|"relational-graph-v1"|"runtime-flow-v1",
 profileExtension?,repositoryId?,graphScope?,manifest,inventory}
```

Read project and immutable base first. initial (also omitted mode) requires an
unsourced base and rejects repositoryId/graphScope/profileExtension. Omitted
profile means foundation. Reconcile requires its sole repositoryId, exact provider
compatibility except the explicit permitted extension below, and graphScope:
`{profile:selectedProfile,status:"complete"|"partial",gaps:[]}`.
complete scope has no gaps; partial scope lists them. This is whole snapshot
reconciliation, not incremental import. Save the full begin input before sending.
The returned `id` is importId for subsequent calls; also save repositoryId,
snapshotId and session version. Begin leaves project head/metadata unchanged.

## Commands and identity order

Each command has op and exactly one matching addressed record:

```text
{op:"upsert_node",node:{externalKey,kind,name,parentKey?,attributes,evidenceKeys}}
{op:"upsert_edge",edge:{externalKey,kind,fromKey,toKey,attributes,evidenceKeys}}
{op:"upsert_evidence",evidence:{externalKey,subjectType:"node"|"edge",subjectKey,
 propertyPath?,method,status,source,explanation,snippet?}}
source: {repositoryId,snapshotId,file,contentHash,symbol?,startLine?,endLine?}
{op:"map_identity",identity:{recordType:"node"|"edge",fromExternalKey,toExternalKey,
 expectedId,reason,evidenceKeys}}
{op:"delete_assertion",deletion:{recordType:"node"|"edge",externalKey,expectedId,reason}}
{op:"remove",remove:{recordType:"node"|"edge"|"evidence",externalKey}}
```

Keep active keys. For a changed key, read expectedId at the pinned base and send
a separate map_identity batch before allocating/upserting toExternalKey. Mapping
needs distinct keys, reason and current evidenceKeys. Reassert the mapped subject
and those current-snapshot evidence records in subsequent batches. No automatic
name match, split/merge, kind change or deleted-key reuse. A prior acknowledged
target allocation may conflict; never repair it by inventing a UUID.

Evidence's repository/snapshot must match begin; its file/hash must match an
analyzed manifest entry. Both line bounds are present together, positive and
ordered. Subject references and evidence membership must agree. Forward keys are
allowed in staging, but preview must resolve them. Known node/edge bundles need
source evidence; unresolved_target may use its required explanation. Foundation-selected updated
subject evidence replaces old membership. Relational supplied facets replace only
their facetKeys and retain omitted stale facets plus the needed proof union; an
evidence-only update is invalid.
Omitted bundles stay stale with historical provenance. remove affects staging,
not committed base deletion. Missing observations never delete, even complete scope.
An ordinary-metadata upsert omitting datastore.relational or
symbol.databaseRoutine retains all base facets stale. The server restores their
required old proof IDs alongside new metadata evidence; verify the final union.
Never restage retained proof with a new snapshot.

## Hash, send and retain batch receipts

Size `put_backend_import_batch.commands` by `limits.maxImportBatchCommands`;
size its serialized payload by `limits.maxImportBatchBytes` and any smaller
global maxBodyBytes. Current import limits are500 commands and1MiB.
`limits.maxCommands` limits metadata patches; a value1 there does not reduce
the import batch limit. If maxCommands is1 and maxImportBatchCommands is500,
send related upserts together up to500 within byte limits and ordering rules.
An import limit1 requires singleton batches. Keep map_identity in a separate
batch before an allocating upsert. Check the actual delivered
import limits on resume; keep accepted batch order and exact replay inputs.
Hash the exact submitted commands
using compact, recursively sorted-key UTF-8 JSON without filling optional fields:

```python
payload = json.dumps(commands, ensure_ascii=False, sort_keys=True,
                     separators=(",", ":")).encode("utf-8")
payload_hash = hashlib.sha256(payload).hexdigest()
```

Keep HTML characters and U+2028/U+2029 as UTF-8; absent and null differ. Preserve
integer version/count values without floating-point conversion. Send
`put_backend_import_batch {projectId,importId,batchId,expectedImportVersion,payloadHash,commands}`.
Save the entire input, receipt, acceptedVersion and returned UUID mappings before
continuing. Retry exactly, including original expectedImportVersion. Corrections
use a new batchId and explicit upsert/remove. Each new accepted batch invalidates
the previous preview.

Reuse the saved serialized payloads, source capture and findings for hashing and
audit. Refer to their retained bytes and digests instead of repeatedly embedding
complete payloads in shell arguments. A local file is optional; captured command
output remains valid when all underlying bytes are inspectable.

## Explicit preview, local audit and commit

`preview_backend_import {projectId,importId,expectedImportVersion,baseRevisionId}`
is a mutation; call it explicitly. needs_resolution with null candidateHash
requires a repair batch and fresh preview. ready returns candidateHash and new
session version; keep both. Partial inventory/unknown targets may commit with
visible partial coverage. graphScope partial or any stale record keeps coverage
partial. Historical evidence remains pinned.

Read saved pages via
`get_backend_import_changes {projectId,importId,previewVersion,recordType,limit?,cursor?}`,
where recordType is source/identity/deletion. Use the saved preview version and
hash; cursors bind both. New batches invalidate them. Source absence is confirmed
only with complete files inventory and verified consistency. Omitted partial
source paths remain unknown.

### Local audit gate

Before the first commit send, read recovery and prospective project CAS metadata,
then save a passing local audit record for this exact ready candidate. The record
is private workflow evidence, not an MCP argument, model field, server attestation
or new API. Saved structured command output can be the artifact when its full
underlying bytes and checked findings remain inspectable; a tempfile is optional.
Do not send it to the server. Hashes, ledger counts, source rereads, regenerated extractor output
or a pass label alone are not an audit. No ready preview, coverage result or
postcommit read substitutes for the independent checks and findings below.

Bind at least these local facts; retain their underlying saved bytes:

| Local record group | Exact binding |
|---|---|
| Capture | Captured snapshot/manifest digest, normalized path/original-file-hash roster and its digest, consistency check and source ledger digest. |
| Session | Saved full begin input and its digest; projectId/importId/repositoryId/snapshotId, provider/profile and mode. |
| Accepted batches | In accepted order: each saved serialized full submitted input/digest, batchId, payloadHash, original expectedImportVersion, receipt/digest, acceptedVersion and returned UUID/identity mappings. |
| Ready preview | Saved preview input/result and digests, candidateHash, returned previewVersion/session version, baseRevisionId and prospective project expectedVersion. |
| Audit result | Independent per-declaration/current-claim typed scalar/expression comparisons and native/proof/scope findings, full ledger accounting and a passing conclusion with no unresolved inaccurate claims. |

Compact private record shape (illustrative, not a required wire schema): replace
every bracketed string with an observed binding or concrete checked finding.
The integers illustrate field types; copy actual original integer tokens and
returned versions rather than assuming the example sequence. Repeat findings
for all ledger declarations/current claims; an aggregate count or pass flag
alone is insufficient. Digests reference full retained underlying bytes.

```json
{
  "result": "pass",
  "capture": {"snapshotDigest": "<digest>", "rosterDigest": "<digest>", "ledgerDigest": "<digest>", "consistency": "<checked finding>"},
  "session": {"beginInputDigest": "<digest>", "projectId": "<id>", "importId": "<id>", "repositoryId": "<id>", "snapshotId": "<id>", "provider": "<provider>", "profile": "<profile>", "mode": "<mode>"},
  "accepted": [{"inputDigest": "<digest>", "batchId": "<id>", "payloadHash": "<hash>", "expectedImportVersion": 1, "receiptDigest": "<digest>", "acceptedVersion": 2, "identitiesDigest": "<digest>"}],
  "ready": {"inputDigest": "<digest>", "resultDigest": "<digest>", "candidateHash": "<hash>", "previewVersion": 3, "baseRevisionId": "<id>", "expectedVersion": 1},
  "findings": [{"subjectKey": "<key>", "facetKey": "<key>", "sourceSpan": "<file/hash/physical range>", "nativeBoundary": "<full submitted unit and actual outer delimiter finding>", "proofs": "<exact bytes/ranges/membership finding>", "knownUnknown": "<independent typed scalar/expression values, original-source boundaries and complete nested delimiters; known/unknown scope findings>", "bodyDependencies": "<checked scope and gaps>", "migrationInventory": "<ledger accounting, derivation and gaps>"}]
}
```

The executor records actual independent findings before a passing conclusion;
copying this shape or printing its pass label cannot satisfy the gate.

Perform the audit in this order:

1. Reopen the actual saved serialized submitted inputs and accepted receipts.
   Recompute each payloadHash from those saved commands with the hash rules above;
   preserve exact integer tokens, optional fields and absent versus null. Check
   accepted order/versions, UUID/identity mappings and the session/ready-preview
   bindings. Account for upsert replacement/removal and retained base claims when
   identifying the current candidate assertions. Do not regenerate payloads or
   rerun the same extractor to prove its own submitted result correct.
2. For each current native unit, independently open the captured original source
   around its beginning and actual outer closing delimiter, including surrounding
   physical lines. Compare the submitted full definition to that complete unit,
   including nested bodies and final delimiters. A prefix ending at an inner END
   or semicolon cannot pass even when its substring/hash/range checks succeed.
   Then check original file hashes, unchanged bytes, inclusive physical proof
   ranges, subject/facet evidence membership and every current supporting span.
   Separately compare every asserted typed scalar and expression in the actual
   current submitted candidate to its captured original source. Check complete
   expression boundaries and nested delimiters as well as names, ordered values,
   modifiers and known/unknown wrappers. A complete native definition or valid
   proof hash does not establish the extracted typed value. Unsupported extraction
   must be repaired or represented as unknown with its concrete analysis gap
   before publication, while retaining full native text. Fully inspected source
   scope may support declaration-scoped absence; physical/runtime guarantees
   require supporting proof and cannot be invented from incomplete scope.
3. Account for every discovered declaration/change in the saved ledger against
   current assertions. Check declared known/unknown values, body/dependency scope,
   migration order/handled changes/derived state and inventory/facet gaps. Retain
   complete native text for unsupported analysis. A concrete honest partial scope
   can pass; an inaccurate native body, unaccounted declaration or unsupported
   completeness claim cannot. Historical proof remains bound to its old snapshot.
4. Recheck captured input hashes and the complete file roster, the exact accepted
   payload/receipt/preview tuple and prospective CAS/base. Retain full concrete
   findings and inspected underlying bytes. Complete saved command argv/output may
   carry the record; no file, single event, key name or artifact layout is required.
5. Conclude the audit. Once the independent checks support a passing result, save
   or print an overall statement that the source audit is complete and passes for
   this bound final tuple, with no inaccurate claims or unaccounted declarations.
   Reference retained bindings and findings; they may span several saved events.
   Individual passing findings, server-ready status and printed digests do not
   themselves communicate this completed result. Ordinary wording is sufficient;
   no literal or JSON key is required. Later serialization of unchanged inspected
   data does not invalidate a completed conclusion. Otherwise keep it uncommitted.

For example, after completing the independent checks, save or print this final
conclusion with actual values and references to the retained bindings/findings:

```text
Independent source audit completed and passed for the saved final bindings:
projectId=<actual>, importId=<actual>, candidateHash=<actual>,
previewVersion=<actual>, baseRevisionId=<actual>, expectedVersion=<actual>.
All current typed values, native units, proofs and ledger scopes were checked;
no inaccurate or unaccounted claims remain. Honest scoped gaps are retained.
```

This wording is illustrative. Produce the conclusion only when the retained
concrete findings establish it; printing a statement cannot replace those checks.

Any changed input, batch, accepted mapping/version, preview, candidate/base/CAS,
source bytes/roster or ledger invalidates the record. For an unchanged captured
begin/source manifest, repair with a new batch in this same uncommitted session,
obtain ready preview and repeat the independent audit. Source drift must first
be resolved and reanalyzed; if it requires changing immutable begin manifest or
profile, follow existing explicit abort/new-compatible-session rules. Do not
relabel captured source or silently change a session. Never use a published first
revision as the audit probe or a corrective second commit as its substitute.

### Commit and receipt recovery

**Final checkpoint before first commit:**

- Independent source comparisons and ledger accounting are complete; unresolved
  inaccurate claims have been repaired in staging.
- Saved findings are bound to the final accepted inputs/receipts, ready candidate,
  source roster, base and project CAS.
- An explicit overall completed passing conclusion is recorded and read back.
  Individual passing findings still require this final conclusion.
- These bindings still match immediately before sending the saved commit input.

Immediately before first send, read back the saved audit and require its concrete
findings and overall completed passing conclusion. If the conclusion is missing,
finish the audit while still uncommitted. Reread project metadata and require the
saved audit's exact bindings still match. Preserve the full
`commit_backend_import {projectId,importId,expectedVersion,expectedImportVersion,candidateHash,idempotencyKey}`
input. The session version/hash are from ready preview; project head is its base.
Metadata CAS or changed head has the recovery branches, not blind version
substitution. A changed sourced head needs comparison and explicit preview
against the chosen new base. Mapping aliases/expectedId and deletion proof are
revalidated while acknowledged UUIDs stay stable. Refresh the local audit binding
after changed metadata/base/preview before first sending the new commit input.

The audit gate authorizes the first send. After a lost/uncertain commit response,
recover by replaying the identical original complete input/CAS/key before any
new work, even if current source, head or audit has changed. Receipt recovery is
not a second publication and does not need a new session, audit or substituted
request. Resolve that original receipt first, then read its immutable revision.

Commit atomically stores the new immutable revision and moves project head.
Save its project/revision/sessionId and verify pinned graph/evidence/coverage.
An error never licenses overwriting concurrent changes. Abort affects the candidate
only and preserves model, receipts and acknowledged identity reservations.

## Verified deletion gates

Only explicit delete_assertion can remove a base node/edge. Preview requires all:

- Whole-selected-profile graphScope complete without gaps; relational covers the
  combined foundation/relational repository. Matching repository,
  provider/profile and verified stable snapshot.
- Complete files/endpoints/datastores inventory with counts matching manifest
  files and submitted endpoint/datastore contributions.
- Every old evidence path analyzed now or proven absent by complete files
  inventory. Excluded/unsupported/unavailable prior paths forbid deletion.
- Incident edges, parent links and every nested relational reference explicitly
  removed/rebound. FK pairs, index terms, view/routine dependencies and candidate
  migration targets still block deletion when retained. Historical migration pins
  do not require keeping a deleted object active; foreign/unavailable pins fail.
  Stale dangling references still block commit.

Unrelated excluded secrets do not justify or prevent another proven deletion.
Subject deletion removes attached evidence only from the new revision. Historical
revision/evidence bytes remain unchanged. If preview cannot resolve a decision,
repair or remove that staged decision and preview again; never claim complete zero
to bypass proof. A missing object in partial analysis remains stale.

## Explicit one-way profile extension

For relational initial import, explicitly select profile relational-graph-v1 with
provider profiles exactly foundation plus relational. It may publish schema2 over
the empty initial schema1 revision. Foundation initial/reconcile remains schema1
and strict foundation shapes. Existing provider declarations are never silently
extended. A foundation-selected new write against a relational base is rejected
with backend_unsupported_scope (422); old executed identical requests still
recover original receipts before compatibility/CAS. Initial over a sourced base
continues to return backend_reimport_unsupported (409).

To reconcile a foundation-only base into relational schema2, send:

```json
{
  "profile": "relational-graph-v1",
  "profileExtension": {
    "fromProfile": "foundation-graph-v1",
    "toProfile": "relational-graph-v1"
  },
  "graphScope": {
    "profile": "relational-graph-v1",
    "status": "partial",
    "gaps": ["Some scoped sources could not be analyzed"]
  }
}
```

This excerpt is fixture-only and is not a full begin request. Keep the sole
repository UUID/name and exact provider name/version/namespace/method. Retain all
prior profiles and add only relational. No second repository/provider, changed
provider version/method/namespace or downgrade is allowed; the separate explicit
relational→runtime extension is described below.
Repeated extension against an already relational base is invalid; subsequent
relational reconcile omits profileExtension and requires exact compatibility.
Changing a profile on an open session requires a new session, never reinterpretation
of acknowledged IDs/receipts. Failed batch, abort, CAS conflict or rollback must
publish no profile transition. Extension becomes effective only at atomic commit.
Saved session/status echoes profile and includes profileExtension when selected;
ready preview also exposes modelSchemaVersion and the selected extension. Preserve
returned fields and their omission as reviewable decisions. Historical omitted
profile means foundation; original receipts omit new members they never contained.

## Relational facets, proof and reference repairs

Use stable facetKeys for independent source claims, explicit unknown wrappers and
current analyzed proof. Read backend-database-reference through its verified
database owner in this same set for strict fields/ordered references. Never stage
computed freshness/sourceSnapshotId/facetComparison or committed UUID reference
fields where import expects keys. Native strings count toward advertised byte
limits; no truncation is permitted.

If a refreshed SQL facet reuses evidence that an omitted ORM facet still needs,
backend_facet_evidence_conflict blocks preview rather than overwriting ORM proof.
Remove the staged collision with remove(recordType evidence), add a new stable
proof key for this current observation, and re-upsert the subject/facet's evidence
union; or explicitly reassert every dependent facet with current proof. Use a new
batchId, retain acknowledged identities and preview again. Removing a staged
proof does not remove a committed historical proof.
The repair command is `{op:"remove",remove:{recordType:"evidence",externalKey:oldProofKey}}`;
then upsert the subject with the distinct new proof key and upsert that current
evidence. A remove object without op is invalid. Preserve omitted facets and
their original proof; do not replace them with the new source body.

If SQL retargets an FK edge while an omitted ORM facet retains the previous target,
backend_facet_endpoint_conflict blocks preview. Distinct physical target claims
need distinct stable references edges from the constraint; the inspector groups
those edges without rewriting their endpoints. Reasserting every edge facet can
retarget the same edge explicitly. A new edge plus retained stale/proved old edge
deletion is another explicit path. Wrong target column parent/kind still fails.

Migration target is candidate(objectKey), historical(revisionId/objectId) or
source_only(externalKey/expectedKind/qualifiedName/reason). Historical pins are
same-project/repository immutable base/ancestor reads and can describe any
operation. Before deleting a target, reassert retained candidate migration refs
as historical where warranted. source_only covers a logical source target absent
from candidate and available history, including first-import create then drop.
It requires migration proof and allocates no active UUID. Unknown order,
unsupported operations or incomplete dependencies remain gaps; no DDL replay
establishes a derived state. A missing facet/source never retracts a claim.


## Runtime source profile and audit

For initial source3, select profile:"runtime-flow-v1" and provider profiles exactly
foundation-graph-v1/relational-graph-v1/runtime-flow-v1; no repositoryId,
graphScope or profileExtension. To extend a committed schema2 source, use ordinary
same-repository/provider whole-scope reconcile with matching graphScope.profile
and profileExtension:{fromProfile:"relational-graph-v1",toProfile:"runtime-flow-v1"}.
Preserve the exact prior provider name/version/namespace/method and add only the
runtime profile. Later source3 reconcile requires the same exact profile set and
omits extension. Foundation→relational remains available; direct foundation→
runtime, downgrade, automatic provider migration and proposal→source fail.
Schema1/2 history and receipt bytes stay unchanged; omitted old records stay stale
at original snapshots and proofs instead of gaining new UUIDs/current claims.

Select inspect1 in this same global set and verify backend-flow-reference before
staging schema3. Its typed flow/step/query/transaction and control/access/boundary
shapes use external Keys on import and Ids after preview. Keep entry/exits in the
owning flow; local transaction membership does not propagate to callees. Calls
have explicit candidates and unresolved remainder; table/view-level access keeps
unknown columnScope and its reason. Never infer columns from SQL text or native
names. Known runtime records/edges need source proof with analyzed file hash and
paired physical line bounds, and each asserted candidate/outcome has edge proof.

The existing local audit gate applies to every new control/call/access assertion,
expression, native query/body and source span in actual saved accepted payloads.
Audit complete physical source boundaries independently of the extractor against
captured bytes, not reconstructed commands. Keep unknowns for unsupported scope;
ready validation does not establish source truth. A passing final audit remains
bound to accepted batches/receipts and final candidate/preview/base/project CAS
before first publication. Lost/uncertain commit replays its original complete
request/key/CAS first, even after source/head/audit changes.

Normal reconcile retains omitted steps/transitions/accesses stale. Explicit
assertion deletion must clear all surviving parent, entry/exit, transaction,
call and access references plus the usual complete scope/inventory/proof gates.
An authorized focused gap investigation changes source analysis focus, not the
whole-scope import protocol. Unavailable/inconclusive source retains unknowns
and produces a concrete limitation, never an empty progress commit. Requery the
new acknowledged source revision through inspect1; imported witnesses still do
not establish execution, all writers, field lineage, atomicity or impact.
