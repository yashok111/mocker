import type { BackendProjectStartView } from "@/api/generated/schemas";
import type { BackendWorkspaceSearch } from "../backendWorkspaceSearch";

export function projectEntrySearch(
  search: BackendWorkspaceSearch,
  start?: BackendProjectStartView,
): BackendWorkspaceSearch {
  if (!start || Object.values(search).some((value) => value !== undefined)) return search;
  return start.kind === "diagram_view"
    ? { diagramViewId: start.id, diagramViewVersion: start.version }
    : { viewId: start.id, viewVersion: start.version };
}
