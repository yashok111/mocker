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
};
export const portNames: Record<string, string> & Record<"next" | "true" | "false", string> = {
  next: "Далее",
  true: "Да",
  false: "Нет",
};
export function ports(node: ResponseRuleNode): ResponseRuleEdge["port"][] {
  return node.type === "condition"
    ? ["true", "false"]
    : node.type === "start" || node.type === "delay"
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
  object(value, ["id", "name", "binding", "nodes", "edges"]);
  text(value.name, 200);
  if (!Array.isArray(value.nodes) || !Array.isArray(value.edges)) fail();
  ids([value], 1);
  ids(value.nodes, 100);
  ids(value.edges, 200);
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
        ? ["condition"]
        : node.type === "response"
          ? ["response"]
          : node.type === "delay"
            ? ["delayMs"]
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
      object(node.condition, ["in", "name", "op", "value"]);
      text(node.condition.in, 256);
      text(node.condition.name, 256);
      text(node.condition.op, 256);
      if (node.condition.value !== undefined) text(node.condition.value, 4096);
    }
    if (node.type === "delay" && !Number.isSafeInteger(node.delayMs)) fail();
    if (node.type === "response") {
      object(node.response, ["status", "mediaType", "headers", "bodyJSON"]);
      if (!Number.isSafeInteger(node.response.status)) fail();
      text(node.response.mediaType, 4096);
      fields(node.response.headers);
      safeResponse(node.response);
      if (node.response.bodyJSON !== undefined) text(node.response.bodyJSON, 65536);
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
