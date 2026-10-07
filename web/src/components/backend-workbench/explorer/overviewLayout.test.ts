import { expect, it } from "vitest";
import { sourceOverviewLayout } from "./overviewLayout";
import type { MapNode, MapEdge } from "./model";
import type { DiagramLayoutPoint } from "../../diagram/elkLayout";

function overviewFixture(count: number) {
  const node = (id: string, kind: string, parentId: string | null): MapNode => ({
    id,
    kind,
    parentId,
    name: id,
    description: "",
    attributes: {},
    childCount: 0,
  });
  const nodes = [
    node("system", "system", null),
    node("app", "service", "system"),
    ...Array.from({ length: count }, (_, i) => node(`store-${i}`, "datastore", "app")),
  ];
  const edges: MapEdge[] = nodes.slice(1).map((n) => ({
    id: `contains-${n.id}`,
    from: n.parentId!,
    to: n.id,
    kind: "contains",
    label: "",
  }));
  return { nodes, edges };
}
function segments(points: DiagramLayoutPoint[]) {
  return points.slice(1).map((to, i) => ({ from: points[i]!, to }));
}

it.each([2, 3, 5, 6, 9])(
  "routes %i children independently without shared segments or passing through other cards",
  (count) => {
    const fixture = overviewFixture(count);
    const layout = sourceOverviewLayout(fixture.nodes, fixture.edges)!;
    expect(layout.edges.map((e) => e.id)).toEqual(fixture.edges.map((e) => e.id));
    const all = layout.edges.flatMap((e) => segments(e.points).map((s) => ({ ...s, edge: e })));
    for (const s of all) {
      const vertical = s.from.x === s.to.x;
      expect(vertical || s.from.y === s.to.y).toBe(true);
      expect(s.from).not.toEqual(s.to);
      for (const card of layout.nodes.filter(
        (n) => !["system", s.edge.source, s.edge.target].includes(n.id),
      )) {
        const crosses = vertical
          ? s.from.x > card.x &&
            s.from.x < card.x + card.width &&
            Math.max(s.from.y, s.to.y) > card.y &&
            Math.min(s.from.y, s.to.y) < card.y + card.height
          : s.from.y > card.y &&
            s.from.y < card.y + card.height &&
            Math.max(s.from.x, s.to.x) > card.x &&
            Math.min(s.from.x, s.to.x) < card.x + card.width;
        expect(crosses, `${s.edge.id} crosses ${card.id}`).toBe(false);
      }
      for (const other of all.filter((o) => o.edge.id !== s.edge.id)) {
        const sameLine = vertical
          ? other.from.x === other.to.x && s.from.x === other.from.x
          : other.from.y === other.to.y && s.from.y === other.from.y;
        const axis = vertical ? "y" : "x";
        const overlap =
          Math.min(Math.max(s.from[axis], s.to[axis]), Math.max(other.from[axis], other.to[axis])) -
          Math.max(Math.min(s.from[axis], s.to[axis]), Math.min(other.from[axis], other.to[axis]));
        expect(sameLine && overlap > 0, `${s.edge.id} overlaps ${other.edge.id}`).toBe(false);
        if (vertical && other.from.y === other.to.y) {
          const crosses =
            s.from.x >= Math.min(other.from.x, other.to.x) &&
            s.from.x <= Math.max(other.from.x, other.to.x) &&
            other.from.y >= Math.min(s.from.y, s.to.y) &&
            other.from.y <= Math.max(s.from.y, s.to.y);
          expect(crosses, `${s.edge.id} crosses ${other.edge.id}`).toBe(false);
        }
      }
    }
    const frame = layout.nodes.find((n) => n.id === "system")!;
    const start = layout.edges.find((e) => e.source === "system")!.points[0]!;
    expect(
      start.x === frame.x ||
        start.y === frame.y ||
        start.x === frame.x + frame.width ||
        start.y === frame.y + frame.height,
    ).toBe(true);
  },
);

it("leaves non-hierarchical links to the general layout rather than routing them through cards", () => {
  const { nodes, edges } = overviewFixture(6);
  edges.push({ id: "cross", from: "store-0", to: "store-5", kind: "calls", label: "" });
  expect(sourceOverviewLayout(nodes, edges)).toBeUndefined();
});
it("keeps evidenced relationships when compacting a source overview", () => {
  const n = (id: string, kind: string, parentId: string | null): MapNode => ({
    id,
    kind,
    parentId,
    name: id,
    description: "",
    attributes: {},
    childCount: 0,
  });
  const nodes = [n("s", "system", null), n("a", "service", "s"), n("d", "datastore", "a")];
  const edges: MapEdge[] = [
    { id: "owns", kind: "contains", from: "s", to: "a", label: "" },
    { id: "calls", kind: "writes", from: "a", to: "d", label: "Записывает" },
  ];
  const layout = sourceOverviewLayout(nodes, edges)!;
  expect(layout.edges.map((e) => e.id)).toEqual(["owns", "calls"]);
  expect(layout.edges[1]?.source).toBe("a");
  expect(layout.edges[1]?.target).toBe("d");
  expect(layout.nodes.every((n) => n.x >= 0 && n.y >= 0)).toBe(true);
});
