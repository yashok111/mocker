import { expect, it } from "vitest";
import type { DesignScenarioEventMapReport } from "@/api/generated/schemas";
import { GRAPH_EDGE_CAP, GRAPH_NODE_CAP, graphSubset } from "./eventMapSubset";

it("caps the graph without inventing edges or dropping the full report", () => {
  const nodes = Array.from({ length: 600 }, (_, index) => ({
    id: `channel:${index}`,
    kind: "channel" as const,
    label: `Topic ${index}`,
    locator: { pointer: `/eventModel/channels/${index}` },
  }));
  const edges = Array.from({ length: 500 }, (_, index) => ({
    id: `link:${index}`,
    kind: "channel_message" as const,
    source: `channel:${index}`,
    target: `channel:${index + 1}`,
    label: "link",
    locator: { pointer: `/eventModel/channels/${index}` },
  }));
  const report: DesignScenarioEventMapReport = {
    scenarioId: 7,
    version: 1,
    proposed: false,
    nodes,
    edges,
    diagnostics: [],
    coverage: {
      nodesReturned: nodes.length,
      edgesReturned: edges.length,
      diagnosticsReturned: 0,
      truncatedReasons: [],
    },
    complete: true,
  };
  const subset = graphSubset(report);
  expect(subset.nodes).toHaveLength(GRAPH_NODE_CAP);
  expect(subset.edges.length).toBeLessThanOrEqual(GRAPH_EDGE_CAP);
  const ids = new Set(subset.nodes.map((node) => node.id));
  expect(subset.edges.every((edge) => ids.has(edge.source) && ids.has(edge.target))).toBe(true);
  expect(report.nodes).toHaveLength(600);
  expect(report.edges).toHaveLength(500);
  expect(graphSubset(report)).toEqual(subset);
});
