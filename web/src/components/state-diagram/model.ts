import type {
  StateDiagram,
  StateDiagramState,
  StateDiagramTransition,
} from "@/api/generated/schemas";
import { isRecord, type ApiDocument } from "../api-designer/documentModel";
export type { StateDiagram, StateDiagramState, StateDiagramTransition };
export const EXTENSION = "x-mocker-state-diagrams";
export type Selection = { kind: "state" | "transition"; id: string } | null;
export const newID = () => crypto.randomUUID();

function keys(value: Record<string, unknown>, allowed: string[]) {
  if (Object.keys(value).some((key) => !allowed.includes(key)))
    throw new Error("В диаграмме есть неизвестные поля. Исправьте исходник API.");
}
function text(value: unknown): value is string {
  return typeof value === "string";
}
function checkDiagram(value: unknown): asserts value is StateDiagram {
  if (!isRecord(value)) throw new Error("Ожидается объект диаграммы.");
  keys(value, ["id", "name", "initialStateId", "states", "transitions", "entity"]);
  if (
    !text(value.id) ||
    !text(value.name) ||
    !text(value.initialStateId) ||
    !Array.isArray(value.states) ||
    !Array.isArray(value.transitions)
  )
    throw new Error("Неверная структура диаграммы.");
  if (value.states.length > 100 || value.transitions.length > 300)
    throw new Error("Лимит: 100 состояний и 300 переходов.");
  if (value.entity !== undefined) {
    if (!isRecord(value.entity)) throw new Error("Неверные настройки сущности.");
    keys(value.entity, ["family", "keyParam", "stateField"]);
    if (
      !text(value.entity.family) ||
      !text(value.entity.keyParam) ||
      !text(value.entity.stateField)
    )
      throw new Error("Неверные настройки сущности.");
  }
  for (const s of value.states) {
    if (!isRecord(s)) throw new Error("Неверное состояние.");
    keys(s, ["id", "name", "x", "y", "terminal", "value"]);
    if (
      !text(s.id) ||
      !text(s.name) ||
      typeof s.x !== "number" ||
      !Number.isFinite(s.x) ||
      typeof s.y !== "number" ||
      !Number.isFinite(s.y) ||
      typeof s.terminal !== "boolean"
    )
      throw new Error("Неверное состояние.");
    if (
      s.value !== undefined &&
      (!text(s.value) || s.value === "" || new TextEncoder().encode(s.value).length > 256)
    )
      throw new Error("Значение состояния должно быть непустой строкой до 256 байт.");
  }
  for (const t of value.transitions) {
    if (!isRecord(t)) throw new Error("Неверный переход.");
    keys(t, ["id", "name", "from", "to", "binding", "guard", "patchJSON", "responseStatus"]);
    if (
      !text(t.id) ||
      !text(t.name) ||
      !text(t.from) ||
      !text(t.to) ||
      !text(t.patchJSON) ||
      !Number.isInteger(t.responseStatus)
    )
      throw new Error("Неверный переход.");
    if (t.binding !== undefined) {
      if (!isRecord(t.binding) || !text(t.binding.method) || !text(t.binding.path))
        throw new Error("Неверная привязка к API.");
      keys(t.binding, ["method", "path"]);
    }
    if (t.guard !== undefined) {
      if (!isRecord(t.guard) || !text(t.guard.pointer) || !text(t.guard.equalsJSON))
        throw new Error("Неверное условие перехода.");
      keys(t.guard, ["pointer", "equalsJSON"]);
    }
  }
}
export function readDiagrams(document: ApiDocument): StateDiagram[] {
  const value = document[EXTENSION];
  if (value === undefined) return [];
  if (!isRecord(value))
    throw new Error("Неверный формат диаграмм состояний. Исправьте исходник API.");
  keys(value, ["formatVersion", "diagrams"]);
  if (value.formatVersion !== 1 || !Array.isArray(value.diagrams) || value.diagrams.length > 20)
    throw new Error("Поддерживается формат 1, до 20 диаграмм в API.");
  value.diagrams.forEach(checkDiagram);
  const unique = (items: { id: string }[]) =>
    items.every(
      (item, index) =>
        /^[A-Za-z0-9_-]{1,80}$/.test(item.id) &&
        items.findIndex((other) => other.id === item.id) === index,
    );
  const diagrams = value.diagrams as StateDiagram[];
  if (!unique(diagrams) || diagrams.some((d) => !unique(d.states) || !unique(d.transitions)))
    throw new Error("ID диаграмм, состояний и переходов должны быть корректными и уникальными.");
  return value.diagrams as StateDiagram[];
}
export function writeDiagrams(document: ApiDocument, diagrams: StateDiagram[]): ApiDocument {
  return { ...document, [EXTENSION]: { formatVersion: 1, diagrams } };
}
export function removeState(d: StateDiagram, id: string): StateDiagram {
  return {
    ...d,
    initialStateId: d.initialStateId === id ? "" : d.initialStateId,
    states: d.states.filter((s) => s.id !== id),
    transitions: d.transitions.filter((t) => t.from !== id && t.to !== id),
  };
}
export function blankDiagram(): StateDiagram {
  return { id: newID(), name: "Новая диаграмма", initialStateId: "", states: [], transitions: [] };
}
export function orderTemplate(): StateDiagram {
  return {
    id: newID(),
    name: "Жизненный цикл заказа",
    initialStateId: "created",
    states: [
      { id: "created", name: "Создан", x: 70, y: 100, terminal: false },
      { id: "paid", name: "Оплачен", x: 370, y: 100, terminal: false },
      { id: "cancelled", name: "Отменён", x: 70, y: 320, terminal: true },
      { id: "refunded", name: "Возвращён", x: 370, y: 320, terminal: true },
    ],
    transitions: [
      {
        id: "pay",
        name: "Оплатить",
        from: "created",
        to: "paid",
        guard: { pointer: "/paymentAllowed", equalsJSON: "true" },
        patchJSON: '{"status":"paid"}',
        responseStatus: 200,
      },
      {
        id: "cancel",
        name: "Отменить",
        from: "created",
        to: "cancelled",
        patchJSON: '{"status":"cancelled"}',
        responseStatus: 200,
      },
      {
        id: "refund",
        name: "Вернуть оплату",
        from: "paid",
        to: "refunded",
        patchJSON: '{"status":"refunded"}',
        responseStatus: 200,
      },
    ],
  };
}
