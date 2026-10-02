// @vitest-environment node
import { describe, expect, it } from "vitest";
import { impactGraphModel } from "./model";
import { reportFixture } from "./fixtures.test-support";
import { layoutDiagram } from "../diagram/elkLayout";
import { impactLayoutInput } from "./layout";

describe("impactGraphModel", () => {
  it("uses the server reference chain, side and direction without adding unrelated dependencies", () => {
    const report = reportFixture();
    const graph = impactGraphModel(report, report.changes[0]!);
    expect(graph.nodes.map((node) => node.label)).toContain("/paths/~1orders/get/responses/200");
    expect(graph.nodes.map((node) => node.label)).toContain("GET /orders");
    expect(graph.nodes.map((node) => node.label)).not.toContain("/info/description");
    expect(graph.edges[0]!.label).toContain("Было");
    expect(graph.edges[0]!.label).toContain("Ответ");
    expect(graph.truncated).toBe(false);
    expect(graph.nodes.every((node) => !("x" in node) && !("y" in node))).toBe(true);
    expect(graph.edges.every((edge) => !("vertices" in edge))).toBe(true);
  });

  it("caps nodes at 100 and never creates dangling edges", () => {
    const report = reportFixture();
    report.evidence = Array.from({ length: 140 }, (_, index) => ({
      ...report.evidence[0]!,
      id: `e${index}`,
    }));
    const graph = impactGraphModel(report, report.changes[0]!);
    expect(graph.nodes.length).toBeLessThanOrEqual(100);
    expect(graph.truncated).toBe(true);
    const ids = new Set(graph.nodes.map((node) => node.id));
    expect(graph.edges.every((edge) => ids.has(edge.source) && ids.has(edge.target))).toBe(true);
    expect(report.evidence).toHaveLength(140);
  });

  it("lays out twelve fan-out branches with labels clear of foreign routes, cards and each other", async () => {
    const report = reportFixture();
    report.evidence = Array.from({ length: 12 }, (_, index) => ({
      ...report.evidence[0]!,
      id: `e${index}`,
      side: index % 2 === 0 ? "before" : "after",
      direction: index % 3 === 0 ? "unknown" : "response",
    }));
    const graph = impactGraphModel(report, report.changes[0]!);
    const layout = await layoutDiagram(impactLayoutInput(graph));
    for (let index = 0; index < layout.nodes.length; index++) {
      for (const other of layout.nodes.slice(index + 1))
        expect(overlaps(layout.nodes[index]!, other)).toBe(false);
    }
    for (const edge of layout.edges) {
      for (const node of layout.nodes) {
        if (node.id === edge.source || node.id === edge.target) continue;
        for (let index = 1; index < edge.points.length; index++) {
          const start = edge.points[index - 1]!,
            end = edge.points[index]!;
          expect(
            overlaps(
              { x: node.x + 1, y: node.y + 1, width: node.width - 2, height: node.height - 2 },
              {
                x: Math.min(start.x, end.x),
                y: Math.min(start.y, end.y),
                width: Math.max(0.01, Math.abs(start.x - end.x)),
                height: Math.max(0.01, Math.abs(start.y - end.y)),
              },
            ),
          ).toBe(false);
        }
      }
    }
    const boxes = layout.edges
      .filter((edge) => edge.label)
      .map((edge) => {
        const box = edge.label!;
        for (const node of layout.nodes) expect(overlaps(box, node)).toBe(false);
        for (const route of layout.edges) {
          if (route.id === edge.id) continue;
          for (let i = 1; i < route.points.length; i++) {
            const a = route.points[i - 1]!;
            const b = route.points[i]!;
            expect(
              overlaps(box, {
                x: Math.min(a.x, b.x),
                y: Math.min(a.y, b.y),
                width: Math.max(1, Math.abs(a.x - b.x)),
                height: Math.max(1, Math.abs(a.y - b.y)),
              }),
            ).toBe(false);
          }
        }
        return box;
      });
    expect(boxes).toHaveLength(12);
    for (let i = 0; i < boxes.length; i++) {
      for (const other of boxes.slice(i + 1)) expect(overlaps(boxes[i]!, other)).toBe(false);
    }
  });
});

function overlaps(
  a: { x: number; y: number; width: number; height: number },
  b: { x: number; y: number; width: number; height: number },
) {
  return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}
