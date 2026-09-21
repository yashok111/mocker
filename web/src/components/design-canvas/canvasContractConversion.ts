import {
  getOperation,
  HTTP_METHODS,
  isRecord,
  type ApiDocument,
} from "../api-designer/documentModel";
import { OPERATION_KEY, resolveOperation } from "./canvasModel";
import {
  inheritOperation,
  mergeContractDocuments,
  mergeEntries,
  parameterValue,
  validateMergedReferences,
  validateSourceReferences,
} from "./canvasContractMerge";
import type { CanvasContract, CanvasDocument } from "./types";

export interface ConversionResponse {
  messageId: string;
  label: string;
  status: string;
}
export interface ConversionRow {
  id: string;
  messageIds: string[];
  label: string;
  targetName: string;
  included: boolean;
  method: string;
  path: string;
  responses: ConversionResponse[];
  existingStatuses: string[];
  source?: { contractId: string; operationKey: string };
}
export interface ConversionPreview {
  rows: ConversionRow[];
  skippedCount: number;
}
export interface ConversionIssue {
  rowId?: string;
  message: string;
}
export interface ConversionResult {
  contract: CanvasContract | null;
  bindings: Array<{ messageId: string; operationKey: string }>;
  errors: ConversionIssue[];
  warnings: ConversionIssue[];
}
export function previewCanvasContract(document: CanvasDocument): ConversionPreview {
  const rows: ConversionRow[] = [];
  const groups = new Map<string, ConversionRow>();
  let skippedCount = 0;
  for (const message of document.messages) {
    if (message.kind === "response") continue;
    const target = document.participants.find(({ id }) => id === message.toId);
    if (
      message.kind !== "request" ||
      (message.operation === undefined &&
        (message.fromId === message.toId ||
          (target?.kind !== "service" && target?.kind !== "external")))
    ) {
      skippedCount++;
      continue;
    }
    const resolved =
      message.operation === undefined ? null : resolveOperation(document, message.operation);
    const explicit = /^\s*(GET|POST|PUT|PATCH|DELETE|HEAD|OPTIONS|TRACE)\s+(\/[^\s]*)\s*$/i.exec(
      message.label,
    );
    const method = resolved?.location.method.toUpperCase() ?? explicit?.[1]?.toUpperCase() ?? "";
    const path = resolved?.location.path ?? explicit?.[2] ?? "";
    const identity =
      message.operation !== undefined
        ? JSON.stringify(["bound", message.operation.contractId, message.operation.operationKey])
        : method && path
          ? JSON.stringify(["endpoint", message.toId, method, path])
          : JSON.stringify(["message", message.id]);
    let row = groups.get(identity);
    if (row === undefined) {
      const operation =
        resolved === null ? undefined : getOperation(resolved.contract.document, resolved.location);
      row = {
        id: message.id,
        messageIds: [],
        label: message.label,
        targetName: target?.name ?? message.toId,
        included: true,
        method,
        path,
        responses: [],
        existingStatuses: isRecord(operation?.responses)
          ? Object.keys(operation.responses).filter((key) => !key.startsWith("x-"))
          : [],
        ...(message.operation === undefined ? {} : { source: { ...message.operation } }),
      };
      rows.push(row);
      groups.set(identity, row);
    }
    row.messageIds.push(message.id);
  }
  for (const row of rows) {
    const requestIds = new Set(row.messageIds);
    row.responses = document.messages
      .filter(
        (message) =>
          message.kind === "response" &&
          message.replyToId !== undefined &&
          requestIds.has(message.replyToId),
      )
      .map((message) => ({
        messageId: message.id,
        label: message.label,
        status:
          /^\s*(?:HTTP(?:\/\d(?:\.\d)?)?\s+)?([1-5]\d{2}|[1-5]XX|default)\b/i
            .exec(message.label)?.[1]
            ?.replace(/xx/i, "XX")
            .replace(/default/i, "default") ?? "",
      }));
  }
  return { rows, skippedCount };
}

function groupEditedRows(
  document: CanvasDocument,
  rows: ConversionRow[],
  errors: ConversionIssue[],
): ConversionRow[] {
  const grouped: ConversionRow[] = [];
  const groups = new Map<string, ConversionRow>();
  const rowIds = new Set<string>();
  for (const row of rows) {
    if (rowIds.has(row.id))
      errors.push({ rowId: row.id, message: "Один вызов включён несколько раз" });
    rowIds.add(row.id);
    const receiver = document.messages.find(({ id }) => id === row.messageIds[0])?.toId;
    const method = row.method.trim().toLowerCase();
    const path = row.path.trim();
    const canGroup =
      row.source === undefined && receiver !== undefined && method !== "" && path !== "";
    const key = JSON.stringify([receiver, method, path]);
    const existing = canGroup ? groups.get(key) : undefined;
    if (existing === undefined) {
      const copy = structuredClone(row);
      grouped.push(copy);
      if (canGroup) groups.set(key, copy);
    } else {
      existing.messageIds.push(...row.messageIds);
      existing.responses.push(...structuredClone(row.responses));
    }
  }
  return grouped;
}

export function buildCanvasContract(
  document: CanvasDocument,
  rows: ConversionRow[],
  name: string,
  contractId: string,
): ConversionResult {
  const result: ConversionResult = { contract: null, bindings: [], errors: [], warnings: [] };
  const selected = groupEditedRows(
    document,
    rows.filter(({ included }) => included),
    result.errors,
  );
  if (!name.trim()) result.errors.push({ message: "Укажите название API" });
  if (!contractId.trim() || document.contracts.some(({ id }) => id === contractId))
    result.errors.push({ message: "Для нового контракта нужен новый ID" });
  if (selected.length === 0) result.errors.push({ message: "Выберите хотя бы один HTTP-вызов" });
  const sourceIds = new Set(
    selected.flatMap(({ source }) => (source === undefined ? [] : [source.contractId])),
  );
  const sources = document.contracts.filter(({ id }) => sourceIds.has(id));
  const keepRootInheritance =
    sourceIds.size === 1 && selected.every(({ source }) => source !== undefined);
  const api: ApiDocument = mergeContractDocuments(
    sources,
    name.trim(),
    keepRootInheritance,
    result.errors,
  );
  const paths: Record<string, unknown> = {};
  api.paths = paths;
  const rowIds = new Set<string>();
  const messageIds = new Set<string>();
  const endpointTemplates = new Map<string, string>();
  for (const row of selected) {
    const error = (message: string) => result.errors.push({ rowId: row.id, message });
    const method = row.method.trim().toLowerCase();
    const path = row.path.trim();
    if (rowIds.has(row.id)) error("Один вызов включён несколько раз");
    rowIds.add(row.id);
    for (const id of row.messageIds) {
      if (
        messageIds.has(id) ||
        !document.messages.some((message) => message.id === id && message.kind === "request")
      )
        error(`Вызов ${id} отсутствует или выбран несколько раз`);
      messageIds.add(id);
    }
    if (!HTTP_METHODS.some((candidate) => candidate === method)) error("Укажите HTTP-метод");
    if (!path.startsWith("/") || /[\s?#]/.test(path))
      error("Укажите путь, начинающийся с /, без query-параметров и пробелов");
    const templateNames = [...path.matchAll(/\{([^{} /]+)\}/g)].map((match) => match[1]!);
    if (
      /[{}]/.test(path.replace(/\{[^{} /]+\}/g, "")) ||
      new Set(templateNames).size !== templateNames.length
    )
      error("Проверьте фигурные скобки и уникальные имена параметров пути");
    const templateKey = `${method} ${path.replace(/\{[^{} /]+\}/g, "{}")}`;
    const existingTemplate = endpointTemplates.get(templateKey);
    if (existingTemplate !== undefined && existingTemplate !== path)
      error(`Конфликт шаблонов пути ${existingTemplate} и ${path}`);
    endpointTemplates.set(templateKey, path);
    const resolved = row.source === undefined ? null : resolveOperation(document, row.source);
    if (row.source !== undefined && resolved === null)
      error("Связанная операция не найдена; исправьте привязку на схеме");
    const operation =
      resolved === null
        ? { summary: row.label }
        : structuredClone(getOperation(resolved.contract.document, resolved.location)!);
    const sourcePaths = resolved?.contract.document.paths;
    const originalPath = isRecord(sourcePaths) ? sourcePaths[resolved!.location.path] : undefined;
    const originalPathItem = isRecord(originalPath) ? originalPath : {};
    if (resolved !== null)
      inheritOperation(resolved.contract.document, originalPathItem, operation);
    const parameters = Array.isArray(operation.parameters) ? operation.parameters : [];
    for (const parameterName of templateNames) {
      if (
        parameters.some((parameter) => {
          const value = parameterValue(api, parameter);
          return value?.in === "path" && value.name === parameterName;
        })
      )
        continue;
      parameters.push({
        name: parameterName,
        in: "path",
        required: true,
        schema: { type: "string" },
      });
      result.warnings.push({
        rowId: row.id,
        message: `Для параметра пути ${parameterName} добавлен тип string; проверьте тип в API Designer`,
      });
    }
    for (const parameter of parameters) {
      const value = parameterValue(api, parameter);
      if (
        value?.in === "path" &&
        (!templateNames.includes(String(value.name)) || value.required !== true)
      )
        error(
          `Параметр пути ${String(value.name)} должен присутствовать в URL и быть обязательным`,
        );
    }
    if (parameters.length > 0) operation.parameters = parameters;
    const responses: Record<string, unknown> = isRecord(operation.responses)
      ? operation.responses
      : {};
    operation.responses = responses;
    for (const response of row.responses) {
      const status = response.status.trim();
      if (!/^(?:[1-5]\d{2}|[1-5]XX|default)$/.test(status)) {
        error(`Укажите HTTP-статус ответа «${response.label}»`);
      } else if (!(status in responses)) {
        const original = document.messages.find(({ id }) => id === response.messageId);
        responses[status] = { description: original?.description || response.label || "Ответ" };
      }
    }
    if (Object.keys(responses).filter((key) => !key.startsWith("x-")).length === 0) {
      responses.default = { description: "Ответ не описан — уточните статус и структуру данных" };
      result.warnings.push({
        rowId: row.id,
        message: "Ответ не описан: добавлен default, уточните его в API Designer",
      });
    }
    const operationKey = `${contractId}:${row.id}`;
    operation[OPERATION_KEY] = operationKey;
    const pathItem = isRecord(paths[path]) ? paths[path] : {};
    const metadata = Object.fromEntries(
      Object.entries(originalPathItem).filter(
        ([key]) =>
          !HTTP_METHODS.some((method) => method === key) &&
          !["parameters", "servers"].includes(key),
      ),
    );
    if ("$ref" in metadata)
      error("Путь содержит $ref: перед преобразованием разверните ссылку в исходном контракте");
    mergeEntries(pathItem, metadata, `paths/${path}`, result.errors, row.id);
    if (method in pathItem)
      error(`Коллизия ${method.toUpperCase()} ${path}: объедините вызовы или измените путь`);
    else pathItem[method] = operation;
    paths[path] = pathItem;
    for (const messageId of row.messageIds) result.bindings.push({ messageId, operationKey });
  }
  validateMergedReferences(api, result.errors);
  for (const source of sources) {
    const selections = selected.flatMap((row) => {
      if (row.source?.contractId !== source.id) return [];
      const resolved = resolveOperation(document, row.source);
      return resolved === null
        ? []
        : [
            {
              source: resolved.location,
              target: { path: row.path.trim(), method: row.method.trim().toLowerCase() },
            },
          ];
    });
    validateSourceReferences(source, selections, api, result.errors);
  }
  if (result.errors.length === 0)
    result.contract = { id: contractId, name: name.trim(), mode: "copy", document: api };
  else result.bindings = [];
  return result;
}
