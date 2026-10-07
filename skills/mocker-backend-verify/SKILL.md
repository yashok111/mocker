---
name: mocker-backend-verify
description: Run bounded static backend diagnostics and review exact immutable findings with CAS/history. Use when checking imported structure or reviewing a diagnostic; this workflow never runs imported code or verifies runtime behavior.
metadata:
  workflowId: "mocker-backend-verify"
  workflowVersion: "4"
  requiredModelSchemaVersions: "[\"5\",\"6\"]"
  requiredViewSchemaVersions: "[\"backend-diagram-v1\",\"diagram-view-v1\",\"backend-observations-v1\"]"
  requiredCapabilities: "[\"backend-analysis-jobs\",\"backend-analysis-diagnostics\",\"backend-finding-review\",\"backend-observations\",\"backend-observation-correlation\",\"backend-scenario-measurements\",\"backend-observed-impact\",\"backend-observed-sequences\"]"
  guideSetId: "sha256:888f13728117c684f3e47ab9c7030d1242dba8370a7438f710faa622ed3bcc33"
  manifestHash: "sha256:888f13728117c684f3e47ab9c7030d1242dba8370a7438f710faa622ed3bcc33"
---

# Verify static backend structure

Read get_backend_capabilities and negotiate verify4, exact guideSetId, manifestHash and contentHash. Read the complete pinned server backend-verify entrypoint when local identities differ. B6.2 evaluates uploaded raw samples and observed event witnesses; collection remains external.

1. Read the exact source revision or full changeProposal revision. Diagnosis starts only on explicit user intent, through start_backend_analysis with kind=diagnostics, target, scope={}, limits={}, observationMode=none and a saved idempotencyKey. No source execution occurs.
2. Optionally include diagramScope with an exact pin and nonempty selectors. Use semantic IDs for payload elements; projected C4 relations require architecture_relation plus policy=architecture-v1, level and rootId. Never supply a canvas member subset. A target mismatch is a refusal, not a fallback.
3. Retain returned jobId. Poll get_backend_analysis at the recommended interval until terminal. Select its exact resultVersion. Read get_backend_analysis_results sections findings/checks/gaps; read list_backend_findings {projectId,jobId,resultVersion,limit:100} for immutable occurrences and separate live reviews. Pagination cursor is the last fingerprint.
4. Follow evidence on the report's exact source/proposal target. Incomplete inventory cannot establish unused/absence. N+1 and emit-before-commit are potential structural issues, not measured runtime outcomes. Authored business questions are not source facts.
5. Only on user review intent, call review_backend_finding with fingerprint, expectedVersion, basisHash, status, nonempty reason and saved key. Status is open, accepted_risk, false_positive or resolved. Author is authenticated server context. A human review never satisfies conformance or runtime criteria.
6. Resolved additionally requires resolutionAnalysis {jobId,resultVersion}: a later completed scoped recheck with an explicit sufficiently covered absent check for this fingerprint. Unknown/incomplete checks refuse resolution. The recheck must analyse a revision of this project that is the occurrence's revision or a newer one (a draft counts as its base revision); an older revision answers 422 backend_finding_recheck_target. Fix first, then recheck the new revision. New positive evidence reopens the occurrence and preserves history.
7. Lost review reply: replay the identical key and body. Conflict: read current review/history and inspect the changed basis before making a new decision. Never rebase expectedVersion silently. Keep old resultVersion and diagram pins when navigating historical reports.

See [diagnostics](references/diagnostics.md) for prerequisites and limitations. For uploaded evidence use [observations](references/backend/observations.md) and [correlation](references/backend/correlation.md). Use [measurements](references/backend/measurements.md) for exact measured jobs and [benchmarks](references/backend/benchmarks.md) for opt-in tools. Stop before B6.3 storage rebuild.

## Measured observations

Read pinned `backend-measurements` and `backend-benchmarks` from the same guide set.
Use exact saved source/observation/correlation/report pins; never enrich from latest.
Observed impact explicitly selects none or pinned. Before source evidence cannot
confirm desired after intent. Unknown builds and inferred paths remain qualified.
