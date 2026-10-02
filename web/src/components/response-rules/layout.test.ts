// @vitest-environment node
import { describe, expect, it } from "vitest";
import {
  autoLayout,
  buildRoutes,
  CARD_HEIGHT,
  CARD_WIDTH,
  intersects,
  segmentHits,
  nodeSummary,
} from "./layout";
import { headerTemplate, type ResponseRule } from "./model";

function checkGeometry(rule: ResponseRule) {
  const boxes = rule.nodes.map((n) => ({ ...n, width: CARD_WIDTH, height: CARD_HEIGHT }));
  const routes = buildRoutes(rule);
  for (const route of routes) {
    if (route.label) {
      expect(boxes.some((box) => intersects(box, route.label!))).toBe(false);
      for (const other of routes) {
        if (other.id === route.id || other.router) continue;
        expect(
          other.points
            .slice(1)
            .some((point, index) => segmentHits(other.points[index]!, point, route.label!)),
        ).toBe(false);
      }
    }
    if (!route.router) {
      const obstacles = boxes.filter((box) => box.id !== route.from && box.id !== route.to);
      expect(
        obstacles.some((box) =>
          route.points.slice(1).some((p, i) => segmentHits(route.points[i]!, p, box)),
        ),
      ).toBe(false);
    }
  }
  return routes;
}

describe("response-rule graph layout", () => {
  it("summarizes result comparisons and existence without changing exact JSON text", () => {
    const node = {
      id: "check",
      type: "condition",
      name: "Проверка",
      x: 0,
      y: 0,
      resultCondition: {
        source: { source: "result", nodeId: "read", pointer: "/amount" },
        op: "not_equals",
        valueJSON: "9007199254740993",
      },
    };
    expect(nodeSummary(node as never)).toBe("Результат: read /amount\nНе равно 9007199254740993");
    expect(
      nodeSummary({
        ...node,
        resultCondition: {
          source: { source: "result", nodeId: "read", pointer: "/amount" },
          op: "not_exists",
        },
      } as never),
    ).toBe("Результат: read /amount\nНе существует");
  });
  it("routes found and missing exits separately and describes entity reads", () => {
    const rule = headerTemplate();
    rule.nodes[1] = {
      id: "auth",
      type: "entity_read",
      name: "Заказ",
      x: 380,
      y: 150,
      entity: { family: "/orders", operation: "get", key: { source: "path", name: "id" } },
    };
    rule.edges[1]!.port = "found";
    rule.edges[2]!.port = "missing";
    const routes = checkGeometry(autoLayout(rule));
    expect(routes[1]!.points[0]!.y).not.toBe(routes[2]!.points[0]!.y);
    expect(routes[1]!.label?.text).toBe("Найдена");
    expect(routes[2]!.label?.text).toBe("Не найдена");
    expect(nodeSummary(rule.nodes[1]!)).toContain("/orders");
  });
  it("routes the saved demo's 40/60px gaps without an obstacle-router fallback", () => {
    const rule = headerTemplate();
    const positions = [
      [0, 120],
      [280, 120],
      [580, 20],
      [880, 20],
      [580, 250],
    ];
    rule.nodes = rule.nodes.map((node, index) => ({
      ...node,
      x: positions[index]![0]!,
      y: positions[index]![1]!,
    }));
    const routes = checkGeometry(rule);
    expect(routes.every((route) => !route.router)).toBe(true);
    for (const route of routes) {
      for (const node of rule.nodes) {
        expect(
          route.points.slice(1).some((point, index) =>
            segmentHits(route.points[index]!, point, {
              ...node,
              width: CARD_WIDTH,
              height: CARD_HEIGHT,
            }),
          ),
        ).toBe(false);
      }
      if (route.label) {
        const center = {
          x: route.label.x + route.label.width / 2,
          y: route.label.y + route.label.height / 2,
        };
        expect(
          route.points.slice(1).some((point, index) => {
            const previous = route.points[index]!;
            return (
              (point.x === previous.x &&
                center.x === point.x &&
                center.y >= Math.min(point.y, previous.y) &&
                center.y <= Math.max(point.y, previous.y)) ||
              (point.y === previous.y &&
                center.y === point.y &&
                center.x >= Math.min(point.x, previous.x) &&
                center.x <= Math.max(point.x, previous.x))
            );
          }),
        ).toBe(true);
      }
    }
  });
  it("keeps identities and content while spacing a split graph deterministically", () => {
    const rule = headerTemplate();
    const result = autoLayout(rule);
    expect(autoLayout(rule)).toEqual(result);
    expect(result.edges).toEqual(rule.edges);
    expect(result.nodes.map(({ x: _x, y: _y, ...n }) => n)).toEqual(
      rule.nodes.map(({ x: _x, y: _y, ...n }) => n),
    );
    for (const [i, a] of result.nodes.entries()) {
      for (const b of result.nodes.slice(i + 1)) {
        expect(
          intersects(
            { ...a, width: CARD_WIDTH, height: CARD_HEIGHT },
            { ...b, width: CARD_WIDTH, height: CARD_HEIGHT },
          ),
        ).toBe(false);
      }
    }
    checkGeometry(result);
  });
  it("reserves different labelled exits when both branches rejoin the same target", () => {
    const rule = headerTemplate();
    rule.nodes = rule.nodes.filter((n) => ["start", "auth", "ok"].includes(n.id));
    rule.edges = [
      { id: "a", from: "start", to: "auth", port: "next" },
      { id: "b", from: "auth", to: "ok", port: "true" },
      { id: "c", from: "auth", to: "ok", port: "false" },
    ];
    const routes = checkGeometry(autoLayout(rule));
    const labels = routes.flatMap((r) => (r.label ? [r.label] : []));
    expect(labels).toHaveLength(2);
    expect(intersects(labels[0]!, labels[1]!)).toBe(false);
    expect(routes[1]!.points).not.toEqual(routes[2]!.points);
  });
  it("places labels clear of nearby manually positioned cards", () => {
    const rule = headerTemplate();
    rule.nodes[2]!.x = rule.nodes[1]!.x + CARD_WIDTH + 20;
    rule.nodes[2]!.y = rule.nodes[1]!.y;
    expect(checkGeometry(rule).every((route) => !route.router)).toBe(true);
  });
  it("keeps incomplete or cyclic authoring models bounded and retains all nodes", () => {
    const rule = headerTemplate();
    rule.edges.push({ id: "cycle", from: "ok", to: "auth", port: "next" });
    rule.nodes.push({ id: "island", type: "fallback", name: "Незавершённая ветка", x: 0, y: 0 });
    expect(autoLayout(rule).nodes).toHaveLength(6);
    expect(checkGeometry(autoLayout(rule))).toHaveLength(5);
  });
});
