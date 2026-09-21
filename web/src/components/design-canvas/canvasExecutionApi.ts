import {
  executeDesignScenarioStep,
  runDesignScenario,
  listDesignScenarioRuns,
  getDesignScenarioRun,
  cancelDesignScenarioRun,
} from "@/api/generated/design-scenarios/design-scenarios";
import { describeApiFailureDetailed } from "@/api/errors";
import type {
  CanvasExecuteStepInput,
  CanvasExecuteStepResponse,
  CanvasExecutionRunInput,
  CanvasExecutionRunSummary,
  CanvasExecutionReport,
} from "./canvasExecution";

async function requestRun<T>(signal: AbortSignal, request: () => Promise<T>): Promise<T> {
  try {
    return await request();
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}

export function runScenario(
  id: number,
  input: CanvasExecutionRunInput,
  signal: AbortSignal,
): Promise<CanvasExecutionReport> {
  return requestRun(signal, async () => {
    const response = await runDesignScenario(id, input, { signal });
    if (response.status !== 200 && response.status !== 202)
      throw new Error("Не удалось запустить сценарий");
    // The server validates JSON config; saved snapshots may have intentionally dangling bindings.
    return response.data as CanvasExecutionReport;
  });
}

export function listScenarioRuns(
  id: number,
  signal: AbortSignal,
): Promise<CanvasExecutionRunSummary[]> {
  return requestRun(signal, async () => {
    const response = await listDesignScenarioRuns(id, { signal });
    if (response.status !== 200) throw new Error("Не удалось загрузить прогоны");
    return response.data.runs;
  });
}

export function getScenarioRun(
  id: number,
  runId: string,
  signal: AbortSignal,
): Promise<CanvasExecutionReport> {
  return requestRun(signal, async () => {
    const response = await getDesignScenarioRun(id, runId, { signal });
    if (response.status !== 200) throw new Error("Не удалось загрузить отчёт");
    return response.data as CanvasExecutionReport;
  });
}

export function cancelScenarioRun(
  id: number,
  runId: string,
  signal: AbortSignal,
): Promise<CanvasExecutionReport> {
  return requestRun(signal, async () => {
    const response = await cancelDesignScenarioRun(id, runId, {}, { signal, keepalive: true });
    if (response.status !== 200) throw new Error("Не удалось отменить прогон");
    return response.data as CanvasExecutionReport;
  });
}

export async function executeScenarioStep(
  id: number,
  input: CanvasExecuteStepInput,
  signal: AbortSignal,
): Promise<CanvasExecuteStepResponse> {
  try {
    const response = await executeDesignScenarioStep(id, input, { signal });
    if (response.status !== 200) throw new Error("Не удалось выполнить шаг сценария");
    return response.data;
  } catch (error) {
    if (signal.aborted) throw error;
    throw new Error(describeApiFailureDetailed(error), { cause: error });
  }
}
