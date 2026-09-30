import { canvasTextMeasurer } from "../design-canvas/canvasText";
import { diagramFontFamily } from "../diagram/presentation";
import {
  portNames,
  ports,
  type ResponseRule,
  type ResponseRuleEdge,
  type ResponseRuleNode,
} from "./model";

export const CARD_WIDTH = 240;
export const CARD_HEIGHT = 112;
export type Point = { x: number; y: number };
export type Box = Point & { width: number; height: number };
export type RuleRoute = {
  id: string;
  from: string;
  to: string;
  port: ResponseRuleEdge["port"];
  points: Point[];
  vertices: Point[];
  router: boolean;
  label?: Box & { text: string };
};

export function intersects(a: Box, b: Box): boolean {
  return a.x < b.x + b.width && a.x + a.width > b.x && a.y < b.y + b.height && a.y + a.height > b.y;
}

export function segmentHits(a: Point, b: Point, box: Box): boolean {
  if (a.x === b.x)
    return (
      a.x > box.x &&
      a.x < box.x + box.width &&
      Math.max(a.y, b.y) > box.y &&
      Math.min(a.y, b.y) < box.y + box.height
    );
  if (a.y === b.y)
    return (
      a.y > box.y &&
      a.y < box.y + box.height &&
      Math.max(a.x, b.x) > box.x &&
      Math.min(a.x, b.x) < box.x + box.width
    );
  return true;
}

export function portY(node: ResponseRuleNode, port: string): number {
  return (
    node.y +
    CARD_HEIGHT *
      (ports(node).length === 2 ? (port === "true" || port === "found" ? 1 / 3 : 2 / 3) : 1 / 2)
  );
}

export function autoLayout(rule: ResponseRule): ResponseRule {
  const rank = new Map(rule.nodes.map((node) => [node.id, 0]));
  const incoming = new Map(rule.nodes.map((node) => [node.id, 0]));
  const edges = rule.edges.filter((edge) => incoming.has(edge.from) && incoming.has(edge.to));
  for (const edge of edges) incoming.set(edge.to, incoming.get(edge.to)! + 1);
  const queue = rule.nodes.filter((node) => incoming.get(node.id) === 0).map((node) => node.id);
  const visited = new Set<string>();
  for (let index = 0; index < queue.length; index++) {
    const id = queue[index]!;
    visited.add(id);
    for (const edge of edges.filter((e) => e.from === id)) {
      rank.set(edge.to, Math.max(rank.get(edge.to)!, rank.get(id)! + 1));
      incoming.set(edge.to, incoming.get(edge.to)! - 1);
      if (incoming.get(edge.to) === 0) queue.push(edge.to);
    }
  }
  // Invalid cycles are still editable. Keep their nodes in a separate column
  // instead of repeatedly relaxing ranks while the user repairs the graph.
  const unfinishedRank = Math.max(0, ...rank.values()) + 1;
  for (const node of rule.nodes) if (!visited.has(node.id)) rank.set(node.id, unfinishedRank);
  const columns = new Map<number, string[]>();
  for (const node of rule.nodes) {
    const value = rank.get(node.id)!;
    columns.set(value, [...(columns.get(value) ?? []), node.id]);
  }
  const rows = Math.max(1, ...Array.from(columns.values(), (ids) => ids.length));
  return {
    ...rule,
    nodes: rule.nodes.map((node) => {
      const column = columns.get(rank.get(node.id)!)!;
      return {
        ...node,
        x: 40 + rank.get(node.id)! * 380,
        y: 40 + ((rows - column.length) / 2 + column.indexOf(node.id)) * 200,
      };
    }),
  };
}

export function buildRoutes(rule: ResponseRule): RuleRoute[] {
  const nodes = new Map(rule.nodes.map((node) => [node.id, node]));
  const boxes = rule.nodes.map((node) => ({
    x: node.x,
    y: node.y,
    width: CARD_WIDTH,
    height: CARD_HEIGHT,
  }));
  const measure = canvasTextMeasurer(11, 400, diagramFontFamily);
  const routes: RuleRoute[] = [];
  for (const edge of rule.edges) {
    const source = nodes.get(edge.from),
      target = nodes.get(edge.to);
    if (!source || !target) continue;
    const start = { x: source.x + CARD_WIDTH, y: portY(source, edge.port) };
    const end = { x: target.x, y: target.y + CARD_HEIGHT / 2 };
    const text = edge.port !== "next" ? portNames[edge.port] : undefined;
    const width = text ? Math.ceil(measure(text)) + 20 : 0;
    const obstacles = [...boxes, ...routes.flatMap((route) => (route.label ? [route.label] : []))];
    const gap = end.x - start.x;
    // Saved diagrams may have a gap of only 40px. A fixed 96px departure
    // enters the target card and gives Manhattan an impossible waypoint.
    const laneX =
      start.x +
      (gap > 0
        ? Math.min(gap / 2, edge.port === "false" || edge.port === "missing" ? 124 : 96)
        : 18);
    const clearance = gap > 0 ? Math.min(18, gap / 3) : 18;
    const outside = [
      Math.min(...obstacles.map((box) => box.y)) - 42,
      Math.max(...obstacles.map((box) => box.y + box.height)) + 42,
    ];
    const candidates = [
      [start, { x: laneX, y: start.y }, { x: laneX, y: end.y }, end],
      ...outside.map((y) => [
        start,
        { x: start.x + clearance, y: start.y },
        { x: start.x + clearance, y },
        { x: end.x - clearance, y },
        { x: end.x - clearance, y: end.y },
        end,
      ]),
    ].map(compactPoints);
    let chosen: RuleRoute | undefined;
    for (const points of candidates) {
      if (obstacles.some((box) => pathHits(points, box))) continue;
      const label = text ? labelOnPath(points, text, width, obstacles, routes) : undefined;
      if (text && !label) continue;
      chosen = { ...edge, points, vertices: points.slice(1, -1), router: false, label };
      break;
    }
    // Complex manual layouts still use X6's obstacle search. Its only waypoint
    // is outside every card, never a fixed departure inside a crowded target.
    if (!chosen) {
      const center = { x: (start.x + end.x) / 2, y: outside[0]! };
      const label = text
        ? { x: center.x - width / 2, y: center.y - 13, width, height: 26, text }
        : undefined;
      chosen = {
        ...edge,
        points: candidates[1]!,
        vertices: label ? [center] : [],
        router: true,
        label,
      };
    }
    routes.push(chosen);
  }
  return routes;
}

function pathHits(points: Point[], box: Box): boolean {
  return points.slice(1).some((point, index) => segmentHits(points[index]!, point, box));
}

function compactPoints(points: Point[]): Point[] {
  const result: Point[] = [];
  for (const point of points) {
    const last = result.at(-1);
    if (last?.x === point.x && last.y === point.y) continue;
    const before = result.at(-2);
    if (
      before &&
      last &&
      ((before.x === last.x && last.x === point.x) || (before.y === last.y && last.y === point.y))
    )
      result.pop();
    result.push(point);
  }
  return result;
}

function labelOnPath(
  points: Point[],
  text: string,
  width: number,
  obstacles: Box[],
  previous: RuleRoute[],
): RuleRoute["label"] {
  for (let index = 1; index < points.length; index++) {
    const a = points[index - 1]!,
      b = points[index]!;
    const horizontal = a.y === b.y;
    const length = Math.abs(a.x - b.x) + Math.abs(a.y - b.y);
    if (length < (horizontal ? width : 26) + 16) continue;
    const label = { x: (a.x + b.x - width) / 2, y: (a.y + b.y - 26) / 2, width, height: 26, text };
    if (
      obstacles.some((box) => intersects(box, label)) ||
      previous.some((route) => pathHits(route.points, label))
    )
      continue;
    return label;
  }
  return undefined;
}

export function nodeSummary(node: ResponseRuleNode): string {
  switch (node.type) {
    case "entity_read":
      return `${node.entity.operation === "list" ? "Список" : "По ключу"} · ${node.entity.family || "Выберите семейство"}`;
    case "entity_create":
      return `Создать · ${node.entity.family || "Выберите семейство"}`;
    case "entity_update":
      return `Изменить поля · ${node.entity.family || "Выберите семейство"}`;
    case "start":
      return "Входящий запрос";
    case "fallback":
      return "Передать стандартной обработке";
    case "delay":
      return `${node.delayMs} мс`;
    case "response":
      return `HTTP ${node.response.status} · ${node.response.mediaType}`;
    case "condition": {
      const source =
        { query: "Query", header: "Заголовок", body: "Поле JSON" }[node.condition.in] ??
        node.condition.in;
      const op =
        { equals: "равно", contains: "содержит", exists: "существует" }[node.condition.op] ??
        node.condition.op;
      return `${source}: ${node.condition.name}\n${op}${node.condition.op === "exists" ? "" : ` ${node.condition.value ?? ""}`}`;
    }
  }
}
