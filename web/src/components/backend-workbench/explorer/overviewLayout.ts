import type { MapNode, MapEdge } from "./model";
import { relationNames } from "./model";
import type { DiagramLayoutResult } from "../../diagram/elkLayout";

// Declared source containment is a boundary, never an invented call. Keep every
// returned relationship when using this compact layout instead of ELK's tall fanout.
export function sourceOverviewLayout(
  nodes: MapNode[],
  edges: MapEdge[] = [],
): DiagramLayoutResult | undefined {
  const roots = nodes.filter((n) => n.kind === "system");
  const apps = nodes.filter((n) => n.kind === "service");
  if (
    roots.length !== 1 ||
    apps.length !== 1 ||
    nodes.length > 11 ||
    nodes.some((n) => !["system", "service", "datastore", "external_system"].includes(n.kind))
  )
    return;
  const root = roots[0]!;
  const app = apps[0]!;
  if (app.parentId !== root.id) return;
  const members = nodes.filter((n) => n.id !== root.id && n.id !== app.id);
  if (members.some((n) => n.parentId !== app.id && n.parentId !== root.id)) return;
  const memberIds = new Set(members.map((n) => n.id));
  const pairs = edges.map((e) => `${e.from}:${e.to}`);
  // This compact layout is a hierarchy. Let ELK handle cross-links or parallel
  // relationships rather than forcing them through the hierarchy's card lanes.
  if (
    new Set(pairs).size !== pairs.length ||
    edges.some(
      (e) =>
        !(
          (e.from === root.id && (e.to === app.id || memberIds.has(e.to))) ||
          (e.from === app.id && memberIds.has(e.to))
        ),
    )
  )
    return;
  const rows = Math.max(1, Math.ceil(members.length / 2));
  const centerRow = (rows - 1) / 2;
  const width = 1120;
  const positions = [
    { id: root.id, x: 0, y: 0, width, height: 80 + rows * 180 },
    { id: app.id, x: 436, y: 80 + centerRow * 180, width: 248, height: 146 },
    ...members.map((n, i) => ({
      id: n.id,
      x: i % 2 === 0 ? 32 : 840,
      y: 80 + Math.floor(i / 2) * 180,
      width: 248,
      height: 146,
    })),
  ];
  const placed = new Map(positions.map((p) => [p.id, p]));
  const routes: DiagramLayoutResult["edges"] = edges.map((e) => {
    const a = placed.get(e.from)!;
    const b = placed.get(e.to)!;
    const row = Math.floor(members.findIndex((n) => n.id === e.to) / 2);
    const left = b.x < 436;
    let points: DiagramLayoutResult["edges"][number]["points"];
    if (e.from === root.id) {
      // Attach containment to the actual frame, never to empty header space.
      points =
        e.to === app.id
          ? [
              { x: b.x + b.width / 2, y: 0 },
              { x: b.x + b.width / 2, y: b.y },
            ]
          : [
              { x: left ? 0 : width, y: b.y + b.height / 2 },
              { x: left ? b.x : b.x + b.width, y: b.y + b.height / 2 },
            ];
    } else {
      const start = {
        x: left ? a.x : a.x + a.width,
        y: a.y + a.height / 2 + (row - centerRow) * 24,
      };
      const end = { x: left ? b.x + b.width : b.x, y: b.y + b.height / 2 };
      // Farther rows turn closer to the application, so branches neither share
      // a trunk nor cross the shorter branches on their way to the card.
      const lane = 36 + (Math.floor(centerRow) - Math.floor(Math.abs(row - centerRow))) * 24;
      const middle = start.x + (left ? -lane : lane);
      points =
        start.y === end.y
          ? [start, end]
          : [start, { x: middle, y: start.y }, { x: middle, y: end.y }, end];
    }
    const start = points[0]!;
    const end = points.at(-1)!;
    return {
      id: e.id,
      source: e.from,
      target: e.to,
      points,
      ...(e.kind === "contains" && e.from === root.id
        ? {}
        : {
            label: {
              text: e.kind === "contains" ? "Содержит" : e.label || relationNames[e.kind] || e.kind,
              x: (start.x + end.x) / 2 - 75,
              y: (start.y + end.y) / 2 - 12,
              width: 150,
              height: 24,
            },
          }),
    };
  });
  return { nodes: positions, edges: routes };
}

export function architectureOverviewLayout(
  nodes: MapNode[],
  edges: MapEdge[],
): DiagramLayoutResult | undefined {
  const roots = nodes.filter((n) => n.boundary);
  if (roots.length !== 1 || nodes.length > 12 || nodes.some((n) => n.x !== undefined)) return;
  const root = roots[0]!;
  const inside = nodes.filter((n) => n.parentId === root.id);
  const outside = nodes.filter((n) => n.id !== root.id && n.parentId !== root.id);
  if (!inside.length || outside.some((n) => n.parentId)) return;
  const columns = Math.min(inside.length, 2),
    rows = Math.ceil(inside.length / columns);
  const width = columns * 248 + (columns - 1) * 180 + 80,
    height = rows * 180 + 100;
  const positions = [
    { id: root.id, x: 0, y: 0, width, height },
    ...inside.map((n, i) => ({
      id: n.id,
      x: 40 + (i % columns) * 428,
      y: 80 + Math.floor(i / columns) * 180,
      width: 248,
      height: 146,
    })),
    ...outside.map((n, i) => ({
      id: n.id,
      x: width + 100,
      y: 80 + i * 180,
      width: 248,
      height: 146,
    })),
  ];
  const placed = new Map(positions.map((n) => [n.id, n]));
  const member = new Set(inside.map((n) => n.id));
  const routes: DiagramLayoutResult["edges"] = edges.flatMap((e) => {
    const a = placed.get(e.from),
      b = placed.get(e.to);
    if (!a || !b) return [];
    const crosses = member.has(e.from) !== member.has(e.to);
    const start = crosses
      ? { x: a.x + a.width / 2, y: a.y }
      : { x: a.x + a.width, y: a.y + a.height / 2 };
    const end = crosses ? { x: b.x + b.width / 2, y: b.y } : { x: b.x, y: b.y + b.height / 2 };
    const middle = crosses ? 52 : (start.x + end.x) / 2;
    const points = crosses
      ? [start, { x: start.x, y: middle }, { x: end.x, y: middle }, end]
      : [start, { x: middle, y: start.y }, { x: middle, y: end.y }, end];
    return [
      {
        id: e.id,
        source: e.from,
        target: e.to,
        points,
        label: {
          text: e.label || relationNames[e.kind] || e.kind,
          x: (start.x + end.x) / 2 - 75,
          y: crosses ? 28 : (start.y + end.y) / 2 - 12,
          width: 150,
          height: 24,
        },
      },
    ];
  });
  return { nodes: positions, edges: routes };
}
