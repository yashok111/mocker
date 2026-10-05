import type {
  BackendAnalysisJobDetail,
  BackendAnalysisInputContextV1,
  BackendAnalysisResultManifest,
} from "@/api/generated/schemas";
import { backendChangeSchemas } from "./backendChangeSchema";
import {
  changeFixtureValue,
  changeTestDetail,
  changeTestID,
  changeHash,
} from "./backendChangeTestFixtures";
type LegacyDetail = BackendAnalysisJobDetail & { input: BackendAnalysisInputContextV1 };
export function analysisTestDetail(): LegacyDetail {
  const value = changeFixtureValue(
    backendChangeSchemas.BackendAnalysisJobDetail!,
  ) as unknown as LegacyDetail;
  const proposal = changeTestDetail();
  value.job = {
    ...value.job,
    id: changeTestID,
    projectId: changeTestID,
    status: "completed",
    kind: "impact",
    version: 2,
    resultVersion: 2,
    analysisInputHash: changeHash,
    recommendedPollIntervalMs: 2000,
  };
  value.input = {
    ...value.input,
    kind: "impact",
    fromRevisionId: proposal.revision.baseRevisionId,
    target: {
      changeProposal: {
        proposalId: proposal.proposal.id,
        proposalRevisionId: proposal.revision.id,
      },
    },
  };
  value.input.afterPins.effectiveSemanticHash = proposal.revision.semanticHash;
  value.input.afterPins.baseRevisionId = proposal.revision.baseRevisionId;
  value.input.beforeSource.semanticHash = proposal.revision.baseSemanticHash;
  return value;
}
export function analysisTestManifest(): BackendAnalysisResultManifest {
  const value = changeFixtureValue(
    backendChangeSchemas.BackendAnalysisResultManifest!,
  ) as unknown as BackendAnalysisResultManifest;
  return {
    ...value,
    jobId: changeTestID,
    resultVersion: 2,
    analysisInputHash: changeHash,
    semanticResultHash: changeHash,
    complete: true,
    verdict: "incompatible",
    runtimeVerified: false,
  };
}
