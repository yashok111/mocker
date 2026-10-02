// @vitest-environment node
import { describe, expect, it, vi } from "vitest";
import { layoutDiagram, type DiagramLayoutInput, type DiagramLayoutResult } from "./elkLayout";

type Point = { x: number; y: number };
type Box = Point & { width: number; height: number };

function fixture(): DiagramLayoutInput {
  const nodes = [
    "orders",
    "billing",
    "notifications",
    "broker",
    "kafka",
    "publish",
    "bill",
    "notify",
    "topic",
    "retry",
    "dlq",
    "event",
    "headers",
    "key",
    "payload",
    "http",
    "state",
  ];
  const links: [string, string, string, string?][] = [
    ["api", "notify", "http"],
    ["dlq-event", "dlq", "event"],
    ["topic-event", "topic", "event"],
    ["retry-event", "retry", "event"],
    ["billing-dlq", "bill", "dlq", "DLQ"],
    ["notify-dlq", "notify", "dlq", "DLQ"],
    ["headers-edge", "event", "headers", "HEADERS"],
    ["key-edge", "event", "key", "KEY"],
    ["owns-bill", "billing", "bill"],
    ["owns-notify", "notifications", "notify"],
    ["owns-publish", "orders", "publish"],
    ["payload-edge", "event", "payload", "PAYLOAD"],
    ["receive-bill", "topic", "bill", "Получает · OrderCreated"],
    ["receive-notify", "topic", "notify", "Получает · OrderCreated"],
    ["notify-retry", "notify", "retry", "retry"],
    ["send", "publish", "topic", "Публикует · OrderCreated"],
    ["server-dlq", "kafka", "dlq"],
    ["server-topic", "kafka", "topic"],
    ["server-retry", "kafka", "retry"],
    ["state-edge", "notify", "state"],
  ];
  return {
    nodes: nodes.map((id) => ({ id, width: 190, height: 72 })),
    edges: links.map(([id, source, target, text]) => ({
      id,
      source,
      target,
      ...(text
        ? {
            label: { text, width: text.length > 10 ? 160 : 65, height: text.length > 10 ? 42 : 28 },
          }
        : {}),
    })),
  };
}

function segments(edge: DiagramLayoutResult["edges"][number]) {
  return edge.points.slice(1).map((to, index) => ({ from: edge.points[index]!, to }));
}

function intersectsBox(from: Point, to: Point, box: Box) {
  const left = box.x + 0.01;
  const right = box.x + box.width - 0.01;
  const top = box.y + 0.01;
  const bottom = box.y + box.height - 0.01;
  if (from.x === to.x)
    return (
      from.x > left &&
      from.x < right &&
      Math.max(from.y, to.y) > top &&
      Math.min(from.y, to.y) < bottom
    );
  return (
    from.y > top &&
    from.y < bottom &&
    Math.max(from.x, to.x) > left &&
    Math.min(from.x, to.x) < right
  );
}

function overlaps(a: Box, b: Box) {
  return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

describe("diagram layout", () => {
  it("keeps semantic ports fixed and scopes matching port names to each node", async () => {
    const result = await layoutDiagram({
      nodes: [
        { id: "a", width: 240, height: 112, ports: [{ id: "out", x: 240, y: 37, side: "EAST" }] },
        {
          id: "b",
          width: 240,
          height: 112,
          ports: [
            { id: "in", x: 0, y: 56, side: "WEST" },
            { id: "out", x: 240, y: 75, side: "EAST" },
          ],
        },
        { id: "c", width: 240, height: 112, ports: [{ id: "in", x: 0, y: 56, side: "WEST" }] },
      ],
      edges: [
        { id: "first", source: "a", target: "b", sourcePort: "out", targetPort: "in" },
        { id: "second", source: "b", target: "c", sourcePort: "out", targetPort: "in" },
      ],
    });
    const [a, b, c] = result.nodes;
    expect(result.edges[0]!.points[0]).toEqual({ x: a!.x + 240, y: a!.y + 37 });
    expect(result.edges[0]!.points.at(-1)).toEqual({ x: b!.x, y: b!.y + 56 });
    expect(result.edges[1]!.points[0]).toEqual({ x: b!.x + 240, y: b!.y + 75 });
    expect(result.edges[1]!.points.at(-1)).toEqual({ x: c!.x, y: c!.y + 56 });
  });

  it("rejects an unknown port without letting ELK choose another attachment", async () => {
    await expect(
      layoutDiagram({
        nodes: [{ id: "a", width: 100, height: 60 }],
        edges: [{ id: "loop", source: "a", target: "a", sourcePort: "missing" }],
      }),
    ).rejects.toThrow("Invalid diagram layout input");
  });

  it("routes the branching Kafka map without foreign labels, cards, or shared segments", async () => {
    const input = fixture();
    const result = await layoutDiagram(input);
    expect(result.nodes.map((node) => node.id)).toEqual(input.nodes.map((node) => node.id));
    expect(result.edges.map((edge) => edge.id)).toEqual(input.edges.map((edge) => edge.id));
    for (const edge of result.edges) {
      if (edge.label) {
        for (const node of result.nodes)
          expect(overlaps(edge.label, node), `label ${edge.id} / ${node.id}`).toBe(false);
        for (const other of result.edges)
          if (other.id !== edge.id && other.label)
            expect(overlaps(edge.label, other.label), `label ${edge.id} / ${other.id}`).toBe(false);
      }
      for (const { from, to } of segments(edge)) {
        expect(from.x === to.x || from.y === to.y, edge.id).toBe(true);
        for (const node of result.nodes)
          expect(intersectsBox(from, to, node), `${edge.id} / ${node.id}`).toBe(false);
        for (const other of result.edges) {
          if (other.id === edge.id) continue;
          if (other.label)
            expect(intersectsBox(from, to, other.label), `${edge.id} / label ${other.id}`).toBe(
              false,
            );
          for (const segment of segments(other)) {
            if (from.x === to.x && segment.from.x === segment.to.x && from.x === segment.from.x) {
              const overlap =
                Math.min(Math.max(from.y, to.y), Math.max(segment.from.y, segment.to.y)) -
                Math.max(Math.min(from.y, to.y), Math.min(segment.from.y, segment.to.y));
              expect(overlap, `${edge.id} / ${other.id} shared vertical`).toBeLessThan(24);
            }
            if (from.y === to.y && segment.from.y === segment.to.y && from.y === segment.from.y) {
              const overlap =
                Math.min(Math.max(from.x, to.x), Math.max(segment.from.x, segment.to.x)) -
                Math.max(Math.min(from.x, to.x), Math.min(segment.from.x, segment.to.x));
              expect(overlap, `${edge.id} / ${other.id} shared horizontal`).toBeLessThan(24);
            }
          }
        }
      }
    }
    expect(await layoutDiagram(input)).toEqual(result);
  });

  it("preserves disconnected nodes and accepts an empty map without mutating input", async () => {
    expect(await layoutDiagram({ nodes: [], edges: [] })).toEqual({ nodes: [], edges: [] });
    const input = { nodes: [{ id: "alone", width: 190, height: 72 }], edges: [] };
    const snapshot = structuredClone(input);
    const result = await layoutDiagram(input);
    expect(result.nodes).toEqual([
      { id: "alone", x: expect.any(Number), y: expect.any(Number), width: 190, height: 72 },
    ]);
    expect(input).toEqual(snapshot);
  });

  it("rejects invalid endpoints and non-finite sizes", async () => {
    await expect(
      layoutDiagram({
        nodes: [{ id: "a", width: 190, height: 72 }],
        edges: [{ id: "e", source: "a", target: "missing" }],
      }),
    ).rejects.toThrow("Invalid diagram layout input");
    await expect(
      layoutDiagram({ nodes: [{ id: "a", width: NaN, height: 72 }], edges: [] }),
    ).rejects.toThrow("Invalid diagram layout input");
  });

  it("rejects an incomplete route instead of silently drawing a straight fallback", async () => {
    const { default: ELK } = await import("elkjs/lib/elk.bundled.js");
    const layout = vi.spyOn(ELK.prototype, "layout").mockResolvedValueOnce({
      id: "root",
      children: [
        { id: "a", x: 0, y: 0, width: 190, height: 72 },
        { id: "b", x: 300, y: 0, width: 190, height: 72 },
      ],
      edges: [{ id: "e", sources: ["a"], targets: ["b"], sections: [] }],
    });
    try {
      await expect(
        layoutDiagram({
          nodes: [
            { id: "a", width: 190, height: 72 },
            { id: "b", width: 190, height: 72 },
          ],
          edges: [{ id: "e", source: "a", target: "b" }],
        }),
      ).rejects.toThrow("Diagram layout returned an incomplete route: e");
    } finally {
      layout.mockRestore();
    }
  });
});
