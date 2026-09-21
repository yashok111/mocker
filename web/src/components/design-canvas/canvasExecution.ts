import { resolveOperation } from "./canvasModel";
import { validateCanvasExecutionSettings } from "./canvasStorage";
import type { CanvasDocument, CanvasStepExecution } from "./types";

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

export interface CanvasExecutionStepResult {
  messageId: string;
  status: CanvasExecutionStatus;
  reason?: string;
  request?: CanvasExecuteStepInput;
  response?: CanvasExecuteStepResponse;
  assertions: CanvasAssertionResult[];
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
  if (document.fragments.length > 0)
    return "Исполнение блоков opt/loop пока не поддерживается. Удалите блоки перед запуском.";
  const executable = document.messages.filter(
    (message) =>
      message.kind === "request" &&
      message.operation !== undefined &&
      message.execution?.enabled !== false,
  );
  if (executable.length === 0) return "В сценарии нет включённых HTTP-запросов с операцией API.";
  for (const message of executable) {
    const resolved = resolveOperation(document, message.operation!);
    if (!resolved) return `Операция API сообщения «${message.label}» недоступна.`;
    if (resolved.contract.mode !== "linked" || !resolved.contract.source)
      return `Свяжите контракт «${resolved.contract.name}» с проектом API перед запуском.`;
  }
  return null;
}
