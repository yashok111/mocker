import { analyzeDesignScenarioDataFlow } from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type { CanvasDocument, DataFlowAnalysis } from "./types";

export async function analyzeScenarioDataFlow(
  id: number,
  document: CanvasDocument,
  signal: AbortSignal,
): Promise<DataFlowAnalysis> {
  try {
    const response = await analyzeDesignScenarioDataFlow(id, { document }, { signal });
    if (response.status !== 200) throw new Error("Не удалось проверить передачу данных");
    return response.data as DataFlowAnalysis;
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}
