import { createCanvasId } from "./canvasId";
import { emptyCanvas } from "./canvasModel";
import type { CanvasDocument, CanvasParticipant, CanvasMessage } from "./types";

export type CanvasTemplateKind = "blank" | "login" | "checkout" | "error";

export function createCanvasTemplate(kind: CanvasTemplateKind, title: string): CanvasDocument {
  const doc = { ...emptyCanvas(), title };
  if (kind === "blank") return doc;
  const participant = (name: string, type: CanvasParticipant["kind"]): CanvasParticipant => ({
    id: createCanvasId(),
    name,
    kind: type,
    description: "",
  });
  const [client, service, dependency] =
    kind === "login"
      ? [
          participant("Клиент", "client"),
          participant("Сервис авторизации", "service"),
          participant("Хранилище пользователей", "database"),
        ]
      : kind === "checkout"
        ? [
            participant("Клиент", "client"),
            participant("Сервис заказов", "service"),
            participant("Платёжный сервис", "external"),
          ]
        : [
            participant("Клиент", "client"),
            participant("Сервис", "service"),
            participant("Зависимый сервис", "external"),
          ];
  const request = (
    from: CanvasParticipant,
    to: CanvasParticipant,
    label: string,
  ): CanvasMessage => ({
    id: createCanvasId(),
    fromId: from.id,
    toId: to.id,
    kind: "request",
    label,
    description: "",
  });
  const response = (
    to: CanvasParticipant,
    from: CanvasParticipant,
    label: string,
    replyToId: string,
  ): CanvasMessage => ({
    id: createCanvasId(),
    fromId: from.id,
    toId: to.id,
    kind: "response",
    label,
    description: "",
    replyToId,
  });
  const labels =
    kind === "login"
      ? ["Войти", "Проверить учётные данные", "Пользователь найден", "Сессия создана"]
      : kind === "checkout"
        ? ["Оформить заказ", "Провести платёж", "Платёж принят", "Заказ создан"]
        : ["Отправить запрос", "Обратиться к зависимости", "Ошибка зависимости", "Ошибка запроса"];
  const outer = request(client, service, labels[0]!);
  const inner = request(service, dependency, labels[1]!);
  return {
    ...doc,
    participants: [client, service, dependency],
    messages: [
      outer,
      inner,
      response(service, dependency, labels[2]!, inner.id),
      response(client, service, labels[3]!, outer.id),
    ],
  };
}
