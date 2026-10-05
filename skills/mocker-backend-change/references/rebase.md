# Rebase a saved full proposal

This topic belongs to change4. Negotiate `mocker-backend-change` in one exact guideSetId/manifestHash and verify this topic's owner/contentHash. Shared `backend-recovery` remains import8-owned; `backend-analysis` remains inspect12-owned. Rebase changes desired history only, never imported source, evidence, annotations or artifact owner documents.

## Capture B, O and N

B is the selected draft's immutable source base; O is that exact saved desired draft; N is the explicitly selected new source revision in the same project. N need not be project head. Read the proposal/draft and both source revisions before choosing N. Source5→source6 and same-schema bases are supported; source6→source5 is refused as a vocabulary/provenance downgrade. Same-base rebase is allowed and still appends a versioned draft/action/receipt. Empty legacy contentHash does not imply an empty sourceSnapshotIds array; preserve returned source5 vector and legacy artifact-context shape exactly.

`preview_backend_change_proposal_rebase` takes:

```text
{projectId,proposalId,expectedVersion,proposalRevisionId,newBaseRevisionId,
 identityResolutions:[],resolutions:[],repairCommands:[]}
```

The three arrays must be present, even when empty. Preview is read-only and reserves no IDs. Review returned old/new base IDs and semantic hashes, proposal/version/draft, sourcePins, artifactPins, conflicts and diagnostics. A null candidateHash means Apply is unavailable.

## Resolve exact conflicts

Each conflict contains its typed object/selector plus `base`, `ours`, `newSource`. Each value has independent presence and optional value: absent differs from present null, false, empty or zero. Arrays merge as ordered atomic groups. UUIDs, qualified source claims and explicit correspondence establish identity; names and similar shape do not.

For an offered conflict select deliberately:

```text
{conflictId,choice:"keep_proposal",reason:"Reviewed intent must remain"}
{conflictId,choice:"take_source",reason:"Accept the new source value"}
{conflictId,choice:"replace",reason:"Reviewed replacement",value:typedValue}
```

Use only one entry per exact conflictId; stale/unknown/duplicate IDs reject. A replacement matches the conflict's closed typed semantic group. There is no arbitrary JSON path or whole-record replacement. Whole-record repair uses actual typed `repairCommands` with new permanent commandId and nonblank reason. Explicit identity correspondence uses `{oldSourceId,newSourceId,reason}` in identityResolutions, never guessed name matching.

After any choice, reason, replacement, correspondence or repair edit, preview again. The new candidateHash binds exact B/O/N pins, all resolution inputs and ordered repairs; semanticHash alone is not approval. Repair missing dependencies/references without silently removing criteria or their reasons.

If O retains an object deleted by N, its historical identity is `carried_source_identity` with exact qualified `source` and `basis:{revisionId,semanticHash}`. It is neither a new intent_identity nor a current source claim. Preserve all carried providers independently, original proof basis and stable UUID; do not promote historical evidence into N proof. `map_identity` and identity criteria must retain the returned carried tag/basis. Rebase-resolution authorship uses `rebaseResolution:{proposalRevisionId,resolutionId,reason}` instead of a fabricated commandId. Earlier imported/tombstoned IDs remain reserved.

## Apply and recover

Persist the exact preview input plus `candidateHash` and a new stable `idempotencyKey` before `apply_backend_change_proposal_rebase`. Review authorization applies only to those bytes and pins. The receipt appends a new immutable draft on N, increments aggregate version, records accepted repairs and returns status draft, clearing the current ready association. Verify returned base revision/hash/source vector, draft and rebase action against the saved request. A historical receipt does not describe current aggregate state.

For an unknown result, replay the identical path/body/key. After definitive409, retain the refused attempt, reread the proposal owner and choose the current exact draft/base explicitly, then preview fresh. Changing only expectedVersion or transplanting old conflict IDs cannot reconcile intent. Browser recovery waits for explicit retry and refuses storage overwrite/CAS loss. An accepted response with local cleanup failure is still accepted; recover local cleanup without sending a new mutation.
