import { describe, it, expect } from "vitest";
import { readDiagrams, writeDiagrams, removeState, orderTemplate, EXTENSION } from "./model";

describe("state diagram model", () => {
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
