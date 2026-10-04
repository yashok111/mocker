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
