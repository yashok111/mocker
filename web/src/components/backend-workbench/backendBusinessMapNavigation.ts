import type { BackendDiagramVersion } from "@/api/generated/schemas";
import { diagramSearch, type BackendWorkspaceSearch } from "./backendWorkspaceSearch";
export function businessMapArchitectureSearch(
  architecture: BackendDiagramVersion,
  targetHash: string,
  elementId?: string,
): BackendWorkspaceSearch {
  if (architecture.document.kind !== "architecture" || architecture.targetHash !== targetHash)
    throw new Error("Архитектура другого target");
  const p = architecture.document.payload,
    e = elementId ? p.elements.find((e) => e.id === elementId) : undefined;
  if (elementId && !e)
    throw new Error("Историческое C4 соответствие отсутствует; выберите точную архитектуру");
  const level =
    e?.role === "component"
      ? "components"
      : e?.role === "application" || e?.role === "data_store"
        ? "containers"
        : "context";
  return {
    ...diagramSearch(architecture.pin),
    diagramLevel: level,
    diagramRoot: level === "context" ? p.primarySystemId : e!.parentId,
    ...(elementId ? { diagramSelection: `element:${elementId}` } : {}),
  };
}
