import { expect, it } from "vitest";
import { savedCanvas } from "./savedPresentation";
import type { MapNode } from "./model";
const node = (id: string, attributes: Record<string, unknown> = {}): MapNode => ({
  id,
  kind: "flow_step",
  name: id,
  parentId: "flow",
  description: "",
  childCount: 0,
  attributes,
});
it("restores saved coordinates and transaction collapse without removing accessible records", () => {
  const nodes = [
    node("inside", { transactionContext: { status: "known", transactionId: "tx" } }),
    node("outside"),
  ];
  const edges = [{ id: "edge", kind: "next", from: "inside", to: "outside", label: "" }];
  const saved = {
    state: {
      kind: "flow" as const,
      positions: [
        { nodeId: "outside", x: 300, y: -20 },
        { nodeId: "offpage", x: 5, y: 7 },
      ],
      collapsedGroupIds: ["tx"],
    },
  };
  const canvas = savedCanvas(nodes, edges, saved);
  expect(canvas.nodes).toHaveLength(1);
  expect(canvas.nodes[0]).toMatchObject({ id: "outside", x: 300, y: -20 });
  expect(canvas.edges).toEqual([]);
  expect(nodes).toHaveLength(2);
  expect(saved.state.positions).toHaveLength(2);
  expect(savedCanvas(nodes, edges, saved, true).nodes).toHaveLength(2);
});
it("collapses only tables in declared saved database schema groups", () => {
  const nodes = [
    { ...node("a"), kind: "table", parentId: "hidden-schema" },
    { ...node("b"), kind: "table", parentId: "visible-schema" },
  ];
  const saved = {
    state: { kind: "database" as const, positions: [], collapsedGroupIds: ["hidden-schema"] },
  };
  expect(savedCanvas(nodes, [], saved).nodes.map((n) => n.id)).toEqual(["b"]);
});
