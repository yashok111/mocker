import type {
  BackendEffectiveGraphPins,
  BackendReadTarget,
  BackendNode,
  BackendStructuralNodeResponse,
  BackendEdge,
  BackendStructuralEdgeResponse,
} from "@/api/generated/schemas";
import { backendReadTargetFrom, backendReadTargetKey } from "./backendReadTargets";
import { readBackendNode } from "./backendGraphReads";
export type ProjectionReadProps = {
  revisionId?: string;
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
};
export type ProjectionNode =
  | BackendNode
  | (BackendStructuralNodeResponse & { externalKey?: never; ownership?: never; freshness?: never });
export type ProjectionEdge =
  | BackendEdge
  | (BackendStructuralEdgeResponse & { externalKey?: never; ownership?: never; freshness?: never });
export function projectionTarget(props: ProjectionReadProps): BackendReadTarget {
  const target = props.target ?? backendReadTargetFrom({ revisionId: props.revisionId });
  backendReadTargetKey(target);
  if (target.importCandidate)
    throw new Error("Для Preview доступны только граф, объекты и основания.");
  return target;
}
export const projectionKey = (target: BackendReadTarget, pins?: BackendEffectiveGraphPins) =>
  JSON.stringify([backendReadTargetKey(target), pins ?? null]);
export async function readProjectionNode(
  projectId: string,
  target: BackendReadTarget,
  id: string,
  signal: AbortSignal,
  pins?: BackendEffectiveGraphPins,
): Promise<ProjectionNode> {
  const response = await readBackendNode(projectId, target, id, signal, pins);
  if ("node" in response) return response.node;
  if ("proposalProjection" in response)
    throw new Error("Используйте инспектор старого предложения");
  return response;
}

export function projectionReturnTarget(
  target: BackendReadTarget | undefined,
  revisionId: string,
): Record<string, string> {
  if (target) backendReadTargetKey(target);
  return target?.changeProposal
    ? {
        returnChangeProposalId: target.changeProposal.proposalId,
        returnProposalRevisionId: target.changeProposal.proposalRevisionId,
      }
    : { returnRevisionId: target?.revisionId ?? revisionId };
}
export function projectionURLTarget(target: BackendReadTarget, revisionId: string) {
  backendReadTargetKey(target);
  return target.changeProposal
    ? {
        changeProposalId: target.changeProposal.proposalId,
        proposalRevisionId: target.changeProposal.proposalRevisionId,
      }
    : { revisionId: target.revisionId ?? revisionId };
}
