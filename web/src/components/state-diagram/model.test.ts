// @vitest-environment node
import { describe, it, expect } from "vitest";
import { readDiagrams, writeDiagrams, removeState, orderTemplate, EXTENSION } from "./model";

describe("state diagram model", () => {
  it("roundtrips entity settings and explicit values without changing graph identities", () => {
    const diagram = {
      ...orderTemplate(),
      entity: { family: "/orders", keyParam: "orderId", stateField: "status" },
    };
    diagram.states = diagram.states.map((state) => ({ ...state, value: state.id + "-value" }));
    expect(readDiagrams(writeDiagrams({}, [diagram]))).toEqual([diagram]);
    expect(diagram.states[0]?.id).toBe("created");
    expect(diagram.transitions[0]?.from).toBe("created");
  });
  it("rejects unknown entity fields and malformed optional state values", () => {
    const diagram = orderTemplate();
    const read = (value: unknown) =>
      readDiagrams({ [EXTENSION]: { formatVersion: 1, diagrams: [value] } });
    expect(() =>
      read({
        ...diagram,
        entity: { family: "/orders", keyParam: "id", stateField: "status", future: true },
      }),
    ).toThrow();
    expect(() => read({ ...diagram, entity: { family: "/orders", keyParam: "id" } })).toThrow();
    for (const value of [null, 12, "", "a".repeat(257), "💡".repeat(65)]) {
      expect(() => read({ ...diagram, states: [{ ...diagram.states[0], value }] })).toThrow();
    }
  });
  it("preserves unrelated contract fields and diagrams", () => {
    const doc = { paths: { "/orders": {} }, "x-other": { value: 1 } };
    const d = orderTemplate();
    const next = writeDiagrams(doc, [d]);
    expect(next.paths).toBe(doc.paths);
    expect(next["x-other"]).toEqual({ value: 1 });
    expect(readDiagrams(next)).toEqual([d]);
    expect(doc).not.toHaveProperty(EXTENSION);
  });
  it("refuses unknown formats and fields instead of erasing them", () => {
    expect(() => readDiagrams({ [EXTENSION]: { formatVersion: 2, diagrams: [] } })).toThrow();
    expect(() =>
      readDiagrams({
        [EXTENSION]: { formatVersion: 1, diagrams: [{ ...orderTemplate(), future: true }] },
      }),
    ).toThrow();
  });
  it("removes incident edges and clears the initial state without mutating input", () => {
    const d = orderTemplate();
    const next = removeState(d, d.initialStateId);
    expect(next.initialStateId).toBe("");
    expect(
      next.transitions.every((t) => t.from !== d.initialStateId && t.to !== d.initialStateId),
    ).toBe(true);
    expect(d.states).toHaveLength(4);
  });
});
