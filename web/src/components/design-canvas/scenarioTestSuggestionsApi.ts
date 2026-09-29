import { suggestDesignScenarioTests } from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type { TestSuggestions } from "./ScenarioTestSuggestions";

export async function suggestScenarioTests(
  id: number,
  revisionId: number,
  signal: AbortSignal,
): Promise<TestSuggestions> {
  try {
    const response = await suggestDesignScenarioTests(id, { revisionId }, { signal });
    if (response.status !== 200) throw new Error("Не удалось подобрать тесты");
    return response.data;
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}
