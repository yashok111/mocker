import { describe, expect, it, vi } from "vitest";
import { analyzeScenarioDataFlow } from "./scenarioDataFlowApi";
import { analyzeDesignScenarioDataFlow } from "@/api/generated/design-scenarios/design-scenarios";
import { emptyCanvas } from "./canvasModel";

vi.mock("@/api/generated/design-scenarios/design-scenarios", () => ({
  analyzeDesignScenarioDataFlow: vi.fn(),
}));

describe("analyzeScenarioDataFlow", () => {
  it("sends the candidate document and cancellation signal through the generated client", async () => {
    const document = emptyCanvas();
    const analysis = { messages: [], bindings: [], diagnostics: [] };
    vi.mocked(analyzeDesignScenarioDataFlow).mockResolvedValue({
      status: 200,
      data: analysis,
      headers: new Headers(),
    });
    const signal = new AbortController().signal;
    expect(await analyzeScenarioDataFlow(7, document, signal)).toEqual(analysis);
    expect(analyzeDesignScenarioDataFlow).toHaveBeenCalledWith(7, { document }, { signal });
  });

  it("preserves cancellation errors", async () => {
    const controller = new AbortController();
    controller.abort();
    const error = new DOMException("Aborted", "AbortError");
    vi.mocked(analyzeDesignScenarioDataFlow).mockRejectedValue(error);
    await expect(analyzeScenarioDataFlow(7, emptyCanvas(), controller.signal)).rejects.toBe(error);
  });
});
