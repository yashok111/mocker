// @vitest-environment node
import { describe, expect, it } from "vitest";
import { layoutDiagram } from "../diagram/elkLayout";
import { applyStateLayout, stateLayoutInput, STATE_HEIGHT, STATE_WIDTH } from "./layout";
import { orderTemplate } from "./model";

describe("state diagram ELK adapter", () => {
  it("ignores saved positions and preserves semantic content while placing loops and cycles", async () => {
    const diagram = orderTemplate();
    diagram.transitions.push(
      { ...diagram.transitions[0]!, id: "return", from: "paid", to: "created" },
      { ...diagram.transitions[0]!, id: "loop", from: "created", to: "created" },
    );
    const input = stateLayoutInput(diagram);
    const moved = {
      ...diagram,
      states: diagram.states.map((state) => ({ ...state, x: 999, y: 123 })),
    };
    expect(stateLayoutInput(moved)).toEqual(input);
    const layout = await layoutDiagram(input);
    const result = applyStateLayout(diagram, layout);
    expect(result.transitions).toBe(diagram.transitions);
    expect(result.initialStateId).toBe(diagram.initialStateId);
    expect(result.states.map(({ x: _x, y: _y, ...state }) => state)).toEqual(
      diagram.states.map(({ x: _x, y: _y, ...state }) => state),
    );
    expect(diagram.states[0]!.x).toBe(70);
    expect(layout.edges).toHaveLength(diagram.transitions.length);
    for (const edge of layout.edges) {
      const source = layout.nodes.find((node) => node.id === edge.source)!;
      const target = layout.nodes.find((node) => node.id === edge.target)!;
      expect(edge.points[0]!.x).toBeCloseTo(source.x + STATE_WIDTH);
      expect(edge.points[0]!.y).toBeCloseTo(source.y + STATE_HEIGHT / 2);
      expect(edge.points.at(-1)!.x).toBeCloseTo(target.x);
      expect(edge.points.at(-1)!.y).toBeCloseTo(target.y + STATE_HEIGHT / 2);
    }
  });
});
