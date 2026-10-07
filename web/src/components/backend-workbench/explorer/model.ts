import type {
  BackendExploreNode,
  BackendExploreEdge,
  BackendDiagramRef,
  BackendReadTarget,
} from "@/api/generated/schemas";
export type MapNode = Omit<BackendExploreNode, "origin"> & {
  origin?: string;
  boundary?: boolean;
  refs?: BackendDiagramRef[];
  details?: Record<string, string>;
  group?: string;
  badge?: string;
  change?: "added" | "removed" | "changed";
  before?: BackendExploreNode;
  after?: BackendExploreNode;
  evidenceTarget?: BackendReadTarget;
  x?: number;
  y?: number;
};
export type MapEdge = BackendExploreEdge & {
  origin?: string;
  refs?: BackendDiagramRef[];
  details?: Record<string, string>;
  witness?: unknown;
  evidenceTarget?: BackendReadTarget;
};
export type MapData = {
  presentation?: "catalog" | "map";
  nodes: MapNode[];
  edges: MapEdge[];
  total: number;
  nextCursor?: string;
  title: string;
  subtitle: string;
  partial?: string;
  metadata?: { title: string; value: unknown }[];
};
export const kindNames: Record<string, string> = {
  actor: "Участник",
  command: "Команда",
  business_event: "Бизнес-событие",
  policy: "Правило",
  read_model: "Представление",
  question: "Вопрос",
  request: "Запрос",
  response: "Ответ",
  send: "Отправка",
  receive: "Получение",
  error: "Исключение",
  api_field: "Поле API",
  symbol: "Символ исходников",
  constraint: "Ограничение",
  index: "Индекс",
  db_schema: "Схема БД",
  migration: "Миграция",
  field_mapping: "Преобразование значения",
  rpc_operation: "RPC-операция",
  system: "Система",
  software_system: "Система",
  service: "Приложение",
  application: "Приложение",
  datastore: "Хранилище",
  data_store: "Хранилище",
  external_system: "Внешняя система",
  person: "Участник",
  component: "Компонент",
  module: "Пакет исходников",
  http_operation: "API-операция",
  handler: "Обработчик",
  flow: "Логика операции",
  flow_step: "Шаг",
  table: "Таблица",
  column: "Поле",
  field: "Поле",
  job: "Фоновая задача",
  query: "Запрос к данным",
  channel: "Канал событий",
  message: "Сообщение",
  consumer: "Потребитель",
  event: "Событие",
  collection: "Коллекция",
  state: "Состояние",
  participant: "Участник",
  step: "Взаимодействие",
  boundary: "Граница",
};
export const kindName = (kind: string) => kindNames[kind] ?? kind;
export const relationNames: Record<string, string> = {
  contains: "Содержит",
  handles: "Обрабатывает",
  calls: "Вызывает",
  reads: "Читает",
  writes: "Записывает",
  deletes: "Удаляет",
  returns: "Возвращает",
  error: "При ошибке",
  emits: "Публикует",
  delivered_to: "Доставка",
  retries: "Повторная попытка",
  dead_letters: "Недоставленные сообщения",
  next: "Далее",
  branch: "Условие",
  references: "Ссылается",
  depends_on: "Зависит от",
  publishes: "Публикует",
  consumes: "Получает",
};
export function nodeSubtitle(n: MapNode) {
  const a = n.attributes;
  if (typeof a.method === "string" && typeof a.path === "string") return `${a.method} ${a.path}`;
  for (const value of [
    a.technology,
    a.engine,
    a.nativeType,
    a.dataType,
    a.qualifiedName,
    a.protocol,
  ]) {
    if (typeof value === "string" && value) return value;
    if (value && typeof value === "object") {
      if ("status" in value && value.status === "unknown") return "Тип не определён";
      for (const key of ["value", "text", "name"]) {
        const text = (value as Record<string, unknown>)[key];
        if (typeof text === "string" && text) return text;
      }
    }
  }
  return "";
}

const vocabulary: Record<string, string> = {
  account: "Учётная запись",
  achievements: "Достижения",
  analytics: "Аналитика",
  broadcasts: "Трансляции",
  campaigns: "Кампании",
  channels: "Каналы",
  cities: "Города",
  components: "Компоненты",
  contacts: "Контакты",
  ejd: "EJD",
  countries: "Страны",
  notifications: "Уведомления",
  specification_other: "Прочие операции",
  quizzes: "Викторины",
  organizations: "Организации",
  grades: "Оценки",
  chats: "Чаты",
  invitations: "Приглашения",
  users: "Пользователи",
  admin: "Администрирование",
  platform: "Платформа",
  auth: "Авторизация",
  groups: "Группы",
  sessions: "Сессии",
  files: "Файлы",
  events: "События",
  courses: "Курсы",
  lessons: "Уроки",
};
export function collectionName(label: string) {
  const part = label.split("/").filter(Boolean).at(-1) ?? label;
  return (
    vocabulary[part.toLowerCase()] ??
    (label.startsWith("/") ? part.replace(/^./, (c) => c.toUpperCase()) : label)
  );
}
export function sourceNode(n: BackendExploreNode): MapNode {
  return {
    ...n,
    origin:
      n.origin === "intent"
        ? "Авторское изменение"
        : n.origin === "base"
          ? "Исходная версия предложения"
          : "Исходный код",
    badge: n.origin === "intent" ? "Изменено в предложении" : undefined,
  };
}

export const diagramKindNames: Record<string, string> = {
  architecture: "Архитектура",
  interactions: "Взаимодействия",
  lifecycle: "Жизненный цикл",
  business_map: "Бизнес-сценарий",
};
