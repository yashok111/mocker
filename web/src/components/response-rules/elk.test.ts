import { describe, expect, it } from "vitest";
import { layoutDiagram } from "../diagram/elkLayout";
import { applyRuleLayout, ruleLayoutInput } from "./elk";
import { CARD_HEIGHT, CARD_WIDTH, portY } from "./layout";
import { headerTemplate } from "./model";

describe("response rules ELK adapter", () => {
  it("preserves fixed condition output ports, inputs, data and source identities", async () => {
    const rule = headerTemplate();
    const input = ruleLayoutInput(rule);
    expect(
      ruleLayoutInput({ ...rule, nodes: rule.nodes.map((node) => ({ ...node, x: 400, y: 800 })) }),
    ).toEqual(input);
    const layout = await layoutDiagram(input);
    const result = applyRuleLayout(rule, layout);
    expect(result.edges).toBe(rule.edges);
    expect(result.nodes.map(({ x: _x, y: _y, ...node }) => node)).toEqual(
      rule.nodes.map(({ x: _x, y: _y, ...node }) => node),
    );
    for (const edge of rule.edges) {
      const route = layout.edges.find((item) => item.id === `edge:${edge.id}`)!;
      const source = layout.nodes.find((node) => node.id === `node:${edge.from}`)!;
      const target = layout.nodes.find((node) => node.id === `node:${edge.to}`)!;
      const node = rule.nodes.find((item) => item.id === edge.from)!;
      expect(route.points[0]!.x).toBeCloseTo(source.x + CARD_WIDTH);
      expect(route.points[0]!.y).toBeCloseTo(source.y + portY(node, edge.port) - node.y);
      expect(route.points.at(-1)!.x).toBeCloseTo(target.x);
      expect(route.points.at(-1)!.y).toBeCloseTo(target.y + CARD_HEIGHT / 2);
    }
    expect(layout.edges.filter((edge) => edge.label).map((edge) => edge.label!.text)).toEqual([
      "Да",
      "Нет",
    ]);
  });
});
