import {
  getBackendDiagramView,
  getBackendSavedView,
  queryBackendExplore,
} from "@/api/generated/backend-projects/backend-projects";
import type {
  BackendReadTarget,
  BackendDiagramVersion,
  BackendDiagramView,
  BackendSavedViewResponse,
  BackendExplorePage,
  QueryBackendExploreRequest,
  QueryBackendFlowRequest,
} from "@/api/generated/schemas";
import { backendReadTargetKey } from "../backendReadTargets";
import {
  explicitDiagramPin,
  workspacePinError,
  type BackendWorkspaceSearch,
} from "../backendWorkspaceSearch";
import { readDiagram } from "../backendDiagramReads";
import { checkBackendSavedView } from "../backendSavedViewReads";
import { readFlowPage, flowPageOf } from "../backendFlowReads";
import { readBackendNode, readBackendGraph } from "../backendGraphReads";
import { readPages, uniqueBy } from "./readPages";
import { flowStepContext } from "../backendFlowLayout";
import { kindName, collectionName, sourceNode, type MapData, type MapNode } from "./model";
export type Entry = {
  target: BackendReadTarget;
  diagram?: BackendDiagramVersion;
  diagramView?: BackendDiagramView;
  saved?: BackendSavedViewResponse;
  search: BackendWorkspaceSearch;
};
export async function resolveEntry(
  projectId: string,
  search: BackendWorkspaceSearch,
  head: string,
  signal: AbortSignal,
): Promise<Entry> {
  const error = workspacePinError(search);
  if (error) throw new Error(error);
  if (search.diagramViewId) {
    const response = await getBackendDiagramView(
      projectId,
      search.diagramViewId,
      search.diagramViewVersion!,
      { signal },
    );
    signal.throwIfAborted();
    if (
      response.status !== 200 ||
      response.data.id !== search.diagramViewId ||
      response.data.version !== search.diagramViewVersion
    )
      throw new Error("Сохранённый вид недоступен");
    const diagram = await readDiagram(projectId, response.data.state.diagram, signal);
    return { target: diagram.document.target, diagram, diagramView: response.data, search };
  }
  const pin = explicitDiagramPin(search);
  if (pin) {
    const diagram = await readDiagram(projectId, pin, signal);
    return { target: diagram.document.target, diagram, search };
  }
  if (search.viewId) {
    const response = await getBackendSavedView(
      projectId,
      search.viewId,
      search.viewVersion ? { version: search.viewVersion } : undefined,
      { signal },
    );
    signal.throwIfAborted();
    if (response.status !== 200) throw new Error("Сохранённый вид недоступен");
    const saved = checkBackendSavedView(response.data, projectId, {
      id: search.viewId,
      version: search.viewVersion,
    });
    const target = saved.target;
    return {
      target,
      saved,
      search: {
        viewId: saved.id,
        viewVersion: saved.version,
        ...saved.state.scope,
        wbView: saved.state.kind === "flow" ? "scenarios" : "data",
        wbMode: saved.state.kind === "flow" ? "flow" : "objects",
        wbQuery: saved.state.filters.search,
        ...(saved.state.kind === "flow"
          ? {
              wbAccessKind: saved.state.filters.accessKind || undefined,
              wbReverseAccessKind: saved.state.filters.reverseAccessKind || undefined,
            }
          : { dataNodeId: saved.state.filters.relationshipTableId }),
        recordId: saved.state.selection?.id,
        recordType: saved.state.selection?.recordType,
      },
    };
  }
  if (search.wbTarget) {
    const target: BackendReadTarget = JSON.parse(search.wbTarget);
    backendReadTargetKey(target);
    return { target, search };
  }
  const target: BackendReadTarget =
    search.changeProposalId || search.proposalRevisionId
      ? {
          changeProposal: {
            proposalId: search.changeProposalId ?? "",
            proposalRevisionId: search.proposalRevisionId ?? "",
          },
        }
      : { revisionId: search.revisionId ?? head };
  backendReadTargetKey(target);
  return { target, search };
}
export async function readExplore(
  projectId: string,
  input: QueryBackendExploreRequest,
  signal: AbortSignal,
): Promise<BackendExplorePage> {
  const expected = backendReadTargetKey(input.target);
  const response = await queryBackendExplore(projectId, input, { signal });
  signal.throwIfAborted();
  if (
    response.status !== 200 ||
    backendReadTargetKey(response.data.target) !== expected ||
    !/^[0-9a-f]{64}$/.test(response.data.targetHash) ||
    !/^[0-9a-f]{64}$/.test(response.data.semanticHash)
  )
    throw new Error("Обзор выбранной версии недоступен");
  return response.data;
}
export async function readExploreScope(
  projectId: string,
  input: QueryBackendExploreRequest,
  signal: AbortSignal,
) {
  const pages = await readPages(
    (cursor) => readExplore(projectId, { ...input, cursor, limit: 100 }, signal),
    signal,
    (p) => `${p.targetHash}:${p.semanticHash}`,
  );
  const first = pages[0]!;
  return {
    ...first,
    nodes: uniqueBy(
      pages.flatMap((p) => p.nodes),
      (n) => n.id,
    ),
    edges: uniqueBy(
      pages.flatMap((p) => p.edges),
      (e) => e.id,
    ),
    groups: uniqueBy(
      pages.flatMap((p) => p.groups),
      (g) => g.id,
    ),
    beforeNodes: uniqueBy(
      pages.flatMap((p) => p.beforeNodes ?? []),
      (n) => n.id,
    ),
    beforeEdges: uniqueBy(
      pages.flatMap((p) => p.beforeEdges ?? []),
      (e) => e.id,
    ),
    changes: uniqueBy(
      pages.flatMap((p) => p.changes ?? []),
      (c) => `${c.recordType}:${c.id}`,
    ),
    nextCursor: "",
  };
}
export async function readExploreNodes(
  projectId: string,
  target: BackendReadTarget,
  ids: string[],
  signal: AbortSignal,
) {
  const nodes: BackendExplorePage["nodes"] = [];
  const unique = [...new Set(ids)];
  for (let offset = 0; offset < unique.length; offset += 100) {
    const page = await readExploreScope(
      projectId,
      { target, mode: "objects", nodeIds: unique.slice(offset, offset + 100) },
      signal,
    );
    nodes.push(...page.nodes);
  }
  return nodes;
}
export function nodeOf(raw: {
  id: string;
  kind: string;
  name: string;
  parentId: string | null;
  attributes: object;
}): MapNode {
  const attributes: Record<string, unknown> = { ...raw.attributes };
  if (raw.kind === "http_operation" && typeof attributes.description === "string") {
    try {
      const value = JSON.parse(attributes.description);
      if (
        value &&
        typeof value === "object" &&
        (typeof value.summary === "string" || Array.isArray(value.tags))
      ) {
        if (!attributes.summary && typeof value.summary === "string")
          attributes.summary = value.summary;
        attributes.description = typeof value.description === "string" ? value.description : "";
      }
    } catch {
      /* A normal prose description remains prose. */
    }
  }
  return {
    id: raw.id,
    kind: raw.kind,
    name: String(attributes.summary ?? raw.name),
    description: String(attributes.description ?? ""),
    parentId: raw.parentId,
    attributes,
    childCount: 0,
    origin: "Исходный код",
  };
}
export async function readSourceMap(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData & { page: BackendExplorePage }> {
  const mode =
    search.wbMode === "children"
      ? "children"
      : search.wbMode === "collections"
        ? "collections"
        : search.wbMode === "neighborhood"
          ? "neighborhood"
          : search.wbMode === "objects"
            ? "objects"
            : "overview";
  const page = await readExploreScope(
    projectId,
    {
      target,
      mode,
      scopeId: search.wbScope,
      kind: search.wbKind,
      group: search.wbGroup,
      search: search.wbQuery,
    },
    signal,
  );
  let nodes = page.nodes.map(sourceNode);
  let edges = page.edges;
  if (mode === "overview" && (nodes.length > 100 || page.edgeTotal > edges.length)) {
    const ids = new Set(nodes.map((n) => n.id));
    const graph = await readPages(
      (cursor) =>
        readBackendGraph(projectId, target, { recordType: "edges", limit: 500, cursor }, signal),
      signal,
    );
    edges = graph.flatMap((p) =>
      p.edges
        .filter((e) => ids.has(e.from) && ids.has(e.to))
        .map((e) => ({
          id: e.id,
          kind: e.kind,
          from: e.from,
          to: e.to,
          label:
            "label" in e.attributes && typeof e.attributes.label === "string"
              ? e.attributes.label
              : "",
        })),
    );
  }
  if (search.wbKind === "job" && nodes.length) {
    const jobs = await readPages(
      (cursor) =>
        readBackendGraph(
          projectId,
          target,
          { recordType: "nodes", kind: "job", limit: 500, cursor },
          signal,
        ),
      signal,
    );
    const byId = new Map<string, Record<string, unknown>>();
    for (const page of jobs)
      for (const node of page.nodes) byId.set(node.id, { ...node.attributes });
    nodes = nodes.map((n) => {
      const raw = byId.get(n.id);
      return raw ? { ...n, attributes: raw } : n;
    });
  }
  let title =
    mode === "overview"
      ? "Обзор из исходников"
      : mode === "collections"
        ? "API-операции"
        : search.wbGroup
          ? collectionName(search.wbGroup.replace(/^(prefix|tag):/, ""))
          : search.wbKind
            ? ((
                {
                  job: "Фоновые задачи",
                  http_operation: "API-операции",
                  symbol: "Функции и типы",
                  module: "Пакеты",
                  datastore: "Хранилища",
                } as Record<string, string>
              )[search.wbKind] ?? kindName(search.wbKind))
            : "Что внутри";
  if (mode === "collections")
    nodes = page.groups.map((g) => ({
      id: g.id,
      kind: "collection",
      name: collectionName(g.label),
      description:
        g.basis === "path_prefix"
          ? g.label
          : g.basis === "api_tag"
            ? `Тег API: ${g.label}`
            : "Без тега и пути",
      attributes: {},
      parentId: search.wbScope ?? null,
      childCount: g.count,
      group: g.id,
      badge: `${g.count} операций`,
    }));
  if (mode === "children" && Object.keys(page.counts).length > 0 && page.total > 15) {
    const groups: [string, string, string][] = [
      ["http_operation", "API-операции", "Контракт и логика запросов"],
      ["datastore", "Хранилища данных", "Таблицы и связи"],
      ["job", "Фоновые задачи", "Запуски по расписанию и обработчики"],
      ["module", "Исходный код", "Пакеты репозитория"],
      ["all", "Все объекты", "Включая поля контрактов, символы и нераспределённые объекты"],
    ];
    nodes = groups
      .filter(([kind]) => kind === "all" || (page.counts[kind] ?? 0) > 0)
      .map(([kind, name, description]) => ({
        id: `kind:${kind}`,
        kind: "collection",
        name,
        description,
        parentId: search.wbScope ?? null,
        attributes: {},
        childCount:
          kind === "all"
            ? Object.values(page.counts).reduce((a, b) => a + b, 0)
            : page.counts[kind]!,
        group: `kind:${kind}`,
        badge: `${kind === "all" ? Object.values(page.counts).reduce((a, b) => a + b, 0) : page.counts[kind]} объектов`,
      }));
    title = page.scope?.name ?? title;
  }
  return {
    presentation: ["children", "collections", "objects"].includes(mode) ? "catalog" : "map",
    nodes,
    edges,
    total: mode === "children" && nodes[0]?.group ? nodes.length : page.total,
    nextCursor: mode === "children" && nodes[0]?.group ? "" : page.nextCursor,
    title,
    subtitle: target.changeProposal
      ? "Авторская модель предложения · изменения не подтверждают реализацию"
      : mode === "collections"
        ? "Группы по путям и тегам API · одна операция может входить в несколько групп"
        : "Из импортированной модели · только известные связи",
    page,
  };
}
export async function readFlowMap(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData & { flowId?: string; entrypoint?: MapNode; handlerIds?: string[] }> {
  if (search.wbFlowPart === "entrypoints" || (!search.entrypointId && !search.flowId)) {
    const responses = await readPages(
      (cursor) =>
        readFlowPage(
          projectId,
          {
            ...target,
            view: "entrypoints",
            search: search.wbQuery ?? "",
            limit: 100,
            cursor,
          } as QueryBackendFlowRequest,
          signal,
        ),
      signal,
      (p) => p.semanticHash,
    );
    const nodes = uniqueBy(
      responses.flatMap((p) => flowPageOf(p, "entrypoints")?.entrypointItems ?? []),
      (i) => i.operation.id,
    ).map((i) => nodeOf(i.operation));
    return {
      presentation: "catalog",
      nodes,
      edges: [],
      total: nodes.length,
      title: "Операции",
      subtitle: search.wbQuery ? `Поиск: ${search.wbQuery}` : "Операции выбранной версии",
    };
  }
  let flowId = search.flowId;
  let entrypoint: MapNode | undefined;
  let handlerIds: string[] = [];
  if (search.entrypointId) {
    const detail = await readBackendNode(projectId, target, search.entrypointId, signal);
    const operation = "node" in detail ? detail.node : detail;
    if (!("name" in operation)) throw new Error("Операция недоступна");
    entrypoint = nodeOf(operation);
    if (operation.kind === "symbol" && flowId)
      throw new Error("Для этого символа не подтверждена связь с выбранным Flow");
    if (operation.kind === "symbol")
      return {
        nodes: [],
        edges: [],
        total: 0,
        title: entrypoint.name,
        subtitle: "Связи по исходникам",
        entrypoint,
        handlerIds: [],
      };
    if (operation.kind === "handler") {
      const flows = await readExploreScope(
        projectId,
        { target, mode: "children", scopeId: operation.id, kind: "flow", limit: 100 },
        signal,
      );
      if (flowId && !flows.nodes.some((n) => n.id === flowId))
        throw new Error("Flow не принадлежит выбранному обработчику");
      if (!flowId && (flows.nodes.length !== 1 || flows.nextCursor))
        return {
          presentation: "catalog",
          entrypoint,
          handlerIds: [operation.id],
          nodes: flows.nodes.map(sourceNode),
          edges: [],
          total: flows.total,
          nextCursor: flows.nextCursor,
          title: nodeOf(operation).name,
          subtitle: "Логика обработчика",
          partial: flows.nodes.length ? undefined : "Внутренняя логика ещё не детализирована",
        };
      const result = await readFlowMap(
        projectId,
        target,
        { ...search, entrypointId: undefined, flowId: flowId ?? flows.nodes[0]!.id },
        signal,
      );
      return { ...result, entrypoint, handlerIds: [operation.id] };
    }
    let cursor = "";
    let ids: string[] = [];
    const visited = new Set<string>();
    do {
      if (visited.has(cursor)) throw new Error("Сервер повторил страницу входов в сценарий");
      visited.add(cursor);
      const result = await readFlowPage(
        projectId,
        {
          ...target,
          view: "entrypoints",
          search: operation.name,
          limit: 40,
          ...(cursor ? { cursor } : {}),
        } as QueryBackendFlowRequest,
        signal,
      );
      const page = flowPageOf(result, "entrypoints");
      const entry = page?.entrypointItems.find((e) => e.operation.id === search.entrypointId);
      if (entry) {
        ids = entry.flowIds;
        handlerIds = entry.handlerIds;
        break;
      }
      cursor = page?.nextCursor ?? "";
    } while (cursor);
    if (flowId && !ids.includes(flowId))
      throw new Error("Flow не принадлежит выбранной точке входа");
    if (!flowId && ids.length !== 1) {
      return {
        presentation: "catalog",
        entrypoint,
        handlerIds,
        nodes: ids.map((id, i) => ({
          id,
          kind: "flow",
          name: `Логика обработчика ${i + 1}`,
          description: "Выберите конкретный обработчик",
          attributes: {},
          parentId: null,
          childCount: 1,
        })),
        edges: [],
        total: ids.length,
        title: nodeOf(operation).name,
        subtitle: "Логика операции",
        partial: ids.length
          ? "У операции несколько обработчиков"
          : "Внутренняя логика ещё не детализирована",
      };
    }
    flowId ??= ids[0];
  }
  if (!flowId)
    return {
      nodes: [],
      edges: [],
      total: 0,
      title: "Логика операции",
      subtitle: "Выберите операцию через поиск или карту",
    };
  const [stepPage, edgePage] = await Promise.all([
    readPages(
      (cursor) =>
        readFlowPage(
          projectId,
          {
            ...target,
            view: "steps",
            flowId,
            limit: 100,
            cursor,
          } as QueryBackendFlowRequest,
          signal,
        ),
      signal,
      (p) => p.semanticHash,
    ),
    readPages(
      (cursor) =>
        readFlowPage(
          projectId,
          { ...target, view: "transitions", flowId, limit: 300, cursor } as QueryBackendFlowRequest,
          signal,
        ),
      signal,
      (p) => p.semanticHash,
    ),
  ]);
  const steps = uniqueBy(
    stepPage.flatMap((p) => flowPageOf(p, "steps")?.stepItems ?? []),
    (n) => n.id,
  );
  const edges = uniqueBy(
    edgePage.flatMap((p) => flowPageOf(p, "transitions")?.transitionItems ?? []),
    (e) => e.id,
  );
  const nodes = steps.map((step) => {
    const context = flowStepContext(step);
    return {
      ...nodeOf(step),
      ...(context.length ? { details: { "Контекст шага": context.join("\n") } } : {}),
    };
  });
  return {
    flowId,
    entrypoint,
    handlerIds,
    nodes,
    edges: edges.map((e) => ({
      id: e.id,
      kind: e.kind,
      from: e.from,
      to: e.to,
      label: String(("label" in e.attributes ? e.attributes.label : "") ?? ""),
    })),
    total: nodes.length,
    title: "Логика операции",
    subtitle: "Статическая модель выполнения",
    partial: nodes.some(
      (n) => n.attributes.stepKind === "opaque" || n.attributes.analysisStatus === "partial",
    )
      ? "Внутренняя логика ещё не детализирована"
      : stepPage.some((p) => p.limitations.length)
        ? "Есть ограничения анализа; доказательства доступны у выбранного объекта"
        : undefined,
  };
}

export async function readLineageMap(
  projectId: string,
  target: BackendReadTarget,
  search: BackendWorkspaceSearch,
  signal: AbortSignal,
): Promise<MapData> {
  const id = search.wbScope ?? search.recordId;
  if (!id) throw new Error("Выберите поле");
  const detail = await readBackendNode(projectId, target, id, signal);
  const raw = "node" in detail ? detail.node : detail;
  if (!("kind" in raw) || !("attributes" in raw)) throw new Error("Поле недоступно");
  const seedNode = nodeOf(raw);
  let seed: import("@/api/generated/schemas").BackendLineageValueRef;
  if (search.wbLineageSeed) {
    seed = JSON.parse(search.wbLineageSeed);
    if (seed.nodeId !== id) throw new Error("Значение относится к другому объекту");
  } else if (raw.kind === "column") {
    const facets = "facets" in raw.attributes ? Object.keys(raw.attributes.facets) : [];
    const facet = search.facetKey ?? (facets.length === 1 ? facets[0] : undefined);
    if (!facet)
      return {
        nodes: facets.map((f) => ({
          ...seedNode,
          id: `facet:${f}`,
          kind: "collection",
          name: f,
          description: "Представление поля",
          group: `facet:${f}`,
        })),
        edges: [],
        total: facets.length,
        title: "Выберите представление поля",
        subtitle: seedNode.name,
      };
    seed = { kind: "column", nodeId: id, facetKey: facet };
  } else if (raw.kind === "api_field" || raw.kind === "representation_field")
    seed = { kind: raw.kind, nodeId: id };
  else throw new Error("У этого объекта нет выбранного значения");
  const { readLineagePage, lineageRefKey } = await import("../backendLineageReads");
  const pages = await readPages(
    (cursor) =>
      readLineagePage(
        projectId,
        {
          ...target,
          seed,
          direction: "reverse",
          limit: 100,
          maxDepth: 32,
          cursor,
        } as import("@/api/generated/schemas").QueryBackendLineageRequest,
        signal,
      ),
    signal,
    (p) => p.semanticHash,
  );
  const page = {
    ...pages[0]!,
    items: uniqueBy(
      pages.flatMap((p) => p.items),
      (i) => i.mapping.id,
    ),
    nextCursor: "",
  };
  const nodes: MapNode[] = [];
  const edges: import("./model").MapEdge[] = [];
  const known = new Set<string>();
  const addValue = (ref: import("@/api/generated/schemas").BackendLineageValueRef) => {
    const key = lineageRefKey(ref);
    if (!known.has(key)) {
      known.add(key);
      nodes.push({
        id: key,
        kind: "field",
        name:
          ref.nodeId === id
            ? seedNode.name
            : ref.kind === "port"
              ? ref.portKey
              : ref.kind === "column"
                ? `Поле · ${ref.facetKey}`
                : "Связанное поле",
        description:
          ref.nodeId === id ? "Выбранное значение" : "Точное имя доступно в связанном объекте",
        attributes: { valueRef: ref },
        parentId: null,
        childCount: 0,
        refs: [{ kind: "record", recordType: "node", id: ref.nodeId }],
        origin: "Исходный код",
      });
    }
    return key;
  };
  addValue(seed);
  for (const item of page.items) {
    const attrs = item.mapping.attributes;
    if (!("sources" in attrs) || !("destination" in attrs)) continue;
    nodes.push({
      ...nodeOf(item.mapping),
      badge: item.expansion === "boundary" ? "Граница детализации" : item.status,
    });
    for (const source of attrs.sources)
      edges.push({
        id: `${item.mapping.id}:${lineageRefKey(source)}`,
        kind: "value",
        from: addValue(source),
        to: item.mapping.id,
        label: "Исходное значение",
      });
    edges.push({
      id: `${item.mapping.id}:destination`,
      kind: "value",
      from: item.mapping.id,
      to: addValue(attrs.destination),
      label: "Преобразование",
    });
  }
  return {
    nodes,
    edges,
    total: nodes.length,
    nextCursor: page.nextCursor,
    title: "Происхождение значения",
    subtitle: seedNode.name,
    partial: page.truncated
      ? "Показана ограниченная область зависимостей"
      : page.items.length === 0
        ? "Нет явных соответствий в этой области. Другие зависимости остаются неизвестными."
        : undefined,
  };
}
