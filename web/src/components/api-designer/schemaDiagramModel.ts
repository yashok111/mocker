import {
  getAtJsonPointer,
  isRecord,
  unescapeJsonPointerToken,
  type ApiDocument,
} from "./documentModel";

export interface SchemaDiagramOptions {
  schema?: string;
  depth?: number;
}

export interface SchemaDiagramResult {
  source: string;
  schemaCount: number;
  relationCount: number;
  warnings: string[];
}

interface Reference {
  ref: string;
  label: string;
}

interface SchemaNode {
  members: string[];
  references: Reference[];
}

const MAX_SCHEMAS = 40;
const MAX_MEMBERS = 24;
const MAX_RELATIONS = 120;
const COMPOSITIONS = ["allOf", "oneOf", "anyOf"] as const;

// IDs never come from the document. Entity encoding keeps labels inside their
// Mermaid statement even when a schema/property contains quotes or newlines.
function label(value: string, warn: (message: string) => void): string {
  let result = "";
  const normalized = value.replace(/\s+/g, " ");
  for (const [index, char] of Array.from(normalized).entries()) {
    const encoded = /[\p{L}\p{N}_ ./[\]?:,|-]/u.test(char) ? char : `#${char.codePointAt(0)};`;
    if (index >= 140 || result.length + encoded.length > 200) {
      warn("Длинные подписи сокращены.");
      return `${result}…`;
    }
    result += encoded;
  }
  return result;
}

function referenceTarget(
  ref: string,
): { name: string; pointer: string; suffix: string } | undefined {
  if (!ref.startsWith("#/")) return undefined;
  try {
    const pointer = decodeURIComponent(ref.slice(1));
    const tokens = pointer.slice(1).split("/").map(unescapeJsonPointerToken);
    if (tokens[0] !== "components" || tokens[1] !== "schemas" || tokens[2] === undefined)
      return undefined;
    return { name: tokens[2], pointer, suffix: tokens.slice(3).join("/") };
  } catch {
    return undefined;
  }
}

function typeLabel(schema: unknown, depth = 0): string {
  if (schema === true) return "any";
  if (schema === false) return "never";
  if (!isRecord(schema) || depth > 4) return "any";
  let type: string;
  if (typeof schema.$ref === "string") {
    const target = referenceTarget(schema.$ref);
    type = target ? `${target.name}${target.suffix ? `/${target.suffix}` : ""}` : "$ref";
  } else if (schema.type === "array" || schema.items !== undefined) {
    type = `${typeLabel(schema.items, depth + 1)}[]`;
  } else {
    type = Array.isArray(schema.type)
      ? schema.type.filter((part) => typeof part === "string").join(" | ")
      : typeof schema.type === "string"
        ? schema.type
        : isRecord(schema.properties)
          ? "object"
          : (COMPOSITIONS.find((key) => Array.isArray(schema[key])) ?? "any");
  }
  if (typeof schema.format === "string") type += ` / ${schema.format}`;
  if (schema.nullable === true || schema["x-nullable"] === true) type += " | null";
  if (Array.isArray(schema.enum)) {
    const values = schema.enum
      .slice(0, 4)
      .map((value) =>
        value === null || ["string", "number", "boolean"].includes(typeof value)
          ? String(value)
          : "object",
      );
    type += ` enum: ${values.join(", ")}${schema.enum.length > 4 ? ", …" : ""}`;
  }
  return type;
}

function collectSchema(schema: unknown, warn: (message: string) => void): SchemaNode {
  const members: string[] = [];
  const references: Reference[] = [];
  let visited = 0;
  const member = (value: string): void => {
    if (members.length < MAX_MEMBERS) members.push(value);
    else warn(`Поля сокращены до ${MAX_MEMBERS} на схему.`);
  };
  const visit = (value: unknown, path: string, nesting: number, optional: boolean): void => {
    if (++visited > 1000 || nesting > 12) {
      warn("Обход вложенных полей ограничен. Часть деталей не показана.");
      return;
    }
    if (!isRecord(value)) return;
    if (typeof value.$ref === "string") {
      if (references.length < MAX_RELATIONS)
        references.push({ ref: value.$ref, label: path || "$ref" });
      else warn(`Количество связей ограничено до ${MAX_RELATIONS}.`);
    }
    // Only schema-bearing keywords are traversed. A $ref inside an example,
    // default or extension is ordinary user data, not a model relationship.
    if (isRecord(value.properties)) {
      const required = new Set(Array.isArray(value.required) ? value.required : []);
      for (const key of Object.keys(value.properties).sort()) {
        if (visited > 1000) break;
        const child = value.properties[key];
        const childPath = path ? `${path}.${key}` : key;
        const childOptional = optional || !required.has(key);
        member(`${typeLabel(child)} ${childPath}${childOptional ? "?" : ""}`);
        visit(child, childPath, nesting + 1, childOptional);
      }
    }
    if (value.items !== undefined) {
      if (Array.isArray(value.items))
        value.items.forEach((item, i) => visit(item, `${path}[${i}]`, nesting + 1, optional));
      else visit(value.items, `${path}[]`, nesting + 1, optional);
    }
    for (const keyword of COMPOSITIONS) {
      const branches = value[keyword];
      if (!Array.isArray(branches)) continue;
      member(
        `${keyword} ${branches
          .slice(0, 4)
          .map((branch) => typeLabel(branch))
          .join(" | ")}${branches.length > 4 ? " | …" : ""}`,
      );
      for (const [i, branch] of branches.entries()) {
        if (visited > 1000) break;
        visit(branch, `${path ? `${path}.` : ""}${keyword}[${i + 1}]`, nesting + 1, optional);
      }
    }
    for (const keyword of [
      "additionalProperties",
      "not",
      "if",
      "then",
      "else",
      "contains",
      "propertyNames",
      "unevaluatedProperties",
      "unevaluatedItems",
      "additionalItems",
    ]) {
      if (isRecord(value[keyword]))
        visit(value[keyword], `${path ? `${path}.` : ""}${keyword}`, nesting + 1, optional);
    }
    for (const keyword of ["patternProperties", "dependentSchemas", "$defs", "definitions"]) {
      const map = value[keyword];
      if (!isRecord(map)) continue;
      for (const key of Object.keys(map).sort()) {
        if (visited > 1000) break;
        visit(map[key], `${path ? `${path}.` : ""}${keyword}.${key}`, nesting + 1, optional);
      }
    }
    if (Array.isArray(value.prefixItems)) {
      for (const [i, item] of value.prefixItems.entries()) {
        if (visited > 1000) break;
        visit(item, `${path}[${i}]`, nesting + 1, optional);
      }
    }
  };
  visit(schema, "", 0, false);
  if (members.length === 0) member(`${typeLabel(schema)} value`);
  return { members, references };
}

export function buildSchemaDiagram(
  document: ApiDocument,
  options: SchemaDiagramOptions = {},
): SchemaDiagramResult {
  const schemas =
    isRecord(document.components) && isRecord(document.components.schemas)
      ? document.components.schemas
      : {};
  const names = Object.keys(schemas).sort();
  const warnings = new Set<string>();
  const warn = (message: string): void => {
    if (warnings.size < 20) warnings.add(message);
    else warnings.add("Есть дополнительные ограничения и неразрешённые ссылки.");
  };
  const nodes = new Map<string, SchemaNode>();
  const relations: { from: string; to: string; label: string }[] = [];
  const depth = Number.isFinite(options.depth) ? Math.max(0, Math.min(3, options.depth ?? 1)) : 1;
  const roots =
    options.schema === undefined ? names : names.filter((name) => name === options.schema);
  const queue = roots.slice(0, MAX_SCHEMAS).map((name) => ({ name, depth: 0 }));
  const selected = new Set(queue.map(({ name }) => name));
  if (roots.length > MAX_SCHEMAS)
    warn(`Показаны первые ${MAX_SCHEMAS} схем из ${roots.length}. Выберите отдельную схему.`);
  if (options.schema !== undefined && roots.length === 0)
    warn(`Схема «${options.schema}» не найдена в черновике.`);

  for (const current of queue) {
    const node = collectSchema(schemas[current.name], warn);
    nodes.set(current.name, node);
    for (const reference of node.references) {
      const target = referenceTarget(reference.ref);
      if (
        !target ||
        !Object.hasOwn(schemas, target.name) ||
        getAtJsonPointer(document, target.pointer) === undefined
      ) {
        warn(`Не показана ссылка: ${reference.ref.slice(0, 200)}`);
        continue;
      }
      if (options.schema !== undefined && current.depth < depth && !selected.has(target.name)) {
        if (selected.size < MAX_SCHEMAS) {
          selected.add(target.name);
          queue.push({ name: target.name, depth: current.depth + 1 });
        } else warn(`Количество схем ограничено до ${MAX_SCHEMAS}. Уменьшите глубину связей.`);
      }
      relations.push({
        from: current.name,
        to: target.name,
        label: `${reference.label}${target.suffix ? ` → ${target.suffix}` : ""}`,
      });
    }
  }

  const sorted = [...nodes.keys()].sort();
  if (sorted.length === 0)
    return { source: "", schemaCount: 0, relationCount: 0, warnings: [...warnings] };
  const ids = new Map(sorted.map((name, i) => [name, `s${i}`]));
  const declarations = sorted.map((name) => {
    const id = ids.get(name);
    return { name, header: [`class ${id}["${label(name, warn)}"]`, `class ${id} {`] };
  });
  const edges = [
    ...new Set(
      relations
        .filter((relation) => ids.has(relation.to))
        .map(
          (relation) =>
            `${ids.get(relation.from)} --> ${ids.get(relation.to)} : ${label(relation.label, warn)}`,
        ),
    ),
  ].sort();
  if (edges.length > MAX_RELATIONS) warn(`Количество связей ограничено до ${MAX_RELATIONS}.`);
  const lines = ["classDiagram", "direction LR"];
  // Reserve every node and edge before fitting members into Mermaid's default
  // 50 kB text limit. Escaped labels can be much larger than their visible text.
  let remaining =
    48_000 -
    [
      ...lines,
      ...declarations.flatMap(({ header }) => [...header, "}"]),
      ...edges.slice(0, MAX_RELATIONS),
    ].join("\n").length;
  for (const { name, header } of declarations) {
    lines.push(...header);
    for (const value of nodes.get(name)?.members ?? []) {
      const line = `  ${label(value, warn)}`;
      if (line.length + 1 <= remaining) {
        lines.push(line);
        remaining -= line.length + 1;
      } else
        warn("Часть полей скрыта: достигнут предел размера диаграммы. Выберите отдельную схему.");
    }
    lines.push("}");
  }
  lines.push(...edges.slice(0, MAX_RELATIONS));
  return {
    source: lines.join("\n"),
    schemaCount: sorted.length,
    relationCount: Math.min(edges.length, MAX_RELATIONS),
    warnings: [...warnings],
  };
}
