import { getOperation, listOperations, isRecord } from "../api-designer/documentModel";
import { validateFragmentTree } from "./canvasFragments";
import { OPERATION_KEY } from "./canvasModel";
import { isCanvasColor } from "./canvasColors";
import { CANVAS_EXECUTION_LIMITS, MAX_PARTICIPANT_OFFSET_X } from "./types";
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
const FRAGMENT_KINDS = new Set(["opt", "loop", "alt"]);

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

function requireEventId(value: unknown, path: string): string {
  const id = requireString(value, path, false, 100);
  if (!/^[A-Za-z0-9_-]{1,100}$/.test(id)) fail(`${path} содержит недопустимый ID`);
  return id;
}

function validateEventJSON(source: string, path: string, schema = false): void {
  if (new TextEncoder().encode(source).length > 262144) fail(`${path} превышает 256 КиБ`);
  if (schema && source === "") return;
  let parsed: unknown;
  try {
    parsed = JSON.parse(source);
  } catch {
    fail(`${path} должен содержать корректный JSON`);
  }
  if (
    schema &&
    !(
      typeof parsed === "boolean" ||
      (parsed !== null && typeof parsed === "object" && !Array.isArray(parsed))
    )
  )
    fail(`${path} должен быть JSON-объектом или boolean`);
  let nodes = 0;
  const visit = (value: unknown, depth: number): void => {
    nodes++;
    if (depth > 64 || nodes > 10_000) fail(`${path} превышает лимит глубины или узлов JSON`);
    if (Array.isArray(value)) value.forEach((item) => visit(item, depth + 1));
    else if (value !== null && typeof value === "object")
      Object.values(value).forEach((item) => visit(item, depth + 1));
  };
  visit(parsed, 0);
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
  if (config.bindings !== undefined) validateDataBindings(config.bindings, `${path}.bindings`);
}

export function validateDataBindings(value: unknown, path = "Передача данных"): void {
  const ids: string[] = [];
  const targets: string[] = [];
  const bodyPointers: string[] = [];
  requireArray(value, path, CANVAS_EXECUTION_LIMITS.entries).forEach((value, index) => {
    const at = `${path}[${index}]`;
    const binding = requireRecord(value, at);
    for (const key of Object.keys(binding))
      if (!["id", "sourceMessageId", "sourcePointer", "target", "prefix"].includes(key))
        fail(`${at}.${key}: неизвестное поле`);
    const id = executionString(binding.id, `${at}.id`, 100, false);
    if (!/^[A-Za-z0-9_-]+$/.test(id)) fail(`${at}.id: используйте буквы, цифры, _ и -`);
    ids.push(id);
    executionString(binding.sourceMessageId, `${at}.sourceMessageId`, 200, false);
    validatePointer(binding.sourcePointer, `${at}.sourcePointer`);
    if (binding.prefix !== undefined)
      executionString(binding.prefix, `${at}.prefix`, CANVAS_EXECUTION_LIMITS.value);
    const target = requireRecord(binding.target, `${at}.target`);
    const body = target.kind === "body";
    if (!["body", "path", "query", "header"].includes(String(target.kind)))
      fail(`${at}.target.kind: выберите назначение`);
    for (const key of Object.keys(target))
      if (!["kind", body ? "pointer" : "name"].includes(key))
        fail(`${at}.target.${key}: поле недопустимо для этого назначения`);
    if (body) {
      validatePointer(target.pointer, `${at}.target.pointer`);
      bodyPointers.push(target.pointer as string);
    } else {
      const name = executionString(
        target.name,
        `${at}.target.name`,
        CANVAS_EXECUTION_LIMITS.key,
        false,
      );
      targets.push(`${target.kind}:${target.kind === "header" ? name.toLowerCase() : name}`);
    }
  });
  assertNoDuplicates(ids, path);
  assertNoDuplicates(targets, `${path}: назначения`);
  for (let i = 0; i < bodyPointers.length; i++)
    for (let j = i + 1; j < bodyPointers.length; j++) {
      const a = bodyPointers[i]!;
      const b = bodyPointers[j]!;
      if (a === b || a === "" || b === "" || a.startsWith(`${b}/`) || b.startsWith(`${a}/`))
        fail(`${path}: назначения JSON-тела пересекаются`);
    }
}

function validateDocumentExecution(value: unknown): void {
  if (value === undefined) return;
  const config = requireRecord(value, "execution");
  validateExecutionMap(config.variables, "execution.variables", true);
}

function validateCondition(value: unknown, path: string): void {
  const condition = requireRecord(value, path);
  variableName(condition.variable, `${path}.variable`);
  if (!["equals", "not_equals", "exists", "not_exists"].includes(String(condition.operator)))
    fail(`${path}.operator содержит неизвестное сравнение`);
  if (condition.operator === "equals" || condition.operator === "not_equals") {
    executionString(condition.value, `${path}.value`, CANVAS_EXECUTION_LIMITS.value);
  } else if (condition.value !== undefined) {
    fail(`${path}.value допустимо только для сравнения строк`);
  }
}

function validateFragmentExecution(
  value: unknown,
  kind: CanvasFragment["kind"],
  path: string,
): void {
  const execution = requireRecord(value, path);
  if (kind === "alt") fail(`${path} не поддерживается для alt`);
  if (execution.condition !== undefined)
    validateCondition(execution.condition, `${path}.condition`);
  if (
    execution.iterations !== undefined &&
    (kind !== "loop" ||
      !Number.isInteger(execution.iterations) ||
      (execution.iterations as number) < 1 ||
      (execution.iterations as number) > 100)
  )
    fail(`${path}.iterations должен быть целым числом от 1 до 100 для loop`);
}

function validateBranchExecution(value: unknown, path: string): void {
  const execution = requireRecord(value, path);
  if (execution.condition !== undefined)
    validateCondition(execution.condition, `${path}.condition`);
  if (execution.otherwise !== undefined && typeof execution.otherwise !== "boolean")
    fail(`${path}.otherwise должен быть логическим значением`);
  if (execution.otherwise === true && execution.condition !== undefined)
    fail(`${path} не может совмещать условие и otherwise`);
}

// Server documents may retain unresolved bindings as diagnostics. Editing or
// running execution settings must not turn skipped bindings into fatal errors.
export function validateCanvasExecutionSettings(document: CanvasDocument): void {
  validateDocumentExecution(document.execution);
  for (const [index, message] of document.messages.entries()) {
    if (message.execution !== undefined)
      validateStepExecution(message.execution, `messages[${index}].execution`);
  }
  for (const [index, fragment] of document.fragments.entries()) {
    if (fragment.execution !== undefined)
      validateFragmentExecution(fragment.execution, fragment.kind, `fragments[${index}].execution`);
    for (const [branchIndex, branch] of (fragment.branches ?? []).entries()) {
      if (branch.execution !== undefined)
        validateBranchExecution(
          branch.execution,
          `fragments[${index}].branches[${branchIndex}].execution`,
        );
    }
    const branches = fragment.branches ?? [];
    if (
      branches.some(
        (branch, branchIndex) => branch.execution?.otherwise && branchIndex !== branches.length - 1,
      )
    )
      fail(`fragments[${index}]: otherwise допустим только для последней ветки`);
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
  if (
    participant.offsetX !== undefined &&
    (typeof participant.offsetX !== "number" ||
      !Number.isInteger(participant.offsetX) ||
      participant.offsetX < 0 ||
      participant.offsetX > MAX_PARTICIPANT_OFFSET_X)
  )
    fail(`${path}.offsetX должен быть целым числом от 0 до ${MAX_PARTICIPANT_OFFSET_X}`);
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
  if (message.eventBindings !== undefined) {
    for (const [bindingIndex, value] of requireArray(
      message.eventBindings,
      `${path}.eventBindings`,
      2,
    ).entries()) {
      const binding = requireRecord(value, `${path}.eventBindings[${bindingIndex}]`);
      requireEventId(binding.contractId, `${path}.eventBindings[${bindingIndex}].contractId`);
      requireEventId(binding.operationId, `${path}.eventBindings[${bindingIndex}].operationId`);
    }
  }
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
  if (fragment.parentFragmentId !== undefined)
    requireId(fragment.parentFragmentId, `${path}.parentFragmentId`);
  if (fragment.parentBranchId !== undefined)
    requireId(fragment.parentBranchId, `${path}.parentBranchId`);
  if (fragment.execution !== undefined)
    validateFragmentExecution(
      fragment.execution,
      kind as CanvasFragment["kind"],
      `${path}.execution`,
    );
  if (fragment.branches !== undefined) {
    const branches = requireArray(fragment.branches, `${path}.branches`, 100);
    for (const [branchIndex, value] of branches.entries()) {
      const branchPath = `${path}.branches[${branchIndex}]`;
      const branch = requireRecord(value, branchPath);
      requireId(branch.id, `${branchPath}.id`);
      requireString(branch.label, `${branchPath}.label`);
      requireId(branch.fromMessageId, `${branchPath}.fromMessageId`);
      requireId(branch.toMessageId, `${branchPath}.toMessageId`);
      if (branch.execution !== undefined)
        validateBranchExecution(branch.execution, `${branchPath}.execution`);
    }
    if (
      branches.some(
        (value, branchIndex) =>
          isRecord(value) &&
          isRecord(value.execution) &&
          value.execution.otherwise === true &&
          branchIndex !== branches.length - 1,
      )
    )
      fail(`${path}: otherwise допустим только для последней ветки`);
  }
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

function validateEventModel(value: unknown, document: Record<string, unknown>): void {
  const model = requireRecord(value, "eventModel");
  const limits = {
    servers: 100,
    channels: 500,
    messages: 1000,
    schemas: 1000,
    contracts: 200,
  } as const;
  const ids = new Map<string, Set<string>>();
  for (const [kind, limit] of Object.entries(limits)) {
    const items = requireArray(model[kind], `eventModel.${kind}`, limit);
    const itemIds: string[] = [];
    for (const [index, raw] of items.entries()) {
      const path = `eventModel.${kind}[${index}]`;
      const item = requireRecord(raw, path);
      itemIds.push(requireEventId(item.id, `${path}.id`));
      requireString(item.name, `${path}.name`);
      requireString(item.description, `${path}.description`);
      if (kind === "servers") {
        requireString(item.host, `${path}.host`);
        if (!["kafka", "kafka-secure"].includes(String(item.protocol)))
          fail(`${path}.protocol неверен`);
        if (
          !["unspecified", "none", "plain", "scramSha256", "scramSha512"].includes(
            String(item.auth),
          )
        )
          fail(`${path}.auth неверен`);
      } else if (kind === "channels") {
        requireString(item.address, `${path}.address`);
        if (item.discriminatorProperty !== undefined)
          requireString(item.discriminatorProperty, `${path}.discriminatorProperty`);
        if (item.kafka !== undefined) {
          const kafka = requireRecord(item.kafka, `${path}.kafka`);
          for (const field of ["partitions", "replicas"] as const) {
            if (kafka[field] === undefined) continue;
            if (
              typeof kafka[field] !== "number" ||
              !Number.isInteger(kafka[field]) ||
              (kafka[field] as number) < 1 ||
              (kafka[field] as number) > 2147483647
            )
              fail(`${path}.kafka.${field} должен быть положительным целым числом int32`);
          }
        }
        requireArray(item.serverIds, `${path}.serverIds`, 100).forEach((id) =>
          requireEventId(id, `${path}.serverIds`),
        );
        requireArray(item.messageIds, `${path}.messageIds`, 100).forEach((id) =>
          requireEventId(id, `${path}.messageIds`),
        );
      } else if (kind === "messages") {
        for (const field of ["payloadSchemaId", "headersSchemaId", "keySchemaId"])
          if (item[field] !== undefined) requireEventId(item[field], `${path}.${field}`);
        for (const [exampleIndex, rawExample] of requireArray(
          item.examples,
          `${path}.examples`,
          20,
        ).entries()) {
          const example = requireRecord(rawExample, `${path}.examples[${exampleIndex}]`);
          requireString(example.name, `${path}.examples[${exampleIndex}].name`);
          validateEventJSON(
            requireString(
              example.payloadJSON,
              `${path}.examples[${exampleIndex}].payloadJSON`,
              true,
              262144,
            ),
            `${path}.examples[${exampleIndex}].payloadJSON`,
          );
          if (example.headersJSON !== undefined)
            validateEventJSON(
              requireString(
                example.headersJSON,
                `${path}.examples[${exampleIndex}].headersJSON`,
                true,
                262144,
              ),
              `${path}.examples[${exampleIndex}].headersJSON`,
            );
        }
      } else if (kind === "schemas") {
        validateEventJSON(
          requireString(item.schemaJSON, `${path}.schemaJSON`, true, 262144),
          `${path}.schemaJSON`,
          true,
        );
      } else {
        requireId(item.participantId, `${path}.participantId`);
        requireString(item.version, `${path}.version`);
        const operations = requireArray(item.operations, `${path}.operations`, 2000);
        const operationIds = operations.map((rawOperation, operationIndex) => {
          const operationPath = `${path}.operations[${operationIndex}]`;
          const operation = requireRecord(rawOperation, operationPath);
          requireString(operation.name, `${operationPath}.name`);
          requireString(operation.description, `${operationPath}.description`);
          if (operation.action !== "send" && operation.action !== "receive")
            fail(`${operationPath}.action неверен`);
          if (operation.kafka !== undefined) {
            const kafka = requireRecord(operation.kafka, `${operationPath}.kafka`);
            for (const field of ["groupId", "clientId"] as const) {
              if (kafka[field] === undefined) continue;
              const metadata = requireString(
                kafka[field],
                `${operationPath}.kafka.${field}`,
                false,
              );
              if (
                operation.action !== "receive" ||
                Array.from(metadata).length > 256 ||
                /[\r\n\0]/.test(metadata) ||
                metadata.includes("{{")
              )
                fail(
                  `${operationPath}.kafka.${field} допустим только для receive, до 256 символов, без управляющих символов и шаблонов`,
                );
            }
          }
          requireEventId(operation.channelId, `${operationPath}.channelId`);
          requireEventId(operation.messageId, `${operationPath}.messageId`);
          return requireEventId(operation.id, `${operationPath}.id`);
        });
        assertNoDuplicates(operationIds, `${path}.operations`);
      }
    }
    assertNoDuplicates(itemIds, `eventModel.${kind}`);
    ids.set(kind, new Set(itemIds));
  }
  const schemas = ids.get("schemas")!;
  const messages = ids.get("messages")!;
  const servers = ids.get("servers")!;
  const channels = ids.get("channels")!;
  const contracts = ids.get("contracts")!;
  for (const [index, raw] of (model.messages as unknown[]).entries()) {
    const item = raw as Record<string, unknown>;
    for (const field of ["payloadSchemaId", "headersSchemaId", "keySchemaId"])
      if (item[field] !== undefined && !schemas.has(item[field] as string))
        fail(`eventModel.messages[${index}].${field} не ссылается на схему`);
  }
  const channelMap = new Map<string, Record<string, unknown>>();
  for (const [index, raw] of (model.channels as unknown[]).entries()) {
    const item = raw as Record<string, unknown>;
    channelMap.set(item.id as string, item);
    assertNoDuplicates(item.serverIds as string[], `eventModel.channels[${index}].serverIds`);
    assertNoDuplicates(item.messageIds as string[], `eventModel.channels[${index}].messageIds`);
    for (const id of item.serverIds as string[])
      if (!servers.has(id)) fail(`eventModel.channels[${index}].serverIds не ссылается на сервер`);
    for (const id of item.messageIds as string[])
      if (!messages.has(id))
        fail(`eventModel.channels[${index}].messageIds не ссылается на тип события`);
  }
  const owners = new Set<string>();
  const participantMap = new Map(
    (document.participants as Record<string, unknown>[]).map((item) => [item.id as string, item]),
  );
  const httpIDs = new Set((document.contracts as Record<string, unknown>[]).map((item) => item.id));
  const contractMap = new Map<string, Record<string, unknown>>();
  for (const [index, raw] of (model.contracts as unknown[]).entries()) {
    const item = raw as Record<string, unknown>;
    const id = item.id as string;
    contractMap.set(id, item);
    if (httpIDs.has(id)) fail(`eventModel.contracts[${index}].id пересекается с HTTP-контрактом`);
    const ownerId = item.participantId as string;
    const owner = participantMap.get(ownerId);
    if (!owner || !["client", "service", "external", "other"].includes(String(owner.kind)))
      fail(`eventModel.contracts[${index}].participantId не ссылается на приложение`);
    if (owners.has(ownerId))
      fail(`eventModel.contracts[${index}].participantId уже владеет контрактом`);
    owners.add(ownerId);
    const triplets = new Set<string>();
    for (const [operationIndex, rawOperation] of (item.operations as unknown[]).entries()) {
      const operation = rawOperation as Record<string, unknown>;
      const path = `eventModel.contracts[${index}].operations[${operationIndex}]`;
      if (!channels.has(operation.channelId as string))
        fail(`${path}.channelId не ссылается на topic`);
      if (!messages.has(operation.messageId as string))
        fail(`${path}.messageId не ссылается на тип события`);
      const referencedChannel = channelMap.get(operation.channelId as string);
      if (
        !referencedChannel ||
        !(referencedChannel.messageIds as string[]).includes(operation.messageId as string)
      )
        fail(`${path}.messageId отсутствует на topic`);
      const triplet = JSON.stringify([operation.action, operation.channelId, operation.messageId]);
      if (triplets.has(triplet)) fail(`${path} повторяет роль, topic и тип события`);
      triplets.add(triplet);
    }
  }
  for (const [index, raw] of (document.messages as Record<string, unknown>[]).entries()) {
    const bindings = raw.eventBindings as { contractId: string; operationId: string }[] | undefined;
    if (bindings === undefined) continue;
    if (!bindings.length || raw.kind !== "event" || raw.operation !== undefined)
      fail(`messages[${index}].eventBindings несовместимы со стрелкой`);
    const pairs = new Set<string>();
    let first: Record<string, unknown> | undefined;
    for (const binding of bindings) {
      const key = `${binding.contractId}/${binding.operationId}`;
      if (pairs.has(key)) fail(`messages[${index}].eventBindings повторяются`);
      pairs.add(key);
      if (!contracts.has(binding.contractId))
        fail(`messages[${index}].eventBindings не ссылаются на контракт`);
      const contract = contractMap.get(binding.contractId)!;
      const operation = (contract.operations as Record<string, unknown>[]).find(
        (item) => item.id === binding.operationId,
      );
      if (!operation) fail(`messages[${index}].eventBindings не ссылаются на операцию`);
      if (contract.participantId !== (operation.action === "send" ? raw.fromId : raw.toId))
        fail(`messages[${index}].eventBindings не совпадают с владельцем`);
      if (
        first &&
        (first.action === operation.action ||
          first.channelId !== operation.channelId ||
          first.messageId !== operation.messageId)
      )
        fail(`messages[${index}].eventBindings используют разные события`);
      first = operation;
    }
  }
}

function validateCanvas(value: unknown): CanvasDocument {
  const document = requireRecord(value, "корень");
  if (document.formatVersion !== 1 && document.formatVersion !== 2 && document.formatVersion !== 3)
    fail("неподдерживаемая версия формата formatVersion");
  if (document.eventModel !== undefined && document.formatVersion !== 3)
    fail("eventModel требует formatVersion 3");
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
  if (document.eventModel !== undefined) validateEventModel(document.eventModel, document);

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
    if (
      message.eventBindings !== undefined &&
      (document.formatVersion !== 3 || document.eventModel === undefined)
    )
      fail(`messages[${index}].eventBindings требует eventModel и formatVersion 3`);
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

  validateFragmentTree(document as unknown as CanvasDocument);
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
