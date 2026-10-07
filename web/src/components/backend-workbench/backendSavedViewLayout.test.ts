// @vitest-environment node
import { expect, it } from "vitest";
import {
  overlaySavedPositions,
  updateSavedPosition,
  acceptAutomaticPositions,
  collapseFlowScene,
  collapseDatabaseScene,
} from "./backendSavedViewLayout";
import { buildFlowScene } from "./backendFlowLayout";
import { buildDatabaseScene } from "./backendDatabaseLayout";
import type { BackendNode, BackendDatabaseTableItem } from "@/api/generated/schemas";
const layout = {
  nodes: [
    { id: "a", x: 0, y: 0, width: 260, height: 120 },
    { id: "b", x: 400, y: 0, width: 260, height: 120 },
  ],
  edges: [
    {
      id: "edge",
      source: "a",
      target: "b",
      points: [
        { x: 260, y: 60 },
        { x: 400, y: 60 },
      ],
    },
  ],
};
it("overlays manual coordinates and reroutes incident edges without changing the semantic graph", () => {
  const result = overlaySavedPositions(layout, [
    { nodeId: "a", x: 10, y: 100 },
    { nodeId: "offpage", x: -5, y: 1 },
  ]);
  expect(result.nodes[0]).toMatchObject({ x: 10, y: 100 });
  expect(result.edges[0]).toMatchObject({ id: "edge", source: "a", target: "b" });
  expect(result.edges[0]?.points[0]).toEqual({ x: 270, y: 160 });
});
it("translates a saved self reference and its label without cutting through its table", () => {
  const loop = {
    id: "self",
    source: "a",
    target: "a",
    points: [
      { x: 40, y: 0 },
      { x: 40, y: -60 },
      { x: 200, y: -60 },
      { x: 200, y: 0 },
    ],
    label: { x: 70, y: -90, width: 100, height: 24, text: "parent_id → id" },
  };
  const result = overlaySavedPositions({ nodes: layout.nodes, edges: [loop] }, [
    { nodeId: "a", x: 300, y: 150 },
  ]);
  expect(result.edges[0]?.points).toEqual(loop.points.map((p) => ({ x: p.x + 300, y: p.y + 150 })));
  expect(result.edges[0]?.label).toEqual({ ...loop.label, x: 370, y: 60 });
  expect(loop.points[0]).toEqual({ x: 40, y: 0 });
});
it("keeps off-page coordinates when accepting ELK, and rejects silent eviction or invalid moves", () => {
  const positions = [
    { nodeId: "a", x: 1, y: 2 },
    { nodeId: "offpage", x: 3, y: 4 },
  ];
  expect(acceptAutomaticPositions(positions, ["a"])).toEqual([positions[1]]);
  expect(() =>
    updateSavedPosition(
      Array.from({ length: 200 }, (_, i) => ({ nodeId: String(i), x: 0, y: 0 })),
      { nodeId: "new", x: 0, y: 0 },
    ),
  ).toThrow(/200/);
  expect(() => updateSavedPosition([], { nodeId: "a", x: Infinity, y: 0 })).toThrow();
});
it("collapses only real known transaction members before caps without synthetic edges", () => {
  const nodes = [
    { id: "a", attributes: { transactionContext: { status: "known", transactionId: "tx" } } },
    { id: "b", attributes: { transactionContext: { status: "unknown", reason: "unknown" } } },
  ] as BackendNode[];
  const edges = [{ id: "edge", from: "a", to: "b", kind: "flows_to", attributes: {} }] as never;
  const collapsed = collapseFlowScene(nodes, edges, ["tx"]);
  const scene = buildFlowScene(collapsed.nodes, collapsed.edges);
  expect(scene.input.nodes.map((n) => n.id)).toEqual(["b"]);
  expect(scene.input.edges).toEqual([]);
  expect(collapsed.hiddenNodes).toBe(1);
  expect(collapsed.hiddenEdges).toBe(1);
});
it("uses actual schema membership and keeps schemaless tables with distinct cap counts", () => {
  const tables = [
    { tableId: "a", schemaId: "schema" },
    { tableId: "b", schemaId: "" },
    ...Array.from({ length: 201 }, (_, i) => ({ tableId: String(i), schemaId: "other" })),
  ] as BackendDatabaseTableItem[];
  const collapsed = collapseDatabaseScene(tables, [], ["schema"]);
  const scene = buildDatabaseScene(collapsed.tables, collapsed.relationships);
  expect(collapsed.hiddenNodes).toBe(1);
  expect(scene.input.nodes[0]?.id).toBe("b");
  expect(scene.excludedTables).toBe(2);
});
