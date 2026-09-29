import { expect, it, vi } from "vitest";
import { getDesignScenarioEventMap } from "@/api/generated/design-scenarios/design-scenarios";
import { getScenarioEventMap } from "./scenarioEventMapApi";

vi.mock("@/api/generated/design-scenarios/design-scenarios", () => ({
  getDesignScenarioEventMap: vi.fn(),
}));

it("passes the editor base revision to the generated saved-map GET", async () => {
  vi.mocked(getDesignScenarioEventMap).mockResolvedValue({
    status: 200,
    data: { revisionId: 12 },
  } as Awaited<ReturnType<typeof getDesignScenarioEventMap>>);
  const signal = new AbortController().signal;
  await getScenarioEventMap(7, 12, signal);
  expect(getDesignScenarioEventMap).toHaveBeenCalledWith(7, { revisionId: 12 }, { signal });
});
