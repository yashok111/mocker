import type { BackendReadTarget, QueryBackendDatabaseRequest } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { databaseStatus, readDatabasePage } from "../backendDatabaseReads";
import { cardinalityText } from "../backendDatabaseLayout";
import { readBackendNode } from "../backendGraphReads";
import type { MapData, MapNode } from "./model";
import { nodeOf, readExploreNodes } from "./reads";
import { readPages, uniqueBy } from "./readPages";
export async function readDatabaseMap(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData> {
  const datastoreId = search.datastoreId ?? search.wbScope;
  if (!datastoreId) throw new Error("Выберите хранилище");
  let facetKey = search.facetKey;
  if (!facetKey) {
    const response = await readBackendNode(projectId, target, datastoreId, signal);
    const raw = "node" in response ? response.node : response;
    if (!("attributes" in raw)) throw new Error("Хранилище недоступно");
    const attrs: Record<string, unknown> = { ...raw.attributes };
    const relational =
      attrs.relational && typeof attrs.relational === "object"
        ? (attrs.relational as Record<string, unknown>)
        : undefined;
    const available = attrs.facets ?? relational?.facets;
    const facets = available && typeof available === "object" ? Object.keys(available) : [];
    if (facets.length === 1) facetKey = facets[0];
    else
      return {
        nodes: facets.map((key) => ({
          ...nodeOf(raw),
          id: `db-facet:${key}`,
          kind: "collection",
          name: key,
          group: `db-facet:${key}`,
          description: "Представление схемы",
        })),
        edges: [],
        total: facets.length,
        title: "Представления хранилища",
        subtitle: nodeOf(raw).name,
        partial: facets.length ? undefined : "Реляционная схема этого хранилища не извлечена",
      };
  }
  const context = {
    projectId,
    revisionId: target.revisionId ?? search.revisionId ?? "",
    target,
    datastoreId,
    facetKey: facetKey!,
    ...(target.proposal ? { proposal: target.proposal } : {}),
  };
  const base = { ...target, datastoreId, facetKey: facetKey!, limit: 100 };
  const [tables, relations] = await Promise.all([
    readPages(
      (cursor) =>
        readDatabasePage(
          context,
          {
            ...base,
            recordType: "tables",
            search: search.wbQuery,
            limit: 100,
            cursor,
          } as QueryBackendDatabaseRequest,
          signal,
        ).then((r) => r.data),
      signal,
      (p) => `${p.projectId}:${p.revisionId}:${p.semanticHash}:${p.datastoreId}:${p.facetKey}`,
    ),
    readPages(
      (cursor) =>
        readDatabasePage(
          context,
          {
            ...base,
            recordType: "relationships",
            cursor,
            ...(search.dataNodeId ? { tableId: search.dataNodeId } : {}),
          } as QueryBackendDatabaseRequest,
          signal,
        ).then((r) => r.data),
      signal,
      (p) => `${p.projectId}:${p.revisionId}:${p.semanticHash}:${p.datastoreId}:${p.facetKey}`,
    ),
  ]);
  for (const response of [...tables, ...relations]) {
    if (
      response.datastoreId !== datastoreId ||
      response.facetKey !== facetKey ||
      (target.revisionId && response.revisionId !== target.revisionId)
    )
      throw new Error("Получена другая область данных");
  }
  const nodes: MapNode[] = uniqueBy(
    tables.flatMap((p) => p.tableItems),
    (t) => t.tableId,
  ).map((t) => ({
    id: t.tableId,
    name: t.qualifiedName,
    kind: "table",
    parentId: t.schemaId,
    description: `${t.columnCount} полей`,
    childCount: t.columnCount,
    attributes: { datastoreId, facetKey },
    origin: "Объявленная схема",
  }));
  const ids = new Set(nodes.map((n) => n.id));
  const allRelations = uniqueBy(
    relations.flatMap((p) => p.relationshipItems),
    (e) => e.edgeId,
  );
  const visibleRelations = allRelations.filter(
    (e) => e.targetTableId && ids.has(e.sourceTableId) && ids.has(e.targetTableId),
  );
  const hiddenRelationships = allRelations.filter(
    (e) => e.targetTableId && ids.has(e.sourceTableId) !== ids.has(e.targetTableId),
  ).length;
  const fieldIds = [
    ...new Set(
      visibleRelations.flatMap((e) =>
        e.columnPairs.flatMap((pair) => [pair.fromColumnId, pair.toColumnId]),
      ),
    ),
  ];
  // Labels are optional decoration: retain the pinned, verified relationships
  // when this bounded name lookup is unavailable. Never substitute another target.
  const fields = fieldIds.length
    ? await readExploreNodes(projectId, target, fieldIds, signal).catch(() => {
        signal.throwIfAborted();
        return undefined;
      })
    : undefined;
  const names = new Map(fields?.map((n) => [n.id, n.name]));
  const tableNames = new Map(nodes.map((n) => [n.id, n.name]));
  return {
    nodes,
    hiddenRelationships,
    edges: visibleRelations.map((e) => {
      const named =
        e.columnPairs.length > 0 &&
        e.columnPairs.every((p) => names.has(p.fromColumnId) && names.has(p.toColumnId));
      const label = named
        ? `${e.columnPairs.map((p) => names.get(p.fromColumnId)).join(", ")} → ${e.columnPairs.map((p) => names.get(p.toColumnId)).join(", ")}`
        : "Внешний ключ";
      return {
        id: e.edgeId,
        kind: "references",
        from: e.sourceTableId,
        to: e.targetTableId!,
        label,
        details: {
          "Из таблицы": tableNames.get(e.sourceTableId)!,
          "В таблицу": tableNames.get(e.targetTableId!)!,
          "Кратность исходной таблицы": cardinalityText(
            e.sourceCardinality.min,
            e.sourceCardinality.max,
          ),
          "Кратность целевой таблицы": cardinalityText(
            e.targetCardinality.min,
            e.targetCardinality.max,
          ),
          Основание: databaseStatus(e.status),
        },
        witness: e,
        origin: e.status === "explicit" ? "Исходный код" : databaseStatus(e.status),
      };
    }),
    total: nodes.length,
    title: "Схема данных",
    subtitle: `${facetKey} · статическая модель`,
    partial:
      tables[0]!.facetStatus === "desired"
        ? "Авторская схема предложения"
        : tables[0]!.facetStatus === "stale"
          ? "В схеме есть устаревшие сведения"
          : tables[0]!.facetStatus === "unknown"
            ? "Детализация схемы неизвестна"
            : tables.some((p) => p.limitations.length)
              ? "Есть ограничения детализации схемы"
              : undefined,
  };
}
