import {
  analyzeDesignScenarioEventMap,
  getDesignScenarioEventMap,
} from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type { DesignScenarioEventMapReport } from "@/api/generated/schemas";
import type { CanvasDocument } from "./types";

export async function getScenarioEventMap(
  id: number,
  revisionId: number,
  signal: AbortSignal,
): Promise<DesignScenarioEventMapReport> {
  try {
    const response = await getDesignScenarioEventMap(id, { revisionId }, { signal });
    if (response.status !== 200) throw new Error("Не удалось получить сохранённую карту событий");
    return response.data;
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}

export async function analyzeScenarioEventMap(
  id: number,
  document: CanvasDocument,
  signal: AbortSignal,
): Promise<DesignScenarioEventMapReport> {
  try {
    const response = await analyzeDesignScenarioEventMap(id, { document }, { signal });
    if (response.status !== 200) throw new Error("Не удалось проверить карту событий");
    return response.data;
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}
