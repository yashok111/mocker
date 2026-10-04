import { hashBackendJSON } from "./backendImportHash";
import { changeIdentityKey } from "./backendChangeState";
import {
  getBackendChangeProposal,
  listBackendChangeProposals,
  queryBackendGraph,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendChangeProposalDetail,
  BackendEffectiveIdentity,
} from "@/api/generated/schemas";

export function verifyChangeDetail(
  detail: BackendChangeProposalDetail,
  projectId: string,
  proposalId?: string,
  revisionId?: string,
): BackendChangeProposalDetail {
  if (
    detail.proposal.projectId !== projectId ||
    (proposalId && detail.proposal.id !== proposalId) ||
    detail.revision.proposalId !== detail.proposal.id ||
    (revisionId && detail.revision.id !== revisionId) ||
    detail.revision.documentVersion !== "proposal-graph-v1" ||
    !Number.isSafeInteger(detail.proposal.version) ||
    detail.proposal.version < 1
  )
    throw new Error("Ответ не соответствует выбранному предложению или точной ревизии");
  return detail;
}
export async function readChangeProposals(projectId: string, signal?: AbortSignal) {
  const items: BackendChangeProposalDetail["proposal"][] = [];
  const seen = new Set<string>();
  let cursor = "";
  do {
    const response = await listBackendChangeProposals(
      projectId,
      { limit: 500, ...(cursor ? { cursor } : {}) },
      { signal },
    );
    signal?.throwIfAborted();
    if (response.status !== 200 || response.data.items.some((item) => item.projectId !== projectId))
      throw new Error("Не удалось прочитать предложения проекта");
    items.push(...response.data.items);
    cursor = response.data.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("Список предложений неполон");
    seen.add(cursor);
  } while (cursor);
  return items;
}
export async function readChangeProposal(
  projectId: string,
  proposalId: string,
  revisionId?: string,
  signal?: AbortSignal,
) {
  const response = await getBackendChangeProposal(
    projectId,
    proposalId,
    { limit: 500, ...(revisionId ? { proposalRevisionId: revisionId } : {}) },
    { signal },
  );
  signal?.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось прочитать предложение");
  const detail = verifyChangeDetail(response.data, projectId, proposalId, revisionId);
  const history = [...detail.history];
  const seen = new Set<string>();
  let cursor = detail.nextCursor;
  while (cursor) {
    if (seen.has(cursor)) throw new Error("История предложения неполна");
    seen.add(cursor);
    const next = await getBackendChangeProposal(
      projectId,
      proposalId,
      { limit: 500, cursor, proposalRevisionId: detail.revision.id },
      { signal },
    );
    signal?.throwIfAborted();
    if (next.status !== 200) throw new Error("История предложения неполна");
    verifyChangeDetail(next.data, projectId, proposalId, detail.revision.id);
    history.push(...next.data.history);
    cursor = next.data.nextCursor;
  }
  return { ...detail, history, nextCursor: "" };
}
export async function readChangeIdentities(
  projectId: string,
  detail: BackendChangeProposalDetail,
  signal?: AbortSignal,
): Promise<BackendEffectiveIdentity[]> {
  const identities = new Map<string, BackendEffectiveIdentity>();
  let hash = "";
  for (const recordType of ["nodes", "edges"] as const) {
    let cursor = "";
    const seen = new Set<string>();
    do {
      const response = await queryBackendGraph(
        projectId,
        {
          changeProposal: {
            proposalId: detail.proposal.id,
            proposalRevisionId: detail.revision.id,
          },
          recordType,
          limit: 500,
          ...(cursor ? { cursor } : {}),
        },
        { signal },
      );
      signal?.throwIfAborted();
      if (
        response.status !== 200 ||
        !("pins" in response.data) ||
        !response.data.pins ||
        response.data.pins.effectiveSemanticHash !== detail.revision.semanticHash ||
        response.data.pins.baseRevisionId !== detail.revision.baseRevisionId ||
        response.data.pins.baseSemanticHash !== detail.revision.baseSemanticHash ||
        response.data.pins.viewSchemaVersion !== "proposal-graph-v1" ||
        response.data.pins.structuralSchemaVersion !== "6" ||
        response.data.target?.changeProposal?.proposalRevisionId !== detail.revision.id ||
        response.data.target.changeProposal.proposalId !== detail.proposal.id
      )
        throw new Error("Идентичности относятся к другой ревизии");
      if (hash && hash !== response.data.pins.targetHash)
        throw new Error("Изменился точный контекст идентичностей");
      hash = response.data.pins.targetHash;
      for (const item of response.data.identities ?? [])
        identities.set(changeIdentityKey(item.target), item);
      cursor = response.data.nextCursor;
      if (cursor && seen.has(cursor)) throw new Error("Список идентичностей неполон");
      seen.add(cursor);
    } while (cursor);
  }
  return [...identities.values()];
}

/** Rebase deliberately changes the baseline; ordinary apply keeps its stricter old-base checks. */
export async function verifyRebaseAcceptance(
  result: import("@/api/generated/schemas").BackendChangeProposalApplyResult,
  base: BackendChangeProposalDetail,
  expected: {
    newBaseRevisionId: string;
    newBaseSemanticHash: string;
    sourceVectorHash: string;
    sourceSnapshotIds: string[];
    candidateHash: string;
    semanticHash: string;
  },
) {
  if (
    result.proposal.id !== base.proposal.id ||
    result.proposal.projectId !== base.proposal.projectId ||
    result.proposal.version !== base.proposal.version + 1 ||
    result.proposal.status !== "draft" ||
    result.proposal.readyReference ||
    result.revision.proposalId !== base.proposal.id ||
    result.revision.parentRevisionId !== base.revision.id ||
    result.revision.id === base.revision.id ||
    result.proposal.currentDraftRevisionId !== result.revision.id ||
    result.revision.baseRevisionId !== expected.newBaseRevisionId ||
    result.revision.baseSemanticHash !== expected.newBaseSemanticHash ||
    result.revision.semanticHash !== expected.semanticHash ||
    (await hashBackendJSON(result.revision.sourceVector)) !== expected.sourceVectorHash ||
    JSON.stringify(result.revision.sourceSnapshotIds) !==
      JSON.stringify(expected.sourceSnapshotIds) ||
    result.revision.rebase?.candidateHash !== expected.candidateHash
  )
    throw new Error("Rebase не подтвердил новую базу, точные source pins и новый черновик.");
  return {
    ...base,
    proposal: result.proposal,
    revision: result.revision,
    history: [
      {
        id: result.revision.id,
        parentRevisionId: result.revision.parentRevisionId,
        semanticHash: result.revision.semanticHash,
        author: result.revision.author,
        summary: result.revision.summary,
        createdAt: result.revision.createdAt,
      },
      ...base.history,
    ],
  };
}
