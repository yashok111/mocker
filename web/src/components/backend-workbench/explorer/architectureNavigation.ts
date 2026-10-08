import type {
  BackendArchitectureDocument,
  BackendArchitectureNavigation,
  BackendArchitectureElement,
  BackendDiagramPin,
  BackendDiagramRef,
  BackendDiagramVersion,
  BackendDiagramViewState,
} from "@/api/generated/schemas";
import { diagramSearch, type BackendWorkspaceSearch } from "../backendWorkspaceSearch";
import { resolvedTargetSearch } from "./navigation";
import type { MapData, MapNode } from "./model";

export const architectureLevelNames = {
  context: "Окружение",
  containers: "Приложения и данные",
  components: "Компоненты",
};
export function architectureSearch(
  pin: BackendDiagramPin,
  level: "context" | "containers" | "components",
  rootId: string,
  focus?: string,
): BackendWorkspaceSearch {
  return {
    ...diagramSearch(pin, { level, rootId, selection: null }),
    wbView: "structure",
    ...(focus ? { diagramFocus: focus } : {}),
  };
}
export function architecturePlace(
  doc: BackendArchitectureDocument,
  search: BackendWorkspaceSearch,
  saved?: BackendDiagramViewState,
) {
  const level = search.diagramLevel ?? saved?.level ?? "containers";
  const rootId = search.diagramRoot ?? saved?.rootId ?? doc.payload.primarySystemId;
  const root = doc.payload.elements.find((e) => e.id === rootId);
  const systemId = root?.role === "application" ? root.parentId! : rootId;
  const focus = doc.payload.elements.find((e) => e.id === search.diagramFocus);
  let error: string | undefined;
  if (!root || root.role !== (level === "components" ? "application" : "software_system"))
    error = "Этот уровень недоступен для выбранного объекта.";
  if (
    search.diagramFocus &&
    (level !== "components" || focus?.role !== "component" || focus.parentId !== rootId)
  )
    error = "Компонент не принадлежит выбранному приложению.";
  return { level, rootId, root, systemId, focus, error };
}
export type ArchitectureDestination = {
  key: string;
  name: string;
  description: string;
  search: BackendWorkspaceSearch;
};
export function explicitArchitectureSearch(
  entry: BackendArchitectureNavigation,
): BackendWorkspaceSearch {
  return entry.kind === "diagram"
    ? architectureSearch(entry.diagram, entry.level, entry.rootId, entry.focusId)
    : {
        ...resolvedTargetSearch(entry.target),
        wbView: "scenarios",
        wbMode: "flow",
        flowId: entry.flowId,
      };
}
export function sourceRefIds(refs: BackendDiagramRef[] = []) {
  return [
    ...new Set(refs.flatMap((r) => (r.kind === "record" && r.recordType === "node" ? [r.id] : []))),
  ].sort();
}
export function architectureMemberIds(element?: BackendArchitectureElement) {
  return [
    ...new Set([...sourceRefIds(element?.refs), ...(element?.membership?.nodeIds ?? [])]),
  ].sort();
}
export function architectureDestinations(
  diagram: Pick<BackendDiagramVersion, "pin" | "document">,
  subjects: string[],
): ArchitectureDestination[] {
  if (diagram.document.kind !== "architecture") return [];
  const doc = diagram.document;
  const matches = subjects.length
    ? doc.payload.elements.filter((e) =>
        architectureMemberIds(e).some((id) => subjects.includes(id)),
      )
    : doc.payload.elements.filter((e) => e.id === doc.payload.primarySystemId);
  return matches.flatMap((e) => {
    const level =
      e.role === "application" || e.role === "component"
        ? "components"
        : e.role === "person"
          ? "context"
          : "containers";
    const rootId =
      e.role === "component" || e.role === "data_store"
        ? e.parentId
        : e.role === "person"
          ? doc.payload.primarySystemId
          : e.id;
    if (!rootId) return [];
    const search = architectureSearch(
      diagram.pin,
      level,
      rootId,
      e.role === "component" ? e.id : undefined,
    );
    if (architecturePlace(doc, search).error) return [];
    return [
      {
        key: `${diagram.pin.id}:${diagram.pin.version}:${rootId}:${e.id}`,
        name: e.label,
        description: e.responsibility,
        search,
      },
    ];
  });
}
export function focusArchitecture(data: MapData, focusId?: string): MapData {
  if (!focusId) return data;
  const ids = new Set([focusId]);
  for (const edge of data.edges)
    if (edge.from === focusId || edge.to === focusId) {
      ids.add(edge.from);
      ids.add(edge.to);
    }
  const byId = new Map(data.nodes.map((n) => [n.id, n]));
  for (const id of ids) {
    const parent = byId.get(id)?.parentId;
    if (parent && byId.get(parent)?.boundary) ids.add(parent);
  }
  const nodes = data.nodes.filter((n) => ids.has(n.id));
  return {
    ...data,
    nodes,
    edges: data.edges.filter((e) => ids.has(e.from) && ids.has(e.to)),
    total: nodes.length,
  };
}
export function architectureSourceSearch(
  target: BackendDiagramVersion["document"]["target"],
  node: MapNode,
): BackendWorkspaceSearch {
  const pin = resolvedTargetSearch(target);
  if (["http_operation", "handler", "job", "symbol"].includes(node.kind))
    return { ...pin, wbView: "scenarios", wbMode: "flow", entrypointId: node.id };
  if (node.kind === "flow") return { ...pin, wbView: "scenarios", wbMode: "flow", flowId: node.id };
  return {
    ...pin,
    wbView: ["table", "column", "datastore", "data_store"].includes(node.kind)
      ? "data"
      : "structure",
    wbMode: node.childCount > 0 ? "children" : "neighborhood",
    wbScope: node.id,
    ...(node.childCount > 0 ? {} : { recordId: node.id }),
  };
}
