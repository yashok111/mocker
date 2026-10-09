import type { BackendReadTarget } from "@/api/generated/schemas";
import { readBackendGraph } from "../backendGraphReads";
import { readExploreNodes } from "./reads";
import { readPages } from "./readPages";
import { sourceNode, type MapNode } from "./model";

export const isAccessEntrypoint = (kind: string) =>
  ["http_operation", "job", "consumer"].includes(kind);

// The API's access scope is a declared entrypoint, never its handler/symbol.
// Only exact incoming handles bindings establish a choice for this Flow owner.
export async function readFlowAccessEntrypoints(
  projectId: string,
  target: BackendReadTarget,
  ownerId: string,
  signal: AbortSignal,
): Promise<MapNode[]> {
  const pages = await readPages(
    (cursor) =>
      readBackendGraph(
        projectId,
        target,
        {
          recordType: "edges",
          kind: "handles",
          to: ownerId,
          limit: 500,
          cursor,
        },
        signal,
      ),
    signal,
  );
  const ids = [
    ...new Set(
      pages.flatMap((page) =>
        page.edges
          .filter((edge) => edge.kind === "handles" && edge.to === ownerId)
          .map((edge) => edge.from),
      ),
    ),
  ];
  if (!ids.length) return [];
  const nodes = await readExploreNodes(projectId, target, ids, signal);
  return nodes
    .filter((node) => ids.includes(node.id) && isAccessEntrypoint(node.kind))
    .map(sourceNode);
}
