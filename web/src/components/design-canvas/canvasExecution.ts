import { resolveOperation } from "./canvasModel";
import { validateCanvasExecutionSettings } from "./canvasStorage";
import type { CanvasDocument, CanvasStepExecution, DataBindingTarget } from "./types";

export { CANVAS_EXECUTION_LIMITS } from "./types";

export type CanvasExecutionStatus =
  | "pending"
  | "running"
  | "passed"
  | "failed"
  | "skipped"
  | "cancelled";

export interface CanvasExecuteStepInput {
  revisionId: number;
  messageId: string;
  pathParams: Record<string, string>;
  query: Record<string, string>;
  headers: Record<string, string>;
  body: string;
}

export interface CanvasExecuteStepResponse {
  scenarioRevisionId: number;
  designId: number;
  designRevisionId: number;
  workspaceRevision: number;
  method: string;
  path: string;
  status: number;
  headers: Record<string, string>;
  body: string;
  durationMs: number;
}

export type CanvasExecuteStep = (
  input: CanvasExecuteStepInput,
  signal: AbortSignal,
) => Promise<CanvasExecuteStepResponse>;

export interface CanvasAssertionResult {
  pointer: string;
  expectedJson: string;
  actualJson?: string;
  passed: boolean;
  error?: string;
}

export interface CanvasBindingResult {
  bindingId: string;
  sourceMessageId: string;
  sourcePointer: string;
  sourceOccurrence: number;
  sourceIterations?: CanvasLoopIteration[];
  target: DataBindingTarget;
  valueJson: string;
  transformedValueJson?: string;
}

export interface CanvasExecutionStepResult {
  messageId: string;
  status: CanvasExecutionStatus;
  reason?: string;
  request?: CanvasExecuteStepInput;
  response?: CanvasExecuteStepResponse;
  assertions: CanvasAssertionResult[];
  bindingResults?: CanvasBindingResult[];
  occurrence?: number;
  iterations?: CanvasLoopIteration[];
}

export interface CanvasLoopIteration {
  fragmentId: string;
  iteration: number;
}

export interface CanvasControlFlowResult {
  fragmentId: string;
  branchId?: string;
  outcome: "taken" | "skipped";
  iterations?: CanvasLoopIteration[];
  reason?: string;
}

export interface CanvasExecutionRunInput {
  runId: string;
  revisionId: number;
  variables?: Record<string, string>;
  name?: string;
}

export interface CanvasExecutionRunSummary {
  id: string;
  scenarioId: number;
  revisionId: number;
  version: number;
  name: string;
  source: "ui" | "mcp";
  status: "running" | "passed" | "failed" | "cancelled";
  reason?: string;
  startedAt: number;
  finishedAt?: number;
}

export interface CanvasExecutionReport extends CanvasExecutionRunSummary {
  document: CanvasDocument;
  inputVariables: Record<string, string>;
  variables: Record<string, string>;
  steps: CanvasExecutionStepResult[];
  controlFlow?: CanvasControlFlowResult[];
}

export interface CanvasExecutionCoverage {
  revisionId: number;
  runCount: number;
  sampleLimit: number;
  messages: {
    messageId: string;
    attempted: number;
    passed: number;
    failed: number;
    skipped: number;
  }[];
  paths: { fragmentId: string; branchId?: string; outcome: "taken" | "skipped"; hits: number }[];
}

export function defaultStepExecution(): CanvasStepExecution {
  return {
    enabled: true,
    pathParams: {},
    query: {},
    headers: {},
    body: "",
    assertions: [],
    extract: [],
  };
}

export function canvasExecutionBlockReason(document: CanvasDocument): string | null {
  try {
    validateCanvasExecutionSettings(document);
  } catch (error) {
    return error instanceof Error ? error.message : "Не удалось проверить настройки запуска.";
  }
  for (const fragment of document.fragments) {
    if (fragment.kind === "opt" && !fragment.execution?.condition)
      return `Задайте условие выполнения блока «${fragment.label || fragment.id}».`;
    if (fragment.kind === "loop" && !fragment.execution?.iterations)
      return `Задайте число повторений блока «${fragment.label || fragment.id}».`;
    if (fragment.kind === "alt") {
      const branches = fragment.branches ?? [];
      if (
        !branches.length ||
        branches.some(
          (branch) => !branch.execution?.condition && branch.execution?.otherwise !== true,
        )
      )
        return `Задайте условие или «иначе» для каждой ветки блока «${fragment.label || fragment.id}».`;
      const otherwise = branches.filter((branch) => branch.execution?.otherwise === true);
      if (
        otherwise.length > 1 ||
        (otherwise.length === 1 && branches.at(-1)?.id !== otherwise[0]?.id)
      )
        return `В блоке «${fragment.label || fragment.id}» ветка «иначе» может быть только последней.`;
    }
  }
  const executable = document.messages.filter(
    (message) =>
      message.kind === "request" &&
      message.operation !== undefined &&
      message.execution?.enabled !== false,
  );
  if (executable.length === 0)
    return document.messages.some((message) => message.kind === "event")
      ? "В сценарии нет включённых HTTP-запросов. Выполнение Kafka-событий пока не поддерживается."
      : "В сценарии нет включённых HTTP-запросов с операцией API.";
  for (const message of executable) {
    const resolved = resolveOperation(document, message.operation!);
    if (!resolved) return `Операция API сообщения «${message.label}» недоступна.`;
    if (resolved.contract.mode !== "linked" || !resolved.contract.source)
      return `Свяжите контракт «${resolved.contract.name}» с проектом API перед запуском.`;
  }
  return null;
}
