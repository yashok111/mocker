import type { BackendReadTarget } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { readExploreScope } from "./reads";
import { sourceNode, type MapData, type MapNode, type MapEdge } from "./model";
export async function readComparisonMap(
  projectId: string,
  target: BackendReadTarget,
  _search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData> {
  if (!target.changeProposal) throw new Error("Для сравнения требуется точная версия предложения");
  const page = await readExploreScope(projectId, { target, mode: "comparison" }, signal);
  if (!page.beforeTarget) throw new Error("Основание сравнения недоступно");
  const ids = [
    ...new Set([...(page.beforeNodes ?? []).map((n) => n.id), ...page.nodes.map((n) => n.id)]),
  ];
  const nodes: MapNode[] = ids.flatMap((id) => {
    const old = page.beforeNodes?.find((n) => n.id === id);
    const next = page.nodes.find((n) => n.id === id);
    const n = next ?? old;
    if (!n) return [];
    const change = page.changes?.find((c) => c.recordType === "node" && c.id === id)?.change;
    return [
      {
        ...sourceNode(n),
        change,
        before: old,
        after: next,
        evidenceTarget: next ? target : page.beforeTarget,
        badge:
          change === "added"
            ? "Добавлено"
            : change === "removed"
              ? "Удалено"
              : change === "changed"
                ? "Изменено"
                : "Контекст изменения",
        origin:
          change === "removed"
            ? "Исходная версия · удаляется предложением"
            : change
              ? "Авторское изменение"
              : "Исходный код",
      },
    ];
  });
  const edges: MapEdge[] = [
    ...(page.beforeEdges ?? []).filter((e) => !page.edges.some((n) => n.id === e.id)),
    ...page.edges,
  ].map((e) => {
    const change = page.changes?.find((c) => c.recordType === "edge" && c.id === e.id)?.change;
    const old = page.beforeEdges?.find((n) => n.id === e.id);
    const next = page.edges.find((n) => n.id === e.id);
    const label = (side: typeof old, before: boolean) =>
      side
        ? `${(before ? page.beforeNodes : page.nodes)?.find((n) => n.id === side.from)?.name ?? "Объект"} → ${(before ? page.beforeNodes : page.nodes)?.find((n) => n.id === side.to)?.name ?? "Объект"} · ${side.label || side.kind}`
        : "Связи нет";
    return {
      ...e,
      origin: change === "removed" ? "Удалено" : "Авторское изменение",
      details: {
        Изменение: change === "added" ? "Добавлено" : change === "removed" ? "Удалено" : "Изменено",
        До: label(old, true),
        После: label(next, false),
      },
      evidenceTarget: next ? target : page.beforeTarget,
    };
  });
  return {
    nodes,
    edges,
    total: nodes.length,
    nextCursor: page.nextCursor,
    title: "Изменения предложения",
    subtitle: `Показано ${page.changes?.length ?? 0} из ${page.total} изменений · до и после`,
  };
}
