# Durable static diff and impact jobs

This topic belongs to change4. Verify its exact guideSetId/manifestHash/contentHash and all advertised owner requirements. `backend-analysis` remains inspect12's source inspection procedure; shared recovery remains import8-owned. Check capabilities.analysisSupport for supported kinds, targets, observation modes, document/rule/traversal versions before starting. Analysis reads stored graphs, claims, proof and pinned artifact documents. It never executes imported code, SQL, jobs, scripts or a broker.

## Start from immutable input

Persist the entire request and path projectId before `start_backend_analysis`:

```text
{projectId,kind:"diff"|"impact",fromRevisionId,
 target:{revisionId},scope:{},limits:{},observationMode:"none",idempotencyKey}
```

Target is exactly one of `{revisionId}`, `{proposal:{proposalId,proposalRevisionId}}` for a legacy relational proposal, `{changeProposal:{proposalId,proposalRevisionId}}` for a saved full draft, or `{commandPreview:{changeProposal:{proposalId,proposalRevisionId},expectedVersion,commands,candidateHash}}`. Preview requires a freshly valid full-proposal command preview. No importCandidate, mixed target or mutable latest selector is admitted. Source1–6 can be read according to their existing vocabulary; full bases remain source5/6. Empty legacy contentHash may coexist with snapshots. Never invent source-vector or artifact-context fields missing from a legacy document.

scope and limits must be objects. Empty selects bounded defaults; explicit zero limits reject. Scope has changedIds (typed recordType/id), service, kind, certainty, direction and depth; depth0 selects default32. Use full scope for a future ready gate. Limits cannot exceed advertised bounds: 10,000 states, 50,000 dependency visits, depth32, 10,000 findings, 20,000 records, 8 witnesses/object, 32MiB retained result. Body/saved input is bounded by2MiB and server MaxBody, combined decoded graph/owner inputs by256MiB. Nonempty observationPins and observationMode pinned are unsupported; none with omitted/empty pins means runtime unverified.

Start returns a durable job and recommendedPollIntervalMs (currently2000), REST202 even for exact receipt replay. It is not a completed report. A preview job freezes admitted commands/pins server-side; subsequent saved-draft changes cannot retarget it. Locally changed commands, reasons, forms or targets mark its applicability stale while its saved report remains readable. Keep the job's analysisInputHash; do not recompute it from the shortened public context.

## Poll and freeze pages

Use `get_backend_analysis {projectId,jobId}` to read `{job,input}`. input is the immutable `backend-analysis-context-v1` with before/after target, graph/source/vector/artifact pins and normalized scope/limits. Poll at the advertised interval until completed, failed, cancelled or interrupted. `list_backend_analysis {projectId,status?,kind?,limit?,cursor?}` recovers known jobs; preserve its cursor filters.

When job.resultVersion is present, explicitly select it for `get_backend_analysis_results {projectId,jobId,resultVersion,section,limit?,cursor?,service?,kind?,certainty?,direction?,depth?}`. Sections are changes, findings, witnesses, checks, gaps. Default page size100/max500. Cursor binds exact job/version/section and all filters; depth0 here means no filter. Never replace a page's chosen version with newer progress or silently fall back to latest. A newer manifest does not alter published pages. Select its version explicitly to see new output. Follow nextCursor until empty. Inspect before-side deleted dependencies using before pins and after-side desired facts using after pins; do not resolve historical witnesses against source head.

Review manifest.complete, coveredChangedIds versus changedIds, truncationReasons, gaps, sourceCoverageBefore/After, verdict and runtimeVerified separately. Complete traversal can coexist with partial source inventory and an incompatible static verdict. Source coverage is not traversal completion; static criteria/results never prove executed behavior. Runtime criteria remain unverified and runtimeVerified is false.

## Cancel, restart and explicitly retry

`cancel_backend_analysis {projectId,jobId,idempotencyKey}` is a persisted server mutation. Stopping local polling is not cancellation. A winning queued/running cancellation returns cancelled and retains partial published pages; a fresh cancellation of a terminal job returns409 backend_analysis_terminal_conflict. Exact original cancel receipt still replays.

Startup marks previously queued/running jobs interrupted with explicit incomplete manifests; they do not resume automatically. `retry_backend_analysis {projectId,jobId,idempotencyKey}` with a new key creates a new job from the original immutable input for any terminal state, including completed. It never changes the old job/pages. Retrying queued/running returns409 backend_analysis_not_terminal. Replaying the original Start/Retry key returns its original receipt, not a new run. A new retry keeps analysisInputHash; deterministic equivalent terminal results keep semanticResultHash independently of job/time/publication metadata.

At most2 workers and20 waiting jobs are admitted per process. Queue saturation is429 plus Retry-After; retain the request and retry deliberately. Retained project quota (1000 jobs/256MiB including reservations) is409; it does not authorize silent pruning or smaller fragments presented as a complete report. All unknown Start/Retry/Cancel outcomes replay exact saved bodies/keys before current state or quota checks. Browser storage must persist/read back recovery before dispatch. Keep corrupt/unavailable storage, pending unknown outcome and accepted-cleanup-failure visible and distinct; reload never automatically sends.

## Associate an exact report with ready

Only an authorized saved full draft can become ready. Run impact from that draft's exact base to `{changeProposal:{proposalId,proposalRevisionId}}`. The selected job must be completed with complete traversal, every typed changed object covered and no truncation. A source/legacy/commandPreview report cannot gate ready. A filter that omitted changes fails even if complete is true. A partial source inventory may qualify with all explicit gap acknowledgements; incompatible static verdict alone does not block ready.

Persist before sending `apply_backend_change_proposal_lifecycle`:

```text
{projectId,proposalId,expectedVersion,proposalRevisionId,action:"ready",
 report:{jobId,resultVersion,inputHash:job.analysisInputHash,
         resultHash:manifest.semanticResultHash},
 acknowledgedGapIds:[every exact manifest gap ID],idempotencyKey}
```

Use the complete sorted unique gap set; unknown, duplicate or missing IDs reject. Never submit a client pass/runtime flag. Readiness increments aggregate version and records the exact report association without changing draft ID/hash. New-key ready while already ready is409; original exact key replays. Normal Apply/Restore/Rebase resets current status to draft and clears current association; history/report/receipts remain unchanged after restart. Implemented/archive/unarchive use change4/backend-change-handoff. Live runtime collection remains unsupported.

Executable developer examples live in `internal/mcp/tools_backend_b42_examples_test.go` and use real SDK/admin operations with inert source data. They are not ordinary or live-agent acceptance evidence.

New package/conformance/endpoint_review jobs use closed context-v2 payloads and b43-rules/v1; legacy diff/impact retains context-v1. Read backend-change-handoff and backend-endpoint-review under change4 for exact start fields. The five physical result sections and explicit immutable resultVersion paging remain shared.
