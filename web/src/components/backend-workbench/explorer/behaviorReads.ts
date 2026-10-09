import type { BackendReadTarget, BackendNodeResponse } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { readBackendGraph, readBackendNode } from "../backendGraphReads";
import { readFlowMap, readExploreNodes, nodeOf } from "./reads";
import { readPages } from "./readPages";
import { sourceNode, relationNames, type MapData, type MapNode, type MapEdge } from "./model";

export type BehaviorData = {
  scene: MapData;
  entrypoint: MapNode;
  representation: "sequence" | "dependencies" | "choice";
  flowId?: string;
  expanded: string[];
};
export type BehaviorFlow = Awaited<ReturnType<typeof readFlowMap>>;
function nodeResult(value: BackendNodeResponse): MapNode {
  const raw = "node" in value ? value.node : value;
  if (!("kind" in raw) || !("attributes" in raw)) throw new Error("Объект сценария недоступен");
  return nodeOf(raw);
}
const contextKinds = new Set([
  "handles",
  "calls",
  "reads",
  "writes",
  "deletes",
  "publishes",
  "consumes",
]);
const clean = (node: MapNode): MapNode => ({
  ...node,
  description: ["handler", "symbol", "job"].includes(node.kind) ? "" : node.description,
});

export async function readBehavior(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
  resolvedFlow?: BehaviorFlow,
): Promise<BehaviorData> {
  const flow = resolvedFlow ?? (await readFlowMap(projectId, target, search, signal));
  let entrypoint = flow.entrypoint;
  if (!entrypoint && search.entrypointId)
    entrypoint = nodeResult(await readBackendNode(projectId, target, search.entrypointId, signal));
  let definition: MapNode | undefined;
  if (flow.flowId)
    definition = nodeResult(await readBackendNode(projectId, target, flow.flowId, signal));
  if (!entrypoint && definition)
    entrypoint = definition.parentId
      ? nodeResult(await readBackendNode(projectId, target, definition.parentId, signal))
      : definition;
  if (!entrypoint) throw new Error("Не указан вход в сценарий");
  if (search.entrypointId && definition) {
    const owners = ["handler", "symbol"].includes(entrypoint.kind)
      ? [entrypoint.id]
      : (flow.handlerIds ?? []);
    if (!definition.parentId || !owners.includes(definition.parentId))
      throw new Error("Flow не принадлежит выбранной точке входа");
  }
  const desired = !!target.changeProposal || !!target.proposal;
  const present = (n: MapNode) => ({
    ...clean(n),
    ...(desired ? { origin: "Модель предложения" } : {}),
  });
  const root = present(entrypoint);
  if (flow.presentation === "catalog" && flow.nodes.length > 1)
    return { scene: flow, entrypoint, representation: "choice", expanded: [] };
  const ordered =
    flow.edges.length > 0 ||
    flow.nodes.some(
      (n) => typeof n.attributes.stepKind === "string" && n.attributes.stepKind !== "opaque",
    );
  if (ordered) {
    const start = definition?.attributes.entryStepId;
    const hasStart = typeof start === "string" && flow.nodes.some((n) => n.id === start);
    return {
      entrypoint,
      representation: "sequence",
      flowId: flow.flowId,
      expanded: [],
      scene: {
        ...flow,
        title: entrypoint.name,
        nodes: (hasStart ? [root, ...flow.nodes] : flow.nodes).map((n) =>
          desired ? { ...n, origin: "Модель предложения" } : n,
        ),
        edges: hasStart
          ? [
              {
                id: `entry:${root.id}:${flow.flowId}`,
                kind: "handles",
                from: root.id,
                to: start,
                label: "Начало",
                refs: definition ? [{ kind: "record", recordType: "node", id: definition.id }] : [],
              },
              ...flow.edges,
            ]
          : flow.edges,
      },
    };
  }
  const requested = new Set(search.wbCalls ?? []);
  const queue = [root.id],
    expanded = new Set<string>();
  const edges = new Map<string, MapEdge>();
  const ids = new Set<string>([root.id]);
  for (let index = 0; index < queue.length; index++) {
    const id = queue[index]!;
    if (expanded.has(id)) continue;
    expanded.add(id);
    const pages = await readPages(
      (cursor) =>
        readBackendGraph(
          projectId,
          target,
          { recordType: "edges", from: id, limit: 500, cursor },
          signal,
        ),
      signal,
    );
    for (const page of pages)
      for (const edge of page.edges) {
        if (!contextKinds.has(edge.kind)) continue;
        if (
          id === root.id &&
          edge.kind === "handles" &&
          definition?.parentId &&
          edge.to !== definition.parentId
        )
          continue;
        ids.add(edge.from);
        ids.add(edge.to);
        edges.set(edge.id, {
          id: edge.id,
          kind: edge.kind,
          from: edge.from,
          to: edge.to,
          label: edge.kind === "handles" ? "Обработчик" : (relationNames[edge.kind] ?? edge.kind),
          origin: desired ? "Модель предложения" : "Исходный код",
        });
        if (edge.kind === "handles" || requested.has(edge.to)) queue.push(edge.to);
      }
  }
  const related = await readExploreNodes(
    projectId,
    target,
    [...ids].filter((id) => id !== root.id),
    signal,
  );
  const outgoing = new Set([...edges.values()].map((e) => e.from));
  const nodes = [root, ...related.map((n) => present(sourceNode(n)))].map((n) => ({
    ...n,
    ...(expanded.has(n.id) && !outgoing.has(n.id)
      ? {
          details: {
            ...n.details,
            "Дальнейшие вызовы":
              "В выбранной модели не описаны. Это не доказывает отсутствие вызовов в коде.",
          },
        }
      : {}),
    badge: ["handler", "symbol"].includes(n.kind)
      ? n.id ===
        (definition?.parentId ?? (entrypoint.kind === "handler" ? entrypoint.id : undefined))
        ? "Действия не разобраны"
        : expanded.has(n.id)
          ? outgoing.has(n.id)
            ? "Вызовы раскрыты"
            : "Вызовы не описаны"
          : "Раскрыть вызовы"
      : n.badge,
  }));
  const presentIds = new Set(nodes.map((n) => n.id));
  if ([...ids].some((id) => !presentIds.has(id)))
    throw new Error("Не все связанные объекты этой версии доступны");
  return {
    entrypoint,
    representation: "dependencies",
    flowId: flow.flowId,
    expanded: [...expanded],
    scene: {
      nodes,
      edges: [...edges.values()],
      total: nodes.length,
      title: entrypoint.name,
      subtitle: "Связи по исходникам",
      partial:
        entrypoint.attributes.dispatchStatus === "unknown"
          ? "Переход к обработчику не установлен. Показаны только связи из выбранной модели."
          : "Порядок действий ещё не разобран. Показаны только известные вызовы и связи.",
    },
  };
}
