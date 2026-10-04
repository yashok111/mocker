import type {
  BackendReadTarget,
  BackendEffectiveGraphPins,
  QueryBackendDatabaseRequest,
} from "@/api/generated/schemas";
import { readBackendGraph, readBackendEvidence } from "./backendGraphReads";
import {
  projectionTarget,
  projectionKey,
  type ProjectionNode,
  type ProjectionEdge,
} from "./backendEffectiveProjectionReads";
import { checkBackendProjectionPins, backendReadTargetFrom } from "./backendReadTargets";
import { queryBackendDatabase } from "@/api/generated/backend-projects/backend-projects";
import { useEffect } from "react";
import { useQueryClient } from "@tanstack/react-query";
import type {
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
  target?: BackendReadTarget;
  pins?: BackendEffectiveGraphPins;
  datastoreId: string;
  facetKey: string;
  proposal?: { proposalId: string; proposalRevisionId: string };
};
export const databaseTarget = (context: DatabaseContext) =>
  context.proposal ? { proposal: context.proposal } : projectionTarget(context);
export type DatabaseSelection = { type: "node" | "edge"; id: string; revisionId?: string };
type SourceFacet =
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
type Facet = SourceFacet extends infer F
  ? F extends SourceFacet
    ? Omit<F, "sourceKind" | "evidenceIds" | "freshness" | "sourceSnapshotId"> &
        Partial<Pick<F, "sourceKind" | "evidenceIds" | "freshness" | "sourceSnapshotId">>
    : never
  : never;
export const databaseKey = (context: DatabaseContext) =>
  [
    "backend-database",
    context.projectId,
    projectionKey(databaseTarget(context), context.pins),
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

export function relationalFacets(record: ProjectionNode | ProjectionEdge): Record<string, Facet> {
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
  pins?: BackendEffectiveGraphPins,
) {
  const target = backendReadTargetFrom(input);
  const nodes: ProjectionNode[] = [],
    edges: ProjectionEdge[] = [],
    proposalNodes: BackendProposalProjectedNode[] = [],
    proposalEdges: BackendProposalProjectedEdge[] = [],
    seen = new Set<string>();
  let cursor = "";
  let expectedPins = pins;
  do {
    signal.throwIfAborted();
    const page = await readBackendGraph(
      projectId,
      target,
      { ...input, limit: 500, ...(cursor ? { cursor } : {}) },
      signal,
      expectedPins,
    );
    signal.throwIfAborted();
    if ("pins" in page) expectedPins = page.pins;
    nodes.push(...page.nodes);
    edges.push(...page.edges);
    if ("proposalProjection" in page && page.proposalProjection) {
      proposalNodes.push(...page.proposalProjection.nodes);
      proposalEdges.push(...page.proposalProjection.edges);
    }
    cursor = page.nextCursor;
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
    const page = await readBackendEvidence(
      context.projectId,
      databaseTarget(context),
      params,
      signal,
      context.pins,
    );
    signal.throwIfAborted();
    items.push(...page.items);
    cursor = page.nextCursor;
    if (cursor && seen.has(cursor)) throw new Error("Повтор страницы: основания неполны");
    seen.add(cursor);
  } while (cursor);
  return items;
}

export function datastoreScope(nodes: ProjectionNode[], datastoreId: string) {
  const byId = new Map(nodes.map((node) => [node.id, node]));
  return nodes.filter((node) => {
    const seen = new Set<string>();
    let current: ProjectionNode | undefined = node;
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
        desired: "Желаемая структура",
        unverified: "Не проверено",
      } as Record<string, string>
    )[status] ?? status
  );
}

export async function readDatabasePage(
  context: DatabaseContext,
  input: QueryBackendDatabaseRequest,
  signal: AbortSignal,
) {
  const target = projectionTarget({ ...context, target: databaseTarget(context) });
  signal.throwIfAborted();
  const response = await queryBackendDatabase(context.projectId, input, { signal });
  signal.throwIfAborted();
  if (response.status !== 200) throw new Error("Не удалось загрузить схему базы данных");
  const checkedPins = checkBackendProjectionPins(response.data, target, context.pins);
  if (
    checkedPins &&
    (response.data.projectId !== context.projectId ||
      response.data.revisionId !== checkedPins.baseRevisionId ||
      response.data.semanticHash !== checkedPins.effectiveSemanticHash ||
      response.data.datastoreId !== context.datastoreId ||
      response.data.facetKey !== context.facetKey ||
      response.data.recordType !== input.recordType)
  )
    throw new Error("Получена другая область базы данных");
  return response;
}
