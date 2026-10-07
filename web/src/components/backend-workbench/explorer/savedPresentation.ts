import type { BackendSavedViewResponse } from "@/api/generated/schemas";
import type { MapNode, MapEdge } from "./model";
export function savedCanvas(
  nodes: MapNode[],
  edges: MapEdge[],
  saved?: {
    state: Pick<BackendSavedViewResponse["state"], "kind" | "positions" | "collapsedGroupIds">;
  },
  expanded = false,
) {
  const positions = saved?.state.positions ?? [];
  const groups = expanded ? [] : (saved?.state.collapsedGroupIds ?? []);
  const visible = nodes.filter((n) => {
    if (saved?.state.kind === "database") return !n.parentId || !groups.includes(n.parentId);
    const tx = n.attributes.transactionContext;
    return !(
      tx &&
      typeof tx === "object" &&
      "status" in tx &&
      tx.status === "known" &&
      "transactionId" in tx &&
      groups.includes(String(tx.transactionId))
    );
  });
  const ids = new Set(visible.map((n) => n.id));
  return {
    nodes: visible.map((n) => {
      const p = positions.find((p) => p.nodeId === n.id);
      return p ? { ...n, x: p.x, y: p.y } : n;
    }),
    edges: edges.filter((e) => ids.has(e.from) && ids.has(e.to)),
    hidden: nodes.length - visible.length,
  };
}
