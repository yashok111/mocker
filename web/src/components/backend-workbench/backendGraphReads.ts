import {
  getBackendAssertions,
  getBackendChangeProposalAssertions,
  getBackendChangeProposalCoverage,
  getBackendChangeProposalEvidence,
  getBackendChangeProposalNode,
  getBackendCoverage,
  getBackendEvidence,
  getBackendImportCandidateAssertions,
  getBackendImportCandidateCoverage,
  getBackendImportCandidateEvidence,
  getBackendImportCandidateNode,
  getBackendNode,
  getBackendProposalCoverage,
  getBackendProposalEvidence,
  getBackendProposalNode,
  queryBackendGraph,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendAssertionsPage,
  BackendCoverageResponse,
  BackendEffectiveGraphPins,
  BackendEvidenceResponse,
  BackendGraphResponse,
  BackendNodeResponse,
  BackendReadTarget,
  GetBackendAssertionsParams,
  GetBackendEvidenceParams,
  QueryBackendGraphRequest,
} from "@/api/generated/schemas";
import { BackendReadError, backendReadTargetKey, checkBackendReadPins } from "./backendReadTargets";

export type { BackendReadTarget } from "@/api/generated/schemas";
export { backendReadTargetKey } from "./backendReadTargets";

export type BackendGraphFilters = {
  serviceId?: string;
  sourceSnapshotId?: string;
  certainty?: "explicit" | "inferred" | "unresolved" | "desired" | "stale";
  id?: string;
  kind?: string;
  limit?: number;
  cursor?: string;
} & (
  | { recordType: "nodes"; search?: string; parentId?: string; from?: never; to?: never }
  | { recordType: "edges"; from?: string; to?: string; search?: never; parentId?: never }
);

export function backendReadQueryKey(
  resource: string,
  projectId: string,
  target: BackendReadTarget,
  query?: unknown,
) {
  return ["backend-exact", resource, projectId, backendReadTargetKey(target), query ?? null];
}

function start(target: BackendReadTarget, signal: AbortSignal) {
  signal.throwIfAborted();
  backendReadTargetKey(target);
}

function readFailure(): Error {
  return new BackendReadError("Не удалось прочитать выбранный граф.");
}

export async function readBackendGraph(
  projectId: string,
  target: BackendReadTarget,
  query: BackendGraphFilters,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendGraphResponse> {
  start(target, signal);
  const input = { ...target, ...query } as QueryBackendGraphRequest;
  const response = await queryBackendGraph(projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw readFailure();
  checkBackendReadPins(response.data, target, expected);
  const page = response.data;
  if (
    (query.recordType === "nodes" && page.edges.length > 0) ||
    (query.recordType === "edges" && page.nodes.length > 0) ||
    page.nodes.some(
      (node) =>
        (query.id && node.id !== query.id) ||
        (query.kind && node.kind !== query.kind) ||
        (query.parentId && node.parentId !== query.parentId),
    ) ||
    page.edges.some(
      (edge) =>
        (query.id && edge.id !== query.id) ||
        (query.kind && edge.kind !== query.kind) ||
        (query.from && edge.from !== query.from) ||
        (query.to && edge.to !== query.to),
    )
  ) {
    throw new BackendReadError("Получены записи другой области графа.");
  }
  return response.data;
}

type NodeResult = Awaited<
  ReturnType<
    | typeof getBackendNode
    | typeof getBackendProposalNode
    | typeof getBackendChangeProposalNode
    | typeof getBackendImportCandidateNode
  >
>;

export async function readBackendNode(
  projectId: string,
  target: BackendReadTarget,
  nodeId: string,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendNodeResponse> {
  start(target, signal);
  let response: NodeResult;
  if ("revisionId" in target && target.revisionId) {
    response = await getBackendNode(projectId, target.revisionId, nodeId, { signal });
  } else if ("proposal" in target && target.proposal) {
    const p = target.proposal;
    response = await getBackendProposalNode(projectId, p.proposalId, p.proposalRevisionId, nodeId, {
      signal,
    });
  } else if ("changeProposal" in target && target.changeProposal) {
    const p = target.changeProposal;
    response = await getBackendChangeProposalNode(
      projectId,
      p.proposalId,
      p.proposalRevisionId,
      nodeId,
      { signal },
    );
  } else if ("importCandidate" in target && target.importCandidate) {
    const c = target.importCandidate;
    response = await getBackendImportCandidateNode(
      projectId,
      c.importId,
      nodeId,
      { importVersion: c.importVersion, candidateHash: c.candidateHash },
      { signal },
    );
  } else {
    throw readFailure();
  }
  signal.throwIfAborted();
  if (response.status !== 200) throw readFailure();
  const value = response.data;
  checkBackendReadPins(value, target, expected);
  const returnedID =
    "node" in value
      ? value.node.id
      : "proposalProjection" in value
        ? value.proposalProjection.id
        : value.id;
  if (returnedID !== nodeId) throw new BackendReadError("Получен другой объект графа.");
  return value;
}

type EvidenceResult = Awaited<
  ReturnType<
    | typeof getBackendEvidence
    | typeof getBackendProposalEvidence
    | typeof getBackendChangeProposalEvidence
    | typeof getBackendImportCandidateEvidence
  >
>;

export async function readBackendEvidence(
  projectId: string,
  target: BackendReadTarget,
  query: GetBackendEvidenceParams,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendEvidenceResponse> {
  start(target, signal);
  let response: EvidenceResult;
  if ("revisionId" in target && target.revisionId) {
    response = await getBackendEvidence(projectId, target.revisionId, query, { signal });
  } else if ("proposal" in target && target.proposal) {
    const p = target.proposal;
    response = await getBackendProposalEvidence(
      projectId,
      p.proposalId,
      p.proposalRevisionId,
      query,
      { signal },
    );
  } else if ("changeProposal" in target && target.changeProposal) {
    const p = target.changeProposal;
    response = await getBackendChangeProposalEvidence(
      projectId,
      p.proposalId,
      p.proposalRevisionId,
      query,
      { signal },
    );
  } else if ("importCandidate" in target && target.importCandidate) {
    const c = target.importCandidate;
    response = await getBackendImportCandidateEvidence(
      projectId,
      c.importId,
      { ...query, importVersion: c.importVersion, candidateHash: c.candidateHash },
      { signal },
    );
  } else {
    throw readFailure();
  }
  signal.throwIfAborted();
  if (response.status !== 200) throw readFailure();
  checkBackendReadPins(response.data, target, expected);
  if (
    response.data.items.some(
      (item) =>
        (query.subjectId && item.subjectId !== query.subjectId) ||
        (query.evidenceId && item.id !== query.evidenceId),
    )
  ) {
    throw new BackendReadError("Получены основания другого объекта.");
  }
  const page = response.data;
  if ("source" in page && page.source) {
    for (const basis of page.source.legacyProofBases) {
      const proof = page.items.find((item) => item.id === basis.evidenceId);
      if (
        !proof ||
        basis.projectId !== projectId ||
        basis.recordId !== proof.subjectId ||
        basis.documentVersion !== "legacy-proof-basis-v1" ||
        basis.sourceSchemaVersion !== "5"
      ) {
        throw new BackendReadError(
          "Историческое свидетельство не соответствует выбранному основанию.",
        );
      }
    }
  }
  if ("baselineEvidence" in page && page.baselineEvidence) {
    for (const basis of page.baselineEvidence) {
      const proof = page.items.find((item) => item.id === basis.evidenceId);
      if (
        !proof ||
        basis.subjectId !== proof.subjectId ||
        basis.revisionId !== page.pins.baseRevisionId ||
        basis.semanticHash !== page.pins.baseSemanticHash
      ) {
        throw new BackendReadError("Основание относится к другой базовой ревизии.");
      }
    }
  }
  return response.data;
}

type CoverageResult = Awaited<
  ReturnType<
    | typeof getBackendCoverage
    | typeof getBackendProposalCoverage
    | typeof getBackendChangeProposalCoverage
    | typeof getBackendImportCandidateCoverage
  >
>;

export async function readBackendCoverage(
  projectId: string,
  target: BackendReadTarget,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendCoverageResponse> {
  start(target, signal);
  let response: CoverageResult;
  if ("revisionId" in target && target.revisionId) {
    response = await getBackendCoverage(projectId, target.revisionId, { signal });
  } else if ("proposal" in target && target.proposal) {
    const p = target.proposal;
    response = await getBackendProposalCoverage(projectId, p.proposalId, p.proposalRevisionId, {
      signal,
    });
  } else if ("changeProposal" in target && target.changeProposal) {
    const p = target.changeProposal;
    response = await getBackendChangeProposalCoverage(
      projectId,
      p.proposalId,
      p.proposalRevisionId,
      { signal },
    );
  } else if ("importCandidate" in target && target.importCandidate) {
    const c = target.importCandidate;
    response = await getBackendImportCandidateCoverage(
      projectId,
      c.importId,
      { importVersion: c.importVersion, candidateHash: c.candidateHash },
      { signal },
    );
  } else {
    throw readFailure();
  }
  signal.throwIfAborted();
  if (response.status !== 200) throw readFailure();
  checkBackendReadPins(response.data, target, expected);
  return response.data;
}

type AssertionsResult = Awaited<
  ReturnType<
    | typeof getBackendAssertions
    | typeof getBackendChangeProposalAssertions
    | typeof getBackendImportCandidateAssertions
  >
>;

export async function readBackendAssertions(
  projectId: string,
  target: BackendReadTarget,
  query: GetBackendAssertionsParams,
  signal: AbortSignal,
  expected?: BackendEffectiveGraphPins,
): Promise<BackendAssertionsPage> {
  start(target, signal);
  let response: AssertionsResult;
  if ("revisionId" in target && target.revisionId) {
    response = await getBackendAssertions(projectId, target.revisionId, query, { signal });
  } else if ("changeProposal" in target && target.changeProposal) {
    const p = target.changeProposal;
    response = await getBackendChangeProposalAssertions(
      projectId,
      p.proposalId,
      p.proposalRevisionId,
      query,
      { signal },
    );
  } else if ("importCandidate" in target && target.importCandidate) {
    const c = target.importCandidate;
    response = await getBackendImportCandidateAssertions(
      projectId,
      c.importId,
      { ...query, importVersion: c.importVersion, candidateHash: c.candidateHash },
      { signal },
    );
  } else {
    throw new Error("У этого старого предложения нет отдельных утверждений источника.");
  }
  signal.throwIfAborted();
  if (response.status !== 200) throw readFailure();
  checkBackendReadPins(response.data, target, expected);
  const page = response.data;
  const basis = target.changeProposal
    ? "baseline"
    : target.importCandidate
      ? "candidate"
      : "source";
  if (
    page.documentVersion !== "source-assertions-v1" ||
    page.basis !== basis ||
    page.items.some(
      ({ assertion }) =>
        (query.recordType && assertion.recordType !== query.recordType) ||
        (query.id && assertion.recordId !== query.id) ||
        (query.repositoryId && assertion.owner.repositoryId !== query.repositoryId) ||
        (query.providerNamespace && assertion.owner.providerNamespace !== query.providerNamespace),
    )
  )
    throw new BackendReadError("Получены утверждения другого источника или объекта.");
  return response.data;
}
