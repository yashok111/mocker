---
name: mocker-backend-verify
description: Run bounded static backend diagnostics and review exact immutable findings with CAS/history. Use when checking imported structure or reviewing a diagnostic; this workflow never runs imported code or verifies runtime behavior.
metadata:
  workflowId: "mocker-backend-verify"
  workflowVersion: "1"
  requiredModelSchemaVersions: "[\"5\",\"6\"]"
  requiredViewSchemaVersions: "[\"backend-diagram-v1\",\"diagram-view-v1\"]"
  requiredCapabilities: "[\"backend-analysis-jobs\",\"backend-analysis-diagnostics\",\"backend-finding-review\"]"
  guideSetId: "sha256:69eae1f24ccca6ea59a0cf125d65ec0dd48d2e6a62f0350cd6fee2d38f532d83"
  manifestHash: "sha256:69eae1f24ccca6ea59a0cf125d65ec0dd48d2e6a62f0350cd6fee2d38f532d83"
---

# Verify static backend structure

Read get_backend_capabilities and negotiate verify1, exact guideSetId, manifestHash and contentHash. Read the complete pinned server backend-verify entrypoint when local identities differ. Do not claim B5 replay or B6 observation support.

1. Read the exact source revision or full changeProposal revision. Diagnosis starts only on explicit user intent, through start_backend_analysis with kind=diagnostics, target, scope={}, limits={}, observationMode=none and a saved idempotencyKey. No source execution occurs.
2. Optionally include diagramScope with an exact pin and nonempty selectors. Use semantic IDs for payload elements; projected C4 relations require architecture_relation plus policy=architecture-v1, level and rootId. Never supply a canvas member subset. A target mismatch is a refusal, not a fallback.
3. Retain returned jobId. Poll get_backend_analysis at the recommended interval until terminal. Select its exact resultVersion. Read get_backend_analysis_results sections findings/checks/gaps; read list_backend_findings {projectId,jobId,resultVersion,limit:100} for immutable occurrences and separate live reviews. Pagination cursor is the last fingerprint.
4. Follow evidence on the report's exact source/proposal target. Incomplete inventory cannot establish unused/absence. N+1 and emit-before-commit are potential structural issues, not measured runtime outcomes. Authored business questions are not source facts.
5. Only on user review intent, call review_backend_finding with fingerprint, expectedVersion, basisHash, status, nonempty reason and saved key. Status is open, accepted_risk, false_positive or resolved. Author is authenticated server context. A human review never satisfies conformance or runtime criteria.
6. Resolved additionally requires resolutionAnalysis {jobId,resultVersion}: a later completed scoped recheck with an explicit sufficiently covered absent check for this fingerprint. Unknown/incomplete checks refuse resolution. New positive evidence reopens the occurrence and preserves history.
7. Lost review reply: replay the identical key and body. Conflict: read current review/history and inspect the changed basis before making a new decision. Never rebase expectedVersion silently. Keep old resultVersion and diagram pins when navigating historical reports.

See [diagnostics](references/diagnostics.md) for prerequisites and limitations. Stop before materialization, portable export, replay or observation workflows; verify1 does not implement them.
