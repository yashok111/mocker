# Exact before/after endpoint review

This topic belongs to change4, not the read-only inspect9 workflow. After authorized analysis, verify the selected guideSetId/manifestHash/contentHash and capability backend-endpoint-review. Negotiate backend-analysis-context-v2, backend-analysis-input/v2 and b43-rules/v1 through analysisSupport's additive version arrays; keep b42-traversal/v1. Source/schema compatibility follows the selected immutable source pins.

Resolve exact before and after source revisions, then use their exact HTTP operation UUIDs. Similar route or method names never select identity. An optional full proposal must be a saved exact draft, with both proposalId and proposalRevisionId. A commandPreview or local dirty buffer cannot replace it.

Persist before transport:

```text
start_backend_analysis({projectId,kind:"endpoint_review",
  fromRevisionId,toRevisionId,beforeEndpointId,afterEndpointId,
  changeProposal:{proposalId,proposalRevisionId},
  limits:{},observationMode:"none",idempotencyKey:originalReviewKey})
```

Omit changeProposal entirely when no intent comparison was requested. For an endpoint removed on the after side, retain toRevisionId but send afterEndpointId:null explicitly. Missing afterEndpointId is invalid; null is not another endpoint or an empty result. The before root and witnesses remain visible, and removal uncertainty remains a gap.

Poll get_backend_analysis, explicitly select resultVersion, then page get_backend_analysis_results for changes/findings/witnesses/checks/gaps. Freeze job/version/section/filters/cursors; newer polling never advances the selected result. Retry creates a new job using the same immutable inputs, not source head. Unknown Start/Cancel/Retry replies preserve original body/key; reload never sends automatically. Use change4/backend-analysis-jobs and import8/backend-recovery for the complete recovery rules.

## Read the side-aware findings

endpoint_item distinguishes write, error_branch, emitted_event and affected_consumer, each tagged before/after with exact object, witness and proof. Unchanged writes remain visible. A read never masks a write to the same object. Follow witness steps on their own side; removed paths use before pins. Error self-loops/cycles remain visible without unbounded unfolding. Unknown event delivery stops traversal rather than inventing a consumer path.

endpoint_change shows precise before/after properties. Optional intent supports outside-intent analysis without overriding source proof. endpoint_check distinguishes origin=criterion (exact saved key and required flag) from origin=analysis_rule (required=false, criterionKey=null). Static checks, source coverage, traversal completion, truncation and runtime behavior are separate. behaviorStatus remains unverified; absent errors or complete inventory cannot establish runtime correctness, transactionality or delivery.

## Runnable public SDK example

The real source6 importer fixture includes endpoint/handler/flow/step and an error cycle. It starts ordinary before/after review and explicit-null removal, validates all result pages against public SDK schemas, checks retained before witnesses, and rejects mismatched cursors and unknown result versions. It does not execute imported source.

```sh
mkdir -p /private/tmp/mocker-b43-sdk-endpoint
B43_SDK_ARTIFACT_DIR=/private/tmp/mocker-b43-sdk-endpoint \
GOCACHE=/private/tmp/mocker-b42-analysis-cache go test ./internal/mcp \
  -run '^TestBackendB43SDKEndpointReviewExample$' -count=1 -v
```

Implementation: internal/mcp/tools_backend_b43_examples_test.go. Actual reference transcript: docs/backend-workbench-b43/evidence/public/TestBackendB43SDKEndpointReviewExample.json; reruns emit that filename in the selected artifact directory. Use returned current-run UUIDs only. This is developer protocol evidence, not a live-agent acceptance claim.
