import { describe, expect, it } from "vitest";
import { lifecycleGraph } from "./backendLifecycleGraph";
import type { BackendLifecyclePayload } from "@/api/generated/schemas";
const payload: BackendLifecyclePayload = {
  entity: { kind: "record", recordType: "node", id: "entity" },
  stateFields: [],
  coverage: "partial",
  coverageOrigin: { kind: "authored", reason: "test" },
  states: [
    {
      id: "a",
      label: "Created",
      initial: true,
      terminal: false,
      origin: { kind: "authored", reason: "test" },
      refs: [],
    },
  ],
  transitions: [],
  rules: [],
};
describe("lifecycle graph", () => {
  it("never creates transitions from enum states or desired rules", () => {
    const before = JSON.stringify(payload);
    const view = lifecycleGraph(payload, { positions: [], collapsedIds: [] });
    expect(view.diagram.transitions).toEqual([]);
    expect(JSON.stringify(payload)).toBe(before);
  });
  it("uses exact semantic IDs and layout-only positions", () => {
    const view = lifecycleGraph(payload, {
      positions: [{ id: "a", x: 42, y: 84 }],
      collapsedIds: [],
    });
    expect(view.diagram.states[0]).toMatchObject({ id: "a", x: 42, y: 84 });
    expect(payload.states[0]).not.toHaveProperty("x");
  });
});
