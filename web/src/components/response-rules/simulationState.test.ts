import { describe, expect, it } from "vitest";
import { createEvaluationGate, evaluationIdentity } from "./simulationState";
describe("evaluation identity", () => {
  it("does not accept A after B, even if cancelling A failed", () => {
    const gate = createEvaluationGate();
    gate.update("document A");
    const a = gate.begin();
    gate.update("document B");
    const b = gate.begin();
    expect(gate.accept(b)).toBe(true);
    expect(gate.accept(a)).toBe(false);
    gate.update("document A");
    expect(gate.accept(a)).toBe(false);
    gate.dispose();
    expect(gate.accept(b)).toBe(false);
  });
  it("includes exact buffer, fixture order, selection and pending editor generation", () => {
    const input = {
      designId: 1,
      ruleId: "rule",
      document: "{}",
      generation: "{}",
      request: { query: [], headers: [], bodyJSON: "9007199254740993" },
    };
    const initial = evaluationIdentity(input);
    for (const patch of [
      { designId: 2 },
      { ruleId: "other" },
      { document: "{} " },
      { generation: "pending" },
      { request: { query: [], headers: [] } },
    ]) {
      expect(evaluationIdentity({ ...input, ...patch })).not.toBe(initial);
    }
  });
});
