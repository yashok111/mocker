// @vitest-environment node
import { expect, it } from "vitest";
import { positionSavedScene, preserveAnchorCamera, revealScenarioStart } from "./sceneLayout";
import type { DiagramLayoutResult } from "../../diagram/elkLayout";

const layout: DiagramLayoutResult = {
  nodes: [
    { id: "handler", x: 20, y: 20, width: 248, height: 146 },
    { id: "input", x: 420, y: 20, width: 248, height: 146 },
    { id: "call", x: 820, y: 20, width: 248, height: 146 },
  ],
  edges: [
    {
      id: "next",
      source: "input",
      target: "call",
      points: [
        { x: 668, y: 93 },
        { x: 820, y: 93 },
      ],
    },
  ],
};
it("aligns the automatic scene and routes with a saved step instead of overlapping it", () => {
  const result = positionSavedScene(layout, [{ nodeId: "input", x: 120, y: 60 }]);
  expect(result.nodes.map(({ id, x, y }) => ({ id, x, y }))).toEqual([
    { id: "handler", x: -280, y: 60 },
    { id: "input", x: 120, y: 60 },
    { id: "call", x: 520, y: 60 },
  ]);
  expect(result.edges[0]?.points).toEqual([
    { x: 368, y: 133 },
    { x: 520, y: 133 },
  ]);
  expect(layout.nodes[1]?.x).toBe(420);
});
it("keeps every fixed position while moving automatic cards clear of other saved anchors", () => {
  const result = positionSavedScene(layout, [
    { nodeId: "input", x: 420, y: 20 },
    { nodeId: "handler", x: 820, y: 20 },
  ]);
  expect(result.nodes[0]).toMatchObject({ x: 820, y: 20 });
  expect(result.nodes[1]).toMatchObject({ x: 420, y: 20 });
  expect(result.nodes[2]!.y).toBeGreaterThanOrEqual(198);
  expect(result.edges[0]?.points.at(-1)?.y).toBe(result.nodes[2]!.y + 73);
});
it("ignores positions outside the scene and leaves automatic layout untouched", () => {
  expect(positionSavedScene(layout, [{ nodeId: "other", x: 0, y: 0 }])).toBe(layout);
});
it("keeps the expanded function at the same screen position and zoom after relayout", () => {
  const camera = { x: 50, y: -100, zoom: 0.75 };
  const next = preserveAnchorCamera(
    camera,
    [{ id: "call", x: 200, y: 80 }],
    [{ id: "call", x: 600, y: 480 }],
    "call",
  );
  expect(next).toEqual({ x: -250, y: -400, zoom: 0.75 });
  expect(preserveAnchorCamera(camera, [], layout.nodes, "missing")).toBe(camera);
});
it("opens a long scenario at its entrypoint instead of cropping both ends around the middle", () => {
  const camera = { x: -1500, y: 100, zoom: 0.65 };
  const start = { x: 20, y: 100, width: 248, height: 146 };
  expect(revealScenarioStart(camera, start, { width: 1000, height: 600 })).toEqual({
    x: 19,
    y: 187.55,
    zoom: 0.65,
  });
  const visible = { x: 50, y: 100, zoom: 1 };
  expect(revealScenarioStart(visible, start, { width: 1000, height: 600 })).toBe(visible);
});

it("opens an authored algorithm at a readable scale while keeping its start visible", () => {
  const start = { x: 20, y: 1400, width: 248, height: 146 };
  const camera = revealScenarioStart(
    { x: 12, y: -80, zoom: 0.25 },
    start,
    { width: 1000, height: 700 },
    0.8,
  );
  expect(camera.zoom).toBe(0.8);
  expect(camera.x + start.x * camera.zoom).toBeGreaterThanOrEqual(0);
  expect(camera.y + start.y * camera.zoom).toBeGreaterThanOrEqual(0);
  expect(camera.y + (start.y + start.height) * camera.zoom).toBeLessThanOrEqual(700);
});
