import type { Graph } from "@antv/x6";
import { describe, expect, it, vi } from "vitest";
import { applyDiagramRoutes, diagramRouteOptions } from "./elkX6";
import type { DiagramLayoutResult } from "./elkLayout";

const layout: DiagramLayoutResult = {
  nodes: [
    { id: "a", x: 10, y: 20, width: 100, height: 60 },
    { id: "b", x: 300, y: 40, width: 100, height: 60 },
  ],
  edges: [
    {
      id: "ab",
      source: "a",
      target: "b",
      points: [
        { x: 110, y: 50 },
        { x: 220, y: 50 },
        { x: 220, y: 70 },
        { x: 300, y: 70 },
      ],
      label: { text: "Да", x: 140, y: 36, width: 50, height: 28 },
    },
  ],
};

function fixture() {
  const nodes = layout.nodes.map((node) => ({
    id: node.id,
    bounds: { ...node },
    getBBox() {
      return this.bounds;
    },
    setZIndex: vi.fn(),
  }));
  const props: Record<string, unknown> = {
    source: { cell: "a", port: "old" },
    target: "b",
    router: { name: "manhattan" },
  };
  // X6's object overload deep-merges nested props, retaining obsolete ports and
  // label markup. Require its key/value replacement API at this boundary.
  const edge = {
    id: "ab",
    isEdge: () => true,
    setProp: vi.fn((key: string, value: unknown) => {
      expect(typeof key).toBe("string");
      props[key] = value;
    }),
  };
  const graph = {
    getNodes: () => nodes,
    getEdges: () => [edge],
    getCellById: (id: string) => (id === edge.id ? edge : nodes.find((node) => node.id === id)),
    batchUpdate: (_: string, action: () => void) => action(),
  } as unknown as Graph;
  return { graph, nodes, edge, props };
}

describe("ELK routes in X6", () => {
  it("replaces old ports, endpoints, bend points and label bounds", () => {
    const { graph, props } = fixture();
    expect(applyDiagramRoutes(graph, layout)).toBe(true);
    expect(props.source).toEqual({
      cell: "a",
      anchor: { name: "topLeft", args: { dx: 100, dy: 30 } },
      connectionPoint: "anchor",
    });
    expect(props.target).toEqual({
      cell: "b",
      anchor: { name: "topLeft", args: { dx: 0, dy: 30 } },
      connectionPoint: "anchor",
    });
    expect(props.vertices).toEqual([
      { x: 220, y: 50 },
      { x: 220, y: 70 },
    ]);
    expect(props.labels).toMatchObject([
      {
        position: { distance: 0, offset: { x: 55, y: 0 } },
        attrs: { body: { width: 50, height: 28 } },
      },
    ]);
  });

  it("leaves every route untouched once a saved card has moved", () => {
    const { graph, nodes, edge } = fixture();
    nodes[1]!.bounds.x = 500;
    expect(applyDiagramRoutes(graph, layout)).toBe(false);
    expect(edge.setProp).not.toHaveBeenCalled();
    expect(nodes[0]!.setZIndex).not.toHaveBeenCalled();
  });

  it("accepts rounded persisted coordinates and keeps the reserved geometry", () => {
    const { graph, nodes, props } = fixture();
    nodes[1]!.bounds.x = 300.4;
    nodes[1]!.bounds.y = 39.6;
    expect(applyDiagramRoutes(graph, layout)).toBe(true);
    expect(props.router).toEqual({ name: "normal" });
    expect(diagramRouteOptions(layout, layout.edges[0]!).labels).toHaveLength(1);
  });
});
