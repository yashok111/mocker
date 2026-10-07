import type { BackendReadTarget, QueryBackendFlowRequest } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { readFlowPage, flowPageOf } from "../backendFlowReads";
import { readExploreNodes } from "./reads";
import { readPages, uniqueBy } from "./readPages";
import { sourceNode, relationNames, type MapData, type MapEdge } from "./model";
export async function readAccessMap(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData> {
  const reverse = search.wbFlowPart === "reverse";
  const selector = reverse
    ? { dataNodeId: search.dataNodeId }
    : { entrypointId: search.entrypointId };
  if (reverse ? !search.dataNodeId : !search.entrypointId)
    throw new Error("Для этого вида не указан связанный объект");
  const kind = reverse ? search.wbReverseAccessKind : search.wbAccessKind;
  const responses = await readPages(
    (cursor) =>
      readFlowPage(
        projectId,
        {
          ...target,
          view: "accesses",
          ...selector,
          ...(kind ? { accessKind: kind } : {}),
          limit: 100,
          cursor,
        } as QueryBackendFlowRequest,
        signal,
      ),
    signal,
    (p) => p.semanticHash,
  );
  const pages = responses.map((p) => flowPageOf(p, "accesses"));
  if (pages.some((p) => !p)) throw new Error("Получен другой вид доступов");
  const accessItems = uniqueBy(
    pages.flatMap((p) => p!.accessItems),
    (a) => `${a.accessEdgeId}:${a.entrypointId}`,
  );
  const ids = [
    ...new Set(
      accessItems.flatMap((a) => [
        a.queryId,
        a.targetId,
        ...(a.entrypointId ? [a.entrypointId] : []),
      ]),
    ),
  ];
  const nodes = ids.length
    ? (await readExploreNodes(projectId, target, ids, signal)).map(sourceNode)
    : [];
  const seen = new Set<string>();
  const edges: MapEdge[] = accessItems
    .filter((a) => {
      if (seen.has(a.accessEdgeId)) return false;
      seen.add(a.accessEdgeId);
      return true;
    })
    .map((a) => ({
      id: a.accessEdgeId,
      kind: a.accessKind,
      from: a.queryId,
      to: a.targetId,
      label: relationNames[a.accessKind] ?? a.accessKind,
      details: {
        Режим: a.accessMode,
        Основание: a.status,
        Связь: a.relation,
        Ограничения: a.limitations.slice(0, 4).join(" · "),
      },
      witness: a,
    }));
  return {
    nodes,
    edges,
    total: nodes.length,
    title: reverse ? "Обращения к данным" : "Чтение и запись",
    subtitle: "Извлечённые статические связи · выполнение не подтверждено",
    partial: !edges.length
      ? "Нет извлечённых обращений в этой области. Это не доказывает отсутствие работы с данными."
      : pages.some((p) => p!.limitations.length)
        ? "Детализация обращений частичная"
        : undefined,
  };
}
