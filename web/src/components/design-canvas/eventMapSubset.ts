import type {
  DesignScenarioEventMapEdge,
  DesignScenarioEventMapNode,
  DesignScenarioEventMapReport,
} from "@/api/generated/schemas";

export type GraphSubset = {
  nodes: DesignScenarioEventMapNode[];
  edges: DesignScenarioEventMapEdge[];
};
export const GRAPH_NODE_CAP = 120;
export const GRAPH_EDGE_CAP = 180;
export const EVENT_MAP_COLUMNS: DesignScenarioEventMapNode["kind"][] = [
  "participant",
  "server",
  "operation",
  "channel",
  "message",
  "schema",
  "api_operation",
  "state_transition",
];

export function graphSubset(report: DesignScenarioEventMapReport): GraphSubset {
  const groups = EVENT_MAP_COLUMNS.map((kind) => report.nodes.filter((node) => node.kind === kind));
  const nodes: DesignScenarioEventMapNode[] = [];
  for (let row = 0; nodes.length < GRAPH_NODE_CAP; row++) {
    let added = false;
    for (const group of groups) {
      const node = group[row];
      if (node && nodes.length < GRAPH_NODE_CAP) {
        nodes.push(node);
        added = true;
      }
    }
    if (!added) break;
  }
  const ids = new Set(nodes.map((node) => node.id));
  return {
    nodes,
    edges: report.edges
      .filter((edge) => ids.has(edge.source) && ids.has(edge.target))
      .slice(0, GRAPH_EDGE_CAP),
  };
}
