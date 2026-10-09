import type { DiagramLayoutResult, DiagramLayoutPoint } from "../../diagram/elkLayout";
import { overlaySavedPositions } from "../backendSavedViewLayout";
import type { Camera } from "./navigation";

type Position = DiagramLayoutPoint & { nodeId: string };
type Anchor = DiagramLayoutPoint & { id: string };
export function revealScenarioStart(
  camera: Camera,
  start: DiagramLayoutPoint & { width: number; height: number },
  viewport: { width: number; height: number },
  minimumZoom = 0,
): Camera {
  if (camera.zoom < minimumZoom) camera = { ...camera, zoom: minimumZoom };
  const x = camera.x + start.x * camera.zoom,
    y = camera.y + start.y * camera.zoom;
  if (
    x >= 0 &&
    y >= 0 &&
    x + start.width * camera.zoom <= viewport.width &&
    y + start.height * camera.zoom <= viewport.height
  )
    return camera;
  return {
    ...camera,
    x: 32 - start.x * camera.zoom,
    y: viewport.height / 2 - (start.y + start.height / 2) * camera.zoom,
  };
}
export function positionSavedScene(layout: DiagramLayoutResult, positions: Position[]) {
  const byId = new Map(layout.nodes.map((n) => [n.id, n]));
  const anchor = positions.find((p) => byId.has(p.nodeId));
  if (!anchor) return layout;
  const original = byId.get(anchor.nodeId)!;
  const dx = anchor.x - original.x,
    dy = anchor.y - original.y;
  const translate = <T extends DiagramLayoutPoint>(p: T): T => ({ ...p, x: p.x + dx, y: p.y + dy });
  // Move the automatic scene as a whole first, retaining ELK routes and spacing.
  const aligned = {
    nodes: layout.nodes.map(translate),
    edges: layout.edges.map((e) => ({
      ...e,
      points: e.points.map(translate),
      ...(e.label ? { label: translate(e.label) } : {}),
    })),
  };
  const fixed = new Map(positions.map((p) => [p.nodeId, p]));
  const occupied = aligned.nodes
    .filter((n) => fixed.has(n.id))
    .map((n) => ({ ...n, x: fixed.get(n.id)!.x, y: fixed.get(n.id)!.y }));
  const adjusted: Position[] = [];
  for (const node of aligned.nodes) {
    const saved = fixed.get(node.id);
    const placed = { ...node, ...(saved ? { x: saved.x, y: saved.y } : {}) };
    if (!saved) {
      // Only automatic cards yield to saved cards; saved coordinates are authoritative.
      let collision;
      while (
        (collision = occupied.find(
          (n) =>
            placed.x < n.x + n.width + 32 &&
            placed.x + placed.width + 32 > n.x &&
            placed.y < n.y + n.height + 32 &&
            placed.y + placed.height + 32 > n.y,
        ))
      )
        placed.y = collision.y + collision.height + 32;
      occupied.push(placed);
    }
    if (placed.x !== node.x || placed.y !== node.y)
      adjusted.push({ nodeId: node.id, x: placed.x, y: placed.y });
  }
  return adjusted.length ? overlaySavedPositions(aligned, adjusted) : aligned;
}
export function preserveAnchorCamera(
  camera: Camera,
  previous: Anchor[],
  next: Anchor[],
  anchorId?: string,
): Camera {
  const before = previous.find((n) => n.id === anchorId);
  const after = next.find((n) => n.id === anchorId);
  return before && after
    ? {
        ...camera,
        x: camera.x + (before.x - after.x) * camera.zoom,
        y: camera.y + (before.y - after.y) * camera.zoom,
      }
    : camera;
}
