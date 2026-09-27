import type { CanvasDocument, CanvasMessage } from "./types";

function assertNewId(doc: CanvasDocument, id: string): void {
  if (!id.trim() || doc.messages.some((message) => message.id === id)) {
    throw new Error("Нужен новый уникальный ID сообщения");
  }
}

export function insertCanvasMessage(
  doc: CanvasDocument,
  afterId: string | null,
  message: CanvasMessage,
): CanvasDocument {
  assertNewId(doc, message.id);
  const index = afterId === null ? -1 : doc.messages.findIndex((item) => item.id === afterId);
  if (afterId !== null && index < 0) throw new Error(`Сообщение ${afterId} не найдено`);
  const messages = [...doc.messages];
  messages.splice(index + 1, 0, message);
  return { ...doc, messages };
}

export function addCanvasReply(
  doc: CanvasDocument,
  requestId: string,
  newId: string,
): CanvasDocument {
  const request = doc.messages.find((message) => message.id === requestId);
  if (!request) throw new Error(`Сообщение ${requestId} не найдено`);
  if (request.kind !== "request") throw new Error("Ответ можно добавить только к вызову");
  return insertCanvasMessage(doc, requestId, {
    id: newId,
    fromId: request.toId,
    toId: request.fromId,
    kind: "response",
    label: "Ответ",
    description: "",
    replyToId: requestId,
  });
}

export function duplicateCanvasMessage(
  doc: CanvasDocument,
  messageId: string,
  newId: string,
): CanvasDocument {
  const message = doc.messages.find((item) => item.id === messageId);
  if (!message) throw new Error(`Сообщение ${messageId} не найдено`);
  const afterId = message.kind === "response" && message.replyToId ? message.replyToId : messageId;
  return insertCanvasMessage(doc, afterId, {
    ...message,
    id: newId,
    ...(message.operation ? { operation: { ...message.operation } } : {}),
    ...(message.execution ? { execution: structuredClone(message.execution) } : {}),
  });
}
