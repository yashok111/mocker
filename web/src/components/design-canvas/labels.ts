import type { MessageKind, ParticipantKind } from "./types";

export const participantLabels: Record<ParticipantKind, string> = {
  user: "Пользователь",
  client: "Приложение",
  service: "Backend",
  external: "Внешний сервис",
  database: "База данных",
  queue: "Очередь",
  other: "Другой объект",
};
export const messageLabels: Record<MessageKind, string> = {
  request: "Вызов",
  response: "Ответ",
  event: "Событие",
  note: "Заметка",
};
