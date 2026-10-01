import { expect, it } from "vitest";
import type { BackendNode, BackendEdge } from "@/api/generated/schemas";
import { buildFlowScene, flowStepContext } from "./backendFlowLayout";

it("preserves source branch labels and unknown condition/transaction context", () => {
  const node = {
    id: "condition",
    name: "Guard",
    attributes: {
      expression: { status: "unknown", reason: "Dynamic guard" },
      transactionContext: { status: "unknown", reason: "No source proof" },
    },
  } as BackendNode;
  const edge = {
    id: "branch",
    kind: "branch",
    from: "condition",
    to: "condition",
    attributes: { label: "if allowed", condition: { status: "unknown", reason: "Dynamic guard" } },
  } as BackendEdge;
  expect(buildFlowScene([node], [edge]).input.edges[0]?.label?.text).toBe("branch · if allowed");
  expect(flowStepContext(node)).toEqual([
    "Условие неизвестно: Dynamic guard",
    "Транзакция неизвестна: No source proof",
  ]);
});

it("caps canvas without losing off-page and over-limit transitions as boundary entries", () => {
  const nodes = Array.from(
    { length: 201 },
    (_, i) => ({ id: `step${i}`, name: `Step${i}`, attributes: {} }) as BackendNode,
  );
  const edges = Array.from(
    { length: 602 },
    (_, i) =>
      ({
        id: `edge${i}`,
        from: "step0",
        to: i === 601 ? "off-page" : "step1",
        kind: "next",
        attributes: {},
      }) as BackendEdge,
  );
  const scene = buildFlowScene(nodes, edges);
  expect(scene.input.nodes).toHaveLength(200);
  expect(scene.input.edges).toHaveLength(600);
  expect(scene.excludedNodes).toBe(1);
  expect(scene.boundaries.map((edge) => edge.id)).toEqual(["edge600", "edge601"]);
});
