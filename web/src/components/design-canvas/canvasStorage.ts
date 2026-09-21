import { getOperation, listOperations, isRecord } from "../api-designer/documentModel";
import { OPERATION_KEY } from "./canvasModel";
import { isCanvasColor } from "./canvasColors";
import { CANVAS_EXECUTION_LIMITS } from "./types";
import type {
  CanvasContract,
  CanvasDocument,
  CanvasFragment,
  CanvasMessage,
  CanvasParticipant,
  MessageKind,
  ParticipantKind,
} from "./types";

export const STORAGE_KEY = "mocker.design-canvas.prototype.v1";

const MAX_SERIALIZED_SIZE = 2_000_000;
const MAX_PARTICIPANTS = 200;
const MAX_MESSAGES = 2_000;
const MAX_FRAGMENTS = 500;
const MAX_CONTRACTS = 200;
const MAX_TEXT = 50_000;
const MAX_DRAFTS = 1_000;

const PARTICIPANT_KINDS = new Set<ParticipantKind>([
  "user",
  "client",
  "service",
  "external",
  "database",
  "queue",
  "other",
]);
const MESSAGE_KINDS = new Set<MessageKind>(["request", "response", "event", "note"]);
const FRAGMENT_KINDS = new Set(["opt", "loop"]);

function fail(message: string): never {
  throw new Error(`Некорректный сценарий: ${message}`);
}

function requireRecord(value: unknown, path: string): Record<string, unknown> {
  if (!isRecord(value)) fail(`${path} должен быть объектом`);
  return value;
}

function requireArray(value: unknown, path: string, maximum: number): unknown[] {
  if (!Array.isArray(value)) fail(`${path} должен быть массивом`);
  if (value.length > maximum) fail(`${path} содержит слишком много элементов`);
  return value;
}

function requireString(
  value: unknown,
  path: string,
  allowEmpty = true,
  maximum = MAX_TEXT,
): string {
  if (typeof value !== "string") fail(`${path} должен быть строкой`);
  if (!allowEmpty && value.length === 0) fail(`${path} не должен быть пустым`);
  if (value.length > maximum) fail(`${path} слишком длинный`);
  return value;
}

function requireId(value: unknown, path: string): string {
  return requireString(value, path, false);
}

function validateColor(value: unknown, path: string): void {
  if (value !== undefined && !isCanvasColor(value)) fail(`${path} должен быть цветом #RRGGBB`);
}

function assertNoDuplicates(ids: string[], path: string): void {
  const seen = new Set<string>();
  for (const id of ids) {
    if (seen.has(id)) fail(`идентификатор «${id}» дублируется в ${path}`);
    seen.add(id);
  }
}

function executionString(value: unknown, path: string, maximum: number, allowEmpty = true): string {
  if (typeof value !== "string") fail(`${path} должен быть строкой`);
  if (!allowEmpty && value.length === 0) fail(`${path} не должен быть пустым`);
  if ([...value].length > maximum) fail(`${path} слишком длинный`);
  return value;
}

function variableName(value: unknown, path: string): string {
  const name = executionString(value, path, CANVAS_EXECUTION_LIMITS.name, false);
  if (!/^[A-Za-z_][A-Za-z0-9_]*$/.test(name)) fail(`${path} содержит недопустимое имя переменной`);
  return name;
}

function validateExecutionMap(value: unknown, path: string, variables = false): void {
  const map = requireRecord(value, path);
  if (Object.keys(map).length > CANVAS_EXECUTION_LIMITS.entries)
    fail(`${path} содержит слишком много элементов`);
  for (const [key, item] of Object.entries(map)) {
    if (variables) variableName(key, `${path}.${key}`);
    else executionString(key, `${path}: ключ`, CANVAS_EXECUTION_LIMITS.key, false);
    executionString(item, `${path}.${key}`, CANVAS_EXECUTION_LIMITS.value);
  }
}

function validatePointer(value: unknown, path: string): void {
  const pointer = executionString(value, path, CANVAS_EXECUTION_LIMITS.pointer);
  if ((pointer !== "" && !pointer.startsWith("/")) || /~(?:[^01]|$)/.test(pointer))
    fail(`${path} должен быть JSON Pointer RFC6901`);
}

function validateJSONValue(value: unknown, path: string): void {
  const pending = [value];
  const seen = new Set<object>();
  while (pending.length > 0) {
    const next = pending.pop();
    if (next === null || typeof next === "string" || typeof next === "boolean") continue;
    if (typeof next === "number") {
      if (!Number.isFinite(next) || (Number.isInteger(next) && !Number.isSafeInteger(next)))
        fail(`${path} содержит число, которое нельзя сохранить без потери точности`);
      continue;
    }
    if (Array.isArray(next) || isRecord(next)) {
      if (seen.has(next)) continue;
      seen.add(next);
      for (const child of Object.values(next)) pending.push(child);
      continue;
    }
    fail(`${path} должен содержать значение JSON`);
  }
}

function validateStepExecution(value: unknown, path: string): void {
  const config = requireRecord(value, path);
  if (typeof config.enabled !== "boolean") fail(`${path}.enabled должен быть логическим значением`);
  for (const name of ["pathParams", "query", "headers"])
    validateExecutionMap(config[name], `${path}.${name}`);
  if (typeof config.body !== "string") fail(`${path}.body должен быть строкой`);
  if (new TextEncoder().encode(config.body).length > CANVAS_EXECUTION_LIMITS.body)
    fail(`${path}.body превышает 1 МиБ`);
  if (
    config.expectedStatus !== undefined &&
    (!Number.isInteger(config.expectedStatus) ||
      (config.expectedStatus as number) < 100 ||
      (config.expectedStatus as number) > 599)
  )
    fail(`${path}.expectedStatus должен быть целым числом от 100 до 599`);
  for (const [index, value] of requireArray(
    config.assertions,
    `${path}.assertions`,
    CANVAS_EXECUTION_LIMITS.entries,
  ).entries()) {
    const assertion = requireRecord(value, `${path}.assertions[${index}]`);
    validatePointer(assertion.pointer, `${path}.assertions[${index}].pointer`);
    validateJSONValue(assertion.equals, `${path}.assertions[${index}].equals`);
  }
  const names = requireArray(
    config.extract,
    `${path}.extract`,
    CANVAS_EXECUTION_LIMITS.entries,
  ).map((value, index) => {
    const extraction = requireRecord(value, `${path}.extract[${index}]`);
    validatePointer(extraction.pointer, `${path}.extract[${index}].pointer`);
    return variableName(extraction.name, `${path}.extract[${index}].name`);
  });
  assertNoDuplicates(names, `${path}.extract`);
}

function validateDocumentExecution(value: unknown): void {
  if (value === undefined) return;
  const config = requireRecord(value, "execution");
  validateExecutionMap(config.variables, "execution.variables", true);
}

// Server documents may retain unresolved bindings as diagnostics. Editing or
// running execution settings must not turn skipped bindings into fatal errors.
export function validateCanvasExecutionSettings(document: CanvasDocument): void {
  validateDocumentExecution(document.execution);
  for (const [index, message] of document.messages.entries()) {
    if (message.execution !== undefined)
      validateStepExecution(message.execution, `messages[${index}].execution`);
  }
}

function validateParticipant(value: unknown, index: number): CanvasParticipant {
  const path = `participants[${index}]`;
  const participant = requireRecord(value, path);
  const kind = requireString(participant.kind, `${path}.kind`);
  if (!PARTICIPANT_KINDS.has(kind as ParticipantKind))
    fail(`${path}.kind имеет неизвестное значение`);
  requireId(participant.id, `${path}.id`);
  requireString(participant.name, `${path}.name`);
  requireString(participant.description, `${path}.description`);
  validateColor(participant.color, `${path}.color`);
  return participant as unknown as CanvasParticipant;
}

function validateBinding(
  value: unknown,
  path: string,
): { contractId: string; operationKey: string } {
  const binding = requireRecord(value, path);
  return {
    contractId: requireId(binding.contractId, `${path}.contractId`),
    operationKey: requireId(binding.operationKey, `${path}.operationKey`),
  };
}

function validateMessage(value: unknown, index: number): CanvasMessage {
  const path = `messages[${index}]`;
  const message = requireRecord(value, path);
  const kind = requireString(message.kind, `${path}.kind`);
  if (!MESSAGE_KINDS.has(kind as MessageKind)) fail(`${path}.kind имеет неизвестное значение`);
  requireId(message.id, `${path}.id`);
  requireId(message.fromId, `${path}.fromId`);
  requireId(message.toId, `${path}.toId`);
  requireString(message.label, `${path}.label`);
  requireString(message.description, `${path}.description`);
  validateColor(message.color, `${path}.color`);
  validateColor(message.arrowColor, `${path}.arrowColor`);
  if (message.replyToId !== undefined) requireId(message.replyToId, `${path}.replyToId`);
  if (message.operation !== undefined) validateBinding(message.operation, `${path}.operation`);
  if (message.execution !== undefined)
    validateStepExecution(message.execution, `${path}.execution`);
  return message as unknown as CanvasMessage;
}

function validateFragment(value: unknown, index: number): CanvasFragment {
  const path = `fragments[${index}]`;
  const fragment = requireRecord(value, path);
  const kind = requireString(fragment.kind, `${path}.kind`);
  if (!FRAGMENT_KINDS.has(kind)) fail(`${path}.kind имеет неизвестное значение`);
  requireId(fragment.id, `${path}.id`);
  requireString(fragment.label, `${path}.label`);
  requireId(fragment.fromMessageId, `${path}.fromMessageId`);
  requireId(fragment.toMessageId, `${path}.toMessageId`);
  return fragment as unknown as CanvasFragment;
}

function validateContract(value: unknown, index: number): CanvasContract {
  const path = `contracts[${index}]`;
  const contract = requireRecord(value, path);
  requireId(contract.id, `${path}.id`);
  requireString(contract.name, `${path}.name`);
  const document = requireRecord(contract.document, `${path}.document`);
  if (contract.source !== undefined) {
    const source = requireRecord(contract.source, `${path}.source`);
    for (const field of ["designId", "revisionId"] as const) {
      if (!Number.isSafeInteger(source[field]) || (source[field] as number) < 0) {
        fail(`${path}.source.${field} должен быть неотрицательным целым числом`);
      }
    }
  }
  const operationKeys = new Set<string>();
  for (const location of listOperations(document)) {
    const operationKey = getOperation(document, location)?.[OPERATION_KEY];
    if (operationKey === undefined) continue;
    const key = requireId(operationKey, `${path}.document.${OPERATION_KEY}`);
    if (operationKeys.has(key)) fail(`${path} содержит повторяющийся ${OPERATION_KEY}`);
    operationKeys.add(key);
  }
  return contract as unknown as CanvasContract;
}

function validateCanvas(value: unknown): CanvasDocument {
  const document = requireRecord(value, "корень");
  if (document.formatVersion !== 1) fail("неподдерживаемая версия формата formatVersion");
  requireString(document.title, "title");
  validateDocumentExecution(document.execution);

  const participants = requireArray(document.participants, "participants", MAX_PARTICIPANTS).map(
    validateParticipant,
  );
  const messages = requireArray(document.messages, "messages", MAX_MESSAGES).map(validateMessage);
  const fragments = requireArray(document.fragments, "fragments", MAX_FRAGMENTS).map(
    validateFragment,
  );
  const contracts = requireArray(document.contracts, "contracts", MAX_CONTRACTS).map(
    validateContract,
  );

  assertNoDuplicates(
    participants.map(({ id }) => id),
    "participants",
  );
  assertNoDuplicates(
    messages.map(({ id }) => id),
    "messages",
  );
  assertNoDuplicates(
    fragments.map(({ id }) => id),
    "fragments",
  );
  assertNoDuplicates(
    contracts.map(({ id }) => id),
    "contracts",
  );

  const participantIds = new Set(participants.map(({ id }) => id));
  const contractIds = new Set(contracts.map(({ id }) => id));
  const messagePositions = new Map(messages.map((message, index) => [message.id, index]));
  for (const [index, message] of messages.entries()) {
    if (!participantIds.has(message.fromId))
      fail(`messages[${index}].fromId не ссылается на объект`);
    if (!participantIds.has(message.toId)) fail(`messages[${index}].toId не ссылается на объект`);
    if (message.operation !== undefined && !contractIds.has(message.operation.contractId)) {
      fail(`messages[${index}].operation.contractId не ссылается на контракт`);
    }
    if (message.kind === "response" && message.replyToId !== undefined) {
      const requestPosition = messagePositions.get(message.replyToId);
      if (requestPosition === undefined)
        fail(`messages[${index}].replyToId не ссылается на сообщение`);
      const request = messages[requestPosition];
      if (request?.kind !== "request")
        fail(`messages[${index}].replyToId должен ссылаться на запрос`);
      if (requestPosition > index) fail(`ответ messages[${index}] расположен раньше запроса`);
      if (message.fromId !== request.toId || message.toId !== request.fromId) {
        fail(`направление ответа messages[${index}] не совпадает с направлением запроса`);
      }
    } else if (message.replyToId !== undefined) {
      fail(`messages[${index}].replyToId допустим только для ответа`);
    }
  }

  for (const [index, fragment] of fragments.entries()) {
    const start = messagePositions.get(fragment.fromMessageId);
    const end = messagePositions.get(fragment.toMessageId);
    if (start === undefined) fail(`fragments[${index}].fromMessageId не ссылается на сообщение`);
    if (end === undefined) fail(`fragments[${index}].toMessageId не ссылается на сообщение`);
  }

  return document as unknown as CanvasDocument;
}

function parseJson(text: string): unknown {
  if (text.length > MAX_SERIALIZED_SIZE) throw new Error("Сохранённый сценарий слишком большой");
  try {
    return JSON.parse(text) as unknown;
  } catch {
    throw new Error("Не удалось разобрать JSON сохранённого сценария");
  }
}

function validateFormDrafts(value: unknown): Record<string, string> {
  const drafts = requireRecord(value, "formDrafts");
  const entries = Object.entries(drafts);
  if (entries.length > MAX_DRAFTS) fail("formDrafts содержит слишком много записей");
  for (const [key, draft] of entries) {
    requireString(key, "ключ formDrafts", false);
    requireString(draft, `formDrafts.${key}`, true, MAX_SERIALIZED_SIZE);
  }
  if (typeof drafts.all === "string") {
    const fields = requireRecord(parseJson(drafts.all), "formDrafts.all");
    if (Object.keys(fields).length > MAX_DRAFTS)
      fail("formDrafts.all содержит слишком много полей");
    for (const [pointer, value] of Object.entries(fields)) {
      requireString(pointer, "указатель поля", false);
      const field = requireRecord(value, `formDrafts.all.${pointer}`);
      requireString(field.source, "текст поля", true, MAX_SERIALIZED_SIZE);
      requireString(field.propertySource, "исходный текст поля", true, MAX_SERIALIZED_SIZE);
      if (field.error !== undefined)
        requireString(field.error, "ошибка поля", true, MAX_SERIALIZED_SIZE);
    }
  }
  return drafts as Record<string, string>;
}

export function parseCanvas(text: string): CanvasDocument {
  return validateCanvas(parseJson(text));
}

export function parseSavedCanvas(text: string): {
  document: CanvasDocument;
  formDrafts: Record<string, string>;
} {
  const envelope = requireRecord(parseJson(text), "сохранение");
  return {
    document: validateCanvas(envelope.document),
    formDrafts: validateFormDrafts(envelope.formDrafts),
  };
}

export function serializeSavedCanvas(
  document: CanvasDocument,
  formDrafts: Record<string, string>,
): string {
  validateCanvas(document);
  validateFormDrafts(formDrafts);
  let serialized: string;
  try {
    serialized = JSON.stringify({ document, formDrafts });
  } catch {
    throw new Error("Не удалось сериализовать сохранённый сценарий");
  }
  if (serialized.length > MAX_SERIALIZED_SIZE)
    throw new Error("Сохранённый сценарий слишком большой");
  return serialized;
}
