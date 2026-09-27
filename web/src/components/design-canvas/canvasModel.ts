import {
  getOperation,
  listOperations,
  updateOperation,
  type ApiDocument,
  type OperationLocation,
} from "../api-designer/documentModel";
import { createCanvasId } from "./canvasId";
import { MAX_PARTICIPANT_OFFSET_X } from "./types";
import type { CanvasContract, CanvasDocument, CanvasMessage, OperationBinding } from "./types";

export const OPERATION_KEY = "x-mocker-canvas-operation-id";

export function emptyCanvas(): CanvasDocument {
  return {
    formatVersion: 1,
    title: "Новый сценарий",
    participants: [],
    messages: [],
    fragments: [],
    contracts: [],
  };
}

export function exampleCanvas(): CanvasDocument {
  const operationKey = createCanvasId();
  return {
    formatVersion: 1,
    title: "Оформление заказа",
    participants: [
      { id: "client", name: "Клиент", kind: "client", description: "Покупатель" },
      { id: "orders", name: "Сервис заказов", kind: "service", description: "Создаёт заказы" },
      { id: "database", name: "База заказов", kind: "database", description: "Хранит заказы" },
    ],
    messages: [
      {
        id: "create-order",
        fromId: "client",
        toId: "orders",
        kind: "request",
        label: "POST /orders",
        description: "Создать заказ",
        operation: { contractId: "orders-api", operationKey },
      },
      {
        id: "validate-order",
        fromId: "orders",
        toId: "orders",
        kind: "request",
        label: "Проверить заказ",
        description: "Внутренняя проверка",
      },
      {
        id: "save-order",
        fromId: "orders",
        toId: "database",
        kind: "request",
        label: "Сохранить заказ",
        description: "",
      },
      {
        id: "order-saved",
        fromId: "database",
        toId: "orders",
        kind: "response",
        label: "Заказ сохранён",
        description: "",
        replyToId: "save-order",
      },
      {
        id: "refresh-order",
        fromId: "client",
        toId: "orders",
        kind: "request",
        label: "GET /orders/{id}",
        description: "Повторное обращение к сервису",
      },
      {
        id: "order-created",
        fromId: "orders",
        toId: "client",
        kind: "response",
        label: "201 Created",
        description: "",
        replyToId: "create-order",
      },
    ],
    fragments: [
      {
        id: "save-if-valid",
        kind: "opt",
        label: "Заказ прошёл проверку",
        fromMessageId: "validate-order",
        toMessageId: "order-saved",
      },
    ],
    contracts: [
      {
        id: "orders-api",
        name: "Orders API (локальная копия)",
        document: {
          openapi: "3.1.0",
          info: { title: "Orders API", version: "1.0.0" },
          paths: {
            "/orders": {
              post: {
                [OPERATION_KEY]: operationKey,
                summary: "Создать заказ",
                responses: { "201": { description: "Заказ создан" } },
              },
            },
          },
        },
      },
    ],
  };
}

function moveById<T extends { id: string }>(items: T[], id: string, index: number): T[] | null {
  if (!Number.isInteger(index)) throw new Error("Позиция должна быть целым числом");
  const currentIndex = items.findIndex((item) => item.id === id);
  if (currentIndex < 0) throw new Error(`Элемент ${id} не найден`);
  const targetIndex = Math.max(0, Math.min(index, items.length - 1));
  if (targetIndex === currentIndex) return null;
  const next = [...items];
  const [item] = next.splice(currentIndex, 1);
  next.splice(targetIndex, 0, item!);
  return next;
}

export function moveParticipant(doc: CanvasDocument, id: string, index: number): CanvasDocument {
  const participants = moveById(doc.participants, id, index);
  return participants === null ? doc : { ...doc, participants };
}

export function spaceParticipant(doc: CanvasDocument, id: string, offsetX: number): CanvasDocument {
  if (!Number.isFinite(offsetX)) return doc;
  const offset = Math.max(0, Math.min(MAX_PARTICIPANT_OFFSET_X, Math.round(offsetX)));
  const participant = doc.participants.find((item) => item.id === id);
  if (!participant || (participant.offsetX ?? 0) === offset) return doc;
  return {
    ...doc,
    participants: doc.participants.map((item) =>
      item.id === id ? { ...item, offsetX: offset } : item,
    ),
  };
}

function assertResponsePrecedence(messages: CanvasMessage[]): void {
  const positions = new Map(messages.map((message, index) => [message.id, index]));
  for (const message of messages) {
    if (message.kind !== "response" || message.replyToId === undefined) continue;
    const requestPosition = positions.get(message.replyToId);
    const responsePosition = positions.get(message.id)!;
    if (requestPosition !== undefined && responsePosition < requestPosition) {
      throw new Error(`Нельзя поставить ответ «${message.label}» раньше запроса`);
    }
  }
}

export function moveMessage(doc: CanvasDocument, id: string, index: number): CanvasDocument {
  const messages = moveById(doc.messages, id, index);
  if (messages === null) return doc;
  assertResponsePrecedence(messages);
  const positions = new Map(messages.map((message, index) => [message.id, index]));
  for (const fragment of doc.fragments) {
    const from = positions.get(fragment.fromMessageId);
    const to = positions.get(fragment.toMessageId);
    if (from !== undefined && to !== undefined && from > to) {
      throw new Error(`В блоке «${fragment.label}» начало не может идти после конца`);
    }
  }
  return { ...doc, messages };
}

function applyMessageChanges(
  message: CanvasMessage,
  changes: Partial<CanvasMessage>,
): CanvasMessage {
  const updated = { ...message, ...changes };
  if ("replyToId" in changes && changes.replyToId === undefined) delete updated.replyToId;
  if ("operation" in changes && changes.operation === undefined) delete updated.operation;
  return updated;
}

export function updateMessage(
  doc: CanvasDocument,
  id: string,
  changes: Partial<CanvasMessage>,
): CanvasDocument {
  const message = doc.messages.find((candidate) => candidate.id === id);
  if (message === undefined) throw new Error(`Сообщение ${id} не найдено`);

  const updated = applyMessageChanges(message, changes);
  const requestDetached = message.kind === "request" && updated.kind !== "request";
  const requestEndpointsChanged =
    message.kind === "request" &&
    updated.kind === "request" &&
    (message.fromId !== updated.fromId || message.toId !== updated.toId);
  const messages = doc.messages.map((candidate) => {
    if (candidate.id === id) return updated;
    if (candidate.replyToId !== id) return candidate;
    if (requestDetached) {
      const { replyToId: _replyToId, ...detached } = candidate;
      return detached;
    }
    return requestEndpointsChanged
      ? { ...candidate, fromId: updated.toId, toId: updated.fromId }
      : candidate;
  });
  return { ...doc, messages };
}

function removeMessages(doc: CanvasDocument, initialIds: Set<string>): CanvasDocument {
  const removedIds = new Set(initialIds);
  let changed = true;
  while (changed) {
    changed = false;
    for (const message of doc.messages) {
      if (
        message.replyToId !== undefined &&
        removedIds.has(message.replyToId) &&
        !removedIds.has(message.id)
      ) {
        removedIds.add(message.id);
        changed = true;
      }
    }
  }

  const messages = doc.messages.filter((message) => !removedIds.has(message.id));
  if (messages.length === doc.messages.length) return doc;
  const remainingIds = new Set(messages.map((message) => message.id));
  const fragments = doc.fragments.filter(
    (fragment) =>
      remainingIds.has(fragment.fromMessageId) && remainingIds.has(fragment.toMessageId),
  );
  return { ...doc, messages, fragments };
}

export function removeParticipant(doc: CanvasDocument, id: string): CanvasDocument {
  if (!doc.participants.some((participant) => participant.id === id)) return doc;
  const participants = doc.participants.filter((participant) => participant.id !== id);
  const incidentMessages = new Set(
    doc.messages
      .filter((message) => message.fromId === id || message.toId === id)
      .map((message) => message.id),
  );
  return removeMessages({ ...doc, participants }, incidentMessages);
}

export function removeMessage(doc: CanvasDocument, id: string): CanvasDocument {
  return removeMessages(doc, new Set([id]));
}

export function importContract(
  name: string,
  document: ApiDocument,
  source?: { designId: number; revisionId: number },
): CanvasContract {
  let localDocument = structuredClone(document);
  for (const location of listOperations(localDocument)) {
    localDocument = updateOperation(localDocument, location, (operation) => ({
      ...operation,
      [OPERATION_KEY]: createCanvasId(),
    }));
  }
  return {
    id: createCanvasId(),
    name,
    document: localDocument,
    ...(source === undefined ? {} : { source: { ...source } }),
  };
}

export function resolveOperation(
  doc: CanvasDocument,
  binding: OperationBinding,
): { contract: CanvasContract; location: OperationLocation } | null {
  const contract = doc.contracts.find((candidate) => candidate.id === binding.contractId);
  if (contract === undefined) return null;
  for (const location of listOperations(contract.document)) {
    if (getOperation(contract.document, location)?.[OPERATION_KEY] === binding.operationKey) {
      return { contract, location };
    }
  }
  return null;
}

export interface LocalOperationInput {
  method: string;
  path: string;
  responseStatus: string;
  contractId?: string;
}

export function addLocalOperation(
  doc: CanvasDocument,
  messageId: string,
  input: LocalOperationInput,
): CanvasDocument {
  const message = doc.messages.find((candidate) => candidate.id === messageId);
  if (message === undefined) throw new Error(`Сообщение ${messageId} не найдено`);
  if (message.operation !== undefined) return doc;

  const method = input.method.trim().toLowerCase();
  const path = input.path.trim();
  const status = input.responseStatus.trim();
  if (
    !/^(get|post|put|patch|delete|head|options|trace)$/.test(method) ||
    !path.startsWith("/") ||
    /[\s?#]/.test(path) ||
    /[{}]/.test(path.replace(/\{[^{} /]+\}/g, "")) ||
    !/^(?:[1-5]\d{2}|[1-5]XX|default)$/.test(status)
  ) {
    throw new Error("Укажите корректный метод, путь и статус ответа");
  }
  if (input.contractId !== undefined) {
    const contract = doc.contracts.find(({ id }) => id === input.contractId);
    const key = contract && getOperation(contract.document, { method, path })?.[OPERATION_KEY];
    if (!contract || typeof key !== "string")
      throw new Error("Выбранная операция не найдена в контракте");
    return {
      ...doc,
      messages: doc.messages.map((candidate) =>
        candidate.id === messageId
          ? { ...candidate, operation: { contractId: contract.id, operationKey: key } }
          : candidate,
      ),
    };
  }

  const contractId = createCanvasId();
  const operationKey = createCanvasId();
  const targetName = doc.participants.find((participant) => participant.id === message.toId)?.name;
  const contract: CanvasContract = {
    id: contractId,
    name: `${targetName ?? message.label} — локальный API`,
    document: {
      openapi: "3.1.0",
      info: { title: targetName ?? message.label, version: "1.0.0" },
      paths: {
        [path]: {
          [method]: {
            [OPERATION_KEY]: operationKey,
            summary: message.label,
            ...(Array.from(path.matchAll(/\{([^{} /]+)\}/g)).length > 0
              ? {
                  parameters: Array.from(path.matchAll(/\{([^{} /]+)\}/g), (match) => ({
                    name: match[1],
                    in: "path",
                    required: true,
                    schema: {
                      type: "string",
                      description: "Предположен тип string; уточните в API Designer",
                    },
                  })),
                }
              : {}),
            responses: {
              [status]: {
                description:
                  status === "default"
                    ? "Ответ не описан — уточните статус и структуру данных"
                    : "Уточните структуру ответа",
              },
            },
          },
        },
      },
    },
  };
  const messages = doc.messages.map((candidate) =>
    candidate.id === messageId
      ? { ...candidate, operation: { contractId, operationKey } }
      : candidate,
  );
  return { ...doc, messages, contracts: [...doc.contracts, contract] };
}
