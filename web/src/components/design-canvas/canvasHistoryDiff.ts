import { parseBrowserSafeJson } from "../../api/preciseJson";
import { HTTP_METHODS, isRecord, unescapeJsonPointerToken } from "../api-designer/documentModel";
import { resolveOperation } from "./canvasModel";
import { messageLabels, participantLabels } from "./labels";
import type {
  CanvasContract,
  CanvasDocument,
  CanvasFragment,
  CanvasMessage,
  CanvasParticipant,
  CanvasSelection,
  OperationBinding,
} from "./types";

export interface CanvasHistoryChange {
  key: string;
  status: "added" | "removed" | "changed" | "moved";
  entityType: "scenario" | "participant" | "message" | "fragment" | "contract" | "formDraft";
  entityId?: string;
  label: string;
  selection?: NonNullable<CanvasSelection>;
  fields: { label: string; before?: string; after?: string; color?: boolean }[];
}

interface CanvasRevision {
  document: CanvasDocument;
  formDrafts?: Record<string, string>;
}

export function canvasHistoryHighlights(
  changes: CanvasHistoryChange[],
): Array<NonNullable<CanvasSelection> & { status: CanvasHistoryChange["status"] }> {
  const priorities = { moved: 1, changed: 2, added: 3, removed: 3 };
  const highlights = new Map<
    string,
    {
      highlight: NonNullable<CanvasSelection> & { status: CanvasHistoryChange["status"] };
      priority: number;
    }
  >();
  for (const change of changes) {
    if (change.selection === undefined) continue;
    const direct = change.entityType === change.selection.kind;
    const priority = direct ? priorities[change.status] : 0;
    const key = `${change.selection.kind}:${change.selection.id}`;
    const previous = highlights.get(key);
    if (previous !== undefined && previous.priority >= priority) continue;
    highlights.set(key, {
      highlight: { ...change.selection, status: direct ? change.status : "changed" },
      priority,
    });
  }
  return [...highlights.values()].map(({ highlight }) => highlight);
}

type Field = CanvasHistoryChange["fields"][number];
type EntityType = "participant" | "message" | "fragment" | "contract";

// JSON snapshots can contain bigint values supplied by a precision-preserving
// client. Comparing values directly avoids both rounding and key-order noise.
function equalJson(before: unknown, after: unknown): boolean {
  if (Object.is(before, after)) return true;
  if (Array.isArray(before) && Array.isArray(after)) {
    return (
      before.length === after.length &&
      before.every((value, index) => equalJson(value, after[index]))
    );
  }
  if (!isRecord(before) || !isRecord(after)) return false;
  const keys = Object.keys(before);
  return (
    keys.length === Object.keys(after).length &&
    keys.every((key) => Object.hasOwn(after, key) && equalJson(before[key], after[key]))
  );
}

function display(value: unknown): string | undefined {
  if (value === undefined) return undefined;
  if (value === null) return "null";
  if (value === "") return "Пустая строка";
  if (Array.isArray(value)) return value.length === 0 ? "[]" : `Массив: ${value.length}`;
  if (isRecord(value))
    return Object.keys(value).length === 0 ? "{}" : `Объект: ${Object.keys(value).length} полей`;
  return String(value);
}

function field<T>(
  label: string,
  before: T,
  after: T,
  format: (value: T, side: "before" | "after") => string | undefined = display,
  color?: boolean,
): Field[] {
  if (equalJson(before, after)) return [];
  return [
    {
      label,
      before: format(before, "before"),
      after: format(after, "after"),
      ...(color ? { color } : {}),
    },
  ];
}

function colorField(label: string, before?: string, after?: string): Field[] {
  return field(label, before, after, (value) => value ?? "По умолчанию", true);
}

const apiLabels: Record<string, string> = {
  openapi: "Версия OpenAPI",
  info: "Описание API",
  title: "Название",
  version: "Версия",
  description: "Описание",
  summary: "Краткое описание",
  paths: "Операции",
  components: "Компоненты",
  schemas: "Схемы",
  properties: "Поля",
  type: "Тип",
  format: "Формат",
  required: "Обязательные поля",
  example: "Пример",
  examples: "Примеры",
  default: "По умолчанию",
  minimum: "Минимум",
  maximum: "Максимум",
  responses: "Ответы",
  requestBody: "Тело запроса",
  parameters: "Параметры",
  content: "Содержимое",
  schema: "Схема",
  items: "Элементы",
  enum: "Допустимые значения",
  nullable: "Допускает null",
  servers: "Серверы",
  url: "URL",
  operationId: "Имя операции",
  tags: "Теги",
  security: "Авторизация",
  securitySchemes: "Схемы авторизации",
  headers: "Заголовки",
  name: "Имя",
  in: "Расположение",
  source: "Текст формы",
  propertySource: "Исходное значение",
  error: "Ошибка",
};

function apiLabel(key: string): string {
  return Object.hasOwn(apiLabels, key) ? apiLabels[key]! : key;
}

function displayJson(value: unknown): string | undefined {
  return typeof value === "string" ? JSON.stringify(value) : display(value);
}

function apiPathLabel(tokens: string[]): string {
  const parts: string[] = [];
  let index = 0;
  if (
    tokens[0] === "paths" &&
    tokens[1] !== undefined &&
    tokens[2] !== undefined &&
    HTTP_METHODS.some((method) => method === tokens[2])
  ) {
    parts.push(`${tokens[2].toUpperCase()} ${tokens[1]}`);
    index = 3;
  } else if (tokens[0] === "components" && tokens[1] === "schemas") {
    parts.push("Схемы");
    index = 2;
  }
  for (; index < tokens.length; index += 1) {
    const token = tokens[index]!;
    // Names inside OpenAPI maps belong to the user, even if they happen to be
    // named "title", "description", or another OpenAPI keyword.
    const parent = tokens[index - 1];
    const namedEntry =
      parent !== undefined &&
      [
        "schemas",
        "properties",
        "paths",
        "responses",
        "headers",
        "examples",
        "securitySchemes",
      ].includes(parent);
    parts.push(namedEntry ? token : apiLabel(token));
  }
  return parts.join(" · ");
}

function jsonFields(before: unknown, after: unknown, path: string[] = []): Field[] {
  if (equalJson(before, after)) return [];
  if ((isRecord(before) || before === undefined) && (isRecord(after) || after === undefined)) {
    const keys = [...new Set([...Object.keys(before ?? {}), ...Object.keys(after ?? {})])].sort();
    if (keys.length > 0) {
      return keys.flatMap((key) =>
        jsonFields(
          before && Object.hasOwn(before, key) ? before[key] : undefined,
          after && Object.hasOwn(after, key) ? after[key] : undefined,
          [...path, key],
        ),
      );
    }
  }
  if (
    (Array.isArray(before) || before === undefined) &&
    (Array.isArray(after) || after === undefined)
  ) {
    const length = Math.max(before?.length ?? 0, after?.length ?? 0);
    if (length > 0) {
      return Array.from({ length }, (_, index) =>
        jsonFields(before?.[index], after?.[index], [...path, `[${index + 1}]`]),
      ).flat();
    }
  }
  return [
    {
      label: `API${path.length ? ` · ${apiPathLabel(path)}` : ""}`,
      before: displayJson(before),
      after: displayJson(after),
    },
  ];
}

function participantFields(before?: CanvasParticipant, after?: CanvasParticipant): Field[] {
  return [
    ...field("Название", before?.name, after?.name),
    ...field("Тип объекта", before?.kind, after?.kind, (value) =>
      value === undefined ? undefined : participantLabels[value],
    ),
    ...field("Описание", before?.description, after?.description),
    ...colorField("Цвет объекта", before?.color, after?.color),
  ];
}

function messageName(document: CanvasDocument, id: string | undefined): string | undefined {
  return id === undefined
    ? undefined
    : document.messages.find((message) => message.id === id)?.label || id;
}

function operationName(document: CanvasDocument, binding: OperationBinding | undefined): string {
  if (binding === undefined) return "Не связана";
  const resolved = resolveOperation(document, binding);
  if (resolved !== null)
    return `${resolved.contract.name} · ${resolved.location.method.toUpperCase()} ${resolved.location.path}`;
  const contract = document.contracts.find((candidate) => candidate.id === binding.contractId);
  return `${contract?.name || binding.contractId} · ${binding.operationKey}`;
}

function messageFields(
  before: CanvasMessage | undefined,
  after: CanvasMessage | undefined,
  documents: { before: CanvasDocument; after: CanvasDocument },
): Field[] {
  const participantName = (id: string | undefined, side: "before" | "after") =>
    id === undefined
      ? undefined
      : documents[side].participants.find((participant) => participant.id === id)?.name || id;
  return [
    ...field("Название", before?.label, after?.label),
    ...field("Тип сообщения", before?.kind, after?.kind, (value) =>
      value === undefined ? undefined : messageLabels[value],
    ),
    ...field("Описание", before?.description, after?.description),
    ...field("Отправитель", before?.fromId, after?.fromId, participantName),
    ...field("Получатель", before?.toId, after?.toId, participantName),
    ...field(
      "Ответ на",
      before?.replyToId,
      after?.replyToId,
      (id, side) => messageName(documents[side], id) ?? "Не указан",
    ),
    ...field("Операция API", before?.operation, after?.operation, (binding, side) =>
      operationName(documents[side], binding),
    ),
    ...colorField("Цвет плашки", before?.color, after?.color),
    ...colorField("Цвет стрелки", before?.arrowColor, after?.arrowColor),
    ...field("Настройки исполнения", before?.execution, after?.execution, (value) =>
      value === undefined ? undefined : JSON.stringify(value),
    ),
  ];
}

function fragmentFields(
  before: CanvasFragment | undefined,
  after: CanvasFragment | undefined,
  documents: { before: CanvasDocument; after: CanvasDocument },
): Field[] {
  return [
    ...field("Название", before?.label, after?.label),
    ...field("Тип блока", before?.kind, after?.kind, (value) =>
      value === undefined ? undefined : value === "loop" ? "Цикл" : "Условие",
    ),
    ...field("Первое сообщение", before?.fromMessageId, after?.fromMessageId, (id, side) =>
      messageName(documents[side], id),
    ),
    ...field("Последнее сообщение", before?.toMessageId, after?.toMessageId, (id, side) =>
      messageName(documents[side], id),
    ),
  ];
}

function contractFields(before?: CanvasContract, after?: CanvasContract): Field[] {
  return [
    ...field("Название", before?.name, after?.name),
    ...field(
      "Режим",
      before ? (before.mode ?? "copy") : undefined,
      after ? (after.mode ?? "copy") : undefined,
      (value) =>
        value === undefined ? undefined : value === "linked" ? "Связанный API" : "Локальная копия",
    ),
    ...field("API-источник", before?.source?.designId, after?.source?.designId),
    ...field("Ревизия API", before?.source?.revisionId, after?.source?.revisionId),
    ...field("Версия API", before?.source?.version, after?.source?.version),
    ...jsonFields(before?.document, after?.document),
  ];
}

function contractSelection(
  id: string,
  before: CanvasDocument,
  after: CanvasDocument,
): NonNullable<CanvasSelection> | undefined {
  const previousMessageIds = new Set(
    before.messages
      .filter((candidate) => candidate.operation?.contractId === id)
      .map((candidate) => candidate.id),
  );
  const message =
    after.messages.find(
      (candidate) => candidate.operation?.contractId === id && previousMessageIds.has(candidate.id),
    ) ??
    after.messages.find((candidate) => candidate.operation?.contractId === id) ??
    before.messages.find((candidate) => candidate.operation?.contractId === id);
  return message === undefined ? undefined : { kind: "message", id: message.id };
}

function compareEntities<T extends { id: string }>(
  entityType: EntityType,
  before: T[],
  after: T[],
  fields: (before: T | undefined, after: T | undefined) => Field[],
  label: (entity: T) => string,
  selection: (id: string) => CanvasHistoryChange["selection"],
): CanvasHistoryChange[] {
  const beforeById = new Map(before.map((entity) => [entity.id, entity]));
  const afterById = new Map(after.map((entity) => [entity.id, entity]));
  // Compare ranks among survivors. Inserting/removing an entity alone never
  // creates spurious moves for every following entity.
  const beforeRanks = new Map(
    before
      .filter((entity) => afterById.has(entity.id))
      .map((entity, index) => [entity.id, index + 1]),
  );
  const afterRanks = new Map(
    after
      .filter((entity) => beforeById.has(entity.id))
      .map((entity, index) => [entity.id, index + 1]),
  );
  const changes: CanvasHistoryChange[] = [];
  for (const id of new Set([...beforeById.keys(), ...afterById.keys()])) {
    const previous = beforeById.get(id);
    const next = afterById.get(id);
    const base = {
      entityType,
      entityId: id,
      label: label((next ?? previous)!) || id,
      selection: selection(id),
    };
    const changedFields = fields(previous, next);
    if (previous === undefined || next === undefined || changedFields.length > 0) {
      const status = previous === undefined ? "added" : next === undefined ? "removed" : "changed";
      changes.push({
        ...base,
        key: `${entityType}:${id}:${status}`,
        status,
        fields: changedFields,
      });
    }
    if (
      previous !== undefined &&
      next !== undefined &&
      beforeRanks.get(id) !== afterRanks.get(id)
    ) {
      changes.push({
        ...base,
        key: `${entityType}:${id}:moved`,
        status: "moved",
        fields: [
          {
            label: "Порядок среди общих элементов",
            before: String(beforeRanks.get(id)),
            after: String(afterRanks.get(id)),
          },
        ],
      });
    }
  }
  return changes;
}

interface DraftEntry {
  id: string;
  value: unknown;
}

function draftEntries(drafts: Record<string, string> = {}): Map<string, DraftEntry> {
  const result = new Map<string, DraftEntry>();
  for (const [key, text] of Object.entries(drafts)) {
    if (key === "all") {
      try {
        const parsed = parseBrowserSafeJson(text);
        if (
          isRecord(parsed) &&
          Object.values(parsed).every(
            (value) =>
              isRecord(value) &&
              typeof value.source === "string" &&
              typeof value.propertySource === "string",
          )
        ) {
          for (const [pointer, value] of Object.entries(parsed))
            result.set(`field:${pointer}`, { id: pointer, value });
          continue;
        }
      } catch {
        // Keep a corrupt or unsafe envelope visible without modifying its bytes.
      }
    }
    result.set(`buffer:${key}`, { id: key, value: text });
  }
  return result;
}

function draftContext(id: string, before: CanvasDocument, after: CanvasDocument) {
  const contract = [...after.contracts, ...before.contracts]
    .filter(
      (candidate) =>
        id.startsWith(`/canvas-contract/${candidate.id}/`) ||
        id === `/canvas-contract/${candidate.id}`,
    )
    .sort((a, b) => b.id.length - a.id.length)[0];
  const path = contract ? id.slice(`/canvas-contract/${contract.id}`.length) : id;
  const fieldLabel = apiPathLabel(path.replace(/^\//, "").split("/").map(unescapeJsonPointerToken));
  return {
    label: contract
      ? `${contract.name} · ${fieldLabel || "Форма API"}`
      : fieldLabel || "Буфер формы",
    selection: contract === undefined ? undefined : contractSelection(contract.id, before, after),
  };
}

function draftFields(before: unknown, after: unknown): Field[] {
  if ((isRecord(before) || before === undefined) && (isRecord(after) || after === undefined)) {
    return [...new Set([...Object.keys(before ?? {}), ...Object.keys(after ?? {})])]
      .sort()
      .flatMap((key) =>
        field(
          apiLabel(key),
          before && Object.hasOwn(before, key) ? before[key] : undefined,
          after && Object.hasOwn(after, key) ? after[key] : undefined,
        ),
      );
  }
  // Draft source is text, including empty or invalid JSON, not a parsed API value.
  return field("Текст формы", before, after, (value) =>
    value === undefined ? undefined : String(value),
  );
}

export function compareCanvasRevisions(
  before: CanvasRevision,
  after: CanvasRevision,
): CanvasHistoryChange[] {
  const documents = { before: before.document, after: after.document };
  const titleFields = [
    ...field("Название", before.document.title, after.document.title),
    ...field(
      "Переменные исполнения",
      before.document.execution?.variables,
      after.document.execution?.variables,
      (value) => (value === undefined ? undefined : JSON.stringify(value)),
    ),
  ];
  const changes: CanvasHistoryChange[] = titleFields.length
    ? [
        {
          key: "scenario:title",
          status: "changed",
          entityType: "scenario",
          label: after.document.title || "Сценарий",
          fields: titleFields,
        },
      ]
    : [];
  changes.push(
    ...compareEntities(
      "participant",
      before.document.participants,
      after.document.participants,
      participantFields,
      (entity) => entity.name,
      (id) => ({ kind: "participant", id }),
    ),
    ...compareEntities(
      "message",
      before.document.messages,
      after.document.messages,
      (a, b) => messageFields(a, b, documents),
      (entity) => entity.label,
      (id) => ({ kind: "message", id }),
    ),
    ...compareEntities(
      "fragment",
      before.document.fragments,
      after.document.fragments,
      (a, b) => fragmentFields(a, b, documents),
      (entity) => entity.label,
      (id) => ({ kind: "fragment", id }),
    ),
    ...compareEntities(
      "contract",
      before.document.contracts,
      after.document.contracts,
      contractFields,
      (entity) => entity.name,
      (id) => contractSelection(id, before.document, after.document),
    ),
  );
  const previousDrafts = draftEntries(before.formDrafts);
  const nextDrafts = draftEntries(after.formDrafts);
  for (const key of new Set([...previousDrafts.keys(), ...nextDrafts.keys()])) {
    const previous = previousDrafts.get(key);
    const next = nextDrafts.get(key);
    const fields = draftFields(previous?.value, next?.value);
    if (fields.length === 0) continue;
    const id = (next ?? previous)!.id;
    changes.push({
      key: `formDraft:${key}`,
      status: previous === undefined ? "added" : next === undefined ? "removed" : "changed",
      entityType: "formDraft",
      entityId: id,
      ...draftContext(id, before.document, after.document),
      fields,
    });
  }
  return changes;
}
