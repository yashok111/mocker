# Package, structural conformance and lifecycle handoff

This procedure is change3-owned. Negotiate that exact workflow in the selected immutable guide set, then verify this topic's owner/contentHash. Inspect8 remains read-only and routes authorized analysis here. Shared recovery remains import7-owned. Require backend-change-package, backend-conformance, backend-change-implemented, backend-change-archive and backend-change-unarchive. Endpoint review additionally uses backend-endpoint-review.

Check analysisSupport.kinds for change_package/conformance/endpoint_review, documentVersions for backend-analysis-context-v2, inputDocumentVersions for backend-analysis-input/v2 and ruleSetVersions for b43-rules/v1. Legacy singular documentVersion/ruleSetVersion remain backend-analysis-context-v1/b42-rules/v1; traversalVersion remains b42-traversal/v1. The arrays are additive negotiation, not replacement fields. No new route/tool or execution capability is implied.

## Package an exact saved draft

Read get_backend_change_proposal, retain projectId, proposalId, exact proposalRevisionId, draft hash and aggregate version. Save all local commands first; commandPreview cannot substitute. Persist the entire request before transport:

```text
start_backend_analysis({projectId,kind:"change_package",
  changeProposal:{proposalId,proposalRevisionId},limits:{},
  observationMode:"none",idempotencyKey:originalPackageKey})
```

The job resolves the base from that immutable draft. Poll get_backend_analysis; explicitly select resultVersion and page every physical section with get_backend_analysis_results. Preserve job.analysisInputHash and manifest.semanticResultHash. New progress never changes the selected publication. Package header contains exact base/draft/source/artifact pins, evidence document pins, completeness and packageHash. Changes, baseline/intent evidence, authored criteria and recommended rules are separate typed records. Recommended rules have origin=analysis_rule and required=false; they do not become authored acceptance criteria. Package hash excludes job/time/chunk/publication metadata and does not contain itself. Incomplete output stays incomplete.

## Compare with the implemented source

Import the result separately under the negotiated source workflow; never execute its application, SQL, jobs or broker. Select its exact immutable resultRevisionId. Conformance accepts only the saved full proposal:

```text
start_backend_analysis({projectId,kind:"conformance",
  changeProposal:{proposalId,proposalRevisionId},resultRevisionId,
  identityMap:[{proposalNodeId,sourceNodeId,reason:"Exact importer identity association"}],
  testAttachments:[{criterionKey,attachment:exactSavedCriterionAttachment}],
  limits:{},observationMode:"none",idempotencyKey:originalConformanceKey})
```

identityMap and testAttachments are required arrays; [] is valid. Map new desired nodes explicitly, unique in each UUID column, with nonblank reason. Retained source IDs keep exact identity. Names, routes, paths and kind similarity do not establish identity. A created relationship uses its exact kind and explicitly mapped endpoints; ambiguous parallel edges remain unverified. Mapping never modifies source or proposal.

A supplied test attachment must canonically equal the saved criterion's full locator. Source locators include exact revision/repository/snapshot/file/contentHash and physical line range, plus symbol when present. Artifact locators pin design_scenario id/revision/contentHash and exact jsonPointer. The attachment may intentionally belong to a historical revision different from resultRevisionId: do not rewrite it. Missing association is unverified, unequal locator violated, matching verified locator satisfied structurally. None proves test execution.

Read every conformance_criterion row: criterionKey/kind, required, outcome=satisfied|violated|unverified, reason, exact proposal/source addresses and proof. Positive object_absent requires an immutable confirmed deletion decision plus complete corresponding owner scope; missing graph ID or overall complete coverage is insufficient. Native unsupported semantics, unknown identity, stale proof and unavailable attachments stay unverified. runtime_check is always unverified. outside_intent_change records include unrelated properties on the same object, not just other objects.

## Record implementation without claiming runtime success

First satisfy the separate existing impact/ready gate from backend-analysis-jobs. Implemented is allowed only from ready, with the current exact saved draft/version and a completed, complete, untruncated conformance report. Verify the whole criterion roster, exact report/input/result/source hashes and supported versions. Every required criterion must be satisfied. A required violated or unverified criterion, including runtime_check, blocks implementation even if annotated.

```text
apply_backend_change_proposal_lifecycle({projectId,proposalId,
  action:"implemented",expectedVersion:currentProposal.version,proposalRevisionId,
  report:{jobId,resultVersion,inputHash:job.analysisInputHash,
          resultHash:manifest.semanticResultHash},resultRevisionId,
  exceptions:[{criterionKey:"optional-runtime",author:"Reviewer",reason:"Execution pending"}],
  idempotencyKey:originalImplementedKey})
```

exceptions is required (use []); keys are unique known saved criteria. Exceptions are manual annotations, never converted to pass and never a bypass. Server associations sort by criterionKey: compare normalized arrays while preserving the original request body/key for replay. implementedReference pins report, draft and result source; behaviorStatus stays unverified even with zero runtime criteria. This action does not deploy or execute anything.

Archive accepts draft, ready or implemented; unarchive accepts archived and returns draft:

```text
apply_backend_change_proposal_lifecycle({projectId,proposalId,
  action:"archive",expectedVersion:currentProposal.version,proposalRevisionId,
  idempotencyKey:originalArchiveKey})
apply_backend_change_proposal_lifecycle({projectId,proposalId,
  action:"unarchive",expectedVersion:archivedProposal.version,proposalRevisionId,
  idempotencyKey:originalUnarchiveKey})
```

No report, exceptions or gap fields belong to these arms. Archive retains associations; unarchive clears active ready/implemented references. Implemented/archived drafts refuse Apply/Restore/Rebase. List proposals with status=implemented or archived to rediscover them. Existing public history lists immutable draft revisions, not every lifecycle transition. Stored lifecycle events preserve old associations internally; old reports remain readable through analysis jobs. Never claim the revision list is an event feed.

## Durable recovery and executable SDK evidence

Persist and read back exact owner/path IDs, action, original body/key and acceptance pins before sending. Validate response owner, draft/hash, version+1, target status, exact report/source and normalized exceptions before clearing. Unknown replies preserve body/key; replay exact bytes before current-state/CAS checks. A definitive409 stays a conflict until explicit reconciliation; do not silently retry with a fresh key. Accepted cleanup failure stays accepted and must not offer resend. Browser reload sends nothing. Historical v1 change recovery and v2 analysis records retain their action meaning. See import7/backend-recovery for shared rules and change3/backend-analysis-jobs for job polling/cancellation.

Run the actual public SDK example from repository root. It creates fresh inert source data, polls real workers, validates actual SDK schemas/pages, executes impact→ready→implemented→archive→unarchive and replays the old implemented receipt:

```sh
mkdir -p /private/tmp/mocker-b43-sdk-handoff
B43_SDK_ARTIFACT_DIR=/private/tmp/mocker-b43-sdk-handoff \
GOCACHE=/private/tmp/mocker-b42-analysis-cache go test ./internal/mcp \
  -run '^TestBackendB43SDKPublicWorkflowExample$' -count=1 -v
```

Implementation: internal/mcp/tools_backend_b43_examples_test.go. The generated TestBackendB43SDKPublicWorkflowExample.json contains real request/response bytes. This run's reference transcript is docs/backend-workbench-b43/evidence/public/TestBackendB43SDKPublicWorkflowExample.json. Fresh runs obtain fresh UUIDs; old transcript IDs are not inputs to another server. Developer protocol evidence is not ordinary/live-agent acceptance.
