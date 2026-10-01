import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import {
  getBackendEvidence,
  getBackendProposalEvidence,
  queryBackendGraph,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendEdge,
  BackendNode,
  BackendEvidence,
  QueryBackendGraphRequest,
  BackendDatastoreFacet,
  BackendDatabaseSchemaFacet,
  BackendTableFacet,
  BackendColumnFacet,
  BackendConstraintFacet,
  BackendIndexFacet,
  BackendViewFacet,
  BackendMigrationFacet,
  BackendRoutineFacet,
  BackendReferenceFacet,
  BackendProposalProjectedNode,
  BackendProposalProjectedEdge,
} from "@/api/generated/schemas";

export type DatabaseContext = {
  projectId: string;
  revisionId: string;
  datastoreId: string;
  facetKey: string;
  proposal?: { proposalId: string; proposalRevisionId: string };
};
export const databaseTarget = (context: DatabaseContext) =>
  context.proposal ? { proposal: context.proposal } : { revisionId: context.revisionId };
export type DatabaseSelection = { type: "node" | "edge"; id: string; revisionId?: string };
type Facet =
  | BackendDatastoreFacet
  | BackendDatabaseSchemaFacet
  | BackendTableFacet
  | BackendColumnFacet
  | BackendConstraintFacet
  | BackendIndexFacet
  | BackendViewFacet
  | BackendMigrationFacet
  | BackendRoutineFacet
  | BackendReferenceFacet;
export const databaseKey = (context: DatabaseContext) =>
  [
    "backend-database",
    context.projectId,
    context.revisionId,
    context.datastoreId,
    context.facetKey,
    ...(context.proposal ? [context.proposal.proposalId, context.proposal.proposalRevisionId] : []),
  ] as const;

export function useDatabaseCancellation(key: readonly string[]) {
  const client = useQueryClient();
  const identity = JSON.stringify(key);
  useEffect(
    () => () => {
      void client.cancelQueries({ queryKey: JSON.parse(identity) });
    },
    [client, identity],
  );
}

export function relationalFacets(record: BackendNode | BackendEdge): Record<string, Facet> {
  const attributes = record.attributes;
  if ("facets" in attributes) return attributes.facets;
  if ("relational" in attributes) return attributes.relational.facets;
  if ("databaseRoutine" in attributes) return attributes.databaseRoutine.facets;
  return {};
}

// Fail an incomplete aggregate; all pages retain the original pin and signal.
export async function readDatabaseGraph(
  projectId: string,
  input: QueryBackendGraphRequest,
  signal: AbortSignal,
) {
  const nodes: BackendNode[] = [],
    edges: BackendEdge[] = [],
    proposalNodes: BackendProposalProjectedNode[] = [],
    proposalEdges: BackendProposalProjectedEdge[] = [],
    seen = new Set<string>();
  let cursor = "";
  do {
    signal.throwIfAborted();
    const response = await queryBackendGraph(
      projectId,
      { ...input, limit: 500, ...(cursor ? { cursor } : {}) },
      { signal },
    );
    signal.throwIfAborted();
    if (response.status !== 200) throw new Error("Не удалось загрузить полный список объектов");
    nodes.push(...response.data.nodes);
    edges.push(...response.data.edges);
    if (response.data.proposalProjection) {
      proposalNodes.push(...response.data.proposalProjection.nodes);
      proposalEdges.push(...response.data.proposalProjection.edges);
    }
    cursor = response.data.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("Повтор страницы: список объектов неполон");
    seen.add(cursor);
  } while (cursor);
  return { nodes, edges, proposalNodes, proposalEdges };
}

export async function readDatabaseEvidence(
  context: DatabaseContext,
  subjectId: string,
  signal: AbortSignal,
) {
  const items: BackendEvidence[] = [],
    seen = new Set<string>();
  let cursor = "";
  do {
    signal.throwIfAborted();
    const params = { subjectId, limit: 500, ...(cursor ? { cursor } : {}) };
    const response = context.proposal
      ? await getBackendProposalEvidence(
          context.projectId,
          context.proposal.proposalId,
          context.proposal.proposalRevisionId,
          params,
          { signal },
        )
      : await getBackendEvidence(context.projectId, context.revisionId, params, { signal });
    signal.throwIfAborted();
    if (response.status !== 200) throw new Error("Основания загружены не полностью");
    items.push(...response.data.items);
    cursor = response.data.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("Повтор страницы: основания неполны");
    seen.add(cursor);
  } while (cursor);
  return items;
}

export function datastoreScope(nodes: BackendNode[], datastoreId: string) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  return nodes.filter((node) => {
    const seen = new Set<string>();
    let current: BackendNode | undefined = node;
    while (current && !seen.has(current.id)) {
      if (current.id === datastoreId) return true;
      seen.add(current.id);
      current = current.parentId ? byId.get(current.parentId) : undefined;
    }
    return false;
  });
}

export const databaseWrap = {
  overflowWrap: "anywhere" as const,
  whiteSpace: "pre-wrap" as const,
  minWidth: 0,
};

export const databaseButtonStyles = {
  root: { minWidth: 0, maxWidth: "100%" },
  label: databaseWrap,
};
export function databaseStatus(status: string) {
  return (
    (
      {
        current: "Подтверждено в снимке",
        stale: "Устарело",
        unknown: "Неизвестно",
        different: "Расхождение",
        consistent: "Согласовано",
        complete: "Полное",
        partial: "Частичное",
        unsupported: "Не поддерживается",
        explicit: "Явно объявлено",
        inferred: "Предположение",
        unresolved: "Цель не установлена",
        proposed: "Предложено",
        unverified: "Не проверено",
      } as Record<string, string>
    )[status] ?? status
  );
}
