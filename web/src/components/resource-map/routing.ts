export type Bounds = { x: number; y: number; width: number; height: number };
type Point = { x: number; y: number };
type Side = "left" | "right" | "top" | "bottom";
type Route = {
  sourceSide: Side;
  targetSide: Side;
  sourceOffset: { dx: number; dy: number };
  targetOffset: { dx: number; dy: number };
  vertices: Point[] | undefined;
  labelPosition: { distance: number; offset?: Point };
  labelMaxWidth: number;
  waypoints?: Point[];
};

function pathBlocked(points: Point[], obstacles: Bounds[]) {
  return obstacles.some((box) =>
    points.slice(1).some((b, i) => {
      const a = points[i]!;
      return (
        Math.max(a.x, b.x) >= box.x - 12 &&
        Math.min(a.x, b.x) <= box.x + box.width + 12 &&
        Math.max(a.y, b.y) >= box.y - 12 &&
        Math.min(a.y, b.y) <= box.y + box.height + 12
      );
    }),
  );
}

function labelBlocked(center: Point, maxWidth: number, boxes: Bounds[]) {
  // Reserve the full wrapped label plus its 10px/6px body padding. A clear
  // centerline alone does not guarantee the label clears a neighboring card.
  const halfWidth = (maxWidth + 20) / 2;
  return boxes.some(
    (box) =>
      center.x + halfWidth >= box.x - 4 &&
      center.x - halfWidth <= box.x + box.width + 4 &&
      center.y + 30 >= box.y - 4 &&
      center.y - 30 <= box.y + box.height + 4,
  );
}

function segmentPosition(points: Point[], segment: number) {
  const lengths = points
    .slice(1)
    .map((p, i) => Math.abs(p.x - points[i]!.x) + Math.abs(p.y - points[i]!.y));
  const before = lengths.slice(0, segment).reduce((sum, length) => sum + length, 0);
  return {
    distance: (before + lengths[segment]! / 2) / lengths.reduce((sum, length) => sum + length, 0),
  };
}

function outsideCandidate(source: Bounds, target: Bounds, horizontal: boolean, firstSide: boolean) {
  const sx = source.x + source.width / 2,
    sy = source.y + source.height / 2;
  const tx = target.x + target.width / 2,
    ty = target.y + target.height / 2;
  const forward = horizontal ? tx >= sx : ty >= sy;
  const zero = { dx: 0, dy: 0 };
  const route: Route = {
    sourceSide: "top",
    targetSide: "top",
    sourceOffset: zero,
    targetOffset: zero,
    vertices: [],
    labelPosition: { distance: 0.5 },
    labelMaxWidth: 160,
  };
  let start: Point, end: Point;
  // When the cards leave too little space for a padded label, give the entire
  // relation an outside lane. Opposite outer sides also handle overlapping cards.
  if (horizontal) {
    const y = firstSide
      ? Math.min(source.y, target.y) - 52
      : Math.max(source.y + source.height, target.y + target.height) + 52;
    const separated =
      Math.max(source.x, target.x) >= Math.min(source.x + source.width, target.x + target.width);
    if (separated) {
      route.sourceSide = route.targetSide = firstSide ? "top" : "bottom";
      start = { x: sx, y: source.y + (firstSide ? 0 : source.height) };
      end = { x: tx, y: target.y + (firstSide ? 0 : target.height) };
      route.vertices = [
        { x: sx, y },
        { x: tx, y },
      ];
    } else {
      route.sourceSide = forward ? "left" : "right";
      route.targetSide = forward ? "right" : "left";
      start = { x: source.x + (forward ? 0 : source.width), y: sy };
      end = { x: target.x + (forward ? target.width : 0), y: ty };
      const left = Math.min(source.x, target.x) - 28;
      const right = Math.max(source.x + source.width, target.x + target.width) + 28;
      const fromX = forward ? left : right,
        toX = forward ? right : left;
      route.vertices = [
        { x: fromX, y: sy },
        { x: fromX, y },
        { x: toX, y },
        { x: toX, y: ty },
      ];
    }
  } else {
    const x = firstSide
      ? Math.max(source.x + source.width, target.x + target.width) + 106
      : Math.min(source.x, target.x) - 106;
    const separated =
      Math.max(source.y, target.y) >= Math.min(source.y + source.height, target.y + target.height);
    if (separated) {
      route.sourceSide = route.targetSide = firstSide ? "right" : "left";
      start = { x: source.x + (firstSide ? source.width : 0), y: sy };
      end = { x: target.x + (firstSide ? target.width : 0), y: ty };
      route.vertices = [
        { x, y: sy },
        { x, y: ty },
      ];
    } else {
      route.sourceSide = forward ? "top" : "bottom";
      route.targetSide = forward ? "bottom" : "top";
      start = { x: sx, y: source.y + (forward ? 0 : source.height) };
      end = { x: tx, y: target.y + (forward ? target.height : 0) };
      const top = Math.min(source.y, target.y) - 28;
      const bottom = Math.max(source.y + source.height, target.y + target.height) + 28;
      const fromY = forward ? top : bottom,
        toY = forward ? bottom : top;
      route.vertices = [
        { x: sx, y: fromY },
        { x, y: fromY },
        { x, y: toY },
        { x: tx, y: toY },
      ];
    }
  }
  const points = [start, ...route.vertices, end];
  const segment = route.vertices.length / 2;
  route.labelPosition = segmentPosition(points, segment);
  const labelCenter = {
    x: (points[segment]!.x + points[segment + 1]!.x) / 2,
    y: (points[segment]!.y + points[segment + 1]!.y) / 2,
  };
  return { route, points, start, labelCenter };
}

function outsideRoute(
  source: Bounds,
  target: Bounds,
  horizontal: boolean,
  obstacles: Bounds[],
  reciprocal: boolean,
): Route {
  const forward = horizontal ? target.x >= source.x : target.y >= source.y;
  const firstSide = !reciprocal || forward;
  const boxes = [source, target, ...obstacles];
  const preferred = outsideCandidate(source, target, horizontal, firstSide);
  if (
    !pathBlocked(preferred.points, obstacles) &&
    !labelBlocked(preferred.labelCenter, preferred.route.labelMaxWidth, boxes)
  )
    return preferred.route;
  const alternate = outsideCandidate(source, target, horizontal, !firstSide);
  if (
    !pathBlocked(alternate.points, obstacles) &&
    !labelBlocked(alternate.labelCenter, alternate.route.labelMaxWidth, boxes)
  )
    return alternate.route;

  // Both direct outside lanes are blocked. Let Manhattan reach a waypoint
  // beyond every card; place the label there instead of inside a cramped gap.
  const waypoint = horizontal
    ? {
        x: (source.x + source.width / 2 + target.x + target.width / 2) / 2,
        y: firstSide
          ? Math.min(...boxes.map((box) => box.y)) - 52
          : Math.max(...boxes.map((box) => box.y + box.height)) + 52,
      }
    : {
        x: firstSide
          ? Math.max(...boxes.map((box) => box.x + box.width)) + 106
          : Math.min(...boxes.map((box) => box.x)) - 106,
        y: (source.y + source.height / 2 + target.y + target.height / 2) / 2,
      };
  return {
    ...preferred.route,
    vertices: undefined,
    waypoints: [waypoint],
    labelPosition: {
      distance: 0,
      offset: { x: waypoint.x - preferred.start.x, y: waypoint.y - preferred.start.y },
    },
  };
}

// Explicit midpoint bends are stable in a clear gap. The graph's obstacle
// router handles crowded layouts instead of forcing these bends through cards.
export function resourceRelationRoute(
  source: Bounds,
  target: Bounds,
  obstacles: Bounds[] = [],
  reciprocal = false,
): Route {
  const sx = source.x + source.width / 2;
  const sy = source.y + source.height / 2;
  const tx = target.x + target.width / 2;
  const ty = target.y + target.height / 2;
  const horizontalGap = Math.max(
    target.x - source.x - source.width,
    source.x - target.x - target.width,
  );
  const verticalGap = Math.max(
    target.y - source.y - source.height,
    source.y - target.y - target.height,
  );
  const horizontal =
    horizontalGap >= 40 && (verticalGap < 40 || Math.abs(tx - sx) >= Math.abs(ty - sy));
  const useHorizontal = horizontal || (verticalGap < 40 && Math.abs(tx - sx) >= Math.abs(ty - sy));
  const forward = useHorizontal ? tx >= sx : ty >= sy;
  const laneOffset = reciprocal ? (forward ? 12 : -12) : 0;
  const offset = { dx: useHorizontal ? 0 : laneOffset, dy: useHorizontal ? laneOffset : 0 };
  const route: Route = {
    sourceSide: useHorizontal ? (forward ? "right" : "left") : forward ? "bottom" : "top",
    targetSide: useHorizontal ? (forward ? "left" : "right") : forward ? "top" : "bottom",
    sourceOffset: offset,
    targetOffset: offset,
    vertices: undefined,
    labelPosition: { distance: 0.5 },
    labelMaxWidth: 160,
  };
  if ((useHorizontal ? horizontalGap : verticalGap) < (reciprocal ? 84 : 60))
    return outsideRoute(source, target, useHorizontal, obstacles, reciprocal);

  const start = useHorizontal
    ? { x: source.x + (forward ? source.width : 0), y: sy + laneOffset }
    : { x: sx + laneOffset, y: source.y + (forward ? source.height : 0) };
  const end = useHorizontal
    ? { x: target.x + (forward ? 0 : target.width), y: ty + laneOffset }
    : { x: tx + laneOffset, y: target.y + (forward ? 0 : target.height) };
  const middle = useHorizontal
    ? (start.x + end.x) / 2 + laneOffset
    : (start.y + end.y) / 2 + laneOffset;
  const vertices = useHorizontal
    ? [
        { x: middle, y: start.y },
        { x: middle, y: end.y },
      ]
    : [
        { x: start.x, y: middle },
        { x: end.x, y: middle },
      ];
  const points = [start, ...vertices, end];
  if (pathBlocked(points, obstacles)) return route;
  route.vertices = start.x === end.x || start.y === end.y ? [] : vertices;
  if (useHorizontal)
    route.labelMaxWidth = Math.max(
      24,
      Math.min(160, horizontalGap - 24 - (route.vertices.length ? 2 * Math.abs(laneOffset) : 0)),
    );
  const lengths = points
    .slice(1)
    .map((p, i) => Math.abs(p.x - points[i]!.x) + Math.abs(p.y - points[i]!.y));
  // Keep labels away from elbows: prefer a usable middle leg, otherwise the
  // midpoint of the longest straight leg. Aligned cards have one straight edge.
  if (route.vertices.length) {
    const segment = lengths[1]! >= 60 ? 1 : lengths.indexOf(Math.max(...lengths));
    const before = lengths.slice(0, segment).reduce((total, length) => total + length, 0);
    const total = lengths.reduce((sum, length) => sum + length, 0);
    route.labelPosition = { distance: (before + lengths[segment]! / 2) / total };
    if ((useHorizontal && segment !== 1) || (!useHorizontal && segment === 1))
      route.labelMaxWidth = Math.max(24, Math.min(160, lengths[segment]! - 24));
  }
  return route;
}
