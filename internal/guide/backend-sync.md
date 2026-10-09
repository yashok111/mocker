---
name: mocker-backend-sync
description: Synchronize an explicitly selected backend source repository/provider, preserve qualified claims and proof, review a pinned candidate, and recover exact import requests. Use for composed source6 reconciliation, provider addition/migration or an authorized incremental source update.
metadata:
  workflowId: "mocker-backend-sync"
  workflowVersion: "2"
  requiredModelSchemaVersions: "[\"1\",\"5\",\"6\"]"
  requiredViewSchemaVersions: "[\"import-candidate-v1\"]"
  requiredCapabilities: "[\"backend-projects\",\"backend-revisions\",\"backend-graph-query\",\"backend-source-import\",\"backend-source-sync\",\"backend-source-incremental-sync\",\"backend-source-assertions\",\"backend-import-candidate\",\"backend-representations\",\"backend-analysis-jobs\",\"backend-analysis-diff\",\"backend-analysis-impact\"]"
  guideSetId: "sha256:f6e18cc808ef2fa71d0f8e05fd0fefd3f07819492e99b86082ebec22b7884251"
  manifestHash: "sha256:f6e18cc808ef2fa71d0f8e05fd0fefd3f07819492e99b86082ebec22b7884251"
---

# Synchronize a composed source

For imports or diagrams intended for human understanding, apply the **Human-readable modeling contract** in `backend-interactions` under the selected inspect owner before bulk work. Deliver authored goal/action/outcome scenarios with exact evidence alongside faithful source capture; raw Flow, translated code statements and generated candidates do not satisfy that task. The `mocker-backend-explain` skill orchestrates this editorial work; source-only requests remain source-only.

Update one declared repository/provider scope while retaining the other source partitions, qualified identities and original proof. Source claims describe captured declarations and static dependencies. They do not establish execution, delivery, data safety or runtime impact.
## Select a compatible guide set

Read `get_server_config` and `get_backend_capabilities`. Select advertised `mocker-backend-sync` version `2`, model schema `6`, profile `composed-source-v1`, staged view `import-candidate-v1` and every required capability above. Verify the exact workflowId/version/guideSetId/manifestHash and each needed topic contentHash. If installed text does not match, fetch the complete compatible server entrypoint with `get_guide {topic:"backend-sync",guideSetId:selected.guideSetId}`. Unknown sets fail; never replace one with current text.

All topics use that same global set. Select their actual owner before reading shared material: project2 for `backend-overview`/`backend-annotations`, import8 for `backend-model`, `backend-import-protocol` and `backend-recovery`, inspect13 for source certainty and exact inspection. Selecting a reference owner starts no writes. Older source1–4 bases use import8's existing adjacent transitions before source5; sync does not silently upgrade them. An empty unsourced project can begin composed `add_repository` directly.
## Capture the exact base and selected partition

Read project metadata, its selected immutable revision and coverage. Retain the project version, baseRevisionId, semantic hash, source vector, active partition and snapshot IDs. Select partitions by repositoryId and provider namespace. On a source5 base, retain its exact primary source; the server performs the read-only bootstrap. No externalKey or provider is inferred from a name, URL or first returned claim.

Capture source as inert data. Preserve the import8 rules for normalized relative paths, secret exclusions, before/after file-set and SHA-256 checks, physical spans, native text, the declaration ledger and independent pre-commit audit. The server never reads manifest paths or executes uploaded code. Inventory has all nine categories: files, endpoints, datastores, migrations, producers, consumers, jobs, contracts and tests. State unknown denominators and gaps explicitly.

Every composed Begin has:

```text
projectId, expectedVersion, baseRevisionId, idempotencyKey,
mode:"composed", profile:"composed-source-v1",
sourceScope, scopeStatus, syncPolicy, manifest, inventory
```

Omit legacy top-level `repositoryId` and `graphScope`. `manifest.provider.profiles` contains exactly foundation-graph-v1, relational-graph-v1, runtime-flow-v1, field-lineage-v1, events-service-v1 and composed-source-v1. The descriptor also carries name, version, namespace, method and limitations. Preserve manifest file hashes; never fabricate analyzed files or evidence to make Preview ready.

Select exactly one scope:

| sourceScope | Required members besides kind | Meaning |
| --- | --- | --- |
| `add_repository` | none | Register the incoming manifest's new logical repository; retain all old partitions. |
| `add_provider` | repositoryId | Add a previously absent provider namespace to that repository. |
| `reconcile` | repositoryId, providerNamespace | Replace only this selected provider's observations under the chosen policy. |
| `migrate_provider` | repositoryId, fromProviderNamespace, fromSnapshotId, reason | Introduce a new namespace from this exact active source partition; retain the old partition, claims and proof. |

Mixed scope fields and explicit null are invalid. For existing repositories, manifest.repositoryName matches the selected repository. Reconcile preserves provider name/version/namespace/method and profiles, except the explicitly added composed profile described below. Migration does not choose a winner, erase old proof or authorize name-based identity reuse.

`scopeStatus` is `{status:"complete",gaps:[]}` or `{status:"partial",gaps:[nonblank reasons...]}`. It describes the selected analysis scope, not runtime completeness.

## Negotiate extension per partition

For a source5 base with retained source, explicitly include:

```json
{"profileExtension":{"fromProfile":"events-service-v1","toProfile":"composed-source-v1"}}
```

The head becoming schema6 does not mean every retained provider already has six profiles. A two-step update is valid:

1. From source5, use explicit extension plus `add_repository` and whole-source policy. The new head is source6; the original provider partition can still retain its five-profile descriptor and old snapshot/proof.
2. Read that new vector. Select the retained five-profile partition with `reconcile`, keep its name/version/namespace/method, add exactly composed-source-v1, and include the same explicit extension tag. Use `whole-source-v1`.
3. Verify only the selected new snapshot gains the composed profile; unrelated partitions and old immutable reads remain pinned. Following reconciliation of that now-composed partition omits profileExtension.

An already-composed selected partition rejects a spurious extension. An extension rejects incremental policy. Do not rewrite retained provider descriptors merely because the enclosing revision is schema6.

## Whole and incremental source policies

`whole-source-v1` describes the entire selected partition. Omitted assertions become retained/stale under the source protocol unless explicit, independently supported deletion rules apply. It does not delete another provider's claims.

`incremental-source-v1` is only for reconciliation of an existing composed partition, with no profile extension. It additionally requires:

```text
changeManifest:{
  scope:"affected-subgraph",
  files:[
    {kind:"added",path,afterHash},
    {kind:"modified",path,beforeHash,afterHash},
    {kind:"deleted",path,beforeHash}
  ],
  affectedRoots:[{recordType:"node"|"edge"|"evidence",id}]
}
```

The manifest remains the complete captured incoming file roster. ChangeManifest is the exact hash delta from the selected base snapshot; unchanged, invented, duplicate or mismatched changes are invalid. Roots use exact base UUIDs. Review server-computed affected writes separately from validationDependencies and foreignDependencies. A dependency read is not permission to rewrite that dependency or another partition. Closure-limit refusal calls for a deliberately prepared whole-source request; do not truncate roots, silently widen or restart many smaller scopes and call their union complete.

## Save, stage, resolve and review

1. Persist the complete Begin body/path IDs and stable idempotency key before `begin_backend_import`. Save the returned session `id` as importId, repositoryId, snapshotId and version. Begin does not publish a revision.
2. Persist each `put_backend_import_batch` input: projectId/importId/batchId, original expectedImportVersion, payloadHash and ordered commands. Use the protocol's recursively sorted-key UTF-8 JSON hash, preserving arrays, null/absence and exact numeric tokens. Ordinary JSON insertion order is not a payload hash. Follow advertised batch count/byte limits.
3. Source6 record references use `{localKey}` or `{base:{repositoryId,providerNamespace,recordType,externalKey,expectedId,assertionHash}}`, exclusively. Use them in the exact typed reference fields from the API, including nested references. Evidence keys, subjectKey, opaque port keys and native text remain their own fields; never mechanically replace every `*Key` with a reference.
4. To share an existing UUID, submit `claim_identity` before allocating the incoming external key. Its claimIdentity contains decisionId, recordType, incoming externalKey, exact offered base target, reason and incoming own evidenceKeys. Target has the same repository, record type and kind. Each proof belongs to this session's repository/snapshot and an analyzed manifest file/hash. Another provider's evidence cannot be reassigned.
5. Explicitly call `preview_backend_import` with current expectedImportVersion and the captured baseRevisionId. `needs_resolution` has no readable READY candidate. Page `get_backend_import_changes` with the exact previewVersion; recordType is source, identity, deletion, assertion_conflict, claim_identity or migration.
6. A conflict resolution is `resolve_assertion` with resolution `{decisionId,recordType,id,property,conflictHash,select:{repositoryId,providerNamespace,assertionHash},reason}`. Copy the offered typed property and one offered contender. There is no replacement-value field or arbitrary property path. Keep losing claims/proof visible. A new conflictHash after recomputation requires new explicit review; never retarget a prior approval automatically.
7. A new batch invalidates the prior preview/candidate. Obtain a fresh preview explicitly. Review source changes, retained partitions, identity/claim/migration/deletion decisions, currentness and gaps. Commit only after the import8 independent source audit passes for the final captured source, accepted bytes/receipts and ready tuple.
8. Reread project metadata. `commit_backend_import` uses expectedVersion for project CAS, exact expectedImportVersion, candidateHash and a stable key. An annotation/rename can change project CAS without moving the source head. A changed source base cannot be hidden by updating only CAS: reconcile the intended scope on a new compatible session when the immutable base/vector no longer applies.

For source5 claims, native source assertions are unavailable. An explicit READY bootstrap candidate can provide the exact derived baseline assertions. Do not make incomplete source appear complete to force READY. After staging, only claims provably belonging to the exact baseline provider roster qualify; do not treat the incoming provider's reobserved hash as the old baseline hash.

## Inspect the staged candidate

Use exactly `{importCandidate:{importId,importVersion,candidateHash}}` with graph/node/evidence/coverage/assertion tools. Candidate GETs require that positive decimal version and lowercase SHA-256 once each. The session must still be READY at those pins. New batches, Preview versions and terminal actions can invalidate it; a 409 requires status/review, never fallback to a source head.

Staging supports these basic reads only. Database, Flow, lineage, events, artifact projections and SavedViews reject importCandidate. Staged evidence is not committed history. After Commit, use the immutable revision returned by the receipt for source inspection.

## Recover exact requests

Persist requests before sending, including Begin before its session ID is known. A disconnected/timeout result remains unknown. Repeat only the original body, keys, decision/batch IDs, candidate and CAS. Receipt lookup precedes stale CAS; a successful replay may be historical. Reread live project/session separately before new work.

`list_backend_imports` and `get_backend_import` recover durable session status and accepted batch summaries. A summary does not reconstruct lost command bytes: replay the saved exact batch to recover its original UUID receipt. A known 409 requires explicit reread, intent reconciliation and a new request where appropriate. Preserve the old unknown request until its outcome is established or it is explicitly discarded.

Preview advances the import version and has no idempotency key or replay receipt. After an unknown Preview result, read the durable status/saved preview first. Do not issue another Preview with a guessed newer version or treat a stale-CAS refusal as a successful receipt.

Explicit `abort_backend_import` has its own expectedImportVersion and idempotencyKey. It leaves committed source/history and prior receipts intact. In a browser, restored requests wait for an explicit retry; storage failure must be visible before a new mutation can lose its recovery record.

## Compare the saved source revisions

After commit, retain both immutable revision IDs and select change5's `backend-analysis-jobs` in the same guide set for durable source-to-source diff/impact. Verify that topic's actual owner/hash and the advertised analysisSupport before start. Start takes `fromRevisionId` and `target:{revisionId}`; importCandidate is forbidden. The worker reads stored source evidence only, with observationMode none and runtimeVerified false. Poll the saved job; page one explicitly chosen resultVersion even while newer progress appears. Cancellation is a persisted action, and interrupted work needs an explicit new-key retry.

Source synchronization never rebases a proposal automatically. Select change5's `backend-change-rebase` for explicit new-base selection, reasoned B/O/N resolutions and reviewed repairs. Shared recovery remains import8-owned.
