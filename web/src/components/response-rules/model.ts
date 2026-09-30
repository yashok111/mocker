import type { ResponseRule, ResponseRuleNode, ResponseRuleEdge } from "@/api/generated/schemas";
import { isRecord, type ApiDocument } from "../api-designer/documentModel";
import { hasUnsafeJsonNumber } from "../api-designer/jsonNumberPrecision";

export type { ResponseRule, ResponseRuleNode, ResponseRuleEdge };
export type GraphSelection = { kind: "node" | "edge"; id: string } | null;
export const EXTENSION = "x-mocker-response-rules";
export const newID = () => crypto.randomUUID();
export const nodeNames: Record<ResponseRuleNode["type"], string> = {
  start: "Начало",
  condition: "Условие",
  delay: "Задержка",
  response: "Ответ",
  fallback: "Стандартная обработка",
  entity_read: "Чтение сущностей",
  entity_create: "Создание сущности",
  entity_update: "Изменение сущности",
};
export const resultConditionOpNames = {
  equals: "Равно",
  not_equals: "Не равно",
  exists: "Существует",
  not_exists: "Не существует",
  greater_than: "Больше",
  greater_or_equal: "Больше или равно",
  less_than: "Меньше",
  less_or_equal: "Меньше или равно",
};
export const numericResultConditionOps = new Set([
  "greater_than",
  "greater_or_equal",
  "less_than",
  "less_or_equal",
]);
export const portNames: Record<string, string> & Record<"next" | "true" | "false", string> = {
  next: "Далее",
  true: "Да",
  false: "Нет",
  found: "Найдена",
  missing: "Не найдена",
};
export function ports(node: ResponseRuleNode): ResponseRuleEdge["port"][] {
  if (node.type === "condition") return ["true", "false"];
  if (
    node.type === "entity_update" ||
    (node.type === "entity_read" && node.entity.operation === "get")
  )
    return ["found", "missing"];
  return node.type === "start" ||
    node.type === "delay" ||
    node.type === "entity_create" ||
    node.type === "entity_read"
    ? ["next"]
    : [];
}
const size = (value: string) => new TextEncoder().encode(value).length;
function fail(): never {
  throw new Error(
    "Неверный формат правил ответа. Исправьте расширение x-mocker-response-rules в исходнике API или через MCP.",
  );
}
function object(value: unknown, allowed: string[]): asserts value is Record<string, unknown> {
  if (!isRecord(value) || Object.keys(value).some((key) => !allowed.includes(key))) fail();
}
function text(value: unknown, max: number): asserts value is string {
  if (typeof value !== "string" || size(value) > max) fail();
}
function ids(items: unknown[], max: number) {
  if (items.length > max) fail();
  const seen = new Set<string>();
  for (const value of items) {
    if (
      !isRecord(value) ||
      typeof value.id !== "string" ||
      !/^[A-Za-z0-9_-]{1,80}$/.test(value.id) ||
      seen.has(value.id)
    )
      fail();
    seen.add(value.id);
  }
}
function fields(value: unknown) {
  if (!Array.isArray(value) || value.length > 100) fail();
  for (const field of value) {
    object(field, ["name", "value"]);
    text(field.name, 256);
    text(field.value, 4096);
  }
}
function valueRef(value: unknown) {
  object(value, ["source", "valueJSON", "name", "nodeId", "pointer"]);
  const source = value.source;
  if (!["literal", "path", "query", "header", "body", "result"].includes(String(source))) fail();
  const allowed =
    source === "literal"
      ? ["source", "valueJSON"]
      : source === "result"
        ? ["source", "nodeId", "pointer"]
        : source === "body"
          ? ["source", "pointer"]
          : ["source", "name"];
  object(value, allowed);
  if (source === "literal") text(value.valueJSON, 65536);
  if (["path", "query", "header"].includes(String(source))) text(value.name, 256);
  if (source === "result") text(value.nodeId, 80);
  if (value.pointer !== undefined) text(value.pointer, 2048);
}
// Inspect the number grammar as text: JSON.parse would round large integers and
// turn valid, very large exponents into Infinity before admission.
function trimJSONWhitespace(value: string): string {
  let start = 0;
  let end = value.length;
  while (start < end && " \t\r\n".includes(value[start]!)) start += 1;
  while (end > start && " \t\r\n".includes(value[end - 1]!)) end -= 1;
  return value.slice(start, end);
}
export function isResultConditionNumberJSON(value: string): boolean {
  return /^-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/.test(trimJSONWhitespace(value));
}
export function isResultConditionScalarJSON(value: string): boolean {
  const scalar = trimJSONWhitespace(value);
  if (/^(?:null|true|false|-?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)$/.test(scalar)) return true;
  if (!scalar.startsWith('"')) return false;
  try {
    return typeof JSON.parse(scalar) === "string";
  } catch {
    return false;
  }
}
function resultRef(value: unknown) {
  object(value, ["source", "nodeId", "pointer"]);
  if (value.source !== "result") fail();
  text(value.nodeId, 80);
  if (!/^[A-Za-z0-9_-]{1,80}$/.test(value.nodeId)) fail();
  if (value.pointer !== undefined) {
    text(value.pointer, 2048);
    const pointer = value.pointer;
    if (pointer !== "" && !pointer.startsWith("/")) fail();
    for (let index = 0; index < pointer.length; index += 1) {
      if (pointer[index] !== "~") continue;
      index += 1;
      if (pointer[index] !== "0" && pointer[index] !== "1") fail();
    }
  }
}
function resultCondition(value: unknown, depth = 1, budget = { leaves: 0 }) {
  if (depth > 4) fail();
  object(value, ["source", "op", "valueJSON", "valueFrom", "all", "any"]);
  if (Object.hasOwn(value, "all") || Object.hasOwn(value, "any")) {
    const key = Object.hasOwn(value, "all") ? "all" : "any";
    object(value, [key]);
    const children = value[key];
    if (!Array.isArray(children) || children.length < 2 || children.length > 16) fail();
    for (const child of children) resultCondition(child, depth + 1, budget);
    return;
  }
  budget.leaves += 1;
  if (budget.leaves > 16) fail();
  object(value, ["source", "op", "valueJSON", "valueFrom"]);
  resultRef(value.source);
  if (value.op === "exists" || value.op === "not_exists") {
    if (Object.hasOwn(value, "valueJSON") || Object.hasOwn(value, "valueFrom")) fail();
  } else if (
    value.op === "equals" ||
    value.op === "not_equals" ||
    numericResultConditionOps.has(String(value.op))
  ) {
    if (Object.hasOwn(value, "valueJSON") === Object.hasOwn(value, "valueFrom")) fail();
    if (Object.hasOwn(value, "valueFrom")) resultRef(value.valueFrom);
    else {
      text(value.valueJSON, 65536);
      if (
        !(numericResultConditionOps.has(String(value.op))
          ? isResultConditionNumberJSON(value.valueJSON)
          : isResultConditionScalarJSON(value.valueJSON))
      )
        fail();
    }
  } else fail();
}
function jsonText(value: unknown, max: number) {
  text(value, max);
  let decoded: unknown;
  try {
    decoded = JSON.parse(value);
  } catch {
    fail();
  }
  checkJSONKeys(value);
  return decoded;
}
function checkJSONKeys(value: string, extensionOnly = false) {
  // Scan syntax that JSON.parse discards (duplicate keys) without touching
  // numeric tokens. For whole documents, admit only our extension and its root
  // key; unrelated OpenAPI objects keep their existing duplicate-key policy.
  const stack: {
    object: boolean;
    key: boolean;
    names: Set<string>;
    property?: string;
    checked: boolean;
    depth: number;
  }[] = [];
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (character === '"') {
      const start = index;
      index += 1;
      while (index < value.length && value[index] !== '"') {
        if (value[index] === "\\") index += 1;
        index += 1;
      }
      const parent = stack.at(-1);
      if (parent?.object && parent.key) {
        if (parent.checked || stack.length === 1) {
          const name: string = JSON.parse(value.slice(start, index + 1));
          if (parent.checked || name === EXTENSION) {
            if (parent.names.has(name)) fail();
            parent.names.add(name);
          }
          parent.property = name;
        }
        parent.key = false;
      }
    } else if (character === "{" || character === "[") {
      const parent = stack.at(-1);
      const checked =
        !extensionOnly ||
        !!parent?.checked ||
        (stack.length === 1 && parent?.property === EXTENSION);
      const depth = checked ? (parent?.checked ? parent.depth : 0) + 1 : 0;
      if (depth > 64) fail();
      stack.push({
        object: character === "{",
        key: character === "{",
        names: new Set(),
        checked,
        depth,
      });
    } else if (character === "}" || character === "]") stack.pop();
    else if (character === "," && stack.at(-1)?.object) stack.at(-1)!.key = true;
  }
}
function requestFields(value: unknown, kind: "query" | "headers" | "path") {
  fields(value);
  const names = new Set<string>();
  for (const field of value as { name: string; value: string }[]) {
    if (
      !field.name ||
      (kind === "headers" && (!httpToken.test(field.name) || containsControl(field.value)))
    )
      fail();
    if (kind === "path" && names.has(field.name)) fail();
    names.add(field.name);
  }
}
function exampleRequest(value: unknown) {
  object(value, ["path", "query", "headers", "bodyJSON", "entities"]);
  requestFields(value.query, "query");
  requestFields(value.headers, "headers");
  if (value.path !== undefined) requestFields(value.path, "path");
  if (value.bodyJSON !== undefined) jsonText(value.bodyJSON, 65536);
  if (value.entities !== undefined) {
    if (!Array.isArray(value.entities) || value.entities.length > 100) fail();
    const families = new Set<string>();
    let totalRows = 0;
    for (const entity of value.entities) {
      object(entity, ["family", "idField", "idType", "rows"]);
      text(entity.family, 2048);
      text(entity.idField, 256);
      if (
        !entity.family ||
        !entity.idField.trim() ||
        !["integer", "string"].includes(String(entity.idType))
      )
        fail();
      if (families.has(entity.family)) fail();
      families.add(entity.family);
      if (!entity.family.startsWith("/") || /[?#\\]/.test(entity.family)) fail();
      const segments = entity.family.slice(1).split("/");
      if (
        segments.some(
          (segment) =>
            !segment ||
            segment === "." ||
            segment === ".." ||
            (/[{}]/.test(segment) && segment !== "{}"),
        )
      )
        fail();
      const parents = segments.filter((segment) => segment === "{}").length;
      if (parents > 3) fail();
      if (!Array.isArray(entity.rows) || entity.rows.length > 100) fail();
      totalRows += entity.rows.length;
      if (totalRows > 100) fail();
      const rows = new Set<string>();
      for (const row of entity.rows) {
        object(row, ["key", "scope", "dataJSON"]);
        text(row.key, 128);
        if (!/^[A-Za-z0-9._~-]{1,128}$/.test(row.key) || row.key === "." || row.key === "..")
          fail();
        if (entity.idType === "integer") {
          if (!/^(?:0|-?[1-9]\d*)$/.test(row.key)) fail();
          const key = BigInt(row.key);
          if (key < -9223372036854775808n || key > 9223372036854775807n) fail();
        }
        if (!Array.isArray(row.scope) || row.scope.length !== parents) fail();
        row.scope.forEach((parent) => text(parent, 4096));
        const identity = JSON.stringify([row.key, row.scope]);
        if (rows.has(identity)) fail();
        rows.add(identity);
        if (!isRecord(jsonText(row.dataJSON, 65536))) fail();
      }
    }
  }
  if (size(JSON.stringify(value)) > 128 * 1024) fail();
}
function examples(value: unknown) {
  if (!Array.isArray(value)) fail();
  ids(value, 20);
  for (const example of value) {
    object(example, ["id", "name", "request"]);
    text(example.name, 200);
    if (!example.name.trim()) fail();
    exampleRequest(example.request);
  }
  if (size(JSON.stringify(value)) > 256 * 1024) fail();
}
const httpToken = /^[!#$%&'*+.^_`|~0-9A-Za-z-]+$/;
const managedHeaders = new Set([
  "content-type",
  "content-length",
  "transfer-encoding",
  "connection",
  "keep-alive",
  "proxy-authenticate",
  "proxy-authorization",
  "te",
  "trailer",
  "upgrade",
  "set-cookie",
  "set-cookie2",
]);
function containsControl(value: string) {
  for (const character of value) {
    const code = character.charCodeAt(0);
    if (code < 32 || code === 127) return true;
  }
  return false;
}
function safeResponse(response: Record<string, unknown>) {
  // A syntax guard for loading/authoring; the shared server's MIME parser and
  // admission checks remain authoritative on Save and evaluation.
  const media = response.mediaType as string;
  const token = "[!#$%&'*+.^_`|~0-9A-Za-z-]+";
  const quoted = '"(?:[^"\\\\\\x00-\\x1f\\x7f]|\\\\[^\\x00-\\x1f\\x7f])*"';
  const mime = new RegExp(
    `^application/(?:json|${token}\\+json)(?:[ \\t]*;[ \\t]*${token}[ \\t]*=[ \\t]*(?:${token}|${quoted}))*[ \\t]*$`,
    "i",
  );
  if (containsControl(media) || !mime.test(media.trim())) fail();
  const names = new Set<string>();
  for (const field of response.headers as { name: string; value: string }[]) {
    const name = field.name.toLowerCase();
    if (
      !httpToken.test(field.name) ||
      names.has(name) ||
      managedHeaders.has(name) ||
      containsControl(field.value)
    )
      fail();
    names.add(name);
  }
}
export function checkRule(value: unknown): asserts value is ResponseRule {
  object(value, ["id", "name", "binding", "nodes", "edges", "examples"]);
  text(value.name, 200);
  if (!Array.isArray(value.nodes) || !Array.isArray(value.edges)) fail();
  ids([value], 1);
  ids(value.nodes, 100);
  ids(value.edges, 200);
  if (value.examples !== undefined) examples(value.examples);
  if (value.binding !== undefined) {
    object(value.binding, ["method", "path"]);
    text(value.binding.method, 32);
    text(value.binding.path, 2048);
  }
  for (const node of value.nodes) {
    if (!isRecord(node) || typeof node.type !== "string" || !Object.hasOwn(nodeNames, node.type))
      fail();
    const extra =
      node.type === "condition"
        ? ["condition", "resultCondition"]
        : node.type === "response"
          ? ["response"]
          : node.type === "delay"
            ? ["delayMs"]
            : node.type.startsWith("entity_")
              ? ["entity"]
              : [];
    object(node, ["id", "type", "name", "x", "y", ...extra]);
    text(node.name, 200);
    for (const coordinate of [node.x, node.y])
      if (
        typeof coordinate !== "number" ||
        !Number.isFinite(coordinate) ||
        Math.abs(coordinate) > 100000
      )
        fail();
    if (node.type === "condition") {
      if (Object.hasOwn(node, "condition") === Object.hasOwn(node, "resultCondition")) fail();
      if (Object.hasOwn(node, "resultCondition")) resultCondition(node.resultCondition);
      else {
        object(node.condition, ["in", "name", "op", "value"]);
        text(node.condition.in, 256);
        text(node.condition.name, 256);
        text(node.condition.op, 256);
        if (node.condition.value !== undefined) text(node.condition.value, 4096);
      }
    }
    if (node.type === "delay" && !Number.isSafeInteger(node.delayMs)) fail();
    if (node.type.startsWith("entity_")) {
      object(node.entity, ["family", "operation", "scope", "key", "data"]);
      text(node.entity.family, 2048);
      if (node.entity.operation !== undefined) text(node.entity.operation, 256);
      if (node.entity.scope !== undefined) {
        if (!Array.isArray(node.entity.scope) || node.entity.scope.length > 3) fail();
        node.entity.scope.forEach(valueRef);
      }
      if (node.entity.key !== undefined) valueRef(node.entity.key);
      if (node.entity.data !== undefined) valueRef(node.entity.data);
    }
    if (node.type === "response") {
      object(node.response, ["status", "mediaType", "headers", "bodyJSON", "bodyFrom"]);
      if (!Number.isSafeInteger(node.response.status)) fail();
      text(node.response.mediaType, 4096);
      fields(node.response.headers);
      safeResponse(node.response);
      if (node.response.bodyJSON !== undefined) text(node.response.bodyJSON, 65536);
      if (node.response.bodyFrom !== undefined) {
        if (node.response.bodyJSON !== undefined) fail();
        valueRef(node.response.bodyFrom);
      }
    }
  }
  for (const edge of value.edges) {
    object(edge, ["id", "from", "port", "to"]);
    text(edge.from, 80);
    text(edge.to, 80);
    text(edge.port, 256);
  }
  if (size(JSON.stringify(value)) > 512 * 1024) fail();
}
export function readRules(source: string): { document: ApiDocument; rules: ResponseRule[] } {
  checkJSONKeys(source, true);
  const document: unknown = JSON.parse(source);
  if (!isRecord(document)) fail();
  const value = document[EXTENSION];
  if (value === undefined) return { document, rules: [] };
  object(value, ["formatVersion", "rules"]);
  if (value.formatVersion !== 1 || !Array.isArray(value.rules)) fail();
  if (size(JSON.stringify(value)) > 1024 * 1024) fail();
  ids(value.rules, 20);
  value.rules.forEach(checkRule);
  return { document, rules: value.rules as ResponseRule[] };
}
export function writeRules(source: string, rules: ResponseRule[]): string {
  const { document } = readRules(source);
  if (hasUnsafeJsonNumber(source))
    throw new Error(
      "Документ содержит числа вне точности JavaScript. Используйте исходник или MCP.",
    );
  const result = JSON.stringify({ ...document, [EXTENSION]: { formatVersion: 1, rules } }, null, 2);
  readRules(result);
  return result;
}
export function removeNode(rule: ResponseRule, id: string): ResponseRule {
  return {
    ...rule,
    nodes: rule.nodes.filter((node) => node.id !== id),
    edges: rule.edges.filter((edge) => edge.from !== id && edge.to !== id),
  };
}
export function makeNode(type: ResponseRuleNode["type"], x = 40, y = 40): ResponseRuleNode {
  const base = { id: newID(), name: nodeNames[type], x, y };
  switch (type) {
    case "entity_read":
      return {
        ...base,
        type,
        entity: { family: "", operation: "get", key: { source: "path", name: "id" } },
      };
    case "entity_create":
      return { ...base, type, entity: { family: "", data: { source: "body", pointer: "" } } };
    case "entity_update":
      return {
        ...base,
        type,
        entity: {
          family: "",
          key: { source: "path", name: "id" },
          data: { source: "body", pointer: "" },
        },
      };
    case "condition":
      return { ...base, type, condition: { in: "header", name: "Authorization", op: "exists" } };
    case "delay":
      return { ...base, type, delayMs: 250 };
    case "response":
      return {
        ...base,
        type,
        response: { status: 200, mediaType: "application/json", headers: [], bodyJSON: "{}" },
      };
    default:
      return { ...base, type };
  }
}
export function blankRule(): ResponseRule {
  return {
    id: newID(),
    name: "Новое правило",
    nodes: [
      { id: "start", type: "start", name: "Запрос", x: 40, y: 80 },
      { id: "fallback", type: "fallback", name: "Стандартная обработка", x: 380, y: 80 },
    ],
    edges: [{ id: "e1", from: "start", port: "next", to: "fallback" }],
  };
}
export function headerTemplate(): ResponseRule {
  return {
    id: newID(),
    name: "Проверка заголовка",
    nodes: [
      { id: "start", type: "start", name: "Запрос", x: 40, y: 150 },
      {
        id: "auth",
        type: "condition",
        name: "Есть Authorization?",
        x: 380,
        y: 150,
        condition: { in: "header", name: "Authorization", op: "exists" },
      },
      { id: "slow", type: "delay", name: "Задержка", x: 720, y: 40, delayMs: 250 },
      {
        id: "ok",
        type: "response",
        name: "Успех",
        x: 1060,
        y: 40,
        response: {
          status: 200,
          mediaType: "application/json",
          headers: [],
          bodyJSON: '{"ok":true}',
        },
      },
      {
        id: "unauthorized",
        type: "response",
        name: "Нужен заголовок",
        x: 720,
        y: 300,
        response: {
          status: 401,
          mediaType: "application/json",
          headers: [],
          bodyJSON: '{"error":"missing_header"}',
        },
      },
    ],
    edges: [
      { id: "e1", from: "start", port: "next", to: "auth" },
      { id: "e2", from: "auth", port: "true", to: "slow" },
      { id: "e3", from: "auth", port: "false", to: "unauthorized" },
      { id: "e4", from: "slow", port: "next", to: "ok" },
    ],
  };
}
