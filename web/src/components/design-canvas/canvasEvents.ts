import { upgradeFragmentDocument } from "./canvasFragments";
import type { CanvasDocument } from "./types";
import type { EventBinding, EventCollection, EventModel, EventOperation } from "./eventTypes";
import { createCanvasId } from "./canvasId";

export function emptyEventModel(): EventModel {
  return { servers: [], channels: [], messages: [], schemas: [], contracts: [] };
}

export function createEventModel(document: CanvasDocument): CanvasDocument {
  if (document.eventModel) return document;
  const upgraded = document.formatVersion === 1 ? upgradeFragmentDocument(document) : document;
  return { ...upgraded, formatVersion: 3, eventModel: emptyEventModel() };
}

export function resolveEventOperation(
  document: CanvasDocument,
  binding: EventBinding,
): { contract: EventModel["contracts"][number]; operation: EventOperation } | undefined {
  const contract = document.eventModel?.contracts.find((item) => item.id === binding.contractId);
  const operation = contract?.operations.find((item) => item.id === binding.operationId);
  return contract && operation ? { contract, operation } : undefined;
}

export function bindEventOperation(
  document: CanvasDocument,
  messageId: string,
  binding: EventBinding,
  replaceHTTP = false,
): CanvasDocument {
  const message = document.messages.find((item) => item.id === messageId);
  if (!message || message.kind !== "event") throw new Error("Выберите событийную стрелку");
  if (message.operation && !replaceHTTP)
    throw new Error("Сначала подтвердите замену HTTP-привязки");
  const resolved = resolveEventOperation(document, binding);
  if (!resolved) throw new Error("Операция события не найдена");
  const { contract, operation } = resolved;
  const owner = operation.action === "send" ? message.fromId : message.toId;
  if (contract.participantId !== owner)
    throw new Error("Владелец операции не совпадает со стороной стрелки");
  const existing = message.eventBindings ?? [];
  if (
    existing.some(
      (item) => item.contractId === binding.contractId && item.operationId === binding.operationId,
    )
  )
    return document;
  if (
    existing.some(
      (item) => resolveEventOperation(document, item)?.operation.action === operation.action,
    )
  )
    throw new Error("Для этой стороны стрелки уже выбрана операция");
  if (existing.length >= 2) throw new Error("Допустимы только send и receive");
  for (const item of existing) {
    const other = resolveEventOperation(document, item)?.operation;
    if (
      other &&
      (other.channelId !== operation.channelId || other.messageId !== operation.messageId)
    )
      throw new Error("Send и receive должны использовать один topic и тип события");
  }
  return {
    ...document,
    messages: document.messages.map((item) =>
      item.id === messageId
        ? { ...item, operation: undefined, eventBindings: [...existing, binding] }
        : item,
    ),
  };
}

function schemaReferences(source: string): Set<string> {
  const references = new Set<string>();
  let root: unknown;
  try {
    root = JSON.parse(source);
  } catch {
    return references;
  }
  const visit = (value: unknown): void => {
    if (!value || typeof value !== "object" || Array.isArray(value)) return;
    const schema = value as Record<string, unknown>;
    if (typeof schema.$ref === "string") {
      const match = /^#\/components\/schemas\/([A-Za-z0-9_-]{1,100})(?:\/|$)/.exec(schema.$ref);
      if (match) references.add(match[1]!);
    }
    for (const key of ["properties", "patternProperties", "definitions", "dependencies"]) {
      const children = schema[key];
      if (!children || typeof children !== "object" || Array.isArray(children)) continue;
      for (const child of Object.values(children)) visit(child);
    }
    for (const key of [
      "additionalItems",
      "items",
      "contains",
      "additionalProperties",
      "propertyNames",
      "if",
      "then",
      "else",
      "not",
      "allOf",
      "anyOf",
      "oneOf",
    ]) {
      const child = schema[key];
      if (Array.isArray(child)) child.forEach(visit);
      else visit(child);
    }
  };
  visit(root);
  return references;
}

export function eventDependents(
  document: CanvasDocument,
  collection: EventCollection,
  id: string,
): string[] {
  const model = document.eventModel;
  if (!model) return [];
  const result: string[] = [];
  if (collection === "servers") {
    result.push(
      ...model.channels
        .filter((item) => item.serverIds.includes(id))
        .map((item) => `канал ${item.name || item.id}`),
    );
  } else if (collection === "channels") {
    result.push(
      ...model.contracts.flatMap((contract) =>
        contract.operations
          .filter((operation) => operation.channelId === id)
          .map((operation) => `операция ${contract.name}/${operation.name}`),
      ),
    );
  } else if (collection === "messages") {
    result.push(
      ...model.channels
        .filter((item) => item.messageIds.includes(id))
        .map((item) => `канал ${item.name || item.id}`),
    );
    result.push(
      ...model.contracts.flatMap((contract) =>
        contract.operations
          .filter((operation) => operation.messageId === id)
          .map((operation) => `операция ${contract.name}/${operation.name}`),
      ),
    );
  } else if (collection === "schemas") {
    const refs = new Map(
      model.schemas.map((schema) => [schema.id, schemaReferences(schema.schemaJSON)]),
    );
    const reachesSchema = (start: string, visited = new Set<string>()): boolean => {
      if (start === id) return true;
      if (visited.has(start)) return false;
      visited.add(start);
      return Array.from(refs.get(start) ?? []).some((next) => reachesSchema(next, visited));
    };
    result.push(
      ...model.schemas
        .filter((schema) => schema.id !== id && reachesSchema(schema.id))
        .map((schema) => `схема ${schema.name || schema.id}`),
    );
    const affected = model.messages.filter((item) =>
      [item.payloadSchemaId, item.headersSchemaId, item.keySchemaId].some(
        (schemaId) => schemaId && reachesSchema(schemaId),
      ),
    );
    result.push(...affected.map((item) => `тип события ${item.name || item.id}`));
    const messageIds = new Set(affected.map((item) => item.id));
    result.push(
      ...model.contracts
        .filter((contract) =>
          contract.operations.some(
            (operation) =>
              messageIds.has(operation.messageId) ||
              model.channels.some(
                (channel) =>
                  channel.id === operation.channelId &&
                  channel.messageIds.some((messageId) => messageIds.has(messageId)),
              ),
          ),
        )
        .map((contract) => `контракт ${contract.name || contract.id}`),
    );
  } else {
    result.push(
      ...document.messages
        .filter((item) => item.eventBindings?.some((binding) => binding.contractId === id))
        .map((item) => `стрелка ${item.label || item.id}`),
    );
  }
  return result;
}

export function removeEventEntity(
  document: CanvasDocument,
  collection: EventCollection,
  id: string,
): CanvasDocument {
  const dependents = eventDependents(document, collection, id);
  if (dependents.length)
    throw new Error(`Объект используется: ${dependents.join(", ")}. Сначала измените ссылки.`);
  const model = document.eventModel;
  if (!model) return document;
  return {
    ...document,
    eventModel: {
      ...model,
      [collection]: model[collection].filter((item) => item.id !== id),
    },
  };
}

export function removeEventOperation(
  document: CanvasDocument,
  contractId: string,
  operationId: string,
): CanvasDocument {
  const used = document.messages.filter((item) =>
    item.eventBindings?.some(
      (binding) => binding.contractId === contractId && binding.operationId === operationId,
    ),
  );
  if (used.length)
    throw new Error(
      `Операция используется стрелками: ${used.map((item) => item.label || item.id).join(", ")}`,
    );
  const model = document.eventModel;
  if (!model) return document;
  return {
    ...document,
    eventModel: {
      ...model,
      contracts: model.contracts.map((contract) =>
        contract.id === contractId
          ? {
              ...contract,
              operations: contract.operations.filter((operation) => operation.id !== operationId),
            }
          : contract,
      ),
    },
  };
}

export function describeArrowSide(
  document: CanvasDocument,
  arrowId: string,
  action: "send" | "receive",
  channelId: string,
  messageId: string,
  replaceHTTP = false,
): CanvasDocument {
  const upgraded = createEventModel(document);
  const arrow = upgraded.messages.find((item) => item.id === arrowId);
  const ownerId = action === "send" ? arrow?.fromId : arrow?.toId;
  if (!arrow || !ownerId) throw new Error("Стрелка не найдена");
  const owner = upgraded.participants.find((item) => item.id === ownerId);
  if (!owner || !["client", "service", "external", "other"].includes(owner.kind))
    throw new Error("Выберите приложение для операции; очередь не владеет контрактом");
  const model = upgraded.eventModel!;
  const channel = model.channels.find((item) => item.id === channelId);
  if (!channel || !model.messages.some((item) => item.id === messageId))
    throw new Error("Выберите topic и тип события");
  const channelReady = channel.messageIds.includes(messageId)
    ? model.channels
    : model.channels.map((item) =>
        item.id === channelId ? { ...item, messageIds: [...item.messageIds, messageId] } : item,
      );
  let contract = model.contracts.find((item) => item.participantId === ownerId);
  const contractId = contract?.id ?? createCanvasId();
  const matching = contract?.operations.find(
    (operation) =>
      operation.action === action &&
      operation.channelId === channelId &&
      operation.messageId === messageId,
  );
  const operationId = matching?.id ?? createCanvasId();
  if (!contract)
    contract = {
      id: contractId,
      name: `${owner.name || owner.id} events`,
      description: "",
      participantId: ownerId,
      version: "1.0.0",
      operations: [],
    };
  const nextContract = matching
    ? contract
    : {
        ...contract,
        operations: [
          ...contract.operations,
          {
            id: operationId,
            name: `${action === "send" ? "Send" : "Receive"} ${model.messages.find((item) => item.id === messageId)?.name || messageId}`,
            description: "",
            action,
            channelId,
            messageId,
          },
        ],
      };
  const next = {
    ...upgraded,
    eventModel: {
      ...model,
      channels: channelReady,
      contracts: [...model.contracts.filter((item) => item.id !== contractId), nextContract],
    },
  };
  return bindEventOperation(next, arrowId, { contractId, operationId }, replaceHTTP);
}
