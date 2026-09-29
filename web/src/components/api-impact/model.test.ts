import { describe, expect, it } from "vitest";
import { impactGraphModel } from "./model";
import { reportFixture } from "./fixtures.test-support";

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

  it("keeps fan-out labels clear of sibling routes, cards and each other", () => {
    const report = reportFixture();
    report.evidence = Array.from({ length: 12 }, (_, index) => ({
      ...report.evidence[0]!,
      id: `e${index}`,
      side: index % 2 === 0 ? "before" : "after",
      direction: index % 3 === 0 ? "unknown" : "response",
    }));
    const graph = impactGraphModel(report, report.changes[0]!);
    const nodes = new Map(graph.nodes.map((node) => [node.id, node]));
    const routes = graph.edges.map((edge) => {
      const source = nodes.get(edge.source)!;
      const target = nodes.get(edge.target)!;
      expect(source.width).toBeGreaterThan(0);
      expect(source.height).toBeGreaterThan(0);
      const points = [
        { x: source.x + source.width, y: source.y + source.height / 2 },
        ...edge.vertices,
        { x: target.x, y: target.y + target.height / 2 },
      ];
      return { edge, points };
    });
    const boxes = routes
      .filter(({ edge }) => edge.label)
      .map(({ edge, points }) => {
        const end = points.at(-1)!;
        const start = points.at(-2)!;
        expect(start.y).toBe(end.y);
        expect(edge.labelPosition.distance).toBeLessThan(-1);
        const center = end.x + edge.labelPosition.distance;
        const width = edge.labelMaxWidth + 20;
        const height = edge.labelMaxHeight + 12;
        const box = { x: center - width / 2, y: end.y - height / 2, width, height };
        expect(box.x).toBeGreaterThan(start.x + 6);
        expect(box.x + width).toBeLessThan(end.x - 8);
        for (const node of graph.nodes) expect(overlaps(box, node)).toBe(false);
        for (const route of routes) {
          if (route.edge.id === edge.id) continue;
          for (let i = 1; i < route.points.length; i++) {
            const a = route.points[i - 1]!;
            const b = route.points[i]!;
            // A 1px-wide route is enough to catch the original shared spine
            // crossing a sibling label, including a zero-length segment.
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
